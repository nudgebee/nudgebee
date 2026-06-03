package triage

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
)

// ClassifyPreview returns a preview of the impact of classifying an event
func ClassifyPreview(ctx context.Context, db *sqlx.DB, req ClassifyPreviewRequest, cloudAccountID, tenantID string) (*ClassifyPreviewResponse, error) {
	// 1. Load the event to get fingerprint and title
	event, err := loadEventByID(ctx, db, req.EventID, cloudAccountID)
	if err != nil {
		return nil, fmt.Errorf("failed to load event: %w", err)
	}

	// 2. Determine target status based on classification
	targetStatus := getTargetStatusForClassification(req.Classification, req.ApplyScope)

	// 3. Build current event preview
	currentEvent := CurrentEventPreview{
		ID:        event.ID,
		Title:     event.Title,
		NewStatus: targetStatus,
	}

	// 4. Count existing events that would be affected (only for fingerprint/time-limited scope)
	existingEvents := ExistingEventsPreview{
		Count:         0,
		SampleTitles:  []string{},
		WillBeUpdated: false,
	}

	if req.ApplyScope != ApplyScopeThisEvent && event.Fingerprint != nil {
		count, sampleTitles, err := countExistingEventsWithFingerprint(ctx, db, *event.Fingerprint, cloudAccountID, req.EventID, targetStatus)
		if err != nil {
			slog.WarnContext(ctx, "Failed to count existing events", "error", err)
		} else {
			existingEvents.Count = count
			existingEvents.SampleTitles = sampleTitles
			existingEvents.WillBeUpdated = count > 0
		}
	}

	// 5. Build future events preview
	futureEvents := FutureEventsPreview{
		RuleApplies:      false,
		ScopeDescription: "No automatic action on future events",
	}

	var ruleExpiresAt *time.Time
	if req.ApplyScope != ApplyScopeThisEvent {
		futureEvents.RuleApplies = true
		switch req.Classification {
		case ClassificationFalsePositive:
			if req.ApplyScope == ApplyScopeTimeLimited && req.ApplyUntilHours != nil {
				expiresAt := time.Now().UTC().Add(time.Duration(*req.ApplyUntilHours) * time.Hour)
				ruleExpiresAt = &expiresAt
				futureEvents.ScopeDescription = fmt.Sprintf("Will be auto-suppressed for %d hours", *req.ApplyUntilHours)
			} else {
				futureEvents.ScopeDescription = "Will be auto-suppressed permanently"
			}
		case ClassificationDuplicate:
			if req.ApplyUntilHours != nil {
				expiresAt := time.Now().UTC().Add(time.Duration(*req.ApplyUntilHours) * time.Hour)
				ruleExpiresAt = &expiresAt
				futureEvents.ScopeDescription = fmt.Sprintf("Will be auto-classified as duplicate for %d hours", *req.ApplyUntilHours)
			} else {
				futureEvents.ScopeDescription = "Will be auto-classified as duplicate"
			}
		default:
			futureEvents.ScopeDescription = "Rule will apply to future matching events"
		}
	}

	// 6. Build rule preview if rule will be created
	var rulePreview *TriageRulePreview
	if req.ApplyScope != ApplyScopeThisEvent && event.Fingerprint != nil {
		ruleType := RuleTypeSuppression
		action := ActionSuppress
		if req.Classification == ClassificationDuplicate {
			ruleType = RuleTypeClassification
			action = ActionAutoClassifyDuplicate
		}

		rulePreview = &TriageRulePreview{
			RuleType:      ruleType,
			MatchCriteria: fmt.Sprintf("fingerprint=%s", *event.Fingerprint),
			Action:        action,
			ExpiresAt:     ruleExpiresAt,
		}
	}

	return &ClassifyPreviewResponse{
		CurrentEvent:   currentEvent,
		ExistingEvents: existingEvents,
		FutureEvents:   futureEvents,
		RuleToCreate:   rulePreview,
	}, nil
}

