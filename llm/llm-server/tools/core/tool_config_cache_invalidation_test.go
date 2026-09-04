package core

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"nudgebee/llm/common"
)

// seedToolConfigCaches writes one entry of each key family the llm_tool_config
// namespace holds, tagged the way the production set sites tag them.
func seedToolConfigCaches(t *testing.T, accountId string) {
	t.Helper()
	tag := common.CacheSetWithTags(ToolConfigAccountTag(accountId))
	require.NoError(t, common.CacheSet(CacheNamespaceLlmToolConfig,
		"account_config_summary:v3:"+accountId, []byte(`{"HasAgent":true}`), tag))
	require.NoError(t, common.CacheSet(CacheNamespaceLlmToolConfig,
		fmt.Sprintf("list_tool_configs:%s:%s", accountId, "kubectl_execute"), []byte(`[]`), tag))
	require.NoError(t, common.CacheSet(CacheNamespaceLlmToolConfig,
		"list_all_tool_configs:"+accountId, []byte(`[]`), tag))
	require.NoError(t, common.CacheSet(CacheNamespaceLlmToolConfig,
		fmt.Sprintf("k8s_account_state:%s:%d", accountId, 100), []byte(`{}`), tag))
}

func toolConfigCached(accountId string) []bool {
	out := make([]bool, 0, 4)
	for _, key := range []string{
		"account_config_summary:v3:" + accountId,
		fmt.Sprintf("list_tool_configs:%s:%s", accountId, "kubectl_execute"),
		"list_all_tool_configs:" + accountId,
		fmt.Sprintf("k8s_account_state:%s:%d", accountId, 100),
	} {
		_, ok := common.CacheGet(CacheNamespaceLlmToolConfig, key)
		out = append(out, ok)
	}
	return out
}

// TestInvalidateToolConfigCache_ClearsEveryKeyFamilyForTheAccount is the
// regression guard for tool/integration changes not sticking. Only the
// process-local maps were cleared, and GetAccountConfigSummary re-seeds its map
// FROM the shared cache on the next call — so the stale summary came straight
// back and the account kept resolving tools against integrations it no longer
// had, until the 30-minute TTL expired.
//
// The namespace holds four key families and two of them (per-tool, per-limit)
// cannot be enumerated at invalidation time, which is why the entries carry an
// account tag instead of being deleted by key.
func TestInvalidateToolConfigCache_ClearsEveryKeyFamilyForTheAccount(t *testing.T) {
	const accountId = "acct-tool-config-invalidation"
	seedToolConfigCaches(t, accountId)

	require.Equal(t, []bool{true, true, true, true}, toolConfigCached(accountId), "seed must be cached")

	InvalidateToolConfigCache(accountId)

	assert.Equal(t, []bool{false, false, false, false}, toolConfigCached(accountId),
		"every key family derived from the account must be dropped")
}

// TestInvalidateToolConfigCache_LeavesOtherAccountsAlone pins the blast radius:
// one account's integration change must not evict every other account's
// resolved tool configuration and force a DB re-read for the whole fleet.
func TestInvalidateToolConfigCache_LeavesOtherAccountsAlone(t *testing.T) {
	const mine, theirs = "acct-mine", "acct-theirs"
	seedToolConfigCaches(t, mine)
	seedToolConfigCaches(t, theirs)

	InvalidateToolConfigCache(mine)

	assert.Equal(t, []bool{false, false, false, false}, toolConfigCached(mine))
	assert.Equal(t, []bool{true, true, true, true}, toolConfigCached(theirs),
		"another account's entries must survive")
}

// TestInvalidateToolConfigCache_EmptyAccountIsNoOp guards against an empty
// account id turning into a namespace-wide wipe.
func TestInvalidateToolConfigCache_EmptyAccountIsNoOp(t *testing.T) {
	const accountId = "acct-empty-guard"
	seedToolConfigCaches(t, accountId)

	InvalidateToolConfigCache("")

	assert.Equal(t, []bool{true, true, true, true}, toolConfigCached(accountId),
		"an empty account id must not invalidate anything")
}

// TestInvalidateCachesForAccount_ClearsSharedToolConfigEntries pins the wiring,
// not just the invalidator: the shared llm_tool_config entries must be dropped by
// the account-scoped funnel that tool changes and the cross-replica invalidation
// subscriber both go through. InvalidateToolConfigCache reaches it by registering
// as a tool cache invalidator, so this fails if that registration is dropped or
// the funnel stops running the registered invalidators.
func TestInvalidateCachesForAccount_ClearsSharedToolConfigEntries(t *testing.T) {
	const accountId = "acct-funnel-clears-shared"
	seedToolConfigCaches(t, accountId)
	require.Equal(t, []bool{true, true, true, true}, toolConfigCached(accountId), "seed must be cached")

	invalidateCachesForAccount(accountId)

	assert.Equal(t, []bool{false, false, false, false}, toolConfigCached(accountId),
		"the funnel must clear the shared entries, not just the in-memory maps")
}
