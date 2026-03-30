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
	errorRate := parseFloat(getEnv("ERROR_RATE", "0.0"))
	hostname, _ := os.Hostname()

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
		db:        db,
		version:   version,
		hostname:  hostname,
		errorRate: errorRate,
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
		log.Printf("statgate-demo %s (pod=%s, errorRate=%.2f) listening on :8080", version, hostname, errorRate)
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
