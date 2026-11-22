package tools

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"nudgebee/llm/config"
	"nudgebee/llm/knowledge"
	"nudgebee/llm/security"
	"nudgebee/llm/tools/core"
	"nudgebee/llm/workspace"
)

// skillFolderWorkspace records saved files and serves them back to ReadFile.
type skillFolderWorkspace struct {
	workspace.WorkspaceManager
	files  map[string]string
	saves  []string
	failOn string
}

func (w *skillFolderWorkspace) CallAPIOrLazyCreate(_ *security.RequestContext, _, method, endpoint string, _ map[string]string, body any) ([]byte, error) {
	b := body.(map[string]string)
	if method != "POST" || endpoint != "/api/v1/files/save" || b["conversation_id"] != "conv-1" {
		return nil, errors.New("unexpected call")
	}
	if b["path"] == w.failOn {
		return nil, errors.New("disk full")
	}
	w.saves = append(w.saves, b["path"])
	w.files[b["path"]] = b["content"]
	return []byte(`{}`), nil
}

func (w *skillFolderWorkspace) ReadFile(_ *security.RequestContext, _, conversationId, path string) ([]byte, error) {
	if c, ok := w.files[path]; ok && conversationId == "conv-1" {
		return []byte(c), nil
	}
	return nil, errors.New("not found")
}

func TestLoadSkillsCopiesASkillFolderOnce(t *testing.T) {
	oldEnabled := config.Config.LlmServerKnowledgeWorkspaceEnabled
	oldGet, oldWS, oldRecorded := getSkillFolderFn, newSkillWorkspace, skillScriptsRecordedFn
	t.Cleanup(func() {
		config.Config.LlmServerKnowledgeWorkspaceEnabled = oldEnabled
		getSkillFolderFn, newSkillWorkspace, skillScriptsRecordedFn = oldGet, oldWS, oldRecorded
	})
	config.Config.LlmServerKnowledgeWorkspaceEnabled = true
	folder := &knowledge.SkillFolder{
		SkillID: "kb-1", SkillName: "PromQL Helper", SkillMD: "Use scripts/check.py.", ContentSHA: "v1",
		Files: map[string]string{"scripts/check.py": "print(42)", "references/a.md": "notes"},
	}
	getSkillFolderFn = func(_ *security.RequestContext, account, skill string) (*knowledge.SkillFolder, error) {
		if account == "acct-1" && skill == "PromQL Helper" {
			return folder, nil
		}
		return nil, nil
	}
	ws := &skillFolderWorkspace{files: map[string]string{}}
	newSkillWorkspace = func() workspace.WorkspaceManager { return ws }
	var checked []string
	recorded := true
	skillScriptsRecordedFn = func(account, conv string) bool { checked = append(checked, account+"|"+conv); return recorded }

	ctx := core.NbToolContext{Ctx: security.NewRequestContextForSuperAdmin(), AccountId: "acct-1", ConversationId: "conv-1", MessageId: "m"}
	call := func(name string) core.NBToolResponse {
		t.Helper()
		resp, err := LoadSkillsTool{}.Call(ctx, core.NBToolCallRequest{Arguments: map[string]any{"skill_name": name}})
		require.NoError(t, err)
		return resp
	}

	resp := call("PromQL Helper/")
	require.Equal(t, core.NBToolResponseStatusSuccess, resp.Status, resp.Data)
	assert.Contains(t, resp.Data, "Copied skill PromQL Helper at skills/promql-helper/")
	assert.Contains(t, resp.Data, "- skills/promql-helper/scripts/check.py")
	assert.Contains(t, resp.Data, `work_dir "skills/promql-helper"`)
	assert.Equal(t, map[string]string{
		"skills/promql-helper/SKILL.md":         "Use scripts/check.py.",
		"skills/promql-helper/scripts/check.py": "print(42)",
		"skills/promql-helper/references/a.md":  "notes",
		"skills/promql-helper/.skill-version":   "v1",
	}, ws.files)
	assert.Equal(t, "skills/promql-helper/.skill-version", ws.saves[len(ws.saves)-1], "the marker is written last")
	assert.Equal(t, []string{"acct-1|conv-1"}, checked, "the conversation's record is checked first")

	// Same version: nothing rewritten.
	ws.saves = nil
	resp = call("PromQL Helper/")
	assert.Contains(t, resp.Data, "Already up to date:")
	assert.Empty(t, ws.saves)

	// A new version, or a workspace that lost the folder, is written again.
	folder.ContentSHA = "v2"
	call("PromQL Helper/")
	assert.Len(t, ws.saves, 4)
	assert.Equal(t, "v2", ws.files["skills/promql-helper/.skill-version"])

	// A failed copy leaves the marker unwritten, so it is redone next time.
	folder.ContentSHA = "v3"
	ws.failOn, ws.saves = "skills/promql-helper/scripts/check.py", nil
	resp = call("PromQL Helper/")
	assert.Equal(t, core.NBToolResponseStatusError, resp.Status)
	assert.Contains(t, resp.Data, "disk full")
	assert.NotContains(t, ws.saves, "skills/promql-helper/.skill-version")
	ws.failOn = ""

	// When the conversation has no record of this request, nothing is copied.
	recorded, ws.saves = false, nil
	resp = call("PromQL Helper/")
	assert.Equal(t, core.NBToolResponseStatusError, resp.Status)
	assert.Contains(t, resp.Data, "nothing was copied")
	assert.Empty(t, ws.saves)
	recorded = true

	// A name that isn't a safe folder name is refused before anything is written.
	for _, bad := range []string{"..", ".", "-x", "a..b/c"} {
		folder.SkillName = bad
		ws.saves = nil
		resp = call("PromQL Helper/")
		assert.Equal(t, core.NBToolResponseStatusError, resp.Status, bad)
		assert.Contains(t, resp.Data, "can't be used as a folder name", bad)
		assert.Empty(t, ws.saves, bad)
	}
	folder.SkillName = "PromQL Helper"

	resp = call("unknown/")
	assert.Equal(t, core.NBToolResponseStatusError, resp.Status)
	assert.Contains(t, resp.Data, `no skill synced from a repository is named "unknown"`)

	config.Config.LlmServerKnowledgeWorkspaceEnabled = false
	resp = call("PromQL Helper/")
	assert.Contains(t, resp.Data, "needs the workspace")
}

