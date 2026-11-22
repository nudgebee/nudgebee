package api

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	metricnoop "go.opentelemetry.io/otel/metric/noop"
	tracenoop "go.opentelemetry.io/otel/trace/noop"

	"nudgebee/llm/common"
	"nudgebee/llm/config"
	"nudgebee/llm/security"
)

// Callers for the AI-admin grant tests. Every one is in grantsTenant, whose
// accounts are authzAcctA and authzAcctB; the security context services-server
// would return for each is its wire form.
const grantsTenant = "33333333-3333-3333-3333-333333333333"

func grantsWire(roles []string, extra string) string {
	r, _ := json.Marshal(roles)
	w := `{"TenantId":"` + grantsTenant + `","UserId":"u","Roles":` + string(r) +
		`,"AccountIds":["` + authzAcctA + `","` + authzAcctB + `"]`
	if extra != "" {
		w += "," + extra
	}
	return w + "}"
}

var grantsCallers = map[string]string{
	"tenant_admin":          grantsWire([]string{security.AUTH_TENANT_ADMIN_ROLE}, ""),
	"tenant_admin_readonly": grantsWire([]string{security.AUTH_TENANT_READ_ADMIN_ROLE}, ""),
	"super_admin":           grantsWire([]string{security.AUTH_SUPER_ADMIN_FULL_ROLE}, ""),
	"account_admin_a": grantsWire([]string{security.AUTH_ACCOUNT_ADMIN_ROLE},
		`"ScopedEntityIds":{"account_admin":["`+authzAcctA+`"]}`),
	"account_admin_readonly_a": grantsWire([]string{security.AUTH_ACCOUNT_READ_ADMIN_ROLE},
		`"ScopedEntityIds":{"account_admin_readonly":["`+authzAcctA+`"]}`),
	"no_role":               grantsWire(nil, ""),
	"budget_read":           grantsWire(nil, `"CustomPermissions":{"ai_budget:Read":true}`),
	"budget_write":          grantsWire(nil, `"CustomPermissions":{"ai_budget:Write":true}`),
	"budget_read_scoped_a":  grantsWire(nil, `"ScopedCustomPermissions":{"`+authzAcctA+`":{"ai_budget:Read":true}}`),
	"budget_write_scoped_a": grantsWire(nil, `"ScopedCustomPermissions":{"`+authzAcctA+`":{"ai_budget:Write":true}}`),
	"budget_read_scoped_b":  grantsWire(nil, `"ScopedCustomPermissions":{"`+authzAcctB+`":{"ai_budget:Read":true}}`),
	"models_read":           grantsWire(nil, `"CustomPermissions":{"ai_models:Read":true}`),
	"models_write":          grantsWire(nil, `"CustomPermissions":{"ai_models:Write":true}`),
	"models_write_scoped_a": grantsWire(nil, `"ScopedCustomPermissions":{"`+authzAcctA+`":{"ai_models:Write":true}}`),
	"models_execute":        grantsWire(nil, `"CustomPermissions":{"ai_models:Execute":true}`),
	"kbs_read":              grantsWire(nil, `"CustomPermissions":{"ai_kbs:Read":true}`),
	"kbs_write":             grantsWire(nil, `"CustomPermissions":{"ai_kbs:Write":true}`),
	"kbs_write_scoped_a":    grantsWire(nil, `"ScopedCustomPermissions":{"`+authzAcctA+`":{"ai_kbs:Write":true}}`),
	"agents_write":          grantsWire(nil, `"CustomPermissions":{"ai_agents:Write":true}`),
}

