package adapter

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"nudgebee/services/common"
	"nudgebee/services/config"
	"nudgebee/services/internal/annotations"
	"nudgebee/services/internal/database"
	"nudgebee/services/internal/database/models"
	"nudgebee/services/llm"
	"nudgebee/services/security"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/jmoiron/sqlx"

	"go.opentelemetry.io/otel/trace"
)

// gitlabAdapter implements the AccountAdapter interface for GitLab
type gitlabAdapter struct{}

// gitLabDetailFromDeployment contains git details extracted from deployment annotations for GitLab
type gitLabDetailFromDeployment struct {
	ProjectPath string            // e.g., "group/project" or "group/subgroup/project"
	BaseBranch  string            // target branch for MR
	Token       string            // personal access token
	Username    string            // gitlab username
	FilePath    string            // path to values file
	Annotations map[string]string // workload annotations
	Sha1        string            // specific commit hash
	GitLabURL   string            // gitlab instance URL (default: https://gitlab.com)
}

// getGitLabCredentials fetches GitLab credentials from the integrations table
func getGitLabCredentials(ctx AccountAdapterContext, ticketProvider string) (string, string, string, error) {
	ctx.GetLogger().Info("DEBUG: getGitLabCredentials called",
		"ticket_provider", ticketProvider,
		"tenant_id", ctx.GetSecurityContext().GetTenantId())
	dbms, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		return "", "", "", err
	}

	var rows *sqlx.Rows
	if ticketProvider != "" {
		rows, err = dbms.Db.Queryx(`
			SELECT
				COALESCE(MAX(CASE WHEN icv.name = 'username' THEN icv.value END), '') as username,
				COALESCE(MAX(CASE WHEN icv.name = 'url' THEN icv.value END), 'https://gitlab.com') as url,
				COALESCE(MAX(CASE WHEN icv.name = 'password' THEN icv.value END), '') as password
			FROM integrations i
			JOIN integration_config_values icv ON i.id = icv.integration_id
			WHERE i.tenant_id = $1 AND i.name = $2 AND i.status = 'enabled' AND i.type = 'gitlab'
			GROUP BY i.id
		`, ctx.GetSecurityContext().GetTenantId(), ticketProvider)
	} else {
		rows, err = dbms.Db.Queryx(`
			SELECT
				COALESCE(MAX(CASE WHEN icv.name = 'username' THEN icv.value END), '') as username,
				COALESCE(MAX(CASE WHEN icv.name = 'url' THEN icv.value END), 'https://gitlab.com') as url,
				COALESCE(MAX(CASE WHEN icv.name = 'password' THEN icv.value END), '') as password
			FROM integrations i
			JOIN integration_config_values icv ON i.id = icv.integration_id
			WHERE i.tenant_id = $1 AND i.status = 'enabled' AND i.type = 'gitlab'
			GROUP BY i.id
		`, ctx.GetSecurityContext().GetTenantId())
	}
	if err != nil {
		return "", "", "", err
	}
	defer func() {
		err := rows.Close()
		if err != nil {
			slog.Error("Error closing rows", "error", err)
		}
	}()

	var username string
	var gitURL string
	var password string

	for rows.Next() {
		err := rows.Scan(&username, &gitURL, &password)
		if err != nil {
			return "", "", "", err
		}
	}

	if password == "" {
		return "", "", "", common.ErrorNotFound("error: gitlab integration not found")
	}

	// Decrypt the password/token
	// password, err = common.Decrypt(password)
	if err != nil {
		ctx.GetLogger().Error("error decrypting password", "error", err)
		return "", "", "", common.ErrorInternal("error: unable to process request")
	}
	return gitURL, username, password, nil
}

// checkoutCodeRepoGitLab clones a GitLab repository to a temporary directory
func validateGitLabDetails(gitDetails gitLabDetailFromDeployment) error {
	if gitDetails.BaseBranch != "" && !isValidGitRef(gitDetails.BaseBranch) {
		return fmt.Errorf("invalid git branch name: %q", gitDetails.BaseBranch)
	}
	if gitDetails.ProjectPath != "" && !isValidGitRef(gitDetails.ProjectPath) {
		return fmt.Errorf("invalid gitlab project path: %q", gitDetails.ProjectPath)
	}
	if gitDetails.Sha1 != "" && !validSha1.MatchString(gitDetails.Sha1) {
		return fmt.Errorf("invalid git SHA: %q", gitDetails.Sha1)
	}
	return nil
}

func checkoutCodeRepoGitLab(ctx AccountAdapterContext, request ApplyRecommendationRequest, gitDetails gitLabDetailFromDeployment) (string, error) {
	if err := validateGitLabDetails(gitDetails); err != nil {
		return "", err
	}

	dir, err := os.MkdirTemp("", "checkout")
	if err != nil {
		ctx.GetLogger().Error("Error creating temp dir", "error", err)
		return "", err
	}

	// Construct the GitLab clone URL
	// Format: https://oauth2:<token>@gitlab.com/group/project.git
	gitLabHost := strings.TrimPrefix(gitDetails.GitLabURL, "https://")
	gitLabHost = strings.TrimPrefix(gitLabHost, "http://")

	var gitURL string
	if gitDetails.Token == "" {
		gitURL = fmt.Sprintf("https://%s/%s.git", gitLabHost, gitDetails.ProjectPath)
	} else {
		gitURL = fmt.Sprintf("https://oauth2:%s@%s/%s.git", gitDetails.Token, gitLabHost, gitDetails.ProjectPath)
	}

	cmd := exec.Command("git", "clone", "--depth", "1", "-b", gitDetails.BaseBranch, gitURL, dir)
	output, err := cmd.CombinedOutput()
	if err != nil {
		ctx.GetLogger().Error("Error cloning repo", "error", err, "output", string(output))
		return "", err
	}

	if gitDetails.Sha1 != "" {
		cmdFetch := exec.Command("git", "-C", dir, "fetch", "--depth=1", "origin", gitDetails.Sha1)
		if output, err := cmdFetch.CombinedOutput(); err != nil {
			return "", fmt.Errorf("fetch specific commit failed: %s, %v", output, err)
		}
		cmdCheckout := exec.Command("git", "-C", dir, "checkout", gitDetails.Sha1)
		if output, err := cmdCheckout.CombinedOutput(); err != nil {
			return "", fmt.Errorf("checkout specific commit failed: %s, %v", output, err)
		}
	}
	return dir, nil
}

