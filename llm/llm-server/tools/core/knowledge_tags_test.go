package core

import (
	"encoding/json"
	"errors"
	"fmt"
	"github.com/DATA-DOG/go-sqlmock"
	"github.com/lib/pq"
	"github.com/stretchr/testify/require"
	"net/http"
	"net/http/httptest"
	"nudgebee/llm/common"
	"nudgebee/llm/config"
	"nudgebee/llm/security"
	"strings"
	"testing"
)

func TestKnowledgeTagsNormalize(t *testing.T) {
	tags, err := NormalizeKnowledgeTags(nil)
	require.NoError(t, err)
	require.Nil(t, tags)
	tags, err = NormalizeKnowledgeTags([]string{})
	require.NoError(t, err)
	require.NotNil(t, tags)
	require.Empty(t, tags)
	tags, err = NormalizeKnowledgeTags([]string{" payments ", "Payments", "service: checkout", "", "障害"})
	require.NoError(t, err)
	require.Equal(t, []string{"payments", "service: checkout", "障害"}, tags)
	for _, bad := range [][]string{make([]string, 33), {strings.Repeat("x", 129)}, {"one\ntwo"}, {string([]byte{0xff})}} {
		_, err = NormalizeKnowledgeTags(bad)
		require.Error(t, err)
	}
}

func TestKnowledgeUpdateOmittedTagsPreservesStoredLabels(t *testing.T) {
	mock := registerMockMetastore(t)
	common.ResetDatabaseManager(common.Metastore)
	t.Cleanup(func() { common.ResetDatabaseManager(common.Metastore) })
	endpoint := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(`{}`)) }))
	defer endpoint.Close()
	old := config.Config.ServiceEndpoint
	config.Config.ServiceEndpoint = endpoint.URL
	t.Cleanup(func() { config.Config.ServiceEndpoint = old })
	mock.ExpectQuery(`SELECT kb.id, kb.tenant_id.*kb.context_tags`).WithArgs("kb", "account").WillReturnRows(sqlmock.NewRows([]string{"id", "name", "data", "data_format", "status", "enabled", "context_tags"}).AddRow("kb", "runbook", "original body", "text", "active", true, `{"payments"}`))
	mock.ExpectBegin()
	mock.ExpectExec(`UPDATE llm_knowledgebases.*context_tags = \$9`).WithArgs("runbook", sqlmock.AnyArg(), "original body", "text", "", int64(0), sqlmock.AnyArg(), sqlmock.AnyArg(), `{"payments"}`, "kb", "account").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()
	mock.ExpectQuery(`SELECT agent_id FROM llm_kb_agent_mappings`).WithArgs("kb", "account").WillReturnRows(sqlmock.NewRows([]string{"agent_id"}))
	require.NoError(t, UpdateKnowledgebase(security.NewRequestContextForSuperAdmin(), "account", "kb", Knowledgebase{Description: "changed description"}))
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestKnowledgeTagOnlyUpdateRetainsBodyAndRequestsReindex(t *testing.T) {
	for _, tags := range [][]string{{"checkout"}, {}} {
		t.Run(fmt.Sprint(tags), func(t *testing.T) {
			mock := registerMockMetastore(t)
			common.ResetDatabaseManager(common.Metastore)
			t.Cleanup(func() { common.ResetDatabaseManager(common.Metastore) })
			mock.ExpectQuery(`SELECT kb.id, kb.tenant_id.*kb.context_tags`).WithArgs("kb", "account").WillReturnRows(sqlmock.NewRows([]string{"id", "name", "data", "data_format", "data_filename", "data_size_bytes", "status", "enabled", "context_tags"}).AddRow("kb", "runbook", "original body", "text", "original.txt", 13, "active", true, `{"payments"}`))
			mock.ExpectBegin()
			// Stop at the transaction boundary: assert the persisted values and the
			// processing transition without scheduling background work in this test.
			stopped := errors.New("test transaction stopped")
			mock.ExpectExec(`UPDATE llm_knowledgebases.*context_tags = \$9.*status = \$10`).WithArgs("runbook", sqlmock.AnyArg(), "original body", "text", "original.txt", int64(13), sqlmock.AnyArg(), sqlmock.AnyArg(), pq.StringArray(tags), "processing", "kb", "account").WillReturnError(stopped)
			mock.ExpectRollback()
			err := UpdateKnowledgebase(security.NewRequestContextForSuperAdmin(), "account", "kb", Knowledgebase{ContextTags: pq.StringArray(tags)})
			require.ErrorIs(t, err, stopped)
			require.NoError(t, mock.ExpectationsWereMet())
		})
	}
}

func TestKnowledgeIndexRequestCarriesTagsWithoutChangingSource(t *testing.T) {
	old := config.Config.RAGServerUrl
	t.Cleanup(func() { config.Config.RAGServerUrl = old })
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var payload struct {
			AccountID string   `json:"account_id"`
			Data      string   `json:"data"`
			Format    string   `json:"format"`
			Tags      []string `json:"context_tags"`
		}
		require.NoError(t, json.NewDecoder(r.Body).Decode(&payload))
		require.Equal(t, "account", payload.AccountID)
		require.Equal(t, []string{"service: checkout", "payments"}, payload.Tags)
		var docs []string
		require.NoError(t, json.Unmarshal([]byte(payload.Data), &docs))
		require.Equal(t, []string{"unaltered source"}, docs)
		_, _ = w.Write([]byte(`{"status":"success","document_count":1}`))
	}))
	defer server.Close()
	config.Config.RAGServerUrl = server.URL
	n, err := createKBVectorCollection("account", "kb", "unaltered source", "text", "tester", "user_update", []string{"service: checkout", "payments"})
	require.NoError(t, err)
	require.Equal(t, 1, n)
}

func TestKnowledgeMappedCandidatesIncludeTagsWithoutFilteringUntagged(t *testing.T) {
	mock := registerMockMetastore(t)
	common.ResetDatabaseManager(common.Metastore)
	t.Cleanup(func() { common.ResetDatabaseManager(common.Metastore) })
	mock.ExpectQuery(`SELECT DISTINCT ON.*array_to_string\(kb.context_tags, ' '\).*kb.status = 'active'.*kb.enabled`).WithArgs("account", sqlmock.AnyArg()).WillReturnRows(sqlmock.NewRows([]string{"id", "name", "description"}).AddRow("tagged", "runbook", "memory investigation payments").AddRow("untagged", "database", "database recovery"))
	candidates, err := ListActiveAgentSkillCandidates(security.NewRequestContextForSuperAdmin(), "account", []string{"logs"})
	require.NoError(t, err)
	require.Len(t, candidates, 2)
	require.Equal(t, []string{"tagged"}, SelectRelevantSkills("payments", candidates, 1))
	require.Equal(t, []string{"untagged"}, SelectRelevantSkills("database recovery", candidates, 1))
	require.NoError(t, mock.ExpectationsWereMet())
}
