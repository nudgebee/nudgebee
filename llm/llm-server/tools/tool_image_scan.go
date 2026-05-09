package tools

import (
	"database/sql"
	"errors"
	"fmt"
	"math/rand"
	"nudgebee/llm/common"
	"nudgebee/llm/security"
	"nudgebee/llm/services_server"
	"nudgebee/llm/tools/core"
	"strings"
	"time"

	"golang.org/x/exp/slog"
)

const ToolTriggerImageScan = "trigger_image_scan"
const scanTriggeredEmptyResponse = "Scan triggered but received empty response"

func init() {
	core.RegisterNBToolFactory(ToolTriggerImageScan, func(accountId string) (core.NBTool, error) {
		return ImageScanTool{}, nil
	})
}

type ImageScanTool struct {
}

func (m ImageScanTool) Name() string {
	return ToolTriggerImageScan
}

func (m ImageScanTool) GetType() core.NBToolType {
	return core.NBToolTypeTool
}

func (m ImageScanTool) Description() string {
	return `Triggers a Image scan on the container image associated with a specified workload in a Kubernetes cluster.

**Usage:**

* Use this tool when you need to assess the security of a workload's container image.
* **Input:** workload_name, and namespace.
* **Output:** Returns the status of the scan.

**Example Input:**
{
	"workload_name": "frontend",
	"namespace": "default"
}

**Important Notes:**
* The workload name and namespace must be valid.
* Use the returned status to guide further action or suggest remediation.
`
}

func (m ImageScanTool) InputSchema() core.ToolSchema {
	return core.ToolSchema{
		Type: core.ToolSchemaTypeObject,
		Properties: map[string]core.ToolSchemaProperty{
			"command": {
				Type:        core.ToolSchemaTypeString,
				Description: "Details to scan image",
			},
		},
		Required: []string{"command"},
	}
}

// AgentTaskResult holds the status and response from agent task polling
type AgentTaskResult struct {
	Status   string
	Response string
}

func (m ImageScanTool) Call(nbRequestContext core.NbToolContext, input core.NBToolCallRequest) (core.NBToolResponse, error) {
	startTime := time.Now()
	command := strings.TrimSpace(input.Command)
	if command == "" {
		return core.NBToolResponse{}, fmt.Errorf("empty command")
	}
	var inputMap map[string]any
	err := common.UnmarshalJson([]byte(command), &inputMap)
	if err != nil {
		return core.NBToolResponse{}, fmt.Errorf("failed to parse command: %w", err)
	}
	workloadName, ok := inputMap["workload_name"].(string)
	if !ok {
		return core.NBToolResponse{}, fmt.Errorf("workload_name is required")
	}
	namespace, ok := inputMap["namespace"].(string)
	if !ok {
		return core.NBToolResponse{}, fmt.Errorf("namespace is required")
	}

	// Security validation to prevent SQL injection and ensure valid K8s names
	if !common.IsValidKubernetesName(workloadName) {
		return core.NBToolResponse{}, fmt.Errorf("invalid workload_name: must strictly follow DNS-1123 label standard (lowercase alphanumeric and hyphens)")
	}
	if !common.IsValidKubernetesName(namespace) {
		return core.NBToolResponse{}, fmt.Errorf("invalid namespace: must strictly follow DNS-1123 label standard (lowercase alphanumeric and hyphens)")
	}

	tenantId, err := security.GetTenantIdFromAccountId(nbRequestContext.AccountId)
	if err != nil {
		fmt.Println("Error getting tenant ID:", err)
		return core.NBToolResponse{}, err
	}

	nbRequestContext.Ctx.GetLogger().Info("security: triggering image scan", "accountId", nbRequestContext.AccountId, "workloadName", workloadName, "namespace", namespace)

	response, err := TriggerImageScanApi(tenantId, nbRequestContext.AccountId, nbRequestContext.UserId, workloadName, namespace)
	if err != nil {
		nbRequestContext.Ctx.GetLogger().Error("Unable to trigger Image Scan", "error", err.Error())
		return core.NBToolResponse{
			Data:   "",
			Status: core.NBToolResponseStatusError,
		}, err
	}

	agentResult, err := pollAgentTaskStatus(nbRequestContext, workloadName, namespace, startTime)
	if err != nil {
		nbRequestContext.Ctx.GetLogger().Error("Error during agent task polling", "error", err.Error())
		return core.NBToolResponse{
			Data:   response,
			Status: core.NBToolResponseStatusSuccess,
		}, err
	}

	// Process the agent status for additional operations
	if agentResult != nil {
		return m.handleAgentStatus(nbRequestContext, agentResult.Status, agentResult.Response, workloadName, namespace, startTime)
	}

	return core.NBToolResponse{
		Data:       response,
		Type:       core.NBToolResponseTypeText,
		Status:     core.NBToolResponseStatusSuccess,
		References: []core.NBToolResponseReference{core.GetNudgebeeUIReferenceForClusterDetails(nbRequestContext, []string{"security" + "image-scan"}, "Check Scurity Vulnerabilities", nil, "")},
	}, nil
}

