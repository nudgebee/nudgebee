# Grafana Tempo Trace Integration Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Connect Grafana Tempo as a trace provider so its spans, waterfall and grouped per-operation stats show in the Kubernetes Traces tab labelled "Grafana Tempo".

**Architecture:** A new `grafana_tempo` integration (schema, validation, test connection, stored config) in `api-server/services/integrations`, and a `GrafanaTempoTraceSource` in `api-server/services/observability` that talks to Tempo's HTTP API directly: TraceQL search for listing, `/api/v2/traces/<id>` for the waterfall, TraceQL-metrics instant queries for grouping, tag APIs for labels. The source is split into four focused files (HTTP client + windows, TraceQL builder, response converters, the source itself). The frontend enables the existing disabled tile.

**Tech Stack:** Go 1.x (`testify`, `net/http/httptest`), Atlas migrations (Postgres), Next.js/React app (jest).

**Spec:** `docs/superpowers/specs/2026-09-29-grafana-tempo-integration-design.md` · **Issue:** #39485 · **Branch:** `feature/grafana-tempo-integration`

## Global Constraints

- Integration type string is exactly `grafana_tempo`; category `observability_platform`.
- Config keys: `tempo_url`, `tempo_auth_type` (`none`|`basic`|`bearer`, default `none`), `tempo_username`, `tempo_password` (encrypted), `tempo_token` (encrypted), `tempo_tenant_id` (sent as `X-Scope-OrgID`), `tempo_insecure_skip_verify`; the only default flag is `core.DefaultTraceProvider`.
- **No `getMetricsSource` case** for `grafana_tempo` (decision record §8 of the spec).
- Span `TraceSource` value is `"grafana_tempo"`; status codes are emitted as `STATUS_CODE_OK|ERROR|UNSET` (the UI filters on these strings).
- Search window ≤ 168h, metrics (grouping) window ≤ 24h — clamp to the most recent part, never fail.
- Listing counts are the `-1` estimate (`countTracesByTraceEstimate`); span cap 1000; request timeout 30 s.
- Grouping `by (…)` uses **at most 4** attributes (Tempo rejects 5 with quantiles): `resource.service.name, resource.k8s.namespace.name, name, span.net.peer.name`. Durations from TraceQL metrics are **seconds** → convert to ns.
- Search returns hex ids and unscoped attribute keys; `/api/v2/traces/<id>` returns **base64** ids — decode to hex.
- Commits: `type(nb-39485): …`, **no `Co-Authored-By` trailer**, no Claude footer in the PR.
- Validation before every commit: `cd api-server/services && make validate` (Go tasks) / `cd app && npm run lint2 && npm run test` (frontend task).

## Review Focus

1. **Paging past the cap** — offset ≥ 1000 must return an empty page, not a repeat of page 1 → test in Task 6 (`TestQueryTempoSpans_OffsetBeyondCapIsEmpty`).
2. **"Last 7 days" / "Last 30 days" picker** — listing must still answer (search clamped to 168h), grouping clamped to 24h → tests in Task 3 (`TestTempoTimeWindow_*`) and Task 6 (`TestQueryTempoSpans_ClampsWindow`).
3. **Filter values with quotes/regex metacharacters** (`a"b`, `/api/v1/*`) must not break TraceQL → tests in Task 4 (`TestTempoCondition_Escaping`).
4. **Status filter from the UI** (`status_code _contains STATUS_CODE_ERROR`) must become the TraceQL enum `status = error`, not a string match → test in Task 4 (`TestTempoCondition_Status`).
5. **Series missing a group label** (a leaf service has no `span.net.peer.name`) must merge into one row with an empty destination, not be dropped → test in Task 7 (`TestMergeTempoGroups_MissingLabel`).

---

### Task 1: Register the integration type (migration) and tidy the spec

**Files:**
- Create: `api-server/migrations/migrations/app/<ts>_V<N>_add_grafana_tempo_integration_type.up.sql` / `.down.sql` (scaffolded)
- Modify: `api-server/migrations/migrations/app/atlas.sum` (regenerated)
- Modify: `docs/superpowers/specs/2026-09-29-grafana-tempo-integration-design.md` (§3.5 sample-query bullet, §4 unknown-field line)

- [ ] **Step 1: Scaffold**

Run: `cd api-server/migrations && ./new-migration.sh add_grafana_tempo_integration_type`
Expected: two new files named `<unix-ms>_V<next>_add_grafana_tempo_integration_type.{up,down}.sql` and an updated `atlas.sum`.

- [ ] **Step 2: Fill the up file**

```sql
-- Register Grafana Tempo as an observability platform (traces only), so it can be
-- created from Admin -> Integrations. Issue #39485.
INSERT INTO "public"."integration_types"("name", "category", "description")
VALUES ('grafana_tempo', 'observability_platform', 'Grafana Tempo distributed traces')
ON CONFLICT ("name") DO NOTHING;
```

- [ ] **Step 3: Fill the down file**

```sql
DELETE FROM "public"."integration_types" WHERE "name" = 'grafana_tempo';
```

- [ ] **Step 4: Re-hash and validate**

Run: `cd api-server/migrations && ./new-migration.sh --regen-sum && python validate_migrations.py`
Expected: `atlas.sum` updated; validator exits 0 (an out-of-order warning is acceptable, an error is not).

- [ ] **Step 5: Apply to the local database**

Run:
```bash
cd api-server/migrations && atlas migrate apply -c file://atlas.hcl --env default \
  --url 'postgres://postgres:postgrespassword@localhost:5432/nudgebee?sslmode=disable' --tx-mode file
docker compose exec postgres psql -U postgres -d nudgebee -At -c "select name, category from integration_types where name='grafana_tempo'"
```
Expected: `grafana_tempo|observability_platform`.

- [ ] **Step 6: Tidy the spec** — in §3.5 delete the bullet "Traces-tab Code-mode sample query …" (the Traces tab has no per-provider sample queries; only `QueryModeSwitcher` for logs/metrics does). In §4 replace "Unknown filter field → stripped with a debug log" with "Unknown canonical field → 400 naming it (scoped `resource.*`/`span.*` names pass through); the named unsupported fields are stripped".

- [ ] **Step 7: Commit**

```bash
git add api-server/migrations/migrations/app/ docs/superpowers/specs/2026-09-29-grafana-tempo-integration-design.md
git commit -m "feat(nb-39485): register the grafana_tempo integration type"
```

---

### Task 2: `grafana_tempo` integration — schema, validation, test connection, stored config

**Files:**
- Create: `api-server/services/integrations/grafana_tempo.go`
- Test: `api-server/services/integrations/grafana_tempo_test.go`

**Interfaces:**
- Produces:
  - `const IntegrationGrafanaTempo = "grafana_tempo"` and the `GrafanaTempoConfig*` / `GrafanaTempoAuth*` constants below
  - `type GrafanaTempoConfig struct { URL, AuthType, Username, Password, Token, TenantID string; InsecureSkipVerify bool }`
  - `func (c GrafanaTempoConfig) Headers() map[string]string`
  - `func GetGrafanaTempoConfig(sc *security.RequestContext, accountId string) (GrafanaTempoConfig, error)`
  - `func grafanaTempoConfigFromValues(config []core.IntegrationConfigValue) (GrafanaTempoConfig, error)` (package-private)

- [ ] **Step 1: Write the failing tests**

```go
package integrations

import (
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"testing"

	"nudgebee/services/integrations/core"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func tempoValues(kv map[string]string) []core.IntegrationConfigValue {
	out := make([]core.IntegrationConfigValue, 0, len(kv))
	for k, v := range kv {
		out = append(out, core.IntegrationConfigValue{Name: k, Value: v})
	}
	return out
}

func TestGrafanaTempo_SchemaDeclaresOnlyTheTraceDefault(t *testing.T) {
	s := GrafanaTempo{}.ConfigSchema()
	assert.True(t, s.Testable)
	assert.ElementsMatch(t, []string{GrafanaTempoConfigURL, GrafanaTempoConfigAuthType}, s.Required)
	assert.Contains(t, s.Properties, core.DefaultTraceProvider)
	assert.NotContains(t, s.Properties, core.DefaultMetricsProvider)
	assert.NotContains(t, s.Properties, core.DefaultLogProvider)
	assert.True(t, s.Properties[GrafanaTempoConfigPassword].IsEncrypted)
	assert.True(t, s.Properties[GrafanaTempoConfigToken].IsEncrypted)
	assert.Equal(t, IntegrationGrafanaTempo, GrafanaTempo{}.Name())
	assert.Equal(t, core.IntegrationCategoryObservabilityPlatform, GrafanaTempo{}.Category())
}

func TestGrafanaTempo_ValidateConfig(t *testing.T) {
	cases := []struct {
		name    string
		values  map[string]string
		wantErr string
	}{
		{"none auth ok", map[string]string{GrafanaTempoConfigURL: "http://localhost:3200", GrafanaTempoConfigAuthType: "none"}, ""},
		{"path prefix ok", map[string]string{GrafanaTempoConfigURL: "https://gw.example.com/tempo", GrafanaTempoConfigAuthType: "none"}, ""},
		{"missing url", map[string]string{GrafanaTempoConfigAuthType: "none"}, "tempo_url is required"},
		{"bad scheme", map[string]string{GrafanaTempoConfigURL: "localhost:3200", GrafanaTempoConfigAuthType: "none"}, "must start with http"},
		{"basic needs user", map[string]string{GrafanaTempoConfigURL: "http://t", GrafanaTempoConfigAuthType: "basic", GrafanaTempoConfigPassword: "p"}, "tempo_username is required"},
		{"basic needs password", map[string]string{GrafanaTempoConfigURL: "http://t", GrafanaTempoConfigAuthType: "basic", GrafanaTempoConfigUsername: "u"}, "tempo_password is required"},
		{"bearer needs token", map[string]string{GrafanaTempoConfigURL: "http://t", GrafanaTempoConfigAuthType: "bearer"}, "tempo_token is required"},
		{"unknown auth", map[string]string{GrafanaTempoConfigURL: "http://t", GrafanaTempoConfigAuthType: "sigv4"}, "tempo_auth_type must be"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			errs := GrafanaTempo{}.ValidateConfig(nil, tempoValues(tc.values), "acc")
			if tc.wantErr == "" {
				assert.Empty(t, errs)
				return
			}
			require.NotEmpty(t, errs)
			assert.Contains(t, errs[0].Error(), tc.wantErr)
		})
	}
}

func TestGrafanaTempoConfig_Headers(t *testing.T) {
	basic := GrafanaTempoConfig{AuthType: GrafanaTempoAuthBasic, Username: "u", Password: "p", TenantID: "team-a"}
	assert.Equal(t, "Basic "+base64.StdEncoding.EncodeToString([]byte("u:p")), basic.Headers()["Authorization"])
	assert.Equal(t, "team-a", basic.Headers()["X-Scope-OrgID"])

	bearer := GrafanaTempoConfig{AuthType: GrafanaTempoAuthBearer, Token: "tok"}
	assert.Equal(t, "Bearer tok", bearer.Headers()["Authorization"])

	none := GrafanaTempoConfig{AuthType: GrafanaTempoAuthNone}
	assert.NotContains(t, none.Headers(), "Authorization")
	assert.NotContains(t, none.Headers(), "X-Scope-OrgID")
}

func TestGrafanaTempo_TestConnection(t *testing.T) {
	cases := []struct {
		name     string
		echo     int
		tags     int
		tagsBody string
		wantErr  string
	}{
		{"ok", 200, 200, `{"scopes":[]}`, ""},
		{"auth rejected", 401, 200, "", "rejected the credentials"},
		{"not a tempo endpoint", 404, 200, "", "path prefix"},
		{"tenant missing", 200, 401, "no org id", "tempo_tenant_id"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/api/echo":
					w.WriteHeader(tc.echo)
					_, _ = w.Write([]byte("echo"))
				case "/api/v2/search/tags":
					w.WriteHeader(tc.tags)
					_, _ = w.Write([]byte(tc.tagsBody))
				default:
					w.WriteHeader(404)
				}
			}))
			defer srv.Close()
			err := GrafanaTempo{}.TestConnection(nil, tempoValues(map[string]string{
				GrafanaTempoConfigURL: srv.URL, GrafanaTempoConfigAuthType: "none",
			}), "acc")
			if tc.wantErr == "" {
				assert.NoError(t, err)
				return
			}
			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.wantErr)
		})
	}
}

func TestGrafanaTempo_TestConnectionSendsTenantAndAuth(t *testing.T) {
	var gotOrg, gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotOrg, gotAuth = r.Header.Get("X-Scope-OrgID"), r.Header.Get("Authorization")
		_, _ = w.Write([]byte(`{"scopes":[]}`))
	}))
	defer srv.Close()
	err := GrafanaTempo{}.TestConnection(nil, tempoValues(map[string]string{
		GrafanaTempoConfigURL: srv.URL + "/", GrafanaTempoConfigAuthType: "bearer",
		GrafanaTempoConfigToken: "tok", GrafanaTempoConfigTenantID: "team-a",
	}), "acc")
	require.NoError(t, err)
	assert.Equal(t, "team-a", gotOrg)
	assert.Equal(t, "Bearer tok", gotAuth)
}

func TestGetGrafanaTempoConfig_RequiresAccount(t *testing.T) {
	_, err := GetGrafanaTempoConfig(nil, "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "account_id is required")
}
```

- [ ] **Step 2: Run to verify they fail**

Run: `cd api-server/services && go test ./integrations/ -run 'GrafanaTempo' -count=1`
Expected: FAIL — `undefined: GrafanaTempo`.

- [ ] **Step 3: Implement `grafana_tempo.go`**

