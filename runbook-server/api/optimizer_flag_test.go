package api

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"

	"nudgebee/runbook/config"
	"nudgebee/runbook/services/optimizer"

	"github.com/stretchr/testify/assert"
)

func TestOptimizationFlagDisabled(t *testing.T) {
	// Save original config and restore after test
	originalState := config.Config.OptimizationEnabled
	defer func() { config.Config.OptimizationEnabled = originalState }()

	// Disable optimization
	config.Config.OptimizationEnabled = false

	// Mock dependencies
	mockWorkflowService := &MockWorkflowService{}
	mockConfigService := &MockConfigService{}
	mockOptimizerService := &optimizer.MockOptimizerService{}

	// Create server
	server := NewServer(mockWorkflowService, mockConfigService)
	server.SetOptimizerService(mockOptimizerService)

	// Create request with empty JSON body
	req, _ := http.NewRequest("POST", "/autopilot/recommendation", bytes.NewBufferString("{}"))
	req.Header.Set("Content-Type", "application/json")

	// Serve request
	w := httptest.NewRecorder()
	server.router.ServeHTTP(w, req)

	// Assert 403 Forbidden
	assert.Equal(t, http.StatusForbidden, w.Code)
	assert.JSONEq(t, `{"error": "optimization feature is disabled"}`, w.Body.String())
}

func TestOptimizationFlagEnabled(t *testing.T) {
	// Save original config and restore after test
	originalState := config.Config.OptimizationEnabled
	defer func() { config.Config.OptimizationEnabled = originalState }()

	// Enable optimization
	config.Config.OptimizationEnabled = true

	// Mock dependencies
	mockWorkflowService := &MockWorkflowService{}
	mockConfigService := &MockConfigService{}
	mockOptimizerService := &optimizer.MockOptimizerService{}

	// Create server
	server := NewServer(mockWorkflowService, mockConfigService)
	server.SetOptimizerService(mockOptimizerService)

	// Create request with empty JSON body
	req, _ := http.NewRequest("POST", "/autopilot/recommendation", bytes.NewBufferString("{}"))
	req.Header.Set("Content-Type", "application/json")

	// Serve request
	w := httptest.NewRecorder()
	server.router.ServeHTTP(w, req)

	// Assert NOT "optimization feature is disabled"
	assert.NotContains(t, w.Body.String(), "optimization feature is disabled")
	// It should probably be 401 or 400 because of missing headers/body
	assert.NotEqual(t, http.StatusForbidden, w.Code, "Should not return 403 when enabled")
}
