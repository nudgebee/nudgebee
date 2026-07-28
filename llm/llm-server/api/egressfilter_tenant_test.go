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
