package controllers

import (
	"context"
	"testing"
	"time"

	"github.com/go-logr/logr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/managedfields"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/utils/ptr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	billingv1 "github.com/helmetica-framework/libra/api/v1"
	billingac "github.com/helmetica-framework/libra/applyconfiguration"
)

func newTestScheme(t *testing.T) *runtime.Scheme {
	t.Helper()

	scheme := runtime.NewScheme()
	require.NoError(t, clientgoscheme.AddToScheme(scheme))
	require.NoError(t, billingv1.AddToScheme(scheme))

	return scheme
}

// The status subresource must be registered or every status write is rejected.
func newManager(t *testing.T, objs ...client.Object) (*BillingDataManager, client.Client) {
	t.Helper()

	scheme := newTestScheme(t)
	c := fake.NewClientBuilder().
		WithScheme(scheme).
		WithStatusSubresource(&billingv1.BillingData{}).
		// The deduced converter covers Namespace, which the generated one does
		// not know.
		WithTypeConverters(
			billingac.NewTypeConverter(scheme),
			managedfields.NewDeducedTypeConverter(),
		).
		WithReturnManagedFields().
		WithObjects(objs...).
		Build()

	return &BillingDataManager{
		Client: c,
		Scheme: scheme,
		Log:    logr.Discard(),
		Now:    func() time.Time { return fixedNow },
	}, c
}

func billingData(name, namespace string) *billingv1.BillingData {
	return &billingv1.BillingData{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace, Generation: 2},
		Spec: billingv1.BillingDataSpec{
			Items: []billingv1.BillingItem{{Name: "replicas", Value: "3"}},
		},
	}
}

func reconcileOnce(t *testing.T, m *BillingDataManager, name, namespace string) {
	t.Helper()

	_, err := m.Reconcile(context.Background(), ctrl.Request{
		NamespacedName: types.NamespacedName{Name: name, Namespace: namespace},
	})
	require.NoError(t, err)
}

func getBillingData(t *testing.T, c client.Client, name, namespace string) *billingv1.BillingData {
	t.Helper()

	bd := &billingv1.BillingData{}
	require.NoError(t, c.Get(context.Background(),
		types.NamespacedName{Name: name, Namespace: namespace}, bd))

	return bd
}

func TestDesiredState_Ready(t *testing.T) {
	bd := billingData("sample", "instance")

	got := desiredState(bd, annotatedNamespace("instance"), fixedNow)

	assert.Equal(t, billingv1.BillingDataPhaseReady, got.Phase)
	assert.Equal(t, readyMessage, got.Message)
	require.NotNil(t, got.Claim)
	assert.Equal(t, testClaim, *got.Claim)
	require.NotNil(t, got.Record)
	assert.Equal(t, buildRecord(bd, testClaim, fixedNow), *got.Record)
}

func TestDesiredState_MissingAnnotationsFails(t *testing.T) {
	got := desiredState(billingData("sample", "instance"), plainNamespace("instance"), fixedNow)

	assert.Equal(t, billingv1.BillingDataPhaseFailed, got.Phase)
	assert.Contains(t, got.Message, annClaimName)
	assert.Nil(t, got.Claim)
	assert.Nil(t, got.Record)
}

func TestDesiredState_SuspendedWinsOverMissingAnnotations(t *testing.T) {
	bd := billingData("sample", "instance")
	bd.Spec.Suspend = ptr.To(true)

	got := desiredState(bd, plainNamespace("instance"), fixedNow)

	assert.Equal(t, billingv1.BillingDataPhasePending, got.Phase)
	assert.Equal(t, suspendedMessage, got.Message)
	assert.Nil(t, got.Record)
}

