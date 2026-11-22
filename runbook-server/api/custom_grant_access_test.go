package api

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"nudgebee/runbook/config"
	"nudgebee/runbook/services/optimizer"
	"nudgebee/runbook/services/security"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"
)

// wireContextBuilder builds the context from a fixed security-context wire and
// sets the action's grant key the way buildContextFromPayload does.
type wireContextBuilder struct{ sc *security.SecurityContext }

func (b *wireContextBuilder) BuildContextFromRequestPayload(context.Context, *gin.Context, map[string]string, *trace.Tracer, *metric.Meter, *slog.Logger) (*security.RequestContext, error) {
	return security.NewRequestContext(context.Background(), b.sc, slog.Default(), nil, nil), nil
}

func (b *wireContextBuilder) BuildContextFromPayload(ctx context.Context, _ *gin.Context, h *ActionRequest, _ *trace.Tracer, _ *metric.Meter, _ *slog.Logger) (*security.RequestContext, error) {
	rc := security.NewRequestContext(ctx, b.sc, slog.Default(), nil, nil)
	rc.SetActionPermission(security.ActionKey(h.Action.Name, h.Action.Permission))
	return rc, nil
}

// The autopilot write actions admit a custom-role user holding the action's
// grant, refuse one without it before the service is called, and leave built-in
// roles as they were. workflow_sync_templates reloads the shared templates for
// every tenant, so it is non-grantable: tenant admins only.
func TestGrantableActionsHonourCustomGrants(t *testing.T) {
	orig := config.Config.OptimizationEnabled
	config.Config.OptimizationEnabled = true
	t.Cleanup(func() { config.Config.OptimizationEnabled = orig })

	const other = "33333333-3333-3333-3333-333333333333"
	ctxWire := func(extra string) string {
		return `{"TenantId":"t1","UserId":"u1","AccountIds":["` + gateAcctA + `","` + gateAcctB + `"],` + extra + `}`
	}
	var (
		aoWriteTenant  = ctxWire(`"Roles":[],"CustomPermissions":{"autooptimize:Write":true}`)
		aoWriteScoped  = ctxWire(`"Roles":[],"ScopedCustomPermissions":{"` + gateAcctA + `":{"autooptimize:Write":true}}`)
		aoWriteOtherAc = ctxWire(`"Roles":[],"ScopedCustomPermissions":{"` + gateAcctB + `":{"autooptimize:Write":true}}`)
		aoRead         = ctxWire(`"Roles":[],"CustomPermissions":{"autooptimize:Read":true}`)
		wfWrite        = ctxWire(`"Roles":[],"CustomPermissions":{"workflows:Write":true}`)
		wfExecute      = ctxWire(`"Roles":[],"CustomPermissions":{"workflows:Execute":true}`)
		wfExecScoped   = ctxWire(`"Roles":[],"ScopedCustomPermissions":{"` + gateAcctA + `":{"workflows:Execute":true}}`)
		tenantAdmin    = ctxWire(`"Roles":["tenant_admin"]`)
		accountAdmin   = ctxWire(`"Roles":["account_admin"],"ScopedEntityIds":{"account_admin":["` + gateAcctA + `"]}`)
		accountReader  = ctxWire(`"Roles":["account_admin_readonly"],"ScopedEntityIds":{"account_admin_readonly":["` + gateAcctA + `"]}`)
	)

	// Approving lets the autopilot apply its plan: it is autooptimize:Execute,
	// granted apart from configuring the autopilot (autooptimize:Write).
	autopilot := []struct {
		action, path string
		execute      bool
	}{
		{"autooptimize_create", "/autopilot", false},
		{"autooptimize_update", "/autopilot/update", false},
		{"autooptimize_update_status", "/autopilot/status", false},
		{"autooptimize_update_approval", "/autopilot/approval", true},
	}
	type call struct {
		wire    string
		allowed bool
	}
	aoExecTenant := ctxWire(`"Roles":[],"CustomPermissions":{"autooptimize:Execute":true}`)
	aoExecScoped := ctxWire(`"Roles":[],"ScopedCustomPermissions":{"` + gateAcctA + `":{"autooptimize:Execute":true}}`)
	casesFor := func(execute bool) map[string]call {
		return map[string]call{
			"tenant-wide autooptimize:Write":            {aoWriteTenant, !execute},
			"autooptimize:Write bound to the account":   {aoWriteScoped, !execute},
			"tenant-wide autooptimize:Execute":          {aoExecTenant, execute},
			"autooptimize:Execute bound to the account": {aoExecScoped, execute},
			"autooptimize:Write on another account":     {aoWriteOtherAc, false},
			"autooptimize:Read only":                    {aoRead, false},
			"another module's grant":                    {wfWrite, false},
			"built-in tenant admin":                     {tenantAdmin, true},
			"built-in account admin":                    {accountAdmin, true},
			"built-in account reader":                   {accountReader, false},
		}
	}

	serve := func(t *testing.T, wire, path string, body map[string]any) (*optimizer.MockOptimizerService, int) {
		t.Helper()
		ops := &optimizer.MockOptimizerService{}
		id := "x"
		ops.On("CreateAutoOptimize", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&id, nil).Maybe()
		ops.On("UpdateAutoOptimize", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&id, nil).Maybe()
		ops.On("ChangeStatus", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()
		ops.On("DecideApproval", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return("ok", nil).Maybe()
		ws := new(MockWorkflowService)
		server := NewServer(ws, new(MockConfigService))
		server.SetWorkflowService(ws)
		server.SetOptimizerService(ops)
		server.securityContextBuilder = &wireContextBuilder{sc: gateContext(t, wire)}
		raw, err := json.Marshal(body)
		require.NoError(t, err)
		req := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(raw))
		req.Header.Set(serviceTokenHeader, testServiceToken)
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		server.router.ServeHTTP(rec, req)
		return ops, rec.Code
	}

	for _, route := range autopilot {
		for name, tc := range casesFor(route.execute) {
			t.Run(route.action+"/"+name, func(t *testing.T) {
				ops, code := serve(t, tc.wire, route.path, map[string]any{
					"action": map[string]any{"name": route.action},
					"input": map[string]any{
						"account_id": gateAcctA,
						"id":         other,
						"status":     "ACTIVE",
						"name":       "n",
					},
				})
				if tc.allowed {
					assert.NotEqual(t, http.StatusUnauthorized, code)
					return
				}
				assert.Equal(t, http.StatusUnauthorized, code)
				assert.Empty(t, ops.Calls, "refused before the service is called")
			})
		}
	}

	syncCases := map[string]call{
		"tenant-wide workflows:Execute":         {wfExecute, false},
		"workflows:Execute bound to an account": {wfExecScoped, false},
		"workflows:Write is not Execute":        {wfWrite, false},
		"another module's grant":                {aoWriteTenant, false},
		"built-in tenant admin":                 {tenantAdmin, true},
		"built-in account admin":                {accountAdmin, false},
	}
	for name, tc := range syncCases {
		t.Run("workflow_sync_templates/"+name, func(t *testing.T) {
			_, code := serve(t, tc.wire, "/rpc", map[string]any{
				"action": map[string]any{"name": "workflow_sync_templates"},
				"input":  map[string]any{},
			})
			if tc.allowed {
				// Past the check: the test server has no template syncer.
				assert.Equal(t, http.StatusServiceUnavailable, code)
				return
			}
			assert.Equal(t, http.StatusUnauthorized, code)
		})
	}
}
