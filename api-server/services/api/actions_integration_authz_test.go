package api

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"os"
	"strings"
	"testing"

	"nudgebee/services/security"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

const (
	authzAcctA = "11111111-1111-1111-1111-111111111111"
	authzAcctB = "22222222-2222-2222-2222-222222222222"
)

// authzRequestContext builds a caller from the SecurityContext wire format. The
// tenant owns both test accounts.
func authzRequestContext(t *testing.T, extra string) *security.RequestContext {
	t.Helper()
	wire := `{"TenantId":"tenant-1","UserId":"user-1","AccountIds":["` + authzAcctA + `","` + authzAcctB + `"]`
	if extra != "" {
		wire += "," + extra
	}
	wire += "}"
	var sc security.SecurityContext
	require.NoError(t, json.Unmarshal([]byte(wire), &sc))
	return security.NewRequestContext(context.Background(), &sc, slog.Default(), nil, nil)
}

func TestCanRunIntegrationAction(t *testing.T) {
	previous := listWorkflowWebhookAccountIDs
	t.Cleanup(func() { listWorkflowWebhookAccountIDs = previous })
	listWorkflowWebhookAccountIDs = func(_ *security.RequestContext, name, source string) ([]string, error) {
		if source != "user" {
			// Only the default source has rows in this fixture.
			return nil, nil
		}
		switch name {
		case "wh-on-a":
			return []string{authzAcctA}, nil
		case "wh-on-b":
			return []string{authzAcctB}, nil
		case "wh-lookup-fails":
			return nil, errors.New("db down")
		}
		return nil, nil
	}

	const (
		tenantAdmin       = `"Roles":["tenant_admin"]`
		tenantReadonly    = `"Roles":["tenant_admin_readonly"]`
		superAdmin        = `"Roles":["super_admin"]`
		integrationsWrite = `"CustomPermissions":{"integrations:Write":true}`
		integrationsRead  = `"CustomPermissions":{"integrations:Read":true}`
		workflowsWrite    = `"CustomPermissions":{"workflows:Write":true}`
		accountAdminOnA   = `"Roles":["account_admin"],"ScopedEntityIds":{"account_admin":["` + authzAcctA + `"]}`
		workflowsWriteOnA = `"ScopedCustomPermissions":{"` + authzAcctA + `":{"workflows:Write":true}}`
	)
	webhook := func(name, id string, accounts ...string) integrationActionTarget {
		return integrationActionTarget{IntegrationName: workflowWebhookIntegration, IntegrationConfigName: name, IntegrationId: id, AccountIds: accounts}
	}

	cases := []struct {
		name   string
		caller string
		action string
		target integrationActionTarget
		want   bool
	}{
		{"tenant admin writes", tenantAdmin, "integrations_create_config", integrationActionTarget{IntegrationName: "datadog"}, true},
		{"super admin writes", superAdmin, "integrations_delete_config", integrationActionTarget{IntegrationName: "datadog"}, true},
		{"integrations:Write grant writes", integrationsWrite, "integrations_update_status", integrationActionTarget{}, true},
		{"integrations:Write grant satisfies a Read action", integrationsWrite, "integrations_get_schema", integrationActionTarget{}, true},
		{"integrations:Read grant reads", integrationsRead, "integrations_check_connection", integrationActionTarget{}, true},
		{"integrations:Read grant cannot write", integrationsRead, "integrations_create_config", integrationActionTarget{IntegrationName: "datadog"}, false},
		{"tenant read-only admin cannot write", tenantReadonly, "integrations_delete_config", integrationActionTarget{IntegrationName: "datadog"}, false},
		{"no grant cannot read", "", "integrations_list_es_indexes", integrationActionTarget{}, false},
		{"no grant cannot write", "", "integrations_create_config", integrationActionTarget{IntegrationName: "datadog"}, false},
		{"unknown action refused even for tenant admin", tenantAdmin, "integrations_drop_everything", integrationActionTarget{}, false},
		{"handler-authorized action passes through", "", "integrations_diagnose_connection", integrationActionTarget{}, true},

		// Workflow webhooks: runbook-server's rule, per linked account.
		{"account admin creates workflow webhook on own account", accountAdminOnA, "integrations_create_config", webhook("wh-new", "", authzAcctA), true},
		{"account admin cannot create workflow webhook on other account", accountAdminOnA, "integrations_create_config", webhook("wh-new", "", authzAcctB), false},
		{"account admin cannot create workflow webhook spanning accounts", accountAdminOnA, "integrations_create_config", webhook("wh-new", "", authzAcctA, authzAcctB), false},
		{"workflow webhook create needs an account", accountAdminOnA, "integrations_create_config", webhook("wh-new", ""), false},
		{"workflow webhook create cannot overwrite by id", accountAdminOnA, "integrations_create_config", webhook("wh-new", "existing-id", authzAcctA), false},
		{"scoped workflows:Write creates on its account", workflowsWriteOnA, "integrations_create_config", webhook("wh-new", "", authzAcctA), true},
		{"tenant workflows:Write creates", workflowsWrite, "integrations_create_config", webhook("wh-new", "", authzAcctB), true},
		{"account admin deletes webhook linked to own account", accountAdminOnA, "integrations_delete_config", webhook("wh-on-a", "", authzAcctA), true},
		{"delete uses linked accounts, not the claimed ones", accountAdminOnA, "integrations_delete_config", webhook("wh-on-b", "", authzAcctA), false},
		{"delete of an unlinked webhook falls back to the claimed accounts", accountAdminOnA, "integrations_delete_config", webhook("wh-unknown", "", authzAcctA), true},
		{"delete of an unlinked webhook on another account refused", accountAdminOnA, "integrations_delete_config", webhook("wh-unknown", "", authzAcctB), false},
		{"delete of an unlinked webhook with no accounts refused", accountAdminOnA, "integrations_delete_config", webhook("wh-unknown", ""), false},
		{"delete refused when lookup fails", accountAdminOnA, "integrations_delete_config", webhook("wh-lookup-fails", "", authzAcctA), false},
		{"create refused when lookup fails", accountAdminOnA, "integrations_create_config", webhook("wh-lookup-fails", "", authzAcctA), false},
		// A create whose name exists becomes an update of that row and unlinks
		// the accounts the request leaves out.
		{"create re-saving own existing webhook", accountAdminOnA, "integrations_create_config", webhook("wh-on-a", "", authzAcctA), true},
		{"create cannot take over another account's webhook by name", accountAdminOnA, "integrations_create_config", webhook("wh-on-b", "", authzAcctA), false},
		{"delete of another source's row is not decided by the user row", accountAdminOnA, "integrations_delete_config", integrationActionTarget{IntegrationName: workflowWebhookIntegration, IntegrationConfigName: "wh-on-b", Source: "agent", AccountIds: []string{authzAcctA}}, true},
		{"workflow carve-out does not cover status updates", accountAdminOnA, "integrations_update_status", webhook("wh-on-a", ""), false},
		{"workflow carve-out does not cover other types", accountAdminOnA, "integrations_create_config", integrationActionTarget{IntegrationName: "datadog", AccountIds: []string{authzAcctA}}, false},
		{"workflows grant does not cover other types", workflowsWrite, "integrations_delete_config", integrationActionTarget{IntegrationName: "datadog"}, false},
		{"ambiguous request refused on the workflow carve-out", accountAdminOnA, "integrations_create_config", integrationActionTarget{IntegrationName: workflowWebhookIntegration, IntegrationConfigName: "wh-new", AccountIds: []string{authzAcctA}, Ambiguous: true}, false},
		{"ambiguous request still allowed for a tenant admin", tenantAdmin, "integrations_create_config", integrationActionTarget{IntegrationName: "datadog", Ambiguous: true}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := canRunIntegrationAction(authzRequestContext(t, tc.caller), tc.action, tc.target)
			assert.Equal(t, tc.want, got)
		})
	}
}

