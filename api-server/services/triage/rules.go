package triage

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"regexp"
	"sync"
	"time"

	"nudgebee/services/internal/database/models"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
	"github.com/lib/pq"
)

// CheckTriageRules evaluates all matching rules for an event and returns the result
func CheckTriageRules(ctx context.Context, db *sqlx.DB, event *models.Event) (*TriageRuleResult, error) {
	if event.CloudAccountId == nil || event.Tenant == nil {
		return nil, nil
	}

	// Load matching rules sorted by priority
	rules, err := LoadMatchingRules(ctx, db, *event.Tenant, *event.CloudAccountId)
	if err != nil {
		slog.ErrorContext(ctx, "Failed to load triage rules", "error", err)
		return nil, err
	}

	if len(rules) == 0 {
		return nil, nil
	}

	// Look up occurrence number for this event (needed for occurrence-based rules)
	var occurrenceNumber int
	if event.Fingerprint != nil {
		occurrenceNumber = getEventOccurrenceNumber(ctx, db, event.Id, *event.Fingerprint, *event.CloudAccountId)
	}

	// Evaluate rules in priority order
	var suppressionResult *SuppressionResult
	var scoreAdjustment *ScoreAdjustment
	var autoClassification *AutoClassification
	var suppressionRuleID, scoringRuleID, classificationRuleID string
	var matches []pendingMatch

	for _, rule := range rules {
		if !MatchesRuleWithOccurrence(event, &rule, occurrenceNumber) {
			continue
		}

		switch rule.RuleType {
		case RuleTypeSuppression:
			if suppressionResult == nil {
				suppressionResult = applySuppressionRule(&rule)
				suppressionRuleID = rule.ID
				// Update match count only for the winning rule
				go updateRuleMatchCount(ctx, db, rule.ID)
				matches = append(matches, pendingMatch{rule.ID, rule.RuleType, rule.Action})
				slog.InfoContext(ctx, "Suppression rule matched",
					"event_id", event.Id,
					"rule_id", rule.ID,
					"action", suppressionResult.Action,
				)
			}
		case RuleTypeScoring:
			// Scoring rules stack: all matching scoring rules contribute their adjustments
			adj := applyScoringRule(&rule)
			if scoreAdjustment == nil {
				scoreAdjustment = adj
				scoringRuleID = rule.ID // First scoring rule becomes the primary
			} else {
				scoreAdjustment.Adjustment += adj.Adjustment
			}
			go updateRuleMatchCount(ctx, db, rule.ID)
			matches = append(matches, pendingMatch{rule.ID, rule.RuleType, rule.Action})
			slog.InfoContext(ctx, "Scoring rule matched",
				"event_id", event.Id,
				"rule_id", rule.ID,
				"adjustment", adj.Adjustment,
				"cumulative_adjustment", scoreAdjustment.Adjustment,
			)
		case RuleTypeClassification:
			if autoClassification == nil {
				autoClassification = applyClassificationRule(&rule)
				classificationRuleID = rule.ID
				// Update match count only for the winning rule
				go updateRuleMatchCount(ctx, db, rule.ID)
				matches = append(matches, pendingMatch{rule.ID, rule.RuleType, rule.Action})
				slog.InfoContext(ctx, "Classification rule matched",
					"event_id", event.Id,
					"rule_id", rule.ID,
					"classification", autoClassification.Classification,
				)
			}
		}
	}

	// Insert match records for drilldown tracking (all winning rules per type)
	if len(matches) > 0 && event.CloudAccountId != nil && event.Tenant != nil {
		if err := insertRuleMatches(ctx, db, event.Id, *event.CloudAccountId, *event.Tenant, matches); err != nil {
			slog.WarnContext(ctx, "Failed to insert rule match records", "error", err, "event_id", event.Id)
			// Non-fatal: don't fail triage on tracking error
		}
	}

	// Return nil if no rules matched
	if suppressionResult == nil && scoreAdjustment == nil && autoClassification == nil {
		return nil, nil
	}

	// Build result from matched rules
	result := &TriageRuleResult{
		Suppression:        suppressionResult,
		ScoreAdjustment:    scoreAdjustment,
		AutoClassification: autoClassification,
	}

	// Set the primary action and rule ID based on priority
	if suppressionResult != nil && suppressionResult.Action == ActionDrop {
		result.Action = ActionDrop
		result.RuleType = RuleTypeSuppression
		result.RuleID = suppressionRuleID
	} else if suppressionResult != nil {
		result.Action = suppressionResult.Action
		result.RuleType = RuleTypeSuppression
		result.RuleID = suppressionRuleID
	} else if autoClassification != nil {
		result.Action = autoClassification.Classification
		result.RuleType = RuleTypeClassification
		result.RuleID = classificationRuleID
	} else if scoreAdjustment != nil {
		result.Action = ActionAdjustScore
		result.RuleType = RuleTypeScoring
		result.RuleID = scoringRuleID
	}

	return result, nil
}

// LoadMatchingRules loads all enabled rules for the given tenant/account
// System rules (tenant_id IS NULL AND account_id IS NULL) are included unless overridden
func LoadMatchingRules(ctx context.Context, db *sqlx.DB, tenantID, accountID string) ([]TriageRule, error) {
	// First, get overrides for this account
	overrides, err := getAccountOverrides(ctx, db, accountID)
	if err != nil {
		slog.WarnContext(ctx, "Failed to get account overrides", "error", err)
		overrides = make(map[string]bool)
	}

	query := `
		SELECT
			id, tenant_id, account_id, rule_type,
			match_source, match_alertname, match_namespace, match_service,
			match_fingerprint, match_labels, match_priority, match_finding_type,
			match_occurrence_greater_than,
			action, action_value, priority, is_editable, can_override,
			enabled, effective_from, effective_until,
			name, description, reason,
			created_by, updated_by, created_at, updated_at,
			match_count, last_matched_at,
			apply_to_existing
		FROM event_triage_rules
		WHERE enabled = TRUE
		  AND (tenant_id IS NULL OR tenant_id = $1)
		  AND (account_id IS NULL OR account_id = $2)
		  AND (effective_from IS NULL OR effective_from <= NOW())
		  AND (effective_until IS NULL OR effective_until > NOW())
		ORDER BY priority ASC
	`

	var rules []TriageRule
	err = db.SelectContext(ctx, &rules, query, tenantID, accountID)
	if err != nil {
		return nil, err
	}

	// Filter out overridden system rules and set computed fields
	filteredRules := make([]TriageRule, 0, len(rules))
	for i := range rules {
		rule := &rules[i]
		// Check if this is a system rule (tenant_id AND account_id are both NULL)
		rule.IsSystemRule = rule.TenantID == nil && rule.AccountID == nil

		// Check if system rule is overridden for this account
		if rule.IsSystemRule {
			if disabled, exists := overrides[rule.ID]; exists && disabled {
				rule.IsOverridden = true
				// Skip this rule since it's disabled for this account
				continue
			}
		}

		filteredRules = append(filteredRules, *rule)
	}

	return filteredRules, nil
}