```go
package integrations

import (
	"encoding/base64"
	"fmt"
	"io"
	"net/http"
	"nudgebee/services/common"
	"nudgebee/services/integrations/core"
	"nudgebee/services/security"
	"strconv"
	"strings"
	"time"
)

func init() {
	core.RegisterIntegration(GrafanaTempo{})
}

// IntegrationGrafanaTempo is the Grafana Tempo trace backend. Traces only: Tempo stores
// no logs, and its span metrics are deliberately not exposed as a metrics source (see
// docs/architecture-decisions.md, "Grafana Tempo is a trace source only").
const IntegrationGrafanaTempo = "grafana_tempo"

const (
	GrafanaTempoConfigURL      = "tempo_url"
	GrafanaTempoConfigAuthType = "tempo_auth_type"
	GrafanaTempoConfigUsername = "tempo_username"
	GrafanaTempoConfigPassword = "tempo_password"
	GrafanaTempoConfigToken    = "tempo_token"
	GrafanaTempoConfigTenantID = "tempo_tenant_id"
	GrafanaTempoConfigInsecure = "tempo_insecure_skip_verify"
)

// Tempo has no authentication of its own; "none" is the common case, with basic or
// bearer when it sits behind a gateway (Grafana Cloud, an auth proxy, an ingress).
const (
	GrafanaTempoAuthNone   = "none"
	GrafanaTempoAuthBasic  = "basic"
	GrafanaTempoAuthBearer = "bearer"
)

type GrafanaTempo struct{}

func (GrafanaTempo) Name() string { return IntegrationGrafanaTempo }

func (GrafanaTempo) Category() core.IntegrationCategory {
	return core.IntegrationCategoryObservabilityPlatform
}

func (GrafanaTempo) ConfigSchema() core.IntegrationSchema {
	basic := map[string]any{GrafanaTempoConfigAuthType: GrafanaTempoAuthBasic}
	bearer := map[string]any{GrafanaTempoConfigAuthType: GrafanaTempoAuthBearer}
	return core.IntegrationSchema{
		Type:        core.ToolSchemaTypeObject,
		Required:    []string{GrafanaTempoConfigURL, GrafanaTempoConfigAuthType},
		Testable:    true,
		Description: "Connects to Grafana Tempo over its HTTP query API to show distributed traces.",
		Properties: map[string]core.IntegrationSchemaProperty{
			core.IntegrationConfigName: {
				Type: core.ToolSchemaTypeString, Description: "Custom name for this Grafana Tempo integration.",
				Default: "", Priority: 100,
			},
			core.AccountId: {
				Type: core.ToolSchemaTypeArray, Description: "Associated account(s) for this integration.",
				Default: "", AutoGenerateFunc: "listAccounts", Priority: 95,
			},
			GrafanaTempoConfigURL: {
				Type: core.ToolSchemaTypeString,
				Description: "Base URL of the Tempo query API (e.g. http://tempo.monitoring:3200). Include any " +
					"path prefix a gateway serves it under (e.g. https://gw.example.com/tempo).",
				Priority: 88, IsTestable: true,
			},
			GrafanaTempoConfigAuthType: {
				Type:        core.ToolSchemaTypeString,
				Description: "Tempo has no built-in auth; use basic or bearer only when a gateway in front of it requires them.",
				Enum:        []any{GrafanaTempoAuthNone, GrafanaTempoAuthBasic, GrafanaTempoAuthBearer},
				Default:     GrafanaTempoAuthNone, Priority: 86, IsTestable: true,
			},
			GrafanaTempoConfigUsername: {
				Type: core.ToolSchemaTypeString, Description: "Username for Basic auth.",
				ShowWhen: basic, RequiredWhen: basic, Priority: 84, IsTestable: true,
			},
			GrafanaTempoConfigPassword: {
				Type: core.ToolSchemaTypeString, Description: "Password for Basic auth.", IsEncrypted: true,
				ShowWhen: basic, RequiredWhen: basic, Priority: 82, IsTestable: true,
			},
			GrafanaTempoConfigToken: {
				Type: core.ToolSchemaTypeString, Description: "Token sent as Authorization: Bearer.", IsEncrypted: true,
				ShowWhen: bearer, RequiredWhen: bearer, Priority: 80, IsTestable: true,
			},
			GrafanaTempoConfigTenantID: {
				Type: core.ToolSchemaTypeString,
				Description: "Tenant for multi-tenant Tempo, sent as the X-Scope-OrgID header. Leave empty for single-tenant.",
				Default: "", Priority: 70, IsTestable: true,
			},
			GrafanaTempoConfigInsecure: {
				Type: core.ToolSchemaTypeBoolean, Description: "Skip TLS certificate verification (self-signed certificates).",
				Default: false, Priority: 30, IsTestable: true,
			},
			core.DefaultTraceProvider: {
				Type: core.ToolSchemaTypeBoolean, Description: "Make Grafana Tempo the default Trace Provider",
				Default: false, Priority: 13,
			},
		},
	}
}

func grafanaTempoReadConfig(config []core.IntegrationConfigValue) map[string]core.IntegrationConfigValue {
	out := make(map[string]core.IntegrationConfigValue, len(config))
	for _, c := range config {
		out[c.Name] = c
	}
	return out
}

func grafanaTempoAuthType(raw string) string {
	v := strings.ToLower(strings.TrimSpace(raw))
	if v == "" {
		return GrafanaTempoAuthNone
	}
	return v
}

// normalizeGrafanaTempoURL keeps any path prefix (unlike Splunk, a gateway commonly
// serves Tempo under one) and only trims whitespace and trailing slashes.
func normalizeGrafanaTempoURL(raw string) string {
	return strings.TrimRight(strings.TrimSpace(raw), "/")
}

func (GrafanaTempo) ValidateConfig(sc *security.SecurityContext, config []core.IntegrationConfigValue, accountId string) []error {
	values := grafanaTempoReadConfig(config)
	url := normalizeGrafanaTempoURL(values[GrafanaTempoConfigURL].Value)

	var errs []error
	switch {
	case url == "":
		errs = append(errs, fmt.Errorf("%s is required", GrafanaTempoConfigURL))
	case !strings.HasPrefix(url, "http://") && !strings.HasPrefix(url, "https://"):
		errs = append(errs, fmt.Errorf("%s must start with http:// or https:// (got %q)", GrafanaTempoConfigURL, url))
	}

	switch auth := grafanaTempoAuthType(values[GrafanaTempoConfigAuthType].Value); auth {
	case GrafanaTempoAuthNone:
	case GrafanaTempoAuthBasic:
		if strings.TrimSpace(values[GrafanaTempoConfigUsername].Value) == "" {
			errs = append(errs, fmt.Errorf("%s is required when %s is %q", GrafanaTempoConfigUsername, GrafanaTempoConfigAuthType, auth))
		}
		if values[GrafanaTempoConfigPassword].Value == "" {
			errs = append(errs, fmt.Errorf("%s is required when %s is %q", GrafanaTempoConfigPassword, GrafanaTempoConfigAuthType, auth))
		}
	case GrafanaTempoAuthBearer:
		if strings.TrimSpace(values[GrafanaTempoConfigToken].Value) == "" {
			errs = append(errs, fmt.Errorf("%s is required when %s is %q", GrafanaTempoConfigToken, GrafanaTempoConfigAuthType, auth))
		}
	default:
		errs = append(errs, fmt.Errorf("%s must be %q, %q or %q (got %q)", GrafanaTempoConfigAuthType,
			GrafanaTempoAuthNone, GrafanaTempoAuthBasic, GrafanaTempoAuthBearer, auth))
	}
	return errs
}

// GrafanaTempoConfig is the resolved per-account Tempo connection.
type GrafanaTempoConfig struct {
	URL                string
	AuthType           string
	Username           string
	Password           string
	Token              string
	TenantID           string
	InsecureSkipVerify bool
}

// Headers returns the auth and tenant headers every Tempo request needs.
func (c GrafanaTempoConfig) Headers() map[string]string {
	h := map[string]string{"Accept": "application/json"}
	switch c.AuthType {
	case GrafanaTempoAuthBasic:
		h["Authorization"] = "Basic " + base64.StdEncoding.EncodeToString([]byte(c.Username+":"+c.Password))
	case GrafanaTempoAuthBearer:
		h["Authorization"] = "Bearer " + c.Token
	}
	if c.TenantID != "" {
		h["X-Scope-OrgID"] = c.TenantID
	}
	return h
}

func grafanaTempoSecret(v core.IntegrationConfigValue) (string, error) {
	if v.Value == "" || !v.IsEncrypted {
		return strings.TrimSpace(v.Value), nil
	}
	d, err := common.Decrypt(v.Value)
	return strings.TrimSpace(d), err
}

// grafanaTempoConfigFromValues is shared by TestConnection (unsaved form values) and
// GetGrafanaTempoConfig (stored row) so both normalise identically.
func grafanaTempoConfigFromValues(config []core.IntegrationConfigValue) (GrafanaTempoConfig, error) {
	values := grafanaTempoReadConfig(config)
	cfg := GrafanaTempoConfig{
		URL:                normalizeGrafanaTempoURL(values[GrafanaTempoConfigURL].Value),
		AuthType:           grafanaTempoAuthType(values[GrafanaTempoConfigAuthType].Value),
		Username:           strings.TrimSpace(values[GrafanaTempoConfigUsername].Value),
		TenantID:           strings.TrimSpace(values[GrafanaTempoConfigTenantID].Value),
		InsecureSkipVerify: strings.EqualFold(strings.TrimSpace(values[GrafanaTempoConfigInsecure].Value), "true"),
	}
	var err error
	if cfg.Password, err = grafanaTempoSecret(values[GrafanaTempoConfigPassword]); err != nil {
		return cfg, fmt.Errorf("failed to decrypt %s: %w", GrafanaTempoConfigPassword, err)
	}
	if cfg.Token, err = grafanaTempoSecret(values[GrafanaTempoConfigToken]); err != nil {
		return cfg, fmt.Errorf("failed to decrypt %s: %w", GrafanaTempoConfigToken, err)
	}
	return cfg, nil
}

func grafanaTempoGet(cfg GrafanaTempoConfig, path string, params map[string]string) (int, string, error) {
	opts := []common.HttpOption{
		common.HttpWithHeaders(cfg.Headers()),
		common.HttpWithQueryParams(params),
		common.HttpWithTimeout(15 * time.Second),
	}
	if cfg.InsecureSkipVerify {
		opts = append(opts, common.HttpWithInsecureSkipVerify())
	}
	resp, err := common.HttpGet(cfg.URL+path, opts...)
	if err != nil {
		return 0, "", err
	}
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	return resp.StatusCode, strings.TrimSpace(string(body)), nil
}

// TestConnection implements core.TestableIntegration. /api/echo proves the URL is a
// Tempo query endpoint; the tags call proves the credentials and tenant are accepted by
// the query path itself (a gateway can let /api/echo through unauthenticated).
func (GrafanaTempo) TestConnection(sc *security.RequestContext, config []core.IntegrationConfigValue, accountId string) error {
	cfg, err := grafanaTempoConfigFromValues(config)
	if err != nil {
		return err
	}
	status, _, err := grafanaTempoGet(cfg, "/api/echo", nil)
	if err != nil {
		if strings.Contains(err.Error(), "x509") || strings.Contains(err.Error(), "certificate") {
			return fmt.Errorf("TLS verification failed for Tempo at %s - enable %s for a self-signed certificate: %w",
				cfg.URL, GrafanaTempoConfigInsecure, err)
		}
		return fmt.Errorf("failed to connect to Tempo at %s - the server may be down, or a tunnel/port-forward may have died: %w", cfg.URL, err)
	}
	switch status {
	case http.StatusOK:
	case http.StatusUnauthorized, http.StatusForbidden:
		return fmt.Errorf("tempo rejected the credentials (HTTP %d) - check %s and the matching secret", status, GrafanaTempoConfigAuthType)
	case http.StatusNotFound:
		return fmt.Errorf("%s/api/echo not found - %s is not a Tempo query endpoint (check the path prefix)", cfg.URL, GrafanaTempoConfigURL)
	default:
		return fmt.Errorf("tempo /api/echo returned unexpected status: HTTP %d", status)
	}

	now := time.Now()
	status, body, err := grafanaTempoGet(cfg, "/api/v2/search/tags", map[string]string{
		"start": strconv.FormatInt(now.Add(-time.Hour).Unix(), 10),
		"end":   strconv.FormatInt(now.Unix(), 10),
	})
	if err != nil {
		return fmt.Errorf("tempo tag search failed: %w", err)
	}
	switch {
	case status == http.StatusOK:
		return nil
	case strings.Contains(strings.ToLower(body), "org id"):
		return fmt.Errorf("tempo is multi-tenant and needs a tenant - set %s (sent as X-Scope-OrgID)", GrafanaTempoConfigTenantID)
	case status == http.StatusUnauthorized || status == http.StatusForbidden:
		return fmt.Errorf("tempo rejected the credentials on the query API (HTTP %d) - check %s and the matching secret", status, GrafanaTempoConfigAuthType)
	default:
		return fmt.Errorf("tempo tag search returned HTTP %d: %s", status, body)
	}
}

// GetGrafanaTempoConfig retrieves and decrypts the Tempo configuration for an account.
// An empty accountId is refused: ListIntegrationConfigs only filters by account when one
// is given, so it would otherwise return another account's Tempo.
func GetGrafanaTempoConfig(sc *security.RequestContext, accountId string) (GrafanaTempoConfig, error) {
	if accountId == "" {
		return GrafanaTempoConfig{}, fmt.Errorf("account_id is required to resolve a Grafana Tempo integration")
	}
	configs, err := core.ListIntegrationConfigs(sc, accountId, IntegrationGrafanaTempo)
	if err != nil {
		return GrafanaTempoConfig{}, fmt.Errorf("failed to list Grafana Tempo integration configs: %w", err)
	}
	if len(configs) == 0 {
		return GrafanaTempoConfig{}, fmt.Errorf("grafana tempo integration not found for account: %s", accountId)
	}
	return grafanaTempoConfigFromValues(configs[0].Configs)
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd api-server/services && go test ./integrations/ -run 'GrafanaTempo' -count=1 -v`
Expected: PASS (all subtests). If `TestConnection` is not accepted by the registry's testable interface, compare its signature to `SplunkEnterprise.TestConnection` (`splunk_enterprise.go:290`) — it must match exactly.

- [ ] **Step 5: Commit**

```bash
cd api-server/services && make fmt && cd ../..
git add api-server/services/integrations/grafana_tempo.go api-server/services/integrations/grafana_tempo_test.go
git commit -m "feat(nb-39485): grafana_tempo integration schema, validation and test connection"
```

---

### Task 3: Tempo HTTP client and query windows

**Files:**
- Create: `api-server/services/observability/grafana_tempo_client.go`
- Test: `api-server/services/observability/grafana_tempo_client_test.go`

