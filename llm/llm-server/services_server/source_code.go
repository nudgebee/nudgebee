package services_server

import (
	"encoding/json"
	"errors"
	"fmt"
	"nudgebee/llm/common"
	"nudgebee/llm/config"
	"nudgebee/llm/relay"
	"nudgebee/llm/security"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
)

// Annotation keys recognized by GetSourceCodeAnnotations as valid sources of
// repository / commit information for a workload. Callers that need to check
// whether a workload has a known repository should reference these constants
// rather than hardcoding the strings.
const (
	AnnotationGitRepo       = "workloads.nudgebee.com/git.repo"
	AnnotationGitHash       = "workloads.nudgebee.com/git.hash"
	AnnotationCIGitRepo     = "ci.nudgebee.com/git.repo"
	AnnotationCIGitHash     = "ci.nudgebee.com/git.hash"
	AnnotationArgoCDTracker = "argocd.argoproj.io/tracking-id"
)

// HasKnownRepoAnnotation returns true if the given annotation map contains any
// recognized repository source (Nudgebee git, CI git, or ArgoCD tracking).
func HasKnownRepoAnnotation(annotations map[string]string) bool {
	if len(annotations) == 0 {
		return false
	}
	return annotations[AnnotationGitRepo] != "" ||
		annotations[AnnotationCIGitRepo] != "" ||
		annotations[AnnotationArgoCDTracker] != ""
}

// WorkloadKey represents a unique workload identifier.
type WorkloadKey struct {
	Namespace    string
	WorkloadName string
}

// GetSourceCodeAnnotationsBatch retrieves annotations for multiple workloads in a single query.
func GetSourceCodeAnnotationsBatch(ctx *security.RequestContext, dbManager *common.DatabaseManager, accountId string, keys []WorkloadKey) (map[WorkloadKey]map[string]string, error) {
	if len(keys) == 0 {
		return nil, nil
	}

	lookupKeys := make([]string, len(keys))
	for i, k := range keys {
		lookupKeys[i] = k.Namespace + "/" + k.WorkloadName
	}

	query := `
		SELECT namespace || '/' || name as key, meta::text
		FROM k8s_workloads
		WHERE cloud_account_id = ?
		AND (namespace || '/' || name) IN (?)`

	query, args, err := sqlx.In(query, accountId, lookupKeys)
	if err != nil {
		ctx.GetLogger().Error("failed to construct IN query", "error", err)
		return nil, err
	}
	query = dbManager.Db.Rebind(query)

	ctx.GetLogger().Debug("executing batch SQL query", "query", query, "arg_count", len(args))
	rows, err := dbManager.Db.Queryx(query, args...)
	if err != nil {
		ctx.GetLogger().Error("failed to execute batch query", "error", err)
		return nil, fmt.Errorf("failed to execute batch query: %w", err)
	}
	defer func() {
		if err := rows.Close(); err != nil {
			ctx.GetLogger().Error("source_code: failed to close rows", "error", err)
		}
	}()

	results := make(map[WorkloadKey]map[string]string)

	for rows.Next() {
		var key string
		var metaString string
		if err := rows.Scan(&key, &metaString); err != nil {
			ctx.GetLogger().Warn("failed to scan batch row", "error", err)
			continue
		}

		parts := strings.Split(key, "/")
		if len(parts) != 2 {
			continue
		}
		wk := WorkloadKey{Namespace: parts[0], WorkloadName: parts[1]}

		var meta map[string]any
		if err := common.UnmarshalJson([]byte(metaString), &meta); err != nil {
			ctx.GetLogger().Warn("unable to unmarshal meta json", "error", err)
			continue
		}

		annotations := make(map[string]string)
		if len(meta) > 0 && meta["config"] != nil {
			if config, ok := meta["config"].(map[string]any); ok {
				if annotationsAny, ok := config["annotations"].(map[string]any); ok {
					for k, v := range annotationsAny {
						if k == "workloads.nudgebee.com/git.repo" || k == "workloads.nudgebee.com/git.hash" ||
							k == "argocd.argoproj.io/tracking-id" || k == "ci.nudgebee.com/git.repo" ||
							k == "ci.nudgebee.com/git.hash" {
							annotations[k] = fmt.Sprintf("%v", v)
						}
					}
				}
			}
		}
		results[wk] = annotations
	}

	return results, nil
}

