package v1alpha1

import (
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/scheme"
)

var (
	// GroupVersion is the candidate group+version of the operational API.
	GroupVersion = schema.GroupVersion{Group: "ops.bori.dev", Version: "v1alpha1"}

	// SchemeBuilder registers the operational API types with a scheme.
	SchemeBuilder = &scheme.Builder{GroupVersion: GroupVersion} //nolint:staticcheck

	// AddToScheme adds all ops.bori.dev/v1alpha1 types to the given scheme.
	AddToScheme = SchemeBuilder.AddToScheme
)

func init() {
	SchemeBuilder.Register(&OperationalContract{}, &OperationalContractList{})
	SchemeBuilder.Register(&OperationalTarget{}, &OperationalTargetList{})
	SchemeBuilder.Register(&OperationalReferenceGrant{}, &OperationalReferenceGrantList{})
}
