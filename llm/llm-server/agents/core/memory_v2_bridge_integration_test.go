package core

// Executor-layer integration test for the Memory Module bridge.
//
// This simulates an agent turn — the bridge is exactly what
// agents/core/executor.go:315 calls when it asks for the memory slab. No LLM
// provider is involved; we only verify what the executor sees before it
// forwards the prompt to the planner.
//
// Gated by RUN_MEMORY_INTEGRATION=true; see memory/memory_integration_test.go
// for the matching gating convention.
//
// Run:
//   set -a && source .env && set +a
//   RUN_MEMORY_INTEGRATION=true go test -v -run TestBridge_AgentTurnFlow ./agents/core/...

import (
	"context"
	"os"
	"strings"
	"testing"

	"nudgebee/llm/common"
	"nudgebee/llm/config"
	"nudgebee/llm/memory"
	"nudgebee/llm/security"
	toolcore "nudgebee/llm/tools/core"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// stubAgent is the minimal NBAgent for driving the bridge. We don't need a
// real agent because the bridge only reads the agent's name.
// On phase-1 the bridge resolves the agent module to "generic" (phase-0 adds
// first-class module classification; this test expects that to land later).
type stubAgent struct {
	name string
}

func (a *stubAgent) GetName() string                                                { return a.name }
func (a *stubAgent) GetNameAliases() []string                                       { return nil }
func (a *stubAgent) GetDescription() string                                         { return "stub agent for tests" }
func (a *stubAgent) GetSupportedTools(_ *security.RequestContext) []toolcore.NBTool { return nil }
func (a *stubAgent) GetSystemPrompt(_ *security.RequestContext, _ NBAgentRequest) NBAgentPrompt {
	return NBAgentPrompt{}
}
func (a *stubAgent) GetPlannerType() AgentPlannerType { return AgentPlannerTypeReWoo }

// skipIfNoIntegration gates the test on RUN_MEMORY_INTEGRATION=true and a
// reachable metastore.
func skipIfNoIntegration(t *testing.T) {
	t.Helper()
	if os.Getenv("RUN_MEMORY_INTEGRATION") != "true" {
		t.Skip("set RUN_MEMORY_INTEGRATION=true to run (needs Postgres with Phase-1 migrations)")
	}
	if os.Getenv("LLM_SERVER_DB_URL") == "" {
		t.Skip("LLM_SERVER_DB_URL not set")
	}
	if _, err := common.GetDatabaseManager(common.Metastore); err != nil {
		t.Skipf("metastore unreachable: %v", err)
	}
}

// TestBridge_AgentTurnFlow walks through the executor-side of a conversation:
//
//  1. Seed a user's Soul + Preferences via the public memory API.
//  2. Build a RequestContext that looks like what the executor has at a turn.
//  3. Call composeMemoryV2Block — the exact function executor.go invokes.
//  4. Verify the returned block matches what the LLM will receive, and that
//     the contents line up with the DB state.
//
// Three sub-scenarios cover flag off (rollback), enrolled tenant (primary
// path), and unenrolled tenant (tenant allowlist gating).
func TestBridge_AgentTurnFlow(t *testing.T) {
	skipIfNoIntegration(t)

	tenantID := "bridge-test-" + uuid.NewString()
	userID := "bridge-user-" + uuid.NewString()
	accountID := "bridge-acct-" + uuid.NewString()

	// Flag setup: restore afterwards.
	prev := struct {
		module, compose, soul, prefs bool
		allowlist                    string
	}{
		module:    config.Config.MemoryModuleEnabled,
		compose:   config.Config.MemoryComposeEnabled,
		soul:      config.Config.MemoryLayerSoulEnabled,
		prefs:     config.Config.MemoryLayerPrefsEnabled,
		allowlist: config.Config.MemoryTenantAllowlist,
	}
	config.Config.MemoryModuleEnabled = true
	config.Config.MemoryComposeEnabled = true
	config.Config.MemoryLayerSoulEnabled = true
	config.Config.MemoryLayerPrefsEnabled = true
	config.Config.MemoryTenantAllowlist = tenantID
	defer func() {
		config.Config.MemoryModuleEnabled = prev.module
		config.Config.MemoryComposeEnabled = prev.compose
		config.Config.MemoryLayerSoulEnabled = prev.soul
		config.Config.MemoryLayerPrefsEnabled = prev.prefs
		config.Config.MemoryTenantAllowlist = prev.allowlist
	}()

	// Cleanup rows on exit regardless of outcome.
	defer func() {
		_ = memory.Default().Erase(context.Background(), memory.EraseRequest{
			TenantID: tenantID, UserID: userID,
		})
		if db, err := common.GetDatabaseManager(common.Metastore); err == nil {
			_, _ = db.Db.Exec(`DELETE FROM llm_memory_events WHERE tenant_id = $1`, tenantID)
		}
	}()

	// Seed the user's memory exactly as a user would via /v1/memory_v2.
	m := memory.Default()
	_, err := m.Mutate(context.Background(), memory.MutateRequest{
		TenantID: tenantID, UserID: userID,
		Layer: "soul", Action: "set",
		ActorKind: "user", ActorID: userID,
		Value: map[string]any{
			"style": map[string]any{
				"tone":            "terse",
				"expertise_level": "expert",
				"prefer_cli":      true,
			},
			"markdown": "SRE for payments. I live in kubectl. Skip verbose intros.",
		},
	})
	require.NoError(t, err, "seed soul")

	_, err = m.Mutate(context.Background(), memory.MutateRequest{
		TenantID: tenantID, UserID: userID,
		Layer: "preferences", Action: "set", Key: "preferred_cloud",
		ActorKind: "user", ActorID: userID,
		Value: map[string]any{"value": "k8s", "agent_module": ""},
	})
	require.NoError(t, err, "seed preference")

	// Build an executor-shaped RequestContext. This is what the agent loop has.
	secCtx := security.NewSecurityContextForTenantAccountAdmin(tenantID, userID, []string{accountID})
	reqCtx := security.NewRequestContext(context.Background(), secCtx, nil, nil, nil)

	// The agent that would be executing the user's query this turn.
	agent := &stubAgent{name: "sre_debug"}

	// The user's query that triggered this turn.
	req := NBAgentRequest{
		Query:     "my payments service is erroring, investigate",
		UserId:    userID,
		AccountId: accountID,
		SessionId: "sess-" + uuid.NewString(),
	}

	t.Run("flag on + enrolled tenant: bridge returns soul+prefs block", func(t *testing.T) {
		block := composeMemoryV2Block(reqCtx, req, agent)
		require.NotEmpty(t, block, "bridge should return the memory slab block")

		// These are the exact substrings the LLM will see in the prompt.
		assert.Contains(t, block, "<user_style>")
		assert.Contains(t, block, "tone: terse")
		assert.Contains(t, block, "expertise_level: expert")
		assert.Contains(t, block, "prefer_cli_over_console: true")
		assert.Contains(t, block, "SRE for payments")
		assert.Contains(t, block, "</user_style>")
		assert.Contains(t, block, "<user_preferences>")
		assert.Contains(t, block, "preferred_cloud: k8s")
		assert.Contains(t, block, "</user_preferences>")

		// Ordering: style before preferences.
		assert.Less(t, strings.Index(block, "<user_style>"),
			strings.Index(block, "<user_preferences>"),
			"style should come before preferences in the rendered block")

		t.Logf("\n==== what the executor would append to request.AccountPrompt ====\n%s\n==== end ====\n", block)
	})

	t.Run("flag off: bridge returns empty (rollback path)", func(t *testing.T) {
		config.Config.MemoryModuleEnabled = false
		defer func() { config.Config.MemoryModuleEnabled = true }()

		block := composeMemoryV2Block(reqCtx, req, agent)
		assert.Empty(t, block, "flag off → empty block → prompt byte-identical to main")
	})

	t.Run("different tenant (not allowlisted): bridge returns empty", func(t *testing.T) {
		otherSec := security.NewSecurityContextForTenantAccountAdmin(
			"other-tenant-"+uuid.NewString(), userID, []string{accountID})
		otherCtx := security.NewRequestContext(context.Background(), otherSec, nil, nil, nil)
		block := composeMemoryV2Block(otherCtx, req, agent)
		assert.Empty(t, block, "non-enrolled tenant → empty block")
	})

	t.Run("module-scoped preference visible under generic (phase-1 bridge)", func(t *testing.T) {
		// On phase-1 the bridge resolves the agent module to the literal
		// "generic" (phase-0 introduces proper classification). Cross-agent
		// preferences (agent_module NULL) always surface; module-scoped
		// rows won't surface until phase-0 lands.
		_, err := m.Mutate(context.Background(), memory.MutateRequest{
			TenantID: tenantID, UserID: userID,
			Layer: "preferences", Action: "set", Key: "notification_channel",
			ActorKind: "user", ActorID: userID,
			Value: map[string]any{"value": "slack:#oncall"},
		})
		require.NoError(t, err)

		block := composeMemoryV2Block(reqCtx, req, agent)
		assert.Contains(t, block, "notification_channel: slack:#oncall",
			"cross-agent preference must appear for any agent")
	})
}
