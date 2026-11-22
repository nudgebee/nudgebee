package api

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"nudgebee/services/common"
	"nudgebee/services/security"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const miscGrantAccount = "11111111-1111-1111-1111-111111111111"

// miscGrantCase is one grantable action whose handler used to ask only for a
// built-in role (or for a Read grant on an Execute action).
type miscGrantCase struct {
	action  string
	key     string // the action's grant key in action_keys.json
	handler func(*ActionRequest, *gin.Context)
	input   map[string]any
	// readers: built-in read-only roles reach the action (actions.yaml admits them).
	readers bool
}

var miscGrantCases = []miscGrantCase{
	{
		action:  "slo_config_list",
		key:     "slo:Read",
		handler: func(a *ActionRequest, c *gin.Context) { handleSloAction(a, c, nil, nil, slog.Default()) },
		input: map[string]any{"cloud_account_id": miscGrantAccount, "workload_id": []any{"w"},
			"workload_name": []any{"w"}, "namespace": []any{"ns"}},
		readers: true,
	},
	{
		action:  "slo_config_create",
		key:     "slo:Write",
		handler: func(a *ActionRequest, c *gin.Context) { handleSloAction(a, c, nil, nil, slog.Default()) },
		input: map[string]any{"cloud_account_id": miscGrantAccount, "workload_id": "w", "workload_name": "w", "namespace": "ns",
			"config": []any{map[string]any{"name": "availability", "threshold": 99, "goal": 99, "enabled": true}}},
	},
	{
		action:  "slo_config_update",
		key:     "slo:Write",
		handler: func(a *ActionRequest, c *gin.Context) { handleSloAction(a, c, nil, nil, slog.Default()) },
		input: map[string]any{"cloud_account_id": miscGrantAccount, "workload_id": "w", "workload_name": "w", "namespace": "ns",
			"config": []any{map[string]any{"name": "availability", "threshold": 99, "goal": 99, "enabled": true}}},
	},
	{
		action:  "slo_config_delete",
		key:     "slo:Write",
		handler: func(a *ActionRequest, c *gin.Context) { handleSloAction(a, c, nil, nil, slog.Default()) },
		input:   map[string]any{"cloud_account_id": miscGrantAccount, "workload_name": "w", "namespace": "ns"},
	},
	{
		action:  "forecast_generate",
		key:     "metrics:Execute",
		handler: func(a *ActionRequest, c *gin.Context) { handleForecastGenerate(a, c, nil, nil, slog.Default()) },
		input: map[string]any{"account_id": miscGrantAccount, "workload_namespace": "ns", "workload_name": "w",
			"kind": "Deployment", "metric": "cpu_saturation", "threshold": 80},
		readers: true,
	},
	{
		action:  "agents_create_token",
		key:     "agents:Write",
		handler: func(a *ActionRequest, c *gin.Context) { handleAccountAction(a, c, nil, nil, slog.Default()) },
		input:   map[string]any{"account_id": miscGrantAccount},
	},
	{
		action:  "ml_get_metrics",
		key:     "ml:Read",
		handler: func(a *ActionRequest, c *gin.Context) { handleMlAction(a, c, nil, nil, slog.Default()) },
		input:   map[string]any{"account": miscGrantAccount, "deployment": "w", "namespace": "ns"},
		readers: true,
	},
	{
		action:  "ml_generate_node_recommendations",
		key:     "ml:Execute",
		handler: func(a *ActionRequest, c *gin.Context) { handleMlAction(a, c, nil, nil, slog.Default()) },
		input:   map[string]any{"account": miscGrantAccount},
		readers: true,
	},
	{
		action:  "vm_get_service_map",
		key:     "vm:Read",
		handler: func(a *ActionRequest, c *gin.Context) { handleApplicationEvent(a, c, nil, nil, slog.Default()) },
		input:   map[string]any{"account_id": miscGrantAccount},
		readers: true,
	},
	{
		action:  "ai_feedback_create",
		key:     "ai_conversations:Write",
		handler: func(a *ActionRequest, c *gin.Context) { handleAiFeedbackAction(a, c, nil, nil, slog.Default()) },
		input: map[string]any{"cloud_account_id": miscGrantAccount, "session_id": "s", "module": "m", "question": "q",
			"llm_response": "r", "conversation_id": "conv", "useful": true},
		readers: true,
	},
	{
		action:  "ai_list_own_conversation_feedback",
		key:     "ai_conversations:Read",
		handler: func(a *ActionRequest, c *gin.Context) { handleAiFeedbackAction(a, c, nil, nil, slog.Default()) },
		input:   map[string]any{"cloud_account_id": miscGrantAccount, "session_id": []any{"s"}},
		readers: true,
	},
	{
		action:  "dashboards_execute_query",
		key:     "dashboards:Execute",
		handler: func(a *ActionRequest, c *gin.Context) { handleDashboardAction(a, c, nil, nil, slog.Default()) },
		input:   map[string]any{"account_id": miscGrantAccount, "datasource": "redis", "command": "INFO"},
		readers: true,
	},
	{
		// An unsupported datasource fails right after the account check, so the
		// query engine (which applies its own per-table grant) is not reached.
		action:  "dashboards_execute_entity_query",
		key:     "dashboards:Execute",
		handler: func(a *ActionRequest, c *gin.Context) { handleDashboardAction(a, c, nil, nil, slog.Default()) },
		input: map[string]any{"account_ids": []any{miscGrantAccount}, "datasource": "logs",
			"query": map[string]any{"table": "events_v2"}},
		readers: true,
	},
	{
		action:  "cloud_list_notification_targets",
		key:     "cloud:Read",
		handler: func(a *ActionRequest, c *gin.Context) { handleCloudAction(a, c, nil, nil, slog.Default()) },
		input:   map[string]any{"account_id": miscGrantAccount},
		readers: true,
	},
	{
		action:  "accounts_sync",
		key:     "accounts:Execute",
		handler: func(a *ActionRequest, c *gin.Context) { handleCloudAction(a, c, nil, nil, slog.Default()) },
		input:   map[string]any{"account_id": miscGrantAccount},
	},
	{
		action:  "cloud_sync_service",
		key:     "cloud:Execute",
		handler: func(a *ActionRequest, c *gin.Context) { handleCloudAction(a, c, nil, nil, slog.Default()) },
		input:   map[string]any{"account_id": miscGrantAccount, "service_name": "AmazonEC2"},
	},
	{
		action:  "cloud_apply_command",
		key:     "cloud:Execute",
		handler: func(a *ActionRequest, c *gin.Context) { handleCloudAction(a, c, nil, nil, slog.Default()) },
		input: map[string]any{"account_id": miscGrantAccount, "service_name": "AmazonEC2", "region": "us-east-1",
			"resource_id": "i-1", "command": "stop"},
	},
	{
		action:  "cloud_execute_command",
		key:     "cloud:Execute",
		handler: func(a *ActionRequest, c *gin.Context) { handleCloudAction(a, c, nil, nil, slog.Default()) },
		input:   map[string]any{"account_id": miscGrantAccount, "commands": []any{"aws ec2 describe-instances"}},
	},
	{
		action:  "metrics_list",
		key:     "metrics:Read",
		handler: func(a *ActionRequest, c *gin.Context) { handleMetricsAction(a, c, nil, nil, slog.Default()) },
		input:   map[string]any{"request": map[string]any{"account_id": miscGrantAccount}},
		readers: true,
	},
	{
		action:  "metrics_list_names",
		key:     "metrics:Read",
		handler: func(a *ActionRequest, c *gin.Context) { handleMetricsAction(a, c, nil, nil, slog.Default()) },
		input:   map[string]any{"request": map[string]any{"account_id": miscGrantAccount}},
		readers: true,
	},
	{
		action:  "metrics_list_labels",
		key:     "metrics:Read",
		handler: func(a *ActionRequest, c *gin.Context) { handleMetricsAction(a, c, nil, nil, slog.Default()) },
		input:   map[string]any{"request": map[string]any{"account_id": miscGrantAccount}},
		readers: true,
	},
	{
		action:  "metrics_list_label_values",
		key:     "metrics:Read",
		handler: func(a *ActionRequest, c *gin.Context) { handleMetricsAction(a, c, nil, nil, slog.Default()) },
		input:   map[string]any{"request": map[string]any{"account_id": miscGrantAccount, "label": "pod"}},
		readers: true,
	},
	{
		action:  "metrics_aggregate_utilisation",
		key:     "metrics:Read",
		handler: func(a *ActionRequest, c *gin.Context) { handleMetricsAction(a, c, nil, nil, slog.Default()) },
		input:   map[string]any{"request": map[string]any{"account_id": miscGrantAccount}},
		readers: true,
	},
	{
		action:  "metrics_check_vm_status",
		key:     "metrics:Read",
		handler: func(a *ActionRequest, c *gin.Context) { handleMetricsAction(a, c, nil, nil, slog.Default()) },
		input:   map[string]any{"request": map[string]any{"account_id": miscGrantAccount}},
		readers: true,
	},
	{
		action:  "metrics_get_query",
		key:     "metrics:Read",
		handler: func(a *ActionRequest, c *gin.Context) { handleMetricsAction(a, c, nil, nil, slog.Default()) },
		input:   map[string]any{"request": map[string]any{"account_id": miscGrantAccount}},
		readers: true,
	},
	{
		// Served through the relay route the gateway forwards it to; past the
		// account check the forward fails (no relay server), with 502.
		action:  "relay_forward_request",
		key:     "k8s:Read",
		handler: serveRelayRequest,
		input:   map[string]any{"body": map[string]any{"account_id": miscGrantAccount, "action_name": "get_resource"}},
		readers: true,
	},
}

