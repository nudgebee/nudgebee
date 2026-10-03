package aws

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func clearAccountTenantCache(t *testing.T) {
	t.Helper()
	accountTenantCacheMu.Lock()
	accountTenantCache = make(map[string]accountTenantEntry)
	accountTenantLastPrune = time.Time{}
	accountTenantCacheMu.Unlock()
}

// TestAccountTenantCache_TwoTenantsOnOneAwsAccountDoNotCollide is the regression
// guard for the bug this cache had while it was keyed by AWS account number: the
// same AWS account number can belong to multiple Nudgebee tenants — the reason
// accountLookupCache is keyed by (external_id, account_number) — so both tenants
// shared one entry and whichever resolved last won. updateCloudResource then
// wrote a resource status update against the wrong tenant.
func TestAccountTenantCache_TwoTenantsOnOneAwsAccountDoNotCollide(t *testing.T) {
	clearAccountTenantCache(t)

	// Two Nudgebee accounts for the SAME AWS account number, owned by different tenants.
	const accountA, tenantA = "acct-uuid-a", "tenant-a"
	const accountB, tenantB = "acct-uuid-b", "tenant-b"

	setAccountTenant(accountA, tenantA)
	setAccountTenant(accountB, tenantB)

	gotA, foundA := GetAccountTenant(accountA)
	require.True(t, foundA)
	assert.Equal(t, tenantA, gotA, "account A must resolve to its own tenant")

	gotB, foundB := GetAccountTenant(accountB)
	require.True(t, foundB)
	assert.Equal(t, tenantB, gotB, "account B must not inherit account A's tenant")
}

// TestAccountTenantCache_ExpiredEntryIsAMiss pins the TTL: the entry is refreshed
// on every account resolution, so an aged-out one must report a miss rather than
// hand back metadata for an account that may have been deleted or re-onboarded.
func TestAccountTenantCache_ExpiredEntryIsAMiss(t *testing.T) {
	clearAccountTenantCache(t)

	const accountId = "acct-uuid-expired"
	accountTenantCacheMu.Lock()
	accountTenantCache[accountId] = accountTenantEntry{tenantId: "tenant-x", expiry: time.Now().Add(-time.Minute)}
	accountTenantCacheMu.Unlock()

	_, found := GetAccountTenant(accountId)
	assert.False(t, found, "an expired entry must be a miss")
}

// TestAccountTenantCache_PrunesExpiredEntriesOnWrite pins the bound: the map had
// no eviction at all, so every account the process ever saw stayed resident.
func TestAccountTenantCache_PrunesExpiredEntriesOnWrite(t *testing.T) {
	clearAccountTenantCache(t)

	accountTenantCacheMu.Lock()
	accountTenantCache["stale-1"] = accountTenantEntry{tenantId: "t", expiry: time.Now().Add(-time.Hour)}
	accountTenantCache["stale-2"] = accountTenantEntry{tenantId: "t", expiry: time.Now().Add(-time.Hour)}
	accountTenantCacheMu.Unlock()

	setAccountTenant("fresh", "tenant-fresh")

	accountTenantCacheMu.RLock()
	defer accountTenantCacheMu.RUnlock()
	assert.Len(t, accountTenantCache, 1, "aged-out entries must be dropped on write")
	_, ok := accountTenantCache["fresh"]
	assert.True(t, ok, "the entry just written must survive its own prune")
}

// TestAccountTenantCache_EmptyAccountIdIsNeverCached guards the shared-bucket
// case: an unresolved account must not write or read a "" key that every other
// unresolved account would also hit.
func TestAccountTenantCache_EmptyAccountIdIsNeverCached(t *testing.T) {
	clearAccountTenantCache(t)

	setAccountTenant("", "tenant-should-not-be-stored")

	_, found := GetAccountTenant("")
	assert.False(t, found, "an empty account id must never resolve to a tenant")

	accountTenantCacheMu.RLock()
	defer accountTenantCacheMu.RUnlock()
	assert.Empty(t, accountTenantCache, "an empty account id must not be cached")
}

// TestAccountTenantCache_SweepIsAmortisedNotPerWrite pins that the sweep runs at
// most once per interval. Sweeping on every write puts an O(N) scan on the hot
// path — a write happens per resolved SQS message — while holding the exclusive
// lock every other consumer goroutine needs.
func TestAccountTenantCache_SweepIsAmortisedNotPerWrite(t *testing.T) {
	clearAccountTenantCache(t)

	// A sweep just happened, and an entry has aged out since.
	accountTenantCacheMu.Lock()
	accountTenantLastPrune = time.Now()
	accountTenantCache["stale"] = accountTenantEntry{tenantId: "t", expiry: time.Now().Add(-time.Hour)}
	accountTenantCacheMu.Unlock()

	setAccountTenant("fresh", "tenant-fresh")

	accountTenantCacheMu.RLock()
	_, stillThere := accountTenantCache["stale"]
	accountTenantCacheMu.RUnlock()
	assert.True(t, stillThere, "a write inside the interval must not trigger another full scan")

	// Deferring the sweep must never let an expired entry be served: reads check
	// expiry themselves, so the sweep bounds memory rather than enforcing the TTL.
	_, found := GetAccountTenant("stale")
	assert.False(t, found, "an unswept but expired entry must still read as a miss")
}