// commitCodeGitLab creates a new branch and commits changes for GitLab
func commitCodeGitLab(ctx AccountAdapterContext, dir string, request ApplyRecommendationRequest, gitDetails gitLabDetailFromDeployment, updateExistingMR bool) (string, error) {
	branchName := "nb/" + request.Recommendation.Id
	if updateExistingMR {
		cmd := exec.Command("git", "remote", "set-branches", "--add", "origin", branchName)
		cmd.Dir = dir
		output, err := cmd.Output()
		if err != nil {
			ctx.GetLogger().Error("Error getting remote branch", "error", err, "output", string(output), "branch", branchName)
			return "", err
		}
		cmd1 := exec.Command("git", "fetch")
		cmd1.Dir = dir
		output1, err1 := cmd1.Output()
		if err1 != nil {
			ctx.GetLogger().Error("Error fetching remote branch", "error", err1, "output", string(output1), "branch", branchName)
			return "", err1
		}
		cmd2 := exec.Command("git", "checkout", "-b", branchName, "origin/"+branchName)
		cmd2.Dir = dir
		output2, err2 := cmd2.Output()
		if err2 != nil {
			ctx.GetLogger().Error("Error checking out remote branch", "error", err2, "output", string(output2), "branch", branchName)
			return "", err2
		}
	} else {
		cmd := exec.Command("git", "checkout", "-b", branchName)
		cmd.Dir = dir
		output, err := cmd.Output()
		if err != nil {
			ctx.GetLogger().Error("Error checking out branch", "error", err, "output", string(output), "branch", branchName)
			return "", err
		}
	}

	err := updateCode(ctx, dir, request, gitDetailFromDeployment{
		Annotations: gitDetails.Annotations,
		FilePath:    gitDetails.FilePath,
	})
	if err != nil {
		ctx.GetLogger().Error("Error updating code", "error", err)
		return "", err
	}

	cmd := exec.Command("git", "status", "-s")
	cmd.Dir = dir
	output, err := cmd.Output()
	if err != nil {
		ctx.GetLogger().Error("Error getting status of git for files", "error", err, "output", string(output))
		return "", err
	}

	if len(string(output)) == 0 {
		return "", fmt.Errorf("no changes found")
	}

	// commit file
	cmd = exec.Command("git", "add", ".")
	cmd.Dir = dir
	output, err = cmd.Output()
	if err != nil {
		ctx.GetLogger().Error("Error adding files to commit", "error", err, "output", string(output))
		return "", err
	}

	// Configure user email
	cmd = exec.Command("git", "config", "user.email", config.Config.GitCommitNudgebeeEmail)
	cmd.Dir = dir
	output, err = cmd.CombinedOutput()
	if err != nil {
		ctx.GetLogger().Error("Error configuring user email", "error", err, "output", string(output))
		return "", err
	}

	// Configure user name
	cmd = exec.Command("git", "config", "user.name", config.Config.GitCommitNudgebeeUser)
	cmd.Dir = dir
	output, err = cmd.CombinedOutput()
	if err != nil {
		ctx.GetLogger().Error("Error configuring user name", "error", err, "output", string(output))
		return "", err
	}

	// Commit files
	cmd = exec.Command("git", "commit", "-m", fmt.Sprintf("ci: Updated %s %s", request.ResolverType, request.Recommendation.Id))
	cmd.Dir = dir
	output, err = cmd.CombinedOutput()
	if err != nil {
		ctx.GetLogger().Error("Error committing files", "error", err, "output", string(output))
		return "", err
	}

	return branchName, nil
}

// commitCodeForEventGitLab handles event-based code commits for GitLab
func commitCodeForEventGitLab(ctx AccountAdapterContext, dir string, request ApplyRecommendationRequest, gitDetails gitLabDetailFromDeployment, updateExistingMR bool) (string, error) {
	branchName := "nb/" + request.Recommendation.Id
	if updateExistingMR {
		cmd := exec.Command("git", "remote", "set-branches", "--add", "origin", branchName)
		cmd.Dir = dir
		output, err := cmd.Output()
		if err != nil {
			ctx.GetLogger().Error("Error getting remote branch", "error", err, "output", string(output), "branch", branchName)
			return "", err
		}
		cmd1 := exec.Command("git", "fetch")
		cmd1.Dir = dir
		output1, err1 := cmd1.Output()
		if err1 != nil {
			ctx.GetLogger().Error("Error fetching remote branch", "error", err, "output", string(output1), "branch", branchName)
			return "", err
		}
		cmd2 := exec.Command("git", "checkout", "-b", branchName, "origin/"+branchName)
		cmd2.Dir = dir
		output2, err2 := cmd2.Output()
		if err2 != nil {
			ctx.GetLogger().Error("Error checking out remote branch", "error", err, "output", string(output2), "branch", branchName)
			return "", err
		}
	} else {
		cmd := exec.Command("git", "checkout", "-b", branchName)
		cmd.Dir = dir
		output, err := cmd.Output()
		if err != nil {
			ctx.GetLogger().Error("Error checking out branch", "error", err, "output", string(output), "branch", branchName)
			return "", err
		}
	}

	recommendationMap, _ := request.Recommendation.Recommendation.Object().(map[string]any)
	fileName, _ := recommendationMap["fileName"].(string)
	lineNumber := recommendationMap["lineNumber"].(int)
	newLine, _ := recommendationMap["newLine"].(string)
	oldLine, _ := recommendationMap["oldLine"].(string)

	safeFile, err := safeFilePath(dir, fileName)
	if err != nil {
		return "", err
	}
	err = readUpdateCodeFile(safeFile, lineNumber, newLine, oldLine)
	if err != nil {
		ctx.GetLogger().Error("Error updating code", "error", err)
		return "", err
	}

	cmd := exec.Command("git", "status", "-s")
	cmd.Dir = dir
	output, err := cmd.Output()
	if err != nil {
		ctx.GetLogger().Error("Error getting status of git for files", "error", err, "output", string(output))
		return "", err
	}

	if len(string(output)) == 0 {
		return "", fmt.Errorf("no changes found")
	}

	// commit file
	cmd = exec.Command("git", "add", ".")
	cmd.Dir = dir
	output, err = cmd.Output()
	if err != nil {
		ctx.GetLogger().Error("Error adding files to commit", "error", err, "output", string(output))
		return "", err
	}

	// Configure user email
	cmd = exec.Command("git", "config", "user.email", config.Config.GitCommitNudgebeeEmail)
	cmd.Dir = dir
	output, err = cmd.CombinedOutput()
	if err != nil {
		ctx.GetLogger().Error("Error configuring user email", "error", err, "output", string(output))
		return "", err
	}

	// Configure user name
	cmd = exec.Command("git", "config", "user.name", config.Config.GitCommitNudgebeeUser)
	cmd.Dir = dir
	output, err = cmd.CombinedOutput()
	if err != nil {
		ctx.GetLogger().Error("Error configuring user name", "error", err, "output", string(output))
		return "", err
	}

	// Commit files
	cmd = exec.Command("git", "commit", "-m", fmt.Sprintf("ci: Updated %s %s", request.ResolverType, request.Recommendation.Id))
	cmd.Dir = dir
	output, err = cmd.CombinedOutput()
	if err != nil {
		ctx.GetLogger().Error("Error committing files", "error", err, "output", string(output))
		return "", err
	}

	return branchName, nil
}

