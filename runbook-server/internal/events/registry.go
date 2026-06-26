package events

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"nudgebee/runbook/internal/model"

	"github.com/nikolalohinski/gonja/v2"
	"github.com/nikolalohinski/gonja/v2/exec"
)

type TriggerStore interface {
	FindEventTriggers(ctx context.Context) ([]model.WorkflowEventTriggerRule, error)
}

type CompiledRule struct {
	Rule  model.WorkflowEventTriggerRule
	Query *exec.Template
}

type EventRegistry struct {
	store  TriggerStore
	logger *slog.Logger

	mu sync.RWMutex
	// map[EventType][]CompiledRule — rules scoped to a specific event_type.
	triggers map[string][]CompiledRule
	// Rules with no event_type scope; evaluated against every event. Each must have a compiled Query.
	wildcard []CompiledRule
}

func NewEventRegistry(store TriggerStore, logger *slog.Logger) *EventRegistry {
	return &EventRegistry{
		store:    store,
		logger:   logger,
		triggers: make(map[string][]CompiledRule),
	}
}

func (r *EventRegistry) Refresh(ctx context.Context) error {
	rules, err := r.store.FindEventTriggers(ctx)
	if err != nil {
		return fmt.Errorf("failed to fetch event triggers: %w", err)
	}

	newTriggers := make(map[string][]CompiledRule)
	var newWildcard []CompiledRule
	for _, rule := range rules {
		var query *exec.Template
		if rule.Filter != "" {
			q, err := gonja.FromString(rule.Filter)
			if err != nil {
				r.logger.Error("failed to compile gonja filter", "workflow_id", rule.WorkflowID, "filter", rule.Filter, "error", err)
				continue
			}
			query = q
		}

		cr := CompiledRule{Rule: rule, Query: query}
		if rule.EventType == "" {
			// Wildcard rules must have a filter — an unfiltered wildcard would match every event.
			// The validator enforces this; we drop here defensively to protect against legacy/malformed rows.
			if query == nil {
				r.logger.Warn("dropping wildcard event trigger with no filter", "workflow_id", rule.WorkflowID)
				continue
			}
			newWildcard = append(newWildcard, cr)
			continue
		}
		newTriggers[rule.EventType] = append(newTriggers[rule.EventType], cr)
	}

	r.mu.Lock()
	r.triggers = newTriggers
	r.wildcard = newWildcard
	r.mu.Unlock()

	if len(rules) > 0 {
		r.logger.Info("refreshed event triggers", "count", len(rules), "wildcard_count", len(newWildcard))
	}
	return nil
}

func (r *EventRegistry) StartSync(ctx context.Context, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	// Initial load
	if err := r.Refresh(ctx); err != nil {
		r.logger.Error("initial event registry refresh failed", "error", err)
	}

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := r.Refresh(ctx); err != nil {
				r.logger.Error("event registry refresh failed", "error", err)
			}
		}
	}
}

func (r *EventRegistry) Match(eventType string, accountID string, payload any) []model.WorkflowEventTriggerRule {
	r.mu.RLock()
	defer r.mu.RUnlock()

	var matches []model.WorkflowEventTriggerRule
	matches = r.evaluateRules(r.triggers[eventType], accountID, payload, matches)
	matches = r.evaluateRules(r.wildcard, accountID, payload, matches)
	return matches
}

func (r *EventRegistry) evaluateRules(candidates []CompiledRule, accountID string, payload any, matches []model.WorkflowEventTriggerRule) []model.WorkflowEventTriggerRule {
	for _, c := range candidates {
		// Strict tenancy check
		if c.Rule.AccountID != accountID {
			continue
		}

		if c.Query == nil {
			matches = append(matches, c.Rule)
			continue
		}

		// Jinja context uses map[string]any. Inject the built-in date/time vars (now,
		// datetime, timestamp_iso, …) — mirroring workflow task rendering — so trigger
		// filters can do date-based logic, e.g.
		// {{ now() | tz("Asia/Kolkata") | strftime("%H") | int >= 9 }}.
		nowUTC := time.Now().UTC()
		ctx := exec.NewContext(map[string]any{
			"event":         payload,
			"now":           func() time.Time { return time.Now().UTC() },
			"date":          nowUTC.Format("02012006"),
			"date_iso":      nowUTC.Format("2006-01-02"),
			"date_us":       nowUTC.Format("01/02/2006"),
			"time":          nowUTC.Format("1504"),
			"time_hms":      nowUTC.Format("15:04:05"),
			"datetime":      nowUTC.Format("02012006_1504"),
			"timestamp_iso": nowUTC.Format(time.RFC3339),
		})

		var buf bytes.Buffer
		// Recover from filter panics (e.g. tz with an invalid zone, time_add with a
		// bad duration) so a single malformed filter skips its rule instead of
		// crashing the evaluator.
		err := func() (err error) {
			defer func() {
				if rec := recover(); rec != nil {
					if e, ok := rec.(error); ok {
						err = e
					} else {
						err = fmt.Errorf("filter panic: %v", rec)
					}
				}
			}()
			return c.Query.Execute(&buf, ctx)
		}()
		if err != nil {
			r.logger.Warn("failed to evaluate gonja filter", "workflow_id", c.Rule.WorkflowID, "filter", c.Rule.Filter, "error", err)
			continue
		}

		// Interpret the template output as a boolean
		// For consistency with task 'if' conditions, assume 'true' or '1' as positive matches.
		resultStr := buf.String()
		if resultStr == "true" || resultStr == "True" || resultStr == "1" {
			matches = append(matches, c.Rule)
		}
	}
	return matches
}
