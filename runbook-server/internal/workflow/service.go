package workflow

import (
	"context"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"nudgebee/runbook/common"
	"nudgebee/runbook/config"
	"nudgebee/runbook/internal/model" // Updated import
	"nudgebee/runbook/internal/tasks"
	aiTasks "nudgebee/runbook/internal/tasks/ai"
	"nudgebee/runbook/internal/tasks/testutils"
	"nudgebee/runbook/internal/tasks/types"
	"nudgebee/runbook/services/audit"
	"nudgebee/runbook/services/cloud"
	configSvc "nudgebee/runbook/services/config"
	"nudgebee/runbook/services/integrations"
	"nudgebee/runbook/services/security"
	"nudgebee/runbook/services/ticket"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	jsonata "github.com/xiatechs/jsonata-go"
	"go.temporal.io/api/enums/v1"
	"go.temporal.io/api/serviceerror"
	"go.temporal.io/api/workflowservice/v1"
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/converter"
	"go.temporal.io/sdk/temporal"
	"golang.org/x/text/cases"
	"golang.org/x/text/language"
	"google.golang.org/protobuf/types/known/timestamppb"
)

type WorkflowService interface {
	CreateWorkflow(ctx *security.RequestContext, accountId string, wf model.Workflow) (string, string, error)
	ExecuteWorkflow(ctx *security.RequestContext, accountId, id string, triggerType model.WorkflowTrigger, inputs map[string]any) (string, error)
	TriggerWorkflowFromDraft(ctx *security.RequestContext, accountId, id string, inputs map[string]any) (string, error)
	RetriggerWorkflowExecution(ctx *security.RequestContext, accountId, workflowId, executionId string, inputs map[string]any) (string, error)
	ListWorkflows(ctx *security.RequestContext, accountId string, request model.ListWorkflowRequest) (model.ListWorkflowResponse, error)
	GetWorkflow(ctx *security.RequestContext, accountId, id string) (*model.Workflow, error)
	UpdateWorkflow(ctx *security.RequestContext, accountId, id string, workflow model.Workflow) (model.Workflow, error)
	DeleteWorkflow(ctx *security.RequestContext, accountId, id string) error
	ListWorkflowCallers(ctx *security.RequestContext, accountId, id string) (model.ListWorkflowCallersResponse, error)
	UpdateWorkflowStatus(ctx *security.RequestContext, accountId, id string, status model.WorkflowStatus) error
	ListWorkflowExecutions(ctx *security.RequestContext, accountId, workflowId string, request model.ListWorkflowExecutionRequest) (model.ListWorkflowExecutionResponse, error)
	ListWorkflowExecutionsForEvent(ctx *security.RequestContext, accountId, eventId string) (model.ListWorkflowExecutionResponse, error)
	GetDetailedWorkflowExecution(ctx *security.RequestContext, accountId, workflowId, executionId string) (*model.WorkflowExecutionDetails, error)
	UpdateWorkflowExecution(ctx *security.RequestContext, accountId, workflowId, executionId string, inputs map[string]any) error
	CompleteApprovalTask(ctx *security.RequestContext, token, status string, result any) error
	CompleteApprovalTaskFromUI(ctx *security.RequestContext, accountID, workflowID, executionID, taskID, status, comments string) error
	CancelWorkflowExecution(ctx *security.RequestContext, accountId, workflowId, executionId string) error
	PauseWorkflow(ctx *security.RequestContext, accountId, id string) error
	ResumeWorkflow(ctx *security.RequestContext, accountId, id string) error
	ListAllTasks(ctx *security.RequestContext) model.ListTaskDefinitionResponse
	ExecuteTask(ctx *security.RequestContext, accountId, taskType string, params map[string]any) (any, error)
	ListMCPTools(ctx *security.RequestContext, accountId string, params map[string]any) (any, error)
	ValidateWorkflow(ctx *security.RequestContext, accountId string, wf model.Workflow) error
	GetWorkflowState(ctx *security.RequestContext, accountId, id string) ([]model.WorkflowStateItem, error)
	DryRunWorkflow(ctx *security.RequestContext, accountId string, request model.DryRunWorkflowRequest) (model.DryRunWorkflowResponse, error)
	DryRunWorkflowAsync(ctx *security.RequestContext, accountId string, request model.DryRunWorkflowRequest) (string, string, error)
	CountWorkflows(ctx *security.RequestContext, req model.WorkflowCountRequest) (model.WorkflowCountResponse, error)
	CountWorkflowExecutions(ctx *security.RequestContext, req model.WorkflowExecutionCountRequest) (model.WorkflowExecutionCountResponse, error)
	// Template operations (type=system uses global store, type=user reserved for future)
	ListTemplates(ctx *security.RequestContext, request model.ListWorkflowTemplateRequest) (model.ListWorkflowTemplateResponse, error)
	GetTemplate(ctx *security.RequestContext, id string) (*model.WorkflowTemplate, error)
	// FanOutWebhookEvent dispatches a webhook payload to every active workflow whose
	// webhook trigger subscribes to the integration name. Each subscriber's trigger
	// filter is evaluated independently; only matching ones execute.
	FanOutWebhookEvent(ctx *security.RequestContext, integrationName string, payload []byte) (FanOutResult, error)
	// Version history operations.
	ListWorkflowVersions(ctx *security.RequestContext, accountId, id string) ([]model.WorkflowVersion, error)
	GetWorkflowVersion(ctx *security.RequestContext, accountId, id string, versionNumber int) (*model.WorkflowVersion, error)
	RestoreWorkflowVersion(ctx *security.RequestContext, accountId, id string, versionNumber int) (model.Workflow, error)
	PublishWorkflow(ctx *security.RequestContext, accountId, id string, name, description *string, setLive bool, status model.WorkflowStatus) (*model.WorkflowVersion, error)
	UpdateWorkflowVersionStatus(ctx *security.RequestContext, accountId, id string, versionNumber int, status model.WorkflowStatus) (*model.WorkflowVersion, error)
	SetLiveWorkflowVersion(ctx *security.RequestContext, accountId, id string, versionNumber int) (*model.Workflow, error)
	UpdateWorkflowVersionMetadata(ctx *security.RequestContext, accountId, id string, versionNumber int, name, description *string) (*model.WorkflowVersion, error)
}

// FanOutResult summarizes a webhook fan-out dispatch — one entry per subscriber
// workflow, with a status of "fired", "filtered" (filter returned non-true),
// "skipped" (auth/state mismatch), or "failed".
type FanOutResult struct {
	Subscribers []FanOutSubscriberResult `json:"subscribers"`
	Fired       int                      `json:"fired"`
	Filtered    int                      `json:"filtered"`
	Skipped     int                      `json:"skipped"`
	Failed      int                      `json:"failed"`
}

// FanOutSubscriberResult describes one workflow's outcome during fan-out.
type FanOutSubscriberResult struct {
	WorkflowID  string `json:"workflow_id"`
	AccountID   string `json:"account_id"`
	Status      string `json:"status"`
	ExecutionID string `json:"execution_id,omitempty"`
	Error       string `json:"error,omitempty"`
}

type Service struct {
	temporalClient   client.Client
	store            model.WorkflowStore // Use model.WorkflowStore
	templateStore    model.WorkflowTemplateStore
	dataConverter    converter.DataConverter
	taskRegistry     *tasks.TaskRegistry
	workflowExecutor *WorkflowExecutor
	configService    configSvc.ConfigService
}

func NewService(temporalClient client.Client, store model.WorkflowStore, dataConverter converter.DataConverter, taskRegistry *tasks.TaskRegistry, workflowExecutor *WorkflowExecutor, configService configSvc.ConfigService, templateStore ...model.WorkflowTemplateStore) *Service {
	s := &Service{
		temporalClient:   temporalClient,
		store:            store,
		dataConverter:    dataConverter,
		taskRegistry:     taskRegistry,
		workflowExecutor: workflowExecutor,
		configService:    configService,
	}
	if len(templateStore) > 0 {
		s.templateStore = templateStore[0]
	}
	return s
}

// validateTaskTypes checks if all task types in a workflow definition are valid and their parameters conform to the task's schema.
func (s *Service) validateTaskTypes(ctx *security.RequestContext, accountID string, wf model.Workflow) error {
	knownTaskTypes := make(map[string]types.Task)
	for _, task := range s.taskRegistry.ListTasks() {
		knownTaskTypes[task.GetName()] = task
	}

	tenantID := ctx.GetSecurityContext().GetTenantId()

	validateHooks := func(hooks *model.Hooks, ownerID string) error {
		if hooks == nil {
			return nil
		}
		lists := map[string][]model.Action{
			"success": hooks.Success,
			"failure": hooks.Failure,
			"always":  hooks.Always,
		}
		for hookType, actions := range lists {
			for i, action := range actions {
				if action.Type == "" {
					return common.ErrorBadRequest(fmt.Sprintf("hook '%s[%d]' for '%s' has an empty type", hookType, i, ownerID))
				}
				taskImpl, ok := knownTaskTypes[action.Type]
				if !ok {
					return common.ErrorBadRequest(fmt.Sprintf("hook '%s[%d]' for '%s' has an unknown type '%s'", hookType, i, ownerID, action.Type))
				}
				if taskImpl.InputSchema() != nil {
					if err := taskImpl.InputSchema().Validate(action.Params); err != nil {
						return common.ErrorBadRequest(fmt.Sprintf("hook '%s[%d]' for '%s' parameters validation failed: %v", hookType, i, ownerID, err))
					}
				}
			}
		}
		return nil
	}

	if err := validateHooks(wf.Definition.Hooks, "workflow"); err != nil {
		return err
	}

	var validateRecursive func(tasks []model.Task) error
	validateRecursive = func(tasks []model.Task) error {
		for _, task := range tasks {
			if task.Type == "" {
				return common.ErrorBadRequest(fmt.Sprintf("task '%s' has an empty type", task.ID))
			}

			taskImpl, ok := knownTaskTypes[task.Type]
			if !ok {
				return common.ErrorBadRequest(fmt.Sprintf("task '%s' has an unknown type '%s'", task.ID, task.Type))
			}

			if err := validateHooks(task.Hooks, task.ID); err != nil {
				return err
			}

			// Validate task parameters against its schema
			if taskImpl.InputSchema() != nil {
				if err := taskImpl.InputSchema().Validate(task.Params); err != nil {
					return common.ErrorBadRequest(fmt.Sprintf("task '%s' parameters validation failed: %v", task.ID, err))
				}

				// Specific validation for core.call-workflow
				if task.Type == "core.call-workflow" {
					workflowName, nameOk := task.Params["workflow_name"].(string)
					if !nameOk || workflowName == "" {
						return common.ErrorBadRequest(fmt.Sprintf("core.call-workflow task '%s' missing or invalid 'workflow_name' parameter", task.ID))
					}
					// Skip DB validation for template expressions — they resolve at execution time
					if !strings.Contains(workflowName, "{{") {
						// Validate that the referenced workflow exists
						_, err := s.store.FindByName(context.Background(), tenantID, accountID, workflowName)
						if err != nil {
							if errors.Is(err, sql.ErrNoRows) {
								return common.ErrorBadRequest(fmt.Sprintf("core.call-workflow task '%s' references non-existent workflow '%s'", task.ID, workflowName))
							}
							return fmt.Errorf("failed to validate workflow reference for task '%s': %w", task.ID, err)
						}
					}
				}

				// Specific validation for data.transform / data.filter: compile JSONata expressions
				if task.Type == "data.transform" || task.Type == "data.filter" {
					if expression, ok := task.Params["expression"].(string); ok && expression != "" {
						// Skip template expressions — they resolve at execution time
						if !strings.Contains(expression, "{{") {
							if _, err := jsonata.Compile(expression); err != nil {
								return common.ErrorBadRequest(fmt.Sprintf("task '%s' has invalid JSONata expression: %v", task.ID, err))
							}
						}
					}
				}

				// Extended validation for specific types
				for paramName, prop := range taskImpl.InputSchema().Properties {
					val, exists := task.Params[paramName]
					if !exists || val == "" {
						continue
					}

					strVal, ok := val.(string)
					if !ok {
						continue
					}

					// Skip DB-level validation for template expressions — they resolve at execution time
					if strings.Contains(strVal, "{{") {
						continue
					}

					switch prop.Type {
					case types.PropertyTypeAccount:
						if err := cloud.ValidateAccount(ctx, strVal); err != nil {
							return common.ErrorBadRequest(fmt.Sprintf("task '%s' parameter '%s' validation failed: %v", task.ID, paramName, err))
						}
					case types.PropertyTypeTicket:
						if err := ticket.ValidateTicketConfig(ctx, strVal); err != nil {
							return common.ErrorBadRequest(fmt.Sprintf("task '%s' parameter '%s' validation failed: %v", task.ID, paramName, err))
						}
					case types.PropertyTypeIntegration:
						if prop.SubType != "" {
							resolvedID, err := integrations.ResolveIntegrationID(ctx, strVal, []string{prop.SubType})
							if err != nil {
								return common.ErrorBadRequest(fmt.Sprintf("task '%s' parameter '%s' validation failed: %v", task.ID, paramName, err))
							}
							if _, err := integrations.GetIntegration(ctx, accountID, prop.SubType, resolvedID); err != nil {
								return common.ErrorBadRequest(fmt.Sprintf("task '%s' parameter '%s' validation failed: %v", task.ID, paramName, err))
							}
						}
					}
				}
			}

			// Recursively validate child tasks for group and foreach task types

			var tasksToValidate []model.Task

			switch task.Type {
			case "core.group", "group":
				// core.group uses Tasks field or params.tasks
				if len(task.Tasks) > 0 {
					tasksToValidate = append(tasksToValidate, task.Tasks...)
				}
				if childTasks, ok := task.Params["tasks"].([]any); ok {
					for _, t := range childTasks {
						var modelTask model.Task
						taskBytes, err := json.Marshal(t)
						if err != nil {
							return fmt.Errorf("failed to marshal child task in group '%s': %w", task.ID, err)
						}
						if err := json.Unmarshal(taskBytes, &modelTask); err != nil {
							return fmt.Errorf("failed to unmarshal child task in group '%s': %w", task.ID, err)
						}
						tasksToValidate = append(tasksToValidate, modelTask)
					}
				}

			case "core.foreach":
				// core.foreach stores subtasks in params.tasks
				if childTasks, ok := task.Params["tasks"].([]any); ok {
					for _, t := range childTasks {
						var modelTask model.Task
						taskBytes, err := json.Marshal(t)
						if err != nil {
							return fmt.Errorf("failed to marshal subtask in foreach '%s': %w", task.ID, err)
						}
						if err := json.Unmarshal(taskBytes, &modelTask); err != nil {
							return fmt.Errorf("failed to unmarshal subtask in foreach '%s': %w", task.ID, err)
						}
						tasksToValidate = append(tasksToValidate, modelTask)
					}
				}
			}

			if len(tasksToValidate) > 0 {
				if err := validateRecursive(tasksToValidate); err != nil {
					return fmt.Errorf("in task '%s': %w", task.ID, err)
				}
			}
		}
		return nil
	}

	return validateRecursive(wf.Definition.Tasks)
}

// triggerInternalNameCanonical returns Internal.Name with any legacy
// "wf-<workflowID>-" shadow prefix removed. Used only for cross-trigger
// matching inside enforceWebhookSecrets, so a legacy row whose existing
// trigger still has the prefixed form ("wf-<id>-github_ci_webhook")
// matches an incoming request that carries the unprefixed picker name
// ("github_ci_webhook"). The actual Internal.Name on either side stays
// untouched so the cleanup paths in UpdateWorkflow / DeleteWorkflow can
// still read the prefix as the auto-managed marker.
func triggerInternalNameCanonical(name, workflowID string) string {
	if workflowID == "" {
		return name
	}
	prefix := "wf-" + workflowID + "-"
	if strings.HasPrefix(name, prefix) {
		return strings.TrimPrefix(name, prefix)
	}
	return name
}

func enforceWebhookSecrets(wf, existingWf *model.Workflow, workflowID string) error {
	for i, t := range wf.Definition.Triggers {
		if t.Type == model.WorkflowTriggerWebhook {
			userSecret, userProvided := t.Params["secret"].(string)
			if !userProvided && t.Params["secret"] != nil {
				return common.ErrorBadRequest("webhook secret must be a string")
			}

			if existingWf == nil {
				// Create case: No secrets allowed
				if userProvided {
					return common.ErrorBadRequest("webhook secret is system managed and cannot be provided")
				}
				continue
			}

			// Update case
			matchFound := false
			if t.Internal != nil && t.Internal.Name != "" {
				incomingCanonical := triggerInternalNameCanonical(t.Internal.Name, workflowID)
				for _, et := range existingWf.Definition.Triggers {
					if et.Type != model.WorkflowTriggerWebhook || et.Internal == nil {
						continue
					}
					if triggerInternalNameCanonical(et.Internal.Name, workflowID) != incomingCanonical {
						continue
					}
					matchFound = true
					if systemSecret, ok := et.Params["secret"].(string); ok {
						if userProvided {
							if userSecret != systemSecret {
								return common.ErrorBadRequest("webhook secret cannot be modified")
							}
						} else {
							if wf.Definition.Triggers[i].Params == nil {
								wf.Definition.Triggers[i].Params = make(map[string]any)
							}
							wf.Definition.Triggers[i].Params["secret"] = systemSecret
						}
					}
					break
				}
			}

			if !matchFound && userProvided {
				return common.ErrorBadRequest("webhook secret cannot be provided for new trigger")
			}
		}
	}
	return nil
}

// webhookTriggerMatchesIntegration reports whether a webhook trigger
// subscribes to the integration identified by integrationName. Matches on
// either trigger.params.integration_name (picker flow / new workflows where
// params and internal are the same string) or trigger.internal.name (legacy
// workflows where internal carries the `wf-<workflowID>-<name>` prefix while
// params is the bare name).
//
// Must stay aligned with the JSONB predicate in
// WorkflowDao.ListByIntegrationName — the DAO selects candidate workflows
// from Postgres; this function picks the matching trigger inside each
// candidate. If only one side knows about internal.name, fan-out either
// drops subscribers (DAO too narrow) or wastes ExecuteWorkflow calls on
// non-matching workflows (loop too narrow).
func webhookTriggerMatchesIntegration(trigger model.Trigger, integrationName string) bool {
	if trigger.Type != model.WorkflowTriggerWebhook {
		return false
	}
	if integrationName == "" {
		return false
	}
	if paramsName, _ := trigger.Params["integration_name"].(string); paramsName == integrationName {
		return true
	}
	if trigger.Internal != nil && trigger.Internal.Name == integrationName {
		return true
	}
	return false
}

func normalizeWebhookTriggers(workflowID string, wf *model.Workflow) {
	prefix := fmt.Sprintf("wf-%s-", workflowID)
	for i, trigger := range wf.Definition.Triggers {
		if trigger.Type != model.WorkflowTriggerWebhook {
			continue
		}
		integrationID, ok := trigger.Params["integration_name"].(string)
		if !ok || integrationID == "" {
			continue
		}
		if wf.Definition.Triggers[i].Internal == nil {
			wf.Definition.Triggers[i].Internal = &model.TriggerInternal{}
		}
		// Already normalized on a prior save — keep the existing binding.
		// Re-deriving here would rebind the workflow to a different
		// integration if the user later created one with the same
		// unprefixed name. Internal.Name intentionally keeps the legacy
		// "wf-<id>-" shadow prefix on pre-#30059 rows because the cleanup
		// paths in UpdateWorkflow / DeleteWorkflow read that prefix to
		// distinguish auto-managed shadow integrations from user-managed
		// ones. Stripping here would orphan the shadow on legacy rows.
		// enforceWebhookSecrets unprefixes at compare time instead.
		if wf.Definition.Triggers[i].Internal.Name != "" {
			continue
		}
		if strings.HasPrefix(integrationID, prefix) {
			// Legacy data path: Params held the prefixed name. Strip it.
			wf.Definition.Triggers[i].Internal.Name = integrationID
			wf.Definition.Triggers[i].Params["integration_name"] = strings.TrimPrefix(integrationID, prefix)
			continue
		}
		// New picker flow: Params has the user's chosen integration name.
		// Use it as-is so we bind to (and reuse) the user-managed
		// workflow_webhook integration instead of auto-creating a
		// prefixed shadow that the user can't see in the UI.
		wf.Definition.Triggers[i].Internal.Name = integrationID
	}
}

func (s *Service) CreateWorkflow(ctx *security.RequestContext, accountId string, wf model.Workflow) (string, string, error) {
	if !ctx.GetSecurityContext().HasAccountAccess(accountId, security.SecurityAccessTypeCreate) {
		return "", "", common.ErrorUnauthorized("account not accessible")
	}

	wf.CreatedBy = ctx.GetSecurityContext().GetUserId()
	wf.UpdatedBy = ctx.GetSecurityContext().GetUserId()
	wf.ID = uuid.New().String() // Always generate a new ID for CreateWorkflow

	validStatuses := []model.WorkflowStatus{model.WorkflowStatusActive, model.WorkflowStatusPaused, model.WorkflowStatusInactive}
	isValidStatus := false
	for _, status := range validStatuses {
		if wf.Status == status {
			isValidStatus = true
			break
		}
	}
	if wf.Status == "" || !isValidStatus {
		// New workflows now land PAUSED so the user opts in before scheduled /
		// event triggers fire. With status moved onto each workflow_versions row
		// (V746) and workflow.status acting as a derived mirror of the live
		// version's status, a paused v1 keeps the workflow runnable via manual
		// triggers while preventing surprise auto-execution after create —
		// reversing the V742 auto-activate decision that confused users who
		// wanted to inspect their workflow before letting it loose.
		wf.Status = model.WorkflowStatusPaused
	}

	if wf.Definition.Version == "" {
		wf.Definition.Version = "v1"
	}

	normalizeWebhookTriggers(wf.ID, &wf)

	if err := enforceWebhookSecrets(&wf, nil, wf.ID); err != nil {
		return "", "", err
	}

	if err := s.ValidateWorkflow(ctx, accountId, wf); err != nil {
		return "", "", err
	}

	// Validate schedule trigger inputs before saving
	for i, trigger := range wf.Definition.Triggers {
		if trigger.Type == model.WorkflowTriggerSchedule {
			inputs := make(map[string]any)
			for k, v := range trigger.Params {
				if k != "cron" && k != "overlap_policy" && k != "catchup_window" {
					inputs[k] = v
				}
			}
			if err := s.validateWorkflowInputs(wf.Definition.Inputs, inputs); err != nil {
				return "", "", fmt.Errorf("invalid inputs for schedule trigger index %d: %w", i, err)
			}
		}
	}

	// Check if a workflow with the same name already exists for this tenant and account
	existingWf, err := s.store.FindByName(ctx.GetContext(), ctx.GetSecurityContext().GetTenantId(), accountId, wf.Name)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return "", "", fmt.Errorf("failed to check for existing workflow: %w", err)
	}
	if existingWf != nil {
		return "", "", common.ErrorBadRequest(fmt.Sprintf("workflow with name '%s' already exists for this tenant and account", wf.Name))
	}

	// Insert the workflow, snapshot it as v1, and mark v1 live — all in one
	// transaction. Atomicity matters: a workflow with no live version cannot be
	// executed, so a partial create must not be left behind.
	storedId, _, err := s.store.CreateWorkflowWithInitialVersion(ctx.GetContext(), ctx.GetSecurityContext().GetTenantId(), accountId, wf)
	if err != nil {
		return "", "", err
	}
	wf.ID = storedId
	wf.TenantID = ctx.GetSecurityContext().GetTenantId()
	wf.AccountID = accountId

	// Only handle schedule triggers here, webhooks are handled by a generic endpoint
	_, token, err := s.handleWorkflowTrigger(ctx, storedId, ctx.GetSecurityContext().GetTenantId(), accountId, &wf)
	if err != nil {
		return storedId, "", err
	}
	emitWorkflowAudit(
		ctx, accountId,
		audit.EventTypeAutorunbookCreate, audit.EventActionCreate, audit.EventStatusSuccess,
		storedId, nil, workflowAuditSnapshot(&wf), nil,
	)
	return storedId, token, nil
}