// raiseMrForCodeRepo pushes the branch and creates a Merge Request in GitLab
func raiseMrForCodeRepo(ctx AccountAdapterContext, dir string, branchName string, gitDetail gitLabDetailFromDeployment, updateExistingMR bool, existingMRIID int, mrBody string, resolverType string, mrTitle string) (string, error) {
	// push branch
	cmd := exec.Command("git", "push", "origin", branchName, "-f")
	cmd.Dir = dir
	output, err := cmd.Output()
	if err != nil {
		ctx.GetLogger().Error("Error pushing branch", "error", err, "output", string(output))
		return "", err
	}

	// GitLab API base URL
	gitLabAPIBase := strings.TrimSuffix(gitDetail.GitLabURL, "/") + "/api/v4"

	// URL-encode the project path for API calls
	encodedProjectPath := strings.ReplaceAll(gitDetail.ProjectPath, "/", "%2F")

	// Update or create MR
	if updateExistingMR {
		resp, err := common.HttpPut(
			fmt.Sprintf("%s/projects/%s/merge_requests/%d", gitLabAPIBase, encodedProjectPath, existingMRIID),
			common.HttpWithHeaders(map[string]string{
				"PRIVATE-TOKEN": gitDetail.Token,
				"Content-Type":  "application/json",
			}),
			common.HttpWithJsonBody(map[string]any{
				"description": "Automated MR for recommendation update. Please review and merge.\n" + mrBody,
			}),
		)

		if err != nil {
			ctx.GetLogger().Error("Error updating existing MR", "error", err)
			return "", err
		}

		defer func() {
			err := resp.Body.Close()
			if err != nil {
				ctx.GetLogger().Error("Error closing response body", "error", err)
			}
		}()

		data, err := io.ReadAll(resp.Body)
		if err != nil {
			ctx.GetLogger().Error("Error reading existing MR response body", "error", err)
			return "", err
		}

		if resp.StatusCode != 200 {
			ctx.GetLogger().Error("Error updating existing MR", "status", resp.StatusCode, "data", string(data))
			return "", fmt.Errorf("error updating existing MR: %s", string(data))
		}

		return string(data), err
	}

	if mrTitle == "" {
		mrTitle = fmt.Sprintf("ci: Updated %s %s", resolverType, branchName)
	}

	resp, err := common.HttpPost(
		fmt.Sprintf("%s/projects/%s/merge_requests", gitLabAPIBase, encodedProjectPath),
		common.HttpWithHeaders(map[string]string{
			"PRIVATE-TOKEN": gitDetail.Token,
			"Content-Type":  "application/json",
		}),
		common.HttpWithJsonBody(map[string]any{
			"title":         mrTitle,
			"source_branch": branchName,
			"target_branch": gitDetail.BaseBranch,
			"description":   fmt.Sprintf("Automated MR for %s update. Please review and merge.\n", resolverType) + mrBody,
		}),
	)

	if err != nil {
		ctx.GetLogger().Error("Error raising MR", "error", err)
		return "", err
	}

	defer func() {
		err := resp.Body.Close()
		if err != nil {
			ctx.GetLogger().Error("Error closing response body", "error", err)
		}
	}()

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		ctx.GetLogger().Error("Error reading response body", "error", err)
		return "", err
	}

	if resp.StatusCode != 201 {
		ctx.GetLogger().Error("Error raising MR", "status", resp.StatusCode, "data", string(data))
		return "", fmt.Errorf("error raising MR: %s", string(data))
	}

	return string(data), err
}

