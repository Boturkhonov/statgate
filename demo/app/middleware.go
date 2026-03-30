package main

import (
	"net/http"
	"strconv"
	"time"
)

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
		path := r.URL.Path
		statusStr := strconv.Itoa(rec.status)

		httpRequestsTotal.WithLabelValues(r.Method, path, statusStr, version).Inc()
		httpRequestDuration.WithLabelValues(r.Method, path, version).Observe(duration)
	})
}