func (s *Service) handleWorkflowTrigger(ctx *security.RequestContext, id, tenantId, accountId string, wf *model.Workflow) (client.WorkflowRun, string, error) {
	scheduleClient := s.temporalClient.ScheduleClient()

	var webhookToken string
	scheduleCount := 0

	// Process all triggers
	for i, trigger := range wf.Definition.Triggers {
		switch trigger.Type {
		case model.WorkflowTriggerSchedule:
			if c, ok := trigger.Params["cron"].(string); ok && c != "" {
				cron := c
				var overlapPolicy string
				var catchupWindow string
				inputs := make(map[string]any)

				// Extract schedule configuration params
				if op, ok := trigger.Params["overlap_policy"].(string); ok {
					overlapPolicy = op
				}
				if cw, ok := trigger.Params["catchup_window"].(string); ok {
					catchupWindow = cw
				}

				// Extract workflow inputs from params (everything else)
				for k, v := range trigger.Params {
					if k != "cron" && k != "overlap_policy" && k != "catchup_window" {
						inputs[k] = v
					}
				}

				// If workflow is Inactive, ensure schedule is removed.
				if wf.Status == model.WorkflowStatusInactive {
					// Logic to remove schedules handled below
				} else {
					// Create or update this specific schedule
					paused := wf.Status == model.WorkflowStatusPaused
					err := s.createOrUpdateSchedule(ctx.GetContext(), id, tenantId, accountId, wf, scheduleCount, cron, overlapPolicy, catchupWindow, inputs, paused)
					if err != nil {
						return nil, "", fmt.Errorf("failed to create or update schedule index %d: %w", scheduleCount, err)
					}
					scheduleCount++
				}
			}
		case model.WorkflowTriggerWebhook:
			if trigger.Internal == nil || trigger.Internal.Name == "" {
				return nil, "", common.ErrorBadRequest("webhook trigger missing internal name after normalization")
			}
			integrationID := trigger.Internal.Name

			// Check if secret already exists
			if _, hasSecret := trigger.Params["secret"]; !hasSecret {
				resp, err := integrations.CreateWorkflowWebhookTrigger(ctx, accountId, id, integrationID)
				if err != nil {
					return nil, "", fmt.Errorf("failed to create webhook trigger: %w", err)
				}
				wf.Definition.Triggers[i].Params["secret"] = resp.Token
				webhookToken = resp.Token

				// Update workflow in DB to persist the secret. This is an
				// internal touch-up (webhook secret injection), not a user
				// publish, so we only write the draft — no version row.
				err = s.store.Update(ctx.GetContext(), tenantId, accountId, id, *wf)
				if err != nil {
					return nil, "", fmt.Errorf("failed to update workflow with webhook secret: %w", err)
				}
			} else {
				if token, ok := trigger.Params["secret"].(string); ok {
					webhookToken = token
				}
			}
		}
	}

	// Cleanup logic
	// 1. Check for and delete legacy single schedule "workflow-schedule-<id>"
	legacyScheduleID := "workflow-schedule-" + id
	if _, err := scheduleClient.GetHandle(ctx.GetContext(), legacyScheduleID).Describe(ctx.GetContext()); err == nil {
		_ = scheduleClient.GetHandle(ctx.GetContext(), legacyScheduleID).Delete(ctx.GetContext())
	}

	// 2. Identify all valid schedule IDs we just processed
	validScheduleIDs := make(map[string]bool)
	// If workflow is Inactive, valid set is empty (all should be deleted)
	if wf.Status != model.WorkflowStatusInactive {
		for i := 0; i < scheduleCount; i++ {
			validScheduleIDs[fmt.Sprintf("workflow-schedule-%s-%d", id, i)] = true
		}
	}

	// 3. List ALL schedules for this workflow using Search Attribute and delete invalid ones.
	// We rely on the fact that we add SearchAttrWorkflowID to every schedule we create.
	// Query: "nb_workflow_id = '<id>'"
	listView, err := scheduleClient.List(ctx.GetContext(), client.ScheduleListOptions{
		Query: fmt.Sprintf("%s = '%s'", model.SearchAttrWorkflowID, id),
	})
	if err != nil {
		slog.Error("failed to list schedules for cleanup", "workflowID", id, "error", err)
		// Fallback to sequential cleanup if list fails (e.g. search attribute not indexed yet)
		// Try to cleanup next 10 potential schedules to be safe.
		for i := scheduleCount; ; i++ {
			sid := fmt.Sprintf("workflow-schedule-%s-%d", id, i)
			handle := scheduleClient.GetHandle(ctx.GetContext(), sid)
			_, err := handle.Describe(ctx.GetContext())
			if err != nil {
				var notFound *serviceerror.NotFound
				if errors.As(err, &notFound) {
					break
				}
				slog.Warn("failed to describe schedule for cleanup fallback", "scheduleID", sid, "error", err)
				break
			}
			if err := handle.Delete(ctx.GetContext()); err != nil {
				slog.Warn("failed to delete extra schedule (fallback)", "scheduleID", sid, "error", err)
			}
		}
	} else {
		// Iterate through the list view
		for listView.HasNext() {
			sEntry, err := listView.Next()
			if err != nil {
				slog.Error("failed to iterate schedule list", "error", err)
				break
			}

			// Check if this schedule belongs to us and is NOT in the valid list
			// Note: legacy schedules might show up here too if they have the search attribute.
			// We already deleted legacy specific ID above, but let's be safe.
			if !validScheduleIDs[sEntry.ID] {
				// Double check it matches our naming convention to avoid deleting something else (unlikely with search attr)
				if strings.HasPrefix(sEntry.ID, "workflow-schedule-"+id) {
					slog.Info("Deleting orphaned schedule", "scheduleID", sEntry.ID)
					if err := scheduleClient.GetHandle(ctx.GetContext(), sEntry.ID).Delete(ctx.GetContext()); err != nil {
						slog.Warn("failed to delete orphaned schedule", "scheduleID", sEntry.ID, "error", err)
					}
				}
			}
		}
	}

	return nil, webhookToken, nil
}

func (s *Service) createOrUpdateSchedule(ctx context.Context, workflowID string, tenantId, accountId string, wf *model.Workflow, index int, cron, overlapPolicy, catchupWindow string, inputs map[string]any, paused bool) error {
	if tenantId == "" || accountId == "" {
		return fmt.Errorf("tenantId and accountId are required for scheduling")
	}

	scheduleClient := s.temporalClient.ScheduleClient()
	scheduleID := fmt.Sprintf("workflow-schedule-%s-%d", workflowID, index)

	searchAttributes := make(map[string]any)
	if len(wf.Tags) > 0 {
		var tags []string
		for k, v := range wf.Tags {
			if s, ok := v.(string); ok {
				tags = append(tags, fmt.Sprintf("%s:%s", k, s))
			}
		}
		if len(tags) > 0 {
			searchAttributes[model.SearchAttrExecutionTags] = tags
		}
	}

	searchAttributes[model.SearchAttrTenantID] = tenantId
	searchAttributes[model.SearchAttrAccountID] = accountId
	searchAttributes[model.SearchAttrWorkflowID] = workflowID
	searchAttributes[model.SearchAttrWorkflowTrigger] = "schedule"

	// Create a shallow copy of the workflow to ensure we don't modify the original pointer
	wfCopy := *wf
	wfCopy.AccountID = accountId
	wfCopy.TenantID = tenantId

	ensureUpdatedByUser(&wfCopy)

	// IMPORTANT: Deep copy Inputs slice because we will modify elements within it
	if wf.Definition.Inputs != nil {
		newInputs := make([]model.Input, len(wf.Definition.Inputs))
		copy(newInputs, wf.Definition.Inputs)
		wfCopy.Definition.Inputs = newInputs
	}

	// Patch default values with provided inputs for this schedule
	for key, value := range inputs {
		for i, input := range wfCopy.Definition.Inputs {
			if input.ID == key {
				wfCopy.Definition.Inputs[i].Default = value
			}
		}
	}

	var typedSAs []temporal.SearchAttributeUpdate
	for k, v := range searchAttributes {
		if k == model.SearchAttrExecutionTags {
			if tags, ok := v.([]string); ok {
				typedSAs = append(typedSAs, temporal.NewSearchAttributeKeyKeywordList(k).ValueSet(tags))
			}
		} else if s, ok := v.(string); ok {
			typedSAs = append(typedSAs, temporal.NewSearchAttributeKeyKeyword(k).ValueSet(s))
		}
	}

	action := &client.ScheduleWorkflowAction{
		ID:                    fmt.Sprintf("scheduled-run-%s-%d", workflowID, index), // Unique run ID prefix? Temporal adds timestamp usually.
		Workflow:              s.workflowExecutor.ExecuteWorkflowInternal,
		Args:                  []any{&wfCopy, inputs},
		TaskQueue:             config.Config.RunbookServerTemporalQueue,
		TypedSearchAttributes: temporal.NewSearchAttributes(typedSAs...),
	}

	spec := client.ScheduleSpec{
		CronExpressions: []string{cron},
	}

	overlap := enums.SCHEDULE_OVERLAP_POLICY_SKIP // Default

	switch overlapPolicy {
	case "BufferOne":
		overlap = enums.SCHEDULE_OVERLAP_POLICY_BUFFER_ONE
	case "BufferAll":
		overlap = enums.SCHEDULE_OVERLAP_POLICY_BUFFER_ALL
	case "CancelOther":
		overlap = enums.SCHEDULE_OVERLAP_POLICY_CANCEL_OTHER
	case "TerminateOther":
		overlap = enums.SCHEDULE_OVERLAP_POLICY_TERMINATE_OTHER
	case "AllowAll":
		overlap = enums.SCHEDULE_OVERLAP_POLICY_ALLOW_ALL
	case "Skip":
		overlap = enums.SCHEDULE_OVERLAP_POLICY_SKIP
	}

	var catchup time.Duration
	if catchupWindow != "" {
		if d, err := time.ParseDuration(catchupWindow); err == nil {
			catchup = d
		}
	}

	// Check if schedule exists
	handle := scheduleClient.GetHandle(ctx, scheduleID)
	_, err := handle.Describe(ctx)
	exists := err == nil

	if exists {
		// Delete and recreate to ensure all properties (especially SearchAttributes and Action Args) are updated
		if err := handle.Delete(ctx); err != nil {
			return fmt.Errorf("failed to delete existing schedule for update: %w", err)
		}
	}

	_, err = scheduleClient.Create(ctx, client.ScheduleOptions{
		ID:               scheduleID,
		Spec:             spec,
		Action:           action,
		SearchAttributes: searchAttributes,
		Overlap:          overlap,
		CatchupWindow:    catchup,
		Paused:           paused,
	})
	return err
}

// resolveUserDisplayName looks up a user's display name from the database.
// Returns empty string if the lookup fails or the user is not found.
func resolveUserDisplayName(userID string) string {
	if userID == "" {
		return ""
	}
	dbManager, err := common.GetDatabaseManager(common.Metastore)
	if err != nil {
		slog.Warn("Failed to get database manager for user display name lookup", "error", err)
		return ""
	}
	var displayName string
	err = dbManager.Db.Get(&displayName, "SELECT display_name FROM users WHERE id = $1", userID)
	if err != nil {
		return ""
	}
	return displayName
}

// ensureUpdatedByUser populates wf.UpdatedByUser if it's not already set.
func ensureUpdatedByUser(wf *model.Workflow) {
	if wf.UpdatedByUser != nil || wf.UpdatedBy == "" {
		return
	}
	displayName := resolveUserDisplayName(wf.UpdatedBy)
	if displayName != "" {
		wf.UpdatedByUser = &model.WorkflowUser{
			ID:          wf.UpdatedBy,
			DisplayName: displayName,
		}
	}
}

// setTriggeredByUser populates wf.TriggeredByUser based on the requesting user.
// Empty triggererID (e.g. scheduled/webhook triggers) is a no-op.
func setTriggeredByUser(wf *model.Workflow, triggererID string) {
	if triggererID == "" {
		return
	}
	displayName := resolveUserDisplayName(triggererID)
	wf.TriggeredByUser = &model.WorkflowUser{
		ID:          triggererID,
		DisplayName: displayName,
	}
}

func (s *Service) ExecuteWorkflow(ctx *security.RequestContext, accountId, id string, triggerType model.WorkflowTrigger, inputs map[string]any) (string, error) {
	if !ctx.GetSecurityContext().HasAccountAccess(accountId, security.SecurityAccessTypeCreate) {
		return "", common.ErrorUnauthorized("account not accessible")
	}

	wf, err := s.store.Find(ctx.GetContext(), ctx.GetSecurityContext().GetTenantId(), accountId, id)
	if err != nil {
		return "", err
	}

	if triggerType == "" {
		triggerType = model.WorkflowTriggerManual
	}

	if wf.Status != model.WorkflowStatusActive {
		switch wf.Status {
		case model.WorkflowStatusPaused:
			if triggerType != model.WorkflowTriggerManual {
				return "", fmt.Errorf("workflow %s is paused", id)
			}
		default:
			return "", fmt.Errorf("workflow %s is not active, status is %s", id, wf.Status)
		}
	}

	// Load the LIVE version snapshot — executions run published versions, not
	// the draft (workflows.definition). CreateWorkflow auto-publishes v1 and
	// sets it live, so a missing pointer is a defensive error.
	if wf.LiveVersionID == nil || *wf.LiveVersionID == "" {
		return "", fmt.Errorf("workflow %s has no live version", id)
	}
	liveVersion, err := s.store.GetLiveWorkflowVersion(ctx.GetContext(), id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", fmt.Errorf("workflow %s live version (%s) not found", id, *wf.LiveVersionID)
		}
		return "", fmt.Errorf("failed to load live workflow version: %w", err)
	}

	// Run the live version snapshot and stamp its identity into Memo so the
	// execution detail UI can render exactly what ran.
	wf.Definition = liveVersion.Definition
	memo := map[string]any{
		model.MemoWorkflowVersionID:     liveVersion.ID,
		model.MemoWorkflowVersionNumber: int64(liveVersion.VersionNumber),
		model.MemoWorkflowVersionName:   strDerefOrEmpty(liveVersion.Name),
	}
	return s.runWorkflow(ctx, accountId, id, wf, memo, triggerType, inputs)
}

// TriggerWorkflowFromDraft runs the workflow's current on-screen draft definition
// directly (no version snapshot), tagging the execution with a draft-run Memo
// marker. Powers the canvas "Run current" button so a user can iterate on a draft
// without publishing. The exact graph that ran is recovered from Temporal history
// (the start-event input) by GetDetailedWorkflowExecution — so we don't pollute
// workflow_versions (and its retention cap) with throwaway dev runs.
//
// Differences from ExecuteWorkflow:
//   - Runs the current draft (workflows.definition), not the live version pointer.
//   - Writes no workflow_versions row and never touches the live pointer.
//   - Trigger type is forced to manual.
func (s *Service) TriggerWorkflowFromDraft(ctx *security.RequestContext, accountId, id string, inputs map[string]any) (string, error) {
	if !ctx.GetSecurityContext().HasAccountAccess(accountId, security.SecurityAccessTypeCreate) {
		return "", common.ErrorUnauthorized("account not accessible")
	}

	wf, err := s.store.Find(ctx.GetContext(), ctx.GetSecurityContext().GetTenantId(), accountId, id)
	if err != nil {
		return "", err
	}

	// Manual draft runs are allowed for Active and Paused workflows (both accept
	// manual triggers). Only fully Inactive / unknown statuses are refused, so a
	// disabled workflow can't be hot-revived through the draft path.
	if wf.Status != model.WorkflowStatusActive && wf.Status != model.WorkflowStatusPaused {
		return "", fmt.Errorf("workflow %s is not runnable, status is %s", id, wf.Status)
	}

	// wf.Definition is the current draft as loaded by Find — run it as-is.
	memo := map[string]any{model.MemoWorkflowIsDraftRun: true}
	return s.runWorkflow(ctx, accountId, id, wf, memo, model.WorkflowTriggerManual, inputs)
}

// runWorkflow is the shared tail of ExecuteWorkflow and TriggerWorkflowFromDraft.
// The caller sets wf.Definition to the exact definition to run (live snapshot for
// ExecuteWorkflow, on-screen draft for TriggerWorkflowFromDraft) and supplies the
// Temporal Memo (version linkage, or the draft-run marker). This function kicks
// off the Temporal workflow; Temporal records wf (incl. its definition) in history,
// which is the canonical record of what ran. Callers own status checks.
func (s *Service) runWorkflow(ctx *security.RequestContext, accountId, id string, wf *model.Workflow, memo map[string]any, triggerType model.WorkflowTrigger, inputs map[string]any) (string, error) {
	// 1. Validate provided inputs against workflow definition
	if err := s.validateWorkflowInputs(wf.Definition.Inputs, inputs); err != nil {
		return "", err // Return validation error
	}

	var runWorkflowID string
	if triggerType != model.WorkflowTriggerSchedule {
		runWorkflowID = uuid.New().String()
	} else {
		runWorkflowID = id
	}

	searchAttributes := make(map[string]any)
	if len(wf.Tags) > 0 {
		var tags []string
		for k, v := range wf.Tags {
			if s, ok := v.(string); ok {
				tags = append(tags, fmt.Sprintf("%s:%s", k, s))
			}
		}
		if len(tags) > 0 {
			searchAttributes[model.SearchAttrExecutionTags] = tags
		}
	}

	searchAttributes[model.SearchAttrTenantID] = ctx.GetSecurityContext().GetTenantId()
	searchAttributes[model.SearchAttrAccountID] = accountId
	searchAttributes[model.SearchAttrWorkflowID] = id // Store the definition ID as well
	searchAttributes[model.SearchAttrWorkflowTrigger] = triggerType
	searchAttributes[model.SearchAttrTriggeredBy] = ctx.GetSecurityContext().GetUserId()

	// memo (execution-to-version linkage, or the draft-run marker) is supplied by
	// the caller and stamped into Temporal Memo. Memo (not SearchAttributes) keeps
	// zero coupling to per-namespace admin config — the only consumer is
	// GetDetailedWorkflowExecution for display, never a filter.
	if memo == nil {
		memo = map[string]any{}
	}

	// Determine workflow timeout
	workflowTimeout := 1 * time.Hour // Default
	if wf.Definition.Timeout != "" {
		if d, err := time.ParseDuration(wf.Definition.Timeout); err == nil {
			workflowTimeout = d
		} else {
			slog.Warn("Invalid workflow timeout duration, using default", "timeout", wf.Definition.Timeout, "error", err)
		}
	}

	options := client.StartWorkflowOptions{
		ID:                       runWorkflowID, // Use the generated unique ID for Temporal Workflow ID
		TaskQueue:                config.Config.RunbookServerTemporalQueue,
		SearchAttributes:         searchAttributes,
		Memo:                     memo,
		WorkflowExecutionTimeout: workflowTimeout,
	}

	for key, value := range inputs {
		for i, input := range wf.Definition.Inputs {
			if input.ID == key {
				wf.Definition.Inputs[i].Default = value
			}
		}
	}

	wf.AccountID = accountId
	wf.TenantID = ctx.GetSecurityContext().GetTenantId()
	wf.ID = id

	ensureUpdatedByUser(wf)
	setTriggeredByUser(wf, ctx.GetSecurityContext().GetUserId())

	we, err := s.temporalClient.ExecuteWorkflow(context.Background(), options, s.workflowExecutor.ExecuteWorkflowInternal, wf, inputs)
	if err != nil {
		return "", err
	}
	if triggerType == model.WorkflowTriggerManual {
		emitWorkflowAudit(
			ctx, accountId,
			audit.EventTypeAutorunbookManualRun, audit.EventActionCreate, audit.EventStatusSuccess,
			id, nil,
			map[string]any{"workflow_id": id, "workflow_name": wf.Name},
			map[string]any{"execution_id": we.GetRunID(), "trigger_type": string(triggerType)},
		)
	}
	return we.GetRunID(), nil // Return the actual Temporal Run ID
}

