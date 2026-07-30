package triage

import (
	"context"
	"database/sql"
	"strings"

	"nudgebee/services/internal/database/models"

	"github.com/jmoiron/sqlx"
)

// Authority tags recorded in score_factors so every score is explainable and so downstream
// write paths know not to mutate a human-authored score.
const (
	authorityHumanEvent = "human:event"
	authorityHumanClass = "human:class"
)

// resolveHumanOverride returns an ABSOLUTE, authoritative human-set score for an event, or nil
// when no human correction applies. Precedence (highest first):
//  1. per-event correction (event_classification.corrected_priority set by a real user) —
//     survives recurrence because the same event_id is upserted on each occurrence,
//  2. a matching priority_pin rule (single winner by scope-specificity > rule priority > recency).
//
// When non-nil, the caller MUST treat it as final: it bypasses the LLM/legacy machine score AND
// suppresses additive scoring-rule adjustments. This is the single guarantee that a human
// correction is never silently overwritten by the machine on re-score.
func resolveHumanOverride(ctx context.Context, db *sqlx.DB, event *models.Event) *ScoreResult {
	if event == nil || event.Id == "" {
		return nil
	}
	// 1. Per-event human correction.
	if p := getEventCorrectedPriority(ctx, db, event.Id); p != "" {
		return absoluteHumanResult(p, authorityHumanEvent, "")
	}
	// 2. Per-class priority pin.
	if pin := resolvePriorityPin(ctx, db, event); pin != nil {
		return absoluteHumanResult(pin.priority, authorityHumanClass, pin.ruleID)
	}
	return nil
}

// IsHumanAuthority reports whether a score result was set by a human override (so callers like
// processor.go suppress additive scoring rules and recompute paths preserve it).
func IsHumanAuthority(result *ScoreResult) bool {
	if result == nil || result.Factors == nil {
		return false
	}
	a, _ := result.Factors["authority"].(string)
	return strings.HasPrefix(a, "human:")
}

func absoluteHumanResult(priority, authority, ruleID string) *ScoreResult {
	priority = strings.ToUpper(strings.TrimSpace(priority))
	score := priorityToMinScore(priority)
	factors := map[string]interface{}{
		"scoring_path": "human_override",
		"authority":    authority,
		"priority":     priority,
		"final_score":  score,
	}
	if ruleID != "" {
		factors["winning_rule_id"] = ruleID
	}
	return &ScoreResult{Score: score, Priority: priority, Factors: factors, Confidence: 1.0}
}

// getEventCorrectedPriority returns a human-set corrected_priority for this event, or "".
// Excludes system-authored classifications (auto-suppression etc.).
func getEventCorrectedPriority(ctx context.Context, db *sqlx.DB, eventID string) string {
	var p sql.NullString
	err := db.GetContext(ctx, &p, `
		SELECT corrected_priority
		FROM event_classification
		WHERE event_id = $1
		  AND corrected_priority IS NOT NULL
		  AND classified_by <> $2
		ORDER BY classified_at DESC
		LIMIT 1`, eventID, systemUserID)
	if err != nil || !p.Valid {
		return ""
	}
	return strings.TrimSpace(p.String)
}

type pinResolution struct {
	priority string
	ruleID   string
}

// resolvePriorityPin finds the single winning priority_pin rule for an event, or nil.
func resolvePriorityPin(ctx context.Context, db *sqlx.DB, event *models.Event) *pinResolution {
	if event.Tenant == nil || event.CloudAccountId == nil || *event.Tenant == "" || *event.CloudAccountId == "" {
		return nil
	}
	rules, err := LoadMatchingRules(ctx, db, *event.Tenant, *event.CloudAccountId)
	if err != nil {
		return nil
	}
	occ := 0
	if event.Fingerprint != nil {
		occ = getEventOccurrenceNumber(ctx, db, event.Id, *event.Fingerprint, *event.CloudAccountId)
	}
	var best *TriageRule
	for i := range rules {
		r := &rules[i]
		if r.RuleType != RuleTypePriorityPin {
			continue
		}
		if !MatchesRuleWithOccurrence(event, r, occ) {
			continue
		}
		if best == nil || pinMoreSpecific(r, best) {
			best = r
		}
	}
	if best == nil {
		return nil
	}
	av, err := ParseActionValue(best.ActionValue)
	if err != nil || av == nil || av.Priority == nil || strings.TrimSpace(*av.Priority) == "" {
		return nil
	}
	return &pinResolution{priority: *av.Priority, ruleID: best.ID}
}

// pinMoreSpecific implements the total order for picking a single winning pin:
// scope-specificity DESC (account > tenant > system), then rule.priority ASC, then created_at DESC.
func pinMoreSpecific(a, b *TriageRule) bool {
	sa, sb := pinScopeRank(a), pinScopeRank(b)
	if sa != sb {
		return sa > sb
	}
	if a.Priority != b.Priority {
		return a.Priority < b.Priority
	}
	return a.CreatedAt.After(b.CreatedAt)
}

// isValidPriority reports whether s is one of the canonical P0..P3 levels (case/space-insensitive).
func isValidPriority(s string) bool {
	switch strings.ToUpper(strings.TrimSpace(s)) {
	case "P0", "P1", "P2", "P3":
		return true
	default:
		return false
	}
}

func pinScopeRank(r *TriageRule) int {
	switch {
	case r.AccountID != nil:
		return 2
	case r.TenantID != nil:
		return 1
	default:
		return 0
	}
}
