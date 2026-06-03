package playbooks

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"nudgebee/services/common"
	"nudgebee/services/internal/annotations"
	"nudgebee/services/internal/database"
	"strings"
	"time"
)

type githubPRHistoryAction struct{}

// Request parameters
type githubPRHistoryParams struct {
	RepoURL    string `json:"repo_url"`           // e.g., "https://github.com/org/repo"
	Revision   string `json:"revision,omitempty"` // Commit hash (optional)
	Limit      int    `json:"limit,omitempty"`    // Number of PRs to fetch (default: 3)
	AccountId  string `json:"account_id,omitempty"`
	ConfigName string `json:"config_name,omitempty"` // GitHub config name from jira_configurations
}

// GitHub API response structures
type githubPRItem struct {
	Number   int           `json:"number"`
	Title    string        `json:"title"`
	HTMLURL  string        `json:"html_url"`
	State    string        `json:"state"`
	MergedAt *time.Time    `json:"merged_at"`
	User     githubUser    `json:"user"`
	Labels   []githubLabel `json:"labels"`
	Head     githubRef     `json:"head"`
	Base     githubRef     `json:"base"`
	MergedBy *githubUser   `json:"merged_by"`
}

type githubUser struct {
	Login string `json:"login"`
}

type githubLabel struct {
	Name  string `json:"name"`
	Color string `json:"color"`
}

type githubRef struct {
	Ref string `json:"ref"`
	SHA string `json:"sha"`
}

// GitHub Actions workflow run structures
type githubWorkflowRunsResponse struct {
	TotalCount   int                 `json:"total_count"`
	WorkflowRuns []githubWorkflowRun `json:"workflow_runs"`
}

type githubWorkflowRun struct {
	ID         int64      `json:"id"`
	Name       string     `json:"name"`
	Status     string     `json:"status"`
	Conclusion string     `json:"conclusion"`
	HeadSHA    string     `json:"head_sha"`
	HTMLURL    string     `json:"html_url"`
	CreatedAt  time.Time  `json:"created_at"`
	UpdatedAt  time.Time  `json:"updated_at"`
	HeadBranch string     `json:"head_branch"`
	Event      string     `json:"event"`
	RunNumber  int        `json:"run_number"`
	Actor      githubUser `json:"actor"`
}

// Workflow run summary for response
type workflowRunSummary struct {
	ID         int64     `json:"id"`
	Name       string    `json:"name"`
	Status     string    `json:"status"`
	Conclusion string    `json:"conclusion"`
	URL        string    `json:"url"`
	CommitSHA  string    `json:"commit_sha"`
	Branch     string    `json:"branch"`
	Event      string    `json:"event"`
	RunNumber  int       `json:"run_number"`
	Actor      string    `json:"actor"`
	CreatedAt  time.Time `json:"created_at"`
	RepoURL    string    `json:"repo_url"`
}

// Workload annotation details returned from k8s_workloads
type workloadGitInfo struct {
	SourceRepoURL string // workloads.<domain>/git.repo
	CIRepoURL     string // ci.<domain>/git.repo
	SourceGitHash string // workloads.<domain>/git.hash (app code commit)
	CIGitHash     string // ci.<domain>/git.hash (infra/helm commit)
}

// Response structure
type githubPRHistoryResponse struct {
	RepoURL      string               `json:"repo_url"`
	Revision     string               `json:"revision,omitempty"`
	TotalPRs     int                  `json:"total_prs"`
	PullRequests []pullRequestSummary `json:"pull_requests"`
	WorkflowRuns []workflowRunSummary `json:"workflow_runs,omitempty"`
}

type pullRequestSummary struct {
	Number     int       `json:"number"`
	Title      string    `json:"title"`
	URL        string    `json:"url"`
	Author     string    `json:"author"`
	MergedAt   time.Time `json:"merged_at"`
	MergedBy   string    `json:"merged_by,omitempty"`
	Labels     []string  `json:"labels"`
	BaseBranch string    `json:"base_branch"`
	HeadBranch string    `json:"head_branch"`
	CommitSHA  string    `json:"commit_sha"`
}

