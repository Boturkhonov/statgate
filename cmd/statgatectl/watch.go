package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"time"

	"sigs.k8s.io/controller-runtime/pkg/client"

	statgatev1alpha1 "github.com/boturkhonov/statgate/api/v1alpha1"
)

// cmdWatch re-fetches the CanaryRollout on a fixed cadence and redraws the
// dashboard until the user cancels. SPRT accumulator state is updated by the
// controller (default every 10s) and persisted in the CR status, so polling
// the CR is sufficient to observe live SPRT progress — no separate
// Prometheus connection from the client side.
func cmdWatch(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("watch", flag.ContinueOnError)
	cf := registerCommon(fs)
	intervalSec := fs.Int("interval", 2, "refresh interval in seconds")
	positional, err := parse(fs, args)
	if err != nil {
		return err
	}
	if len(positional) == 0 {
		return fmt.Errorf("watch: <name> required")
	}
	name := positional[0]

	c, err := newClient(cf.kubeconfig)
	if err != nil {
		return err
	}

	refresh := time.Duration(*intervalSec) * time.Second
	if refresh < time.Second {
		refresh = time.Second
	}

	render := func() {
		cr := &statgatev1alpha1.CanaryRollout{}
		getErr := c.Get(ctx, client.ObjectKey{Namespace: cf.namespace, Name: name}, cr)
		clearScreen()
		if getErr != nil {
			fmt.Fprintf(os.Stdout, "error fetching %s/%s: %v\n", cf.namespace, name, getErr)
			return
		}
		renderRollout(os.Stdout, cr, time.Now())
		fmt.Printf("\n(refreshing every %s — press Ctrl+C to exit)\n", refresh)
	}

	render()
	ticker := time.NewTicker(refresh)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			render()
		}
	}
}

// clearScreen emits the ANSI "erase display + home cursor" escape sequence.
// Modern Windows terminals (Windows Terminal, VS Code, recent PowerShell, git
// bash) all interpret ANSI. On exotic consoles the sequence becomes harmless
// noise and the output simply scrolls.
func clearScreen() {
	fmt.Print("\x1b[2J\x1b[H")
}