// ClassifyEvent performs the actual classification of an event
func ClassifyEvent(ctx context.Context, db *sqlx.DB, req ClassifyEventRequest, cloudAccountID, tenantID, userID string) (*ClassifyEventResponse, error) {
	// 1. Validate request
	if err := validateClassifyRequest(req); err != nil {
		return nil, err
	}

	// 2. Validate confirmation for bulk operations
	if req.ApplyScope != ApplyScopeThisEvent && req.ApplyToExisting && !req.Confirmed {
		return nil, fmt.Errorf("bulk operation requires confirmed=true")
	}

	// 3. Load the event
	event, err := loadEventByID(ctx, db, req.EventID, cloudAccountID)
	if err != nil {
		return nil, fmt.Errorf("failed to load event: %w", err)
	}

	// 4. Create classification record
	classificationID := uuid.New().String()
	classification := EventClassification{
		ID:                classificationID,
		EventID:           req.EventID,
		CloudAccountID:    cloudAccountID,
		TenantID:          tenantID,
		Classification:    req.Classification,
		ReasonCode:        req.ReasonCode,
		ReasonText:        req.ReasonText,
		PriorityDirection: req.PriorityDirection,
		CorrectedPriority: req.CorrectedPriority,
		ApplyScope:        req.ApplyScope,
		LinkedEventID:     req.LinkedEventID,
		ClassifiedBy:      userID,
		ClassifiedAt:      time.Now().UTC(),
		OriginalScore:     event.ComputedScore,
	}

	// Set apply_until for time-limited scope
	if req.ApplyScope == ApplyScopeTimeLimited && req.ApplyUntilHours != nil {
		applyUntil := time.Now().UTC().Add(time.Duration(*req.ApplyUntilHours) * time.Hour)
		classification.ApplyUntil = &applyUntil
	}

	// 5. Begin transaction for core database operations
	tx, err := db.BeginTxx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer func() {
		if err != nil {
			if rbErr := tx.Rollback(); rbErr != nil {
				slog.ErrorContext(ctx, "Failed to rollback transaction", "error", rbErr)
			}
		}
	}()

	// 6. Insert classification record (within transaction)
	if err = insertClassificationTx(ctx, tx, &classification); err != nil {
		return nil, fmt.Errorf("failed to insert classification: %w", err)
	}

	// 7. Determine target status and update event (within transaction)
	targetStatus := getTargetStatusForClassification(req.Classification, req.ApplyScope)
	if err = updateEventNBStatusTx(ctx, tx, req.EventID, targetStatus, userID); err != nil {
		return nil, fmt.Errorf("failed to update event status: %w", err)
	}

	// 8. Sync computed_score/computed_priority when priority is manually corrected
	if req.CorrectedPriority != nil {
		newScore := priorityToMinScore(*req.CorrectedPriority)
		if err = updateEventScoreTx(ctx, tx, req.EventID, newScore, *req.CorrectedPriority); err != nil {
			return nil, fmt.Errorf("failed to update event score for corrected priority: %w", err)
		}
		slog.InfoContext(ctx, "Updated event score for manual priority correction",
			"event_id", req.EventID,
			"corrected_priority", *req.CorrectedPriority,
			"new_score", newScore,
		)
	}

	// 9. Add duplicate label if classification is duplicate (within transaction)
	if req.Classification == ClassificationDuplicate && req.LinkedEventID != nil {
		if err = addDuplicateLabelTx(ctx, tx, req.EventID, *req.LinkedEventID); err != nil {
			return nil, fmt.Errorf("failed to add duplicate label: %w", err)
		}
	}

	// 10. Create triage rule if scope is not this_event (within transaction)
	var ruleID *string
	var ruleExpiresAt *time.Time
	if req.ApplyScope != ApplyScopeThisEvent && event.Fingerprint != nil {
		rule, ruleErr := createRuleFromClassificationTx(ctx, tx, &classification, event, userID)
		if ruleErr != nil {
			err = ruleErr
			return nil, fmt.Errorf("failed to create triage rule: %w", err)
		}
		ruleID = &rule.ID
		ruleExpiresAt = rule.EffectiveUntil
	}

	// 11. Commit transaction
	if err = tx.Commit(); err != nil {
		return nil, fmt.Errorf("failed to commit transaction: %w", err)
	}

	// 12. Queue bulk update for existing events (async, outside transaction)
	var bulkOp *BulkOperationResponse
	if req.ApplyToExisting && req.ApplyScope != ApplyScopeThisEvent && event.Fingerprint != nil {
		targetStatus := getTargetStatusForClassification(req.Classification, req.ApplyScope)
		bulkOperation, bulkErr := queueBulkClassification(ctx, db, BulkClassificationJob{
			Fingerprint:      *event.Fingerprint,
			AccountID:        cloudAccountID,
			NewStatus:        targetStatus,
			Classification:   req.Classification,
			RuleID:           ruleID,
			ClassificationID: &classificationID,
			ExcludeEventID:   req.EventID,
			CreatedBy:        &userID,
		})
		if bulkErr != nil {
			slog.ErrorContext(ctx, "Failed to queue bulk classification", "error", bulkErr)
		} else if bulkOperation != nil {
			bulkOp = &BulkOperationResponse{
				JobID:          bulkOperation.ID,
				EventsToUpdate: bulkOperation.TotalEvents,
				Status:         bulkOperation.Status,
			}
		}
	}

	// 13. Log to event_history (outside transaction, non-critical)
	logClassificationToHistory(ctx, db, req.EventID, cloudAccountID, tenantID, userID, req.Classification)

	slog.InfoContext(ctx, "Event classified",
		"event_id", req.EventID,
		"classification", req.Classification,
		"apply_scope", req.ApplyScope,
		"rule_created", ruleID != nil,
	)

	return &ClassifyEventResponse{
		Success:          true,
		ClassificationID: classificationID,
		RuleCreated:      ruleID != nil,
		RuleID:           ruleID,
		RuleExpiresAt:    ruleExpiresAt,
		BulkOperation:    bulkOp,
	}, nil
}

