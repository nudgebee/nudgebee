package playbooks

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"nudgebee/services/common"
	"nudgebee/services/internal/database"
	"nudgebee/services/relay"
	"regexp"
	"time"
)

// argoCDAppNameRegex bounds the application name before it is interpolated into
// the `argocd app get <name>` shell command. ArgoCD app references are
// "[namespace/]name" with DNS-1123 characters, so allow alphanumerics, dash, dot,
// underscore, and a single slash — and nothing that could break out of the
// argument (spaces, ;, |, &, $, backticks, parentheses, …).
var argoCDAppNameRegex = regexp.MustCompile(`^[a-zA-Z0-9._-]+(/[a-zA-Z0-9._-]+)?$`)

type argoCDHistoryAction struct{}

type argoCDHistoryParams struct {
	ApplicationName string `json:"application_name,omitempty"`
	AccountId       string `json:"account_id,omitempty"`
	IntegrationName string `json:"integration_name,omitempty"` // Optional: specific integration config name
}

// ArgoCD API response structures
type argoCDApplicationStatus struct {
	Sync           argoCDSyncStatus      `json:"sync"`
	Health         argoCDHealthStatus    `json:"health"`
	OperationState *argoCDOperationState `json:"operationState,omitempty"`
}

type argoCDSyncStatus struct {
	Status     string           `json:"status"`
	Revision   string           `json:"revision"`
	ComparedTo argoCDComparedTo `json:"comparedTo"`
}

type argoCDComparedTo struct {
	Source      argoCDSource      `json:"source"`
	Destination argoCDDestination `json:"destination"`
}

type argoCDSource struct {
	RepoURL        string `json:"repoURL"`
	Path           string `json:"path"`
	TargetRevision string `json:"targetRevision"`
	Ref            string `json:"ref,omitempty"`
}

type argoCDDestination struct {
	Server    string `json:"server"`
	Namespace string `json:"namespace"`
}

type argoCDHealthStatus struct {
	Status  string `json:"status"`
	Message string `json:"message,omitempty"`
}

type argoCDMetadata struct {
	Name              string            `json:"name"`
	Namespace         string            `json:"namespace"`
	CreationTimestamp time.Time         `json:"creationTimestamp"`
	Labels            map[string]string `json:"labels,omitempty"`
}

type argoCDSpec struct {
	Source      argoCDSource      `json:"source"`
	Destination argoCDDestination `json:"destination"`
	Project     string            `json:"project"`
}

type argoCDOperationState struct {
	Phase      string     `json:"phase"`
	Message    string     `json:"message,omitempty"`
	StartedAt  time.Time  `json:"startedAt"`
	FinishedAt *time.Time `json:"finishedAt,omitempty"`
}

// ArgoCD history entry structure
type argoCDHistoryEntry struct {
	ID            int64     `json:"id"`
	Revision      string    `json:"revision"`
	DeployedAt    time.Time `json:"deployedAt"`
	DeployStarted time.Time `json:"deployStartedAt"`
	Source        struct {
		RepoURL        string `json:"repoURL"`
		Path           string `json:"path"`
		TargetRevision string `json:"targetRevision"`
	} `json:"source"`
}

type argoCDApplication struct {
	Metadata argoCDMetadata          `json:"metadata"`
	Spec     argoCDSpec              `json:"spec"`
	Status   argoCDApplicationStatus `json:"status"`
}

// Deployment history entry for response
type deploymentHistoryEntry struct {
	ID             int64     `json:"id"`
	Revision       string    `json:"revision"`
	ShortRevision  string    `json:"short_revision"`
	DeployedAt     time.Time `json:"deployed_at"`
	DeployStarted  time.Time `json:"deploy_started_at"`
	RepoURL        string    `json:"repo_url"`
	Path           string    `json:"path"`
	TargetRevision string    `json:"target_revision"`
	TimeSinceEvent string    `json:"time_since_event,omitempty"` // e.g., "2 hours before event"
	IsRelevant     bool      `json:"is_relevant"`                // true if deployment is close to event time
}

