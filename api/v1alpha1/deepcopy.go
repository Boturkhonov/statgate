package v1alpha1

// DeepCopyInto copies the receiver into out. in must be non-nil.
func (in *CanaryRolloutSpec) DeepCopyInto(out *CanaryRolloutSpec) {
	*out = *in
	if in.Steps != nil {
		in, out := &in.Steps, &out.Steps
		*out = make([]CanaryStep, len(*in))
		copy(*out, *in)
	}
}

// DeepCopy creates a new CanaryRolloutSpec.
func (in *CanaryRolloutSpec) DeepCopy() *CanaryRolloutSpec {
	if in == nil {
		return nil
	}
	out := new(CanaryRolloutSpec)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyInto copies the receiver into out. in must be non-nil.
func (in *CanaryRolloutStatus) DeepCopyInto(out *CanaryRolloutStatus) {
	*out = *in
	in.LastTransitionTime.DeepCopyInto(&out.LastTransitionTime)
}

// DeepCopy creates a new CanaryRolloutStatus.
func (in *CanaryRolloutStatus) DeepCopy() *CanaryRolloutStatus {
	if in == nil {
		return nil
	}
	out := new(CanaryRolloutStatus)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyInto copies the receiver into out. in must be non-nil.
func (in *CanaryStep) DeepCopyInto(out *CanaryStep) {
	*out = *in
}

// DeepCopy creates a new CanaryStep.
func (in *CanaryStep) DeepCopy() *CanaryStep {
	if in == nil {
		return nil
	}
	out := new(CanaryStep)
	in.DeepCopyInto(out)
	return out
}
