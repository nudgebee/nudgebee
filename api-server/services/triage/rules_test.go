package triage

import (
	"context"
	"encoding/json"
	"testing"

	"nudgebee/services/internal/database"
	"nudgebee/services/internal/database/models"
	"nudgebee/services/internal/testenv"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMatchesRule(t *testing.T) {
	// Create test event
	fingerprint := "test-fingerprint-abc123"
	title := "API Failures - POST /webhook/{uuid} (409)"
	// Alertname rules match against aggregation_key (see MatchesRule / the SQL
	// query builder), not the event title.
	aggregationKey := "API Failures - POST /webhook/{uuid} (409)"
	namespace := "nudgebee"
	source := "prometheus"
	serviceName := "workflow-server"

	event := &models.Event{
		Id:               "test-event-id",
		Fingerprint:      &fingerprint,
		Title:            title,
		AggregationKey:   &aggregationKey,
		SubjectNamespace: &namespace,
		Source:           &source,
		SubjectOwner:     &serviceName,
	}

	testCases := []struct {
		name        string
		rule        TriageRule
		expectMatch bool
	}{
		{
			name: "match by fingerprint",
			rule: TriageRule{
				MatchFingerprint: &fingerprint,
			},
			expectMatch: true,
		},
		{
			name: "no match - wrong fingerprint",
			rule: TriageRule{
				MatchFingerprint: strPtr("different-fingerprint"),
			},
			expectMatch: false,
		},
		{
			name: "match by alertname regex",
			rule: TriageRule{
				MatchAlertname: strPtr("API Failures.*"),
			},
			expectMatch: true,
		},
		{
			name: "no match - wrong alertname",
			rule: TriageRule{
				MatchAlertname: strPtr("^Database.*"),
			},
			expectMatch: false,
		},
		{
			name: "match by namespace",
			rule: TriageRule{
				MatchNamespace: strPtr("nudgebee"),
			},
			expectMatch: true,
		},
		{
			name: "match by source",
			rule: TriageRule{
				MatchSource: strPtr("prometheus"),
			},
			expectMatch: true,
		},
		{
			name: "match by service regex",
			rule: TriageRule{
				MatchService: strPtr("workflow-.*"),
			},
			expectMatch: true,
		},
		{
			name: "match all criteria (AND logic)",
			rule: TriageRule{
				MatchFingerprint: &fingerprint,
				MatchNamespace:   strPtr("nudgebee"),
				MatchSource:      strPtr("prometheus"),
			},
			expectMatch: true,
		},
		{
			name: "no match - one criterion fails (AND logic)",
			rule: TriageRule{
				MatchFingerprint: &fingerprint,
				MatchNamespace:   strPtr("different-namespace"),
				MatchSource:      strPtr("prometheus"),
			},
			expectMatch: false,
		},
		{
			name: "match with nil event fields",
			rule: TriageRule{
				MatchFingerprint: &fingerprint,
				// MatchPriority is set but event.Priority is nil
				MatchPriority: strPtr("HIGH"),
			},
			expectMatch: false, // Priority doesn't match because event.Priority is nil
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			result := MatchesRule(event, &tc.rule)
			assert.Equal(t, tc.expectMatch, result, "Match result mismatch")
		})
	}
}

func TestMatchesLabels(t *testing.T) {
	testCases := []struct {
		name        string
		eventLabels string
		ruleLabels  string
		expectMatch bool
	}{
		{
			name:        "exact match single label",
			eventLabels: `{"env": "prod", "team": "platform"}`,
			ruleLabels:  `{"env": "prod"}`,
			expectMatch: true,
		},
		{
			name:        "match multiple labels",
			eventLabels: `{"env": "prod", "team": "platform", "region": "us-east-1"}`,
			ruleLabels:  `{"env": "prod", "team": "platform"}`,
			expectMatch: true,
		},
		{
			name:        "no match - value differs",
			eventLabels: `{"env": "staging", "team": "platform"}`,
			ruleLabels:  `{"env": "prod"}`,
			expectMatch: false,
		},
		{
			name:        "no match - label missing",
			eventLabels: `{"team": "platform"}`,
			ruleLabels:  `{"env": "prod"}`,
			expectMatch: false,
		},
		{
			name:        "empty rule labels matches all",
			eventLabels: `{"env": "prod"}`,
			ruleLabels:  `{}`,
			expectMatch: true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			eventLabels := &models.Json{}
			err := json.Unmarshal([]byte(tc.eventLabels), eventLabels)
			require.NoError(t, err)

			result := matchesLabels(eventLabels, tc.ruleLabels)
			assert.Equal(t, tc.expectMatch, result)
		})
	}
}

