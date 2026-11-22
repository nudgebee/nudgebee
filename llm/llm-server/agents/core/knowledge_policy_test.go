package core

import (
	"context"
	"errors"
	"github.com/DATA-DOG/go-sqlmock"
	"github.com/jmoiron/sqlx"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	toolcore "nudgebee/llm/tools/core"
)

func TestReadKnowledgePolicy(t *testing.T) {
	for _, value := range []string{"", "auto", "always", "llm_only", "disabled", "bad"} {
		t.Run(value, func(t *testing.T) {
			db, mock, err := sqlmock.New()
			require.NoError(t, err)
			defer func() { _ = db.Close() }()
			rows := sqlmock.NewRows([]string{"value"})
			if value != "" {
				rows.AddRow(value)
			}
			mock.ExpectQuery("SELECT value FROM cloud_account_attrs").WithArgs("account-a").WillReturnRows(rows)
			got, err := readKnowledgePolicy(context.Background(), sqlx.NewDb(db, "postgres"), "account-a")
			if value == "bad" {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
				want, _ := parseKnowledgePolicy(value)
				assert.Equal(t, want, got)
			}
			require.NoError(t, mock.ExpectationsWereMet())
		})
	}
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()
	mock.ExpectQuery("SELECT value FROM cloud_account_attrs").WithArgs("account-b").WillReturnError(errors.New("offline"))
	_, err = readKnowledgePolicy(context.Background(), sqlx.NewDb(db, "postgres"), "account-b")
	require.Error(t, err, "DB errors must not silently enable a disabled account")
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestKnowledgePolicyBlocksLegacyPromptRAG(t *testing.T) {
	for _, policy := range []KnowledgePolicy{KnowledgeDisabled, KnowledgeLLMOnly, KnowledgeAuto} {
		// No RAG server is needed: all these requests must skip the retrieval path.
		request := NBAgentRequest{Query: "hello", KnowledgePolicy: policy}
		prompt := GetPromptTemplate(NBAgentPrompt{Rag: NBAgentPromptRag{Module: "knowledge_base", Records: 2}}, request, AgentPlannerTypeReAct)
		text, err := prompt.Format(map[string]any{"history": ""})
		require.NoError(t, err)
		assert.NotContains(t, text, "<rag_examples>")
		assert.False(t, AutomaticKnowledgeAllowed(request))
	}
}

func TestParseKnowledgePolicy(t *testing.T) {
	for _, tc := range []struct {
		value string
		want  KnowledgePolicy
	}{
		{"", KnowledgeAuto}, {"auto", KnowledgeAuto}, {"always", KnowledgeAlways},
		{"llm_only", KnowledgeLLMOnly}, {"disabled", KnowledgeDisabled}, {" AUTO ", KnowledgeAuto},
	} {
		got, err := parseKnowledgePolicy(tc.value)
		require.NoError(t, err)
		assert.Equal(t, tc.want, got)
	}
	_, err := parseKnowledgePolicy("adaptive")
	require.Error(t, err)
}

func TestKnowledgeDiscoveryDecision(t *testing.T) {
	for _, tc := range []struct {
		query   string
		history string
		want    bool
	}{
		{"hello!", "", false}, {"thanks", "", false}, {"", "", false},
		{"investigate payments in prod", "", true}, {"find the deployment runbook", "", true},
		{"hello, investigate prod", "", true}, {"continue", "previous context", true},
		{"make it shorter", "previous context", false}, {"make it shorter", "", true},
		{"summarize the production runbook", "previous context", true},
	} {
		t.Run(tc.query+tc.history, func(t *testing.T) {
			r := NBAgentRequest{Query: tc.query, ConversationContext: tc.history}
			assert.Equal(t, tc.want, shouldDiscoverKnowledge(KnowledgeAuto, r))
			assert.Equal(t, tc.query != "", shouldDiscoverKnowledge(KnowledgeAlways, r))
			assert.False(t, shouldDiscoverKnowledge(KnowledgeLLMOnly, r))
			assert.False(t, shouldDiscoverKnowledge(KnowledgeDisabled, r))
		})
	}
	assert.True(t, shouldDiscoverKnowledge(KnowledgeAuto, NBAgentRequest{Query: "hello", QueryConfig: toolcore.NBQueryConfig{EventId: "event"}}))
	assert.True(t, shouldDiscoverKnowledge(KnowledgeAuto, NBAgentRequest{OriginalQuery: "hello", Query: "inspect production logs"}))
}

func TestApplyKnowledgePolicy(t *testing.T) {
	for _, policy := range []KnowledgePolicy{KnowledgeDisabled, KnowledgeLLMOnly} {
		r := NBAgentRequest{SkillsContext: "inherited content", KBPrestepContent: "chunks", SkillListsMenu: "menu"}
		applyKnowledgePolicy(&r, policy)
		assert.Empty(t, r.SkillsContext)
		assert.Empty(t, r.KBPrestepContent)
		assert.Empty(t, r.SkillListsMenu)
		assert.Equal(t, policy, r.KnowledgePolicy)
		assert.True(t, r.KnowledgePolicyResolved)
		if policy == KnowledgeDisabled {
			assert.Contains(t, r.Capabilities.DisabledTools, "search_skills")
			assert.Contains(t, r.Capabilities.DisabledTools, "load_skills")
			assert.Contains(t, r.Capabilities.DisabledTools, "search_docs")
		} else {
			assert.Empty(t, r.Capabilities.DisabledTools)
		}
	}
}

// Always-load skills are operator instructions (e.g. per-tool conventions for the
// code agent), not retrieved knowledge, so no policy may clear them.
func TestApplyKnowledgePolicy_KeepsAgentInstructions(t *testing.T) {
	for _, policy := range []KnowledgePolicy{KnowledgeAlways, KnowledgeAuto, KnowledgeLLMOnly, KnowledgeDisabled} {
		r := NBAgentRequest{AgentInstructions: "<skill name=\"terraform\">select workspace first</skill>", AlwaysLoadedKBIDs: []string{"kb1"}}
		applyKnowledgePolicy(&r, policy)
		assert.Equal(t, "<skill name=\"terraform\">select workspace first</skill>", r.AgentInstructions, string(policy))
		assert.Equal(t, []string{"kb1"}, r.AlwaysLoadedKBIDs, string(policy))
		assert.Contains(t, AgentInstructionsBlock(r), "select workspace first")
	}
}

func TestKnowledgePolicyTools(t *testing.T) {
	for _, policy := range []KnowledgePolicy{KnowledgeAlways, KnowledgeAuto, KnowledgeLLMOnly, KnowledgeDisabled} {
		custom := &nbCustomAgent{agent: AgentDto{Name: "custom", ExecutorType: AgentPlannerTypeReAct}}
		tools := FilterAndInjectDefaultTools("account", custom, "<skill-lists>menu</skill-lists>", nil, toolcore.AgentCapabilities{}, policy)
		assert.Equal(t, policy != KnowledgeDisabled, hasLoadSkills(tools))
		foundSearch := false
		for _, tool := range tools {
			foundSearch = foundSearch || tool.Name() == "search_skills"
		}
		assert.Equal(t, policy != KnowledgeDisabled, foundSearch)
		assert.False(t, HasShellTool(tools))
		tools = FilterAndInjectDefaultTools("account", custom, "", nil, toolcore.AgentCapabilities{DisabledTools: []string{"load_skills", "search_skills"}}, policy)
		assert.Empty(t, tools)
	}
}

type policyChunkAgent struct{ mockPlainAgent }

func (policyChunkAgent) GetPlannerType() AgentPlannerType     { return AgentPlannerTypeCustom }
func (policyChunkAgent) GetKnowledgeMode() AgentKnowledgeMode { return AgentKnowledgeAutoChunks }

type policyLegacyAgent struct{ mockPlainAgent }

func (policyLegacyAgent) GetPlannerType() AgentPlannerType { return AgentPlannerTypeCustom }
func (policyLegacyAgent) UsesAutomaticKnowledge() bool     { return true }

func TestKnowledgePolicyCustomSupport(t *testing.T) {
	for _, agent := range []NBAgent{policyChunkAgent{}, policyLegacyAgent{}} {
		require.Error(t, validateKnowledgePolicyForAgent(KnowledgeLLMOnly, agent))
		for _, policy := range []KnowledgePolicy{KnowledgeAuto, KnowledgeAlways, KnowledgeDisabled} {
			require.NoError(t, validateKnowledgePolicyForAgent(policy, agent))
		}
	}
	require.NoError(t, validateKnowledgePolicyForAgent(KnowledgeLLMOnly, mockPlainAgent{}))
}