// FanOutWebhookEvent dispatches a webhook payload to every active workflow whose
// webhook trigger subscribes to the given integration name, scoped to the calling
// security context's tenant. For each subscriber:
//
//  1. The trigger's params.filter (Jinja) is evaluated against the payload.
//     A subscriber whose filter does not render to "true" is counted as
//     `filtered` and skipped without executing.
//  2. Otherwise ExecuteWorkflow is called with the workflow's own accountId so
//     cross-account routing forwards the correct tenant+account pair.
//
// A best-effort dispatch: errors on individual subscribers are captured per-
// entry in the returned result; the function only errors out when the
// subscriber lookup itself fails (DB failure, etc.). Caller can rely on
// the per-subscriber Status to know what actually happened.
//
// The security context must be tenant-admin level for this to work — every
// subscriber may live in a different account inside the tenant.
func (s *Service) FanOutWebhookEvent(ctx *security.RequestContext, integrationName string, payload []byte) (FanOutResult, error) {
	if integrationName == "" {
		return FanOutResult{}, common.ErrorBadRequest("integration_name is required")
	}

	tenantID := ctx.GetSecurityContext().GetTenantId()
	if tenantID == "" {
		return FanOutResult{}, common.ErrorUnauthorized("tenant id missing on security context")
	}

	subscribers, err := s.store.ListByIntegrationName(ctx.GetContext(), tenantID, integrationName)
	if err != nil {
		ctx.GetLogger().Error("fanout: failed to list subscribers", "integration_name", integrationName, "tenant_id", tenantID, "error", err)
		return FanOutResult{}, err
	}

	// Parse the payload once; reuse for every subscriber's filter evaluation.
	// Non-JSON payloads fall through as a raw string so Jinja expressions like
	// {{ webhook_payload | trim }} can still operate on them.
	var payloadData any
	if json.Unmarshal(payload, &payloadData) != nil {
		payloadData = string(payload)
	}
	payloadString := string(payload)

	// Build the template context once. The payload is identical across every
	// subscriber's filter render, and Render does not mutate the context, so
	// constructing it inside the loop would be pure overhead at the hot path.
	tplCtx := NewTemplateContext(nil, nil)
	tplCtx.Secrets = nil // Don't expose secrets via filter expressions
	tplCtx.Inputs["webhook_payload"] = payloadData
	tplCtx.Vars["webhook_payload"] = payloadData

	result := FanOutResult{Subscribers: make([]FanOutSubscriberResult, 0, len(subscribers))}
	for _, wf := range subscribers {
		sub := FanOutSubscriberResult{WorkflowID: wf.ID, AccountID: wf.AccountID}
		// A single workflow can declare multiple webhook triggers; iterate
		// every trigger that matches the integration and try each one. A
		// filter failure or non-true result on one trigger must not
		// short-circuit subsequent triggers — pick the best outcome across
		// all of them: fired > failed > filtered > skipped.
		matched := false
		fired := false
		sawFailure := false
		sawFiltered := false
		var lastErr string

		for _, trigger := range wf.Definition.Triggers {
			if trigger.Type != model.WorkflowTriggerWebhook {
				continue
			}
			if !webhookTriggerMatchesIntegration(trigger, integrationName) {
				continue
			}
			matched = true

			triggerFilter, _ := trigger.Params["filter"].(string)
			if triggerFilter != "" {
				rendered, ferr := Render(triggerFilter, tplCtx)
				if ferr != nil {
					sawFailure = true
					lastErr = "filter render error: " + ferr.Error()
					ctx.GetLogger().Error("fanout: failed to render filter", "workflow_id", wf.ID, "error", ferr)
					continue
				}
				if strings.ToLower(strings.TrimSpace(rendered)) != "true" {
					sawFiltered = true
					continue
				}
			}

			inputs := map[string]any{"webhook_payload": payloadString}
			runID, exerr := s.ExecuteWorkflow(ctx, wf.AccountID, wf.ID, model.WorkflowTriggerWebhook, inputs)
			if exerr != nil {
				sawFailure = true
				lastErr = exerr.Error()
				ctx.GetLogger().Error("fanout: failed to execute workflow", "workflow_id", wf.ID, "account_id", wf.AccountID, "error", exerr)
				continue
			}
			sub.ExecutionID = runID
			fired = true
			break // Fire once per workflow; don't double-execute even with multiple matching triggers.
		}

		switch {
		case !matched:
			// Workflow row came back from the index but its in-memory definition
			// lost the matching trigger (rare — possible mid-edit race).
			sub.Status = "skipped"
			sub.Error = "no webhook trigger matches integration_name"
			result.Skipped++
		case fired:
			sub.Status = "fired"
			result.Fired++
		case sawFailure:
			sub.Status = "failed"
			sub.Error = lastErr
			result.Failed++
		case sawFiltered:
			sub.Status = "filtered"
			result.Filtered++
		}
		result.Subscribers = append(result.Subscribers, sub)
	}

	return result, nil
}

func strDerefOrEmpty(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

func (s *Service) RetriggerWorkflowExecution(ctx *security.RequestContext, accountId, workflowId, executionId string, inputs map[string]any) (string, error) {
	if !ctx.GetSecurityContext().HasAccountAccess(accountId, security.SecurityAccessTypeCreate) {
		return "", common.ErrorUnauthorized("account not accessible")
	}

	// 1. Get original execution details
	details, err := s.GetDetailedWorkflowExecution(ctx, accountId, workflowId, executionId)
	if err != nil {
		return "", fmt.Errorf("failed to get original execution details: %w", err)
	}

	// 2. Check status (Must NOT be RUNNING)
	if details.Status == model.WorkflowExecutionStatusRunning {
		return "", common.ErrorBadRequest(fmt.Sprintf("cannot relay running workflow execution %s", executionId))
	}

	// 3. Retrieve original workflow definition to validate
	wf, err := s.store.Find(ctx.GetContext(), ctx.GetSecurityContext().GetTenantId(), accountId, workflowId)
	if err != nil {
		return "", fmt.Errorf("failed to retrieve workflow definition: %w", err)
	}

	if wf.Status != model.WorkflowStatusActive && wf.Status != model.WorkflowStatusPaused {
		return "", fmt.Errorf("workflow %s is not active or paused", workflowId)
	}

	// 4. Merge inputs
	mergedInputs := make(map[string]any)
	// Start with original inputs
	for k, v := range details.Inputs {
		mergedInputs[k] = v
	}
	// Override with new inputs
	for k, v := range inputs {
		mergedInputs[k] = v
	}

	// 5. Validate merged inputs against workflow definition
	if err := s.validateWorkflowInputs(wf.Definition.Inputs, mergedInputs); err != nil {
		return "", err
	}

	runWorkflowID := uuid.New().String()

	searchAttributes := make(map[string]any)
	if len(wf.Tags) > 0 {
		var tags []string
		for k, v := range wf.Tags {
			if s, ok := v.(string); ok {
				tags = append(tags, fmt.Sprintf("%s:%s", k, s))
			}
		}
		if len(tags) > 0 {
			searchAttributes[model.SearchAttrExecutionTags] = tags
		}
	}

	searchAttributes[model.SearchAttrTenantID] = ctx.GetSecurityContext().GetTenantId()
	searchAttributes[model.SearchAttrAccountID] = accountId
	searchAttributes[model.SearchAttrWorkflowID] = workflowId
	searchAttributes[model.SearchAttrWorkflowTrigger] = model.WorkflowTriggerManual
	searchAttributes[model.SearchAttrTriggeredBy] = ctx.GetSecurityContext().GetUserId()

	// Determine workflow timeout
	workflowTimeout := 1 * time.Hour // Default
	if wf.Definition.Timeout != "" {
		if d, err := time.ParseDuration(wf.Definition.Timeout); err == nil {
			workflowTimeout = d
		} else {
			slog.Warn("Invalid workflow timeout duration, using default", "timeout", wf.Definition.Timeout, "error", err)
		}
	}

	options := client.StartWorkflowOptions{
		ID:                       runWorkflowID,
		TaskQueue:                config.Config.RunbookServerTemporalQueue,
		SearchAttributes:         searchAttributes,
		WorkflowExecutionTimeout: workflowTimeout,
	}

	// Create a shallow copy of the workflow to ensure we don't modify the original pointer
	wfCopy := *wf

	// IMPORTANT: Deep copy Inputs slice because we will modify elements within it
	if wf.Definition.Inputs != nil {
		newInputs := make([]model.Input, len(wf.Definition.Inputs))
		copy(newInputs, wf.Definition.Inputs)
		wfCopy.Definition.Inputs = newInputs
	}

	// Update default values in definition for execution
	for key, value := range mergedInputs {
		for i, input := range wfCopy.Definition.Inputs {
			if input.ID == key {
				wfCopy.Definition.Inputs[i].Default = value
			}
		}
	}

	wfCopy.AccountID = accountId
	wfCopy.TenantID = ctx.GetSecurityContext().GetTenantId()
	wfCopy.ID = workflowId

	ensureUpdatedByUser(&wfCopy)
	setTriggeredByUser(&wfCopy, ctx.GetSecurityContext().GetUserId())

	we, err := s.temporalClient.ExecuteWorkflow(ctx.GetContext(), options, s.workflowExecutor.ExecuteWorkflowInternal, &wfCopy, mergedInputs)
	if err != nil {
		return "", err
	}
	emitWorkflowAudit(
		ctx, accountId,
		audit.EventTypeAutorunbookManualRun, audit.EventActionCreate, audit.EventStatusSuccess,
		workflowId, nil,
		map[string]any{"workflow_id": workflowId, "workflow_name": wf.Name},
		map[string]any{"execution_id": we.GetRunID(), "replay_of": executionId, "trigger_type": "manual"},
	)
	return we.GetRunID(), nil
}

func (s *Service) DryRunWorkflow(ctx *security.RequestContext, accountId string, request model.DryRunWorkflowRequest) (model.DryRunWorkflowResponse, error) {
	if !ctx.GetSecurityContext().HasAccountAccess(accountId, security.SecurityAccessTypeRead) {
		return model.DryRunWorkflowResponse{}, common.ErrorUnauthorized("account not accessible")
	}

	// 1. Validate provided inputs against workflow definition
	if err := s.validateWorkflowInputs(request.Definition.Inputs, request.Inputs); err != nil {
		return model.DryRunWorkflowResponse{}, err
	}

	// 2. Validate task types and parameters
	dummyWf := model.Workflow{
		Definition: request.Definition,
	}
	if err := s.validateTaskTypes(ctx, accountId, dummyWf); err != nil {
		return model.DryRunWorkflowResponse{}, err
	}

	runWorkflowID := "dry-run-" + uuid.New().String()

	searchAttributes := make(map[string]any)
	searchAttributes[model.SearchAttrTenantID] = ctx.GetSecurityContext().GetTenantId()
	searchAttributes[model.SearchAttrAccountID] = accountId
	searchAttributes[model.SearchAttrWorkflowTrigger] = model.WorkflowTriggerManual
	searchAttributes[model.SearchAttrTriggeredBy] = ctx.GetSecurityContext().GetUserId()

	// Determine workflow timeout for dry run
	workflowTimeout := 30 * time.Minute // Default for dry run
	if request.Definition.Timeout != "" {
		if d, err := time.ParseDuration(request.Definition.Timeout); err == nil {
			workflowTimeout = d
		}
	}

	options := client.StartWorkflowOptions{
		ID:                       runWorkflowID,
		TaskQueue:                config.Config.RunbookServerTemporalQueue,
		SearchAttributes:         searchAttributes,
		WorkflowExecutionTimeout: workflowTimeout,
	}

	wf := model.Workflow{
		ID:         runWorkflowID,
		TenantID:   ctx.GetSecurityContext().GetTenantId(),
		AccountID:  accountId,
		Definition: request.Definition,
		DryRun:     true,
		Name:       dryRunWorkflowName(request.Name),
		CreatedBy:  ctx.GetSecurityContext().GetUserId(),
		UpdatedBy:  ctx.GetSecurityContext().GetUserId(),
	}

	run, err := s.temporalClient.ExecuteWorkflow(context.Background(), options, s.workflowExecutor.ExecuteWorkflowInternal, &wf, request.Inputs)
	if err != nil {
		return model.DryRunWorkflowResponse{}, err
	}

	var result string
	err = run.Get(context.Background(), &result)

	response := model.DryRunWorkflowResponse{
		Status: model.WorkflowExecutionStatusCompleted,
	}

	if err != nil {
		response.Status = model.WorkflowExecutionStatusFailed
		response.Error = err.Error()
	}

	// Fetch details from history to populate tasks and inputs
	historyIterator := s.temporalClient.GetWorkflowHistory(ctx.GetContext(), run.GetID(), run.GetRunID(), false, enums.HISTORY_EVENT_FILTER_TYPE_ALL_EVENT)
	historyDetails, historyErr := s.processWorkflowHistory(ctx, accountId, historyIterator, request.Definition)
	if historyErr == nil {
		response.Tasks = historyDetails.Tasks
		response.Inputs = historyDetails.Inputs

		if historyDetails.WorkflowResult != nil {
			response.Output = historyDetails.WorkflowResult
		}

		// Use more detailed error from history if available
		if response.Status == model.WorkflowExecutionStatusFailed && historyDetails.Error != "" {
			response.Error = historyDetails.Error
		}
	} else {
		ctx.GetLogger().Warn("failed to get history for dry run", "error", historyErr)
	}

	// For DryRun, history processing might miss tasks because activities are skipped.
	// Try to query the execution trace directly from the workflow.
	if len(response.Tasks) == 0 {
		queryResp, queryErr := s.temporalClient.QueryWorkflow(context.Background(), run.GetID(), run.GetRunID(), "getDryRunTrace")
		if queryErr == nil {
			var trace []model.TaskExecutionDetails
			if err := queryResp.Get(&trace); err == nil {
				response.Tasks = trace
			} else {
				ctx.GetLogger().Warn("failed to deserialize dry run trace", "error", err)
			}
		} else {
			// Query might fail if workflow failed/panic early, or history replay failed.
			ctx.GetLogger().Warn("failed to query dry run trace", "error", queryErr)
		}
	}

	// Fallback for output if history processing didn't capture it (e.g. malformed history or race)
	if response.Output == nil && result != "" {
		var output any
		if err := json.Unmarshal([]byte(result), &output); err == nil {
			response.Output = output
		}
	}

	return response, nil
}

// DryRunWorkflowAsync starts a dry-run workflow in Temporal and returns immediately
// with the workflow ID and execution ID for polling. The caller should use
// GetDetailedWorkflowExecution to poll for the result.
func (s *Service) DryRunWorkflowAsync(ctx *security.RequestContext, accountId string, request model.DryRunWorkflowRequest) (string, string, error) {
	if !ctx.GetSecurityContext().HasAccountAccess(accountId, security.SecurityAccessTypeRead) {
		return "", "", common.ErrorUnauthorized("account not accessible")
	}

	// Validate provided inputs against workflow definition
	if err := s.validateWorkflowInputs(request.Definition.Inputs, request.Inputs); err != nil {
		return "", "", err
	}

	// Validate task types and parameters
	dummyWf := model.Workflow{
		Definition: request.Definition,
	}
	if err := s.validateTaskTypes(ctx, accountId, dummyWf); err != nil {
		return "", "", err
	}

	runWorkflowID := "dry-run-" + uuid.New().String()

	searchAttributes := make(map[string]any)
	searchAttributes[model.SearchAttrTenantID] = ctx.GetSecurityContext().GetTenantId()
	searchAttributes[model.SearchAttrAccountID] = accountId
	searchAttributes[model.SearchAttrWorkflowTrigger] = model.WorkflowTriggerManual
	searchAttributes[model.SearchAttrTriggeredBy] = ctx.GetSecurityContext().GetUserId()
	searchAttributes[model.SearchAttrWorkflowID] = runWorkflowID

	// Determine workflow timeout for dry run
	workflowTimeout := 30 * time.Minute
	if request.Definition.Timeout != "" {
		if d, err := time.ParseDuration(request.Definition.Timeout); err == nil {
			workflowTimeout = d
		}
	}

	options := client.StartWorkflowOptions{
		ID:                       runWorkflowID,
		TaskQueue:                config.Config.RunbookServerTemporalQueue,
		SearchAttributes:         searchAttributes,
		WorkflowExecutionTimeout: workflowTimeout,
	}

	wf := model.Workflow{
		ID:         runWorkflowID,
		TenantID:   ctx.GetSecurityContext().GetTenantId(),
		AccountID:  accountId,
		Definition: request.Definition,
		DryRun:     true,
		Name:       dryRunWorkflowName(request.Name),
		CreatedBy:  ctx.GetSecurityContext().GetUserId(),
		UpdatedBy:  ctx.GetSecurityContext().GetUserId(),
	}

	run, err := s.temporalClient.ExecuteWorkflow(context.Background(), options, s.workflowExecutor.ExecuteWorkflowInternal, &wf, request.Inputs)
	if err != nil {
		return "", "", err
	}

	return runWorkflowID, run.GetRunID(), nil
}

// dryRunWorkflowName uses the supplied automation name, falling back to a
// synthetic timestamped placeholder when empty.
func dryRunWorkflowName(name string) string {
	if strings.TrimSpace(name) != "" {
		return name
	}
	return "dry-run-" + time.Now().Format(time.RFC3339)
}

// escapeTemporalString escapes single quotes in a string to prevent injection in Temporal visibility queries.
// Temporal uses SQL-like syntax where string literals are enclosed in single quotes, and single quotes
// within the string are escaped by doubling them (e.g. ' -> ”).
func escapeTemporalString(s string) string {
	return strings.ReplaceAll(s, "'", "''")
}

// validateWorkflowInputs checks if the provided inputs conform to the workflow's defined inputs.
func (s *Service) validateWorkflowInputs(definedInputs []model.Input, providedParams map[string]any) error {
	for _, input := range definedInputs {
		val, exists := providedParams[input.ID]

		if input.Required {
			// Check if input is provided OR if a default value exists
			if (!exists || val == nil) && input.Default == nil {
				return common.ErrorBadRequest(fmt.Sprintf("required input '%s' is missing", input.ID))
			}
			// For string types, check if empty string.
			// Note: If input.Default is defined, we assume it's valid.
			// If user provided explicitly empty string "", that might be invalid if required.
			if input.Type == "string" {
				if exists && val != nil {
					if strVal, ok := val.(string); ok && strVal == "" {
						return common.ErrorBadRequest(fmt.Sprintf("required string input '%s' cannot be empty", input.ID))
					}
				} else if input.Default != nil {
					// If relying on default, check if default is empty string (though definition validtion should catch this)
					if defStr, ok := input.Default.(string); ok && defStr == "" {
						// This arguably should be allowed if default is explicitly "", but usually required means non-empty.
						// Let's keep it strict: required means non-empty value eventually.
						return common.ErrorBadRequest(fmt.Sprintf("required input '%s' is missing and default is empty", input.ID))
					}
				}
			}
		}

		if exists && val != nil { // Only validate type if the value is provided
			switch input.Type {
			case "string":
				if _, ok := val.(string); !ok {
					return common.ErrorBadRequest(fmt.Sprintf("input '%s' must be a string, got %T", input.ID, val))
				}
			case "int":
				// JSON unmarshals numbers to float64 by default.
				// We accept either float64 or int for flexibility.
				if _, ok := val.(float64); !ok {
					if _, ok := val.(int); !ok {
						return common.ErrorBadRequest(fmt.Sprintf("input '%s' must be an integer, got %T", input.ID, val))
					}
				}
			case "bool":
				if _, ok := val.(bool); !ok {
					return common.ErrorBadRequest(fmt.Sprintf("input '%s' must be a boolean, got %T", input.ID, val))
				}
			case "json":
				// Attempt to marshal and unmarshal to ensure it's valid JSON
				// This handles cases where 'val' might be a string that needs parsing or an object.
				jsonBytes, err := json.Marshal(val)
				if err != nil {
					return common.ErrorBadRequest(fmt.Sprintf("input '%s' could not be marshaled to JSON: %v", input.ID, err))
				}
				var temp any
				if err := json.Unmarshal(jsonBytes, &temp); err != nil {
					return common.ErrorBadRequest(fmt.Sprintf("input '%s' must be valid JSON, parsing failed: %v", input.ID, err))
				}
			case "object": // Alias for JSON object
				// Check if it's a map (JSON object)
				if _, ok := val.(map[string]any); !ok {
					// Also check if it's a string that can be unmarshaled into a map
					if strVal, ok := val.(string); ok {
						var tempMap map[string]any
						if err := json.Unmarshal([]byte(strVal), &tempMap); err != nil {
							return common.ErrorBadRequest(fmt.Sprintf("input '%s' must be a JSON object, parsing failed: %v", input.ID, err))
						}
					} else {
						return common.ErrorBadRequest(fmt.Sprintf("input '%s' must be a JSON object, got %T", input.ID, val))
					}
				}
			case "array": // New case for JSON arrays
				if _, ok := val.([]any); !ok {
					// Also check if it's a string that can be unmarshaled into a slice
					if strVal, ok := val.(string); ok {
						var tempSlice []any
						if err := json.Unmarshal([]byte(strVal), &tempSlice); err != nil {
							return common.ErrorBadRequest(fmt.Sprintf("input '%s' must be a JSON array, parsing failed: %v", input.ID, err))
						}
					} else {
						return common.ErrorBadRequest(fmt.Sprintf("input '%s' must be a JSON array, got %T", input.ID, val))
					}
				}

			// Add other types as needed
			default:
				// If type is unspecified, or a custom type, we just pass through for now.
				// For now, only validate explicitly defined types.
				slog.Warn("Unhandled input type during validation", "inputType", input.Type, "inputID", input.ID)
			}
		}
	}
	return nil
}

func (s *Service) ListWorkflows(ctx *security.RequestContext, accountId string, request model.ListWorkflowRequest) (model.ListWorkflowResponse, error) {
	if !ctx.GetSecurityContext().HasAccountAccess(accountId, security.SecurityAccessTypeRead) {
		return model.ListWorkflowResponse{}, common.ErrorUnauthorized("account not accessible")
	}

	if request.Limit <= 0 {
		request.Limit = 50
	}

	workflows, totalCount, err := s.store.List(ctx.GetContext(), ctx.GetSecurityContext().GetTenantId(), accountId, request)
	if err != nil {
		return model.ListWorkflowResponse{}, err
	}
	for i := range workflows {
		if workflows[i].Tags == nil {
			workflows[i].Tags = make(map[string]any)
		}
		if workflows[i].Definition.Inputs == nil {
			workflows[i].Definition.Inputs = []model.Input{}
		}
		if workflows[i].Definition.Triggers == nil {
			workflows[i].Definition.Triggers = []model.Trigger{}
		}
		if workflows[i].Definition.Tasks == nil {
			workflows[i].Definition.Tasks = []model.Task{}
		}
		if workflows[i].Definition.Version == "" {
			workflows[i].Definition.Version = "v1"
		}
	}

	// Reconcile stale RUNNING statuses by checking Temporal's actual execution status.
	// When WorkflowExecutionTimeout fires, Temporal terminates the workflow server-side
	// without running cleanup code, leaving last_execution_status as RUNNING in the DB.
	s.reconcileRunningStatuses(ctx, accountId, workflows)

	var nextPageToken string
	if len(workflows) == request.Limit {
		offset, _ := strconv.Atoi(request.NextPageToken)
		nextPageToken = strconv.Itoa(offset + request.Limit)
	}

	return model.ListWorkflowResponse{
		Workflows:     workflows,
		NextPageToken: nextPageToken,
		TotalCount:    totalCount,
	}, nil
}

// reconcileRunningStatuses checks workflows with RUNNING status against Temporal
// to fix stale statuses caused by server-side timeouts or terminations.
// NOTE: This makes one Temporal visibility query per RUNNING workflow. This is acceptable
// because (a) very few workflows are in RUNNING state at any time, and (b) after the first
// reconciliation the DB is corrected so subsequent calls won't re-query for that workflow.
func (s *Service) reconcileRunningStatuses(ctx *security.RequestContext, accountId string, workflows []model.Workflow) {
	tenantID := ctx.GetSecurityContext().GetTenantId()
	for i := range workflows {
		if workflows[i].LastExecutionStatus != model.WorkflowExecutionStatusRunning {
			continue
		}

		query := fmt.Sprintf("%s='%s' AND %s='%s' AND %s='%s'",
			model.SearchAttrTenantID, tenantID,
			model.SearchAttrAccountID, accountId,
			model.SearchAttrWorkflowID, workflows[i].ID)

		resp, err := s.temporalClient.ListWorkflow(ctx.GetContext(), &workflowservice.ListWorkflowExecutionsRequest{
			Query:    query,
			PageSize: 1,
		})
		if err != nil || resp == nil || len(resp.Executions) == 0 {
			continue
		}

		latestExec := resp.Executions[0]
		latestStatus := mapTemporalStatusToModelStatus(latestExec.GetStatus())
		if latestStatus == model.WorkflowExecutionStatusRunning {
			continue
		}

		// Status is stale — update the workflow in the response
		workflows[i].LastExecutionStatus = latestStatus

		// Asynchronously update the DB so future reads are correct
		wfID := workflows[i].ID
		closeTime := time.Now().UTC()
		if latestExec.CloseTime != nil {
			closeTime = latestExec.CloseTime.AsTime()
		}
		var statusMessage string
		if latestStatus == model.WorkflowExecutionStatusTimedOut {
			statusMessage = "Workflow execution timed out"
		}
		go func() {
			dbCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			if err := s.store.SetLastExecutionStatus(dbCtx, tenantID, accountId, wfID, latestStatus, closeTime, statusMessage); err != nil {
				slog.Error("Failed to reconcile stale workflow status", "workflowID", wfID, "error", err)
			}
		}()
	}
}

func (s *Service) GetWorkflow(ctx *security.RequestContext, accountId, id string) (*model.Workflow, error) {
	if !ctx.GetSecurityContext().HasAccountAccess(accountId, security.SecurityAccessTypeRead) {
		return nil, common.ErrorUnauthorized("account not accessible")
	}

	if accountId == "" || id == "" {
		return nil, fmt.Errorf("tenantId, accountId, id are required")
	}
	wf, err := s.store.Find(ctx.GetContext(), ctx.GetSecurityContext().GetTenantId(), accountId, id)
	if err != nil {
		return nil, err
	}

	if wf.Tags == nil {
		wf.Tags = make(map[string]any)
	}

	// Ensure slices within WorkflowDefinition are not nil
	if wf.Definition.Inputs == nil {
		wf.Definition.Inputs = []model.Input{}
	}
	if wf.Definition.Triggers == nil {
		wf.Definition.Triggers = []model.Trigger{}
	}
	if wf.Definition.Tasks == nil {
		wf.Definition.Tasks = []model.Task{}
	}

	if wf.Definition.Version == "" {
		wf.Definition.Version = "v1"
	}

	// If the workflow has a schedule, try to get the next execution time.
	// We scan all possible schedules (legacy and indexed) and pick the earliest next run.
	var earliestNextRun *time.Time

	// Check legacy
	legacyScheduleID := "workflow-schedule-" + id
	if desc, err := s.temporalClient.ScheduleClient().GetHandle(ctx.GetContext(), legacyScheduleID).Describe(ctx.GetContext()); err == nil {
		if len(desc.Info.NextActionTimes) > 0 {
			next := desc.Info.NextActionTimes[0]
			earliestNextRun = &next
		}
	}

	// Check indexed schedules (scan up to 10 for example, or count triggers)
	// We can limit scan to number of schedule triggers defined in workflow, assuming consistency.
	scheduleTriggers := 0
	for _, t := range wf.Definition.Triggers {
		if t.Type == model.WorkflowTriggerSchedule {
			scheduleTriggers++
		}
	}

	for i := 0; i < scheduleTriggers; i++ {
		sid := fmt.Sprintf("workflow-schedule-%s-%d", id, i)
		if desc, err := s.temporalClient.ScheduleClient().GetHandle(ctx.GetContext(), sid).Describe(ctx.GetContext()); err == nil {
			if len(desc.Info.NextActionTimes) > 0 {
				next := desc.Info.NextActionTimes[0]
				if earliestNextRun == nil || next.Before(*earliestNextRun) {
					earliestNextRun = &next
				}
			}
		}
	}

	if earliestNextRun != nil {
		if wf.TriggerDetails == nil {
			wf.TriggerDetails = &model.TriggerInfo{}
		}
		wf.TriggerDetails.NextScheduledRun = earliestNextRun
	}

	return wf, nil
}

func (s *Service) UpdateWorkflow(ctx *security.RequestContext, accountId, id string, workflow model.Workflow) (model.Workflow, error) {
	// Snapshot the pre-edit state for the audit row. Tolerate a lookup miss
	// (sql.ErrNoRows) — updateWorkflowInternal will surface the real error;
	// we only need prevSnap for the audit and a nil snapshot is fine if the
	// workflow is missing.
	var prevSnap map[string]any
	if existing, err := s.store.Find(ctx.GetContext(), ctx.GetSecurityContext().GetTenantId(), accountId, id); err == nil {
		prevSnap = workflowAuditSnapshot(existing)
	}
	updated, err := s.updateWorkflowInternal(ctx, accountId, id, workflow)
	if err != nil {
		return updated, err
	}
	emitWorkflowAudit(
		ctx, accountId,
		audit.EventTypeAutorunbookUpdate, audit.EventActionUpdate, audit.EventStatusSuccess,
		id, prevSnap, workflowAuditSnapshot(&updated),
		map[string]any{"action": "edit"},
	)
	return updated, nil
}

// updateWorkflowInternal is the shared update path for user saves and version
// restores. It only mutates the draft (workflows.definition) — no version row
// is written here. Publishing is a separate, explicit action (see
// PublishWorkflow / Service.SetLiveWorkflowVersion).
func (s *Service) updateWorkflowInternal(ctx *security.RequestContext, accountId, id string, workflow model.Workflow) (model.Workflow, error) {
	if accountId == "" || id == "" {
		return model.Workflow{}, fmt.Errorf("tenantId, accountId, id are required")
	}
	if !ctx.GetSecurityContext().HasAccountAccess(accountId, security.SecurityAccessTypeUpdate) {
		return model.Workflow{}, common.ErrorUnauthorized("account not accessible")
	}

	workflow.UpdatedBy = ctx.GetSecurityContext().GetUserId()

	// Check if workflow exists
	existingWf, err := s.store.Find(ctx.GetContext(), ctx.GetSecurityContext().GetTenantId(), accountId, id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return model.Workflow{}, fmt.Errorf("workflow with ID %s not found: %w", id, sql.ErrNoRows)
		}
		return model.Workflow{}, err
	}

	// Preserve Status if not provided or validate if provided
	if workflow.Status == "" {
		workflow.Status = existingWf.Status
	} else {
		validStatuses := []model.WorkflowStatus{model.WorkflowStatusActive, model.WorkflowStatusPaused, model.WorkflowStatusInactive}
		isValidStatus := false
		for _, status := range validStatuses {
			if workflow.Status == status {
				isValidStatus = true
				break
			}
		}
		if !isValidStatus {
			return model.Workflow{}, fmt.Errorf("invalid workflow status: %s", workflow.Status)
		}
	}
	// Always preserve LastExecutionStatus (system managed)
	workflow.LastExecutionStatus = existingWf.LastExecutionStatus

	if workflow.Definition.Version == "" {
		workflow.Definition.Version = "v1"
	}

	normalizeWebhookTriggers(id, &workflow)
	normalizeWebhookTriggers(id, existingWf) // Normalize existing to facilitate matching

	if err := enforceWebhookSecrets(&workflow, existingWf, id); err != nil {
		return model.Workflow{}, err
	}

	if err := s.ValidateWorkflow(ctx, accountId, workflow); err != nil {
		return model.Workflow{}, err
	}

	// Validate schedule trigger inputs before saving
	for i, trigger := range workflow.Definition.Triggers {
		if trigger.Type == model.WorkflowTriggerSchedule {
			inputs := make(map[string]any)
			for k, v := range trigger.Params {
				if k != "cron" && k != "overlap_policy" && k != "catchup_window" {
					inputs[k] = v
				}
			}
			if err := s.validateWorkflowInputs(workflow.Definition.Inputs, inputs); err != nil {
				return model.Workflow{}, fmt.Errorf("invalid inputs for schedule trigger index %d: %w", i, err)
			}
		}
	}

	err = s.store.Update(ctx.GetContext(), ctx.GetSecurityContext().GetTenantId(), accountId, id, workflow)
	if err != nil {
		return model.Workflow{}, err
	}

	// Cleanup removed webhook triggers
	newWebhookTriggers := make(map[string]bool)
	for _, t := range workflow.Definition.Triggers {
		if t.Type == model.WorkflowTriggerWebhook {
			if t.Internal != nil && t.Internal.Name != "" {
				newWebhookTriggers[t.Internal.Name] = true
			}
		}
	}

	autoManagedPrefix := fmt.Sprintf("wf-%s-", id)
	for _, t := range existingWf.Definition.Triggers {
		if t.Type == model.WorkflowTriggerWebhook {
			if t.Internal == nil || t.Internal.Name == "" {
				// This should not happen if normalizeWebhookTriggers was called correctly.
				// If it happens, it indicates a workflow definition error or bug in normalization.
				slog.Warn("webhook trigger missing internal name in existing workflow during update cleanup", "workflowID", id)
				continue
			}
			integrationID := t.Internal.Name

			if !newWebhookTriggers[integrationID] {
				// Only delete auto-managed integrations (the wf-{id}- prefix
				// signals one this server created with the workflow). For
				// user-managed integrations (created via the Integrations
				// tab and shared with this workflow), leave them — the
				// user owns them and may reuse them elsewhere.
				if !strings.HasPrefix(integrationID, autoManagedPrefix) {
					continue
				}
				err := integrations.DeleteWorkflowWebhookTrigger(ctx, accountId, integrationID)
				if err != nil {
					// Log error but don't fail update (best effort cleanup)
					slog.Warn("failed to delete webhook trigger during update", "workflowID", id, "integrationID", integrationID, "error", err)
				}
			}
		}
	}

	workflow.TenantID = ctx.GetSecurityContext().GetTenantId()
	workflow.AccountID = accountId
	workflow.ID = id
	_, _, err = s.handleWorkflowTrigger(ctx, id, ctx.GetSecurityContext().GetTenantId(), accountId, &workflow)
	if err != nil {
		return workflow, err
	}

	if workflow.Tags == nil {
		workflow.Tags = make(map[string]any)
	}

	// Ensure slices within WorkflowDefinition are not nil
	if workflow.Definition.Inputs == nil {
		workflow.Definition.Inputs = []model.Input{}
	}
	if workflow.Definition.Triggers == nil {
		workflow.Definition.Triggers = []model.Trigger{}
	}
	if workflow.Definition.Tasks == nil {
		workflow.Definition.Tasks = []model.Task{}
	}

	if workflow.Definition.Version == "" {
		workflow.Definition.Version = "v1"
	}

	// Audit emit is the caller's responsibility — UpdateWorkflow tags it as a
	// user "edit", RestoreWorkflowVersion tags it as a "restore_version", and
	// any future caller (e.g. a system-driven status flip) tags its own intent.
	// Emitting here would double-fire on restores and lose the action context
	// every other caller needs.
	return workflow, nil
}

