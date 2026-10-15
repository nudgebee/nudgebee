package core

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tmc/langchaingo/prompts"

	"nudgebee/llm/security"
	toolcore "nudgebee/llm/tools/core"
)

func stubAlwaysLoadSkills(t *testing.T, skills []toolcore.AlwaysLoadSkill) *[]string {
	t.Helper()
	orig := listAlwaysLoadSkillsFn
	t.Cleanup(func() { listAlwaysLoadSkillsFn = orig })
	var queried []string
	listAlwaysLoadSkillsFn = func(_ *security.RequestContext, _ string, names []string) ([]toolcore.AlwaysLoadSkill, error) {
		queried = append(queried, names...)
		return skills, nil
	}
	return &queried
}

func TestResolveAgentInstructions_ParentNamesFirst(t *testing.T) {
	queried := stubAlwaysLoadSkills(t, []toolcore.AlwaysLoadSkill{
		{ID: "kb-parent", Name: "ES setup", AgentId: "metrics", Data: "metrics live in ccs-self:metrics-*"},
		{ID: "kb-own", Name: "Units", AgentId: "elastic_search_metrics", Data: "report latency in ms"},
	})
	ctx := security.NewRequestContextForSuperAdmin()

	text, ids := resolveAgentInstructions(ctx, "acc-1",
		[]string{"elastic_search_metrics", toolcore.KBAgentWildcard, "metrics"}, []string{"metrics"})

	assert.Equal(t, []string{"kb-parent", "kb-own"}, ids)
	assert.Equal(t, []string{"metrics", "elastic_search_metrics"}, *queried, "parent first, deduped, wildcard never looked up")
	assert.Less(t, strings.Index(text, "metrics live in"), strings.Index(text, "report latency"))
	assert.Contains(t, text, `<skill name="ES setup">`)
}

func TestResolveAgentInstructions_SkipsEmptyAndCaps(t *testing.T) {
	long := strings.Repeat("é", toolcore.AlwaysLoadMaxChars+10)
	stubAlwaysLoadSkills(t, []toolcore.AlwaysLoadSkill{
		{ID: "kb-long", Name: "Long", AgentId: "metrics", Data: long},
		{ID: "kb-empty", Name: "Empty", AgentId: "metrics", Data: "   "},
	})
	text, ids := resolveAgentInstructions(security.NewRequestContextForSuperAdmin(), "acc-1", []string{"metrics"}, nil)

	assert.Empty(t, ids, "an empty skill adds nothing; a cut skill stays searchable")
	assert.Equal(t, toolcore.AlwaysLoadMaxChars, strings.Count(text, "é"))
	assert.Contains(t, text, `[Cut at 4000 characters. Search the knowledge base for "Long" to read the rest.]`)
}

func TestResolveAgentInstructions_NoAccount(t *testing.T) {
	queried := stubAlwaysLoadSkills(t, []toolcore.AlwaysLoadSkill{{ID: "x", AgentId: "metrics", Data: "x"}})
	text, ids := resolveAgentInstructions(security.NewRequestContextForSuperAdmin(), "", []string{"metrics"}, nil)
	assert.Empty(t, text)
	assert.Empty(t, ids)
	assert.Empty(t, *queried)
}

func TestWithAgentInstructions(t *testing.T) {
	assert.Equal(t, "sys", WithAgentInstructions(NBAgentRequest{}, "sys"))
	got := WithAgentInstructions(NBAgentRequest{AgentInstructions: "rule"}, "sys")
	assert.Equal(t, "sys\n\n<additional_agent_prompt>\nrule\n</additional_agent_prompt>", got)
	assert.Equal(t, "<additional_agent_prompt>\nrule\n</additional_agent_prompt>", WithAgentInstructions(NBAgentRequest{AgentInstructions: "rule"}, ""))
}

func TestCombinedAccountPrompt_IncludesInstructionsFirst(t *testing.T) {
	got := CombinedAccountPrompt(NBAgentRequest{AgentInstructions: "rule", AccountPrompt: "prompt", AccountContext: "context"})
	assert.Equal(t, "rule\n\nprompt\n\ncontext", got)
}

func TestReAct4_AgentInstructionsInSystemExactlyOnce(t *testing.T) {
	request := NBAgentRequest{AccountId: "acc-1", AgentInstructions: "BANANA rule"}
	ctx := security.NewRequestContextForSuperAdmin()
	system := composeReact4SystemMessage(ctx, request, notebookOptOutAgent{}, "agent prompt", request.AgentInstructions, nil)
	planner := &NBReActPlanner4{request: request}

	assert.Equal(t, 1, strings.Count(system, "BANANA rule"))
	assert.NotContains(t, planner.humanText("q"), "BANANA rule")
}

func TestReAct3_AgentInstructionsInSystemExactlyOnce(t *testing.T) {
	ctx := security.NewRequestContextForSuperAdmin()
	request := NBAgentRequest{AccountId: "acc-1", AgentInstructions: "BANANA rule"}
	tmpl, _, err := reActCreatePrompt3(ctx, "agent prompt", []toolcore.NBTool{}, "", []prompts.MessageFormatter{}, request, &MockAgent{})
	require.NoError(t, err)
	value, err := tmpl.FormatPrompt(map[string]any{"input": "q", "scratchpad": "", "notebook": ""})
	require.NoError(t, err)

	var all strings.Builder
	for _, m := range value.Messages() {
		all.WriteString(m.GetContent())
	}
	assert.Equal(t, 1, strings.Count(all.String(), "BANANA rule"))
	assert.Contains(t, all.String(), "<additional_agent_prompt>")
}

func TestKnowledgeDiscovery_SkipsAlwaysLoadedKBs(t *testing.T) {
	kbs := []toolcore.Knowledgebase{{Id: "a"}, {Id: "b"}}
	assert.Equal(t, []toolcore.Knowledgebase{{Id: "b"}}, withoutKBs(kbs, []string{"a"}))
	assert.Equal(t, kbs, withoutKBs(kbs, nil))

	docs := toolcore.RAGSearchResults{
		{Document: "pinned", Metadata: map[string]any{"collection": "kb_a"}},
		{Document: "other", Metadata: map[string]any{"collection": "kb_b"}},
		{Document: "global", Metadata: map[string]any{}},
	}
	got := withoutDocsFromKBs(docs, []string{"a"})
	require.Len(t, got, 2)
	assert.Equal(t, "other", got[0].Document)
	assert.Equal(t, "global", got[1].Document)
}
