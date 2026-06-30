package audit

import (
	"encoding/json"
	"fmt"
	"nudgebee/collector/cloud/common"
	"nudgebee/collector/cloud/providers"
	"nudgebee/collector/cloud/security"
	"strings"
	"time"
)

// valueOrNil returns nil if the string is empty, otherwise returns the string.
// This matches the pattern used in api-server/services/audit/service.go
// and allows proper NULL insertion into UUID columns.
func valueOrNil(value string) any {
	if value == "" {
		return nil
	}
	return value
}

// ResourceActionAudit represents audit log entry for resource actions
type ResourceActionAudit struct {
	UserID         string                 `db:"user_id"`
	TenantID       string                 `db:"tenant_id"`
	AccountID      string                 `db:"account_id"`
	EventTime      time.Time              `db:"event_time"`
	EventCategory  EventCategory          `db:"event_category"`
	EventType      EventType              `db:"event_type"`
	EventPrevState string                 `db:"event_prev_state"`
	EventState     string                 `db:"event_state"`
	EventActor     EventActor             `db:"event_actor"`
	EventTarget    string                 `db:"event_target"`
	EventAction    EventAction            `db:"event_action"`
	EventStatus    EventStatus            `db:"event_status"`
	TransactionID  string                 `db:"transaction_id"`
	EventAttr      map[string]interface{} `db:"event_attr"`
}

// LogResourceAction logs a resource action to the audit table
func LogResourceAction(
	ctx providers.CloudProviderContext,
	command providers.ApplyCommandRequest,
	account providers.Account,
	status EventStatus,
	errorMessage string,
) error {
	return logResourceActionWithTarget(ctx, command, account, status, errorMessage, command.ResourceId)
}

// LogResourceActionBatch logs resource actions for multiple targets (batch operations)
func LogResourceActionBatch(
	ctx providers.CloudProviderContext,
	command providers.ApplyCommandRequest,
	account providers.Account,
	status EventStatus,
	errorMessage string,
	resourceIDs []string,
) error {
	// Log one audit record per resource in the batch
	for _, resourceID := range resourceIDs {
		err := logResourceActionWithTarget(ctx, command, account, status, errorMessage, resourceID)
		if err != nil {
			ctx.GetLogger().Warn("failed to log audit record for resource", "resource_id", resourceID, "error", err)
			// Continue logging others even if one fails
		}
	}
	return nil
}

// logResourceActionWithTarget logs a resource action with a specific target
func logResourceActionWithTarget(
	ctx providers.CloudProviderContext,
	command providers.ApplyCommandRequest,
	account providers.Account,
	status EventStatus,
	errorMessage string,
	targetResourceID string,
) error {
	// Extract user/tenant info from security context
	// Now we can directly call GetSecurityContext() on the CloudProviderContext interface
	var userID string
	var tenantID string

	if securityContext := ctx.GetSecurityContext(); securityContext != nil {
		userID = securityContext.GetUserId()
		tenantID = securityContext.GetTenantId()
	}

	// Build event attributes
	eventAttr := map[string]interface{}{
		"resource_id":    targetResourceID,
		"resource_type":  command.ServiceName,
		"service_name":   command.ServiceName,
		"cloud_provider": account.CloudProvider,
		"region":         command.Region,
		"command":        command.Command,
	}

	// Add command args to attributes
	if command.Args != nil {
		eventAttr["command_args"] = command.Args
	}

	// Add error message if failed
	if status == EventStatusFailure && errorMessage != "" {
		eventAttr["error_message"] = errorMessage
	}

	// Convert eventAttr to JSON string for storage
	eventAttrJSON, err := json.Marshal(eventAttr)
	if err != nil {
		ctx.GetLogger().Error("failed to marshal event attributes", "error", err)
		eventAttrJSON = []byte("{}")
	}

	// Get database manager
	dbm, err := common.GetDatabaseManager(common.Metastore)
	if err != nil {
		return fmt.Errorf("failed to get database manager: %w", err)
	}

	// Map command to event action
	eventAction := commandToEventAction(command.Command)

	// Determine event state (current state of resource after action)
	eventState := commandToState(command.Command, status)
	// Determine prior state implied by the command. We don't query the cloud
	// provider for the actual prior state — we record the state the command
	// expects to transition from (e.g. "stop" implies the resource was started).
	eventPrevState := commandToPrevState(command.Command, status)

	// Insert audit record
	query := `
		INSERT INTO audit (
			user_id, tenant_id, account_id, event_time, event_category, event_type,
			event_prev_state, event_state, event_actor, event_target, event_action,
			event_status, transaction_id, event_attr
		) VALUES (
			$1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14
		)`

	_, err = dbm.Exec(
		query,
		valueOrNil(userID),       // user_id - nullable UUID (nil if empty)
		tenantID,                 // tenant_id
		account.ID,               // account_id
		time.Now().UTC(),         // event_time
		EventCategoryAccount,     // event_category
		"RESOURCE_ACTION",        // event_type
		eventPrevState,           // event_prev_state - state implied by the command before execution
		eventState,               // event_state - resulting state
		EventActorCloudCollector, // event_actor - using constant now
		targetResourceID,         // event_target - the resource acted upon
		eventAction,              // event_action
		status,                   // event_status
		"",                       // transaction_id - can be populated if needed
		string(eventAttrJSON),    // event_attr - full action details
	)

	if err != nil {
		ctx.GetLogger().Error("failed to insert audit record", "error", err)
		return fmt.Errorf("failed to insert audit record: %w", err)
	}

	return nil
}

