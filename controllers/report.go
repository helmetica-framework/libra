package controllers

import (
	"encoding/json"
	"fmt"
	"time"

	billingv1 "github.com/helmetica-framework/libra/api/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
	recordVersion = 1
	eventCreated  = "created"
	eventObserved = "observed"
	eventDeleted  = "deleted"

	// keyTimeLayout starts every key, in UTC, so keys sort by time.
	keyTimeLayout = "2006/01/02/T150405Z"
)

// recordDoc is the JSON body of one S3 object.
type recordDoc struct {
	Version int    `json:"version"`
	Event   string `json:"event"`
	Record
}

// observedKey is the S3 key for a record observed at t.
func observedKey(t time.Time, uid string, generation int64) string {
	return fmt.Sprintf("%s_%s_%d.json", t.UTC().Format(keyTimeLayout), uid, generation)
}

// deletedKey is the S3 key for the deleted record of uid. t is the
// deletionTimestamp, so retries get the same key.
func deletedKey(t time.Time, uid string) string {
	return fmt.Sprintf("%s_%s_deleted.json", t.UTC().Format(keyTimeLayout), uid)
}

// encodeRecord renders rec as the body of one S3 object.
func encodeRecord(rec Record, event string) ([]byte, error) {
	rd := recordDoc{
		Record:  rec,
		Event:   event,
		Version: recordVersion,
	}

	if rd.Metadata == nil {
		rd.Metadata = map[string]string{}
	}

	if rd.Items == nil {
		rd.Items = []billingv1.BillingItem{}
	}

	b, err := json.Marshal(rd)
	if err != nil {
		return nil, fmt.Errorf("marshalling record: %w", err)
	}

	return b, nil
}

// nextReport returns the report to write for bd, or nil when the current
// (generation, claim UID) was already reported. isNew means it must be saved
// in status before the write.
func nextReport(bd *billingv1.BillingData, claim ClaimRef, now time.Time) (report *billingv1.Report, isNew bool) {
	if sameChange(bd.Status.LastReport, bd.GetGeneration(), claim.UID) {
		return nil, false
	}

	if sameChange(bd.Status.PendingReport, bd.GetGeneration(), claim.UID) {
		return bd.Status.PendingReport.DeepCopy(), false
	}

	return &billingv1.Report{
		Key:        observedKey(now, string(bd.GetUID()), bd.GetGeneration()),
		Generation: bd.GetGeneration(),
		ObservedAt: metav1.NewTime(now),
		Claim:      claim,
	}, true
}

// sameChange reports whether r was made for this generation and claim.
func sameChange(r *billingv1.Report, generation int64, claimUID string) bool {
	return r != nil && r.Generation == generation && r.Claim.UID == claimUID
}