// Implement PlaybookAction interface
func (a *githubPRHistoryAction) Execute(ctx PlaybookActionContext, rawParams map[string]any) (PlaybookActionResponse, error) {
	// Parse and validate parameters
	params, err := parseGitHubPRParams(rawParams)
	if err != nil {
		return nil, err
	}

	// Set default limit if not provided
	if params.Limit <= 0 {
		params.Limit = 3
	}

	// Validate repo URL
	if params.RepoURL == "" {
		return nil, errors.New("repo_url is required")
	}

	// Parse org/repo from URL
	org, repo, err := parseRepoURL(params.RepoURL)
	if err != nil {
		return nil, err
	}

	// Fetch GitHub token from jira_configurations
	token, err := fetchGitHubCredentials(ctx.GetTenantId(), params.ConfigName)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch GitHub credentials: %w", err)
	}

	// Fetch PRs — prefer commit-based lookup when we have a revision (accurate for monorepos),
	// fall back to fetching latest merged PRs when no revision is available.
	var prs []githubPRItem
	if params.Revision != "" {
		prs, err = fetchPRsForCommit(org, repo, token, params.Revision)
		if err != nil {
			ctx.GetLogger().Debug("github_pr_history: commit-based PR lookup failed, falling back to recent PRs", "error", err)
		}
	}
	if len(prs) == 0 {
		prs, err = fetchMergedPRs(org, repo, token, params.Limit)
		if err != nil {
			return nil, fmt.Errorf("failed to fetch PRs from GitHub: %w", err)
		}
	}

	// Build response
	response := githubPRHistoryResponse{
		RepoURL:      params.RepoURL,
		Revision:     params.Revision,
		TotalPRs:     len(prs),
		PullRequests: []pullRequestSummary{},
	}

	for _, pr := range prs {
		labels := []string{}
		for _, label := range pr.Labels {
			labels = append(labels, label.Name)
		}

		mergedBy := ""
		if pr.MergedBy != nil {
			mergedBy = pr.MergedBy.Login
		}

		response.PullRequests = append(response.PullRequests, pullRequestSummary{
			Number:     pr.Number,
			Title:      pr.Title,
			URL:        pr.HTMLURL,
			Author:     pr.User.Login,
			MergedAt:   *pr.MergedAt,
			MergedBy:   mergedBy,
			Labels:     labels,
			BaseBranch: pr.Base.Ref,
			HeadBranch: pr.Head.Ref,
			CommitSHA:  pr.Head.SHA,
		})
	}

	// Fetch workflow runs if we have a revision (commit hash)
	if params.Revision != "" {
		runs := fetchWorkflowRunsForCommit(ctx.GetLogger(), org, repo, token, params.Revision, params.RepoURL)
		response.WorkflowRuns = append(response.WorkflowRuns, runs...)
	}

	// If we have a CI repo URL from raw params, fetch workflow runs using the CI-specific revision
	if ciRepoURL, ok := rawParams["ci_repo_url"].(string); ok && ciRepoURL != "" && ciRepoURL != params.RepoURL {
		ciOrg, ciRepo, ciErr := parseRepoURL(ciRepoURL)
		if ciErr == nil {
			// Use ci_revision (infra commit) for the infra repo, not the app commit
			ciRevision, _ := rawParams["ci_revision"].(string)
			if ciRevision == "" {
				ciRevision = params.Revision
			}
			if ciRevision != "" {
				runs := fetchWorkflowRunsForCommit(ctx.GetLogger(), ciOrg, ciRepo, token, ciRevision, ciRepoURL)
				response.WorkflowRuns = append(response.WorkflowRuns, runs...)
			}
		}
	}

	// Generate insights
	insights := []PlaybookActionResponseInsight{}

	if len(prs) > 0 {
		insights = append(insights, PlaybookActionResponseInsight{
			Message:  fmt.Sprintf("Found %d merged pull request(s)", len(prs)),
			Severity: "info",
		})

		// Add info about the most recent PR
		firstPR := prs[0]
		insights = append(insights, PlaybookActionResponseInsight{
			Message:  fmt.Sprintf("Latest PR #%d: %s (merged by %s)", firstPR.Number, firstPR.Title, firstPR.User.Login),
			Severity: "info",
		})
	} else {
		insights = append(insights, PlaybookActionResponseInsight{
			Message:  "No merged pull requests found",
			Severity: "warning",
		})
	}

	// Add workflow run insights
	for _, run := range response.WorkflowRuns {
		shortSHA := run.CommitSHA[:min(7, len(run.CommitSHA))]
		switch run.Conclusion {
		case "failure":
			insights = append(insights, PlaybookActionResponseInsight{
				Message:  fmt.Sprintf("CI workflow '%s' failed for commit %s", run.Name, shortSHA),
				Severity: "warning",
			})
		case "success":
			insights = append(insights, PlaybookActionResponseInsight{
				Message:  fmt.Sprintf("CI workflow '%s' passed for commit %s", run.Name, shortSHA),
				Severity: "info",
			})
		}
	}

	metadata := map[string]any{
		"query-result-version": "1.0",
		"query":                rawParams,
		"github_repo":          fmt.Sprintf("%s/%s", org, repo),
	}

	additionalInfo := map[string]any{
		"organization": org,
		"repository":   repo,
		"total_prs":    len(prs),
	}

	// Extract labels for downstream actions
	extractedLabels := map[string]any{}
	if params.RepoURL != "" {
		extractedLabels["repo_url"] = params.RepoURL
	}
	if params.Revision != "" {
		extractedLabels["revision"] = params.Revision
	}

	return NewPlaybookActionResponseJsonWithLabels(response, additionalInfo, insights, metadata, extractedLabels), nil
}

