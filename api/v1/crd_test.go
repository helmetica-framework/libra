package v1

import (
	"context"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"k8s.io/apiextensions-apiserver/pkg/apis/apiextensions"
	apiextensionsinstall "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/install"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	apiextensionsvalidation "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/validation"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/yaml"
)

// The CEL rule only runs in a real API server, so these check the generated CRD.

func loadCRD(t *testing.T) *apiextensionsv1.CustomResourceDefinition {
	t.Helper()

	raw, err := os.ReadFile("../../config/crd/bases/libra.helmetica.io_billingdata.yaml")
	require.NoError(t, err)

	crd := &apiextensionsv1.CustomResourceDefinition{}
	require.NoError(t, yaml.Unmarshal(raw, crd))

	return crd
}

func itemValueSchema(t *testing.T, crd *apiextensionsv1.CustomResourceDefinition) apiextensionsv1.JSONSchemaProps {
	t.Helper()

	require.Len(t, crd.Spec.Versions, 1)
	spec := crd.Spec.Versions[0].Schema.OpenAPIV3Schema.Properties["spec"]
	items := spec.Properties["items"]
	require.NotNil(t, items.Items, "spec.items must be a list")

	return items.Items.Schema.Properties["value"]
}

func TestCRD_ItemValueIsCheckedAsQuantity(t *testing.T) {
	value := itemValueSchema(t, loadCRD(t))

	assert.Equal(t, "string", value.Type)
	require.Len(t, value.XValidations, 1)
	assert.Equal(t, "isQuantity(self)", value.XValidations[0].Rule)
	assert.Contains(t, value.XValidations[0].Message, "quantity")
}

func TestCRD_ItemValueHasMaxLength(t *testing.T) {
	value := itemValueSchema(t, loadCRD(t))

	require.NotNil(t, value.MaxLength, "CEL rules on strings need a max length for the cost budget")
	assert.Equal(t, int64(64), *value.MaxLength)
}

// Runs the same checks the API server runs on create, CEL cost budget included.
func TestCRD_PassesAPIServerValidation(t *testing.T) {
	scheme := runtime.NewScheme()
	apiextensionsinstall.Install(scheme)

	internal := &apiextensions.CustomResourceDefinition{}
	require.NoError(t, scheme.Convert(loadCRD(t), internal, nil))

	// The API server fills this in on create, before it validates.
	for _, v := range internal.Spec.Versions {
		if v.Storage {
			internal.Status.StoredVersions = []string{v.Name}
		}
	}

	errs := apiextensionsvalidation.ValidateCustomResourceDefinition(context.Background(), internal)
	assert.Empty(t, errs.ToAggregate(), "the API server would reject this CRD")
}

func TestCRD_ItemsHaveMaxItems(t *testing.T) {
	crd := loadCRD(t)
	items := crd.Spec.Versions[0].Schema.OpenAPIV3Schema.Properties["spec"].Properties["items"]

	require.NotNil(t, items.MaxItems, "the CEL rule on each item needs a bounded list for the cost budget")
	assert.Equal(t, int64(64), *items.MaxItems)
}
