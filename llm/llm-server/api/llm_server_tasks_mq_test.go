package api

import (
	"context"
	"testing"

	"nudgebee/llm/common"
	"nudgebee/llm/config"
	"nudgebee/llm/tools/core"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// services-server publishes to this queue under the same names (pinned by its
// TestLlmServerTask_WireFormat).
func TestLlmServerTasks_DefaultNamesMatchServicesServer(t *testing.T) {
	assert.Equal(t, "llm_server_tasks", config.Config.RabbitMqLlmServerTasksExchange)
	assert.Equal(t, "llm_server_tasks", config.Config.RabbitMqLlmServerTasksQueue)
}

func TestRunLlmServerTask_SyncIntegrationKBsKicksEachTenant(t *testing.T) {
	var kicked []string
	require.NoError(t, runLlmServerTask(
		[]byte(`{"type":"sync_integration_kbs","keys":["tenant-1","","tenant-2"],"sender":"services-server/pod-a"}`),
		func(id string) { kicked = append(kicked, id) }))
	assert.Equal(t, []string{"tenant-1", "tenant-2"}, kicked)
}

// Never requeued: a malformed task, or a type this replica does not know yet,
// is dropped without running anything.
func TestRunLlmServerTask_DropsUnusableTasks(t *testing.T) {
	for _, data := range []string{"not-json", `{}`, `{"type":"some_future_task","keys":["k"]}`} {
		called := false
		assert.NoError(t, runLlmServerTask([]byte(data), func(string) { called = true }), "%q must be dropped, not requeued", data)
		assert.False(t, called, data)
	}
}

func TestRunLlmServerTask_SurvivesAPanickingKick(t *testing.T) {
	var kicked []string
	assert.NotPanics(t, func() {
		_ = runLlmServerTask([]byte(`{"type":"sync_integration_kbs","keys":["tenant-1","tenant-2"]}`), func(id string) {
			kicked = append(kicked, id)
			if id == "tenant-1" {
				panic("kb sync failed")
			}
		})
	})
	assert.Equal(t, []string{"tenant-1", "tenant-2"}, kicked, "a panic for one tenant must not skip the rest")
}

// handleIntegrationChanged drops each distinct account's caches once, and the
// integration_changed broadcast reaches it.
func TestHandleIntegrationChanged_DedupsAndDropsCaches(t *testing.T) {
	const id = "acct-integration-change-no-kick"
	core.SetMCPIntegrationToolCache(id, []core.NBTool{})
	defer core.ClearMCPIntegrationToolCache(id)

	assert.Equal(t, []string{id}, handleIntegrationChanged([]string{id, id, ""}))
	_, present := core.GetMCPIntegrationToolCache(id)
	assert.False(t, present, "a direct call drops the cache")

	core.SetMCPIntegrationToolCache(id, []core.NBTool{})
	common.ReplicaBroadcast(context.Background(), common.ChangeIntegration, id)
	_, present = core.GetMCPIntegrationToolCache(id)
	assert.False(t, present, "the broadcast drops the cache")
}