func TestCheckTriageRules(t *testing.T) {
	ctx := context.Background()

	// Get database connection
	testenv.RequireMetastore(t)
	dbManager, err := database.GetDatabaseManager(database.Metastore)
	require.NoError(t, err, "Failed to get database manager")

	db := dbManager.Db

	// Fetch a real event to test with
	eventID := testenv.RequireEnv(t, "TEST_EVENT_ID")["TEST_EVENT_ID"]

	var event models.Event
	query := `
		SELECT id, title, fingerprint, source, priority, finding_type,
		       subject_name, subject_namespace, subject_owner,
		       cloud_account_id, tenant, labels
		FROM events
		WHERE id = $1
	`
	err = db.GetContext(ctx, &event, query, eventID)
	if err != nil {
		t.Skipf("Event %s not found, skipping", eventID)
		return
	}

	t.Logf("=== TEST EVENT ===")
	t.Logf("Event ID: %s", event.Id)
	t.Logf("Title: %s", event.Title)
	t.Logf("Fingerprint: %v", ptrToStr(event.Fingerprint))
	t.Logf("Source: %v", ptrToStr(event.Source))
	t.Logf("Namespace: %v", ptrToStr(event.SubjectNamespace))

	// Check triage rules for this event
	result, err := CheckTriageRules(ctx, db, &event, 1)
	require.NoError(t, err)

	t.Logf("\n=== TRIAGE RULES RESULT ===")
	if result == nil {
		t.Log("No matching rules found")
	} else {
		t.Logf("Rule ID: %s", result.RuleID)
		if result.Suppression != nil {
			t.Logf("Suppression Action: %s", result.Suppression.Action)
		}
		if result.ScoreAdjustment != nil {
			t.Logf("Score Adjustment: %+d", result.ScoreAdjustment.Adjustment)
		}
		if result.AutoClassification != nil {
			t.Logf("Auto Classification: %s", result.AutoClassification.Classification)
		}
	}

	resultJson, _ := json.MarshalIndent(result, "", "  ")
	t.Logf("Full Result:\n%s", string(resultJson))
}

