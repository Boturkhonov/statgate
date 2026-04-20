package main

import (
	"bytes"
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

	// First render: clear the screen once so the dashboard occupies the full
	// terminal from the top. Subsequent renders move the cursor back to the
	// home position and overwrite in place — the same technique used by top(1).
	firstRender := true

	render := func() {
		cr := &statgatev1alpha1.CanaryRollout{}
		getErr := c.Get(ctx, client.ObjectKey{Namespace: cf.namespace, Name: name}, cr)

		var buf bytes.Buffer
		if getErr != nil {
			fmt.Fprintf(&buf, "error fetching %s/%s: %v\n", cf.namespace, name, getErr)
		} else {
			renderRollout(&buf, cr, time.Now())
		}
		fmt.Fprintf(&buf, "\n(refreshing every %s — press Ctrl+C to exit)\n", refresh)

		if firstRender {
			// Full clear on the very first paint so the dashboard starts at row 0.
			fmt.Print("\x1b[2J")
			firstRender = false
		}
		// Move cursor to top-left, write the new frame, then erase anything
		// below the current cursor position (leftover lines from a taller
		// previous frame). This is the same approach used by top(1) and
		// watch(1): no flicker, no blank-screen flash.
		fmt.Print("\x1b[H")
		os.Stdout.Write(buf.Bytes())
		fmt.Print("\x1b[J")
	}

	render()
	ticker := time.NewTicker(refresh)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			fmt.Print("\x1b[?25h") // restore cursor visibility on exit
			return nil
		case <-ticker.C:
			render()
		}
	}
}