// getAccountOverrides returns a map of system_rule_id -> disabled for an account
func getAccountOverrides(ctx context.Context, db *sqlx.DB, accountID string) (map[string]bool, error) {
	query := `
		SELECT system_rule_id, disabled
		FROM event_triage_rule_overrides
		WHERE account_id = $1
	`

	type override struct {
		SystemRuleID string `db:"system_rule_id"`
		Disabled     bool   `db:"disabled"`
	}

	var overrides []override
	err := db.SelectContext(ctx, &overrides, query, accountID)
	if err != nil {
		return nil, err
	}

	result := make(map[string]bool)
	for _, o := range overrides {
		result[o.SystemRuleID] = o.Disabled
	}
	return result, nil
}

// getScopedMatchCounts returns match counts for the given rule IDs scoped by account or tenant.
// If cloudAccountID is provided, counts are scoped to that account; otherwise scoped to tenant.
func getScopedMatchCounts(ctx context.Context, db *sqlx.DB, ruleIDs []string, cloudAccountID, tenantID string) map[string]int {
	result := make(map[string]int)
	if len(ruleIDs) == 0 {
		return result
	}

	var query string
	var args []interface{}

	if cloudAccountID != "" {
		query = `
			SELECT rule_id, COUNT(*) as cnt
			FROM event_triage_rule_matches
			WHERE rule_id = ANY($1) AND cloud_account_id = $2
			GROUP BY rule_id
		`
		args = []interface{}{pq.Array(ruleIDs), cloudAccountID}
	} else {
		query = `
			SELECT rule_id, COUNT(*) as cnt
			FROM event_triage_rule_matches
			WHERE rule_id = ANY($1) AND tenant_id = $2
			GROUP BY rule_id
		`
		args = []interface{}{pq.Array(ruleIDs), tenantID}
	}

	type ruleCount struct {
		RuleID string `db:"rule_id"`
		Count  int    `db:"cnt"`
	}

	var counts []ruleCount
	err := db.SelectContext(ctx, &counts, query, args...)
	if err != nil {
		slog.WarnContext(ctx, "Failed to get scoped match counts", "error", err)
		return result
	}

	for _, c := range counts {
		result[c.RuleID] = c.Count
	}
	return result
}

// MatchesRule checks if an event matches all criteria of a rule (AND logic)
// Note: For rules with occurrence-based matching, use MatchesRuleWithOccurrence instead
func MatchesRule(event *models.Event, rule *TriageRule) bool {
	return MatchesRuleWithOccurrence(event, rule, 0)
}

// MatchesRuleWithOccurrence checks if an event matches all criteria of a rule (AND logic)
// occurrenceNumber is the event's position in its duplicate chain (1 = first, 2 = second, etc.)
func MatchesRuleWithOccurrence(event *models.Event, rule *TriageRule, occurrenceNumber int) bool {
	// Check occurrence number (for system duplicate rule)
	// If rule has match_occurrence_greater_than set, event must have occurrence > that value
	if rule.MatchOccurrenceGreaterThan != nil {
		// If we don't have occurrence info (occurrenceNumber == 0), this rule doesn't match
		// An occurrence of 0 means the event hasn't been processed for duplicates yet
		if occurrenceNumber <= *rule.MatchOccurrenceGreaterThan {
			return false
		}
	}

	// Check fingerprint (exact match)
	if rule.MatchFingerprint != nil && *rule.MatchFingerprint != "" {
		if event.Fingerprint == nil || *event.Fingerprint != *rule.MatchFingerprint {
			return false
		}
	}

	// Check alertname (regex match against aggregation_key)
	if rule.MatchAlertname != nil && *rule.MatchAlertname != "" {
		if event.AggregationKey == nil || !matchesRegex(*event.AggregationKey, *rule.MatchAlertname) {
			return false
		}
	}

	// Check namespace (exact match)
	if rule.MatchNamespace != nil && *rule.MatchNamespace != "" {
		if event.SubjectNamespace == nil || *event.SubjectNamespace != *rule.MatchNamespace {
			return false
		}
	}

	// Check service (regex match on subject_owner)
	if rule.MatchService != nil && *rule.MatchService != "" {
		if event.SubjectOwner == nil || !matchesRegex(*event.SubjectOwner, *rule.MatchService) {
			return false
		}
	}

	// Check source (exact match)
	if rule.MatchSource != nil && *rule.MatchSource != "" {
		if event.Source == nil || *event.Source != *rule.MatchSource {
			return false
		}
	}

	// Check priority (exact match)
	if rule.MatchPriority != nil && *rule.MatchPriority != "" {
		if event.Priority == nil || *event.Priority != *rule.MatchPriority {
			return false
		}
	}

	// Check finding type (exact match)
	if rule.MatchFindingType != nil && *rule.MatchFindingType != "" {
		if event.FindingType == nil || *event.FindingType != *rule.MatchFindingType {
			return false
		}
	}

	// Check labels (JSONB containment)
	if rule.MatchLabels != nil && *rule.MatchLabels != "" {
		if !matchesLabels(event.Labels, *rule.MatchLabels) {
			return false
		}
	}

	return true
}

// getEventOccurrenceNumber returns the occurrence number for an event from event_duplicates
// Returns 0 if not found (meaning event hasn't been processed for duplicates or is the first)
func getEventOccurrenceNumber(ctx context.Context, db *sqlx.DB, eventID, fingerprint, cloudAccountID string) int {
	query := `
		SELECT occurrence_number
		FROM event_duplicates
		WHERE event_id = $1
		  AND fingerprint = $2
		  AND cloud_account_id = $3
		LIMIT 1
	`

	var occurrenceNumber int
	err := db.GetContext(ctx, &occurrenceNumber, query, eventID, fingerprint, cloudAccountID)
	if err != nil {
		// Not found or error - return 0 (will be treated as first occurrence)
		return 0
	}

	return occurrenceNumber
}

