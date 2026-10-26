package v1

import (
	"testing"

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
