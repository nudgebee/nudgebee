package playbooks

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"nudgebee/services/internal/database"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

type deploymentHistoryAction struct{}

type deploymentHistoryResponse struct {
	ServiceName      string                   `json:"service_name"`
	RolloutName      string                   `json:"rollout_name"`
	Namespace        string                   `json:"namespace"`
	DeploymentsFound int                      `json:"deployments_found"`
	TimeRangeHours   int                      `json:"time_range_hours"`
	Deployments      []deploymentEventSummary `json:"deployments"`
	Title            string                   `json:"title"`
	Description      string                   `json:"description"`
	FindingType      string                   `json:"finding_type"`
	Priority         string                   `json:"priority"`
}

type deploymentEventSummary struct {
	EventId            string    `json:"event_id"`
	Timestamp          time.Time `json:"timestamp"`
	Title              string    `json:"title"`
	Description        string    `json:"description"`
	FindingType        string    `json:"finding_type"`
	Priority           string    `json:"priority"`
	TimeBeforeEvent    string    `json:"time_before_event,omitempty"`
	DeploymentStrategy string    `json:"deployment_strategy,omitempty"` // canary, blue-green, rolling
	CanarySteps        string    `json:"canary_steps,omitempty"`        // e.g., "10% → 30min pause"
	RolloutStatus      string    `json:"rollout_status,omitempty"`      // aborted, paused, completed, updated
}

func (a *deploymentHistoryAction) CanAutoExecute(ctx PlaybookActionContext) bool {
	// Can auto-execute when event has a subject_name
	return ctx.GetEvent().SubjectName != ""
}

func (a *deploymentHistoryAction) AutoExecute(ctx PlaybookActionContext) (PlaybookActionResponse, error) {
	subjectName := ctx.GetEvent().SubjectName
	if subjectName == "" {
		return nil, errors.New("no subject_name in event")
	}

	accountId := ctx.GetAccountId()

	// Try to get namespace from SubjectNamespace first, then labels
	namespace := ctx.GetEvent().SubjectNamespace
	if namespace == "" && ctx.GetEvent().Labels != nil {
		if ns, ok := ctx.GetEvent().Labels["namespace"]; ok {
			namespace = ns
		}
	}

	// Step 1: Try using subject_name directly as rollout name. For a Pod
	// subject (Go-agent trigger findings), prefer subject_owner — the Pod
	// name has a hash suffix that won't match the StatefulSet/Deployment
	// row in k8s_workloads.
	rolloutName := subjectName
	if owner := ctx.GetEvent().SubjectOwner; owner != "" {
		rolloutName = owner
	}

	// Step 2: Verify by querying events table
	eventStartTime := time.Now()
	if ctx.GetEvent().StartedAt != nil {
		eventStartTime = *ctx.GetEvent().StartedAt
	}

	deployments, err := queryDeploymentEvents(accountId, rolloutName, namespace, eventStartTime, 5)

	// Step 3: If no deployments found, use fallback to find rollout name from k8s_workloads.
	// Robusta findings carry "service" in labels; Go-agent findings carry the same
	// info under "target_service" (set by the api-server's knowledge-graph enrichment).
	if err != nil || len(deployments) == 0 {
		serviceLabel := ""
		if ctx.GetEvent().Labels != nil {
			serviceLabel = ctx.GetEvent().Labels["service"]
			if serviceLabel == "" {
				serviceLabel = ctx.GetEvent().Labels["target_service"]
			}
		}
		if serviceLabel != "" {
			slog.Info("No deployments found with subject_name directly, trying fallback",
				"subject_name", subjectName, "account_id", accountId)

			foundRolloutName, foundNamespace, fallbackErr := findRolloutNameFromWorkload(accountId, serviceLabel)
			if fallbackErr == nil && foundRolloutName != "" {
				rolloutName = foundRolloutName
				if foundNamespace != "" {
					namespace = foundNamespace
				}
			}
		}
	}

	// Execute with discovered rollout name
	params := map[string]any{
		"service_name": subjectName,
		"rollout_name": rolloutName,
		"namespace":    namespace,
	}

	return a.Execute(ctx, params)
}

