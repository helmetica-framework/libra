package controllers

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/go-logr/logr"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/events"
	"k8s.io/utils/ptr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	billingv1 "github.com/helmetica-framework/libra/api/v1"
	billingacv1 "github.com/helmetica-framework/libra/applyconfiguration/api/v1"
)

const (
	readyMessage     = "billing data ready"
	suspendedMessage = "reconciliation suspended"
	reportFinalizer  = "libra.helmetica.io/report-deletion"
)

var fieldOwner = client.FieldOwner("libra:billingdata")

// RecordWriter stores one billing record.
type RecordWriter interface {
	Put(ctx context.Context, key string, body []byte) error
}

// BillingDataManager reconciles BillingData objects.
type BillingDataManager struct {
	client.Client
	Scheme   *runtime.Scheme
	Recorder events.EventRecorder
	Log      logr.Logger
	// Now is the clock records are stamped with.
	Now    func() time.Time
	Writer RecordWriter
}

// state is everything one pass decided.
type state struct {
	Phase   billingv1.BillingDataPhase
	Message string
	// Claim and Record are set only when Phase is Ready.
	Claim  *ClaimRef
	Record *Record
}

// +kubebuilder:rbac:groups=libra.helmetica.io,resources=billingdata,verbs=get;list;watch;update;patch
// +kubebuilder:rbac:groups=libra.helmetica.io,resources=billingdata/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=libra.helmetica.io,resources=billingdata/finalizers,verbs=update
// +kubebuilder:rbac:groups=events.k8s.io,resources=events,verbs=create;patch
// +kubebuilder:rbac:groups="",resources=namespaces,verbs=get;list;watch

// Reconcile drives a BillingData's status to reflect desiredState.
func (r *BillingDataManager) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := r.Log.WithValues("billingdata", req.NamespacedName)

	bd := &billingv1.BillingData{}
	if err := r.Get(ctx, req.NamespacedName, bd); err != nil {
		if apierrors.IsNotFound(err) {
			log.V(1).Info("billing data is gone, nothing to do")
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, fmt.Errorf("getting billing data: %w", err)
	}

	if !bd.GetDeletionTimestamp().IsZero() {
		return ctrl.Result{}, r.finalize(ctx, bd)
	}

	err := r.ensureFinalizer(ctx, bd)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("ensuring finalizer: %w", err)
	}

	ns := &corev1.Namespace{}

	err = r.Get(ctx, client.ObjectKey{Name: bd.GetNamespace()}, ns)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("getting namespace: %w", err)
	}

	// The API server keeps whole seconds, so a retry reads back the same time.
	now := r.Now().UTC().Truncate(time.Second)
	want := desiredState(bd, ns, now)

	st := billingv1.BillingDataStatus{
		Phase:              want.Phase,
		ObservedGeneration: bd.Generation,
		Message:            want.Message,
		PendingReport:      bd.Status.PendingReport.DeepCopy(),
		LastReport:         bd.Status.LastReport.DeepCopy(),
	}
	if want.Claim != nil {
		st.ClaimNamespace, st.ClaimName = want.Claim.Namespace, want.Claim.Name
	}

	if want.Phase == billingv1.BillingDataPhaseReady {
		if err := r.writeReport(ctx, bd, want, now, &st); err != nil {
			return ctrl.Result{}, err
		}
	}

	return ctrl.Result{}, r.applyStatus(ctx, bd, st)
}

// writeReport writes the record of a Ready pass to S3 and updates st to
// match. On a write error st is Failed and the error is returned.
func (r *BillingDataManager) writeReport(ctx context.Context, bd *billingv1.BillingData, want state, now time.Time, st *billingv1.BillingDataStatus) error {
	rep, isNew := nextReport(bd, *want.Claim, now)
	if rep == nil {
		return nil
	}

	if isNew {
		st.PendingReport = rep

		err := r.applyStatus(ctx, bd, *st)
		if err != nil {
			return fmt.Errorf("writing pending record to status: %w", err)
		}
	}

	rec := *want.Record
	rec.ObservedAt = rep.ObservedAt.Time

	event := eventObserved
	if st.LastReport == nil {
		event = eventCreated
	}

	body, err := encodeRecord(rec, event)
	if err != nil {
		return fmt.Errorf("encoding record for %s/%s: %w", bd.GetNamespace(), bd.GetName(), err)
	}

	err = r.Writer.Put(ctx, rep.Key, body)
	if err != nil {
		errMsg := fmt.Errorf("writing record: %w", err)

		st.Phase = billingv1.BillingDataPhaseFailed
		st.Message = errMsg.Error()

		err := r.applyStatus(ctx, bd, *st)
		if err != nil {
			return errors.Join(errMsg, err)
		}

		return errMsg
	}

	st.LastReport = rep
	st.PendingReport = nil

	return nil
}