func TestSplitSkillFolderName(t *testing.T) {
	for in, want := range map[string]string{"promql/": "promql", " PromQL Helper / ": "PromQL Helper"} {
		got, ok := splitSkillFolderName(in)
		require.True(t, ok, in)
		assert.Equal(t, want, got)
	}
	for _, in := range []string{"promql", "promql/a.md", "a,b/", "/", "knowledge:x/", "a/b/"} {
		_, ok := splitSkillFolderName(in)
		assert.False(t, ok, in)
	}
}

func TestShellCommandRunsCode(t *testing.T) {
	for _, c := range []string{
		`{"command":"python3 scripts/a.py"}`, "bash x.sh", "cat a | sh", "echo $(python -c 1)", "./run", "env FOO=1 node x.js", "make test",
		"xargs -I{} perl {}",
		// Spellings a deny list misses (review of this change).
		"timeout 10 python3 x.py", "nice -n 5 python3 x.py", "sudo -u root python3 x.py", "env -u A python3 x.py",
		"if true; then python3 x.py; fi", "for i in 1; do bash x.sh; done", "source scripts/x.sh", ". scripts/x.sh",
		"eval python3 x.py", "python3.11 x.py", "ipython x.py", "tsx x.ts", "awk 'BEGIN{system(\"id\")}'", "sed e x",
		"scripts/run.sh", "/work/skills/a/run.sh", "py''thon3 x.py", "P=python3; $P x.py", "\\python3 x.py",
		"(python3 x.py)", "find . -name '*.py' -exec python3 {} ;", "find . -delete", "busybox sh x.sh",
		// Review of the tokenizer: programs run from arguments, quoted options,
		// process substitution.
		"rg --pre python3 . scripts/", "rg --pre-glob '*.py' --pre python3 x", "sort --compress-program=sh -S 1 big.txt",
		"sort --compress=sh f", "cat <(python3 scripts/x.py)", `find . -name '*.py' '-exec' python3 {} \;`,
		`sed -n 1p "-e" "1e id" SKILL.md`, "find . -fprintf out.txt '%p'", "less x.txt",
		// Writes into files, an expanding here-doc, a file descriptor that is a file.
		"cat a.py > skills/foo/scripts/run.py", "echo x >> notes.txt", "ls >& out.txt", "cat <<EOF\n$(id)\nEOF",
		"cat a 1<> b", "x=1", "ls &", "time ls", "[[ -f a ]] && ls", "f() { ls; }; f", "cat 'unterminated",
		// sed only reads when it prints a line range with -n.
		"sed -n '1,5p;e id' a.py", "sed -n '1w out' a.py", "sed -n 1,5p -i a.py", "sed -n 1p a.txt > skills/foo/scripts/run.py", "sed -n 1p a.txt >x.py", "sed -i 's/a/b/' a.py", "sed '1,5p' a.py",
	} {
		assert.True(t, ShellCommandRunsCode(c), c)
	}
	for _, c := range []string{
		`{"command":"cat SKILL.md"}`, "ls scripts", "grep -rn python references", "head -5 scripts/a.py",
		"kubectl get pods", "cat ./notes.txt", "find . -name '*.py'", "cd skills/x && ls -la && wc -l scripts/a.py",
		"jq .name data.json | sort | uniq", "",
		"sed -n '51,120p' skills/skill-creator/scripts/quick_validate.py", "sed -n 80p SKILL.md references/a.md", "which python3 python",
		"ls 2>/dev/null", "kubectl get pods 2>&1 | head -5", "grep -n x < a.txt", "cat <<'EOF'\nhello\nEOF",
		"rg -n 'def main' scripts/", "sort -u a.txt | uniq -c", `cat "a b.txt"`, "find . -name '*.py' -type f",
		"ls > /dev/null 2>&1",
	} {
		assert.False(t, ShellCommandRunsCode(c), c)
	}
}

