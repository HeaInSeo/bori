package v1alpha1

// DeepCopyInto methods for sub-types referenced from root API objects.
//
// controller-gen's object generator produces DeepCopyInto only for root types
// (+kubebuilder:object:root=true). Sub-types with slice/pointer fields are
// called by the generated root methods but must be written here.
//
// When adding or removing fields to sub-types, update this file accordingly.

// DeepCopyInto copies all fields of BoriDataPlaneStatus into out.
func (in *BoriDataPlaneStatus) DeepCopyInto(out *BoriDataPlaneStatus) {
	*out = *in
	out.ObservedAt = in.ObservedAt
	if in.Conditions != nil {
		in2, out2 := &in.Conditions, &out.Conditions
		*out2 = make([]Condition, len(*in2))
		for i := range *in2 {
			(*in2)[i].DeepCopyInto(&(*out2)[i])
		}
	}
	if in.Components != nil {
		in2, out2 := &in.Components, &out.Components
		*out2 = make([]ComponentStatus, len(*in2))
		for i := range *in2 {
			(*in2)[i].DeepCopyInto(&(*out2)[i])
		}
	}
}

// DeepCopyInto copies all fields of ComponentStatus into out.
func (in *ComponentStatus) DeepCopyInto(out *ComponentStatus) {
	*out = *in
	if in.Conditions != nil {
		in2, out2 := &in.Conditions, &out.Conditions
		*out2 = make([]Condition, len(*in2))
		for i := range *in2 {
			(*in2)[i].DeepCopyInto(&(*out2)[i])
		}
	}
}

// DeepCopyInto copies all fields of BoriReleaseSpec into out.
func (in *BoriReleaseSpec) DeepCopyInto(out *BoriReleaseSpec) {
	*out = *in
	if in.Components != nil {
		in2, out2 := &in.Components, &out.Components
		*out2 = make([]BoriReleaseComponentRef, len(*in2))
		copy(*out2, *in2)
	}
	if in.Verification.Policies != nil {
		policies := make([]string, len(in.Verification.Policies))
		copy(policies, in.Verification.Policies)
		out.Verification.Policies = policies
	}
}

// DeepCopyInto copies all fields of BoriReleaseStatus into out.
func (in *BoriReleaseStatus) DeepCopyInto(out *BoriReleaseStatus) {
	*out = *in
	out.ObservedAt = in.ObservedAt
}

// DeepCopyInto copies all fields of BoriRevisionSpec into out.
func (in *BoriRevisionSpec) DeepCopyInto(out *BoriRevisionSpec) {
	*out = *in
	if in.Components != nil {
		in2, out2 := &in.Components, &out.Components
		*out2 = make([]RevisionComponentRef, len(*in2))
		copy(*out2, *in2)
	}
}

// DeepCopyInto copies all fields of BoriRevisionStatus into out.
func (in *BoriRevisionStatus) DeepCopyInto(out *BoriRevisionStatus) {
	*out = *in
	out.ObservedAt = in.ObservedAt
	if in.PromotedAt != nil {
		t := *in.PromotedAt
		out.PromotedAt = &t
	}
}
