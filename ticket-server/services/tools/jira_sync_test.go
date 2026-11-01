package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
	"time"

	jira "github.com/andygrunwald/go-jira"
)

// fakeJiraSync serves just the search endpoints FetchIssuesByKeys touches, as
// either deployment, and can be told to answer 429 a fixed number of times
// before succeeding.
type fakeJiraSync struct {
	cloud       bool
	rejectFirst int // number of requests to answer with 429 before succeeding
	retryAfter  string
	failCall    int      // 1-indexed call number to permanently fail with 500 (non-retryable), 0 disables
	calls       []string // jql seen by each request, in order
}

var jiraSyncKeyPattern = regexp.MustCompile(`[A-Z]+-\d+`)

func (f *fakeJiraSync) issuesForJQL(jql string) []byte {
	keys := jiraSyncKeyPattern.FindAllString(jql, -1)
	issues := make([]map[string]interface{}, 0, len(keys))
	for _, k := range keys {
		issues = append(issues, map[string]interface{}{
			"key": k,
			"fields": map[string]interface{}{
				"status":   map[string]string{"name": "Done"},
				"assignee": map[string]string{"displayName": k + "-owner"},
			},
		})
	}
	body, _ := json.Marshal(map[string]interface{}{"issues": issues})
	return body
}

