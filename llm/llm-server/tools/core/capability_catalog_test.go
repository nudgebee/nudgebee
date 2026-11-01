package core

import (
	"database/sql/driver"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"nudgebee/llm/common"
	"nudgebee/llm/config"
	"nudgebee/llm/security"
)

// unorderedNameSet is a sqlmock.Argument matcher for a pq.Array(names)
// parameter, ignoring element order — nbSystemTools is a Go map, so the
// knownNames slice SyncCapabilityCatalog builds from it has no stable order
// across runs.
type unorderedNameSet []string

func (want unorderedNameSet) Match(v driver.Value) bool {
	s, ok := v.(string)
	if !ok {
		return false
	}
	s = strings.TrimSuffix(strings.TrimPrefix(s, "{"), "}")
	var got []string
	if s != "" {
		for _, elem := range strings.Split(s, ",") {
			got = append(got, strings.Trim(elem, `"`))
		}
	}
	if len(got) != len(want) {
		return false
	}
	remaining := map[string]int{}
	for _, n := range want {
		remaining[n]++
	}
	for _, n := range got {
		if remaining[n] == 0 {
			return false
		}
		remaining[n]--
	}
	return true
}

// TestClassifyCapability_UnknownIsUnclassifiedNotNarrow is the regression
// guard for the exact defect two adversarial design reviews were run
// against (EPIC #38459): a capability absent from capabilityScopeTable must
// never resolve to narrow, and callers must be able to tell "not yet
// classified" apart from "classified as narrow" via the found return.
func TestClassifyCapability_UnknownIsUnclassifiedNotNarrow(t *testing.T) {
	scope, found := ClassifyCapability("some_brand_new_task_nobody_has_reviewed")
	assert.False(t, found)
	assert.Equal(t, CapabilityScopeUnclassified, scope)
}

func TestClassifyCapability_KnownEntries(t *testing.T) {
	cases := []struct {
		name string
		want CapabilityScope
	}{
		{"kubectl_execute", CapabilityScopePassThrough},
		{"k8s.cli", CapabilityScopePassThrough},
		// The entry that broke a naive "count all fields" heuristic during
		// design review: one required field (script) alongside 13 optional
		// ones. Must stay pass_through regardless of total field count.
		{"scripting.run_script", CapabilityScopePassThrough},
		{"k8s.workload_restart", CapabilityScopeNarrow},
		{"k8s.horizontal_rightsize", CapabilityScopeNarrow},
	}
	for _, tc := range cases {
		scope, found := ClassifyCapability(tc.name)
		assert.True(t, found, "expected %q to be classified", tc.name)
		assert.Equal(t, tc.want, scope, "capability %q", tc.name)
	}
}

func TestListWorkflowTaskCapabilities_ParsesAndClassifiesFromLiveResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/tasks", r.URL.Path)
		_, _ = w.Write([]byte(`{"tasks": [
			{"name": "k8s.workload_restart", "description": "Rolling restart"},
			{"name": "some_brand_new_task", "description": "not yet reviewed"}
		]}`))
	}))
	defer server.Close()

	old := config.Config.WorkflowServerEndpoint
	config.Config.WorkflowServerEndpoint = server.URL
	t.Cleanup(func() { config.Config.WorkflowServerEndpoint = old })

	entries, err := listWorkflowTaskCapabilities(security.NewRequestContextForSuperAdmin(), "acct-1")
	require.NoError(t, err)
	require.Len(t, entries, 2)

	byName := map[string]CapabilityEntry{}
	for _, e := range entries {
		byName[e.Name] = e
	}
	assert.Equal(t, CapabilityScopeNarrow, byName["k8s.workload_restart"].Scope)
	assert.Equal(t, CapabilitySurfaceWorkflowTask, byName["k8s.workload_restart"].Surface)
	// Not yet in capabilityScopeTable — must surface as unclassified, never
	// guessed as narrow just because it parsed fine.
	assert.Equal(t, CapabilityScopeUnclassified, byName["some_brand_new_task"].Scope)
}

