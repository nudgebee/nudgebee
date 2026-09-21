package integrations

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

// Jira Data Center rejects personal access tokens over Basic with 401, so the
// datacenter_pat auth_type must send a bearer token; every other auth_type
// (including unset, which older integrations store) keeps Basic.
func TestNewJiraClient_AuthHeader(t *testing.T) {
	var gotAuth string
	ts := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"name":"svc"}`))
	}))
	defer ts.Close()

	// The auth transports fall back to http.DefaultTransport; point it at the
	// test server's TLS config.
	orig := http.DefaultTransport
	http.DefaultTransport = ts.Client().Transport
	defer func() { http.DefaultTransport = orig }()

	host := strings.TrimPrefix(ts.URL, "https://")
	tests := []struct {
		name, authType, username, want string
	}{
		{"datacenter pat uses bearer", JiraAuthDataCenterPAT, "", "Bearer secret-pat"},
		{"token uses basic", JiraAuthToken, "user", "Basic dXNlcjpzZWNyZXQtcGF0"},
		{"unset uses basic", "", "user", "Basic dXNlcjpzZWNyZXQtcGF0"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotAuth = ""
			client, err := newJiraClient(host, tt.authType, tt.username, "secret-pat", 5*time.Second)
			assert.NoError(t, err)
			_, _, err = client.User.GetSelf()
			assert.NoError(t, err)
			assert.Equal(t, tt.want, gotAuth)
		})
	}
}
