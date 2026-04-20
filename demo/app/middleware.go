package main

import (
	"net/http"
	"regexp"
	"strconv"
	"time"
)

// uuidRE matches a standard UUID v4 in a URL path segment.
var uuidRE = regexp.MustCompile(`[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}`)

// normalizePath replaces UUID path segments with the literal "{id}" so that
// Prometheus labels aggregate /orders/<uuid> into /orders/{id}.
func normalizePath(p string) string {
	return uuidRE.ReplaceAllString(p, "{id}")
}

// statusRecorder wraps http.ResponseWriter to capture the written status code.
type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (r *statusRecorder) WriteHeader(code int) {
	r.status = code
	r.ResponseWriter.WriteHeader(code)
}

// MetricsMiddleware records request count and latency for every request.
// The version label is sourced from the APP_VERSION environment variable.
func MetricsMiddleware(version string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		start := time.Now()

		next.ServeHTTP(rec, r)

		duration := time.Since(start).Seconds()
		path := normalizePath(r.URL.Path)
		statusStr := strconv.Itoa(rec.status)

		httpRequestsTotal.WithLabelValues(r.Method, path, statusStr, version).Inc()
		httpRequestDuration.WithLabelValues(r.Method, path, version).Observe(duration)
	})
}
