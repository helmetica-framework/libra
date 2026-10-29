package v1

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/utils/ptr"
)

func TestSchemeRegistration(t *testing.T) {
	scheme := runtime.NewScheme()
	require.NoError(t, AddToScheme(scheme))

	assert.True(t, scheme.Recognizes(GroupVersion.WithKind("BillingData")))
	assert.True(t, scheme.Recognizes(GroupVersion.WithKind("BillingDataList")))
	assert.Equal(t, "libra.helmetica.io", GroupVersion.Group)
	assert.Equal(t, "v1", GroupVersion.Version)
}

func TestBillingDataDeepCopy(t *testing.T) {
	orig := &BillingData{
		ObjectMeta: metav1.ObjectMeta{
			Name:       "sample",
			Namespace:  "default",
			Generation: 3,
			Labels:     map[string]string{"key": "value"},
		},
		Spec: BillingDataSpec{
			Suspend: ptr.To(true),
		},
		Status: BillingDataStatus{
			Phase:              BillingDataPhaseReady,
			ObservedGeneration: 3,
		},
	}

	cp := orig.DeepCopy()
	require.Equal(t, orig, cp)

	cp.Labels["key"] = "mutated"
	assert.Equal(t, "value", orig.Labels["key"], "deepcopy must not share the labels map")

	*cp.Spec.Suspend = false
	assert.True(t, *orig.Spec.Suspend, "deepcopy must not share the suspend pointer")
}

func TestBillingDataListDeepCopy(t *testing.T) {
	orig := &BillingDataList{
		Items: []BillingData{
			{
				ObjectMeta: metav1.ObjectMeta{Name: "one"},
				Spec:       BillingDataSpec{Suspend: ptr.To(true)},
			},
		},
	}

	cp := orig.DeepCopy()
	require.Equal(t, orig, cp)

	*cp.Items[0].Spec.Suspend = false
	assert.True(t, *orig.Items[0].Spec.Suspend,
		"deepcopy must not share the items slice")
}

func TestBillingDataDeepCopy_MetadataAndItems(t *testing.T) {
	orig := &BillingData{
		Spec: BillingDataSpec{
			Metadata: map[string]string{"sla": "guaranteed"},
			Items: []BillingItem{
				{Name: "replicas", Value: "3"},
				{Name: "storage", Value: "10Gi"},
			},
		},
		Status: BillingDataStatus{ClaimNamespace: "tenant", ClaimName: "my-redis"},
	}

	cp := orig.DeepCopy()
	require.Equal(t, orig, cp)

	cp.Spec.Metadata["sla"] = "mutated"
	assert.Equal(t, "guaranteed", orig.Spec.Metadata["sla"],
		"deepcopy must not share the metadata map")

	cp.Spec.Items[0].Value = "mutated"
	assert.Equal(t, "3", orig.Spec.Items[0].Value,
		"deepcopy must not share the items slice")
}

func TestBillingDataDeepCopy_EmptySpec(t *testing.T) {
	orig := &BillingData{}

	cp := orig.DeepCopy()

	assert.Nil(t, cp.Spec.Metadata)
	assert.Nil(t, cp.Spec.Items)
}

func TestBillingDataDeepCopy_Reports(t *testing.T) {
	orig := &BillingData{
		Status: BillingDataStatus{
			PendingReport: &Report{
				Key:        "2026/10/26/T130000Z_bd-uid_3.json",
				Generation: 3,
				ObservedAt: metav1.NewTime(time.Date(2026, 10, 26, 13, 0, 0, 0, time.UTC)),
				Claim:      ClaimReference{Name: "my-redis", UID: "claim-uid"},
			},
			LastReport: &Report{
				Key:        "2026/10/26/T120000Z_bd-uid_2.json",
				Generation: 2,
				ObservedAt: metav1.NewTime(time.Date(2026, 10, 26, 12, 0, 0, 0, time.UTC)),
				Claim:      ClaimReference{Name: "my-redis", UID: "claim-uid"},
			},
		},
	}

	cp := orig.DeepCopy()
	require.Equal(t, orig, cp)

	cp.Status.PendingReport.Key = "mutated"
	cp.Status.LastReport.Claim.UID = "mutated"

	assert.Equal(t, "2026/10/26/T130000Z_bd-uid_3.json", orig.Status.PendingReport.Key)
	assert.Equal(t, "claim-uid", orig.Status.LastReport.Claim.UID)
}

func TestBillingDataDeepCopy_NoReports(t *testing.T) {
	cp := (&BillingData{}).DeepCopy()

	assert.Nil(t, cp.Status.PendingReport)
	assert.Nil(t, cp.Status.LastReport)
}

func TestReport_JSONFieldNames(t *testing.T) {
	raw, err := json.Marshal(Report{
		Key:        "k",
		Generation: 1,
		ObservedAt: metav1.NewTime(time.Date(2026, 10, 26, 12, 0, 0, 0, time.UTC)),
		Claim: ClaimReference{
			APIVersion: "example.io/v1",
			Kind:       "Redis",
			Namespace:  "tenant",
			Name:       "my-redis",
			UID:        "claim-uid",
		},
	})
	require.NoError(t, err)

	assert.JSONEq(t, `{
		"key": "k",
		"generation": 1,
		"observedAt": "2026-10-26T12:00:00Z",
		"claim": {
			"apiVersion": "example.io/v1",
			"kind": "Redis",
			"namespace": "tenant",
			"name": "my-redis",
			"uid": "claim-uid"
		}
	}`, string(raw))
}