// handlePollingResult processes the result of a single polling attempt
func handlePollingResult(nbRequestContext core.NbToolContext, status, response string, found bool, err error, attempt, maxAttempts int) (*AgentTaskResult, bool, error) {
	if err != nil {
		nbRequestContext.Ctx.GetLogger().Error("Error querying agent task status", "error", err.Error(), "attempt", attempt+1)
		if attempt == maxAttempts-1 {
			return nil, false, err
		}
		return nil, true, nil // Continue polling
	}

	if !found {
		nbRequestContext.Ctx.GetLogger().Info("No agent task status found", "attempt", attempt+1)
		if attempt == maxAttempts-1 {
			return nil, false, fmt.Errorf("no agent task status found after maximum attempts")
		}
		return nil, true, nil // Continue polling
	}

	nbRequestContext.Ctx.GetLogger().Info("Agent task status fetched", "status", status, "response", response, "attempt", attempt+1)
	if isTaskCompleted(status) {
		nbRequestContext.Ctx.GetLogger().Info("Agent task completed", "status", status, "attempt", attempt+1)
		return &AgentTaskResult{Status: status, Response: response}, false, nil // Task completed
	}

	nbRequestContext.Ctx.GetLogger().Info("Agent task still in progress", "status", status, "attempt", attempt+1)
	return nil, true, nil // Continue polling
}

// calculateBackoffWaitTime calculates the wait time with exponential backoff and jitter
func calculateBackoffWaitTime(currentInterval time.Duration, jitterFactor float64, backoffMultiplier float64, maxInterval time.Duration) (time.Duration, time.Duration) {
	// Add jitter: ±jitterFactor% of current interval
	jitter := time.Duration(float64(currentInterval) * jitterFactor * (2*rand.Float64() - 1))
	waitTime := currentInterval + jitter

	// Calculate next interval for exponential backoff
	nextInterval := time.Duration(float64(currentInterval) * backoffMultiplier)
	if nextInterval > maxInterval {
		nextInterval = maxInterval
	}

	return waitTime, nextInterval
}

// pollAgentTaskStatus polls the agent_task table until the scan task is completed
// Uses exponential backoff with jitter to reduce resource usage and avoid thundering herd
// Returns the final agent status and response (for FAILED status) and any error
func pollAgentTaskStatus(nbRequestContext core.NbToolContext, workloadName, namespace string, startTime time.Time) (*AgentTaskResult, error) {
	// Exponential backoff configuration
	maxAttempts := 20                  // Reduced attempts due to longer intervals
	initialInterval := 2 * time.Second // Start with 2 seconds
	maxInterval := 30 * time.Second    // Cap at 30 seconds
	backoffMultiplier := 1.5           // Gentler exponential growth
	jitterFactor := 0.1                // Add 10% jitter to avoid thundering herd

	currentInterval := initialInterval

	for attempt := range maxAttempts {
		status, response, found, err := queryAgentTaskStatus(nbRequestContext, workloadName, namespace, startTime, attempt)

		result, shouldContinue, resultErr := handlePollingResult(nbRequestContext, status, response, found, err, attempt, maxAttempts)
		if !shouldContinue {
			return result, resultErr
		}

		// Wait with backoff before next attempt (except on last attempt)
		if attempt < maxAttempts-1 {
			waitTime, nextInterval := calculateBackoffWaitTime(currentInterval, jitterFactor, backoffMultiplier, maxInterval)
			nbRequestContext.Ctx.GetLogger().Info("Waiting before next polling attempt", "waitTime", waitTime, "baseInterval", currentInterval, "nextAttempt", attempt+2)
			time.Sleep(waitTime)
			currentInterval = nextInterval
		}
	}

	nbRequestContext.Ctx.GetLogger().Warn("Agent task polling timed out after maximum attempts", "maxAttempts", maxAttempts)
	return &AgentTaskResult{Status: "TIMEOUT", Response: ""}, nil
}

