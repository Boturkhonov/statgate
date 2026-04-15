package controller

import (
	"context"
	"testing"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

// newVSFakeClient builds a controller-runtime fake client that knows about
// the Istio VirtualService GVK as an unstructured type. This is the minimum
// plumbing needed to exercise SetVirtualServiceWeights without running real
// Istio CRDs.
func newVSFakeClient(t *testing.T, seed *unstructured.Unstructured) client.Client {
	t.Helper()
	scheme := runtime.NewScheme()
	scheme.AddKnownTypeWithName(virtualServiceGVK, &unstructured.Unstructured{})
	scheme.AddKnownTypeWithName(
		virtualServiceGVK.GroupVersion().WithKind("VirtualServiceList"),
		&unstructured.UnstructuredList{},
	)
	return fake.NewClientBuilder().WithScheme(scheme).WithObjects(seed).Build()
}

// newTestVS returns a minimally-populated unstructured VirtualService with
// two routes — one to a stable host, one to a canary host — that
// SetVirtualServiceWeights can patch.
func newTestVS(name, namespace, stableHost, canaryHost string, stableWeight, canaryWeight int64) *unstructured.Unstructured {
	vs := &unstructured.Unstructured{}
	vs.SetGroupVersionKind(virtualServiceGVK)
	vs.SetName(name)
	vs.SetNamespace(namespace)
	vs.Object["spec"] = map[string]interface{}{
		"http": []interface{}{
			map[string]interface{}{
				"route": []interface{}{
					map[string]interface{}{
						"destination": map[string]interface{}{"host": stableHost},
						"weight":      stableWeight,
					},
					map[string]interface{}{
						"destination": map[string]interface{}{"host": canaryHost},
						"weight":      canaryWeight,
					},
				},
			},
		},
	}
	return vs
}

// readWeights fetches a VirtualService via the client and returns the
// weights for each destination host as a map. Used by the assertions below
// to verify that SetVirtualServiceWeights produced the expected post-state.
func readWeights(t *testing.T, cl client.Client, namespace, name string) map[string]int64 {
	t.Helper()
	got := &unstructured.Unstructured{}
	got.SetGroupVersionKind(virtualServiceGVK)
	if err := cl.Get(context.Background(), client.ObjectKey{Namespace: namespace, Name: name}, got); err != nil {
		t.Fatalf("get VS: %v", err)
	}
	httpRoutes, found, err := unstructured.NestedSlice(got.Object, "spec", "http")
	if err != nil || !found {
		t.Fatalf("read spec.http: err=%v found=%v", err, found)
	}
	firstRoute, ok := httpRoutes[0].(map[string]interface{})
	if !ok {
		t.Fatalf("spec.http[0] not a map: %T", httpRoutes[0])
	}
	routes, _, err := unstructured.NestedSlice(firstRoute, "route")
	if err != nil {
		t.Fatalf("read route: %v", err)
	}

	weights := map[string]int64{}
	for _, r := range routes {
		rm, ok := r.(map[string]interface{})
		if !ok {
			continue
		}
		host, _, _ := unstructured.NestedString(rm, "destination", "host")
		w, _, _ := unstructured.NestedInt64(rm, "weight")
		weights[host] = w
	}
	return weights
}

func TestSetVirtualServiceWeights_UpdatesBothDestinations(t *testing.T) {
	cl := newVSFakeClient(t, newTestVS("demo-vs", "default", "stable-svc", "canary-svc", 100, 0))

	err := SetVirtualServiceWeights(
		context.Background(), cl, "default", "demo-vs",
		"stable-svc", 70,
		"canary-svc", 30,
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	got := readWeights(t, cl, "default", "demo-vs")
	if got["stable-svc"] != 70 {
		t.Errorf("stable weight: got %d, want 70", got["stable-svc"])
	}
	if got["canary-svc"] != 30 {
		t.Errorf("canary weight: got %d, want 30", got["canary-svc"])
	}
}

// Initial weights should be completely overwritten on each call — the
// function must not merge with prior values, otherwise a rollback (100/0)
// following a 50/50 step would leave stale state behind.
func TestSetVirtualServiceWeights_Rollback(t *testing.T) {
	cl := newVSFakeClient(t, newTestVS("demo-vs", "default", "stable-svc", "canary-svc", 50, 50))

	err := SetVirtualServiceWeights(
		context.Background(), cl, "default", "demo-vs",
		"stable-svc", 100,
		"canary-svc", 0,
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	got := readWeights(t, cl, "default", "demo-vs")
	if got["stable-svc"] != 100 || got["canary-svc"] != 0 {
		t.Errorf("post-rollback weights = %+v, want stable=100 canary=0", got)
	}
}

// Routes whose destination host matches neither the stable nor the canary
// reference (e.g. an unrelated mirror target) must be left untouched.
func TestSetVirtualServiceWeights_LeavesUnrelatedHostsAlone(t *testing.T) {
	vs := newTestVS("demo-vs", "default", "stable-svc", "canary-svc", 80, 20)
	// Inject a third route destined for an unrelated host.
	http, _, _ := unstructured.NestedSlice(vs.Object, "spec", "http")
	first := http[0].(map[string]interface{})
	routes, _, _ := unstructured.NestedSlice(first, "route")
	routes = append(routes, map[string]interface{}{
		"destination": map[string]interface{}{"host": "mirror-svc"},
		"weight":      int64(5),
	})
	_ = unstructured.SetNestedSlice(first, routes, "route")
	http[0] = first
	_ = unstructured.SetNestedSlice(vs.Object, http, "spec", "http")

	cl := newVSFakeClient(t, vs)

	err := SetVirtualServiceWeights(
		context.Background(), cl, "default", "demo-vs",
		"stable-svc", 60,
		"canary-svc", 40,
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	got := readWeights(t, cl, "default", "demo-vs")
	if got["stable-svc"] != 60 || got["canary-svc"] != 40 {
		t.Errorf("target weights wrong: %+v", got)
	}
	if got["mirror-svc"] != 5 {
		t.Errorf("unrelated host was modified: %d, want 5", got["mirror-svc"])
	}
}

func TestSetVirtualServiceWeights_MissingVS(t *testing.T) {
	cl := newVSFakeClient(t, newTestVS("other-vs", "default", "a", "b", 100, 0))

	err := SetVirtualServiceWeights(
		context.Background(), cl, "default", "demo-vs",
		"a", 50, "b", 50,
	)
	if err == nil {
		t.Errorf("expected error for missing VirtualService, got nil")
	}
}

func TestSetVirtualServiceWeights_NoHTTPRoutes(t *testing.T) {
	vs := &unstructured.Unstructured{}
	vs.SetGroupVersionKind(virtualServiceGVK)
	vs.SetName("empty-vs")
	vs.SetNamespace("default")
	vs.Object["spec"] = map[string]interface{}{} // no "http"
	cl := newVSFakeClient(t, vs)

	err := SetVirtualServiceWeights(
		context.Background(), cl, "default", "empty-vs",
		"stable", 50, "canary", 50,
	)
	if err == nil {
		t.Errorf("expected error for VirtualService without http routes, got nil")
	}
}