// serveRelayRequest posts the action's input to /v1/relay/request as the gateway
// does, with the caller's identity headers.
func serveRelayRequest(a *ActionRequest, c *gin.Context) {
	router := gin.New()
	handleRelayApis(router, nil, nil, slog.Default())
	raw, _ := json.Marshal(map[string]any{"input": a.Input})
	req := httptest.NewRequest(http.MethodPost, "/v1/relay/request", bytes.NewReader(raw))
	req.Header = c.Request.Header.Clone()
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(c.Writer, req)
}

// miscGrantCallers are the security contexts each action is called with. The
// grant placeholder is replaced by the case's key, or another module's.
var miscGrantCallers = []struct {
	name    string
	context string
	allowed func(tc miscGrantCase) bool
}{
	{
		name:    "custom role, tenant-wide grant for the action's key",
		context: `{"AccountIds":["` + miscGrantAccount + `"],"Roles":[],"CustomPermissions":{"KEY":true}}`,
		allowed: func(miscGrantCase) bool { return true },
	},
	{
		name:    "custom role, grant for the action's key bound to the account",
		context: `{"AccountIds":["` + miscGrantAccount + `"],"Roles":[],"ScopedCustomPermissions":{"` + miscGrantAccount + `":{"KEY":true}}}`,
		allowed: func(miscGrantCase) bool { return true },
	},
	{
		name:    "custom role, grant bound to another account",
		context: `{"AccountIds":["` + miscGrantAccount + `"],"Roles":[],"ScopedCustomPermissions":{"99999999-9999-9999-9999-999999999999":{"KEY":true}}}`,
		allowed: func(miscGrantCase) bool { return false },
	},
	{
		name:    "custom role, another module's grant",
		context: `{"AccountIds":["` + miscGrantAccount + `"],"Roles":[],"CustomPermissions":{"OTHER":true}}`,
		allowed: func(miscGrantCase) bool { return false },
	},
	{
		name:    "custom role, no grant",
		context: `{"AccountIds":["` + miscGrantAccount + `"],"Roles":[]}`,
		allowed: func(miscGrantCase) bool { return false },
	},
	{
		name: "built-in account_admin",
		context: `{"AccountIds":["` + miscGrantAccount + `"],"Roles":["account_admin"],
			"ScopedEntityIds":{"account_admin":["` + miscGrantAccount + `"]}}`,
		allowed: func(miscGrantCase) bool { return true },
	},
	{
		name: "built-in account_admin_readonly",
		context: `{"AccountIds":["` + miscGrantAccount + `"],"Roles":["account_admin_readonly"],
			"ScopedEntityIds":{"account_admin_readonly":["` + miscGrantAccount + `"]}}`,
		allowed: func(tc miscGrantCase) bool { return tc.readers },
	},
}

