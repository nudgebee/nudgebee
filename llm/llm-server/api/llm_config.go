package api

import (
	"fmt"
	"log/slog"
	"net/http"
	"strings"

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

// testConnectionResponse carries the per-target probe outcomes so the
// api-server can humanize each one and surface a single concise aggregate
// message to the UI. The top-level OK/Error fields preserve the original
// (single-probe) wire shape for backwards compatibility with any caller
// that doesn't read Results yet.
type testConnectionResponse struct {
	OK      bool               `json:"ok"`
	Error   string             `json:"error,omitempty"`
	Results []core.ProbeResult `json:"results,omitempty"`
	Summary string             `json:"summary,omitempty"`
}

// handleLLMConfigTestApis registers POST /v1/llm-config/test-connection.
//
// Authentication mirrors prompts.go: shared token in LlmServerTokenHeader.
// This is an internal service-to-service endpoint — only api-server calls it.
//
// We answer HTTP 200 in both success and "config rejected by the provider"
// cases so the caller can render a clear inline error to the user; transport
// errors (timeout, etc.) bubble up as native HTTP errors.
//
// Multi-model probe: every (provider, model) pair across global + tier +
// agent + fallbacks is probed in parallel (see TestLLMProviderConnectionAll).
// OK=true only when no probe failed; untestable targets (vertexai) count as
// success. Per-target details ride in Results so the api-server can build a
// user-actionable message.
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

		results, err := core.TestLLMProviderConnectionAll(c.Request.Context(), req.Config)
		if err != nil {
			slog.Info("llm-config: connectivity probe enumeration failed",
				"provider", req.Config["llm_provider"], "error", err)
			c.JSON(http.StatusOK, testConnectionResponse{OK: false, Error: err.Error()})
			return
		}

		// Aggregate counts for the summary string.
		var passed, failed, untestable int
		for _, r := range results {
			switch {
			case r.Untestable:
				untestable++
			case r.OK:
				passed++
			default:
				failed++
			}
		}
		ok := failed == 0
		summary := buildProbeSummary(passed, failed, untestable)

		// Backward-compat top-level Error field — first failure's error so old
		// callers that don't read Results still get something useful.
		var firstErr string
		if !ok {
			for _, r := range results {
				if !r.OK && !r.Untestable {
					firstErr = r.Error
					break
				}
			}
		}
		c.JSON(http.StatusOK, testConnectionResponse{
			OK:      ok,
			Error:   firstErr,
			Results: results,
			Summary: summary,
		})
	})
}

func buildProbeSummary(passed, failed, untestable int) string {
	total := passed + failed + untestable
	if total == 0 {
		return "no models probed"
	}
	parts := []string{fmt.Sprintf("%d/%d verified", passed, total)}
	if untestable > 0 {
		parts = append(parts, fmt.Sprintf("%d untestable", untestable))
	}
	if failed > 0 {
		parts = append(parts, fmt.Sprintf("%d failed", failed))
	}
	return strings.Join(parts, ", ")
}
