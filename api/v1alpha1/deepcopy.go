package v1alpha1

// DeepCopyInto copies the receiver into out. in must be non-nil.
func (in *SPRTMetric) DeepCopyInto(out *SPRTMetric) {
	*out = *in
}

// DeepCopy creates a new SPRTMetric.
func (in *SPRTMetric) DeepCopy() *SPRTMetric {
	if in == nil {
		return nil
	}
	out := new(SPRTMetric)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyInto copies the receiver into out. in must be non-nil.
func (in *SPRTAnalysis) DeepCopyInto(out *SPRTAnalysis) {
	*out = *in
	if in.Metrics != nil {
		in, out := &in.Metrics, &out.Metrics
		*out = make([]SPRTMetric, len(*in))
		copy(*out, *in)
	}
}

// DeepCopy creates a new SPRTAnalysis.
func (in *SPRTAnalysis) DeepCopy() *SPRTAnalysis {
	if in == nil {
		return nil
	}
	out := new(SPRTAnalysis)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyInto copies the receiver into out. in must be non-nil.
func (in *SPRTMetricState) DeepCopyInto(out *SPRTMetricState) {
	*out = *in
}

// DeepCopy creates a new SPRTMetricState.
func (in *SPRTMetricState) DeepCopy() *SPRTMetricState {
	if in == nil {
		return nil
	}
	out := new(SPRTMetricState)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyInto copies the receiver into out. in must be non-nil.
func (in *CanaryRolloutSpec) DeepCopyInto(out *CanaryRolloutSpec) {
	*out = *in
	if in.Steps != nil {
		in, out := &in.Steps, &out.Steps
		*out = make([]CanaryStep, len(*in))
		copy(*out, *in)
	}
	if in.Analysis != nil {
		in, out := &in.Analysis, &out.Analysis
		*out = new(SPRTAnalysis)
		(*in).DeepCopyInto(*out)
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
	if in.AnalysisState != nil {
		in, out := &in.AnalysisState, &out.AnalysisState
		*out = make([]SPRTMetricState, len(*in))
		copy(*out, *in)
	}
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
