package events

import (
	"context"
	"log/slog"
	"nudgebee/runbook/internal/model"
	"nudgebee/runbook/internal/storage"
	"nudgebee/runbook/services/security"
	"time"
)

// RecommendationStore defines the interface for querying new recommendations.
type RecommendationStore interface {
	FindNewRecommendations(ctx context.Context, since time.Time) ([]storage.RecommendationEvent, error)
}

// RecommendationPoller periodically polls the recommendation table for new entries
// and triggers matching optimization workflows.
type RecommendationPoller struct {
	store    RecommendationStore
	registry *EventRegistry
	executor WorkflowExecutor
	logger   *slog.Logger
	interval time.Duration
	lastPoll time.Time
}

// NewRecommendationPoller creates a new poller that checks for new recommendations.
func NewRecommendationPoller(
	store RecommendationStore,
	registry *EventRegistry,
	executor WorkflowExecutor,
	logger *slog.Logger,
	interval time.Duration,
) *RecommendationPoller {
	return &RecommendationPoller{
		store:    store,
		registry: registry,
		executor: executor,
		logger:   logger,
		interval: interval,
		lastPoll: time.Now().UTC(),
	}
}

// Start begins the polling loop. It blocks until the context is cancelled.
func (p *RecommendationPoller) Start(ctx context.Context) {
	p.logger.Info("starting recommendation poller", "interval", p.interval)
	ticker := time.NewTicker(p.interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			p.logger.Info("recommendation poller stopped")
			return
		case <-ticker.C:
			p.poll(ctx)
		}
	}
}

func (p *RecommendationPoller) poll(ctx context.Context) {
	since := p.lastPoll
	now := time.Now().UTC()

	recommendations, err := p.store.FindNewRecommendations(ctx, since)
	if err != nil {
		p.logger.Error("failed to poll recommendations", "error", err)
		return
	}

	if len(recommendations) == 0 {
		p.lastPoll = now
		return
	}

	p.logger.Info("found new recommendations", "count", len(recommendations), "since", since)

	triggered := 0
	for _, rec := range recommendations {
		event := map[string]any{
			"event_type":        "optimization.recommendation",
			"cloud_account_id":  rec.CloudAccountID,
			"account_id":        rec.CloudAccountID,
			"tenant_id":         rec.TenantID,
			"category":          rec.Category,
			"rule_name":         rec.RuleName,
			"resource_id":       rec.ResourceID,
			"severity":          rec.Severity,
			"estimated_savings": rec.EstimatedSavings,
			"status":            rec.Status,
			"recommendation_id": rec.ID,
			"cluster":           rec.Cluster,
		}

		matches := p.registry.Match("optimization.recommendation", rec.CloudAccountID, event)
		if len(matches) == 0 {
			continue
		}

		for _, rule := range matches {
			reqCtx := security.NewRequestContextForTenantAccountAdmin(rule.TenantID, "recommendation-poller", []string{rule.AccountID})
			inputs := map[string]any{
				"event": event,
			}

			runID, err := p.executor.ExecuteWorkflow(reqCtx, rule.AccountID, rule.WorkflowID, model.WorkflowTriggerOptimization, inputs)
			if err != nil {
				p.logger.Error("failed to execute optimization workflow",
					"workflow_id", rule.WorkflowID,
					"recommendation_id", rec.ID,
					"error", err,
				)
			} else {
				triggered++
				p.logger.Info("triggered optimization workflow",
					"workflow_id", rule.WorkflowID,
					"run_id", runID,
					"recommendation_id", rec.ID,
					"category", rec.Category,
					"rule_name", rec.RuleName,
				)
			}
		}
	}

	if triggered > 0 {
		p.logger.Info("recommendation poll complete", "triggered_workflows", triggered, "recommendations_checked", len(recommendations))
	}

	p.lastPoll = now
}
