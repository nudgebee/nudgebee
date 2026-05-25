package api

import (
	"log/slog"
	"net/http"

	"nudgebee/llm/agents/core"
	"nudgebee/llm/config"

	"github.com/gin-gonic/gin"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"
)

// testConnectionRequest is the body the api-server posts to
// POST /v1/llm-config/test-connection when a user clicks "Test Connection"
// in the LLM integration form. The Config map carries the raw LLM config
// field name/value pairs (already decrypted by api-server).
type testConnectionRequest struct {
	Config map[string]string `json:"config"`
}

type testConnectionResponse struct {
	OK    bool   `json:"ok"`
	Error string `json:"error,omitempty"`
}

// handleLLMConfigTestApis registers POST /v1/llm-config/test-connection.
//
// Authentication mirrors prompts.go: shared token in LlmServerTokenHeader.
// This is an internal service-to-service endpoint — only api-server calls it.
//
// We answer HTTP 200 in both success and "config rejected by the provider"
// cases so the caller can render a clear inline error to the user; transport
// errors (timeout, etc.) bubble up as native HTTP errors.
func handleLLMConfigTestApis(r *gin.Engine, _ trace.Tracer, _ metric.Meter) {
	group := r.Group("/v1/llm-config")
	group.Use(adminAuthMiddleware())

	group.POST("/test-connection", func(c *gin.Context) {
		var req testConnectionRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, testConnectionResponse{Error: "invalid request: " + err.Error()})
			return
		}
		if len(req.Config) == 0 {
			c.JSON(http.StatusBadRequest, testConnectionResponse{Error: "config is required"})
			return
		}
		_ = config.Config // keep import alive when LlmServerToken is empty in tests

		if err := core.TestLLMProviderConnection(c.Request.Context(), req.Config); err != nil {
			slog.Info("llm-config: connectivity probe failed",
				"provider", req.Config["llm_provider"], "error", err)
			c.JSON(http.StatusOK, testConnectionResponse{OK: false, Error: err.Error()})
			return
		}
		c.JSON(http.StatusOK, testConnectionResponse{OK: true})
	})
}
