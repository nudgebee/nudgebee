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

func TestTempoGet_OversizedBodyIsAClearError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		chunk := make([]byte, 1<<20)
		for i := 0; i <= tempoMaxBody/len(chunk); i++ {
			_, _ = w.Write(chunk)
		}
	}))
	defer srv.Close()
	_, err := tempoGet(integrations.GrafanaTempoConfig{URL: srv.URL}, "/api/search", nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "/api/search response too large (>32MB)")
}
