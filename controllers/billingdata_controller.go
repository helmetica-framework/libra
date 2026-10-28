package controllers

import (
	"context"
	"fmt"

	"github.com/go-logr/logr"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/events"
	"k8s.io/utils/ptr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

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
}

// +kubebuilder:rbac:groups=libra.helmetica.io,resources=billingdata,verbs=get;list;watch;update;patch
// +kubebuilder:rbac:groups=libra.helmetica.io,resources=billingdata/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=libra.helmetica.io,resources=billingdata/finalizers,verbs=update
// +kubebuilder:rbac:groups=events.k8s.io,resources=events,verbs=create;patch

// Reconcile drives a BillingData's status to reflect desiredPhase.
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

	phase, message := desiredPhase(bd)

	if bd.Status.Phase == phase &&
		bd.Status.ObservedGeneration == bd.Generation &&
		bd.Status.Message == message {
		return ctrl.Result{}, nil
	}

	if bd.Status.Phase != phase {
		log.Info("billing data phase changed", "from", bd.Status.Phase, "to", phase)
	}

	status := billingacv1.BillingData(bd.GetName(), bd.GetNamespace()).
		WithStatus(billingacv1.BillingDataStatus().
			WithPhase(phase).
			WithObservedGeneration(bd.Generation).
			WithMessage(message))

	if err := r.Status().Apply(ctx, status, fieldOwner, client.ForceOwnership); err != nil {
		return ctrl.Result{}, fmt.Errorf("applying billing data status: %w", err)
	}

	return ctrl.Result{}, nil
}

func desiredPhase(bd *billingv1.BillingData) (billingv1.BillingDataPhase, string) {
	if ptr.Deref(bd.Spec.Suspend, false) {
		return billingv1.BillingDataPhasePending, suspendedMessage
	}

	return billingv1.BillingDataPhaseReady, readyMessage
}

// SetupWithManager wires the controller.
func (r *BillingDataManager) SetupWithManager(name string, mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		Named(name).
		For(&billingv1.BillingData{}).
		Complete(r)
}