func TestCreateAndDeleteTriageRule(t *testing.T) {
	ctx := context.Background()

	// Get database connection
	testenv.RequireMetastore(t)
	dbManager, err := database.GetDatabaseManager(database.Metastore)
	require.NoError(t, err, "Failed to get database manager")

	db := dbManager.Db

	// Get a test account
	var accountID, tenantID string
	err = db.GetContext(ctx, &struct {
		AccountID *string `db:"cloud_account_id"`
		TenantID  *string `db:"tenant"`
	}{&accountID, &tenantID}, `
		SELECT DISTINCT cloud_account_id, tenant
		FROM events
		WHERE cloud_account_id IS NOT NULL
		LIMIT 1
	`)
	if err != nil {
		t.Skip("No events with cloud_account_id found, skipping")
		return
	}

	t.Logf("Test Account: %s, Tenant: %s", accountID, tenantID)

	var createdRuleID string

	// Test creating a rule
	t.Run("create_suppression_rule", func(t *testing.T) {
		req := CreateTriageRuleRequest{
			RuleType:       RuleTypeSuppression,
			Action:         ActionSuppress,
			MatchAlertname: strPtr("Test Alert Pattern.*"),
			MatchNamespace: strPtr("test-namespace"),
			Name:           strPtr("Test Suppression Rule"),
			Description:    strPtr("Created by unit test"),
		}

		rule, err := CreateTriageRule(ctx, db, req, accountID, tenantID, "test-user")
		require.NoError(t, err)

		createdRuleID = rule.ID

		t.Logf("Created rule: %s", rule.ID)
		assert.NotEmpty(t, rule.ID)
		assert.Equal(t, RuleTypeSuppression, rule.RuleType)
		assert.Equal(t, ActionSuppress, rule.Action)
		assert.Equal(t, "Test Alert Pattern.*", *rule.MatchAlertname)
	})

	// Test getting rules
	t.Run("get_rules", func(t *testing.T) {
		if createdRuleID == "" {
			t.Skip("No rule created, skipping")
			return
		}

		req := GetTriageRulesRequest{}
		rules, err := GetTriageRules(ctx, db, req, []string{accountID}, tenantID)
		require.NoError(t, err)

		t.Logf("Found %d rules", len(rules))

		// Find our created rule
		found := false
		for _, r := range rules {
			if r.ID == createdRuleID {
				found = true
				t.Logf("Found created rule: %s - %s", r.ID, *r.Name)
				break
			}
		}
		assert.True(t, found, "Created rule should be in list")
	})

	// Test deleting the rule (soft delete)
	t.Run("soft_delete_rule", func(t *testing.T) {
		if createdRuleID == "" {
			t.Skip("No rule created, skipping")
			return
		}

		err := DeleteTriageRule(ctx, db, createdRuleID, false, accountID, tenantID)
		require.NoError(t, err)

		// Verify rule is disabled
		var enabled bool
		err = db.GetContext(ctx, &enabled, `
			SELECT enabled FROM event_triage_rules WHERE id = $1
		`, createdRuleID)
		require.NoError(t, err)
		assert.False(t, enabled, "Rule should be disabled after soft delete")
	})

	// Re-enabling a disabled rule, and whether a plain edit preserves it, are
	// covered by TestUpdateTriageRuleEnabledField_DB — this test's rule depends
	// on the account lookup above finding real seeded data, so it isn't a
	// hermetic place for that coverage (#40111).

	// Cleanup - hard delete
	t.Cleanup(func() {
		if createdRuleID != "" {
			_, err := db.ExecContext(ctx, `DELETE FROM event_triage_rules WHERE id = $1`, createdRuleID)
			if err != nil {
				t.Logf("Warning: Failed to cleanup rule: %v", err)
			}
		}
	})
}

func TestRulePriorityOrdering(t *testing.T) {
	ctx := context.Background()

	// Get database connection
	testenv.RequireMetastore(t)
	dbManager, err := database.GetDatabaseManager(database.Metastore)
	require.NoError(t, err, "Failed to get database manager")

	db := dbManager.Db

	// Get test account
	var accountID, tenantID string
	err = db.GetContext(ctx, &struct {
		AccountID *string `db:"cloud_account_id"`
		TenantID  *string `db:"tenant"`
	}{&accountID, &tenantID}, `
		SELECT DISTINCT cloud_account_id, tenant
		FROM events
		WHERE cloud_account_id IS NOT NULL
		LIMIT 1
	`)
	if err != nil {
		t.Skip("No events with cloud_account_id found, skipping")
		return
	}

	// Create multiple rules with different priorities
	rules := []CreateTriageRuleRequest{
		{
			RuleType:       RuleTypeSuppression,
			Action:         ActionSuppress,
			MatchAlertname: strPtr("Priority Test.*"),
			Priority:       intPtr(300), // Lower priority (higher number)
			Name:           strPtr("Low Priority Rule"),
		},
		{
			RuleType:       RuleTypeSuppression,
			Action:         ActionDrop,
			MatchAlertname: strPtr("Priority Test.*"),
			Priority:       intPtr(200), // Higher priority (lower number)
			Name:           strPtr("High Priority Rule"),
		},
	}

	var createdRuleIDs []string

	for _, req := range rules {
		rule, err := CreateTriageRule(ctx, db, req, accountID, tenantID, "test-user")
		require.NoError(t, err)
		createdRuleIDs = append(createdRuleIDs, rule.ID)
		t.Logf("Created rule: %s (priority %d)", *req.Name, *req.Priority)
	}

	// Verify ordering when fetching
	t.Run("verify_priority_ordering", func(t *testing.T) {
		req := GetTriageRulesRequest{}
		fetchedRules, err := GetTriageRules(ctx, db, req, []string{accountID}, tenantID)
		require.NoError(t, err)

		// Filter to our test rules
		var testRules []TriageRule
		for _, r := range fetchedRules {
			for _, id := range createdRuleIDs {
				if r.ID == id {
					testRules = append(testRules, r)
					break
				}
			}
		}

		if len(testRules) >= 2 {
			// First rule should have lower priority number (higher precedence)
			assert.Less(t, testRules[0].Priority, testRules[1].Priority,
				"Rules should be ordered by priority (lower number = higher precedence)")
			t.Logf("Rule 1: %s (priority %d)", *testRules[0].Name, testRules[0].Priority)
			t.Logf("Rule 2: %s (priority %d)", *testRules[1].Name, testRules[1].Priority)
		}
	})

	// Cleanup
	t.Cleanup(func() {
		for _, id := range createdRuleIDs {
			_, _ = db.ExecContext(ctx, `DELETE FROM event_triage_rules WHERE id = $1`, id)
		}
	})
}