// GetSourceCodeReposBatch retrieves source code repo info for multiple workloads efficiently.
func GetSourceCodeReposBatch(ctx *security.RequestContext, accountId string, keys []WorkloadKey) (map[WorkloadKey]GetSourceCodeRepoResponse, error) {
	dbManager, err := common.GetDatabaseManager(common.Metastore)
	if err != nil {
		ctx.GetLogger().Error("failed to get database manager", "error", err)
		return nil, err
	}

	batchAnnotations, err := GetSourceCodeAnnotationsBatch(ctx, dbManager, accountId, keys)
	if err != nil {
		return nil, err
	}

	responses := make(map[WorkloadKey]GetSourceCodeRepoResponse)

	for _, key := range keys {
		annotations := batchAnnotations[key]
		// Initialize response with Nudgebee annotations
		response := GetSourceCodeRepoResponse{
			CodeRepo:           annotations["workloads.nudgebee.com/git.repo"],
			CIRepo:             annotations["ci.nudgebee.com/git.repo"],
			CodeRepoCommitHash: annotations["workloads.nudgebee.com/git.hash"],
			CIRepoCommitHash:   annotations["ci.nudgebee.com/git.hash"],
		}

		// Check for ArgoCD tracking annotation
		argoCDTrackingID := annotations["argocd.argoproj.io/tracking-id"]
		if argoCDTrackingID != "" {
			argoCDAppName := extractArgoCDAppName(argoCDTrackingID)
			if argoCDAppName != "" {
				// We still do singular ArgoCD lookup here because batching relay calls is complex
				// and requires API changes. But we saved N database calls already.
				argoCDResponse, err := GetSourceCodeFromArgoCD(ctx, accountId, argoCDAppName)
				if err != nil {
					ctx.GetLogger().Warn("failed to get ArgoCD source information", "error", err, "app_name", argoCDAppName)
				} else {
					if response.CodeRepo == "" {
						response.CodeRepo = argoCDResponse.CodeRepo
					}
					if response.CodeRepoCommitHash == "" {
						response.CodeRepoCommitHash = argoCDResponse.CodeRepoCommitHash
					}
					response.ArgoCDApp = argoCDResponse.ArgoCDApp
					response.TargetRevision = argoCDResponse.TargetRevision
					response.ManifestPath = argoCDResponse.ManifestPath
					response.SyncStatus = argoCDResponse.SyncStatus
					response.ValuesFiles = argoCDResponse.ValuesFiles
					response.ValuesRepoURL = argoCDResponse.ValuesRepoURL
					response.ValuesPath = argoCDResponse.ValuesPath
					response.HelmChartRepo = argoCDResponse.HelmChartRepo
					response.HelmChartName = argoCDResponse.HelmChartName
					response.HelmReleaseName = argoCDResponse.HelmReleaseName

					if response.CIRepo != "" || annotations["workloads.nudgebee.com/git.repo"] != "" {
						response.Source = "both"
					} else {
						response.Source = "argocd"
					}
				}
			}
		} else if response.CodeRepo != "" || response.CIRepo != "" {
			response.Source = "nudgebee"
		}
		responses[key] = response
	}

	return responses, nil
}

