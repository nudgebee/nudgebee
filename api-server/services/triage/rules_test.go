package triage

import (
	"context"
	"encoding/json"
	"testing"

	"nudgebee/services/internal/database"
	"nudgebee/services/internal/database/models"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMatchesRule(t *testing.T) {
	// Create test event
	fingerprint := "test-fingerprint-abc123"
	title := "API Failures - POST /webhook/{uuid} (409)"
	namespace := "nudgebee"
	source := "prometheus"
	serviceName := "workflow-server"

	event := &models.Event{
		Id:               "test-event-id",
		Fingerprint:      &fingerprint,
		Title:            title,
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
	dbManager, err := database.GetDatabaseManager(database.Metastore)
	require.NoError(t, err, "Failed to get database manager")

	db := dbManager.Db

	// Fetch a real event to test with
	eventID := "beabc0e6-c276-46ad-aeb7-734190314c1a"

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
	result, err := CheckTriageRules(ctx, db, &event)
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
		rules, err := GetTriageRules(ctx, db, req, accountID, tenantID)
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
		fetchedRules, err := GetTriageRules(ctx, db, req, accountID, tenantID)
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
	dbManager, err := database.GetDatabaseManager(database.Metastore)
	require.NoError(t, err, "Failed to get database manager")

	db := dbManager.Db

	// Find a test event
	eventID := "beabc0e6-c276-46ad-aeb7-734190314c1a"

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
