package main

import (
	"bytes"
	"math"
	"strings"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	statgatev1alpha1 "github.com/boturkhonov/statgate/api/v1alpha1"
)

func TestHumanDuration(t *testing.T) {
	tests := []struct {
		d    time.Duration
		want string
	}{
		{0, "0s"},
		{5 * time.Second, "5s"},
		{59 * time.Second, "59s"},
		{time.Minute, "1m0s"},
		{75 * time.Second, "1m15s"},
		{time.Hour, "1h0m"},
		{90 * time.Minute, "1h30m"},
		{26 * time.Hour, "1d"},
		{-5 * time.Second, "0s"}, // negative input clamped to zero
	}
	for _, tc := range tests {
		if got := humanDuration(tc.d); got != tc.want {
			t.Errorf("humanDuration(%v) = %q, want %q", tc.d, got, tc.want)
		}
	}
}

func TestTruncate(t *testing.T) {
	tests := []struct {
		s    string
		max  int
		want string
	}{
		{"hello", 10, "hello"},      // shorter than limit — unchanged
		{"hello", 5, "hello"},       // exactly the limit — unchanged
		{"hello world", 5, "hell…"}, // over limit — ellipsis at end
		{"", 5, ""},                 // empty input
		{"abc", 0, ""},              // zero limit — empty
		{"abc", 1, "a"},             // tiny limit — no room for ellipsis
		{"привет мир", 5, "прив…"},  // multi-byte runes counted as 1 each
	}
	for _, tc := range tests {
		if got := truncate(tc.s, tc.max); got != tc.want {
			t.Errorf("truncate(%q, %d) = %q, want %q", tc.s, tc.max, got, tc.want)
		}
	}
}