// CommandStatus represents the execution state of a single command.
type CommandStatus string

const (
	CommandStatusSuccess     CommandStatus = "SUCCESS"
	CommandStatusFailed      CommandStatus = "FAILED"
	CommandStatusNotExecuted CommandStatus = "NOT_EXECUTED"
)

// CommandExecutionResult holds the result of a single command in a batch execution.
type CommandExecutionResult struct {
	Command string        `json:"command"`
	Status  CommandStatus `json:"status"`
	Output  string        `json:"output,omitempty"`
	Error   string        `json:"error,omitempty"`
}

// LogCliBatchCommand logs a batch CLI command execution as a single audit record.
// event_attr stores {"recommendation_id": "...", "commands": [{command, output, error}, ...]}.
// status should be EventStatusSuccess only if every command succeeded.
func LogCliBatchCommand(
	ctx *security.RequestContext,
	accountId string,
	recommendationId string,
	resolutionId string,
	results []CommandExecutionResult,
	status EventStatus,
) error {
	var userID string
	var tenantID string

	if sc := ctx.GetSecurityContext(); sc != nil {
		userID = sc.GetUserId()
		tenantID = sc.GetTenantId()
	}

	eventAttr := map[string]interface{}{
		"commands": results,
	}
	if recommendationId != "" {
		eventAttr["recommendation_id"] = recommendationId
	}
	if resolutionId != "" {
		eventAttr["resolution_id"] = resolutionId
	}

	eventAttrJSON, err := json.Marshal(eventAttr)
	if err != nil {
		ctx.GetLogger().Error("failed to marshal batch event attributes", "error", err)
		eventAttrJSON = []byte("{}")
	}

	dbm, err := common.GetDatabaseManager(common.Metastore)
	if err != nil {
		return fmt.Errorf("failed to get database manager: %w", err)
	}

	eventState := "Command Execution Succeeded"
	if status == EventStatusFailure {
		eventState = "Command Execution Failed"
	}

	eventTarget := accountId
	if recommendationId != "" {
		eventTarget = recommendationId
	}

	query := `
		INSERT INTO audit (
			user_id, tenant_id, account_id, event_time, event_category, event_type,
			event_prev_state, event_state, event_actor, event_target, event_action,
			event_status, transaction_id, event_attr
		) VALUES (
			$1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14
		)`

	_, err = dbm.Exec(
		query,
		valueOrNil(userID),
		tenantID,
		accountId,
		time.Now().UTC(),
		EventCategoryCloudCommand,
		EventTypeCliExecute,
		"",
		eventState,
		EventActorCloudCollector,
		eventTarget,
		EventActionExecute,
		status,
		resolutionId,
		string(eventAttrJSON),
	)
	if err != nil {
		ctx.GetLogger().Error("failed to insert batch audit record for CLI commands", "error", err)
		return fmt.Errorf("failed to insert audit record: %w", err)
	}

	return nil
}

// commandToEventAction maps command strings to EventAction enum
func commandToEventAction(command string) EventAction {
	switch command {
	case "start", "stop", "reboot", "restart", "pause", "resume", "redeploy", "scale", "run_command":
		return EventActionUpdate
	case "delete":
		return EventActionDelete
	default:
		return EventActionUpdate
	}
}

// commandToState maps command and status to resulting state
func commandToState(command string, status EventStatus) string {
	if status == EventStatusFailure {
		return "FAILED"
	}

	switch strings.ToLower(command) {
	case "start", "resume":
		return "STARTED"
	case "stop", "pause":
		return "STOPPED"
	case "reboot", "restart":
		return "REBOOTING"
	case "delete":
		return "DELETED"
	case "scale":
		return "SCALING"
	case "redeploy", "force_redeploy":
		return "DEPLOYING"
	case "run_command":
		return "COMMAND_EXECUTED"
	default:
		return "UPDATED"
	}
}

// commandToPrevState returns the state the resource is expected to have been
// in before the command was issued. This is derived from the command (we do
// not query the provider for the actual prior state). On failure the resource
// did not transition, so the implied prior state is the same value we use for
// the success case — that gives consumers a sensible "intended-from" value
// for the audit diff.
func commandToPrevState(command string, _ EventStatus) string {
	switch strings.ToLower(command) {
	case "start", "resume":
		return "STOPPED"
	case "stop", "pause", "reboot", "restart", "redeploy", "force_redeploy", "scale", "run_command":
		return "STARTED"
	case "delete":
		return "EXISTING"
	default:
		return ""
	}
}