func TestApplyTriageRuleActions(t *testing.T) {
	ctx := context.Background()

	// Get database connection
	testenv.RequireMetastore(t)
	dbManager, err := database.GetDatabaseManager(database.Metastore)
	require.NoError(t, err, "Failed to get database manager")

	db := dbManager.Db

	// Find a test event
	eventID := testenv.RequireEnv(t, "TEST_EVENT_ID")["TEST_EVENT_ID"]

	var event models.Event
	query := `
		SELECT id, title, fingerprint, source, priority,
		       subject_namespace, cloud_account_id, tenant
		FROM events
		WHERE id = $1
	`
	err = db.GetContext(ctx, &event, query, eventID)
	if err != nil {
		t.Skipf("Event %s not found, skipping", eventID)
		return
	}

	// Get original status from separate query (nb_status may be added via migration)
	var originalStatus string
	err = db.GetContext(ctx, &originalStatus, `SELECT COALESCE(nb_status, 'OPEN') FROM events WHERE id = $1`, eventID)
	if err != nil {
		originalStatus = "OPEN"
	}

	t.Run("apply_suppression_action", func(t *testing.T) {
		// Create a mock triage result with suppression
		result := &TriageRuleResult{
			RuleID:   "test-rule-id",
			RuleType: RuleTypeSuppression,
			Action:   ActionSuppress,
			Suppression: &SuppressionResult{
				Action:    ActionSuppress,
				NewStatus: NBStatusSuppressed,
			},
		}

		err := ApplyTriageRuleActions(ctx, db, &event, result)
		require.NoError(t, err)

		// Verify status was updated
		var newStatus string
		err = db.GetContext(ctx, &newStatus, `SELECT nb_status FROM events WHERE id = $1`, eventID)
		require.NoError(t, err)

		assert.Equal(t, NBStatusSuppressed, newStatus)
		t.Logf("Event status changed to: %s", newStatus)
	})

	// Restore original status
	t.Cleanup(func() {
		_, _ = db.ExecContext(ctx, `
			UPDATE events SET nb_status = $1, nb_status_changed_at = NOW()
			WHERE id = $2
		`, originalStatus, eventID)
	})
}

// Helper function to create string pointer
func strPtr(s string) *string {
	return &s
}