// getGitLabDetailsFromRecommendation extracts GitLab repository details from recommendation metadata
func getGitLabDetailsFromRecommendation(ctx AccountAdapterContext, request ApplyRecommendationRequest) (gitLabDetailFromDeployment, error) {
	dbms, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		return gitLabDetailFromDeployment{}, err
	}

	var providerName string
	if len(request.ProviderConfig) > 0 && request.ProviderConfig["name"] != nil {
		providerName = request.ProviderConfig["name"].(string)
	}

	gitLabURL, username, password, err := getGitLabCredentials(ctx, providerName)
	if err != nil {
		return gitLabDetailFromDeployment{}, err
	}

	metaData, ok := request.Resource.Meta.Object().(map[string]any)
	if !ok {
		ctx.GetLogger().Error("error getting resource meta")
		return gitLabDetailFromDeployment{}, common.ErrorNotFound("error: resource meta not found")
	}

	controllerKind, ok := metaData["controllerKind"].(string)
	if !ok {
		ctx.GetLogger().Error("error getting controller kind")
		return gitLabDetailFromDeployment{}, common.ErrorNotFound("error: controller kind not found")
	}

	controllerName, ok := metaData["controller"].(string)
	if !ok {
		controllerName = *request.Resource.Name
	}

	controllerNamespace, ok := metaData["namespace"].(string)
	if !ok {
		ctx.GetLogger().Error("error getting controller namespace")
		return gitLabDetailFromDeployment{}, common.ErrorNotFound("error: controller namespace not found")
	}

	// get deployment
	rows, err := dbms.Db.Queryx("SELECT meta::varchar FROM k8s_workloads WHERE tenant_id = $1 and cloud_account_id = $2 and kind = $3 and namespace = $4 and name = $5 and is_active = true ", ctx.GetSecurityContext().GetTenantId(), request.Recommendation.CloudAccountId, controllerKind, controllerNamespace, controllerName)
	if err != nil {
		return gitLabDetailFromDeployment{}, err
	}
	defer func() {
		err := rows.Close()
		if err != nil {
			slog.Error("Error closing rows", "error", err)
		}
	}()

	var meta string
	for rows.Next() {
		err := rows.Scan(&meta)
		if err != nil {
			return gitLabDetailFromDeployment{}, err
		}
	}

	workloadMetadata := make(map[string]any)
	err = common.UnmarshalJson([]byte(meta), &workloadMetadata)
	if err != nil {
		ctx.GetLogger().Error("error unmarshalling workload metadata")
		return gitLabDetailFromDeployment{}, err
	}

	workloadMetadataConfig, ok := workloadMetadata["config"]
	if !ok {
		ctx.GetLogger().Error("error getting workload metadata config")
		return gitLabDetailFromDeployment{}, common.ErrorNotFound("error: workload metadata config not found")
	}

	workloadAnnotations := workloadMetadataConfig.(map[string]any)["annotations"]
	if !ok {
		ctx.GetLogger().Error("error getting workload metadata annotations")
		return gitLabDetailFromDeployment{}, common.ErrorNotFound("error: workload metadata annotations not found")
	}

	workloadAnnotationsMap := map[string]string{}
	for key, value := range workloadAnnotations.(map[string]any) {
		workloadAnnotationsMap[key] = value.(string)
	}

	// Strategy 1: Try Nudgebee annotations first (supports both github and gitlab repos)
	gitlabRepo := workloadAnnotationsMap[annotations.CIGitRepo]
	orgBranch := workloadAnnotationsMap[annotations.CIGitBranch]
	filePath := workloadAnnotationsMap[annotations.CIHelmValuesPath]

	// Strategy 2: Check cloud_resource_attributes for manually configured git details
	if gitlabRepo == "" {
		ctx.GetLogger().Info("No annotations found, checking cloud_resource_attributes for manual mapping")

		// Get cloud_resource_id from k8s_workloads
		var workloadResourceId string
		err := dbms.Db.Get(&workloadResourceId,
			`SELECT cloud_resource_id::varchar FROM k8s_workloads
			 WHERE tenant_id = $1 AND cloud_account_id = $2 AND kind = $3
			 AND namespace = $4 AND name = $5 AND is_active = true`,
			ctx.GetSecurityContext().GetTenantId(), request.Recommendation.CloudAccountId,
			controllerKind, controllerNamespace, controllerName)

		if err == nil && workloadResourceId != "" {
			// Fetch attributes from cloud_resource_attributes
			var attributes []struct {
				Name  string `db:"name"`
				Value string `db:"value"`
			}
			err := dbms.Db.Select(&attributes,
				`SELECT name, value FROM cloud_resource_attributes
				 WHERE resource_id = $1 AND (name LIKE $2 OR name LIKE $3)`,
				workloadResourceId, annotations.CIPrefix+"/%", annotations.WorkloadsPrefix+"/%")

			if err == nil {
				for _, attr := range attributes {
					switch attr.Name {
					case annotations.CIGitRepo:
						gitlabRepo = attr.Value
					case annotations.CIGitBranch:
						orgBranch = attr.Value
					case annotations.CIHelmValuesPath:
						filePath = attr.Value
					case annotations.WorkloadGitRepo:
						if gitlabRepo == "" {
							gitlabRepo = attr.Value
						}
					}
				}
				if gitlabRepo != "" {
					ctx.GetLogger().Info("Found git details from cloud_resource_attributes", "repo", gitlabRepo)
				}
			}
		}
	}

	// If still no repo found, return error
	if gitlabRepo == "" {
		return gitLabDetailFromDeployment{}, common.ErrorNotFound("error: gitlab repo not found in workload annotations or manual mapping")
	}

	// Extract project path from GitLab URL
	// GitLab URLs can be: https://gitlab.com/group/project or https://gitlab.example.com/group/subgroup/project
	projectPath, err := extractGitLabProjectPath(gitlabRepo, gitLabURL)
	if err != nil {
		ctx.GetLogger().Error("error extracting project path", "repo", gitlabRepo, "error", err)
		return gitLabDetailFromDeployment{}, err
	}

	// Set defaults
	if orgBranch == "" {
		orgBranch = "main"
	}
	if filePath == "" {
		filePath = "values.yaml"
	}

	recommendationMap, _ := request.Recommendation.Recommendation.Object().(map[string]any)
	sha1, _ := recommendationMap["sha1"].(string)

	return gitLabDetailFromDeployment{
		Token:       password,
		Username:    username,
		ProjectPath: projectPath,
		BaseBranch:  orgBranch,
		Annotations: workloadAnnotationsMap,
		FilePath:    filePath,
		Sha1:        sha1,
		GitLabURL:   gitLabURL,
	}, nil
}

// extractGitLabProjectPath extracts the project path from a GitLab URL
// e.g., "https://gitlab.com/group/project" -> "group/project"
// e.g., "https://gitlab.example.com/group/subgroup/project" -> "group/subgroup/project"
func extractGitLabProjectPath(repoURL string, configuredGitLabURL string) (string, error) {
	// Remove trailing .git if present
	repoURL = strings.TrimSuffix(repoURL, ".git")

	// Try to extract from the configured GitLab URL
	gitLabHost := strings.TrimPrefix(configuredGitLabURL, "https://")
	gitLabHost = strings.TrimPrefix(gitLabHost, "http://")
	gitLabHost = strings.TrimSuffix(gitLabHost, "/")

	// Check if the repo URL starts with the configured GitLab URL
	if strings.Contains(repoURL, gitLabHost) {
		parts := strings.SplitN(repoURL, gitLabHost+"/", 2)
		if len(parts) == 2 && parts[1] != "" {
			return parts[1], nil
		}
	}

	// Fallback: try to extract from common URL formats
	// Format: https://gitlab.com/group/project or gitlab.com/group/project
	for _, prefix := range []string{"https://", "http://", ""} {
		for _, host := range []string{"gitlab.com/", gitLabHost + "/"} {
			fullPrefix := prefix + host
			if strings.HasPrefix(repoURL, fullPrefix) {
				path := strings.TrimPrefix(repoURL, fullPrefix)
				if path != "" {
					return path, nil
				}
			}
		}
	}

	return "", fmt.Errorf("could not extract project path from URL: %s", repoURL)
}