// ApplyTriageRuleActions applies the rule actions to an event
// Only applies the winning rule type's actions based on priority:
// Suppression > Classification > Scoring
func ApplyTriageRuleActions(ctx context.Context, db *sqlx.DB, event *models.Event, result *TriageRuleResult) error {
	if result == nil {
		return nil
	}

	// Track which rule updated this event (for UI linking)
	if result.RuleID != "" {
		if err := AddTriageRuleLabel(ctx, db, event.Id, result.RuleID); err != nil {
			slog.WarnContext(ctx, "Failed to add triage rule label", "error", err, "event_id", event.Id, "rule_id", result.RuleID)
		}
	}

	// Only apply the winning rule type's actions (based on result.RuleType set by CheckTriageRules)
	// This prevents lower-priority rules from overwriting higher-priority rule actions
	switch result.RuleType {
	case RuleTypeSuppression:
		// Apply suppression
		if result.Suppression != nil {
			newStatus := result.Suppression.NewStatus
			if err := updateEventNBStatusFromEvent(ctx, db, event.Id, newStatus); err != nil {
				return err
			}

			// Create classification record for suppression (so drilldown works)
			if event.CloudAccountId != nil && event.Tenant != nil {
				classificationID := uuid.New().String()
				systemUserID := "00000000-0000-0000-0000-000000000000" // System UUID for auto-suppression
				classification := EventClassification{
					ID:             classificationID,
					EventID:        event.Id,
					CloudAccountID: *event.CloudAccountId,
					TenantID:       *event.Tenant,
					Classification: ClassificationFalsePositive,
					ReasonCode:     "known_noise", // Suppressed events are treated as known noise
					ApplyScope:     ApplyScopeThisEvent,
					ClassifiedBy:   systemUserID,
					ClassifiedAt:   time.Now(),
					RuleID:         &result.RuleID,
				}

				if err := insertClassification(ctx, db, &classification); err != nil {
					slog.WarnContext(ctx, "Failed to insert suppression classification record", "error", err)
				}

				// Log to event_history for timeline API
				logClassificationToHistory(ctx, db, event.Id, *event.CloudAccountId, *event.Tenant, systemUserID, ClassificationFalsePositive)
			}

			slog.InfoContext(ctx, "Applied suppression rule",
				"event_id", event.Id,
				"new_status", newStatus,
				"rule_id", result.RuleID,
			)
		}

	case RuleTypeClassification:
		// Apply auto-classification
		if result.AutoClassification != nil {
			if err := applyAutoClassificationToEvent(ctx, db, event, result.AutoClassification); err != nil {
				return err
			}
			slog.InfoContext(ctx, "Applied auto-classification rule",
				"event_id", event.Id,
				"classification", result.AutoClassification.Classification,
				"rule_id", result.RuleID,
			)
		}

	case RuleTypeScoring:
		// Score adjustments are applied separately in ProcessEvent after ComputeScore
		slog.InfoContext(ctx, "Scoring rule matched (adjustment applied during scoring)",
			"event_id", event.Id,
			"rule_id", result.RuleID,
		)
	}

	return nil
}

// GetScoreAdjustmentFromRules returns the total score adjustment from rules
func GetScoreAdjustmentFromRules(result *TriageRuleResult) int {
	if result == nil || result.ScoreAdjustment == nil {
		return 0
	}
	return result.ScoreAdjustment.Adjustment
}

// -------------------- Rule Application Functions --------------------

// applySuppressionRule creates a suppression result from a rule
func applySuppressionRule(rule *TriageRule) *SuppressionResult {
	newStatus := NBStatusSuppressed
	if rule.Action == ActionDrop {
		newStatus = NBStatusDropped
	}

	return &SuppressionResult{
		Action:    rule.Action,
		NewStatus: newStatus,
	}
}

// applyScoringRule creates a score adjustment from a rule
func applyScoringRule(rule *TriageRule) *ScoreAdjustment {
	adjustment := 0
	reason := ""

	if rule.ActionValue != nil {
		actionValue, err := ParseActionValue(rule.ActionValue)
		if err == nil && actionValue != nil {
			if actionValue.Adjustment != nil {
				adjustment = *actionValue.Adjustment
			}
			if actionValue.Reason != nil {
				reason = *actionValue.Reason
			}
		}
	}

	return &ScoreAdjustment{
		Adjustment: adjustment,
		Reason:     reason,
	}
}

// applyClassificationRule creates an auto-classification from a rule
func applyClassificationRule(rule *TriageRule) *AutoClassification {
	classification := ClassificationDuplicate
	var linkedEventID *string
	reasonCode := "duplicate_incident"
	newStatus := NBStatusDuplicate // Fixed: Use correct status for duplicates

	if rule.ActionValue != nil {
		actionValue, err := ParseActionValue(rule.ActionValue)
		if err == nil && actionValue != nil {
			if actionValue.Classification != nil {
				classification = *actionValue.Classification
			}
			linkedEventID = actionValue.LinkedEventID
			if actionValue.ReasonCode != nil {
				reasonCode = *actionValue.ReasonCode
			}
		}
	}

	// Set status based on action
	if rule.Action == ActionAutoClassifyFP {
		newStatus = NBStatusSuppressed
		classification = ClassificationFalsePositive
	}

	return &AutoClassification{
		Classification: classification,
		LinkedEventID:  linkedEventID,
		ReasonCode:     reasonCode,
		NewStatus:      newStatus,
		RuleID:         &rule.ID,
	}
}