// GetSourceCodeAnnotations retrieves source code annotations from the database based on event or pod information.
// It supports both event-based and pod-based lookups.
func GetSourceCodeAnnotations(ctx *security.RequestContext, dbManager *common.DatabaseManager, accountId string, options SourceCodeAnnotationOptions) (map[string]string, error) {
	ctx.GetLogger().Info("executing Source Code Annotations", "options", options, "account_id", accountId)

	var query string
	var args []any

	switch {
	case options.EventId != "":
		// Event-based queries
		if options.Namespace != "" && options.WorkloadName != "" {
			query = `
				SELECT meta::text 
				FROM k8s_workloads kw 
				WHERE kw."name" = $2 
				  AND kw."namespace" = $3 
				  AND kw.cloud_account_id = $1`
			args = []any{accountId, options.WorkloadName, options.Namespace}
			ctx.GetLogger().Info("using namespace and workload name strategy", "namespace", options.Namespace, "workload", options.WorkloadName)
		} else if options.CloudResourceId != "" {

			query = `
				SELECT meta::text 
				FROM k8s_workloads kw 
				JOIN events e ON e.cloud_resource_id = kw.cloud_resource_id 
				WHERE e.id = $1 AND e.cloud_account_id = $2
				UNION
				SELECT cr.meta::text 
				FROM events e 
				JOIN k8s_pods ksp ON e.cloud_resource_id = ksp.cloud_resource_id
				JOIN k8s_workloads ksw ON ksp.workload_name = ksw."name" AND ksp."namespace" = ksw."namespace"
				JOIN cloud_resourses cr ON cr.id = ksw.cloud_resource_id
				WHERE e.id = $3 AND cr.account = $4`
			args = []any{options.EventId, accountId, options.EventId, accountId}
			ctx.GetLogger().Info("using cloud resource ID strategy", "event_id", options.EventId, "cloud_resource_id", options.CloudResourceId)
		} else {
			return nil, fmt.Errorf("EventID is provided but either Namespace+WorkloadName or CloudResourceId is required")
		}

	case options.WorkloadName != "":
		// If workload name is provided, use it (ignoring pod name)

		if options.Namespace != "" {
			query = `
			SELECT DISTINCT meta::text 
			FROM k8s_workloads ksw
			WHERE ksw."name" = $2 AND ksw.cloud_account_id = $1 AND ksw."namespace" = $3`
			args = []any{accountId, options.WorkloadName, options.Namespace}
		} else {
			query = `
			SELECT DISTINCT meta::text 
			FROM k8s_workloads ksw
			WHERE ksw."name" = $2 AND ksw.cloud_account_id = $1`
			args = []any{accountId, options.WorkloadName}
			ctx.GetLogger().Info("using workload name strategy", "workload", options.WorkloadName)
		}

	default:
		ctx.GetLogger().Error("no valid lookup options provided")
		return nil, fmt.Errorf("either EventId, PodName, or WorkloadName must be provided")
	}

	ctx.GetLogger().Debug("executing SQL query", "query", query, "arg_count", len(args))
	rows, err := dbManager.Db.Queryx(query, args...)
	if err != nil {
		ctx.GetLogger().Error("failed to execute query", "error", err)
		return nil, fmt.Errorf("failed to execute query: %w", err)
	}
	defer func() {
		if err := rows.Close(); err != nil {
			ctx.GetLogger().Error("source_code: failed to close rows", "error", err)
		}
	}()

	var meta map[string]any
	rowFound := false
	for rows.Next() {
		rowFound = true
		var metaString string
		if err := rows.Scan(&metaString); err != nil {
			ctx.GetLogger().Warn("failed to scan row", "error", err)
			continue
		}
		if metaString == "" {
			ctx.GetLogger().Debug("empty meta string in row")
			continue
		}

		err := common.UnmarshalJson([]byte(metaString), &meta)
		if err != nil {
			ctx.GetLogger().Warn("unable to unmarshal meta json", "error", err, "meta_string_length", len(metaString))
			continue
		}
		break // Use first valid row
	}

	if !rowFound {
		ctx.GetLogger().Info("no rows returned from query")
	}

	annotations := make(map[string]string)
	if len(meta) > 0 && meta["config"] != nil {

		configAny, ok := meta["config"]
		if !ok {
			ctx.GetLogger().Info("config key not found in meta")
			return annotations, nil
		}

		config, ok := configAny.(map[string]any)
		if !ok {
			ctx.GetLogger().Info("config is not a map[string]any", "type", fmt.Sprintf("%T", configAny))
			return annotations, nil
		}

		annotationsAny, ok := config["annotations"]
		if !ok {
			ctx.GetLogger().Info("annotations key not found in config")
			return annotations, nil
		}

		annotation, ok := annotationsAny.(map[string]any)
		if !ok {
			ctx.GetLogger().Info("annotations is not a map[string]any", "type", fmt.Sprintf("%T", annotationsAny))
			return annotations, nil
		}

		for key, value := range annotation {
			if key == "workloads.nudgebee.com/git.repo" || key == "workloads.nudgebee.com/git.hash" ||
				key == "argocd.argoproj.io/tracking-id" || key == "ci.nudgebee.com/helm.values.filePath" || key == "ci.nudgebee.com/git.repo" || key == "ci.nudgebee.com/git.hash" {
				annotations[key] = fmt.Sprintf("%v", value)
				ctx.GetLogger().Info("found annotation", "key", key, "value", value)
			}
		}
	} else {
		ctx.GetLogger().Info("meta is empty or doesn't contain config key", "meta_length", len(meta))
	}

	return annotations, nil
}

