package agents

import (
	"strings"
	"testing"
	"time"

	"nudgebee/llm/common"
	"nudgebee/llm/security"

	"github.com/stretchr/testify/assert"
)

// clearFinOpsContextCache drops both keys for an account so each test starts
// from a known state.
func clearFinOpsContextCache(t *testing.T, accountId string) {
	t.Helper()
	_ = common.CacheDelete(finOpsAccountContextCacheNS, finOpsContextKey(accountId))
	_ = common.CacheDelete(finOpsAccountContextCacheNS, finOpsContextFreshKey(accountId))
}

// A cached-but-stale block is served as-is and the refresh happens behind the
// turn. Building it runs three metastore aggregates (3.6s median, 23.6s tail on
// dev) from inside GetSystemPrompt, so a rebuild must never be on the path of a
// turn that already has something to serve.
func TestFinOpsAccountContext_StaleIsServedWithoutBlocking(t *testing.T) {
	const accountId = "acct-finops-stale"
	clearFinOpsContextCache(t, accountId)
	t.Cleanup(func() { clearFinOpsContextCache(t, accountId) })

	stale := "<account_context>\nCACHED-STALE-BLOCK\n</account_context>"
	assert.NoError(t, common.CacheSet(finOpsAccountContextCacheNS, finOpsContextKey(accountId),
		[]byte(stale), common.CacheSetWithExpiration(finOpsContextRetainFor)))
	// No freshness marker: the entry is past its window and due a refresh.

	agent := &FinOpsAgent{accountId: accountId}
	start := time.Now()
	got := agent.fetchFinOpsAccountContext(security.NewRequestContextForSuperAdmin())
	elapsed := time.Since(start)

	assert.Equal(t, stale, got, "the stale block must be served verbatim, not rebuilt")
	assert.Less(t, elapsed, finOpsContextFirstBuildBudget,
		"serving a cached block must not wait on the rebuild")

	_, marked := common.CacheGet(finOpsAccountContextCacheNS, finOpsContextFreshKey(accountId))
	assert.True(t, marked, "the refresh must claim the freshness marker so concurrent turns don't stack rebuilds")
}

// A fresh entry is served with no refresh triggered at all.
func TestFinOpsAccountContext_FreshIsServedUntouched(t *testing.T) {
	const accountId = "acct-finops-fresh"
	clearFinOpsContextCache(t, accountId)
	t.Cleanup(func() { clearFinOpsContextCache(t, accountId) })

	fresh := "<account_context>\nCACHED-FRESH-BLOCK\n</account_context>"
	assert.NoError(t, common.CacheSet(finOpsAccountContextCacheNS, finOpsContextKey(accountId),
		[]byte(fresh), common.CacheSetWithExpiration(finOpsContextRetainFor)))
	assert.NoError(t, common.CacheSet(finOpsAccountContextCacheNS, finOpsContextFreshKey(accountId),
		[]byte("1"), common.CacheSetWithExpiration(finOpsContextFreshFor)))

	agent := &FinOpsAgent{accountId: accountId}
	got := agent.fetchFinOpsAccountContext(security.NewRequestContextForSuperAdmin())

	assert.Equal(t, fresh, got)
}

// With a first build already in flight — marker claimed, nothing cached yet —
// a concurrent turn takes the footprint-only block immediately instead of
// starting a second build or waiting out the budget for one it can't use.
func TestFinOpsAccountContext_InFlightFirstBuildIsNotStacked(t *testing.T) {
	const accountId = "acct-finops-inflight"
	clearFinOpsContextCache(t, accountId)
	t.Cleanup(func() { clearFinOpsContextCache(t, accountId) })

	assert.NoError(t, common.CacheSet(finOpsAccountContextCacheNS, finOpsContextFreshKey(accountId),
		[]byte("1"), common.CacheSetWithExpiration(finOpsContextFreshFor)))

	agent := &FinOpsAgent{accountId: accountId}
	got := agent.fetchFinOpsAccountContext(security.NewRequestContextForSuperAdmin())

	assert.True(t, strings.HasPrefix(got, "<account_context>"), "a well-formed block is still returned")

	// No wall-clock assertion here: this path renders the footprint-only block,
	// whose cost is AccountConfigSummary's — sub-ms once cached (every FinOps turn
	// warms it via GetSupportedTools) but a full DB fallthrough in a bare test.
	//
	// The guarantee to pin is that no second build was started. A build writes the
	// content key when it finishes, so the key staying empty past the window a
	// build needs is the observable proof it never ran.
	deadline := time.Now().Add(2 * finOpsContextFirstBuildBudget)
	for time.Now().Before(deadline) {
		if _, cached := common.CacheGet(finOpsAccountContextCacheNS, finOpsContextKey(accountId)); cached {
			t.Fatal("a second build ran while one was already in flight — the marker guard did not hold")
		}
		time.Sleep(50 * time.Millisecond)
	}
}
