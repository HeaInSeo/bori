package v1alpha1

import "k8s.io/apimachinery/pkg/runtime"

// DeepCopyObject implements runtime.Object for BoriDataPlane.
func (in *BoriDataPlane) DeepCopyObject() runtime.Object {
	if c := in.DeepCopy(); c != nil {
		return c
	}
	return nil
}

// DeepCopy returns a deep copy of BoriDataPlane.
func (in *BoriDataPlane) DeepCopy() *BoriDataPlane {
	if in == nil {
		return nil
	}
	out := new(BoriDataPlane)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyInto copies all fields of BoriDataPlane into out.
func (in *BoriDataPlane) DeepCopyInto(out *BoriDataPlane) {
	*out = *in
	out.TypeMeta = in.TypeMeta
	in.ObjectMeta.DeepCopyInto(&out.ObjectMeta)
	out.Spec = in.Spec
	in.Status.DeepCopyInto(&out.Status)
}

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

// DeepCopyObject implements runtime.Object for BoriDataPlaneList.
func (in *BoriDataPlaneList) DeepCopyObject() runtime.Object {
	if c := in.DeepCopy(); c != nil {
		return c
	}
	return nil
}

// DeepCopy returns a deep copy of BoriDataPlaneList.
func (in *BoriDataPlaneList) DeepCopy() *BoriDataPlaneList {
	if in == nil {
		return nil
	}
	out := new(BoriDataPlaneList)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyInto copies all fields of BoriDataPlaneList into out.
func (in *BoriDataPlaneList) DeepCopyInto(out *BoriDataPlaneList) {
	*out = *in
	out.TypeMeta = in.TypeMeta
	in.ListMeta.DeepCopyInto(&out.ListMeta)
	if in.Items != nil {
		in2, out2 := &in.Items, &out.Items
		*out2 = make([]BoriDataPlane, len(*in2))
		for i := range *in2 {
			(*in2)[i].DeepCopyInto(&(*out2)[i])
		}
	}
}