func TestReconcile_InstanceNamespaceGoesReady(t *testing.T) {
	m, c := newManager(t, annotatedNamespace("instance"), billingData("sample", "instance"))

	reconcileOnce(t, m, "sample", "instance")

	bd := getBillingData(t, c, "sample", "instance")
	assert.Equal(t, billingv1.BillingDataPhaseReady, bd.Status.Phase)
	assert.Equal(t, readyMessage, bd.Status.Message)
	assert.Equal(t, int64(2), bd.Status.ObservedGeneration)
	assert.Equal(t, "tenant", bd.Status.ClaimNamespace)
	assert.Equal(t, "my-redis", bd.Status.ClaimName)
}

func TestReconcile_PlainNamespaceFails(t *testing.T) {
	m, c := newManager(t, plainNamespace("instance"), billingData("sample", "instance"))

	reconcileOnce(t, m, "sample", "instance")

	bd := getBillingData(t, c, "sample", "instance")
	assert.Equal(t, billingv1.BillingDataPhaseFailed, bd.Status.Phase)
	assert.Contains(t, bd.Status.Message, annClaimName)
	assert.Empty(t, bd.Status.ClaimName)
	assert.Empty(t, bd.Status.ClaimNamespace)
}

func TestReconcile_RecoversWhenAnnotationsAppear(t *testing.T) {
	m, c := newManager(t, plainNamespace("instance"), billingData("sample", "instance"))

	reconcileOnce(t, m, "sample", "instance")
	require.Equal(t, billingv1.BillingDataPhaseFailed,
		getBillingData(t, c, "sample", "instance").Status.Phase)

	ns := &corev1.Namespace{}
	require.NoError(t, c.Get(context.Background(), types.NamespacedName{Name: "instance"}, ns))
	ns.Annotations = annotatedNamespace("instance").Annotations
	require.NoError(t, c.Update(context.Background(), ns))

	reconcileOnce(t, m, "sample", "instance")

	bd := getBillingData(t, c, "sample", "instance")
	assert.Equal(t, billingv1.BillingDataPhaseReady, bd.Status.Phase)
	assert.Equal(t, "my-redis", bd.Status.ClaimName)
}

func TestReconcile_SuspendedHoldsPending(t *testing.T) {
	bd := billingData("sample", "instance")
	bd.Spec.Suspend = ptr.To(true)
	m, c := newManager(t, annotatedNamespace("instance"), bd)

	reconcileOnce(t, m, "sample", "instance")

	got := getBillingData(t, c, "sample", "instance")
	assert.Equal(t, billingv1.BillingDataPhasePending, got.Status.Phase)
	assert.Equal(t, suspendedMessage, got.Status.Message)
}

func TestReconcile_WritesWithFieldOwner(t *testing.T) {
	m, c := newManager(t, annotatedNamespace("instance"), billingData("sample", "instance"))

	reconcileOnce(t, m, "sample", "instance")

	var managers []string
	for _, mf := range getBillingData(t, c, "sample", "instance").GetManagedFields() {
		managers = append(managers, mf.Manager)
	}
	assert.Contains(t, managers, string(fieldOwner))
}

func TestReconcile_MissingObjectIsNoError(t *testing.T) {
	m, _ := newManager(t)

	reconcileOnce(t, m, "gone", "instance")
}

func TestNamespaceToBillingData_MapsEveryObjectInNamespace(t *testing.T) {
	m, _ := newManager(t,
		billingData("one", "instance"),
		billingData("two", "instance"),
		billingData("elsewhere", "other"),
	)

	got := m.namespaceToBillingData(context.Background(), plainNamespace("instance"))

	assert.ElementsMatch(t, []reconcile.Request{
		{NamespacedName: types.NamespacedName{Name: "one", Namespace: "instance"}},
		{NamespacedName: types.NamespacedName{Name: "two", Namespace: "instance"}},
	}, got)
}

func TestNamespaceToBillingData_EmptyNamespace(t *testing.T) {
	m, _ := newManager(t)

	got := m.namespaceToBillingData(context.Background(), plainNamespace("instance"))

	assert.Empty(t, got)
}
