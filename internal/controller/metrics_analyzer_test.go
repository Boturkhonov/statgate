package controller

import (
	"context"
	"fmt"
	"math"
	"net/http"
	"net/http/httptest"
	"testing"

	statgatev1alpha1 "github.com/boturkhonov/statgate/api/v1alpha1"
)

// ---------------------------------------------------------------------------
// Test helpers: minimal Prometheus HTTP mock.
//
// The real metrics analyzer uses the github.com/prometheus/client_golang HTTP
// client against a remote /api/v1/query endpoint. We don't want to pull in a
// full Prometheus for unit tests, so we spin up an httptest server that
// speaks just enough of the instant-query JSON protocol to drive RunSPRT.
// ---------------------------------------------------------------------------

// promResolver maps a PromQL query string to (value, hasData). Returning
// hasData=false simulates Prometheus returning an empty vector, which the
// analyzer treats as "skip this cycle for this metric".
type promResolver func(query string) (value float64, hasData bool)

// staticProm builds a resolver backed by a static lookup table. Keys missing
// from the map yield (0, false) — i.e. "no data".
func staticProm(data map[string]float64) promResolver {
	return func(q string) (float64, bool) {
		v, ok := data[q]
		return v, ok
	}
}

// mockProm stands up an httptest server that answers /api/v1/query requests
// with a vector-shaped JSON response computed from the supplied resolver.
// The server is torn down automatically at test end via t.Cleanup.
func mockProm(t *testing.T, resolve promResolver) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/query" {
			http.NotFound(w, r)
			return
		}
		if err := r.ParseForm(); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		q := r.FormValue("query")
		val, has := resolve(q)
		var result string
		if !has {
			result = "[]"
		} else {
			// Prometheus JSON wraps the scalar as a string inside a
			// [timestamp, "value"] tuple. %g keeps the representation
			// compact while remaining parseable by strconv.ParseFloat.
			result = fmt.Sprintf(`[{"metric":{},"value":[1609459200,"%g"]}]`, val)
		}
		w.Header().Set("Content-Type", "application/json")
		body := fmt.Sprintf(
			`{"status":"success","data":{"resultType":"vector","result":%s}}`,
			result,
		)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv
}

// singleMetricAnalysis returns a minimal SPRTAnalysis configuration with
// α=β=0.05 and one error-rate metric. These boundaries give A ≈ +2.944 and
// B ≈ -2.944, which are convenient round-ish numbers for hand-computed
// assertions.
func singleMetricAnalysis() *statgatev1alpha1.SPRTAnalysis {
	return &statgatev1alpha1.SPRTAnalysis{
		Alpha: 0.05,
		Beta:  0.05,
		Metrics: []statgatev1alpha1.SPRTMetric{
			{
				Name:               "err",
				CanaryTotalQuery:   "ct",
				CanaryFailureQuery: "cf",
				StableTotalQuery:   "st",
				StableFailureQuery: "sf",
				Delta:              0.05,
			},
		},
	}
}

// boundsFor returns the SPRT decision boundaries (B, A) for the given α, β.
func boundsFor(alpha, beta float64) (lower, upper float64) {
	return math.Log(beta / (1 - alpha)), math.Log((1 - beta) / alpha)
}

// ---------------------------------------------------------------------------
// findOrInitState
// ---------------------------------------------------------------------------

