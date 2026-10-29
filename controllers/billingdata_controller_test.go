package controllers

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/go-logr/logr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/managedfields"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/utils/ptr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
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
		Writer: &fakeWriter{},
	}, c
}

func billingData(name, namespace string) *billingv1.BillingData {
	return &billingv1.BillingData{
		ObjectMeta: metav1.ObjectMeta{
			Name:       name,
			Namespace:  namespace,
			UID:        types.UID(name + "-uid"),
			Generation: 2,
		},
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

// fakeWriter keeps objects in memory. onPut runs before the write.
type fakeWriter struct {
	objects map[string][]byte
	puts    []string
	bodies  []string
	err     error
	onPut   func(key string)
}

func (w *fakeWriter) Put(_ context.Context, key string, body []byte) error {
	w.puts = append(w.puts, key)
	w.bodies = append(w.bodies, string(body))
	if w.onPut != nil {
		w.onPut(key)
	}
	if w.err != nil {
		return w.err
	}
	if w.objects == nil {
		w.objects = map[string][]byte{}
	}
	w.objects[key] = body

	return nil
}

func writerOf(m *BillingDataManager) *fakeWriter {
	return m.Writer.(*fakeWriter)
}

const sampleKey = "2026/10/26/T120000Z_sample-uid_2.json"

func reconcileErr(m *BillingDataManager, name, namespace string) error {
	_, err := m.Reconcile(context.Background(), ctrl.Request{
		NamespacedName: types.NamespacedName{Name: name, Namespace: namespace},
	})

	return err
}

func TestReconcile_WritesOneRecord(t *testing.T) {
	m, c := newManager(t, annotatedNamespace("instance"), billingData("sample", "instance"))

	reconcileOnce(t, m, "sample", "instance")

	w := writerOf(m)
	assert.Equal(t, []string{sampleKey}, w.puts)

	bd := getBillingData(t, c, "sample", "instance")
	assert.Equal(t, billingv1.BillingDataPhaseReady, bd.Status.Phase)
	assert.Nil(t, bd.Status.PendingReport)
	require.NotNil(t, bd.Status.LastReport)
	assert.Equal(t, sampleKey, bd.Status.LastReport.Key)
	assert.Equal(t, int64(2), bd.Status.LastReport.Generation)
	assert.Equal(t, testClaim, bd.Status.LastReport.Claim)
	assert.True(t, bd.Status.LastReport.ObservedAt.Time.Equal(fixedNow))
}

func TestReconcile_RecordBody(t *testing.T) {
	m, _ := newManager(t, annotatedNamespace("instance"), billingData("sample", "instance"))

	reconcileOnce(t, m, "sample", "instance")

	assert.JSONEq(t, `{
		"version": 1,
		"event": "created",
		"observedAt": "2026-10-26T12:00:00Z",
		"claim": {
			"apiVersion": "example.io/v1",
			"kind": "Redis",
			"namespace": "tenant",
			"name": "my-redis",
			"uid": "claim-uid"
		},
		"source": {"namespace": "instance", "name": "sample", "uid": "sample-uid", "generation": 2},
		"metadata": {},
		"items": [{"name": "replicas", "value": "3"}]
	}`, string(writerOf(m).objects[sampleKey]))
}

func TestReconcile_NoChangeNoSecondWrite(t *testing.T) {
	m, _ := newManager(t, annotatedNamespace("instance"), billingData("sample", "instance"))

	reconcileOnce(t, m, "sample", "instance")
	m.Now = func() time.Time { return fixedNow.Add(time.Hour) }
	reconcileOnce(t, m, "sample", "instance")

	assert.Equal(t, []string{sampleKey}, writerOf(m).puts)
}

func TestReconcile_NewGenerationWritesAgain(t *testing.T) {
	m, c := newManager(t, annotatedNamespace("instance"), billingData("sample", "instance"))
	reconcileOnce(t, m, "sample", "instance")

	bd := getBillingData(t, c, "sample", "instance")
	bd.Spec.Items[0].Value = "5"
	// The fake client does not bump generation on its own.
	bd.Generation = 3
	require.NoError(t, c.Update(context.Background(), bd))

	m.Now = func() time.Time { return fixedNow.Add(time.Hour) }
	reconcileOnce(t, m, "sample", "instance")

	newKey := "2026/10/26/T130000Z_sample-uid_3.json"
	assert.Equal(t, []string{sampleKey, newKey}, writerOf(m).puts)
	last := getBillingData(t, c, "sample", "instance").Status.LastReport
	require.NotNil(t, last)
	assert.Equal(t, newKey, last.Key)

	w := writerOf(m)
	assert.Equal(t, eventCreated, decodeDoc(t, w.objects[sampleKey]).Event)
	assert.Equal(t, eventObserved, decodeDoc(t, w.objects[newKey]).Event)
}

func TestReconcile_WriteErrorFailsAndKeepsPending(t *testing.T) {
	m, c := newManager(t, annotatedNamespace("instance"), billingData("sample", "instance"))
	writerOf(m).err = errors.New("bucket down")

	err := reconcileErr(m, "sample", "instance")

	require.Error(t, err)
	bd := getBillingData(t, c, "sample", "instance")
	assert.Equal(t, billingv1.BillingDataPhaseFailed, bd.Status.Phase)
	assert.Contains(t, bd.Status.Message, "bucket down")
	require.NotNil(t, bd.Status.PendingReport)
	assert.Equal(t, sampleKey, bd.Status.PendingReport.Key)
	assert.Nil(t, bd.Status.LastReport)
}

func TestReconcile_RetryReusesPendingKey(t *testing.T) {
	m, c := newManager(t, annotatedNamespace("instance"), billingData("sample", "instance"))
	w := writerOf(m)
	w.err = errors.New("bucket down")
	require.Error(t, reconcileErr(m, "sample", "instance"))

	w.err = nil
	m.Now = func() time.Time { return fixedNow.Add(5 * time.Minute) }
	reconcileOnce(t, m, "sample", "instance")

	assert.Equal(t, []string{sampleKey, sampleKey}, w.puts)
	bd := getBillingData(t, c, "sample", "instance")
	assert.Equal(t, billingv1.BillingDataPhaseReady, bd.Status.Phase)
	assert.Nil(t, bd.Status.PendingReport)
	require.NotNil(t, bd.Status.LastReport)
	assert.True(t, bd.Status.LastReport.ObservedAt.Time.Equal(fixedNow),
		"the retry must keep the first observation time")
}

func TestReconcile_PendingSavedBeforeWrite(t *testing.T) {
	m, c := newManager(t, annotatedNamespace("instance"), billingData("sample", "instance"))

	var pendingAtPut *billingv1.Report
	writerOf(m).onPut = func(string) {
		pendingAtPut = getBillingData(t, c, "sample", "instance").Status.PendingReport
	}

	reconcileOnce(t, m, "sample", "instance")

	require.NotNil(t, pendingAtPut, "pendingReport must be in status before the write")
	assert.Equal(t, sampleKey, pendingAtPut.Key)
}

func TestReconcile_SuspendedWritesNothing(t *testing.T) {
	bd := billingData("sample", "instance")
	bd.Spec.Suspend = ptr.To(true)
	m, _ := newManager(t, annotatedNamespace("instance"), bd)

	reconcileOnce(t, m, "sample", "instance")

	assert.Empty(t, writerOf(m).puts)
}

func TestReconcile_PlainNamespaceWritesNothing(t *testing.T) {
	m, _ := newManager(t, plainNamespace("instance"), billingData("sample", "instance"))

	reconcileOnce(t, m, "sample", "instance")

	assert.Empty(t, writerOf(m).puts)
}

func TestReconcile_RetryWritesSameBody(t *testing.T) {
	m, _ := newManager(t, annotatedNamespace("instance"), billingData("sample", "instance"))
	m.Now = func() time.Time { return fixedNow.Add(123456789 * time.Nanosecond) }
	w := writerOf(m)
	w.err = errors.New("bucket down")
	require.Error(t, reconcileErr(m, "sample", "instance"))

	w.err = nil
	reconcileOnce(t, m, "sample", "instance")

	require.Len(t, w.bodies, 2)
	assert.Equal(t, w.bodies[0], w.bodies[1])
	assert.Contains(t, w.bodies[0], `"observedAt":"2026-10-26T12:00:00Z"`)
}

// failStatusApply makes every status apply of m fail.
func failStatusApply(m *BillingDataManager) {
	m.Client = interceptor.NewClient(m.Client.(client.WithWatch), interceptor.Funcs{
		SubResourceApply: func(context.Context, client.Client, string, runtime.ApplyConfiguration, ...client.SubResourceApplyOption) error {
			return errors.New("api down")
		},
	})
}

func TestReconcile_PendingSaveFailsNoWrite(t *testing.T) {
	m, _ := newManager(t, annotatedNamespace("instance"), billingData("sample", "instance"))
	failStatusApply(m)

	err := reconcileErr(m, "sample", "instance")

	require.ErrorContains(t, err, "api down")
	assert.Empty(t, writerOf(m).puts)
}

func TestReconcile_WriteAndStatusFailBothReported(t *testing.T) {
	bd := billingData("sample", "instance")
	bd.Status.PendingReport = &billingv1.Report{
		Key:        sampleKey,
		Generation: 2,
		ObservedAt: metav1.NewTime(fixedNow),
		Claim:      testClaim,
	}
	m, _ := newManager(t, annotatedNamespace("instance"), bd)
	failStatusApply(m)
	writerOf(m).err = errors.New("bucket down")

	err := reconcileErr(m, "sample", "instance")

	require.ErrorContains(t, err, "bucket down")
	assert.ErrorContains(t, err, "api down")
}

func deleteBillingData(t *testing.T, c client.Client, name, namespace string) *billingv1.BillingData {
	t.Helper()

	require.NoError(t, c.Delete(context.Background(), getBillingData(t, c, name, namespace)))
	bd := getBillingData(t, c, name, namespace)
	require.NotNil(t, bd.DeletionTimestamp)

	return bd
}

func requireGone(t *testing.T, c client.Client, name, namespace string) {
	t.Helper()

	err := c.Get(context.Background(),
		types.NamespacedName{Name: name, Namespace: namespace}, &billingv1.BillingData{})
	require.True(t, apierrors.IsNotFound(err), "expected NotFound, got %v", err)
}

func decodeDoc(t *testing.T, raw []byte) recordDoc {
	t.Helper()

	doc := recordDoc{}
	require.NoError(t, json.Unmarshal(raw, &doc))

	return doc
}

func TestReconcile_AddsFinalizer(t *testing.T) {
	for name, tc := range map[string]struct {
		ns      *corev1.Namespace
		suspend bool
	}{
		"ready":     {ns: annotatedNamespace("instance")},
		"failed":    {ns: plainNamespace("instance")},
		"suspended": {ns: annotatedNamespace("instance"), suspend: true},
	} {
		t.Run(name, func(t *testing.T) {
			bd := billingData("sample", "instance")
			bd.Spec.Suspend = ptr.To(tc.suspend)
			m, c := newManager(t, tc.ns, bd)

			reconcileOnce(t, m, "sample", "instance")

			assert.Contains(t, getBillingData(t, c, "sample", "instance").Finalizers, reportFinalizer)
		})
	}
}

func TestReconcile_DeletionWritesDeletedRecord(t *testing.T) {
	m, c := newManager(t, annotatedNamespace("instance"), billingData("sample", "instance"))
	reconcileOnce(t, m, "sample", "instance")

	bd := deleteBillingData(t, c, "sample", "instance")
	reconcileOnce(t, m, "sample", "instance")

	key := deletedKey(bd.DeletionTimestamp.Time, "sample-uid")
	w := writerOf(m)
	require.Contains(t, w.objects, key)

	doc := decodeDoc(t, w.objects[key])
	assert.Equal(t, eventDeleted, doc.Event)
	assert.Equal(t, testClaim, doc.Claim)
	assert.True(t, doc.ObservedAt.Equal(bd.DeletionTimestamp.Time))
	assert.Equal(t, []billingv1.BillingItem{{Name: "replicas", Value: "3"}}, doc.Items)
	assert.Equal(t, eventCreated, decodeDoc(t, w.objects[sampleKey]).Event,
		"the created record must stay")

	requireGone(t, c, "sample", "instance")
}

func TestReconcile_DeletionUsesSavedClaim(t *testing.T) {
	m, c := newManager(t, annotatedNamespace("instance"), billingData("sample", "instance"))
	reconcileOnce(t, m, "sample", "instance")

	ns := &corev1.Namespace{}
	require.NoError(t, c.Get(context.Background(), types.NamespacedName{Name: "instance"}, ns))
	ns.Annotations = nil
	require.NoError(t, c.Update(context.Background(), ns))

	bd := deleteBillingData(t, c, "sample", "instance")
	reconcileOnce(t, m, "sample", "instance")

	doc := decodeDoc(t, writerOf(m).objects[deletedKey(bd.DeletionTimestamp.Time, "sample-uid")])
	assert.Equal(t, testClaim, doc.Claim)
	requireGone(t, c, "sample", "instance")
}

func TestReconcile_DeletionWithOnlyPendingStillReports(t *testing.T) {
	m, c := newManager(t, annotatedNamespace("instance"), billingData("sample", "instance"))
	w := writerOf(m)
	w.err = errors.New("bucket down")
	require.Error(t, reconcileErr(m, "sample", "instance"))
	w.err = nil

	bd := deleteBillingData(t, c, "sample", "instance")
	reconcileOnce(t, m, "sample", "instance")

	assert.Contains(t, w.objects, deletedKey(bd.DeletionTimestamp.Time, "sample-uid"))
	requireGone(t, c, "sample", "instance")
}

func TestReconcile_DeletionWithoutReportWritesNothing(t *testing.T) {
	m, c := newManager(t, plainNamespace("instance"), billingData("sample", "instance"))
	reconcileOnce(t, m, "sample", "instance")

	deleteBillingData(t, c, "sample", "instance")
	reconcileOnce(t, m, "sample", "instance")

	assert.Empty(t, writerOf(m).puts)
	requireGone(t, c, "sample", "instance")
}

func TestReconcile_DeletionWriteErrorKeepsFinalizer(t *testing.T) {
	m, c := newManager(t, annotatedNamespace("instance"), billingData("sample", "instance"))
	reconcileOnce(t, m, "sample", "instance")

	deleteBillingData(t, c, "sample", "instance")
	w := writerOf(m)
	w.err = errors.New("bucket down")

	require.Error(t, reconcileErr(m, "sample", "instance"))

	bd := getBillingData(t, c, "sample", "instance")
	assert.Contains(t, bd.Finalizers, reportFinalizer)
	assert.Equal(t, billingv1.BillingDataPhaseFailed, bd.Status.Phase)
	assert.Contains(t, bd.Status.Message, "bucket down")
	assert.NotNil(t, bd.Status.LastReport, "reports must survive the failed pass")

	w.err = nil
	reconcileOnce(t, m, "sample", "instance")
	requireGone(t, c, "sample", "instance")
}

func TestReconcile_DeletionWhileSuspendedStillReports(t *testing.T) {
	m, c := newManager(t, annotatedNamespace("instance"), billingData("sample", "instance"))
	reconcileOnce(t, m, "sample", "instance")

	bd := getBillingData(t, c, "sample", "instance")
	bd.Spec.Suspend = ptr.To(true)
	require.NoError(t, c.Update(context.Background(), bd))

	bd = deleteBillingData(t, c, "sample", "instance")
	reconcileOnce(t, m, "sample", "instance")

	assert.Contains(t, writerOf(m).objects, deletedKey(bd.DeletionTimestamp.Time, "sample-uid"))
	requireGone(t, c, "sample", "instance")
}

func TestReconcile_DeletionWithoutOurFinalizerDoesNothing(t *testing.T) {
	bd := billingData("sample", "instance")
	bd.Finalizers = []string{"example.io/keep"}
	m, c := newManager(t, annotatedNamespace("instance"), bd)

	deleteBillingData(t, c, "sample", "instance")
	reconcileOnce(t, m, "sample", "instance")

	assert.Empty(t, writerOf(m).puts)
	got := getBillingData(t, c, "sample", "instance")
	assert.True(t, slices.Equal([]string{"example.io/keep"}, got.Finalizers))
}

func TestReconcile_DeletionPrefersLastReportClaim(t *testing.T) {
	pendingClaim := testClaim
	pendingClaim.UID = "pending-claim-uid"

	bd := billingData("sample", "instance")
	bd.Finalizers = []string{reportFinalizer}
	bd.Status.LastReport = &billingv1.Report{
		Key: sampleKey, Generation: 2, ObservedAt: metav1.NewTime(fixedNow), Claim: testClaim,
	}
	bd.Status.PendingReport = &billingv1.Report{
		Key: "pending", Generation: 3, ObservedAt: metav1.NewTime(fixedNow), Claim: pendingClaim,
	}
	m, c := newManager(t, annotatedNamespace("instance"), bd)

	deleted := deleteBillingData(t, c, "sample", "instance")
	reconcileOnce(t, m, "sample", "instance")

	doc := decodeDoc(t, writerOf(m).objects[deletedKey(deleted.DeletionTimestamp.Time, "sample-uid")])
	assert.Equal(t, testClaim, doc.Claim)
	requireGone(t, c, "sample", "instance")
}
