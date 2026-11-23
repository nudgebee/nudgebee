package core

import (
	"testing"

	"github.com/stretchr/testify/require"
	"nudgebee/llm/knowledge"
	"nudgebee/llm/security"
	"nudgebee/llm/tools"
	toolcore "nudgebee/llm/tools/core"
)

// Once a synced skill's folder is in a conversation's workspace, every shell
// command that runs code needs approval (classified update); reads stay reads,
// and other conversations are unchanged.
func TestSkillScriptsConversationAsksBeforeRunningCode(t *testing.T) {
	agent := mockAgentWithTools{
		mockPlainAgent: mockPlainAgent{name: "skills_agent"},
		tools:          []toolcore.NBTool{tools.ShellTool{AccountId: "acct-skills"}},
	}
	withSkill := NBAgentRequest{AccountId: "acct-skills", ConversationId: "conv-with-skill"}
	without := NBAgentRequest{AccountId: "acct-skills", ConversationId: "conv-plain"}
	old := knowledge.ConversationHasSkillScripts
	t.Cleanup(func() { knowledge.ConversationHasSkillScripts = old })
	knowledge.ConversationHasSkillScripts = func(account, conversation string) bool {
		return account == withSkill.AccountId && conversation == withSkill.ConversationId
	}

	classify := func(request NBAgentRequest, command string) toolcore.ToolRequestType {
		t.Helper()
		finish, requestType, err := IsAgentToolAuthorizedToProcessRequest(security.NewRequestContextForSuperAdmin(), agent, request,
			NBAgentPlannerToolAction{Tool: toolcore.ToolExecuteShellCommand, ToolInput: `{"command":` + jsonString(command) + `,"work_dir":"reports"}`})
		require.NoError(t, err)
		require.Nil(t, finish)
		require.NotNil(t, requestType)
		return *requestType
	}

	for _, command := range []string{
		"python3 scripts/check.py",
		"bash scripts/run.sh --fast",
		"cat scripts/check.py | python3 -",
		"cp skills/promql/scripts/check.py /tmp/x.py && python3 /tmp/x.py",
		"python3 - <<'EOF'\nprint(1)\nEOF",
		"node -e 'console.log(1)'",
		`find . -name '*.py' -exec python3 {} \;`,
		"chmod +x scripts/run.sh && ./scripts/run.sh",
		"ls && sh scripts/run.sh",
		// Bypasses found in review of the string tokenizer (#41652).
		"rg --pre python3 . scripts/",
		"sort --compress-program=sh -S 1 big.txt",
		"cat <(python3 scripts/x.py)",
		`find . -name '*.py' '-exec' python3 {} \;`,
		`sed -n 1p "-e" "1e id" SKILL.md`,
	} {
		require.Equal(t, toolcore.ToolRequestTypeUpdate, classify(withSkill, command), command)
		want := toolcore.ToolRequestTypeRead // unchanged in a conversation without a skill folder…
		if tools.ShellCommandNamesSkillFolder(command) {
			want = toolcore.ToolRequestTypeUpdate // …unless the command names one
		}
		require.Equal(t, want, classify(without, command), command)
	}
	// In any conversation, running code from a skill folder by path asks,
	// including another conversation's copy reached by an absolute path, and
	// running from a skill folder as the working directory.
	_, inSkillDir, err := IsAgentToolAuthorizedToProcessRequest(security.NewRequestContextForSuperAdmin(), agent, without,
		NBAgentPlannerToolAction{Tool: toolcore.ToolExecuteShellCommand, ToolInput: `{"command":"python3 scripts/check.py","work_dir":"skills/promql"}`})
	require.NoError(t, err)
	require.Equal(t, toolcore.ToolRequestTypeUpdate, *inSkillDir)
	for _, command := range []string{
		"python3 skills/promql/scripts/check.py",
		"python3 /tmp/code-analysis/exec_workspaces/other/skills/promql/scripts/check.py --help",
	} {
		require.Equal(t, toolcore.ToolRequestTypeUpdate, classify(without, command), command)
	}
	for _, command := range []string{"cat SKILL.md", "ls -la scripts", "grep -n rate references/patterns.md", "wc -l scripts/check.py"} {
		require.Equal(t, toolcore.ToolRequestTypeRead, classify(withSkill, command), command)
	}
}

