package v1alpha1

import "k8s.io/apimachinery/pkg/runtime"

func (in *BoriRelease) DeepCopyObject() runtime.Object {
	if c := in.DeepCopy(); c != nil {
		return c
	}
	return nil
}

func (in *BoriRelease) DeepCopy() *BoriRelease {
	if in == nil {
		return nil
	}
	out := new(BoriRelease)
	in.DeepCopyInto(out)
	return out
}

func (in *BoriRelease) DeepCopyInto(out *BoriRelease) {
	*out = *in
	out.TypeMeta = in.TypeMeta
	in.ObjectMeta.DeepCopyInto(&out.ObjectMeta)
	in.Spec.DeepCopyInto(&out.Spec)
	out.Status = in.Status
}

func (in *BoriReleaseSpec) DeepCopyInto(out *BoriReleaseSpec) {
	*out = *in
	if in.Components != nil {
		in2, out2 := &in.Components, &out.Components
		*out2 = make([]BoriReleaseComponentRef, len(*in2))
		copy(*out2, *in2)
	}
	out.Compatibility = in.Compatibility
	if in.Verification.Policies != nil {
		in2, out2 := &in.Verification.Policies, &out.Verification.Policies
		*out2 = make([]string, len(*in2))
		copy(*out2, *in2)
	}
	out.Promotion = in.Promotion
}

func (in *BoriReleaseList) DeepCopyObject() runtime.Object {
	if c := in.DeepCopy(); c != nil {
		return c
	}
	return nil
}

func (in *BoriReleaseList) DeepCopy() *BoriReleaseList {
	if in == nil {
		return nil
	}
	out := new(BoriReleaseList)
	in.DeepCopyInto(out)
	return out
}

func (in *BoriReleaseList) DeepCopyInto(out *BoriReleaseList) {
	*out = *in
	out.TypeMeta = in.TypeMeta
	in.ListMeta.DeepCopyInto(&out.ListMeta)
	if in.Items != nil {
		in2, out2 := &in.Items, &out.Items
		*out2 = make([]BoriRelease, len(*in2))
		for i := range *in2 {
			(*in2)[i].DeepCopyInto(&(*out2)[i])
		}
	}
}
