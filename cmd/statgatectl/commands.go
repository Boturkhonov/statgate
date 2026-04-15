package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/yaml"

	statgatev1alpha1 "github.com/boturkhonov/statgate/api/v1alpha1"
)

func cmdList(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("list", flag.ContinueOnError)
	cf := registerCommon(fs)
	if _, err := parse(fs, args); err != nil {
		return err
	}
	c, err := newClient(cf.kubeconfig)
	if err != nil {
		return err
	}

	var list statgatev1alpha1.CanaryRolloutList
	if err := c.List(ctx, &list, client.InNamespace(cf.namespace)); err != nil {
		return err
	}
	if len(list.Items) == 0 {
		fmt.Printf("no canary rollouts in namespace %q\n", cf.namespace)
		return nil
	}

	fmt.Printf("%-28s %-10s %-8s %-8s %-12s %s\n", "NAME", "PHASE", "WEIGHT", "STEP", "AGE", "MESSAGE")
	for _, cr := range list.Items {
		age := humanDuration(time.Since(cr.CreationTimestamp.Time))
		totalSteps := len(cr.Spec.Steps)
		step := fmt.Sprintf("%d/%d", int(cr.Status.CurrentStep)+1, totalSteps)
		phase := string(cr.Status.Phase)
		if phase == "" {
			phase = "-"
		}
		fmt.Printf("%-28s %-10s %-8s %-8s %-12s %s\n",
			cr.Name,
			phase,
			fmt.Sprintf("%d%%", cr.Status.CurrentWeight),
			step,
			age,
			truncate(cr.Status.Message, 60),
		)
	}
	return nil
}

func cmdGet(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("get", flag.ContinueOnError)
	cf := registerCommon(fs)
	positional, err := parse(fs, args)
	if err != nil {
		return err
	}
	if len(positional) == 0 {
		return fmt.Errorf("get: <name> required")
	}
	c, err := newClient(cf.kubeconfig)
	if err != nil {
		return err
	}

	cr := &statgatev1alpha1.CanaryRollout{}
	key := client.ObjectKey{Namespace: cf.namespace, Name: positional[0]}
	if err := c.Get(ctx, key, cr); err != nil {
		return err
	}
	renderRollout(os.Stdout, cr, time.Now())
	return nil
}

func cmdApply(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("apply", flag.ContinueOnError)
	cf := registerCommon(fs)
	var file string
	fs.StringVar(&file, "f", "", "path to CanaryRollout YAML (or '-' for stdin)")
	fs.StringVar(&file, "file", "", "path to CanaryRollout YAML (alias for -f)")
	if _, err := parse(fs, args); err != nil {
		return err
	}
	if file == "" {
		return fmt.Errorf("apply: -f <file> required")
	}

	var (
		data []byte
		err  error
	)
	if file == "-" {
		data, err = io.ReadAll(os.Stdin)
	} else {
		data, err = os.ReadFile(file)
	}
	if err != nil {
		return fmt.Errorf("read %s: %w", file, err)
	}

	cr := &statgatev1alpha1.CanaryRollout{}
	if err := yaml.Unmarshal(data, cr); err != nil {
		return fmt.Errorf("parse %s: %w", file, err)
	}
	if cr.Kind != "" && cr.Kind != "CanaryRollout" {
		return fmt.Errorf("apply: expected kind CanaryRollout, got %q", cr.Kind)
	}
	if cr.Name == "" {
		return fmt.Errorf("apply: metadata.name is required")
	}
	if cr.Namespace == "" {
		cr.Namespace = cf.namespace
	}

	c, err := newClient(cf.kubeconfig)
	if err != nil {
		return err
	}

	existing := &statgatev1alpha1.CanaryRollout{}
	key := client.ObjectKey{Namespace: cr.Namespace, Name: cr.Name}
	getErr := c.Get(ctx, key, existing)
	switch {
	case apierrors.IsNotFound(getErr):
		// Clear TypeMeta so controller-runtime uses the scheme-registered GVK.
		cr.TypeMeta = cr.TypeMeta
		if err := c.Create(ctx, cr); err != nil {
			return fmt.Errorf("create: %w", err)
		}
		fmt.Printf("canaryrollout/%s created in namespace %s\n", cr.Name, cr.Namespace)
		return nil
	case getErr != nil:
		return getErr
	}

	existing.Spec = cr.Spec
	if err := c.Update(ctx, existing); err != nil {
		return fmt.Errorf("update: %w", err)
	}
	fmt.Printf("canaryrollout/%s configured in namespace %s\n", cr.Name, cr.Namespace)
	return nil
}

// patchRollout is the shared skeleton for pause / resume / abort: fetch the
// CR by name, apply the caller-supplied mutation to its spec, and update.
func patchRollout(ctx context.Context, args []string, verb, pastTense string, mutate func(*statgatev1alpha1.CanaryRollout)) error {
	fs := flag.NewFlagSet(verb, flag.ContinueOnError)
	cf := registerCommon(fs)
	positional, err := parse(fs, args)
	if err != nil {
		return err
	}
	if len(positional) == 0 {
		return fmt.Errorf("%s: <name> required", verb)
	}
	c, err := newClient(cf.kubeconfig)
	if err != nil {
		return err
	}

	cr := &statgatev1alpha1.CanaryRollout{}
	key := client.ObjectKey{Namespace: cf.namespace, Name: positional[0]}
	if err := c.Get(ctx, key, cr); err != nil {
		return err
	}
	mutate(cr)
	if err := c.Update(ctx, cr); err != nil {
		return fmt.Errorf("update: %w", err)
	}
	fmt.Printf("canaryrollout/%s %s\n", cr.Name, pastTense)
	return nil
}

func cmdPause(ctx context.Context, args []string) error {
	return patchRollout(ctx, args, "pause", "paused", func(cr *statgatev1alpha1.CanaryRollout) {
		cr.Spec.Paused = true
	})
}

func cmdResume(ctx context.Context, args []string) error {
	return patchRollout(ctx, args, "resume", "resumed", func(cr *statgatev1alpha1.CanaryRollout) {
		cr.Spec.Paused = false
		cr.Spec.Abort = false
	})
}

func cmdAbort(ctx context.Context, args []string) error {
	return patchRollout(ctx, args, "abort", "aborted — rolling back to stable", func(cr *statgatev1alpha1.CanaryRollout) {
		cr.Spec.Abort = true
	})
}

func cmdDelete(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("delete", flag.ContinueOnError)
	cf := registerCommon(fs)
	positional, err := parse(fs, args)
	if err != nil {
		return err
	}
	if len(positional) == 0 {
		return fmt.Errorf("delete: <name> required")
	}
	c, err := newClient(cf.kubeconfig)
	if err != nil {
		return err
	}

	cr := &statgatev1alpha1.CanaryRollout{}
	cr.Name = positional[0]
	cr.Namespace = cf.namespace
	if err := c.Delete(ctx, cr); err != nil {
		return err
	}
	fmt.Printf("canaryrollout/%s deleted\n", positional[0])
	return nil
}
