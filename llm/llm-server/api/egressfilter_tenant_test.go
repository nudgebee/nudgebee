package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"nudgebee/llm/security/egressfilter"
)

// setupEgressfilterRouter builds a gin engine wired to the tenant-config
// endpoints. Auth is enforced by global middleware in cmd/main.go that
// these tests don't exercise — every request here is treated as authed.
func setupEgressfilterRouter(t *testing.T) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)
	r := gin.New()
	handleEgressfilterTenantApis(r)
	return r
}

// stubDAO replaces the DAO function variables for the duration of one test
// and restores them on cleanup. Pass nil for any function the test
// shouldn't exercise; the stub will t.Fatal if it's invoked unexpectedly.
func stubDAO(t *testing.T,
	get func(context.Context, uuid.UUID) (*egressfilter.TenantConfig, error),
	upsert func(context.Context, *egressfilter.TenantConfig) error,
	del func(context.Context, uuid.UUID) error,
	list func(context.Context, int) ([]egressfilter.TenantConfig, error),
) {
	t.Helper()
	prevGet, prevUpsert, prevDel, prevList :=
		daoGetTenantConfig, daoUpsertTenantConfig, daoDeleteTenantConfig, daoListTenantConfigs

	if get == nil {
		get = func(_ context.Context, _ uuid.UUID) (*egressfilter.TenantConfig, error) {
			t.Fatalf("unexpected daoGetTenantConfig call")
			return nil, nil
		}
	}
	if upsert == nil {
		upsert = func(_ context.Context, _ *egressfilter.TenantConfig) error {
			t.Fatalf("unexpected daoUpsertTenantConfig call")
			return nil
		}
	}
	if del == nil {
		del = func(_ context.Context, _ uuid.UUID) error {
			t.Fatalf("unexpected daoDeleteTenantConfig call")
			return nil
		}
	}
	if list == nil {
		list = func(_ context.Context, _ int) ([]egressfilter.TenantConfig, error) {
			t.Fatalf("unexpected daoListTenantConfigs call")
			return nil, nil
		}
	}
	daoGetTenantConfig = get
	daoUpsertTenantConfig = upsert
	daoDeleteTenantConfig = del
	daoListTenantConfigs = list

	t.Cleanup(func() {
		daoGetTenantConfig = prevGet
		daoUpsertTenantConfig = prevUpsert
		daoDeleteTenantConfig = prevDel
		daoListTenantConfigs = prevList
	})
}

