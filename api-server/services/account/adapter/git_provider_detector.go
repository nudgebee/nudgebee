package adapter

import (
	"log/slog"
	"net/url"
	"nudgebee/services/common"
	"nudgebee/services/internal/annotations"
	"nudgebee/services/internal/database"
	"nudgebee/services/internal/database/models"
	"strings"
)

// GitProvider constants for supported git providers
const (
	GitProviderGitHub = "github"
	GitProviderGitLab = "gitlab"
)

// DetectGitProviderFromURL analyzes a git repository URL and returns the detected provider.
// Returns "github", "gitlab", or "" (unknown/unsupported).
func DetectGitProviderFromURL(repoURL string) string {
	if repoURL == "" {
		return ""
	}

	// Normalize URL
	repoURL = strings.TrimSpace(repoURL)
	if repoURL == "" {
		return ""
	}
	repoURL = strings.TrimSuffix(repoURL, ".git")

	// Parse URL - add scheme if missing for proper parsing
	urlToParse := repoURL
	if !strings.Contains(repoURL, "://") {
		urlToParse = "https://" + repoURL
	}

	parsedURL, err := url.Parse(urlToParse)
	if err != nil {
		return ""
	}

	host := strings.ToLower(parsedURL.Host)

	// GitHub detection
	if strings.Contains(host, "github.com") || strings.HasPrefix(host, "github.") {
		return GitProviderGitHub
	}

	// GitLab detection (including self-hosted instances)
	if strings.Contains(host, "gitlab.com") || strings.HasPrefix(host, "gitlab.") {
		return GitProviderGitLab
	}

	// Additional GitLab indicator for self-hosted instances with /gitlab/ path
	pathLower := strings.ToLower(parsedURL.Path)
	if strings.HasPrefix(pathLower, "/gitlab/") {
		return GitProviderGitLab
	}

	return "" // Unknown provider
}

// GetGitRepoURLFromWorkload extracts the ci.<domain>/git.repo annotation from workload metadata.
// This is a lightweight version that only retrieves the git repo URL without credentials.
// cloudAccountId is the account ID to query workloads for (from Recommendation.CloudAccountId or Event.CloudAccountId).
func GetGitRepoURLFromWorkload(ctx AccountAdapterContext, resource models.Resource, cloudAccountId string) (string, error) {
	dbms, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		return "", err
	}

	// Extract controller info from resource metadata
	metaData, ok := resource.Meta.Object().(map[string]any)
	if !ok {
		return "", common.ErrorNotFound("error: resource meta not found")
	}

	controllerKind, ok := metaData["controllerKind"].(string)
	if !ok {
		return "", common.ErrorNotFound("error: controller kind not found")
	}

	controllerName, ok := metaData["controller"].(string)
	if !ok {
		controllerName = *resource.Name
	}

	controllerNamespace, ok := metaData["namespace"].(string)
	if !ok {
		return "", common.ErrorNotFound("error: controller namespace not found")
	}

	// Query k8s_workloads for the workload metadata
	rows, err := dbms.Db.Queryx(
		"SELECT meta::varchar FROM k8s_workloads WHERE tenant_id = $1 AND cloud_account_id = $2 AND kind = $3 AND namespace = $4 AND name = $5 AND is_active = true",
		ctx.GetSecurityContext().GetTenantId(),
		cloudAccountId,
		controllerKind,
		controllerNamespace,
		controllerName,
	)
	if err != nil {
		return "", err
	}
	defer func() {
		if err := rows.Close(); err != nil {
			slog.Error("Error closing rows", "error", err)
		}
	}()

	var meta string
	for rows.Next() {
		if err := rows.Scan(&meta); err != nil {
			return "", err
		}
	}

	if meta == "" {
		return "", common.ErrorNotFound("error: workload not found")
	}

	// Parse workload metadata
	workloadMetadata := make(map[string]any)
	if err := common.UnmarshalJson([]byte(meta), &workloadMetadata); err != nil {
		return "", err
	}

	workloadMetadataConfig, ok := workloadMetadata["config"]
	if !ok {
		return "", common.ErrorNotFound("error: workload metadata config not found")
	}

	configMap, ok := workloadMetadataConfig.(map[string]any)
	if !ok {
		return "", common.ErrorNotFound("error: workload metadata config format invalid")
	}

	workloadAnnotations, ok := configMap["annotations"]
	if !ok {
		return "", common.ErrorNotFound("error: workload metadata annotations not found")
	}

	annotationsMap, ok := workloadAnnotations.(map[string]any)
	if !ok {
		return "", common.ErrorNotFound("error: workload annotations format invalid")
	}

	// Extract git.repo annotation
	gitRepo, ok := annotationsMap[annotations.CIGitRepo]
	if !ok || gitRepo == nil {
		return "", nil // No git.repo annotation, not an error
	}

	gitRepoStr, ok := gitRepo.(string)
	if !ok {
		return "", nil
	}

	return gitRepoStr, nil
}