// TestListWorkflowTaskCapabilities_NilSecurityContextDoesNotPanic guards the
// nil-dereference a Gemini Code Assist review found: SyncCapabilityCatalog
// is only ever called with a real security context today, but a nil one
// must degrade to an empty tenant/user rather than panicking at startup.
func TestListWorkflowTaskCapabilities_NilSecurityContextDoesNotPanic(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"tasks": [{"name": "k8s.workload_restart", "description": "Rolling restart"}]}`))
	}))
	defer server.Close()

	old := config.Config.WorkflowServerEndpoint
	config.Config.WorkflowServerEndpoint = server.URL
	t.Cleanup(func() { config.Config.WorkflowServerEndpoint = old })

	ctx := security.NewRequestContext(t.Context(), nil, nil, nil, nil)
	entries, err := listWorkflowTaskCapabilities(ctx, "acct-1")
	require.NoError(t, err)
	assert.Len(t, entries, 1)
}

// TestListWorkflowTaskCapabilities_ReturnsErrorWhenNoTasks guards the bug a
// human review found on top of the earlier clock-skew/transient-construction-
// failure fixes: a runbook-server response of `{"tasks": []}` is a 200, not
// an error, so it used to return (nil, nil) — success, zero entries. Once
// SyncCapabilityCatalog started deleting by knownNames membership, that
// "success" silently wiped every workflow_task row, because the agent-tool
// half alone keeps len(catalog) > 0 and nothing in the empty task list
// contributes a name to knownNames. Failing here instead — before
// SyncCapabilityCatalog ever runs its DELETE — is what
// TestSyncCapabilityCatalog_EmptyTaskListDoesNotDeleteWorkflowTaskRows below
// confirms end to end.
func TestListWorkflowTaskCapabilities_ReturnsErrorWhenNoTasks(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"tasks": []}`))
	}))
	defer server.Close()

	old := config.Config.WorkflowServerEndpoint
	config.Config.WorkflowServerEndpoint = server.URL
	t.Cleanup(func() { config.Config.WorkflowServerEndpoint = old })

	entries, err := listWorkflowTaskCapabilities(security.NewRequestContextForSuperAdmin(), "acct-1")
	require.Error(t, err)
	assert.Nil(t, entries)
}

// TestGetCapabilityCatalog_FailsFastWhenRunbookServerUnreachable guards the
// PRE-003-style requirement from design review: an incomplete catalog must
// fail the whole fetch, not silently return only the agent-tool half.
func TestGetCapabilityCatalog_FailsFastWhenRunbookServerUnreachable(t *testing.T) {
	old := config.Config.WorkflowServerEndpoint
	config.Config.WorkflowServerEndpoint = "http://127.0.0.1:1" // nothing listens here
	t.Cleanup(func() { config.Config.WorkflowServerEndpoint = old })

	_, err := GetCapabilityCatalog(security.NewRequestContextForSuperAdmin(), "acct-1")
	assert.Error(t, err)
}

// fakeNBTool is a minimal NBTool for tests. The real tools (kubectl_execute
// etc.) are registered by package `tools`' init() functions, which never run
// here — `tools/core`'s test binary doesn't import package `tools` (that
// import runs the other way, to avoid a cycle), so nbSystemTools is empty
// in this package's own tests. Injecting a fake registration is the correct
// way to test the merge/classification logic in isolation.
type fakeNBTool struct{ name, description string }

func (f fakeNBTool) Name() string        { return f.name }
func (f fakeNBTool) Description() string { return f.description }
func (f fakeNBTool) GetType() NBToolType { return NBToolTypeTool }
func (f fakeNBTool) InputSchema() ToolSchema {
	return ToolSchema{Type: ToolSchemaTypeObject, Required: []string{"command"}}
}
func (f fakeNBTool) Call(ctx NbToolContext, input NBToolCallRequest) (NBToolResponse, error) {
	return NBToolResponse{}, nil
}