// extractArgoCDAppName extracts the application name from ArgoCD tracking ID
// Format: "app-name:group/Kind:namespace/resource-name"
// Example: "sample-app:apps/Deployment:demo/frontend" -> "sample-app"
func extractArgoCDAppName(trackingID string) string {
	if trackingID == "" {
		return ""
	}
	parts := strings.SplitN(trackingID, ":", 2)
	if len(parts) > 0 {
		return parts[0]
	}
	return ""
}

// extractRelayResponse extracts the response string from relay response data
func extractRelayResponse(relayResponse map[string]any) (string, error) {
	data, ok := relayResponse["data"].(map[string]any)
	if !ok || data == nil {
		return "", errors.New("data field not found in relay response")
	}

	findings, ok := data["findings"].([]any)
	if !ok || findings == nil || len(findings) == 0 {
		return "", errors.New("findings field not found or empty in relay response")
	}

	firstFinding, ok := findings[0].(map[string]any)
	if !ok {
		return "", errors.New("invalid finding format")
	}

	evidence, ok := firstFinding["evidence"].([]any)
	if !ok || len(evidence) == 0 {
		return "", errors.New("evidence field not found or empty")
	}

	firstEvidence, ok := evidence[0].(map[string]any)
	if !ok {
		return "", errors.New("invalid evidence format")
	}

	evidenceDataStr, ok := firstEvidence["data"].(string)
	if !ok {
		return "", errors.New("evidence data not found")
	}

	// Parse the command response array
	var commandResponseArray []any
	if err := json.Unmarshal([]byte(evidenceDataStr), &commandResponseArray); err != nil {
		return "", fmt.Errorf("failed to unmarshal command response array: %w", err)
	}

	if len(commandResponseArray) == 0 {
		return "", errors.New("command response array is empty")
	}

	firstResponse, ok := commandResponseArray[0].(map[string]any)
	if !ok {
		return "", errors.New("invalid command response format")
	}

	responseDataStr, ok := firstResponse["data"].(string)
	if !ok {
		return "", errors.New("response data not found")
	}

	// Parse the final response
	var commandResponse map[string]any
	if err := json.Unmarshal([]byte(responseDataStr), &commandResponse); err != nil {
		return "", fmt.Errorf("failed to unmarshal command response: %w", err)
	}

	responseStr, ok := commandResponse["response"].(string)
	if !ok {
		return "", errors.New("response string not found in command response")
	}

	return responseStr, nil
}

