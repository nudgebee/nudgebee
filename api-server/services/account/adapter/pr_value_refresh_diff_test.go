package adapter

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"nudgebee/services/internal/database"
	"nudgebee/services/security"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAddedLinesFromPatch(t *testing.T) {
	patch := "@@ -10,4 +10,4 @@ resources:\n   requests:\n-    cpu: 318m\n+    cpu: 265m\n-    memory: 350Mi\n+    memory: 278Mi\r\n"
	assert.Equal(t, []string{"    cpu: 265m", "    memory: 278Mi"}, addedLinesFromPatch(patch))
	assert.Empty(t, addedLinesFromPatch("--- a/values.yaml\n+++ b/values.yaml\n"), "file headers are not added lines")
}

// withGitHubAPI points githubAPIBase at a fake for the duration of the test.
func withGitHubAPI(t *testing.T, handler http.HandlerFunc) {
	t.Helper()
	server := httptest.NewServer(handler)
	prev := githubAPIBase
	githubAPIBase = server.URL
	t.Cleanup(func() {
		githubAPIBase = prev
		server.Close()
	})
}

func githubTestMeta() prMetadata {
	return prMetadata{PRURL: "https://github.com/acme/infra/pull/42", RepoURL: "https://github.com/acme/infra", Provider: "github"}
}

// A chart bump can touch more than one page of files; the values file may be
// on any of them.
func TestFetchGitHubPRAddedLinesReadsEveryPage(t *testing.T) {
	withGitHubAPI(t, func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/repos/acme/infra/pulls/42/files", r.URL.Path)
		assert.Equal(t, "Bearer tok", r.Header.Get("Authorization"))
		var files []githubPRFile
		switch r.URL.Query().Get("page") {
		case "1":
			for i := 0; i < 100; i++ {
				files = append(files, githubPRFile{Filename: fmt.Sprintf("charts/c%d/README.md", i), Changes: 1, Patch: "+docs"})
			}
		case "2":
			files = []githubPRFile{{Filename: "deploy/values.yaml", Changes: 2, Patch: "-    cpu: 318m\n+    cpu: 265m"}}
		}
		_ = json.NewEncoder(w).Encode(files)
	})

	added, err := fetchGitHubPRAddedLines(security.NewRequestContextForSuperAdmin(nil, nil, nil), githubTestMeta(), map[string]string{"password": "tok"})
	require.NoError(t, err)
	assert.Len(t, added, 101)
	assert.Equal(t, "    cpu: 265m", added[100])
}

// GitHub omits the patch of a large file. For a config file that is "could not
// check", never "the values are not there".
func TestFetchGitHubPRAddedLinesRefusesAWithheldConfigDiff(t *testing.T) {
	withGitHubAPI(t, func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode([]githubPRFile{
			{Filename: "logo.png", Changes: 1},
			{Filename: "deploy/values.yaml", Changes: 4000},
		})
	})

	_, err := fetchGitHubPRAddedLines(security.NewRequestContextForSuperAdmin(nil, nil, nil), githubTestMeta(), map[string]string{"password": "tok"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "deploy/values.yaml")
}

func TestFetchGitHubPRAddedLinesReportsANon200(t *testing.T) {
	withGitHubAPI(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"message":"Resource not accessible by integration"}`))
	})

	_, err := fetchGitHubPRAddedLines(security.NewRequestContextForSuperAdmin(nil, nil, nil), githubTestMeta(), map[string]string{"password": "tok"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "403")
}

// An older code-analysis never reads the instruction and answers with its usual
// "nothing to do"; the missing acknowledgement is how that is told apart from a
// run that tried and failed.
func TestFollowupInstructionHandled(t *testing.T) {
	assert.True(t, followupInstructionHandled([]string{`{"execution_status":"no_op","followup_instruction_handled":true}`}))
	assert.False(t, followupInstructionHandled([]string{`{"execution_status":"no_op"}`}))
	assert.False(t, followupInstructionHandled([]string{`not json`}))
	assert.False(t, followupInstructionHandled(nil))
}

// The review loop sends no instruction and its request must not change; a value
// refresh's instruction travels in its own field.
func TestBuildPRFollowupChatRequestCarriesTheInstruction(t *testing.T) {
	meta := prMetadata{PRURL: "https://github.com/acme/infra/pull/42", RepoURL: "https://github.com/acme/infra"}

	review := buildPRFollowupChatRequest(meta, "tok", "Follow up on PR", "", nil)
	assert.NotContains(t, review.Query, `"instruction"`)

	refresh := buildPRFollowupChatRequest(meta, "tok", "Apply cpu 265m", "Apply cpu 265m", nil)
	var payload map[string]any
	require.NoError(t, json.Unmarshal([]byte(strings.TrimPrefix(refresh.Query, "@agent_code_2 ")), &payload))
	assert.Equal(t, "Apply cpu 265m", payload["instruction"])
	assert.Equal(t, true, payload["followup"])
}

// A run that finished without the values on the branch is charged to the cap —
// that is what stops the same request going to the same agent forever — and,
// like any failure, keeps the cooldown stamp.
func TestRecordValueRefreshNotLandedChargesTheCap(t *testing.T) {
	sql := captureExec(t, func(dbms *database.DatabaseManager) {
		recordValueRefreshNotLanded(dbms, "res-1", "cpu request 265m is not in the pull request's changes")
	})

	assert.Contains(t, sql, "value_refresh_count = value_refresh_count + 1")
	assert.Contains(t, sql, "status_message")
	assert.NotContains(t, sql, "last_value_refresh_at", "the cooldown stamp stays as the claim wrote it")
}
