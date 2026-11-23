package knowledge

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"

	"nudgebee/llm/config"
	"nudgebee/llm/security"
)

// acctGrantCase is one caller of an account-scoped action on kbGrantAccountB.
type acctGrantCase struct {
	name   string
	caller *security.RequestContext
	allow  bool
}

// accountGrantCases are the callers of an action keyed module:class on account
// B: built-in roles as before, the module's grant at the action's class
// (tenant-wide or on B; Write also reads), and the refusals: another module,
// another class, an account-bound grant on another account, no grant.
func accountGrantCases(t *testing.T, module, class string) []acctGrantCase {
	perm := func(m, c string) map[string]bool { return map[string]bool{m + ":" + c: true} }
	scoped := func(acct, m, c string) map[string]map[string]bool {
		return map[string]map[string]bool{acct: {m + ":" + c: true}}
	}
	otherModule := "ai_misc"
	cases := []acctGrantCase{
		{"tenant_admin", kbGrantCaller(t, []string{security.AUTH_TENANT_ADMIN_ROLE}, nil, nil, nil), true},
		{"account_admin_b", kbGrantCaller(t, []string{security.AUTH_ACCOUNT_ADMIN_ROLE},
			map[string][]string{security.AUTH_ACCOUNT_ADMIN_ROLE: {kbGrantAccountB}}, nil, nil), true},
		{"account_admin_readonly_b", kbGrantCaller(t, []string{security.AUTH_ACCOUNT_READ_ADMIN_ROLE},
			map[string][]string{security.AUTH_ACCOUNT_READ_ADMIN_ROLE: {kbGrantAccountB}}, nil, nil), class == "Read"},
		{"account_admin_a", kbGrantCaller(t, []string{security.AUTH_ACCOUNT_ADMIN_ROLE},
			map[string][]string{security.AUTH_ACCOUNT_ADMIN_ROLE: {orgTestAccount}}, nil, nil), false},
		{"no_role", kbGrantCaller(t, nil, nil, nil, nil), false},
		{module + ":" + class, kbGrantCaller(t, nil, nil, perm(module, class), nil), true},
		{module + ":" + class + " on b", kbGrantCaller(t, nil, nil, nil, scoped(kbGrantAccountB, module, class)), true},
		{module + ":" + class + " on a", kbGrantCaller(t, nil, nil, nil, scoped(orgTestAccount, module, class)), false},
		{otherModule + ":" + class, kbGrantCaller(t, nil, nil, perm(otherModule, class), nil), false},
	}
	switch class {
	case "Read":
		cases = append(cases,
			acctGrantCase{module + ":Write", kbGrantCaller(t, nil, nil, perm(module, "Write"), nil), true},
			acctGrantCase{module + ":Execute", kbGrantCaller(t, nil, nil, perm(module, "Execute"), nil), false})
	case "Write":
		cases = append(cases,
			acctGrantCase{module + ":Read", kbGrantCaller(t, nil, nil, perm(module, "Read"), nil), false},
			acctGrantCase{module + ":Execute", kbGrantCaller(t, nil, nil, perm(module, "Execute"), nil), false})
	case "Execute":
		cases = append(cases,
			acctGrantCase{module + ":Read", kbGrantCaller(t, nil, nil, perm(module, "Read"), nil), false},
			acctGrantCase{module + ":Write", kbGrantCaller(t, nil, nil, perm(module, "Write"), nil), false})
	}
	return cases
}

type acctGrantAction struct {
	action  string
	module  string
	class   string
	refused string // the error a refused caller gets
	// prepare sets the mock up for the reads before the check; anything after
	// it is unexpected and fails the call with a non-authorization error.
	prepare func(sqlmock.Sqlmock)
	call    func(*security.RequestContext) error
}

func runAccountGrantActions(t *testing.T, actions []acctGrantAction) {
	for _, a := range actions {
		t.Run(a.action, func(t *testing.T) {
			for _, tc := range accountGrantCases(t, a.module, a.class) {
				t.Run(tc.name, func(t *testing.T) {
					mock := kbGrantMockDB(t)
					if a.prepare != nil {
						a.prepare(mock)
					}
					err := a.call(tc.caller)
					refused := err != nil && err.Error() == a.refused
					if tc.allow {
						assert.False(t, refused, "want allowed, got %v", err)
					} else {
						assert.True(t, refused, "want refused, got %v", err)
					}
				})
			}
		})
	}
}