// fetchArgoCDIntegrationConfig fetches ArgoCD integration config from database
func fetchArgoCDIntegrationConfig(accountId string) (secretName, serverURL, authTokenKeyInSecret string, err error) {
	dbManager, err := common.GetDatabaseManager(common.Metastore)
	if err != nil {
		return "", "", "", fmt.Errorf("failed to get database manager: %w", err)
	}

	// Query to find ArgoCD integration for the account
	query := `
		SELECT i.id::text
		FROM integrations i
		JOIN integrations_cloud_accounts ica ON i.id = ica.integration_id
		WHERE i.type = 'argocd'
		  AND ica.cloud_account_id = $1
		LIMIT 1
	`

	var integrationId string
	err = dbManager.Db.QueryRowx(query, accountId).Scan(&integrationId)
	if err != nil {
		return "", "", "", fmt.Errorf("no argocd integration found for account: %w", err)
	}

	// Fetch integration config values
	configQuery := `
		SELECT name::text, value::text, is_encrypted
		FROM integration_config_values
		WHERE integration_id = $1
	`
	rows, err := dbManager.Db.Queryx(configQuery, integrationId)
	if err != nil {
		return "", "", "", fmt.Errorf("failed to fetch integration config values: %w", err)
	}
	defer func() {
		if closeErr := rows.Close(); closeErr != nil {
			err = fmt.Errorf("failed to close rows: %w", closeErr)
		}
	}()

	configs := make(map[string]string)
	for rows.Next() {
		var configName, value string
		var isEncrypted bool
		if err := rows.Scan(&configName, &value, &isEncrypted); err != nil {
			return "", "", "", fmt.Errorf("failed to scan config value: %w", err)
		}

		// Decrypt if encrypted
		if isEncrypted && value != "" {
			decrypted, err := common.Decrypt(value)
			if err != nil {
				return "", "", "", fmt.Errorf("failed to decrypt config value %s: %w", configName, err)
			}
			configs[configName] = decrypted
		} else {
			configs[configName] = value
		}
	}

	// Check for errors from iterating over rows
	if err := rows.Err(); err != nil {
		return "", "", "", fmt.Errorf("error iterating config values: %w", err)
	}

	// Extract required config values
	secretName = configs["k8s_secret"]
	serverURL = configs["server"]
	authTokenKeyInSecret = configs["auth_token_key_in_secret"]

	// Set defaults
	if authTokenKeyInSecret == "" {
		authTokenKeyInSecret = "ARGOCD_AUTH_TOKEN"
	}

	if secretName == "" {
		return "", "", "", errors.New("k8s_secret not found in argocd integration config")
	}

	if serverURL == "" {
		return "", "", "", errors.New("server URL not found in argocd integration config")
	}

	return secretName, serverURL, authTokenKeyInSecret, nil
}

