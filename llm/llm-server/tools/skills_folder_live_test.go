package tools

import (
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"nudgebee/llm/config"
	"nudgebee/llm/security"
	"nudgebee/llm/tools/core"
	"nudgebee/llm/workspace"
)

// Copies a synced skill's folder into a real local workspace and runs one of
// its scripts there. Needs a database with the skill synced and active, and a
// workspace cluster (docker-desktop), e.g.:
//
//	RUN_SKILL_FOLDER_LIVE=true TEST_TENANT=… TEST_USER=… TEST_ACCOUNT=… TEST_SKILL=skill-creator \
//	TEST_SCRIPT='python3 scripts/aggregate_benchmark.py --help' TEST_EXPECT='usage:' \
//	LLM_SERVER_DB_URL=… LLM_SERVER_WORKSPACE_KUBE_CONTEXT=docker-desktop \
//	LLM_SERVER_AGENT_CODEAGENT_IMAGE=nudgebee/code-analysis-cli:local go test ./tools -run SkillFolderLive -v
func TestSkillFolderLive(t *testing.T) {
	if os.Getenv("RUN_SKILL_FOLDER_LIVE") != "true" {
		t.Skip("local workspace and a synced skill required")
	}
	old := config.Config
	defer func() { config.Config = old }()
	config.Config.LlmServerKnowledgeWorkspaceEnabled = true
	account := os.Getenv("TEST_ACCOUNT")
	ctx := core.NbToolContext{
		Ctx:       security.NewRequestContextForTenantAccountAdmin(os.Getenv("TEST_TENANT"), os.Getenv("TEST_USER"), []string{account}),
		AccountId: account, UserId: os.Getenv("TEST_USER"), ConversationId: uuid.NewString(), MessageId: uuid.NewString(),
	}
	skill := os.Getenv("TEST_SKILL")

	start := time.Now()
	resp, err := (LoadSkillsTool{}).Call(ctx, core.NBToolCallRequest{Arguments: map[string]any{"skill_name": skill + "/"}})
	require.NoError(t, err)
	require.Equal(t, core.NBToolResponseStatusSuccess, resp.Status, fmt.Sprint(resp.Data))
	t.Logf("first load (%s):\n%s", time.Since(start).Round(time.Millisecond), resp.Data)

	start = time.Now()
	resp, err = (LoadSkillsTool{}).Call(ctx, core.NBToolCallRequest{Arguments: map[string]any{"skill_name": skill + "/"}})
	require.NoError(t, err)
	require.Contains(t, fmt.Sprint(resp.Data), "Already up to date:")
	t.Logf("second load (%s): not rewritten", time.Since(start).Round(time.Millisecond))

	wm := workspace.NewWorkspaceManagerWithTimeout(60 * time.Second)
	out, err := wm.ExecuteCommand(ctx.Ctx, ctx.AccountId, ctx.ConversationId, "cd skills/"+skillDirName(skill)+" && head -4 SKILL.md && cat .skill-version && echo && "+os.Getenv("TEST_SCRIPT"), nil)
	require.NoError(t, err)
	t.Logf("workspace output:\n%s", out)
	require.Contains(t, out, os.Getenv("TEST_EXPECT"))
}