// GetDuplicateSuggestions returns suggested original events for duplicate classification
func GetDuplicateSuggestions(ctx context.Context, db *sqlx.DB, eventID, cloudAccountID string) ([]DuplicateSuggestion, error) {
	// Single optimized query that gets fingerprint and suggestions in one round trip
	query := `
		WITH current_event AS (
			SELECT fingerprint
			FROM events
			WHERE id = $1 AND cloud_account_id = $2
		),
		first_event AS (
			SELECT id, starts_at
			FROM events e, current_event c
			WHERE e.fingerprint = c.fingerprint
			  AND e.cloud_account_id = $2
			ORDER BY e.starts_at ASC
			LIMIT 1
		)
		SELECT
			e.id,
			e.title,
			e.starts_at,
			CASE WHEN e.id = f.id THEN 1 ELSE 2 END as occurrence_number,
			(e.id = f.id) as is_first
		FROM events e
		INNER JOIN current_event c ON e.fingerprint = c.fingerprint
		CROSS JOIN first_event f
		WHERE e.cloud_account_id = $2
		  AND e.id != $1
		ORDER BY
			CASE WHEN e.id = f.id THEN 0 ELSE 1 END,
			e.starts_at ASC
		LIMIT 10
	`

	var suggestions []DuplicateSuggestion
	err := db.SelectContext(ctx, &suggestions, query, eventID, cloudAccountID)
	if err != nil {
		return nil, fmt.Errorf("failed to query suggestions: %w", err)
	}

	return suggestions, nil
}

// -------------------- Helper Functions --------------------

// eventBasicInfo holds basic event info for internal use
type eventBasicInfo struct {
	ID             string  `db:"id"`
	Title          string  `db:"title"`
	Fingerprint    *string `db:"fingerprint"`
	ComputedScore  *int    `db:"computed_score"`
	CloudAccountID string  `db:"cloud_account_id"`
	TenantID       string  `db:"tenant"`
}

// loadEventByID loads basic event info by ID
func loadEventByID(ctx context.Context, db *sqlx.DB, eventID, cloudAccountID string) (*eventBasicInfo, error) {
	query := `
		SELECT id, title, fingerprint, computed_score, cloud_account_id, tenant
		FROM events
		WHERE id = $1 AND cloud_account_id = $2
	`

	var event eventBasicInfo
	err := db.GetContext(ctx, &event, query, eventID, cloudAccountID)
	if err != nil {
		return nil, err
	}

	return &event, nil
}

// countExistingEventsWithFingerprint counts events with same fingerprint that can be affected
// and returns up to 5 sample titles, all in a single query.
// If excludeStatus is non-empty, events already in that status are excluded (they don't need updating).
func countExistingEventsWithFingerprint(ctx context.Context, db *sqlx.DB, fingerprint, cloudAccountID, excludeEventID, excludeStatus string) (int, []string, error) {
	var count int
	var sampleTitles []string

	baseWhere := `fingerprint = $1 AND cloud_account_id = $2 AND id != $3`
	args := []interface{}{fingerprint, cloudAccountID, excludeEventID}
	if excludeStatus != "" {
		baseWhere += ` AND nb_status IS DISTINCT FROM $4`
		args = append(args, excludeStatus)
	}

	countQuery := `SELECT COUNT(*) FROM events WHERE ` + baseWhere
	err := db.GetContext(ctx, &count, countQuery, args...)
	if err != nil {
		return 0, nil, err
	}

	sampleQuery := `SELECT title FROM events WHERE ` + baseWhere + ` ORDER BY starts_at DESC LIMIT 5`
	err = db.SelectContext(ctx, &sampleTitles, sampleQuery, args...)
	if err != nil {
		slog.WarnContext(ctx, "Failed to get sample titles", "error", err)
		sampleTitles = []string{}
	}

	return count, sampleTitles, nil
}