// Implement PlaybookAutoAction interface
func (a *githubPRHistoryAction) CanAutoExecute(ctx PlaybookActionContext) bool {
	if !hasGitHubIntegration(ctx.GetTenantId()) {
		return false
	}

	// Path 1: repo_url already in labels (e.g., from ArgoCD action)
	labels := ctx.GetEvent().Labels
	if labels != nil {
		if repoURL, ok := labels["repo_url"]; ok && repoURL != "" {
			return true
		}
	}

	// Path 2: check workload annotations for git repo info
	if ctx.GetEvent().SubjectName == "" {
		return false
	}

	namespace := ctx.GetEvent().SubjectNamespace
	service := ""
	if namespace == "" && ctx.GetEvent().Labels != nil {
		namespace = ctx.GetEvent().Labels["namespace"]
	}
	if ctx.GetEvent().Labels != nil {
		service = ctx.GetEvent().Labels["service"]
	}

	ctx.GetLogger().Info("github_pr_history: CanAutoExecute checking workload annotations",
		"subject_name", ctx.GetEvent().SubjectName, "subject_owner", ctx.GetEvent().SubjectOwner,
		"namespace", namespace, "account_id", ctx.GetAccountId())

	gitInfo, err := fetchRepoURLFromWorkload(ctx.GetAccountId(), ctx.GetEvent().SubjectName, ctx.GetEvent().SubjectOwner, service, namespace)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return false
		}
		ctx.GetLogger().Info("github_pr_history: CanAutoExecute failed to fetch workload git info",
			"error", err, "subject_name", ctx.GetEvent().SubjectName, "account_id", ctx.GetAccountId())
		return false
	}
	if gitInfo.SourceRepoURL == "" && gitInfo.CIRepoURL == "" {
		ctx.GetLogger().Info("github_pr_history: no git annotations found on workload",
			"subject_name", ctx.GetEvent().SubjectName, "account_id", ctx.GetAccountId())
		return false
	}

	ctx.GetLogger().Info("github_pr_history: CanAutoExecute found git annotations",
		"source_repo", gitInfo.SourceRepoURL, "ci_repo", gitInfo.CIRepoURL)
	return true
}

