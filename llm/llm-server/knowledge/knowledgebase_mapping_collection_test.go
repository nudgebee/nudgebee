package knowledge

import (
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/require"

	"nudgebee/llm/common"
	"nudgebee/llm/security"
)

func TestListAgentKBsPreservesIntegrationCollection(t *testing.T) {
	mock := registerMockMetastore(t)
	const account = "mapped-integration-collection-test"
	const agent = "k8s_orchestrator"
	key := "kb_mapping:" + account + ":" + agent
	_ = common.CacheDelete(CacheNamespaceLlmKbMapping, key)
	t.Cleanup(func() { _ = common.CacheDelete(CacheNamespaceLlmKbMapping, key) })
	mock.ExpectQuery(`(?s)SELECT kb.id,.*kb.integration_id.*FROM llm_knowledgebases kb.*INNER JOIN llm_kb_agent_mappings`).WithArgs(account, agent).
		WillReturnRows(sqlmock.NewRows([]string{"id", "name", "kb_type", "integration_id", "status", "enabled"}).AddRow("mapped-kb", "Incident SOP", KBTypeIntegration, "integration-source", "active", true))
	kbs, err := ListAgentKBs(security.NewRequestContextForSuperAdmin(), account, agent)
	require.NoError(t, err)
	require.Len(t, kbs, 1)
	require.Equal(t, "integration-source_knowledge_base", KnowledgebaseCollectionName(kbs[0]))
	require.NoError(t, mock.ExpectationsWereMet())
}
