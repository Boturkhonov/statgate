package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// CanaryStep defines a single step in the rollout progression.
type CanaryStep struct {
	// Weight is the percentage of traffic to send to the canary (0-100).
	Weight int32 `json:"weight"`
	// PauseSeconds is how long to wait at this step before advancing.
	PauseSeconds int32 `json:"pauseSeconds"`
}

// SPRTMetric defines a single SPRT health gate using the Bernoulli model.
// Four PromQL queries supply the raw counters needed to compute the
// log-likelihood ratio.
type SPRTMetric struct {
	// Name is a human-readable label for this metric (used in status messages).
	Name string `json:"name"`
	// CanaryTotalQuery is a PromQL query returning the cumulative total
	// request counter for the canary version (must be monotonically increasing).
	CanaryTotalQuery string `json:"canaryTotalQuery"`
	// CanaryFailureQuery is a PromQL query returning the cumulative failure
	// counter for the canary version (must be monotonically increasing).
	CanaryFailureQuery string `json:"canaryFailureQuery"`
	// StableTotalQuery is a PromQL query returning the cumulative total
	// request counter for the stable version.
	StableTotalQuery string `json:"stableTotalQuery"`
	// StableFailureQuery is a PromQL query returning the cumulative failure
	// counter for the stable version.
	StableFailureQuery string `json:"stableFailureQuery"`
	// Delta is the minimum detectable effect size (percentage points).
	// The alternative hypothesis is p1 = p0 + delta.
	// Example: 0.05 detects a 5-percentage-point increase in error rate.
	Delta float64 `json:"delta"`
}

// SPRTAnalysis configures Sequential Probability Ratio Test analysis
// for canary health evaluation.
type SPRTAnalysis struct {
	// Alpha is the maximum probability of a false rollback (Type I error).
	// +kubebuilder:validation:Minimum=0
	// +kubebuilder:validation:Maximum=1
	Alpha float64 `json:"alpha"`
	// Beta is the maximum probability of missing a real degradation (Type II error).
	// +kubebuilder:validation:Minimum=0
	// +kubebuilder:validation:Maximum=1
	Beta float64 `json:"beta"`
	// AnalysisIntervalSeconds controls how frequently the SPRT runs during
	// the pause window. Defaults to 10.
	// +optional
	AnalysisIntervalSeconds int32 `json:"analysisIntervalSeconds,omitempty"`
	// Metrics is the list of SPRT health gates.
	// +kubebuilder:validation:MinItems=1
	Metrics []SPRTMetric `json:"metrics"`
}

// SPRTMetricState stores the accumulated SPRT state for a single metric
// across reconcile cycles. Persisted in CanaryRolloutStatus so that the
// test survives controller restarts.
type SPRTMetricState struct {
	// Name matches the SPRTMetric.Name this state corresponds to.
	Name string `json:"name"`
	// LogLikelihood is the accumulated log-likelihood ratio (Λ).
	LogLikelihood float64 `json:"logLikelihood"`
	// Observations is the total number of canary requests observed so far.
	Observations int64 `json:"observations"`
	// Failures is the total number of canary failures observed so far.
	Failures int64 `json:"failures"`
	// BaselineRate is the most recently computed p0 from stable data.
	BaselineRate float64 `json:"baselineRate"`
	// LastCanaryTotal is the last-seen value of the canary total counter.
	LastCanaryTotal float64 `json:"lastCanaryTotal"`
	// LastCanaryFailure is the last-seen value of the canary failure counter.
	LastCanaryFailure float64 `json:"lastCanaryFailure"`
	// Decision is the current per-metric verdict: "pending", "promote", or "rollback".
	Decision string `json:"decision"`
}

// CanaryRolloutSpec defines the desired state of a CanaryRollout.
type CanaryRolloutSpec struct {
	// TargetRef is the name of the canary Deployment (same namespace).
	TargetRef string `json:"targetRef"`
	// StableServiceRef is the name of the Service routing to the stable pods.
	StableServiceRef string `json:"stableServiceRef"`
	// CanaryServiceRef is the name of the Service routing to the canary pods.
	CanaryServiceRef string `json:"canaryServiceRef"`
	// VirtualServiceRef is the name of the Istio VirtualService to patch.
	VirtualServiceRef string `json:"virtualServiceRef"`
	// Steps defines the ordered list of weight/pause steps for the rollout.
	// +kubebuilder:validation:MinItems=1
	Steps []CanaryStep `json:"steps"`
	// Paused halts the rollout progression when set to true.
	// +optional
	Paused bool `json:"paused,omitempty"`
	// Abort triggers an immediate rollback to 100% stable traffic.
	// +optional
	Abort bool `json:"abort,omitempty"`
	// PrometheusURL is the base URL of the Prometheus instance to query.
	// If empty, SPRT analysis is skipped.
	// +optional
	PrometheusURL string `json:"prometheusURL,omitempty"`
	// Analysis configures SPRT-based statistical analysis of canary metrics.
	// If nil, no metric analysis is performed.
	// +optional
	Analysis *SPRTAnalysis `json:"analysis,omitempty"`
}

// RolloutPhase describes the current phase of the rollout.
// +kubebuilder:validation:Enum=Pending;Running;Paused;Promoted;Aborted;Failed
type RolloutPhase string

const (
	PhasePending  RolloutPhase = "Pending"
	PhaseRunning  RolloutPhase = "Running"
	PhasePaused   RolloutPhase = "Paused"
	PhasePromoted RolloutPhase = "Promoted"
	PhaseAborted  RolloutPhase = "Aborted"
	PhaseFailed   RolloutPhase = "Failed"
)

// CanaryRolloutStatus defines the observed state of a CanaryRollout.
type CanaryRolloutStatus struct {
	// Phase is the current phase of the rollout.
	// +optional
	Phase RolloutPhase `json:"phase,omitempty"`
	// CurrentStep is the zero-based index into spec.steps[].
	// +optional
	CurrentStep int32 `json:"currentStep,omitempty"`
	// CurrentWeight is the current canary traffic weight percentage.
	// +optional
	CurrentWeight int32 `json:"currentWeight,omitempty"`
	// LastTransitionTime is the timestamp of the last step or phase change.
	// +optional
	LastTransitionTime metav1.Time `json:"lastTransitionTime,omitempty"`
	// Message is a human-readable description of the current state.
	// +optional
	Message string `json:"message,omitempty"`
	// AnalysisState holds the per-metric SPRT accumulator state.
	// Reset to nil when advancing to the next step.
	// +optional
	AnalysisState []SPRTMetricState `json:"analysisState,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="Phase",type=string,JSONPath=`.status.phase`
// +kubebuilder:printcolumn:name="Weight",type=integer,JSONPath=`.status.currentWeight`
// +kubebuilder:printcolumn:name="Step",type=integer,JSONPath=`.status.currentStep`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// CanaryRollout is the Schema for the canaryrollouts API.
type CanaryRollout struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   CanaryRolloutSpec   `json:"spec,omitempty"`
	Status CanaryRolloutStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// CanaryRolloutList contains a list of CanaryRollout.
type CanaryRolloutList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []CanaryRollout `json:"items"`
}

func init() {
	SchemeBuilder.Register(&CanaryRollout{}, &CanaryRolloutList{})
}