// ListWorkflowCallers returns workflows in the same account whose definition
// contains a `core.call-workflow` task referencing the given workflow's name.
// Used by the UI as a "where used" pre-check before deleting or renaming so
// the user sees which callers will silently break.
//
// Permission model matches GetWorkflow (read): a caller who can see the target
// workflow can see what references it; we don't expose any names they couldn't
// already enumerate via workflow_list.
//
// Caveats: templated `workflow_name` values (`{{ ... }}`) can't be resolved
// statically and are excluded — UI should note that the list is best-effort.
func (s *Service) ListWorkflowCallers(ctx *security.RequestContext, accountId, id string) (model.ListWorkflowCallersResponse, error) {
	if !ctx.GetSecurityContext().HasAccountAccess(accountId, security.SecurityAccessTypeRead) {
		return model.ListWorkflowCallersResponse{}, common.ErrorUnauthorized("account not accessible")
	}
	tenantID := ctx.GetSecurityContext().GetTenantId()
	wf, err := s.store.Find(ctx.GetContext(), tenantID, accountId, id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return model.ListWorkflowCallersResponse{}, fmt.Errorf("workflow with ID %s not found: %w", id, sql.ErrNoRows)
		}
		return model.ListWorkflowCallersResponse{}, fmt.Errorf("failed to retrieve workflow: %w", err)
	}
	callers, err := s.store.ListCallers(ctx.GetContext(), tenantID, accountId, wf.Name)
	if err != nil {
		return model.ListWorkflowCallersResponse{}, fmt.Errorf("failed to list callers: %w", err)
	}
	// Exclude self in case the target name happens to match its own
	// core.call-workflow reference (legal in templated chains but never useful
	// to surface in the warning).
	// Allocate as `[]WorkflowCaller{}` (not `nil`) so JSON marshals to `[]`
	// rather than `null`. The GraphQL schema declares callers non-nullable
	// (`[WorkflowCaller!]!`); returning null would surface as an execution
	// error to the client even though "no callers" is the expected happy path.
	filtered := []model.WorkflowCaller{}
	for _, c := range callers {
		if c.ID == id {
			continue
		}
		filtered = append(filtered, c)
	}
	return model.ListWorkflowCallersResponse{Callers: filtered}, nil
}

func (s *Service) DeleteWorkflow(ctx *security.RequestContext, accountId, id string) error {
	if !ctx.GetSecurityContext().HasAccountAccess(accountId, security.SecurityAccessTypeDelete) {
		return common.ErrorUnauthorized("account not accessible")
	}
	// Retrieve the workflow to check for schedule triggers
	wf, err := s.store.Find(ctx.GetContext(), ctx.GetSecurityContext().GetTenantId(), accountId, id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("workflow with ID %s not found: %w", id, sql.ErrNoRows)
		}
		return fmt.Errorf("failed to retrieve workflow: %w", err)
	}

	normalizeWebhookTriggers(id, wf)

	autoManagedPrefix := fmt.Sprintf("wf-%s-", id)
	for _, trigger := range wf.Definition.Triggers {
		switch trigger.Type {
		case model.WorkflowTriggerWebhook:
			if trigger.Internal == nil || trigger.Internal.Name == "" {
				// This should not happen if normalizeWebhookTriggers was called correctly.
				// If it happens, it indicates a workflow definition error or bug in normalization.
				slog.Warn("webhook trigger missing internal name in workflow during deletion", "workflowID", id)
				continue
			}
			integrationID := trigger.Internal.Name

			// Only delete auto-managed integrations (wf-{id}- prefix);
			// leave user-managed shared webhooks intact.
			if !strings.HasPrefix(integrationID, autoManagedPrefix) {
				continue
			}
			err := integrations.DeleteWorkflowWebhookTrigger(ctx, accountId, integrationID)
			if err != nil {
				slog.Warn("failed to delete webhook trigger during deletion", "workflowID", id, "integrationID", integrationID, "error", err)
			}
		}
	}

	// Delete all associated schedules
	scheduleClient := s.temporalClient.ScheduleClient()

	// Try to delete legacy
	legacyScheduleID := "workflow-schedule-" + id
	if _, err := scheduleClient.GetHandle(ctx.GetContext(), legacyScheduleID).Describe(ctx.GetContext()); err == nil {
		_ = scheduleClient.GetHandle(ctx.GetContext(), legacyScheduleID).Delete(ctx.GetContext())
	}

	// List ALL schedules for this workflow using Search Attribute and delete them.
	// Query: "nb_workflow_id = '<id>'"
	listView, err := scheduleClient.List(ctx.GetContext(), client.ScheduleListOptions{
		Query: fmt.Sprintf("%s = '%s'", model.SearchAttrWorkflowID, id),
	})
	if err != nil {
		slog.Error("failed to list schedules for deletion cleanup", "workflowID", id, "error", err)
		// Fallback to sequential cleanup if list fails
		for i := 0; ; i++ {
			sid := fmt.Sprintf("workflow-schedule-%s-%d", id, i)
			handle := scheduleClient.GetHandle(ctx.GetContext(), sid)
			if _, err := handle.Describe(ctx.GetContext()); err != nil {
				break
			}
			_ = handle.Delete(ctx.GetContext())
		}
	} else {
		for listView.HasNext() {
			sEntry, err := listView.Next()
			if err != nil {
				slog.Error("failed to iterate schedule list during deletion", "error", err)
				break
			}
			// Double check if this schedule belongs to us (redundant if query works, but safe)
			if strings.HasPrefix(sEntry.ID, "workflow-schedule-"+id) {
				_ = scheduleClient.GetHandle(ctx.GetContext(), sEntry.ID).Delete(ctx.GetContext())
			}
		}
	}

	// Delete workflow from database
	err = s.store.Delete(ctx.GetContext(), ctx.GetSecurityContext().GetTenantId(), accountId, id)
	if err != nil {
		return fmt.Errorf("failed to delete workflow from database: %w", err)
	}

	// Terminate any running workflow executions
	listExecutionsReq := model.ListWorkflowExecutionRequest{
		Limit: 100, // Adjust limit as needed
	}
	executionsResp, err := s.ListWorkflowExecutions(ctx, accountId, id, listExecutionsReq)
	if err != nil {
		return fmt.Errorf("failed to list workflow executions for termination: %w", err)
	}

	for _, exec := range executionsResp.Executions {
		// Only terminate if the workflow is still running
		if exec.Status == model.WorkflowExecutionStatusRunning {
			err = s.temporalClient.TerminateWorkflow(ctx.GetContext(), exec.TemporalWorkflowID, exec.ID, "Workflow deleted by user")
			if err != nil {
				// Log the error but continue with other terminations
				fmt.Printf("Failed to terminate workflow execution %s (run %s): %v\n", exec.TemporalWorkflowID, exec.ID, err)
			}
		}
	}

	emitWorkflowAudit(
		ctx, accountId,
		audit.EventTypeAutorunbookDelete, audit.EventActionDelete, audit.EventStatusSuccess,
		id, workflowAuditSnapshot(wf), nil, nil,
	)
	return nil
}

func (s *Service) UpdateWorkflowStatus(ctx *security.RequestContext, accountId, id string, status model.WorkflowStatus) error {
	if !ctx.GetSecurityContext().HasAccountAccess(accountId, security.SecurityAccessTypeUpdate) {
		return common.ErrorUnauthorized("account not accessible")
	}

	wf, err := s.store.Find(ctx.GetContext(), ctx.GetSecurityContext().GetTenantId(), accountId, id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil // If the workflow doesn't exist, there's nothing to update.
		}
		return fmt.Errorf("failed to find workflow to update status: %w", err)
	}

	// Handle side-effects based on the new status
	switch status {
	case model.WorkflowStatusInactive:
		// Update workflow status to target
		wf.Status = status
		// Re-run handleWorkflowTrigger to cleanup schedules
		// We can set Status to target, pass it to handleTrigger.
		// handleWorkflowTrigger checks status and cleans up schedules if inactive.
		_, _, err := s.handleWorkflowTrigger(ctx, id, ctx.GetSecurityContext().GetTenantId(), accountId, wf)
		if err != nil {
			return fmt.Errorf("failed to cleanup schedules for inactive workflow: %w", err)
		}
	case model.WorkflowStatusActive:
		// When reactivating, recreate the schedule if it has a schedule trigger.
		wf.Status = status
		_, _, err := s.handleWorkflowTrigger(ctx, id, ctx.GetSecurityContext().GetTenantId(), accountId, wf)
		if err != nil {
			return fmt.Errorf("failed to reactivate workflow trigger: %w", err)
		}
	case model.WorkflowStatusPaused:
		// Guide the user to use PauseWorkflow instead of directly updating status to Paused.
		// PauseWorkflow handles both Temporal scheduling actions and DB status updates.
		return fmt.Errorf("use PauseWorkflow to set workflow status to Paused")
	}

	// Finally, update the status in the database.
	if err := s.store.UpdateWorkflowStatus(ctx.GetContext(), ctx.GetSecurityContext().GetTenantId(), accountId, id, status); err != nil {
		return err
	}
	emitWorkflowAudit(
		ctx, accountId,
		audit.EventTypeAutorunbookUpdate, audit.EventActionUpdate, audit.EventStatusSuccess,
		id, map[string]any{"status": string(wf.Status)}, map[string]any{"status": string(status)},
		map[string]any{"action": "status_update"},
	)
	return nil
}

func mapTemporalStatusToModelStatus(status enums.WorkflowExecutionStatus) model.WorkflowExecutionStatus {
	switch status {
	case enums.WORKFLOW_EXECUTION_STATUS_RUNNING:
		return model.WorkflowExecutionStatusRunning
	case enums.WORKFLOW_EXECUTION_STATUS_COMPLETED:
		return model.WorkflowExecutionStatusCompleted
	case enums.WORKFLOW_EXECUTION_STATUS_FAILED:
		return model.WorkflowExecutionStatusFailed
	case enums.WORKFLOW_EXECUTION_STATUS_CANCELED:
		return model.WorkflowExecutionStatusCanceled
	case enums.WORKFLOW_EXECUTION_STATUS_TERMINATED:
		return model.WorkflowExecutionStatusTerminated
	case enums.WORKFLOW_EXECUTION_STATUS_CONTINUED_AS_NEW:
		return model.WorkflowExecutionStatusContinuedAsNew
	case enums.WORKFLOW_EXECUTION_STATUS_TIMED_OUT:
		return model.WorkflowExecutionStatusTimedOut
	default:
		return model.WorkflowExecutionStatusUnspecified
	}
}

func timestampPBToTimestamp(timePb *timestamppb.Timestamp) *time.Time {
	var timeNative *time.Time
	if timePb != nil {
		startTime2 := timePb.AsTime()
		timeNative = &startTime2
	}
	return timeNative
}

