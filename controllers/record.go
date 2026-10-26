package controllers

import (
	"time"

	billingv1 "github.com/helmetica-framework/libra/api/v1"
)

// SourceRef names the BillingData a record came from.
type SourceRef struct {
	Namespace  string
	Name       string
	UID        string
	Generation int64
}

// Record is one observation of a BillingData.
type Record struct {
	Claim      ClaimRef
	Source     SourceRef
	Metadata   map[string]string
	Items      []billingv1.BillingItem
	ObservedAt time.Time
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