func TestDecisionBadge(t *testing.T) {
	tests := []struct {
		in, want string
	}{
		{"promote", "✔ promote"},
		{"rollback", "✗ rollback"},
		{"pending", "… pending"},
		{"", "… pending"},        // empty falls through to default
		{"unknown", "… pending"}, // unrecognized values don't crash
	}
	for _, tc := range tests {
		if got := decisionBadge(tc.in); got != tc.want {
			t.Errorf("decisionBadge(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// llrBar has several moving parts: clamping out-of-range Λ, placing the
// current-value marker, placing the zero marker when the range straddles 0,
// and drawing the B/A edge bars. These assertions pin down each piece
// without resorting to a brittle full-string comparison.
func TestLlrBar(t *testing.T) {
	A := math.Log(0.95 / 0.05)
	B := -A

	// Non-zero value so that current marker (●) and zero marker (┼) land
	// in different cells.
	bar := llrBar(1.0, B, A)

	for _, want := range []string{"●", "┼", "│", "promote", "rollback"} {
		if !strings.Contains(bar, want) {
			t.Errorf("llrBar(1.0, B, A) missing %q\nbar: %s", want, bar)
		}
	}

	// Clamping: values outside [B, A] should not panic and should still
	// render a marker (at the relevant edge).
	for _, extreme := range []float64{1e9, -1e9, A + 1, B - 1} {
		out := llrBar(extreme, B, A)
		if !strings.Contains(out, "●") {
			t.Errorf("llrBar(%v, B, A) missing current-value marker", extreme)
		}
	}

	// Degenerate range: lo==hi must not divide-by-zero.
	out := llrBar(0, 0, 0)
	if !strings.Contains(out, "●") {
		t.Errorf("llrBar with zero range missing marker: %s", out)
	}
}

// sampleRollout builds a representative CanaryRollout object used by the
// render tests below. Kept as a helper so the individual tests stay focused
// on assertions rather than fixture boilerplate.
func sampleRollout(now time.Time) *statgatev1alpha1.CanaryRollout {
	cr := &statgatev1alpha1.CanaryRollout{}
	cr.Name = "demo-rollout"
	cr.Namespace = "statgate-demo"
	cr.Spec.TargetRef = "demo-canary"
	cr.Spec.StableServiceRef = "demo-stable"
	cr.Spec.CanaryServiceRef = "demo-canary"
	cr.Spec.VirtualServiceRef = "demo-vs"
	cr.Spec.PrometheusURL = "http://prometheus.example:9090"
	cr.Spec.Steps = []statgatev1alpha1.CanaryStep{
		{Weight: 10, PauseSeconds: 30},
		{Weight: 50, PauseSeconds: 60},
		{Weight: 100, PauseSeconds: 0},
	}
	cr.Spec.Analysis = &statgatev1alpha1.SPRTAnalysis{
		Alpha:                   0.05,
		Beta:                    0.05,
		AnalysisIntervalSeconds: 10,
		Metrics: []statgatev1alpha1.SPRTMetric{
			{Name: "error-rate", Delta: 0.05},
		},
	}
	cr.Status.Phase = statgatev1alpha1.PhaseRunning
	cr.Status.CurrentStep = 1
	cr.Status.CurrentWeight = 50
	cr.Status.Message = "SPRT: collecting evidence"
	cr.Status.LastTransitionTime = metav1.NewTime(now.Add(-45 * time.Second))
	cr.Status.AnalysisState = []statgatev1alpha1.SPRTMetricState{
		{
			Name:          "error-rate",
			LogLikelihood: -1.5,
			Observations:  500,
			Failures:      3,
			BaselineRate:  0.01,
			Decision:      "pending",
		},
	}
	return cr
}

// TestRenderRollout_FullDashboard covers the happy-path render: phase, SPRT
// config, per-metric state, step progress, and age formatting. Each
// assertion targets a single line / label so a breakage points at what
// regressed.
func TestRenderRollout_FullDashboard(t *testing.T) {
	now := time.Date(2026, 4, 14, 14, 0, 0, 0, time.UTC)
	cr := sampleRollout(now)

	var buf bytes.Buffer
	renderRollout(&buf, cr, now)
	out := buf.String()

	wants := []string{
		"statgate-demo/demo-rollout", // header
		"Running",                    // phase
		"50%",                        // weight
		"2 / 3",                      // step N/total
		"SPRT: collecting evidence",  // status message
		"α (false rollback)",         // SPRT header
		"β (missed failure)",
		"A (reject H0)",
		"B (accept H0)",
		"analysis interval   = 10s",
		"error-rate",         // metric name
		"… pending",          // decision badge
		"Λ (log-likelihood)", // metric math labels
		"baseline p0",
		"alternative p1=p0+Δ",
		"canary observed:       500 requests, 3 failures",
		"[✔] step 0", // completed step marker
		"[▶] step 1", // current step marker
		"[ ] step 2", // upcoming step marker
		"← current",  // arrow for current
		"45s ago",    // age formatting
	}
	for _, w := range wants {
		if !strings.Contains(out, w) {
			t.Errorf("rendered output missing %q\n--- full output ---\n%s", w, out)
		}
	}
}

func TestRenderRollout_NoAnalysis(t *testing.T) {
	cr := &statgatev1alpha1.CanaryRollout{}
	cr.Name = "demo"
	cr.Namespace = "default"
	cr.Spec.Steps = []statgatev1alpha1.CanaryStep{{Weight: 100, PauseSeconds: 0}}
	cr.Status.Phase = statgatev1alpha1.PhasePending

	var buf bytes.Buffer
	renderRollout(&buf, cr, time.Now())
	out := buf.String()

	if !strings.Contains(out, "SPRT analysis: not configured") {
		t.Errorf("missing 'not configured' notice\n%s", out)
	}
	// The SPRT config block must be absent when analysis is nil.
	if strings.Contains(out, "α (false rollback)") {
		t.Errorf("SPRT config rendered despite nil analysis\n%s", out)
	}
}

// When the rollout has reached Promoted, every step should be marked done
// and the arrow marker should disappear (nothing is "current").
func TestRenderRollout_PromotedMarksAllStepsDone(t *testing.T) {
	now := time.Now()
	cr := sampleRollout(now)
	cr.Status.Phase = statgatev1alpha1.PhasePromoted
	cr.Status.CurrentStep = 2
	cr.Status.CurrentWeight = 100

	var buf bytes.Buffer
	renderRollout(&buf, cr, now)
	out := buf.String()

	if strings.Contains(out, "[▶]") {
		t.Errorf("promoted rollout should not show current-step arrow\n%s", out)
	}
	if strings.Contains(out, "← current") {
		t.Errorf("promoted rollout should not show 'current' marker\n%s", out)
	}
	if strings.Count(out, "[✔]") != len(cr.Spec.Steps) {
		t.Errorf("expected all %d steps marked done, got:\n%s", len(cr.Spec.Steps), out)
	}
}
