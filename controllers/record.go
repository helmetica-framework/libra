package controllers

import (
	"time"

	billingv1 "github.com/helmetica-framework/libra/api/v1"
)

// SourceRef names the BillingData a record came from.
type SourceRef struct {
	Namespace  string `json:"namespace"`
	Name       string `json:"name"`
	UID        string `json:"uid"`
	Generation int64  `json:"generation"`
}

// Record is one observation of a BillingData.
type Record struct {
	Claim      ClaimRef                `json:"claim"`
	Source     SourceRef               `json:"source"`
	Metadata   map[string]string       `json:"metadata"`
	Items      []billingv1.BillingItem `json:"items"`
	ObservedAt time.Time               `json:"observedAt"`
}

// buildRecord turns bd into the record the S3 writer sends.
func buildRecord(bd *billingv1.BillingData, claim ClaimRef, now time.Time) Record {
	spec := bd.Spec.DeepCopy()

	return Record{
		Claim: claim,
		Source: SourceRef{
			Namespace:  bd.GetNamespace(),
			Name:       bd.GetName(),
			UID:        string(bd.GetUID()),
			Generation: bd.GetGeneration(),
		},
		Metadata:   spec.Metadata,
		Items:      spec.Items,
		ObservedAt: now,
	}
}
