package eventrule

import (
	"context"
	"errors"
	"log/slog"
	"regexp"
	"testing"

	"nudgebee/services/common"
	"nudgebee/services/internal/database"
	"nudgebee/services/security"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The Alert Manager rule actions are grantable at the gateway, but the handlers
// used to accept only a built-in role, so a custom-role user the gateway admitted
// was refused here. They now take the action's grant key.

const (
	grantRuleAccount = "11111111-1111-1111-1111-111111111111"
	grantRuleID      = "33333333-3333-3333-3333-333333333333"
)

var errGrantRuleFiltered = errors.New("rule filtered out")

func TestRuleActionsHonourTheActionsGrant(t *testing.T) {
	cases := []struct {
		action  string
		key     string
		builtin string // the built-in role that passed before and still must
		// tenantWideOnly: an account-bound grant is not enough (a tenant-level operation)
		tenantWideOnly bool
		// call stops right after the authorization when it is let through (a blank
		// name, an id that resolves to nothing) so it never reaches a provider.
		call func(t *testing.T, mock sqlmock.Sqlmock, ctx *security.RequestContext) error
	}{
		{"alertmanager_create_rule", "alertmanager:Write", "account_admin", false, func(_ *testing.T, _ sqlmock.Sqlmock, ctx *security.RequestContext) error {
			_, err := CreateEventRule(ctx, EventConfig{AccountID: grantRuleAccount})
			return err
		}},
		{"alertmanager_update_rule", "alertmanager:Write", "account_admin", false, func(_ *testing.T, _ sqlmock.Sqlmock, ctx *security.RequestContext) error {
			_, err := UpdateEventRule(ctx, EventConfig{AccountID: grantRuleAccount})
			return err
		}},
		{"alertmanager_disable_rule", "alertmanager:Execute", "account_admin", false, func(_ *testing.T, _ sqlmock.Sqlmock, ctx *security.RequestContext) error {
			_, err := DisableEventRule(ctx, DisableEventConfig{AccountID: grantRuleAccount, Id: "not-a-rule"})
			return err
		}},
		{"eventrules_apply_rule_change", "eventrules:Execute", "account_admin", false, func(_ *testing.T, _ sqlmock.Sqlmock, ctx *security.RequestContext) error {
			_, err := ApplyRuleChange(ctx, ApplyRuleChangeRequest{RuleID: "not-a-rule", AccountID: grantRuleAccount})
			return err
		}},
		{"eventrules_get_capabilities", "eventrules:Read", "account_admin_readonly", false, func(t *testing.T, mock sqlmock.Sqlmock, ctx *security.RequestContext) error {
			mock.ExpectQuery(regexp.QuoteMeta(`FROM event_rules WHERE tenant_id = $1 AND id = ANY($2::uuid[])`)).
				WillReturnRows(sqlmock.NewRows([]string{"id", "account_id", "alert", "source"}).
					AddRow(grantRuleID, grantRuleAccount, "HighCPU", "AWS_CloudWatch_Alarm"))
			mock.ExpectQuery(regexp.QuoteMeta(`FROM event_triage_rules`)).WillReturnRows(sqlmock.NewRows([]string{"id"}))
			caps, err := GetRuleCapabilities(ctx, []string{grantRuleID})
			require.NoError(t, err)
			if len(caps) == 0 {
				return errGrantRuleFiltered
			}
			return nil
		}},
	}

	denied := func(err error) bool {
		if errors.Is(err, errGrantRuleFiltered) {
			return true
		}
		return err != nil && (err.Error() == "unauthorized" || err.Error() == "account unauthorized")
	}
	person := func(extra map[string]any) map[string]any {
		wire := map[string]any{"TenantId": "tenant-1", "UserId": "user-1", "AccountIds": []string{grantRuleAccount}, "Roles": []string{}}
		for k, v := range extra {
			wire[k] = v
		}
		return wire
	}

	for _, tc := range cases {
		t.Run(tc.action, func(t *testing.T) {
			run := func(wire map[string]any, key string) error {
				dbms, mock := mockDB(t)
				database.RegisterDatabaseManagerHook(database.Metastore, func() (*database.DatabaseManager, error) { return dbms, nil })
				t.Cleanup(func() { database.RegisterDatabaseManagerHook(database.Metastore, nil) })
				raw, err := common.MarshalJson(wire)
				require.NoError(t, err)
				var sc security.SecurityContext
				require.NoError(t, common.UnmarshalJson(raw, &sc))
				ctx := security.NewRequestContext(context.Background(), &sc, slog.Default(), nil, nil)
				ctx.SetActionPermission(key)
				return tc.call(t, mock, ctx)
			}
			grant := map[string]bool{tc.key: true}

			assert.False(t, denied(run(person(map[string]any{"CustomPermissions": grant}), tc.key)), "tenant-wide grant for the key")
			assert.Equal(t, tc.tenantWideOnly, denied(run(person(map[string]any{"ScopedCustomPermissions": map[string]any{grantRuleAccount: grant}}), tc.key)), "grant bound to the account")
			assert.True(t, denied(run(person(nil), tc.key)), "no grant")
			assert.True(t, denied(run(person(map[string]any{"CustomPermissions": map[string]bool{"cost:Write": true}}), tc.key)), "another module's grant")
			assert.True(t, denied(run(person(map[string]any{"CustomPermissions": grant}), "")), "no key: built-in roles only")
			assert.False(t, denied(run(person(map[string]any{
				"Roles": []string{tc.builtin}, "ScopedEntityIds": map[string]any{tc.builtin: []string{grantRuleAccount}},
			}), tc.key)), "built-in role")
		})
	}

	// Creating a rule needs create access to its account only, as updating and
	// disabling one do: an account admin of another account is refused.
	t.Run("alertmanager_create_rule refuses an admin of another account", func(t *testing.T) {
		var sc security.SecurityContext
		require.NoError(t, common.UnmarshalJson([]byte(`{"TenantId":"tenant-1","UserId":"user-1","AccountIds":["`+grantRuleAccount+`","other-account"],
			"Roles":["account_admin"],"ScopedEntityIds":{"account_admin":["other-account"]}}`), &sc))
		ctx := security.NewRequestContext(context.Background(), &sc, slog.Default(), nil, nil)
		ctx.SetActionPermission("alertmanager:Write")
		_, err := CreateEventRule(ctx, EventConfig{AccountID: grantRuleAccount})
		assert.True(t, denied(err))
	})
}
