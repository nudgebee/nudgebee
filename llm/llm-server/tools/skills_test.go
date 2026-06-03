package tools

import (
	"nudgebee/llm/security"
	"nudgebee/llm/tools/core"
	"os"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/tmc/langchaingo/llms"
)

func TestLoadSkillsTool_ParseSkillNames(t *testing.T) {
	tool := LoadSkillsTool{}

	testCases := []struct {
		name     string
		input    string
		expected []string
	}{
		{
			name:     "Single skill",
			input:    "k8s-docs",
			expected: []string{"k8s-docs"},
		},
		{
			name:     "Multiple skills comma-separated",
			input:    "k8s-docs, postgres-docs, redis-docs",
			expected: []string{"k8s-docs", "postgres-docs", "redis-docs"},
		},
		{
			name:     "Deduplicates case-insensitively",
			input:    "k8s-docs, K8S-Docs, postgres-docs",
			expected: []string{"k8s-docs", "postgres-docs"},
		},
		{
			name:     "Trims whitespace",
			input:    "  k8s-docs  ,  postgres-docs  ",
			expected: []string{"k8s-docs", "postgres-docs"},
		},
		{
			name:     "Skips empty segments",
			input:    "k8s-docs,,postgres-docs",
			expected: []string{"k8s-docs", "postgres-docs"},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			result := tool.parseSkillNames(tc.input)
			assert.Equal(t, tc.expected, result)
		})
	}
}

func TestSkillData_IntegrationTypeRouting(t *testing.T) {
	// Verify that the kb_type/kb_source fields correctly determine
	// which skills need RAG enrichment vs which have inline data.
	testCases := []struct {
		name     string
		skill    skillData
		needsRAG bool
	}{
		{
			name:     "Manual skill with data — no RAG needed",
			skill:    skillData{ID: "1", Data: "some content", KBType: "manual"},
			needsRAG: false,
		},
		{
			name:     "Integration skill with empty data — needs RAG",
			skill:    skillData{ID: "2", Data: "", KBType: "integration", KBSource: strPtr("confluence")},
			needsRAG: true,
		},
		{
			name:     "Integration skill with whitespace-only data — needs RAG",
			skill:    skillData{ID: "3", Data: "   ", KBType: "integration", KBSource: strPtr("servicenow")},
			needsRAG: true,
		},
		{
			name:     "Integration skill with data already populated — no RAG needed",
			skill:    skillData{ID: "4", Data: "cached content", KBType: "integration", KBSource: strPtr("confluence")},
			needsRAG: false,
		},
		{
			name:     "Empty kb_type defaults to manual behavior — no RAG needed",
			skill:    skillData{ID: "5", Data: "content", KBType: ""},
			needsRAG: false,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			needsRAG := tc.skill.KBType == "integration" && len(strings.TrimSpace(tc.skill.Data)) == 0
			assert.Equal(t, tc.needsRAG, needsRAG)
		})
	}
}

func TestSearchSkillsTool_Metadata(t *testing.T) {
	tool := SearchSkillsTool{}
	assert.Equal(t, "search_skills", tool.Name())
	assert.Equal(t, core.NBToolTypeTool, tool.GetType())
	assert.Contains(t, tool.Description(), "knowledge bases")

	schema := tool.InputSchema()
	_, hasQuery := schema.Properties["query"]
	assert.True(t, hasQuery, "schema must have 'query' property")
	assert.Contains(t, schema.Required, "query")
}

func TestLoadSkillsTool_ArgumentParsing(t *testing.T) {
	tool := LoadSkillsTool{}

	testCases := []struct {
		name          string
		arguments     map[string]any
		command       string
		expectedSkill string
	}{
		{
			name: "Standard argument",
			arguments: map[string]any{
				"skill_name": "k8s-docs",
			},
			expectedSkill: "k8s-docs",
		},
		{
			name: "Unnamed argument",
			arguments: map[string]any{
				"value": "postgres-docs",
			},
			expectedSkill: "postgres-docs",
		},
		{
			name:          "Command with colon",
			command:       "skill_name: redis-docs",
			expectedSkill: "redis-docs",
		},
		{
			name:          "Command with equals",
			command:       "name=aws-docs",
			expectedSkill: "aws-docs",
		},
		{
			name:          "Command with quotes",
			command:       "load the skill 'datadog-docs' please",
			expectedSkill: "datadog-docs",
		},
		{
			name:          "Multiple skills in command with colon",
			command:       "Load skill guides: redis_internal_troubleshooting, rabbitmq_internal_guide, postgres_performance_tuning",
			expectedSkill: "redis_internal_troubleshooting, rabbitmq_internal_guide, postgres_performance_tuning",
		},
		{
			name: "skill_names as slice",
			arguments: map[string]any{
				"skill_names": []any{"postgres_performance_tuning"},
			},
			expectedSkill: "postgres_performance_tuning",
		},
		{
			name: "skills as slice",
			arguments: map[string]any{
				"skills": []any{"postgres_performance_tuning"},
			},
			expectedSkill: "postgres_performance_tuning",
		},
		{
			name:          "Plain skill name as command",
			command:       "postgres_performance_tuning",
			expectedSkill: "postgres_performance_tuning",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// We test the parsing logic in isolation
			parsedSkill := tool.ParseSkillName(core.NBToolCallRequest{
				Arguments: tc.arguments,
				Command:   tc.command,
			})

			assert.Equal(t, tc.expectedSkill, parsedSkill)
		})
	}
}

// ---------------------------------------------------------------------------
// Integration tests — require TEST_ACCOUNT and TEST_USER env vars.
// These hit real DB and (if configured) RAG server.
// ---------------------------------------------------------------------------

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

// ---------------------------------------------------------------------------
// Integration tests — RAG-based skill loading and search
// ---------------------------------------------------------------------------

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
