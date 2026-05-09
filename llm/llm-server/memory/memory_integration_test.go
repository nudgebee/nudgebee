package memory_test

// End-to-end integration test for the Memory Module.
//
// Scenario (the "full picture"):
//
//   1. A user sets their Soul (stylistic profile) and some Preferences via
//      the Mutate API — just like the /v1/memory_v2 HTTP handlers do.
//   2. Every Mutate fires an Observe; we verify events land in
//      llm_memory_events.
//   3. On the next agent turn, Compose is called; we verify it returns a
//      MemorySlab populated with the Soul and Preferences blocks.
//   4. MemorySlab.Render() produces exactly the text that would be appended
//      to request.AccountPrompt and surfaced to the LLM.
//   5. We flip the module flag off and confirm Compose returns empty — the
//      rollback path.
//
// Gating:
//   This test talks to a real metastore Postgres. Skipped unless
//   RUN_MEMORY_INTEGRATION=true to keep `make test` fast. Expects the
//   Phase-1 migrations (V707 / V708 / V709 / V717) applied.
//
//   RUN: RUN_MEMORY_INTEGRATION=true go test -v -run TestMemoryPhase1_FullFlow ./memory/...

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"nudgebee/llm/common"
	"nudgebee/llm/config"
	"nudgebee/llm/memory"
	"nudgebee/llm/memory/stores/eventlog"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// requireIntegration gates the test behind an env var. If RUN_MEMORY_INTEGRATION
// is not set, the test skips so local `make test` stays fast and offline-friendly.
func requireIntegration(t *testing.T) {
	t.Helper()
	if os.Getenv("RUN_MEMORY_INTEGRATION") != "true" {
		t.Skip("set RUN_MEMORY_INTEGRATION=true to run (needs Postgres with Phase-1 migrations)")
	}
	if os.Getenv("LLM_SERVER_DB_URL") == "" {
		t.Skip("LLM_SERVER_DB_URL not set")
	}
	// Ensure DB is actually reachable before we pretend to test anything.
	if _, err := common.GetDatabaseManager(common.Metastore); err != nil {
		t.Skipf("metastore unreachable: %v", err)
	}
}

// setFlagsForTest turns on everything Phase 1 needs + the test tenant, and
// returns a cleanup func that restores prior values.
func setFlagsForTest(t *testing.T, tenantID string) (cleanup func()) {
	t.Helper()
	prev := struct {
		module, compose, soul, prefs bool
		allowlist                    string
	}{
		module:    config.Config.MemoryModuleEnabled,
		compose:   config.Config.MemoryComposeEnabled,
		soul:      config.Config.MemoryLayerSoulEnabled,
		prefs:     config.Config.MemoryLayerPrefsEnabled,
		allowlist: config.Config.MemoryTenantAllowlist,
	}
	config.Config.MemoryModuleEnabled = true
	config.Config.MemoryComposeEnabled = true
	config.Config.MemoryLayerSoulEnabled = true
	config.Config.MemoryLayerPrefsEnabled = true
	config.Config.MemoryTenantAllowlist = tenantID

	return func() {
		config.Config.MemoryModuleEnabled = prev.module
		config.Config.MemoryComposeEnabled = prev.compose
		config.Config.MemoryLayerSoulEnabled = prev.soul
		config.Config.MemoryLayerPrefsEnabled = prev.prefs
		config.Config.MemoryTenantAllowlist = prev.allowlist
	}
}

// cleanupTestRows removes any rows this test created so re-runs start clean
// and tests don't pollute the dev DB.
func cleanupTestRows(t *testing.T, tenantID, userID string) {
	t.Helper()
	m := memory.Default()
	if err := m.Erase(context.Background(), memory.EraseRequest{
		TenantID: tenantID, UserID: userID,
	}); err != nil {
		t.Logf("cleanup: erase failed (non-fatal): %v", err)
	}
	db, err := common.GetDatabaseManager(common.Metastore)
	if err == nil {
		_, _ = db.Db.Exec(`DELETE FROM llm_memory_events WHERE tenant_id = $1`, tenantID)
	}
}

