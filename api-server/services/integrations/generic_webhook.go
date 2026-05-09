package integrations

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"nudgebee/services/common"
	"nudgebee/services/config"
	"nudgebee/services/integrations/core"
	"nudgebee/services/security"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/noirbizarre/gonja"
	"github.com/noirbizarre/gonja/exec"
)

// filterTemplateCache caches compiled gonja templates for workflow_webhook
// filter_expression values. Keyed by raw expression string; templates are
// safe for concurrent Execute calls.
var filterTemplateCache sync.Map

func compileFilterTemplate(expr string) (*exec.Template, error) {
	if cached, ok := filterTemplateCache.Load(expr); ok {
		return cached.(*exec.Template), nil
	}
	tpl, err := gonja.FromString(expr)
	if err != nil {
		return nil, err
	}
	actual, _ := filterTemplateCache.LoadOrStore(expr, tpl)
	return actual.(*exec.Template), nil
}

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
			},
			"token": {
				Type:             core.ToolSchemaTypeString,
				Default:          "",
				AutoGenerateFunc: "",
			},
			"filter_expression": {
				Type:        core.ToolSchemaTypeString,
				Description: "Optional Jinja2 expression evaluated against webhook_payload. Workflow runs only when result is \"true\".",
				Default:     "",
			},
		},
	}
}

func (m WorkflowWebhook) ValidateConfig(sc *security.SecurityContext, config []core.IntegrationConfigValue, accountId string) []error {
	for _, c := range config {
		if c.Name == "filter_expression" && c.Value != "" {
			if _, err := compileFilterTemplate(c.Value); err != nil {
				return []error{fmt.Errorf("invalid filter_expression: %w", err)}
			}
		}
	}
	return []error{}
}

func (m WorkflowWebhook) ProcessEventWebook(sc *security.RequestContext, settings []core.IntegrationConfigValue, accountId, webhookPayloadString string) ([]core.EventIncomingWebhook, error) {
	workflowId, _ := core.GetSettingValue(settings, "workflow_id")
	token, _ := core.GetSettingValue(settings, "token")
	filterExpr, _ := core.GetSettingValue(settings, "filter_expression")

	if workflowId == "" {
		sc.GetLogger().Error("generic_webhook: unable to process workflow", "workflow", workflowId, "payload", webhookPayloadString)
		return nil, errors.New("unable to identify workflow")
	}

	if filterExpr != "" {
		var payloadData any
		if err := json.Unmarshal([]byte(webhookPayloadString), &payloadData); err != nil {
			payloadData = webhookPayloadString
		}
		tpl, err := compileFilterTemplate(filterExpr)
		if err != nil {
			sc.GetLogger().Error("generic_webhook: invalid filter_expression", "filter", filterExpr, "error", err)
			return nil, fmt.Errorf("invalid filter_expression: %w", err)
		}
		rendered, err := tpl.Execute(gonja.Context{"webhook_payload": payloadData})
		if err != nil {
			sc.GetLogger().Error("generic_webhook: failed to render filter_expression", "filter", filterExpr, "error", err)
			return nil, fmt.Errorf("failed to render filter_expression: %w", err)
		}
		if strings.ToLower(strings.TrimSpace(rendered)) != "true" {
			sc.GetLogger().Info("generic_webhook: payload filtered out", "workflow", workflowId, "filter", filterExpr)
			return []core.EventIncomingWebhook{}, nil
		}
	}

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
