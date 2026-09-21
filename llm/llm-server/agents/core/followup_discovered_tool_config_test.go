package core

import (
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/jmoiron/sqlx"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"nudgebee/llm/common"
	"nudgebee/llm/security"
	toolcore "nudgebee/llm/tools/core"
)

// discoveredFakeConfigTool: minimal NBTool+NBToolConfig for the search_tools-discovery
// path, no dependency on real tools from the `tools` package.
type discoveredFakeConfigTool struct{}

func (discoveredFakeConfigTool) Name() string                 { return "fake_discovered_configurable_tool" }
func (discoveredFakeConfigTool) Description() string          { return "test-only configurable tool" }
func (discoveredFakeConfigTool) GetType() toolcore.NBToolType { return toolcore.NBToolTypeTool }
func (discoveredFakeConfigTool) InputSchema() toolcore.ToolSchema {
	return toolcore.ToolSchema{}
}
func (discoveredFakeConfigTool) Call(_ toolcore.NbToolContext, _ toolcore.NBToolCallRequest) (toolcore.NBToolResponse, error) {
	return toolcore.NBToolResponse{}, nil
}
func (discoveredFakeConfigTool) ConfigSchema(_ *security.RequestContext) toolcore.ToolConfigSchema {
	return toolcore.ToolConfigSchema{
		ConfigType:   "fake_discovered_type",
		ConfigSource: toolcore.ToolConfigSourceIntegration,
	}
}

// discoveredFakeCasingTool: separate from discoveredFakeConfigTool so each subtest
// gets its own name/config (RegisterNBToolFactory is package-level).
type discoveredFakeCasingTool struct{}

func (discoveredFakeCasingTool) Name() string                 { return "fake_discovered_casing_tool" }
func (discoveredFakeCasingTool) Description() string          { return "test-only configurable tool" }
func (discoveredFakeCasingTool) GetType() toolcore.NBToolType { return toolcore.NBToolTypeTool }
func (discoveredFakeCasingTool) InputSchema() toolcore.ToolSchema {
	return toolcore.ToolSchema{}
}
func (discoveredFakeCasingTool) Call(_ toolcore.NbToolContext, _ toolcore.NBToolCallRequest) (toolcore.NBToolResponse, error) {
	return toolcore.NBToolResponse{}, nil
}
func (discoveredFakeCasingTool) ConfigSchema(_ *security.RequestContext) toolcore.ToolConfigSchema {
	return toolcore.ToolConfigSchema{
		ConfigType:   "fake_discovered_casing_type",
		ConfigSource: toolcore.ToolConfigSourceIntegration,
	}
}

// preloadedFakeCasingTool: covers a preloaded (not discovered) tool called
// with non-canonical casing.
type preloadedFakeCasingTool struct{}

func (preloadedFakeCasingTool) Name() string                 { return "fake_preloaded_casing_tool" }
func (preloadedFakeCasingTool) Description() string          { return "test-only configurable tool" }
func (preloadedFakeCasingTool) GetType() toolcore.NBToolType { return toolcore.NBToolTypeTool }
func (preloadedFakeCasingTool) InputSchema() toolcore.ToolSchema {
	return toolcore.ToolSchema{}
}
func (preloadedFakeCasingTool) Call(_ toolcore.NbToolContext, _ toolcore.NBToolCallRequest) (toolcore.NBToolResponse, error) {
	return toolcore.NBToolResponse{}, nil
}
func (preloadedFakeCasingTool) ConfigSchema(_ *security.RequestContext) toolcore.ToolConfigSchema {
	return toolcore.ToolConfigSchema{
		ConfigType:   "fake_preloaded_casing_type",
		ConfigSource: toolcore.ToolConfigSourceIntegration,
	}
}