// getTargetStatusForClassification determines target nb_status based on classification and scope
func getTargetStatusForClassification(classification, applyScope string) string {
	// Determine status based on classification type
	switch classification {
	case ClassificationFalsePositive:
		return NBStatusSuppressed
	case ClassificationDuplicate:
		return NBStatusDuplicate
	case ClassificationTruePositive:
		// True Positive = confirmed real issue that needs fix
		return NBStatusActionRequired
	case ClassificationBenignPositive:
		// Benign Positive = real but expected/acceptable, no action needed
		return NBStatusResolved
	default:
		return NBStatusResolved
	}
}

// validateClassifyRequest validates the classification request
func validateClassifyRequest(req ClassifyEventRequest) error {
	if req.EventID == "" {
		return fmt.Errorf("event_id is required")
	}

	validClassifications := []string{
		ClassificationTruePositive,
		ClassificationFalsePositive,
		ClassificationBenignPositive,
		ClassificationDuplicate,
	}
	if !contains(validClassifications, req.Classification) {
		return fmt.Errorf("invalid classification: %s", req.Classification)
	}

	validScopes := []string{ApplyScopeThisEvent, ApplyScopeThisFingerprint, ApplyScopeTimeLimited}
	if !contains(validScopes, req.ApplyScope) {
		return fmt.Errorf("invalid apply_scope: %s", req.ApplyScope)
	}

	// Validate reason code
	validCodes, ok := ValidReasonCodes[req.Classification]
	if !ok || !contains(validCodes, req.ReasonCode) {
		return fmt.Errorf("invalid reason_code '%s' for classification '%s'", req.ReasonCode, req.Classification)
	}

	// Duplicate requires linked_event_id
	if req.Classification == ClassificationDuplicate && (req.LinkedEventID == nil || *req.LinkedEventID == "") {
		return fmt.Errorf("linked_event_id is required for duplicate classification")
	}

	// Time-limited requires apply_until_hours
	if req.ApplyScope == ApplyScopeTimeLimited {
		if req.ApplyUntilHours == nil || *req.ApplyUntilHours <= 0 {
			return fmt.Errorf("apply_until_hours is required for time_limited scope")
		}
		if *req.ApplyUntilHours > 720 {
			return fmt.Errorf("apply_until_hours must be between 1 and 720")
		}
	}

	return nil
}

// insertClassification inserts a classification record
func insertClassification(ctx context.Context, db *sqlx.DB, c *EventClassification) error {
	query := `
		INSERT INTO event_classification (
			id, event_id, cloud_account_id, tenant_id,
			classification, original_priority, corrected_priority, priority_direction,
			reason_code, reason_text, apply_scope, apply_until,
			linked_event_id, classified_by, classified_at, original_score, rule_id
		) VALUES (
			$1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17
		)
		ON CONFLICT (event_id, cloud_account_id) DO UPDATE SET
			classification = EXCLUDED.classification,
			reason_code = EXCLUDED.reason_code,
			reason_text = EXCLUDED.reason_text,
			apply_scope = EXCLUDED.apply_scope,
			apply_until = EXCLUDED.apply_until,
			linked_event_id = EXCLUDED.linked_event_id,
			classified_by = EXCLUDED.classified_by,
			classified_at = EXCLUDED.classified_at,
			rule_id = EXCLUDED.rule_id
	`

	_, err := db.ExecContext(ctx, query,
		c.ID, c.EventID, c.CloudAccountID, c.TenantID,
		c.Classification, c.OriginalPriority, c.CorrectedPriority, c.PriorityDirection,
		c.ReasonCode, c.ReasonText, c.ApplyScope, c.ApplyUntil,
		c.LinkedEventID, c.ClassifiedBy, c.ClassifiedAt, c.OriginalScore, c.RuleID,
	)

	return err
}

// insertClassificationTx inserts a classification record within a transaction
func insertClassificationTx(ctx context.Context, tx *sqlx.Tx, c *EventClassification) error {
	query := `
		INSERT INTO event_classification (
			id, event_id, cloud_account_id, tenant_id,
			classification, original_priority, corrected_priority, priority_direction,
			reason_code, reason_text, apply_scope, apply_until,
			linked_event_id, classified_by, classified_at, original_score, rule_id
		) VALUES (
			$1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17
		)
		ON CONFLICT (event_id, cloud_account_id) DO UPDATE SET
			classification = EXCLUDED.classification,
			reason_code = EXCLUDED.reason_code,
			reason_text = EXCLUDED.reason_text,
			apply_scope = EXCLUDED.apply_scope,
			apply_until = EXCLUDED.apply_until,
			linked_event_id = EXCLUDED.linked_event_id,
			classified_by = EXCLUDED.classified_by,
			classified_at = EXCLUDED.classified_at,
			rule_id = EXCLUDED.rule_id
	`

	_, err := tx.ExecContext(ctx, query,
		c.ID, c.EventID, c.CloudAccountID, c.TenantID,
		c.Classification, c.OriginalPriority, c.CorrectedPriority, c.PriorityDirection,
		c.ReasonCode, c.ReasonText, c.ApplyScope, c.ApplyUntil,
		c.LinkedEventID, c.ClassifiedBy, c.ClassifiedAt, c.OriginalScore, c.RuleID,
	)

	return err
}