func (a *deploymentHistoryAction) Execute(ctx PlaybookActionContext, rawParams map[string]any) (PlaybookActionResponse, error) {
	// Parse parameters
	serviceName, _ := rawParams["service_name"].(string)
	rolloutName, _ := rawParams["rollout_name"].(string)
	namespace, _ := rawParams["namespace"].(string)
	accountId := ctx.GetAccountId()

	if rolloutName == "" {
		rolloutName = serviceName
	}

	if rolloutName == "" {
		return nil, errors.New("rollout_name or service_name is required")
	}

	// Get max deployments (default: 5)
	maxDeployments := 5
	if maxDep, ok := rawParams["max_deployments"].(int); ok && maxDep > 0 {
		maxDeployments = maxDep
	}

	// Get event start time
	eventStartTime := time.Now()
	if ctx.GetEvent().StartedAt != nil {
		eventStartTime = *ctx.GetEvent().StartedAt
	}

	// Query deployment events
	deployments, err := queryDeploymentEvents(accountId, rolloutName, namespace, eventStartTime, maxDeployments)
	if err != nil {
		return nil, fmt.Errorf("failed to query deployment events: %w", err)
	}

	// Calculate time before event for each deployment
	for i := range deployments {
		if ctx.GetEvent().StartedAt != nil {
			timeBefore := eventStartTime.Sub(deployments[i].Timestamp)
			deployments[i].TimeBeforeEvent = formatDuration(timeBefore)
		}
	}

	// Build response
	response := deploymentHistoryResponse{
		ServiceName:      serviceName,
		RolloutName:      rolloutName,
		Namespace:        namespace,
		DeploymentsFound: len(deployments),
		TimeRangeHours:   12,
		Deployments:      deployments,
	}

	if len(deployments) == 0 {
		return nil, nil
	}
	// Generate insights
	insights := generateDeploymentInsights(deployments, ctx.GetEvent().StartedAt)

	// Generate labels for correlation
	labels := map[string]any{
		"deployment_count": len(deployments),
		"rollout_name":     rolloutName,
	}

	if len(deployments) > 0 {
		labels["last_deployment_time"] = deployments[0].Timestamp.Format(time.RFC3339)
		labels["first_deployment_time"] = deployments[len(deployments)-1].Timestamp.Format(time.RFC3339)
	}

	metadata := map[string]any{
		"query-result-version": "1.0",
		"query":                rawParams,
		"time_range_hours":     12,
	}

	additionalInfo := map[string]any{
		"deployments_queried": maxDeployments,
		"deployments_found":   len(deployments),
	}

	return NewPlaybookActionResponseJsonWithLabels(response, additionalInfo, insights, metadata, labels), nil
}

// findRolloutNameFromWorkload queries k8s_workloads to find rollout name using service label matching
func findRolloutNameFromWorkload(accountId, serviceName string) (rolloutName, namespace string, err error) {
	dbms, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		return "", "", fmt.Errorf("failed to get database manager: %w", err)
	}

	query := `
		SELECT
			name,
			namespace
		FROM k8s_workloads ksw
		WHERE ksw.cloud_account_id = $1
		  AND (ksw.labels->>'service'::text = $2
		       OR ksw.labels->>'tags.datadoghq.com/service'::text = $2)
		  AND is_active IS NOT FALSE
		LIMIT 1
	`

	err = dbms.Db.QueryRowx(query, accountId, serviceName).Scan(&rolloutName, &namespace)
	if err != nil {
		return "", "", fmt.Errorf("failed to find rollout name from workload: %w", err)
	}

	return rolloutName, namespace, nil
}

// queryDeploymentEvents queries the events table for rollout deployment history
func queryDeploymentEvents(accountId, rolloutName, namespace string, eventStartTime time.Time, limit int) ([]deploymentEventSummary, error) {
	dbms, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		return nil, fmt.Errorf("failed to get database manager: %w", err)
	}

	// Calculate time range: 12 hours before event start time
	startTime := eventStartTime.Add(-12 * time.Hour)

	query := `
		SELECT
			id::text,
			starts_at,
			title,
			description,
			finding_type,
			priority,
			evidences
		FROM events
		WHERE cloud_account_id = $1
		  AND subject_name = $2
		  AND finding_type = 'configuration_change'
		  AND starts_at >= $3
		  AND starts_at <= $4
	`

	// Add namespace filter if provided
	args := []interface{}{accountId, rolloutName, startTime, eventStartTime}
	if namespace != "" {
		query += " AND subject_namespace = $5"
		args = append(args, namespace)
	}

	query += " ORDER BY starts_at DESC LIMIT $" + fmt.Sprintf("%d", len(args)+1)
	args = append(args, limit)

	rows, err := dbms.Db.Queryx(query, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to query deployment events: %w", err)
	}
	defer func() {
		if err := rows.Close(); err != nil {
			slog.Error("failed to close rows", "error", err)
		}
	}()

	var deployments []deploymentEventSummary
	for rows.Next() {
		var dep deploymentEventSummary
		var evidencesJSON string

		err := rows.Scan(
			&dep.EventId,
			&dep.Timestamp,
			&dep.Title,
			&dep.Description,
			&dep.FindingType,
			&dep.Priority,
			&evidencesJSON,
		)
		if err != nil {
			slog.Error("failed to scan deployment event row", "error", err)
			continue
		}

		// Parse evidences JSON only to extract deployment strategy, then discard
		var evidences []map[string]interface{}
		if evidencesJSON != "" {
			if err := json.Unmarshal([]byte(evidencesJSON), &evidences); err != nil {
				slog.Error("failed to parse evidences JSON", "error", err, "event_id", dep.EventId)
			}
		}

		// Extract deployment strategy from evidences
		dep.DeploymentStrategy, dep.CanarySteps = extractDeploymentStrategy(evidences)

		// Extract rollout status from title
		dep.RolloutStatus = extractRolloutStatus(dep.Title)

		deployments = append(deployments, dep)
	}

	return deployments, nil
}