// GetSourceCodeFromArgoCD queries ArgoCD API to get source code information
func GetSourceCodeFromArgoCD(ctx *security.RequestContext, accountId, appName string) (GetSourceCodeRepoResponse, error) {
	if appName == "" {
		return GetSourceCodeRepoResponse{}, fmt.Errorf("argocd app name is empty")
	}

	ctx.GetLogger().Info("querying ArgoCD application", "app_name", appName)

	// Fetch ArgoCD integration config
	secretName, serverURL, authTokenKeyInSecret, err := fetchArgoCDIntegrationConfig(accountId)
	if err != nil {
		ctx.GetLogger().Error("failed to fetch ArgoCD integration", "error", err)
		return GetSourceCodeRepoResponse{}, fmt.Errorf("failed to fetch ArgoCD integration: %w", err)
	}

	// Strip protocol from serverURL as argocd CLI expects hostname only
	serverHost := strings.TrimPrefix(serverURL, "https://")
	serverHost = strings.TrimPrefix(serverHost, "http://")

	// Build the argocd CLI command
	// ArgoCD CLI automatically uses ARGOCD_AUTH_TOKEN environment variable
	argoCDCmd := fmt.Sprintf(
		`argocd app get %s --server %s --insecure --grpc-web --output json`,
		appName,
		serverHost,
	)

	ctx.GetLogger().Debug("executing ArgoCD command", "command", argoCDCmd, "server", serverHost)

	// Execute via relay server
	// Map the secret key to ARGOCD_AUTH_TOKEN env var for ArgoCD CLI
	envFromSecret := map[string]string{
		"ARGOCD_AUTH_TOKEN": authTokenKeyInSecret,
	}

	actionParams := map[string]any{
		"image":    config.Config.LlmServerShellImage,
		"command":  argoCDCmd,
		"pod_name": "nb-argocd-query-" + uuid.NewString(),
	}

	if strings.Contains(secretName, "/") {
		namespaceAndSecret := strings.Split(secretName, "/")
		actionParams["namespace"] = namespaceAndSecret[0]
		actionParams["secret"] = namespaceAndSecret[1]
	} else {
		actionParams["secret"] = secretName
	}

	actionParams["env_from_secret_keys"] = envFromSecret

	actionParam := relay.ActionExecuteBody{
		AccountID:    accountId,
		ActionName:   "pod_script_run_enricher",
		ActionParams: actionParams,
	}
	actionParam.Timeout = time.Second * 60

	response, err := relay.Execute(actionParam)
	if err != nil {
		ctx.GetLogger().Error("failed to execute argocd command", "error", err)
		return GetSourceCodeRepoResponse{}, fmt.Errorf("failed to execute argocd command: %w", err)
	}

	// Log the raw relay response for debugging
	responseJSON, _ := json.MarshalIndent(response, "", "  ")
	ctx.GetLogger().Debug("raw relay response", "response", string(responseJSON))

	// Check if relay execution was successful
	if data, ok := response["data"].(map[string]any); ok {
		if success, ok := data["success"].(bool); ok && !success {
			// Relay execution failed
			errorMsg := "unknown error"
			if msg, ok := data["msg"].(string); ok {
				errorMsg = msg
			}
			ctx.GetLogger().Error("relay execution failed", "error", errorMsg, "error_code", data["error_code"])
			return GetSourceCodeRepoResponse{}, fmt.Errorf("relay execution failed: %s", errorMsg)
		}
	}

	// Parse relay response
	respStr, err := extractRelayResponse(response)
	if err != nil {
		// Log the full response structure for debugging
		ctx.GetLogger().Error("failed to parse relay response", "error", err, "response", string(responseJSON))
		return GetSourceCodeRepoResponse{}, fmt.Errorf("failed to parse relay response: %w", err)
	}

	// Parse ArgoCD application response
	var app argoCDApplication
	if err := json.Unmarshal([]byte(respStr), &app); err != nil {
		ctx.GetLogger().Error("failed to unmarshal ArgoCD application", "error", err, "response", respStr[:min(500, len(respStr))])
		return GetSourceCodeRepoResponse{}, fmt.Errorf("failed to unmarshal ArgoCD application: %w", err)
	}

	result := GetSourceCodeRepoResponse{
		ArgoCDApp:  app.Metadata.Name,
		SyncStatus: app.Status.Sync.Status,
		Source:     "argocd",
	}

	// Handle multi-source applications
	if len(app.Spec.Sources) > 0 {
		ctx.GetLogger().Info("processing multi-source ArgoCD application", "source_count", len(app.Spec.Sources))

		for i, source := range app.Spec.Sources {
			ctx.GetLogger().Debug("processing source", "index", i, "repoURL", source.RepoURL, "chart", source.Chart, "ref", source.Ref)

			// Source with Helm chart (typically the main application source)
			// NOTE: repoURL here is a Helm chart repository (e.g., https://charts.helm.sh/stable)
			// This is NOT a Git repository - it's where the Helm chart is published
			if source.Chart != "" {
				result.HelmChartRepo = source.RepoURL // Helm chart repo URL (not Git)
				result.HelmChartName = source.Chart
				result.TargetRevision = source.TargetRevision

				if source.Helm != nil {
					result.ValuesFiles = source.Helm.ValueFiles // e.g., ["$values/path/to/values.yaml"]
					result.HelmReleaseName = source.Helm.ReleaseName
				}
			}

			// Source with ref (typically the values file repository)
			// NOTE: repoURL here is a Git repository containing values files
			// The $values/ prefix in ValuesFiles refers to this repository
			if source.Ref != "" {
				result.ValuesRepoURL = source.RepoURL // Git repo URL where values files exist
				result.ValuesPath = source.Path
				// Use the sync revision as the commit hash for the values repo
				if app.Status.Sync.Revision != "" {
					result.CodeRepoCommitHash = app.Status.Sync.Revision
				}
			}

			// Source with path (could be Git repo with manifests)
			if source.Path != "" && source.Chart == "" {
				result.ManifestPath = source.Path
				if result.CodeRepo == "" {
					result.CodeRepo = source.RepoURL
				}
			}
		}
	} else if app.Spec.Source != nil {
		// Handle single-source applications
		ctx.GetLogger().Info("processing single-source ArgoCD application")
		source := app.Spec.Source

		result.CodeRepo = source.RepoURL
		result.TargetRevision = source.TargetRevision
		result.ManifestPath = source.Path

		if app.Status.Sync.Revision != "" {
			result.CodeRepoCommitHash = app.Status.Sync.Revision
		}

		if source.Helm != nil {
			result.ValuesFiles = source.Helm.ValueFiles
			result.HelmReleaseName = source.Helm.ReleaseName
			result.ValuesRepoURL = source.RepoURL
			result.ValuesPath = source.Path
		}

		if source.Chart != "" {
			result.HelmChartRepo = source.RepoURL
			result.HelmChartName = source.Chart
		}
	}

	ctx.GetLogger().Info("successfully retrieved ArgoCD source information",
		"app_name", appName,
		"code_repo", result.CodeRepo,
		"values_repo", result.ValuesRepoURL,
		"helm_chart", result.HelmChartName)

	return result, nil
}