func (s *Service) ListWorkflowExecutions(ctx *security.RequestContext, accountId, workflowId string, request model.ListWorkflowExecutionRequest) (model.ListWorkflowExecutionResponse, error) {
	if accountId == "" || workflowId == "" {
		return model.ListWorkflowExecutionResponse{}, fmt.Errorf("tenantId, accountId, and workflowId are required")
	}
	if !ctx.GetSecurityContext().HasAccountAccess(accountId, security.SecurityAccessTypeRead) {
		return model.ListWorkflowExecutionResponse{}, common.ErrorUnauthorized("account not accessible")
	}

	if request.Limit <= 0 {
		request.Limit = 50
	}

	var conditions []string
	conditions = append(conditions, fmt.Sprintf("%s='%s'", model.SearchAttrWorkflowID, workflowId))
	conditions = append(conditions, fmt.Sprintf("%s='%s'", model.SearchAttrTenantID, ctx.GetSecurityContext().GetTenantId()))
	conditions = append(conditions, fmt.Sprintf("%s='%s'", model.SearchAttrAccountID, accountId))

	if request.Status != "" && request.Status != model.WorkflowExecutionStatusUnspecified {
		// Temporal status values are case-sensitive in some contexts, but usually uppercase in query.
		// Map model status to Temporal status string if needed, or rely on compatible string values.
		// Model: "RUNNING", Temporal: "Running" or "RUNNING"?
		// Temporal Visibility Query uses "ExecutionStatus = 'Running'".
		// We need to map our UPPERCASE status to TitleCase for Temporal if that's what it expects,
		// or check if Temporal accepts uppercase. Temporal is case-sensitive for ExecutionStatus values.
		// Valid values: Running, Completed, Failed, Canceled, Terminated, ContinuedAsNew, TimedOut.
		temporalStatus := ""
		switch request.Status {
		case model.WorkflowExecutionStatusRunning:
			temporalStatus = "Running"
		case model.WorkflowExecutionStatusCompleted:
			temporalStatus = "Completed"
		case model.WorkflowExecutionStatusFailed:
			temporalStatus = "Failed"
		case model.WorkflowExecutionStatusCanceled:
			temporalStatus = "Canceled"
		case model.WorkflowExecutionStatusTerminated:
			temporalStatus = "Terminated"
		case model.WorkflowExecutionStatusTimedOut:
			temporalStatus = "TimedOut"
		case model.WorkflowExecutionStatusContinuedAsNew:
			temporalStatus = "ContinuedAsNew"
		}
		if temporalStatus != "" {
			conditions = append(conditions, fmt.Sprintf("ExecutionStatus='%s'", temporalStatus))
		}
	}

	if request.TriggerType != "" {
		conditions = append(conditions, fmt.Sprintf("%s='%s'", model.SearchAttrWorkflowTrigger, escapeTemporalString(request.TriggerType)))
	}

	if request.TriggeredBy != "" {
		conditions = append(conditions, fmt.Sprintf("%s='%s'", model.SearchAttrTriggeredBy, escapeTemporalString(request.TriggeredBy)))
	}

	if len(request.Tags) > 0 {
		for k, v := range request.Tags {
			// Assuming tags are stored as "key:value"
			tagStr := fmt.Sprintf("%s:%s", escapeTemporalString(k), escapeTemporalString(v))
			conditions = append(conditions, fmt.Sprintf("%s='%s'", model.SearchAttrExecutionTags, tagStr))
		}
	}

	query := strings.Join(conditions, " and ")

	if request.OrderBy != "" {
		orderDir := "DESC" // Default
		if strings.EqualFold(request.OrderDir, "ASC") {
			orderDir = "ASC"
		}
		// Whitelist allowed sort keys
		switch request.OrderBy {
		case "StartTime", "CloseTime":
			query += fmt.Sprintf(" ORDER BY %s %s", request.OrderBy, orderDir)
		}
	}

	var nextPageToken []byte
	if request.NextPageToken != "" {
		nextPageToken = []byte(request.NextPageToken)
	}

	resp, err := s.temporalClient.ListWorkflow(ctx.GetContext(), &workflowservice.ListWorkflowExecutionsRequest{
		Query:         query,
		PageSize:      int32(request.Limit),
		NextPageToken: nextPageToken,
	})
	if err != nil {
		return model.ListWorkflowExecutionResponse{}, err
	}

	summaries := make([]model.WorkflowExecutionSummary, 0, len(resp.Executions))
	for _, info := range resp.Executions {
		summary := model.WorkflowExecutionSummary{
			WorkflowID:         workflowId,
			TemporalWorkflowID: info.Execution.GetWorkflowId(),
			ID:                 info.Execution.GetRunId(),
			Status:             mapTemporalStatusToModelStatus(info.GetStatus()),
			StartTime:          timestampPBToTimestamp(info.StartTime),
			CloseTime:          timestampPBToTimestamp(info.CloseTime),
		}

		// Extract TriggeredBy, TriggerType, and ParentWorkflowID from Search Attributes
		if info.SearchAttributes != nil {
			if sa, ok := info.SearchAttributes.GetIndexedFields()[model.SearchAttrTriggeredBy]; ok {
				var triggeredBy string
				if err := s.dataConverter.FromPayload(sa, &triggeredBy); err == nil {
					summary.TriggeredBy = triggeredBy
				}
			}
			if sa, ok := info.SearchAttributes.GetIndexedFields()[model.SearchAttrWorkflowTrigger]; ok {
				var triggerType string
				if err := s.dataConverter.FromPayload(sa, &triggerType); err == nil {
					summary.TriggerType = triggerType
				}
			}
			// Check both "parent_workflow_id" (legacy/incorrect) and "nb_parent_workflow_id" (new constant)
			// to be safe during migration or if existing data has mixed keys.
			// Prefer the constant one.
			if sa, ok := info.SearchAttributes.GetIndexedFields()[model.SearchAttrParentWorkflowID]; ok {
				var parentWorkflowID string
				if err := s.dataConverter.FromPayload(sa, &parentWorkflowID); err == nil {
					summary.ParentWorkflowID = parentWorkflowID
				}
			} else if sa, ok := info.SearchAttributes.GetIndexedFields()["parent_workflow_id"]; ok {
				var parentWorkflowID string
				if err := s.dataConverter.FromPayload(sa, &parentWorkflowID); err == nil {
					summary.ParentWorkflowID = parentWorkflowID
				}
			}
		}

		summaries = append(summaries, summary)
	}

	if summaries == nil {
		summaries = []model.WorkflowExecutionSummary{}
	}

	return model.ListWorkflowExecutionResponse{
		Executions:    summaries,
		NextPageToken: string(resp.NextPageToken),
	}, nil
}

func (s *Service) ListWorkflowExecutionsForEvent(ctx *security.RequestContext, accountId, eventId string) (model.ListWorkflowExecutionResponse, error) {
	if accountId == "" || eventId == "" {
		return model.ListWorkflowExecutionResponse{}, fmt.Errorf("accountId and eventId are required")
	}
	if !ctx.GetSecurityContext().HasAccountAccess(accountId, security.SecurityAccessTypeRead) {
		return model.ListWorkflowExecutionResponse{}, common.ErrorUnauthorized("account not accessible")
	}

	tenantID := ctx.GetSecurityContext().GetTenantId()
	if tenantID == "" {
		return model.ListWorkflowExecutionResponse{}, fmt.Errorf("tenantID is required")
	}
	query := fmt.Sprintf("%s='%s' and %s='%s' and %s='%s'",
		model.SearchAttrTenantID, escapeTemporalString(tenantID),
		model.SearchAttrAccountID, escapeTemporalString(accountId),
		model.SearchAttrEventID, escapeTemporalString(eventId))

	resp, err := s.temporalClient.ListWorkflow(ctx.GetContext(), &workflowservice.ListWorkflowExecutionsRequest{
		Query:    query,
		PageSize: 50,
	})
	if err != nil {
		return model.ListWorkflowExecutionResponse{}, err
	}

	summaries := make([]model.WorkflowExecutionSummary, 0, len(resp.Executions))
	idSet := map[string]struct{}{}
	for _, info := range resp.Executions {
		summary := model.WorkflowExecutionSummary{
			TemporalWorkflowID: info.Execution.GetWorkflowId(),
			ID:                 info.Execution.GetRunId(),
			Status:             mapTemporalStatusToModelStatus(info.GetStatus()),
			StartTime:          timestampPBToTimestamp(info.StartTime),
			CloseTime:          timestampPBToTimestamp(info.CloseTime),
		}
		if info.SearchAttributes != nil {
			if sa, ok := info.SearchAttributes.GetIndexedFields()[model.SearchAttrWorkflowID]; ok {
				var wfID string
				if err := s.dataConverter.FromPayload(sa, &wfID); err == nil {
					summary.WorkflowID = wfID
					if wfID != "" {
						idSet[wfID] = struct{}{}
					}
				}
			}
			if sa, ok := info.SearchAttributes.GetIndexedFields()[model.SearchAttrTriggeredBy]; ok {
				var triggeredBy string
				if err := s.dataConverter.FromPayload(sa, &triggeredBy); err == nil {
					summary.TriggeredBy = triggeredBy
				}
			}
			if sa, ok := info.SearchAttributes.GetIndexedFields()[model.SearchAttrWorkflowTrigger]; ok {
				var triggerType string
				if err := s.dataConverter.FromPayload(sa, &triggerType); err == nil {
					summary.TriggerType = triggerType
				}
			}
		}
		summaries = append(summaries, summary)
	}

	if len(idSet) > 0 {
		ids := make([]string, 0, len(idSet))
		for id := range idSet {
			ids = append(ids, id)
		}
		names, nameErr := s.store.GetWorkflowNames(ctx.GetContext(), tenantID, accountId, ids)
		if nameErr != nil {
			ctx.GetLogger().Error("failed to get workflow names for executions", "error", nameErr)
		} else {
			for i := range summaries {
				if name, ok := names[summaries[i].WorkflowID]; ok {
					summaries[i].WorkflowName = name
				}
			}
		}
	}

	return model.ListWorkflowExecutionResponse{
		Executions:    summaries,
		NextPageToken: string(resp.NextPageToken),
	}, nil
}

func (s *Service) GetDetailedWorkflowExecution(ctx *security.RequestContext, accountId, workflowId, executionId string) (*model.WorkflowExecutionDetails, error) {
	logger := ctx.GetLogger()
	if accountId == "" || workflowId == "" || executionId == "" {
		return nil, fmt.Errorf("tenantId, accountId, workflowId, executionId are required")
	}
	if !ctx.GetSecurityContext().HasAccountAccess(accountId, security.SecurityAccessTypeRead) {
		return nil, common.ErrorUnauthorized("account not accessible")
	}

	temporalId, err := s.ResolveTemporalWorkflowID(ctx, accountId, workflowId, executionId)
	if err != nil {
		return nil, err
	}

	// 1. Get high-level workflow execution info
	describeResp, err := s.temporalClient.DescribeWorkflowExecution(ctx.GetContext(), temporalId, executionId)
	if err != nil {
		// If not found, try to find the correct Workflow ID using Visibility (ListWorkflow) by RunID
		// This handles cases like Scheduled workflows where the Temporal Workflow ID differs from the Runbook Workflow ID
		var notFound *serviceerror.NotFound
		if errors.As(err, &notFound) {
			listResp, listErr := s.temporalClient.ListWorkflow(ctx.GetContext(), &workflowservice.ListWorkflowExecutionsRequest{
				Query: fmt.Sprintf("RunId = '%s'", executionId),
			})
			if listErr == nil && len(listResp.Executions) > 0 {
				// Use the found Temporal Workflow ID
				workflowId = listResp.Executions[0].Execution.GetWorkflowId()
				// Retry Describe
				describeResp, err = s.temporalClient.DescribeWorkflowExecution(ctx.GetContext(), temporalId, executionId)
			}
		}
		if err != nil {
			return nil, fmt.Errorf("failed to describe workflow execution: %w", err)
		}
	}

	info := describeResp.WorkflowExecutionInfo
	dbWorkflowId := workflowId // Assume it's a parent by default

	// Try to get definition ID from Search Attributes (Preferred)
	if info.SearchAttributes != nil {
		if sa, ok := info.SearchAttributes.GetIndexedFields()[model.SearchAttrWorkflowID]; ok {
			var saWorkflowID string
			if err := s.dataConverter.FromPayload(sa, &saWorkflowID); err == nil && saWorkflowID != "" {
				dbWorkflowId = saWorkflowID
			}
		}
	}

	// If it's a child, its memo will contain the definition ID
	if info.Memo != nil {
		if memo, ok := info.Memo.GetFields()["child_definition_id"]; ok {
			var childDefinitionID string
			if err := s.dataConverter.FromPayload(memo, &childDefinitionID); err == nil {
				dbWorkflowId = childDefinitionID
			}
		}
	}

	// Fallback: If still using composite ID (contains slash), it means we failed to resolve the definition ID from Memo.
	if strings.Contains(dbWorkflowId, "/") && !strings.HasPrefix(dbWorkflowId, "inline-") {
		return nil, fmt.Errorf("failed to resolve workflow definition ID for execution %s: 'child_definition_id' missing from memo and ID contains '/'", workflowId)
	}

	// 2. Fetch the workflow definition
	var wf *model.Workflow
	var findErr error

	isNonPersistedWorkflow := strings.HasPrefix(dbWorkflowId, "inline-") || strings.HasPrefix(dbWorkflowId, "dry-run-")
	if isNonPersistedWorkflow {
		// Inline and dry-run workflows are not persisted in DB.
		// Set error to sql.ErrNoRows to trigger the history reconstruction logic below.
		findErr = sql.ErrNoRows
	} else {
		wf, findErr = s.store.Find(ctx.GetContext(), ctx.GetSecurityContext().GetTenantId(), accountId, dbWorkflowId)
	}

	// If not found in DB and it's a non-persisted workflow, try to reconstruct from history
	if errors.Is(findErr, sql.ErrNoRows) && isNonPersistedWorkflow {
		// Create a new history iterator to get the STARTED event input
		startHistoryIterator := s.temporalClient.GetWorkflowHistory(ctx.GetContext(), temporalId, executionId, false, enums.HISTORY_EVENT_FILTER_TYPE_ALL_EVENT)
		foundStartEvent := false
		for startHistoryIterator.HasNext() {
			event, iterErr := startHistoryIterator.Next()
			if iterErr != nil {
				return nil, fmt.Errorf("failed to read history for non-persisted workflow: %w", iterErr)
			}
			if event.GetEventType() == enums.EVENT_TYPE_WORKFLOW_EXECUTION_STARTED {
				attrs := event.GetWorkflowExecutionStartedEventAttributes()
				if attrs.GetInput() != nil && len(attrs.GetInput().GetPayloads()) > 0 {
					var syntheticWf model.Workflow
					if dcErr := s.dataConverter.FromPayload(attrs.GetInput().GetPayloads()[0], &syntheticWf); dcErr == nil {
						wf = &syntheticWf
						foundStartEvent = true
						break
					} else {
						logger.Error("Failed to deserialize workflow input from history", "error", dcErr)
					}
				}
			}
		}
		if !foundStartEvent || wf == nil {
			return nil, fmt.Errorf("workflow definition %s not found in history input", dbWorkflowId)
		}
		findErr = nil // Clear findErr as we successfully found the definition in history
	}

	if findErr != nil {
		if errors.Is(findErr, sql.ErrNoRows) {
			return nil, fmt.Errorf("workflow with ID %s not found: %w", dbWorkflowId, sql.ErrNoRows)
		}
		return nil, fmt.Errorf("failed to retrieve workflow definition: %w", findErr)
	}

	workflowDetails := &model.WorkflowExecutionDetails{
		WorkflowId:       workflowId,
		Id:               info.Execution.GetRunId(),
		Status:           mapTemporalStatusToModelStatus(info.GetStatus()),
		StartTime:        timestampPBToTimestamp(info.GetStartTime()),
		CloseTime:        timestampPBToTimestamp(info.GetCloseTime()),
		HistoryLength:    info.GetHistoryLength(),
		TaskQueue:        info.GetTaskQueue(),
		HistorySizeBytes: info.GetHistorySizeBytes(),
	}

	// Extract Memo and harvest the workflow-version linkage keys (stamped by
	// ExecuteWorkflow). Missing keys -> legacy execution from before V736.
	var versionIDFromMemo string
	var isDraftRun bool
	if info.Memo != nil {
		memoMap := make(map[string]any)
		for k, v := range info.Memo.GetFields() {
			var val any
			if err := s.dataConverter.FromPayload(v, &val); err != nil {
				fmt.Printf("Failed to deserialize memo field %s: %v\n", k, err)
				continue
			}
			memoMap[k] = val
			switch k {
			case model.MemoWorkflowVersionID:
				if vid, ok := val.(string); ok && vid != "" {
					versionIDFromMemo = vid
					workflowDetails.VersionID = &vid
				}
			case model.MemoWorkflowVersionNumber:
				if vn, ok := toInt(val); ok {
					workflowDetails.VersionNumber = &vn
				}
			case model.MemoWorkflowVersionName:
				if vn, ok := val.(string); ok && vn != "" {
					workflowDetails.VersionName = &vn
				}
			case model.MemoWorkflowIsDraftRun:
				if b, ok := val.(bool); ok {
					isDraftRun = b
				}
			}
		}
		workflowDetails.Memo = memoMap
	}

	// Prefer the version snapshot for definition display so the execution
	// canvas shows what actually ran (not the current draft). Fall back to
	// the live workflow row when the version row was pruned away or the
	// execution predates Memo stamping ("live_fallback" — UI should warn).
	if versionIDFromMemo != "" {
		if v, vErr := s.store.GetWorkflowVersionByID(ctx.GetContext(), versionIDFromMemo); vErr == nil && v != nil {
			snapshotCopy := v.Definition
			workflowDetails.Definition = &snapshotCopy
			workflowDetails.DefinitionSource = "version"
		}
	}
	if workflowDetails.DefinitionSource == "" && !isNonPersistedWorkflow && !isDraftRun {
		// Memo missing or version row pruned. Prefer the current live
		// version snapshot — for pre-V741 executions, V741's backfill
		// makes live == what ran at backfill time, which is a much closer
		// proxy than the current draft (which the user may have edited).
		// Only fall through to the draft if even the live snapshot is
		// unavailable.
		if wf.LiveVersionID != nil && *wf.LiveVersionID != "" {
			if v, vErr := s.store.GetLiveWorkflowVersion(ctx.GetContext(), wf.ID); vErr == nil && v != nil {
				snapshotCopy := v.Definition
				workflowDetails.Definition = &snapshotCopy
				workflowDetails.DefinitionSource = "legacy_live_snapshot"
				vn := v.VersionNumber
				workflowDetails.VersionNumber = &vn
				if v.Name != nil {
					name := *v.Name
					workflowDetails.VersionName = &name
				}
			}
		}
		if workflowDetails.DefinitionSource == "" {
			liveCopy := wf.Definition
			workflowDetails.Definition = &liveCopy
			workflowDetails.DefinitionSource = "live_fallback"
		}
	}

	// Extract Search Attributes
	if info.SearchAttributes != nil {
		saMap := make(map[string]any)
		for k, v := range info.SearchAttributes.GetIndexedFields() {
			var val any
			if err := s.dataConverter.FromPayload(v, &val); err != nil {
				fmt.Printf("Failed to deserialize search attribute %s: %v\n", k, err)
				continue
			}
			saMap[k] = val
			if k == model.SearchAttrTriggeredBy {
				if triggeredBy, ok := val.(string); ok {
					workflowDetails.TriggeredBy = triggeredBy
				}
			}
			if k == model.SearchAttrParentWorkflowID || k == "parent_workflow_id" {
				if parentWorkflowID, ok := val.(string); ok {
					workflowDetails.ParentWorkflowID = parentWorkflowID
				}
			}
		}
		workflowDetails.SearchAttributes = saMap
	}

	// 2. Get workflow history and process it.
	// Feed processWorkflowHistory the snapshot definition we decided on above
	// (version preferred, draft fallback) so task reconstruction matches what
	// actually ran rather than current draft state.
	processingDef := wf.Definition
	if workflowDetails.Definition != nil {
		processingDef = *workflowDetails.Definition
	}
	historyIterator := s.temporalClient.GetWorkflowHistory(ctx.GetContext(), temporalId, executionId, false, enums.HISTORY_EVENT_FILTER_TYPE_ALL_EVENT)
	historyDetails, err := s.processWorkflowHistory(ctx, accountId, historyIterator, processingDef)
	if err != nil {
		return nil, err
	}

	workflowDetails.Inputs = historyDetails.Inputs
	workflowDetails.WorkflowResult = historyDetails.WorkflowResult
	workflowDetails.Error = historyDetails.Error
	workflowDetails.Tasks = historyDetails.Tasks
	// For non-persisted (inline/dry-run) workflows the history reconstruction
	// is the only source of truth for the definition itself — keep it.
	if isNonPersistedWorkflow && historyDetails.Definition != nil {
		workflowDetails.Definition = historyDetails.Definition
		workflowDetails.DefinitionSource = "history"
	}
	// Draft ("Run current") executions snapshot no version row — the exact graph
	// that ran lives only in Temporal history. Render that, tagged so the UI can
	// banner it as a draft run.
	if isDraftRun && historyDetails.Definition != nil {
		workflowDetails.Definition = historyDetails.Definition
		workflowDetails.DefinitionSource = "draft_run"
	}

	return workflowDetails, nil
}

// toInt coerces interface{} values from Temporal Memo deserialization into an
// int. The data converter may yield int64, float64, json.Number, etc.
// depending on payload encoding.
func toInt(v any) (int, bool) {
	switch x := v.(type) {
	case int:
		return x, true
	case int32:
		return int(x), true
	case int64:
		return int(x), true
	case float64:
		return int(x), true
	case float32:
		return int(x), true
	case json.Number:
		if n, err := x.Int64(); err == nil {
			return int(n), true
		}
	}
	return 0, false
}

// TODO: Implement update logic
// Can be status, inputs, etc.
func (s *Service) UpdateWorkflowExecution(ctx *security.RequestContext, accountId, workflowId, executionId string, inputs map[string]any) error {
	if accountId == "" || workflowId == "" || executionId == "" {
		return fmt.Errorf("tenantId, accountId, workflowId, executionId are required")
	}

	signal := model.UpdateWorkflowExecutionSignal{
		Inputs: inputs,
	}

	temporalId, err := s.ResolveTemporalWorkflowID(ctx, accountId, workflowId, executionId)
	if err != nil {
		return err
	}

	return s.temporalClient.SignalWorkflow(ctx.GetContext(), temporalId, executionId, "update-workflow-inputs", signal)
}

