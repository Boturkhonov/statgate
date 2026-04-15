package main

import (
	"fmt"
	"io"
	"math"
	"strings"
	"time"

	statgatev1alpha1 "github.com/boturkhonov/statgate/api/v1alpha1"
)

const hrule = "────────────────────────────────────────────────────────────────────────────────"

// renderRollout prints a full dashboard for a single CanaryRollout. It is
// shared between `get` (one-shot) and `watch` (redrawn on an interval).
func renderRollout(w io.Writer, cr *statgatev1alpha1.CanaryRollout, now time.Time) {
	ns := cr.Namespace
	if ns == "" {
		ns = "default"
	}

	fmt.Fprintf(w, "StatGate canary rollout  %s/%s    (as of %s)\n", ns, cr.Name, now.Format("15:04:05"))
	fmt.Fprintln(w, hrule)

	phase := string(cr.Status.Phase)
	if phase == "" {
		phase = "—"
	}
	step := "—"
	if len(cr.Spec.Steps) > 0 {
		step = fmt.Sprintf("%d / %d", int(cr.Status.CurrentStep)+1, len(cr.Spec.Steps))
	}
	lastChange := "—"
	if !cr.Status.LastTransitionTime.IsZero() {
		lastChange = humanDuration(now.Sub(cr.Status.LastTransitionTime.Time)) + " ago"
	}

	fmt.Fprintf(w, "  Phase        : %-22s  Step        : %s\n", phase, step)
	fmt.Fprintf(w, "  Weight       : %-22s  Paused      : %t\n", fmt.Sprintf("%d%%", cr.Status.CurrentWeight), cr.Spec.Paused)
	fmt.Fprintf(w, "  Abort        : %-22t  Last change : %s\n", cr.Spec.Abort, lastChange)
	fmt.Fprintf(w, "  Target       : %-22s  VS          : %s\n", cr.Spec.TargetRef, cr.Spec.VirtualServiceRef)
	if cr.Status.Message != "" {
		fmt.Fprintf(w, "  Message      : %s\n", cr.Status.Message)
	}
	fmt.Fprintln(w)

	if cr.Spec.Analysis != nil {
		renderSPRT(w, cr)
	} else {
		fmt.Fprintln(w, "SPRT analysis: not configured")
		fmt.Fprintln(w)
	}

	renderSteps(w, cr)
}

// renderSPRT prints the SPRT configuration, decision boundaries, and live
// per-metric state (log-likelihood ratio, baseline rate, observations).
//
// The log-likelihood ratio visualization places the current Λ on a bar
// between the lower boundary B (accept H0 → promote) and upper boundary A
// (reject H0 → rollback), giving an at-a-glance sense of which direction
// the test is trending.
func renderSPRT(w io.Writer, cr *statgatev1alpha1.CanaryRollout) {
	a := cr.Spec.Analysis
	upper := math.Log((1 - a.Beta) / a.Alpha) // reject H0 → rollback
	lower := math.Log(a.Beta / (1 - a.Alpha)) // accept H0 → promote
	interval := int32(10)
	if a.AnalysisIntervalSeconds > 0 {
		interval = a.AnalysisIntervalSeconds
	}
	promURL := cr.Spec.PrometheusURL
	if promURL == "" {
		promURL = "(not set — SPRT disabled)"
	}

	fmt.Fprintln(w, "SPRT configuration:")
	fmt.Fprintf(w, "  α (false rollback)  = %-10.4f  A (reject H0)  = %+0.4f\n", a.Alpha, upper)
	fmt.Fprintf(w, "  β (missed failure)  = %-10.4f  B (accept H0)  = %+0.4f\n", a.Beta, lower)
	fmt.Fprintf(w, "  analysis interval   = %ds\n", interval)
	fmt.Fprintf(w, "  Prometheus          = %s\n", promURL)
	fmt.Fprintln(w)

	if len(a.Metrics) == 0 {
		return
	}

	stateByName := make(map[string]statgatev1alpha1.SPRTMetricState, len(cr.Status.AnalysisState))
	for _, s := range cr.Status.AnalysisState {
		stateByName[s.Name] = s
	}

	for _, m := range a.Metrics {
		s, haveState := stateByName[m.Name]
		decision := "pending"
		if haveState && s.Decision != "" {
			decision = s.Decision
		}

		fmt.Fprintf(w, "Metric: %-24s  decision: %s\n", m.Name, decisionBadge(decision))
		if !haveState {
			fmt.Fprintln(w, "  (no observations yet — SPRT runs during pause windows)")
			fmt.Fprintln(w)
			continue
		}

		obsRate := 0.0
		if s.Observations > 0 {
			obsRate = float64(s.Failures) / float64(s.Observations)
		}
		p1 := s.BaselineRate + m.Delta
		if p1 > 1 {
			p1 = 1
		}

		fmt.Fprintf(w, "  Λ (log-likelihood):    %+0.4f\n", s.LogLikelihood)
		fmt.Fprintf(w, "  %s\n", llrBar(s.LogLikelihood, lower, upper))
		fmt.Fprintf(w, "  boundaries:            B=%+0.4f  (promote)    A=%+0.4f  (rollback)\n", lower, upper)
		fmt.Fprintf(w, "  baseline p0 (stable):  %.4f  (%.2f%%)\n", s.BaselineRate, s.BaselineRate*100)
		fmt.Fprintf(w, "  alternative p1=p0+Δ:   %.4f  (Δ=%.4f)\n", p1, m.Delta)
		fmt.Fprintf(w, "  canary observed:       %d requests, %d failures (%.2f%%)\n",
			s.Observations, s.Failures, obsRate*100)
		fmt.Fprintln(w)
	}
}

