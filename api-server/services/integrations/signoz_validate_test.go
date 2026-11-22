package integrations

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"nudgebee/services/integrations/core"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const testSignozPassword = "secret"

// signozServer emulates a SigNoz release: version is what /api/v1/version
// reports, v1/v2 say which login API exists. A release with v2 and without v1
// serves its SPA on /api/v1/login, as v0.145 does.
func signozServer(t *testing.T, version string, v1, v2 bool) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]string
		if r.Method == http.MethodPost {
			_ = json.NewDecoder(r.Body).Decode(&body)
		}
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/api/v1/version":
			_ = json.NewEncoder(w).Encode(map[string]any{"version": version})
		case r.URL.Path == "/api/v2/sessions/context" && v2:
			_, _ = fmt.Fprint(w, `{"status":"success","data":{"exists":true,"orgs":[{"id":"org-1","authNSupport":{"password":[{"provider":"email_password"}]}}]}}`)
		case r.URL.Path == "/api/v2/sessions/email_password" && v2:
			if body["password"] != testSignozPassword {
				w.WriteHeader(http.StatusUnauthorized)
				_, _ = fmt.Fprint(w, `{"status":"error","error":{"type":"unauthenticated","code":"incorrect_password","message":"invalid email or password"}}`)
				return
			}
			_, _ = fmt.Fprint(w, `{"status":"success","data":{"accessToken":"v2-token","expiresIn":1799}}`)
		case r.URL.Path == "/api/v1/login" && v1:
			if body["password"] != testSignozPassword {
				w.WriteHeader(http.StatusUnauthorized)
				_, _ = fmt.Fprint(w, `{"errors":[{"code":401,"msg":"invalid credentials"}]}`)
				return
			}
			_, _ = fmt.Fprint(w, `{"accessJwt":"v1-token","accessJwtExpiry":4102444800}`)
		case r.URL.Path == "/api/v1/login":
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = fmt.Fprint(w, "<!doctype html><html></html>")
		default:
			w.Header().Set("Content-Type", "text/plain")
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func signozCfg(url, password, mode string) []core.IntegrationConfigValue {
	return []core.IntegrationConfigValue{
		{Name: "signoz_url", Value: url},
		{Name: "signoz_username", Value: "admin@example.com"},
		{Name: "signoz_password", Value: password},
		{Name: "signoz_mode", Value: mode},
	}
}

func TestSignoz_ValidateConfig_AcceptsEverySelfHostedRelease(t *testing.T) {
	cases := []struct {
		name    string
		version string
		v1, v2  bool
	}{
		{"legacy v0.49 (v1 login)", "v0.49.1", true, false},
		{"legacy v0.51 (v1 login)", "v0.51.0", true, false},
		{"transitional v0.100 (both)", "v0.100.0", true, true},
		{"v0.132 (v2 sessions, previously rejected by the version cap)", "v0.132.4", false, true},
		{"current v0.145 (v2 sessions)", "v0.145.0", false, true},
		{"unparseable dev build", "main", false, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := signozServer(t, tc.version, tc.v1, tc.v2)
			errs := Signoz{}.ValidateConfig(nil, signozCfg(srv.URL, testSignozPassword, SignozModeSelfHosted), "acc-1")
			assert.Empty(t, errs)
		})
	}
}

func TestSignoz_ValidateConfig_WrongPasswordOnEveryRelease(t *testing.T) {
	for _, tc := range []struct {
		name   string
		v1, v2 bool
	}{{"legacy", true, false}, {"current", false, true}} {
		t.Run(tc.name, func(t *testing.T) {
			srv := signozServer(t, "v0.0.0", tc.v1, tc.v2)
			errs := Signoz{}.ValidateConfig(nil, signozCfg(srv.URL, "wrong", SignozModeSelfHosted), "acc-1")
			require.Len(t, errs, 1)
			assert.Contains(t, errs[0].Error(), "invalid SigNoz credentials")
		})
	}
}

func TestSignoz_ValidateConfig_CloudModeAgainstLegacyServerFails(t *testing.T) {
	srv := signozServer(t, "v0.51.0", true, false)
	errs := Signoz{}.ValidateConfig(nil, signozCfg(srv.URL, testSignozPassword, SignozModeCloud), "acc-1")
	require.Len(t, errs, 1)
	assert.Contains(t, errs[0].Error(), "SigNoz Cloud")
}

func TestSignoz_ValidateConfig_NotSigNozIsRejected(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = fmt.Fprint(w, "<!doctype html><html></html>")
	}))
	t.Cleanup(srv.Close)

	errs := Signoz{}.ValidateConfig(nil, signozCfg(srv.URL, testSignozPassword, SignozModeSelfHosted), "acc-1")
	require.Len(t, errs, 1)
	assert.Contains(t, errs[0].Error(), "neither /api/v2/sessions nor /api/v1/login")
}

func TestSignoz_ValidateConfig_UnreachableHost(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	url := srv.URL
	srv.Close()

	errs := Signoz{}.ValidateConfig(nil, signozCfg(url, testSignozPassword, SignozModeSelfHosted), "acc-1")
	require.Len(t, errs, 1)
	assert.Contains(t, errs[0].Error(), "failed to connect")
}