**Interfaces:**
- Consumes: `integrations.GrafanaTempoConfig`, `(GrafanaTempoConfig).Headers()` (Task 2)
- Produces:
  - constants `tempoSearchMaxWindow`, `tempoMetricsMaxWindow`, `tempoRequestTimeout`, `tempoDefaultPageSize = 50`, `tempoSpanCap = 1000`
  - `type tempoWindow struct { Start, End int64; Clamped bool }` (unix seconds)
  - `func tempoTimeWindow(start, end int64, maxWindow time.Duration, now time.Time) tempoWindow`
  - `func (w tempoWindow) params() map[string]string` → `{"start": …, "end": …}`
  - `func tempoGet(cfg integrations.GrafanaTempoConfig, path string, params map[string]string) ([]byte, error)`

- [ ] **Step 1: Write the failing tests**

```go
package observability

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"nudgebee/services/integrations"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var tempoNow = time.Unix(1_790_000_000, 0)

func TestTempoTimeWindow_AcceptsMillisAndSeconds(t *testing.T) {
	ms := tempoTimeWindow(1_789_999_000_000, 1_790_000_000_000, tempoSearchMaxWindow, tempoNow)
	s := tempoTimeWindow(1_789_999_000, 1_790_000_000, tempoSearchMaxWindow, tempoNow)
	assert.Equal(t, tempoWindow{Start: 1_789_999_000, End: 1_790_000_000}, ms)
	assert.Equal(t, ms, s)
}

func TestTempoTimeWindow_DefaultsToLastHour(t *testing.T) {
	w := tempoTimeWindow(0, 0, tempoSearchMaxWindow, tempoNow)
	assert.Equal(t, tempoNow.Unix(), w.End)
	assert.Equal(t, tempoNow.Add(-time.Hour).Unix(), w.Start)
	assert.False(t, w.Clamped)
}

func TestTempoTimeWindow_ClampsSevenDaysForMetrics(t *testing.T) {
	w := tempoTimeWindow(tempoNow.Add(-7*24*time.Hour).Unix(), tempoNow.Unix(), tempoMetricsMaxWindow, tempoNow)
	assert.True(t, w.Clamped)
	assert.Equal(t, tempoNow.Unix()-int64(tempoMetricsMaxWindow/time.Second), w.Start)
	assert.Less(t, w.End-w.Start, int64(24*3600))
}

func TestTempoTimeWindow_ClampsThirtyDaysForSearch(t *testing.T) {
	w := tempoTimeWindow(tempoNow.Add(-30*24*time.Hour).Unix(), tempoNow.Unix(), tempoSearchMaxWindow, tempoNow)
	assert.True(t, w.Clamped)
	assert.Less(t, w.End-w.Start, int64(168*3600))
}

func TestTempoGet_SendsHeadersAndSurfacesTempoErrors(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Scope-OrgID") != "team-a" {
			w.WriteHeader(401)
			_, _ = w.Write([]byte("no org id"))
			return
		}
		if r.URL.Query().Get("q") == "bad" {
			w.WriteHeader(400)
			_, _ = w.Write([]byte("range specified by start and end exceeds 168h0m0s"))
			return
		}
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer srv.Close()
	cfg := integrations.GrafanaTempoConfig{URL: srv.URL, AuthType: "none", TenantID: "team-a"}

	body, err := tempoGet(cfg, "/api/search", map[string]string{"q": "{}"})
	require.NoError(t, err)
	assert.JSONEq(t, `{"ok":true}`, string(body))

	_, err = tempoGet(cfg, "/api/search", map[string]string{"q": "bad"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "HTTP 400")
	assert.Contains(t, err.Error(), "exceeds 168h0m0s")

	_, err = tempoGet(integrations.GrafanaTempoConfig{URL: srv.URL}, "/api/search", nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no org id")
}
```

- [ ] **Step 2: Run to verify they fail**

Run: `cd api-server/services && go test ./observability/ -run 'TempoTimeWindow|TempoGet' -count=1`
Expected: FAIL — `undefined: tempoTimeWindow`.

- [ ] **Step 3: Implement `grafana_tempo_client.go`**

```go
package observability

import (
	"fmt"
	"io"
	"nudgebee/services/common"
	"nudgebee/services/integrations"
	"strconv"
	"strings"
	"time"
)

// Tempo 3.0 rejects a search window over 168h and a TraceQL-metrics window of 24h or
// more (probed against the dev Tempo, 2026-09-29). A minute is kept back so a window the
// UI computed to the second never lands exactly on the limit.
const (
	tempoSearchMaxWindow  = 168*time.Hour - time.Minute
	tempoMetricsMaxWindow = 24*time.Hour - time.Minute
	tempoRequestTimeout   = 30 * time.Second
	tempoDefaultPageSize  = 50
	// tempoSpanCap bounds how deep the listing pages: /api/search has no offset, so page N
	// is served by fetching the newest N*size spans and slicing. 1000 matches the largest
	// caller (the LLM trace path).
	tempoSpanCap = 1000
	tempoMaxBody = 32 << 20
)

// tempoWindow is a query window in unix seconds, as Tempo's start/end params take it.
type tempoWindow struct {
	Start   int64
	End     int64
	Clamped bool
}

func (w tempoWindow) params() map[string]string {
	return map[string]string{
		"start": strconv.FormatInt(w.Start, 10),
		"end":   strconv.FormatInt(w.End, 10),
	}
}

// tempoTimeWindow normalises a request window (callers send milliseconds; seconds are
// tolerated) and keeps only its most recent maxWindow, so a long picker range still answers
// with the newest data instead of a Tempo 400.
func tempoTimeWindow(start, end int64, maxWindow time.Duration, now time.Time) tempoWindow {
	toSec := func(v int64) int64 {
		if v >= 1e12 {
			return v / 1000
		}
		return v
	}
	w := tempoWindow{Start: toSec(start), End: toSec(end)}
	if w.End <= 0 {
		w.End = now.Unix()
	}
	if w.Start <= 0 || w.Start >= w.End {
		w.Start = w.End - int64(time.Hour/time.Second)
	}
	if limit := int64(maxWindow / time.Second); w.End-w.Start > limit {
		w.Start = w.End - limit
		w.Clamped = true
	}
	return w
}

// tempoGet performs one authenticated GET against Tempo. common.HttpGet does not fail on
// HTTP errors, so a non-2xx is turned into an error carrying Tempo's own message, which
// is plain text and names the actual problem (window too long, bad TraceQL, no org id).
func tempoGet(cfg integrations.GrafanaTempoConfig, path string, params map[string]string) ([]byte, error) {
	opts := []common.HttpOption{
		common.HttpWithHeaders(cfg.Headers()),
		common.HttpWithQueryParams(params),
		common.HttpWithTimeout(tempoRequestTimeout),
	}
	if cfg.InsecureSkipVerify {
		opts = append(opts, common.HttpWithInsecureSkipVerify())
	}
	resp, err := common.HttpGet(cfg.URL+path, opts...)
	if err != nil {
		return nil, fmt.Errorf("grafana tempo: %s request failed: %w", path, err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(io.LimitReader(resp.Body, tempoMaxBody))
	if err != nil {
		return nil, fmt.Errorf("grafana tempo: reading %s response: %w", path, err)
	}
	if resp.StatusCode/100 != 2 {
		msg := strings.TrimSpace(string(body))
		if len(msg) > 300 {
			msg = msg[:300]
		}
		return nil, fmt.Errorf("grafana tempo: %s returned HTTP %d: %s", path, resp.StatusCode, msg)
	}
	return body, nil
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd api-server/services && go test ./observability/ -run 'TempoTimeWindow|TempoGet' -count=1 -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add api-server/services/observability/grafana_tempo_client.go api-server/services/observability/grafana_tempo_client_test.go
git commit -m "feat(nb-39485): Tempo HTTP client and query window clamping"
```

### Task 4: TraceQL builder (label mapping, where → TraceQL, raw queries, grouping queries)

**Files:**
- Create: `api-server/services/observability/grafana_tempo_traceql.go`
- Test: `api-server/services/observability/grafana_tempo_traceql_test.go`

**Interfaces:**
- Consumes: `cubeAPMFilterValues(val any) []string` (existing, `cubeapm_traces.go:178`), `query.QueryWhereClause` and the `query.*` operator constants.
- Produces:
  - `var tempoTraceLabelMapping map[string]string`, `var tempoTraceUnsupportedFields map[string]bool`, `var tempoSupportedOperators []string`
  - `const tempoSelectClause`, `const tempoGroupBy`
  - `func tempoAttrFor(label string) (string, error)`
  - `func tempoSelector(where query.QueryWhereClause) (string, error)`
  - `func tempoSearchQuery(raw string, where query.QueryWhereClause) (string, error)`
  - `type tempoGroupQueries struct { Count, Errors, Quantiles, Max string }` and `func buildTempoGroupQueries(raw string, where query.QueryWhereClause) (tempoGroupQueries, error)`

- [ ] **Step 1: Write the failing tests**

```go
package observability

import (
	"testing"

	"nudgebee/services/query"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func tempoWhereOf(binary query.BinaryWhereClause) query.QueryWhereClause {
	return query.QueryWhereClause{Binary: binary}
}

func TestTempoSelector_EmptyMatchesEverything(t *testing.T) {
	sel, err := tempoSelector(query.QueryWhereClause{})
	require.NoError(t, err)
	assert.Equal(t, "{}", sel)
}

func TestTempoSelector_CanonicalAndScopedFields(t *testing.T) {
	sel, err := tempoSelector(tempoWhereOf(query.BinaryWhereClause{
		"workload_namespace":    {query.Eq: "demo"},
		"resource.k8s.pod.name": {query.Eq: "p-1"},
	}))
	require.NoError(t, err)
	assert.Equal(t, `{ resource.k8s.pod.name = "p-1" && resource.k8s.namespace.name = "demo" }`, sel)
}

func TestTempoCondition_Status(t *testing.T) {
	sel, err := tempoSelector(tempoWhereOf(query.BinaryWhereClause{"status_code": {query.Contains: "STATUS_CODE_ERROR"}}))
	require.NoError(t, err)
	assert.Equal(t, `{ status = error }`, sel)

	sel, err = tempoSelector(tempoWhereOf(query.BinaryWhereClause{"status_code": {query.Nq: "STATUS_CODE_OK"}}))
	require.NoError(t, err)
	assert.Equal(t, `{ status != ok }`, sel)

	_, err = tempoSelector(tempoWhereOf(query.BinaryWhereClause{"status_code": {query.Eq: "BROKEN"}}))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unknown span status")
}

func TestTempoCondition_Operators(t *testing.T) {
	cases := []struct {
		name  string
		label string
		op    query.BinaryWhereClauseType
		val   any
		want  string
	}{
		{"numeric eq", "http_status_code", query.Eq, "503", `span.http.status_code = 503`},
		{"in", "workload_name", query.In, []any{"a", "b"}, `(resource.service.name = "a" || resource.service.name = "b")`},
		{"not in", "workload_name", query.NotIn, []any{"a", "b"}, `(resource.service.name != "a" && resource.service.name != "b")`},
		{"contains", "resource", query.Contains, "/api", `span.http.target =~ "(?i).*/api.*"`},
		{"not contains", "resource", query.NIContains, "/api", `span.http.target !~ "(?i).*/api.*"`},
		{"like", "workload_name", query.Like, "%pay%", `resource.service.name =~ "(?i).*pay.*"`},
		{"regex", "span_name", query.Regex, "GET .*", `name =~ "GET .*"`},
		{"duration gt", "duration_ns", query.Gt, "500000000", `duration > 500000000ns`},
		{"trace id", "trace_id", query.Eq, "abc", `trace:id = "abc"`},
		{"destination", "destination_workload_name", query.Eq, "payments", `span.net.peer.name = "payments"`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			sel, err := tempoSelector(tempoWhereOf(query.BinaryWhereClause{tc.label: {tc.op: tc.val}}))
			require.NoError(t, err)
			assert.Equal(t, "{ "+tc.want+" }", sel)
		})
	}
}

func TestTempoCondition_Escaping(t *testing.T) {
	sel, err := tempoSelector(tempoWhereOf(query.BinaryWhereClause{"workload_name": {query.Eq: `a"b`}}))
	require.NoError(t, err)
	assert.Equal(t, `{ resource.service.name = "a\"b" }`, sel)

	sel, err = tempoSelector(tempoWhereOf(query.BinaryWhereClause{"resource": {query.Contains: "/api/v1/*"}}))
	require.NoError(t, err)
	assert.Equal(t, `{ span.http.target =~ "(?i).*/api/v1/\\*.*" }`, sel)
}

func TestTempoWhere_StripsUnsupportedAndRejectsUnknown(t *testing.T) {
	sel, err := tempoSelector(tempoWhereOf(query.BinaryWhereClause{"destination_workload_namespace": {query.Eq: "demo"}}))
	require.NoError(t, err)
	assert.Equal(t, "{}", sel, "a field Tempo cannot express is dropped, not turned into an always-false match")

	_, err = tempoSelector(tempoWhereOf(query.BinaryWhereClause{"podname": {query.Eq: "x"}}))
	require.Error(t, err)
	assert.Contains(t, err.Error(), `unsupported trace field "podname"`)

	_, err = tempoSelector(tempoWhereOf(query.BinaryWhereClause{"workload_name": {query.HasKey: "x"}}))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unsupported operator")
}

func TestTempoWhere_OrAndGroups(t *testing.T) {
	sel, err := tempoSelector(query.QueryWhereClause{
		Or: []query.QueryWhereClause{
			tempoWhereOf(query.BinaryWhereClause{"workload_name": {query.Eq: "a"}}),
			tempoWhereOf(query.BinaryWhereClause{"workload_name": {query.Eq: "b"}}),
		},
		And: []query.QueryWhereClause{tempoWhereOf(query.BinaryWhereClause{"workload_namespace": {query.Eq: "demo"}})},
	})
	require.NoError(t, err)
	assert.Equal(t, `{ (resource.k8s.namespace.name = "demo") && (resource.service.name = "a" || resource.service.name = "b") }`, sel)
}