// updateEventScoreTx updates computed_score and computed_priority within a transaction.
// Used when a user manually corrects the priority so that score stays in sync.
// Also sets score_factors to indicate this was a manual correction and score_confidence to 1.0.
func updateEventScoreTx(ctx context.Context, tx *sqlx.Tx, eventID string, score int, priority string) error {
	query := `
		UPDATE events
		SET computed_score = $1,
		    computed_priority = $2,
		    score_factors = jsonb_build_object('manual_correction', true, 'corrected_priority', $2, 'corrected_score', $1),
		    score_confidence = 1.0,
		    updated_at = NOW()
		WHERE id = $3
	`

	_, err := tx.ExecContext(ctx, query, score, priority, eventID)
	return err
}

// updateEventNBStatusTx updates the event's nb_status within a transaction
func updateEventNBStatusTx(ctx context.Context, tx *sqlx.Tx, eventID, nbStatus, userID string) error {
	query := `
		UPDATE events
		SET nb_status = $1,
		    nb_status_changed_at = NOW(),
		    nb_status_changed_by = $2,
		    updated_at = NOW()
		WHERE id = $3
	`

	_, err := tx.ExecContext(ctx, query, nbStatus, userID, eventID)
	return err
}

// addDuplicateLabel adds nb_duplicate_of label to event
func addDuplicateLabel(ctx context.Context, db *sqlx.DB, eventID, linkedEventID string) error {
	query := `
		UPDATE events
		SET labels = COALESCE(labels, '{}'::jsonb) || jsonb_build_object('nb_duplicate_of', $1::text),
		    updated_at = NOW()
		WHERE id = $2
	`

	_, err := db.ExecContext(ctx, query, linkedEventID, eventID)
	return err
}

// AddTriageRuleLabel adds nb_triage_rule_id label to event to track which rule updated it
func AddTriageRuleLabel(ctx context.Context, db *sqlx.DB, eventID, ruleID string) error {
	query := `
		UPDATE events
		SET labels = COALESCE(labels, '{}'::jsonb) || jsonb_build_object('nb_triage_rule_id', $1::text),
		    updated_at = NOW()
		WHERE id = $2
	`

	_, err := db.ExecContext(ctx, query, ruleID, eventID)
	return err
}

// addDuplicateLabelTx adds nb_duplicate_of label to event within a transaction
func addDuplicateLabelTx(ctx context.Context, tx *sqlx.Tx, eventID, linkedEventID string) error {
	query := `
		UPDATE events
		SET labels = COALESCE(labels, '{}'::jsonb) || jsonb_build_object('nb_duplicate_of', $1::text),
		    updated_at = NOW()
		WHERE id = $2
	`

	_, err := tx.ExecContext(ctx, query, linkedEventID, eventID)
	return err
}

// getFirstEventIDFromChainTx looks up the TRUE first event for a fingerprint from event_duplicates (within transaction)
func getFirstEventIDFromChainTx(ctx context.Context, tx *sqlx.Tx, fingerprint, cloudAccountID string) string {
	query := `
		SELECT first_event_id
		FROM event_duplicates
		WHERE fingerprint = $1
		  AND cloud_account_id = $2
		  AND occurrence_number = 1
		LIMIT 1
	`

	var firstEventID string
	err := tx.GetContext(ctx, &firstEventID, query, fingerprint, cloudAccountID)
	if err != nil {
		slog.DebugContext(ctx, "No duplicate chain found for fingerprint",
			"fingerprint", fingerprint,
			"error", err,
		)
		return ""
	}

	return firstEventID
}

// insertTriageRule inserts a triage rule
func insertTriageRule(ctx context.Context, db *sqlx.DB, r *TriageRule) error {
	query := `
		INSERT INTO event_triage_rules (
			id, tenant_id, account_id, rule_type,
			match_source, match_alertname, match_namespace, match_service,
			match_fingerprint, match_labels, match_priority, match_finding_type,
			action, action_value, priority, is_editable, can_override,
			enabled, effective_from, effective_until,
			name, description, reason,
			created_by, created_at, updated_at,
			apply_to_existing
		) VALUES (
			$1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12,
			$13, $14, $15, $16, $17, $18, $19, $20, $21, $22, $23, $24, $25, $26,
			$27
		)
	`

	_, err := db.ExecContext(ctx, query,
		r.ID, r.TenantID, r.AccountID, r.RuleType,
		r.MatchSource, r.MatchAlertname, r.MatchNamespace, r.MatchService,
		r.MatchFingerprint, r.MatchLabels, r.MatchPriority, r.MatchFindingType,
		r.Action, r.ActionValue, r.Priority, r.IsEditable, r.CanOverride,
		r.Enabled, r.EffectiveFrom, r.EffectiveUntil,
		r.Name, r.Description, r.Reason,
		r.CreatedBy, r.CreatedAt, r.UpdatedAt,
		r.ApplyToExisting,
	)

	return err
}

