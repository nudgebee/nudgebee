package memory_test

import (
	"context"
	"testing"

	"nudgebee/llm/common"
	"nudgebee/llm/ee/memory"
	memprefs "nudgebee/llm/ee/memory/stores/preferences"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Keep used to be a plain "set", which promoted the row to source='explicit'.
// That did three damaging things at once (#35371): it nulled the evidence, it
// made the change irreversible (the upsert guard refuses to downgrade an
// explicit row), and it left the preference indistinguishable from a typed
// setting — so the b-Cortex page dropped it and Settings, which renders only
// five fixed keys, never showed it. Confirm keeps the injection behaviour and
// none of the damage.

// seedInferred writes one inferred preference carrying evidence, the way the
// extractor does, and returns nothing — the tests read it back through the DAO.
func seedInferred(t *testing.T, tenantID, userID, key string, value any) {
	t.Helper()
	require.NoError(t, func() error {
		_, err := memprefs.Upsert(&memprefs.Preference{
			TenantID:   tenantID,
			Scope:      memprefs.ScopeUser,
			UserID:     userID,
			Key:        key,
			Value:      value,
			Source:     memprefs.SourceInferred,
			Confidence: 0.8,
			Evidence:   map[string]any{"rationale": "said so in chat", "quote": "keep it short"},
		})
		return err
	}())
}

func findPref(t *testing.T, tenantID, userID, key string) (memprefs.Preference, bool) {
	t.Helper()
	rows, err := memprefs.ListForUser(tenantID, userID, "")
	require.NoError(t, err)
	for _, r := range rows {
		if r.Key == key {
			return r, true
		}
	}
	return memprefs.Preference{}, false
}

func TestPreferenceConfirmKeepsEvidenceAndStaysReversible(t *testing.T) {
	tenantID, userID, m, cleanup := requireEdgeIntegration(t)
	defer cleanup()

	const key = "confirm_roundtrip_verbosity"
	seedInferred(t, tenantID, userID, key, "concise")

	before, ok := findPref(t, tenantID, userID, key)
	require.True(t, ok, "seeded preference should be listed")
	require.Equal(t, memprefs.SourceInferred, before.Source)
	require.NotEmpty(t, before.Evidence, "extractor writes evidence")

	// Keep.
	resp, err := m.Mutate(context.Background(), memory.MutateRequest{
		TenantID: tenantID, UserID: userID, Layer: "preferences",
		Action: "confirm", Key: key,
		Value:     map[string]any{"agent_module": ""},
		ActorKind: "user", ActorID: userID,
	})
	require.NoError(t, err)
	require.True(t, resp.Success)
	require.False(t, resp.Skipped, "an inferred row was there to confirm")

	kept, ok := findPref(t, tenantID, userID, key)
	require.True(t, ok, "keeping must not remove the preference from the list — that was the bug")
	assert.Equal(t, memprefs.SourceExplicit, kept.Source, "kept prefs inject ambiently, so they must be explicit")
	assert.Equal(t, memprefs.OriginInferred, kept.Origin, "origin records that this came from inference")
	assert.NotNil(t, kept.ConfirmedAt, "confirmed_at is what marks it Kept in the UI")
	assert.True(t, kept.IsKept())
	assert.NotEmpty(t, kept.Evidence, "the evidence justifying the inference must survive being accepted")

	// Unkeep.
	resp, err = m.Mutate(context.Background(), memory.MutateRequest{
		TenantID: tenantID, UserID: userID, Layer: "preferences",
		Action: "unconfirm", Key: key,
		Value:     map[string]any{"agent_module": ""},
		ActorKind: "user", ActorID: userID,
	})
	require.NoError(t, err)
	require.True(t, resp.Success)

	back, ok := findPref(t, tenantID, userID, key)
	require.True(t, ok, "unkeeping returns it to review, it does not delete it")
	assert.Equal(t, memprefs.SourceInferred, back.Source)
	assert.Nil(t, back.ConfirmedAt)
	assert.False(t, back.IsKept())
	assert.Equal(t, memprefs.OriginInferred, back.Origin, "origin is write-once and survives the round trip")
}

// A preference the user typed into Settings has no inferred past, so Unkeep
// must not touch it. Without the origin guard this would silently downgrade a
// deliberate setting to an inferred guess that later extraction could overwrite.
func TestPreferenceUnconfirmIgnoresUserAuthored(t *testing.T) {
	tenantID, userID, m, cleanup := requireEdgeIntegration(t)
	defer cleanup()

	const key = memprefs.KeyDefaultNamespace
	_, err := m.Mutate(context.Background(), memory.MutateRequest{
		TenantID: tenantID, UserID: userID, Layer: "preferences",
		Action: "set", Key: key,
		Value:     map[string]any{"value": "payments", "agent_module": ""},
		ActorKind: "user", ActorID: userID,
	})
	require.NoError(t, err)

	resp, err := m.Mutate(context.Background(), memory.MutateRequest{
		TenantID: tenantID, UserID: userID, Layer: "preferences",
		Action: "unconfirm", Key: key,
		Value:     map[string]any{"agent_module": ""},
		ActorKind: "user", ActorID: userID,
	})
	require.NoError(t, err)
	assert.True(t, resp.Skipped, "nothing of inferred origin matched, so nothing changed")

	row, ok := findPref(t, tenantID, userID, key)
	require.True(t, ok)
	assert.Equal(t, memprefs.SourceExplicit, row.Source, "a typed setting stays explicit")
	assert.Empty(t, row.Origin)
}

// The mirror of TestPreferenceUnconfirmIgnoresUserAuthored. Without an
// origin/source guard on Confirm, confirming a typed setting would stamp
// origin='inferred' on it, which then lets Unconfirm downgrade it to
// source='inferred' — leaving a deliberate setting open to being overwritten
// by a later extractor inference.
func TestPreferenceConfirmIgnoresUserAuthored(t *testing.T) {
	tenantID, userID, m, cleanup := requireEdgeIntegration(t)
	defer cleanup()

	const key = memprefs.KeyDefaultNamespace
	_, err := m.Mutate(context.Background(), memory.MutateRequest{
		TenantID: tenantID, UserID: userID, Layer: "preferences",
		Action: "set", Key: key,
		Value:     map[string]any{"value": "payments", "agent_module": ""},
		ActorKind: "user", ActorID: userID,
	})
	require.NoError(t, err)

	resp, err := m.Mutate(context.Background(), memory.MutateRequest{
		TenantID: tenantID, UserID: userID, Layer: "preferences",
		Action: "confirm", Key: key,
		Value:     map[string]any{"agent_module": ""},
		ActorKind: "user", ActorID: userID,
	})
	require.NoError(t, err)
	assert.True(t, resp.Skipped, "a typed setting has no inferred origin to confirm")

	row, ok := findPref(t, tenantID, userID, key)
	require.True(t, ok)
	assert.Empty(t, row.Origin, "confirming must not invent an inferred origin")
	assert.Nil(t, row.ConfirmedAt)
	assert.False(t, row.IsKept())
}

// A key can exist as a kept inference and then be typed in Settings — several
// of the five Settings keys also appear as inferred rows. The explicit write
// must take the row over: leaving origin and confirmed_at behind would leave
// the user's own value reading as Kept and open to being "unkept" back to
// inferred, where a later extractor could overwrite it.
func TestExplicitWriteClearsKeptState(t *testing.T) {
	tenantID, userID, m, cleanup := requireEdgeIntegration(t)
	defer cleanup()

	const key = memprefs.KeyDefaultNamespace
	seedInferred(t, tenantID, userID, key, "inferred-ns")

	_, err := m.Mutate(context.Background(), memory.MutateRequest{
		TenantID: tenantID, UserID: userID, Layer: "preferences",
		Action: "confirm", Key: key,
		Value:     map[string]any{"agent_module": ""},
		ActorKind: "user", ActorID: userID,
	})
	require.NoError(t, err)
	kept, ok := findPref(t, tenantID, userID, key)
	require.True(t, ok)
	require.True(t, kept.IsKept(), "precondition: the row is kept")

	// The user now types their own value for the same key in Settings.
	_, err = m.Mutate(context.Background(), memory.MutateRequest{
		TenantID: tenantID, UserID: userID, Layer: "preferences",
		Action: "set", Key: key,
		Value:     map[string]any{"value": "typed-ns", "agent_module": ""},
		ActorKind: "user", ActorID: userID,
	})
	require.NoError(t, err)

	row, ok := findPref(t, tenantID, userID, key)
	require.True(t, ok)
	assert.Equal(t, "typed-ns", row.Value)
	assert.Empty(t, row.Origin, "an explicit write takes the row over")
	assert.Nil(t, row.ConfirmedAt)
	assert.False(t, row.IsKept(), "the user's own value must not read as a kept inference")
}

// Upsert stores keys trimmed, so the lookups must trim too. Without it a
// padded key passes the non-empty check and then matches nothing, reporting
// "not found" for a preference that is sitting right there.
func TestPreferenceConfirmTrimsKey(t *testing.T) {
	tenantID, userID, m, cleanup := requireEdgeIntegration(t)
	defer cleanup()

	const key = "confirm_trim_padding"
	seedInferred(t, tenantID, userID, key, "concise")

	resp, err := m.Mutate(context.Background(), memory.MutateRequest{
		TenantID: tenantID, UserID: userID, Layer: "preferences",
		Action: "confirm", Key: "  " + key + "  ",
		Value:     map[string]any{"agent_module": ""},
		ActorKind: "user", ActorID: userID,
	})
	require.NoError(t, err)
	require.False(t, resp.Skipped, "a padded key must still find the stored row")

	kept, ok := findPref(t, tenantID, userID, key)
	require.True(t, ok)
	assert.True(t, kept.IsKept())
}

// Confirming something already gone reports Skipped rather than a false
// success, so the UI can re-fetch instead of showing a Kept chip on a row the
// server no longer has.
func TestPreferenceConfirmMissingRowIsSkipped(t *testing.T) {
	tenantID, userID, m, cleanup := requireEdgeIntegration(t)
	defer cleanup()

	if _, err := common.GetDatabaseManager(common.Metastore); err != nil {
		t.Skipf("metastore unreachable: %v", err)
	}

	resp, err := m.Mutate(context.Background(), memory.MutateRequest{
		TenantID: tenantID, UserID: userID, Layer: "preferences",
		Action: "confirm", Key: "never_existed_key",
		Value:     map[string]any{"agent_module": ""},
		ActorKind: "user", ActorID: userID,
	})
	require.NoError(t, err)
	assert.True(t, resp.Skipped)
}
