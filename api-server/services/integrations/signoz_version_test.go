package integrations

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"nudgebee/services/integrations/core"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseSignozVersion(t *testing.T) {
	cases := []struct {
		in   string
		want [3]int
		ok   bool
	}{
		{"v0.51.0", [3]int{0, 51, 0}, true},
		{"0.51.0", [3]int{0, 51, 0}, true},
		{" v0.132.4 ", [3]int{0, 132, 4}, true},
		{"v0.52.0-rc.1", [3]int{0, 52, 0}, true},
		{"1.2.3", [3]int{1, 2, 3}, true},
		{"", [3]int{}, false},
		{"main", [3]int{}, false},
		{"deadbeef", [3]int{}, false},
		{"v0.51", [3]int{}, false},
	}
	for _, c := range cases {
		got, ok := parseSignozVersion(c.in)
		assert.Equal(t, c.ok, ok, "ok for %q", c.in)
		if c.ok {
			assert.Equal(t, c.want, got, "value for %q", c.in)
		}
	}
}

func TestSignozVersionGreater(t *testing.T) {
	assert.True(t, signozVersionGreater([3]int{0, 132, 0}, [3]int{0, 51, 0}))
	assert.True(t, signozVersionGreater([3]int{0, 51, 1}, [3]int{0, 51, 0}))
	assert.True(t, signozVersionGreater([3]int{1, 0, 0}, [3]int{0, 51, 0}))
	assert.False(t, signozVersionGreater([3]int{0, 51, 0}, [3]int{0, 51, 0}))
	assert.False(t, signozVersionGreater([3]int{0, 50, 9}, [3]int{0, 51, 0}))
}

// versionServer returns an httptest server whose /api/v1/version replies with
// the given version string (status 200). Login is stubbed to succeed so a
// non-blocking version check falls through to a passing validation.
func versionServer(t *testing.T, version string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/version":
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{"version": version})
		case "/api/v1/login":
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]string{"accessJwt": "jwt-token"})
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
}

func signozCfg(url string) []core.IntegrationConfigValue {
	return []core.IntegrationConfigValue{
		{Name: "signoz_url", Value: url},
		{Name: "signoz_username", Value: "admin@example.com"},
		{Name: "signoz_password", Value: "secret"},
		{Name: "signoz_mode", Value: SignozModeSelfHosted},
	}
}

func TestSignoz_ValidateConfig_RejectsNewerSelfHostedVersion(t *testing.T) {
	srv := versionServer(t, "v0.132.4")
	defer srv.Close()

	errs := Signoz{}.ValidateConfig(nil, signozCfg(srv.URL), "acc-1")
	require.Len(t, errs, 1)
	assert.Contains(t, errs[0].Error(), "newer than the highest tested version")
	assert.Contains(t, errs[0].Error(), SignozMaxTestedVersion)
}

func TestSignoz_ValidateConfig_AllowsTestedSelfHostedVersion(t *testing.T) {
	srv := versionServer(t, "v0.51.0")
	defer srv.Close()

	errs := Signoz{}.ValidateConfig(nil, signozCfg(srv.URL), "acc-1")
	assert.Empty(t, errs)
}

func TestSignoz_ValidateConfig_AllowsOlderSelfHostedVersion(t *testing.T) {
	srv := versionServer(t, "v0.49.1")
	defer srv.Close()

	errs := Signoz{}.ValidateConfig(nil, signozCfg(srv.URL), "acc-1")
	assert.Empty(t, errs)
}

// A non-semver build (dev/custom image) can't be compared, so the version gate
// must not block — validation falls through to the login probe.
func TestSignoz_ValidateConfig_NonSemverVersionDoesNotBlock(t *testing.T) {
	srv := versionServer(t, "main")
	defer srv.Close()

	errs := Signoz{}.ValidateConfig(nil, signozCfg(srv.URL), "acc-1")
	assert.Empty(t, errs)
}

// When /api/v1/version is unreachable/absent the gate must not block on
// version; the login probe still runs and here it succeeds.
func TestSignoz_ValidateConfig_MissingVersionEndpointDoesNotBlock(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/login" {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]string{"accessJwt": "jwt-token"})
			return
		}
		w.WriteHeader(http.StatusNotFound) // /api/v1/version → 404
	}))
	defer srv.Close()

	errs := Signoz{}.ValidateConfig(nil, signozCfg(srv.URL), "acc-1")
	assert.Empty(t, errs)
}

func TestCheckSignozVersion_UnreachableHostDoesNotBlock(t *testing.T) {
	// Nothing listening — HttpGet errors, so no version-based rejection.
	errs := checkSignozVersion("http://127.0.0.1:0")
	assert.Empty(t, errs)
}

func TestSignoz_MaxTestedVersionParses(t *testing.T) {
	_, ok := parseSignozVersion(SignozMaxTestedVersion)
	assert.True(t, ok, "SignozMaxTestedVersion %q must be a valid semver", SignozMaxTestedVersion)
	assert.False(t, strings.HasPrefix(SignozMaxTestedVersion, "v"), "store the bare version; the 'v' is added in messages")
}
