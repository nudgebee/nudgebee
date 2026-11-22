package api

import (
	"context"
	"log/slog"
	"sync"

	"nudgebee/llm/config"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
)

var (
	workspaceAuthCounter     metric.Int64Counter
	workspaceAuthCounterOnce sync.Once
)

// recordWorkspaceAuth counts requests authenticated with a workspace token,
// by route and token audience. It shows which routes still receive the pod's
// own token, the signal for refusing it everywhere. Labels are bounded: route
// is the registered route template, never a raw path.
func recordWorkspaceAuth(ctx context.Context, route, audience string) {
	workspaceAuthCounterOnce.Do(func() {
		var err error
		workspaceAuthCounter, err = otel.Meter(config.SERVICE_NAME).Int64Counter(
			"nb_llm_workspace_auth_total",
			metric.WithDescription("Requests authenticated with a workspace token, by route and token audience"),
		)
		if err != nil {
			slog.Error("workspace metrics: failed to create auth counter", "error", err)
		}
	})
	if workspaceAuthCounter == nil {
		return
	}
	workspaceAuthCounter.Add(ctx, 1, metric.WithAttributes(
		attribute.String("route", route),
		attribute.String("audience", audience),
	))
}

var (
	workspaceNbctlCounter     metric.Int64Counter
	workspaceNbctlCounterOnce sync.Once
)

// recordWorkspaceNbctl counts requests from nbctl in Nubi's workspace by action
// and outcome, so the flag's adoption and refusals can be read off a dashboard.
// Labels are bounded: action is an allowlisted action or "other", and outcome
// is one of the handler's fixed results.
func recordWorkspaceNbctl(ctx context.Context, action, outcome string) {
	workspaceNbctlCounterOnce.Do(func() {
		var err error
		workspaceNbctlCounter, err = otel.Meter(config.SERVICE_NAME).Int64Counter(
			"nb_llm_workspace_nbctl_requests_total",
			metric.WithDescription("Requests from nbctl in Nubi's workspace, by action and outcome"),
		)
		if err != nil {
			slog.Error("workspace metrics: failed to create nbctl counter", "error", err)
		}
	})
	if workspaceNbctlCounter == nil {
		return
	}
	workspaceNbctlCounter.Add(ctx, 1, metric.WithAttributes(
		attribute.String("action", action),
		attribute.String("outcome", outcome),
	))
}
