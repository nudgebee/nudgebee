package event

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"unicode/utf8"

	"nudgebee/services/config"
	"nudgebee/services/security"

	"github.com/stretchr/testify/assert"
)

func TestParseSeveritySet(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want map[string]bool
	}{
		{"empty", "", map[string]bool{}},
		{"single", "HIGH", map[string]bool{"HIGH": true}},
		{"csv with spaces", " high , Medium ", map[string]bool{"HIGH": true, "MEDIUM": true}},
		{"trailing comma + blanks", "HIGH,,", map[string]bool{"HIGH": true}},
		{"lowercased normalized", "low", map[string]bool{"LOW": true}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			assert.Equal(t, c.want, parseSeveritySet(c.in))
		})
	}
}

func TestBuildRCAComment(t *testing.T) {
	t.Run("includes header rca body and deep link", func(t *testing.T) {
		out := buildRCAComment("root cause: OOMKilled", "evt-123")
		assert.Contains(t, out, "NudgeBee RCA Analysis")
		assert.Contains(t, out, "root cause: OOMKilled")
		assert.Contains(t, out, "/investigate?id=evt-123")
	})

	t.Run("truncates long bodies rune-safely without panicking", func(t *testing.T) {
		// Multi-byte runes ensure naive byte slicing would split a rune.
		long := strings.Repeat("é", rcaWritebackMaxCommentChars+500)
		out := buildRCAComment(long, "evt-1")
		assert.Contains(t, out, "…(truncated)")
		assert.True(t, strings.Contains(out, "/investigate?id=evt-1"))
		// Output must remain valid UTF-8 (no broken rune from truncation).
		assert.True(t, utf8.ValidString(out))
	})

	t.Run("short body is not truncated", func(t *testing.T) {
		out := buildRCAComment("brief", "e")
		assert.NotContains(t, out, "…(truncated)")
	})
}

func TestRCAContentHashStableAndDistinct(t *testing.T) {
	a := rcaContentHash("same text")
	b := rcaContentHash("same text")
	c := rcaContentHash("different text")
	assert.Equal(t, a, b, "same input must hash identically (idempotency depends on this)")
	assert.NotEqual(t, a, c, "different RCA versions must hash differently (revision -> new note)")
}

// TestRCAWritebackHook pins the investigation.completed hook's field mapping:
// it pulls the event id / account off the event map, prefers analysis_summary as
// the note body and falls back to analysis_investigation, and never attempts a
// writeback for an event map missing an id.
func TestRCAWritebackHook(t *testing.T) {
	orig := processRCAWritebackFn
	t.Cleanup(func() { processRCAWritebackFn = orig })

	var got RCAWritebackRequest
	var called bool
	processRCAWritebackFn = func(_ *security.RequestContext, req RCAWritebackRequest) error {
		called = true
		got = req
		return nil
	}

	t.Run("prefers analysis_summary and pulls ids off the event map", func(t *testing.T) {
		called, got = false, RCAWritebackRequest{}
		err := rcaWritebackHook(nil, map[string]any{
			"id":                     "evt-1",
			"cloud_account_id":       "acct-1",
			"analysis_summary":       "the summary",
			"analysis_investigation": "the investigation",
		})
		assert.NoError(t, err)
		assert.True(t, called)
		assert.Equal(t, "evt-1", got.EventId)
		assert.Equal(t, "acct-1", got.AccountId)
		assert.Equal(t, "the summary", got.RCAText)
	})

	t.Run("falls back to analysis_investigation when summary is blank", func(t *testing.T) {
		called, got = false, RCAWritebackRequest{}
		err := rcaWritebackHook(nil, map[string]any{
			"id":                     "evt-2",
			"analysis_summary":       "   ",
			"analysis_investigation": "the investigation",
		})
		assert.NoError(t, err)
		assert.True(t, called)
		assert.Equal(t, "the investigation", got.RCAText)
	})

	t.Run("no-ops without calling writeback when event id is missing", func(t *testing.T) {
		called, got = false, RCAWritebackRequest{}
		err := rcaWritebackHook(nil, map[string]any{"analysis_summary": "x"})
		assert.NoError(t, err)
		assert.False(t, called, "must not attempt writeback without an event id")
	})
}

// TestRCAWritebackSourceSeam pins the source seam: each registered event source
// maps to the right add-comment token + integration-config namespace.
func TestRCAWritebackSourceSeam(t *testing.T) {
	zd, ok := rcaWritebackSources["zenduty_webhook"]
	assert.True(t, ok, "zenduty_webhook must be a registered writeback source")
	assert.Equal(t, "zenduty", zd.commentSource)
	assert.Equal(t, "zenduty", zd.configNamespace)

	pd, ok := rcaWritebackSources["pagerduty_webhook"]
	assert.True(t, ok, "pagerduty_webhook must be a registered writeback source")
	assert.Equal(t, "pagerduty", pd.commentSource)
	assert.Equal(t, "pagerduty", pd.configNamespace)
}

// TestPostIncidentCommentCarriesTenantSessionVariable pins the RPC contract
// with ticket-server: bindAndAuthoriseTicketRequest unconditionally overwrites
// the object's tenant with session_variables.tenant_id, so the tenant must be
// present there — otherwise the ticket-server tool-config lookup runs with an
// empty tenant and every add-comment fails with 400.
func TestPostIncidentCommentCarriesTenantSessionVariable(t *testing.T) {
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/tickets/rpc/add-comment", r.URL.Path)
		assert.NoError(t, json.NewDecoder(r.Body).Decode(&gotBody))
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	orig := config.Config.TicketServiceUrl
	config.Config.TicketServiceUrl = srv.URL
	t.Cleanup(func() { config.Config.TicketServiceUrl = orig })

	err := postIncidentComment("tenant-1", "acct-1", "zenduty", "incident-1", "note body")
	assert.NoError(t, err)

	sv, _ := gotBody["session_variables"].(map[string]any)
	assert.Equal(t, "admin", sv["role"])
	assert.Equal(t, "tenant-1", sv["tenant_id"])

	input, _ := gotBody["input"].(map[string]any)
	obj, _ := input["object"].(map[string]any)
	assert.Equal(t, "tenant-1", obj["tenant"])
	assert.Equal(t, "acct-1", obj["account_id"])
	assert.Equal(t, "zenduty", obj["source"])
	assert.Equal(t, "incident-1", obj["ticket_id"])
	assert.Equal(t, "note body", obj["comment"])
}
