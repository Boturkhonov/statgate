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

// MetricCheck defines a single Prometheus-based health gate evaluated before
// advancing past a rollout step.
type MetricCheck struct {
	// Name is a human-readable label for this check (used in status messages).
	Name string `json:"name"`
	// Query is a PromQL instant query evaluated against PrometheusURL.
	Query string `json:"query"`
	// Threshold is the numeric value to compare the query result against.
	Threshold float64 `json:"threshold"`
	// Operator is the comparison operator used to evaluate the check.
	// The check passes when: queryResult <Operator> Threshold.
	// Allowed values: >, <, >=, <=, ==, !=
	// +kubebuilder:validation:Pattern=`^(>|<|>=|<=|==|!=)$`
	Operator string `json:"operator"`
	// Interval is informational — embed it directly in the Query string
	// (e.g. rate(metric[1m])). Stored here for documentation purposes only.
	// +optional
	Interval string `json:"interval,omitempty"`
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
	// PrometheusURL is the base URL of the Prometheus instance to query for
	// metric checks. If empty, all Metrics checks are skipped.
	// +optional
	PrometheusURL string `json:"prometheusURL,omitempty"`
	// Metrics is an optional list of Prometheus health gates evaluated after
	// each step's pause period before advancing. Any failure triggers auto-abort.
	// +optional
	Metrics []MetricCheck `json:"metrics,omitempty"`
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
