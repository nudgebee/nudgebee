package slo

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"nudgebee/services/common"
	"nudgebee/services/forecast"
	"nudgebee/services/internal/database"
	"nudgebee/services/security"
)

// CPU SLOs (cpu_throttling, cpu_saturation) are threshold SLOs that Nudgebee
// evaluates itself through the metrics API, instead of handing a PromQL SLI to the
// agent's slo_generator. Everything that differs for them lives in this file; the
// rest of the package branches once on isForecastConfig and delegates here.
// See docs/cpu-throttling-forecast-spec.md.

// forecastMethod is slo_config.method for a CPU SLO.
const forecastMethod = "forecast"

func isForecastConfig(c DBSLOConfig) bool { return c.Method == forecastMethod }

func isForecastType(name string) bool { return forecast.IsSLOCondition(strings.ToLower(name)) }

// isContainerScoped reports whether the forecast SLO type watches one container.
func isContainerScoped(name string) bool {
	p, ok := forecast.PresetFor(forecast.Condition(strings.ToLower(name)))
	return ok && p.ContainerScoped
}

// Seams for tests.
var (
	evaluateForecast  = forecast.EvaluateWithSeries
	syncForecast      = forecast.Sync
	closeForecastAll  = forecast.CloseAll
	storeLastForecast = writeLastForecast
	workloadKindOf    = workloadKind
	accountNameOf     = readAccountName
)

// executeForecastConfig is one hourly evaluation of a CPU SLO.
func executeForecastConfig(config DBSLOConfig, accountId string, dbms *database.DatabaseManager) error {
	ctx := security.NewRequestContextForTenantAdmin(config.TenantId, slog.Default(), nil, nil)
	start := time.Now()
	res, series := evaluateForecast(ctx, forecast.Request{
		AccountId:    accountId,
		Namespace:    config.Namespace,
		Name:         config.WorkloadName,
		Kind:         workloadKindOf(config.TenantId, accountId, config.WorkloadName, config.Namespace),
		Container:    common.StrVal(config.Container),
		Condition:    forecast.Condition(strings.ToLower(config.Name)),
		ThresholdPct: config.ThresholdBucket,
	})
	slog.Info("slo: forecast evaluated", "config_id", config.Id, "account_id", accountId, "condition", config.Name,
		"workload", config.WorkloadName, "namespace", config.Namespace, "container", common.StrVal(config.Container), "status", res.Status,
		"duration_ms", time.Since(start).Milliseconds())

	if err := storeLastForecast(dbms, config.Id, res); err != nil {
		slog.Error("slo: failed to store last forecast", "error", err, "config_id", config.Id)
	}
	name, err := accountNameOf(dbms, accountId)
	if err != nil {
		return err
	}
	return syncForecast(forecast.Target{
		TenantId: config.TenantId, AccountId: accountId, AccountName: name, SLOConfigId: config.Id,
	}, res, series)
}

// validateForecastConfig refuses a CPU SLO that could never be evaluated, before it
// is saved: the threshold is not a percentage, the workload has no CPU limit, or the
// account's metrics provider is not supported (nb_forecast_providers="" turns every
// provider off, the server-level kill switch).
func validateForecastConfig(ctx *security.RequestContext, req SLORequest, c SLOConfigRequest) error {
	// Switching a CPU SLO off is always allowed and needs no forecast, whatever its
	// threshold or the workload's limit now: it only closes the SLO's open alerts.
	if !c.Enabled {
		return nil
	}
	if c.Threshold <= 0 || c.Threshold > 100 {
		return fmt.Errorf("%s threshold must be a percentage above 0 and at most 100, got %v", c.Name, c.Threshold)
	}
	if isContainerScoped(c.Name) && strings.TrimSpace(c.Container) == "" {
		return fmt.Errorf("a %s SLO needs a container", c.Name)
	}
	kind := workloadKindOf(ctx.GetSecurityContext().GetTenantId(), req.AccountId, req.WorkloadName, req.Namespace)
	res, _ := evaluateForecast(ctx, forecast.Request{
		AccountId: req.AccountId, Namespace: req.Namespace, Name: req.WorkloadName, Kind: kind,
		Container: strings.TrimSpace(c.Container),
		Condition: forecast.Condition(strings.ToLower(c.Name)), ThresholdPct: c.Threshold,
	})
	switch res.Status {
	case forecast.StatusNoLimit, forecast.StatusProviderUnsupported:
		return fmt.Errorf("cannot add a %s SLO to %s: %s", c.Name, req.WorkloadName, res.Reason)
	}
	return nil
}

// closeForecastEvents ends the forecast events of a CPU SLO that will not be
// evaluated again (disabled or deleted). closeSLOEventByFingerprint is not enough:
// a forecast has one fingerprint per urgency level, and its close must also set
// nb_status and the close reason the re-fire cooldown reads.
func closeForecastEvents(tenantId, accountId, name, workload, namespace, container string) {
	kind := workloadKindOf(tenantId, accountId, workload, namespace)
	err := closeForecastAll(forecast.Target{TenantId: tenantId, AccountId: accountId}, namespace, kind, workload, container, forecast.Condition(strings.ToLower(name)))
	if err != nil {
		slog.Error("slo: failed to close forecast events", "error", err, "workload", workload, "namespace", namespace, "name", name, "container", container)
	}
}

// storedContainer is the container a forecast SLO watched before this save, or "".
func storedContainer(dbms *database.DatabaseManager, tenantId string, req SLORequest, name string) string {
	var c *string
	err := dbms.Db.Get(&c, `SELECT container FROM slo_config
		WHERE tenant_id=$1 AND cloud_account_id=$2 AND workload_name=$3 AND workload_namespace=$4 AND "name"=$5`,
		tenantId, req.AccountId, req.WorkloadName, req.Namespace, name)
	if err != nil {
		return ""
	}
	return common.StrVal(c)
}

// workloadKind is resolveSubjectType for callers that hold no config row.
func workloadKind(tenantId, accountId, workload, namespace string) string {
	dbms, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		return "deployment"
	}
	return resolveSubjectType(dbms, DBSLOConfig{TenantId: tenantId, CloudAccountId: accountId, WorkloadName: workload, Namespace: namespace})
}

func writeLastForecast(dbms *database.DatabaseManager, configId string, res forecast.Result) error {
	raw, err := common.MarshalJson(res)
	if err != nil {
		return err
	}
	_, err = dbms.Db.Exec(`UPDATE slo_config SET last_forecast = $1::jsonb WHERE id = $2`, string(raw), configId)
	return err
}

// lastForecastJSON passes a stored forecast through to the list response as-is.
func lastForecastJSON(raw *string) json.RawMessage {
	if raw == nil || *raw == "" {
		return nil
	}
	return json.RawMessage(*raw)
}

func readAccountName(dbms *database.DatabaseManager, accountId string) (string, error) {
	var name string
	err := dbms.Db.Get(&name, `SELECT account_name FROM cloud_accounts WHERE id = $1`, accountId)
	return name, err
}
