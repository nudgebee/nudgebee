package queue

import "time"

// VMScanMessage represents a request to sweep+inventory+vuln-match a single
// discovery-type integration. Kept minimal (ids only, no labels) so the
// consumer always re-resolves current state via
// vmpackage.GetDiscoveryDatasourceByID rather than acting on a possibly
// stale copy sitting in the queue.
type VMScanMessage struct {
	IntegrationID string    `json:"integration_id"`
	TenantID      string    `json:"tenant_id"`
	AccountID     string    `json:"account_id"`
	Source        string    `json:"source"` // What triggered: "cron", "manual"
	RequestedAt   time.Time `json:"requested_at"`
	CorrelationID string    `json:"correlation_id"`
}
