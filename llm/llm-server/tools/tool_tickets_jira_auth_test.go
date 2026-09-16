package tools

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
)

// Jira Data Center rejects personal access tokens over Basic with 401, so the
// datacenter_pat auth_type must send a bearer token; every other auth_type
// (including unset, which older integrations store) keeps Basic.
func TestNewJiraClient_AuthHeader(t *testing.T) {
	var gotAuth string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"name":"svc"}`))
	}))
	defer ts.Close()

	tests := []struct {
		name, authType, username, want string
	}{
		{"datacenter pat uses bearer", jiraAuthDataCenterPAT, "", "Bearer secret-pat"},
		{"token uses basic", "token", "user", "Basic dXNlcjpzZWNyZXQtcGF0"},
		{"unset uses basic", "", "user", "Basic dXNlcjpzZWNyZXQtcGF0"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotAuth = ""
			client, err := newJiraClient(map[string]string{
				"url":       ts.URL,
				"username":  tt.username,
				"token":     "secret-pat",
				"auth_type": tt.authType,
			})
			assert.NoError(t, err)
			_, _, err = client.User.GetSelf()
			assert.NoError(t, err)
			assert.Equal(t, tt.want, gotAuth)
		})
	}
}
