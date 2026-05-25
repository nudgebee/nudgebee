package queue

import "time"

// KGUpdateMessage represents a message requesting KG update for a tenant
type KGUpdateMessage struct {
	TenantID      string    `json:"tenant_id"`      // Required: tenant to update
	Source        string    `json:"source"`         // What triggered: "cron", "webhook", "manual"
	RequestedAt   time.Time `json:"requested_at"`   // When requested
	CorrelationID string    `json:"correlation_id"` // For tracing
}
