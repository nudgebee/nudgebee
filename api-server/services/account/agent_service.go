package account

import (
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"nudgebee/services/common"
	"nudgebee/services/internal/database"
	"nudgebee/services/internal/database/models"
	"nudgebee/services/security"
	"strings"
	"sync"
	"time"
)

// agentConnectThresholdMinutes is how long an agent can go unheard-from
// before this cron retires it.
//
// MUST stay comfortably above relay-server's relaySessionHeartbeatInterval
// (10m, relay-server/pkg/server/handlers/register.go); its
// TestHeartbeatFitsCronThreshold pins the relationship from the other side.
const agentConnectThresholdMinutes = 30

// AgentCheckAndUpdateStatus retires agents nothing has heard from recently.
//
// It reads two independent signals, and only retires an agent when BOTH are
// stale:
//
//   - last_connected_at — when the agent's current session STARTED (written by
//     relay-server on connect/disconnect), plus the k8s runner's 60s telemetry
//     post. It is not a liveness signal on its own: a session that stays up for
//     hours never moves it, which is how this cron used to retire perfectly
//     healthy long-lived connections and fail every request routed through them
//     (#36114).
//   - connection_status.relayLastSeenAt — written every 10 minutes by the relay
//     for each live websocket session. This is the real liveness signal.
//
// Requiring both to be stale means the check is correct whether or not a given
// agent's relay is emitting heartbeats yet, so no deploy ordering is implied.
// Its remaining job is the case no in-band signal can cover: a relay pod killed
// before its disconnect write runs, which would otherwise leave rows CONNECTED
// forever.
func AgentCheckAndUpdateStatus(ctx *security.RequestContext) error {
	dbms, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		return err
	}
	rows, err := dbms.Db.Queryx(fmt.Sprintf(`select ca.account_name, ca.id::varchar as account_id, a.id::varchar as agent_id, a.last_connected_at as agent_last_connected_at, a.status  as agent_status
		from agent a
		join cloud_accounts ca on ca.id = cloud_account_id
		where a.status != 'NOT_CONNECTED'
		and a.last_connected_at < (now() - interval '%[1]d minutes')
		and coalesce((a.connection_status->>'relayLastSeenAt')::timestamptz, '-infinity'::timestamptz) < (now() - interval '%[1]d minutes')
		and (ca.cloud_provider not in ('AWS', 'Azure', 'GCP', 'CloudFoundry') or a.type = 'proxy')`, agentConnectThresholdMinutes))

	if err != nil {
		ctx.GetLogger().Error("Error in fetching agent status", "error", err)
		return err
	}

	defer func() {
		err := rows.Close()
		if err != nil {
			ctx.GetLogger().Error("Error closing rows", "error", err)
		}
	}()

	notConnectedAgents := make([]any, 0)

	for rows.Next() {
		agent := map[string]any{}
		err = rows.MapScan(agent)
		if err != nil {
			ctx.GetLogger().Error("Error in mapping agent status", "error", err)
			return err

		}
		ctx.GetLogger().Info(fmt.Sprintf("Agent not connected for last %d mins", agentConnectThresholdMinutes), "agent", slog.AnyValue(agent))
		notConnectedAgents = append(notConnectedAgents, agent["agent_id"].(string))
	}

	if len(notConnectedAgents) > 0 {
		_, err = dbms.Exec(`update agent set status = 'NOT_CONNECTED' where id::varchar in (?)`, notConnectedAgents)
		if err != nil {
			ctx.GetLogger().Error("Error in updating agent status", "error", err)
			return err
		}
		ctx.GetLogger().Info("Agent status updated to not_connected", "agents", slog.AnyValue(notConnectedAgents))
	}

	return nil

}

type AgentDetails struct {
	Id              string               `json:"id" mapstructure:"id" validate:"required"`
	CreatedAt       *time.Time           `json:"created_at" mapstructure:"created_at" validate:"required"`
	UpdatedAt       *time.Time           `json:"updated_at" mapstructure:"updated_at" validate:"required"`
	Type            string               `json:"type" mapstructure:"type" validate:"required"`
	Status          string               `json:"status" mapstructure:"status" validate:"required"`
	LastConnectedAt *time.Time           `json:"last_connected_at" mapstructure:"last_connected_at"`
	StatusMessage   *string              `json:"status_message" mapstructure:"status_message"`
	LastSyncedAt    *time.Time           `json:"last_synced_at" mapstructure:"last_synced_at"`
	Version         *string              `json:"version" mapstructure:"version"`
	K8sVersion      *string              `json:"k8s_version" mapstructure:"k8s_version"`
	Features        AgentDetailsFeatures `json:"features" mapstructure:"features"`
	K8sProvider     *string              `json:"k8s_provider" mapstructure:"k8s_provider"`
}