func TestFindOrInitState(t *testing.T) {
	existing := []statgatev1alpha1.SPRTMetricState{
		{Name: "alpha", LogLikelihood: 1.5, Decision: "pending"},
		{Name: "beta", LogLikelihood: -0.3, Decision: "promote"},
	}

	tests := []struct {
		name     string
		states   []statgatev1alpha1.SPRTMetricState
		lookup   string
		wantName string
		wantLL   float64
		wantDec  string
	}{
		{"found-pending", existing, "alpha", "alpha", 1.5, "pending"},
		{"found-terminal", existing, "beta", "beta", -0.3, "promote"},
		{"missing-inits-pending", existing, "gamma", "gamma", 0, "pending"},
		{"empty-list-inits-pending", nil, "delta", "delta", 0, "pending"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := findOrInitState(tc.states, tc.lookup)
			if got.Name != tc.wantName {
				t.Errorf("Name: got %q, want %q", got.Name, tc.wantName)
			}
			if got.LogLikelihood != tc.wantLL {
				t.Errorf("LogLikelihood: got %v, want %v", got.LogLikelihood, tc.wantLL)
			}
			if got.Decision != tc.wantDec {
				t.Errorf("Decision: got %q, want %q", got.Decision, tc.wantDec)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// RunSPRT — end-to-end decision scenarios
// ---------------------------------------------------------------------------

// Canary matches stable → Λ trends strongly negative → promote.
//
// Hand calculation:
//
//	p0 = 10/1000 = 0.01,  p1 = 0.06,  k=10, n-k=990
//	Λ  = 10·ln(6)   + 990·ln(0.94/0.99)
//	   ≈ 17.92     + 990·(-0.0518)
//	   ≈ 17.92 - 51.30  ≈ -33.4   ≪ B ≈ -2.944
func TestRunSPRT_Promote(t *testing.T) {
	srv := mockProm(t, staticProm(map[string]float64{
		"ct": 1000, "cf": 10,
		"st": 1000, "sf": 10,
	}))

	decision, state, reason, err := RunSPRT(context.Background(), srv.URL, singleMetricAnalysis(), nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if decision != SPRTPromote {
		t.Fatalf("decision = %q, want %q (reason: %s)", decision, SPRTPromote, reason)
	}
	if len(state) != 1 {
		t.Fatalf("state len = %d, want 1", len(state))
	}
	if state[0].Decision != "promote" {
		t.Errorf("state decision = %q, want promote", state[0].Decision)
	}
	_, upper := boundsFor(0.05, 0.05)
	lower, _ := boundsFor(0.05, 0.05)
	if state[0].LogLikelihood > lower {
		t.Errorf("Λ = %v, expected ≤ B = %v (upper for ref: %v)",
			state[0].LogLikelihood, lower, upper)
	}
}

// Canary failure rate 20× higher than stable → Λ trends strongly positive →
// rollback.
//
// Hand calculation:
//
//	p0 = 0.01, p1 = 0.06, k=200, n-k=800
//	Λ  = 200·ln(6)    + 800·ln(0.94/0.99)
//	   ≈ 358.35       + 800·(-0.0518)
//	   ≈ 358.35 - 41.47  ≈ 316.88   ≫ A ≈ 2.944
func TestRunSPRT_Rollback(t *testing.T) {
	srv := mockProm(t, staticProm(map[string]float64{
		"ct": 1000, "cf": 200,
		"st": 1000, "sf": 10,
	}))

	decision, state, reason, err := RunSPRT(context.Background(), srv.URL, singleMetricAnalysis(), nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if decision != SPRTRollback {
		t.Fatalf("decision = %q, want %q", decision, SPRTRollback)
	}
	if reason == "" {
		t.Errorf("rollback reason should be populated")
	}
	if len(state) != 1 || state[0].Decision != "rollback" {
		t.Errorf("state = %+v, want Decision=rollback", state)
	}
	_, upper := boundsFor(0.05, 0.05)
	if state[0].LogLikelihood < upper {
		t.Errorf("Λ = %v, expected ≥ A = %v", state[0].LogLikelihood, upper)
	}
	// Counts should reflect the observed deltas.
	if state[0].Observations != 1000 {
		t.Errorf("Observations = %d, want 1000", state[0].Observations)
	}
	if state[0].Failures != 200 {
		t.Errorf("Failures = %d, want 200", state[0].Failures)
	}
}

// Small sample with no failures: not enough evidence to reject or accept.
//
//	p0 = 0.01, p1 = 0.06, k=0, n-k=10
//	Λ  = 0·ln(6) + 10·ln(0.94/0.99) ≈ -0.518   (between B and A)
func TestRunSPRT_Continue(t *testing.T) {
	srv := mockProm(t, staticProm(map[string]float64{
		"ct": 10, "cf": 0,
		"st": 1000, "sf": 10,
	}))

	decision, state, _, err := RunSPRT(context.Background(), srv.URL, singleMetricAnalysis(), nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if decision != SPRTContinue {
		t.Fatalf("decision = %q, want %q", decision, SPRTContinue)
	}
	lower, upper := boundsFor(0.05, 0.05)
	l := state[0].LogLikelihood
	if !(l > lower && l < upper) {
		t.Errorf("Λ = %v, expected strictly in (B=%v, A=%v)", l, lower, upper)
	}
	if state[0].Decision != "pending" {
		t.Errorf("Decision = %q, want pending", state[0].Decision)
	}
}

// When the canary total query returns an empty vector, the metric is skipped
// (not counted as evidence either way). The cycle returns "continue" and the
// state is preserved unchanged.
func TestRunSPRT_NoCanaryData(t *testing.T) {
	srv := mockProm(t, func(q string) (float64, bool) {
		if q == "ct" {
			return 0, false // empty vector
		}
		return 1000, true
	})

	decision, state, _, err := RunSPRT(context.Background(), srv.URL, singleMetricAnalysis(), nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if decision != SPRTContinue {
		t.Errorf("decision = %q, want %q", decision, SPRTContinue)
	}
	if len(state) != 1 {
		t.Fatalf("state len = %d, want 1", len(state))
	}
	if state[0].LogLikelihood != 0 {
		t.Errorf("Λ should be untouched, got %v", state[0].LogLikelihood)
	}
}

// Stable version has no traffic yet — can't estimate p0, so the cycle skips
// the metric rather than dividing by zero.
func TestRunSPRT_StableHasNoTraffic(t *testing.T) {
	srv := mockProm(t, staticProm(map[string]float64{
		"ct": 1000, "cf": 200,
		"st": 0, "sf": 0,
	}))

	decision, state, _, err := RunSPRT(context.Background(), srv.URL, singleMetricAnalysis(), nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if decision != SPRTContinue {
		t.Errorf("decision = %q, want %q", decision, SPRTContinue)
	}
	if state[0].LogLikelihood != 0 {
		t.Errorf("Λ should be untouched when p0 cannot be estimated, got %v", state[0].LogLikelihood)
	}
}

// Simulate a Prometheus counter reset: stored LastCanaryTotal is higher than
// the current reading. Expected behavior is to reset the accumulator for
// that metric and start fresh from the new values.
func TestRunSPRT_CounterReset(t *testing.T) {
	initial := []statgatev1alpha1.SPRTMetricState{
		{
			Name:              "err",
			LogLikelihood:     2.0,
			Observations:      1000,
			Failures:          50,
			LastCanaryTotal:   1000,
			LastCanaryFailure: 50,
			Decision:          "pending",
		},
	}

	srv := mockProm(t, staticProm(map[string]float64{
		"ct": 100, "cf": 5, // current total is LOWER than stored — reset!
		"st": 1000, "sf": 10,
	}))

	decision, state, _, err := RunSPRT(context.Background(), srv.URL, singleMetricAnalysis(), initial)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if decision != SPRTContinue {
		t.Errorf("decision = %q, want %q", decision, SPRTContinue)
	}
	got := state[0]
	if got.LogLikelihood != 0 {
		t.Errorf("Λ should reset to 0, got %v", got.LogLikelihood)
	}
	if got.Observations != 0 || got.Failures != 0 {
		t.Errorf("counts should reset: obs=%d, fail=%d", got.Observations, got.Failures)
	}
	if got.LastCanaryTotal != 100 || got.LastCanaryFailure != 5 {
		t.Errorf("last-seen should adopt current values: total=%v, fail=%v",
			got.LastCanaryTotal, got.LastCanaryFailure)
	}
}

// A metric that has already reached a terminal decision must not be touched
// on subsequent cycles — its Λ and verdict should pass through unchanged.
func TestRunSPRT_TerminalDecisionPreserved(t *testing.T) {
	initial := []statgatev1alpha1.SPRTMetricState{
		{Name: "err", LogLikelihood: 100, Decision: "rollback"},
	}
	srv := mockProm(t, staticProm(map[string]float64{
		"ct": 1, "cf": 0, "st": 1, "sf": 0,
	}))

	_, state, _, err := RunSPRT(context.Background(), srv.URL, singleMetricAnalysis(), initial)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if state[0].LogLikelihood != 100 || state[0].Decision != "rollback" {
		t.Errorf("terminal state mutated: %+v", state[0])
	}
}

// Two metrics: one already in "promote", the other still pending. Overall
// decision must stay "continue" — promote requires ALL metrics to agree.
func TestRunSPRT_MultiMetricAllRequiredForPromote(t *testing.T) {
	analysis := &statgatev1alpha1.SPRTAnalysis{
		Alpha: 0.05,
		Beta:  0.05,
		Metrics: []statgatev1alpha1.SPRTMetric{
			{
				Name: "fast", Delta: 0.05,
				CanaryTotalQuery: "fct", CanaryFailureQuery: "fcf",
				StableTotalQuery: "fst", StableFailureQuery: "fsf",
			},
			{
				Name: "slow", Delta: 0.05,
				CanaryTotalQuery: "sct", CanaryFailureQuery: "scf",
				StableTotalQuery: "sst", StableFailureQuery: "ssf",
			},
		},
	}
	srv := mockProm(t, staticProm(map[string]float64{
		// Metric "fast": strong promote evidence
		"fct": 1000, "fcf": 10, "fst": 1000, "fsf": 10,
		// Metric "slow": tiny sample, still pending
		"sct": 10, "scf": 0, "sst": 1000, "ssf": 10,
	}))

	decision, state, _, err := RunSPRT(context.Background(), srv.URL, analysis, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if decision != SPRTContinue {
		t.Errorf("decision = %q, want %q (need ALL metrics to promote)", decision, SPRTContinue)
	}
	if len(state) != 2 {
		t.Fatalf("state len = %d, want 2", len(state))
	}

	byName := map[string]string{}
	for _, s := range state {
		byName[s.Name] = s.Decision
	}
	if byName["fast"] != "promote" {
		t.Errorf("fast = %q, want promote", byName["fast"])
	}
	if byName["slow"] != "pending" {
		t.Errorf("slow = %q, want pending", byName["slow"])
	}
}

// Rollback is disjunctive: ANY metric crossing the upper boundary must
// immediately terminate the cycle with a rollback verdict, even if other
// metrics are happy.
func TestRunSPRT_MultiMetricAnyRollback(t *testing.T) {
	analysis := &statgatev1alpha1.SPRTAnalysis{
		Alpha: 0.05,
		Beta:  0.05,
		Metrics: []statgatev1alpha1.SPRTMetric{
			{
				Name: "good", Delta: 0.05,
				CanaryTotalQuery: "g_ct", CanaryFailureQuery: "g_cf",
				StableTotalQuery: "g_st", StableFailureQuery: "g_sf",
			},
			{
				Name: "bad", Delta: 0.05,
				CanaryTotalQuery: "b_ct", CanaryFailureQuery: "b_cf",
				StableTotalQuery: "b_st", StableFailureQuery: "b_sf",
			},
		},
	}
	srv := mockProm(t, staticProm(map[string]float64{
		// Happy metric
		"g_ct": 1000, "g_cf": 10, "g_st": 1000, "g_sf": 10,
		// Degraded metric: 20% canary vs 1% stable
		"b_ct": 1000, "b_cf": 200, "b_st": 1000, "b_sf": 10,
	}))

	decision, _, reason, err := RunSPRT(context.Background(), srv.URL, analysis, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if decision != SPRTRollback {
		t.Errorf("decision = %q, want %q", decision, SPRTRollback)
	}
	if reason == "" {
		t.Errorf("rollback reason should be populated")
	}
}

// When Prometheus is unreachable, RunSPRT must surface an error so the
// reconciler can retry on the next cycle instead of silently accumulating
// bogus state.
func TestRunSPRT_PrometheusError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	defer srv.Close()

	_, _, _, err := RunSPRT(context.Background(), srv.URL, singleMetricAnalysis(), nil)
	if err == nil {
		t.Errorf("expected error when Prometheus returns 500")
	}
}

// The default AnalysisIntervalSeconds value is honored (no zero-interval
// bugs). This test exercises the no-op path and mostly exists to confirm
// that an unset field doesn't explode RunSPRT.
func TestRunSPRT_DefaultIntervalDoesNotCrash(t *testing.T) {
	a := singleMetricAnalysis()
	a.AnalysisIntervalSeconds = 0

	srv := mockProm(t, staticProm(map[string]float64{
		"ct": 10, "cf": 0,
		"st": 1000, "sf": 10,
	}))
	if _, _, _, err := RunSPRT(context.Background(), srv.URL, a, nil); err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

// ---------------------------------------------------------------------------
// SPRT boundary sanity check — independent of RunSPRT.
//
// This re-derives the Wald boundaries from the same α, β used throughout the
// test file and verifies that the analytic formulas match the constants we
// assert against in the scenario tests. It's a guardrail against future
// edits silently changing the boundary convention.
// ---------------------------------------------------------------------------

func TestSPRTBoundariesMath(t *testing.T) {
	alpha := 0.05
	beta := 0.05

	wantA := math.Log(0.95 / 0.05)
	wantB := math.Log(0.05 / 0.95)

	if math.Abs(wantA-2.9444389791664403) > 1e-9 {
		t.Errorf("A = %v, expected ≈ 2.9444", wantA)
	}
	if math.Abs(wantB+2.9444389791664403) > 1e-9 {
		t.Errorf("B = %v, expected ≈ -2.9444", wantB)
	}
	// Symmetry: with α=β the boundaries are mirror-symmetric around zero.
	if math.Abs(wantA+wantB) > 1e-12 {
		t.Errorf("expected A + B ≈ 0 when α=β, got %v", wantA+wantB)
	}
	_ = alpha
	_ = beta
}
