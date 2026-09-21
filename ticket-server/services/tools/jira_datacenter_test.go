package tools

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"nudgebee/tickets-server/models"
	"reflect"
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
	cloud   bool
	hits    map[string]int
	lastPUT map[string]interface{}
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