func doRequest(t *testing.T, r *gin.Engine, method, path string, body any) *httptest.ResponseRecorder {
	t.Helper()
	var reader *bytes.Reader
	if body != nil {
		b, err := json.Marshal(body)
		require.NoError(t, err)
		reader = bytes.NewReader(b)
	} else {
		reader = bytes.NewReader(nil)
	}
	req, err := http.NewRequest(method, path, reader)
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

// --- Input validation paths (no DAO calls expected) -------------------------

func TestGetTenantConfig_InvalidUUID(t *testing.T) {
	stubDAO(t, nil, nil, nil, nil) // any DAO call fails
	r := setupEgressfilterRouter(t)
	w := doRequest(t, r, "GET", "/api/admin/egressfilter/tenant/not-a-uuid", nil)
	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Contains(t, w.Body.String(), "invalid tenant_id")
}

func TestUpsertTenantConfig_InvalidMode(t *testing.T) {
	stubDAO(t, nil, nil, nil, nil)
	r := setupEgressfilterRouter(t)
	tid := uuid.New()
	w := doRequest(t, r, "PUT",
		fmt.Sprintf("/api/admin/egressfilter/tenant/%s", tid),
		map[string]any{"mode": "garbage"})
	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Contains(t, w.Body.String(), "mode must be one of")
}

func TestUpsertTenantConfig_BudgetExceeded(t *testing.T) {
	stubDAO(t, nil, nil, nil, nil)
	r := setupEgressfilterRouter(t)
	tid := uuid.New()
	// 501 allowlist entries — over the 500 cap.
	allowlist := make([]string, 501)
	for i := range allowlist {
		allowlist[i] = fmt.Sprintf("v%d", i)
	}
	w := doRequest(t, r, "PUT",
		fmt.Sprintf("/api/admin/egressfilter/tenant/%s", tid),
		map[string]any{"allowlist": allowlist})
	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Contains(t, w.Body.String(), "allowlist exceeds")
}

// --- Happy paths through the stubbed DAO ------------------------------------

func TestGetTenantConfig_404WhenNoRow(t *testing.T) {
	stubDAO(t,
		func(_ context.Context, _ uuid.UUID) (*egressfilter.TenantConfig, error) { return nil, nil },
		nil, nil, nil)
	r := setupEgressfilterRouter(t)
	tid := uuid.New()
	w := doRequest(t, r, "GET", fmt.Sprintf("/api/admin/egressfilter/tenant/%s", tid), nil)
	assert.Equal(t, http.StatusNotFound, w.Code)
	assert.Contains(t, w.Body.String(), "no per-tenant override")
}

func TestGetTenantConfig_ReturnsConfig(t *testing.T) {
	tid := uuid.New()
	cfg := &egressfilter.TenantConfig{
		TenantID:      tid,
		Mode:          egressfilter.ModeEnforce,
		Enabled:       true,
		Allowlist:     []string{"AKIAIOSFODNN7EXAMPLE"},
		DisabledRules: []string{"github-pat"},
	}
	stubDAO(t,
		func(_ context.Context, got uuid.UUID) (*egressfilter.TenantConfig, error) {
			assert.Equal(t, tid, got)
			return cfg, nil
		},
		nil, nil, nil)
	r := setupEgressfilterRouter(t)
	w := doRequest(t, r, "GET", fmt.Sprintf("/api/admin/egressfilter/tenant/%s", tid), nil)
	require.Equal(t, http.StatusOK, w.Code)

	var resp map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Equal(t, tid.String(), resp["tenant_id"])
	assert.Equal(t, "enforce", resp["mode"])
}

func TestUpsertTenantConfig_Success(t *testing.T) {
	tid := uuid.New()
	var captured *egressfilter.TenantConfig
	stubDAO(t,
		nil,
		func(_ context.Context, c *egressfilter.TenantConfig) error {
			captured = c
			return nil
		},
		nil, nil)
	r := setupEgressfilterRouter(t)
	w := doRequest(t, r, "PUT",
		fmt.Sprintf("/api/admin/egressfilter/tenant/%s", tid),
		map[string]any{
			"mode":           "enforce",
			"enabled":        true,
			"allowlist":      []string{"AKIAIOSFODNN7EXAMPLE", "AKIAIOSFODNN7EXAMPLE", " "}, // dup + empty
			"disabled_rules": []string{"github-pat"},
		})
	require.Equal(t, http.StatusOK, w.Code)
	require.NotNil(t, captured)
	assert.Equal(t, tid, captured.TenantID)
	assert.Equal(t, egressfilter.ModeEnforce, captured.Mode)
	assert.Equal(t, []string{"AKIAIOSFODNN7EXAMPLE"}, captured.Allowlist,
		"dedup + empty-trim must apply before persist")
}

func TestPatchTenantConfig_AddRemoveSemantics(t *testing.T) {
	tid := uuid.New()
	existing := &egressfilter.TenantConfig{
		TenantID:  tid,
		Mode:      egressfilter.ModeDetect,
		Enabled:   true,
		Allowlist: []string{"keep_me", "drop_me"},
	}
	var captured *egressfilter.TenantConfig
	stubDAO(t,
		func(_ context.Context, _ uuid.UUID) (*egressfilter.TenantConfig, error) { return existing, nil },
		func(_ context.Context, c *egressfilter.TenantConfig) error { captured = c; return nil },
		nil, nil)
	r := setupEgressfilterRouter(t)
	w := doRequest(t, r, "PATCH",
		fmt.Sprintf("/api/admin/egressfilter/tenant/%s", tid),
		map[string]any{
			"allowlist_add":    []string{"add_me", "keep_me"}, // keep_me already present → dedup
			"allowlist_remove": []string{"drop_me"},
		})
	require.Equal(t, http.StatusOK, w.Code)
	require.NotNil(t, captured)
	assert.ElementsMatch(t, []string{"keep_me", "add_me"}, captured.Allowlist)
}

func TestDeleteTenantConfig_Success(t *testing.T) {
	tid := uuid.New()
	called := false
	stubDAO(t,
		nil, nil,
		func(_ context.Context, got uuid.UUID) error {
			called = true
			assert.Equal(t, tid, got)
			return nil
		},
		nil)
	r := setupEgressfilterRouter(t)
	w := doRequest(t, r, "DELETE", fmt.Sprintf("/api/admin/egressfilter/tenant/%s", tid), nil)
	assert.Equal(t, http.StatusOK, w.Code)
	assert.True(t, called, "DAO delete must be invoked")
}

// --- Pure helpers ------------------------------------------------------------

func TestApplyAddRemove(t *testing.T) {
	cases := []struct {
		name           string
		base, add, rem []string
		want           []string
	}{
		{"empty all", nil, nil, nil, nil},
		{"add only", nil, []string{"a", "b"}, nil, []string{"a", "b"}},
		{"remove first then add", []string{"a", "b"}, []string{"a", "c"}, []string{"b"}, []string{"a", "c"}},
		{"dedup add", []string{"a"}, []string{"a", "a", "b"}, nil, []string{"a", "b"}},
		{"trim and skip empty", []string{" "}, []string{" ", "x"}, nil, []string{"x"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := applyAddRemove(c.base, c.add, c.rem)
			assert.Equal(t, c.want, got)
		})
	}
}

// --- PII validators (V827) ---------------------------------------------------
//
// Small unit tests on the parse / validate helpers so a regression in the
// admin API's PII surface fails fast — separate from the wrapper tests
// which exercise the runtime path.

func TestParsePIIMode(t *testing.T) {
	cases := []struct {
		in      string
		wantOut string
		wantErr bool
	}{
		{"detect", "detect", false},
		{"enforce", "enforce", false},
		{"DETECT", "detect", false},
		{"  Enforce  ", "enforce", false},
		{"", "", false}, // empty = clear back to inherit env
		{"redact", "", true},
		{"audit", "", true},
		{"garbage", "", true},
	}
	for _, c := range cases {
		t.Run(c.in, func(t *testing.T) {
			out, errMsg := parsePIIMode(c.in)
			if c.wantErr {
				assert.NotEmpty(t, errMsg)
				return
			}
			assert.Empty(t, errMsg)
			assert.Equal(t, c.wantOut, out)
		})
	}
}

func TestValidatePIICategories(t *testing.T) {
	t.Run("closed set uppercased, deduped", func(t *testing.T) {
		out, errMsg := validatePIICategories([]string{"email", "Email", "  PERSON ", ""})
		assert.Empty(t, errMsg)
		assert.Equal(t, []string{"EMAIL", "PERSON"}, out)
	})
	t.Run("unknown category rejected", func(t *testing.T) {
		_, errMsg := validatePIICategories([]string{"EMAIL", "IPADDRESS"})
		assert.Contains(t, errMsg, "IPADDRESS")
		assert.Contains(t, errMsg, "EMAIL, PERSON, PHONE, LOCATION")
	})
	t.Run("empty input", func(t *testing.T) {
		out, errMsg := validatePIICategories(nil)
		assert.Empty(t, errMsg)
		assert.Nil(t, out)
	})
}

// PUT with PII fields set writes them onto the config; the DAO stub
// captures the persisted shape so we can assert PIIEnabled etc. reached
// the write path unmodified.
func TestUpsert_PIIFieldsWritten(t *testing.T) {
	var captured *egressfilter.TenantConfig
	stubDAO(t,
		func(_ context.Context, _ uuid.UUID) (*egressfilter.TenantConfig, error) { return nil, nil },
		func(_ context.Context, cfg *egressfilter.TenantConfig) error { captured = cfg; return nil },
		nil, nil,
	)
	r := setupEgressfilterRouter(t)
	tid := uuid.New()

	body := map[string]any{
		"pii_enabled":             true,
		"pii_mode":                "enforce",
		"pii_ner_enabled":         false,
		"pii_disabled_categories": []string{"email", "PHONE"},
	}
	raw, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPut, fmt.Sprintf("/api/admin/egressfilter/tenant/%s", tid), bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	require.NotNil(t, captured)
	require.NotNil(t, captured.PIIEnabled)
	assert.True(t, *captured.PIIEnabled)
	assert.Equal(t, "enforce", captured.PIIMode)
	require.NotNil(t, captured.PIINerEnabled)
	assert.False(t, *captured.PIINerEnabled)
	assert.Equal(t, []string{"EMAIL", "PHONE"}, captured.PIIDisabledCategories,
		"categories must be uppercased before persistence")
}

// PUT rejects an unknown category with 400 and does not touch the DAO.
func TestUpsert_UnknownPIICategoryRejected(t *testing.T) {
	stubDAO(t,
		func(_ context.Context, _ uuid.UUID) (*egressfilter.TenantConfig, error) { return nil, nil },
		func(_ context.Context, _ *egressfilter.TenantConfig) error {
			t.Fatal("upsert must not be called when validation fails")
			return nil
		},
		nil, nil,
	)
	r := setupEgressfilterRouter(t)
	tid := uuid.New()

	body := map[string]any{"pii_disabled_categories": []string{"EMAIL", "IPADDRESS"}}
	raw, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPut, fmt.Sprintf("/api/admin/egressfilter/tenant/%s", tid), bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusBadRequest, w.Code)
	assert.Contains(t, w.Body.String(), "IPADDRESS")
}

