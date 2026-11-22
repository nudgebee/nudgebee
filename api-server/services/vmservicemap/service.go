package vmservicemap

import (
	"log/slog"
	"time"

	"nudgebee/services/traces"
)

// Get builds the service map of one VM account over the window ending at end.
func Get(accountID, tenantID string, window time.Duration, end time.Time) (traces.ServiceMap, error) {
	if window <= 0 {
		window = DefaultWindow
	}
	if window > MaxWindow {
		window = MaxWindow
	}
	store, err := ResolveStore(accountID, tenantID)
	if err != nil {
		return traces.ServiceMap{}, err
	}
	slog.Info("vmservicemap: asking the forager", "account_id", accountID,
		"datasource_key", store.DatasourceKey, "agent_type", store.AgentType, "window", window)
	resp, err := Fetch(accountID, store, end.Add(-window), end)
	if err != nil {
		return traces.ServiceMap{}, err
	}
	slog.Info("vmservicemap: forager answered", "account_id", accountID,
		"hosts", len(resp.Hosts), "edges", len(resp.Edges))

	return Build(resp, end), nil
}