// ApplyRecommendation implements the AccountAdapter interface for GitLab
func (g *gitlabAdapter) ApplyRecommendation(ctx AccountAdapterContext, request ApplyRecommendationRequest, existingRecommendations []models.RecommendationResolution, recommendResolutionId string) (ApplyRecommendationResponse, error) {
	ctx.GetLogger().Info("DEBUG: GitLab adapter ApplyRecommendation called",
		"category", request.Recommendation.Category,
		"rule_name", request.Recommendation.RuleName,
		"recommendation_id", request.Recommendation.Id,
		"provider_config", request.ProviderConfig)
	updateExistingMR := false
	var existingMRIID int
	mrBody := ""
	mrTitle := ""

	switch {
	case request.Recommendation.Category == "RightSizing" && request.Recommendation.RuleName == "pod_right_sizing":
		if request.Resource.Id == "" {
			return ApplyRecommendationResponse{}, fmt.Errorf("rule is not supported for empty resource id")
		}

		// Get git details from the recommendation
		gitDetail, err := getGitLabDetailsFromRecommendation(ctx, request)
		if err != nil {
			return ApplyRecommendationResponse{}, err
		}

		// Use code agent to apply rightsizing
		err = ApplyRightsizingRecommendationUsingCodeAgentGitLab(ctx, request, gitDetail, recommendResolutionId)
		if err != nil {
			return ApplyRecommendationResponse{}, err
		}

		return ApplyRecommendationResponse{
			Data:                     map[string]any{},
			Status:                   RecommendationResolutionStatusInProgress,
			ResolutionType:           RecommendationResolutionTypePullRequest,
			ResolutionTypeRefrenceId: "",
			StatusMessage:            string(models.RecommendationResolutionStatusInProgress),
		}, nil

	case request.Recommendation.Category == "RightSizing" && request.Recommendation.RuleName == "replica_right_sizing":
		if request.Resource.Id == "" {
			return ApplyRecommendationResponse{}, fmt.Errorf("rule is not supported for empty resource id")
		}

		// Get git details from the recommendation
		gitDetail, err := getGitLabDetailsFromRecommendation(ctx, request)
		if err != nil {
			return ApplyRecommendationResponse{}, err
		}

		// Use code agent to apply replica rightsizing
		err = ApplyRightsizingRecommendationUsingCodeAgentGitLab(ctx, request, gitDetail, recommendResolutionId)
		if err != nil {
			return ApplyRecommendationResponse{}, err
		}

		return ApplyRecommendationResponse{
			Data:                     map[string]any{},
			Status:                   RecommendationResolutionStatusInProgress,
			ResolutionType:           RecommendationResolutionTypePullRequest,
			ResolutionTypeRefrenceId: "",
			StatusMessage:            string(models.RecommendationResolutionStatusInProgress),
		}, nil

	case request.Recommendation.Category == "EventResolutionRaisePr":
		gitDetail, err := getGitLabDetailsFromRecommendation(ctx, request)
		if err != nil {
			return ApplyRecommendationResponse{}, err
		}

		ReferenceLink := ""
		ReferenceLink = fmt.Sprintf("%s/investigate?id=%s&accountId=%s", os.Getenv("BASE_URL"), request.Recommendation.Id, request.Recommendation.CloudAccountId)
		if request.ReferenceLink != nil {
			ReferenceLink = *request.ReferenceLink
		}

		mrBody = mrBody + fmt.Sprintf("\nFor more details. Please visit [Nudgebee](%s)", ReferenceLink)
		go func() {
			dbms, err := database.GetDatabaseManager(database.Metastore)
			if err != nil {
				ctx.GetLogger().Error("failed recommendation resolution db connection", "error", err)
				return
			}

			// checkout code repo
			dir, err := checkoutCodeRepoGitLab(ctx, request, gitDetail)
			if err != nil {
				ctx.GetLogger().Error("Error doing checkout", "error", err)
				_, err = dbms.Db.Exec(`UPDATE event_resolution SET status = $1, updated_at = $2, status_message = $4 WHERE id = $3`,
					models.RecommendationResolutionStatusFailed, time.Now(), recommendResolutionId, "Failed at checkout the Code")
				if err != nil {
					ctx.GetLogger().Error("error updating recommendation resolution at checkout", "error", err)
				}
				return
			}
			defer func() {
				err := os.RemoveAll(dir)
				if err != nil {
					ctx.GetLogger().Error("Error removing temp dir", "error", err)
					_, err = dbms.Db.Exec(`UPDATE event_resolution SET status = $1, updated_at = $2, status_message = $4 WHERE id = $3`,
						models.RecommendationResolutionStatusFailed, time.Now(), recommendResolutionId, "Failed at Remove temp dir")
					if err != nil {
						ctx.GetLogger().Error("error updating recommendation resolution at remove dir", "error", err)
					}
				}
			}()

			// update code
			branchName, err := commitCodeForEventGitLab(ctx, dir, request, gitDetail, updateExistingMR)
			if err != nil {
				message := "Failed at committing the Code"
				status := models.RecommendationResolutionStatusFailed
				if err.Error() == "No Changes Found" {
					message = "No Changes Found"
					status = models.RecommendationResolutionStatusSuccess
				}
				ctx.GetLogger().Error("Error committing the code", "error", err)
				_, err = dbms.Db.Exec(`UPDATE event_resolution SET status = $1, updated_at = $2, status_message = $4 WHERE id = $3`,
					status, time.Now(), recommendResolutionId, message)
				if err != nil {
					ctx.GetLogger().Error("error updating recommendation resolution at commit code", "error", err)
				}
				return
			}

			// raise MR
			resp, err := raiseMrForCodeRepo(ctx, dir, branchName, gitDetail, updateExistingMR, existingMRIID, mrBody, request.ResolverType, mrTitle)
			if err != nil {
				ctx.GetLogger().Error("Error raising Merge Request", "error", err)
				_, err = dbms.Db.Exec(`UPDATE event_resolution SET status = $1, updated_at = $2, status_message = $4 WHERE id = $3`,
					models.RecommendationResolutionStatusFailed, time.Now(), recommendResolutionId, "Failed at raising MR")
				if err != nil {
					ctx.GetLogger().Error("error updating recommendation resolution at raise mr", "error", err)
				}
				return
			}

			mrResponse := map[string]any{}
			err = common.UnmarshalJson([]byte(resp), &mrResponse)
			if err != nil {
				ctx.GetLogger().Error("Error unmarshalling MR response", "error", err)
				_, err = dbms.Db.Exec(`UPDATE event_resolution SET status = $1, updated_at = $2, status_message = $4 WHERE id = $3`,
					models.RecommendationResolutionStatusFailed, time.Now(), recommendResolutionId, "Failed at unmarshalling MR Response")
				if err != nil {
					ctx.GetLogger().Error("error updating recommendation resolution", "error", err)
				}
				return
			}

			_, err = dbms.Db.Exec(`UPDATE event_resolution SET status = $1, updated_at = $2, type_reference_id = $5, status_message = $4 WHERE id = $3`,
				models.RecommendationStatusInProgress, time.Now(), recommendResolutionId, "MR raised successfully", mrResponse["web_url"].(string))
			if err != nil {
				ctx.GetLogger().Error("error updating recommendation resolution", "error", err)
			}
		}()

		return ApplyRecommendationResponse{
			Data:                     map[string]any{},
			Status:                   RecommendationResolutionStatusInProgress,
			ResolutionType:           RecommendationResolutionTypePullRequest,
			ResolutionTypeRefrenceId: "",
			StatusMessage:            string(models.RecommendationResolutionStatusInProgress),
		}, nil

	default:
		return ApplyRecommendationResponse{}, fmt.Errorf("unsupported recommendation category: %s, rule: %s", request.Recommendation.Category, request.Recommendation.RuleName)
	}
}