func GetSourceCodeRepo(ctx *security.RequestContext, accountId string, options SourceCodeAnnotationOptions) GetSourceCodeRepoResponse {
	dbManager, err := common.GetDatabaseManager(common.Metastore)
	if err != nil {
		ctx.GetLogger().Error("failed to get database manager", "error", err)
		return GetSourceCodeRepoResponse{}
	}

	annotations, err := GetSourceCodeAnnotations(ctx, dbManager, accountId, options)
	if err != nil {
		return GetSourceCodeRepoResponse{}
	}

	// Initialize response with Nudgebee annotations
	response := GetSourceCodeRepoResponse{
		CodeRepo:           annotations["workloads.nudgebee.com/git.repo"],
		CIRepo:             annotations["ci.nudgebee.com/git.repo"],
		CodeRepoCommitHash: annotations["workloads.nudgebee.com/git.hash"],
		CIRepoCommitHash:   annotations["ci.nudgebee.com/git.hash"],
		ValuesPath:         annotations["ci.nudgebee.com/helm.values.filePath"],
	}

	// Check for ArgoCD tracking annotation
	argoCDTrackingID := annotations["argocd.argoproj.io/tracking-id"]
	if argoCDTrackingID != "" {
		argoCDAppName := extractArgoCDAppName(argoCDTrackingID)
		if argoCDAppName != "" {
			ctx.GetLogger().Info("found ArgoCD application", "app_name", argoCDAppName, "tracking_id", argoCDTrackingID)

			// Query ArgoCD for source information
			argoCDResponse, err := GetSourceCodeFromArgoCD(ctx, accountId, argoCDAppName)
			if err != nil {
				ctx.GetLogger().Warn("failed to get ArgoCD source information", "error", err, "app_name", argoCDAppName)
			} else {
				// Merge ArgoCD information with Nudgebee annotations
				// Nudgebee annotations take precedence for code repo if both exist
				if response.CodeRepo == "" {
					response.CodeRepo = argoCDResponse.CodeRepo
				}
				if response.CodeRepoCommitHash == "" {
					response.CodeRepoCommitHash = argoCDResponse.CodeRepoCommitHash
				}

				// Add ArgoCD-specific fields
				response.ArgoCDApp = argoCDResponse.ArgoCDApp
				response.TargetRevision = argoCDResponse.TargetRevision
				response.ManifestPath = argoCDResponse.ManifestPath
				response.SyncStatus = argoCDResponse.SyncStatus
				response.ValuesFiles = argoCDResponse.ValuesFiles
				response.ValuesRepoURL = argoCDResponse.ValuesRepoURL
				response.ValuesPath = argoCDResponse.ValuesPath
				response.HelmChartRepo = argoCDResponse.HelmChartRepo
				response.HelmChartName = argoCDResponse.HelmChartName
				response.HelmReleaseName = argoCDResponse.HelmReleaseName

				// Set source indicator
				if response.CIRepo != "" || annotations["workloads.nudgebee.com/git.repo"] != "" {
					response.Source = "both"
				} else {
					response.Source = "argocd"
				}

				ctx.GetLogger().Info("merged ArgoCD and Nudgebee source information",
					"source", response.Source,
					"code_repo", response.CodeRepo,
					"values_repo", response.ValuesRepoURL,
					"helm_chart", response.HelmChartName)
			}
		}
	} else if response.CodeRepo != "" || response.CIRepo != "" {
		response.Source = "nudgebee"
	}

	return response
}