// queryAgentTaskStatus executes a parameterized SQL query and returns the status, response, whether found, and any error
func queryAgentTaskStatus(nbRequestContext core.NbToolContext, workloadName, namespace string, startTime time.Time, attempt int) (string, string, bool, error) {
	dbManager, err := common.GetDatabaseManager(common.Metastore)
	if err != nil {
		nbRequestContext.Ctx.GetLogger().Error("Unable to get database manager", "error", err.Error(), "attempt", attempt+1)
		return "", "", false, fmt.Errorf("queryAgentTaskStatus: failed to get database manager: %w", err)
	}

	const query = `SELECT status, response::text FROM agent_task
		WHERE cloud_account_id = $1
		AND payload -> 'action_params' ->> 'name' LIKE $2
		AND payload -> 'action_params' ->> 'namespace' = $3
		AND payload ->> 'action_name' = 'image_scanner'
		AND created_at >= $4
		ORDER BY updated_at DESC LIMIT 1`

	var status, response *string
	err = dbManager.Db.QueryRow(query, nbRequestContext.AccountId, workloadName+"%", namespace, startTime.UTC()).Scan(&status, &response)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			nbRequestContext.Ctx.GetLogger().Info("No agent task status found", "attempt", attempt+1)
			return "", "", false, nil
		}
		nbRequestContext.Ctx.GetLogger().Error("Unable to fetch agent task status", "error", err.Error(), "attempt", attempt+1)
		return "", "", false, fmt.Errorf("queryAgentTaskStatus: %w", err)
	}

	if status == nil {
		nbRequestContext.Ctx.GetLogger().Error("Status field is null in agent task response", "attempt", attempt+1)
		return "", "", false, fmt.Errorf("status field is null in agent task response")
	}

	nbRequestContext.Ctx.GetLogger().Info("Agent task data retrieved", "status", *status, "attempt", attempt+1)

	var responseStr string
	if response != nil {
		responseStr = *response
	}

	return *status, responseStr, true, nil
}

// isTaskCompleted checks if the task status indicates completion (not TODO or PROCESSING)
func isTaskCompleted(status string) bool {
	return status != "" && !strings.Contains(status, "TODO") && !strings.Contains(status, "PROCESSING")
}

// dummy function to check the invoking of tool_image_Scan
// func TriggerImageScanApi(s, workloadName, namespace string) (any, error) {
// 	return `{"ok","success"}`, nil
// }

func TriggerImageScanApi(tenantId, accountId, userId, workloadName, namespace string) (string, error) {
	// Construct the ServicesRequest payload
	request := services_server.ScanImageServiceRequest{
		Action: services_server.Action{
			Name: "security_scan_image",
		},
		Input: services_server.ScanImageRequest{
			AccountId: accountId,
			Namespace: namespace,
			Workload:  workloadName,
		},
		SessionVariables: services_server.SessionVariables{
			UserID:       userId,
			UserTenantID: tenantId,
		},
	}

	// Call the Execute function
	result, err := services_server.ExecuteScanImageQuery(request)
	if err != nil {
		return "", fmt.Errorf("failed to trigger image scan: %w", err)
	}
	// Check if the result is empty
	if len(result) == 0 {
		return "", fmt.Errorf("no data returned from scan image query")
	}

	// check if the result contains the "result" key
	if _, ok := result["result"]; !ok {
		slog.Error("trigger_image_scan: result key not found in response", "result", result)
		return scanTriggeredEmptyResponse, nil
	}
	// Get the JSON string from the map
	jsonStr := result["result"]

	// Define a structure to hold the parsed data
	var parsedData []struct {
		Image string `json:"image"`
	}

	// Parse the JSON
	if err := common.UnmarshalJson([]byte(jsonStr), &parsedData); err != nil {
		slog.Error("trigger_image_scan: error parsing JSON response", "error", err)
		return scanTriggeredEmptyResponse, nil
	}

	// Extract all image values into a slice
	images := make([]string, len(parsedData))
	for i, item := range parsedData {
		images[i] = item.Image
	}

	// Join the images with commas
	imageList := strings.Join(images, ",")
	if imageList == "" {
		return scanTriggeredEmptyResponse, nil
	}
	return fmt.Sprintf("Scan triggered successfully for %s.\n Image/s: %s", workloadName, imageList), nil

}