func TestTempoSearchQuery_AppendsSelectAndMostRecent(t *testing.T) {
	q, err := tempoSearchQuery("", tempoWhereOf(query.BinaryWhereClause{"workload_namespace": {query.Eq: "demo"}}))
	require.NoError(t, err)
	assert.Equal(t, `{ resource.k8s.namespace.name = "demo" } | `+tempoSelectClause+` with (most_recent=true)`, q)

	q, err = tempoSearchQuery(`{ status = error }`, query.QueryWhereClause{})
	require.NoError(t, err)
	assert.Equal(t, `{ status = error } | `+tempoSelectClause+` with (most_recent=true)`, q)

	raw := `{ status = error } | select(span.http.target) with (most_recent=true)`
	q, err = tempoSearchQuery(raw, query.QueryWhereClause{})
	require.NoError(t, err)
	assert.Equal(t, raw, q, "a raw query that already selects and orders is sent unchanged")
}

func TestBuildTempoGroupQueries(t *testing.T) {
	g, err := buildTempoGroupQueries("", tempoWhereOf(query.BinaryWhereClause{"workload_namespace": {query.Eq: "demo"}}))
	require.NoError(t, err)
	assert.Equal(t, `{ resource.k8s.namespace.name = "demo" } | count_over_time() `+tempoGroupBy, g.Count)
	assert.Equal(t, `{ (resource.k8s.namespace.name = "demo") && status = error } | count_over_time() `+tempoGroupBy, g.Errors)
	assert.Equal(t, `{ resource.k8s.namespace.name = "demo" } | quantile_over_time(duration, .95, .99) `+tempoGroupBy, g.Quantiles)
	assert.Equal(t, `{ resource.k8s.namespace.name = "demo" } | max_over_time(duration) `+tempoGroupBy, g.Max)

	g, err = buildTempoGroupQueries("", query.QueryWhereClause{})
	require.NoError(t, err)
	assert.Equal(t, `{ status = error } | count_over_time() `+tempoGroupBy, g.Errors)

	_, err = buildTempoGroupQueries(`{ status = error }`, query.QueryWhereClause{})
	require.Error(t, err)
}
```

- [ ] **Step 2: Run to verify they fail**

Run: `cd api-server/services && go test ./observability/ -run 'TempoSelector|TempoCondition|TempoWhere|TempoSearchQuery|BuildTempoGroupQueries' -count=1`
Expected: FAIL — `undefined: tempoSelector`.

- [ ] **Step 3: Implement `grafana_tempo_traceql.go`**

```go
package observability

import (
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"nudgebee/services/query"
)

// tempoTraceLabelMapping maps canonical trace fields to TraceQL attributes. Resource
// attributes carry the span's own Kubernetes identity; the callee is the OTel
// net.peer.name span attribute.
var tempoTraceLabelMapping = map[string]string{
	"trace_id":                  "trace:id",
	"span_id":                   "span:id",
	"parent_id":                 "span:parentID",
	"span_name":                 "name",
	"service_name":              "resource.service.name",
	"workload_name":             "resource.service.name",
	"workload_namespace":        "resource.k8s.namespace.name",
	"destination_workload_name": "span.net.peer.name",
	"destination_name":          "span.net.peer.name",
	"http_status_code":          "span.http.status_code",
	"resource":                  "span.http.target",
	"status_code":               "status",
	"duration_ns":               "duration",
}

// tempoTraceUnsupportedFields are canonical fields a Tempo span cannot express: a span
// names its callee but carries no callee namespace or zone. Dropped rather than matched,
// for the reason Splunk and OpenObserve drop them — an unmatched field would blank the
// destination side of a caller-OR-callee evidence search.
var tempoTraceUnsupportedFields = map[string]bool{
	"destination_workload_namespace": true,
	"destination_workload_zone":      true,
}

var tempoNumericAttrs = map[string]bool{"span.http.status_code": true, "duration": true}

// Scoped attribute names (as QueryLabels returns them) pass through unchanged.
var tempoAttrPattern = regexp.MustCompile(`^(resource|span)\.[A-Za-z0-9_.\-]+$`)

var tempoSupportedOperators = []string{
	string(query.Eq), string(query.Nq), string(query.In), string(query.NotIn),
	string(query.Contains), string(query.IContains), string(query.NIContains),
	string(query.Like), string(query.ILike), string(query.NLike),
	string(query.Regex), string(query.NRegex),
	string(query.Gt), string(query.Gte), string(query.Lt), string(query.Lte),
}

// tempoSelectClause asks search to return the attributes the listing renders; without
// select() Tempo returns only span id, name, start and duration.
const tempoSelectClause = "select(resource.service.name, resource.k8s.namespace.name, resource.k8s.pod.name, " +
	"span.net.peer.name, span.peer.service, span.http.status_code, span.http.target, span.url.path, " +
	"status, kind, span:parentID)"

// tempoGroupBy has four keys on purpose: Tempo rejects five group-by values once the
// quantile's own `p` label is added ("metrics group by 5 values not yet supported").
const tempoGroupBy = "by (resource.service.name, resource.k8s.namespace.name, name, span.net.peer.name)"

func tempoAttrFor(label string) (string, error) {
	if attr, ok := tempoTraceLabelMapping[label]; ok {
		return attr, nil
	}
	if tempoAttrPattern.MatchString(label) {
		return label, nil
	}
	return "", fmt.Errorf("grafana tempo: unsupported trace field %q", label)
}

// tempoStatusLiteral turns the UI's STATUS_CODE_ERROR (or a bare ERROR/error) into the
// TraceQL status enum, which is unquoted: `status = error`.
func tempoStatusLiteral(v string) (string, error) {
	s := strings.ToLower(strings.TrimPrefix(strings.ToUpper(strings.TrimSpace(v)), "STATUS_CODE_"))
	switch s {
	case "error", "ok", "unset":
		return s, nil
	}
	return "", fmt.Errorf("grafana tempo: unknown span status %q (want ok, error or unset)", v)
}

func tempoLiteral(attr, v string) string {
	if tempoNumericAttrs[attr] {
		if _, err := strconv.ParseInt(v, 10, 64); err == nil {
			if attr == "duration" {
				return v + "ns"
			}
			return v
		}
	}
	return strconv.Quote(v)
}

func tempoJoin(parts []string, sep string) string {
	if len(parts) == 1 {
		return parts[0]
	}
	return "(" + strings.Join(parts, sep) + ")"
}

func tempoStatusCondition(op query.BinaryWhereClauseType, values []string) (string, error) {
	cmp, sep := "=", " || "
	switch op {
	case query.Eq, query.In, query.Contains, query.IContains, query.Like, query.ILike:
	case query.Nq, query.NotIn, query.NIContains, query.NLike:
		cmp, sep = "!=", " && "
	default:
		return "", fmt.Errorf("grafana tempo: unsupported operator %q for field %q", op, "status")
	}
	parts := make([]string, 0, len(values))
	for _, v := range values {
		lit, err := tempoStatusLiteral(v)
		if err != nil {
			return "", err
		}
		parts = append(parts, "status "+cmp+" "+lit)
	}
	return tempoJoin(parts, sep), nil
}

func tempoCondition(attr string, op query.BinaryWhereClauseType, raw any) (string, error) {
	values := cubeAPMFilterValues(raw)
	if len(values) == 0 {
		return "", nil
	}
	if attr == "status" {
		return tempoStatusCondition(op, values)
	}
	plain := func(v string) string { return tempoLiteral(attr, v) }
	contains := func(v string) string { return strconv.Quote("(?i).*" + regexp.QuoteMeta(v) + ".*") }
	like := func(v string) string { return strconv.Quote("(?i)" + strings.ReplaceAll(regexp.QuoteMeta(v), "%", ".*")) }
	rx := func(v string) string { return strconv.Quote(v) }
	each := func(cmp string, lit func(string) string, sep string) string {
		parts := make([]string, 0, len(values))
		for _, v := range values {
			parts = append(parts, attr+" "+cmp+" "+lit(v))
		}
		return tempoJoin(parts, sep)
	}
	switch op {
	case query.Eq, query.In:
		return each("=", plain, " || "), nil
	case query.Nq, query.NotIn:
		return each("!=", plain, " && "), nil
	case query.Contains, query.IContains:
		return each("=~", contains, " || "), nil
	case query.NIContains:
		return each("!~", contains, " && "), nil
	case query.Like, query.ILike:
		return each("=~", like, " || "), nil
	case query.NLike:
		return each("!~", like, " && "), nil
	case query.Regex:
		return each("=~", rx, " || "), nil
	case query.NRegex:
		return each("!~", rx, " && "), nil
	case query.Gt:
		return each(">", plain, " || "), nil
	case query.Gte:
		return each(">=", plain, " || "), nil
	case query.Lt:
		return each("<", plain, " || "), nil
	case query.Lte:
		return each("<=", plain, " || "), nil
	}
	return "", fmt.Errorf("grafana tempo: unsupported operator %q for field %q", op, attr)
}

// tempoWhereExpr renders a where clause as the body of one TraceQL span selector.
// Binary predicates are rendered in sorted label/operator order so the query is stable.
func tempoWhereExpr(w query.QueryWhereClause) (string, error) {
	var parts []string

	labels := make([]string, 0, len(w.Binary))
	for label := range w.Binary {
		labels = append(labels, label)
	}
	sort.Strings(labels)
	for _, label := range labels {
		if tempoTraceUnsupportedFields[label] {
			continue
		}
		attr, err := tempoAttrFor(label)
		if err != nil {
			return "", err
		}
		ops := make([]string, 0, len(w.Binary[label]))
		for op := range w.Binary[label] {
			ops = append(ops, string(op))
		}
		sort.Strings(ops)
		for _, op := range ops {
			cond, err := tempoCondition(attr, query.BinaryWhereClauseType(op), w.Binary[label][query.BinaryWhereClauseType(op)])
			if err != nil {
				return "", err
			}
			if cond != "" {
				parts = append(parts, cond)
			}
		}
	}
	for _, sub := range w.And {
		e, err := tempoWhereExpr(sub)
		if err != nil {
			return "", err
		}
		if e != "" {
			parts = append(parts, "("+e+")")
		}
	}
	if len(w.Or) > 0 {
		ors := make([]string, 0, len(w.Or))
		for _, sub := range w.Or {
			e, err := tempoWhereExpr(sub)
			if err != nil {
				return "", err
			}
			if e != "" {
				ors = append(ors, e)
			}
		}
		if len(ors) > 0 {
			parts = append(parts, "("+strings.Join(ors, " || ")+")")
		}
	}
	if w.Not != nil {
		return "", fmt.Errorf("grafana tempo: negated filter groups are not supported")
	}
	return strings.Join(parts, " && "), nil
}

func tempoSelector(w query.QueryWhereClause) (string, error) {
	cond, err := tempoWhereExpr(w)
	if err != nil {
		return "", err
	}
	if cond == "" {
		return "{}", nil
	}
	return "{ " + cond + " }", nil
}

// tempoSearchQuery builds the /api/search query. A raw Code-mode query is used as the
// selector; the standard select() and newest-first hint are appended only when absent,
// so a user who wrote their own is not overridden.
func tempoSearchQuery(raw string, w query.QueryWhereClause) (string, error) {
	q := strings.TrimSpace(raw)
	if q == "" {
		sel, err := tempoSelector(w)
		if err != nil {
			return "", err
		}
		q = sel
	}
	if !strings.Contains(q, "select(") {
		q += " | " + tempoSelectClause
	}
	if !strings.Contains(q, "most_recent") {
		q += " with (most_recent=true)"
	}
	return q, nil
}

type tempoGroupQueries struct {
	Count, Errors, Quantiles, Max string
}