func TestGetCapabilityCatalog_IncludesRegisteredAgentTools(t *testing.T) {
	const fakeName = "fake_capability_catalog_test_tool"
	RegisterNBToolFactory(fakeName, func(accountId string) (NBTool, error) {
		return fakeNBTool{name: fakeName, description: "a fake tool for this test"}, nil
	})
	t.Cleanup(func() { delete(nbSystemTools, fakeName) })

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"tasks": [{"name": "k8s.workload_restart", "description": "Rolling restart"}]}`))
	}))
	defer server.Close()
	old := config.Config.WorkflowServerEndpoint
	config.Config.WorkflowServerEndpoint = server.URL
	t.Cleanup(func() { config.Config.WorkflowServerEndpoint = old })

	entries, err := GetCapabilityCatalog(security.NewRequestContextForSuperAdmin(), "acct-1")
	require.NoError(t, err)

	found := false
	for _, e := range entries {
		if e.Name == fakeName {
			found = true
			assert.Equal(t, CapabilitySurfaceAgentTool, e.Surface)
			assert.Equal(t, "a fake tool for this test", e.Description)
			// Not in capabilityScopeTable — must be unclassified, never guessed.
			assert.Equal(t, CapabilityScopeUnclassified, e.Scope)
		}
	}
	assert.True(t, found, "expected the fake tool to be present in the merged catalog")
}

func TestUpsertCapabilityEntry_WritesExpectedUpsert(t *testing.T) {
	mock := registerMockMetastore(t)
	// GetDatabaseManager caches by name on first call; without resetting, a
	// manager cached by an earlier test would silently win over this mock.
	common.ResetDatabaseManager(common.Metastore)
	t.Cleanup(func() { common.ResetDatabaseManager(common.Metastore) })

	dbms, err := common.GetDatabaseManager(common.Metastore)
	require.NoError(t, err)

	mock.ExpectExec(`INSERT INTO llm_kb_capability_entries`).
		WithArgs("kubectl_execute", CapabilitySurfaceAgentTool, CapabilityScopePassThrough, "runs kubectl commands").
		WillReturnResult(sqlmock.NewResult(0, 1))

	err = upsertCapabilityEntry(security.NewRequestContextForSuperAdmin(), dbms, CapabilityEntry{
		Name:        "kubectl_execute",
		Surface:     CapabilitySurfaceAgentTool,
		Scope:       CapabilityScopePassThrough,
		Description: "runs kubectl commands",
	})
	require.NoError(t, err)
	require.NoError(t, mock.ExpectationsWereMet())
}

// TestDeleteObsoleteCapabilityEntries_RemovesRowsNotInKnownNames pins the
// mechanism a tool/task must lose its grounding eligibility through once
// removed from the live registries: a row whose name isn't in knownNames is
// deleted, closing the gap a Gemini Code Assist review found where a removed
// capability's row would otherwise linger and keep validating against a
// name that no longer exists.
func TestDeleteObsoleteCapabilityEntries_RemovesRowsNotInKnownNames(t *testing.T) {
	mock := registerMockMetastore(t)
	common.ResetDatabaseManager(common.Metastore)
	t.Cleanup(func() { common.ResetDatabaseManager(common.Metastore) })

	dbms, err := common.GetDatabaseManager(common.Metastore)
	require.NoError(t, err)

	knownNames := []string{"kubectl_execute", "aws_execute"}
	mock.ExpectExec(`DELETE FROM llm_kb_capability_entries WHERE entry_type = 'platform_capability' AND NOT \(name = ANY\(\$1\)\)`).
		WithArgs(unorderedNameSet(knownNames)).
		WillReturnResult(sqlmock.NewResult(0, 2))

	err = deleteObsoleteCapabilityEntries(security.NewRequestContextForSuperAdmin(), dbms, knownNames)
	require.NoError(t, err)
	require.NoError(t, mock.ExpectationsWereMet())
}

// TestSyncCapabilityCatalog_ToolConstructionFailureDoesNotDeleteItsRow is
// the regression guard for the second bug a Gemini Code Assist review found
// in the original updated_at < syncStarted deletion (on top of the clock-
// skew bug that approach also had): a tool still registered in
// nbSystemTools but that fails to construct this run (network hiccup,
// transient dependency error — see listAgentToolCapabilities' recover for
// the panic case) never reaches the upsert loop, so its updated_at is never
// refreshed. Deleting-by-"not touched this sync" would then delete that
// tool's still-valid row over a transient failure, not a real removal.
// Deleting-by-known-name-set fixes this: every nbSystemTools key counts as
// known regardless of whether it constructed successfully this run.
func TestSyncCapabilityCatalog_ToolConstructionFailureDoesNotDeleteItsRow(t *testing.T) {
	const goodName = "fake_good_sync_test_tool"
	const brokenName = "fake_broken_sync_test_tool"
	RegisterNBToolFactory(goodName, func(accountId string) (NBTool, error) {
		return fakeNBTool{name: goodName, description: "a fake tool for this test"}, nil
	})
	RegisterNBToolFactory(brokenName, func(accountId string) (NBTool, error) {
		return nil, assert.AnError
	})
	t.Cleanup(func() {
		delete(nbSystemTools, goodName)
		delete(nbSystemTools, brokenName)
	})

	const taskName = "k8s.workload_restart"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"tasks": [{"name": "` + taskName + `", "description": "Rolling restart"}]}`))
	}))
	defer server.Close()
	old := config.Config.WorkflowServerEndpoint
	config.Config.WorkflowServerEndpoint = server.URL
	t.Cleanup(func() { config.Config.WorkflowServerEndpoint = old })

	mock := registerMockMetastore(t)
	common.ResetDatabaseManager(common.Metastore)
	t.Cleanup(func() { common.ResetDatabaseManager(common.Metastore) })

	// Only the good tool + the fetched task reach the upsert loop — the
	// broken tool errors out of listAgentToolCapabilities' factory call and
	// is skipped there. This test binary never imports package `tools` (see
	// fakeNBTool's doc comment above), so nbSystemTools holds exactly these
	// two fakes — the known-name set the delete call receives must be
	// exactly {goodName, brokenName, taskName}, proving the broken tool's
	// name survives despite never reaching the upsert loop.
	mock.ExpectExec(`INSERT INTO llm_kb_capability_entries`).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(`INSERT INTO llm_kb_capability_entries`).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(`DELETE FROM llm_kb_capability_entries WHERE entry_type = 'platform_capability' AND NOT \(name = ANY\(\$1\)\)`).
		WithArgs(unorderedNameSet{goodName, brokenName, taskName}).
		WillReturnResult(sqlmock.NewResult(0, 0))

	err := SyncCapabilityCatalog(security.NewRequestContextForSuperAdmin())
	require.NoError(t, err)
	require.NoError(t, mock.ExpectationsWereMet())
}