// applyAutoClassificationToEvent applies auto-classification to an event
func applyAutoClassificationToEvent(ctx context.Context, db *sqlx.DB, event *models.Event, ac *AutoClassification) error {
	// Update nb_status
	if err := updateEventNBStatusFromEvent(ctx, db, event.Id, ac.NewStatus); err != nil {
		return err
	}

	// Add duplicate label if applicable - always use TRUE first event from chain
	if ac.Classification == ClassificationDuplicate && event.Fingerprint != nil && event.CloudAccountId != nil {
		// Look up the TRUE first event from the duplicate chain
		firstEventID := getFirstEventIDFromChain(ctx, db, *event.Fingerprint, *event.CloudAccountId)
		if firstEventID != "" && firstEventID != event.Id {
			if err := addDuplicateLabel(ctx, db, event.Id, firstEventID); err != nil {
				slog.WarnContext(ctx, "Failed to add duplicate label", "error", err)
			}
			// Update ac.LinkedEventID to use the correct first event for classification record
			ac.LinkedEventID = &firstEventID
		} else if ac.LinkedEventID != nil {
			// Fallback to provided linked_event_id if no chain found
			if err := addDuplicateLabel(ctx, db, event.Id, *ac.LinkedEventID); err != nil {
				slog.WarnContext(ctx, "Failed to add duplicate label", "error", err)
			}
		}
	}

	// Create auto classification record
	if event.CloudAccountId != nil && event.Tenant != nil {
		classificationID := uuid.New().String()
		systemUserID := "00000000-0000-0000-0000-000000000000" // System UUID for auto-classification
		classification := EventClassification{
			ID:             classificationID,
			EventID:        event.Id,
			CloudAccountID: *event.CloudAccountId,
			TenantID:       *event.Tenant,
			Classification: ac.Classification,
			ReasonCode:     ac.ReasonCode,
			ApplyScope:     ApplyScopeThisEvent,
			ClassifiedBy:   systemUserID, // Fixed: Use valid UUID instead of "system"
			ClassifiedAt:   time.Now(),
			RuleID:         ac.RuleID,
		}
		if ac.LinkedEventID != nil {
			classification.LinkedEventID = ac.LinkedEventID
		}

		if err := insertClassification(ctx, db, &classification); err != nil {
			slog.WarnContext(ctx, "Failed to insert auto-classification record", "error", err)
		}

		// Log to event_history for timeline API
		logClassificationToHistory(ctx, db, event.Id, *event.CloudAccountId, *event.Tenant, systemUserID, ac.Classification)
	}

	return nil
}

// getFirstEventIDFromChain looks up the TRUE first event for a fingerprint from event_duplicates
func getFirstEventIDFromChain(ctx context.Context, db *sqlx.DB, fingerprint, cloudAccountID string) string {
	query := `
		SELECT first_event_id
		FROM event_duplicates
		WHERE fingerprint = $1
		  AND cloud_account_id = $2
		  AND occurrence_number = 1
		LIMIT 1
	`

	var firstEventID string
	err := db.GetContext(ctx, &firstEventID, query, fingerprint, cloudAccountID)
	if err != nil {
		slog.DebugContext(ctx, "No duplicate chain found for fingerprint",
			"fingerprint", fingerprint,
			"error", err,
		)
		return ""
	}

	return firstEventID
}

// updateEventNBStatusFromEvent updates event nb_status (system action)
func updateEventNBStatusFromEvent(ctx context.Context, db *sqlx.DB, eventID, nbStatus string) error {
	query := `
		UPDATE events
		SET nb_status = $1,
		    nb_status_changed_at = NOW(),
		    updated_at = NOW()
		WHERE id = $2
	`

	_, err := db.ExecContext(ctx, query, nbStatus, eventID)
	return err
}

// updateRuleMatchCount increments the match count for a rule
func updateRuleMatchCount(ctx context.Context, db *sqlx.DB, ruleID string) {
	query := `
		UPDATE event_triage_rules
		SET match_count = match_count + 1,
		    last_matched_at = NOW()
		WHERE id = $1
	`

	_, err := db.ExecContext(ctx, query, ruleID)
	if err != nil {
		slog.WarnContext(ctx, "Failed to update rule match count", "error", err, "rule_id", ruleID)
	}
}

// updateRuleMatchCountBy increments the match count for a rule by the given amount.
func updateRuleMatchCountBy(ctx context.Context, db *sqlx.DB, ruleID string, count int) {
	query := `
		UPDATE event_triage_rules
		SET match_count = match_count + $1,
		    last_matched_at = NOW()
		WHERE id = $2
	`

	_, err := db.ExecContext(ctx, query, count, ruleID)
	if err != nil {
		slog.WarnContext(ctx, "Failed to update rule match count", "error", err, "rule_id", ruleID, "count", count)
	}
}

// pendingMatch holds data collected during rule evaluation for batch insertion into event_triage_rule_matches.
type pendingMatch struct {
	ruleID   string
	ruleType string
	action   string
}

// insertRuleMatches batch-inserts match records into event_triage_rule_matches.
// Uses ON CONFLICT DO NOTHING to be idempotent for re-processed events.
func insertRuleMatches(ctx context.Context, db *sqlx.DB, eventID, cloudAccountID, tenantID string, matches []pendingMatch) error {
	if len(matches) == 0 {
		return nil
	}

	query := `
		INSERT INTO event_triage_rule_matches (event_id, rule_id, cloud_account_id, tenant_id, rule_type, action)
		VALUES `
	args := make([]interface{}, 0, len(matches)*6)
	for i, m := range matches {
		if i > 0 {
			query += ", "
		}
		base := i * 6
		query += fmt.Sprintf("($%d, $%d, $%d, $%d, $%d, $%d)", base+1, base+2, base+3, base+4, base+5, base+6)
		args = append(args, eventID, m.ruleID, cloudAccountID, tenantID, m.ruleType, m.action)
	}
	query += " ON CONFLICT (event_id, rule_id, cloud_account_id) DO NOTHING"

	_, err := db.ExecContext(ctx, query, args...)
	return err
}

// -------------------- Helper Functions --------------------

// regexCache caches compiled regex patterns to avoid recompilation on every match.
// Triage rule patterns are few and stable, so this cache grows slowly and is read-heavy.
var regexCache sync.Map

// matchesRegex checks if a string matches a regex pattern.
// Compiled patterns are cached — regexp.Compile is expensive (~1-5µs per call)
// and this function is called for every event × every regex rule on the hot path.
// Both valid and invalid patterns are cached to avoid repeated compilation attempts.
func matchesRegex(value, pattern string) bool {
	if cached, ok := regexCache.Load(pattern); ok {
		re, _ := cached.(*regexp.Regexp)
		return re != nil && re.MatchString(value)
	}
	re, err := regexp.Compile(pattern)
	regexCache.Store(pattern, re) // stores nil on compile error — intentional
	if err != nil {
		return false
	}
	return re.MatchString(value)
}