type AgentDetailsFeatures struct {
	AlertManagerConnection *bool   `json:"alertManagerConnection" mapstructure:"alertManagerConnection"`
	AlertmanagerUrl        *string `json:"alertmanagerUrl" mapstructure:"alertmanagerUrl"`
	// Per-feature failure reasons emitted by the agent's health checks alongside
	// the boolean *Connection flags. Populated only when the corresponding health
	// check failed (e.g. "signoz: 401 token is expired"); nil/empty when healthy.
	// Pass-through JSONB — surfaced next to the "Disconnected" status in the UI.
	PrometheusConnectionError   *string        `json:"prometheusConnectionError" mapstructure:"prometheusConnectionError"`
	AlertManagerConnectionError *string        `json:"alertManagerConnectionError" mapstructure:"alertManagerConnectionError"`
	LogsConnectionError         *string        `json:"logsConnectionError" mapstructure:"logsConnectionError"`
	TracesConnectionError       *string        `json:"tracesConnectionError" mapstructure:"tracesConnectionError"`
	NodeAgentConnectionError    *string        `json:"nodeAgentConnectionError" mapstructure:"nodeAgentConnectionError"`
	OpencostConnectionError     *string        `json:"opencostConnectionError" mapstructure:"opencostConnectionError"`
	GrafanaEnabled              *bool          `json:"grafanaEnabled" mapstructure:"grafanaEnabled"`
	InstallationNamespace       *string        `json:"installationNamespace" mapstructure:"installationNamespace"`
	AutoscalerEnabled           *bool          `json:"autoScalerEnabled" mapstructure:"autoScalerEnabled"`
	AutoscalerNamespace         *string        `json:"autoScalerNamespace" mapstructure:"autoScalerNamespace"`
	AutoscalerVersion           *string        `json:"autoScalerVersion" mapstructure:"autoScalerVersion"`
	AutoscalerType              *string        `json:"autoScalerType" mapstructure:"autoScalerType"`
	LogProviderUrl              *string        `json:"logProviderUrl" mapstructure:"logProviderUrl"`
	LogProviderConfig           map[string]any `json:"logProviderConfig" mapstructure:"logProviderConfig"`
	LogsConnection              *bool          `json:"logsConnection" mapstructure:"logsConnection"`
	LogsConnectionProvider      *string        `json:"logsConnectionProvider" mapstructure:"logsConnectionProvider"`
	NodeAgentConnection         *bool          `json:"nodeAgentConnection" mapstructure:"nodeAgentConnection"`
	NodeAgentCount              *int           `json:"nodeAgentCount" mapstructure:"nodeAgentCount"`
	OpencostConnection          *bool          `json:"opencostConnection" mapstructure:"opencostConnection"`
	// OpencostServerSide is a server-managed marker stamped by the spend sync
	// (spend.setOpenCostServerSide). It is true when OpenCost is collected
	// server-side for this cluster (the migrated default, agent OpenCost off);
	// not reported by the agent itself.
	OpencostServerSide *bool `json:"opencostServerSide" mapstructure:"opencostServerSide"`
	// ProviderStatus is a server-managed record of which provider actually serves
	// each signal (logs / metrics / traces) and, for the ones served by a non-agent
	// integration, whether that integration passed its own validation probe. Written
	// by observability.RefreshProviderStatus on a cron, never by the agent. Kept as
	// pass-through JSONB: the Agent Details page reads it straight from
	// connection_status, and typing it here would duplicate that contract.
	ProviderStatus          map[string]any `json:"providerStatus" mapstructure:"providerStatus"`
	OpencostUrl             *string        `json:"opencostUrl" mapstructure:"opencostUrl"`
	PrometheusConnection    *bool          `json:"prometheusConnection" mapstructure:"prometheusConnection"`
	PrometheusRetentionTime *string        `json:"prometheusRetentionTime" mapstructure:"prometheusRetentionTime"`
	PrometheusUrl           *string        `json:"prometheusUrl" mapstructure:"prometheusUrl"`
	RelayConnection         *bool          `json:"relayConnection" mapstructure:"relayConnection"`
	TracesEnabled           *bool          `json:"tracesEnabled" mapstructure:"tracesEnabled"`
	TracesUrl               *string        `json:"tracesUrl" mapstructure:"tracesUrl"`
	TraceProvider           *string        `json:"traceProvider" mapstructure:"traceProvider"`
	TraceProviderConfig     map[string]any `json:"traceProviderConfig" mapstructure:"traceProviderConfig"`
	// ScheduleJobs surfaces the legacy Robusta wire shape stored under
	// connection_status -> 'schedule_jobs'. Repopulated by scan_orchestrator
	// after each server-side scan run; existing UI parsers already understand
	// the shape (job_id, runnable_params.action_func_name, scheduling_params,
	// state.{exec_count, job_status, last_exec_time_sec}).
	ScheduleJobs []map[string]any `json:"schedule_jobs,omitempty" mapstructure:"schedule_jobs"`
}

const agentCacheNamespace = "nb_agents"
const AgentTraceTableConfigKey = "otel_traces"

func init() {
	common.CacheCreateNamespace(agentCacheNamespace, common.CacheNamespaceWithExpiration(time.Minute*agentConnectThresholdMinutes))
}

var inFlightUpdates sync.Map // key: accountId, value: struct{}