// createRuleFromClassificationTx creates a triage rule based on classification within a transaction
func createRuleFromClassificationTx(ctx context.Context, tx *sqlx.Tx, c *EventClassification, event *eventBasicInfo, userID string) (*TriageRule, error) {
	ruleID := uuid.New().String()

	var ruleType, action string
	var actionValue *string

	switch c.Classification {
	case ClassificationFalsePositive:
		ruleType = RuleTypeSuppression
		action = ActionSuppress
	case ClassificationDuplicate:
		ruleType = RuleTypeClassification
		action = ActionAutoClassifyDuplicate

		// Look up the TRUE first event from the duplicate chain
		// This ensures nb_duplicate_of always points to the original event
		linkedEventID := c.LinkedEventID
		if event.Fingerprint != nil {
			firstEventID := getFirstEventIDFromChainTx(ctx, tx, *event.Fingerprint, c.CloudAccountID)
			if firstEventID != "" {
				linkedEventID = &firstEventID
				slog.InfoContext(ctx, "Using true first event for duplicate rule",
					"original_linked_id", c.LinkedEventID,
					"first_event_id", firstEventID,
				)
			}
		}

		// Build action_value JSON with the TRUE first event
		av := map[string]interface{}{
			"linked_event_id": linkedEventID,
			"classification":  ClassificationDuplicate,
			"reason_code":     c.ReasonCode,
		}
		avBytes, _ := json.Marshal(av)
		avStr := string(avBytes)
		actionValue = &avStr
	default:
		return nil, fmt.Errorf("no rule needed for classification: %s", c.Classification)
	}

	rule := &TriageRule{
		ID:               ruleID,
		TenantID:         &c.TenantID,
		AccountID:        &c.CloudAccountID,
		RuleType:         ruleType,
		MatchFingerprint: event.Fingerprint,
		Action:           action,
		ActionValue:      actionValue,
		Priority:         200, // Account-level rules start at 200
		IsEditable:       true,
		CanOverride:      true,
		Enabled:          true,
		EffectiveUntil:   c.ApplyUntil,
		CreatedBy:        &userID,
		CreatedAt:        time.Now().UTC(),
		UpdatedAt:        time.Now().UTC(),
	}

	// Set name
	name := fmt.Sprintf("Auto-%s: %s", c.Classification, event.Title)
	if len(name) > 255 {
		name = name[:255]
	}
	rule.Name = &name

	if err := insertTriageRuleTx(ctx, tx, rule); err != nil {
		return nil, err
	}

	slog.InfoContext(ctx, "Created triage rule from classification",
		"rule_id", ruleID,
		"rule_type", ruleType,
		"action", action,
		"fingerprint", event.Fingerprint,
	)

	return rule, nil
}

// insertTriageRuleTx inserts a triage rule within a transaction
func insertTriageRuleTx(ctx context.Context, tx *sqlx.Tx, r *TriageRule) error {
	query := `
		INSERT INTO event_triage_rules (
			id, tenant_id, account_id, rule_type,
			match_source, match_alertname, match_namespace, match_service,
			match_fingerprint, match_labels, match_priority, match_finding_type,
			action, action_value, priority, is_editable, can_override,
			enabled, effective_from, effective_until,
			name, description, reason,
			created_by, created_at, updated_at,
			apply_to_existing
		) VALUES (
			$1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12,
			$13, $14, $15, $16, $17, $18, $19, $20, $21, $22, $23, $24, $25, $26,
			$27
		)
	`

	_, err := tx.ExecContext(ctx, query,
		r.ID, r.TenantID, r.AccountID, r.RuleType,
		r.MatchSource, r.MatchAlertname, r.MatchNamespace, r.MatchService,
		r.MatchFingerprint, r.MatchLabels, r.MatchPriority, r.MatchFindingType,
		r.Action, r.ActionValue, r.Priority, r.IsEditable, r.CanOverride,
		r.Enabled, r.EffectiveFrom, r.EffectiveUntil,
		r.Name, r.Description, r.Reason,
		r.CreatedBy, r.CreatedAt, r.UpdatedAt,
		r.ApplyToExisting,
	)

	return err
}