func (a *githubPRHistoryAction) AutoExecute(ctx PlaybookActionContext) (PlaybookActionResponse, error) {
	labels := ctx.GetEvent().Labels

	// Path 1: repo_url from labels (e.g., from ArgoCD action)
	if labels != nil {
		if repoURL, ok := labels["repo_url"]; ok && repoURL != "" {
			params := map[string]any{
				"repo_url": repoURL,
				"limit":    3,
			}
			if revision, ok := labels["revision"]; ok && revision != "" {
				params["revision"] = revision
			}
			return a.Execute(ctx, params)
		}
	}

	// Path 2: fetch repo URL from workload annotations
	namespace := ctx.GetEvent().SubjectNamespace
	service := ""
	if labels != nil {
		if namespace == "" {
			namespace = labels["namespace"]
		}
		service = labels["service"]
	}
	gitInfo, err := fetchRepoURLFromWorkload(ctx.GetAccountId(), ctx.GetEvent().SubjectName, ctx.GetEvent().SubjectOwner, service, namespace)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch repo URL from workload annotations: %w", err)
	}

	// Use source repo (workloads.<domain>/git.repo) for PR history
	repoURL := gitInfo.SourceRepoURL
	if repoURL == "" {
		repoURL = gitInfo.CIRepoURL
	}
	if repoURL == "" {
		return nil, errors.New("no repo_url found in event labels or workload annotations")
	}

	params := map[string]any{
		"repo_url": repoURL,
		"limit":    3,
	}

	// Use the app source commit hash for PR lookup (finds the PR that deployed this service).
	// The CI hash is for the infra repo and would return unrelated PRs in a monorepo.
	if gitInfo.SourceGitHash != "" {
		params["revision"] = gitInfo.SourceGitHash
	}

	// Pass CI repo URL and CI hash so Execute can fetch workflow runs from the infra repo
	if gitInfo.CIRepoURL != "" && gitInfo.CIRepoURL != repoURL {
		params["ci_repo_url"] = gitInfo.CIRepoURL
	}
	if gitInfo.CIGitHash != "" {
		params["ci_revision"] = gitInfo.CIGitHash
	}

	return a.Execute(ctx, params)
}

// fetchRepoURLFromWorkload queries k8s_workloads for git repo annotations.
// It tries to match by service labels, workload name, or subject owner (deployment name for pod events).
// If namespace is provided, it's used to narrow down the match.
func fetchRepoURLFromWorkload(accountId, subjectName, subjectOwner, serviceName, namespace string) (workloadGitInfo, error) {
	dbms, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		return workloadGitInfo{}, err
	}

	if serviceName == "" {
		serviceName = subjectName
	}
	if subjectOwner == "" {
		subjectOwner = subjectName
	}

	// Match by service label (using serviceName), subject name/owner as service label, or workload name.
	// subjectOwner is the deployment/statefulset name when the event subject is a pod.
	query := `
		SELECT meta::text FROM k8s_workloads
		WHERE cloud_account_id = $1
		  AND (labels ->> 'service'::text = $2
		    OR labels ->> 'tags.datadoghq.com/service'::text = $2
		    OR labels ->> 'service'::text = $3
		    OR labels ->> 'tags.datadoghq.com/service'::text = $3
		    OR name = $3
		    OR name = $4
		    OR name IN (SELECT workload_name FROM k8s_pods WHERE cloud_account_id = $1 AND name = $3 AND is_active IS NOT FALSE AND ($5 = '' OR namespace = $5)))
		  AND ($5 = '' OR namespace = $5)
		  AND is_active IS NOT FALSE
		LIMIT 1`

	var metaStr string
	err = dbms.Db.QueryRowx(query, accountId, serviceName, subjectName, subjectOwner, namespace).Scan(&metaStr)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return workloadGitInfo{}, nil
		}
		return workloadGitInfo{}, fmt.Errorf("workload not found for %s: %w", subjectName, err)
	}

	// Parse meta JSON to extract annotations
	var meta map[string]any
	if err := json.Unmarshal([]byte(metaStr), &meta); err != nil {
		return workloadGitInfo{}, fmt.Errorf("failed to parse workload meta: %w", err)
	}

	configMap, ok := meta["config"].(map[string]any)
	if !ok {
		return workloadGitInfo{}, nil
	}

	annotationsMap, ok := configMap["annotations"].(map[string]any)
	if !ok {
		return workloadGitInfo{}, nil
	}

	info := workloadGitInfo{}
	if v, ok := annotationsMap[annotations.WorkloadGitRepo].(string); ok {
		info.SourceRepoURL = v
	}
	if v, ok := annotationsMap[annotations.CIGitRepo].(string); ok {
		info.CIRepoURL = v
	}
	if v, ok := annotationsMap[annotations.WorkloadGitHash].(string); ok && v != "" {
		info.SourceGitHash = v
	}
	if v, ok := annotationsMap[annotations.CIGitHash].(string); ok && v != "" {
		info.CIGitHash = v
	}

	return info, nil
}