func TestMemoryPhase1_FullFlow(t *testing.T) {
	requireIntegration(t)

	// ── Setup ─────────────────────────────────────────────────────────────
	tenantID := "test-tenant-" + uuid.NewString()
	userID := "test-user-" + uuid.NewString()

	defer setFlagsForTest(t, tenantID)()
	defer cleanupTestRows(t, tenantID, userID)

	ctx := context.Background()
	m := memory.Default()

	// ── 1. User curates their Soul ────────────────────────────────────────
	t.Run("user sets their soul", func(t *testing.T) {
		resp, err := m.Mutate(ctx, memory.MutateRequest{
			TenantID:  tenantID,
			UserID:    userID,
			Layer:     "soul",
			Action:    "set",
			ActorKind: "user",
			ActorID:   userID,
			Value: map[string]any{
				"style": map[string]any{
					"tone":                "terse",
					"expertise_level":     "expert",
					"prefer_cli":          true,
					"confirm_destructive": true,
				},
				"markdown": "I prefer AWS CLI commands I can copy. Skip boilerplate.",
			},
		})
		require.NoError(t, err)
		assert.True(t, resp.Success)
	})

	// ── 2. User sets two preferences ──────────────────────────────────────
	t.Run("user sets preferences", func(t *testing.T) {
		for _, pref := range []struct{ key string; value any }{
			{"timezone", "America/New_York"},
			{"preferred_cloud", "aws"},
		} {
			resp, err := m.Mutate(ctx, memory.MutateRequest{
				TenantID:  tenantID,
				UserID:    userID,
				Layer:     "preferences",
				Action:    "set",
				Key:       pref.key,
				ActorKind: "user",
				ActorID:   userID,
				Value: map[string]any{
					"value":        pref.value,
					"agent_module": "",
				},
			})
			require.NoError(t, err, "failed to set %s", pref.key)
			assert.True(t, resp.Success)
		}
	})

	// ── 3. Every write emitted an Observe → event log row ────────────────
	// Observe is async via the projection worker pool; give it a brief
	// window to flush. In practice the event log append is sync so this
	// should be near-instant, but we allow a few hundred ms for safety.
	t.Run("events landed in event log", func(t *testing.T) {
		var events []eventlog.Event
		deadline := time.Now().Add(2 * time.Second)
		for time.Now().Before(deadline) {
			events, _ = eventlog.Scan(tenantID, userID, time.Now().Add(-1*time.Hour), 100)
			if len(events) >= 3 {
				break
			}
			time.Sleep(50 * time.Millisecond)
		}
		require.GreaterOrEqual(t, len(events), 3,
			"expected 3 events (1 soul.updated + 2 preference.set); got %d", len(events))

		eventTypes := map[string]int{}
		for _, e := range events {
			eventTypes[e.EventType]++
		}
		assert.Equal(t, 1, eventTypes[eventlog.EventTypeSoulUpdated])
		assert.Equal(t, 2, eventTypes[eventlog.EventTypePreferenceSet])
	})

	// ── 4. Compose returns the expected MemorySlab ───────────────────────
	// This is what every agent turn invokes. No user_id in the ComposeRequest
	// → no per-user data. With user_id, we expect Soul + Preferences blocks.
	var slab memory.MemorySlab
	t.Run("compose returns soul and preferences for the user", func(t *testing.T) {
		var err error
		slab, err = m.Compose(ctx, memory.ComposeRequest{
			TenantID:    tenantID,
			UserID:      userID,
			AgentModule: "generic",
			Query:       "what's my environment?",
			TokenBudget: 2000,
		})
		require.NoError(t, err)

		// Structured trace should show which flags applied on this call.
		assert.True(t, slab.Trace.FlagsApplied["soul"], "soul flag should be active for enrolled tenant")
		assert.True(t, slab.Trace.FlagsApplied["preferences"], "prefs flag should be active")

		// The actual content blocks.
		assert.Contains(t, slab.Soul, "<user_style>")
		assert.Contains(t, slab.Soul, "tone: terse")
		assert.Contains(t, slab.Soul, "expertise_level: expert")
		assert.Contains(t, slab.Soul, "prefer_cli_over_console: true")
		assert.Contains(t, slab.Soul, "confirm_before_destructive: true")
		assert.Contains(t, slab.Soul, "I prefer AWS CLI")
		assert.Contains(t, slab.Soul, "</user_style>")

		assert.Contains(t, slab.Preferences, "<user_preferences>")
		assert.Contains(t, slab.Preferences, "timezone: America/New_York")
		assert.Contains(t, slab.Preferences, "preferred_cloud: aws")
		assert.Contains(t, slab.Preferences, "</user_preferences>")

		// Layers we haven't implemented yet should be empty in Phase 1.
		assert.Empty(t, slab.Patterns, "Phase 1: patterns layer not yet active")
		assert.Empty(t, slab.Decisions, "Phase 1: decisions layer not yet active")
	})

	// ── 5. MemorySlab.Render() produces the prompt-ready text ────────────
	t.Run("slab renders the complete prompt fragment", func(t *testing.T) {
		rendered := slab.Render()
		assert.Contains(t, rendered, "<user_style>")
		assert.Contains(t, rendered, "<user_preferences>")
		// Ordering: Soul first, Preferences second (from MemorySlab.Render).
		styleIdx := `<user_style>`
		prefsIdx := `<user_preferences>`
		assert.Less(t, indexOf(rendered, styleIdx), indexOf(rendered, prefsIdx),
			"Soul should render before Preferences")
		t.Logf("\n---- rendered memory slab ----\n%s\n---- end slab ----\n", rendered)
	})

	// ── 6. Rollback: flag off → empty slab (byte-identical-to-main behaviour)
	t.Run("flag off returns empty slab", func(t *testing.T) {
		config.Config.MemoryModuleEnabled = false
		defer func() { config.Config.MemoryModuleEnabled = true }()

		slab, err := m.Compose(ctx, memory.ComposeRequest{
			TenantID:    tenantID,
			UserID:      userID,
			AgentModule: "generic",
		})
		require.NoError(t, err)
		assert.Empty(t, slab.Soul, "flag off → no soul block")
		assert.Empty(t, slab.Preferences, "flag off → no preferences block")
		assert.Empty(t, slab.Render(), "flag off → empty rendered slab")
	})

	// ── 7. Non-enrolled tenant → empty slab even with flag on ────────────
	t.Run("unenrolled tenant returns empty slab", func(t *testing.T) {
		slab, err := m.Compose(ctx, memory.ComposeRequest{
			TenantID:    "some-other-tenant-" + uuid.NewString(),
			UserID:      userID,
			AgentModule: "generic",
		})
		require.NoError(t, err)
		assert.Empty(t, slab.Soul)
		assert.Empty(t, slab.Preferences)
	})

	// ── 8. DB audit: dump actual rows so the test is self-explanatory ───
	// This confirms what's physically stored, not just that Compose returned
	// the right shape. Matters because Compose output could pass while DB
	// state is wrong (e.g., JSONB malformed, wrong columns populated).
	t.Run("audit DB rows match what was written", func(t *testing.T) {
		db, err := common.GetDatabaseManager(common.Metastore)
		require.NoError(t, err)

		// llm_memory_soul
		var soulRow struct {
			TenantID  string `db:"tenant_id"`
			UserID    string `db:"user_id"`
			Version   int    `db:"version"`
			StyleJSON []byte `db:"style"`
			Markdown  string `db:"markdown"`
		}
		err = db.Db.Get(&soulRow, `
			SELECT tenant_id, user_id, version, style, COALESCE(markdown, '') AS markdown
			FROM llm_memory_soul WHERE tenant_id = $1 AND user_id = $2
		`, tenantID, userID)
		require.NoError(t, err, "soul row must exist for this user")
		assert.Equal(t, tenantID, soulRow.TenantID)
		assert.Equal(t, userID, soulRow.UserID)
		assert.GreaterOrEqual(t, soulRow.Version, 1)
		// Postgres JSONB normalizes with a space after the colon.
		assert.Contains(t, string(soulRow.StyleJSON), `"tone": "terse"`)
		assert.Contains(t, string(soulRow.StyleJSON), `"expertise_level": "expert"`)
		assert.Contains(t, string(soulRow.StyleJSON), `"prefer_cli": true`)
		assert.Contains(t, soulRow.Markdown, "AWS CLI")
		t.Logf("\n[llm_memory_soul]\n  tenant_id: %s\n  user_id:   %s\n  version:   %d\n  style:     %s\n  markdown:  %q",
			soulRow.TenantID, soulRow.UserID, soulRow.Version, string(soulRow.StyleJSON), soulRow.Markdown)

		// llm_memory_preferences — expect 2 rows for this user
		type prefRow struct {
			Key        string `db:"key"`
			ValueJSON  []byte `db:"value"`
			Source     string `db:"source"`
			Confidence string `db:"confidence"`
			Module     *string `db:"agent_module"`
		}
		var prefRows []prefRow
		err = db.Db.Select(&prefRows, `
			SELECT key, value, source, confidence::text, agent_module
			FROM llm_memory_preferences
			WHERE tenant_id = $1 AND user_id = $2
			ORDER BY key
		`, tenantID, userID)
		require.NoError(t, err)
		require.Len(t, prefRows, 2, "expected 2 preference rows")
		t.Logf("\n[llm_memory_preferences] %d rows:", len(prefRows))
		for _, p := range prefRows {
			module := "(NULL / cross-agent)"
			if p.Module != nil {
				module = *p.Module
			}
			t.Logf("  %-18s  value=%-25s  source=%s  confidence=%s  module=%s",
				p.Key, string(p.ValueJSON), p.Source, p.Confidence, module)
		}
		// Assert content
		keys := map[string]string{}
		for _, p := range prefRows {
			keys[p.Key] = string(p.ValueJSON)
			assert.Equal(t, "explicit", p.Source, "explicit Mutate should record source=explicit")
			assert.Equal(t, "1.00", p.Confidence, "explicit writes default to confidence=1.0")
		}
		assert.Contains(t, keys["timezone"], "America/New_York")
		assert.Contains(t, keys["preferred_cloud"], "aws")

		// llm_memory_events — one soul.updated + two preference.set
		type evtRow struct {
			EventType  string `db:"event_type"`
			ActorKind  string `db:"actor_kind"`
			PayloadTxt string `db:"payload_text"`
			CreatedAt time.Time `db:"created_at"`
		}
		var evtRows []evtRow
		err = db.Db.Select(&evtRows, `
			SELECT event_type, actor_kind, payload::text AS payload_text, created_at
			FROM llm_memory_events
			WHERE tenant_id = $1 AND user_id = $2
			ORDER BY created_at ASC
		`, tenantID, userID)
		require.NoError(t, err)
		require.GreaterOrEqual(t, len(evtRows), 3, "expected ≥3 events")
		t.Logf("\n[llm_memory_events] %d rows:", len(evtRows))
		for _, e := range evtRows {
			t.Logf("  %s  %-20s actor=%s  payload=%s",
				e.CreatedAt.Format("15:04:05.000"), e.EventType, e.ActorKind, e.PayloadTxt)
		}

		// Row-counts by event type
		counts := map[string]int{}
		for _, e := range evtRows {
			counts[e.EventType]++
		}
		assert.Equal(t, 1, counts["soul.updated"])
		assert.Equal(t, 2, counts["preference.set"])
		// Every Observe event must have actor_kind set
		for _, e := range evtRows {
			assert.NotEmpty(t, e.ActorKind, "actor_kind required on every event")
		}
	})
}

// indexOf returns the byte offset of substr in s, or len(s)+1 if not found
// (so "not present" compares greater-than any real index).
func indexOf(s, substr string) int {
	for i := 0; i+len(substr) <= len(s); i++ {
		if s[i:i+len(substr)] == substr {
			return i
		}
	}
	return len(s) + 1
}

// quiet the "unused import" if test tags drop references.
var _ = fmt.Sprintf
