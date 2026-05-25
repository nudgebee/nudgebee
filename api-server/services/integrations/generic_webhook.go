package integrations

import (
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/url"
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
				Description:      "Select Account(s)",
				Default:          "",
				AutoGenerateFunc: "listAccounts",
				// Multi-select: a workflow_webhook can fan out to workflows
				// across multiple accounts. Each subscribing workflow declares
				// its own webhook trigger pointing at this integration; the
				// fan-out endpoint enumerates them tenant-wide.
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
			//
			// Note: workflow_id is no longer the routing key under fan-out —
			// subscribers are resolved by querying every workflow whose
			// webhook trigger lists this integration's name. The column is
			// kept so the existing CreateWorkflowWebhookTrigger upsert path
			// stays a no-op DB write and so legacy rows continue to parse.
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
	token, _ := core.GetSettingValue(settings, "token")
	integrationName, _ := core.GetSettingValue(settings, core.SyntheticIntegrationNameKey)

	// Routing is now tenant-wide fan-out: runbook-server enumerates every active
	// workflow whose webhook trigger subscribes to this integration's name,
	// evaluates each per-trigger filter, and executes matching workflows in
	// their own accounts. workflow_id (1:1 fallback) and workflow_mapping rules
	// on the integration are no longer used for routing — each workflow's
	// webhook trigger is now the single source of truth, with its own filter
	// expression. Legacy workflow_mapping config rows continue to validate via
	// ValidateConfig so existing data is not lost, but they're ignored here.
	if integrationName == "" {
		sc.GetLogger().Error("generic_webhook: integration name not resolved from settings — fan-out cannot proceed")
		return nil, errors.New("unable to resolve integration name for fan-out")
	}

	resp, err := common.HttpPost(config.Config.WorkflowServerEndpoint+"/webhook/by-integration/"+url.PathEscape(integrationName),
		common.HttpWithStringBody(webhookPayloadString),
		common.HttpWithHeaders(map[string]string{
			"X-Webhook-Secret": token,
			"x-tenant-id":      sc.GetSecurityContext().GetTenantId(),
			"x-account-id":     accountId,
		}),
	)
	if err != nil {
		sc.GetLogger().Error("generic_webhook: failed to fan out webhook", "integration", integrationName, "payload", webhookPayloadString, "error", err)
		return nil, errors.New("unable to fan out webhook for integration - " + integrationName)
	}

	defer func() {
		if err := resp.Body.Close(); err != nil {
			slog.Error("unable to close body", "error", err)
		}
	}()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		sc.GetLogger().Error("generic_webhook: unable to read fan-out resp", "integration", integrationName, "payload", webhookPayloadString, "error", err)
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
		EventTitle:            "Webhook fan-out for integration - " + integrationName,
		EventDescription:      triggerInfo,
		EventTags:             nil,
		EventSubjectName:      integrationName,
		EventSubjectNamespace: "Workflow",
		EventCreatedAt:        t,
		EventEndsAt:           t,
	}}, nil
}

func (m WorkflowWebhook) MergeEventWebhooks(sc *security.RequestContext, previous core.EventIncomingWebhook, new core.EventIncomingWebhook) (core.EventIncomingWebhook, error) {
	return new, nil
}
