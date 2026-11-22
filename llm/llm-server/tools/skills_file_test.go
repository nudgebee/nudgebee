package tools

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"nudgebee/llm/knowledge"
	"nudgebee/llm/security"
	"nudgebee/llm/tools/core"
)

// load_skills "<skill>/<path>" reads one stored file of a skill; a missing
// file or skill is a clear failure, and other names keep their usual meaning.
func TestLoadSkillsReadsASkillFile(t *testing.T) {
	old := getSkillFileFn
	t.Cleanup(func() { getSkillFileFn = old })
	var asked [][2]string
	getSkillFileFn = func(_ *security.RequestContext, accountID, skill, file string) (*knowledge.SkillFile, error) {
		asked = append(asked, [2]string{skill, file})
		assert.Equal(t, "acct-1", accountID)
		switch {
		case skill == "promql" && file == "references/patterns.md":
			return &knowledge.SkillFile{SkillID: "kb-1", SkillName: "promql", Path: file, Content: "# Patterns\nrate(x[5m])\nhistogram_quantile(0.9, y)\n"}, nil
		case skill == "promql":
			return nil, knowledge.ErrSkillFileNotFound
		case skill == "broken":
			return nil, errors.New("database unavailable")
		}
		return nil, nil
	}
	ctx := core.NbToolContext{Ctx: security.NewRequestContextForSuperAdmin(), AccountId: "acct-1", ConversationId: "c", MessageId: "m"}
	call := func(args map[string]any) core.NBToolResponse {
		t.Helper()
		resp, err := LoadSkillsTool{}.Call(ctx, core.NBToolCallRequest{Arguments: args})
		require.NoError(t, err)
		return resp
	}

	resp := call(map[string]any{"skill_name": " promql / references/patterns.md "})
	assert.Equal(t, core.NBToolResponseStatusSuccess, resp.Status)
	assert.Contains(t, resp.Data, "File references/patterns.md of skill promql")
	assert.Contains(t, resp.Data, "histogram_quantile")
	require.Len(t, resp.References, 1)
	assert.Equal(t, "promql/references/patterns.md", resp.References[0].Text)
	assert.Equal(t, "kb-1", resp.References[0].Url)

	resp = call(map[string]any{"skill_name": "promql/references/patterns.md", "keyword": "histogram"})
	assert.Contains(t, resp.Data, "histogram_quantile")

	resp = call(map[string]any{"skill_name": "promql/missing.md"})
	assert.Equal(t, core.NBToolResponseStatusError, resp.Status)
	assert.Contains(t, resp.Data, `skill "promql" has no file "missing.md"`)

	resp = call(map[string]any{"skill_name": "nope/a.md"})
	assert.Equal(t, core.NBToolResponseStatusError, resp.Status)
	assert.Contains(t, resp.Data, `no skill named "nope"`)

	resp = call(map[string]any{"skill_name": "broken/a.md"})
	assert.Equal(t, core.NBToolResponseStatusError, resp.Status)

	assert.Equal(t, [][2]string{
		{"promql", "references/patterns.md"}, {"promql", "references/patterns.md"},
		{"promql", "missing.md"}, {"nope", "a.md"}, {"broken", "a.md"},
	}, asked, "only skill/file names reach the file lookup")
}

func TestSplitSkillFileName(t *testing.T) {
	for in, want := range map[string][2]string{
		"promql/references/a.md": {"promql", "references/a.md"},
		" promql / a.md ":        {"promql", "a.md"},
	} {
		skill, file, ok := splitSkillFileName(in)
		require.True(t, ok, in)
		assert.Equal(t, want, [2]string{skill, file}, in)
	}
	for _, in := range []string{"promql", "a,b/c", "knowledge:abc/def", "/a.md", "promql/"} {
		_, _, ok := splitSkillFileName(in)
		assert.False(t, ok, in)
	}
}

// A skill found through search is addressed by its candidate id; its files and
// folder resolve through the candidate to the skill's name.
func TestResolveSkillCandidatePath(t *testing.T) {
	ctx := core.NbToolContext{Ctx: security.NewRequestContextForSuperAdmin(), AccountId: "acct-cand", ConversationId: "conv-cand", MessageId: "msg-cand"}
	require.NoError(t, knowledge.StoreKnowledgeCandidate(ctx.AccountId, ctx.ConversationId, ctx.MessageId,
		knowledge.KnowledgeCandidate{ID: "knowledge:abc123", Title: "adaptive-metrics", KBID: "kb-1"}))
	require.NoError(t, knowledge.StoreKnowledgeCandidate(ctx.AccountId, ctx.ConversationId, ctx.MessageId,
		knowledge.KnowledgeCandidate{ID: "knowledge:doc999", Title: "Confluence page"}))

	assert.Equal(t, "adaptive-metrics/references/api.md", resolveSkillCandidatePath(ctx, "knowledge:abc123/references/api.md"))
	assert.Equal(t, "adaptive-metrics/", resolveSkillCandidatePath(ctx, " knowledge:abc123/ "))
	assert.Equal(t, "knowledge:abc123", resolveSkillCandidatePath(ctx, "knowledge:abc123"), "the skill itself loads by id as before")
	assert.Equal(t, "knowledge:doc999/a.md", resolveSkillCandidatePath(ctx, "knowledge:doc999/a.md"), "not a manual skill")
	assert.Equal(t, "knowledge:nope/a.md", resolveSkillCandidatePath(ctx, "knowledge:nope/a.md"), "unknown id")
	assert.Equal(t, "promql/a.md", resolveSkillCandidatePath(ctx, "promql/a.md"))
}
