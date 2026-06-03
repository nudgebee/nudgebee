//go:build e2e

package agents

import (
	"nudgebee/llm/common"
	"testing"
)

// FetchRecentEventID returns the most recent event UUID for the given cloud
// account. Used by analyzer integration tests so we don't hardcode internal
// event IDs. Skips the test if no events exist for the account.
//
// Pattern: integration tests that previously hardcoded a specific event UUID
// (real internal data) now fetch a fresh one at runtime. The test bodies all
// assert "non-empty summary + COMPLETED status" — they smoke-test the analyzer,
// they don't validate event-type-specific behavior — so which event gets used
// doesn't matter for the assertion. See agents/TESTING.md.
func FetchRecentEventID(t *testing.T, accountID string) string {
	t.Helper()
	if accountID == "" {
		t.Skip("skipping: account ID unset")
	}
	dbms, err := common.GetDatabaseManager(common.Metastore)
	if err != nil {
		t.Skipf("skipping: db unavailable: %v", err)
	}
	var id string
	err = dbms.Db.QueryRow(
		`SELECT id FROM events WHERE cloud_account_id = $1 ORDER BY created_at DESC LIMIT 1`,
		accountID,
	).Scan(&id)
	if err != nil {
		t.Skipf("skipping: no recent events found for account %s: %v", accountID, err)
	}
	return id
}
