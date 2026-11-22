package workflow

import (
	"context"
	"log/slog"
	"testing"

	"nudgebee/runbook/internal/model"
	"nudgebee/runbook/services/security"

	"github.com/stretchr/testify/assert"
)

// grantContext is the RequestContext an action request builds: the caller's
// security context plus the action's grant key.
func grantContext(t *testing.T, wire, actionKey string) *security.RequestContext {
	t.Helper()
	rc := security.NewRequestContext(context.Background(), scFromWire(t, wire), slog.Default(), nil, nil)
	rc.SetActionPermission(actionKey)
	return rc
}

// workflow_check_trigger_match (workflows:Read) and the listened-account check
// of workflow_create / workflow_update (workflows:Write) admit a custom-role
// user holding the action's grant on the account, refuse one without it, and
// leave built-in roles as they were.
func TestWorkflowChecksHonourCustomGrants(t *testing.T) {
	const (
		own  = "00000000-0000-0000-0000-00000000000a"
		peer = "00000000-0000-0000-0000-00000000000b"
	)
	orig := tenantForAccount
	tenantForAccount = func(string) (string, error) { return "t1", nil }
	t.Cleanup(func() { tenantForAccount = orig })

	wire := func(extra string) string {
		return `{"TenantId":"t1","UserId":"u1","AccountIds":["` + own + `","` + peer + `"],` + extra + `}`
	}
	cases := []struct {
		name    string
		wire    string
		allowed bool
	}{
		{"tenant-wide workflows:Write", wire(`"Roles":[],"CustomPermissions":{"workflows:Write":true}`), true},
		{"workflows:Read bound to the account", wire(`"Roles":[],"ScopedCustomPermissions":{"` + peer + `":{"workflows:Read":true}}`), true},
		{"workflows grant on another account only", wire(`"Roles":[],"ScopedCustomPermissions":{"` + own + `":{"workflows:Write":true}}`), false},
		{"another module's grant", wire(`"Roles":[],"CustomPermissions":{"autooptimize:Write":true}`), false},
		{"no roles and no grants", wire(`"Roles":[]`), false},
		{"built-in tenant admin", wire(`"Roles":["tenant_admin"]`), true},
		{"built-in account reader", wire(`"Roles":["account_admin_readonly"],"ScopedEntityIds":{"account_admin_readonly":["` + peer + `"]}`), true},
	}
	for _, tc := range cases {
		t.Run("workflow_check_trigger_match/"+tc.name, func(t *testing.T) {
			_, err := (&Service{}).CheckTriggerMatch(grantContext(t, tc.wire, "workflows:Read"), peer, model.CheckTriggerMatchRequest{
				TriggerType: model.WorkflowTriggerEvent,
				Params:      map[string]any{"event_type": "alert"},
				Payload:     map[string]any{"event_type": "alert", "account_id": peer},
			})
			if tc.allowed {
				assert.NoError(t, err)
			} else {
				assert.ErrorContains(t, err, "account not accessible")
			}
		})
		t.Run("workflow_create listened account/"+tc.name, func(t *testing.T) {
			triggers := []model.Trigger{{Type: model.WorkflowTriggerEvent, Params: map[string]any{"event_type": "alert", "accounts": []any{peer}}}}
			err := validateTriggerAccounts(grantContext(t, tc.wire, "workflows:Write"), own, triggers)
			if tc.allowed {
				assert.NoError(t, err)
			} else {
				assert.ErrorContains(t, err, "not accessible")
			}
		})
	}
}
