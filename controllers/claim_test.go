package controllers

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

var testClaim = ClaimRef{
	APIVersion: "example.io/v1",
	Kind:       "Redis",
	Namespace:  "tenant",
	Name:       "my-redis",
	UID:        "claim-uid",
}

func annotatedNamespace(name string) *corev1.Namespace {
	return &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: name,
			Annotations: map[string]string{
				annClaimAPIVersion: testClaim.APIVersion,
				annClaimKind:       testClaim.Kind,
				annClaimNamespace:  testClaim.Namespace,
				annClaimName:       testClaim.Name,
				annClaimUID:        testClaim.UID,
			},
		},
	}
}

func plainNamespace(name string) *corev1.Namespace {
	return &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: name}}
}

func TestClaimFromNamespace_AllAnnotations(t *testing.T) {
	claim, err := claimFromNamespace(annotatedNamespace("instance"))

	require.NoError(t, err)
	assert.Equal(t, testClaim, claim)
}

func TestClaimFromNamespace_NoAnnotations(t *testing.T) {
	_, err := claimFromNamespace(plainNamespace("instance"))

	require.Error(t, err)
	for _, ann := range []string{
		annClaimAPIVersion, annClaimKind, annClaimNamespace, annClaimName, annClaimUID,
	} {
		assert.Contains(t, err.Error(), ann)
	}
}

func TestClaimFromNamespace_OneMissing(t *testing.T) {
	ns := annotatedNamespace("instance")
	delete(ns.Annotations, annClaimUID)

	_, err := claimFromNamespace(ns)

	require.Error(t, err)
	assert.Contains(t, err.Error(), annClaimUID)
	assert.NotContains(t, err.Error(), annClaimKind)
}

func TestClaimFromNamespace_EmptyValueCountsAsMissing(t *testing.T) {
	ns := annotatedNamespace("instance")
	ns.Annotations[annClaimKind] = ""

	_, err := claimFromNamespace(ns)

	require.Error(t, err)
	assert.Contains(t, err.Error(), annClaimKind)
}

func TestClaimFromNamespace_ErrorNamesNamespace(t *testing.T) {
	_, err := claimFromNamespace(plainNamespace("instance"))

	require.Error(t, err)
	assert.Contains(t, err.Error(), "namespace instance")
}

func TestClaimFromNamespace_StableOrder(t *testing.T) {
	_, first := claimFromNamespace(plainNamespace("instance"))
	_, second := claimFromNamespace(plainNamespace("instance"))

	require.Error(t, first)
	assert.Equal(t, first.Error(), second.Error())
	assert.Equal(t,
		"namespace instance is missing claim annotations: "+
			"chrysopoeia.io/claim-apiVersion, chrysopoeia.io/claim-kind, "+
			"chrysopoeia.io/claim-namespace, chrysopoeia.io/claim-name, "+
			"chrysopoeia.io/claim-uid",
		first.Error())
}
