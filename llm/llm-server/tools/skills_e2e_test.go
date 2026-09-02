//go:build e2e

package tools

import (
	"html"
	"nudgebee/llm/security"
	"nudgebee/llm/tools/core"
	"os"
	"regexp"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tmc/langchaingo/llms"
)

func TestLoadSkillsTool_Integration_EmptyName(t *testing.T) {
	skipIfNoTestAccount(t)
	tool := LoadSkillsTool{}
	ctx := newSkillToolContext(t, tool, "")

	resp, err := tool.Call(ctx, core.NBToolCallRequest{
		Arguments: map[string]any{"skill_name": ""},
	})
	assert.NoError(t, err)
	assert.Equal(t, core.NBToolResponseStatusError, resp.Status)
	assert.Contains(t, resp.Data, "skill_name is required")
}

func TestLoadSkillsTool_Integration_NonExistentSkill(t *testing.T) {
	skipIfNoTestAccount(t)
	tool := LoadSkillsTool{}
	ctx := newSkillToolContext(t, tool, "nonexistent_skill_xyz_12345")

	resp, err := tool.Call(ctx, core.NBToolCallRequest{
		Arguments: map[string]any{"skill_name": "nonexistent_skill_xyz_12345"},
	})
	assert.NoError(t, err)
	assert.Equal(t, core.NBToolResponseStatusError, resp.Status)
	assert.Contains(t, resp.Data, "not found")
}

func TestLoadSkillsTool_Integration_MultipleNonExistent(t *testing.T) {
	skipIfNoTestAccount(t)
	tool := LoadSkillsTool{}
	ctx := newSkillToolContext(t, tool, "fake_skill_a, fake_skill_b")

	resp, err := tool.Call(ctx, core.NBToolCallRequest{
		Arguments: map[string]any{"skill_name": "fake_skill_a, fake_skill_b"},
	})
	assert.NoError(t, err)
	assert.Equal(t, core.NBToolResponseStatusError, resp.Status)
}

func TestSearchSkillsTool_Integration_EmptyQuery(t *testing.T) {
	skipIfNoTestAccount(t)
	tool := SearchSkillsTool{}
	ctx := newSkillToolContext(t, tool, "")

	resp, err := tool.Call(ctx, core.NBToolCallRequest{
		Arguments: map[string]any{"query": ""},
	})
	assert.NoError(t, err)
	assert.Equal(t, core.NBToolResponseStatusError, resp.Status)
	assert.Contains(t, resp.Data, "query is required")
}

func TestSearchSkillsTool_Integration_UnmatchedQuerySmoke(t *testing.T) {
	// Nearest-neighbour search may return weak matches. This is a connectivity
	// smoke test, not an assertion that the retrieval service rejects them.
	skipIfNoTestAccount(t)
	tool := SearchSkillsTool{}
	ctx := newSkillToolContext(t, tool, "xyznonexistentquery98765")

	resp, err := tool.Call(ctx, core.NBToolCallRequest{
		Arguments: map[string]any{"query": "xyznonexistentquery98765"},
	})
	assert.NoError(t, err)
	assert.Equal(t, core.NBToolResponseStatusSuccess, resp.Status)
}

func TestSearchSkillsTool_Integration_BasicQuery(t *testing.T) {
	skipIfNoTestAccount(t)
	tool := SearchSkillsTool{}
	query := "sqs eventbridge"
	ctx := newSkillToolContext(t, tool, query)

	resp, err := tool.Call(ctx, core.NBToolCallRequest{
		Arguments: map[string]any{"query": query},
	})
	assert.NoError(t, err)
	assert.Equal(t, core.NBToolResponseStatusSuccess, resp.Status)
	// Response is valid whether or not results are found.
	assert.NotEmpty(t, resp.Data)
}

func TestSearchSkillsTool_Integration_CommandFallback(t *testing.T) {
	skipIfNoTestAccount(t)
	tool := SearchSkillsTool{}
	ctx := newSkillToolContext(t, tool, "kubernetes pods")

	// Test that Command field is used when Arguments has no query.
	resp, err := tool.Call(ctx, core.NBToolCallRequest{
		Command: "kubernetes pods",
	})
	assert.NoError(t, err)
	assert.Equal(t, core.NBToolResponseStatusSuccess, resp.Status)
}

