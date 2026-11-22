package account

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var archiveUpdate = regexp.MustCompile(`(?is)update\s+cloud_resourses\s+set\s+is_active\s*=\s*false\s*,\s*status\s*=\s*'Deleted'\s+where`)

// Every archive UPDATE must carry notYetArchived, otherwise a sync that finds
// nothing rewrites each already-deleted row of the service on every run.
// Statements that already guard on `status != 'Deleted'` are exempt.
func TestArchiveUpdatesSkipRowsAlreadyDeleted(t *testing.T) {
	src, err := os.ReadFile("etl_resources.go")
	require.NoError(t, err)
	content := string(src)

	matches := archiveUpdate.FindAllStringIndex(content, -1)
	seen := 0
	for i, m := range matches {
		end := min(m[1]+300, len(content))
		if i+1 < len(matches) {
			end = min(end, matches[i+1][0])
		}
		tail := content[m[1]:end]
		if strings.Contains(tail, "status != 'Deleted'") {
			continue
		}
		seen++
		assert.Contains(t, tail, "notYetArchived", "unguarded archive UPDATE: %.120s", content[m[0]:m[1]])
	}
	assert.Equal(t, 6, seen, "archive UPDATE count changed; update this test with it")
}

// The archive UPDATEs find their rows through
// idx_cloud_resourses_live_account_service, and Postgres uses a partial index
// only when the query implies its predicate. If the guard and the index
// predicate drift apart nothing fails: the UPDATEs quietly go back to reading
// an account's whole history on every sync. Skipped when the migrations tree
// is not next to this module (an isolated build of the collector).
func TestArchiveGuardMatchesLiveRowIndexPredicate(t *testing.T) {
	migrations, err := filepath.Glob("../../../api-server/migrations/migrations/app/*_index_cloud_resourses_live_account_service.up.sql")
	require.NoError(t, err)
	if len(migrations) == 0 {
		if _, statErr := os.Stat("../../../api-server/migrations/migrations/app"); statErr != nil {
			t.Skip("migrations tree not available")
		}
		t.Fatal("migration creating idx_cloud_resourses_live_account_service not found")
	}
	require.Len(t, migrations, 1)

	sql, err := os.ReadFile(migrations[0])
	require.NoError(t, err)
	var statement strings.Builder
	for _, line := range strings.Split(string(sql), "\n") {
		if !strings.HasPrefix(strings.TrimSpace(line), "--") {
			statement.WriteString(line + " ")
		}
	}
	predicate := regexp.MustCompile(`(?is)\bWHERE\s+(.+?)\s*;`).FindStringSubmatch(statement.String())
	require.Len(t, predicate, 2, "no WHERE predicate in %s", migrations[0])

	normalize := func(expr string) string {
		return strings.ToLower(strings.Join(strings.Fields(strings.Trim(strings.TrimSpace(expr), "()")), " "))
	}
	assert.Equal(t, normalize(predicate[1]), normalize(notYetArchived))
}
