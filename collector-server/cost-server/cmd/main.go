// Command cost-server is the nudgebee cost engine: a thin standalone HTTP service
// that computes per-cluster OpenCost allocations over UNMODIFIED upstream
// github.com/opencost/opencost (imported as a library, not forked). The nudgebee
// adapter (pkg/opencostengine) implements upstream's public interfaces so each
// cluster's Prometheus + K8s objects are reached through the relay-server, with
// pricing from the nudgebee DB — no in-cluster OpenCost exporter needed.
//
// Multitenant: one deployment serves every cluster; the cluster is selected per
// request by the X-Scope-OrgID header (= cloud_accounts.id). The api-server
// "OpenCost Spend Sync" cron is the only client.
package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"nudgebee/cost-server/pkg/opencostengine"
)

var logger = slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))

func main() {
	port := os.Getenv("PORT")
	if port == "" {
		port = "9003"
	}

	// Stop early on a configuration error (e.g. NUDGEBEE_DB unset). The DB itself
	// is opened lazily (see NewDB) — an unreachable metastore surfaces on the first
	// allocation request, not at boot — so a brief DB blip won't crash-loop the pod.
	if err := opencostengine.Init(); err != nil {
		logger.Error("cost-server: engine init failed", "error", err)
		os.Exit(1)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /allocation/compute", opencostengine.AllocationHandler)
	mux.HandleFunc("GET /status", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})

	srv := &http.Server{
		Addr:              ":" + port,
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
	}

	go func() {
		logger.Info("cost-server listening", "port", port)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Error("cost-server: listen failed", "error", err)
			os.Exit(1)
		}
	}()

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)
	<-stop

	logger.Info("cost-server shutting down")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := srv.Shutdown(ctx); err != nil {
		logger.Error("cost-server: graceful shutdown failed", "error", err)
	}
}