func TestShellCommandNamesSkillFolder(t *testing.T) {
	for _, c := range []string{
		`{"command":"python3 scripts/a.py","work_dir":"skills/promql"}`,
		`{"command":"python3 skills/promql/scripts/a.py"}`,
		`{"command":"python3 /tmp/code-analysis/exec_workspaces/abc/skills/promql/scripts/a.py --help"}`,
		`{"command":"cd skills/promql && bash run.sh"}`,
		"cp ./skills/promql/a.py /tmp/x.py",
	} {
		assert.True(t, ShellCommandNamesSkillFolder(c), c)
	}
	for _, c := range []string{
		`{"command":"python3 analyze.py"}`, `{"command":"ls myskills/x"}`, `{"command":"echo skills"}`, `{"command":"python3 x.py","work_dir":"reports"}`,
	} {
		assert.False(t, ShellCommandNamesSkillFolder(c), c)
	}
}

func TestShellConfirmationQuestionForSkillScripts(t *testing.T) {
	q := ShellTool{}.ConfirmationQuestion(`{"command":"python3 scripts/check.py --help","work_dir":"skills/promql"}`)
	assert.Equal(t, "Run this command on skill folder **promql** in the workspace (in skills/promql)? Skill files may come from a repository rather than from Nudgebee, so each command that can run code there needs approval.\nCommand - python3 scripts/check.py --help", q)
	assert.Contains(t, ShellTool{}.ConfirmationQuestion(`{"command":"python3 /tmp/x/skills/beyla/scripts/a.py"}`), "skill folder **beyla**")
	assert.Equal(t, "Run this command in the workspace (in reports)? This conversation has skill files from a repository rather than from Nudgebee, so each command that can run code needs approval.\nCommand - which python3",
		SkillConversationQuestion(`{"command":"which python3","work_dir":"reports"}`))
	for _, other := range []string{
		`{"command":"kubectl delete pod x"}`,                    // a cluster write keeps the default card
		`{"command":"python3 analyze.py"}`,                      // code outside a skill folder
		`{"command":"cat SKILL.md","work_dir":"skills/promql"}`, // a read
	} {
		assert.Empty(t, ShellTool{}.ConfirmationQuestion(other), other)
	}
}
