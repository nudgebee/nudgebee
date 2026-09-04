package integrations

import (
	"strings"
	"testing"

	"nudgebee/services/integrations/core"
)

func TestNormalizeCubeAPMURL(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"plain", "http://cube:3140", "http://cube:3140"},
		{"trailing slash", "http://cube:3140/", "http://cube:3140"},
		{"pasted browser path", "http://cube:3140/logs/explorer?q=1", "http://cube:3140"},
		{"whitespace", "  https://cube.example.com:3140  ", "https://cube.example.com:3140"},
		{"port preserved", "https://cube.example.com:8443", "https://cube.example.com:8443"},
		{"empty", "", ""},
		{"no scheme falls through", "cube:3140", "cube:3140"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := normalizeCubeAPMURL(tt.in); got != tt.want {
				t.Errorf("normalizeCubeAPMURL(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestDeriveCubeAPMAdminURL(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"standard port pair", "http://cube:3140", "http://cube:3199"},
		{"https", "https://cube.example.com:3140", "https://cube.example.com:3199"},
		// A non-standard port means we cannot know where the admin server is;
		// guessing would silently target whatever else is listening.
		{"non-standard port refuses", "http://cube:8080", ""},
		{"no port refuses", "http://cube", ""},
		{"empty", "", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := deriveCubeAPMAdminURL(tt.in); got != tt.want {
				t.Errorf("deriveCubeAPMAdminURL(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestCubeAPMRequestHeaders(t *testing.T) {
	t.Run("omits Authorization when no token", func(t *testing.T) {
		headers := CubeAPMRequestHeaders("", "application/json")
		if _, present := headers["Authorization"]; present {
			t.Error("Authorization header must be absent when no token is configured — " +
				"an empty Bearer is a malformed credential, not an absent one")
		}
		if headers["Content-Type"] != "application/json" {
			t.Errorf("Content-Type = %q, want application/json", headers["Content-Type"])
		}
	})

	t.Run("sets bearer token", func(t *testing.T) {
		headers := CubeAPMRequestHeaders("  secret  ", "")
		if headers["Authorization"] != "Bearer secret" {
			t.Errorf("Authorization = %q, want %q", headers["Authorization"], "Bearer secret")
		}
		if _, present := headers["Content-Type"]; present {
			t.Error("Content-Type must be absent when not requested")
		}
	})
}

func TestCubeAPMValidateConfig(t *testing.T) {
	values := func(pairs ...string) []core.IntegrationConfigValue {
		var out []core.IntegrationConfigValue
		for i := 0; i < len(pairs); i += 2 {
			out = append(out, core.IntegrationConfigValue{Name: pairs[i], Value: pairs[i+1]})
		}
		return out
	}

	tests := []struct {
		name        string
		config      []core.IntegrationConfigValue
		wantErrs    int
		wantContain string
	}{
		{
			name:     "valid minimal",
			config:   values("cubeapm_url", "http://cube:3140"),
			wantErrs: 0,
		},
		{
			name:     "valid with admin url",
			config:   values("cubeapm_url", "http://cube:3140", "cubeapm_admin_url", "http://cube:3199"),
			wantErrs: 0,
		},
		{
			name:        "missing url",
			config:      values(),
			wantErrs:    1,
			wantContain: "cubeapm_url is required",
		},
		{
			name:        "missing scheme",
			config:      values("cubeapm_url", "cube:3140"),
			wantErrs:    1,
			wantContain: "must start with http://",
		},
		{
			name:        "url with path",
			config:      values("cubeapm_url", "http://cube:3140/logs"),
			wantErrs:    1,
			wantContain: "remove the path",
		},
		{
			name:        "admin url with path",
			config:      values("cubeapm_url", "http://cube:3140", "cubeapm_admin_url", "http://cube:3199/api"),
			wantErrs:    1,
			wantContain: "cubeapm_admin_url",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			errs := CubeAPM{}.ValidateConfig(nil, tt.config, "acct")
			if len(errs) != tt.wantErrs {
				t.Fatalf("got %d errors %v, want %d", len(errs), errs, tt.wantErrs)
			}
			if tt.wantContain != "" && !strings.Contains(errs[0].Error(), tt.wantContain) {
				t.Errorf("error %q does not mention %q", errs[0], tt.wantContain)
			}
		})
	}
}

func TestCubeAPMConfigSchema(t *testing.T) {
	schema := CubeAPM{}.ConfigSchema()

	if len(schema.Required) != 1 || schema.Required[0] != "cubeapm_url" {
		t.Errorf("Required = %v, want only cubeapm_url — the tokens are optional because "+
			"CubeAPM's query port is unauthenticated by default", schema.Required)
	}
	if !schema.Testable {
		t.Error("schema must be Testable; CubeAPM implements TestConnection")
	}

	for _, secret := range []string{"cubeapm_token", "cubeapm_admin_token"} {
		prop, ok := schema.Properties[secret]
		if !ok {
			t.Fatalf("schema is missing %s", secret)
		}
		if !prop.IsEncrypted {
			t.Errorf("%s must be marked IsEncrypted", secret)
		}
	}
}

func TestCubeAPMIntegrationIdentity(t *testing.T) {
	m := CubeAPM{}
	if m.Name() != IntegrationCubeAPM {
		t.Errorf("Name() = %q, want %q", m.Name(), IntegrationCubeAPM)
	}
	if m.Category() != core.IntegrationCategoryObservabilityPlatform {
		t.Errorf("Category() = %q, want observability_platform", m.Category())
	}

	// The dispatchers in observability/service.go key on this exact string.
	if IntegrationCubeAPM != "cubeapm" {
		t.Errorf("IntegrationCubeAPM = %q; the log/trace/metric source dispatchers "+
			"match on the literal \"cubeapm\"", IntegrationCubeAPM)
	}
}

func TestCubeAPMRegisteredInRegistry(t *testing.T) {
	for _, name := range []string{IntegrationCubeAPM, IntegrationCubeAPMWebhook} {
		if _, ok := core.GetIntegration(name); !ok {
			t.Errorf("integration %q is not registered; its init() must call core.RegisterIntegration", name)
		}
	}
}

// ListIntegrationConfigs applies its cloud_account_id filter only when accountId
// is non-empty, so an empty one silently resolves to another account's CubeAPM
// integration — different URL, different token — and reports success.
func TestGetCubeAPMConfigsRequiresAccountID(t *testing.T) {
	_, err := GetCubeAPMConfigs(nil, "")
	if err == nil {
		t.Fatal("expected an error for an empty account id")
	}
	if !strings.Contains(err.Error(), "account_id is required") {
		t.Errorf("error = %v", err)
	}
}
