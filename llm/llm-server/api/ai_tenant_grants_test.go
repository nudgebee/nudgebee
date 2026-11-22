package api

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	metricnoop "go.opentelemetry.io/otel/metric/noop"
	tracenoop "go.opentelemetry.io/otel/trace/noop"

	"nudgebee/llm/security"
)

func init() {
	for name, wire := range map[string]string{
		"account_admin_b":  grantsWire([]string{security.AUTH_ACCOUNT_ADMIN_ROLE}, `"ScopedEntityIds":{"account_admin":["`+authzAcctB+`"]}`),
		"agents_read":      grantsWire(nil, `"CustomPermissions":{"ai_agents:Read":true}`),
		"agents_read_b":    grantsWire(nil, `"ScopedCustomPermissions":{"`+authzAcctB+`":{"ai_agents:Read":true}}`),
		"agents_read_a":    grantsWire(nil, `"ScopedCustomPermissions":{"`+authzAcctA+`":{"ai_agents:Read":true}}`),
		"kbs_read_b":       grantsWire(nil, `"ScopedCustomPermissions":{"`+authzAcctB+`":{"ai_kbs:Read":true}}`),
		"kbs_read_a":       grantsWire(nil, `"ScopedCustomPermissions":{"`+authzAcctA+`":{"ai_kbs:Read":true}}`),
		"kbs_execute":      grantsWire(nil, `"CustomPermissions":{"ai_kbs:Execute":true}`),
		"misc_read":        grantsWire(nil, `"CustomPermissions":{"ai_misc:Read":true}`),
		"misc_write":       grantsWire(nil, `"CustomPermissions":{"ai_misc:Write":true}`),
		"misc_execute":     grantsWire(nil, `"CustomPermissions":{"ai_misc:Execute":true}`),
		"misc_write_a":     grantsWire(nil, `"ScopedCustomPermissions":{"`+authzAcctA+`":{"ai_misc:Write":true}}`),
		"models_read_a":    grantsWire(nil, `"ScopedCustomPermissions":{"`+authzAcctA+`":{"ai_models:Read":true}}`),
		"approval_read":    grantsWire(nil, `"CustomPermissions":{"toolapproval:Read":true}`),
		"approval_write":   grantsWire(nil, `"CustomPermissions":{"toolapproval:Write":true}`),
		"approval_execute": grantsWire(nil, `"CustomPermissions":{"toolapproval:Execute":true}`),
		"approval_write_a": grantsWire(nil, `"ScopedCustomPermissions":{"`+authzAcctA+`":{"toolapproval:Write":true}}`),
	} {
		grantsCallers[name] = wire
	}
}

// ai_list_kb_agents is keyed ai_agents:Read and ai_list_kb_retrieval
// ai_kbs:Read: each handler now checks the action's own module.
func TestKBHandlerModuleGrants(t *testing.T) {
	r := grantsServer(t, func(r *gin.Engine) {
		handleKnowledgebaseApis(r, tracenoop.NewTracerProvider().Tracer("test"), metricnoop.NewMeterProvider().Meter("test"))
	})
	t.Run("ai_list_kb_agents", func(t *testing.T) {
		runGrantActionCases(t, r, "/v1/knowledgebases", "ai_list_kb_agents",
			map[string]any{"account_id": authzAcctB, "kb_id": "kb-1"}, []grantCase{
				{"tenant_admin", true},
				{"account_admin_b", true},
				{"agents_read", true},
				{"agents_write", true},
				{"agents_read_b", true},
				{"account_admin_a", false},
				{"agents_read_a", false},
				{"kbs_read", false},
				{"no_role", false},
			})
	})
	t.Run("ai_list_kb_retrieval", func(t *testing.T) {
		runGrantActionCases(t, r, "/v1/knowledgebases", "ai_list_kb_retrieval",
			map[string]any{"account_id": authzAcctB, "query": "disk pressure"}, []grantCase{
				{"tenant_admin", true},
				{"account_admin_b", true},
				{"kbs_read", true},
				{"kbs_write", true},
				{"kbs_read_b", true},
				{"account_admin_a", false},
				{"kbs_read_a", false},
				{"kbs_execute", false},
				{"agents_read", false},
				{"no_role", false},
			})
	})
	t.Run("ai_list_kb_skill_sources", func(t *testing.T) {
		runGrantActionCases(t, r, "/v1/knowledgebases", "ai_list_kb_skill_sources", map[string]any{}, []grantCase{
			{"tenant_admin", true},
			{"tenant_admin_readonly", true},
			{"kbs_read", true},
			{"kbs_write", true},
			{"account_admin_a", false},
			{"kbs_read_a", false},
			{"kbs_execute", false},
			{"agents_read", false},
			{"no_role", false},
		})
	})
}

