package api

import (
	"context"
	"log/slog"
	"strconv"
	"sync"

	"nudgebee/llm/config"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
)

var (
	workspaceExecuteAuthCounter metric.Int64Counter
	workspaceMetricsOnce        sync.Once
)

// recordWorkspaceExecuteAuth counts workspace-token calls to
// /api/v1/workspace/execute by token audience. Labels are bounded enums; the
// account lives in the log line, never in a label.
func recordWorkspaceExecuteAuth(ctx context.Context, audience string, enforced, allowed bool) {
	workspaceMetricsOnce.Do(func() {
		var err error
		workspaceExecuteAuthCounter, err = otel.Meter(config.SERVICE_NAME).Int64Counter(
			"nb_llm_workspace_execute_auth_total",
			metric.WithDescription("Workspace-token calls to /api/v1/workspace/execute, by token audience and outcome"),
		)
		if err != nil {
			slog.Error("workspace metrics: failed to create execute auth counter", "error", err)
		}
	})
	if workspaceExecuteAuthCounter == nil {
		return
	}
	result := "accepted"
	if !allowed {
		result = "rejected"
	}
	workspaceExecuteAuthCounter.Add(ctx, 1, metric.WithAttributes(
		attribute.String("audience", audience),
		attribute.String("enforced", strconv.FormatBool(enforced)),
		attribute.String("result", result),
	))
}
