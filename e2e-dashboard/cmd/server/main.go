package main

import (
	"log/slog"
	"os"

	"github.com/nudgebee/e2e-dashboard/internal/api"
	"github.com/nudgebee/e2e-dashboard/internal/store"
)

func main() {
	// Configure structured logging
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	}))
	slog.SetDefault(logger)

	slog.Info("Starting E2E Dashboard server")

	// Initialize in-memory store
	s, err := store.New()
	if err != nil {
		slog.Error("Failed to initialize store", "error", err)
		os.Exit(1)
	}
	defer func() { _ = s.Close() }()

	// Setup router
	router := api.SetupRouter(s)

	// Get port from environment or default to 8080
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	slog.Info("Server listening", "port", port)
	if err := router.Run(":" + port); err != nil {
		slog.Error("Server failed", "error", err)
		os.Exit(1)
	}
}
