package integrations

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"nudgebee/services/integrations/core"
	"nudgebee/services/integrations/orionclient"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func orionValues(overrides map[string]string) []core.IntegrationConfigValue {
	values := map[string]string{
		OrionURLKey:            "https://orion.example.com:17774",
		OrionUsernameKey:       "svc_nudgebee",
		OrionPasswordKey:       "secret",
		OrionConnectionModeKey: OrionConnectionModeDirect,
	}
	for k, v := range overrides {
		if v == "<omit>" {
			delete(values, k)
			continue
		}
		values[k] = v
	}
	out := make([]core.IntegrationConfigValue, 0, len(values))
	for k, v := range values {
		out = append(out, core.IntegrationConfigValue{Name: k, Value: v})
	}
	return out
}

func TestOrionValidateConfig_RequiresTheFieldsAQueryNeeds(t *testing.T) {
	for _, c := range []struct {
		name  string
		over  map[string]string
		field string
	}{
		{"no url", map[string]string{OrionURLKey: "<omit>"}, OrionURLKey},
		{"no username", map[string]string{OrionUsernameKey: ""}, OrionUsernameKey},
		{"no password", map[string]string{OrionPasswordKey: ""}, OrionPasswordKey},
	} {
		t.Run(c.name, func(t *testing.T) {
			errs := Orion{}.ValidateConfig(nil, orionValues(c.over), "acct-1")
			require.NotEmpty(t, errs)
			assert.Contains(t, joinErrs(errs), c.field)
		})
	}
}

// A forager-reached Orion is on a private address that Nudgebee cannot dial, so
// neither the egress check nor the live probe may run against it. The pre-save
// test goes through the forager instead, exactly as Prometheus's vm_agent mode does.
func TestOrionValidateConfig_ForagerModeSkipsTheProbe(t *testing.T) {
	errs := Orion{}.ValidateConfig(nil, orionValues(map[string]string{
		OrionConnectionModeKey: OrionConnectionModeVMAgent,
		OrionURLKey:            "https://10.0.0.5:17774",
	}), "acct-1")
	assert.Empty(t, errs)

	// The URL is still shape-checked, so a typo is caught at save time.
	errs = Orion{}.ValidateConfig(nil, orionValues(map[string]string{
		OrionConnectionModeKey: OrionConnectionModeVMAgent,
		OrionURLKey:            "10.0.0.5:17774",
	}), "acct-1")
	require.NotEmpty(t, errs)
	assert.Contains(t, joinErrs(errs), "http or https")
}

func TestNewOrionConfig_ReadsTheStoredShape(t *testing.T) {
	cfg := NewOrionConfig(map[string]string{
		OrionURLKey:            " https://orion.example.com:17774 ",
		OrionUsernameKey:       " svc ",
		OrionPasswordKey:       "secret",
		OrionTLSSkipVerifyKey:  "true",
		OrionConnectionModeKey: OrionConnectionModeVMAgent,
	})
	assert.Equal(t, "https://orion.example.com:17774", cfg.BaseURL)
	assert.Equal(t, "svc", cfg.Username)
	assert.Equal(t, "secret", cfg.Password)
	assert.True(t, cfg.TLSSkipVerify, "SWIS ships a self-signed certificate")
	assert.True(t, cfg.ViaForager)
}

// Shapes below are what a live SolarWinds Platform 2026.4.0 returned.
func TestOrionClientQuery_ParsesSWISRows(t *testing.T) {
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, orionclient.QueryPath, r.URL.Path)
		user, pass, ok := r.BasicAuth()
		assert.True(t, ok)
		assert.Equal(t, "svc", user)
		assert.Equal(t, "pw", pass)
		require.NoError(t, json.NewDecoder(r.Body).Decode(&gotBody))
		_, _ = w.Write([]byte(`{"results":[{"NodeID":1,"Caption":"ORION-TESTBED","AvgLoad":16}]}`))
	}))
	defer srv.Close()

	cfg := orionclient.Config{BaseURL: srv.URL, Username: "svc", Password: "pw"}
	rows, err := cfg.Query(context.Background(), "SELECT NodeID FROM Orion.Nodes WHERE NodeID = @id", map[string]any{"id": 1})
	require.NoError(t, err)
	require.Len(t, rows, 1)
	assert.Equal(t, "ORION-TESTBED", rows[0]["Caption"])

	// Parameters travel to SWIS rather than being pasted into the statement.
	assert.Equal(t, map[string]any{"id": float64(1)}, gotBody["parameters"])
}

func TestOrionClientQuery_ExplainsFailuresInOrionsTerms(t *testing.T) {
	for _, c := range []struct {
		name   string
		status int
		body   string
		expect string
	}{
		{"swis message wins", 400, `{"Message":"Entity Orion.Nope not found"}`, "Orion.Nope not found"},
		{"bad credentials", 401, ``, "rejected the credentials"},
		{"no read access", 403, ``, "read access"},
		{"wrong port", 404, ``, "17774"},
		{"web console html", 200, `<html>login</html>`, "did not return SWIS JSON"},
	} {
		t.Run(c.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(c.status)
				_, _ = w.Write([]byte(c.body))
			}))
			defer srv.Close()

			_, err := orionclient.Config{BaseURL: srv.URL}.Query(context.Background(), "SELECT 1", nil)
			require.Error(t, err)
			assert.Contains(t, err.Error(), c.expect)
		})
	}
}

// Without a transport a forager-mode config must refuse rather than dial a host
// that is only reachable from inside the customer's network.
func TestOrionClientQuery_ForagerModeRefusesToDialDirectly(t *testing.T) {
	_, err := orionclient.Config{BaseURL: "https://10.0.0.5:17774", ViaForager: true}.Query(context.Background(), "SELECT 1", nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "refusing to dial")
}

func joinErrs(errs []error) string {
	parts := make([]string, 0, len(errs))
	for _, e := range errs {
		parts = append(parts, e.Error())
	}
	return strings.Join(parts, "; ")
}

// SolarWinds' own SWIS certificate has a negative serial number, which Go refuses
// to parse. That happens before verification, so tls_skip_verify cannot rescue it
// and the raw error says nothing useful. Confirmed against a live 2026.4.0 server
// (CN=SolarWinds-Orion, serial -4EAB6A6A1879596BBFA2A5BACD295E6F).
func TestOrionClientQuery_ExplainsSolarWindsSelfSignedCertificate(t *testing.T) {
	cfg := orionclient.Config{BaseURL: "https://127.0.0.1:1", TLSSkipVerify: true}
	_, err := cfg.Query(context.Background(), "SELECT 1", nil)
	require.Error(t, err)

	wrapped := orionclient.ExplainTLSFailureForTest(
		errors.New(`tls: failed to parse certificate from server: x509: negative serial number`))
	assert.Contains(t, wrapped.Error(), "Replace the SWIS certificate")
	assert.Contains(t, wrapped.Error(), "before verification")

	// Any other TLS error is passed through untouched.
	other := errors.New("tls: handshake failure")
	assert.Equal(t, other, orionclient.ExplainTLSFailureForTest(other))
}