// fetchWorkflowRunsForCommit fetches GitHub Actions workflow runs for a specific commit SHA.
// Fails silently — returns empty slice on any error.
func fetchWorkflowRunsForCommit(logger *slog.Logger, org, repo, token, commitSHA, repoURL string) []workflowRunSummary {
	url := fmt.Sprintf("https://api.github.com/repos/%s/%s/actions/runs?head_sha=%s&per_page=5",
		org, repo, commitSHA)

	resp, err := common.HttpGet(url, common.HttpWithHeaders(map[string]string{
		"Authorization": fmt.Sprintf("Bearer %s", token),
		"Accept":        "application/vnd.github.v3+json",
	}))
	if err != nil {
		logger.Debug("failed to fetch GitHub workflow runs", "error", err, "repo", fmt.Sprintf("%s/%s", org, repo))
		return nil
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		logger.Debug("failed to read GitHub workflow runs response", "error", err)
		return nil
	}

	if resp.StatusCode != 200 {
		logger.Debug("GitHub workflow runs API error", "status", resp.StatusCode, "repo", fmt.Sprintf("%s/%s", org, repo))
		return nil
	}

	var runsResp githubWorkflowRunsResponse
	if err := json.Unmarshal(body, &runsResp); err != nil {
		logger.Debug("failed to parse GitHub workflow runs response", "error", err)
		return nil
	}

	var summaries []workflowRunSummary
	for _, run := range runsResp.WorkflowRuns {
		summaries = append(summaries, workflowRunSummary{
			ID:         run.ID,
			Name:       run.Name,
			Status:     run.Status,
			Conclusion: run.Conclusion,
			URL:        run.HTMLURL,
			CommitSHA:  run.HeadSHA,
			Branch:     run.HeadBranch,
			Event:      run.Event,
			RunNumber:  run.RunNumber,
			Actor:      run.Actor.Login,
			CreatedAt:  run.CreatedAt,
			RepoURL:    repoURL,
		})
	}

	return summaries
}

// Helper function to parse parameters
func parseGitHubPRParams(rawParams map[string]any) (githubPRHistoryParams, error) {
	params := githubPRHistoryParams{}

	if repoURL, ok := rawParams["repo_url"].(string); ok {
		params.RepoURL = repoURL
	}

	if revision, ok := rawParams["revision"].(string); ok {
		params.Revision = revision
	}

	if limit, ok := rawParams["limit"].(float64); ok {
		params.Limit = int(limit)
	} else if limit, ok := rawParams["limit"].(int); ok {
		params.Limit = limit
	}

	if accountId, ok := rawParams["account_id"].(string); ok {
		params.AccountId = accountId
	}

	if configName, ok := rawParams["config_name"].(string); ok {
		params.ConfigName = configName
	}

	return params, nil
}

func hasGitHubIntegration(tenantId string) bool {
	dbms, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		return false
	}
	var exists bool
	err = dbms.Db.QueryRowx(
		"SELECT EXISTS(SELECT 1 FROM integrations WHERE tenant_id = $1 AND status = 'enabled' AND type = 'github')",
		tenantId,
	).Scan(&exists)
	return err == nil && exists
}

// Helper function to fetch GitHub credentials from jira_configurations
func fetchGitHubCredentials(tenantId, configName string) (token string, err error) {
	dbms, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		return "", err
	}

	query := `SELECT
	            MAX(CASE WHEN icv.name = 'password' THEN icv.value END) as password,
	            MAX(CASE WHEN icv.name = 'auth_type' THEN icv.value END) as auth_type
              FROM integrations i
              JOIN integration_config_values icv ON i.id = icv.integration_id
              WHERE i.tenant_id = $1 AND i.status = 'enabled' AND i.type = 'github'`

	args := []interface{}{tenantId}

	// If config name is specified, filter by it
	if configName != "" {
		query += " AND i.name = $2"
		args = append(args, configName)
	}

	query += " GROUP BY i.id LIMIT 1"

	var encryptedPassword string
	var authType *string
	err = dbms.Db.QueryRowx(query, args...).Scan(&encryptedPassword, &authType)
	if err != nil {
		return "", fmt.Errorf("GitHub configuration not found: %w", err)
	}

	if encryptedPassword == "" {
		return "", errors.New("GitHub token is empty in configuration")
	}

	// Decrypt the password/installation_id
	password, err := common.Decrypt(encryptedPassword)
	if err != nil {
		return "", fmt.Errorf("failed to decrypt GitHub token: %w", err)
	}

	// For GitHub App auth, password is the installation ID — generate an installation token
	if authType != nil && *authType == "application" {
		token, err = common.GetGithubAppInstallationToken(context.Background(), password)
		if err != nil {
			return "", fmt.Errorf("failed to get GitHub App installation token: %w", err)
		}
		return token, nil
	}

	return password, nil
}

