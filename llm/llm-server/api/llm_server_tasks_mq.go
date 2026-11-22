package api

import (
	"context"
	"log/slog"
	"testing"

	"nudgebee/llm/common"
	"nudgebee/llm/config"
	"nudgebee/llm/knowledge"
)

// llmServerTask is a message on llm-server's task queue (#40807): work another
// service asks one llm-server replica to do once. It has the replica broadcast's
// shape, but a broadcast reaches every replica, while each task is taken by one.
// The JSON shape MUST stay in sync with the publishers (services-server:
// api/integration_cache.go).
type llmServerTask struct {
	Type   string   `json:"type"`
	Keys   []string `json:"keys,omitempty"`
	Sender string   `json:"sender,omitempty"`
}

// Task types llm-server handles, and what Keys holds for each. Task types are
// commands (a verb); broadcast types are facts (<thing>_changed).
const (
	// TaskSyncIntegrationKBs kicks the knowledge-base sync for these tenants
	// after an integration change. Keys: tenant ids.
	TaskSyncIntegrationKBs = "sync_integration_kbs"
)

func init() {
	// Do not connect to RabbitMQ in unit tests.
	if testing.Testing() {
		return
	}
	slog.Info("llm-server-tasks: registering rabbitmq consumer",
		"exchange", config.Config.RabbitMqLlmServerTasksExchange,
		"queue", config.Config.RabbitMqLlmServerTasksQueue)
	if err := common.MqConsume(config.Config.RabbitMqLlmServerTasksExchange, config.Config.RabbitMqLlmServerTasksQueue,
		config.Config.RabbitMqLlmServerTasksQueue, 1, processLlmServerTask); err != nil {
		slog.Error("llm-server-tasks: unable to register consumer", "error", err)
	}
}

func processLlmServerTask(_ context.Context, data []byte) error {
	return runLlmServerTask(data, knowledge.EnsureIntegrationKBsForTenant)
}

// runLlmServerTask runs one task. It always returns nil, so a task is never
// requeued: a malformed task or one of a type this replica does not know (a
// newer publisher during a rollout) is logged and dropped, and the work behind
// every task type has its own safety net (here the periodic knowledge-base sync).
func runLlmServerTask(data []byte, syncTenantKBs func(tenantID string)) error {
	var task llmServerTask
	if err := common.UnmarshalJson(data, &task); err != nil {
		slog.Error("llm-server-tasks: unable to decode task — dropping", "error", err, "data", string(data))
		return nil
	}
	switch task.Type {
	case TaskSyncIntegrationKBs:
		for _, tenantID := range task.Keys {
			if tenantID != "" {
				ensureIntegrationKBs(tenantID, syncTenantKBs)
			}
		}
		slog.Info("llm-server-tasks: task done", "type", task.Type, "keys", len(task.Keys), "sender", task.Sender)
	default:
		slog.Warn("llm-server-tasks: unknown task type — dropping", "type", task.Type, "sender", task.Sender)
	}
	return nil
}
