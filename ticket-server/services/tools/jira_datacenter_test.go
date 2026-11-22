package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"nudgebee/tickets-server/models"
	"reflect"
	"strconv"
	"strings"
	"testing"

	jira "github.com/andygrunwald/go-jira"
)

func mustEqual(t *testing.T, what string, got, want interface{}) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("%s = %#v, want %#v", what, got, want)
	}
}

// fakeJira serves the subset of the Jira REST API the ticket paths touch, as
// either deployment. Unknown paths redirect to the login page, which is how
// Data Center answers endpoints it lacks.
type fakeJira struct {
	cloud       bool
	hits        map[string]int
	lastPUT     map[string]interface{}
	searchPages []interface{}
	count       int // approximate-count answer; 0 leaves the endpoint unavailable
}

func (f *fakeJira) handler(w http.ResponseWriter, r *http.Request) {
	f.hits[r.URL.Path]++
	w.Header().Set("Content-Type", "application/json")
	switch {
	case r.URL.Path == "/rest/api/2/serverInfo":
		if f.cloud {
			_, _ = w.Write([]byte(`{"deploymentType":"Cloud"}`))
		} else {
			_, _ = w.Write([]byte(`{"deploymentType":"Server"}`))
		}
	case r.URL.Path == "/rest/api/2/user/search":
		q := r.URL.Query()
		switch {
		case f.cloud && q.Get("query") == "jdoe@example.com":
			_, _ = w.Write([]byte(`[{"accountId":"5b10ac8d82e05b22cc7d4ef5","emailAddress":"jdoe@example.com"}]`))
		case !f.cloud && q.Get("username") == "jdoe@example.com":
			_, _ = w.Write([]byte(`[{"name":"other","emailAddress":"other@example.com"},{"name":"jdoe","key":"jdoe","emailAddress":"jdoe@example.com"}]`))
		default:
			_, _ = w.Write([]byte(`[]`))
		}
	case r.Method == http.MethodPost && r.URL.Path == "/rest/api/2/search/jql" && f.cloud:
		var body struct {
			MaxResults    int    `json:"maxResults"`
			NextPageToken string `json:"nextPageToken"`
		}
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &body)
		f.searchPages = append(f.searchPages, body.MaxResults)
		// Four issues served two per page regardless of the requested size.
		switch body.NextPageToken {
		case "":
			_, _ = w.Write([]byte(`{"issues":[{"key":"PROJ-1","fields":{"summary":"a"}},{"key":"PROJ-2","fields":{"summary":"b"}}],"nextPageToken":"p2"}`))
		case "p2":
			_, _ = w.Write([]byte(`{"issues":[{"key":"PROJ-3","fields":{"summary":"c"}},{"key":"PROJ-4","fields":{"summary":"d"}}]}`))
		default:
			http.NotFound(w, r)
		}
	case r.Method == http.MethodPost && r.URL.Path == "/rest/api/2/search/approximate-count" && f.cloud && f.count > 0:
		_, _ = fmt.Fprintf(w, `{"count":%d}`, f.count)
	case r.Method == http.MethodGet && r.URL.Path == "/rest/api/2/search" && !f.cloud:
		f.searchPages = append(f.searchPages, r.URL.Query().Get("startAt")+"/"+r.URL.Query().Get("maxResults"))
		_, _ = w.Write([]byte(`{"startAt":2,"maxResults":2,"total":7,"issues":[{"key":"PROJ-3","fields":{"summary":"c"}},{"key":"PROJ-4","fields":{"summary":"d"}}]}`))
	case r.Method == http.MethodPut && r.URL.Path == "/rest/api/2/issue/PROJ-1":
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &f.lastPUT)
		w.WriteHeader(http.StatusNoContent)
	case r.URL.Path == "/rest/api/2/issue/createmeta":
		http.NotFound(w, r)
	case r.URL.Path == "/rest/api/2/issue/createmeta/PROJ/issuetypes":
		_, _ = w.Write([]byte(`{"isLast":true,"values":[{"id":"10001","name":"Task","subtask":false}]}`))
	case r.URL.Path == "/rest/api/2/issue/createmeta/PROJ/issuetypes/10001":
		if r.URL.Query().Get("startAt") == "0" {
			_, _ = w.Write([]byte(`{"isLast":false,"values":[{"fieldId":"summary","name":"Summary","required":true,"schema":{"type":"string","system":"summary"}}]}`))
		} else {
			_, _ = w.Write([]byte(`{"isLast":true,"values":[{"fieldId":"priority","name":"Priority","required":false,"schema":{"type":"priority","system":"priority"},"allowedValues":[{"id":"1","name":"High"}]}]}`))
		}
	case r.URL.Path == "/rest/api/2/priority":
		_, _ = w.Write([]byte(`[{"id":"1","name":"High"}]`))
	case r.URL.Path == "/rest/api/2/user/assignable/search":
		_, _ = w.Write([]byte(`[{"name":"jdoe","displayName":"J Doe"}]`))
	default:
		http.Redirect(w, r, "/login.jsp", http.StatusFound)
	}
}