// grantsServer serves the handlers registered by register, with services-server
// stubbed to answer each caller's security context and the database unavailable.
// An authorized call therefore gets past the check and fails on the database
// (500, or 200 for a handler that needs none); a refused one is a 403.
func grantsServer(t *testing.T, register func(*gin.Engine)) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			UserId string `json:"user_id"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		wire, ok := grantsCallers[body.UserId]
		if !ok {
			w.WriteHeader(404)
			return
		}
		_, _ = w.Write([]byte(`{"context":` + wire + `}`))
	}))
	t.Cleanup(srv.Close)
	prev := config.Config
	t.Cleanup(func() { config.Config = prev })
	config.Config.ServiceEndpoint = srv.URL
	config.Config.LlmServerSecurityMode = ""
	config.Config.SecurityContextRetryAttempts = 0
	config.Config.ServiceApiServerTimeoutSeconds = 5

	common.RegisterDatabaseManagerHook(common.Metastore, func() (*common.DatabaseManager, error) {
		return nil, errors.New("no database in this test")
	})
	t.Cleanup(func() { common.RegisterDatabaseManagerHook(common.Metastore, nil) })
	security.SetTenantIdCacheForTest(authzAcctA, grantsTenant)
	security.SetTenantIdCacheForTest(authzAcctB, grantsTenant)

	r := gin.New()
	register(r)
	return r
}

func grantsCall(t *testing.T, r *gin.Engine, path, action, caller string, request map[string]any) int {
	t.Helper()
	body, err := json.Marshal(map[string]any{"action": map[string]any{"name": action}, "input": map[string]any{"request": request}})
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-tenant-id", grantsTenant)
	req.Header.Set("x-user-id", caller)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code == 401 {
		t.Fatalf("%s: caller %s was not authenticated: %s", path, caller, rec.Body.String())
	}
	return rec.Code
}

type grantCase struct {
	caller string
	allow  bool
}

func runGrantCases(t *testing.T, r *gin.Engine, path string, request map[string]any, cases []grantCase) {
	t.Helper()
	runGrantActionCases(t, r, path, "test", request, cases)
}

func runGrantActionCases(t *testing.T, r *gin.Engine, path, action string, request map[string]any, cases []grantCase) {
	t.Helper()
	for _, tc := range cases {
		t.Run(tc.caller, func(t *testing.T) {
			code := grantsCall(t, r, path, action, tc.caller, request)
			if tc.allow && code == 403 {
				t.Errorf("%s refused (403), want allowed", tc.caller)
			}
			if !tc.allow && code != 403 {
				t.Errorf("%s got %d, want 403", tc.caller, code)
			}
		})
	}
}

// Budgets & Limits: reads are for tenant readers and tenant-wide ai_budget
// Read/Write grants; writes for tenant/super admins and tenant-wide
// ai_budget:Write. Account-scoped grants and other modules' grants do not reach
// the tenant-level screens.
func TestBudgetGrants(t *testing.T) {
	r := grantsServer(t, func(r *gin.Engine) {
		handleBudgetStatusApi(r, tracenoop.NewTracerProvider().Tracer("test"), metricnoop.NewMeterProvider().Meter("test"))
	})

	reads := []grantCase{
		{"tenant_admin", true},
		{"tenant_admin_readonly", true},
		{"super_admin", true},
		{"budget_read", true},
		{"budget_write", true},
		{"account_admin_a", false},
		{"no_role", false},
		{"budget_read_scoped_a", false},
		{"models_write", false},
	}
	t.Run("ai_get_budget_system_defaults", func(t *testing.T) {
		runGrantCases(t, r, "/v1/budget/system-defaults", map[string]any{}, reads)
	})
	t.Run("ai_list_budget_config", func(t *testing.T) {
		runGrantCases(t, r, "/v1/budget/config/list", map[string]any{}, reads)
	})
	t.Run("ai_list_budget_config for one account", func(t *testing.T) {
		runGrantCases(t, r, "/v1/budget/config/list", map[string]any{"entity_type": "account", "entity_id": authzAcctA}, reads)
	})

	t.Run("ai_get_budget_status for an account", func(t *testing.T) {
		runGrantCases(t, r, "/v1/budget/status", map[string]any{"account_id": authzAcctA}, []grantCase{
			{"tenant_admin", true},
			{"tenant_admin_readonly", true},
			{"account_admin_a", true},
			{"account_admin_readonly_a", true},
			{"budget_read", true},
			{"budget_write", true},
			{"budget_read_scoped_a", true},
			{"budget_read_scoped_b", false},
			{"no_role", false},
			{"models_read", false},
		})
	})

	writes := []grantCase{
		{"tenant_admin", true},
		{"super_admin", true},
		{"budget_write", true},
		{"tenant_admin_readonly", false},
		{"account_admin_a", false},
		{"budget_read", false},
		{"budget_write_scoped_a", false},
		{"models_write", false},
	}
	t.Run("ai_upsert_budget_config for the tenant", func(t *testing.T) {
		runGrantCases(t, r, "/v1/budget/config/upsert",
			map[string]any{"entity_type": "tenant", "entity_id": grantsTenant, "module": "investigation"}, writes)
	})
	t.Run("ai_upsert_budget_config for an account", func(t *testing.T) {
		runGrantCases(t, r, "/v1/budget/config/upsert",
			map[string]any{"entity_type": "account", "entity_id": authzAcctA, "module": "investigation"}, writes)
	})
	t.Run("ai_delete_budget_config", func(t *testing.T) {
		runGrantCases(t, r, "/v1/budget/config/delete", map[string]any{"id": "cfg-1"}, writes)
	})
}

// Model pricing: the list keeps its built-in audience and adds tenant-wide
// ai_models Read/Write; changing prices is tenant admin or tenant-wide
// ai_models:Write.
func TestModelPricingGrants(t *testing.T) {
	r := grantsServer(t, func(r *gin.Engine) {
		handleConversationApis(r, tracenoop.NewTracerProvider().Tracer("test"), metricnoop.NewMeterProvider().Meter("test"))
	})

	t.Run("ai_list_model_pricing", func(t *testing.T) {
		runGrantCases(t, r, "/v1/conversations/ai_list_model_pricing", map[string]any{}, []grantCase{
			{"tenant_admin", true},
			{"tenant_admin_readonly", true},
			{"account_admin_a", true},
			{"super_admin", true},
			{"models_read", true},
			{"models_write", true},
			{"no_role", false},
			{"models_write_scoped_a", false},
			{"models_execute", false},
			{"budget_write", false},
		})
	})

	writes := []grantCase{
		{"tenant_admin", true},
		{"models_write", true},
		{"tenant_admin_readonly", false},
		{"account_admin_a", false},
		{"models_read", false},
		{"models_write_scoped_a", false},
		{"budget_write", false},
	}
	t.Run("ai_upsert_model_pricing", func(t *testing.T) {
		runGrantCases(t, r, "/v1/conversations/ai_upsert_model_pricing", map[string]any{"prices": []any{}}, writes)
	})
	t.Run("ai_delete_model_pricing", func(t *testing.T) {
		runGrantCases(t, r, "/v1/conversations/ai_delete_model_pricing",
			map[string]any{"provider_name": "openai", "model_name": "gpt"}, writes)
	})
}

// ai_update_kb_visibility's handler gate: tenant admins as before, plus a
// tenant-wide ai_kbs:Write grant (the service then pins it to the KB's tenant —
// see knowledge.TestOrgKBSharingGrants).
func TestKBVisibilityHandlerGrants(t *testing.T) {
	r := grantsServer(t, func(r *gin.Engine) {
		handleKnowledgebaseApis(r, tracenoop.NewTracerProvider().Tracer("test"), metricnoop.NewMeterProvider().Meter("test"))
	})
	runGrantActionCases(t, r, "/v1/knowledgebases", "ai_update_kb_visibility",
		map[string]any{"kb_id": "kb-1", "visibility": "organisation"}, []grantCase{
			{"tenant_admin", true},
			{"super_admin", true},
			{"kbs_write", true},
			{"tenant_admin_readonly", false},
			{"account_admin_a", false},
			{"kbs_read", false},
			{"kbs_write_scoped_a", false},
			{"agents_write", false},
		})
}