// InvalidateAgentCache drops the cached AgentDetails for one account, so a writer
// that changed the agent row (e.g. observability's provider-status stamp) is not
// shadowed by this package's 15-minute cache until it expires.
func InvalidateAgentCache(accountId string) error {
	return common.CacheDelete(agentCacheNamespace, accountId)
}

func GetAgentConnectionDetails(accountId string) (AgentDetails, error) {
	if accountId == "" {
		return AgentDetails{}, fmt.Errorf("accountId is required")
	}

	if agentBytes, ok := common.CacheGet(agentCacheNamespace, accountId); ok {
		agent := AgentDetails{}
		if err := common.UnmarshalJson(agentBytes, &agent); err != nil {
			return AgentDetails{}, err
		}
		return agent, nil
	}

	databaseManager, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		return AgentDetails{}, err
	}

	agent := models.Agent{}
	if err := databaseManager.Db.Get(&agent, `SELECT id, created_at, updated_at, tenant, cloud_account_id, type, status,
			last_connected_at, access_key, access_secret, status_message, last_synced_at, version,
			k8s_version, connection_status, k8s_provider, access_secret_v2
		FROM agent WHERE cloud_account_id = $1 AND type != 'proxy'`, accountId); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return AgentDetails{}, fmt.Errorf("no agent connected for account %s", accountId)
		}
		return AgentDetails{}, err
	}

	if agent.ConnectionStatus == nil {
		return AgentDetails{}, fmt.Errorf("agent not connected")
	}

	var cloudProvider string
	if err := databaseManager.Db.Get(&cloudProvider, "SELECT cloud_provider FROM cloud_accounts WHERE id = $1", accountId); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return AgentDetails{}, fmt.Errorf("cloud account %s not found", accountId)
		}
		return AgentDetails{}, fmt.Errorf("failed to get cloud provider: %w", err)
	}

	normalizedType := strings.ToLower(cloudProvider)

	var canonicalType string
	switch normalizedType {
	case "aws":
		canonicalType = "AWS"
	case "azure":
		canonicalType = "Azure"
	case "gcp":
		canonicalType = "GCP"
	default:
		canonicalType = agent.Type
	}

	// Only update agent type if needed and not already in-flight
	if agent.Type != canonicalType {
		if _, loaded := inFlightUpdates.LoadOrStore(accountId, struct{}{}); !loaded {
			go func(accountId string, canonicalType string) {
				defer inFlightUpdates.Delete(accountId) // remove from map when done
				if _, updateErr := databaseManager.Db.Exec(
					"UPDATE agent SET type = $1, updated_at = NOW() WHERE cloud_account_id = $2 AND type != 'proxy'",
					canonicalType, accountId,
				); updateErr != nil {
					slog.Error("failed to update agent type", "accountId", accountId, "error", updateErr)
				} else {
					if cacheErr := common.CacheDelete(agentCacheNamespace, accountId); cacheErr != nil {
						slog.Error("failed to invalidate agent cache", "accountId", accountId, "error", cacheErr)
					}
				}
			}(accountId, canonicalType)
		}
	}

	agentFeatures := AgentDetailsFeatures{}
	if err := common.UnmarshalJson([]byte(*agent.ConnectionStatus), &agentFeatures); err != nil {
		return AgentDetails{}, err
	}

	details := AgentDetails{
		Id:              agent.Id,
		Type:            normalizedType,
		CreatedAt:       agent.CreatedAt,
		UpdatedAt:       agent.UpdatedAt,
		Status:          agent.Status,
		LastConnectedAt: agent.LastConnectedAt,
		StatusMessage:   agent.StatusMessage,
		LastSyncedAt:    agent.LastSyncedAt,
		Version:         agent.Version,
		K8sVersion:      agent.K8sVersion,
		K8sProvider:     agent.K8sProvider,
		Features:        agentFeatures,
	}

	if details.Features.TracesEnabled != nil && *details.Features.TracesEnabled && details.Features.TracesUrl != nil {
		traceUrl := *details.Features.TracesUrl

		if details.Features.TraceProvider == nil || *details.Features.TraceProvider == "" {
			traceProvider := "otel_clickhouse"
			details.Features.TraceProvider = &traceProvider
		}
		if details.Features.TraceProviderConfig == nil {
			details.Features.TraceProviderConfig = make(map[string]any)
		}
		if *details.Features.TraceProvider == "bigquery" {
			details.Features.TraceProviderConfig[AgentTraceTableConfigKey] = fmt.Sprintf("`%s`", traceUrl)
		} else if strings.Contains(traceUrl, "last9.io") {
			details.Features.TraceProviderConfig[AgentTraceTableConfigKey] = "otel.traces"
		} else {
			details.Features.TraceProviderConfig[AgentTraceTableConfigKey] = "otel_traces"
		}
	}

	detailBytes, err := common.MarshalJson(details)
	if err != nil {
		slog.Error("unable to marshal data", "error", err)
		return details, nil
	}

	if err := common.CacheSet(agentCacheNamespace, accountId, detailBytes, common.CacheSetWithExpiration(15*time.Minute)); err != nil {
		slog.Error("unable to cache data", "error", err)
	}

	return details, nil
}