// llrBar renders a fixed-width visual of where the log-likelihood ratio sits
// between the two SPRT boundaries. The marker '●' shows the current Λ, the
// '│' characters are the boundary edges (B on the left → promote, A on the
// right → rollback), and the centered '0' is the neutral point.
func llrBar(value, lo, hi float64) string {
	const width = 50
	clamped := value
	if clamped < lo {
		clamped = lo
	}
	if clamped > hi {
		clamped = hi
	}
	frac := 0.0
	if hi > lo {
		frac = (clamped - lo) / (hi - lo)
	}
	pos := int(frac*float64(width-1) + 0.5)
	if pos < 0 {
		pos = 0
	}
	if pos > width-1 {
		pos = width - 1
	}

	// Zero point is where (0 - lo)/(hi - lo) lands on the bar.
	zeroPos := -1
	if lo < 0 && hi > 0 {
		zeroPos = int(((-lo)/(hi-lo))*float64(width-1) + 0.5)
	}

	bar := make([]rune, width)
	for i := range bar {
		bar[i] = '─'
	}
	if zeroPos >= 0 && zeroPos < width {
		bar[zeroPos] = '┼'
	}
	bar[0] = '│'
	bar[width-1] = '│'
	bar[pos] = '●'

	return "  promote B " + string(bar) + " A rollback"
}

// renderSteps prints the step list with a visual progress marker. Steps
// before the current one are marked done, the current one gets an arrow,
// and upcoming steps are empty.
func renderSteps(w io.Writer, cr *statgatev1alpha1.CanaryRollout) {
	fmt.Fprintln(w, "Steps:")
	cur := int(cr.Status.CurrentStep)
	terminal := cr.Status.Phase == statgatev1alpha1.PhasePromoted ||
		cr.Status.Phase == statgatev1alpha1.PhaseAborted ||
		cr.Status.Phase == statgatev1alpha1.PhaseFailed
	for i, s := range cr.Spec.Steps {
		marker := "[ ]"
		switch {
		case cr.Status.Phase == statgatev1alpha1.PhasePromoted:
			marker = "[✔]"
		case terminal:
			if i < cur {
				marker = "[✔]"
			} else if i == cur {
				marker = "[✗]"
			}
		case i < cur:
			marker = "[✔]"
		case i == cur:
			marker = "[▶]"
		}
		suffix := ""
		if !terminal && i == cur {
			suffix = "   ← current"
		}
		fmt.Fprintf(w, "  %s step %d   weight=%3d%%   pause=%ds%s\n",
			marker, i, s.Weight, s.PauseSeconds, suffix)
	}
}

func decisionBadge(d string) string {
	switch d {
	case "promote":
		return "✔ promote"
	case "rollback":
		return "✗ rollback"
	default:
		return "… pending"
	}
}

func humanDuration(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm%ds", int(d.Minutes()), int(d.Seconds())%60)
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh%dm", int(d.Hours()), int(d.Minutes())%60)
	default:
		return fmt.Sprintf("%dd", int(d.Hours()/24))
	}
}

func truncate(s string, max int) string {
	if max <= 0 {
		return ""
	}
	if len([]rune(s)) <= max {
		return s
	}
	runes := []rune(s)
	if max < 2 {
		return string(runes[:max])
	}
	return strings.TrimRight(string(runes[:max-1]), " ") + "…"
}
