package v1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

type BillingDataPhase string

const (
	BillingDataPhasePending BillingDataPhase = "Pending"
	BillingDataPhaseReady   BillingDataPhase = "Ready"
	BillingDataPhaseFailed  BillingDataPhase = "Failed"
)

// BillingItem is one billable quantity.
type BillingItem struct {
	// +kubebuilder:validation:MinLength=1
	Name string `json:"name"`

	// Value is a Kubernetes quantity, kept exactly as written.
	// +kubebuilder:validation:MaxLength=64
	// +kubebuilder:validation:XValidation:rule="isQuantity(self)",message="value must be a quantity, like 3, 0.5 or 10Gi"
	Value string `json:"value"`
}

// BillingDataSpec describes the billing data to collect.
type BillingDataSpec struct {
	// Suspend stops reconciliation while true.
	// +optional
	Suspend *bool `json:"suspend,omitempty"`

	// Metadata applies to every item.
	// +optional
	Metadata map[string]string `json:"metadata,omitempty"`

	// +listType=map
	// +listMapKey=name
	// +kubebuilder:validation:MaxItems=64
	// +optional
	Items []BillingItem `json:"items,omitempty"`
}

type BillingDataStatus struct {
	// +optional
	Phase BillingDataPhase `json:"phase,omitempty"`
	// ObservedGeneration is the spec generation the phase was computed from.
	// +optional
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`
	// Message explains a Failed phase.
	// +optional
	Message string `json:"message,omitempty"`

	// +optional
	ClaimNamespace string `json:"claimNamespace,omitempty"`
	// +optional
	ClaimName string `json:"claimName,omitempty"`
}

// BillingData holds what one instance is billed for.
// +kubebuilder:object:root=true
// +kubebuilder:ac:generate=true
// +kubebuilder:resource:path=billingdata,singular=billingdata
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="Claim",type=string,JSONPath=`.status.claimName`
// +kubebuilder:printcolumn:name="Phase",type=string,JSONPath=`.status.phase`
// +kubebuilder:printcolumn:name="Message",type=string,JSONPath=`.status.message`
type BillingData struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   BillingDataSpec   `json:"spec,omitempty"`
	Status BillingDataStatus `json:"status,omitempty"`
}

// BillingDataList contains a list of BillingData.
// +kubebuilder:object:root=true
type BillingDataList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`

	Items []BillingData `json:"items"`
}

func init() { SchemeBuilder.Register(&BillingData{}, &BillingDataList{}) }