func jsonString(s string) string {
	b := []byte{'"'}
	for _, r := range s {
		switch r {
		case '"':
			b = append(b, '\\', '"')
		case '\\':
			b = append(b, '\\', '\\')
		case '\n':
			b = append(b, '\\', 'n')
		default:
			b = append(b, string(r)...)
		}
	}
	return string(append(b, '"'))
}

// Each command that runs a skill's code is approved on its own: a "yes" to
// reading a file with python3 -c must not run the skill's scripts after it
// (seen live: one approval covered a validator run and a PYTHONPATH rewrite).
// The same command keeps its key, so the approval it was asked for is found
// when the turn resumes; reads and other conversations keep the tool's key.
func TestSkillCodeIsApprovedPerCommand(t *testing.T) {
	old := knowledge.ConversationHasSkillScripts
	t.Cleanup(func() { knowledge.ConversationHasSkillScripts = old })
	knowledge.ConversationHasSkillScripts = func(_, conversation string) bool { return conversation == "conv-with-skill" }
	shell := tools.ShellTool{AccountId: "acct-skills"}
	key := func(conversation, command string) string {
		e := &plannerExecutor{
			ctx:          security.NewRequestContextForSuperAdmin(),
			agentRequest: NBAgentRequest{AccountId: "acct-skills", ConversationId: conversation},
		}
		action := NBAgentPlannerToolAction{Tool: toolcore.ToolExecuteShellCommand, ToolInput: `{"command":` + jsonString(command) + `}`}
		return e.approvalKey(shell, action, nil)
	}

	read := key("conv-with-skill", "python3 -c \"print(open('skills/skill-creator/scripts/quick_validate.py').read())\"")
	validate := key("conv-with-skill", "python3 skills/skill-creator/scripts/quick_validate.py skills/restart-payments")
	rewrite := key("conv-with-skill", "PYTHONPATH=/opt/azure-cli-venv/lib/python3.12/site-packages python3 skills/skill-creator/scripts/quick_validate.py skills/restart-payments")
	require.NotEqual(t, read, validate)
	require.NotEqual(t, validate, rewrite)
	require.NotEqual(t, toolcore.ToolExecuteShellCommand, validate)
	require.Equal(t, validate, key("conv-with-skill", "python3 skills/skill-creator/scripts/quick_validate.py skills/restart-payments"), "same command, same key on resume")

	require.Equal(t, toolcore.ToolExecuteShellCommand, key("conv-with-skill", "cat skills/skill-creator/SKILL.md"), "a read keeps the tool's key")
	// A key already naming a cluster gets the command too: an earlier "yes"
	// to a kubectl write on k8s-dev must not cover code chained after one.
	shellOnDev := "shell_execute@k8s-dev"
	require.Equal(t, shellOnDev, tools.ShellTool{}.ConfirmationKey(`{"command":"kubectl --context k8s-dev get pods"}`))
	chained := key("conv-with-skill", "kubectl --context k8s-dev get pods; python3 skills/x/scripts/y.py")
	require.NotEqual(t, shellOnDev, chained)
	require.Contains(t, chained, "python3 skills/x/scripts/y.py")
	require.Equal(t, toolcore.ToolExecuteShellCommand, key("conv-plain", "python3 report.py"), "no skill scripts in this conversation")
	require.NotEqual(t, toolcore.ToolExecuteShellCommand, key("conv-plain", "python3 skills/promql/scripts/check.py"), "a skill folder named by path, in any conversation")
}