// matchesLabels checks if event labels contain all rule labels
func matchesLabels(eventLabels *models.Json, ruleLabels string) bool {
	if eventLabels == nil {
		return false
	}

	// Parse rule labels
	var ruleLabelMap map[string]interface{}
	if err := json.Unmarshal([]byte(ruleLabels), &ruleLabelMap); err != nil {
		return false
	}

	// Get event labels as map
	eventLabelMap, ok := eventLabels.Object().(map[string]interface{})
	if !ok {
		return false
	}

	// Check if event labels contain all rule labels
	for key, value := range ruleLabelMap {
		eventValue, ok := eventLabelMap[key]
		if !ok {
			return false
		}
		// Compare values (convert to string for comparison)
		if fmt.Sprintf("%v", eventValue) != fmt.Sprintf("%v", value) {
			return false
		}
	}

	return true
}

// -------------------- CRUD Operations for Rules --------------------

// ValidationError represents a user input validation error
type ValidationError struct {
	Message string
}

func (e *ValidationError) Error() string {
	return e.Message
}

// validateJSONBField checks if a string pointer contains valid JSON for JSONB columns
func validateJSONBField(value *string, fieldName string) error {
	if value == nil || *value == "" {
		return nil
	}
	if !json.Valid([]byte(*value)) {
		return &ValidationError{Message: fmt.Sprintf("%s must be valid JSON, got: %s", fieldName, *value)}
	}
	return nil
}

// CreateTriageRule creates a new triage rule
func CreateTriageRule(ctx context.Context, db *sqlx.DB, req CreateTriageRuleRequest, cloudAccountID, tenantID, userID string) (*TriageRule, error) {
	// Validate JSONB fields before insertion
	if err := validateJSONBField(req.MatchLabels, "match_labels"); err != nil {
		return nil, err
	}
	if err := validateJSONBField(req.ActionValue, "action_value"); err != nil {
		return nil, err
	}

	ruleID := uuid.New().String()

	priority := 200 // Default for account rules
	if req.Priority != nil {
		priority = *req.Priority
	}

	rule := &TriageRule{
		ID:               ruleID,
		TenantID:         &tenantID,
		AccountID:        &cloudAccountID,
		RuleType:         req.RuleType,
		MatchSource:      req.MatchSource,
		MatchAlertname:   req.MatchAlertname,
		MatchNamespace:   req.MatchNamespace,
		MatchService:     req.MatchService,
		MatchFingerprint: req.MatchFingerprint,
		MatchLabels:      req.MatchLabels,
		MatchPriority:    req.MatchPriority,
		MatchFindingType: req.MatchFindingType,
		Action:           req.Action,
		ActionValue:      req.ActionValue,
		Priority:         priority,
		IsEditable:       true,
		CanOverride:      true,
		Enabled:          true,
		Name:             req.Name,
		Description:      req.Description,
		ApplyToExisting:  req.ApplyToExisting,
		CreatedBy:        &userID,
		CreatedAt:        time.Now(),
		UpdatedAt:        time.Now(),
	}

	// Parse effective_until if provided
	if req.EffectiveUntil != nil {
		t, err := time.Parse(time.RFC3339, *req.EffectiveUntil)
		if err == nil {
			rule.EffectiveUntil = &t
		}
	}

	if err := insertTriageRule(ctx, db, rule); err != nil {
		return nil, err
	}

	return rule, nil
}

// GetTriageRules retrieves triage rules for an account (or all accounts if cloudAccountID is empty)
// Includes system rules and sets computed fields (IsSystemRule, IsOverridden)
func GetTriageRules(ctx context.Context, db *sqlx.DB, req GetTriageRulesRequest, cloudAccountID, tenantID string) ([]TriageRule, error) {
	var query string
	var args []interface{}
	var argIdx int

	if cloudAccountID == "" {
		// When no account ID is provided, return all rules for the tenant
		query = `
			SELECT
				id, tenant_id, account_id, rule_type,
				match_source, match_alertname, match_namespace, match_service,
				match_fingerprint, match_labels, match_priority, match_finding_type,
				match_occurrence_greater_than,
				action, action_value, priority, is_editable, can_override,
				enabled, effective_from, effective_until,
				name, description, reason,
				created_by, updated_by, created_at, updated_at,
				match_count, last_matched_at,
				apply_to_existing
			FROM event_triage_rules
			WHERE (tenant_id IS NULL OR tenant_id = $1)
		`
		args = []interface{}{tenantID}
		argIdx = 2
	} else {
		// When account ID is provided, filter by both tenant and account
		// Also include system rules (account_id IS NULL)
		query = `
			SELECT
				id, tenant_id, account_id, rule_type,
				match_source, match_alertname, match_namespace, match_service,
				match_fingerprint, match_labels, match_priority, match_finding_type,
				match_occurrence_greater_than,
				action, action_value, priority, is_editable, can_override,
				enabled, effective_from, effective_until,
				name, description, reason,
				created_by, updated_by, created_at, updated_at,
				match_count, last_matched_at,
				apply_to_existing
			FROM event_triage_rules
			WHERE (tenant_id IS NULL OR tenant_id = $1)
			  AND (account_id IS NULL OR account_id = $2)
		`
		args = []interface{}{tenantID, cloudAccountID}
		argIdx = 3
	}

	if req.RuleType != nil {
		query += " AND rule_type = $" + string(rune('0'+argIdx))
		args = append(args, *req.RuleType)
		argIdx++
	}

	if req.Enabled != nil {
		query += " AND enabled = $" + string(rune('0'+argIdx))
		args = append(args, *req.Enabled)
	}

	query += " ORDER BY priority ASC, created_at DESC"

	var rules []TriageRule
	err := db.SelectContext(ctx, &rules, query, args...)
	if err != nil {
		return nil, err
	}

	// Get overrides for this account to set IsOverridden field
	var overrides map[string]bool
	if cloudAccountID != "" {
		overrides, err = getAccountOverrides(ctx, db, cloudAccountID)
		if err != nil {
			slog.WarnContext(ctx, "Failed to get account overrides", "error", err)
			overrides = make(map[string]bool)
		}
	} else {
		overrides = make(map[string]bool)
	}

	// Collect system rule IDs to fix their match counts (scoped to account/tenant)
	var systemRuleIDs []string

	// Set computed fields
	for i := range rules {
		rule := &rules[i]
		// Check if this is a system rule (tenant_id AND account_id are both NULL)
		rule.IsSystemRule = rule.TenantID == nil && rule.AccountID == nil

		// Check if system rule is overridden for this account
		if rule.IsSystemRule {
			if disabled, exists := overrides[rule.ID]; exists && disabled {
				rule.IsOverridden = true
			}
			systemRuleIDs = append(systemRuleIDs, rule.ID)
		}
	}

	// For system rules, replace the global match_count with a scoped count
	// from event_triage_rule_matches filtered by account or tenant
	if len(systemRuleIDs) > 0 {
		scopedCounts := getScopedMatchCounts(ctx, db, systemRuleIDs, cloudAccountID, tenantID)
		for i := range rules {
			if rules[i].IsSystemRule {
				if count, ok := scopedCounts[rules[i].ID]; ok {
					rules[i].MatchCount = count
				} else {
					rules[i].MatchCount = 0
				}
			}
		}
	}

	return rules, nil
}