func (s *Service) CompleteApprovalTask(ctx *security.RequestContext, token, status string, result any) error {
	var taskTokenStr string
	var accountID, workflowID, runID, taskID string

	// Parse compound token if present (hexToken:accountID:workflowID:runID:taskID)
	parts := strings.Split(token, ":")
	if len(parts) == 5 {
		taskTokenStr = parts[0]
		accountID = parts[1]
		workflowID = parts[2]
		runID = parts[3]
		taskID = parts[4]
	} else {
		taskTokenStr = token
	}

	tokenBytes, err := hex.DecodeString(taskTokenStr)
	if err != nil {
		return fmt.Errorf("invalid token format: %w", err)
	}

	// Validate status against approval_options if metadata is available
	if accountID != "" && workflowID != "" && runID != "" && taskID != "" {
		details, err := s.GetDetailedWorkflowExecution(ctx, accountID, workflowID, runID)
		if err == nil && details != nil {
			var targetTask *model.TaskExecutionDetails
			for i := range details.Tasks {
				if details.Tasks[i].ID == taskID {
					targetTask = &details.Tasks[i]
					break
				}
			}

			if targetTask != nil && targetTask.Input != nil {
				if opts, ok := targetTask.Input["approval_options"].([]any); ok && len(opts) > 0 {
					valid := false
					var validOptions []string
					for _, opt := range opts {
						optStr := fmt.Sprint(opt)
						validOptions = append(validOptions, optStr)
						if optStr == status {
							valid = true
							break
						}
					}
					if !valid {
						return common.ErrorBadRequest(fmt.Sprintf("invalid status '%s'. must be one of: %s", status, strings.Join(validOptions, ", ")))
					}
				}
			}
		} else if err != nil {
			slog.Warn("failed to fetch workflow details for approval validation", "workflowID", workflowID, "runID", runID, "error", err)
		}
	}

	// Construct the output map
	output := make(map[string]any)

	// Merge result if it's a map
	if result != nil {
		if m, ok := result.(map[string]any); ok {
			for k, v := range m {
				output[k] = v
			}
		} else if s, ok := result.(string); ok {
			// If result is a string, map it to 'comments' to preserve data and match schema expectations
			output["comments"] = s
		}
	}

	// Set/Overwrite mandatory fields
	output["status"] = status
	output["approver"] = ctx.GetSecurityContext().GetUserId()

	// Merge IM thread coordinates the activity stashed via heartbeat (#29110)
	// so downstream notifications.im tasks can reply in the same Slack thread
	// via {{ tasks.<approval_id>.im_message_id }}. Best-effort: a missing/decode
	// failure just leaves the fields absent and downstream messages post top-level.
	if workflowID != "" && runID != "" && taskID != "" {
		if imCtx, lookupErr := s.fetchApprovalIMContext(ctx.GetContext(), workflowID, runID, taskID); lookupErr == nil {
			for k, v := range imCtx {
				if v == "" {
					continue
				}
				if _, exists := output[k]; !exists {
					output[k] = v
				}
			}
		} else {
			slog.Warn("failed to fetch approval IM context", "workflowID", workflowID, "runID", runID, "taskID", taskID, "error", lookupErr)
		}
	}

	// Completing the activity successfully allows the workflow to proceed with the provided status.
	err = s.temporalClient.CompleteActivity(ctx.GetContext(), tokenBytes, output, nil)
	if err != nil {
		var notFound *serviceerror.NotFound
		if errors.As(err, &notFound) {
			return common.Error{Code: 409, Message: "approval has already been processed"}
		}
		return err
	}
	// Compound-token path (Slack / MS Teams approval URLs) carries the
	// workflow/execution/task triple. Plain hex-token path (len(parts) == 1)
	// has no audit context here — CompleteApprovalTaskFromUI emits for the
	// UI path with the fields it already has.
	if accountID != "" && workflowID != "" && runID != "" && taskID != "" {
		emitWorkflowAudit(
			ctx, accountID,
			audit.EventTypeAutorunbookTaskManualRun, audit.EventActionUpdate, audit.EventStatusSuccess,
			workflowID, nil,
			map[string]any{"workflow_id": workflowID, "execution_id": runID, "task_id": taskID, "status": status},
			map[string]any{"source": "compound_token"},
		)
	}
	return nil
}

// CompleteApprovalTaskFromUI completes a pending core.approval task without
// requiring the caller to know the compound approval token. The token is
// resolved by querying the running Temporal workflow's pendingApprovalTokens
// map (via the "getApprovalToken" query handler), and the rest of the flow
// (status validation against approval_options, output construction, IM
// context merge, activity completion) reuses CompleteApprovalTask so the UI
// and Slack/MS Teams paths cannot drift.
func (s *Service) CompleteApprovalTaskFromUI(ctx *security.RequestContext, accountID, workflowID, executionID, taskID, status, comments string) error {
	if accountID == "" || workflowID == "" || executionID == "" || taskID == "" {
		return common.ErrorBadRequest("account_id, workflow_id, execution_id, task_id are required")
	}
	if status == "" {
		return common.ErrorBadRequest("status is required")
	}

	if !ctx.GetSecurityContext().HasAccountAccess(accountID, security.SecurityAccessTypeUpdate) {
		return common.ErrorUnauthorized("account not accessible")
	}

	temporalID, err := s.ResolveTemporalWorkflowID(ctx, accountID, workflowID, executionID)
	if err != nil {
		return err
	}

	queryResp, err := s.temporalClient.QueryWorkflow(ctx.GetContext(), temporalID, executionID, "getApprovalToken", taskID)
	if err != nil {
		return common.ErrorBadRequest(fmt.Sprintf("failed to resolve approval token: %v", err))
	}
	var token string
	if err := queryResp.Get(&token); err != nil {
		return fmt.Errorf("decode approval token: %w", err)
	}
	if token == "" {
		return common.Error{Code: 409, Message: "no pending approval for this task"}
	}

	result := map[string]any{}
	if comments != "" {
		result["comments"] = comments
	}
	if err := s.CompleteApprovalTask(ctx, token, status, result); err != nil {
		return err
	}
	emitWorkflowAudit(
		ctx, accountID,
		audit.EventTypeAutorunbookTaskManualRun, audit.EventActionUpdate, audit.EventStatusSuccess,
		workflowID, nil,
		map[string]any{"workflow_id": workflowID, "execution_id": executionID, "task_id": taskID, "status": status},
		map[string]any{"source": "ui"},
	)
	return nil
}

// fetchApprovalIMContext reads the IM thread coordinates the approval activity
// stashed via heartbeat (see ApprovalTask.Execute). Returns nil map (and no
// error) when the activity didn't heartbeat — e.g. email approvals or sends
// that failed.
func (s *Service) fetchApprovalIMContext(ctx context.Context, workflowID, runID, taskID string) (map[string]string, error) {
	desc, err := s.temporalClient.DescribeWorkflowExecution(ctx, workflowID, runID)
	if err != nil {
		return nil, err
	}
	for _, pa := range desc.GetPendingActivities() {
		if pa.GetActivityId() != taskID {
			continue
		}
		details := pa.GetHeartbeatDetails()
		if details == nil {
			return nil, nil
		}
		var imCtx map[string]string
		if err := s.dataConverter.FromPayloads(details, &imCtx); err != nil {
			return nil, fmt.Errorf("decode heartbeat: %w", err)
		}
		return imCtx, nil
	}
	return nil, nil
}

func (s *Service) CancelWorkflowExecution(ctx *security.RequestContext, accountId, workflowId, executionId string) error {
	if !ctx.GetSecurityContext().HasAccountAccess(accountId, security.SecurityAccessTypeCreate) {
		return common.ErrorUnauthorized("account not accessible")
	}

	temporalId, err := s.ResolveTemporalWorkflowID(ctx, accountId, workflowId, executionId)
	if err != nil {
		return err
	}
	if err := s.temporalClient.CancelWorkflow(ctx.GetContext(), temporalId, executionId); err != nil {
		return err
	}
	emitWorkflowAudit(
		ctx, accountId,
		audit.EventTypeAutorunbookUpdate, audit.EventActionUpdate, audit.EventStatusSuccess,
		workflowId, nil,
		map[string]any{"workflow_id": workflowId, "execution_id": executionId},
		map[string]any{"action": "cancel_execution"},
	)
	return nil
}

func (s *Service) PauseWorkflow(ctx *security.RequestContext, accountId, id string) error {
	if !ctx.GetSecurityContext().HasAccountAccess(accountId, security.SecurityAccessTypeCreate) {
		return common.ErrorUnauthorized("account not accessible")
	}

	// Pause all schedules
	scheduleClient := s.temporalClient.ScheduleClient()

	// Legacy
	legacyScheduleID := "workflow-schedule-" + id
	if _, err := scheduleClient.GetHandle(ctx.GetContext(), legacyScheduleID).Describe(ctx.GetContext()); err == nil {
		_ = scheduleClient.GetHandle(ctx.GetContext(), legacyScheduleID).Pause(ctx.GetContext(), client.SchedulePauseOptions{Note: "Paused by user"})
	}

	// Indexed
	for i := 0; ; i++ {
		sid := fmt.Sprintf("workflow-schedule-%s-%d", id, i)
		handle := scheduleClient.GetHandle(ctx.GetContext(), sid)
		if _, err := handle.Describe(ctx.GetContext()); err != nil {
			break
		}
		if err := handle.Pause(ctx.GetContext(), client.SchedulePauseOptions{Note: "Paused by user"}); err != nil {
			// Log error
			slog.Error("failed to pause schedule", "scheduleID", sid, "error", err)
		}
	}

	if err := s.applyStatusToLiveVersion(ctx, accountId, id, model.WorkflowStatusPaused); err != nil {
		return err
	}
	emitWorkflowAudit(
		ctx, accountId,
		audit.EventTypeAutorunbookUpdate, audit.EventActionUpdate, audit.EventStatusSuccess,
		id, nil,
		map[string]any{"status": string(model.WorkflowStatusPaused)},
		map[string]any{"action": "pause"},
	)
	return nil
}

func (s *Service) ResumeWorkflow(ctx *security.RequestContext, accountId, id string) error {
	if !ctx.GetSecurityContext().HasAccountAccess(accountId, security.SecurityAccessTypeCreate) {
		return common.ErrorUnauthorized("account not accessible")
	}

	// Unpause all schedules
	scheduleClient := s.temporalClient.ScheduleClient()

	// Legacy
	legacyScheduleID := "workflow-schedule-" + id
	if _, err := scheduleClient.GetHandle(ctx.GetContext(), legacyScheduleID).Describe(ctx.GetContext()); err == nil {
		_ = scheduleClient.GetHandle(ctx.GetContext(), legacyScheduleID).Unpause(ctx.GetContext(), client.ScheduleUnpauseOptions{Note: "Resumed by user"})
	}

	// Indexed
	for i := 0; ; i++ {
		sid := fmt.Sprintf("workflow-schedule-%s-%d", id, i)
		handle := scheduleClient.GetHandle(ctx.GetContext(), sid)
		if _, err := handle.Describe(ctx.GetContext()); err != nil {
			break
		}
		if err := handle.Unpause(ctx.GetContext(), client.ScheduleUnpauseOptions{Note: "Resumed by user"}); err != nil {
			slog.Error("failed to unpause schedule", "scheduleID", sid, "error", err)
		}
	}

	if err := s.applyStatusToLiveVersion(ctx, accountId, id, model.WorkflowStatusActive); err != nil {
		return err
	}
	emitWorkflowAudit(
		ctx, accountId,
		audit.EventTypeAutorunbookUpdate, audit.EventActionUpdate, audit.EventStatusSuccess,
		id, nil,
		map[string]any{"status": string(model.WorkflowStatusActive)},
		map[string]any{"action": "resume"},
	)
	return nil
}

// applyStatusToLiveVersion is the canonical "set this workflow's status"
// helper post-V746. It writes status onto workflows.live_version row and the
// DAO mirrors it onto workflows.status in the same tx. Falls back to the
// pre-versioning UpdateWorkflowStatus path when a workflow somehow has no
// live version (defensive — CreateWorkflow always sets one). Both branches
// fail fast on missing access checks because callers already validated above.
func (s *Service) applyStatusToLiveVersion(ctx *security.RequestContext, accountId, id string, status model.WorkflowStatus) error {
	tenantId := ctx.GetSecurityContext().GetTenantId()
	wf, err := s.store.Find(ctx.GetContext(), tenantId, accountId, id)
	if err != nil {
		// Propagate sql.ErrNoRows up so the HTTP handler can return 404 instead
		// of silently 200ing a pause/resume call on a deleted or wrong-tenant
		// workflow. The legacy UpdateWorkflowStatus path swallowed ErrNoRows
		// for idempotency, but PauseWorkflow / ResumeWorkflow are explicit
		// state-machine transitions and a missing target is a client error.
		if errors.Is(err, sql.ErrNoRows) {
			return sql.ErrNoRows
		}
		return fmt.Errorf("failed to load workflow for status change: %w", err)
	}
	if wf.LiveVersionID == nil || *wf.LiveVersionID == "" {
		return s.store.UpdateWorkflowStatus(ctx.GetContext(), tenantId, accountId, id, status)
	}
	if _, err := s.store.UpdateVersionStatus(ctx.GetContext(), tenantId, accountId, id, *wf.LiveVersionID, status); err != nil {
		return fmt.Errorf("failed to update live version status: %w", err)
	}
	return nil
}

func convertSchemaPropertiesToMapAny(properties map[string]types.Property) map[string]any {
	result := make(map[string]any)
	for k, v := range properties {
		title := v.Title
		if title == "" {
			title = cases.Title(language.English, cases.NoLower).String(strings.ReplaceAll(k, "_", " "))
		}

		propMap := map[string]any{
			"type":         v.Type,
			"description":  v.Description,
			"required":     v.Required,
			"default":      v.Default,
			"title":        title,
			"order":        v.Order,
			"is_encrypted": v.IsEncrypted,
		}
		if v.Help != "" {
			propMap["help"] = v.Help
		}
		if v.Hidden {
			propMap["hidden"] = true
		}
		if v.SubType != "" {
			propMap["sub_type"] = v.SubType
		}
		if len(v.Options) > 0 {
			propMap["options"] = v.Options
		}
		if v.Schema != nil {
			propMap["schema"] = convertSchemaPropertiesToMapAny(v.Schema.Properties)
		}
		if len(v.DependsOn) > 0 {
			propMap["depends_on"] = v.DependsOn
		}
		if v.VisibleWhen != nil {
			propMap["visible_when"] = map[string]any{
				"field": v.VisibleWhen.Field,
				"value": v.VisibleWhen.Value,
			}
		}
		if v.RequiredWhen != nil {
			propMap["required_when"] = map[string]any{
				"field": v.RequiredWhen.Field,
				"value": v.RequiredWhen.Value,
			}
		}
		if v.OptionsSource != nil {
			osMap := map[string]any{
				"type": v.OptionsSource.Type,
			}
			if len(v.OptionsSource.DependencyMapping) > 0 {
				osMap["dependency_mapping"] = v.OptionsSource.DependencyMapping
			}
			propMap["options_source"] = osMap
		}
		if v.DynamicFieldsSource != nil {
			dfsMap := map[string]any{
				"type": v.DynamicFieldsSource.Type,
			}
			if len(v.DynamicFieldsSource.DependencyMapping) > 0 {
				dfsMap["dependency_mapping"] = v.DynamicFieldsSource.DependencyMapping
			}
			propMap["dynamic_fields_source"] = dfsMap
		}
		result[k] = propMap
	}
	return result
}

func (s *Service) ListAllTasks(ctx *security.RequestContext) model.ListTaskDefinitionResponse {
	taskDefs := make([]model.TaskDefinition, 0)
	for _, task := range s.taskRegistry.ListTasks() {
		var outputSchema map[string]any
		if task.OutputSchema() != nil {
			outputSchema = convertSchemaPropertiesToMapAny(task.OutputSchema().Properties)
		} else {
			outputSchema = make(map[string]any) // Initialize to empty map if nil
		}

		displayName := task.GetDisplayName()
		if displayName == "" {
			parts := strings.Split(task.GetName(), ".")
			if len(parts) > 0 {
				lastPart := parts[len(parts)-1]
				displayName = cases.Title(language.English, cases.NoLower).String(strings.ReplaceAll(lastPart, "_", " "))
			} else {
				displayName = task.GetName()
			}
		}

		var runtimeNotes []string
		if rn, ok := task.(types.RuntimeNotesProvider); ok {
			runtimeNotes = rn.RuntimeNotes()
		}

		taskDefs = append(taskDefs, model.TaskDefinition{
			Name:         task.GetName(),
			DisplayName:  displayName,
			Description:  task.GetDescription(),
			InputSchema:  convertSchemaPropertiesToMapAny(task.InputSchema().Properties),
			OutputSchema: outputSchema,
			RuntimeNotes: runtimeNotes,
			Aliases:      tasks.AliasesFor(task.GetName()),
		})
	}
	return model.ListTaskDefinitionResponse{
		Tasks: taskDefs,
	}
}

func (s *Service) ExecuteTask(ctx *security.RequestContext, accountId, taskType string, params map[string]any) (any, error) {
	if !ctx.GetSecurityContext().HasAccountAccess(accountId, security.SecurityAccessTypeCreate) {
		return nil, common.ErrorUnauthorized("account not accessible")
	}

	task, err := s.taskRegistry.GetTask(taskType)
	if err != nil {
		return nil, err
	}

	if schema := task.InputSchema(); schema != nil {
		if err := schema.Validate(params); err != nil {
			return nil, common.ErrorBadRequest(fmt.Sprintf("task '%s' parameters validation failed: %v", taskType, err))
		}
	}

	tenantId := ctx.GetSecurityContext().GetTenantId()

	// Fetch configs and secrets for template rendering
	var renderedParams map[string]any
	if s.configService != nil {
		configs, err := s.configService.ListConfigsDecrypted(ctx.GetContext(), tenantId, &accountId, nil)
		if err != nil {
			ctx.GetLogger().Warn("failed to fetch configs for task execution, proceeding without config context", "error", err)
			renderedParams = params
		} else {
			// Separate configs and secrets
			configMap := make(map[string]any)
			secretMap := make(map[string]any)
			for _, cfg := range configs {
				if cfg.Type == model.ConfigTypeSecret {
					secretMap[cfg.Key] = cfg.Value
				} else {
					configMap[cfg.Key] = cfg.Value
				}
			}

			// Create template context with configs and secrets
			templateContext := NewTemplateContext(nil, nil)
			templateContext.Configs = configMap
			templateContext.Secrets = secretMap

			// Render params using template context
			renderedValue, err := ProcessValue(params, templateContext)
			if err != nil {
				return nil, fmt.Errorf("failed to render task params: %w", err)
			}
			var ok bool
			renderedParams, ok = renderedValue.(map[string]any)
			if !ok {
				return nil, fmt.Errorf("rendered params is not a map")
			}
		}
	} else {
		renderedParams = params
	}

	// Create a test TaskContext for execution
	taskCtx := testutils.NewTestTaskContext(tenantId, accountId, ctx.GetSecurityContext().GetUserId(), ctx.GetLogger())
	return task.Execute(taskCtx, renderedParams)
}

func (s *Service) ListMCPTools(ctx *security.RequestContext, accountId string, params map[string]any) (any, error) {
	if !ctx.GetSecurityContext().HasAccountAccess(accountId, security.SecurityAccessTypeRead) {
		return nil, common.ErrorUnauthorized("account not accessible")
	}

	tenantId := ctx.GetSecurityContext().GetTenantId()
	taskCtx := testutils.NewTestTaskContext(tenantId, accountId, ctx.GetSecurityContext().GetUserId(), ctx.GetLogger())

	mcpTask := &aiTasks.MCPTask{}
	tools, err := mcpTask.ListTools(taskCtx, params)
	if err != nil {
		return nil, err
	}
	return map[string]any{"tools": tools}, nil
}

func (s *Service) ValidateWorkflow(ctx *security.RequestContext, accountId string, wf model.Workflow) error {
	if !ctx.GetSecurityContext().HasAccountAccess(accountId, security.SecurityAccessTypeCreate) {
		return common.ErrorUnauthorized("account not accessible")
	}

	if err := model.ValidateWorkflow(wf); err != nil {
		return err
	}

	// Validate DAG (cycles, missing dependencies, duplicate IDs, dependency completeness)
	if err := ValidateDAG(wf.Definition.Tasks); err != nil {
		return common.ErrorBadRequest(err.Error())
	}

	// Validate template syntax (parse all templates without evaluating them)
	if err := ValidateTemplateSyntax(wf.Definition.Tasks); err != nil {
		return common.ErrorBadRequest(err.Error())
	}

	return s.validateTaskTypes(ctx, accountId, wf)
}

func (s *Service) GetWorkflowState(ctx *security.RequestContext, accountId, id string) ([]model.WorkflowStateItem, error) {
	if !ctx.GetSecurityContext().HasAccountAccess(accountId, security.SecurityAccessTypeCreate) {
		return []model.WorkflowStateItem{}, common.ErrorUnauthorized("account not accessible")
	}
	// Validate workflow exists and belongs to account
	_, err := s.store.Find(ctx.GetContext(), ctx.GetSecurityContext().GetTenantId(), accountId, id)
	if err != nil {
		return nil, err
	}

	return s.store.GetState(ctx.GetContext(), id)
}

// resolveTemporalWorkflowID resolves the actual Temporal Workflow ID for a given runbook workflow definition ID and Temporal Run ID.
// This is needed because the Temporal Workflow ID can be dynamic (e.g., for webhooks/events) while the API might receive the
// static runbook workflow definition ID.
func (s *Service) ResolveTemporalWorkflowID(reqCtx *security.RequestContext, accountID, workflowDefinitionID, runID string) (string, error) {
	for range 3 {
		query := fmt.Sprintf("%s='%s' and RunId='%s' and %s='%s' and %s='%s'",
			model.SearchAttrWorkflowID, workflowDefinitionID, runID,
			model.SearchAttrTenantID, reqCtx.GetSecurityContext().GetTenantId(),
			model.SearchAttrAccountID, accountID)

		listResp, err := s.temporalClient.ListWorkflow(reqCtx.GetContext(), &workflowservice.ListWorkflowExecutionsRequest{
			Query:    query,
			PageSize: 1, // We only expect one result
		})
		if err != nil {
			return "", fmt.Errorf("failed to list workflow executions to resolve Temporal Workflow ID: %w", err)
		}

		if len(listResp.Executions) == 0 {
			continue
		}

		return listResp.Executions[0].Execution.GetWorkflowId(), nil
	}
	return "", common.ErrorNotFound(fmt.Sprintf("workflow execution not found for definition ID '%s' and run ID '%s'", workflowDefinitionID, runID))
}

