package api

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"nudgebee/services/config"
	"nudgebee/services/internal/database"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// TestGithubWebhookHandler_IssueComment_DispatchesFollowup_DB drives the real
// POST /api/webhooks/github handler — HMAC signature verification, event
// parsing (extractGithubPRSignal), and the HasOpenPRResolutionForURL /
// ProcessOpenPRFollowup dispatch (#36457) — the same webhook path GitHub
// itself calls in production, as opposed to the pr-lifecycle-check cron
// backstop already covered by pr_lifecycle_stale_test.go.
//
// llm-server is stubbed via httptest so the background dispatch goroutine
// finalizes deterministically without a live LLM or code-analysis service.
//
// DB-gated: skips when no database is reachable (CI without a metastore).
func TestGithubWebhookHandler_IssueComment_DispatchesFollowup_DB(t *testing.T) {
	dbms, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		t.Skipf("skipping: database not accessible: %v", err)
	}

	stub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		respJSON, _ := json.Marshal(map[string]any{"execution_status": "no_op"})
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"data": map[string]any{"status": "COMPLETED", "response": []string{string(respJSON)}, "conversation_id": ""},
		})
	}))
	defer stub.Close()

	const webhookSecret = "test-webhook-secret"
	origEndpoint := config.Config.LLMServerEndpoint
	origSecret := config.Config.GithubWebhookSecret
	config.Config.LLMServerEndpoint = stub.URL
	config.Config.GithubWebhookSecret = webhookSecret
	t.Cleanup(func() {
		config.Config.LLMServerEndpoint = origEndpoint
		config.Config.GithubWebhookSecret = origSecret
	})

	// Unique PR URL per run so this can be re-run against a shared local DB
	// without colliding with a previous run's row.
	prURL := fmt.Sprintf("https://github.com/acme/infra/pull/%d", time.Now().UnixNano())
	const tenant = "00000000-0000-0000-0000-000000000002"

	meta := fmt.Sprintf(`{"pr_url":%q,"repo_url":"https://github.com/acme/infra","branch":"fix-1","provider":"github","tenant_id":%q}`, prURL, tenant)
	_, err = dbms.Db.Exec(`
		INSERT INTO event_resolution (id, event_id, type, data, status, type_reference_id, resolver_type, resolver_id, pr_lifecycle_state, pr_iteration_count)
		VALUES (gen_random_uuid(), gen_random_uuid(), 'PullRequest', $1::jsonb, 'InProgress', $2, 'NBLLM', 'webhook-test', 'needs_followup', 0)`,
		meta, prURL)
	require.NoError(t, err)
	t.Cleanup(func() {
		_, _ = dbms.Db.Exec(`DELETE FROM event_resolution WHERE type_reference_id = $1`, prURL)
		_, _ = dbms.Db.Exec(`DELETE FROM pr_followup WHERE pr_url = $1`, prURL)
	})

	gin.SetMode(gin.TestMode)
	r := gin.New()
	handlePublicWebhooksApis(r, nil, nil, slog.Default())

	body := fmt.Sprintf(`{
		"action": "created",
		"issue": {
			"html_url": %q,
			"number": 1,
			"pull_request": {"url": "https://api.github.com/repos/acme/infra/pulls/1"}
		},
		"comment": {"id": 1, "body": "please take another look", "user": {"login": "tester", "type": "User"}},
		"repository": {"html_url": "https://github.com/acme/infra", "full_name": "acme/infra"}
	}`, prURL)

	mac := hmac.New(sha256.New, []byte(webhookSecret))
	mac.Write([]byte(body))
	signature := "sha256=" + hex.EncodeToString(mac.Sum(nil))

	req := httptest.NewRequest(http.MethodPost, "/api/webhooks/github", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-GitHub-Event", "issue_comment")
	req.Header.Set("X-GitHub-Delivery", "test-delivery-"+prURL)
	req.Header.Set("X-Hub-Signature-256", signature)

	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	require.Contains(t, w.Body.String(), `"status":"queued"`)

	// Dispatch runs in a background goroutine (the handler returns before the
	// LLM round-trip completes, by design — see the comment in
	// githubWebhookHandler about the <10s webhook response budget), so poll
	// pr_followup for the claim to land and finalize.
	deadline := time.Now().Add(3 * time.Second)
	var state string
	for time.Now().Before(deadline) {
		_ = dbms.Db.QueryRow(`SELECT pr_lifecycle_state FROM pr_followup WHERE pr_url = $1`, prURL).Scan(&state)
		if state != "" && state != "addressing" {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	require.Equal(t, "needs_followup", state, "no_op outcome from the stub should leave the row needs_followup with the counter unchanged")
}

// TestGithubWebhookHandler_BadSignature_Rejected pins the auth boundary: a
// payload signed with the wrong secret must never reach dispatch. No DB
// needed — GithubWebhookSecret rejects before any lookup.
func TestGithubWebhookHandler_BadSignature_Rejected(t *testing.T) {
	origSecret := config.Config.GithubWebhookSecret
	config.Config.GithubWebhookSecret = "correct-secret"
	t.Cleanup(func() { config.Config.GithubWebhookSecret = origSecret })

	gin.SetMode(gin.TestMode)
	r := gin.New()
	handlePublicWebhooksApis(r, nil, nil, slog.Default())

	body := `{"action":"created"}`
	req := httptest.NewRequest(http.MethodPost, "/api/webhooks/github", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-GitHub-Event", "issue_comment")
	req.Header.Set("X-Hub-Signature-256", "sha256=0000000000000000000000000000000000000000000000000000000000000000")

	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusUnauthorized, w.Code)
}