// BatchGetSourceCodeRepo retrieves enrichment data for multiple workloads in a single call.
func BatchGetSourceCodeRepo(ctx *security.RequestContext, accountId string, workloads []SourceCodeAnnotationOptions) (map[string]GetSourceCodeRepoResponse, error) {
	results := make(map[string]GetSourceCodeRepoResponse)
	for _, opt := range workloads {
		// Currently fallback to individual calls as a simple implementation,
		// but encapsulated for future query batching optimization.
		repoInfo := GetSourceCodeRepo(ctx, accountId, opt)
		key := fmt.Sprintf("%s/%s", opt.Namespace, opt.WorkloadName)
		results[key] = repoInfo
	}
	return results, nil
}

// SourceCodeAnnotationOptions contains options for retrieving source code annotations.
type SourceCodeAnnotationOptions struct {
	EventId         string
	PodName         string
	WorkloadName    string
	Namespace       string
	CloudResourceId string
}

type GetSourceCodeRepoResponse struct {
	CodeRepo           string `json:"code_repo"`
	CIRepo             string `json:"ci_repo"`
	CodeRepoCommitHash string `json:"code_repo_commit_hash"`
	CIRepoCommitHash   string `json:"ci_repo_commit_hash"`

	// ArgoCD fields
	ArgoCDApp      string `json:"argocd_app,omitempty"`
	TargetRevision string `json:"target_revision,omitempty"`
	ManifestPath   string `json:"manifest_path,omitempty"`
	SyncStatus     string `json:"sync_status,omitempty"`

	// Helm values file information
	ValuesFiles     []string `json:"values_files,omitempty"`      // e.g., ["$values/deploy/values.yaml"]
	ValuesRepoURL   string   `json:"values_repo_url,omitempty"`   // Git repo URL containing values files (e.g., https://github.com/org/repo.git)
	ValuesPath      string   `json:"values_path,omitempty"`       // Path to values files in Git repo (e.g., deploy/kubernetes/app)
	HelmChartRepo   string   `json:"helm_chart_repo,omitempty"`   // Helm chart repository URL - NOT a Git repo (e.g., https://charts.helm.sh/stable)
	HelmChartName   string   `json:"helm_chart_name,omitempty"`   // Chart name (e.g., nginx-ingress)
	HelmReleaseName string   `json:"helm_release_name,omitempty"` // Helm release name

	Source string `json:"source,omitempty"` // "nudgebee", "argocd", or "both"
}

// ArgoCD API response structures
type argoCDHelmConfig struct {
	ValueFiles   []string               `json:"valueFiles,omitempty"`
	Values       string                 `json:"values,omitempty"`
	ValuesObject map[string]interface{} `json:"valuesObject,omitempty"`
	Parameters   []argoCDHelmParameter  `json:"parameters,omitempty"`
	ReleaseName  string                 `json:"releaseName,omitempty"`
}

type argoCDHelmParameter struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

type argoCDSource struct {
	RepoURL        string            `json:"repoURL"`
	Path           string            `json:"path,omitempty"`
	TargetRevision string            `json:"targetRevision"`
	Ref            string            `json:"ref,omitempty"`
	Chart          string            `json:"chart,omitempty"` // For Helm chart repos
	Helm           *argoCDHelmConfig `json:"helm,omitempty"`
}

type argoCDDestination struct {
	Server    string `json:"server"`
	Namespace string `json:"namespace"`
}

type argoCDSyncStatus struct {
	Status   string `json:"status"`
	Revision string `json:"revision,omitempty"`
}

type argoCDApplicationStatus struct {
	Sync argoCDSyncStatus `json:"sync"`
}

type argoCDSpec struct {
	Source      *argoCDSource     `json:"source,omitempty"`  // Single source
	Sources     []argoCDSource    `json:"sources,omitempty"` // Multi-source
	Destination argoCDDestination `json:"destination"`
}

type argoCDMetadata struct {
	Name string `json:"name"`
}

type argoCDApplication struct {
	Metadata argoCDMetadata          `json:"metadata"`
	Spec     argoCDSpec              `json:"spec"`
	Status   argoCDApplicationStatus `json:"status"`
}