func (s *Service) processWorkflowHistory(ctx *security.RequestContext, accountID string, historyIterator client.HistoryEventIterator, wfDef model.WorkflowDefinition) (*model.WorkflowExecutionDetails, error) {
	logger := ctx.GetLogger()
	workflowDetails := &model.WorkflowExecutionDetails{
		Inputs: make(map[string]any),
	}
	taskMap := make(map[string]*model.TaskExecutionDetails)
	scheduledEventIdToTaskIdMap := make(map[int64]string)

	// Define internal Temporal activity names to filter out
	internalTemporalActivities := map[string]bool{
		Internal_UpdateLastExecutionStatusActivity:   true,
		FetchWorkflowStateActivity:                   true,
		FetchWorkflowConfigsActivity:                 true,
		UpdateWorkflowStateActivity:                  true,
		FetchWorkflowDefinitionActivity:              true,
		Internal_UpdateEventResolutionStatusActivity: true,
	}

	// 3. Build a set of defined task IDs from the workflow definition. The companion
	// definedTaskByID map preserves the full task definition so child-workflow events
	// (which only carry the parent task ID via memo) can recover the real task Type
	// and Params instead of falling back to a generic "uses" placeholder.
	definedTaskIDs := make(map[string]struct{})
	definedTaskByID := make(map[string]model.Task)
	var buildDefinedTaskIDs func(tasks []model.Task)
	buildDefinedTaskIDs = func(tasks []model.Task) {
		for _, task := range tasks {
			definedTaskIDs[task.ID] = struct{}{}
			definedTaskByID[task.ID] = task
			if len(task.Tasks) > 0 {
				buildDefinedTaskIDs(task.Tasks)
			}
		}
	}
	buildDefinedTaskIDs(wfDef.Tasks)

	for historyIterator.HasNext() {
		event, err := historyIterator.Next()
		if err != nil {
			return nil, fmt.Errorf("failed to get next history event: %w", err)
		}

		switch event.GetEventType() {
		case enums.EVENT_TYPE_WORKFLOW_EXECUTION_STARTED:
			if attrs := event.GetWorkflowExecutionStartedEventAttributes(); attrs.GetInput() != nil {
				payloads := attrs.GetInput().GetPayloads()
				if len(payloads) > 0 {
					// Arg 0: model.Workflow — captures the definition snapshot that ACTUALLY ran,
					// which can drift from the current saved def if the workflow was edited
					// after the run.
					var wfArgs model.Workflow
					if err := s.dataConverter.FromPayload(payloads[0], &wfArgs); err == nil {
						def := wfArgs.Definition
						workflowDetails.Definition = &def
						for _, input := range wfArgs.Definition.Inputs {
							if input.Default != nil {
								workflowDetails.Inputs[input.ID] = input.Default
							}
						}
					}
				}
				if len(payloads) > 1 {
					// Arg 1: inputs map[string]any
					var inputsArg map[string]any
					if err := s.dataConverter.FromPayload(payloads[1], &inputsArg); err == nil {
						for k, v := range inputsArg {
							workflowDetails.Inputs[k] = v
						}
					}
				}
			}

		case enums.EVENT_TYPE_ACTIVITY_TASK_SCHEDULED:
			attrs := event.GetActivityTaskScheduledEventAttributes()
			activityType := attrs.GetActivityType().GetName()
			// Filter out internal Temporal activities
			if internalTemporalActivities[activityType] {
				continue
			}

			taskID := attrs.GetActivityId()
			scheduledEventIdToTaskIdMap[event.GetEventId()] = taskID
			taskMap[taskID] = &model.TaskExecutionDetails{
				ID:               taskID,
				ActivityID:       attrs.GetActivityId(),
				Type:             activityType,
				Status:           model.TaskStatusScheduled,
				ScheduledEventID: event.GetEventId(),
				StartTime:        timestampPBToTimestamp(event.GetEventTime()),
			}
			if attrs.GetRetryPolicy() != nil {
				rp := attrs.GetRetryPolicy()
				taskMap[taskID].RetryPolicy = &model.WorkflowRetryPolicy{
					InitialInterval:        rp.GetInitialInterval().String(),
					BackoffCoefficient:     rp.GetBackoffCoefficient(),
					MaximumInterval:        rp.GetMaximumInterval().String(),
					MaximumAttempts:        rp.GetMaximumAttempts(),
					NonRetryableErrorTypes: rp.GetNonRetryableErrorTypes(),
				}
			}

			if attrs.GetInput() != nil && len(attrs.GetInput().GetPayloads()) > 0 {
				var activityInput map[string]any
				if err := s.dataConverter.FromPayload(attrs.GetInput().GetPayloads()[0], &activityInput); err == nil {
					taskMap[taskID].Input = activityInput
					renderedParams := make(map[string]any)
					for k, v := range activityInput {
						if !strings.HasPrefix(k, "_") {
							renderedParams[k] = v
						}
					}
					if len(renderedParams) > 0 {
						taskMap[taskID].RenderedParams = renderedParams
					}
				}
			}

		case enums.EVENT_TYPE_ACTIVITY_TASK_STARTED:
			attrs := event.GetActivityTaskStartedEventAttributes()
			if taskID, ok := scheduledEventIdToTaskIdMap[attrs.GetScheduledEventId()]; ok {
				if task, ok := taskMap[taskID]; ok {
					task.Status = model.TaskStatusStarted
					task.StartedEventID = event.GetEventId()
					task.StartTime = timestampPBToTimestamp(event.GetEventTime())
					task.Attempt = int(attrs.GetAttempt())
				}
			}

		case enums.EVENT_TYPE_ACTIVITY_TASK_COMPLETED:
			attrs := event.GetActivityTaskCompletedEventAttributes()
			if taskID, ok := scheduledEventIdToTaskIdMap[attrs.GetScheduledEventId()]; ok {
				if task, ok := taskMap[taskID]; ok {
					task.Status = model.TaskStatusCompleted
					task.CompletedEventID = event.GetEventId()
					task.EndTime = timestampPBToTimestamp(event.GetEventTime())
					var result any
					if attrs.GetResult() != nil && len(attrs.GetResult().GetPayloads()) > 0 {
						if err := s.dataConverter.FromPayload(attrs.GetResult().GetPayloads()[0], &result); err == nil {
							// If result is a JSON string, unmarshal it
							if resStr, ok := result.(string); ok && resStr != "" {
								var resMap any
								if err := json.Unmarshal([]byte(resStr), &resMap); err == nil {
									result = resMap
								}
							}
							task.Output = result
						} else {
							task.Error = fmt.Sprintf("Failed to deserialize activity result: %v", err)
						}
					}
				}
			}

		case enums.EVENT_TYPE_ACTIVITY_TASK_FAILED:
			attrs := event.GetActivityTaskFailedEventAttributes()
			if taskID, ok := scheduledEventIdToTaskIdMap[attrs.GetScheduledEventId()]; ok {
				if task, ok := taskMap[taskID]; ok {
					task.Status = model.TaskStatusFailed
					task.CompletedEventID = event.GetEventId()
					task.EndTime = timestampPBToTimestamp(event.GetEventTime())
					if failure := attrs.GetFailure(); failure != nil {
						var sb strings.Builder
						sb.WriteString(failure.GetMessage())
						if cause := failure.GetCause(); cause != nil {
							if causeMsg := cause.GetMessage(); causeMsg != "" {
								sb.WriteString(" | Cause: ")
								sb.WriteString(causeMsg)
							}
						}
						if stack := failure.GetStackTrace(); stack != "" {
							sb.WriteString(" | StackTrace: ")
							sb.WriteString(stack)
						}
						task.Error = sb.String()
					} else {
						task.Error = "Unknown failure (no details)"
					}
				}
			}

		case enums.EVENT_TYPE_ACTIVITY_TASK_TIMED_OUT:
			attrs := event.GetActivityTaskTimedOutEventAttributes()
			if taskID, ok := scheduledEventIdToTaskIdMap[attrs.GetScheduledEventId()]; ok {
				if task, ok := taskMap[taskID]; ok {
					task.Status = model.TaskStatusTimedOut
					task.CompletedEventID = event.GetEventId()
					task.EndTime = timestampPBToTimestamp(event.GetEventTime())
					if failure := attrs.GetFailure(); failure != nil {
						var sb strings.Builder
						sb.WriteString(failure.GetMessage())
						if cause := failure.GetCause(); cause != nil {
							if causeMsg := cause.GetMessage(); causeMsg != "" {
								sb.WriteString(" | Cause: ")
								sb.WriteString(causeMsg)
							}
						}
						if stack := failure.GetStackTrace(); stack != "" {
							sb.WriteString(" | StackTrace: ")
							sb.WriteString(stack)
						}
						task.Error = sb.String()
					} else {
						task.Error = "Timeout (no failure details)"
					}
				}
			}

		case enums.EVENT_TYPE_START_CHILD_WORKFLOW_EXECUTION_INITIATED:
			attrs := event.GetStartChildWorkflowExecutionInitiatedEventAttributes()
			var parentTaskID string
			found := false

			// Try to get from Memo first
			if attrs.Memo != nil {
				if memo, ok := attrs.Memo.GetFields()["parent_task_id"]; ok {
					if err := s.dataConverter.FromPayload(memo, &parentTaskID); err == nil {
						found = true
					}
				}
			}

			// Fallback: Parse from WorkflowID
			if !found {
				// Format: tenantID/accountID/parentWfID-taskID-parentRunID
				wfID := attrs.GetWorkflowId()
				parts := strings.Split(wfID, "/")
				if len(parts) > 0 {
					compositeID := parts[len(parts)-1]
					if len(compositeID) > 74 {
						remaining := compositeID[37:]
						if len(remaining) > 37 {
							parentTaskID = remaining[:len(remaining)-37]
							found = true
						}
					}
				}
			}

			if !found {
				logger.Error("Failed to resolve parent task ID for initiated child workflow", "workflowID", attrs.GetWorkflowId(), "error", "parent_task_id missing from memo and fallback parsing failed")
			}

			if found {
				if _, ok := definedTaskIDs[parentTaskID]; ok {
					// Prefer the real task Type/Params from the definition so the UI can render
					// a typed icon, schema-aware input panel, and the right detail label.
					// Falls back to the legacy "uses" placeholder when the parent task
					// isn't found in the definition (e.g. mid-edit workflow).
					defTask, hasDef := definedTaskByID[parentTaskID]
					taskType := "uses"
					if hasDef && defTask.Type != "" {
						taskType = defTask.Type
					}
					details := &model.TaskExecutionDetails{
						ID:               parentTaskID,
						Type:             taskType,
						Status:           model.TaskStatusScheduled,
						ScheduledEventID: event.GetEventId(),
						StartTime:        timestampPBToTimestamp(event.GetEventTime()),
					}
					if hasDef && len(defTask.Params) > 0 {
						details.Input = defTask.Params
					} else if attrs.GetInput() != nil && len(attrs.GetInput().GetPayloads()) > 0 {
						// Legacy fallback: surface the child workflow's declared input defaults
						// when we can't recover the call-site params from the definition.
						var childWf model.Workflow
						if err := s.dataConverter.FromPayload(attrs.GetInput().GetPayloads()[0], &childWf); err == nil {
							inputMap := make(map[string]any)
							for _, inp := range childWf.Definition.Inputs {
								inputMap[inp.ID] = inp.Default
							}
							details.Input = inputMap
						}
					}
					taskMap[parentTaskID] = details
				}
			}

		case enums.EVENT_TYPE_WORKFLOW_EXECUTION_COMPLETED:
			attrs := event.GetWorkflowExecutionCompletedEventAttributes()
			if attrs.GetResult() != nil && len(attrs.GetResult().GetPayloads()) > 0 {
				var workflowResult any
				if err := s.dataConverter.FromPayload(attrs.GetResult().GetPayloads()[0], &workflowResult); err == nil {
					// If result is a JSON string, unmarshal it
					if resStr, ok := workflowResult.(string); ok && resStr != "" {
						var resMap any
						if err := json.Unmarshal([]byte(resStr), &resMap); err == nil {
							workflowResult = resMap
						}
					}
					workflowDetails.WorkflowResult = workflowResult
				} else {
					logger.Error("Failed to deserialize workflow result", "error", err)
				}
			}

		case enums.EVENT_TYPE_WORKFLOW_EXECUTION_FAILED:
			attrs := event.GetWorkflowExecutionFailedEventAttributes()
			if failure := attrs.GetFailure(); failure != nil {
				var sb strings.Builder
				sb.WriteString(failure.GetMessage())
				if cause := failure.GetCause(); cause != nil {
					if causeMsg := cause.GetMessage(); causeMsg != "" {
						sb.WriteString(" | Cause: ")
						sb.WriteString(causeMsg)
					}
				}
				if stack := failure.GetStackTrace(); stack != "" {
					sb.WriteString(" | StackTrace: ")
					sb.WriteString(stack)
				}
				workflowDetails.Error = sb.String()
			} else {
				workflowDetails.Error = "Unknown failure (no details)"
			}
		case enums.EVENT_TYPE_CHILD_WORKFLOW_EXECUTION_COMPLETED:
			attrs := event.GetChildWorkflowExecutionCompletedEventAttributes()
			childWorkflowId := attrs.GetWorkflowExecution().GetWorkflowId()
			childRunId := attrs.GetWorkflowExecution().GetRunId()

			var parentTaskID string
			var resolvedChildWorkflowID = childWorkflowId // Fallback to Temporal ID
			found := false

			// Describe the child workflow to get its memo
			childDescribe, err := s.temporalClient.DescribeWorkflowExecution(ctx.GetContext(), childWorkflowId, childRunId)
			if err != nil {
				logger.Error("Failed to describe child workflow execution", "error", err)
				continue
			}

			if childDescribe.WorkflowExecutionInfo.Memo != nil {
				memoFields := childDescribe.WorkflowExecutionInfo.Memo.GetFields()
				if memo, ok := memoFields["parent_task_id"]; ok {
					if err := s.dataConverter.FromPayload(memo, &parentTaskID); err == nil {
						found = true
					}
				}
				if memo, ok := memoFields["child_definition_id"]; ok {
					var childDefID string
					if err := s.dataConverter.FromPayload(memo, &childDefID); err == nil {
						resolvedChildWorkflowID = childDefID
					} else {
						logger.Error("Failed to decode child_definition_id from memo", "error", err)
					}
				}
			}

			if resolvedChildWorkflowID == childWorkflowId && !strings.HasPrefix(resolvedChildWorkflowID, "inline-") {
				logger.Warn("Could not resolve child definition ID from Memo, using Temporal WorkflowID", "temporalID", childWorkflowId)
			}

			// Fallback: Parse from WorkflowID
			if !found {
				parts := strings.Split(childWorkflowId, "/")
				if len(parts) > 0 {
					compositeID := parts[len(parts)-1]
					if len(compositeID) > 74 {
						remaining := compositeID[37:]
						if len(remaining) > 37 {
							parentTaskID = remaining[:len(remaining)-37]
							found = true
						}
					}
				}
			}

			if !found {
				logger.Error("Failed to resolve parent task ID for completed child workflow", "workflowID", childWorkflowId, "error", "parent_task_id missing from memo and fallback parsing failed")
			}

			if found {
				if parentTask, ok := taskMap[parentTaskID]; ok {
					parentTask.Status = model.TaskStatusCompleted
					parentTask.EndTime = timestampPBToTimestamp(event.GetEventTime())

					// Capture the child workflow's result on the parent task so the
					// Executions panel renders Output (was always empty before).
					// core.call-workflow has a fixed OutputSchema of
					// {workflow_id, run_id, output} — shape the payload to match so the
					// "Formatted according to ... output schema" UI binds correctly.
					if attrs.GetResult() != nil && len(attrs.GetResult().GetPayloads()) > 0 {
						var childResult any
						if err := s.dataConverter.FromPayload(attrs.GetResult().GetPayloads()[0], &childResult); err == nil {
							if parentTask.Type == "core.call-workflow" {
								parentTask.Output = map[string]any{
									"workflow_id": resolvedChildWorkflowID,
									"run_id":      childRunId,
									"output":      childResult,
								}
							} else {
								parentTask.Output = childResult
							}
						} else {
							logger.Error("Failed to deserialize child workflow result", "error", err)
						}
					}

					childDetails, err := s.GetDetailedWorkflowExecution(ctx, accountID, resolvedChildWorkflowID, childRunId)
					if err != nil {
						logger.Error("Failed to get child workflow execution details", "error", err)
					} else {
						parentTask.Children = childDetails.Tasks
					}
				}
			}
		}
	}

	// Handle recursion for group tasks
	for _, task := range taskMap {
		if task.Type == "core.group" && task.Status == model.TaskStatusCompleted {
			if resultMap, ok := task.Output.(map[string]any); ok {
				childWorkflowId, wOK := resultMap["workflowId"].(string)
				childRunId, rOK := resultMap["runId"].(string)
				if wOK && rOK {
					childDetails, err := s.GetDetailedWorkflowExecution(ctx, accountID, childWorkflowId, childRunId)
					if err != nil {
						fmt.Printf("Failed to get child workflow execution details: %v\n", err)
					} else {
						task.Children = childDetails.Tasks
					}
				}
			}
		}
	}

	// core.call-workflow drill-down. When the executor's inline branch fires we already
	// populate Children from CHILD_WORKFLOW_EXECUTION_COMPLETED above. But the fallback
	// path in CallWorkflowTask.Execute() starts the called workflow via temporalClient
	// rather than as a Temporal child workflow, so the parent's history only carries
	// an ActivityTaskCompleted with the {workflow_id, run_id, output} payload — no
	// CHILD_WORKFLOW events. Recover the called run's task list by re-resolving from
	// the payload here so the Executions panel can show the nested tasks just like for
	// individually-run workflows.
	for _, task := range taskMap {
		if task.Type != "core.call-workflow" || task.Status != model.TaskStatusCompleted {
			continue
		}
		if task.Children != nil {
			continue // already populated via the inline child-workflow path
		}
		resultMap, ok := task.Output.(map[string]any)
		if !ok {
			continue
		}
		childWfID, wOK := resultMap["workflow_id"].(string)
		childRunID, rOK := resultMap["run_id"].(string)
		if !wOK || !rOK || childWfID == "" || childRunID == "" {
			continue
		}
		childDetails, err := s.GetDetailedWorkflowExecution(ctx, accountID, childWfID, childRunID)
		if err != nil {
			logger.Warn("Failed to resolve core.call-workflow drill-down", "taskID", task.ID, "childWorkflowID", childWfID, "childRunID", childRunID, "error", err)
			continue
		}
		task.Children = childDetails.Tasks
	}

	tasks := make([]model.TaskExecutionDetails, 0, len(taskMap))
	for _, task := range taskMap {
		tasks = append(tasks, *task)
	}

	// Virtualize container tasks. We detect containers by parameter shape so any
	// task adopting these conventions is handled uniformly:
	//   - `Tasks` / `params["tasks"]` — task lists embedded directly (group, foreach).
	//   - `params["cases"]`            — case-routing tasks where children execute
	//                                    under hydrated IDs like "{parentID}-{targetID}"
	//                                    (core.switch). The container itself runs as
	//                                    a real activity (resolution dispatch), so it
	//                                    arrives in taskMap with Input/Output/Status
	//                                    already populated and is folded back into the
	//                                    synthesized container below.
	containerTasks := make(map[string]bool)
	for _, t := range wfDef.Tasks {
		if len(t.Tasks) > 0 {
			containerTasks[t.ID] = true
		} else if _, ok := t.Params["tasks"]; ok {
			containerTasks[t.ID] = true
		} else if _, ok := t.Params["cases"]; ok {
			containerTasks[t.ID] = true
		}
	}

	if len(containerTasks) > 0 {
		var newTasks []model.TaskExecutionDetails
		containerChildren := make(map[string][]model.TaskExecutionDetails)
		// Some container tasks (case-routing tasks like core.switch) dispatch
		// their resolution as an activity, so taskMap already carries an entry
		// for the container itself. Pull those aside so they become the
		// synthesized container rather than appearing as duplicates at the top
		// level.
		preexistingContainerEntries := make(map[string]model.TaskExecutionDetails)

		for _, t := range tasks {
			if _, isContainer := containerTasks[t.ID]; isContainer {
				preexistingContainerEntries[t.ID] = t
				continue
			}
			matchedContainer := ""
			// Find if task belongs to any container
			for containerID := range containerTasks {
				prefix := containerID + "-"
				if strings.HasPrefix(t.ID, prefix) {
					// Check if it's the longest match (handle nested naming if ambiguous)
					if len(containerID) > len(matchedContainer) {
						matchedContainer = containerID
					}
				}
			}

			if matchedContainer != "" {
				child := t
				child.ID = strings.TrimPrefix(t.ID, matchedContainer+"-")
				containerChildren[matchedContainer] = append(containerChildren[matchedContainer], child)
			} else {
				newTasks = append(newTasks, t)
			}
		}

		// Ensure containers with a preexisting entry are emitted even when no
		// child branch was scheduled (e.g. a switch that fell through with no
		// matching case and an empty default).
		for containerID := range preexistingContainerEntries {
			if _, ok := containerChildren[containerID]; !ok {
				containerChildren[containerID] = nil
			}
		}

		for containerID, children := range containerChildren {
			// Find definitions to get the type
			var taskType string
			for _, t := range wfDef.Tasks {
				if t.ID == containerID {
					taskType = t.Type
					break
				}
			}

			// Special handling for core.foreach to group by iteration
			if taskType == "core.foreach" {
				iterationGroups := make(map[string][]model.TaskExecutionDetails)
				for _, child := range children {
					// Expected format: "{index}-{childTaskID}"
					parts := strings.SplitN(child.ID, "-", 2)
					if len(parts) == 2 {
						index := parts[0]
						// Update child ID to remove the index prefix for cleaner display inside the iteration group
						child.ID = parts[1]
						iterationGroups[index] = append(iterationGroups[index], child)
					} else {
						// Fallback for unexpected ID format
						iterationGroups["unknown"] = append(iterationGroups["unknown"], child)
					}
				}

				var newChildren []model.TaskExecutionDetails

				// Extract keys and sort them numerically
				var indices []int
				for indexStr := range iterationGroups {
					if idx, err := strconv.Atoi(indexStr); err == nil {
						indices = append(indices, idx)
					} else {
						// Handle non-numeric keys, or just append them directly
						newChildren = append(newChildren, model.TaskExecutionDetails{
							ID:       fmt.Sprintf("Iteration %s", indexStr),
							Type:     "iteration",
							Status:   model.TaskStatusFailed,
							Error:    fmt.Sprintf("Non-numeric iteration index: %s", indexStr),
							Children: iterationGroups[indexStr],
						})
					}
				}
				sort.Ints(indices) // Sort numerically

				for _, idx := range indices {
					index := strconv.Itoa(idx)
					iterChildren := iterationGroups[index]
					// Synthesize an "iteration" container
					iterTask := model.TaskExecutionDetails{
						ID:       fmt.Sprintf("Iteration %s", index),
						Type:     "iteration",               // synthetic type
						Status:   model.TaskStatusCompleted, // Aggregate status below
						Children: iterChildren,
					}

					// Aggregate status/time for the iteration container
					hasRunning := false
					hasFailed := false
					var minStart, maxEnd time.Time

					for _, child := range iterChildren {
						if child.Status == model.TaskStatusFailed {
							hasFailed = true
						}
						if child.Status == model.TaskStatusStarted || child.Status == model.TaskStatusScheduled {
							hasRunning = true
						}
						if child.StartTime != nil {
							if minStart.IsZero() || child.StartTime.Before(minStart) {
								minStart = *child.StartTime
							}
						}
						if child.EndTime != nil {
							if maxEnd.IsZero() || child.EndTime.After(maxEnd) {
								maxEnd = *child.EndTime
							}
						}
					}

					if hasFailed {
						iterTask.Status = model.TaskStatusFailed
					} else if hasRunning {
						iterTask.Status = model.TaskStatusStarted
					}

					if !minStart.IsZero() {
						iterTask.StartTime = &minStart
					}
					if !maxEnd.IsZero() {
						iterTask.EndTime = &maxEnd
					}
					newChildren = append(newChildren, iterTask)
				}
				children = newChildren
			}

			// Synthesize container task. If a preexisting entry exists (e.g. the
			// core.switch dispatched its resolution as an activity), use it as
			// the base so we preserve the container's own Input, Output, Status,
			// Error, and timings — children are the only field we always derive
			// here.
			preexisting, hasPreexisting := preexistingContainerEntries[containerID]
			var containerTask model.TaskExecutionDetails
			if hasPreexisting {
				containerTask = preexisting
				containerTask.Children = children
			} else {
				containerTask = model.TaskExecutionDetails{
					ID:       containerID,
					Type:     taskType,
					Status:   model.TaskStatusCompleted,
					Children: children,
				}
			}

			hasRunning := false
			hasFailed := false
			var minStart, maxEnd time.Time

			for _, child := range children {
				if child.Status == model.TaskStatusFailed {
					hasFailed = true
				}
				if child.Status == model.TaskStatusStarted || child.Status == model.TaskStatusScheduled {
					hasRunning = true
				}
				if child.StartTime != nil {
					if minStart.IsZero() || child.StartTime.Before(minStart) {
						minStart = *child.StartTime
					}
				}
				if child.EndTime != nil {
					if maxEnd.IsZero() || child.EndTime.After(maxEnd) {
						maxEnd = *child.EndTime
					}
				}
			}

			// Only aggregate child status/timings into the container when it did NOT
			// record its own. The activity entry captures the container's intrinsic
			// outcome (e.g. switch resolved successfully) — a child branch failure
			// shouldn't overwrite that.
			if !hasPreexisting {
				if hasFailed {
					containerTask.Status = model.TaskStatusFailed
				} else if hasRunning {
					containerTask.Status = model.TaskStatusStarted
				}
				if !minStart.IsZero() {
					containerTask.StartTime = &minStart
				}
				if !maxEnd.IsZero() {
					containerTask.EndTime = &maxEnd
				}
			}

			newTasks = append(newTasks, containerTask)
		}
		tasks = newTasks
	}

	workflowDetails.Tasks = tasks

	var sanitizeTaskExecutionDetails func([]model.TaskExecutionDetails)
	sanitizeTaskExecutionDetails = func(tasks []model.TaskExecutionDetails) {
		for i := range tasks {
			if tasks[i].Children == nil {
				tasks[i].Children = []model.TaskExecutionDetails{}
			}
			if tasks[i].RetryPolicy != nil && tasks[i].RetryPolicy.NonRetryableErrorTypes == nil {
				tasks[i].RetryPolicy.NonRetryableErrorTypes = []string{}
			}
			sanitizeTaskExecutionDetails(tasks[i].Children) // Recurse for children
		}
	}
	sanitizeTaskExecutionDetails(workflowDetails.Tasks) // Start sanitization from the top-level tasks

	return workflowDetails, nil
}