// The AI cost report schedule (ai_misc) and the tenant's model list
// (ai_models) are tenant-level: tenant roles as before, plus a tenant-wide
// grant of the action's module and class. Account-bound grants never count.
func TestTenantConversationSettingsGrants(t *testing.T) {
	r := grantsServer(t, func(r *gin.Engine) {
		handleConversationApis(r, tracenoop.NewTracerProvider().Tracer("test"), metricnoop.NewMeterProvider().Meter("test"))
	})
	t.Run("ai_get_cost_report_schedule", func(t *testing.T) {
		runGrantCases(t, r, "/v1/conversations/ai_get_cost_report_schedule", map[string]any{}, []grantCase{
			{"tenant_admin", true},
			{"tenant_admin_readonly", true},
			{"super_admin", true},
			{"misc_read", true},
			{"misc_write", true},
			{"account_admin_a", false},
			{"no_role", false},
			{"misc_execute", false},
			{"misc_write_a", false},
			{"models_read", false},
		})
	})
	t.Run("ai_upsert_cost_report_schedule", func(t *testing.T) {
		runGrantCases(t, r, "/v1/conversations/ai_upsert_cost_report_schedule", map[string]any{"send_hour_utc": 9}, []grantCase{
			{"tenant_admin", true},
			{"super_admin", true},
			{"misc_write", true},
			{"tenant_admin_readonly", false},
			{"account_admin_a", false},
			{"no_role", false},
			{"misc_read", false},
			{"misc_execute", false},
			{"misc_write_a", false},
			{"models_write", false},
		})
	})
	t.Run("ai_list_tenant_models", func(t *testing.T) {
		runGrantCases(t, r, "/v1/conversations/ai_list_tenant_models", map[string]any{}, []grantCase{
			{"tenant_admin", true},
			{"super_admin", true},
			{"models_read", true},
			{"models_write", true},
			{"account_admin_a", false},
			{"no_role", false},
			{"models_execute", false},
			{"models_read_a", false},
			{"misc_read", false},
		})
	})
}

// Tool approval rules are tenant-level: tenant roles as before, plus a
// tenant-wide toolapproval Read (or Write) grant to read them and Write to
// change them.
func TestToolApprovalGrants(t *testing.T) {
	r := grantsServer(t, func(r *gin.Engine) {
		handleToolApprovalConfigApis(r, tracenoop.NewTracerProvider().Tracer("test"), metricnoop.NewMeterProvider().Meter("test"))
	})
	reads := []grantCase{
		{"tenant_admin", true},
		{"tenant_admin_readonly", true},
		{"super_admin", true},
		{"approval_read", true},
		{"approval_write", true},
		{"account_admin_a", false},
		{"no_role", false},
		{"approval_execute", false},
		{"approval_write_a", false},
		{"misc_write", false},
	}
	writes := []grantCase{
		{"tenant_admin", true},
		{"super_admin", true},
		{"approval_write", true},
		{"tenant_admin_readonly", false},
		{"account_admin_a", false},
		{"no_role", false},
		{"approval_read", false},
		{"approval_execute", false},
		{"approval_write_a", false},
		{"misc_write", false},
	}
	for _, action := range []string{"toolapproval_list", "toolapproval_check_match", "toolapproval_list_options", "toolapproval_list_tools"} {
		t.Run(action, func(t *testing.T) {
			runGrantActionCases(t, r, "/v1/tool-approval/config", action, map[string]any{}, reads)
		})
	}
	for _, action := range []string{"toolapproval_create", "toolapproval_upsert", "toolapproval_delete"} {
		t.Run(action, func(t *testing.T) {
			runGrantActionCases(t, r, "/v1/tool-approval/config", action, map[string]any{}, writes)
		})
	}
}

