package dashboard

import (
	"encoding/json"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Replays a definition captured verbatim from a real dashboard row, to prove the
// upgrader fixes the panels that actually shipped rather than a fixture shaped
// to pass. Skips when the capture is absent so CI stays hermetic.
//
// Capture with:
//
//	psql -At -c "select definition from public.dashboards order by updated_at desc limit 1" > /tmp/real_def.json
func TestUpgradeDefinition_AgainstCapturedRow(t *testing.T) {
	path := os.Getenv("DASHBOARD_DEF_FIXTURE")
	if path == "" {
		t.Skip("set DASHBOARD_DEF_FIXTURE to a captured definition to run this")
	}
	raw, err := os.ReadFile(path)
	require.NoError(t, err)

	var def Definition
	require.NoError(t, json.Unmarshal(raw, &def))
	require.NotEmpty(t, def.Panels, "captured definition has no panels")

	// Before: the stored shape is exactly what the UI reported as "No account".
	for _, p := range def.Panels {
		assert.Empty(t, p.AccountType)
		assert.Empty(t, p.AccountIds)
	}

	upgradeDefinition(&def)

	for _, p := range def.Panels {
		assert.NotEmpty(t, p.AccountIds, "panel %q still has no accounts after upgrade", p.Title)
		assert.Empty(t, p.LegacyAccountId)
	}
	require.NoError(t, ValidateDefinition(def), "upgraded definition must save cleanly")
}