func TestIntegrationTargetFromRequest(t *testing.T) {
	got := integrationTargetFromRequest(map[string]any{
		"integration_name":        "workflow_webhook",
		"integration_config_name": "wh-1",
		"integration_id":          "id-1",
		"source":                  "user",
		"account_ids":             []any{authzAcctA, 7, authzAcctB},
	})
	assert.Equal(t, integrationActionTarget{
		IntegrationName:       "workflow_webhook",
		IntegrationConfigName: "wh-1",
		IntegrationId:         "id-1",
		Source:                "user",
		AccountIds:            []string{authzAcctA, authzAcctB},
	}, got)
	// The handlers decode case-insensitively, so this check must too.
	assert.Equal(t, "id-2", integrationTargetFromRequest(map[string]any{"Integration_Id": "id-2"}).IntegrationId)
	assert.Equal(t, integrationActionTarget{}, integrationTargetFromRequest(map[string]any{"integration_name": 5}))
	// Two spellings of one field: the handler would decode the exact-case key,
	// so the target is ambiguous rather than whichever key came last.
	for _, field := range []string{"integration_name", "integration_config_name", "integration_id", "source", "account_ids"} {
		assert.True(t, integrationTargetFromRequest(map[string]any{field: "a", strings.ToUpper(field): "b"}).Ambiguous, field)
	}
	assert.False(t, integrationTargetFromRequest(map[string]any{"integration_name": "a", "integration_id": "b"}).Ambiguous)
}

// TestIntegrationActionClassCoversEveryRoutedAction pins integrationActionClass
// to actions.yaml: every action routed to /rpc/integration must be decided by
// the table or authorize itself in the handler, and a write verb must need
// Write. Without this a new action would be refused (fail closed) here while
// the gateway lets it through. The EE overlay of actions.yaml is not read: OSS
// code may not reference EE paths (scripts/check-oss-boundary.sh), and it routes
// no integration action today — one added there and not here is refused, the
// safe direction.
func TestIntegrationActionClassCoversEveryRoutedAction(t *testing.T) {
	raw, err := os.ReadFile("../../../app/src/lib/actions.yaml")
	require.NoError(t, err)
	var doc struct {
		Actions []struct {
			Name       string `yaml:"name"`
			Definition struct {
				Handler string `yaml:"handler"`
			} `yaml:"definition"`
		} `yaml:"actions"`
	}
	require.NoError(t, yaml.Unmarshal(raw, &doc))
	actions := doc.Actions

	writeVerbs := map[string]bool{"create": true, "update": true, "delete": true, "upsert": true}
	routed := 0
	for _, a := range actions {
		if !strings.HasSuffix(a.Definition.Handler, "/rpc/integration") {
			continue
		}
		routed++
		if integrationActionsAuthorizedInHandler[a.Name] {
			continue
		}
		class, ok := integrationActionClass[a.Name]
		if !assert.Truef(t, ok, "%s is routed to /rpc/integration but missing from integrationActionClass", a.Name) {
			continue
		}
		verb := strings.SplitN(strings.TrimPrefix(strings.TrimPrefix(a.Name, "integrations_"), "integration_"), "_", 2)[0]
		if writeVerbs[verb] {
			assert.Equalf(t, "Write", class, "%s has write verb %q", a.Name, verb)
		} else {
			assert.Equalf(t, "Read", class, "%s has read verb %q", a.Name, verb)
		}
	}
	assert.NotZero(t, routed, "no /rpc/integration actions found — has the yaml shape changed?")
}
