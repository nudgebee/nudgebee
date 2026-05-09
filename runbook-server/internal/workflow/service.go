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
	RetriggerWorkflowExecution(ctx *security.RequestContext, accountId, workflowId, executionId string, inputs map[string]any) (string, error)
	ListWorkflows(ctx *security.RequestContext, accountId string, request model.ListWorkflowRequest) (model.ListWorkflowResponse, error)
	GetWorkflow(ctx *security.RequestContext, accountId, id string) (*model.Workflow, error)
	UpdateWorkflow(ctx *security.RequestContext, accountId, id string, workflow model.Workflow) (model.Workflow, error)
	DeleteWorkflow(ctx *security.RequestContext, accountId, id string) error
	UpdateWorkflowStatus(ctx *security.RequestContext, accountId, id string, status model.WorkflowStatus) error
	ListWorkflowExecutions(ctx *security.RequestContext, accountId, workflowId string, request model.ListWorkflowExecutionRequest) (model.ListWorkflowExecutionResponse, error)
	GetDetailedWorkflowExecution(ctx *security.RequestContext, accountId, workflowId, executionId string) (*model.WorkflowExecutionDetails, error)
	UpdateWorkflowExecution(ctx *security.RequestContext, accountId, workflowId, executionId string, inputs map[string]any) error
	CompleteApprovalTask(ctx *security.RequestContext, token, status string, result any) error
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

func enforceWebhookSecrets(wf, existingWf *model.Workflow) error {
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
				for _, et := range existingWf.Definition.Triggers {
					if et.Type == model.WorkflowTriggerWebhook && et.Internal != nil && et.Internal.Name == t.Internal.Name {
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
			}

			if !matchFound && userProvided {
				return common.ErrorBadRequest("webhook secret cannot be provided for new trigger")
			}
		}
	}
	return nil
}

func normalizeWebhookTriggers(workflowID string, wf *model.Workflow) {
	prefix := fmt.Sprintf("wf-%s-", workflowID)
	for i, trigger := range wf.Definition.Triggers {
		if trigger.Type == model.WorkflowTriggerWebhook {
			if integrationID, ok := trigger.Params["integration_name"].(string); ok && integrationID != "" {
				if wf.Definition.Triggers[i].Internal == nil {
					wf.Definition.Triggers[i].Internal = &model.TriggerInternal{}
				}

				if strings.HasPrefix(integrationID, prefix) {
					// Legacy: Normalized name is in Params. Move to Internal.Name and strip prefix from Params.
					wf.Definition.Triggers[i].Internal.Name = integrationID
					wf.Definition.Triggers[i].Params["integration_name"] = strings.TrimPrefix(integrationID, prefix)
				} else {
					// New: Params has user name. Set Internal.Name.
					wf.Definition.Triggers[i].Internal.Name = prefix + integrationID
				}
			}
		}
	}
}

