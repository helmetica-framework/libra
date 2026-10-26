package controllers

import (
	"fmt"
	"strings"

	corev1 "k8s.io/api/core/v1"
)

const (
	annClaimAPIVersion = "chrysopoeia.io/claim-apiVersion"
	annClaimKind       = "chrysopoeia.io/claim-kind"
	annClaimNamespace  = "chrysopoeia.io/claim-namespace"
	annClaimName       = "chrysopoeia.io/claim-name"
	annClaimUID        = "chrysopoeia.io/claim-uid"
)

// ClaimRef names the claim a BillingData bills for.
type ClaimRef struct {
	APIVersion string
	Kind       string
	Namespace  string
	Name       string
	UID        string
}

// claimFromNamespace reads the claim chrysopoeia stamped on ns.
func claimFromNamespace(ns *corev1.Namespace) (ClaimRef, error) {
	ann := ns.GetAnnotations()

	var missing []string
	for _, a := range []string{
		annClaimAPIVersion,
		annClaimKind,
		annClaimNamespace,
		annClaimName,
		annClaimUID,
	} {
		if ann[a] == "" {
			missing = append(missing, a)
		}
	}

	if len(missing) > 0 {
		return ClaimRef{}, fmt.Errorf("namespace %s is missing claim annotations: %s",
			ns.GetName(), strings.Join(missing, ", "))
	}

	return ClaimRef{
		APIVersion: ann[annClaimAPIVersion],
		Kind:       ann[annClaimKind],
		Namespace:  ann[annClaimNamespace],
		Name:       ann[annClaimName],
		UID:        ann[annClaimUID],
	}, nil
}