// TestApplyRuleToExistingEventsRecordsMatches covers the retroactive apply path: a rule created
// with "apply to existing" must record its matches, not just flip events.nb_status. Before the
// fix the rule reported Matches: 0 and an empty Matched Events tab for a backlog it had actually
// suppressed (#38740).
func TestApplyRuleToExistingEventsRecordsMatches(t *testing.T) {
	ctx := context.Background()

	testenv.RequireMetastore(t)
	dbManager, err := database.GetDatabaseManager(database.Metastore)
	require.NoError(t, err, "Failed to get database manager")

	db := dbManager.Db

	// Seed one OPEN event in a namespace no real rule matches. Enum-backed columns are cloned
	// from an existing row so the insert satisfies the events FK constraints.
	const testNamespace = "zz-apply-existing-test"
	var seeded struct {
		ID        string `db:"id"`
		AccountID string `db:"cloud_account_id"`
		TenantID  string `db:"tenant"`
	}
	err = db.GetContext(ctx, &seeded, `
		INSERT INTO events (id, finding_id, created_at, updated_at, title, source, aggregation_key,
		                    finding_type, subject_type, subject_name, subject_namespace, cluster,
		                    priority, starts_at, fingerprint, evidences, tenant, cloud_account_id,
		                    status, nb_status)
		SELECT gen_random_uuid(), 'zz-apply-existing-test', NOW(), NOW(), 'apply-existing match test',
		       source, 'TestApplyExisting', finding_type, subject_type, 'test-pod', $1, cluster,
		       priority, NOW(), 'zz-apply-existing-test-fp', '[]'::jsonb, tenant, cloud_account_id,
		       status, 'OPEN'
		FROM events
		WHERE cloud_account_id IS NOT NULL AND tenant IS NOT NULL AND cluster IS NOT NULL
		LIMIT 1
		RETURNING id, cloud_account_id, tenant
	`, testNamespace)
	if err != nil {
		t.Skipf("Could not seed a test event, skipping: %v", err)
		return
	}

	t.Cleanup(func() {
		// Deleting the event cascades to event_triage_rule_matches.
		if _, err := db.ExecContext(ctx, `DELETE FROM events WHERE id = $1`, seeded.ID); err != nil {
			t.Logf("Warning: Failed to cleanup test event: %v", err)
		}
	})

	rule, err := CreateTriageRule(ctx, db, CreateTriageRuleRequest{
		RuleType:       RuleTypeSuppression,
		Action:         ActionSuppress,
		MatchNamespace: strPtr(testNamespace),
		Name:           strPtr("Test Apply-To-Existing Rule"),
		Description:    strPtr("Created by unit test"),
	}, seeded.AccountID, seeded.TenantID, systemUserID)
	require.NoError(t, err)

	t.Cleanup(func() {
		if _, err := db.ExecContext(ctx, `DELETE FROM event_triage_rules WHERE id = $1`, rule.ID); err != nil {
			t.Logf("Warning: Failed to cleanup rule: %v", err)
		}
	})

	updated, err := ApplyRuleToExistingEvents(ctx, db, rule, seeded.AccountID, systemUserID)
	require.NoError(t, err)
	require.Equal(t, 1, updated, "the seeded event should have been updated")

	// The event was suppressed...
	var nbStatus string
	require.NoError(t, db.GetContext(ctx, &nbStatus, `SELECT nb_status FROM events WHERE id = $1`, seeded.ID))
	assert.Equal(t, NBStatusSuppressed, nbStatus)

	// ...and the rule reports it. Both assertions below failed before the fix.
	var matchRows int
	require.NoError(t, db.GetContext(ctx, &matchRows, `
		SELECT COUNT(*) FROM event_triage_rule_matches WHERE rule_id = $1 AND event_id = $2
	`, rule.ID, seeded.ID))
	assert.Equal(t, 1, matchRows, "retroactive apply should record a rule match row")

	var matchCount int
	require.NoError(t, db.GetContext(ctx, &matchCount, `
		SELECT match_count FROM event_triage_rules WHERE id = $1
	`, rule.ID))
	assert.Equal(t, 1, matchCount, "retroactive apply should bump the rule's match_count")

	// The Matched Events tab reads through this call.
	events, total, err := GetEventsForTriageRule(ctx, db, rule.ID, seeded.TenantID, &seeded.AccountID, 50, 0, nil, nil)
	require.NoError(t, err)
	assert.Equal(t, 1, total)
	require.Len(t, events, 1)
	assert.Equal(t, seeded.ID, events[0].ID)

	// Re-applying must not double-count: the match row already exists.
	_, err = db.ExecContext(ctx, `UPDATE events SET nb_status = 'OPEN' WHERE id = $1`, seeded.ID)
	require.NoError(t, err)

	_, err = ApplyRuleToExistingEvents(ctx, db, rule, seeded.AccountID, systemUserID)
	require.NoError(t, err)

	require.NoError(t, db.GetContext(ctx, &matchCount, `
		SELECT match_count FROM event_triage_rules WHERE id = $1
	`, rule.ID))
	assert.Equal(t, 1, matchCount, "re-applying the same rule must not double-count match_count")
}