// expectAccountKBOwner answers resolveKBForWrite's owner read: kb-1 belongs to
// account B.
func expectAccountKBOwner(mock sqlmock.Sqlmock) {
	expectOwner(mock, "kb-1", kbGrantAccountB, orgTestTenant)
}

// Guardrails (global contexts) of an account: ai_guardrails Read reads them,
// ai_guardrails Write creates, changes and deletes them.
func TestGlobalContextAccountGrants(t *testing.T) {
	const b = kbGrantAccountB
	runAccountGrantActions(t, []acctGrantAction{
		{"ai_create_gc", "ai_guardrails", "Write", errGCUnauthorized, nil, func(sc *security.RequestContext) error {
			_, err := CreateGlobalContext(sc, GlobalContext{AccountId: b, Name: "runbook_rules", Data: "x",
				DataFormat: string(GCFormatText), DataFilename: "x.txt"})
			return err
		}},
		{"ai_get_gc", "ai_guardrails", "Read", errGCUnauthorized, nil, func(sc *security.RequestContext) error {
			_, err := GetGlobalContext(sc, b, "gc-1")
			return err
		}},
		{"ai_list_gc", "ai_guardrails", "Read", errGCUnauthorized, nil, func(sc *security.RequestContext) error {
			_, err := ListGlobalContexts(sc, b)
			return err
		}},
		{"ai_update_gc", "ai_guardrails", "Write", errGCUnauthorized, nil, func(sc *security.RequestContext) error {
			return UpdateGlobalContext(sc, b, "gc-1", GlobalContext{})
		}},
		{"ai_delete_gc", "ai_guardrails", "Write", errGCUnauthorized, nil, func(sc *security.RequestContext) error {
			return DeleteGlobalContext(sc, b, "gc-1")
		}},
	})
}

// Knowledge bases of an account: ai_kbs Read reads them and their documents,
// ai_kbs Write creates, changes, maps and deletes them, ai_kbs Execute syncs
// them (Write does not imply Execute).
func TestKnowledgebaseAccountGrants(t *testing.T) {
	const b = kbGrantAccountB
	runAccountGrantActions(t, []acctGrantAction{
		{"ai_create_kb", "ai_kbs", "Write", errKBUnauthorized, nil, func(sc *security.RequestContext) error {
			_, err := CreateKnowledgebase(sc, b, Knowledgebase{Name: "runbooks"})
			return err
		}},
		{"ai_get_kb", "ai_kbs", "Read", errKBUnauthorized, nil, func(sc *security.RequestContext) error {
			_, err := GetKnowledgebaseScoped(sc, b, "kb-1", nil)
			return err
		}},
		{"ai_list_kb", "ai_kbs", "Read", errKBUnauthorized, nil, func(sc *security.RequestContext) error {
			_, err := ListKnowledgebases(sc, b)
			return err
		}},
		{"ai_update_kb", "ai_kbs", "Write", errKBUnauthorized, expectAccountKBOwner, func(sc *security.RequestContext) error {
			return UpdateKnowledgebase(sc, b, "kb-1", Knowledgebase{})
		}},
		{"ai_update_kb_enabled", "ai_kbs", "Write", errKBUnauthorized, expectAccountKBOwner, func(sc *security.RequestContext) error {
			return SetKnowledgebaseEnabled(sc, b, "kb-1", false)
		}},
		{"ai_delete_kb", "ai_kbs", "Write", errKBUnauthorized, expectAccountKBOwner, func(sc *security.RequestContext) error {
			return DeleteKnowledgebase(sc, b, "kb-1")
		}},
		{"ai_sync_kb", "ai_kbs", "Execute", errKBUnauthorized, expectAccountKBOwner, func(sc *security.RequestContext) error {
			return RetriggerKnowledgebase(sc, b, "kb-1")
		}},
		{"ai_get_kb_load_history", "ai_kbs", "Read", errKBUnauthorized, nil, func(sc *security.RequestContext) error {
			_, err := GetKBLoadHistory(sc, b, "kb-1")
			return err
		}},
		// ai_list_kb_documents, ai_get_kb_document, ai_get_kb_document_source and
		// ai_list_kb_run_documents all resolve the KB through kbDocumentsCollection.
		{"ai_list_kb_documents", "ai_kbs", "Read", errKBUnauthorized, nil, func(sc *security.RequestContext) error {
			_, err := ListKBDocuments(sc, b, "kb-1", 10, "")
			return err
		}},
		{"kb documents collection", "ai_kbs", "Read", errKBUnauthorized, nil, func(sc *security.RequestContext) error {
			_, _, err := kbDocumentsCollection(sc, b, "kb-1")
			return err
		}},
		{"ai_upsert_kb_document_category", "ai_kbs", "Write", errKBUnauthorized, nil, func(sc *security.RequestContext) error {
			return SetKBDocumentCategory(sc, b, "kb-1", "doc-1", KBNoteCategoryFact)
		}},
		{"ai_create_kb_mapping", "ai_kbs", "Write", "you don't have permission to access this resource", nil, func(sc *security.RequestContext) error {
			_, err := MapKBToAgent(sc, b, "kb-1", "agent-1", nil)
			return err
		}},
		{"ai_delete_kb_mapping", "ai_kbs", "Write", "you don't have permission to access this resource", nil, func(sc *security.RequestContext) error {
			return UnmapKBFromAgent(sc, b, "kb-1", "agent-1")
		}},
	})
}

