// statctl is the command-line client for the StatGate canary rollout
// operator. It offers a kubectl-like UX focused solely on CanaryRollout
// resources (list/get/apply/pause/resume/abort/delete) and a live dashboard
// subcommand (watch) that renders SPRT accumulator state in real time.
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"
)

const version = "v0.1.0"

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	sub := os.Args[1]
	args := os.Args[2:]

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		ch := make(chan os.Signal, 1)
		signal.Notify(ch, os.Interrupt, syscall.SIGTERM)
		<-ch
		cancel()
	}()

	var err error
	switch sub {
	case "list", "ls":
		err = cmdList(ctx, args)
	case "get", "status":
		err = cmdGet(ctx, args)
	case "apply", "create":
		err = cmdApply(ctx, args)
	case "pause":
		err = cmdPause(ctx, args)
	case "resume", "start":
		err = cmdResume(ctx, args)
	case "abort", "rollback":
		err = cmdAbort(ctx, args)
	case "delete", "rm":
		err = cmdDelete(ctx, args)
	case "watch":
		err = cmdWatch(ctx, args)
	case "version", "--version", "-v":
		fmt.Printf("statctl %s\n", version)
	case "help", "--help", "-h":
		usage()
	default:
		fmt.Fprintf(os.Stderr, "unknown subcommand: %q\n\n", sub)
		usage()
		os.Exit(2)
	}
	if err != nil {
		if ctx.Err() != nil {
			return
		}
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}

func usage() {
	fmt.Print(`statctl — manage StatGate canary rollouts

Usage:
  statctl <command> [args] [flags]

Commands:
  list                 List canary rollouts in a namespace
  get <name>           Show details of a single rollout
  apply -f <file>      Create or update a rollout from a YAML manifest
  pause <name>         Halt progression (sets spec.paused=true)
  resume <name>        Resume progression (clears paused/abort)
  abort <name>         Trigger immediate rollback (sets spec.abort=true)
  delete <name>        Delete a rollout
  watch <name>         Live dashboard with SPRT state and metrics
  version              Print client version
  help                 Show this help

Global flags (accepted on every subcommand):
  -n, --namespace <ns>    Target namespace (default "default")
  --kubeconfig <path>     Path to kubeconfig (default $KUBECONFIG or ~/.kube/config)

Watch-only flags:
  --interval <seconds>    Refresh cadence (default 2)

Examples:
  statctl apply -f demo/manifests/05-rollout.yaml
  statctl list -n statgate-demo
  statctl watch demo-rollout -n statgate-demo
  statctl pause demo-rollout -n statgate-demo
  statctl abort demo-rollout -n statgate-demo
`)
}

// splitArgs separates a subcommand's raw argv into flag tokens and positional
// tokens, allowing flags to appear in any order relative to positional args
// (stdlib's flag package stops at the first non-flag, which is too strict for
// a kubectl-style CLI).
//
// Assumption: all string/int flags consume the next token as their value
// unless the flag is written as --name=value. The tool defines no boolean
// flags, so there is no ambiguity.
func splitArgs(args []string) (flags []string, positional []string) {
	i := 0
	for i < len(args) {
		a := args[i]
		// "-" is a convention for stdin, treat as positional.
		if strings.HasPrefix(a, "-") && a != "-" {
			flags = append(flags, a)
			i++
			if strings.Contains(a, "=") {
				continue
			}
			// Consume the next token as the flag value. "-" is valid (stdin).
			if i < len(args) && (!strings.HasPrefix(args[i], "-") || args[i] == "-") {
				flags = append(flags, args[i])
				i++
			}
			continue
		}
		positional = append(positional, a)
		i++
	}
	return
}