func (s *Service) CreateWorkflow(ctx *security.RequestContext, accountId string, wf model.Workflow) (string, string, error) {
	if !ctx.GetSecurityContext().HasAccountAccess(accountId, security.SecurityAccessTypeCreate) {
		return "", "", common.ErrorUnauthorized("account not accessible")
	}

	wf.CreatedBy = ctx.GetSecurityContext().GetUserId()
	wf.UpdatedBy = ctx.GetSecurityContext().GetUserId()
	wf.ID = uuid.New().String() // Always generate a new ID for CreateWorkflow

	validStatuses := []model.WorkflowStatus{model.WorkflowStatusActive, model.WorkflowStatusPaused, model.WorkflowStatusDraft, model.WorkflowStatusInactive}
	isValidStatus := false
	for _, status := range validStatuses {
		if wf.Status == status {
			isValidStatus = true
			break
		}
	}
	if wf.Status == "" || !isValidStatus {
		wf.Status = model.WorkflowStatusDraft
	}

	if wf.Definition.Version == "" {
		wf.Definition.Version = "v1"
	}

	normalizeWebhookTriggers(wf.ID, &wf)

	if err := enforceWebhookSecrets(&wf, nil); err != nil {
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

	storedId, err := s.store.Save(ctx.GetContext(), ctx.GetSecurityContext().GetTenantId(), accountId, wf)
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

				// If workflow is Inactive or Draft, ensure schedule is removed.
				if wf.Status == model.WorkflowStatusInactive || wf.Status == model.WorkflowStatusDraft {
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

				// Update workflow in DB to persist the secret
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
	// If workflow is Inactive/Draft, valid set is empty (all should be deleted)
	if wf.Status != model.WorkflowStatusInactive && wf.Status != model.WorkflowStatusDraft {
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
		case model.WorkflowStatusDraft:
			return "", fmt.Errorf("workflow %s is in draft status and cannot be executed", id)
		default:
			return "", fmt.Errorf("workflow %s is not active, status is %s", id, wf.Status)
		}
	}

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
	return we.GetRunID(), nil // Return the actual Temporal Run ID
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
		Name:       "dry-run-" + time.Now().Format(time.RFC3339),
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
		Name:       "dry-run-" + time.Now().Format(time.RFC3339),
		CreatedBy:  ctx.GetSecurityContext().GetUserId(),
		UpdatedBy:  ctx.GetSecurityContext().GetUserId(),
	}

	run, err := s.temporalClient.ExecuteWorkflow(context.Background(), options, s.workflowExecutor.ExecuteWorkflowInternal, &wf, request.Inputs)
	if err != nil {
		return "", "", err
	}

	return runWorkflowID, run.GetRunID(), nil
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
		validStatuses := []model.WorkflowStatus{model.WorkflowStatusActive, model.WorkflowStatusPaused, model.WorkflowStatusDraft, model.WorkflowStatusInactive}
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

	if err := enforceWebhookSecrets(&workflow, existingWf); err != nil {
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
				// Trigger was removed, delete it
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

	return workflow, nil
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
	case model.WorkflowStatusInactive, model.WorkflowStatusDraft:
		// Update workflow status to target
		wf.Status = status
		// Re-run handleWorkflowTrigger to cleanup schedules
		// We can set Status to target, pass it to handleTrigger.
		// handleWorkflowTrigger checks status and cleans up schedules if inactive.
		_, _, err := s.handleWorkflowTrigger(ctx, id, ctx.GetSecurityContext().GetTenantId(), accountId, wf)
		if err != nil {
			return fmt.Errorf("failed to cleanup schedules for inactive/draft workflow: %w", err)
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
	return s.store.UpdateWorkflowStatus(ctx.GetContext(), ctx.GetSecurityContext().GetTenantId(), accountId, id, status)
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

	// Extract Memo
	if info.Memo != nil {
		memoMap := make(map[string]any)
		for k, v := range info.Memo.GetFields() {
			var val any
			if err := s.dataConverter.FromPayload(v, &val); err != nil {
				fmt.Printf("Failed to deserialize memo field %s: %v\n", k, err)
				continue
			}
			memoMap[k] = val
		}
		workflowDetails.Memo = memoMap
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

	// 2. Get workflow history and process it
	historyIterator := s.temporalClient.GetWorkflowHistory(ctx.GetContext(), temporalId, executionId, false, enums.HISTORY_EVENT_FILTER_TYPE_ALL_EVENT)
	historyDetails, err := s.processWorkflowHistory(ctx, accountId, historyIterator, wf.Definition)
	if err != nil {
		return nil, err
	}

	workflowDetails.Inputs = historyDetails.Inputs
	workflowDetails.WorkflowResult = historyDetails.WorkflowResult
	workflowDetails.Error = historyDetails.Error
	workflowDetails.Tasks = historyDetails.Tasks
	workflowDetails.Definition = historyDetails.Definition

	return workflowDetails, nil
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
	return s.temporalClient.CancelWorkflow(ctx.GetContext(), temporalId, executionId)
}

func (s *Service) PauseWorkflow(ctx *security.RequestContext, accountId, id string) error {
	if !ctx.GetSecurityContext().HasAccountAccess(accountId, security.SecurityAccessTypeCreate) {
		return common.ErrorUnauthorized("account not accessible")
	}

	wf, err := s.store.Find(ctx.GetContext(), ctx.GetSecurityContext().GetTenantId(), accountId, id)
	if err != nil {
		return err
	}

	if wf.Status == model.WorkflowStatusDraft {
		return fmt.Errorf("cannot pause a draft workflow")
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

	return s.store.UpdateWorkflowStatus(ctx.GetContext(), ctx.GetSecurityContext().GetTenantId(), accountId, id, model.WorkflowStatusPaused)
}

func (s *Service) ResumeWorkflow(ctx *security.RequestContext, accountId, id string) error {
	if !ctx.GetSecurityContext().HasAccountAccess(accountId, security.SecurityAccessTypeCreate) {
		return common.ErrorUnauthorized("account not accessible")
	}

	wf, err := s.store.Find(ctx.GetContext(), ctx.GetSecurityContext().GetTenantId(), accountId, id)
	if err != nil {
		return err
	}

	if wf.Status == model.WorkflowStatusDraft {
		return fmt.Errorf("cannot resume a draft workflow")
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

	return s.store.UpdateWorkflowStatus(ctx.GetContext(), ctx.GetSecurityContext().GetTenantId(), accountId, id, model.WorkflowStatusActive)
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

	tenantId := ctx.GetSecurityContext().GetTenantId()

	// Fetch configs and secrets for template rendering
	var renderedParams map[string]any
	if s.configService != nil {
		configs, err := s.configService.ListConfigsDecrypted(ctx.GetContext(), tenantId, accountId, nil)
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
		Internal_UpdateLastExecutionStatusActivity:      true,
		FetchWorkflowStateActivity:                      true,
		FetchWorkflowConfigsActivity:                    true,
		UpdateWorkflowStateActivity:                     true,
		FetchWorkflowDefinitionActivity:                 true,
		Internal_UpdateEventResolutionStatusActivity:     true,
	}

	// 3. Build a set of defined task IDs from the workflow definition
	definedTaskIDs := make(map[string]struct{})
	var buildDefinedTaskIDs func(tasks []model.Task)
	buildDefinedTaskIDs = func(tasks []model.Task) {
		for _, task := range tasks {
			definedTaskIDs[task.ID] = struct{}{}
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
					taskMap[parentTaskID] = &model.TaskExecutionDetails{
						ID:               parentTaskID,
						Type:             "uses", // Or something to indicate it's a child workflow
						Status:           model.TaskStatusScheduled,
						ScheduledEventID: event.GetEventId(),
						StartTime:        timestampPBToTimestamp(event.GetEventTime()),
					}

					if attrs.GetInput() != nil && len(attrs.GetInput().GetPayloads()) > 0 {
						var childWf model.Workflow
						if err := s.dataConverter.FromPayload(attrs.GetInput().GetPayloads()[0], &childWf); err == nil {
							inputMap := make(map[string]any)
							for _, inp := range childWf.Definition.Inputs {
								inputMap[inp.ID] = inp.Default
							}
							taskMap[parentTaskID].Input = inputMap
						}
					}
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

	tasks := make([]model.TaskExecutionDetails, 0, len(taskMap))
	for _, task := range taskMap {
		tasks = append(tasks, *task)
	}

	// Virtualize container tasks
	containerTasks := make(map[string]bool)
	for _, t := range wfDef.Tasks {
		if len(t.Tasks) > 0 {
			containerTasks[t.ID] = true
		} else if _, ok := t.Params["tasks"]; ok {
			containerTasks[t.ID] = true
		}
	}

	if len(containerTasks) > 0 {
		var newTasks []model.TaskExecutionDetails
		containerChildren := make(map[string][]model.TaskExecutionDetails)

		for _, t := range tasks {
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

			// Synthesize container task
			containerTask := model.TaskExecutionDetails{
				ID:       containerID,
				Type:     taskType,
				Status:   model.TaskStatusCompleted,
				Children: children,
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