// logClassificationToHistory logs the classification to event_history
func logClassificationToHistory(ctx context.Context, db *sqlx.DB, eventID, cloudAccountID, tenantID, userID, classification string) {
	query := `
		INSERT INTO event_history (
			id, event_id, cloud_account_id, tenant_id,
			change_type, changed_by, new_value, change_reason
		) VALUES (
			$1, $2, $3, $4, $5, $6, $7, $8
		)
	`

	historyID := uuid.New().String()
	changeType := "labels" // Using 'labels' as it's an allowed value

	// Safely marshal JSON
	newValueData := map[string]string{"classification": classification}
	newValueJSON, err := json.Marshal(newValueData)
	if err != nil {
		slog.WarnContext(ctx, "Failed to marshal classification value", "error", err)
		return
	}

	_, err = db.ExecContext(ctx, query, historyID, eventID, cloudAccountID, tenantID, changeType, userID, string(newValueJSON), fmt.Sprintf("classified_as_%s", classification))
	if err != nil {
		slog.WarnContext(ctx, "Failed to log to event_history", "error", err)
	}
}

// contains checks if a string is in a slice
func contains(slice []string, item string) bool {
	for _, s := range slice {
		if s == item {
			return true
		}
	}
	return false
}

// GetEventClassification retrieves the classification for an event
func GetEventClassification(ctx context.Context, db *sqlx.DB, eventID, cloudAccountID string) (*EventClassification, error) {
	query := `
		SELECT
			id, event_id, cloud_account_id, tenant_id,
			classification, original_priority, corrected_priority, priority_direction,
			reason_code, reason_text, apply_scope, apply_until,
			linked_event_id, classified_by, classified_at, original_score
		FROM event_classification
		WHERE event_id = $1
		  AND cloud_account_id = $2
	`

	var c EventClassification
	err := db.GetContext(ctx, &c, query, eventID, cloudAccountID)
	if err != nil {
		return nil, err
	}

	return &c, nil
}

// UpdateNBStatus updates the nb_status of an event (public API function)
func UpdateNBStatus(ctx context.Context, db *sqlx.DB, req UpdateNBStatusRequest, cloudAccountID, userID string) (*UpdateNBStatusResponse, error) {
	// Validate nb_status
	validStatuses := []string{
		NBStatusOpen, NBStatusAcknowledged, NBStatusInvestigating, NBStatusActionRequired,
		NBStatusSnoozed, NBStatusSuppressed, NBStatusDropped, NBStatusDuplicate, NBStatusResolved,
	}
	if !contains(validStatuses, req.NBStatus) {
		return nil, fmt.Errorf("invalid nb_status: %s", req.NBStatus)
	}

	// Get current status
	var prevStatus string
	err := db.GetContext(ctx, &prevStatus, "SELECT nb_status FROM events WHERE id = $1 AND cloud_account_id = $2", req.EventID, cloudAccountID)
	if err != nil {
		return nil, fmt.Errorf("event not found: %w", err)
	}

	// Update the status
	var query string
	if req.NBStatus == NBStatusSnoozed && req.SnoozedUntil != nil {
		query = `
			UPDATE events
			SET nb_status = $1,
			    nb_status_changed_at = NOW(),
			    nb_status_changed_by = $2,
			    snoozed_until = $3,
			    updated_at = NOW()
			WHERE id = $4
			  AND cloud_account_id = $5
		`
		_, err = db.ExecContext(ctx, query, req.NBStatus, userID, req.SnoozedUntil, req.EventID, cloudAccountID)
	} else {
		query = `
			UPDATE events
			SET nb_status = $1,
			    nb_status_changed_at = NOW(),
			    nb_status_changed_by = $2,
			    snoozed_until = NULL,
			    updated_at = NOW()
			WHERE id = $3
			  AND cloud_account_id = $4
		`
		_, err = db.ExecContext(ctx, query, req.NBStatus, userID, req.EventID, cloudAccountID)
	}

	if err != nil {
		return nil, fmt.Errorf("failed to update nb_status: %w", err)
	}

	// Log to event_history
	go logStatusChangeToHistory(ctx, db, req.EventID, cloudAccountID, userID, prevStatus, req.NBStatus)

	return &UpdateNBStatusResponse{
		Success:    true,
		PrevStatus: prevStatus,
		NewStatus:  req.NBStatus,
	}, nil
}

