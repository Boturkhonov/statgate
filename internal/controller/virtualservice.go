package controller

import (
	"context"
	"fmt"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

var virtualServiceGVR = schema.GroupVersionResource{
	Group:    "networking.istio.io",
	Version:  "v1beta1",
	Resource: "virtualservices",
}

var virtualServiceGVK = schema.GroupVersionKind{
	Group:   "networking.istio.io",
	Version: "v1beta1",
	Kind:    "VirtualService",
}

// SetVirtualServiceWeights patches the Istio VirtualService to set traffic
// weights for stable and canary destinations.
func SetVirtualServiceWeights(
	ctx context.Context,
	cl client.Client,
	namespace, vsName string,
	stableHost string, stableWeight int32,
	canaryHost string, canaryWeight int32,
) error {
	vs := &unstructured.Unstructured{}
	vs.SetGroupVersionKind(virtualServiceGVK)

	if err := cl.Get(ctx, client.ObjectKey{Namespace: namespace, Name: vsName}, vs); err != nil {
		return fmt.Errorf("get VirtualService %s/%s: %w", namespace, vsName, err)
	}

	httpRoutes, found, err := unstructured.NestedSlice(vs.Object, "spec", "http")
	if err != nil || !found || len(httpRoutes) == 0 {
		return fmt.Errorf("VirtualService %s/%s has no spec.http routes", namespace, vsName)
	}

	firstRoute, ok := httpRoutes[0].(map[string]interface{})
	if !ok {
		return fmt.Errorf("VirtualService %s/%s: spec.http[0] is not a map", namespace, vsName)
	}

	routes, found, err := unstructured.NestedSlice(firstRoute, "route")
	if err != nil || !found {
		return fmt.Errorf("VirtualService %s/%s: spec.http[0].route not found", namespace, vsName)
	}

	for i, r := range routes {
		route, ok := r.(map[string]interface{})
		if !ok {
			continue
		}

		host, _, _ := unstructured.NestedString(route, "destination", "host")

		switch host {
		case stableHost:
			if err := unstructured.SetNestedField(route, int64(stableWeight), "weight"); err != nil {
				return fmt.Errorf("set stable weight: %w", err)
			}
		case canaryHost:
			if err := unstructured.SetNestedField(route, int64(canaryWeight), "weight"); err != nil {
				return fmt.Errorf("set canary weight: %w", err)
			}
		}

		routes[i] = route
	}

	if err := unstructured.SetNestedSlice(firstRoute, routes, "route"); err != nil {
		return fmt.Errorf("set routes: %w", err)
	}

	httpRoutes[0] = firstRoute
	if err := unstructured.SetNestedSlice(vs.Object, httpRoutes, "spec", "http"); err != nil {
		return fmt.Errorf("set http routes: %w", err)
	}

	if err := cl.Update(ctx, vs); err != nil {
		return fmt.Errorf("update VirtualService %s/%s: %w", namespace, vsName, err)
	}

	return nil
}
