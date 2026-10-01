// +kubebuilder:object:generate=true
// +groupName=ops.bori.dev

// Package v1alpha1 is the O2 candidate public wire for BORI's app-neutral
// operational contract model: OperationalContract, OperationalTarget and
// OperationalReferenceGrant.
//
// CANDIDATE WIRE — NOT A COMPATIBILITY PROMISE. The group ops.bori.dev and
// version v1alpha1 are a candidate namespace pending central and Guardrail
// acceptance; the group/domain may be renamed and the schema may change
// without migration support until then.
//
// These types only transport the semantics owned by pkg/operations (O1). They
// define no evaluation rules of their own.
package v1alpha1
