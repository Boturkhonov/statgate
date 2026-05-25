package controller

import (
	"github.com/prometheus/client_golang/prometheus"
	"sigs.k8s.io/controller-runtime/pkg/metrics"

	statgatev1alpha1 "github.com/boturkhonov/statgate/api/v1alpha1"
)

// Common labels for per-rollout, per-metric series.
var rolloutMetricLabels = []string{"namespace", "rollout", "metric"}

// Per-rollout labels (no SPRT metric dimension).
var rolloutLabels = []string{"namespace", "rollout"}

var (
	sprtLogLikelihood = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "statgate_sprt_log_likelihood",
		Help: "Current accumulated SPRT log-likelihood ratio Λ for an analyzed metric.",
	}, rolloutMetricLabels)

	sprtBoundaryA = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "statgate_sprt_boundary_a",
		Help: "Wald upper boundary A = ln((1-β)/α). Λ ≥ A → rollback.",
	}, rolloutLabels)

	sprtBoundaryB = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "statgate_sprt_boundary_b",
		Help: "Wald lower boundary B = ln(β/(1-α)). Λ ≤ B → promote.",
	}, rolloutLabels)

	sprtObservations = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "statgate_sprt_observations",
		Help: "Total canary requests accumulated by the SPRT analyser for a metric (since the current step started).",
	}, rolloutMetricLabels)

	sprtFailures = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "statgate_sprt_failures",
		Help: "Canary failures accumulated by the SPRT analyser for a metric (since the current step started).",
	}, rolloutMetricLabels)

	canaryWeight = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "statgate_canary_weight",
		Help: "Current percentage of traffic routed to the canary version (0..100) as set in the Istio VirtualService.",
	}, rolloutLabels)

	rolloutPhase = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "statgate_rollout_phase",
		Help: "Numeric encoding of the CanaryRollout phase: 0=Pending, 1=Running, 2=Paused, 3=Promoted, 4=Aborted, 5=Failed.",
	}, rolloutLabels)
)

func init() {
	metrics.Registry.MustRegister(
		sprtLogLikelihood,
		sprtBoundaryA,
		sprtBoundaryB,
		sprtObservations,
		sprtFailures,
		canaryWeight,
		rolloutPhase,
	)
}

// recordSPRTBoundaries publishes Wald's A and B for the rollout.
func recordSPRTBoundaries(namespace, rollout string, upper, lower float64) {
	sprtBoundaryA.WithLabelValues(namespace, rollout).Set(upper)
	sprtBoundaryB.WithLabelValues(namespace, rollout).Set(lower)
}

// recordSPRTMetricState publishes Λ, observations and failures for a single
// analyzed metric of a rollout.
func recordSPRTMetricState(namespace, rollout string, state statgatev1alpha1.SPRTMetricState) {
	sprtLogLikelihood.WithLabelValues(namespace, rollout, state.Name).Set(state.LogLikelihood)
	sprtObservations.WithLabelValues(namespace, rollout, state.Name).Set(float64(state.Observations))
	sprtFailures.WithLabelValues(namespace, rollout, state.Name).Set(float64(state.Failures))
}

// recordCanaryWeight publishes the current canary traffic share (0..100).
func recordCanaryWeight(namespace, rollout string, weight int32) {
	canaryWeight.WithLabelValues(namespace, rollout).Set(float64(weight))
}

// recordRolloutPhase publishes the numeric encoding of the rollout phase.
// Unknown phases are not published — the previous value remains.
func recordRolloutPhase(namespace, rollout string, phase statgatev1alpha1.RolloutPhase) {
	code, ok := phaseCode(phase)
	if !ok {
		return
	}
	rolloutPhase.WithLabelValues(namespace, rollout).Set(float64(code))
}

func phaseCode(phase statgatev1alpha1.RolloutPhase) (int, bool) {
	switch phase {
	case statgatev1alpha1.PhasePending:
		return 0, true
	case statgatev1alpha1.PhaseRunning:
		return 1, true
	case statgatev1alpha1.PhasePaused:
		return 2, true
	case statgatev1alpha1.PhasePromoted:
		return 3, true
	case statgatev1alpha1.PhaseAborted:
		return 4, true
	case statgatev1alpha1.PhaseFailed:
		return 5, true
	default:
		return 0, false
	}
}

// resetSPRTMetricSeries drops Λ/observations/failures gauges for a metric.
// Used when advancing to the next step (SPRT state is reset).
func resetSPRTMetricSeries(namespace, rollout, metricName string) {
	sprtLogLikelihood.DeleteLabelValues(namespace, rollout, metricName)
	sprtObservations.DeleteLabelValues(namespace, rollout, metricName)
	sprtFailures.DeleteLabelValues(namespace, rollout, metricName)
}