func startFakeJira(t *testing.T, cloud bool) (*fakeJira, *httptest.Server) {
	t.Helper()
	f := &fakeJira{cloud: cloud, hits: map[string]int{}}
	ts := httptest.NewTLSServer(http.HandlerFunc(f.handler))
	t.Cleanup(ts.Close)
	orig := http.DefaultTransport
	http.DefaultTransport = ts.Client().Transport
	t.Cleanup(func() { http.DefaultTransport = orig })
	return f, ts
}

func TestResolveJiraAssignee_ByDeployment(t *testing.T) {
	tests := []struct {
		name     string
		cloud    bool
		input    string
		want     *jira.User
		wantHits map[string]int
	}{
		{
			name: "account id passes through without lookups", cloud: true, input: "712020:abc",
			want:     &jira.User{AccountID: "712020:abc"},
			wantHits: map[string]int{},
		},
		{
			name: "cloud resolves email to accountId", cloud: true, input: "jdoe@example.com",
			want:     &jira.User{AccountID: "5b10ac8d82e05b22cc7d4ef5"},
			wantHits: map[string]int{"/rest/api/2/serverInfo": 1, "/rest/api/2/user/search": 1},
		},
		{
			name: "data center resolves email to name", cloud: false, input: "jdoe@example.com",
			want:     &jira.User{Name: "jdoe"},
			wantHits: map[string]int{"/rest/api/2/serverInfo": 1, "/rest/api/2/user/search": 1},
		},
		{
			name: "data center keeps an unknown input as a name", cloud: false, input: "svc-bot",
			want:     &jira.User{Name: "svc-bot"},
			wantHits: map[string]int{"/rest/api/2/serverInfo": 1, "/rest/api/2/user/search": 1},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f, ts := startFakeJira(t, tt.cloud)
			client, err := jira.NewClient(ts.Client(), ts.URL)
			if err != nil {
				t.Fatalf("NewClient: %v", err)
			}

			mustEqual(t, "assignee", resolveJiraAssignee(client, tt.input), tt.want)
			mustEqual(t, "hits", f.hits, tt.wantHits)
		})
	}
}

func TestJiraUpdate_UsesV2WithPlainDescription(t *testing.T) {
	f, ts := startFakeJira(t, false)
	config := models.TicketConfigurations{URL: ts.URL, Username: "u", Password: "p"}

	err := (&JiraService{}).Update(nil, config, "PROJ-1", models.UpdateFields{
		Description: "line one\nline two",
		Assignees:   []string{"jdoe@example.com"},
		Labels:      []string{"nudgebee"},
	})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}

	mustEqual(t, "PUT count", f.hits["/rest/api/2/issue/PROJ-1"], 1)
	fields, _ := f.lastPUT["fields"].(map[string]interface{})
	mustEqual(t, "description", fields["description"], "line one\nline two")
	mustEqual(t, "assignee", fields["assignee"], map[string]interface{}{"name": "jdoe"})
	mustEqual(t, "labels", fields["labels"], []interface{}{"nudgebee"})
}

