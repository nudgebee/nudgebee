package signozclient

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// versionServer answers /api/v1/version with body (status 200), or 404 when body
// is empty; every other route goes to next when set.
func versionServer(t *testing.T, body string, next http.Handler) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/version" {
			if body == "" {
				http.NotFound(w, r)
				return
			}
			writeJSON(w, http.StatusOK, body)
			return
		}
		if next != nil {
			next.ServeHTTP(w, r)
			return
		}
		http.NotFound(w, r)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestRuleSchema_ByReleaseVersion(t *testing.T) {
	cases := []struct {
		version string
		want    string
	}{
		{"v0.51.0", RuleSchemaV4},
		{"v0.105.0", RuleSchemaV4},
		{"v0.117.0", RuleSchemaV4},
		{"v0.118.0", RuleSchemaV5},
		{"v0.145.0", RuleSchemaV5},
		{"v0.145.0-rc.1", RuleSchemaV5},
		{"v1.0.0", RuleSchemaV5},
	}
	for _, tc := range cases {
		t.Run(tc.version, func(t *testing.T) {
			srv := versionServer(t, `{"version":"`+tc.version+`","ee":"Y","setupCompleted":true}`, nil)
			got, err := RuleSchema(context.Background(), http.DefaultClient,
				Credentials{BaseURL: srv.URL, AuthType: AuthAPIKey, APIKey: "k"})
			require.NoError(t, err)
			assert.Equal(t, tc.want, got)
		})
	}
}

func TestRuleSchema_CloudIsAlwaysV5(t *testing.T) {
	got, err := RuleSchema(context.Background(), http.DefaultClient,
		Credentials{BaseURL: "http://192.0.2.1:1", Mode: ModeCloud, AuthType: AuthAPIKey})
	require.NoError(t, err)
	assert.Equal(t, RuleSchemaV5, got)
}

func TestRuleSchema_UnreadableVersionFallsBackToV1LoginEvidence(t *testing.T) {
	f := legacy()
	srv := versionServer(t, "", f)
	creds := Credentials{BaseURL: srv.URL, Email: "admin@example.com", Password: testPassword}
	got, err := RuleSchema(context.Background(), http.DefaultClient, creds)
	require.NoError(t, err)
	assert.Equal(t, RuleSchemaV4, got)
}

// A v2 login is shared by v0.100–v0.117 (v4 bodies) and v0.118+ (v5 bodies), so
// without a version it must not guess.
func TestRuleSchema_UnreadableVersionWithV2LoginIsAnError(t *testing.T) {
	f := modern()
	srv := versionServer(t, "", f)
	creds := Credentials{BaseURL: srv.URL, Email: "admin@example.com", Password: testPassword}
	_, err := RuleSchema(context.Background(), http.DefaultClient, creds)
	require.ErrorIs(t, err, ErrUnknownRuleSchema)
}

func TestRuleSchema_UnreadableVersionWithAPIKeyIsAnError(t *testing.T) {
	srv := versionServer(t, `{"version":"nightly"}`, nil)
	_, err := RuleSchema(context.Background(), http.DefaultClient,
		Credentials{BaseURL: srv.URL, AuthType: AuthAPIKey, APIKey: "k"})
	require.ErrorIs(t, err, ErrUnknownRuleSchema)
}