// buildTempoGroupQueries returns the four instant TraceQL-metrics queries behind the
// grouped view. Grouping needs builder filters: a raw query may already carry a pipeline
// that cannot be combined with `&& status = error`.
func buildTempoGroupQueries(raw string, w query.QueryWhereClause) (tempoGroupQueries, error) {
	if strings.TrimSpace(raw) != "" {
		return tempoGroupQueries{}, fmt.Errorf("grafana tempo: the grouped view uses the filter builder; raw TraceQL is supported in the span listing only")
	}
	cond, err := tempoWhereExpr(w)
	if err != nil {
		return tempoGroupQueries{}, err
	}
	all, errs := "{}", "{ status = error }"
	if cond != "" {
		all = "{ " + cond + " }"
		errs = "{ (" + cond + ") && status = error }"
	}
	return tempoGroupQueries{
		Count:     all + " | count_over_time() " + tempoGroupBy,
		Errors:    errs + " | count_over_time() " + tempoGroupBy,
		Quantiles: all + " | quantile_over_time(duration, .95, .99) " + tempoGroupBy,
		Max:       all + " | max_over_time(duration) " + tempoGroupBy,
	}, nil
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd api-server/services && go test ./observability/ -run 'TempoSelector|TempoCondition|TempoWhere|TempoSearchQuery|BuildTempoGroupQueries' -count=1 -v`
Expected: PASS.

- [ ] **Step 5: Check the generated TraceQL against the real Tempo** (port-forward on `localhost:3200`)

Run:
```bash
E=$(date +%s); for q in '{ resource.k8s.namespace.name = "demo" && status = error } | select(resource.service.name, resource.k8s.namespace.name, resource.k8s.pod.name, span.net.peer.name, span.peer.service, span.http.status_code, span.http.target, span.url.path, status, kind, span:parentID) with (most_recent=true)' '{ span.http.target =~ "(?i).*/check.*" && duration > 1000ns }'; do curl -s -o /dev/null -w "%{http_code}\n" -G http://localhost:3200/api/search --data-urlencode "q=$q" --data-urlencode start=$((E-900)) --data-urlencode end=$E; done
```
Expected: `200` twice (Tempo accepts the syntax the builder emits).

- [ ] **Step 6: Commit**

```bash
git add api-server/services/observability/grafana_tempo_traceql.go api-server/services/observability/grafana_tempo_traceql_test.go
git commit -m "feat(nb-39485): build TraceQL from Nudgebee trace filters"
```

---

### Task 5: Response converters (search spans, OTLP waterfall, metrics series)

**Files:**
- Create: `api-server/services/observability/grafana_tempo_convert.go`
- Test: `api-server/services/observability/grafana_tempo_convert_test.go`

**Interfaces:**
- Produces:
  - `func tempoSearchToSpans(body []byte) ([]common.OpenTelemetryTrace, error)`
  - `func tempoOTLPToHeatmap(body []byte) ([]common.OpenTelemetryTraceHeatMap, error)`
  - `type tempoSeries struct { Labels map[string]string; Value float64 }` and `func parseTempoMetricSeries(body []byte) ([]tempoSeries, error)`
  - `func tempoStatusCode(s string) string` (`error`→`STATUS_CODE_ERROR`, `ok`→`STATUS_CODE_OK`, else `STATUS_CODE_UNSET`)

- [ ] **Step 1: Write the failing tests** (fixtures are trimmed from real Tempo 3.0.3 responses probed on 2026-09-29)

```go
package observability

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const tempoSearchFixture = `{"traces":[{"traceID":"5b8efff798038103d269b633813fc60c","rootServiceName":"frontend",
 "spanSet":{"spans":[{"spanID":"eee19b7ec3c1b174","name":"GET /checkout","startTimeUnixNano":"1790626201857133800","durationNanos":"150000000","attributes":[]}]},
 "spanSets":[{"spans":[
  {"spanID":"eee19b7ec3c1b174","name":"GET /checkout","startTimeUnixNano":"1790626201857133800","durationNanos":"150000000","attributes":[
   {"key":"service.name","value":{"stringValue":"frontend"}},
   {"key":"k8s.namespace.name","value":{"stringValue":"demo"}},
   {"key":"k8s.pod.name","value":{"stringValue":"frontend-abc"}},
   {"key":"net.peer.name","value":{"stringValue":"payments"}},
   {"key":"peer.service","value":{"stringValue":"nil"}},
   {"key":"http.status_code","value":{"intValue":"503"}},
   {"key":"http.target","value":{"stringValue":"/checkout"}},
   {"key":"status","value":{"stringValue":"error"}},
   {"key":"kind","value":{"stringValue":"server"}},
   {"key":"span:parentID","value":{"stringValue":"0000000000000000"}}]},
  {"spanID":"ddba5d0cdcc241d7","name":"GET /charge","startTimeUnixNano":"1790626201859133800","durationNanos":"40000000","attributes":[
   {"key":"service.name","value":{"stringValue":"payments"}},
   {"key":"http.status_code","value":{"stringValue":"nil"}},
   {"key":"status","value":{"stringValue":"ok"}},
   {"key":"kind","value":{"stringValue":"client"}},
   {"key":"span:parentID","value":{"stringValue":"eee19b7ec3c1b174"}}]}
 ]}]}]}`

func TestTempoSearchToSpans(t *testing.T) {
	spans, err := tempoSearchToSpans([]byte(tempoSearchFixture))
	require.NoError(t, err)
	require.Len(t, spans, 2, "a span present in both spanSet and spanSets is returned once")

	root := spans[0]
	assert.Equal(t, "5b8efff798038103d269b633813fc60c", root.TraceID)
	assert.Equal(t, "eee19b7ec3c1b174", root.SpanID)
	assert.Equal(t, "", root.ParentSpanID, "an all-zero parent id means root")
	assert.Equal(t, "GET /checkout", root.SpanName)
	assert.Equal(t, "SPAN_KIND_SERVER", root.SpanKind)
	assert.Equal(t, "frontend", root.ServiceName)
	assert.Equal(t, "frontend", root.WorkloadName)
	assert.Equal(t, "demo", root.WorkloadNamespace)
	assert.Equal(t, "payments", root.DestinationWorkload)
	assert.Equal(t, "payments", root.DestinationName)
	assert.Equal(t, "503", root.HTTPStatusCode)
	assert.Equal(t, "STATUS_CODE_ERROR", root.StatusCode)
	assert.Equal(t, "/checkout", root.Resource)
	assert.Equal(t, int64(150000000), root.DurationNs)
	assert.Equal(t, "grafana_tempo", root.TraceSource)
	assert.Equal(t, "frontend-abc", root.ResourceAttributes["k8s.pod.name"])
	assert.NotContains(t, root.SpanAttributes, "peer.service", `"nil" is Tempo's missing-value marker`)
	assert.NotContains(t, root.SpanAttributes, "status")
	ms, err := ParseDateToMillis(root.Timestamp)
	require.NoError(t, err)
	assert.Equal(t, int64(1790626201857), ms)

	child := spans[1]
	assert.Equal(t, "eee19b7ec3c1b174", child.ParentSpanID)
	assert.Equal(t, "", child.HTTPStatusCode)
	assert.Equal(t, "STATUS_CODE_OK", child.StatusCode)
	assert.Equal(t, "SPAN_KIND_CLIENT", child.SpanKind)
}

const tempoOTLPFixture = `{"trace":{"resourceSpans":[{"resource":{"attributes":[
  {"key":"service.name","value":{"stringValue":"payments"}},{"key":"k8s.namespace.name","value":{"stringValue":"demo"}}]},
 "scopeSpans":[{"scope":{},"spans":[{"traceId":"W47/95gDgQPSabYzgT/GDA==","spanId":"3bpdDNzCQdc=","parentSpanId":"7uGbfsPBsXQ=",
  "name":"GET /charge","kind":"SPAN_KIND_CLIENT","startTimeUnixNano":"1790626201859133800","endTimeUnixNano":"1790626201899133800",
  "attributes":[{"key":"http.status_code","value":{"intValue":"503"}}],
  "status":{"code":"STATUS_CODE_ERROR","message":"upstream returned 503"},
  "events":[{"name":"exception","timeUnixNano":"1790626201860000000","attributes":[{"key":"exception.message","value":{"stringValue":"boom"}}]}]}]}]}]}}`

func TestTempoOTLPToHeatmap(t *testing.T) {
	out, err := tempoOTLPToHeatmap([]byte(tempoOTLPFixture))
	require.NoError(t, err)
	require.Len(t, out, 1)
	s := out[0]
	assert.Equal(t, "5b8efff798038103d269b633813fc60c", s.TraceID, "trace-by-id ids are base64 and must be decoded to hex")
	assert.Equal(t, "ddba5d0cdcc241d7", s.SpanID)
	assert.Equal(t, "payments", s.ServiceName)
	assert.Equal(t, "demo", s.ResourceAttributes["k8s.namespace.name"])
	assert.Equal(t, "STATUS_CODE_ERROR", s.StatusCode)
	assert.Equal(t, int64(40000000), s.DurationNs)
	assert.Equal(t, "503", s.SpanAttributes["http.status_code"])
	assert.Equal(t, []string{"exception"}, s.EventsName)
	assert.Equal(t, "boom", s.EventsAttributes[0]["exception.message"])
}

func TestTempoOTLPToHeatmap_EmptyTrace(t *testing.T) {
	out, err := tempoOTLPToHeatmap([]byte(`{"trace":{},"metrics":{}}`))
	require.NoError(t, err)
	assert.Empty(t, out)
}

func TestParseTempoMetricSeries(t *testing.T) {
	body := `{"series":[
	 {"labels":[{"key":"resource.service.name","value":{"stringValue":"frontend"}},{"key":"p","value":{"doubleValue":0.95}}],"value":0.51},
	 {"labels":[{"key":"resource.service.name","value":{"stringValue":"ledger"}}],"value":36}]}`
	series, err := parseTempoMetricSeries([]byte(body))
	require.NoError(t, err)
	require.Len(t, series, 2)
	assert.Equal(t, "0.95", series[0].Labels["p"])
	assert.Equal(t, 0.51, series[0].Value)
	assert.Equal(t, "ledger", series[1].Labels["resource.service.name"])
	assert.Equal(t, 36.0, series[1].Value)
}
```

- [ ] **Step 2: Run to verify they fail**

Run: `cd api-server/services && go test ./observability/ -run 'TempoSearchToSpans|TempoOTLPToHeatmap|ParseTempoMetricSeries' -count=1`
Expected: FAIL — `undefined: tempoSearchToSpans`.

- [ ] **Step 3: Implement `grafana_tempo_convert.go`**

```go
package observability

import (
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"nudgebee/services/common"
)

const tempoTraceSourceName = "grafana_tempo"

type tempoKV struct {
	Key   string         `json:"key"`
	Value map[string]any `json:"value"`
}

// tempoValueString flattens an OTLP AnyValue ({"stringValue":…}, {"intValue":"…"},
// {"doubleValue":…}, {"boolValue":…}). Tempo reports a selected attribute that is absent
// on a span as the string "nil", which is treated as empty.
func tempoValueString(v map[string]any) string {
	for _, raw := range v {
		var s string
		switch x := raw.(type) {
		case string:
			s = x
		case float64:
			s = strconv.FormatFloat(x, 'f', -1, 64)
		case bool:
			s = strconv.FormatBool(x)
		default:
			s = fmt.Sprint(x)
		}
		if s == "nil" {
			return ""
		}
		return s
	}
	return ""
}

func tempoAttrs(kvs []tempoKV) map[string]string {
	out := make(map[string]string, len(kvs))
	for _, kv := range kvs {
		if v := tempoValueString(kv.Value); v != "" {
			out[kv.Key] = v
		}
	}
	return out
}

func tempoStatusCode(s string) string {
	switch strings.ToLower(s) {
	case "error":
		return "STATUS_CODE_ERROR"
	case "ok":
		return "STATUS_CODE_OK"
	}
	return "STATUS_CODE_UNSET"
}

func tempoSpanKind(s string) string {
	if s == "" {
		return "SPAN_KIND_UNSPECIFIED"
	}
	return "SPAN_KIND_" + strings.ToUpper(s)
}

func tempoFirst(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

type tempoSearchSpan struct {
	SpanID            string    `json:"spanID"`
	Name              string    `json:"name"`
	StartTimeUnixNano string    `json:"startTimeUnixNano"`
	DurationNanos     string    `json:"durationNanos"`
	Attributes        []tempoKV `json:"attributes"`
}

type tempoSpanSet struct {
	Spans []tempoSearchSpan `json:"spans"`
}

type tempoSearchResponse struct {
	Traces []struct {
		TraceID  string         `json:"traceID"`
		SpanSet  *tempoSpanSet  `json:"spanSet"`
		SpanSets []tempoSpanSet `json:"spanSets"`
	} `json:"traces"`
}

// Keys the converter lifts into dedicated fields; everything else stays a span attribute.
var tempoResourceKeys = []string{"service.name", "k8s.namespace.name", "k8s.pod.name"}
var tempoLiftedKeys = map[string]bool{
	"service.name": true, "k8s.namespace.name": true, "k8s.pod.name": true,
	"status": true, "kind": true, "span:parentID": true,
}

// tempoSearchToSpans flattens /api/search into spans. Tempo returns the first spanset
// both as `spanSet` and inside `spanSets`, so spans are de-duplicated by trace+span id.
func tempoSearchToSpans(body []byte) ([]common.OpenTelemetryTrace, error) {
	var resp tempoSearchResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("grafana tempo: decoding search response: %w", err)
	}
	seen := map[string]bool{}
	out := []common.OpenTelemetryTrace{}
	for _, tr := range resp.Traces {
		sets := tr.SpanSets
		if len(sets) == 0 && tr.SpanSet != nil {
			sets = []tempoSpanSet{*tr.SpanSet}
		}
		for _, set := range sets {
			for _, sp := range set.Spans {
				key := tr.TraceID + "/" + sp.SpanID
				if seen[key] {
					continue
				}
				seen[key] = true
				out = append(out, tempoSearchSpanToTrace(tr.TraceID, sp))
			}
		}
	}
	return out, nil
}

func tempoSearchSpanToTrace(traceID string, sp tempoSearchSpan) common.OpenTelemetryTrace {
	attrs := tempoAttrs(sp.Attributes)
	start, _ := strconv.ParseInt(sp.StartTimeUnixNano, 10, 64)
	dur, _ := strconv.ParseInt(sp.DurationNanos, 10, 64)

	parent := attrs["span:parentID"]
	if strings.Trim(parent, "0") == "" {
		parent = ""
	}
	res := map[string]string{}
	for _, k := range tempoResourceKeys {
		if v := attrs[k]; v != "" {
			res[k] = v
		}
	}
	spanAttrs := map[string]string{}
	for k, v := range attrs {
		if !tempoLiftedKeys[k] {
			spanAttrs[k] = v
		}
	}
	dest := tempoFirst(attrs["net.peer.name"], attrs["peer.service"])
	ts := time.Unix(0, start).UTC()
	return common.OpenTelemetryTrace{
		Timestamp:           ts.Format(time.RFC3339Nano),
		TraceID:             traceID,
		SpanID:              sp.SpanID,
		ParentSpanID:        parent,
		SpanName:            sp.Name,
		SpanKind:            tempoSpanKind(attrs["kind"]),
		ServiceName:         attrs["service.name"],
		ResourceAttributes:  res,
		SpanAttributes:      spanAttrs,
		DurationNs:          dur,
		StatusCode:          tempoStatusCode(attrs["status"]),
		WorkloadName:        attrs["service.name"],
		WorkloadNamespace:   attrs["k8s.namespace.name"],
		Resource:            tempoFirst(attrs["http.target"], attrs["url.path"]),
		DestinationName:     dest,
		DestinationWorkload: dest,
		HTTPStatusCode:      attrs["http.status_code"],
		StartTime:           ts.Format(time.RFC3339Nano),
		EndTime:             ts.Add(time.Duration(dur)).Format(time.RFC3339Nano),
		StartTimeUnixNano:   sp.StartTimeUnixNano,
		EndTimeUnixNano:     strconv.FormatInt(start+dur, 10),
		TraceSource:         tempoTraceSourceName,
	}
}

type tempoOTLPSpan struct {
	TraceID           string    `json:"traceId"`
	SpanID            string    `json:"spanId"`
	Name              string    `json:"name"`
	StartTimeUnixNano string    `json:"startTimeUnixNano"`
	EndTimeUnixNano   string    `json:"endTimeUnixNano"`
	Attributes        []tempoKV `json:"attributes"`
	Status            struct {
		Code string `json:"code"`
	} `json:"status"`
	Events []struct {
		Name       string    `json:"name"`
		Attributes []tempoKV `json:"attributes"`
	} `json:"events"`
}

type tempoOTLPResponse struct {
	Trace struct {
		ResourceSpans []struct {
			Resource struct {
				Attributes []tempoKV `json:"attributes"`
			} `json:"resource"`
			ScopeSpans []struct {
				Spans []tempoOTLPSpan `json:"spans"`
			} `json:"scopeSpans"`
		} `json:"resourceSpans"`
	} `json:"trace"`
}

// tempoOTLPID decodes the base64 ids /api/v2/traces returns into the hex form search
// and the rest of Nudgebee use.
func tempoOTLPID(b64 string) string {
	raw, err := base64.StdEncoding.DecodeString(b64)
	if err != nil || len(raw) == 0 {
		return b64
	}
	return hex.EncodeToString(raw)
}

func tempoOTLPToHeatmap(body []byte) ([]common.OpenTelemetryTraceHeatMap, error) {
	var resp tempoOTLPResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("grafana tempo: decoding trace response: %w", err)
	}
	out := []common.OpenTelemetryTraceHeatMap{}
	for _, rs := range resp.Trace.ResourceSpans {
		res := tempoAttrs(rs.Resource.Attributes)
		for _, ss := range rs.ScopeSpans {
			for _, sp := range ss.Spans {
				start, _ := strconv.ParseInt(sp.StartTimeUnixNano, 10, 64)
				end, _ := strconv.ParseInt(sp.EndTimeUnixNano, 10, 64)
				status := sp.Status.Code
				if status == "" {
					status = "STATUS_CODE_UNSET"
				}
				names := make([]string, 0, len(sp.Events))
				evAttrs := make([]map[string]string, 0, len(sp.Events))
				for _, ev := range sp.Events {
					names = append(names, ev.Name)
					evAttrs = append(evAttrs, tempoAttrs(ev.Attributes))
				}
				out = append(out, common.OpenTelemetryTraceHeatMap{
					Timestamp:          time.Unix(0, start).UTC().Format(time.RFC3339Nano),
					ResourceAttributes: res,
					SpanName:           sp.Name,
					StatusCode:         status,
					DurationNs:         end - start,
					SpanAttributes:     tempoAttrs(sp.Attributes),
					TraceID:            tempoOTLPID(sp.TraceID),
					SpanID:             tempoOTLPID(sp.SpanID),
					ServiceName:        res["service.name"],
					EventsName:         names,
					EventsAttributes:   evAttrs,
				})
			}
		}
	}
	return out, nil
}

