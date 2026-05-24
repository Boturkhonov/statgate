package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/prometheus/client_golang/prometheus/promhttp"
)

func main() {
	version := getEnv("APP_VERSION", "unknown")
	dbURL := getEnv("DATABASE_URL", "")
	hostname, _ := os.Hostname()

	injector := newErrorInjector(loadInjectorConfig())

	var db *DB
	if dbURL != "" {
		var err error
		db, err = NewDB(dbURL)
		if err != nil {
			log.Fatalf("db init: %v", err)
		}
		defer db.conn.Close()
	} else {
		log.Println("DATABASE_URL not set, running without persistence")
	}

	app := &App{
		db:       db,
		version:  version,
		hostname: hostname,
		injector: injector,
	}

	mux := http.NewServeMux()
	mux.HandleFunc("POST /orders", app.handleCreateOrder)
	mux.HandleFunc("GET /orders", app.handleListOrders)
	mux.HandleFunc("GET /orders/{id}", app.handleGetOrder)
	mux.HandleFunc("DELETE /orders/{id}", app.handleDeleteOrder)
	mux.HandleFunc("GET /healthz", app.handleHealthz)
	mux.Handle("GET /metrics", promhttp.Handler())

	handler := MetricsMiddleware(version, mux)

	srv := &http.Server{
		Addr:    ":8080",
		Handler: handler,
	}

	go func() {
		log.Printf("statgate-demo %s (pod=%s) listening on :8080", version, hostname)
		log.Printf("error injector: start=%.3f end=%.3f ramp=%.0fs spikeAt=%.0fs spikeDur=%.0fs spikeRate=%.3f",
			injector.rateStart, injector.rateEnd, injector.rampSeconds,
			injector.spikeAtSeconds, injector.spikeDurationSeconds, injector.spikeRate)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("listen: %v", err)
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := srv.Shutdown(ctx); err != nil {
		log.Fatalf("shutdown: %v", err)
	}
}

// loadInjectorConfig reads the injector configuration from env vars.
// ERROR_RATE remains as a backwards-compatible default for ERROR_RATE_START
// and ERROR_RATE_END.
func loadInjectorConfig() errorInjectorConfig {
	baseRate := parseFloat(getEnv("ERROR_RATE", "0.0"))

	rateStart := baseRate
	if v := os.Getenv("ERROR_RATE_START"); v != "" {
		rateStart = parseFloat(v)
	}

	rateEnd := rateStart
	if v := os.Getenv("ERROR_RATE_END"); v != "" {
		rateEnd = parseFloat(v)
	}

	return errorInjectorConfig{
		RateStart:            rateStart,
		RateEnd:              rateEnd,
		RampSeconds:          parseFloat(getEnv("ERROR_RATE_RAMP_SECONDS", "0")),
		SpikeAtSeconds:       parseFloat(getEnv("ERROR_SPIKE_AT_SECONDS", "0")),
		SpikeDurationSeconds: parseFloat(getEnv("ERROR_SPIKE_DURATION_SECONDS", "0")),
		SpikeRate:            parseFloat(getEnv("ERROR_SPIKE_RATE", "0")),
	}
}

func getEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func parseFloat(s string) float64 {
	v, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return 0
	}
	return v
}