// eventDigestCaller is the security context services-server returns for name.
func eventDigestCaller(t *testing.T, wire string) *security.RequestContext {
	t.Helper()
	sc := &security.SecurityContext{}
	if err := json.Unmarshal([]byte(wire), sc); err != nil {
		t.Fatal(err)
	}
	return security.NewRequestContext(context.Background(), sc, nil, nil, nil)
}

// The weekly event analysis digest is tenant-wide: reading it takes a built-in
// tenant reader or a tenant-wide events Read (or Write) grant; generating it a
// tenant writer or a tenant-wide events:Execute grant. An account-bound grant
// never reaches it.
func TestEventAnalysisDigestGrants(t *testing.T) {
	wires := map[string]string{
		"tenant_admin":          grantsCallers["tenant_admin"],
		"tenant_admin_readonly": grantsCallers["tenant_admin_readonly"],
		"account_admin_a":       grantsCallers["account_admin_a"],
		"no_role":               grantsCallers["no_role"],
		"events_read":           grantsWire(nil, `"CustomPermissions":{"events:Read":true}`),
		"events_write":          grantsWire(nil, `"CustomPermissions":{"events:Write":true}`),
		"events_execute":        grantsWire(nil, `"CustomPermissions":{"events:Execute":true}`),
		"events_read_a":         grantsWire(nil, `"ScopedCustomPermissions":{"`+authzAcctA+`":{"events:Read":true}}`),
		"events_execute_a":      grantsWire(nil, `"ScopedCustomPermissions":{"`+authzAcctA+`":{"events:Execute":true}}`),
		"misc_execute":          grantsWire(nil, `"CustomPermissions":{"ai_misc:Execute":true}`),
	}
	refused := func(err error) bool { return err != nil && strings.Contains(err.Error(), "digest: forbidden") }
	check := func(t *testing.T, cases []grantCase, call func(*security.RequestContext) error) {
		for _, tc := range cases {
			t.Run(tc.caller, func(t *testing.T) {
				err := call(eventDigestCaller(t, wires[tc.caller]))
				if tc.allow == refused(err) {
					t.Errorf("%s: allow=%v, got %v", tc.caller, tc.allow, err)
				}
			})
		}
	}
	reads := []grantCase{
		{"tenant_admin", true},
		{"tenant_admin_readonly", true},
		{"events_read", true},
		{"events_write", true},
		{"account_admin_a", false},
		{"no_role", false},
		{"events_execute", false},
		{"events_read_a", false},
		{"misc_execute", false},
	}
	// A bad period_start fails right after the check, so an allowed call stops
	// there instead of reading or generating anything.
	t.Run("events_list_analysis_digests", func(t *testing.T) {
		grantsServer(t, func(*gin.Engine) {})
		check(t, reads, func(ctx *security.RequestContext) error {
			_, err := HandleListEventAnalysisDigestsApi(ctx, ListEventAnalysisDigestsRequest{})
			return err
		})
	})
	t.Run("events_get_analysis_digest", func(t *testing.T) {
		check(t, reads, func(ctx *security.RequestContext) error {
			_, err := HandleGetEventAnalysisDigestApi(ctx, GetEventAnalysisDigestRequest{PeriodStart: "not-a-date"})
			return err
		})
	})
	t.Run("events_generate_analysis_digest", func(t *testing.T) {
		check(t, []grantCase{
			{"tenant_admin", true},
			{"events_execute", true},
			{"tenant_admin_readonly", false},
			{"account_admin_a", false},
			{"no_role", false},
			{"events_read", false},
			{"events_write", false},
			{"events_execute_a", false},
			{"misc_execute", false},
		}, func(ctx *security.RequestContext) error {
			_, err := HandleGenerateEventAnalysisDigestApi(ctx, GenerateEventAnalysisDigestRequest{PeriodStart: "not-a-date"})
			return err
		})
	})
}
