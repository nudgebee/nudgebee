package tools

import (
	"html"
	"nudgebee/llm/security"
	"nudgebee/llm/tools/core"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSearchSkills_CandidatesLoadExactChunks(t *testing.T) {
	useLegacySkillLoading(t)
	ctx := core.NbToolContext{Ctx: security.NewRequestContextForSuperAdmin(),
		AccountId: "search-roundtrip-account", ConversationId: "conversation", MessageId: "message"}
	docs := core.RAGSearchResults{
		{Document: "first chunk <canary>", Metadata: map[string]any{"title": "Article", "url": "https://example.com/article"}},
		{Document: "second chunk", Metadata: map[string]any{"title": "Article", "url": "https://example.com/article"}},
		{Document: "   "},
	}
	results := cacheSearchKnowledgeCandidates(ctx, docs)
	require.Len(t, results, 2)
	var ids []string
	for i, result := range results {
		match := regexp.MustCompile(`id="(knowledge:[a-f0-9]+)"`).FindStringSubmatch(result)
		require.Len(t, match, 2)
		ids = append(ids, match[1])
		resp, err := (LoadSkillsTool{}).Call(ctx, core.NBToolCallRequest{Arguments: map[string]any{"skill_name": match[1]}})
		require.NoError(t, err)
		require.Equal(t, core.NBToolResponseStatusSuccess, resp.Status)
		assert.Contains(t, resp.Data, html.EscapeString(docs[i].Document))
		require.Len(t, resp.References, 1)
		assert.Equal(t, "https://example.com/article", resp.References[0].Url)
	}
	assert.NotEqual(t, ids[0], ids[1], "chunks sharing a URL must not overwrite each other")
	ctx.MessageId = "different-turn"
	resp, err := (LoadSkillsTool{}).Call(ctx, core.NBToolCallRequest{Arguments: map[string]any{"skill_name": ids[0]}})
	require.NoError(t, err)
	assert.Equal(t, core.NBToolResponseStatusError, resp.Status)
}

// A page marked SOP must reach the agent as a procedure however its candidate
// was built — including load_skills naming an integration knowledge base, which
// caches candidates without going through search_skills.
func TestCachedCandidatesCarryDocumentMarks(t *testing.T) {
	useLegacySkillLoading(t)
	previous := resolveDocumentCategoriesFn
	resolveDocumentCategoriesFn = func(_ *security.RequestContext, _ string, docs core.RAGSearchResults) core.RAGSearchResults {
		out := make(core.RAGSearchResults, len(docs))
		for i, doc := range docs {
			metadata := map[string]any{}
			for k, v := range doc.Metadata {
				metadata[k] = v
			}
			if metadata["page_id"] == "101" {
				metadata[core.DocumentNoteCategoryKey] = core.KBNoteCategorySOP
			}
			doc.Metadata = metadata
			out[i] = doc
		}
		return out
	}
	t.Cleanup(func() { resolveDocumentCategoriesFn = previous })

	ctx := core.NbToolContext{Ctx: security.NewRequestContextForSuperAdmin(),
		AccountId: "marked-candidates-account", ConversationId: "conversation", MessageId: "message"}
	docs := core.RAGSearchResults{
		{Document: "runbook steps", Metadata: map[string]any{"collection": "integration-a_knowledge_base", "page_id": "101", "title": "Runbook", "url": "https://wiki.test/101"}},
		{Document: "background", Metadata: map[string]any{"collection": "integration-a_knowledge_base", "page_id": "102", "title": "Notes", "url": "https://wiki.test/102"}},
	}
	results := cacheSearchKnowledgeCandidates(ctx, docs)
	require.Len(t, results, 2)
	var purposes []core.KnowledgeContentPurpose
	for _, result := range results {
		match := regexp.MustCompile(`id="(knowledge:[a-f0-9]+)"`).FindStringSubmatch(result)
		require.Len(t, match, 2)
		candidate, ok := core.LoadKnowledgeCandidate(ctx.AccountId, ctx.ConversationId, ctx.MessageId, match[1])
		require.True(t, ok)
		purposes = append(purposes, candidate.Purpose)
	}
	assert.Equal(t, []core.KnowledgeContentPurpose{core.KnowledgePurposeProcedure, core.KnowledgePurposeReference}, purposes)
}

func TestLoadSkillsTool_MixedValidAndExpiredCandidatesReportsMissing(t *testing.T) {
	useLegacySkillLoading(t)
	ctx := core.NbToolContext{Ctx: security.NewRequestContextForSuperAdmin(),
		AccountId: "mixed-candidates-account", ConversationId: "conversation", MessageId: "message"}
	const valid = "knowledge:abcdef0123456789"
	const missing = "knowledge:missing"
	require.NoError(t, core.StoreKnowledgeCandidate(ctx.AccountId, ctx.ConversationId, ctx.MessageId,
		core.KnowledgeCandidate{ID: valid, Content: "valid body"}))
	resp, err := (LoadSkillsTool{}).Call(ctx, core.NBToolCallRequest{Arguments: map[string]any{"skill_name": valid + ", " + missing}})
	require.NoError(t, err)
	assert.Equal(t, core.NBToolResponseStatusSuccess, resp.Status)
	assert.Contains(t, resp.Data, "The following requested skills were not found: "+missing)
	require.Len(t, resp.References, 1)
}

func TestLoadSkillsTool_SchemaValidationAcceptsLegacyAliases(t *testing.T) {
	tool := LoadSkillsTool{}

	for _, input := range []string{
		`{"skill_names":"EC2_CPUUtilization_alarm"}`,
		`{"skill_names":["EC2_CPUUtilization_alarm"]}`,
		`{"skills":"EC2_CPUUtilization_alarm"}`,
		`{"skill_names":[],"skills":"EC2_CPUUtilization_alarm"}`,
		`{"skill_names":"   ","skills":"EC2_CPUUtilization_alarm"}`,
	} {
		t.Run(input, func(t *testing.T) {
			assert.Nil(t, core.ValidateToolInput(tool, input))
		})
	}
}

func TestLoadSkillsTool_SchemaValidationNormalizerLeavesNonObjectsUnchanged(t *testing.T) {
	tool := LoadSkillsTool{}
	for _, input := range []string{"", "  null  ", " [] ", "plain skill name"} {
		t.Run(input, func(t *testing.T) {
			assert.Equal(t, input, tool.NormalizeInputForSchemaValidation(input))
		})
	}
}

func TestLoadSkillsTool_LoadDiscoveredKnowledgeCandidateWithoutDB(t *testing.T) {
	useLegacySkillLoading(t)
	const (
		accountID      = "account-candidate-test"
		conversationID = "conversation-candidate-test"
		messageID      = "message-candidate-test"
		candidateID    = "knowledge:0123456789abcdef"
	)
	require.NoError(t, core.StoreKnowledgeCandidate(accountID, conversationID, messageID, core.KnowledgeCandidate{
		ID:      candidateID,
		Title:   "Checkout log fields",
		Source:  "confluence",
		URL:     "https://example.atlassian.net/wiki/checkout-logs",
		Content: "Use service_name rather than service when querying checkout logs.",
	}))

	resp, err := (LoadSkillsTool{}).Call(core.NbToolContext{
		Ctx:            security.NewRequestContextForSuperAdmin(),
		AccountId:      accountID,
		ConversationId: conversationID,
		MessageId:      messageID,
	}, core.NBToolCallRequest{Arguments: map[string]any{"skill_name": candidateID}})

	require.NoError(t, err)
	assert.Equal(t, core.NBToolResponseStatusSuccess, resp.Status)
	assert.Contains(t, resp.Data, "Use service_name rather than service")
	assert.Contains(t, resp.Data, "confluence")
	require.Len(t, resp.References, 1)
	assert.Equal(t, candidateID, resp.References[0].Url)
}

func TestLoadSkillsTool_ExpiredKnowledgeCandidateDoesNotRequireDB(t *testing.T) {
	useLegacySkillLoading(t)
	const candidateID = "knowledge:expired0000000"
	resp, err := (LoadSkillsTool{}).Call(core.NbToolContext{
		Ctx:            security.NewRequestContextForSuperAdmin(),
		AccountId:      "account-expired-candidate",
		ConversationId: "conversation-expired-candidate",
		MessageId:      "message-expired-candidate",
	}, core.NBToolCallRequest{Arguments: map[string]any{"skill_name": candidateID}})

	require.NoError(t, err)
	assert.Equal(t, core.NBToolResponseStatusError, resp.Status)
	assert.Contains(t, resp.Data, "may have expired")
	assert.Empty(t, resp.References)
}

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

func TestTruncateRunesExact(t *testing.T) {
	tests := []struct {
		name  string
		input string
		limit int
		want  string
	}{
		{name: "ascii", input: "abcdef", limit: 3, want: "abc"},
		{name: "multibyte", input: "你好世界", limit: 3, want: "你好世"},
		{name: "short", input: "你好", limit: 3, want: "你好"},
		{name: "zero", input: "content", limit: 0, want: ""},
		{name: "negative", input: "content", limit: -1, want: ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, truncateRunesExact(tc.input, tc.limit))
		})
	}
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
			name: "skill_names as string slice",
			arguments: map[string]any{
				"skill_names": []string{"postgres_performance_tuning", "redis-docs"},
			},
			expectedSkill: "postgres_performance_tuning,redis-docs",
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

// ---------------------------------------------------------------------------
// Integration tests — RAG-based skill loading and search
// ---------------------------------------------------------------------------