type tempoSeries struct {
	Labels map[string]string
	Value  float64
}

func parseTempoMetricSeries(body []byte) ([]tempoSeries, error) {
	var resp struct {
		Series []struct {
			Labels []tempoKV `json:"labels"`
			Value  *float64  `json:"value"`
		} `json:"series"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("grafana tempo: decoding metrics response: %w", err)
	}
	out := make([]tempoSeries, 0, len(resp.Series))
	for _, s := range resp.Series {
		if s.Value == nil {
			continue
		}
		out = append(out, tempoSeries{Labels: tempoAttrs(s.Labels), Value: *s.Value})
	}
	return out, nil
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd api-server/services && go test ./observability/ -run 'TempoSearchToSpans|TempoOTLPToHeatmap|ParseTempoMetricSeries' -count=1 -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add api-server/services/observability/grafana_tempo_convert.go api-server/services/observability/grafana_tempo_convert_test.go
git commit -m "feat(nb-39485): convert Tempo search, trace and metrics responses"
```

---

### Task 6: `GrafanaTempoTraceSource` — listing, paging, counts, labels, waterfall

**Files:**
- Create: `api-server/services/observability/grafana_tempo_traces.go`
- Test: `api-server/services/observability/grafana_tempo_traces_test.go`

**Interfaces:**
- Consumes: Tasks 2–5 (`integrations.GetGrafanaTempoConfig`, `tempoGet`, `tempoTimeWindow`, `tempoSearchQuery`, `tempoAttrFor`, `tempoSelector`, `tempoSearchToSpans`, `tempoOTLPToHeatmap`, `tempoStatusCode`, `tempoTraceLabelMapping`, `tempoSupportedOperators`); existing `queryRootSpansViaSpans`, `countTracesByTraceEstimate`.
- Produces:
  - `type GrafanaTempoTraceSource struct{}` implementing `TraceSource` (grouping methods land in Task 7)
  - `func queryTempoSpans(cfg integrations.GrafanaTempoConfig, req TracesV3Request, now time.Time) ([]common.OpenTelemetryTrace, error)`
  - `func queryTempoLabels(cfg integrations.GrafanaTempoConfig, req FetchTraceLabelRequest, now time.Time) ([]OutputTraceLabel, error)`
  - `func queryTempoLabelValues(cfg integrations.GrafanaTempoConfig, req TracesV3LabelValuesRequest, now time.Time) ([]string, error)`
  - `func queryTempoHeatmap(cfg integrations.GrafanaTempoConfig, req TracesHeatMapRequest, now time.Time) ([]common.OpenTelemetryTraceHeatMap, error)`

- [ ] **Step 1: Write the failing tests** (a fake Tempo serves newest-first spans; page math is asserted without the network being the thing under test)

```go
package observability

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"nudgebee/services/integrations"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeTempoSearch returns n single-span traces, newest first, one second apart, and
// honours `limit` the way Tempo does (number of traces).
func fakeTempoSearch(t *testing.T, n int, calls *int32, lastParams *map[string]string) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(calls, 1)
		q := r.URL.Query()
		if lastParams != nil {
			*lastParams = map[string]string{"q": q.Get("q"), "start": q.Get("start"), "end": q.Get("end"), "limit": q.Get("limit"), "spss": q.Get("spss")}
		}
		limit, _ := strconv.Atoi(q.Get("limit"))
		if limit > n {
			limit = n
		}
		var traces []string
		for i := 0; i < limit; i++ {
			start := int64(1790626200000000000) - int64(i)*int64(time.Second)
			traces = append(traces, fmt.Sprintf(`{"traceID":"%032x","spanSets":[{"spans":[{"spanID":"%016x","name":"op","startTimeUnixNano":"%d","durationNanos":"1000","attributes":[{"key":"service.name","value":{"stringValue":"svc"}}]}]}]}`, i+1, i+1, start))
		}
		_, _ = w.Write([]byte(`{"traces":[` + strings.Join(traces, ",") + `]}`))
	}))
}

func tempoReq(offset, limit int) TracesV3Request {
	return TracesV3Request{AccountId: "acc", QueryRequest: TracesQueryBuilderRequest{Offset: offset, Limit: limit}}
}

func TestQueryTempoSpans_PagesDoNotOverlap(t *testing.T) {
	var calls int32
	var params map[string]string
	srv := fakeTempoSearch(t, 10, &calls, &params)
	defer srv.Close()
	cfg := integrations.GrafanaTempoConfig{URL: srv.URL}

	page0, err := queryTempoSpans(cfg, tempoReq(0, 3), time.Now())
	require.NoError(t, err)
	page1, err := queryTempoSpans(cfg, tempoReq(3, 3), time.Now())
	require.NoError(t, err)

	require.Len(t, page0, 3)
	require.Len(t, page1, 3)
	assert.Equal(t, "6", params["limit"], "page 2 fetches offset+size traces")
	seen := map[string]bool{}
	for _, s := range append(page0, page1...) {
		assert.False(t, seen[s.SpanID], "span %s appears on two pages", s.SpanID)
		seen[s.SpanID] = true
	}
	assert.Greater(t, page0[2].StartTimeUnixNano, page1[0].StartTimeUnixNano, "page 1 is newer than page 2")
	assert.Contains(t, params["q"], "with (most_recent=true)")
}

func TestQueryTempoSpans_OffsetBeyondCapIsEmpty(t *testing.T) {
	var calls int32
	srv := fakeTempoSearch(t, 10, &calls, nil)
	defer srv.Close()
	spans, err := queryTempoSpans(integrations.GrafanaTempoConfig{URL: srv.URL}, tempoReq(tempoSpanCap, 50), time.Now())
	require.NoError(t, err)
	assert.Empty(t, spans)
	assert.Equal(t, int32(0), calls, "no request is made for a page past the cap")
}

func TestQueryTempoSpans_ClampsWindow(t *testing.T) {
	var calls int32
	var params map[string]string
	srv := fakeTempoSearch(t, 1, &calls, &params)
	defer srv.Close()
	now := time.Now()
	req := tempoReq(0, 10)
	req.StartTime = now.Add(-30 * 24 * time.Hour).UnixMilli()
	req.EndTime = now.UnixMilli()
	_, err := queryTempoSpans(integrations.GrafanaTempoConfig{URL: srv.URL}, req, now)
	require.NoError(t, err)
	start, _ := strconv.ParseInt(params["start"], 10, 64)
	end, _ := strconv.ParseInt(params["end"], 10, 64)
	assert.Less(t, end-start, int64(168*3600))
}

func TestQueryTempoLabelsAndValues(t *testing.T) {
	var gotPath, gotQ string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath, gotQ = r.URL.Path, r.URL.Query().Get("q")
		switch {
		case r.URL.Path == "/api/v2/search/tags":
			_, _ = w.Write([]byte(`{"scopes":[{"name":"intrinsic","tags":["status"]},{"name":"resource","tags":["service.name"]},{"name":"span","tags":["http.target"]}]}`))
		case strings.HasSuffix(r.URL.Path, "/status/values"):
			_, _ = w.Write([]byte(`{"tagValues":[{"type":"keyword","value":"error"},{"type":"keyword","value":"ok"}]}`))
		default:
			_, _ = w.Write([]byte(`{"tagValues":[{"type":"string","value":"nudgebee"},{"type":"string","value":"demo"}]}`))
		}
	}))
	defer srv.Close()
	cfg := integrations.GrafanaTempoConfig{URL: srv.URL}

	labels, err := queryTempoLabels(cfg, FetchTraceLabelRequest{AccountId: "acc"}, time.Now())
	require.NoError(t, err)
	var names []string
	for _, l := range labels {
		names = append(names, l.Label)
	}
	assert.Equal(t, []string{"resource.service.name", "span.http.target"}, names)

	vals, err := queryTempoLabelValues(cfg, TracesV3LabelValuesRequest{AccountId: "acc", Label: "workload_namespace"}, time.Now())
	require.NoError(t, err)
	assert.Equal(t, "/api/v2/search/tag/resource.k8s.namespace.name/values", gotPath)
	assert.Equal(t, "{}", gotQ)
	assert.Equal(t, []string{"demo", "nudgebee"}, vals)

	vals, err = queryTempoLabelValues(cfg, TracesV3LabelValuesRequest{AccountId: "acc", Label: "status_code"}, time.Now())
	require.NoError(t, err)
	assert.Equal(t, []string{"STATUS_CODE_ERROR", "STATUS_CODE_OK"}, vals)
}

func TestQueryTempoHeatmap(t *testing.T) {
	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		_, _ = w.Write([]byte(tempoOTLPFixture))
	}))
	defer srv.Close()
	out, err := queryTempoHeatmap(integrations.GrafanaTempoConfig{URL: srv.URL},
		TracesHeatMapRequest{AccountId: "acc", TraceId: "5b8efff798038103d269b633813fc60c"}, time.Now())
	require.NoError(t, err)
	assert.Equal(t, "/api/v2/traces/5b8efff798038103d269b633813fc60c", gotPath)
	require.Len(t, out, 1)
}

func TestGrafanaTempoTraceSource_StaticContract(t *testing.T) {
	var src TraceSource = &GrafanaTempoTraceSource{}
	assert.Equal(t, tempoTraceLabelMapping, src.GetLabelMapping())
	assert.Equal(t, tempoSupportedOperators, src.GetSupportedOperators())
	count, err := src.CountTraces(nil, TracesV3Request{})
	require.NoError(t, err)
	assert.Equal(t, -1, count.Count, "listing counts are estimates so they never depend on Tempo's metrics engine")
}
```

- [ ] **Step 2: Run to verify they fail**

Run: `cd api-server/services && go test ./observability/ -run 'QueryTempo|GrafanaTempoTraceSource' -count=1`
Expected: FAIL — `undefined: queryTempoSpans`.

- [ ] **Step 3: Implement `grafana_tempo_traces.go`** (grouping methods return an error until Task 7 replaces them)

```go
package observability

import (
	"encoding/json"
	"fmt"
	neturl "net/url"
	"sort"
	"strconv"
	"time"

	"nudgebee/services/common"
	"nudgebee/services/integrations"
	"nudgebee/services/query"
	"nudgebee/services/security"
)

// GrafanaTempoTraceSource reads spans from Grafana Tempo's HTTP API. Listing uses
// TraceQL search, the waterfall uses trace-by-id, grouping uses TraceQL metrics. Counts
// are estimates: Tempo has no count API, and deriving one from TraceQL metrics would tie
// the listing to an optional, 24h-capped engine.
type GrafanaTempoTraceSource struct{}

var _ TraceSource = (*GrafanaTempoTraceSource)(nil)

func tempoTraceConfig(ctx *security.RequestContext, accountId string) (integrations.GrafanaTempoConfig, error) {
	cfg, err := integrations.GetGrafanaTempoConfig(ctx, accountId)
	if err != nil {
		return cfg, fmt.Errorf("failed to get Grafana Tempo config: %w", err)
	}
	return cfg, nil
}

func sortTempoSpans(spans []common.OpenTelemetryTrace, orderBy []query.QueryOrderBy) {
	col, desc := "timestamp", true
	if len(orderBy) > 0 {
		col = orderBy[0].Column
		o := orderBy[0].Order
		desc = o == query.Desc || o == query.DescNullsFirst || o == query.DescNullsLast
	}
	key := func(s common.OpenTelemetryTrace) int64 {
		if col == "duration_ns" {
			return s.DurationNs
		}
		v, _ := strconv.ParseInt(s.StartTimeUnixNano, 10, 64)
		return v
	}
	sort.SliceStable(spans, func(i, j int) bool {
		if desc {
			return key(spans[i]) > key(spans[j])
		}
		return key(spans[i]) < key(spans[j])
	})
}

// queryTempoSpans serves one page. /api/search has no offset, so it fetches the newest
// offset+size traces (with up to that many spans each), sorts and slices. Beyond
// tempoSpanCap the page is empty rather than a re-shuffled sample.
func queryTempoSpans(cfg integrations.GrafanaTempoConfig, req TracesV3Request, now time.Time) ([]common.OpenTelemetryTrace, error) {
	size := req.QueryRequest.Limit
	if size <= 0 {
		size = tempoDefaultPageSize
	}
	offset := req.QueryRequest.Offset
	if offset < 0 {
		offset = 0
	}
	need := offset + size
	if need > tempoSpanCap {
		need = tempoSpanCap
	}
	if offset >= need {
		return []common.OpenTelemetryTrace{}, nil
	}
	q, err := tempoSearchQuery(req.Query, req.QueryRequest.Where)
	if err != nil {
		return nil, err
	}
	params := tempoTimeWindow(req.StartTime, req.EndTime, tempoSearchMaxWindow, now).params()
	params["q"] = q
	params["limit"] = strconv.Itoa(need)
	params["spss"] = strconv.Itoa(need)
	body, err := tempoGet(cfg, "/api/search", params)
	if err != nil {
		return nil, err
	}
	spans, err := tempoSearchToSpans(body)
	if err != nil {
		return nil, err
	}
	sortTempoSpans(spans, req.QueryRequest.OrderBy)
	end := need
	if end > len(spans) {
		end = len(spans)
	}
	if offset >= end {
		return []common.OpenTelemetryTrace{}, nil
	}
	return spans[offset:end], nil
}

func queryTempoLabels(cfg integrations.GrafanaTempoConfig, req FetchTraceLabelRequest, now time.Time) ([]OutputTraceLabel, error) {
	body, err := tempoGet(cfg, "/api/v2/search/tags", tempoTimeWindow(req.StartTime, req.EndTime, tempoSearchMaxWindow, now).params())
	if err != nil {
		return nil, err
	}
	var resp struct {
		Scopes []struct {
			Name string   `json:"name"`
			Tags []string `json:"tags"`
		} `json:"scopes"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("grafana tempo: decoding tags: %w", err)
	}
	out := []OutputTraceLabel{}
	for _, s := range resp.Scopes {
		if s.Name != "resource" && s.Name != "span" {
			continue // intrinsics are already covered by the canonical fields
		}
		for _, tag := range s.Tags {
			out = append(out, OutputTraceLabel{Label: s.Name + "." + tag, Attributes: map[string]any{}})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Label < out[j].Label })
	return out, nil
}