// Response structure for the action
type argoCDHistoryResponse struct {
	ApplicationName      string                   `json:"application_name"`
	Namespace            string                   `json:"namespace"`
	Project              string                   `json:"project"`
	CurrentRevision      string                   `json:"current_revision"`
	SyncStatus           string                   `json:"sync_status"`
	HealthStatus         string                   `json:"health_status"`
	HealthMessage        string                   `json:"health_message,omitempty"`
	CreatedAt            time.Time                `json:"created_at"`
	LastSyncedAt         *time.Time               `json:"last_synced_at,omitempty"`
	RepoURL              string                   `json:"repo_url"`
	Path                 string                   `json:"path"`
	TargetRevision       string                   `json:"target_revision"`
	Ref                  string                   `json:"ref,omitempty"`
	DestinationServer    string                   `json:"destination_server"`
	DestinationNamespace string                   `json:"destination_namespace"`
	Labels               map[string]string        `json:"labels,omitempty"`
	DeploymentStrategy   string                   `json:"deployment_strategy,omitempty"`
	DeploymentType       string                   `json:"deployment_type,omitempty"`
	DeploymentHistory    []deploymentHistoryEntry `json:"deployment_history,omitempty"` // Recent deployment history
	DeploymentContext    map[string]interface{}   `json:"deployment_context,omitempty"` // Contextual insights
}

func (a *argoCDHistoryAction) CanAutoExecute(ctx PlaybookActionContext) bool {
	// Skip deployment history for AWS CloudWatch alarms - they're infrastructure alerts, not deployment-related
	labels := ctx.GetEvent().Labels
	if labels["aws_region"] != "" || labels["aws_account"] != "" || ctx.GetEvent().Source == "AWS_CloudWatch_Alarm" {
		return false
	}

	_, _, _, _, err := fetchArgoCDIntegration(ctx.GetAccountId(), "")
	return err == nil
}

func (a *argoCDHistoryAction) AutoExecute(ctx PlaybookActionContext) (PlaybookActionResponse, error) {

	dbms, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		return nil, fmt.Errorf("failed to get database manager: %w", err)
	}

	serviceName := ctx.GetEvent().SubjectName
	if ctx.GetEvent().Labels["service"] != "" {
		serviceName = ctx.GetEvent().Labels["service"]
	}

	// Match by service label (using serviceName), subject name as service label, or workload name
	query := `
		SELECT COALESCE(meta-> 'config' -> 'labels' ->> 'argocd.argoproj.io/instance', '')::text AS argo_instance_name
		FROM k8s_workloads ksw
		WHERE ksw.cloud_account_id = $1
		AND (ksw.labels ->> 'service'::text = $2
			OR ksw.labels ->> 'tags.datadoghq.com/service'::text = $2
			OR ksw.labels ->> 'service'::text = $3
			OR ksw.labels ->> 'tags.datadoghq.com/service'::text = $3
			OR ksw.name = $3)
		AND is_active IS NOT FALSE LIMIT 1`
	var argoInstanceName string
	err = dbms.Db.QueryRowx(query, ctx.GetAccountId(), serviceName, ctx.GetEvent().SubjectName).Scan(&argoInstanceName)
	if err != nil {
		// Workload not found in k8s_workloads — expected for non-k8s resources
		return nil, nil
	}

	if argoInstanceName == "" {
		// Workload exists but has no ArgoCD label — not managed by ArgoCD
		return nil, nil
	}

	params := map[string]any{
		"application_name": argoInstanceName,
	}

	return a.Execute(ctx, params)
}

