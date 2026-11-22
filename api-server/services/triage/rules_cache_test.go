package triage

import (
	"context"
	"testing"
	"time"

	"nudgebee/services/common"

	"github.com/stretchr/testify/require"
)

func seedTriageRulesCache(keys ...string) {
	triageRulesCacheMu.Lock()
	defer triageRulesCacheMu.Unlock()
	triageRulesCache = make(map[string]triageRulesCacheEntry)
	for _, key := range keys {
		triageRulesCache[key] = triageRulesCacheEntry{rules: []TriageRule{}, expiresAt: time.Now().Add(time.Hour)}
	}
}

func cachedTriageKeys() []string {
	triageRulesCacheMu.RLock()
	defer triageRulesCacheMu.RUnlock()
	keys := make([]string, 0, len(triageRulesCache))
	for key := range triageRulesCache {
		keys = append(keys, key)
	}
	return keys
}

func TestClearTriageRulesCache_DropsOnlyThatTenant(t *testing.T) {
	seedTriageRulesCache("tenant-a:acct-1", "tenant-a:acct-2", "tenant-ab:acct-3", "tenant-b:acct-4")

	ClearTriageRulesCache("tenant-a")

	require.ElementsMatch(t, []string{"tenant-ab:acct-3", "tenant-b:acct-4"}, cachedTriageKeys())
}

func TestTriageRulesResync_DropsEverything(t *testing.T) {
	seedTriageRulesCache("tenant-a:acct-1", "tenant-b:acct-4")

	// What a pod receives after it (re)subscribes.
	common.ReplicaBroadcastAll(context.Background(), common.ChangeTriageRules)

	require.Empty(t, cachedTriageKeys())
}