const testSignozAPIKey = "key-1"

// signozKeyServer emulates SigNoz's query API for API-key auth: the label
// autocomplete answers the one valid key, and forbidden models a key whose role
// may not read logs. Login routes are absent, as an API key never logs in.
func signozKeyServer(t *testing.T, forbidden bool) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/v1/version":
			_, _ = fmt.Fprint(w, `{"version":"v0.145.0"}`)
		case "/api/v3/autocomplete/attribute_keys":
			switch {
			case r.Header.Get("SIGNOZ-API-KEY") != testSignozAPIKey:
				w.WriteHeader(http.StatusUnauthorized)
				_, _ = fmt.Fprint(w, `{"status":"error","error":{"type":"unauthenticated","message":"unauthenticated"}}`)
			case forbidden:
				w.WriteHeader(http.StatusForbidden)
				_, _ = fmt.Fprint(w, `{"status":"error","error":{"type":"forbidden","message":"forbidden"}}`)
			default:
				_, _ = fmt.Fprint(w, `{"status":"success","data":{"attributeKeys":[]}}`)
			}
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func signozKeyCfg(url, key string) []core.IntegrationConfigValue {
	return []core.IntegrationConfigValue{
		{Name: "signoz_url", Value: url},
		{Name: "signoz_mode", Value: SignozModeSelfHosted},
		{Name: SignozAuthTypeKey, Value: SignozAuthTypeAPIKey},
		{Name: "signoz_api_key", Value: key},
	}
}

func TestSignoz_ValidateConfig_APIKey(t *testing.T) {
	cases := []struct {
		name      string
		forbidden bool
		key       string
		wantErr   string
	}{
		{"valid key", false, testSignozAPIKey, ""},
		{"wrong key", false, "nope", "invalid SigNoz API key"},
		{"key whose role may not read logs", true, testSignozAPIKey, "not allowed to read logs"},
		{"missing key", false, "", "signoz_api_key is required"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := signozKeyServer(t, tc.forbidden)
			errs := Signoz{}.ValidateConfig(nil, signozKeyCfg(srv.URL, tc.key), "acc-1")
			if tc.wantErr == "" {
				assert.Empty(t, errs)
				return
			}
			require.Len(t, errs, 1)
			assert.Contains(t, errs[0].Error(), tc.wantErr)
		})
	}
}

// An API-key row does not need a username/password, and a password row that
// still stores a stale key (switched back from API key) validates by password.
func TestSignoz_ValidateConfig_UsesOnlyTheSelectedAuthType(t *testing.T) {
	keySrv := signozKeyServer(t, false)
	assert.Empty(t, Signoz{}.ValidateConfig(nil, signozKeyCfg(keySrv.URL, testSignozAPIKey), "acc-1"))

	pwSrv := signozServer(t, "v0.145.0", false, true)
	cfg := append(signozCfg(pwSrv.URL, testSignozPassword, SignozModeSelfHosted),
		core.IntegrationConfigValue{Name: SignozAuthTypeKey, Value: SignozAuthTypePassword},
		core.IntegrationConfigValue{Name: "signoz_api_key", Value: "stale-revoked-key"})
	assert.Empty(t, Signoz{}.ValidateConfig(nil, cfg, "acc-1"))
}

func TestSignoz_ValidateConfig_UnknownAuthType(t *testing.T) {
	cfg := append(signozCfg("http://signoz.example.com", testSignozPassword, SignozModeSelfHosted),
		core.IntegrationConfigValue{Name: SignozAuthTypeKey, Value: "oauth"})
	errs := Signoz{}.ValidateConfig(nil, cfg, "acc-1")
	require.Len(t, errs, 1)
	assert.Contains(t, errs[0].Error(), "signoz_auth_type must be")
}

// The failure from #41200: the tunnel in front of SigNoz is down, so every
// route answers 502 with the proxy's own HTML page. The form's error popup
// showed that page's source; it must now read as a reachability problem, on
// both auth types.
func TestSignoz_ValidateConfig_ProxyErrorPageIsReadable(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusBadGateway)
		_, _ = fmt.Fprint(w, `<!DOCTYPE html> <html class="h-full" lang="en-US" dir="ltr"> <head> `+
			`<meta charset="utf-8"> <meta name="viewport" content="width=device-width, initial-scale=1"> `+
			`<link rel="preload" href="/static/fonts/Euclid.woff2" as="font"> </head> `+
			`<body>ERR_NGROK_8012</body> </html>`)
	}))
	t.Cleanup(srv.Close)

	cases := []struct {
		name string
		cfg  []core.IntegrationConfigValue
	}{
		{"password auth", signozCfg(srv.URL, testSignozPassword, SignozModeSelfHosted)},
		{"API key auth", signozKeyCfg(srv.URL, testSignozAPIKey)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			errs := Signoz{}.ValidateConfig(nil, tc.cfg, "acc-1")
			require.Len(t, errs, 1)
			msg := errs[0].Error()
			t.Logf("popup shows: %s", msg)
			assert.NotContains(t, msg, "<", "the popup carries page markup")
			assert.NotContains(t, msg, "DOCTYPE")
			assert.Contains(t, msg, "SigNoz is not reachable (HTTP 502)")
		})
	}
}