// TestFollowupRequestForMultipleToolConfigs_DiscoveredTool is the regression test
// for #36653: a search_tools-discovered configurable tool w/ 2+ configs must get a
// disambiguation follow-up instead of silently executing with none.
func TestFollowupRequestForMultipleToolConfigs_DiscoveredTool(t *testing.T) {
	// One shared mock for all subtests: GetDatabaseManager caches Metastore globally
	// on first use, so a later hook registration in this binary would be ignored.
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	sqlxDB := sqlx.NewDb(db, "postgresql")
	common.RegisterDatabaseManagerHook(common.Metastore, func() (*common.DatabaseManager, error) {
		return &common.DatabaseManager{Db: sqlxDB}, nil
	})

	cols := []string{"id", "name", "labels", "config_name", "config_value", "config_encrypted"}

	t.Run("exact name match", func(t *testing.T) {
		const toolName = "fake_discovered_configurable_tool"
		toolcore.RegisterNBToolFactory(toolName, func(_ string) (toolcore.NBTool, error) {
			return discoveredFakeConfigTool{}, nil
		})

		const accountId = "acct-discovered-1"
		const conversationId = "conv-discovered-1"

		mock.ExpectQuery(`FROM integrations i`).
			WithArgs(accountId, "fake_discovered_type").
			WillReturnRows(sqlmock.NewRows(cols).
				AddRow("cfg-1", "ConfigA", "{}", "url", "https://a", false).
				AddRow("cfg-2", "ConfigB", "{}", "url", "https://b", false))

		// Simulate search_tools discovery this conversation (see auth_agent.go:84).
		RecordDiscoveredTools(conversationId, []string{toolName})

		agent := &MockAgent{SupportedTools: []toolcore.NBTool{}} // tool deliberately NOT preloaded
		action := NBAgentPlannerToolAction{Tool: toolName, ToolID: "step-1"}
		query := NBAgentRequest{
			AccountId:      accountId,
			ConversationId: conversationId,
			AgentId:        "11111111-1111-1111-1111-111111111111",
		}

		ctx := &security.RequestContext{}
		followupRequest, err := FollowupRequestForMultipleToolConfigs(ctx, query, agent, action)
		require.NoError(t, err)
		require.NoError(t, mock.ExpectationsWereMet())

		assert.NotEmpty(t, followupRequest.Question, "must ask which config to use instead of silently proceeding with none")
		assert.Equal(t, FollowupTypeToolConfig, followupRequest.FollowupType)
		assert.ElementsMatch(t, []string{"ConfigA", "ConfigB"}, followupRequest.FollowupOptions)
	})

	// Casing mismatch: discovery match is case-insensitive, but the match loop
	// below compares verbatim — must normalize or it silently skips.
	t.Run("casing mismatch normalizes to canonical", func(t *testing.T) {
		const canonicalName = "fake_discovered_casing_tool"
		const calledName = "Fake_Discovered_Casing_Tool" // non-canonical casing the planner might emit
		toolcore.RegisterNBToolFactory(canonicalName, func(_ string) (toolcore.NBTool, error) {
			return discoveredFakeCasingTool{}, nil
		})

		const accountId = "acct-discovered-casing-1"
		const conversationId = "conv-discovered-casing-1"

		mock.ExpectQuery(`FROM integrations i`).
			WithArgs(accountId, "fake_discovered_casing_type").
			WillReturnRows(sqlmock.NewRows(cols).
				AddRow("cfg-1", "ConfigA", "{}", "url", "https://a", false).
				AddRow("cfg-2", "ConfigB", "{}", "url", "https://b", false))

		RecordDiscoveredTools(conversationId, []string{canonicalName})

		agent := &MockAgent{SupportedTools: []toolcore.NBTool{}}
		action := NBAgentPlannerToolAction{Tool: calledName, ToolID: "step-1"}
		query := NBAgentRequest{
			AccountId:      accountId,
			ConversationId: conversationId,
			AgentId:        "33333333-3333-3333-3333-333333333333",
		}

		ctx := &security.RequestContext{}
		followupRequest, err := FollowupRequestForMultipleToolConfigs(ctx, query, agent, action)
		require.NoError(t, err)
		require.NoError(t, mock.ExpectationsWereMet())

		assert.NotEmpty(t, followupRequest.Question, "casing mismatch must not suppress the disambiguation follow-up")
		assert.Equal(t, canonicalName, followupRequest.ToolName)
	})

	// Same casing gap, but for a PRELOADED tool: the "already known" map lookup
	// is exact, so wrong casing must still resolve here, not via discovery.
	t.Run("preloaded tool casing mismatch normalizes to canonical", func(t *testing.T) {
		const canonicalName = "fake_preloaded_casing_tool"
		const calledName = "Fake_Preloaded_Casing_Tool"
		tool := preloadedFakeCasingTool{}

		const accountId = "acct-preloaded-casing-1"
		const conversationId = "conv-preloaded-casing-1"

		mock.ExpectQuery(`FROM integrations i`).
			WithArgs(accountId, "fake_preloaded_casing_type").
			WillReturnRows(sqlmock.NewRows(cols).
				AddRow("cfg-1", "ConfigA", "{}", "url", "https://a", false).
				AddRow("cfg-2", "ConfigB", "{}", "url", "https://b", false))

		// Preloaded, not discovered — casing is the only mismatch.
		agent := &MockAgent{SupportedTools: []toolcore.NBTool{tool}}
		action := NBAgentPlannerToolAction{Tool: calledName, ToolID: "step-1"}
		query := NBAgentRequest{
			AccountId:      accountId,
			ConversationId: conversationId,
			AgentId:        "44444444-4444-4444-4444-444444444444",
		}

		ctx := &security.RequestContext{}
		followupRequest, err := FollowupRequestForMultipleToolConfigs(ctx, query, agent, action)
		require.NoError(t, err)
		require.NoError(t, mock.ExpectationsWereMet())

		assert.NotEmpty(t, followupRequest.Question, "casing mismatch on a preloaded tool must not suppress the disambiguation follow-up")
		assert.Equal(t, canonicalName, followupRequest.ToolName)
	})
}

// TestFollowupRequestForMultipleToolConfigs_UndiscoveredToolStillIgnored: a tool
// neither preloaded nor discovered must still be ignored, same as before.
func TestFollowupRequestForMultipleToolConfigs_UndiscoveredToolStillIgnored(t *testing.T) {
	agent := &MockAgent{SupportedTools: []toolcore.NBTool{}}
	action := NBAgentPlannerToolAction{Tool: "never_discovered_tool", ToolID: "step-1"}
	query := NBAgentRequest{
		AccountId:      "acct-2",
		ConversationId: "conv-without-discovery",
		AgentId:        "22222222-2222-2222-2222-222222222222",
	}

	ctx := &security.RequestContext{}
	followupRequest, err := FollowupRequestForMultipleToolConfigs(ctx, query, agent, action)
	require.NoError(t, err)
	assert.Empty(t, followupRequest.Question)
}