// ai_create_rag: ai_kbs Write (the documents are knowledge-base content).
func TestCreateAgentRagGrants(t *testing.T) {
	old := config.Config.RAGServerUrl
	t.Cleanup(func() { config.Config.RAGServerUrl = old })
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"status":"success"}`))
	}))
	t.Cleanup(server.Close)
	config.Config.RAGServerUrl = server.URL

	runAccountGrantActions(t, []acctGrantAction{
		{"ai_create_rag", "ai_kbs", "Write", ragUnauthorized, nil, func(sc *security.RequestContext) error {
			_, err := CreateAgentRag(sc, kbGrantAccountB, "agent-1", "notes", "text", "notes.txt")
			return err
		}},
	})
}

// The agent's view of its knowledge bases is ai_agents Read; the retrieval
// probe reads the same mapping as ai_kbs Read (it returns KB content).
func TestAgentKBListGrants(t *testing.T) {
	const b = kbGrantAccountB
	const refused = "you don't have permission to access this resource"
	runAccountGrantActions(t, []acctGrantAction{
		{"ai_list_agent_kbs", "ai_agents", "Read", refused, nil, func(sc *security.RequestContext) error {
			_, err := ListAgentKBs(sc, b, "agent-1", "ai_agents")
			return err
		}},
		{"ai_list_kb_retrieval (agent scope)", "ai_kbs", "Read", refused, nil, func(sc *security.RequestContext) error {
			_, err := ListAgentKBs(sc, b, "agent-1", "ai_kbs")
			return err
		}},
		{"ai_list_agents_with_kb_counts", "ai_agents", "Read", refused, nil, func(sc *security.RequestContext) error {
			_, err := ListAgentsWithKBCounts(sc, b)
			return err
		}},
		{"ai_list_kb_agents", "ai_agents", "Read", refused, nil, func(sc *security.RequestContext) error {
			_, err := ListKBAgents(sc, b, "kb-1")
			return err
		}},
	})
	// The run path (no module) keeps the built-in check only.
	for _, tc := range accountGrantCases(t, "ai_agents", "Read") {
		t.Run("run path/"+tc.name, func(t *testing.T) {
			kbGrantMockDB(t)
			_, err := ListAgentKBs(tc.caller, b, "agent-1", "")
			refused := err != nil && err.Error() == refused
			builtIn := tc.caller.GetSecurityContext().HasAccountAccess(b, security.SecurityAccessTypeRead)
			assert.Equal(t, !builtIn, refused, "got %v", err)
		})
	}
}

// Skill sources are tenant-level: tenant admins as before, and a tenant-wide
// ai_kbs grant at the action's class (Read or Write to list, Write to add,
// share or delete, Execute to sync). An account-bound grant never reaches them.
func TestSkillSourceGrants(t *testing.T) {
	callers := kbGrantCallers(t)
	callers["kbs_write_scoped_a"] = kbGrantCaller(t, nil, nil, nil, map[string]map[string]bool{orgTestAccount: {"ai_kbs:Write": true}})
	callers["kbs_execute_scoped_a"] = kbGrantCaller(t, nil, nil, nil, map[string]map[string]bool{orgTestAccount: {"ai_kbs:Execute": true}})
	callers["misc_write"] = kbGrantCaller(t, nil, nil, map[string]bool{"ai_misc:Write": true}, nil)
	// Creating, changing, syncing and deleting a source writes organisation KBs,
	// which stays tenant-admin only; only the list takes a tenant-wide grant.
	writes := []kbGrantCase{
		{"tenant_admin", true},
		{"kbs_write", false},
		{"kbs_execute", false},
		{"tenant_admin_readonly", false},
		{"account_admin_a", false},
		{"no_role", false},
		{"kbs_read", false},
		{"kbs_write_scoped_a", false},
		{"misc_write", false},
	}
	actions := []struct {
		action string
		cases  []kbGrantCase
		call   func(*security.RequestContext) error
	}{
		{"ai_create_kb_skill_source", writes, func(sc *security.RequestContext) error {
			_, err := CreateSkillSource(sc, SkillSource{Repo: "acme/skills"})
			return err
		}},
		{"ai_list_kb_skill_sources", []kbGrantCase{
			{"tenant_admin", true},
			{"tenant_admin_readonly", true},
			{"kbs_read", true},
			{"kbs_write", true},
			{"account_admin_a", false},
			{"no_role", false},
			{"kbs_execute", false},
			{"kbs_read_scoped_a", false},
			{"misc_write", false},
		}, func(sc *security.RequestContext) error {
			_, err := ListSkillSources(sc)
			return err
		}},
		{"ai_update_kb_skill_source", writes, func(sc *security.RequestContext) error {
			_, err := UpdateSkillSourceVisibility(sc, "src-1", KBVisibilityOrganisation, nil)
			return err
		}},
		{"ai_delete_kb_skill_source", writes, func(sc *security.RequestContext) error {
			return DeleteSkillSource(sc, "src-1")
		}},
		{"ai_sync_kb_skill_source", writes, func(sc *security.RequestContext) error {
			_, err := SyncSkillSource(sc, "src-1")
			return err
		}},
	}
	for _, a := range actions {
		t.Run(a.action, func(t *testing.T) {
			for _, tc := range a.cases {
				t.Run(tc.caller, func(t *testing.T) {
					kbGrantMockDB(t)
					err := a.call(callers[tc.caller])
					refused := err != nil && err.Error() == errKBOrgAdminOnly
					if tc.allow {
						assert.False(t, refused, "want allowed, got %v", err)
					} else {
						assert.True(t, refused, "want refused, got %v", err)
					}
				})
			}
		})
	}
}

// A caller refused ai_create_rag sends nothing to rag-server: the access check
// runs before the ingest call, not after it.
func TestCreateAgentRagRefusalSendsNothing(t *testing.T) {
	old := config.Config.RAGServerUrl
	t.Cleanup(func() { config.Config.RAGServerUrl = old })
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		_, _ = w.Write([]byte(`{"status":"success"}`))
	}))
	t.Cleanup(server.Close)
	config.Config.RAGServerUrl = server.URL

	_, err := CreateAgentRag(kbGrantCaller(t, nil, nil, nil, nil), kbGrantAccountB, "agent-1", "notes", "text", "notes.txt")
	assert.EqualError(t, err, ragUnauthorized)
	assert.Equal(t, 0, calls, "rag-server was called for a refused caller")
}
