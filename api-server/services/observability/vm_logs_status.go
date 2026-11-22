package observability

import (
	"errors"
	"fmt"
	"time"

	"nudgebee/services/account"
	"nudgebee/services/security"
)

// VM logs connection status, the counterpart of CheckVMMetricsStatus. Unlike metrics it
// does not attribute logs to individual VMs (that needs the node-agent's log label
// contract), so the answer is about the store, not the account.

// The state values match the VM metrics states so the UI treats them alike.
const (
	VMLogsNotConfigured = "not_configured"
	VMLogsCheckFailed   = "check_failed"
	VMLogsUnreachable   = "unreachable"
	VMLogsNoData        = "no_data"
	VMLogsConnected     = "connected"
)

// vmLogsLookback is how recent a stream must be to count as live; it matches the metrics window.
const vmLogsLookback = 10 * time.Minute

// VMLogsStatus is the logs connection state of a self-hosted VM account.
type VMLogsStatus struct {
	State           string `json:"state"`
	Provider        string `json:"provider,omitempty"`
	IntegrationId   string `json:"integration_id,omitempty"`
	IntegrationName string `json:"integration_name,omitempty"`
	Error           string `json:"error,omitempty"`
}

// listRecentLogLabels returns a non-empty result when the account's log store holds
// logs inside the lookback window. It asks the provider's own source, not
// FetchLogLabels, which unions in the canonical vocabulary and so is never empty.
// SigNoz's label endpoint takes no time range, so it is checked with a one-row
// logs query over the window instead.
var listRecentLogLabels = func(ctx *security.RequestContext, accountId, provider, source string, now time.Time) ([]OutputLogLabel, error) {
	logSource, err := getLogSourceForAccount(ctx, accountId, provider, source)
	if err != nil {
		return nil, err
	}
	if provider == "signoz" {
		logs, err := logSource.QueryLogs(ctx, FetchLogRequest{
			AccountId:         accountId,
			LogProvider:       provider,
			LogProviderSource: source,
			Query:             "[]",
			StartTime:         now.Add(-vmLogsLookback).UnixMilli(),
			EndTime:           now.UnixMilli(),
			Limit:             1,
		})
		if err != nil || len(logs) == 0 {
			return nil, err
		}
		return []OutputLogLabel{{Label: "body"}}, nil
	}
	return logSource.QueryLabels(ctx, FetchLogLabelRequest{
		AccountId:         accountId,
		LogProvider:       provider,
		LogProviderSource: source,
		StartTime:         now.Add(-vmLogsLookback).UnixMilli(),
		EndTime:           now.UnixMilli(),
	})
}

// CheckVMLogsStatus reports whether a self-hosted VM account has working logs. Like the
// metrics check, it queries the store first and runs the connection test only on failure.
func CheckVMLogsStatus(ctx *security.RequestContext, accountId string) (VMLogsStatus, error) {
	if accountId == "" {
		return VMLogsStatus{}, fmt.Errorf("observability: account_id is required")
	}
	var status VMLogsStatus

	provider, source, integration, err := resolveProviderForStatus(ctx, accountId, "", "logs", "")
	switch {
	case err != nil && !errors.Is(err, account.ErrAgentNotConnected):
		status.State = VMLogsCheckFailed
		status.Error = err.Error()
		return status, nil
	case err != nil || integration == nil || source == "agent":
		// Nothing a VM account can read from: no default logs integration, or only a
		// Kubernetes agent's (which a VM account does not have).
		status.State = VMLogsNotConfigured
		return status, nil
	}

	status.Provider = provider
	status.IntegrationId = integration.Id
	status.IntegrationName = integration.Name

	labels, queryErr := listRecentLogLabels(ctx, accountId, provider, source, time.Now())
	if queryErr != nil {
		status.State = VMLogsUnreachable
		status.Error = queryErr.Error()
		if probeErr := probeIntegrationForAccount(ctx, integration.Id, accountId); probeErr != nil {
			status.Error = probeErr.Error()
		}
		return status, nil
	}

	if len(labels) == 0 {
		status.State = VMLogsNoData
		return status, nil
	}
	status.State = VMLogsConnected
	return status, nil
}
