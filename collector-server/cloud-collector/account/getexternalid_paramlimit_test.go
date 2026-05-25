package account

import (
	"fmt"
	"os"
	"testing"

	"nudgebee/collector/cloud/common"
	"nudgebee/collector/cloud/security"

	"github.com/stretchr/testify/assert"
)

// TestGetExternalIdAndResourceIdMap_ParameterLimit reproduces the failure mode
// fixed in this commit: passing a slice with > 65,535 elements to
// getExternalIdAndResourceIdMap previously hit PostgreSQL's positional-parameter
// cap because sqlx.In expanded the slice into one $N per element. The fix
// switches the predicate to ANY($2::text[]) and binds the slice as a single
// pq.Array, so the parameter count is constant regardless of slice size.
//
// Skipped automatically when TEST_DB or TEST_ACCOUNT are not set, since it
// requires a real PostgreSQL connection (the in-memory drivers don't enforce
// the parameter limit).
func TestGetExternalIdAndResourceIdMap_ParameterLimit(t *testing.T) {
	tenantId := os.Getenv("TEST_TENANT")
	accountId := os.Getenv("TEST_ACCOUNT")
	if tenantId == "" || accountId == "" {
		t.Skip("TEST_TENANT and TEST_ACCOUNT must be set")
	}

	ctx := security.NewRequestContextForTenantAdmin(tenantId)
	dbms, err := common.GetDatabaseManager(common.Metastore)
	assert.Nil(t, err)
	assert.NotNil(t, dbms)

	const n = 80000 // comfortably above the old 65535 cap
	ids := make([]string, 0, n)
	for i := 0; i < n; i++ {
		ids = append(ids, fmt.Sprintf("paramlimit-test-arn-%d", i))
	}

	result, err := getExternalIdAndResourceIdMap(ctx, dbms, accountId, ids)
	assert.NoError(t, err)
	assert.Equal(t, n, len(result), "every input id should appear in the result map (mapped or empty)")
}
