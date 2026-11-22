package eventrule

import (
	"log/slog"
	"regexp"
	"testing"

	"nudgebee/services/internal/database"
	"nudgebee/services/security"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/jmoiron/sqlx"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// upsertEventRule decides whether a write may refresh a rule's definition
// (expr / duration / alert_type) or only its firing-instance fields. That used
// to be inferred from the source string ending in `_webhook`, which
// misclassified SyncProviderRules: it writes authoritative provider
// definitions under the provider's own *_webhook ruleSource, so every synced
// expr was discarded on any rule that already had a row. For Elasticsearch
// that left event_rules.expr pinned to the per-instance
// `{{context.conditions}}` sentence the first firing happened to carry
// ("Number of matching documents for group \"<some-pod>\" is greater than 50")
// instead of the rule's real Kibana query.
//
// These tests pin the replacement signal — the explicit isDefinition argument
// — at the two places it can silently regress: the bind position, and the SQL
// branches that read it.

// mockDB returns a DatabaseManager backed by a fresh sqlmock.
//
// upsertEventRule takes its manager as an argument and never resolves the
// global metastore, so this stays local: no package-level hook to register, and
// nothing for a concurrently-running test in this package to race against.
func mockDB(t *testing.T) (*database.DatabaseManager, sqlmock.Sqlmock) {
	t.Helper()
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	return &database.DatabaseManager{Db: sqlx.NewDb(db, "postgres")}, mock
}

// callUpsert drives upsertEventRule with the argument shape SyncProviderRules
// and CreateEventRule both use, varying only the definition flag.
func callUpsert(dbms *database.DatabaseManager, isDefinition bool) error {
	_, _, err := upsertEventRule(dbms,
		map[string]string{"summary": "NB test: stderr spike by pod"},
		map[string]string{"severity": "critical"},
		"NB test: stderr spike by pod",
		`{"query":{"bool":{"filter":[{"term":{"stream":"stderr"}}]}}}`,
		"5m",
		"dfa1c8b5-8dd5-4e64-976c-fa7176f182b8", "b82de7ca-0b5b-4f6f-a202-b283a4b94e41",
		"elasticsearch_webhook", "alert", "critical", true,
		"log", "ES", "user",
		"1412f337-a517-4098-9818-e61a839ff8e0", map[string]interface{}{"kibana_rule_type_id": ".es-query"},
		isDefinition)
	return err
}

// TestUpsertEventRuleBindsDefinitionFlagToEveryDefinitionColumn pins the flag
// to all three columns it governs.
//
// database.prepareQueryForIn rewrites every `$N` occurrence to a positional `?`
// and duplicates the argument behind it, so the single isDefinition parameter
// arrives once per reference — three times, immediately after the sixteen real
// columns. A column silently dropped from the guard shows up here as a missing
// trailing flag rather than as a passing test.
func TestUpsertEventRuleBindsDefinitionFlagToEveryDefinitionColumn(t *testing.T) {
	for _, isDefinition := range []bool{true, false} {
		dbms, mock := mockDB(t)

		mock.ExpectQuery(`INSERT INTO event_rules`).
			WithArgs(
				sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(),
				sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(),
				sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(),
				sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(),
				// expr, duration, alert_type.
				isDefinition, isDefinition, isDefinition,
			).
			WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow("e10e6319-2547-4da4-94a5-3678b1ec0712"))

		require.NoError(t, callUpsert(dbms, isDefinition))
		assert.NoError(t, mock.ExpectationsWereMet(),
			"isDefinition=%v must reach expr, duration and alert_type", isDefinition)
	}
}

