package triage

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The live matcher (MatchesRuleWithOccurrence) compares a rule's namespace exactly. The
// preview and the three "apply to existing" statements build their own WHERE clause, and
// used to compare it as an unanchored regex: a rule for "prod" previewed and back-applied
// to "prod-eu" and "nonprod" too, events the live matcher would never have given it.
//
// The comparison is a SQL operator, so this runs against a real Postgres. Named _DB to
// match the package's other real-database tests; skipped locally unless a database is
// configured.
func TestRuleNamespaceIsExactInSQL_DB(t *testing.T) {
	db := muteTestDB(t)
	ctx := context.Background()

	// Only the columns the four statements read and write. match_count and
	// event_triage_rule_matches are where an apply records what it matched.
	_, err := db.Exec(`
		ALTER TABLE event_triage_rules
			ADD COLUMN match_count int NOT NULL DEFAULT 0, ADD COLUMN last_matched_at timestamptz;
		CREATE TABLE events (
			id uuid PRIMARY KEY, tenant uuid, cloud_account_id uuid, title text, starts_at timestamp,
			fingerprint text, aggregation_key text, subject_namespace text, subject_owner text,
			source text, priority text, finding_type text, labels jsonb,
			nb_status text, nb_status_changed_at timestamp, nb_status_changed_by uuid,
			computed_score int, computed_priority text, score_factors jsonb,
			score_confidence numeric(3, 2), updated_at timestamp
		);
		CREATE TABLE event_classification (event_id uuid, corrected_priority text, classified_by uuid);
		CREATE TABLE event_triage_rule_matches (
			event_id uuid NOT NULL, rule_id uuid NOT NULL, cloud_account_id uuid NOT NULL,
			tenant_id uuid NOT NULL, rule_type text NOT NULL, action text NOT NULL,
			UNIQUE (event_id, rule_id, cloud_account_id)
		)`)
	require.NoError(t, err)

	// One open event per namespace, and one with none, in an account of its own so that
	// what one statement writes cannot hide an event from the next.
	seed := func(t *testing.T) string {
		account := uuid.New().String()
		for _, ns := range []string{"prod", "prod-eu", "nonprod", "kube-system", "monitoring", ""} {
			_, err := db.Exec(`
				INSERT INTO events (id, tenant, cloud_account_id, title, starts_at, subject_namespace, nb_status)
				VALUES (gen_random_uuid(), $1, $2, 'namespace match', now(), NULLIF($3, ''), 'OPEN')`,
				muteTenant, account, ns)
			require.NoError(t, err)
		}
		return account
	}
	// Every apply stamps updated_at on the events it writes; seeded events have none.
	applied := func(t *testing.T, account string, rule *TriageRule) []string {
		rule.ID = uuid.New().String()
		_, err := ApplyRuleToExistingEvents(ctx, db, rule, account, muteUser)
		require.NoError(t, err)
		var got []string
		require.NoError(t, db.Select(&got, `
			SELECT COALESCE(subject_namespace, '') FROM events
			WHERE cloud_account_id = $1 AND updated_at IS NOT NULL`, account))
		return got
	}

	// Each returns the namespaces of the events its statement picked for the rule's namespace.
	paths := []struct {
		name    string
		matched func(t *testing.T, account, namespace string) []string
	}{
		{"preview", func(t *testing.T, account, namespace string) []string {
			preview, err := PreviewTriageRule(ctx, db, RulePreviewRequest{
				RuleType: RuleTypeSuppression, Action: ActionSuppress, MatchNamespace: &namespace,
			}, account, muteTenant)
			require.NoError(t, err)
			got := make([]string, 0, len(preview.SampleEvents))
			for _, e := range preview.SampleEvents {
				got = append(got, e.Namespace)
			}
			require.Len(t, got, preview.MatchingEventsCount, "count and samples come from the same conditions")
			return got
		}},
		{"apply to existing", func(t *testing.T, account, namespace string) []string {
			return applied(t, account, &TriageRule{
				RuleType: RuleTypeSuppression, Action: ActionSuppress, MatchNamespace: &namespace,
			})
		}},
		{"apply to existing, scoring", func(t *testing.T, account, namespace string) []string {
			return applied(t, account, &TriageRule{
				RuleType: RuleTypeScoring, Action: ActionAdjustScore, ActionValue: strPtr(`{"adjustment": 10}`),
				MatchNamespace: &namespace,
			})
		}},
		{"apply to existing, priority pin", func(t *testing.T, account, namespace string) []string {
			return applied(t, account, &TriageRule{
				RuleType: RuleTypePriorityPin, Action: ActionPinPriority, ActionValue: strPtr(`{"priority": "P0"}`),
				MatchNamespace: &namespace,
			})
		}},
	}
	cases := []struct {
		namespace string
		want      []string
	}{
		{"prod", []string{"prod"}},
		// A pattern is not a namespace: it names no event, not the namespaces it spells out.
		{"kube-system|monitoring", nil},
	}
	for _, p := range paths {
		for _, tc := range cases {
			t.Run(p.name+"/"+tc.namespace, func(t *testing.T) {
				assert.ElementsMatch(t, tc.want, p.matched(t, seed(t), tc.namespace))
			})
		}
	}
}