// logStatusChangeToHistory logs status changes to event_history
func logStatusChangeToHistory(ctx context.Context, db *sqlx.DB, eventID, cloudAccountID, userID, prevStatus, newStatus string) {
	// Get tenant_id for the event
	var tenantID string
	err := db.GetContext(ctx, &tenantID, "SELECT tenant FROM events WHERE id = $1", eventID)
	if err != nil {
		slog.WarnContext(ctx, "Failed to get tenant for event history", "error", err)
		return
	}

	query := `
		INSERT INTO event_history (
			id, event_id, cloud_account_id, tenant_id,
			change_type, changed_by, old_value, new_value, change_reason
		) VALUES (
			$1, $2, $3, $4, $5, $6, $7, $8, $9
		)
	`

	historyID := uuid.New().String()
	changeType := "status" // Using 'status' as it's an allowed value
	oldValue, _ := json.Marshal(map[string]string{"nb_status": prevStatus})
	newValue, _ := json.Marshal(map[string]string{"nb_status": newStatus})

	_, err = db.ExecContext(ctx, query, historyID, eventID, cloudAccountID, tenantID, changeType, userID, string(oldValue), string(newValue), "manual_status_update")
	if err != nil {
		slog.WarnContext(ctx, "Failed to log status change to history", "error", err)
	}
}

// TriageRuleEvent represents an event matched by a triage rule
type TriageRuleEvent struct {
	ID               string  `json:"id" db:"id"`
	AccountID        string  `json:"account_id" db:"account_id"`
	Title            string  `json:"title" db:"title"`
	SubjectName      *string `json:"subject_name,omitempty" db:"subject_name"`
	SubjectNamespace *string `json:"subject_namespace,omitempty" db:"subject_namespace"`
	SubjectType      *string `json:"subject_type,omitempty" db:"subject_type"`
	Priority         *string `json:"priority,omitempty" db:"priority"`
	Status           *string `json:"status,omitempty" db:"status"`
	NBStatus         *string `json:"nb_status,omitempty" db:"nb_status"`
	StartsAt         *string `json:"starts_at,omitempty" db:"starts_at"`
	ClassifiedAt     string  `json:"classified_at" db:"classified_at"`
	Classification   string  `json:"classification" db:"classification"`
}

// GetEventsForTriageRule returns events matched by a specific triage rule.
// Uses event_triage_rule_matches as the single source of truth for all rule types.
// If accountID is provided, filters to only that account (used for system rules viewed in account context).
func GetEventsForTriageRule(ctx context.Context, db *sqlx.DB, ruleID, tenantID string, accountID *string, limit, offset int, startDate, endDate *time.Time) ([]TriageRuleEvent, int, error) {
	// Build WHERE clause against the unified rule matches table
	whereClause := "m.rule_id = $1 AND m.tenant_id = $2"
	args := []interface{}{ruleID, tenantID}
	argIdx := 3

	// Filter by account if provided (for system rules viewed in account context)
	if accountID != nil && *accountID != "" {
		whereClause += fmt.Sprintf(" AND m.cloud_account_id = $%d", argIdx)
		args = append(args, *accountID)
		argIdx++
	}

	if startDate != nil {
		whereClause += fmt.Sprintf(" AND m.matched_at >= $%d", argIdx)
		args = append(args, *startDate)
		argIdx++
	}
	if endDate != nil {
		whereClause += fmt.Sprintf(" AND m.matched_at <= $%d", argIdx)
		args = append(args, *endDate)
		argIdx++
	}

	// Count query
	countQuery := fmt.Sprintf(`
		SELECT COUNT(*)
		FROM event_triage_rule_matches m
		WHERE %s
	`, whereClause)

	var total int
	err := db.GetContext(ctx, &total, countQuery, args...)
	if err != nil {
		return nil, 0, fmt.Errorf("failed to count events: %w", err)
	}

	// Main query with event details
	query := fmt.Sprintf(`
		SELECT
			e.id,
			e.cloud_account_id as account_id,
			e.title,
			e.subject_name,
			e.subject_namespace,
			e.subject_type,
			e.priority,
			e.status,
			e.nb_status,
			TO_CHAR(e.starts_at, 'YYYY-MM-DD"T"HH24:MI:SS"Z"') as starts_at,
			TO_CHAR(m.matched_at, 'YYYY-MM-DD"T"HH24:MI:SS"Z"') as classified_at,
			m.action as classification
		FROM event_triage_rule_matches m
		JOIN events e ON e.id = m.event_id
		WHERE %s
		ORDER BY m.matched_at DESC
		LIMIT $%d OFFSET $%d
	`, whereClause, argIdx, argIdx+1)

	args = append(args, limit, offset)

	var events []TriageRuleEvent
	err = db.SelectContext(ctx, &events, query, args...)
	if err != nil {
		return nil, 0, fmt.Errorf("failed to get events: %w", err)
	}

	return events, total, nil
}
