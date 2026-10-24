package core

import (
	"sync"
	"testing"
	"time"

	"nudgebee/llm/common"
	toolcore "nudgebee/llm/tools/core"

	"github.com/stretchr/testify/assert"
)

// Saving an organisation agent touches every account of the tenant; it must
// send one agent_config_changed per batch, not one per account.
func TestInvalidateOrgAgentCaches_OneBroadcastPerBatch(t *testing.T) {
	orig := toolcore.TenantAccountIDs
	t.Cleanup(func() { toolcore.TenantAccountIDs = orig })
	toolcore.TenantAccountIDs = func(string) []string { return []string{"a1", "a2", "a3"} }

	var mu sync.Mutex
	var broadcasts [][]string
	got := make(chan struct{}, 4)
	t.Cleanup(common.OnReplicaBroadcast(common.ChangeAgentConfig, common.ReplicaHandler{
		OnKeys: func(keys []string) {
			mu.Lock()
			broadcasts = append(broadcasts, keys)
			mu.Unlock()
			got <- struct{}{}
		},
		OnAll: func() {},
	}))

	invalidateOrgAgentCaches("tenant-1", []string{"a2"}, "old_name", "new_name")

	for i := 0; i < 2; i++ {
		select {
		case <-got:
		case <-time.After(5 * time.Second):
			t.Fatal("expected two broadcasts")
		}
	}
	mu.Lock()
	defer mu.Unlock()
	assert.Equal(t, [][]string{
		{"a2:old_name", "a2:new_name"},
		{"a1:old_name", "a1:new_name", "a3:old_name", "a3:new_name"},
	}, broadcasts)
}
