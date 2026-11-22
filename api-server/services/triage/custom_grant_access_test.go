package triage

import (
	"context"
	"log/slog"
	"testing"

	"nudgebee/services/common"
	"nudgebee/services/internal/database"
	"nudgebee/services/security"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/jmoiron/sqlx"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The threshold-suggestion and mute actions are grantable at the gateway, but
// their service functions used to accept only a built-in role, so a custom-role
// user the gateway admitted was refused here. They now take the action's grant
// key. A call let through stops at its first database read (the mock expects
// none) or at a rule id that resolves to nothing.
func TestTriageRuleActionsHonourTheActionsGrant(t *testing.T) {
	const account = "11111111-1111-1111-1111-111111111111"
	cases := []struct {
		action  string
		key     string
		builtin string // the built-in role that passed before and still must
		call    func(ctx *security.RequestContext) error
	}{
		{"event_apply_threshold_suggestion", "events:Execute", "account_admin", func(ctx *security.RequestContext) error {
			_, err := ApplyThresholdSuggestion(ctx, ApplyThresholdRequest{AlertRuleKey: "k", CloudAccountID: account, Method: "direct"})
			return err
		}},
		{"event_revert_threshold_suggestion", "events:Execute", "account_admin", func(ctx *security.RequestContext) error {
			_, err := RevertThresholdSuggestion(ctx, "k", account)
			return err
		}},
		{"event_get_threshold_apply_options", "events:Read", "account_admin_readonly", func(ctx *security.RequestContext) error {
			_, err := GetThresholdApplyOptions(ctx, "k", account)
			return err
		}},
		{"eventrules_update_mute", "eventrules:Write", "account_admin", func(ctx *security.RequestContext) error {
			_, err := UpdateRuleMute(ctx, UpdateRuleMuteRequest{RuleID: "not-a-rule", AccountID: account, Muted: true})
			return err
		}},
		{"eventrules_get_event_alert_controls", "eventrules:Read", "account_admin_readonly", func(ctx *security.RequestContext) error {
			_, err := GetEventAlertControls(ctx, EventAlertControlsRequest{EventID: "e1", AccountID: account})
			return err
		}},
		{"eventrules_update_event_mute", "eventrules:Write", "account_admin", func(ctx *security.RequestContext) error {
			_, err := UpdateEventMute(ctx, UpdateEventMuteRequest{EventID: "e1", AccountID: account})
			return err
		}},
	}

	denied := func(err error) bool {
		return err != nil && (err.Error() == "account unauthorized" || err.Error() == "unauthorized")
	}
	person := func(extra map[string]any) map[string]any {
		wire := map[string]any{"TenantId": "tenant-1", "UserId": "user-1", "AccountIds": []string{account}, "Roles": []string{}}
		for k, v := range extra {
			wire[k] = v
		}
		return wire
	}

	for _, tc := range cases {
		t.Run(tc.action, func(t *testing.T) {
			run := func(wire map[string]any, key string) error {
				db, _, err := sqlmock.New()
				require.NoError(t, err)
				t.Cleanup(func() { _ = db.Close() })
				database.RegisterDatabaseManagerHook(database.Metastore, func() (*database.DatabaseManager, error) {
					return &database.DatabaseManager{Db: sqlx.NewDb(db, "postgres")}, nil
				})
				t.Cleanup(func() { database.RegisterDatabaseManagerHook(database.Metastore, nil) })
				raw, err := common.MarshalJson(wire)
				require.NoError(t, err)
				var sc security.SecurityContext
				require.NoError(t, common.UnmarshalJson(raw, &sc))
				ctx := security.NewRequestContext(context.Background(), &sc, slog.Default(), nil, nil)
				ctx.SetActionPermission(key)
				return tc.call(ctx)
			}
			grant := map[string]bool{tc.key: true}

			assert.False(t, denied(run(person(map[string]any{"CustomPermissions": grant}), tc.key)), "tenant-wide grant for the key")
			assert.False(t, denied(run(person(map[string]any{"ScopedCustomPermissions": map[string]any{account: grant}}), tc.key)), "grant bound to the account")
			assert.True(t, denied(run(person(nil), tc.key)), "no grant")
			assert.True(t, denied(run(person(map[string]any{"CustomPermissions": map[string]bool{"cost:Write": true}}), tc.key)), "another module's grant")
			assert.True(t, denied(run(person(map[string]any{"CustomPermissions": grant}), "")), "no key: built-in roles only")
			assert.False(t, denied(run(person(map[string]any{
				"Roles": []string{tc.builtin}, "ScopedEntityIds": map[string]any{tc.builtin: []string{account}},
			}), tc.key)), "built-in role")
		})
	}
}

// eventrules_get_event_alert_controls is open to tenant_usage, which has no
// per-account scope: it reads any account of its own tenant, never another's.
func TestEventAlertControlsAdmitTenantUsageWithinItsTenant(t *testing.T) {
	const own, foreign = "11111111-1111-1111-1111-111111111111", "33333333-3333-3333-3333-333333333333"
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	database.RegisterDatabaseManagerHook(database.Metastore, func() (*database.DatabaseManager, error) {
		return &database.DatabaseManager{Db: sqlx.NewDb(db, "postgres")}, nil
	})
	t.Cleanup(func() { database.RegisterDatabaseManagerHook(database.Metastore, nil) })

	var sc security.SecurityContext
	require.NoError(t, common.UnmarshalJson([]byte(`{"TenantId":"tenant-1","UserId":"user-1","AccountIds":["`+own+`"],"Roles":["tenant_usage"]}`), &sc))
	ctx := security.NewRequestContext(context.Background(), &sc, slog.Default(), nil, nil)
	ctx.SetActionPermission("eventrules:Read")

	_, err = GetEventAlertControls(ctx, EventAlertControlsRequest{EventID: "e1", AccountID: own})
	assert.False(t, err != nil && (err.Error() == "account unauthorized" || err.Error() == "unauthorized"), "own tenant's account: %v", err)

	mock.ExpectQuery("SELECT EXISTS").WillReturnRows(sqlmock.NewRows([]string{"exists"}).AddRow(false))
	_, err = GetEventAlertControls(ctx, EventAlertControlsRequest{EventID: "e1", AccountID: foreign})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unauthorized", "another tenant's account")
}