// PreviewTriageRule counts how many existing events would match a rule's criteria
func PreviewTriageRule(ctx context.Context, db *sqlx.DB, req RulePreviewRequest, cloudAccountID, tenantID string) (*RulePreviewResponse, error) {
	// Build dynamic query based on match criteria
	query := `
		SELECT COUNT(*)
		FROM events
		WHERE cloud_account_id = $1
		  AND nb_status IN ('OPEN', 'ACKNOWLEDGED', 'INVESTIGATING')
	`
	sampleQuery := `
		SELECT id, title, COALESCE(subject_namespace, '') as namespace, COALESCE(subject_owner, '') as service
		FROM events
		WHERE cloud_account_id = $1
		  AND nb_status IN ('OPEN', 'ACKNOWLEDGED', 'INVESTIGATING')
	`

	args := []interface{}{cloudAccountID}
	conditions := ""
	argIndex := 2

	// Add match conditions
	if req.MatchFingerprint != nil && *req.MatchFingerprint != "" {
		conditions += fmt.Sprintf(" AND fingerprint = $%d", argIndex)
		args = append(args, *req.MatchFingerprint)
		argIndex++
	}

	if req.MatchAlertname != nil && *req.MatchAlertname != "" {
		conditions += fmt.Sprintf(" AND aggregation_key ~ $%d", argIndex)
		args = append(args, *req.MatchAlertname)
		argIndex++
	}

	if req.MatchNamespace != nil && *req.MatchNamespace != "" {
		conditions += fmt.Sprintf(" AND subject_namespace ~ $%d", argIndex)
		args = append(args, *req.MatchNamespace)
		argIndex++
	}

	if req.MatchService != nil && *req.MatchService != "" {
		conditions += fmt.Sprintf(" AND subject_owner ~ $%d", argIndex)
		args = append(args, *req.MatchService)
		argIndex++
	}

	if req.MatchSource != nil && *req.MatchSource != "" {
		conditions += fmt.Sprintf(" AND source = $%d", argIndex)
		args = append(args, *req.MatchSource)
		argIndex++
	}

	if req.MatchPriority != nil && *req.MatchPriority != "" {
		conditions += fmt.Sprintf(" AND priority = $%d", argIndex)
		args = append(args, *req.MatchPriority)
		argIndex++
	}

	if req.MatchFindingType != nil && *req.MatchFindingType != "" {
		conditions += fmt.Sprintf(" AND finding_type = $%d", argIndex)
		args = append(args, *req.MatchFindingType)
		argIndex++
	}

	if req.MatchLabels != nil && *req.MatchLabels != "" {
		conditions += fmt.Sprintf(" AND labels @> $%d::jsonb", argIndex)
		args = append(args, *req.MatchLabels)
	}

	// Count matching events
	var count int
	err := db.GetContext(ctx, &count, query+conditions, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to count matching events: %w", err)
	}

	// Get sample events
	samples := make([]RulePreviewSampleEvent, 0)
	err = db.SelectContext(ctx, &samples, sampleQuery+conditions+" ORDER BY starts_at DESC LIMIT 5", args...)
	if err != nil {
		slog.WarnContext(ctx, "Failed to get sample events", "error", err)
		samples = []RulePreviewSampleEvent{}
	}

	// Determine new status based on rule type and action
	var newStatus string
	if req.RuleType == RuleTypeScoring {
		newStatus = NBStatusNoChange
	} else if req.Action == ActionDrop {
		newStatus = NBStatusDropped
	} else if req.RuleType == RuleTypeClassification {
		newStatus = NBStatusResolved
	} else {
		newStatus = NBStatusSuppressed
	}

	return &RulePreviewResponse{
		MatchingEventsCount: count,
		SampleEvents:        samples,
		NewStatus:           newStatus,
	}, nil
}

// ApplyRuleToExistingEvents applies a rule to existing matching events directly
func ApplyRuleToExistingEvents(ctx context.Context, db *sqlx.DB, rule *TriageRule, cloudAccountID, userID string) (int, error) {
	// For scoring rules, adjust scores instead of changing status
	if rule.RuleType == RuleTypeScoring {
		return applyScoreAdjustmentToExistingEvents(ctx, db, rule, cloudAccountID)
	}

	// Determine target status
	targetStatus := NBStatusSuppressed
	if rule.Action == ActionDrop {
		targetStatus = NBStatusDropped
	} else if rule.RuleType == RuleTypeClassification {
		targetStatus = NBStatusResolved
	}

	// Build dynamic UPDATE query based on match criteria
	query := `
		UPDATE events
		SET nb_status = $1,
		    nb_status_changed_at = NOW(),
		    nb_status_changed_by = $2,
		    updated_at = NOW()
		WHERE cloud_account_id = $3
		  AND nb_status IN ('OPEN', 'ACKNOWLEDGED', 'INVESTIGATING')
	`

	args := []interface{}{targetStatus, userID, cloudAccountID}
	argIndex := 4

	// Add match conditions
	if rule.MatchFingerprint != nil && *rule.MatchFingerprint != "" {
		query += fmt.Sprintf(" AND fingerprint = $%d", argIndex)
		args = append(args, *rule.MatchFingerprint)
		argIndex++
	}

	if rule.MatchAlertname != nil && *rule.MatchAlertname != "" {
		query += fmt.Sprintf(" AND aggregation_key ~ $%d", argIndex)
		args = append(args, *rule.MatchAlertname)
		argIndex++
	}

	if rule.MatchNamespace != nil && *rule.MatchNamespace != "" {
		query += fmt.Sprintf(" AND subject_namespace ~ $%d", argIndex)
		args = append(args, *rule.MatchNamespace)
		argIndex++
	}

	if rule.MatchService != nil && *rule.MatchService != "" {
		query += fmt.Sprintf(" AND subject_owner ~ $%d", argIndex)
		args = append(args, *rule.MatchService)
		argIndex++
	}

	if rule.MatchSource != nil && *rule.MatchSource != "" {
		query += fmt.Sprintf(" AND source = $%d", argIndex)
		args = append(args, *rule.MatchSource)
		argIndex++
	}

	if rule.MatchPriority != nil && *rule.MatchPriority != "" {
		query += fmt.Sprintf(" AND priority = $%d", argIndex)
		args = append(args, *rule.MatchPriority)
		argIndex++
	}

	if rule.MatchFindingType != nil && *rule.MatchFindingType != "" {
		query += fmt.Sprintf(" AND finding_type = $%d", argIndex)
		args = append(args, *rule.MatchFindingType)
		argIndex++
	}

	if rule.MatchLabels != nil && *rule.MatchLabels != "" {
		query += fmt.Sprintf(" AND labels @> $%d::jsonb", argIndex)
		args = append(args, *rule.MatchLabels)
	}

	// Execute the update
	result, err := db.ExecContext(ctx, query, args...)
	if err != nil {
		return 0, fmt.Errorf("failed to apply rule to existing events: %w", err)
	}

	rowsAffected, _ := result.RowsAffected()

	slog.InfoContext(ctx, "Applied rule to existing events",
		"rule_id", rule.ID,
		"events_updated", rowsAffected,
		"new_status", targetStatus,
	)

	return int(rowsAffected), nil
}