// Helper function to parse repo URL
func parseRepoURL(repoURL string) (org, repo string, err error) {
	// Remove .git suffix if present
	repoURL = strings.TrimSuffix(repoURL, ".git")

	// Remove trailing slashes
	repoURL = strings.TrimSuffix(repoURL, "/")

	// Parse URL: https://github.com/org/repo
	repoURL = strings.TrimPrefix(repoURL, "https://")
	repoURL = strings.TrimPrefix(repoURL, "http://")

	parts := strings.Split(repoURL, "/")
	if len(parts) < 3 || parts[0] != "github.com" {
		return "", "", fmt.Errorf("invalid GitHub repo URL: %s (expected format: https://github.com/org/repo)", repoURL)
	}

	return parts[1], parts[2], nil
}

// Helper function to fetch merged PRs from GitHub API
func fetchMergedPRs(org, repo, token string, limit int) ([]githubPRItem, error) {
	// Fetch more PRs than needed since we'll filter for merged ones
	perPage := limit * 3
	if perPage > 100 {
		perPage = 100 // GitHub API max per page
	}

	url := fmt.Sprintf("https://api.github.com/repos/%s/%s/pulls?state=closed&sort=updated&direction=desc&per_page=%d",
		org, repo, perPage)

	resp, err := common.HttpGet(url, common.HttpWithHeaders(map[string]string{
		"Authorization": fmt.Sprintf("Bearer %s", token),
		"Accept":        "application/vnd.github.v3+json",
	}))
	if err != nil {
		return nil, fmt.Errorf("GitHub API request failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read GitHub API response: %w", err)
	}

	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("GitHub API error (status %d): %s", resp.StatusCode, string(body))
	}

	var prs []githubPRItem
	err = json.Unmarshal(body, &prs)
	if err != nil {
		return nil, fmt.Errorf("failed to parse GitHub API response: %w", err)
	}

	// Filter for merged PRs only and limit to requested count
	var mergedPRs []githubPRItem
	for _, pr := range prs {
		if pr.MergedAt != nil {
			mergedPRs = append(mergedPRs, pr)
			if len(mergedPRs) >= limit {
				break
			}
		}
	}

	return mergedPRs, nil
}

// fetchPRsForCommit finds PRs associated with a specific commit using
// GET /repos/{owner}/{repo}/commits/{sha}/pulls.
// This is more accurate than fetchMergedPRs for monorepos because it returns
// only the PR(s) that introduced the given commit, not the latest repo-wide PRs.
func fetchPRsForCommit(org, repo, token, commitSHA string) ([]githubPRItem, error) {
	url := fmt.Sprintf("https://api.github.com/repos/%s/%s/commits/%s/pulls",
		org, repo, commitSHA)

	resp, err := common.HttpGet(url, common.HttpWithHeaders(map[string]string{
		"Authorization": fmt.Sprintf("Bearer %s", token),
		"Accept":        "application/vnd.github.v3+json",
	}))
	if err != nil {
		return nil, fmt.Errorf("GitHub API request failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read GitHub API response: %w", err)
	}

	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("GitHub API error (status %d): %s", resp.StatusCode, string(body))
	}

	var prs []githubPRItem
	if err := json.Unmarshal(body, &prs); err != nil {
		return nil, fmt.Errorf("failed to parse GitHub API response: %w", err)
	}

	// Filter for merged PRs only
	var mergedPRs []githubPRItem
	for _, pr := range prs {
		if pr.MergedAt != nil {
			mergedPRs = append(mergedPRs, pr)
		}
	}

	return mergedPRs, nil
}
