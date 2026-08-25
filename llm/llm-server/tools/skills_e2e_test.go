//go:build e2e

package tools

import (
	"nudgebee/llm/security"
	"nudgebee/llm/tools/core"
	"os"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
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

func TestSearchSkillsTool_Integration_NoResults(t *testing.T) {
	skipIfNoTestAccount(t)
	tool := SearchSkillsTool{}
	ctx := newSkillToolContext(t, tool, "xyznonexistentquery98765")

	resp, err := tool.Call(ctx, core.NBToolCallRequest{
		Arguments: map[string]any{"query": "xyznonexistentquery98765"},
	})
	assert.NoError(t, err)
	assert.Equal(t, core.NBToolResponseStatusSuccess, resp.Status)
	// Either no results or some results — both are valid; should not error.
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

func TestLoadSkillsTool_Integration_RAGFallback(t *testing.T) {
	skipIfNoTestAccount(t)
	tool := LoadSkillsTool{}
	// Use a name that doesn't exist in DB but might match RAG content.
	// The RAG fallback should search using the name as a query.
	skillName := "AWS Infrastructure Setup"
	ctx := newSkillToolContext(t, tool, skillName)

	resp, err := tool.Call(ctx, core.NBToolCallRequest{
		Arguments: map[string]any{"skill_name": skillName},
	})
	assert.NoError(t, err)
	// If RAG has matching content, status is success and data contains it.
	// If RAG has no content, status is error with "not found".
	// Both are valid — we just verify no panic or unexpected error.
	if resp.Status == core.NBToolResponseStatusSuccess {
		assert.NotEmpty(t, resp.Data)
		assert.Contains(t, resp.Data, "<skill>")
	} else {
		assert.Contains(t, resp.Data, "not found")
	}
}

func TestLoadSkillsTool_Integration_RAGFallbackMultiple(t *testing.T) {
	skipIfNoTestAccount(t)
	tool := LoadSkillsTool{}
	// Multiple names: one likely in RAG, one definitely not.
	skillName := "AWS Infrastructure Setup, xyznonexistent12345"
	ctx := newSkillToolContext(t, tool, skillName)

	resp, err := tool.Call(ctx, core.NBToolCallRequest{
		Arguments: map[string]any{"skill_name": skillName},
	})
	assert.NoError(t, err)
	// Should handle gracefully — load what it can, report what's missing.
	if resp.Status == core.NBToolResponseStatusSuccess {
		assert.NotEmpty(t, resp.Data)
		// The missing one should be noted
		assert.Contains(t, resp.Data, "xyznonexistent12345")
	}
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
		os.Getenv("TEST_AWS_ACCOUNT"),
		os.Getenv("TEST_USER"),
		uuid.NewString(), uuid.NewString(), uuid.NewString(),
		query, []llms.MessageContent{}, "",
		core.NBQueryConfig{}, "",
	)
}
