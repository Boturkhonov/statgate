package main

import (
	"reflect"
	"testing"
)

// splitArgs is the linchpin of statgatectl's kubectl-style UX: it lets users
// write flags in any position relative to positional arguments. If it breaks,
// every subcommand breaks. Hence a fat table test covering the orderings we
// advertise in --help plus the awkward stdin-sentinel edge cases.
func TestSplitArgs(t *testing.T) {
	tests := []struct {
		name      string
		input     []string
		wantFlags []string
		wantPos   []string
	}{
		{
			name:    "empty",
			input:   []string{},
			wantPos: nil,
		},
		{
			name:    "only-positional",
			input:   []string{"demo-rollout"},
			wantPos: []string{"demo-rollout"},
		},
		{
			name:      "flag-before-positional",
			input:     []string{"-n", "my-ns", "demo-rollout"},
			wantFlags: []string{"-n", "my-ns"},
			wantPos:   []string{"demo-rollout"},
		},
		{
			name:      "positional-before-flag",
			input:     []string{"demo-rollout", "-n", "my-ns"},
			wantFlags: []string{"-n", "my-ns"},
			wantPos:   []string{"demo-rollout"},
		},
		{
			name:      "equals-form",
			input:     []string{"--namespace=my-ns", "demo-rollout"},
			wantFlags: []string{"--namespace=my-ns"},
			wantPos:   []string{"demo-rollout"},
		},
		{
			name:      "short-flag-equals",
			input:     []string{"-n=my-ns", "demo-rollout"},
			wantFlags: []string{"-n=my-ns"},
			wantPos:   []string{"demo-rollout"},
		},
		{
			name:      "stdin-as-flag-value",
			input:     []string{"-f", "-"},
			wantFlags: []string{"-f", "-"},
			wantPos:   nil,
		},
		{
			name:    "lonely-stdin-is-positional",
			input:   []string{"-"},
			wantPos: []string{"-"},
		},
		{
			name:      "multiple-flags",
			input:     []string{"-n", "ns", "--kubeconfig", "/tmp/kc", "demo"},
			wantFlags: []string{"-n", "ns", "--kubeconfig", "/tmp/kc"},
			wantPos:   []string{"demo"},
		},
		{
			name:      "interleaved-watch-usage",
			input:     []string{"demo", "--interval", "5", "-n", "ns"},
			wantFlags: []string{"--interval", "5", "-n", "ns"},
			wantPos:   []string{"demo"},
		},
		{
			name:      "apply-with-file-after-positional",
			input:     []string{"-f", "rollout.yaml"},
			wantFlags: []string{"-f", "rollout.yaml"},
			wantPos:   nil,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			flags, pos := splitArgs(tc.input)
			if !reflect.DeepEqual(flags, tc.wantFlags) {
				t.Errorf("flags\n  got:  %q\n  want: %q", flags, tc.wantFlags)
			}
			if !reflect.DeepEqual(pos, tc.wantPos) {
				t.Errorf("positional\n  got:  %q\n  want: %q", pos, tc.wantPos)
			}
		})
	}
}
