package services

import (
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	jira "github.com/andygrunwald/go-jira"
	"nudgebee/tickets-server/models"
)

// Cloud pages projects through project/search; Server/Data Center returns the
// whole list from project and ignores paging parameters.
func TestListJiraProjects_ByDeployment(t *testing.T) {
	for _, tt := range []struct {
		name           string
		deploymentType string
		wantHits       map[string]int
	}{
		{"cloud", "Cloud", map[string]int{"/rest/api/2/serverInfo": 1, "/rest/api/2/project/search": 2}},
		{"data center", "Server", map[string]int{"/rest/api/2/serverInfo": 1, "/rest/api/2/project": 1}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			hits := map[string]int{}
			ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				hits[r.URL.Path]++
				w.Header().Set("Content-Type", "application/json")
				switch r.URL.Path {
				case "/rest/api/2/serverInfo":
					_, _ = w.Write([]byte(`{"deploymentType":"` + tt.deploymentType + `"}`))
				case "/rest/api/2/project/search":
					if r.URL.Query().Get("startAt") == "0" {
						_, _ = w.Write([]byte(`{"isLast":false,"values":[{"key":"A","name":"Alpha"}]}`))
					} else {
						_, _ = w.Write([]byte(`{"isLast":true,"values":[{"key":"B","name":"Beta"}]}`))
					}
				case "/rest/api/2/project":
					_, _ = w.Write([]byte(`[{"key":"A","name":"Alpha"},{"key":"B","name":"Beta"}]`))
				default:
					http.NotFound(w, r)
				}
			}))
			defer ts.Close()
			client, err := jira.NewClient(ts.Client(), ts.URL)
			if err != nil {
				t.Fatalf("NewClient: %v", err)
			}

			got, err := listJiraProjects(client)
			if err != nil {
				t.Fatalf("listJiraProjects: %v", err)
			}
			want := []models.Project{{Name: "Alpha", Key: "A"}, {Name: "Beta", Key: "B"}}
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("projects = %#v, want %#v", got, want)
			}
			if !reflect.DeepEqual(hits, tt.wantHits) {
				t.Fatalf("hits = %#v, want %#v", hits, tt.wantHits)
			}
		})
	}
}
