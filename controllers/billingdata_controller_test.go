package controllers

import (
	"context"
	"testing"

	"github.com/go-logr/logr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/utils/ptr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	billingv1 "github.com/helmetica-framework/libra/api/v1"
	billingac "github.com/helmetica-framework/libra/applyconfiguration"
)

func newTestScheme(t *testing.T) *runtime.Scheme {
	t.Helper()

	scheme := runtime.NewScheme()
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
		WithTypeConverters(billingac.NewTypeConverter(scheme)).
		WithReturnManagedFields().
		WithObjects(objs...).
		Build()

	return &BillingDataManager{
		Client: c,
		Scheme: scheme,
		Log:    logr.Discard(),
	}, c
}

func reconcileOnce(t *testing.T, m *BillingDataManager, name string) {
	t.Helper()

	_, err := m.Reconcile(context.Background(), ctrl.Request{
		NamespacedName: types.NamespacedName{Name: name, Namespace: "default"},
	})
	require.NoError(t, err)
}

func getBillingData(t *testing.T, c client.Client, name string) *billingv1.BillingData {
	t.Helper()

	bd := &billingv1.BillingData{}
	require.NoError(t, c.Get(context.Background(),
		types.NamespacedName{Name: name, Namespace: "default"}, bd))

	return bd
}

func TestDesiredPhase_Ready(t *testing.T) {
	phase, message := desiredPhase(&billingv1.BillingData{})

	assert.Equal(t, billingv1.BillingDataPhaseReady, phase)
	assert.Equal(t, readyMessage, message)
}

func TestDesiredPhase_Suspended(t *testing.T) {
	phase, message := desiredPhase(&billingv1.BillingData{
		Spec: billingv1.BillingDataSpec{Suspend: ptr.To(true)},
	})

	assert.Equal(t, billingv1.BillingDataPhasePending, phase)
	assert.Equal(t, suspendedMessage, message)
}

func TestReconcile_EmptySpecGoesReady(t *testing.T) {
	m, c := newManager(t, &billingv1.BillingData{
		ObjectMeta: metav1.ObjectMeta{Name: "sample", Namespace: "default", Generation: 2},
	})

	reconcileOnce(t, m, "sample")

	bd := getBillingData(t, c, "sample")
	assert.Equal(t, billingv1.BillingDataPhaseReady, bd.Status.Phase)
	assert.Equal(t, readyMessage, bd.Status.Message)
	assert.Equal(t, int64(2), bd.Status.ObservedGeneration)
}

func TestReconcile_SuspendedHoldsPending(t *testing.T) {
	m, c := newManager(t, &billingv1.BillingData{
		ObjectMeta: metav1.ObjectMeta{Name: "sample", Namespace: "default"},
		Spec:       billingv1.BillingDataSpec{Suspend: ptr.To(true)},
	})

	reconcileOnce(t, m, "sample")

	bd := getBillingData(t, c, "sample")
	assert.Equal(t, billingv1.BillingDataPhasePending, bd.Status.Phase)
	assert.Equal(t, suspendedMessage, bd.Status.Message)
}

func TestReconcile_WritesWithFieldOwner(t *testing.T) {
	m, c := newManager(t, &billingv1.BillingData{
		ObjectMeta: metav1.ObjectMeta{Name: "sample", Namespace: "default"},
	})

	reconcileOnce(t, m, "sample")

	bd := getBillingData(t, c, "sample")

	var managers []string
	for _, mf := range bd.GetManagedFields() {
		managers = append(managers, mf.Manager)
	}
	assert.Contains(t, managers, string(fieldOwner))
}

func TestReconcile_MissingObjectIsNoError(t *testing.T) {
	m, _ := newManager(t)

	reconcileOnce(t, m, "gone")
}