// Each of these handlers admits a custom-role user holding the grant the gateway
// admitted the action on (tenant-wide or bound to the account), refuses one
// without it before touching the database, and keeps admitting the built-in
// roles it admitted before.
func TestMiscHandlersHonourCustomGrants(t *testing.T) {
	gin.SetMode(gin.TestMode)
	keys := security.ActionKeys()
	for i, tc := range miscGrantCases {
		require.Equal(t, tc.key, keys[tc.action], "grant key of %s", tc.action)
		other := "k8s:Read"
		if strings.HasPrefix(tc.key, "k8s:") {
			other = "events:Read"
		}
		for j, caller := range miscGrantCallers {
			t.Run(tc.action+"/"+caller.name, func(t *testing.T) {
				// No expectations: past the access check the handler's first query
				// fails, so a refused caller is told apart by its 403 / "unauthorized",
				// and nothing can be written either way.
				mockMetastore(t)
				tenantId := "tenant-misc"
				userId := fmt.Sprintf("user-misc-%d-%d", i, j)
				sc := strings.NewReplacer("KEY", tc.key, "OTHER", other).Replace(caller.context)
				sc = `{"TenantId":"` + tenantId + `","UserId":"` + userId + `",` + sc[1:]
				require.NoError(t, common.CacheSet("security_context_v3", tenantId+":"+userId, []byte(sc)))
				t.Cleanup(func() { _ = common.CacheDelete("security_context_v3", tenantId+":"+userId) })

				rec := httptest.NewRecorder()
				c, _ := gin.CreateTestContext(rec)
				c.Request = httptest.NewRequest(http.MethodPost, "/rpc/test", nil)
				c.Request.Header.Set("x-tenant-id", tenantId)
				c.Request.Header.Set("x-user-id", userId)

				tc.handler(&ActionRequest{Action: ActionRequestAction{Name: tc.action}, Input: tc.input}, c)

				body := strings.ToLower(rec.Body.String())
				refused := rec.Code == http.StatusForbidden || strings.Contains(body, "unauthorized")
				assert.Equal(t, !caller.allowed(tc), refused, "status %d body %s", rec.Code, rec.Body.String())
			})
		}
	}
}
