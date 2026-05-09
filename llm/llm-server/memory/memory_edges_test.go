package memory_test

// Edge-case behaviour tests for the Memory Module, complementing the
// baseline flow in memory_integration_test.go.
//
// Each test exercises a single behaviour the user cares about in
// production:
//
//   TestUpdate_NewSoulVisibleInNextCompose    — updates apply immediately
//   TestClear_EmptiesSoulBlock                — clear removes the block
//   TestClearPref_RemovesOnlyThatKey          — granular preference removal
//   TestEraseAll_DropsEverythingForUser       — GDPR erase is complete
//   TestConcurrentCompose_NoDataRace          — parallel reads are safe
//   TestMultiplePrefsSameKey_LatestWins       — upsert semantics
//
// All tests gated on RUN_MEMORY_INTEGRATION=true; skip cleanly otherwise.
// Each test uses a unique tenant/user pair and defers its own cleanup so
// tests are isolated and the dev DB stays tidy.

import (
	"context"
	"os"
	"strings"
	"sync"
	"testing"

	"nudgebee/llm/common"
	"nudgebee/llm/config"
	"nudgebee/llm/memory"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// requireEdgeIntegration is the same gate used in memory_integration_test.go.
func requireEdgeIntegration(t *testing.T) (tenantID, userID string, m memory.Memory, cleanup func()) {
	t.Helper()
	if os.Getenv("RUN_MEMORY_INTEGRATION") != "true" {
		t.Skip("set RUN_MEMORY_INTEGRATION=true to run")
	}
	if os.Getenv("LLM_SERVER_DB_URL") == "" {
		t.Skip("LLM_SERVER_DB_URL not set")
	}
	if _, err := common.GetDatabaseManager(common.Metastore); err != nil {
		t.Skipf("metastore unreachable: %v", err)
	}

	tenantID = "edge-tenant-" + uuid.NewString()
	userID = "edge-user-" + uuid.NewString()

	// Flag setup.
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

	m = memory.Default()

	cleanup = func() {
		_ = m.Erase(context.Background(), memory.EraseRequest{TenantID: tenantID, UserID: userID})
		if db, err := common.GetDatabaseManager(common.Metastore); err == nil {
			_, _ = db.Db.Exec(`DELETE FROM llm_memory_events WHERE tenant_id = $1`, tenantID)
		}
		config.Config.MemoryModuleEnabled = prev.module
		config.Config.MemoryComposeEnabled = prev.compose
		config.Config.MemoryLayerSoulEnabled = prev.soul
		config.Config.MemoryLayerPrefsEnabled = prev.prefs
		config.Config.MemoryTenantAllowlist = prev.allowlist
	}
	return
}

// setSoul is a small helper to reduce boilerplate in these tests.
func setSoul(t *testing.T, m memory.Memory, tenantID, userID, tone, markdown string) {
	t.Helper()
	resp, err := m.Mutate(context.Background(), memory.MutateRequest{
		TenantID: tenantID, UserID: userID,
		Layer: "soul", Action: "set",
		ActorKind: "user", ActorID: userID,
		Value: map[string]any{
			"style":    map[string]any{"tone": tone},
			"markdown": markdown,
		},
	})
	require.NoError(t, err)
	require.True(t, resp.Success)
}

// setPref is a helper for setting a preference.
func setPref(t *testing.T, m memory.Memory, tenantID, userID, key string, value any) {
	t.Helper()
	resp, err := m.Mutate(context.Background(), memory.MutateRequest{
		TenantID: tenantID, UserID: userID,
		Layer: "preferences", Action: "set", Key: key,
		ActorKind: "user", ActorID: userID,
		Value: map[string]any{"value": value, "agent_module": ""},
	})
	require.NoError(t, err)
	require.True(t, resp.Success)
}

// compose fetches the slab under the given request params.
func composeSlab(t *testing.T, m memory.Memory, tenantID, userID string) memory.MemorySlab {
	t.Helper()
	slab, err := m.Compose(context.Background(), memory.ComposeRequest{
		TenantID:    tenantID,
		UserID:      userID,
		AgentModule: "generic",
	})
	require.NoError(t, err)
	return slab
}

// ── UPDATE ──────────────────────────────────────────────────────────────

// TestUpdate_NewSoulVisibleInNextCompose ensures that after updating the Soul,
// a subsequent Compose returns the NEW content (not the stale cached one).
// This validates the cache-invalidation path in mutateSoul.
func TestUpdate_NewSoulVisibleInNextCompose(t *testing.T) {
	tenantID, userID, m, cleanup := requireEdgeIntegration(t)
	defer cleanup()

	// Initial soul — tone: terse.
	setSoul(t, m, tenantID, userID, "terse", "Short answers only.")
	slab := composeSlab(t, m, tenantID, userID)
	assert.Contains(t, slab.Soul, "tone: terse")
	assert.Contains(t, slab.Soul, "Short answers only.")

	// Update — tone: friendly.
	setSoul(t, m, tenantID, userID, "friendly", "Be warm and welcoming.")
	slab = composeSlab(t, m, tenantID, userID)
	assert.Contains(t, slab.Soul, "tone: friendly",
		"updated soul must surface immediately (cache invalidation must work)")
	assert.Contains(t, slab.Soul, "Be warm and welcoming.")
	assert.NotContains(t, slab.Soul, "tone: terse",
		"stale tone must NOT appear after update")
}

// ── CLEAR ───────────────────────────────────────────────────────────────

// TestClear_EmptiesSoulBlock ensures that after clearing the Soul, Compose
// returns an empty Soul block.
func TestClear_EmptiesSoulBlock(t *testing.T) {
	tenantID, userID, m, cleanup := requireEdgeIntegration(t)
	defer cleanup()

	setSoul(t, m, tenantID, userID, "terse", "whatever")
	slab := composeSlab(t, m, tenantID, userID)
	require.NotEmpty(t, slab.Soul, "soul should exist before clear")

	// Clear.
	resp, err := m.Mutate(context.Background(), memory.MutateRequest{
		TenantID: tenantID, UserID: userID,
		Layer: "soul", Action: "clear",
		ActorKind: "user", ActorID: userID,
	})
	require.NoError(t, err)
	require.True(t, resp.Success)

	slab = composeSlab(t, m, tenantID, userID)
	assert.Empty(t, slab.Soul, "soul block must be empty after clear")
}

// ── CLEAR ONE PREFERENCE ────────────────────────────────────────────────

// TestClearPref_RemovesOnlyThatKey verifies granular deletion: clearing one
// key must leave the others intact.
func TestClearPref_RemovesOnlyThatKey(t *testing.T) {
	tenantID, userID, m, cleanup := requireEdgeIntegration(t)
	defer cleanup()

	setPref(t, m, tenantID, userID, "timezone", "America/New_York")
	setPref(t, m, tenantID, userID, "preferred_cloud", "aws")
	setPref(t, m, tenantID, userID, "notification_channel", "slack:#oncall")

	slab := composeSlab(t, m, tenantID, userID)
	assert.Contains(t, slab.Preferences, "timezone")
	assert.Contains(t, slab.Preferences, "preferred_cloud")
	assert.Contains(t, slab.Preferences, "notification_channel")

	// Clear only preferred_cloud.
	resp, err := m.Mutate(context.Background(), memory.MutateRequest{
		TenantID: tenantID, UserID: userID,
		Layer: "preferences", Action: "clear", Key: "preferred_cloud",
		ActorKind: "user", ActorID: userID,
		Value: map[string]any{"agent_module": ""},
	})
	require.NoError(t, err)
	require.True(t, resp.Success)

	slab = composeSlab(t, m, tenantID, userID)
	assert.Contains(t, slab.Preferences, "timezone",
		"unrelated key must remain")
	assert.Contains(t, slab.Preferences, "notification_channel",
		"unrelated key must remain")
	assert.NotContains(t, slab.Preferences, "preferred_cloud",
		"cleared key must be gone")
}

// ── ERASE (GDPR) ────────────────────────────────────────────────────────

// TestEraseAll_DropsEverythingForUser verifies that Erase removes the Soul
// AND all Preferences for a user, leaving Compose empty.
func TestEraseAll_DropsEverythingForUser(t *testing.T) {
	tenantID, userID, m, cleanup := requireEdgeIntegration(t)
	defer cleanup()

	setSoul(t, m, tenantID, userID, "terse", "test")
	setPref(t, m, tenantID, userID, "timezone", "UTC")
	setPref(t, m, tenantID, userID, "preferred_cloud", "gcp")

	require.NotEmpty(t, composeSlab(t, m, tenantID, userID).Soul)

	require.NoError(t, m.Erase(context.Background(), memory.EraseRequest{
		TenantID: tenantID, UserID: userID,
	}))

	slab := composeSlab(t, m, tenantID, userID)
	assert.Empty(t, slab.Soul, "soul must be gone after Erase")
	assert.Empty(t, slab.Preferences, "preferences must be gone after Erase")
}

// ── CONCURRENT READS ────────────────────────────────────────────────────

// TestConcurrentCompose_NoDataRace ensures parallel Compose calls on the same
// user don't race. Run under `go test -race` to catch data races.
func TestConcurrentCompose_NoDataRace(t *testing.T) {
	tenantID, userID, m, cleanup := requireEdgeIntegration(t)
	defer cleanup()

	setSoul(t, m, tenantID, userID, "terse", "concurrent-test")
	setPref(t, m, tenantID, userID, "preferred_cloud", "aws")

	const n = 32
	var wg sync.WaitGroup
	errs := make(chan error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			slab, err := m.Compose(context.Background(), memory.ComposeRequest{
				TenantID: tenantID, UserID: userID, AgentModule: "generic",
			})
			if err != nil {
				errs <- err
				return
			}
			if !strings.Contains(slab.Soul, "tone: terse") {
				errs <- assertFail("soul missing in concurrent Compose")
				return
			}
			if !strings.Contains(slab.Preferences, "preferred_cloud: aws") {
				errs <- assertFail("prefs missing in concurrent Compose")
				return
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Errorf("concurrent compose error: %v", err)
	}
}

// ── UPSERT SEMANTICS ────────────────────────────────────────────────────

// TestMultiplePrefsSameKey_LatestWins verifies that repeated PUT on the same
// preference key updates in place instead of creating duplicates.
func TestMultiplePrefsSameKey_LatestWins(t *testing.T) {
	tenantID, userID, m, cleanup := requireEdgeIntegration(t)
	defer cleanup()

	// Set preferred_cloud three times.
	setPref(t, m, tenantID, userID, "preferred_cloud", "gcp")
	setPref(t, m, tenantID, userID, "preferred_cloud", "azure")
	setPref(t, m, tenantID, userID, "preferred_cloud", "aws")

	slab := composeSlab(t, m, tenantID, userID)
	assert.Contains(t, slab.Preferences, "preferred_cloud: aws",
		"last write must win")
	// Old values must not appear (no duplicate rows surfacing).
	assert.NotContains(t, slab.Preferences, "preferred_cloud: gcp")
	assert.NotContains(t, slab.Preferences, "preferred_cloud: azure")

	// DB must contain exactly one row for this (tenant, user, key).
	db, err := common.GetDatabaseManager(common.Metastore)
	require.NoError(t, err)
	var count int
	err = db.Db.Get(&count, `
		SELECT COUNT(*) FROM llm_memory_preferences
		WHERE tenant_id = $1 AND user_id = $2 AND key = 'preferred_cloud'
	`, tenantID, userID)
	require.NoError(t, err)
	assert.Equal(t, 1, count,
		"upsert must keep exactly one row per (tenant, user, key)")
}

// assertFail wraps a string as an error so concurrent goroutines can ship
// assertion failures through a channel.
type assertError string

func (e assertError) Error() string { return string(e) }
func assertFail(msg string) error   { return assertError(msg) }
