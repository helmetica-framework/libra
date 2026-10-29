package controllers

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	billingv1 "github.com/helmetica-framework/libra/api/v1"
)

var fixedNow = time.Date(2026, 10, 26, 12, 0, 0, 0, time.UTC)

func recordSource() *billingv1.BillingData {
	return &billingv1.BillingData{
		ObjectMeta: metav1.ObjectMeta{
			Name:       "default",
			Namespace:  "instance",
			UID:        types.UID("bd-uid"),
			Generation: 4,
		},
		Spec: billingv1.BillingDataSpec{
			Metadata: map[string]string{"sla": "guaranteed"},
			Items: []billingv1.BillingItem{
				{Name: "replicas", Value: "3"},
				{Name: "storage", Value: "10Gi"},
			},
		},
	}
}

func TestBuildRecord_CarriesEverything(t *testing.T) {
	rec := buildRecord(recordSource(), testClaim, fixedNow)

	assert.Equal(t, Record{
		Claim: testClaim,
		Source: SourceRef{
			Namespace:  "instance",
			Name:       "default",
			UID:        "bd-uid",
			Generation: 4,
		},
		Metadata: map[string]string{"sla": "guaranteed"},
		Items: []billingv1.BillingItem{
			{Name: "replicas", Value: "3"},
			{Name: "storage", Value: "10Gi"},
		},
		ObservedAt: fixedNow,
	}, rec)
}

func TestBuildRecord_EmptySpec(t *testing.T) {
	bd := recordSource()
	bd.Spec = billingv1.BillingDataSpec{}

	rec := buildRecord(bd, testClaim, fixedNow)

	assert.Empty(t, rec.Metadata)
	assert.Empty(t, rec.Items)
	assert.Equal(t, testClaim, rec.Claim)
}

func TestBuildRecord_DoesNotShareMemory(t *testing.T) {
	bd := recordSource()

	rec := buildRecord(bd, testClaim, fixedNow)
	require.Len(t, rec.Items, 2)

	bd.Spec.Metadata["sla"] = "mutated"
	bd.Spec.Items[0].Value = "mutated"

	assert.Equal(t, "guaranteed", rec.Metadata["sla"])
	assert.Equal(t, "3", rec.Items[0].Value)
}

func TestBuildRecord_MetadataCannotOverrideClaim(t *testing.T) {
	bd := recordSource()
	bd.Spec.Metadata["claimName"] = "someone-else"

	rec := buildRecord(bd, testClaim, fixedNow)

	assert.Equal(t, "my-redis", rec.Claim.Name)
	assert.Equal(t, "someone-else", rec.Metadata["claimName"])
}

func TestBuildRecord_ValuesPassThrough(t *testing.T) {
	bd := recordSource()
	bd.Spec.Items = []billingv1.BillingItem{{Name: "storage", Value: "1.5Gi"}}

	rec := buildRecord(bd, testClaim, fixedNow)

	require.Len(t, rec.Items, 1)
	assert.Equal(t, "1.5Gi", rec.Items[0].Value, "must not be rewritten to 1536Mi")
}
