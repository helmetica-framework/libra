package controllers

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	billingv1 "github.com/helmetica-framework/libra/api/v1"
)

func TestObservedKey(t *testing.T) {
	assert.Equal(t, "2026/10/26/T120000Z_bd-uid_4.json", observedKey(fixedNow, "bd-uid", 4))
}

func TestObservedKey_UsesUTC(t *testing.T) {
	cet := time.FixedZone("CET", 3600)

	assert.Equal(t, "2026/10/26/T120000Z_bd-uid_4.json", observedKey(fixedNow.In(cet), "bd-uid", 4))
}

func TestObservedKey_DayRollover(t *testing.T) {
	cet := time.FixedZone("CET", 3600)
	justAfterMidnight := time.Date(2026, 10, 27, 0, 30, 0, 0, cet)

	assert.Equal(t, "2026/10/26/T233000Z_bd-uid_1.json", observedKey(justAfterMidnight, "bd-uid", 1))
}

func TestDeletedKey(t *testing.T) {
	assert.Equal(t, "2026/10/26/T120000Z_bd-uid_deleted.json", deletedKey(fixedNow, "bd-uid"))
}

func TestKeys_SortByTime(t *testing.T) {
	earlier := observedKey(fixedNow, "zzz", 9)
	later := observedKey(fixedNow.Add(time.Second), "aaa", 1)

	assert.Less(t, earlier, later)
}

func TestEncodeRecord_Observed(t *testing.T) {
	raw, err := encodeRecord(buildRecord(recordSource(), testClaim, fixedNow), eventObserved)
	require.NoError(t, err)

	assert.JSONEq(t, `{
		"version": 1,
		"event": "observed",
		"observedAt": "2026-10-26T12:00:00Z",
		"claim": {
			"apiVersion": "example.io/v1",
			"kind": "Redis",
			"namespace": "tenant",
			"name": "my-redis",
			"uid": "claim-uid"
		},
		"source": {"namespace": "instance", "name": "default", "uid": "bd-uid", "generation": 4},
		"metadata": {"sla": "guaranteed"},
		"items": [
			{"name": "replicas", "value": "3"},
			{"name": "storage", "value": "10Gi"}
		]
	}`, string(raw))
}

func TestEncodeRecord_Deleted(t *testing.T) {
	raw, err := encodeRecord(buildRecord(recordSource(), testClaim, fixedNow), eventDeleted)
	require.NoError(t, err)

	doc := map[string]any{}
	require.NoError(t, json.Unmarshal(raw, &doc))
	assert.Equal(t, "deleted", doc["event"])
}

func TestEncodeRecord_EmptySpecWritesEmptyCollections(t *testing.T) {
	bd := recordSource()
	bd.Spec = billingv1.BillingDataSpec{}
	rec := buildRecord(bd, testClaim, fixedNow)

	raw, err := encodeRecord(rec, eventObserved)
	require.NoError(t, err)

	doc := map[string]any{}
	require.NoError(t, json.Unmarshal(raw, &doc))
	assert.Equal(t, map[string]any{}, doc["metadata"])
	assert.Equal(t, []any{}, doc["items"])
	assert.Nil(t, rec.Metadata, "encodeRecord must not change the caller's record")
	assert.Nil(t, rec.Items, "encodeRecord must not change the caller's record")
}

func reportFor(key string, generation int64, at time.Time, claim ClaimRef) *billingv1.Report {
	return &billingv1.Report{
		Key:        key,
		Generation: generation,
		ObservedAt: metav1.NewTime(at),
		Claim:      claim,
	}
}

func TestNextReport_NothingReported(t *testing.T) {
	bd := recordSource()

	got, isNew := nextReport(bd, testClaim, fixedNow)

	assert.True(t, isNew)
	assert.Equal(t, reportFor("2026/10/26/T120000Z_bd-uid_4.json", 4, fixedNow, testClaim), got)
}

func TestNextReport_AlreadyReported(t *testing.T) {
	bd := recordSource()
	bd.Status.LastReport = reportFor("old", 4, fixedNow.Add(-time.Hour), testClaim)

	got, isNew := nextReport(bd, testClaim, fixedNow)

	assert.Nil(t, got)
	assert.False(t, isNew)
}

func TestNextReport_PendingIsReused(t *testing.T) {
	bd := recordSource()
	pending := reportFor("pending-key", 4, fixedNow.Add(-time.Minute), testClaim)
	bd.Status.PendingReport = pending

	got, isNew := nextReport(bd, testClaim, fixedNow)

	assert.False(t, isNew)
	assert.Equal(t, pending, got, "a retry must keep the first key and time")
}

func TestNextReport_StalePendingIsReplaced(t *testing.T) {
	bd := recordSource()
	bd.Status.PendingReport = reportFor("stale", 3, fixedNow.Add(-time.Minute), testClaim)

	got, isNew := nextReport(bd, testClaim, fixedNow)

	assert.True(t, isNew)
	require.NotNil(t, got)
	assert.Equal(t, "2026/10/26/T120000Z_bd-uid_4.json", got.Key)
}

func TestNextReport_ClaimChangeCounts(t *testing.T) {
	bd := recordSource()
	bd.Status.LastReport = reportFor("old", 4, fixedNow.Add(-time.Hour), testClaim)

	other := testClaim
	other.UID = "other-claim-uid"

	got, isNew := nextReport(bd, other, fixedNow)

	assert.True(t, isNew)
	require.NotNil(t, got)
	assert.Equal(t, other, got.Claim)
}

func TestNextReport_PendingForOtherClaimIsReplaced(t *testing.T) {
	bd := recordSource()
	old := testClaim
	old.UID = "old-claim-uid"
	bd.Status.PendingReport = reportFor("stale", 4, fixedNow.Add(-time.Minute), old)

	got, isNew := nextReport(bd, testClaim, fixedNow)

	assert.True(t, isNew)
	require.NotNil(t, got)
	assert.Equal(t, testClaim, got.Claim)
}

func TestNextReport_PendingIsACopy(t *testing.T) {
	bd := recordSource()
	bd.Status.PendingReport = reportFor("pending-key", 4, fixedNow.Add(-time.Minute), testClaim)

	got, _ := nextReport(bd, testClaim, fixedNow)
	require.NotNil(t, got)
	got.Key = "mutated"

	assert.Equal(t, "pending-key", bd.Status.PendingReport.Key)
}
