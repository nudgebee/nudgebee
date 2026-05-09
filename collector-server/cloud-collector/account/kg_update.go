package account

import (
	"log/slog"
	"sync"
	"time"

	"nudgebee/collector/cloud/common"
	"nudgebee/collector/cloud/config"

	"github.com/google/uuid"
)

// kgUpdateMessage matches the KGUpdateMessage struct expected by the api-server consumer.
// Fields use json tags matching api-server/services/knowledge_graph/queue/types.go
type kgUpdateMessage struct {
	TenantID      string    `json:"tenant_id"`
	Source        string    `json:"source"`
	RequestedAt   time.Time `json:"requested_at"`
	CorrelationID string    `json:"correlation_id"`
}

const kgUpdateCacheTTL = time.Hour

var (
	kgUpdateCache   = make(map[string]time.Time)
	kgUpdateCacheMu sync.Mutex
)

// publishKGUpdate publishes a KG update message for a tenant.
// Each tenant_id is published at most once per hour (deduplicated via in-memory cache).
// Failures are logged but do NOT propagate — KG updates are best-effort.
func publishKGUpdate(tenantID string) {
	now := time.Now().UTC()

	kgUpdateCacheMu.Lock()
	// Evict expired entries to prevent unbounded cache growth.
	for tid, ts := range kgUpdateCache {
		if now.Sub(ts) >= kgUpdateCacheTTL {
			delete(kgUpdateCache, tid)
		}
	}
	if lastPublished, ok := kgUpdateCache[tenantID]; ok && now.Sub(lastPublished) < kgUpdateCacheTTL {
		kgUpdateCacheMu.Unlock()
		slog.Debug("kg_update: skipping duplicate publish (within 1h cache TTL)",
			"tenant_id", tenantID,
			"last_published", lastPublished,
		)
		return
	}
	kgUpdateCache[tenantID] = now
	kgUpdateCacheMu.Unlock()

	message := kgUpdateMessage{
		TenantID:      tenantID,
		Source:        "cloud-collector",
		RequestedAt:   now,
		CorrelationID: uuid.New().String(),
	}

	err := common.MqPublish(
		config.Config.RabbitMqKGUpdateExchange,
		config.Config.RabbitMqKGUpdateQueue,
		message,
		common.MqPublishWithExpiration(2*time.Hour),
	)
	if err != nil {
		// Evict from cache so a retry is allowed on next call.
		kgUpdateCacheMu.Lock()
		delete(kgUpdateCache, tenantID)
		kgUpdateCacheMu.Unlock()

		slog.Error("kg_update: failed to publish KG update message",
			"tenant_id", tenantID,
			"error", err,
		)
		return
	}

	slog.Info("kg_update: published KG update message",
		"tenant_id", tenantID,
		"correlation_id", message.CorrelationID,
	)
}