func TestFetchJiraIssueCreateMeta_FallsBackToIssueTypeEndpoints(t *testing.T) {
	f, ts := startFakeJira(t, false)
	config := models.TicketConfigurations{URL: ts.URL, Username: "u", Password: "p"}

	meta, err := FetchJiraIssueCreateMeta(config, "PROJ")
	if err != nil {
		t.Fatalf("FetchJiraIssueCreateMeta: %v", err)
	}

	mustEqual(t, "legacy createmeta tried once", f.hits["/rest/api/2/issue/createmeta"], 1)
	mustEqual(t, "field pages followed", f.hits["/rest/api/2/issue/createmeta/PROJ/issuetypes/10001"], 2)

	templates, _ := meta.(map[string]interface{})["data"].([]Template)
	if len(templates) != 1 {
		t.Fatalf("templates = %d, want 1", len(templates))
	}
	mustEqual(t, "issue type", templates[0].Name, "Task")
	mustEqual(t, "summary key", templates[0].Fields["summary"].Key, "summary")
	mustEqual(t, "summary required", templates[0].Fields["summary"].Required, true)
	mustEqual(t, "priority key", templates[0].Fields["priority"].Key, "priority")
	mustEqual(t, "priority options", len(templates[0].Fields["priority"].AllowedValues), 1)
}