// handleAgentStatus processes the final agent status and performs appropriate actions
// Returns the final NBToolResponse based on the agent status
// For FAILED status, includes the response message in the error details
func (m ImageScanTool) handleAgentStatus(nbRequestContext core.NbToolContext, agentStatus, agentResponse, workloadName, namespace string, startTime time.Time) (core.NBToolResponse, error) {
	nbRequestContext.Ctx.GetLogger().Info("Agent task final status", "status", agentStatus, "response", agentResponse, "workloadName", workloadName, "namespace", namespace)

	switch agentStatus {
	case "COMPLETED":
		securityQuery := SecurityView + ` AND cr.cloud_account_id = $1::uuid AND cr.workload_name = $2 AND cr.namespace = $3 AND r.updated_at >= $4 LIMIT 10`
		dbManager, err := common.GetDatabaseManager(common.Metastore)
		if err != nil {
			return core.NBToolResponse{
				Data:   fmt.Sprintf("Scan for workload '%s' in namespace '%s' completed, but unable to fetch security view.", workloadName, namespace),
				Status: core.NBToolResponseStatusError,
			}, fmt.Errorf("handleAgentStatus: failed to get database manager: %w", err)
		}
		var data []map[string]any
		err = dbManager.QueryAndScan(&data, securityQuery, nbRequestContext.AccountId, workloadName, namespace, startTime.UTC())
		if err != nil {
			nbRequestContext.Ctx.GetLogger().Error("Unable to fetch security view", "error", err.Error(), "workloadName", workloadName, "namespace", namespace)
			return core.NBToolResponse{
				Data:   fmt.Sprintf("Scan for workload '%s' in namespace '%s' completed, but unable to fetch security view.", workloadName, namespace),
				Status: core.NBToolResponseStatusError,
			}, fmt.Errorf("scan for workload '%s' in namespace '%s' completed, but unable to fetch security view: %w", workloadName, namespace, err)
		}
		if len(data) == 0 {
			nbRequestContext.Ctx.GetLogger().Info("No security findings found for the scanned workload", "workloadName", workloadName, "namespace", namespace)
			return core.NBToolResponse{
				Data:   fmt.Sprintf("Scan for workload '%s' in namespace '%s' completed, but no security findings found.", workloadName, namespace),
				Status: core.NBToolResponseStatusSuccess,
			}, nil
		}
		bytesData, err := common.MarshalJson(data)
		if err != nil {
			return core.NBToolResponse{Status: core.NBToolResponseStatusError}, fmt.Errorf("handleAgentStatus: marshal error: %w", err)
		}
		nbRequestContext.Ctx.GetLogger().Info("Security findings found for the scanned workload", "workloadName", workloadName, "namespace", namespace)
		return core.NBToolResponse{
			Data:       string(bytesData),
			Type:       core.NBToolResponseTypeTable,
			Status:     core.NBToolResponseStatusSuccess,
			References: []core.NBToolResponseReference{core.GetNudgebeeUIReferenceForClusterDetails(nbRequestContext, []string{"security", "image-scan"}, "Check Scurity Vulnerabilities", nil, "")},
		}, nil
	case "FAILED":
		errorMsg := fmt.Sprintf("Scan for workload '%s' in namespace '%s' failed.", workloadName, namespace)
		if agentResponse != "" {
			errorMsg += fmt.Sprintf(" Error details: %s", agentResponse)
		}
		return core.NBToolResponse{
			Data:   errorMsg,
			Status: core.NBToolResponseStatusError,
		}, fmt.Errorf("scan for workload '%s' in namespace '%s' failed: %s", workloadName, namespace, agentResponse)
	case "TIMEOUT":
		return core.NBToolResponse{
			Data:   fmt.Sprintf("Scan for workload '%s' in namespace '%s' timed out.", workloadName, namespace),
			Status: core.NBToolResponseStatusError,
		}, fmt.Errorf("scan for workload '%s' in namespace '%s' timed out", workloadName, namespace)
	default:
		nbRequestContext.Ctx.GetLogger().Warn("Unexpected Image Scan status", "status", agentStatus, "workloadName", workloadName, "namespace", namespace)
		return core.NBToolResponse{
			Data:   fmt.Sprintf("Unexpected Image Scan status '%s' for workload '%s' in namespace '%s'.", agentStatus, workloadName, namespace),
			Status: core.NBToolResponseStatusError,
		}, fmt.Errorf("unexpected Image Scan status '%s' for workload '%s' in namespace '%s'", agentStatus, workloadName, namespace)
	}
}

func (m ImageScanTool) InferToolRequestTypePrompt(ctx *security.RequestContext, toolName, input string) (string, error) {
	// This tool only triggers a scan, which is considered a 'read' operation as it doesn't change any state.
	return "read", nil
}
