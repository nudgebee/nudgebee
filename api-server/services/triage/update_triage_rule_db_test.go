package triage

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Re-enabling a disabled rule via UpdateTriageRule, and confirming a plain edit
// (nil Enabled) leaves the current state untouched (#40111). Named _DB so the
// "PR lifecycle sweeps (real Postgres)" job's -run '_DB$' filter picks it up;
// skipped locally unless a database is configured.
func TestUpdateTriageRuleEnabledField_DB(t *testing.T) {
	db := muteTestDB(t)
	ctx := context.Background()

	// UpdateTriageRule's RETURNING clause reads these two; muteTestDB's table
	// omits them since most of the package's other _DB tests don't need them.
	_, err := db.Exec(`ALTER TABLE event_triage_rules
		ADD COLUMN match_count int NOT NULL DEFAULT 0, ADD COLUMN last_matched_at timestamptz`)
	require.NoError(t, err)

	rule, err := CreateTriageRule(ctx, db, CreateTriageRuleRequest{
		RuleType:       RuleTypeSuppression,
		Action:         ActionSuppress,
		MatchAlertname: strPtr("Test Alert Pattern.*"),
		MatchNamespace: strPtr("test-namespace"),
		Name:           strPtr("Test Suppression Rule"),
		Description:    strPtr("Created by unit test"),
	}, muteAccount, muteTenant, muteUser)
	require.NoError(t, err)

	// The request resends the rule's existing fields alongside Enabled,
	// mirroring how the frontend's Enable action calls this endpoint -
	// UpdateTriageRule assigns every other column directly (no COALESCE), so a
	// request that omitted them would silently null the rule's match criteria.
	t.Run("re_enable_rule", func(t *testing.T) {
		require.NoError(t, DeleteTriageRule(ctx, db, rule.ID, false, muteAccount, muteTenant))

		enabledTrue := true
		req := UpdateTriageRuleRequest{
			RuleID:         rule.ID,
			RuleType:       RuleTypeSuppression,
			Action:         ActionSuppress,
			MatchAlertname: strPtr("Test Alert Pattern.*"),
			MatchNamespace: strPtr("test-namespace"),
			Name:           strPtr("Test Suppression Rule"),
			Description:    strPtr("Created by unit test"),
			Enabled:        &enabledTrue,
		}
		updated, err := UpdateTriageRule(ctx, db, req, muteAccount, muteTenant, muteUser)
		require.NoError(t, err)

		assert.True(t, updated.Enabled, "rule should be re-enabled")
		require.NotNil(t, updated.MatchAlertname)
		assert.Equal(t, "Test Alert Pattern.*", *updated.MatchAlertname, "match criteria must survive the re-enable call")
		require.NotNil(t, updated.MatchNamespace)
		assert.Equal(t, "test-namespace", *updated.MatchNamespace, "match criteria must survive the re-enable call")
	})

	// A nil Enabled must leave the current state untouched, so an unrelated
	// edit (e.g. renaming a rule) can't silently flip a disabled rule back on.
	t.Run("update_without_enabled_field_preserves_state", func(t *testing.T) {
		require.NoError(t, DeleteTriageRule(ctx, db, rule.ID, false, muteAccount, muteTenant))

		req := UpdateTriageRuleRequest{
			RuleID:   rule.ID,
			RuleType: RuleTypeSuppression,
			Action:   ActionSuppress,
			Name:     strPtr("Test Suppression Rule"),
		}
		_, err := UpdateTriageRule(ctx, db, req, muteAccount, muteTenant, muteUser)
		require.NoError(t, err)

		var enabled bool
		require.NoError(t, db.GetContext(ctx, &enabled, `SELECT enabled FROM event_triage_rules WHERE id = $1`, rule.ID))
		assert.False(t, enabled, "a plain edit must not re-enable a disabled rule")
	})
}