func TestFetchJiraPages_FailsWhenPagingNeverEnds(t *testing.T) {
	calls := 0
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"isLast":false,"values":[{"id":"1"}]}`))
	}))
	defer ts.Close()
	client, err := jira.NewClient(ts.Client(), ts.URL)
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}

	var out []map[string]interface{}
	if err := fetchJiraPages(client, "rest/api/2/issue/createmeta/PROJ/issuetypes", &out); err == nil {
		t.Fatal("expected an error when paging never reports isLast")
	}
	mustEqual(t, "requests made", calls, 50)
}

func listKeys(t *models.ListResult) []string {
	keys := make([]string, 0, len(t.Tickets))
	for _, tk := range t.Tickets {
		keys = append(keys, tk.TicketID)
	}
	return keys
}

func TestJiraList_CloudWalksCursorToServeOffsetWindow(t *testing.T) {
	tests := []struct {
		name          string
		offset, limit int
		wantKeys      []string
		wantHasMore   bool
		wantTotal     int
		wantRequests  int
	}{
		{"window inside first page", 0, 1, []string{"PROJ-1"}, true, 0, 1},
		{"window spans pages", 1, 2, []string{"PROJ-2", "PROJ-3"}, true, 0, 2},
		{"window reaches the end", 2, 2, []string{"PROJ-3", "PROJ-4"}, false, 4, 2},
		{"offset beyond the end", 6, 2, []string{}, false, 4, 2},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f, ts := startFakeJira(t, true)
			config := models.TicketConfigurations{URL: ts.URL, Username: "u", Password: "p"}

			got, err := (&JiraService{}).List(nil, config, models.ListParams{ProjectKey: "PROJ", Offset: tt.offset, Limit: tt.limit})
			if err != nil {
				t.Fatalf("List: %v", err)
			}
			mustEqual(t, "keys", listKeys(got), tt.wantKeys)
			mustEqual(t, "has_more", got.HasMore, tt.wantHasMore)
			mustEqual(t, "total", got.Total, tt.wantTotal)
			mustEqual(t, "search requests", len(f.searchPages), tt.wantRequests)
			mustEqual(t, "legacy search never called", f.hits["/rest/api/2/search"], 0)
		})
	}
}

func TestJiraList_DataCenterUsesOffsetSearch(t *testing.T) {
	f, ts := startFakeJira(t, false)
	config := models.TicketConfigurations{URL: ts.URL, Username: "u", Password: "p"}

	got, err := (&JiraService{}).List(nil, config, models.ListParams{ProjectKey: "PROJ", Offset: 2, Limit: 2})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	mustEqual(t, "keys", listKeys(got), []string{"PROJ-3", "PROJ-4"})
	mustEqual(t, "total", got.Total, 7)
	mustEqual(t, "has_more", got.HasMore, true)
	mustEqual(t, "search request", f.searchPages, []interface{}{"2/2"})
	mustEqual(t, "cloud search never called", f.hits["/rest/api/2/search/jql"], 0)
}

func TestUsersToFieldValues_DataCenterIdentityAndEmailLookup(t *testing.T) {
	f, ts := startFakeJira(t, false)
	client, err := jira.NewClient(ts.Client(), ts.URL)
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	// A picker result without an email, as privacy settings can hide it.
	resp := &jira.Response{Response: &http.Response{Body: io.NopCloser(strings.NewReader(`[{"name":"jdoe","displayName":"jdoe@example.com"}]`))}}

	got, err := usersToFieldValues(client, resp, nil)
	if err != nil {
		t.Fatalf("usersToFieldValues: %v", err)
	}
	mustEqual(t, "values", got, []models.FieldValue{{ID: "jdoe", Name: "jdoe@example.com", Value: "jdoe@example.com"}})
	mustEqual(t, "lookup used the data center parameter", f.hits["/rest/api/2/user/search"], 1)
}

func TestSearchJiraCloudPage_FailsWhenOffsetIsBeyondThePageCap(t *testing.T) {
	calls := 0
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.Header().Set("Content-Type", "application/json")
		// One issue per page with a cursor that never ends.
		_, _ = w.Write([]byte(`{"issues":[{"key":"PROJ-1"}],"nextPageToken":"more"}`))
	}))
	defer ts.Close()
	client, err := jira.NewClient(ts.Client(), ts.URL)
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}

	issues, hasMore, _, err := searchJiraCloudPage(context.Background(), client, "", "project = PROJ", 50, 10)
	if err == nil {
		t.Fatalf("expected an error, got %d issues has_more=%v", len(issues), hasMore)
	}
	mustEqual(t, "pages walked before giving up", calls, 20)
}

// A second page resumes from the cached cursor instead of re-walking from the
// first page, and a stale cursor falls back to a full walk.
func TestSearchJiraCloudPage_ResumesFromCachedCursor(t *testing.T) {
	const total = 6
	var bodies []map[string]interface{}
	rejectCursor := false
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]interface{}
		_ = json.NewDecoder(r.Body).Decode(&body)
		bodies = append(bodies, body)
		w.Header().Set("Content-Type", "application/json")
		start := 0
		if tok, ok := body["nextPageToken"].(string); ok {
			if rejectCursor {
				http.Error(w, `{"errorMessages":["bad token"]}`, http.StatusBadRequest)
				return
			}
			start, _ = strconv.Atoi(strings.TrimPrefix(tok, "t"))
		}
		n := min(int(body["maxResults"].(float64)), 2, total-start) // server caps pages at 2 rows
		var issues []string
		for i := 0; i < n; i++ {
			issues = append(issues, fmt.Sprintf(`{"key":"PROJ-%d"}`, start+i))
		}
		next := ""
		if start+n < total {
			next = fmt.Sprintf(`,"nextPageToken":"t%d"`, start+n)
		}
		_, _ = w.Write([]byte(`{"issues":[` + strings.Join(issues, ",") + `]` + next + `}`))
	}))
	defer ts.Close()
	client, err := jira.NewClient(ts.Client(), ts.URL)
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	ctx := context.Background()
	const integration, jql = "it-resume", "project = RESUME"

	issues, hasMore, _, err := searchJiraCloudPage(ctx, client, integration, jql, 0, 2)
	if err != nil {
		t.Fatalf("page 1: %v", err)
	}
	mustEqual(t, "page 1 keys", issueKeys(issues), []string{"PROJ-0", "PROJ-1"})
	mustEqual(t, "page 1 has_more", hasMore, true)

	bodies = nil
	issues, hasMore, _, err = searchJiraCloudPage(ctx, client, integration, jql, 2, 2)
	if err != nil {
		t.Fatalf("page 2: %v", err)
	}
	mustEqual(t, "page 2 keys", issueKeys(issues), []string{"PROJ-2", "PROJ-3"})
	mustEqual(t, "page 2 has_more", hasMore, true)
	mustEqual(t, "page 2 requests (resumed, not re-walked)", len(bodies), 1)
	mustEqual(t, "page 2 cursor", bodies[0]["nextPageToken"], "t2")

	issues, hasMore, seen, err := searchJiraCloudPage(ctx, client, integration, jql, 4, 2)
	if err != nil {
		t.Fatalf("page 3: %v", err)
	}
	mustEqual(t, "page 3 keys", issueKeys(issues), []string{"PROJ-4", "PROJ-5"})
	mustEqual(t, "page 3 has_more", hasMore, false)
	mustEqual(t, "page 3 total", seen, total)

	// Stale cursor: the server rejects it, so the search re-walks from the start.
	rejectCursor = true
	bodies = nil
	issues, _, _, err = searchJiraCloudPage(ctx, client, integration, jql, 2, 2)
	if err == nil {
		t.Fatalf("expected the re-walk to fail while the server rejects every cursor, got %v", issueKeys(issues))
	}
	mustEqual(t, "resume attempt then first-page restart", len(bodies) >= 2, true)
	_, hasCursor := bodies[1]["nextPageToken"]
	mustEqual(t, "restart begins without a cursor", hasCursor, false)
}

func issueKeys(issues []jira.Issue) []string {
	keys := []string{}
	for _, i := range issues {
		keys = append(keys, i.Key)
	}
	return keys
}

// Paging advances by the comments actually returned, so an instance that caps
// maxResults below the requested page size does not drop comments.
func TestFetchCommentsFromJira_AdvancesByReturnedCount(t *testing.T) {
	const total, serverCap = 5, 2
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start, _ := strconv.Atoi(r.URL.Query().Get("startAt"))
		n := min(serverCap, total-start)
		var cs []string
		for i := 0; i < n; i++ {
			cs = append(cs, fmt.Sprintf(`{"author":{"displayName":"a"},"body":"c%d"}`, start+i))
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprintf(w, `{"startAt":%d,"maxResults":%d,"total":%d,"comments":[%s]}`, start, serverCap, total, strings.Join(cs, ","))
	}))
	defer ts.Close()
	client, err := jira.NewClient(ts.Client(), ts.URL)
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}

	got, err := fetchCommentsFromJira("PROJ-1", client)
	if err != nil {
		t.Fatalf("fetchCommentsFromJira: %v", err)
	}
	mustEqual(t, "comments fetched", len(got), total)
}

// A wiki-markup image reference ("!file.png|width=588!") that resolves to a
// real issue attachment must be replaced with an attachmentMarker in the
// returned body - not left as raw markup - so the frontend can render the
// image in its original position instead of as literal text.
func TestFetchCommentsFromJira_ReplacesResolvedImageRefWithMarker(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if strings.HasSuffix(r.URL.Path, "/comment") {
			_, _ = fmt.Fprint(w, `{"startAt":0,"maxResults":100,"total":1,"comments":[
				{"author":{"displayName":"alice"},
				 "body":"First Line\n\n!image-20261006.png|width=588,alt=\"image-20261006.png\"!\n\nLast line",
				 "created":"2026-10-06T05:26:00.000+0000","updated":"2026-10-06T05:26:00.000+0000"}
			]}`)
			return
		}
		// jiraIssueAttachmentsByFilename's GET /issue/{id}?fields=attachment
		_, _ = fmt.Fprint(w, `{"fields":{"attachment":[
			{"id":"10033","filename":"image-20261006.png","mimeType":"image/png"}
		]}}`)
	}))
	defer ts.Close()
	client, err := jira.NewClient(ts.Client(), ts.URL)
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}

	got, err := fetchCommentsFromJira("PROJ-1", client)
	if err != nil {
		t.Fatalf("fetchCommentsFromJira: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("len(comments) = %d, want 1", len(got))
	}

	wantBody := "First Line\n\n[[nb-attachment:10033]]\n\nLast line"
	if got[0].Comment != wantBody {
		t.Fatalf("comment body = %q, want %q", got[0].Comment, wantBody)
	}
	if len(got[0].Attachments) != 1 || got[0].Attachments[0].ID != "10033" || got[0].Attachments[0].Name != "image-20261006.png" {
		t.Fatalf("attachments = %+v, want one with id=10033 name=image-20261006.png", got[0].Attachments)
	}
}

// A wiki-markup image reference that does NOT match any known issue
// attachment (e.g. the attachment lookup failed, or it references something
// stale) must be left as literal text, not silently dropped.
func TestFetchCommentsFromJira_LeavesUnresolvedImageRefAsText(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if strings.HasSuffix(r.URL.Path, "/comment") {
			_, _ = fmt.Fprint(w, `{"startAt":0,"maxResults":100,"total":1,"comments":[
				{"author":{"displayName":"alice"}, "body":"see !unknown.png! here",
				 "created":"2026-10-06T05:26:00.000+0000","updated":"2026-10-06T05:26:00.000+0000"}
			]}`)
			return
		}
		_, _ = fmt.Fprint(w, `{"fields":{"attachment":[]}}`)
	}))
	defer ts.Close()
	client, err := jira.NewClient(ts.Client(), ts.URL)
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}

	got, err := fetchCommentsFromJira("PROJ-1", client)
	if err != nil {
		t.Fatalf("fetchCommentsFromJira: %v", err)
	}
	wantBody := "see !unknown.png! here"
	if len(got) != 1 || got[0].Comment != wantBody {
		t.Fatalf("got %+v, want body %q unchanged with no attachments", got, wantBody)
	}
	if len(got[0].Attachments) != 0 {
		t.Fatalf("attachments = %+v, want none", got[0].Attachments)
	}
}

// The approximate count is fetched once per (integration, jql) and reused across
// pages, and an unavailable endpoint leaves the total unknown (0).
func TestJiraApproximateCount_CachesAndDegrades(t *testing.T) {
	calls := 0
	fail := false
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.URL.Path != "/rest/api/2/search/approximate-count" {
			http.NotFound(w, r)
			return
		}
		if fail {
			http.Error(w, `{"errorMessages":["nope"]}`, http.StatusBadRequest)
			return
		}
		var body map[string]interface{}
		_ = json.NewDecoder(r.Body).Decode(&body)
		if strings.Contains(body["jql"].(string), "ORDER BY") {
			http.Error(w, `{"errorMessages":["unexpected ORDER BY"]}`, http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"count":770}`))
	}))
	defer ts.Close()
	client, err := jira.NewClient(ts.Client(), ts.URL)
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	ctx := context.Background()

	mustEqual(t, "first count", jiraApproximateCount(ctx, client, "it-count", `project = "CNT"`), 770)
	mustEqual(t, "second count (cached)", jiraApproximateCount(ctx, client, "it-count", `project = "CNT"`), 770)
	mustEqual(t, "requests made", calls, 1)

	fail = true
	mustEqual(t, "endpoint failing leaves total unknown", jiraApproximateCount(ctx, client, "it-count-fail", `project = "OTHER"`), 0)
}