// applyScoreAdjustmentToExistingEvents adjusts computed_score and computed_priority
// for existing events matching a scoring rule's criteria.
func applyScoreAdjustmentToExistingEvents(ctx context.Context, db *sqlx.DB, rule *TriageRule, cloudAccountID string) (int, error) {
	// Parse the adjustment value from rule.ActionValue
	adjustment := 0
	if rule.ActionValue != nil {
		actionValue, err := ParseActionValue(rule.ActionValue)
		if err == nil && actionValue != nil && actionValue.Adjustment != nil {
			adjustment = *actionValue.Adjustment
		}
	}

	if adjustment == 0 {
		slog.InfoContext(ctx, "Scoring rule has zero adjustment, skipping apply to existing", "rule_id", rule.ID)
		return 0, nil
	}

	// Build dynamic query with CTE to compute the new score once and reuse it.
	// The score-to-priority mapping (P0>=80, P1>=60, P2>=40, else P3) must stay
	// in sync with scoreToPriority() in scoring.go.
	conditions := ""
	args := []interface{}{adjustment, cloudAccountID}
	argIndex := 3

	// Add match conditions
	if rule.MatchFingerprint != nil && *rule.MatchFingerprint != "" {
		conditions += fmt.Sprintf(" AND fingerprint = $%d", argIndex)
		args = append(args, *rule.MatchFingerprint)
		argIndex++
	}

	if rule.MatchAlertname != nil && *rule.MatchAlertname != "" {
		conditions += fmt.Sprintf(" AND aggregation_key ~ $%d", argIndex)
		args = append(args, *rule.MatchAlertname)
		argIndex++
	}

	if rule.MatchNamespace != nil && *rule.MatchNamespace != "" {
		conditions += fmt.Sprintf(" AND subject_namespace ~ $%d", argIndex)
		args = append(args, *rule.MatchNamespace)
		argIndex++
	}

	if rule.MatchService != nil && *rule.MatchService != "" {
		conditions += fmt.Sprintf(" AND subject_owner ~ $%d", argIndex)
		args = append(args, *rule.MatchService)
		argIndex++
	}

	if rule.MatchSource != nil && *rule.MatchSource != "" {
		conditions += fmt.Sprintf(" AND source = $%d", argIndex)
		args = append(args, *rule.MatchSource)
		argIndex++
	}

	if rule.MatchPriority != nil && *rule.MatchPriority != "" {
		conditions += fmt.Sprintf(" AND priority = $%d", argIndex)
		args = append(args, *rule.MatchPriority)
		argIndex++
	}

	if rule.MatchFindingType != nil && *rule.MatchFindingType != "" {
		conditions += fmt.Sprintf(" AND finding_type = $%d", argIndex)
		args = append(args, *rule.MatchFindingType)
		argIndex++
	}

	if rule.MatchLabels != nil && *rule.MatchLabels != "" {
		conditions += fmt.Sprintf(" AND labels @> $%d::jsonb", argIndex)
		args = append(args, *rule.MatchLabels)
	}

	query := fmt.Sprintf(`
		WITH new_scores AS (
			SELECT id,
			       LEAST(GREATEST(COALESCE(computed_score, 50) + $1, 0), 100) AS new_score
			FROM events
			WHERE cloud_account_id = $2
			  AND nb_status IN ('OPEN', 'ACKNOWLEDGED', 'INVESTIGATING')
			  %s
		)
		UPDATE events e
		SET computed_score = ns.new_score,
		    computed_priority = CASE
		        WHEN ns.new_score >= 80 THEN 'P0'
		        WHEN ns.new_score >= 60 THEN 'P1'
		        WHEN ns.new_score >= 40 THEN 'P2'
		        ELSE 'P3'
		    END,
		    updated_at = NOW()
		FROM new_scores ns
		WHERE e.id = ns.id
	`, conditions)

	// Execute the update
	result, err := db.ExecContext(ctx, query, args...)
	if err != nil {
		return 0, fmt.Errorf("failed to apply score adjustment to existing events: %w", err)
	}

	rowsAffected, _ := result.RowsAffected()

	slog.InfoContext(ctx, "Applied scoring rule to existing events",
		"rule_id", rule.ID,
		"events_updated", rowsAffected,
		"adjustment", adjustment,
	)

	return int(rowsAffected), nil
}

