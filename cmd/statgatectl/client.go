package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/clientcmd"
	"sigs.k8s.io/controller-runtime/pkg/client"

	statgatev1alpha1 "github.com/boturkhonov/statgate/api/v1alpha1"
)

// commonFlags holds the flags that every subcommand accepts.
type commonFlags struct {
	namespace  string
	kubeconfig string
}

// registerCommon attaches -n/--namespace and --kubeconfig to a flagset. The
// same underlying field is reused for the short and long namespace form so
// either alias works.
func registerCommon(fs *flag.FlagSet) *commonFlags {
	cf := &commonFlags{}
	fs.StringVar(&cf.namespace, "n", "default", "target namespace")
	fs.StringVar(&cf.namespace, "namespace", "default", "target namespace (alias for -n)")
	fs.StringVar(&cf.kubeconfig, "kubeconfig", "", "path to kubeconfig (default $KUBECONFIG or ~/.kube/config)")
	return cf
}

// parse applies splitArgs to the raw args and then runs fs.Parse on the flag
// tokens, returning the separated positional tokens. This lets subcommands
// accept flags in any position relative to positional arguments.
func parse(fs *flag.FlagSet, args []string) ([]string, error) {
	// Silence flag package's default error output — we print errors ourselves.
	fs.SetOutput(os.Stderr)
	flagArgs, positional := splitArgs(args)
	if err := fs.Parse(flagArgs); err != nil {
		return nil, err
	}
	return positional, nil
}

// newClient constructs a controller-runtime client configured with our CRD
// scheme. The kubeconfig lookup order is:
//  1. explicit --kubeconfig flag
//  2. $KUBECONFIG env var
//  3. ~/.kube/config (if it exists)
func newClient(kubeconfig string) (client.Client, error) {
	if kubeconfig == "" {
		kubeconfig = os.Getenv("KUBECONFIG")
	}
	if kubeconfig == "" {
		if home, err := os.UserHomeDir(); err == nil {
			candidate := filepath.Join(home, ".kube", "config")
			if _, err := os.Stat(candidate); err == nil {
				kubeconfig = candidate
			}
		}
	}

	cfg, err := clientcmd.BuildConfigFromFlags("", kubeconfig)
	if err != nil {
		return nil, fmt.Errorf("load kubeconfig: %w", err)
	}

	scheme := runtime.NewScheme()
	if err := statgatev1alpha1.AddToScheme(scheme); err != nil {
		return nil, fmt.Errorf("register scheme: %w", err)
	}

	c, err := client.New(cfg, client.Options{Scheme: scheme})
	if err != nil {
		return nil, fmt.Errorf("create client: %w", err)
	}
	return c, nil
}