// PATCH tri-state: explicit `null` for a nullable PII field clears the
// stored value back to nil / "" (== inherit env), distinct from field
// absence. Gemini review on PR #35259 flagged the gap in the original
// implementation.
func TestPatch_ExplicitNullClearsPIIFields(t *testing.T) {
	existing := &egressfilter.TenantConfig{
		TenantID:      uuid.New(),
		Mode:          egressfilter.ModeDetect,
		Enabled:       true,
		PIIEnabled:    func() *bool { v := true; return &v }(),
		PIIMode:       "enforce",
		PIINerEnabled: func() *bool { v := true; return &v }(),
	}
	var captured *egressfilter.TenantConfig
	stubDAO(t,
		func(_ context.Context, _ uuid.UUID) (*egressfilter.TenantConfig, error) { return existing, nil },
		func(_ context.Context, cfg *egressfilter.TenantConfig) error { captured = cfg; return nil },
		nil, nil,
	)
	r := setupEgressfilterRouter(t)

	// json.RawMessage lets us send explicit `null` for each nullable
	// field — a plain map[string]any with nil values also works but this
	// makes the wire shape unambiguous.
	req := httptest.NewRequest(http.MethodPatch,
		fmt.Sprintf("/api/admin/egressfilter/tenant/%s", existing.TenantID),
		bytes.NewReader([]byte(`{"pii_enabled":null,"pii_mode":null,"pii_ner_enabled":null}`)))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	require.NotNil(t, captured)
	assert.Nil(t, captured.PIIEnabled, "explicit null must clear to inherit-env")
	assert.Equal(t, "", captured.PIIMode, "explicit null must clear to inherit-env")
	assert.Nil(t, captured.PIINerEnabled, "explicit null must clear to inherit-env")
}

