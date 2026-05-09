package integrations

import (
	"errors"
	"fmt"
	"io"
	"log/slog"
	"nudgebee/services/common"
	"nudgebee/services/config"
	"nudgebee/services/integrations/core"
	"nudgebee/services/security"
	"time"

	"github.com/google/uuid"
)

func init() {
	core.RegisterIntegration(WorkflowWebhook{})
}

type WorkflowWebhook struct {
}

const IntegrationGenericWebhook = "workflow_webhook"

func (m WorkflowWebhook) Name() string {
	return IntegrationGenericWebhook
}

func (m WorkflowWebhook) Category() core.IntegrationCategory {
	return core.IntegrationCategoryIncidentWebhook
}

func (m WorkflowWebhook) ConfigSchema() core.IntegrationSchema {
	return core.IntegrationSchema{
		Type:     core.ToolSchemaTypeObject,
		Required: []string{},
		Properties: map[string]core.IntegrationSchemaProperty{
			"integration_config_name": {
				Type:             core.ToolSchemaTypeString,
				Description:      "Name of Workflow Webhook",
				Default:          "",
				AutoGenerateFunc: "",
			},
			"account_id": {
				Type:             core.ToolSchemaTypeArray,
				Description:      "Select Account",
				Default:          "",
				AutoGenerateFunc: "listAccounts",
				// workflow_webhook is bound 1:1 to a workflow, which itself
				// belongs to a single account — so the account picker is
				// single-select rather than multi.
				SingleSelect: true,
			},
			"token": {
				Type:             core.ToolSchemaTypeString,
				Default:          "",
				AutoGenerateFunc: "",
			},
			// workflow_id binds the webhook to a specific automation. Set
			// programmatically by runbook-server CreateWorkflowWebhookTrigger
			// when an automation attaches an existing webhook integration.
			// Hidden from the integration form. No Default — leaving it nil
			// keeps applySchemaDefaults from injecting an empty value that
			// would clobber the binding when the user edits the integration
			// from the Integrations tab.
			"workflow_id": {
				Type:        core.ToolSchemaTypeString,
				Description: "ID of the automation this webhook is bound to",
				Hidden:      true,
			},
		},
	}
}

func (m WorkflowWebhook) ValidateConfig(sc *security.SecurityContext, config []core.IntegrationConfigValue, accountId string) []error {
	return []error{}
}

func (m WorkflowWebhook) ProcessEventWebook(sc *security.RequestContext, settings []core.IntegrationConfigValue, accountId, webhookPayloadString string) ([]core.EventIncomingWebhook, error) {
	workflowId, _ := core.GetSettingValue(settings, "workflow_id")
	token, _ := core.GetSettingValue(settings, "token")

	if workflowId == "" {
		sc.GetLogger().Error("generic_webhook: unable to process workflow", "workflow", workflowId, "payload", webhookPayloadString)
		return nil, errors.New("unable to identify workflow")
	}

	// Filter is now evaluated at the trigger level by runbook-server
	// (see runbook-server/api/handlers.go webhook handler) so the same
	// integration can fan out to multiple workflows in the future, each
	// with its own filter.
	resp, err := common.HttpPost(config.Config.WorkflowServerEndpoint+"/webhook/"+workflowId,
		common.HttpWithStringBody(webhookPayloadString),
		common.HttpWithHeaders(map[string]string{
			"X-Webhook-Secret": token,
			"x-tenant-id":      sc.GetSecurityContext().GetTenantId(),
			"x-account-id":     accountId,
		}),
	)
	if err != nil {
		sc.GetLogger().Error("generic_webhook: unable to process workflow", "workflow", workflowId, "payload", webhookPayloadString, "error", err)
		return nil, errors.New("unable to process workflow - " + workflowId)
	}

	defer func() {
		if err := resp.Body.Close(); err != nil {
			slog.Error("unable to close body", "error", err)
		}
	}()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		sc.GetLogger().Error("generic_webhook: unable to read workflow trigger resp", "workflow", workflowId, "payload", webhookPayloadString, "error", err)
		return nil, err
	}

	triggerInfo := fmt.Sprintf(`{"status":%d, "body":"%s"}`, resp.StatusCode, string(body))

	webhookId := uuid.NewString()
	t := time.Now()
	return []core.EventIncomingWebhook{{
		WebhookId:             webhookId,
		EventType:             IntegrationGenericWebhook,
		EventUrl:              "",
		EventId:               webhookId,
		EventStatus:           "resolved",
		EventPriority:         "low",
		EventTitle:            "Webhook Event for workflow - " + workflowId,
		EventDescription:      triggerInfo,
		EventTags:             nil,
		EventSubjectName:      workflowId,
		EventSubjectNamespace: "Workflow",
		EventCreatedAt:        t,
		EventEndsAt:           t,
	}}, nil
}

func (m WorkflowWebhook) MergeEventWebhooks(sc *security.RequestContext, previous core.EventIncomingWebhook, new core.EventIncomingWebhook) (core.EventIncomingWebhook, error) {
	return new, nil
}
