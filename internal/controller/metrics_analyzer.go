package controller

import (
	"context"
	"fmt"
	"time"

	promapi "github.com/prometheus/client_golang/api"
	promv1 "github.com/prometheus/client_golang/api/prometheus/v1"
	"github.com/prometheus/common/model"

	statgatev1alpha1 "github.com/boturkhonov/statgate/api/v1alpha1"
)

// AnalyzeCanaryMetrics queries Prometheus for each MetricCheck and evaluates
// the threshold condition. It distinguishes between two failure modes:
//   - transport/parse error (err != nil): transient, caller should retry
//   - threshold violation (!passed, err == nil): caller should abort the rollout
//
// Returns (true, "", nil) when all checks pass or the list is empty.
func AnalyzeCanaryMetrics(
	ctx context.Context,
	prometheusURL string,
	checks []statgatev1alpha1.MetricCheck,
) (passed bool, failReason string, err error) {
	if len(checks) == 0 {
		return true, "", nil
	}

	client, err := promapi.NewClient(promapi.Config{Address: prometheusURL})
	if err != nil {
		return false, "", fmt.Errorf("create prometheus client: %w", err)
	}

	api := promv1.NewAPI(client)

	for _, check := range checks {
		value, err := queryScalar(ctx, api, check.Query)
		if err != nil {
			return false, "", fmt.Errorf("query %q: %w", check.Name, err)
		}

		if value == nil {
			// No data yet — Prometheus hasn't scraped enough samples.
			// Skip this check rather than aborting; the next reconcile will retry.
			continue
		}

		ok, err := compareThreshold(*value, check.Threshold, check.Operator)
		if err != nil {
			return false, "", fmt.Errorf("check %q: %w", check.Name, err)
		}
		if !ok {
			return false, fmt.Sprintf(
				"check %q failed: %.4f %s %.4f",
				check.Name, *value, check.Operator, check.Threshold,
			), nil
		}
	}

	return true, "", nil
}

// queryScalar executes an instant PromQL query and returns the first sample
// value as a float64. Returns nil if the result vector is empty.
// Only model.Vector results are accepted; any other type is an error.
func queryScalar(ctx context.Context, api promv1.API, query string) (*float64, error) {
	result, warnings, err := api.Query(ctx, query, time.Now())
	if err != nil {
		return nil, err
	}
	if len(warnings) > 0 {
		// Warnings are non-fatal; log them via the returned error only if needed.
		_ = warnings
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

// compareThreshold evaluates: value <op> threshold.
// Returns true when the condition holds (check passes).
func compareThreshold(value, threshold float64, op string) (bool, error) {
	switch op {
	case ">":
		return value > threshold, nil
	case "<":
		return value < threshold, nil
	case ">=":
		return value >= threshold, nil
	case "<=":
		return value <= threshold, nil
	case "==":
		return value == threshold, nil
	case "!=":
		return value != threshold, nil
	default:
		return false, fmt.Errorf("unknown operator %q", op)
	}
}
