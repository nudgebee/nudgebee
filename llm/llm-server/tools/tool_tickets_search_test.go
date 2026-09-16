package tools

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeJira answers JQL search like one Jira deployment: Data Center serves
// rest/api/2/search and redirects the unknown v3 path to its login page, which
// returns HTML with 200; Cloud serves rest/api/3/search/jql and answers the
// removed v2 endpoint with 410.
func fakeJira(t *testing.T, dataCenter bool, hits map[string]int) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits[r.URL.Path]++
		switch r.URL.Path {
		case "/rest/api/2/serverInfo":
			w.Header().Set("Content-Type", "application/json")
			if dataCenter {
				_, _ = w.Write([]byte(`{"deploymentType":"Server"}`))
			} else {
				_, _ = w.Write([]byte(`{"deploymentType":"Cloud"}`))
			}
		case "/login.jsp":
			w.Header().Set("Content-Type", "text/html;charset=UTF-8")
			_, _ = w.Write([]byte("<html>login</html>"))
		case "/rest/api/2/search":
			if !dataCenter {
				w.WriteHeader(http.StatusGone)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"issues":[{"key":"PROJ-1","fields":{"summary":"dc"}}]}`))
		case "/rest/api/3/search/jql":
			if dataCenter {
				http.Redirect(w, r, "/login.jsp?permissionViolation=true", http.StatusFound)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"issues":[{"key":"PROJ-2","fields":{"summary":"cloud"}}]}`))
		default:
			http.NotFound(w, r)
		}
	}))
}

func TestSearchIssues_PicksEndpointByDeployment(t *testing.T) {
	tests := []struct {
		name       string
		dataCenter bool
		wantKey    string
		wantHits   map[string]int
	}{
		{
			name: "data center searches v2", dataCenter: true,
			wantKey:  "PROJ-1",
			wantHits: map[string]int{"/rest/api/2/serverInfo": 1, "/rest/api/2/search": 1},
		},
		{
			name: "cloud searches v3", dataCenter: false,
			wantKey:  "PROJ-2",
			wantHits: map[string]int{"/rest/api/2/serverInfo": 1, "/rest/api/3/search/jql": 1},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			hits := map[string]int{}
			srv := fakeJira(t, tt.dataCenter, hits)
			defer srv.Close()

			client, err := newJiraClient(map[string]string{"url": srv.URL, "username": "u", "token": "secret"})
			require.NoError(t, err)

			issues, err := searchIssues(client, "key = PROJ-1")
			require.NoError(t, err)
			require.Len(t, issues, 1)
			assert.Equal(t, tt.wantKey, issues[0].Key)
			assert.Equal(t, tt.wantHits, hits)
		})
	}
}

func TestSearchIssues_FailsWhenDeploymentUnknown(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "unavailable", http.StatusServiceUnavailable)
	}))
	defer srv.Close()

	client, err := newJiraClient(map[string]string{"url": srv.URL, "username": "u", "token": "secret"})
	require.NoError(t, err)

	_, err = searchIssues(client, "key = PROJ-1")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "detecting deployment type")
}