// GetRecommendationResolutionStatus implements the AccountAdapter interface for GitLab
func (g *gitlabAdapter) GetRecommendationResolutionStatus(ctx AccountAdapterContext, recommendation models.Recommendation, resolutionReferenceId string, applyRequestPayload models.Json, resolutionStatusMessage string) (GetRecommendationResolutionStatusResponse, error) {
	// get MR status from GitLab API and update the status
	applyRequestPayloadMap, ok := applyRequestPayload.Object().(map[string]any)
	if !ok {
		ctx.GetLogger().Error("error getting apply request payload")
		return GetRecommendationResolutionStatusResponse{}, errors.New("error getting apply request payload")
	}

	if applyRequestPayloadMap["provider_config"] == nil {
		return GetRecommendationResolutionStatusResponse{}, common.ErrorNotFound("error: provider config not found")
	}

	providerConfigMap, ok := applyRequestPayloadMap["provider_config"].(map[string]any)
	if !ok {
		ctx.GetLogger().Error("error getting provider config")
		return GetRecommendationResolutionStatusResponse{}, errors.New("error getting provider config")
	}

	var providerName string
	if providerConfigMap["name"] != nil && providerConfigMap["name"] != "" {
		providerName = providerConfigMap["name"].(string)
	}

	// get MR id, project from the MR url
	mrURL := resolutionReferenceId

	// Handle case where MR URL is not yet available (async MR creation in progress)
	if mrURL == "" {
		ctx.GetLogger().Info("MR URL not yet available, resolution still in progress", "recommendation", recommendation.Id)
		return GetRecommendationResolutionStatusResponse{
			Status:        RecommendationResolutionStatusInProgress,
			StatusMessage: "MR creation in progress",
		}, nil
	}

	gitLabURL, _, password, err := getGitLabCredentials(ctx, providerName)
	if err != nil {
		return GetRecommendationResolutionStatusResponse{}, err
	}

	// Parse MR URL to extract project path and MR IID
	// GitLab MR URL format: https://gitlab.com/group/project/-/merge_requests/123
	projectPath, mrIID, err := parseGitLabMRURL(mrURL, gitLabURL)
	if err != nil {
		ctx.GetLogger().Error("Invalid GitLab MR URL format", "url", mrURL, "recommendation", recommendation.Id, "error", err)
		return GetRecommendationResolutionStatusResponse{
			Status:        RecommendationResolutionStatusFailed,
			StatusMessage: fmt.Sprintf("Invalid GitLab MR URL format: %s", mrURL),
		}, nil
	}

	// URL-encode the project path for API calls
	encodedProjectPath := strings.ReplaceAll(projectPath, "/", "%2F")

	// GitLab API base URL
	gitLabAPIBase := strings.TrimSuffix(gitLabURL, "/") + "/api/v4"

	resp, err := common.HttpGet(
		fmt.Sprintf("%s/projects/%s/merge_requests/%s", gitLabAPIBase, encodedProjectPath, mrIID),
		common.HttpWithHeaders(map[string]string{
			"PRIVATE-TOKEN": password,
		}),
	)
	if err != nil {
		ctx.GetLogger().Error("Error getting MR status", "error", err)
		return GetRecommendationResolutionStatusResponse{}, err
	}

	jsonResponseBody := resp.Body
	defer func() {
		err := jsonResponseBody.Close()
		if err != nil {
			ctx.GetLogger().Error("Error closing response body", "error", err)
		}
	}()

	jsonBodyBytes, err := io.ReadAll(jsonResponseBody)
	if err != nil {
		return GetRecommendationResolutionStatusResponse{}, err
	}

	jsonBody := map[string]any{}
	err = common.UnmarshalJson(jsonBodyBytes, &jsonBody)
	if err != nil {
		return GetRecommendationResolutionStatusResponse{}, err
	}

	if resp.StatusCode != 200 {
		ctx.GetLogger().Error("Error getting MR status", "status", resp.StatusCode, "data", string(jsonBodyBytes))
		return GetRecommendationResolutionStatusResponse{
			Status:        RecommendationResolutionStatusFailed,
			StatusMessage: string(jsonBodyBytes),
		}, nil
	}

	mrStatus := RecommendationResolutionStatusInProgress
	mrMessage := "MR is open and awaiting review/merge"

	// GitLab MR states: opened, closed, merged, locked
	state, _ := jsonBody["state"].(string)
	switch state {
	case "merged":
		mrStatus = RecommendationResolutionStatusSuccess
		mrMessage = "MR was successfully merged"
	case "closed":
		mrStatus = RecommendationResolutionStatusFailed
		mrMessage = "MR was closed without merging"
	case "opened":
		mrStatus = RecommendationResolutionStatusInProgress
		mrMessage = "MR is open and awaiting review/merge"
	case "locked":
		mrStatus = RecommendationResolutionStatusInProgress
		mrMessage = "MR is locked (discussion in progress)"
	}

	return GetRecommendationResolutionStatusResponse{
		Status:        mrStatus,
		StatusMessage: mrMessage,
	}, nil
}