// applyStatus writes st unless bd already has it.
func (r *BillingDataManager) applyStatus(ctx context.Context, bd *billingv1.BillingData, st billingv1.BillingDataStatus) error {
	if equality.Semantic.DeepEqual(bd.Status, st) {
		return nil
	}

	if bd.Status.Phase != st.Phase {
		r.Log.Info("billing data phase changed",
			"billingdata", client.ObjectKeyFromObject(bd), "from", bd.Status.Phase, "to", st.Phase)
	}

	ac := billingacv1.BillingDataStatus().
		WithPhase(st.Phase).
		WithObservedGeneration(st.ObservedGeneration).
		WithMessage(st.Message)

	if st.ClaimNamespace != "" || st.ClaimName != "" {
		ac.WithClaimNamespace(st.ClaimNamespace).WithClaimName(st.ClaimName)
	}
	if st.PendingReport != nil {
		ac.WithPendingReport(reportAC(st.PendingReport))
	}
	if st.LastReport != nil {
		ac.WithLastReport(reportAC(st.LastReport))
	}

	status := billingacv1.BillingData(bd.GetName(), bd.GetNamespace()).
		WithStatus(ac)

	if err := r.Status().Apply(ctx, status, fieldOwner, client.ForceOwnership); err != nil {
		return fmt.Errorf("applying billing data status: %w", err)
	}

	bd.Status = st

	return nil
}

func reportAC(rep *billingv1.Report) *billingacv1.ReportApplyConfiguration {
	return billingacv1.Report().
		WithKey(rep.Key).
		WithGeneration(rep.Generation).
		WithObservedAt(rep.ObservedAt).
		WithClaim(billingacv1.ClaimReference().
			WithAPIVersion(rep.Claim.APIVersion).
			WithKind(rep.Claim.Kind).
			WithNamespace(rep.Claim.Namespace).
			WithName(rep.Claim.Name).
			WithUID(rep.Claim.UID))
}

// ensureFinalizer adds reportFinalizer if it is not there yet.
func (r *BillingDataManager) ensureFinalizer(ctx context.Context, bd *billingv1.BillingData) error {
	if controllerutil.ContainsFinalizer(bd, reportFinalizer) {
		return nil
	}

	patch := client.MergeFromWithOptions(bd.DeepCopy(), client.MergeFromWithOptimisticLock{})
	controllerutil.AddFinalizer(bd, reportFinalizer)

	err := r.Patch(ctx, bd, patch)
	if err != nil {
		return fmt.Errorf("adding finalizer: %w", err)
	}

	return nil
}

// finalize writes the deleted record, then lets the object go.
func (r *BillingDataManager) finalize(ctx context.Context, bd *billingv1.BillingData) error {
	if !controllerutil.ContainsFinalizer(bd, reportFinalizer) {
		return nil
	}

	rep := bd.Status.LastReport
	if rep == nil {
		rep = bd.Status.PendingReport
	}

	if rep != nil {
		at := bd.DeletionTimestamp.Time
		rec := buildRecord(bd, rep.Claim, at)
		body, err := encodeRecord(rec, eventDeleted)
		if err != nil {
			return fmt.Errorf("encoding deleted record: %w", err)
		}

		err = r.Writer.Put(ctx, deletedKey(at, string(bd.UID)), body)
		if err != nil {
			st := bd.Status.DeepCopy()
			errMsg := fmt.Errorf("writing deleted record: %w", err)

			st.Phase = billingv1.BillingDataPhaseFailed
			st.Message = errMsg.Error()

			err := r.applyStatus(ctx, bd, *st)
			if err != nil {
				return errors.Join(errMsg, err)
			}

			return errMsg
		}
	}

	patch := client.MergeFromWithOptions(bd.DeepCopy(), client.MergeFromWithOptimisticLock{})
	controllerutil.RemoveFinalizer(bd, reportFinalizer)

	err := r.Patch(ctx, bd, patch)
	if err != nil {
		return fmt.Errorf("removing finalizer: %w", err)
	}

	return nil
}

// desiredState decides phase, message, claim and record for one pass.
func desiredState(bd *billingv1.BillingData, ns *corev1.Namespace, now time.Time) state {
	if ptr.Deref(bd.Spec.Suspend, false) {
		return state{
			Phase:   billingv1.BillingDataPhasePending,
			Message: suspendedMessage,
		}
	}

	claim, err := claimFromNamespace(ns)
	if err != nil {
		return state{
			Phase:   billingv1.BillingDataPhaseFailed,
			Message: err.Error(),
		}
	}

	record := buildRecord(bd, claim, now)

	return state{
		Phase:   billingv1.BillingDataPhaseReady,
		Message: readyMessage,
		Claim:   &claim,
		Record:  &record,
	}
}

// namespaceToBillingData maps a namespace to every BillingData in it, so a
// BillingData recovers once chrysopoeia's annotations appear.
func (r *BillingDataManager) namespaceToBillingData(ctx context.Context, obj client.Object) []reconcile.Request {
	bdl := &billingv1.BillingDataList{}

	err := r.List(ctx, bdl, client.InNamespace(obj.GetName()))
	if err != nil {
		r.Log.Error(err, "listing billingData", "namespace", obj.GetName())
		return nil
	}

	req := make([]reconcile.Request, 0, len(bdl.Items))

	for _, bd := range bdl.Items {
		req = append(req, reconcile.Request{
			NamespacedName: types.NamespacedName{
				Namespace: obj.GetName(),
				Name:      bd.GetName(),
			},
		})
	}

	return req
}

// SetupWithManager wires the controller.
func (r *BillingDataManager) SetupWithManager(name string, mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		Named(name).
		For(&billingv1.BillingData{}).
		Watches(&corev1.Namespace{}, handler.EnqueueRequestsFromMapFunc(r.namespaceToBillingData)).
		Complete(r)
}