func (a *argoCDHistoryAction) Execute(ctx PlaybookActionContext, rawParams map[string]any) (PlaybookActionResponse, error) {
	var params argoCDHistoryParams
	err := common.UnmarshalMapToStruct(rawParams, &params)
	if err != nil {
		return nil, err
	}

	if params.ApplicationName == "" {
		return nil, errors.New("application_name is required")
	}
	// application_name is interpolated into the `argocd app get` shell command —
	// reject anything that isn't a plain "[namespace/]name" to prevent injection.
	if !argoCDAppNameRegex.MatchString(params.ApplicationName) {
		return nil, fmt.Errorf("invalid application_name %q: must contain only alphanumeric characters, dashes, dots, underscores, or a single slash", params.ApplicationName)
	}

	if params.AccountId == "" {
		params.AccountId = ctx.GetAccountId()
	}

	if params.AccountId == "" {
		return nil, errors.New("account_id is required")
	}

	// Fetch ArgoCD integration config from database
	secretName, integrationName, authMethod, insecure, err := fetchArgoCDIntegration(params.AccountId, params.IntegrationName)
	if err != nil {
		return nil, err
	}

	// Build the argocd CLI command to fetch ArgoCD application details. The server
	// URL and credentials live in the k8s secret under the standard ArgoCD CLI
	// env-var keys, so nothing sensitive is exposed in the process list and there
	// is a single source of truth (the secret).
	insecureFlag := ""
	if insecure {
		insecureFlag = " --insecure"
	}
	appGetCmd := fmt.Sprintf(
		`argocd app get %s --server "$ARGOCD_SERVER"%s --grpc-web --output json`,
		params.ApplicationName,
		insecureFlag,
	)

	// Mirror the Test Connection probe's auth handling: token auth reads
	// $ARGOCD_AUTH_TOKEN directly; password auth must `argocd login` first.
	var argoCDCmd string
	var envFromSecret map[string]string
	if authMethod == "password" {
		envFromSecret = map[string]string{
			"ARGOCD_SERVER":   "ARGOCD_SERVER",
			"ARGOCD_USERNAME": "ARGOCD_USERNAME",
			"ARGOCD_PASSWORD": "ARGOCD_PASSWORD",
		}
		argoCDCmd = fmt.Sprintf(
			`argocd login "$ARGOCD_SERVER"%s --grpc-web --username "$ARGOCD_USERNAME" --password "$ARGOCD_PASSWORD" && %s`,
			insecureFlag, appGetCmd,
		)
	} else {
		envFromSecret = map[string]string{
			"ARGOCD_SERVER":     "ARGOCD_SERVER",
			"ARGOCD_AUTH_TOKEN": "ARGOCD_AUTH_TOKEN",
		}
		argoCDCmd = appGetCmd
	}

	resp, err := relay.CommandExecutor(params.AccountId, argoCDCmd, secretName, envFromSecret)
	if err != nil {
		return nil, fmt.Errorf("failed to execute argocd API request: %w", err)
	}

	// Extract response data
	respStr, ok := resp["response"].(string)
	if !ok {
		return nil, fmt.Errorf("unexpected response format from argocd API: %v", resp)
	}
	// Parse ArgoCD application response
	var app argoCDApplication
	err = json.Unmarshal([]byte(respStr), &app)
	if err != nil {
		// Response might be in Python dict format (single quotes)
		// Try converting to JSON using Python
		convertedJSON, convErr := common.ConvertPythonDictToJSON(respStr)
		if convErr != nil {
			return nil, fmt.Errorf("failed to parse ArgoCD response as JSON or Python dict: %w (response preview: %s)", err, respStr[:min(500, len(respStr))])
		}

		err = json.Unmarshal([]byte(convertedJSON), &app)
		if err != nil {
			return nil, fmt.Errorf("failed to parse converted ArgoCD response: %w", err)
		}
	}

	// Check if error response
	if app.Metadata.Name == "" {
		// Try to parse as error response
		var errResp map[string]any
		if json.Unmarshal([]byte(respStr), &errResp) == nil {
			if errMsg, ok := errResp["message"].(string); ok {
				return nil, fmt.Errorf("argocd error: %s", errMsg)
			}
			if errMsg, ok := errResp["error"].(string); ok {
				return nil, fmt.Errorf("argocd error: %s", errMsg)
			}
		}
		// Include response preview for debugging
		preview := respStr
		if len(preview) > 300 {
			preview = preview[:300] + "..."
		}
		return nil, fmt.Errorf("failed to fetch application details from ArgoCD (empty metadata name). Response: %s", preview)
	}

	// Process deployment history from the app status (already included in argocd app get response)
	var relevantDeployment *argoCDHistoryEntry
	var deploymentHistory []deploymentHistoryEntry
	var deploymentContext map[string]interface{}

	// Build the history response using relevant deployment (before event) or current state
	response := argoCDHistoryResponse{
		ApplicationName:      app.Metadata.Name,
		Namespace:            app.Metadata.Namespace,
		Project:              app.Spec.Project,
		SyncStatus:           app.Status.Sync.Status,
		HealthStatus:         app.Status.Health.Status,
		HealthMessage:        app.Status.Health.Message,
		CreatedAt:            app.Metadata.CreationTimestamp,
		DestinationServer:    app.Status.Sync.ComparedTo.Destination.Server,
		DestinationNamespace: app.Status.Sync.ComparedTo.Destination.Namespace,
		Labels:               app.Metadata.Labels,
	}

	// Use relevant deployment from history if available, otherwise use current state
	if relevantDeployment != nil {
		// Use the deployment that was active before the event
		response.CurrentRevision = relevantDeployment.Revision
		response.RepoURL = relevantDeployment.Source.RepoURL
		response.Path = relevantDeployment.Source.Path
		response.TargetRevision = relevantDeployment.Source.TargetRevision
		response.LastSyncedAt = &relevantDeployment.DeployedAt
	} else {
		// Fall back to current deployed state from status.sync.comparedTo
		response.CurrentRevision = app.Status.Sync.Revision
		response.RepoURL = app.Status.Sync.ComparedTo.Source.RepoURL
		response.Path = app.Status.Sync.ComparedTo.Source.Path
		response.TargetRevision = app.Status.Sync.ComparedTo.Source.TargetRevision
		response.Ref = app.Status.Sync.ComparedTo.Source.Ref

		// Get last sync time from operation state if available
		if app.Status.OperationState != nil && app.Status.OperationState.FinishedAt != nil {
			response.LastSyncedAt = app.Status.OperationState.FinishedAt
		}
	}

	// Set basic deployment info
	// Note: Deployment strategy detection would require additional API calls
	// For simplicity, we omit this and focus on deployment history
	response.DeploymentStrategy = ""
	response.DeploymentType = "sync"

	// Add deployment history and context to response
	response.DeploymentHistory = deploymentHistory
	response.DeploymentContext = deploymentContext

	// Generate simple insights
	insights := []PlaybookActionResponseInsight{}

	// Show when the last deployment occurred before the event
	if response.LastSyncedAt != nil && ctx.GetEvent().StartedAt != nil {
		deploymentTime := *response.LastSyncedAt
		eventStartTime := *ctx.GetEvent().StartedAt

		if deploymentTime.Before(eventStartTime) {
			timeBefore := eventStartTime.Sub(deploymentTime)
			insights = append(insights, PlaybookActionResponseInsight{
				Message:  fmt.Sprintf("Last deployment was %s before the event started", formatDuration(timeBefore)),
				Severity: "info",
			})
		}
	}

	// Show sync status if not synced
	if app.Status.Sync.Status != "Synced" {
		insights = append(insights, PlaybookActionResponseInsight{
			Message:  fmt.Sprintf("Application is not in sync. Current status: %s", app.Status.Sync.Status),
			Severity: "warning",
		})
	}

	// Show health status if not healthy
	if app.Status.Health.Status != "Healthy" {
		severity := "error"
		if app.Status.Health.Status == "Progressing" || app.Status.Health.Status == "Suspended" {
			severity = "warning"
		}
		message := fmt.Sprintf("Application health: %s", app.Status.Health.Status)
		if app.Status.Health.Message != "" {
			message += fmt.Sprintf(" - %s", app.Status.Health.Message)
		}
		insights = append(insights, PlaybookActionResponseInsight{
			Message:  message,
			Severity: severity,
		})
	}

	// Show positive status if everything is good
	if app.Status.Sync.Status == "Synced" && app.Status.Health.Status == "Healthy" {
		insights = append(insights, PlaybookActionResponseInsight{
			Message:  "Application is synced and healthy",
			Severity: "info",
		})
	}

	// argocd_url/argocd_server are intentionally empty: the server URL now lives in
	// the k8s secret, which api-server cannot read in plaintext. Consumers that need
	// a link should resolve it from the integration's secret.
	metadata := map[string]any{
		"query-result-version": "1.0",
		"query":                rawParams,
		"argocd_url":           "",
		"integration_name":     integrationName,
	}

	additionalInfo := map[string]any{
		"argocd_server": "",
		"integration":   integrationName,
	}

	// Extract labels for event correlation
	labels := map[string]any{}

	// Always add repo URL if available
	if response.RepoURL != "" {
		labels["repo_url"] = response.RepoURL
	}

	// Add current deployed revision (commit hash) if available
	if response.CurrentRevision != "" {
		labels["revision"] = response.CurrentRevision
	}

	// Add target revision (branch/tag) if available and different from current
	if response.TargetRevision != "" && response.TargetRevision != response.CurrentRevision {
		labels["target_revision"] = response.TargetRevision
	}

	// Add deployment timestamp for event correlation
	if response.LastSyncedAt != nil {
		labels["deployment_time"] = response.LastSyncedAt.Format(time.RFC3339)
	}

	return NewPlaybookActionResponseJsonWithLabels(response, additionalInfo, insights, metadata, labels), nil
}

