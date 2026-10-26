package controllers

import (
	"context"
	"fmt"
	"time"

	"github.com/go-logr/logr"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/events"
	"k8s.io/utils/ptr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	billingv1 "github.com/helmetica-framework/libra/api/v1"
	billingacv1 "github.com/helmetica-framework/libra/applyconfiguration/api/v1"
)

const (
	readyMessage     = "billing data ready"
	suspendedMessage = "reconciliation suspended"
)

var fieldOwner = client.FieldOwner("libra:billingdata")

// BillingDataManager reconciles BillingData objects.
type BillingDataManager struct {
	client.Client
	Scheme   *runtime.Scheme
	Recorder events.EventRecorder
	Log      logr.Logger
	// Now is the clock records are stamped with.
	Now func() time.Time
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
		return ctrl.Result{}, nil
	}

	ns := &corev1.Namespace{}

	err := r.Get(ctx, client.ObjectKey{Name: bd.GetNamespace()}, ns)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("getting namespace: %w", err)
	}

	want := desiredState(bd, ns, r.Now())

	var claimNamespace, claimName string
	if want.Claim != nil {
		claimNamespace, claimName = want.Claim.Namespace, want.Claim.Name
	}

	if bd.Status.Phase == want.Phase &&
		bd.Status.ObservedGeneration == bd.Generation &&
		bd.Status.Message == want.Message &&
		bd.Status.ClaimName == claimName &&
		bd.Status.ClaimNamespace == claimNamespace {
		return ctrl.Result{}, nil
	}

	if bd.Status.Phase != want.Phase {
		log.Info("billing data phase changed", "from", bd.Status.Phase, "to", want.Phase)
	}

	st := billingacv1.BillingDataStatus().
		WithPhase(want.Phase).
		WithObservedGeneration(bd.Generation).
		WithMessage(want.Message)

	if want.Claim != nil {
		st.WithClaimNamespace(claimNamespace).WithClaimName(claimName)
	}

	status := billingacv1.BillingData(bd.GetName(), bd.GetNamespace()).
		WithStatus(st)

	if err := r.Status().Apply(ctx, status, fieldOwner, client.ForceOwnership); err != nil {
		return ctrl.Result{}, fmt.Errorf("applying billing data status: %w", err)
	}

	return ctrl.Result{}, nil
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