// extractDeploymentStrategy extracts deployment strategy from evidences field
func extractDeploymentStrategy(evidences []map[string]interface{}) (strategy, canarySteps string) {
	strategy = "rolling" // default
	canarySteps = ""

	if len(evidences) == 0 {
		return
	}

	// Evidences contains array of evidence objects with "data" field
	for _, evidence := range evidences {
		data, ok := evidence["data"].(map[string]interface{})
		if !ok {
			continue
		}

		// Get "new" field which contains the YAML manifest as string
		newYAML, ok := data["new"].(string)
		if !ok {
			continue
		}

		// Parse YAML into structured data to avoid false positives from comments/annotations
		var manifest map[string]interface{}
		if err := yaml.Unmarshal([]byte(newYAML), &manifest); err != nil {
			slog.Debug("failed to parse YAML manifest", "error", err)
			continue
		}

		// Navigate to spec.strategy
		spec, ok := manifest["spec"].(map[string]interface{})
		if !ok {
			continue
		}

		strategyMap, ok := spec["strategy"].(map[string]interface{})
		if !ok {
			continue
		}

		// Check for canary strategy
		if canaryConfig, ok := strategyMap["canary"].(map[string]interface{}); ok {
			strategy = "canary"
			canarySteps = extractCanaryStepsFromConfig(canaryConfig)
			break
		}

		// Check for blueGreen strategy
		if _, ok := strategyMap["blueGreen"].(map[string]interface{}); ok {
			strategy = "blue-green"
			break
		}
	}

	return strategy, canarySteps
}

// extractCanaryStepsFromConfig extracts canary deployment steps from parsed canary configuration
func extractCanaryStepsFromConfig(canaryConfig map[string]interface{}) string {
	// Look for steps array in canary config
	stepsInterface, ok := canaryConfig["steps"]
	if !ok {
		return ""
	}

	steps, ok := stepsInterface.([]interface{})
	if !ok {
		return ""
	}

	var weight string
	var pauseDuration string

	// Iterate through steps to find setWeight and pause
	for _, stepInterface := range steps {
		step, ok := stepInterface.(map[string]interface{})
		if !ok {
			continue
		}

		// Check for setWeight
		if setWeight, ok := step["setWeight"]; ok {
			switch v := setWeight.(type) {
			case int:
				weight = fmt.Sprintf("%d%%", v)
			case float64:
				weight = fmt.Sprintf("%.0f%%", v)
			case string:
				weight = v
				if !strings.HasSuffix(weight, "%") {
					weight += "%"
				}
			}
		}

		// Check for pause with duration
		if pauseInterface, ok := step["pause"]; ok {
			if pause, ok := pauseInterface.(map[string]interface{}); ok {
				if duration, ok := pause["duration"].(string); ok {
					pauseDuration = duration
				}
			}
		}

		// If we found both, we can stop
		if weight != "" && pauseDuration != "" {
			break
		}
	}

	if weight != "" && pauseDuration != "" {
		return fmt.Sprintf("%s → %s pause", weight, pauseDuration)
	} else if weight != "" {
		return weight
	}

	return ""
}