func TestSearchSkillsTool_Integration_RAGResults(t *testing.T) {
	skipIfNoTestAccount(t)
	tool := SearchSkillsTool{}
	// Use a query likely to match integration KB content (e.g. Confluence articles).
	query := "AWS infrastructure setup"
	ctx := newSkillToolContext(t, tool, query)

	resp, err := tool.Call(ctx, core.NBToolCallRequest{
		Arguments: map[string]any{"query": query},
	})
	assert.NoError(t, err)
	assert.Equal(t, core.NBToolResponseStatusSuccess, resp.Status)
	// If RAG has indexed content, we expect results containing the RAG source tag.
	if resp.Data != "No matching skills or knowledge base entries found for the given query." {
		assert.Contains(t, resp.Data, "<result")
	}
}

func TestSearchSkillsTool_Integration_SearchThenLoad(t *testing.T) {
	skipIfNoTestAccount(t)
	ctx := newSkillToolContext(t, SearchSkillsTool{}, "AWS infrastructure setup")
	resp, err := (SearchSkillsTool{}).Call(ctx, core.NBToolCallRequest{
		Arguments: map[string]any{"query": "AWS infrastructure setup"},
	})
	require.NoError(t, err)
	require.Equal(t, core.NBToolResponseStatusSuccess, resp.Status)
	ids := regexp.MustCompile(`id="(knowledge:[a-f0-9]+)"`).FindStringSubmatch(resp.Data)
	if len(ids) < 2 {
		t.Skip("no RAG candidates returned; configure indexed knowledge for this account")
	}
	candidate, ok := core.LoadKnowledgeCandidate(ctx.AccountId, ctx.ConversationId, ctx.MessageId, ids[1])
	require.True(t, ok)
	loaded, err := (LoadSkillsTool{}).Call(ctx, core.NBToolCallRequest{
		Arguments: map[string]any{"skill_name": ids[1]},
	})
	require.NoError(t, err)
	require.Equal(t, core.NBToolResponseStatusSuccess, loaded.Status)
	assert.Contains(t, loaded.Data, html.EscapeString(candidate.Content))
	require.Len(t, loaded.References, 1)
}

func TestLoadSkillsTool_Integration_CandidateAndMissingName(t *testing.T) {
	skipIfNoTestAccount(t)
	ctx := newSkillToolContext(t, LoadSkillsTool{}, "")
	const id = "knowledge:0123456789abcdef"
	const missing = "nonexistent_skill_"
	missingName := missing + uuid.NewString()
	require.NoError(t, core.StoreKnowledgeCandidate(ctx.AccountId, ctx.ConversationId, ctx.MessageId, core.KnowledgeCandidate{
		ID: id, Title: "Test candidate", Source: "test", Content: "CANARY-7392",
	}))
	resp, err := (LoadSkillsTool{}).Call(ctx, core.NBToolCallRequest{
		Arguments: map[string]any{"skill_name": id + ", " + missingName},
	})
	require.NoError(t, err)
	require.Equal(t, core.NBToolResponseStatusSuccess, resp.Status)
	assert.Contains(t, resp.Data, "CANARY-7392")
	assert.Contains(t, resp.Data, "The following requested skills were not found: "+missingName)
	assert.NotContains(t, resp.Data, "<name>"+missingName+"</name>")
	require.Len(t, resp.References, 1)
}

func TestSearchSkillsTool_Integration_RAGContentTruncation(t *testing.T) {
	skipIfNoTestAccount(t)
	tool := SearchSkillsTool{}
	query := "infrastructure deployment guide"
	ctx := newSkillToolContext(t, tool, query)

	resp, err := tool.Call(ctx, core.NBToolCallRequest{
		Arguments: map[string]any{"query": query},
	})
	assert.NoError(t, err)
	assert.Equal(t, core.NBToolResponseStatusSuccess, resp.Status)
	// Verify RAG content is not excessively large (should be capped at ~5K).
	if resp.Data != "No matching skills or knowledge base entries found for the given query." {
		assert.LessOrEqual(t, len(resp.Data), 15000,
			"Response should be bounded — RAG results are capped at LlmServerMaxSkillContentLength")
	}
}

func skipIfNoTestAccount(t *testing.T) {
	t.Helper()
	if os.Getenv("TEST_ACCOUNT") == "" {
		t.Skip("TEST_ACCOUNT not set")
	}
}

func newSkillToolContext(t *testing.T, tool core.NBTool, query string) core.NbToolContext {
	t.Helper()
	sc := security.NewRequestContextForSuperAdmin()
	return core.NewNbToolContext(
		sc, tool,
		os.Getenv("TEST_ACCOUNT"),
		os.Getenv("TEST_USER"),
		uuid.NewString(), uuid.NewString(), uuid.NewString(),
		query, []llms.MessageContent{}, "",
		core.NBQueryConfig{}, "",
	)
}