// UpdateTriageRule updates an existing triage rule
func UpdateTriageRule(ctx context.Context, db *sqlx.DB, req UpdateTriageRuleRequest, cloudAccountID, tenantID, userID string) (*TriageRule, error) {
	// Validate JSONB fields before update
	if err := validateJSONBField(req.MatchLabels, "match_labels"); err != nil {
		return nil, err
	}
	if err := validateJSONBField(req.ActionValue, "action_value"); err != nil {
		return nil, err
	}

	// First verify the rule exists and is editable
	var existingRule TriageRule
	checkQuery := `
		SELECT id, is_editable, account_id
		FROM event_triage_rules
		WHERE id = $1 AND account_id = $2
	`
	err := db.GetContext(ctx, &existingRule, checkQuery, req.RuleID, cloudAccountID)
	if err != nil {
		return nil, fmt.Errorf("rule not found or access denied")
	}

	if !existingRule.IsEditable {
		return nil, fmt.Errorf("rule is not editable")
	}

	priority := 200 // Default for account rules
	if req.Priority != nil {
		priority = *req.Priority
	}

	// Build update query
	query := `
		UPDATE event_triage_rules
		SET rule_type = $1,
		    match_source = $2,
		    match_alertname = $3,
		    match_namespace = $4,
		    match_service = $5,
		    match_fingerprint = $6,
		    match_labels = $7,
		    match_priority = $8,
		    match_finding_type = $9,
		    action = $10,
		    action_value = $11,
		    priority = $12,
		    effective_until = $13,
		    name = $14,
		    description = $15,
		    updated_by = $16,
		    apply_to_existing = $17,
		    updated_at = NOW()
		WHERE id = $18 AND account_id = $19 AND is_editable = TRUE
		RETURNING id, tenant_id, account_id, rule_type,
			match_source, match_alertname, match_namespace, match_service,
			match_fingerprint, match_labels, match_priority, match_finding_type,
			action, action_value, priority, is_editable, can_override,
			enabled, effective_from, effective_until,
			name, description, reason,
			created_by, updated_by, created_at, updated_at,
			match_count, last_matched_at,
			apply_to_existing
	`

	// Parse effective_until if provided
	var effectiveUntil *time.Time
	if req.EffectiveUntil != nil {
		t, err := time.Parse(time.RFC3339, *req.EffectiveUntil)
		if err == nil {
			effectiveUntil = &t
		}
	}

	var updatedRule TriageRule
	err = db.GetContext(ctx, &updatedRule, query,
		req.RuleType,
		req.MatchSource,
		req.MatchAlertname,
		req.MatchNamespace,
		req.MatchService,
		req.MatchFingerprint,
		req.MatchLabels,
		req.MatchPriority,
		req.MatchFindingType,
		req.Action,
		req.ActionValue,
		priority,
		effectiveUntil,
		req.Name,
		req.Description,
		userID,
		req.ApplyToExisting,
		req.RuleID,
		cloudAccountID,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to update rule: %w", err)
	}

	return &updatedRule, nil
}

// DeleteTriageRule deletes or disables a triage rule
func DeleteTriageRule(ctx context.Context, db *sqlx.DB, ruleID string, hardDelete bool, cloudAccountID, tenantID string) error {
	if hardDelete {
		query := `
			DELETE FROM event_triage_rules
			WHERE id = $1
			  AND account_id = $2
			  AND is_editable = TRUE
		`
		_, err := db.ExecContext(ctx, query, ruleID, cloudAccountID)
		return err
	}

	// Soft delete - just disable
	query := `
		UPDATE event_triage_rules
		SET enabled = FALSE,
		    updated_at = NOW()
		WHERE id = $1
		  AND account_id = $2
		  AND is_editable = TRUE
	`
	_, err := db.ExecContext(ctx, query, ruleID, cloudAccountID)
	return err
}

// -------------------- System Rule Override Functions --------------------

// ToggleSystemRuleOverride enables or disables a system rule for a specific account
func ToggleSystemRuleOverride(ctx context.Context, db *sqlx.DB, req ToggleSystemRuleOverrideRequest, cloudAccountID, tenantID string) (*ToggleSystemRuleOverrideResponse, error) {
	// Verify the rule exists and is a system rule (can_override = true)
	var rule TriageRule
	checkQuery := `
		SELECT id, tenant_id, account_id, can_override
		FROM event_triage_rules
		WHERE id = $1
	`
	err := db.GetContext(ctx, &rule, checkQuery, req.SystemRuleID)
	if err != nil {
		errMsg := "system rule not found"
		return &ToggleSystemRuleOverrideResponse{Success: false, Error: &errMsg}, nil
	}

	// Verify it's a system rule (tenant_id AND account_id are NULL)
	if rule.TenantID != nil || rule.AccountID != nil {
		errMsg := "rule is not a system rule"
		return &ToggleSystemRuleOverrideResponse{Success: false, Error: &errMsg}, nil
	}

	// Verify the rule can be overridden
	if !rule.CanOverride {
		errMsg := "system rule cannot be overridden"
		return &ToggleSystemRuleOverrideResponse{Success: false, Error: &errMsg}, nil
	}

	// Upsert the override record
	upsertQuery := `
		INSERT INTO event_triage_rule_overrides (system_rule_id, tenant_id, account_id, disabled, created_at, updated_at)
		VALUES ($1, $2, $3, $4, NOW(), NOW())
		ON CONFLICT (system_rule_id, account_id)
		DO UPDATE SET disabled = $4, updated_at = NOW()
	`
	_, err = db.ExecContext(ctx, upsertQuery, req.SystemRuleID, tenantID, cloudAccountID, req.Disabled)
	if err != nil {
		slog.ErrorContext(ctx, "Failed to toggle system rule override", "error", err, "rule_id", req.SystemRuleID)
		errMsg := "failed to update override"
		return &ToggleSystemRuleOverrideResponse{Success: false, Error: &errMsg}, nil
	}

	slog.InfoContext(ctx, "System rule override toggled",
		"rule_id", req.SystemRuleID,
		"account_id", cloudAccountID,
		"disabled", req.Disabled,
	)

	return &ToggleSystemRuleOverrideResponse{
		Success:      true,
		IsOverridden: req.Disabled,
	}, nil
}

// GetSystemRuleOverride returns the override status for a system rule and account
func GetSystemRuleOverride(ctx context.Context, db *sqlx.DB, systemRuleID, cloudAccountID string) (*TriageRuleOverride, error) {
	query := `
		SELECT id, system_rule_id, tenant_id, account_id, disabled, created_at, updated_at
		FROM event_triage_rule_overrides
		WHERE system_rule_id = $1 AND account_id = $2
	`

	var override TriageRuleOverride
	err := db.GetContext(ctx, &override, query, systemRuleID, cloudAccountID)
	if err != nil {
		return nil, err
	}

	return &override, nil
}