// extractRolloutStatus extracts rollout status from event title
func extractRolloutStatus(title string) string {
	titleLower := strings.ToLower(title)

	if strings.Contains(titleLower, "rolloutaborted") || strings.Contains(titleLower, "aborted") {
		return "aborted"
	}
	if strings.Contains(titleLower, "rolloutpaused") || strings.Contains(titleLower, "paused") {
		return "paused"
	}
	if strings.Contains(titleLower, "rolloutcompleted") || strings.Contains(titleLower, "completed") {
		return "completed"
	}
	if strings.Contains(titleLower, "rolloutupdated") || strings.Contains(titleLower, "updated") {
		return "updated"
	}
	if strings.Contains(titleLower, "rolloutdegraded") || strings.Contains(titleLower, "degraded") {
		return "degraded"
	}

	return ""
}

// generateDeploymentInsights generates insights based on deployment history
func generateDeploymentInsights(deployments []deploymentEventSummary, eventStartTime *time.Time) []PlaybookActionResponseInsight {
	insights := []PlaybookActionResponseInsight{}

	if len(deployments) == 0 {
		insights = append(insights, PlaybookActionResponseInsight{
			Message:  "No deployments found in the 12 hours before the event",
			Severity: "info",
		})
		return insights
	}

	// Insight 1: Summary
	insights = append(insights, PlaybookActionResponseInsight{
		Message:  fmt.Sprintf("Found %d deployment(s) in the 12 hours before the event", len(deployments)),
		Severity: "info",
	})

	// Insight 2: Recent deployment warning (< 1 hour before event)
	if eventStartTime != nil {
		for _, dep := range deployments {
			timeBefore := eventStartTime.Sub(dep.Timestamp)
			if timeBefore > 0 && timeBefore < time.Hour {
				insights = append(insights, PlaybookActionResponseInsight{
					Message:  fmt.Sprintf("Deployment occurred %s before the event - potentially related", formatDuration(timeBefore)),
					Severity: "warning",
				})
				break // Only show warning for the most recent deployment
			}
		}
	}

	// Insight 3: Canary/Blue-Green deployment warnings
	for _, dep := range deployments {
		if dep.DeploymentStrategy == "canary" {
			msg := "Canary deployment detected"
			if dep.CanarySteps != "" {
				msg = fmt.Sprintf("Canary deployment detected (%s)", dep.CanarySteps)
			}
			if dep.TimeBeforeEvent != "" {
				msg += fmt.Sprintf(" - deployed %s before event", dep.TimeBeforeEvent)
			}
			insights = append(insights, PlaybookActionResponseInsight{
				Message:  msg,
				Severity: "warning",
			})
			break // Only show once
		} else if dep.DeploymentStrategy == "blue-green" {
			msg := "Blue-green deployment detected"
			if dep.TimeBeforeEvent != "" {
				msg += fmt.Sprintf(" - deployed %s before event", dep.TimeBeforeEvent)
			}
			insights = append(insights, PlaybookActionResponseInsight{
				Message:  msg,
				Severity: "warning",
			})
			break // Only show once
		}
	}

	// Insight 5: Rollout status issues (aborted, degraded, paused)
	for _, dep := range deployments {
		switch dep.RolloutStatus {
		case "aborted":
			msg := fmt.Sprintf("Rollout was aborted at %s", dep.Timestamp.Format("2006-01-02 15:04:05"))
			if dep.TimeBeforeEvent != "" {
				msg += fmt.Sprintf(" (%s before event)", dep.TimeBeforeEvent)
			}
			insights = append(insights, PlaybookActionResponseInsight{
				Message:  msg,
				Severity: "error",
			})
		case "degraded":
			msg := fmt.Sprintf("Rollout degraded at %s", dep.Timestamp.Format("2006-01-02 15:04:05"))
			if dep.TimeBeforeEvent != "" {
				msg += fmt.Sprintf(" (%s before event)", dep.TimeBeforeEvent)
			}
			insights = append(insights, PlaybookActionResponseInsight{
				Message:  msg,
				Severity: "error",
			})
		case "paused":
			msg := fmt.Sprintf("Rollout paused at %s", dep.Timestamp.Format("2006-01-02 15:04:05"))
			if dep.TimeBeforeEvent != "" {
				msg += fmt.Sprintf(" (%s before event)", dep.TimeBeforeEvent)
			}
			insights = append(insights, PlaybookActionResponseInsight{
				Message:  msg,
				Severity: "warning",
			})
		}
	}

	// Insight 6: Show change summaries from description
	for i, dep := range deployments {
		if i >= 3 {
			break // Only show first 3
		}
		if dep.Description != "" {
			timeStr := dep.Timestamp.Format("2006-01-02 15:04:05")
			insights = append(insights, PlaybookActionResponseInsight{
				Message:  fmt.Sprintf("Deployment at %s: %s", timeStr, dep.Description),
				Severity: "info",
			})
		}
	}

	return insights
}