// TestSyncCapabilityCatalog_EmptyTaskListDoesNotDeleteWorkflowTaskRows is the
// end-to-end regression guard for the bug TestListWorkflowTaskCapabilities_
// ReturnsErrorWhenNoTasks fixes at the source: before that fix, a 200
// response of `{"tasks": []}` let SyncCapabilityCatalog reach its DELETE with
// zero workflow-task names in knownNames, wiping every workflow_task row.
// registerMockMetastore's default (unset) expectations mean any unexpected
// Exec call fails the test, so this also proves no upsert/delete runs at all
// once the catalog fetch fails.
func TestSyncCapabilityCatalog_EmptyTaskListDoesNotDeleteWorkflowTaskRows(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"tasks": []}`))
	}))
	defer server.Close()
	old := config.Config.WorkflowServerEndpoint
	config.Config.WorkflowServerEndpoint = server.URL
	t.Cleanup(func() { config.Config.WorkflowServerEndpoint = old })

	mock := registerMockMetastore(t)
	common.ResetDatabaseManager(common.Metastore)
	t.Cleanup(func() { common.ResetDatabaseManager(common.Metastore) })

	err := SyncCapabilityCatalog(security.NewRequestContextForSuperAdmin())
	require.Error(t, err)
	require.NoError(t, mock.ExpectationsWereMet()) // no Exec expected or allowed
}

func TestListAgentToolCapabilities_OnePanickingFactoryDoesNotDropOthers(t *testing.T) {
	const goodName = "fake_good_tool_for_panic_test"
	const panicName = "fake_panicking_tool_for_panic_test"
	RegisterNBToolFactory(goodName, func(accountId string) (NBTool, error) {
		return fakeNBTool{name: goodName, description: "a fake tool for this test"}, nil
	})
	RegisterNBToolFactory(panicName, func(accountId string) (NBTool, error) {
		panic("simulated factory panic")
	})
	t.Cleanup(func() {
		delete(nbSystemTools, goodName)
		delete(nbSystemTools, panicName)
	})

	var entries []CapabilityEntry
	require.NotPanics(t, func() {
		entries = listAgentToolCapabilities(security.NewRequestContextForSuperAdmin(), "")
	})

	names := make([]string, len(entries))
	for i, e := range entries {
		names[i] = e.Name
	}
	assert.Contains(t, names, goodName)
	assert.NotContains(t, names, panicName)
}