func (f *fakeJiraSync) handler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	if r.URL.Path == "/rest/api/2/serverInfo" {
		if f.cloud {
			_, _ = w.Write([]byte(`{"deploymentType":"Cloud"}`))
		} else {
			_, _ = w.Write([]byte(`{"deploymentType":"Server"}`))
		}
		return
	}

	var jql string
	switch {
	case f.cloud && r.Method == http.MethodPost && r.URL.Path == "/rest/api/2/search/jql":
		raw, _ := io.ReadAll(r.Body)
		var body struct {
			JQL string `json:"jql"`
		}
		_ = json.Unmarshal(raw, &body)
		jql = body.JQL
	case !f.cloud && r.Method == http.MethodGet && r.URL.Path == "/rest/api/2/search":
		jql = r.URL.Query().Get("jql")
	default:
		http.Redirect(w, r, "/login.jsp", http.StatusFound)
		return
	}

	f.calls = append(f.calls, jql)
	callIdx := len(f.calls)
	if f.failCall != 0 && callIdx == f.failCall {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"errorMessages":["boom"]}`))
		return
	}
	if callIdx <= f.rejectFirst {
		if f.retryAfter != "" {
			w.Header().Set("Retry-After", f.retryAfter)
		}
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(`{"errorMessages":["rate limited"]}`))
		return
	}

	_, _ = w.Write(f.issuesForJQL(jql))
}

func startFakeJiraSync(t *testing.T, cloud bool) (*fakeJiraSync, *jira.Client) {
	t.Helper()
	f := &fakeJiraSync{cloud: cloud}
	ts := httptest.NewTLSServer(http.HandlerFunc(f.handler))
	t.Cleanup(ts.Close)
	client, err := jira.NewClient(ts.Client(), ts.URL)
	if err != nil {
		t.Fatalf("jira.NewClient: %v", err)
	}
	return f, client
}

func TestFetchIssuesByKeys_ChunksIntoBatches(t *testing.T) {
	f, client := startFakeJiraSync(t, false)

	keys := make([]string, 150)
	for i := range keys {
		keys[i] = fmt.Sprintf("PROJ-%d", i+1)
	}

	issues, err := FetchIssuesByKeys(context.Background(), client, keys)
	if err != nil {
		t.Fatalf("FetchIssuesByKeys: %v", err)
	}
	if len(issues) != 150 {
		t.Fatalf("got %d issues, want 150", len(issues))
	}
	if len(f.calls) != 2 {
		t.Fatalf("got %d search calls, want 2 (150 keys / batch size 100)", len(f.calls))
	}
	firstBatchKeys := jiraSyncKeyPattern.FindAllString(f.calls[0], -1)
	secondBatchKeys := jiraSyncKeyPattern.FindAllString(f.calls[1], -1)
	if len(firstBatchKeys) != 100 || len(secondBatchKeys) != 50 {
		t.Fatalf("batch sizes = %d, %d, want 100, 50", len(firstBatchKeys), len(secondBatchKeys))
	}
}

func TestFetchIssuesByKeys_DataCenterRetriesOn429(t *testing.T) {
	f, client := startFakeJiraSync(t, false)
	f.rejectFirst = 1
	f.retryAfter = "1"

	start := time.Now()
	issues, err := FetchIssuesByKeys(context.Background(), client, []string{"PROJ-1", "PROJ-2"})
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("FetchIssuesByKeys: %v", err)
	}
	if len(issues) != 2 {
		t.Fatalf("got %d issues, want 2", len(issues))
	}
	if len(f.calls) != 2 {
		t.Fatalf("got %d requests, want 2 (one 429, one success)", len(f.calls))
	}
	if elapsed < time.Second {
		t.Fatalf("elapsed %v, want to have honored the 1s Retry-After header", elapsed)
	}
}

func TestFetchIssuesByKeys_GivesUpAfterMaxRetries(t *testing.T) {
	f, client := startFakeJiraSync(t, false)
	f.rejectFirst = jiraSyncMaxRetries + 1
	f.retryAfter = "0" // falls back to exponential backoff, keeping the test fast

	_, err := FetchIssuesByKeys(context.Background(), client, []string{"PROJ-1"})
	if err == nil {
		t.Fatal("expected an error after exhausting retries, got nil")
	}
	if len(f.calls) != jiraSyncMaxRetries+1 {
		t.Fatalf("got %d requests, want %d (initial + %d retries)", len(f.calls), jiraSyncMaxRetries+1, jiraSyncMaxRetries)
	}
}

func TestFetchIssuesByKeys_CloudUsesPOSTBody(t *testing.T) {
	f, client := startFakeJiraSync(t, true)

	issues, err := FetchIssuesByKeys(context.Background(), client, []string{"PROJ-1", "PROJ-2"})
	if err != nil {
		t.Fatalf("FetchIssuesByKeys: %v", err)
	}
	if len(issues) != 2 {
		t.Fatalf("got %d issues, want 2", len(issues))
	}
	if len(f.calls) != 1 {
		t.Fatalf("got %d requests, want 1", len(f.calls))
	}
	if !strings.Contains(f.calls[0], "key in (") {
		t.Fatalf("jql = %q, want a key-in clause carried in the POST body, not a query string", f.calls[0])
	}
}

// One bad batch (a non-retryable error, e.g. a malformed key tripping the
// JQL parser) must not stop later batches from being attempted — otherwise a
// single broken ticket in the first 100 keys would silently stop sync from
// ever reaching any ticket after it.
func TestFetchIssuesByKeys_OneBadBatchDoesNotStopLaterBatches(t *testing.T) {
	f, client := startFakeJiraSync(t, false)
	f.failCall = 1 // first batch (keys 1-100) fails; second (101-150) must still run

	keys := make([]string, 150)
	for i := range keys {
		keys[i] = fmt.Sprintf("PROJ-%d", i+1)
	}

	issues, err := FetchIssuesByKeys(context.Background(), client, keys)
	if err == nil {
		t.Fatal("expected a non-nil error reporting the failed batch")
	}
	if len(issues) != 50 {
		t.Fatalf("got %d issues, want 50 (the second batch's, despite the first batch failing)", len(issues))
	}
	if len(f.calls) != 2 {
		t.Fatalf("got %d requests, want 2 (both batches attempted)", len(f.calls))
	}
}

func TestFetchIssuesByKeys_Empty(t *testing.T) {
	issues, err := FetchIssuesByKeys(context.Background(), nil, nil)
	if err != nil || issues != nil {
		t.Fatalf("FetchIssuesByKeys(empty) = %v, %v; want nil, nil", issues, err)
	}
}