// fetchArgoCDIntegration queries the database for ArgoCD integration config
func fetchArgoCDIntegration(accountId, integrationName string) (secretName, foundIntegrationName, authMethod string, insecure bool, err error) {
	dbms, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		return "", "", "", false, fmt.Errorf("failed to get database manager: %w", err)
	}

	// Query to find ArgoCD integration for the account
	query := `
		SELECT i.id::text, i.name::text
		FROM integrations i
		JOIN integrations_cloud_accounts ica ON i.id = ica.integration_id
		WHERE i.type = 'argocd'
		  AND ica.cloud_account_id = $1
	`
	args := []interface{}{accountId}

	if integrationName != "" {
		query += " AND i.name = $2"
		args = append(args, integrationName)
	}

	query += " LIMIT 1"

	var integrationId, name string
	err = dbms.Db.QueryRowx(query, args...).Scan(&integrationId, &name)
	if err != nil {
		return "", "", "", false, fmt.Errorf("no argocd integration found for account: %w", err)
	}

	// Fetch integration config values
	configQuery := `
		SELECT name::text, value::text, is_encrypted
		FROM integration_config_values
		WHERE integration_id = $1
	`
	rows, err := dbms.Db.Queryx(configQuery, integrationId)
	if err != nil {
		return "", "", "", false, fmt.Errorf("failed to fetch integration config values: %w", err)
	}
	defer func() {
		if err := rows.Close(); err != nil {
			slog.Error("failed to close rows", "error", err)
		}
	}()

	configs := make(map[string]string)
	for rows.Next() {
		var configName, value string
		var isEncrypted bool
		if err := rows.Scan(&configName, &value, &isEncrypted); err != nil {
			return "", "", "", false, fmt.Errorf("failed to scan config value: %w", err)
		}

		// Decrypt if encrypted
		if isEncrypted && value != "" {
			decrypted, err := common.Decrypt(value)
			if err != nil {
				return "", "", "", false, fmt.Errorf("failed to decrypt config value %s: %w", configName, err)
			}
			configs[configName] = decrypted
		} else {
			configs[configName] = value
		}
	}

	// The server URL and credentials live in the k8s secret under the standard
	// ArgoCD CLI env-var keys; only the secret name, auth method and insecure flag
	// are configured here.
	secretName = configs["k8s_secret"]

	authMethod = configs["auth_method"]
	if authMethod == "" {
		authMethod = "token"
	}

	// Extract optional insecure flag - defaults to false (secure) if not set
	insecureStr := configs["insecure"]
	insecure = insecureStr == "true" || insecureStr == "1"

	if secretName == "" {
		return "", "", "", false, errors.New("k8s_secret not found in argocd integration config")
	}

	return secretName, name, authMethod, insecure, nil
}

// Helper function to format duration in a human-readable way
func formatDuration(d time.Duration) string {
	if d < time.Minute {
		return fmt.Sprintf("%d seconds", int(d.Seconds()))
	} else if d < time.Hour {
		return fmt.Sprintf("%d minutes", int(d.Minutes()))
	} else if d < 24*time.Hour {
		return fmt.Sprintf("%d hours", int(d.Hours()))
	} else {
		return fmt.Sprintf("%d days", int(d.Hours()/24))
	}
}
