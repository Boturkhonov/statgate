package controller

import (
	"context"
	"fmt"
	"math"
	"time"

	promapi "github.com/prometheus/client_golang/api"
	promv1 "github.com/prometheus/client_golang/api/prometheus/v1"
	"github.com/prometheus/common/model"

	statgatev1alpha1 "github.com/boturkhonov/statgate/api/v1alpha1"
)

const epsilon = 1e-10

// SPRTDecision represents the outcome of an SPRT analysis cycle.
type SPRTDecision string

const (
	SPRTContinue SPRTDecision = "continue"
	SPRTPromote  SPRTDecision = "promote"
	SPRTRollback SPRTDecision = "rollback"
)

// RunSPRT executes one cycle of the Sequential Probability Ratio Test for all
// configured metrics. It reads current Prometheus counter values, computes
// deltas since the last cycle, and updates the log-likelihood ratio.
//
// Decision logic:
//   - Any metric crosses the upper boundary → SPRTRollback (evidence of degradation)
//   - All metrics cross the lower boundary  → SPRTPromote  (evidence of no degradation)
//   - Otherwise                             → SPRTContinue (keep observing)
//
// The caller must persist the returned updatedState in the CR status.
func RunSPRT(
	ctx context.Context,
	prometheusURL string,
	analysis *statgatev1alpha1.SPRTAnalysis,
	currentState []statgatev1alpha1.SPRTMetricState,
) (decision SPRTDecision, updatedState []statgatev1alpha1.SPRTMetricState, reason string, err error) {
	client, err := promapi.NewClient(promapi.Config{Address: prometheusURL})
	if err != nil {
		return "", nil, "", fmt.Errorf("create prometheus client: %w", err)
	}
	api := promv1.NewAPI(client)

	// SPRT decision boundaries (Wald's sequential test).
	//   Upper bound A: reject H0 (canary is worse) → rollback
	//   Lower bound B: accept H0 (canary is fine)  → promote
	upperBound := math.Log((1 - analysis.Beta) / analysis.Alpha)
	lowerBound := math.Log(analysis.Beta / (1 - analysis.Alpha))

	// Build a working copy of the state slice.
	updatedState = make([]statgatev1alpha1.SPRTMetricState, 0, len(analysis.Metrics))
	for _, metric := range analysis.Metrics {
		state := findOrInitState(currentState, metric.Name)

		// Skip metrics that already reached a terminal decision.
		if state.Decision == "promote" || state.Decision == "rollback" {
			updatedState = append(updatedState, state)
			continue
		}

		// Query four counters from Prometheus.
		canaryTotal, err := queryScalar(ctx, api, metric.CanaryTotalQuery)
		if err != nil {
			return "", nil, "", fmt.Errorf("metric %q canaryTotalQuery: %w", metric.Name, err)
		}
		canaryFailure, err := queryScalar(ctx, api, metric.CanaryFailureQuery)
		if err != nil {
			return "", nil, "", fmt.Errorf("metric %q canaryFailureQuery: %w", metric.Name, err)
		}
		stableTotal, err := queryScalar(ctx, api, metric.StableTotalQuery)
		if err != nil {
			return "", nil, "", fmt.Errorf("metric %q stableTotalQuery: %w", metric.Name, err)
		}
		stableFailure, err := queryScalar(ctx, api, metric.StableFailureQuery)
		if err != nil {
			return "", nil, "", fmt.Errorf("metric %q stableFailureQuery: %w", metric.Name, err)
		}

		// If any counter is nil, skip this cycle (not enough data yet).
		if canaryTotal == nil || canaryFailure == nil || stableTotal == nil || stableFailure == nil {
			updatedState = append(updatedState, state)
			continue
		}

		// Skip if stable has no traffic yet — can't estimate p0.
		if *stableTotal == 0 {
			updatedState = append(updatedState, state)
			continue
		}

		// Compute baseline error rate p0 from stable and alternative p1 = p0 + delta.
		p0 := *stableFailure / *stableTotal
		if p0 < epsilon {
			p0 = epsilon
		}
		p1 := p0 + metric.Delta
		if p1 >= 1 {
			p1 = 1 - epsilon
		}

		// Compute deltas since last observation.
		newTotal := *canaryTotal - state.LastCanaryTotal
		newFailures := *canaryFailure - state.LastCanaryFailure

		// Detect Prometheus counter resets (negative deltas).
		if newTotal < 0 || newFailures < 0 {
			// Reset this metric's state and start accumulating from current values.
			state = statgatev1alpha1.SPRTMetricState{
				Name:              metric.Name,
				Decision:          "pending",
				LastCanaryTotal:   *canaryTotal,
				LastCanaryFailure: *canaryFailure,
				BaselineRate:      p0,
			}
			updatedState = append(updatedState, state)
			continue
		}

		// Update log-likelihood ratio with new observations.
		if newTotal > 0 {
			newSuccesses := newTotal - newFailures
			// Λ += k·ln(p1/p0) + (n-k)·ln((1-p1)/(1-p0))
			state.LogLikelihood += newFailures*math.Log(p1/p0) + newSuccesses*math.Log((1-p1)/(1-p0))
			state.Observations += int64(newTotal)
			state.Failures += int64(newFailures)
		}

		state.LastCanaryTotal = *canaryTotal
		state.LastCanaryFailure = *canaryFailure
		state.BaselineRate = p0

		// Check SPRT boundaries.
		if state.LogLikelihood >= upperBound {
			state.Decision = "rollback"
			updatedState = append(updatedState, state)
			return SPRTRollback, updatedState, fmt.Sprintf(
				"metric %q: evidence of degradation (Λ=%.4f ≥ A=%.4f, p0=%.4f, observed=%d, failures=%d)",
				metric.Name, state.LogLikelihood, upperBound, p0, state.Observations, state.Failures,
			), nil
		}
		if state.LogLikelihood <= lowerBound {
			state.Decision = "promote"
		}

		updatedState = append(updatedState, state)
	}

	// Promote only when ALL metrics have individually decided "promote".
	allPromote := len(updatedState) > 0
	for _, s := range updatedState {
		if s.Decision != "promote" {
			allPromote = false
			break
		}
	}
	if allPromote {
		return SPRTPromote, updatedState, "all metrics passed SPRT (Λ ≤ B for all)", nil
	}

	return SPRTContinue, updatedState, fmt.Sprintf(
		"SPRT: collecting evidence (%d metrics tracked)", len(updatedState),
	), nil
}

// findOrInitState looks up the existing state for a metric by name.
// If not found, returns a fresh zero-valued state with Decision="pending".
func findOrInitState(states []statgatev1alpha1.SPRTMetricState, name string) statgatev1alpha1.SPRTMetricState {
	for _, s := range states {
		if s.Name == name {
			return s
		}
	}
	return statgatev1alpha1.SPRTMetricState{Name: name, Decision: "pending"}
}

// queryScalar executes an instant PromQL query and returns the first sample
// value as a float64. Returns nil if the result vector is empty.
func queryScalar(ctx context.Context, api promv1.API, query string) (*float64, error) {
	result, _, err := api.Query(ctx, query, time.Now())
	if err != nil {
		return nil, err
	}

	vec, ok := result.(model.Vector)
	if !ok {
		return nil, fmt.Errorf("expected instant vector, got %T", result)
	}
	if len(vec) == 0 {
		return nil, nil
	}

	v := float64(vec[0].Value)
	return &v, nil
}