func queryTempoLabelValues(cfg integrations.GrafanaTempoConfig, req TracesV3LabelValuesRequest, now time.Time) ([]string, error) {
	attr, err := tempoAttrFor(req.Label)
	if err != nil {
		return nil, err
	}
	sel, err := tempoSelector(req.QueryRequest.Where)
	if err != nil {
		return nil, err
	}
	params := tempoTimeWindow(req.StartTime, req.EndTime, tempoSearchMaxWindow, now).params()
	params["q"] = sel
	body, err := tempoGet(cfg, "/api/v2/search/tag/"+neturl.PathEscape(attr)+"/values", params)
	if err != nil {
		return nil, err
	}
	var resp struct {
		TagValues []struct {
			Value string `json:"value"`
		} `json:"tagValues"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("grafana tempo: decoding tag values: %w", err)
	}
	seen := map[string]bool{}
	out := []string{}
	for _, tv := range resp.TagValues {
		v := tv.Value
		if attr == "status" {
			v = tempoStatusCode(v)
		}
		if v != "" && !seen[v] {
			seen[v] = true
			out = append(out, v)
		}
	}
	sort.Strings(out)
	return out, nil
}

func queryTempoHeatmap(cfg integrations.GrafanaTempoConfig, req TracesHeatMapRequest, now time.Time) ([]common.OpenTelemetryTraceHeatMap, error) {
	params := tempoTimeWindow(req.StartTime, req.EndTime, tempoSearchMaxWindow, now).params()
	body, err := tempoGet(cfg, "/api/v2/traces/"+neturl.PathEscape(req.TraceId), params)
	if err != nil {
		return nil, err
	}
	return tempoOTLPToHeatmap(body)
}

func (s *GrafanaTempoTraceSource) QueryTraces(ctx *security.RequestContext, req TracesV3Request) ([]common.OpenTelemetryTrace, error) {
	cfg, err := tempoTraceConfig(ctx, req.AccountId)
	if err != nil {
		return nil, err
	}
	return queryTempoSpans(cfg, req, time.Now())
}

func (s *GrafanaTempoTraceSource) GetQuery(ctx *security.RequestContext, req TracesV3Request) (string, error) {
	return tempoSearchQuery(req.Query, req.QueryRequest.Where)
}

func (s *GrafanaTempoTraceSource) CountTraces(ctx *security.RequestContext, req TracesV3Request) (common.OpenTelemetryTraceCount, error) {
	return countTracesByTraceEstimate()
}

func (s *GrafanaTempoTraceSource) CountTracesByTrace(ctx *security.RequestContext, req TracesV3Request) (common.OpenTelemetryTraceCount, error) {
	return countTracesByTraceEstimate()
}

func (s *GrafanaTempoTraceSource) QueryRootSpansByTrace(ctx *security.RequestContext, req TracesV3Request) ([]common.OpenTelemetryTrace, error) {
	return queryRootSpansViaSpans(ctx, s, req)
}

func (s *GrafanaTempoTraceSource) GetLabelValues(ctx *security.RequestContext, req TracesV3LabelValuesRequest) (common.OpenTelemetryTraceLabelValues, error) {
	cfg, err := tempoTraceConfig(ctx, req.AccountId)
	if err != nil {
		return common.OpenTelemetryTraceLabelValues{}, err
	}
	vals, err := queryTempoLabelValues(cfg, req, time.Now())
	if err != nil {
		return common.OpenTelemetryTraceLabelValues{}, err
	}
	return common.OpenTelemetryTraceLabelValues{Label: req.Label, Values: vals}, nil
}

func (s *GrafanaTempoTraceSource) QueryLabels(ctx *security.RequestContext, req FetchTraceLabelRequest) ([]OutputTraceLabel, error) {
	cfg, err := tempoTraceConfig(ctx, req.AccountId)
	if err != nil {
		return nil, err
	}
	return queryTempoLabels(cfg, req, time.Now())
}

func (s *GrafanaTempoTraceSource) QueryTracesHeatmap(ctx *security.RequestContext, req TracesHeatMapRequest) ([]common.OpenTelemetryTraceHeatMap, error) {
	cfg, err := tempoTraceConfig(ctx, req.AccountId)
	if err != nil {
		return nil, err
	}
	return queryTempoHeatmap(cfg, req, time.Now())
}

func (s *GrafanaTempoTraceSource) QueryGroupedTraces(ctx *security.RequestContext, req TracesV3Request) ([]TraceGroupingValues, error) {
	return nil, fmt.Errorf("grafana tempo: grouping not implemented yet")
}

func (s *GrafanaTempoTraceSource) QueryGroupedTracesCount(ctx *security.RequestContext, req TracesV3Request) (common.OpenTelemetryTraceGroupCount, error) {
	return common.OpenTelemetryTraceGroupCount{}, fmt.Errorf("grafana tempo: grouping not implemented yet")
}

func (s *GrafanaTempoTraceSource) GetLabelMapping() map[string]string { return tempoTraceLabelMapping }

func (s *GrafanaTempoTraceSource) GetSupportedOperators() []string { return tempoSupportedOperators }
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd api-server/services && go test ./observability/ -run 'QueryTempo|GrafanaTempoTraceSource' -count=1 -v`
Expected: PASS.

- [ ] **Step 5: Probe the status tag-values endpoint on the real Tempo**

Run: `E=$(date +%s); curl -s -G "http://localhost:3200/api/v2/search/tag/status/values" --data-urlencode 'q={}' --data-urlencode start=$((E-900)) --data-urlencode end=$E`
Expected: a `tagValues` list containing `error`/`ok`. If Tempo returns 400 for the intrinsic, make `queryTempoLabelValues` return the fixed `[STATUS_CODE_ERROR, STATUS_CODE_OK, STATUS_CODE_UNSET]` for `attr == "status"` without calling Tempo, and adjust the test's status assertion to that list.

- [ ] **Step 6: Commit**

```bash
git add api-server/services/observability/grafana_tempo_traces.go api-server/services/observability/grafana_tempo_traces_test.go
git commit -m "feat(nb-39485): Grafana Tempo trace source listing, labels and waterfall"
```

---

### Task 7: Grouped view from TraceQL metrics

**Files:**
- Modify: `api-server/services/observability/grafana_tempo_traces.go` (replace the two grouping stubs)
- Test: `api-server/services/observability/grafana_tempo_traces_test.go` (append)

**Interfaces:**
- Consumes: `buildTempoGroupQueries`, `parseTempoMetricSeries`, `tempoTimeWindow`, `tempoMetricsMaxWindow`, `tempoGet`.
- Produces:
  - `func mergeTempoGroups(count, errs, quantiles, maxes []tempoSeries) []TraceGroupingValues`
  - `func queryTempoGroups(cfg integrations.GrafanaTempoConfig, req TracesV3Request, now time.Time) ([]TraceGroupingValues, error)`

- [ ] **Step 1: Write the failing tests** (append to `grafana_tempo_traces_test.go`)

```go
func tempoLabels(svc, ns, name, peer string) map[string]string {
	l := map[string]string{"resource.service.name": svc, "resource.k8s.namespace.name": ns, "name": name}
	if peer != "" {
		l["span.net.peer.name"] = peer
	}
	return l
}

func withP(l map[string]string, p string) map[string]string {
	out := map[string]string{"p": p}
	for k, v := range l {
		out[k] = v
	}
	return out
}

func TestMergeTempoGroups(t *testing.T) {
	fe := tempoLabels("frontend", "demo", "GET /checkout", "payments")
	rows := mergeTempoGroups(
		[]tempoSeries{{Labels: fe, Value: 12}},
		[]tempoSeries{{Labels: fe, Value: 2}},
		[]tempoSeries{{Labels: withP(fe, "0.95"), Value: 0.51}, {Labels: withP(fe, "0.99"), Value: 0.536870912}},
		[]tempoSeries{{Labels: fe, Value: 0.358}},
	)
	require.Len(t, rows, 1)
	r := rows[0]
	assert.Equal(t, 12, r.Count)
	assert.Equal(t, 2, r.ErrorCount)
	assert.Equal(t, int64(510000000), r.P95Latency, "TraceQL metrics durations are seconds")
	assert.Equal(t, int64(536870912), r.P99Latency)
	assert.Equal(t, int64(358000000), r.MaxLatency)
	assert.Equal(t, r.P99Latency, r.DurationNS)
	assert.Equal(t, "frontend", r.WorkloadName)
	assert.Equal(t, "demo", r.WorkloadNamespace)
	assert.Equal(t, "GET /checkout", r.SpanName)
	assert.Equal(t, "payments", r.DestinationWorkloadName)
}

func TestMergeTempoGroups_MissingLabel(t *testing.T) {
	leaf := tempoLabels("ledger", "demo", "GET /entries", "")
	rows := mergeTempoGroups([]tempoSeries{{Labels: leaf, Value: 36}}, nil,
		[]tempoSeries{{Labels: withP(leaf, "0.95"), Value: 0.2}}, nil)
	require.Len(t, rows, 1)
	assert.Equal(t, "", rows[0].DestinationWorkloadName)
	assert.Equal(t, 36, rows[0].Count)
	assert.Equal(t, 0, rows[0].ErrorCount, "no error series means zero errors")
}

func TestQueryTempoGroups_ClampsAndFailsWhole(t *testing.T) {
	var maxWindow int64
	fail := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		s, _ := strconv.ParseInt(q.Get("start"), 10, 64)
		e, _ := strconv.ParseInt(q.Get("end"), 10, 64)
		if e-s > maxWindow {
			maxWindow = e - s
		}
		if fail && strings.Contains(q.Get("q"), "max_over_time") {
			w.WriteHeader(400)
			_, _ = w.Write([]byte("metrics query time range exceeds the maximum allowed duration of 24h0m0s"))
			return
		}
		_, _ = w.Write([]byte(`{"series":[{"labels":[{"key":"resource.service.name","value":{"stringValue":"frontend"}}],"value":3}]}`))
	}))
	defer srv.Close()
	cfg := integrations.GrafanaTempoConfig{URL: srv.URL}
	now := time.Now()
	req := TracesV3Request{AccountId: "acc", StartTime: now.Add(-7 * 24 * time.Hour).UnixMilli(), EndTime: now.UnixMilli()}

	rows, err := queryTempoGroups(cfg, req, now)
	require.NoError(t, err)
	require.Len(t, rows, 1)
	assert.Less(t, maxWindow, int64(24*3600), "grouping is clamped to Tempo's 24h metrics limit")

	fail = true
	_, err = queryTempoGroups(cfg, req, now)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "24h0m0s")
}
```

- [ ] **Step 2: Run to verify they fail**

Run: `cd api-server/services && go test ./observability/ -run 'MergeTempoGroups|QueryTempoGroups' -count=1`
Expected: FAIL — `undefined: mergeTempoGroups`.

- [ ] **Step 3: Implement** — add `"sync"` to the imports of `grafana_tempo_traces.go`, add the functions below, and replace the two grouping stubs:

```go
type tempoGroupKey struct{ service, namespace, span, dest string }

func tempoKeyOf(l map[string]string) tempoGroupKey {
	return tempoGroupKey{l["resource.service.name"], l["resource.k8s.namespace.name"], l["name"], l["span.net.peer.name"]}
}

// mergeTempoGroups joins the four TraceQL-metrics results on their shared group labels.
// A label Tempo left out (a leaf service has no span.net.peer.name) is an empty key part,
// so the series still merge into one row. Durations arrive in seconds.
func mergeTempoGroups(count, errs, quantiles, maxes []tempoSeries) []TraceGroupingValues {
	rows := map[tempoGroupKey]*TraceGroupingValues{}
	var order []tempoGroupKey
	row := func(l map[string]string) *TraceGroupingValues {
		k := tempoKeyOf(l)
		if r, ok := rows[k]; ok {
			return r
		}
		r := &TraceGroupingValues{WorkloadName: k.service, WorkloadNamespace: k.namespace, SpanName: k.span, DestinationWorkloadName: k.dest}
		rows[k] = r
		order = append(order, k)
		return r
	}
	ns := func(sec float64) int64 { return int64(sec * float64(time.Second)) }
	for _, s := range count {
		row(s.Labels).Count = int(s.Value)
	}
	for _, s := range errs {
		row(s.Labels).ErrorCount = int(s.Value)
	}
	for _, s := range quantiles {
		r := row(s.Labels)
		switch s.Labels["p"] {
		case "0.95":
			r.P95Latency = ns(s.Value)
		case "0.99":
			r.P99Latency = ns(s.Value)
			r.DurationNS = ns(s.Value)
		}
	}
	for _, s := range maxes {
		row(s.Labels).MaxLatency = ns(s.Value)
	}
	out := make([]TraceGroupingValues, 0, len(order))
	for _, k := range order {
		out = append(out, *rows[k])
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Count != out[j].Count {
			return out[i].Count > out[j].Count
		}
		return out[i].WorkloadName < out[j].WorkloadName
	})
	return out
}

// queryTempoGroups runs the four instant queries concurrently over at most the last 24h
// (Tempo's TraceQL-metrics limit). Any failure fails the grouped view only; the span
// listing never calls this.
func queryTempoGroups(cfg integrations.GrafanaTempoConfig, req TracesV3Request, now time.Time) ([]TraceGroupingValues, error) {
	qs, err := buildTempoGroupQueries(req.Query, req.QueryRequest.Where)
	if err != nil {
		return nil, err
	}
	win := tempoTimeWindow(req.StartTime, req.EndTime, tempoMetricsMaxWindow, now)
	queries := []string{qs.Count, qs.Errors, qs.Quantiles, qs.Max}
	results := make([][]tempoSeries, len(queries))
	errs := make([]error, len(queries))
	var wg sync.WaitGroup
	for i, q := range queries {
		wg.Add(1)
		go func(i int, q string) {
			defer wg.Done()
			params := win.params()
			params["q"] = q
			body, err := tempoGet(cfg, "/api/metrics/query", params)
			if err != nil {
				errs[i] = err
				return
			}
			results[i], errs[i] = parseTempoMetricSeries(body)
		}(i, q)
	}
	wg.Wait()
	for _, err := range errs {
		if err != nil {
			return nil, fmt.Errorf("grafana tempo grouping: %w", err)
		}
	}
	return mergeTempoGroups(results[0], results[1], results[2], results[3]), nil
}

func (s *GrafanaTempoTraceSource) QueryGroupedTraces(ctx *security.RequestContext, req TracesV3Request) ([]TraceGroupingValues, error) {
	cfg, err := tempoTraceConfig(ctx, req.AccountId)
	if err != nil {
		return nil, err
	}
	rows, err := queryTempoGroups(cfg, req, time.Now())
	if err != nil {
		return nil, err
	}
	offset, limit := req.QueryRequest.Offset, req.QueryRequest.Limit
	if offset < 0 || offset >= len(rows) {
		return []TraceGroupingValues{}, nil
	}
	rows = rows[offset:]
	if limit > 0 && limit < len(rows) {
		rows = rows[:limit]
	}
	return rows, nil
}

func (s *GrafanaTempoTraceSource) QueryGroupedTracesCount(ctx *security.RequestContext, req TracesV3Request) (common.OpenTelemetryTraceGroupCount, error) {
	cfg, err := tempoTraceConfig(ctx, req.AccountId)
	if err != nil {
		return common.OpenTelemetryTraceGroupCount{}, err
	}
	rows, err := queryTempoGroups(cfg, req, time.Now())
	if err != nil {
		return common.OpenTelemetryTraceGroupCount{}, err
	}
	return common.OpenTelemetryTraceGroupCount{Count: len(rows)}, nil
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd api-server/services && go test ./observability/ -run 'MergeTempoGroups|QueryTempoGroups|QueryTempo|GrafanaTempo' -count=1 -race -v`
Expected: PASS, no race reports.

- [ ] **Step 5: Commit**

```bash
git add api-server/services/observability/grafana_tempo_traces.go api-server/services/observability/grafana_tempo_traces_test.go
git commit -m "feat(nb-39485): grouped trace view from Tempo TraceQL metrics"
```

---

### Task 8: Wire the source into trace dispatch and pin it

**Files:**
- Modify: `api-server/services/observability/service.go` (`getTraceSource` ~L431, `allProviderCaps` ~L2065)
- Modify: `api-server/services/observability/trace_source_canonical_fields_test.go` (cases ~L142, `registered` ~L187)
- Test: `api-server/services/observability/grafana_tempo_traces_test.go` (append)

- [ ] **Step 1: Write the failing tests** — append to `grafana_tempo_traces_test.go`:

```go
func TestGrafanaTempo_DispatchesAsTraceSourceOnly(t *testing.T) {
	src, err := getTraceSource("grafana_tempo", "user")
	require.NoError(t, err)
	assert.IsType(t, &GrafanaTempoTraceSource{}, src)

	_, err = getMetricsSource("grafana_tempo", "user")
	assert.Error(t, err, "Tempo must not become a metrics provider (docs/architecture-decisions.md)")

	caps, ok := allProviderCaps["grafana_tempo"]
	require.True(t, ok)
	assert.True(t, caps.SupportsTraceGrouping)
	assert.True(t, caps.SupportsHeatmap)
	assert.True(t, caps.SupportsRawQuery)
	assert.False(t, caps.SupportsServiceMap)
}
```

and in `trace_source_canonical_fields_test.go` add this case after the splunk enterprise one:

```go
	{
		name: "grafana tempo advertises mapped fields", provider: "grafana_tempo", source: "user", declared: true,
		expected: []string{"destination_workload_name", "duration_ns", "http_status_code", "resource", "service_name",
			"span_name", "status_code", "trace_id", "workload_name", "workload_namespace"},
	},
```

and append `"grafana_tempo/user"` to the `registered` list.

- [ ] **Step 2: Run to verify they fail**

Run: `cd api-server/services && go test ./observability/ -run 'GrafanaTempo_Dispatches|TestTraceSources_' -count=1`
Expected: FAIL — `unsupported … provider=grafana_tempo`.

- [ ] **Step 3: Implement** — in `getTraceSource`, directly after the `splunk_enterprise` case:

```go
	case provider == "grafana_tempo" && integrationSource == "user":
		return &GrafanaTempoTraceSource{}, nil
```

and in `allProviderCaps`, after `"openobserve"`:

```go
	// Traces only. Grouping is computed by Tempo itself (TraceQL metrics, ≤24h); the
	// waterfall is trace-by-id. ServiceMap stays false for v1: edges would need a TraceQL
	// structural query, deferred (spec §1).
	"grafana_tempo": {
		SupportsServiceMap:    false,
		SupportsTraceGrouping: true,
		SupportsHeatmap:       true,
		SupportsRawQuery:      true,
	},
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd api-server/services && go test ./observability/ -run 'GrafanaTempo|TestTraceSources_' -count=1 -v`
Expected: PASS. If the advertised set differs from the expectation, the difference is a canonical field the mapping does or doesn't resolve — fix the mapping (Task 4), not the expectation.

- [ ] **Step 5: Full backend validation**

Run: `cd api-server/services && make validate`
Expected: fmt clean, `golangci-lint` clean, all tests pass.

- [ ] **Step 6: Commit**

```bash
git add api-server/services/observability/service.go api-server/services/observability/trace_source_canonical_fields_test.go api-server/services/observability/grafana_tempo_traces_test.go
git commit -m "feat(nb-39485): route grafana_tempo traces to the Tempo source"
```

---

### Task 9: Frontend — enable the Grafana Tempo tile and form

**Files:**
- Modify: `app/src/components/accounts/integration.jsx:33,158`
- Modify: `app/src/utils/common.ts:321`, `app/src/lib/integrationDescriptions.ts:104`, `app/src/components/common/icons/CloudIcon.jsx:188`
- Modify: `app/src/api1/account/index.ts:496`, `app/src/pages/accounts/account-form.jsx:234`, `app/src/pages/accounts/ListIntegrations.jsx:88`
- Modify: `app/src/components/integrations/modal/IntegrationDynamicFormModal.jsx:100`, `app/src/lib/navSearchPages.ts:301`
- Test: `app/__tests__/lib/navSearchPages.test.ts` (existing drift test), `app/__tests__/components/common/icons/CloudIcon.test.jsx`

- [ ] **Step 1: Baseline the drift test before changing anything**

Run: `cd app && npx jest __tests__/lib/navSearchPages.test.ts`
Record the result. If it already fails on `main` (missing `OPENOBSERVE`, `CUBEAPM`, `SPLUNK_ENTERPRISE`), fix that in its own commit first (add the missing keys to `integrationProviders` in the observability block), commit as `fix(ui): list OpenObserve, CubeAPM and Splunk Enterprise in integration search`, and re-run until green.

- [ ] **Step 2: Rename the key and enable it**

- `integration.jsx:33`: remove `'GRAFANA-TEMPO'` from `DISABLED_PROVIDERS`.
- `integration.jsx:158`: `'GRAFANA-TEMPO',` → `'GRAFANA_TEMPO',`.
- `common.ts:321`: `case 'GRAFANA-TEMPO':` → `case 'GRAFANA_TEMPO':`.
- `integrationDescriptions.ts:104`: `'GRAFANA-TEMPO': 'Query Grafana Tempo for distributed traces.',` → `GRAFANA_TEMPO: 'Query Grafana Tempo for distributed traces.',`.
- `CloudIcon.jsx:188`: `=== 'GRAFANA-TEMPO'` → `=== 'GRAFANA_TEMPO'`.
- Run `grep -rn "GRAFANA-TEMPO" app/src app/__tests__` — expected: no output.

- [ ] **Step 3: Register the type in the per-type lists**

- `api1/account/index.ts`: add `'grafana_tempo',` after `'splunk_enterprise',`.
- `account-form.jsx`: after the `splunk_enterprise` case add
  ```jsx
          case 'grafana_tempo':
            return <ListIntegrations integrationName={'grafana_tempo'} />;
  ```
- `ListIntegrations.jsx:88`: add `grafana_tempo: 'tempo_url',` after `splunk_enterprise: 'splunk_url',`.
- `IntegrationDynamicFormModal.jsx`: add `'grafana_tempo',` to `TRACE_INTEGRATIONS` after `'splunk_enterprise',`.
- `navSearchPages.ts`: add `'GRAFANA_TEMPO',` after `'JAEGER',` in `integrationProviders`.

- [ ] **Step 4: Run frontend validation**

Run: `cd app && npm run lint2 && npm run test`
Expected: lint clean (including `check-ds`), all tests pass — the drift test now requires `GRAFANA_TEMPO` in `integrationProviders`, which Step 3 added.

- [ ] **Step 5: Commit**

```bash
git add app/src app/__tests__
git commit -m "feat(nb-39485): enable the Grafana Tempo integration tile"
```

---

### Task 10: Observe it working in the local UI (acceptance criteria)

**Files:** none (evidence saved under the session scratchpad as `tempo-evidence/*.png`)

- [ ] **Step 1: Bring the stack up** — Tempo port-forward alive (`curl -s http://localhost:3200/ready` → `ready`; if not, restart the reconnecting loop `while true; do kubectl port-forward -n splunk svc/tempo 3200:3200 14318:4318; sleep 2; done`). Restart the local api-server the way it is currently run (find its command line with `powershell -c "Get-CimInstance Win32_Process -Filter 'ProcessId=<pid on :8000>' | select CommandLine"`) so it picks up the new Go code; the Next.js dev server on :3000 hot-reloads.

- [ ] **Step 2: Criterion 1 — tile and form.** Admin → Integrations → Grafana Tempo (tile enabled) → Add. Fill name `k8s-dev-tempo`, account `k8s-dev`, URL `http://localhost:3200`, auth `none`, tick **Default Traces Provider**. Confirm there is no Default Metrics/Log Provider box. Test Connection → success; Save. Screenshot.

- [ ] **Step 3: Criterion 2 — listing and labels.** Infra → k8s-dev → Traces. Badge reads **Grafana Tempo**. Rows show workload/namespace/destination (`frontend` / `demo` / `payments`). Filter namespace `demo` → only `demo`. Filter status Error → rows show Error + HTTP 503. Screenshot each.

- [ ] **Step 4: Criterion 3 — paging.** Go to page 2: its first row is older than page 1's last row and no span id repeats.

- [ ] **Step 5: Criterion 4 — waterfall.** Open a `frontend` trace: the waterfall shows frontend → payments → ledger. Screenshot.

- [ ] **Step 6: Criterion 5 — grouping and long windows.** Switch to the grouped view: per-operation count, error count, p95/p99/max populated. Pick "Last 7 days": listing still loads; grouped view still loads (clamped to 24h). Screenshot.

- [ ] **Step 7: Criterion 6 — provenance.** Copy a trace id from the Tempo listing and confirm it in both backends:
  `curl -s http://localhost:3200/api/v2/traces/<id> | head -c 200` (Tempo) and, in Splunk, `index=otel_traces trace_id=<id>` via the 8089 port-forward. Then switch the account's default trace provider back to Splunk (`k8s-dev-splunk`) and confirm the badge changes; switch back to Tempo.

- [ ] **Step 8: Criterion 7 — nothing else moved.** Logs tab still shows Splunk; Metrics tab badge still shows the VictoriaMetrics/Prometheus provider and does **not** list Grafana Tempo in its switcher.

- [ ] **Step 9: Record** the evidence list (criterion → screenshot path / command output) for the PR body. No commit.

---

### Task 11: Review, validate, open the PR

- [ ] **Step 1:** `cd api-server/services && make validate` and `cd app && npm run lint2 && npm run test` — both green.
- [ ] **Step 2:** Fresh-context review of the branch diff (`/judge`, or the `correctness-critic` agent on `git diff origin/main...HEAD`); fix confirmed findings with new commits.
- [ ] **Step 3:** Confirm no attribution: `git log origin/main..HEAD --format=%B | grep -ci "co-authored-by"` → `0`.
- [ ] **Step 4:** Open the PR with `/create-pr` against `main`: title `feat(nb-39485): show Grafana Tempo traces in the Traces tab`, body per `docs/writing-for-readers.md` with `Fixes #39485`, reader-side verification steps (Task 10 criteria), and the evidence; **no Claude footer**.
- [ ] **Step 5:** Watch CI in the background (`gh pr checks --watch`), triage failures and bot review comments, ≤4 fix-and-push cycles.