// parseGitLabMRURL extracts the project path and MR IID from a GitLab MR URL
// URL format: https://gitlab.com/group/project/-/merge_requests/123
func parseGitLabMRURL(mrURL string, gitLabURL string) (projectPath string, mrIID string, err error) {
	// Remove trailing slash
	mrURL = strings.TrimSuffix(mrURL, "/")

	// Find the merge_requests part
	mrIndex := strings.Index(mrURL, "/-/merge_requests/")
	if mrIndex == -1 {
		// Try alternative format without /-/
		mrIndex = strings.Index(mrURL, "/merge_requests/")
		if mrIndex == -1 {
			return "", "", fmt.Errorf("not a valid GitLab MR URL: %s", mrURL)
		}
		// Extract MR IID (everything after /merge_requests/)
		parts := strings.Split(mrURL[mrIndex:], "/")
		if len(parts) < 3 {
			return "", "", fmt.Errorf("could not extract MR IID from URL: %s", mrURL)
		}
		mrIID = parts[2]

		// Extract project path
		gitLabHost := strings.TrimPrefix(gitLabURL, "https://")
		gitLabHost = strings.TrimPrefix(gitLabHost, "http://")
		gitLabHost = strings.TrimSuffix(gitLabHost, "/")

		projectPart := mrURL[:mrIndex]
		for _, prefix := range []string{"https://", "http://"} {
			projectPart = strings.TrimPrefix(projectPart, prefix+gitLabHost+"/")
		}
		projectPath = projectPart
	} else {
		// Extract MR IID. mrURL[mrIndex:] is "/-/merge_requests/<iid>", which
		// splits to ["", "-", "merge_requests", "<iid>"], so the IID is parts[3].
		parts := strings.Split(mrURL[mrIndex:], "/")
		if len(parts) < 4 {
			return "", "", fmt.Errorf("could not extract MR IID from URL: %s", mrURL)
		}
		mrIID = parts[3]

		// Extract project path (everything between host and /-/merge_requests/)
		gitLabHost := strings.TrimPrefix(gitLabURL, "https://")
		gitLabHost = strings.TrimPrefix(gitLabHost, "http://")
		gitLabHost = strings.TrimSuffix(gitLabHost, "/")

		projectPart := mrURL[:mrIndex]
		for _, prefix := range []string{"https://", "http://"} {
			projectPart = strings.TrimPrefix(projectPart, prefix+gitLabHost+"/")
		}
		projectPath = projectPart
	}

	// Strip any ?query / #anchor suffix from the IID — covers both URL
	// formats above. The final empty-IID guard below catches the case
	// where the IID segment was entirely query/anchor.
	if idx := strings.IndexAny(mrIID, "?#"); idx != -1 {
		mrIID = mrIID[:idx]
	}

	if projectPath == "" || mrIID == "" {
		return "", "", fmt.Errorf("could not parse GitLab MR URL: %s", mrURL)
	}

	return projectPath, mrIID, nil
}

