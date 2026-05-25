package notification

import (
	"fmt"
	"log/slog"
	"strings"

	"nudgebee/services/common"
	"nudgebee/services/config"
	"nudgebee/services/internal/database"
	"nudgebee/services/internal/database/models"
	"nudgebee/services/security"
)

func ProcessEvent(ctx *security.RequestContext, newEvent map[string]any) (err error) {

	if newEvent["priority"] == nil || newEvent["priority"] != "HIGH" {
		return nil
	}

	if priortiyStr, ok := newEvent["priority"].(string); ok && priortiyStr != "HIGH" {
		return nil
	} else if priortiyStr, ok := newEvent["priority"].(string); ok && priortiyStr != "HIGH" {
		return nil
	}

	// Triage classified this event as suppressed — the user explicitly told
	// us not to surface it, so don't page them. PostProcessEvent runs triage
	// first and propagates nb_status into newEvent, so this gate is reliable
	// even for first-firing events suppressed in the same batch.
	if nbStatus, _ := newEvent["nb_status"].(string); strings.EqualFold(nbStatus, "SUPPRESSED") {
		return nil
	}

	// SLO events are also published directly from slo/service.go:triggerNotification
	// with type=slo_alert. Publishing a second finding notification here results in
	// duplicate delivery (to both the SLO-rule channel and the troubleshoot/default).
	source, _ := newEvent["source"].(string)
	if source == "slo" {
		ctx.GetLogger().Debug("skipping finding notification for SLO event — handled by slo service", "id", newEvent["id"])
		return nil
	}

	ctx.GetLogger().Info(fmt.Sprintf("Publishing new event to notification - %v", newEvent["id"]))
	message := map[string]any{
		"kind":      "notification",
		"type":      "finding",
		"source":    source,
		"tenant_id": newEvent["tenant"],
		"parameters": map[string]any{
			"finding": newEvent,
		},
	}

	err = common.MqPublish(config.Config.RabbitMqNotificationsExchange, config.Config.RabbitMqNotificationsQueue, message)
	if err != nil {
		ctx.GetLogger().Error("Error publishing message to queue", "error", err)
		return err
	}

	return nil
}

func TriggerNotificationForRecommendationResolution(ctx *security.RequestContext, recommendation models.Recommendation, resolution models.RecommendationResolution) (err error) {

	ctx.GetLogger().Info(fmt.Sprintf("Publishing recommendation resolution to notification - %s", recommendation.Id))

	//get account name from db
	dbms, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		ctx.GetLogger().Error("error getting database manager", "error", err)
		return err
	}
	rows, err := dbms.Db.Queryx(`select ca.account_name from cloud_accounts ca where id=$1`, recommendation.CloudAccountId)

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

	accountName := ""
	for rows.Next() {
		err = rows.Scan(&accountName)
		if err != nil {
			ctx.GetLogger().Error("Error getting account name", "error", err)
			return err
		}
	}

	resourceName := ""
	if recommendation.AccountObjectId != nil {
		resourceName = *recommendation.AccountObjectId
	}
	if resourceName == "" {
		resourceName = recommendation.Id
	}

	finopsScore := 0
	if recommendation.FinOpsScore != nil {
		finopsScore = *recommendation.FinOpsScore
	}
	finopsBand := "Low"
	if recommendation.FinOpsBand != nil {
		finopsBand = *recommendation.FinOpsBand
	}

	severity := "Medium"
	if recommendation.Severity != nil {
		severity = *recommendation.Severity
	}

	statusMessage := ""
	if resolution.StatusMessage != nil {
		statusMessage = *resolution.StatusMessage
	}

	message := map[string]any{
		"kind":      "notification",
		"source":    "optimize",
		"type":      "recommendation_resolution",
		"tenant_id": recommendation.TenantId,
		"parameters": map[string]any{
			"organization_id":   recommendation.TenantId,
			"recommendation_id": recommendation.Id,
			"resource_name":     resourceName,
			"rule_name":         recommendation.RuleName,
			"category":          recommendation.Category,
			"account_id":        recommendation.CloudAccountId,
			"account_name":      accountName,
			"finops_score":      finopsScore,
			"finops_band":       finopsBand,
			"estimated_savings": recommendation.EstimatedSavings,
			"severity":          severity,
			"status":            string(recommendation.Status),
			"resolution": map[string]any{
				"resolver":       string(resolution.ResolverType),
				"type":           string(resolution.Type),
				"type_reference": resolution.TypeReferenceId,
				"status":         string(resolution.Status),
				"status_message": statusMessage,
			},
			"base_url": config.Config.BaseUrl,
		},
	}
	ctx.GetLogger().Info(fmt.Sprintf("Publishing recommendation resolution to notification - %s", recommendation.Id), "message", slog.AnyValue(message))
	err = common.MqPublish(config.Config.RabbitMqNotificationsExchange, config.Config.RabbitMqNotificationsQueue, message)
	if err != nil {
		ctx.GetLogger().Error("Error publishing message to queue", "error", err)
		return err
	}

	return nil
}

func TriggerNotificationForEventResolution(ctx *security.RequestContext, event models.Event, resolution models.EventResolution) (err error) {

	ctx.GetLogger().Info(fmt.Sprintf("Publishing recommendation resolution to notification - %s", event.Id))

	//get account name from db
	dbms, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		ctx.GetLogger().Error("error getting database manager", "error", err)
		return err
	}
	rows, err := dbms.Db.Queryx(`select ca.account_name from cloud_accounts ca where id=$1`, *event.CloudAccountId)

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

	accountName := ""
	for rows.Next() {
		err = rows.Scan(&accountName)
		if err != nil {
			ctx.GetLogger().Error("Error getting account name", "error", err)
			return err
		}
	}

	// summary defined by status and type
	message := map[string]any{
		"kind":      "notification",
		"source":    "optimize",
		"type":      "finding",
		"tenant_id": event.Tenant,
		"parameters": map[string]any{
			"finding": event,
		},
	}
	ctx.GetLogger().Info(fmt.Sprintf("Publishing event resolution to notification - %s", event.Id), "message", slog.AnyValue(message))
	err = common.MqPublish(config.Config.RabbitMqNotificationsExchange, config.Config.RabbitMqNotificationsQueue, message)
	if err != nil {
		ctx.GetLogger().Error("Error publishing message to queue", "error", err)
		return err
	}

	return nil
}