// TestUpsertEventRuleGuardsDefinitionFieldsOnTheFlag pins the SQL itself.
//
// sqlmock matches the expected query as a regular expression against the one
// actually executed, so an expectation that does not appear in the statement
// fails the call outright. Each fragment below is therefore a load-bearing
// assertion about the emitted SQL.
func TestUpsertEventRuleGuardsDefinitionFieldsOnTheFlag(t *testing.T) {
	lit := regexp.QuoteMeta

	patterns := map[string]string{
		// All three definition columns must consult the flag before falling
		// back to the source heuristic. A column left on the heuristic alone
		// silently stops being refreshed by syncs — the exact failure this
		// fixes, and one that leaves no error behind to notice. The parameter
		// number is not pinned: prepareQueryForIn renumbers every placeholder.
		"expr consults the flag":       `expr = CASE WHEN \$\d+ THEN ` + lit(`COALESCE(NULLIF(EXCLUDED.expr, ''), event_rules.expr)`),
		"duration consults the flag":   `duration = CASE WHEN \$\d+ THEN ` + lit(`COALESCE(NULLIF(EXCLUDED.duration, ''), event_rules.duration)`),
		"alert_type consults the flag": `alert_type = CASE WHEN \$\d+ THEN ` + lit(`COALESCE(NULLIF(EXCLUDED.alert_type, ''), event_rules.alert_type)`),
		// The fallback must survive, or #33024 reopens and firing webhooks
		// blank agent-synced PromQL again.
		"firing guard remains the fallback": lit(`WHEN EXCLUDED.source NOT LIKE '%\_webhook' THEN EXCLUDED.expr ELSE event_rules.expr END`),
		// A write without provider_config (every UI edit) must keep the stored
		// keys — the sync's managed_by decides whether the rule can be changed
		// at all — and a write with one must merge, not replace.
		"provider_config kept when not sent": lit(`WHEN EXCLUDED.provider_config IS NULL THEN event_rules.provider_config`),
		"provider_config merged":             lit(`COALESCE(event_rules.provider_config, '{}'::jsonb) || EXCLUDED.provider_config`),
		// A write that carries no external id must keep the stored one. Without
		// this, any firing whose payload has no upstream id blanks the column on
		// a webhook-sourced row and silently un-manages a rule that an edit or
		// disable could otherwise reach — for every provider, not just SolarWinds.
		//
		// Plain COALESCE and not COALESCE(NULLIF(...)) as the definition columns
		// above use: this parameter is bound through nilIfEmpty, so an empty id
		// already arrives as NULL and NULLIF could never fire.
		"external_rule_id kept when not sent": lit(`THEN COALESCE(EXCLUDED.external_rule_id, event_rules.external_rule_id)`),
	}

	for name, pattern := range patterns {
		t.Run(name, func(t *testing.T) {
			dbms, mock := mockDB(t)
			mock.ExpectQuery(pattern).
				WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow("e10e6319-2547-4da4-94a5-3678b1ec0712"))

			require.NoError(t, callUpsert(dbms, true),
				"emitted SQL must match: %s", pattern)
			assert.NoError(t, mock.ExpectationsWereMet())
		})
	}
}

// TestUpsertEventRuleRejectsBlankAlert pins the guard against nameless rules.
//
// alert is part of the ON CONFLICT key, so an empty (or whitespace) name is one
// shared slot per account that every unresolved webhook overwrites. The write
// must be refused before any SQL runs — sqlmock fails the test on any query
// it was not told to expect.
func TestUpsertEventRuleRejectsBlankAlert(t *testing.T) {
	for _, alert := range []string{"", "   "} {
		dbms, mock := mockDB(t)

		_, ok, err := upsertEventRule(dbms,
			map[string]string{}, map[string]string{"severity": "warning"},
			alert, "", "0",
			"a2a30b02-0f67-42e5-a2ab-c658230fd798", "890cad87-c452-4aa7-b84a-742cee0454a1",
			"datadog_webhook", "alert", "warning", true,
			"", "", "", "", nil, false)

		assert.ErrorIs(t, err, ErrBlankAlertName, "alert=%q", alert)
		assert.False(t, ok)
		assert.NoError(t, mock.ExpectationsWereMet())
	}
}

// TestCreateEventRuleRejectsBlankAlertBeforeExternalCreate pins the fail-fast
// check at the top of CreateEventRule.
//
// For an external-provider source the rule is created in the provider before
// the local write. If the blank name were only caught by upsertEventRule, the
// provider rule would already exist and the local write would then fail,
// orphaning it. Returning ErrBlankAlertName — rather than a provider or config
// error — proves the call never got that far.
func TestCreateEventRuleRejectsBlankAlertBeforeExternalCreate(t *testing.T) {
	ctx := security.NewRequestContextForSuperAdmin(slog.Default(), nil, nil)
	for _, alert := range []string{"", "  "} {
		_, err := CreateEventRule(ctx, EventConfig{
			Alert:     alert,
			AccountID: "a2a30b02-0f67-42e5-a2ab-c658230fd798",
			Source:    "datadog",
			AlertType: "metric",
		})
		assert.ErrorIs(t, err, ErrBlankAlertName, "alert=%q", alert)
	}
}
