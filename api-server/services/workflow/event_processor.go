package workflow

import (
	"fmt"
	"nudgebee/services/common"
	"nudgebee/services/config"
	"nudgebee/services/security"
	"strings"
	"time"
)

func ProcessEvent(ctx *security.RequestContext, event map[string]any) (err error) {

	if event["tenant"] == nil {
		err = fmt.Errorf("workflow: tenant is required")
		ctx.GetLogger().Error(err.Error())
		return err
	}

	// Triage classified this event as suppressed — running workflows on it
	// would resurface noise the user explicitly silenced. PostProcessEvent
	// runs triage before this processor and propagates nb_status into the
	// event map, so this gate fires reliably even for first-firing events
	// suppressed in the same batch.
	if nbStatus, _ := event["nb_status"].(string); strings.EqualFold(nbStatus, "SUPPRESSED") {
		return nil
	}

	accountId, accountIdOk := event["cloud_account_id"].(string)
	if !accountIdOk {
		err = fmt.Errorf("workflow: cloud_account_id is missing or not a string")
		ctx.GetLogger().Error(err.Error())
		return err
	}

	id, idOk := event["id"].(string)
	if !idOk {
		err = fmt.Errorf("workflow: event id is missing or not a string")
		ctx.GetLogger().Error(err.Error())
		return err
	}

	ctx.GetLogger().Info("workflow: publishing new event to llm-server for processing", "id", id, "account_id", accountId)

	// Publish event metadata without evidences to reduce MQ message size.
	// Evidences can be multi-MB (up to 13+ MB for k8s events) and are not
	// needed for workflow matching. The runbook-server can fetch the full
	// event from DB via the event ID if a workflow needs evidence data.
	lightweight := make(map[string]any, len(event))
	for k, v := range event {
		if k == "evidences" {
			continue
		}
		lightweight[k] = v
	}

	err = common.MqPublish(config.Config.RabbitMqRunbookEventExchange, config.Config.RabbitMqRunbookEventRoutingKey, lightweight, common.MqPublishWithExpiration(2*time.Hour))
	if err != nil {
		ctx.GetLogger().Error("workflow: error publishing message to queue", "error", err)
		return err
	}

	return nil
}
