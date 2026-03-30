package main

import "github.com/prometheus/client_golang/prometheus"

var (
	httpRequestsTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "http_requests_total",
			Help: "Total number of HTTP requests by method, path, status code, and app version.",
		},
		[]string{"method", "path", "status_code", "version"},
	)

	httpRequestDuration = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "http_request_duration_seconds",
			Help:    "HTTP request latency distribution by method, path, and app version.",
			Buckets: prometheus.DefBuckets,
		},
		[]string{"method", "path", "version"},
	)

	dbOperationsTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "db_operations_total",
			Help: "Total number of database operations by operation type and status.",
		},
		[]string{"operation", "status"},
	)
)

func init() {
	prometheus.MustRegister(
		httpRequestsTotal,
		httpRequestDuration,
		dbOperationsTotal,
	)
}