// ApplyRightsizingRecommendationUsingCodeAgentGitLab applies rightsizing recommendations using the code agent for GitLab
func ApplyRightsizingRecommendationUsingCodeAgentGitLab(ctx AccountAdapterContext, request ApplyRecommendationRequest, gitDetail gitLabDetailFromDeployment, recommendResolutionId string) error {
	// Run asynchronously to avoid blocking the request
	go func() {
		// Recover from any panics to prevent crashing the application
		defer func() {
			if r := recover(); r != nil {
				ctx.GetLogger().Error("recommendation_resolution: panic recovered in code agent goroutine", "panic", r, "recommendation_id", recommendResolutionId)

				// Try to update database status to Failed
				dbms, err := database.GetDatabaseManager(database.Metastore)
				if err == nil {
					tableName := "recommendation_resolution"
					if request.IsEventResolution {
						tableName = "event_resolution"
					}
					_, _ = dbms.Db.ExecContext(
						context.Background(),
						fmt.Sprintf(`UPDATE %s SET status = $1, updated_at = $2, status_message = $3 WHERE id = $4`, tableName),
						models.RecommendationResolutionStatusFailed,
						time.Now(),
						fmt.Sprintf("Panic during code agent execution: %v", r),
						recommendResolutionId,
					)
				}
			}
		}()

		// Get database connection for status tracking
		dbms, err := database.GetDatabaseManager(database.Metastore)
		if err != nil {
			ctx.GetLogger().Error("failed recommendation resolution db connection", "error", err)
			return
		}

		tableName := "recommendation_resolution"
		if request.IsEventResolution {
			tableName = "event_resolution"
		}

		// Helper function to update database status
		updateStatus := func(status models.RecommendationResolutionStatus, statusMessage string, mrURL string) {
			query := fmt.Sprintf(`UPDATE %s SET status = $1, updated_at = $2, status_message = $3 WHERE id = $4`, tableName)
			params := []any{status, time.Now(), statusMessage, recommendResolutionId}

			// If MR URL is provided, also update type_reference_id and PR lifecycle columns
			if mrURL != "" {
				query = fmt.Sprintf(`UPDATE %s SET status = $1, updated_at = $2, status_message = $3, type_reference_id = $5, pr_lifecycle_state = $6, pr_iteration_count = $7, last_pr_check_at = $8 WHERE id = $4`, tableName)
				params = append(params, mrURL, "created", 0, time.Now())
			}

			_, err := dbms.Db.ExecContext(context.Background(), query, params...)
			if err != nil {
				ctx.GetLogger().Error("error updating recommendation resolution status", "error", err, "status", status)
			}
		}

		// Build structured prompt with clear instructions
		recommendationJSON, _ := common.MarshalJson(request.Recommendation)

		// Extract namespace from resource meta
		resourceNamespace := ""
		if metaObj, ok := request.Resource.Meta.Object().(map[string]any); ok {
			if ns, ok := metaObj["namespace"].(string); ok {
				resourceNamespace = ns
			}
		}

		queryText := fmt.Sprintf(`Please apply the following Kubernetes resource rightsizing recommendations.

**Repository**: %s (GitLab)
**GitLab URL**: %s
**Branch**: %s
**Helm Values File**: %s
**Resource Namespace**: %s

**Recommendation Context**:
%s

**Instructions**:
1. Find the corresponding Helm deployment template to understand how container resources map to values
2. Identify the correct YAML path in the values file for each container (check template variable references like {{.Values.resources}})
3. Update only the specified CPU/memory values at the correct paths
4. Preserve existing formatting and structure
5. **CRITICAL - CPU Limits**: If the recommendation specifies CPU limit as null, empty, or omitted, you MUST remove the CPU limit line entirely or leave it unset. DO NOT set CPU limit to match the request value. Only set CPU limit if explicitly provided with a non-null value in the recommendation.

**MR Description Requirements**:
When creating the MR, ensure the description includes:
1. **Summary**: Brief explanation of the rightsizing changes being applied
2. **Changes Table**: Before/after comparison of CPU and memory values for each container
3. **Motivation**: Explain the cost optimization and performance benefits

Use this format for the changes table:
| Container | Resource | Before | After | Change |
|-----------|----------|--------|-------|--------|
| <name> | CPU Request | <old> | <new> | <diff> |
| <name> | Memory Limit | <old> | <new> | <diff> |

Make minimal, precise changes only.`,
			gitDetail.ProjectPath,
			gitDetail.GitLabURL,
			gitDetail.BaseBranch,
			gitDetail.FilePath,
			resourceNamespace,
			string(recommendationJSON),
		)

		// Wrap the prompt in a JSON envelope so agent_code_2 receives explicit
		// intent flags (mode + raise_pr) — agent_code_2 does not infer intent
		// from prompt text; the entrypoint declares it.
		codeAgentPayload := map[string]any{
			"query":             queryText,
			"git_repo":          gitDetail.GitLabURL,
			"mode":              "fix",
			"raise_pr":          true,
			"recommendation_id": request.Recommendation.Id,
			"account_id":        request.Recommendation.CloudAccountId,
		}
		codeAgentPayloadJSON, _ := common.MarshalJson(codeAgentPayload)
		prompt := "@agent_code_2 " + string(codeAgentPayloadJSON)

		// Construct the request payload
		chatRequest := llm.ConversationApiRequest{
			Query:     prompt,
			AccountId: request.Recommendation.CloudAccountId,
			UserId:    ctx.GetSecurityContext().GetUserId(),
			Async:     false,
			Source:    "recommendation",
			Config: map[string]any{
				"recommendation_id": request.Recommendation.Id,
				"account_id":        request.Recommendation.CloudAccountId,
			},
		}

		// Create a new context with trace propagation for LLM server call
		span := trace.SpanFromContext(ctx.GetContext())
		llmCtx := trace.ContextWithSpan(context.Background(), span)

		// Call code agent with trace-propagated context
		response, err := llm.ChatCompletion(security.NewRequestContext(llmCtx, ctx.GetSecurityContext(), ctx.GetLogger(), nil, nil), chatRequest)
		if err != nil {
			ctx.GetLogger().Error("recommendation_resolution: failed to get chat completion request", "error", err)
			updateStatus(models.RecommendationResolutionStatusFailed, fmt.Sprintf("Failed to execute code agent: %s", err.Error()), "")
			return
		}

		if response == nil || len(response.Response) == 0 {
			ctx.GetLogger().Warn("recommendation_resolution: chat completion returned empty response")
			updateStatus(models.RecommendationResolutionStatusFailed, "Code agent returned empty response", "")
			return
		}

		// Parse the response to extract MR information
		var agentResponse map[string]any
		err = common.UnmarshalJson([]byte(response.Response[0]), &agentResponse)
		if err != nil {
			ctx.GetLogger().Error("recommendation_resolution: failed to parse agent response", "error", err, "response", response.Response[0])
			updateStatus(models.RecommendationResolutionStatusFailed, "Failed to parse code agent response", "")
			return
		}

		// Extract MR information from response
		var mrURL string
		var mrNumber any

		// Check for automated_fix_pr_info structure (GitLab uses same structure but returns web_url)
		if mrInfo, ok := agentResponse["automated_fix_pr_info"].(map[string]any); ok && mrInfo != nil {
			if url, ok := mrInfo["url"].(string); ok {
				mrURL = url
			}
			if num, ok := mrInfo["number"]; ok {
				mrNumber = num
			}
		}

		// Fallback to fix_pr structure
		if mrURL == "" {
			if mrInfo, ok := agentResponse["fix_pr"].(map[string]any); ok && mrInfo != nil {
				if url, ok := mrInfo["url"].(string); ok {
					mrURL = url
				}
				if num, ok := mrInfo["number"]; ok {
					mrNumber = num
				}
			}
		}

		// Fallback to direct mr_url or pr_url field
		if mrURL == "" {
			if url, ok := agentResponse["mr_url"].(string); ok {
				mrURL = url
			} else if url, ok := agentResponse["pr_url"].(string); ok {
				mrURL = url
			}
		}

		// Check execution status
		executionStatus, _ := agentResponse["execution_status"].(string)

		// Determine success or failure
		if mrURL != "" {
			// Success: MR was created — also store MR metadata in data JSONB for lifecycle tracking
			ctx.GetLogger().Info("recommendation_resolution: MR created successfully", "mr_url", mrURL, "mr_number", mrNumber)
			updateStatus(models.RecommendationResolutionStatusInProgress, "MR raised successfully by code agent", mrURL)

			// Store MR metadata for cron-based lifecycle tracking
			gitlabBaseURL := gitDetail.GitLabURL
			if gitlabBaseURL == "" {
				gitlabBaseURL = "https://gitlab.com"
			}
			repoURL := fmt.Sprintf("%s/%s", gitlabBaseURL, gitDetail.ProjectPath)
			mrMeta := map[string]any{
				"pr_url":       mrURL,
				"pr_number":    mrNumber,
				"repo_url":     repoURL,
				"branch":       gitDetail.BaseBranch,
				"provider":     "gitlab",
				"project_path": gitDetail.ProjectPath,
			}
			if branchName, ok := agentResponse["branch"].(string); ok {
				mrMeta["pr_branch"] = branchName
			}
			mrMetaJSON, marshalErr := common.MarshalJson(mrMeta)
			if marshalErr == nil {
				_, _ = dbms.Db.ExecContext(context.Background(),
					fmt.Sprintf(`UPDATE %s SET data = $1 WHERE id = $2`, tableName),
					string(mrMetaJSON), recommendResolutionId)
			}
		} else if executionStatus == "success" {
			// Agent succeeded but no MR was created
			ctx.GetLogger().Warn("recommendation_resolution: code agent succeeded but no MR URL found")
			updateStatus(models.RecommendationResolutionStatusSuccess, "Code agent applied changes but no MR was created", "")
		} else {
			// Failure: No MR and no success status
			errorMsg := "Code agent execution failed"
			if summary, ok := agentResponse["execution_summary"].(string); ok && summary != "" {
				errorMsg = summary
			} else if msg, ok := agentResponse["error"].(string); ok && msg != "" {
				errorMsg = msg
			} else if executionStatus != "" {
				errorMsg = fmt.Sprintf("Code agent execution status: %s", executionStatus)
			}

			ctx.GetLogger().Error("recommendation_resolution: code agent failed", "error", errorMsg, "response", agentResponse)
			updateStatus(models.RecommendationResolutionStatusFailed, errorMsg, "")
		}
	}()

	return nil
}
