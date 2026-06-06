package v1alpha1

import "k8s.io/apimachinery/pkg/runtime"

func (in *BoriRevision) DeepCopyObject() runtime.Object {
	if c := in.DeepCopy(); c != nil {
		return c
	}
	return nil
}

func (in *BoriRevision) DeepCopy() *BoriRevision {
	if in == nil {
		return nil
	}
	out := new(BoriRevision)
	in.DeepCopyInto(out)
	return out
}

func (in *BoriRevision) DeepCopyInto(out *BoriRevision) {
	*out = *in
	out.TypeMeta = in.TypeMeta
	in.ObjectMeta.DeepCopyInto(&out.ObjectMeta)
	in.Spec.DeepCopyInto(&out.Spec)
	in.Status.DeepCopyInto(&out.Status)
}

func (in *BoriRevisionSpec) DeepCopyInto(out *BoriRevisionSpec) {
	*out = *in
	if in.Components != nil {
		in2, out2 := &in.Components, &out.Components
		*out2 = make([]RevisionComponentRef, len(*in2))
		copy(*out2, *in2)
	}
}

func (in *BoriRevisionStatus) DeepCopyInto(out *BoriRevisionStatus) {
	*out = *in
	out.ObservedAt = in.ObservedAt
	if in.PromotedAt != nil {
		t := *in.PromotedAt
		out.PromotedAt = &t
	}
}

func (in *BoriRevisionList) DeepCopyObject() runtime.Object {
	if c := in.DeepCopy(); c != nil {
		return c
	}
	return nil
}

func (in *BoriRevisionList) DeepCopy() *BoriRevisionList {
	if in == nil {
		return nil
	}
	out := new(BoriRevisionList)
	in.DeepCopyInto(out)
	return out
}

func (in *BoriRevisionList) DeepCopyInto(out *BoriRevisionList) {
	*out = *in
	out.TypeMeta = in.TypeMeta
	in.ListMeta.DeepCopyInto(&out.ListMeta)
	if in.Items != nil {
		in2, out2 := &in.Items, &out.Items
		*out2 = make([]BoriRevision, len(*in2))
		for i := range *in2 {
			(*in2)[i].DeepCopyInto(&(*out2)[i])
		}
	}
}