// While more pages remain the total is Jira's approximate count, and on the last
// page it is the exact number of rows seen, without asking for a count.
func TestJiraList_CloudReportsApproximateTotalWhileMorePagesRemain(t *testing.T) {
	f, ts := startFakeJira(t, true)
	f.count = 40
	config := models.TicketConfigurations{URL: ts.URL, Username: "u", Password: "p"}

	got, err := (&JiraService{}).List(nil, config, models.ListParams{ProjectKey: "PROJ", Offset: 0, Limit: 1})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	mustEqual(t, "has_more", got.HasMore, true)
	mustEqual(t, "approximate total", got.Total, 40)
	mustEqual(t, "count requests", f.hits["/rest/api/2/search/approximate-count"], 1)

	f.hits = map[string]int{}
	got, err = (&JiraService{}).List(nil, config, models.ListParams{ProjectKey: "PROJ", Offset: 2, Limit: 2})
	if err != nil {
		t.Fatalf("List last page: %v", err)
	}
	mustEqual(t, "has_more on last page", got.HasMore, false)
	mustEqual(t, "exact total on last page", got.Total, 4)
	mustEqual(t, "no count request on last page", f.hits["/rest/api/2/search/approximate-count"], 0)
}

// A self-hosted Jira served over plain http under a context path: API calls must
// go to <base>/rest/api/2/..., and issue links must keep the scheme and the path.
func TestJira_ContextPathOverHTTP(t *testing.T) {
	f := &fakeJira{cloud: false, hits: map[string]int{}}
	// StripPrefix makes every request outside /jira/ a 404, so a client that drops
	// the context path fails the test instead of silently working.
	ts := httptest.NewServer(http.StripPrefix("/jira", http.HandlerFunc(f.handler)))
	defer ts.Close()
	config := models.TicketConfigurations{URL: ts.URL + "/jira/", Username: "u", Password: "p"}

	got, err := (&JiraService{}).List(nil, config, models.ListParams{ProjectKey: "PROJ", Offset: 2, Limit: 2})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	mustEqual(t, "issues served", len(got.Tickets), 2)
	for _, tk := range got.Tickets {
		mustEqual(t, "issue link", tk.URL, ts.URL+"/jira/browse/"+tk.TicketID)
	}
	mustEqual(t, "server info fetched under the context path", f.hits["/rest/api/2/serverInfo"], 1)
}