// PATCH `{"pii_disabled_categories": []}` explicitly empties the set.
// Distinct from absence (which would leave it untouched); distinct from
// null (which also clears to nil, and the DAO coerces to '{}' before
// send).
func TestPatch_EmptyArrayClearsCategories(t *testing.T) {
	existing := &egressfilter.TenantConfig{
		TenantID:              uuid.New(),
		Mode:                  egressfilter.ModeDetect,
		Enabled:               true,
		PIIDisabledCategories: []string{"PERSON", "PHONE"},
	}
	var captured *egressfilter.TenantConfig
	stubDAO(t,
		func(_ context.Context, _ uuid.UUID) (*egressfilter.TenantConfig, error) { return existing, nil },
		func(_ context.Context, cfg *egressfilter.TenantConfig) error { captured = cfg; return nil },
		nil, nil,
	)
	r := setupEgressfilterRouter(t)

	req := httptest.NewRequest(http.MethodPatch,
		fmt.Sprintf("/api/admin/egressfilter/tenant/%s", existing.TenantID),
		bytes.NewReader([]byte(`{"pii_disabled_categories":[]}`)))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	require.NotNil(t, captured)
	assert.Empty(t, captured.PIIDisabledCategories, "empty array must replace the existing set")
}

// PATCH updates only the fields the caller provided; absent PII fields
// leave the existing DB values untouched.
func TestPatch_PIIFieldsPartialUpdate(t *testing.T) {
	existing := &egressfilter.TenantConfig{
		TenantID:              uuid.New(),
		Mode:                  egressfilter.ModeDetect,
		Enabled:               true,
		PIIEnabled:            func() *bool { v := true; return &v }(),
		PIIMode:               "detect",
		PIINerEnabled:         func() *bool { v := true; return &v }(),
		PIIDisabledCategories: []string{"PERSON"},
	}
	var captured *egressfilter.TenantConfig
	stubDAO(t,
		func(_ context.Context, _ uuid.UUID) (*egressfilter.TenantConfig, error) { return existing, nil },
		func(_ context.Context, cfg *egressfilter.TenantConfig) error { captured = cfg; return nil },
		nil, nil,
	)
	r := setupEgressfilterRouter(t)

	// Only flip pii_mode. Other PII fields must remain untouched.
	body := map[string]any{"pii_mode": "enforce"}
	raw, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPatch, fmt.Sprintf("/api/admin/egressfilter/tenant/%s", existing.TenantID), bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	require.NotNil(t, captured)
	assert.Equal(t, "enforce", captured.PIIMode, "changed field")
	require.NotNil(t, captured.PIIEnabled)
	assert.True(t, *captured.PIIEnabled, "untouched field preserved")
	require.NotNil(t, captured.PIINerEnabled)
	assert.True(t, *captured.PIINerEnabled, "untouched field preserved")
	assert.Equal(t, []string{"PERSON"}, captured.PIIDisabledCategories, "untouched field preserved")
}

func TestParseMode(t *testing.T) {
	cases := []struct {
		in      string
		wantErr bool
	}{
		{"detect", false},
		{"DETECT", false},
		{"  detect  ", false},
		{"enforce", false},
		{"audit", false},  // legacy alias still accepted
		{"redact", false}, // supported once the redact-mode wrapper landed
		{"garbage", true},
		{"", true},
	}
	for _, c := range cases {
		t.Run(c.in, func(t *testing.T) {
			_, errMsg := parseMode(c.in)
			if c.wantErr {
				assert.NotEmpty(t, errMsg)
			} else {
				assert.Empty(t, errMsg)
			}
		})
	}
}
