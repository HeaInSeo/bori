package v1alpha1

import (
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/scheme"
)

var (
	// GroupVersion is the group+version for all bori API types.
	GroupVersion = schema.GroupVersion{Group: "bori.dev", Version: "v1alpha1"}

	// SchemeBuilder registers BoriDataPlane and BoriDataPlaneList with a scheme.
	SchemeBuilder = &scheme.Builder{GroupVersion: GroupVersion}

	// AddToScheme adds all v1alpha1 types to the given scheme.
	// Use this when building a controller-runtime manager:
	//   _ = v1alpha1.AddToScheme(scheme)
	AddToScheme = SchemeBuilder.AddToScheme
)

func init() {
	SchemeBuilder.Register(&BoriDataPlane{}, &BoriDataPlaneList{})
	SchemeBuilder.Register(&BoriRelease{}, &BoriReleaseList{})
}
