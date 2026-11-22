package core

import (
	"context"
	"testing"
	"time"

	"nudgebee/services/common"

	"github.com/stretchr/testify/require"
)

func TestIntegrationChange_DropsOnlyThoseAccounts(t *testing.T) {
	integrationByTypeCache.Lock()
	integrationByTypeCache.entries = map[string]integrationCacheEntry{
		"acct-1:loki:tenant-a":  {expiresAt: time.Now().Add(time.Hour)},
		"acct-1:slack:tenant-a": {expiresAt: time.Now().Add(time.Hour)},
		"acct-10:loki:tenant-a": {expiresAt: time.Now().Add(time.Hour)},
		"acct-2:loki:tenant-a":  {expiresAt: time.Now().Add(time.Hour)},
	}
	integrationByTypeCache.Unlock()

	common.ReplicaBroadcast(context.Background(), common.ChangeIntegration, "acct-1")

	integrationByTypeCache.RLock()
	defer integrationByTypeCache.RUnlock()
	keys := make([]string, 0, len(integrationByTypeCache.entries))
	for key := range integrationByTypeCache.entries {
		keys = append(keys, key)
	}
	require.ElementsMatch(t, []string{"acct-10:loki:tenant-a", "acct-2:loki:tenant-a"}, keys)
}