// CountWorkflows counts workflows with optional filters for status and trigger type.
func (s *Service) CountWorkflows(ctx *security.RequestContext, req model.WorkflowCountRequest) (model.WorkflowCountResponse, error) {
	if !ctx.GetSecurityContext().HasAccountAccess(req.AccountID, security.SecurityAccessTypeRead) {
		return model.WorkflowCountResponse{}, common.ErrorUnauthorized("account not accessible")
	}

	tenantID := ctx.GetSecurityContext().GetTenantId()

	count, err := s.store.CountWorkflows(ctx.GetContext(), tenantID, req.AccountID, req.Status, req.TriggerType)
	if err != nil {
		return model.WorkflowCountResponse{}, fmt.Errorf("failed to count workflows: %w", err)
	}

	return model.WorkflowCountResponse{Count: count}, nil
}

// CountWorkflowExecutions counts workflow executions with optional filters using Temporal's CountWorkflow API.
func (s *Service) CountWorkflowExecutions(ctx *security.RequestContext, req model.WorkflowExecutionCountRequest) (model.WorkflowExecutionCountResponse, error) {
	if !ctx.GetSecurityContext().HasAccountAccess(req.AccountID, security.SecurityAccessTypeRead) {
		return model.WorkflowExecutionCountResponse{}, common.ErrorUnauthorized("account not accessible")
	}

	tenantID := ctx.GetSecurityContext().GetTenantId()

	// Build the Temporal query with filters
	var conditions []string
	conditions = append(conditions, fmt.Sprintf("%s='%s'", model.SearchAttrTenantID, tenantID))
	conditions = append(conditions, fmt.Sprintf("%s='%s'", model.SearchAttrAccountID, req.AccountID))

	if req.TriggerType != "" {
		conditions = append(conditions, fmt.Sprintf("%s='%s'", model.SearchAttrWorkflowTrigger, escapeTemporalString(req.TriggerType)))
	}

	if req.WorkflowID != "" {
		conditions = append(conditions, fmt.Sprintf("%s='%s'", model.SearchAttrWorkflowID, escapeTemporalString(req.WorkflowID)))
	}

	if req.StartDate != nil {
		conditions = append(conditions, fmt.Sprintf("StartTime >= '%s'", req.StartDate.Format(time.RFC3339)))
	}

	if req.EndDate != nil {
		conditions = append(conditions, fmt.Sprintf("StartTime <= '%s'", req.EndDate.Format(time.RFC3339)))
	}

	if req.Status != "" {
		// Map our status to Temporal execution status
		temporalStatus := mapToTemporalStatus(req.Status)
		if temporalStatus != "" {
			conditions = append(conditions, fmt.Sprintf("ExecutionStatus='%s'", temporalStatus))
		}
	}

	query := strings.Join(conditions, " AND ")

	resp, err := s.temporalClient.CountWorkflow(ctx.GetContext(), &workflowservice.CountWorkflowExecutionsRequest{
		Query: query,
	})
	if err != nil {
		slog.Error("failed to count workflow executions", "error", err, "query", query)
		return model.WorkflowExecutionCountResponse{}, fmt.Errorf("failed to count workflow executions: %w", err)
	}

	return model.WorkflowExecutionCountResponse{Count: resp.GetCount()}, nil
}

// mapToTemporalStatus maps our workflow execution status to Temporal's execution status
func mapToTemporalStatus(status model.WorkflowExecutionStatus) string {
	switch status {
	case model.WorkflowExecutionStatusRunning:
		return "Running"
	case model.WorkflowExecutionStatusCompleted:
		return "Completed"
	case model.WorkflowExecutionStatusFailed:
		return "Failed"
	case model.WorkflowExecutionStatusCanceled:
		return "Canceled"
	case model.WorkflowExecutionStatusTerminated:
		return "Terminated"
	case model.WorkflowExecutionStatusTimedOut:
		return "TimedOut"
	case model.WorkflowExecutionStatusContinuedAsNew:
		return "ContinuedAsNew"
	default:
		return ""
	}
}

// ListWorkflowVersions returns the retained history (up to
// model.MaxWorkflowVersionsPerWorkflow) for a workflow, newest first.
func (s *Service) ListWorkflowVersions(ctx *security.RequestContext, accountId, id string) ([]model.WorkflowVersion, error) {
	if accountId == "" || id == "" {
		return nil, fmt.Errorf("accountId and id are required")
	}
	if !ctx.GetSecurityContext().HasAccountAccess(accountId, security.SecurityAccessTypeRead) {
		return nil, common.ErrorUnauthorized("account not accessible")
	}
	if _, err := s.store.Find(ctx.GetContext(), ctx.GetSecurityContext().GetTenantId(), accountId, id); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, fmt.Errorf("workflow with ID %s not found: %w", id, sql.ErrNoRows)
		}
		return nil, err
	}
	return s.store.ListWorkflowVersions(ctx.GetContext(), id, model.MaxWorkflowVersionsPerWorkflow)
}

// GetWorkflowVersion returns one workflow_versions row for preview.
func (s *Service) GetWorkflowVersion(ctx *security.RequestContext, accountId, id string, versionNumber int) (*model.WorkflowVersion, error) {
	if accountId == "" || id == "" || versionNumber <= 0 {
		return nil, fmt.Errorf("accountId, id, version_number are required")
	}
	if !ctx.GetSecurityContext().HasAccountAccess(accountId, security.SecurityAccessTypeRead) {
		return nil, common.ErrorUnauthorized("account not accessible")
	}
	if _, err := s.store.Find(ctx.GetContext(), ctx.GetSecurityContext().GetTenantId(), accountId, id); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, fmt.Errorf("workflow with ID %s not found: %w", id, sql.ErrNoRows)
		}
		return nil, err
	}
	v, err := s.store.GetWorkflowVersion(ctx.GetContext(), id, versionNumber)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, fmt.Errorf("version %d of workflow %s not found: %w", versionNumber, id, sql.ErrNoRows)
		}
		return nil, err
	}
	return v, nil
}

// RestoreWorkflowVersion loads the definition from `versionNumber` into the
// draft (workflows.definition). It does NOT create a new version row and does
// NOT change the live version pointer — it is purely a draft mutation, so the
// user can either re-publish or keep iterating. Live + history are preserved.
func (s *Service) RestoreWorkflowVersion(ctx *security.RequestContext, accountId, id string, versionNumber int) (model.Workflow, error) {
	if accountId == "" || id == "" || versionNumber <= 0 {
		return model.Workflow{}, fmt.Errorf("accountId, id, version_number are required")
	}
	if !ctx.GetSecurityContext().HasAccountAccess(accountId, security.SecurityAccessTypeUpdate) {
		return model.Workflow{}, common.ErrorUnauthorized("account not accessible")
	}

	current, err := s.store.Find(ctx.GetContext(), ctx.GetSecurityContext().GetTenantId(), accountId, id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return model.Workflow{}, fmt.Errorf("workflow with ID %s not found: %w", id, sql.ErrNoRows)
		}
		return model.Workflow{}, err
	}

	target, err := s.store.GetWorkflowVersion(ctx.GetContext(), id, versionNumber)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return model.Workflow{}, fmt.Errorf("version %d of workflow %s not found: %w", versionNumber, id, sql.ErrNoRows)
		}
		return model.Workflow{}, err
	}

	// Preserve name/tags/status; swap in the target version's definition.
	// updateWorkflowInternal handles updated_by, validation, webhook side-effects.
	toApply := *current
	toApply.Definition = target.Definition
	updated, err := s.updateWorkflowInternal(ctx, accountId, id, toApply)
	if err != nil {
		return updated, err
	}

	// Lineage bookkeeping: the draft now mirrors `target`, so point
	// draft_version_id at it. Done after the definition write succeeds so a
	// validation failure above leaves the existing draft_version_id intact.
	if err := s.store.SetDraftVersionID(ctx.GetContext(), ctx.GetSecurityContext().GetTenantId(), accountId, id, target.ID); err != nil {
		return updated, fmt.Errorf("failed to update draft_version_id after restore: %w", err)
	}
	updated.DraftVersionID = &target.ID
	n := target.VersionNumber
	updated.DraftVersionNumber = &n
	if target.Name != nil {
		nm := *target.Name
		updated.DraftVersionName = &nm
	} else {
		updated.DraftVersionName = nil
	}
	emitWorkflowAudit(
		ctx, accountId,
		audit.EventTypeAutorunbookUpdate, audit.EventActionUpdate, audit.EventStatusSuccess,
		id, workflowAuditSnapshot(current), workflowAuditSnapshot(&updated),
		map[string]any{
			"action":         "restore_version",
			"version_number": versionNumber,
			"version_id":     target.ID,
		},
	)
	return updated, nil
}

// PublishWorkflow snapshots the current draft (workflows.definition) as a new
// workflow_versions row. If setLive is true the new version is also marked
// live so future executions run it; otherwise the existing live pointer is
// unchanged. Either way the draft itself is preserved untouched.
//
// status decides what state the new version (and, when setLive=true, the
// workflow row mirror) lands in. Empty defaults to PAUSED — see the
// CreateWorkflow comment for the reasoning behind opt-in activation. This is
// the only path that lets a user publish directly into ACTIVE / PAUSED /
// INACTIVE; the publish action no longer silently force-activates.
func (s *Service) PublishWorkflow(ctx *security.RequestContext, accountId, id string, name, description *string, setLive bool, status model.WorkflowStatus) (*model.WorkflowVersion, error) {
	if accountId == "" || id == "" {
		return nil, fmt.Errorf("accountId and id are required")
	}
	if !ctx.GetSecurityContext().HasAccountAccess(accountId, security.SecurityAccessTypeUpdate) {
		return nil, common.ErrorUnauthorized("account not accessible")
	}
	if status == "" {
		status = model.WorkflowStatusPaused
	}
	validStatuses := map[model.WorkflowStatus]struct{}{
		model.WorkflowStatusActive:   {},
		model.WorkflowStatusPaused:   {},
		model.WorkflowStatusInactive: {},
	}
	if _, ok := validStatuses[status]; !ok {
		return nil, common.ErrorBadRequest(fmt.Sprintf("invalid status %q for publish", status))
	}
	wf, err := s.store.Find(ctx.GetContext(), ctx.GetSecurityContext().GetTenantId(), accountId, id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, fmt.Errorf("workflow with ID %s not found: %w", id, sql.ErrNoRows)
		}
		return nil, err
	}
	userID := ctx.GetSecurityContext().GetUserId()
	v, err := s.store.PublishVersion(ctx.GetContext(), id, userID, model.WorkflowVersionSourcePublish, name, description, nil, status)
	if err != nil {
		return nil, fmt.Errorf("failed to publish workflow version: %w", err)
	}
	if setLive {
		// SetLiveVersion mirrors the new live version's status onto
		// workflows.status in the same UPDATE, so the workflow row's
		// administrative state always matches what the live version says.
		if err := s.store.SetLiveVersion(ctx.GetContext(), ctx.GetSecurityContext().GetTenantId(), accountId, id, v.ID); err != nil {
			return nil, fmt.Errorf("failed to mark new version live: %w", err)
		}
		v.IsLive = true
		wf.Status = status
		wf.LiveVersionStatus = status

		// (Re)register triggers for the just-published definition. Re-registering
		// on every publish (rather than only on status flips) keeps the system
		// resilient: schedule / webhook triggers may have been added or removed
		// in the new definition, and handleWorkflowTrigger re-syncs them.
		// handleWorkflowTrigger itself looks at wf.Status to decide whether the
		// scheduled job should be left paused or running, so flipping
		// wf.Status above is the single decision point.
		tenantId := ctx.GetSecurityContext().GetTenantId()
		if _, _, err := s.handleWorkflowTrigger(ctx, id, tenantId, accountId, wf); err != nil {
			return nil, fmt.Errorf("failed to register triggers on publish: %w", err)
		}
	}
	publishAttrs := map[string]any{
		"action":         "publish_version",
		"version_number": v.VersionNumber,
		"version_id":     v.ID,
		"set_live":       setLive,
		"status":         string(status),
	}
	if name != nil {
		publishAttrs["name"] = *name
	}
	if description != nil {
		publishAttrs["description"] = *description
	}
	emitWorkflowAudit(
		ctx, accountId,
		audit.EventTypeAutorunbookUpdate, audit.EventActionUpdate, audit.EventStatusSuccess,
		id, nil, workflowAuditSnapshot(wf), publishAttrs,
	)
	return v, nil
}

// SetLiveWorkflowVersion flips workflows.live_version_id to the given version.
// This is a pointer-only change: it does NOT modify the draft (workflows.definition)
// or the workflow's status, so unpublished edits are preserved across rollbacks.
func (s *Service) SetLiveWorkflowVersion(ctx *security.RequestContext, accountId, id string, versionNumber int) (*model.Workflow, error) {
	if accountId == "" || id == "" || versionNumber <= 0 {
		return nil, fmt.Errorf("accountId, id, version_number are required")
	}
	if !ctx.GetSecurityContext().HasAccountAccess(accountId, security.SecurityAccessTypeUpdate) {
		return nil, common.ErrorUnauthorized("account not accessible")
	}
	target, err := s.store.GetWorkflowVersion(ctx.GetContext(), id, versionNumber)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, fmt.Errorf("version %d of workflow %s not found: %w", versionNumber, id, sql.ErrNoRows)
		}
		return nil, err
	}
	if err := s.store.SetLiveVersion(ctx.GetContext(), ctx.GetSecurityContext().GetTenantId(), accountId, id, target.ID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, fmt.Errorf("workflow with ID %s not found: %w", id, sql.ErrNoRows)
		}
		return nil, fmt.Errorf("failed to set live workflow version: %w", err)
	}
	wf, err := s.store.Find(ctx.GetContext(), ctx.GetSecurityContext().GetTenantId(), accountId, id)
	if err != nil {
		return wf, err
	}
	emitWorkflowAudit(
		ctx, accountId,
		audit.EventTypeAutorunbookUpdate, audit.EventActionUpdate, audit.EventStatusSuccess,
		id, nil, workflowAuditSnapshot(wf),
		map[string]any{
			"action":         "set_live_version",
			"version_number": versionNumber,
			"version_id":     target.ID,
		},
	)
	return wf, nil
}

// UpdateWorkflowVersionMetadata patches the mutable metadata fields (name,
// description) on an existing version. A nil pointer leaves the column
// unchanged; "" clears it.
func (s *Service) UpdateWorkflowVersionMetadata(ctx *security.RequestContext, accountId, id string, versionNumber int, name, description *string) (*model.WorkflowVersion, error) {
	if accountId == "" || id == "" || versionNumber <= 0 {
		return nil, fmt.Errorf("accountId, id, version_number are required")
	}
	if !ctx.GetSecurityContext().HasAccountAccess(accountId, security.SecurityAccessTypeUpdate) {
		return nil, common.ErrorUnauthorized("account not accessible")
	}
	if _, err := s.store.Find(ctx.GetContext(), ctx.GetSecurityContext().GetTenantId(), accountId, id); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, fmt.Errorf("workflow with ID %s not found: %w", id, sql.ErrNoRows)
		}
		return nil, err
	}
	v, err := s.store.UpdateVersionMetadata(ctx.GetContext(), id, versionNumber, name, description)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, fmt.Errorf("version %d of workflow %s not found: %w", versionNumber, id, sql.ErrNoRows)
		}
		return nil, err
	}
	metaAttrs := map[string]any{
		"action":         "update_version_metadata",
		"version_number": versionNumber,
		"version_id":     v.ID,
	}
	if name != nil {
		metaAttrs["name"] = *name
	}
	if description != nil {
		metaAttrs["description"] = *description
	}
	emitWorkflowAudit(
		ctx, accountId,
		audit.EventTypeAutorunbookUpdate, audit.EventActionUpdate, audit.EventStatusSuccess,
		id, nil,
		map[string]any{"version_number": v.VersionNumber, "version_id": v.ID, "name": v.Name, "description": v.Description},
		metaAttrs,
	)
	return v, nil
}

// UpdateWorkflowVersionStatus flips a single version's runtime gate
// (Active / Paused / Inactive). When the target is the live version the DAO
// mirrors status onto workflows.status in the same transaction, so listing
// filters stay consistent. Triggers are re-synced after a live-version status
// change so Temporal schedule jobs / webhook subscriptions follow the gate.
func (s *Service) UpdateWorkflowVersionStatus(ctx *security.RequestContext, accountId, id string, versionNumber int, status model.WorkflowStatus) (*model.WorkflowVersion, error) {
	if accountId == "" || id == "" || versionNumber <= 0 {
		return nil, fmt.Errorf("accountId, id, version_number are required")
	}
	if !ctx.GetSecurityContext().HasAccountAccess(accountId, security.SecurityAccessTypeUpdate) {
		return nil, common.ErrorUnauthorized("account not accessible")
	}
	validStatuses := map[model.WorkflowStatus]struct{}{
		model.WorkflowStatusActive:   {},
		model.WorkflowStatusPaused:   {},
		model.WorkflowStatusInactive: {},
	}
	if _, ok := validStatuses[status]; !ok {
		return nil, common.ErrorBadRequest(fmt.Sprintf("invalid status %q", status))
	}
	tenantId := ctx.GetSecurityContext().GetTenantId()
	target, err := s.store.GetWorkflowVersion(ctx.GetContext(), id, versionNumber)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, fmt.Errorf("version %d of workflow %s not found: %w", versionNumber, id, sql.ErrNoRows)
		}
		return nil, err
	}
	wasLive, err := s.store.UpdateVersionStatus(ctx.GetContext(), tenantId, accountId, id, target.ID, status)
	if err != nil {
		return nil, fmt.Errorf("failed to update version status: %w", err)
	}
	// Mutating a non-live version is just a metadata change — no Temporal
	// schedules to (re)wire. Mutating the live version flips the runtime gate,
	// so re-run handleWorkflowTrigger to register / unregister downstream.
	if wasLive {
		wf, err := s.store.Find(ctx.GetContext(), tenantId, accountId, id)
		if err != nil {
			return nil, fmt.Errorf("failed to reload workflow after status change: %w", err)
		}
		if _, _, err := s.handleWorkflowTrigger(ctx, id, tenantId, accountId, wf); err != nil {
			return nil, fmt.Errorf("failed to resync triggers after status change: %w", err)
		}
	}
	target.Status = status
	target.IsLive = wasLive
	emitWorkflowAudit(
		ctx, accountId,
		audit.EventTypeAutorunbookUpdate, audit.EventActionUpdate, audit.EventStatusSuccess,
		id, nil,
		map[string]any{"version_number": versionNumber, "version_id": target.ID, "status": string(status), "is_live": wasLive},
		map[string]any{"action": "update_version_status"},
	)
	return target, nil
}
