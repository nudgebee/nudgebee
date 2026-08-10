package vulnmatcher

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"nudgebee/services/config"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func withTestServer(t *testing.T, handler http.HandlerFunc) {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)

	prev := config.Config.VulnMatcherServerEndpoint
	config.Config.VulnMatcherServerEndpoint = server.URL
	t.Cleanup(func() { config.Config.VulnMatcherServerEndpoint = prev })
}

func TestMatch_Success(t *testing.T) {
	epoch := 1
	want := MatchResponse{
		DBVersion: "v1",
		Findings: []Finding{
			{Key: "pkg-1", VulnID: "CVE-2024-0001", FixState: FixStateFixed, Severity: "High"},
		},
	}
	withTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodPost, r.Method)
		assert.Equal(t, "/v1/match", r.URL.Path)

		var req MatchRequest
		require.NoError(t, json.NewDecoder(r.Body).Decode(&req))
		assert.Equal(t, "redhat", req.OS.Family)
		require.Len(t, req.Packages, 1)
		assert.Equal(t, "openssl", req.Packages[0].SourceName)
		require.NotNil(t, req.Packages[0].Epoch)
		assert.Equal(t, 1, *req.Packages[0].Epoch)

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(want)
	})

	got, err := Match(MatchRequest{
		OS: OS{Family: "redhat", Version: "9"},
		Packages: []Package{
			{Key: "pkg-1", Name: "openssl-libs", Type: PkgTypeRPM, Version: "3.0.7-24.el9", Epoch: &epoch, SourceName: "openssl"},
		},
	})
	require.NoError(t, err)
	assert.Equal(t, want, got)
}

func TestMatch_SuspectZero(t *testing.T) {
	withTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(MatchResponse{SuspectZero: true})
	})

	got, err := Match(MatchRequest{OS: OS{Family: "ubuntu", Version: "22.04"}})
	require.NoError(t, err)
	assert.True(t, got.SuspectZero)
	assert.Empty(t, got.Findings)
}

func TestMatch_422MissingSourceName(t *testing.T) {
	withTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnprocessableEntity)
		_ = json.NewEncoder(w).Encode(apiError{Code: CodeMissingSource, Message: "source_name is required for rpm packages"})
	})

	_, err := Match(MatchRequest{})
	require.Error(t, err)

	var matchErr *MatchError
	require.ErrorAs(t, err, &matchErr)
	assert.Equal(t, CodeMissingSource, matchErr.Code)
}

func TestMatch_UnexpectedStatus(t *testing.T) {
	withTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("boom"))
	})

	_, err := Match(MatchRequest{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "status 500")
}

func TestCapabilities_Success(t *testing.T) {
	want := CapabilitiesResponse{
		DBVersion: "v1",
		Supported: []SupportedOS{{Family: "redhat", Versions: []string{"8", "9"}}},
	}
	withTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodGet, r.Method)
		assert.Equal(t, "/v1/capabilities", r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(want)
	})

	got, err := Capabilities()
	require.NoError(t, err)
	assert.Equal(t, want, got)
	assert.True(t, got.SupportsOS("RedHat", "9"))
	assert.False(t, got.SupportsOS("redhat", "7"))
	assert.False(t, got.SupportsOS("ubuntu", "22.04"))
}

// Regression: a live vuln-matcher-server capabilities response lists Ubuntu
// minor versions un-padded ("22.4"), while /etc/os-release's VERSION_ID is
// zero-padded ("22.04") — /v1/match itself accepts "22.04" directly, so
// SupportsOS must not reject it just because the capabilities listing
// formats it differently.
func TestSupportsOS_TreatsZeroPaddedVersionAsEqual(t *testing.T) {
	caps := CapabilitiesResponse{
		Supported: []SupportedOS{{Family: "ubuntu", Versions: []string{"22.4", "22.10"}}},
	}
	assert.True(t, caps.SupportsOS("ubuntu", "22.04"))
	assert.True(t, caps.SupportsOS("ubuntu", "22.10"))
	assert.False(t, caps.SupportsOS("ubuntu", "22.4.1"))
	assert.False(t, caps.SupportsOS("ubuntu", "23.04"))
}
