package adapter

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/url"
	"nudgebee/services/common"
	"nudgebee/services/config"
	"nudgebee/services/internal/database"
	"nudgebee/services/internal/database/models"
	"nudgebee/services/llm"
	"nudgebee/services/relay"
	"nudgebee/services/security"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"go.opentelemetry.io/otel/trace"
	"gopkg.in/yaml.v3"
)

// annotations to understand the helm configs for the deployment
// ci.nudgebee.com/git.repo
// ci.nudgebee.com/git.hash
// ci.nudgebee.com/git.branch
// ci.nudgebee.com/helm.values.filePath
// ci.nudgebee.com/helm.values.rootPath
// ci.nudgebee.com/helm.values.memoryRequestJsonPath
// ci.nudgebee.com/helm.values.memoryLimitJsonPath
// ci.nudgebee.com/helm.values.cpuRequestJsonPath
// ci.nudgebee.com/helm.values.cpuLimitJsonPath
// ci.nudgebee.com/helm.values.replicaJsonPath
// ci.nudgebee.com/helm.values.pvcValueJsonPath

// validGitRefChars matches the set of allowed characters in a git ref.
var validGitRefChars = regexp.MustCompile(`^[a-zA-Z0-9._/\-]+$`)

// validSha1 matches a hex SHA-1 hash (short or full).
var validSha1 = regexp.MustCompile(`^[0-9a-fA-F]{4,40}$`)

// isValidGitRef checks if a string is a valid git reference
// based on the rules from git check-ref-format.
func isValidGitRef(ref string) bool {
	if !validGitRefChars.MatchString(ref) ||
		strings.Contains(ref, "..") ||
		strings.Contains(ref, "//") ||
		strings.HasPrefix(ref, "/") ||
		strings.HasSuffix(ref, "/") ||
		strings.HasSuffix(ref, ".lock") ||
		strings.Contains(ref, "@{") {
		return false
	}
	for _, component := range strings.Split(ref, "/") {
		if strings.HasPrefix(component, ".") {
			return false
		}
	}
	return true
}

func validateGitDetails(gitDetails gitDetailFromDeployment) error {
	if gitDetails.BaseBranch != "" && !isValidGitRef(gitDetails.BaseBranch) {
		return fmt.Errorf("invalid git branch name: %q", gitDetails.BaseBranch)
	}
	if gitDetails.Org != "" && !isValidGitRef(gitDetails.Org) {
		return fmt.Errorf("invalid git org name: %q", gitDetails.Org)
	}
	if gitDetails.Repo != "" && !isValidGitRef(gitDetails.Repo) {
		return fmt.Errorf("invalid git repo name: %q", gitDetails.Repo)
	}
	if gitDetails.Sha1 != "" && !validSha1.MatchString(gitDetails.Sha1) {
		return fmt.Errorf("invalid git SHA: %q", gitDetails.Sha1)
	}
	return nil
}

func checkoutCodeRepo(ctx AccountAdapterContext, request ApplyRecommendationRequest, gitDetails gitDetailFromDeployment) (string, error) {
	if err := validateGitDetails(gitDetails); err != nil {
		return "", err
	}

	dir, err := os.MkdirTemp("", "checkout")
	if err != nil {
		ctx.GetLogger().Error("Error creating temp dir", "error", err)
		return "", err
	}
	var gitUrl string
	if gitDetails.Token == "" {
		gitUrl = fmt.Sprintf("https://github.com/%s/%s.git", gitDetails.Org, gitDetails.Repo)
	} else {
		gitUrl = fmt.Sprintf("https://oauth2:%s@github.com/%s/%s.git", gitDetails.Token, gitDetails.Org, gitDetails.Repo)
	}
	cmd := exec.Command("git", "clone", "--depth", "1", "-b", gitDetails.BaseBranch, gitUrl, dir)
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

func updateYamlPath(data *yaml.Node, path string, value any) (*yaml.Node, error) {
	paths := strings.Split(path, ".")
	if len(paths) == 0 || path == "" {
		return nil, fmt.Errorf("invalid path")
	}

	// Start with the root content node if it's a document node
	if data.Kind == yaml.DocumentNode {
		data = data.Content[0]
	}
	var err error
	// Recursively update or create the nodes
	if value != nil {
		err = setYamlPathValue(data, paths, value)
	} else {
		err = removeYamlPath(data, paths)
	}
	if err != nil {
		return nil, err
	}

	return data, nil
}

func setYamlPathValue(node *yaml.Node, paths []string, value any) error {
	if len(paths) == 0 {
		return nil
	}

	// Traverse the current level to find the matching key
	for i := 0; i < len(node.Content); i += 2 {
		if node.Content[i].Value == paths[0] {
			// If this is the last key in the path, set the value
			if len(paths) == 1 {
				node.Content[i+1].Value = fmt.Sprintf("%v", value)
				return nil
			}
			// Recursively update the child nodes
			return setYamlPathValue(node.Content[i+1], paths[1:], value)
		}
	}

	// If the key does not exist, create it
	if len(paths) == 1 {
		// Create the key-value pair
		node.Content = append(node.Content, &yaml.Node{Kind: yaml.ScalarNode, Value: paths[0]})
		node.Content = append(node.Content, &yaml.Node{Kind: yaml.ScalarNode, Value: fmt.Sprintf("%v", value)})
	} else {
		// Create the key and an empty mapping node for nested structures
		newMapping := &yaml.Node{Kind: yaml.MappingNode}
		node.Content = append(node.Content, &yaml.Node{Kind: yaml.ScalarNode, Value: paths[0]})
		node.Content = append(node.Content, newMapping)
		return setYamlPathValue(newMapping, paths[1:], value)
	}

	return nil
}

func removeYamlPath(node *yaml.Node, paths []string) error {
	if len(paths) == 0 {
		return nil
	}

	// Traverse the current level to find the matching key
	for i := 0; i < len(node.Content); i += 2 {
		if node.Content[i].Value == paths[0] {
			// If this is the last key in the path, set the value
			if len(paths) == 1 {
				node.Content = append(node.Content[:i], node.Content[i+2:]...)
				return nil
			}
			// Recursively update the child nodes
			return removeYamlPath(node.Content[i+1], paths[1:])
		}
	}
	return nil
}

func updateYamlCode(data []byte, pathsToUpdate map[string]any) ([]byte, []string, error) {
	var yamlFile yaml.Node
	err := yaml.Unmarshal([]byte(data), &yamlFile)
	if err != nil {
		return nil, nil, err
	}
	updatedPaths := []string{}
	for path, value := range pathsToUpdate {
		// update value
		yamlFile2, err := updateYamlPath(&yamlFile, path, value)
		if err != nil {
			return nil, nil, err
		}
		updatedPaths = append(updatedPaths, path)
		yamlFile = *yamlFile2
	}

	if yamlFile.Kind == yaml.DocumentNode {
		yamlFile = *yamlFile.Content[0]
	}

	updatedData, err := common.MarshalYaml(yamlFile)
	if err != nil {
		return nil, nil, err
	}

	return updatedData, updatedPaths, nil
}

func updateCode(ctx AccountAdapterContext, dir string, request ApplyRecommendationRequest, gitDetails gitDetailFromDeployment) error {
	if len(request.Data) == 0 {
		return fmt.Errorf("no data found in request")
	}
	filepath := filepath.Join(dir, gitDetails.FilePath)
	if _, err := os.Stat(filepath); os.IsNotExist(err) {
		ctx.GetLogger().Error("File does not exist", "file", filepath)
		return err
	}
	// read file
	data, err := os.ReadFile(filepath)
	if err != nil {
		ctx.GetLogger().Error("Error reading file", "error", err)
		return err
	}
	// update values
	yamlPathsToUpdate := map[string]any{}
	rootPath := ""
	if gitDetails.Annotations["ci.nudgebee.com/helm.values.rootPath"] != "" {
		rootPath = gitDetails.Annotations["ci.nudgebee.com/helm.values.rootPath"]
	}
	switch {
	case request.Recommendation.Category == "RightSizing" && request.Recommendation.RuleName == "pod_right_sizing":
		for key, value := range request.Data {
			resourceData, ok := value.(map[string]any)
			if !ok {
				continue
			}
			resources := map[string]string{
				"cpuRequest":    "helm.values.cpuRequestJsonPath",
				"cpuLimit":      "helm.values.cpuLimitJsonPath",
				"memoryRequest": "helm.values.memoryRequestJsonPath",
				"memoryLimit":   "helm.values.memoryLimitJsonPath",
			}
			for resourceType, annotationSuffix := range resources {
				var resourceCategory, resourceField string
				switch resourceType {
				case "cpuRequest":
					resourceCategory, resourceField = "cpu", "request"
				case "cpuLimit":
					resourceCategory, resourceField = "cpu", "limit"
				case "memoryRequest":
					resourceCategory, resourceField = "memory", "request"
				case "memoryLimit":
					resourceCategory, resourceField = "memory", "limit"
				}

				// Check if the resource category exists in the data
				resourceDetailsInterface, exists := resourceData[resourceCategory]
				if !exists {
					continue // Skip if this resource category doesn't exist
				}

				// Type assert to map[string]any
				resourceDetails, ok := resourceDetailsInterface.(map[string]any)
				if !ok {
					continue // Skip if it's not the expected type
				}
				path := fmt.Sprintf("resources.%s.%s",
					map[string]string{"request": "requests", "limit": "limits"}[resourceField],
					resourceCategory)
				annotationPath := ""
				if annotation, exists := gitDetails.Annotations["ci.nudgebee.com/"+annotationSuffix]; exists && annotation != "" {
					annotationPath = annotation
				}
				if annotation, exists := gitDetails.Annotations["ci.nudgebee.com/"+key+"."+annotationSuffix]; exists && annotation != "" {
					annotationPath = annotation
				}
				if annotationPath != "" {
					path = annotationPath
				}
				if rootPath != "" {
					path = rootPath + "." + path
				}
				if _, exists := resourceDetails[resourceField]; !exists {
					continue // Skip if the resource field is not present
				}
				yamlPathsToUpdate[path] = resourceDetails[resourceField]
			}
		}

		if len(yamlPathsToUpdate) == 0 {
			ctx.GetLogger().Error("No paths to update found")
			return fmt.Errorf("no paths to update found")
		}

		updatedData, updatedPaths, err := updateYamlCode(data, yamlPathsToUpdate)
		if err != nil {
			ctx.GetLogger().Error("Error updating yaml file", "error", err)
			return err
		}

		if len(updatedPaths) == 0 {
			ctx.GetLogger().Error("No paths updated")
			return fmt.Errorf("no paths updated")
		}

		// write file
		err = os.WriteFile(filepath, updatedData, 0644)
		if err != nil {
			ctx.GetLogger().Error("Error writing file", "error", err)
			return err
		}
	case request.Recommendation.Category == "RightSizing" && request.Recommendation.RuleName == "replica_right_sizing":
		if request.Data["replica_count"] != nil {

			replicaData := request.Data["replica_count"]
			path := "replicaCount"
			if gitDetails.Annotations["ci.nudgebee.com/helm.values.replicaJsonPath"] != "" {
				path = gitDetails.Annotations["ci.nudgebee.com/helm.values.replicaJsonPath"]
			}
			if rootPath != "" {
				path = rootPath + "." + path
			}
			yamlPathsToUpdate[path] = fmt.Sprintf("%v", replicaData)

		}
		if len(yamlPathsToUpdate) == 0 {
			ctx.GetLogger().Error("No paths to update found")
			return fmt.Errorf("no paths to update found")
		}

		updatedData, updatedPaths, err := updateYamlCode(data, yamlPathsToUpdate)
		if err != nil {
			ctx.GetLogger().Error("Error updating yaml file", "error", err)
			return err
		}

		if len(updatedPaths) == 0 {
			ctx.GetLogger().Error("No paths updated")
			return fmt.Errorf("no paths updated")
		}

		// write file
		err = os.WriteFile(filepath, updatedData, 0644)
		if err != nil {
			ctx.GetLogger().Error("Error writing file", "error", err)
			return err
		}
	default:
		ctx.GetLogger().Error("unsupported recommendation category", "category", request.Recommendation.Category, "rule", request.Recommendation.RuleName)
		return fmt.Errorf("unsupported recommendation category: %s, rule: %s", request.Recommendation.Category, request.Recommendation.RuleName)
	}
	return nil
}

func commitCode(ctx AccountAdapterContext, dir string, request ApplyRecommendationRequest, gitDetails gitDetailFromDeployment, updateExistingPR bool) (string, error) {
	branchName := "nb/" + request.Recommendation.Id
	if updateExistingPR {
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

	err := updateCode(ctx, dir, request, gitDetails)
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

func commitCodeForEvent(ctx AccountAdapterContext, dir string, request ApplyRecommendationRequest, gitDetails gitDetailFromDeployment, updateExistingPR bool) (string, error) {
	branchName := "nb/" + request.Recommendation.Id
	if updateExistingPR {
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

	err := readUpdateCodeFile(filepath.Join(dir, fileName), lineNumber, newLine, oldLine)
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

func raisePrForCodeRepo(ctx AccountAdapterContext, dir string, branchName string, gitDetail gitDetailFromDeployment, updateExistingPR bool, existingPRNumber int, githubBody string, resolverType string, githubTitle string) (string, error) {
	// push branch
	cmd := exec.Command("git", "push", "origin", branchName, "-f")
	cmd.Dir = dir
	output, err := cmd.Output()
	if err != nil {
		ctx.GetLogger().Error("Error pushing branch", "error", err, "output", string(output))
		return "", err
	}

	// raise PR
	if updateExistingPR {
		resp, err := common.HttpPatch(fmt.Sprintf("https://api.github.com/repos/%s/%s/pulls/%d", gitDetail.Org, gitDetail.Repo, existingPRNumber), common.HttpWithHeaders(map[string]string{
			"Authorization": fmt.Sprintf("Bearer %s", gitDetail.Token),
		}), common.HttpWithJsonBody(map[string]any{
			"body": "Automated PR for recommendation update. Please review and merge.\n" + githubBody,
		}))

		if err != nil {
			ctx.GetLogger().Error("Error getting exists PR details", "error", err)
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
			ctx.GetLogger().Error("Error reading existing PR response body", "error", err)
			return "", err
		}

		if resp.StatusCode != 200 {
			ctx.GetLogger().Error("Error getting exists PR details", "status", resp.StatusCode, "data", string(data))
			return "", fmt.Errorf("error getting exists PR details: %s", string(data))
		}

		return string(data), err
	}
	if githubTitle == "" {
		githubTitle = fmt.Sprintf("ci: Updated %s %s", resolverType, branchName)
	}
	resp, err := common.HttpPost(fmt.Sprintf("https://api.github.com/repos/%s/%s/pulls", gitDetail.Org, gitDetail.Repo), common.HttpWithHeaders(map[string]string{
		"Authorization": fmt.Sprintf("Bearer %s", gitDetail.Token),
	}), common.HttpWithJsonBody(map[string]any{
		"title": githubTitle,
		"head":  branchName,
		"base":  gitDetail.BaseBranch,
		"body":  fmt.Sprintf("Automated PR for %s update. Please review and merge.\n", resolverType) + githubBody,
	}))

	if err != nil {
		ctx.GetLogger().Error("Error raising PR", "error", err)
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
		ctx.GetLogger().Error("Error raising PR", "status", resp.StatusCode, "data", string(data))
		return "", fmt.Errorf("error raising PR: %s", string(data))
	}

	return string(data), err
}

type gitDetailFromDeployment struct {
	Org         string
	Repo        string
	BaseBranch  string
	Token       string
	Username    string
	FilePath    string
	Annotations map[string]string
	Sha1        string
}

func getGitCredentials(ctx AccountAdapterContext, ticketProvider string) (string, string, string, string, error) {
	ctx.GetLogger().Info("DEBUG: getGitCredentials (GitHub) called",
		"ticket_provider", ticketProvider,
		"tenant_id", ctx.GetSecurityContext().GetTenantId())
	dbms, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		return "", "", "", "", err
	}

	// First, get the integration ID
	var integrationID string
	err = dbms.Db.QueryRowx(`
		SELECT i.id::text
		FROM integrations i
		WHERE i.tenant_id = $1 AND i.name = $2 AND i.status = 'enabled' AND i.type = 'github'
		LIMIT 1
	`, ctx.GetSecurityContext().GetTenantId(), ticketProvider).Scan(&integrationID)
	if err != nil {
		return "", "", "", "", common.ErrorNotFound("error: github integration not found")
	}

	// Fetch integration config values with encryption flag
	configQuery := `
		SELECT name::text, value::text, is_encrypted
		FROM integration_config_values
		WHERE integration_id = $1
	`
	rows, err := dbms.Db.Queryx(configQuery, integrationID)
	if err != nil {
		return "", "", "", "", err
	}
	defer func() {
		err := rows.Close()
		if err != nil {
			slog.Error("Error closing rows", "error", err)
		}
	}()

	configs := make(map[string]string)
	for rows.Next() {
		var configName, value string
		var isEncrypted bool
		if err := rows.Scan(&configName, &value, &isEncrypted); err != nil {
			return "", "", "", "", err
		}

		// Decrypt if encrypted
		if isEncrypted && value != "" {
			decrypted, err := common.Decrypt(value)
			if err != nil {
				ctx.GetLogger().Error("error decrypting config value", "name", configName, "error", err)
				return "", "", "", "", common.ErrorInternal("error: unable to process request")
			}
			configs[configName] = decrypted
		} else {
			configs[configName] = value
		}
	}

	// Check for errors from iterating over rows
	if err := rows.Err(); err != nil {
		return "", "", "", "", err
	}

	// Extract required config values with defaults
	username := configs["username"]
	gitUrl := configs["url"]
	if gitUrl == "" {
		gitUrl = "https://api.github.com"
	}
	password := configs["password"]
	authType := configs["auth_type"]
	if authType == "" {
		authType = "token"
	}

	if password == "" {
		return "", "", "", "", common.ErrorNotFound("error: github integration not found")
	}

	return gitUrl, authType, username, password, nil
}

// argoCDSource represents an ArgoCD source in multi-source application
type argoCDSource struct {
	RepoURL        string      `json:"repoURL"`
	Path           string      `json:"path"`
	TargetRevision string      `json:"targetRevision"`
	Chart          string      `json:"chart"`
	Ref            string      `json:"ref,omitempty"`
	Helm           *argoCDHelm `json:"helm,omitempty"`
}

type argoCDHelm struct {
	ValueFiles  []string `json:"valueFiles,omitempty"`
	ReleaseName string   `json:"releaseName,omitempty"`
}

type argoCDSpec struct {
	Source  argoCDSource   `json:"source"`
	Sources []argoCDSource `json:"sources"`
}

type argoCDMetadata struct {
	Name string `json:"name"`
}

type argoCDApplication struct {
	Metadata argoCDMetadata `json:"metadata"`
	Spec     argoCDSpec     `json:"spec"`
}

// getGitRepoFromArgoCD queries ArgoCD to get the Git repository URL for values files
func getGitRepoFromArgoCD(ctx AccountAdapterContext, accountID, argoCDAppName string) (string, string, string, error) {
	// Fetch ArgoCD integration configuration
	secretName, serverURL, authTokenKeyInSecret, insecure, err := fetchArgoCDIntegrationForGithub(ctx, accountID)
	if err != nil {
		ctx.GetLogger().Info("No ArgoCD integration found", "account", accountID, "error", err)
		return "", "", "", err
	}

	// Build ArgoCD CLI command
	serverHost := strings.TrimPrefix(serverURL, "https://")
	serverHost = strings.TrimPrefix(serverHost, "http://")

	insecureFlag := ""
	if insecure {
		insecureFlag = " --insecure"
	}

	argoCDCmd := fmt.Sprintf(
		`argocd app get %s --server %s%s --grpc-web --output json`,
		argoCDAppName,
		serverHost,
		insecureFlag,
	)

	// Execute via relay server
	envFromSecret := map[string]string{
		"ARGOCD_AUTH_TOKEN": authTokenKeyInSecret,
	}

	resp, err := relay.CommandExecutor(accountID, argoCDCmd, secretName, envFromSecret)
	if err != nil {
		return "", "", "", fmt.Errorf("failed to execute argocd command: %w", err)
	}

	// Extract response
	respStr, ok := resp["response"].(string)
	if !ok {
		return "", "", "", fmt.Errorf("unexpected response format from argocd: %v", resp)
	}

	// Parse ArgoCD application
	var app argoCDApplication
	if err := json.Unmarshal([]byte(respStr), &app); err != nil {
		// Fallback: ArgoCD CLI sometimes outputs Python-style dict (single quotes) instead of JSON
		// Try converting Python dict syntax to JSON
		jsonStr, convErr := common.ConvertPythonDictToJSON(respStr)
		if convErr != nil {
			ctx.GetLogger().Error("Failed to convert Python dict to JSON", "error", convErr)
			return "", "", "", fmt.Errorf("failed to parse ArgoCD response: %w", err)
		}
		if err2 := json.Unmarshal([]byte(jsonStr), &app); err2 != nil {
			ctx.GetLogger().Error("Failed to parse ArgoCD response even after Python dict conversion",
				"original_error", err, "conversion_error", err2)
			return "", "", "", fmt.Errorf("failed to parse ArgoCD response: %w", err)
		}
		ctx.GetLogger().Info("Successfully parsed ArgoCD response after Python dict conversion")
	}

	// Extract values repo URL from multi-source configuration
	var valuesRepoURL, valuesPath, targetRevision string

	// Handle multi-source applications (sources array)
	if len(app.Spec.Sources) > 0 {
		for _, source := range app.Spec.Sources {
			// Source with ref is typically the values repository
			if source.Ref != "" {
				valuesRepoURL = source.RepoURL
				valuesPath = source.Path
				targetRevision = source.TargetRevision

				// Extract values file path from Helm configuration
				// Find the Helm source to get valueFiles
				for _, helmSource := range app.Spec.Sources {
					if helmSource.Chart != "" && helmSource.Helm != nil && len(helmSource.Helm.ValueFiles) > 0 {
						// Extract first values file and clean up $values/ prefix
						vf := helmSource.Helm.ValueFiles[0]
						vf = strings.TrimPrefix(vf, "$values/")
						if valuesPath != "" {
							valuesPath = valuesPath + "/" + vf
						} else {
							valuesPath = vf
						}
						break
					}
				}
				break
			}
		}
	} else if app.Spec.Source.RepoURL != "" {
		// Single source application
		valuesRepoURL = app.Spec.Source.RepoURL
		valuesPath = app.Spec.Source.Path
		targetRevision = app.Spec.Source.TargetRevision
	}

	if valuesRepoURL == "" {
		return "", "", "", fmt.Errorf("no values repository found in ArgoCD application %s", argoCDAppName)
	}

	ctx.GetLogger().Info("Successfully detected repo from ArgoCD",
		"app", argoCDAppName,
		"repo", valuesRepoURL,
		"path", valuesPath,
		"revision", targetRevision)

	return valuesRepoURL, valuesPath, targetRevision, nil
}

// fetchArgoCDIntegrationForGithub fetches ArgoCD integration config (simplified version for github.go)
func fetchArgoCDIntegrationForGithub(ctx AccountAdapterContext, accountID string) (secretName, serverURL, authTokenKeyInSecret string, insecure bool, err error) {
	dbms, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		return "", "", "", false, fmt.Errorf("failed to get database manager: %w", err)
	}

	// Query for ArgoCD integration
	query := `
		SELECT i.id::text
		FROM integrations i
		JOIN integrations_cloud_accounts ica ON i.id = ica.integration_id
		WHERE i.type = 'argocd'
		  AND ica.cloud_account_id = $1
		LIMIT 1
	`

	var integrationID string
	err = dbms.Db.QueryRowx(query, accountID).Scan(&integrationID)
	if err != nil {
		return "", "", "", false, fmt.Errorf("no argocd integration found for account: %w", err)
	}

	// Fetch integration config values
	configQuery := `
		SELECT name::text, value::text, is_encrypted
		FROM integration_config_values
		WHERE integration_id = $1
	`
	rows, err := dbms.Db.Queryx(configQuery, integrationID)
	if err != nil {
		return "", "", "", false, fmt.Errorf("failed to fetch integration config values: %w", err)
	}
	defer func() {
		if closeErr := rows.Close(); closeErr != nil {
			ctx.GetLogger().Error("Failed to close rows", "error", closeErr)
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

	// Check for errors from iterating over rows
	if err := rows.Err(); err != nil {
		return "", "", "", false, fmt.Errorf("error iterating config values: %w", err)
	}

	// Extract required config values
	secretName = configs["k8s_secret"]
	serverURL = configs["server"]
	authTokenKeyInSecret = configs["auth_token_key_in_secret"]
	insecureStr := configs["insecure"]
	insecure = insecureStr == "true" || insecureStr == "1"

	// Set defaults
	if authTokenKeyInSecret == "" {
		authTokenKeyInSecret = "ARGOCD_AUTH_TOKEN"
	}

	if secretName == "" {
		return "", "", "", false, errors.New("k8s_secret not found in argocd integration config")
	}

	if serverURL == "" {
		return "", "", "", false, errors.New("server URL not found in argocd integration config")
	}

	return secretName, serverURL, authTokenKeyInSecret, insecure, nil
}

func getGitDetailsFromRecommendation(ctx AccountAdapterContext, request ApplyRecommendationRequest) (gitDetailFromDeployment, error) {

	dbms, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		return gitDetailFromDeployment{}, err
	}
	if len(request.ProviderConfig) == 0 || request.ProviderConfig["name"] == nil || request.ProviderConfig["name"].(string) == "" {
		return gitDetailFromDeployment{}, common.ErrorNotFound("error: provider config not found")
	}
	_, _, username, password, err := getGitCredentials(ctx, request.ProviderConfig["name"].(string))
	if err != nil {
		return gitDetailFromDeployment{}, err
	}

	metaData, ok := request.Resource.Meta.Object().(map[string]any)
	if !ok {
		ctx.GetLogger().Error("error getting resource meta")
		return gitDetailFromDeployment{}, common.ErrorNotFound("error: resource meta not found")
	}
	controllerKind, ok := metaData["controllerKind"].(string)
	if !ok {
		ctx.GetLogger().Error("error getting controller kind")
		return gitDetailFromDeployment{}, common.ErrorNotFound("error: controller kind not found")

	}
	conrtollerName, ok := metaData["controller"].(string)
	if !ok {
		conrtollerName = *request.Resource.Name
	}
	controllerNamespace, ok := metaData["namespace"].(string)
	if !ok {
		ctx.GetLogger().Error("error getting controller namespace")
		return gitDetailFromDeployment{}, common.ErrorNotFound("error: controller namespace not found")
	}
	// get deployment
	rows, err := dbms.Db.Queryx("SELECT meta::varchar FROM k8s_workloads WHERE tenant_id = $1 and cloud_account_id = $2 and kind = $3 and namespace = $4 and name = $5 and is_active = true ", ctx.GetSecurityContext().GetTenantId(), request.Recommendation.CloudAccountId, controllerKind, controllerNamespace, conrtollerName)
	if err != nil {
		return gitDetailFromDeployment{}, err
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
			return gitDetailFromDeployment{}, err
		}
	}

	workloadMetadata := make(map[string]any)
	err = common.UnmarshalJson([]byte(meta), &workloadMetadata)
	if err != nil {
		ctx.GetLogger().Error("error unmarshalling workload metadata")
		return gitDetailFromDeployment{}, err
	}
	workloadMetadataConfig, ok := workloadMetadata["config"]
	if !ok {
		ctx.GetLogger().Error("error getting workload metadata config")
		return gitDetailFromDeployment{}, common.ErrorNotFound("error: workload metadata config not found")
	}
	workloadAnnotations := workloadMetadataConfig.(map[string]any)["annotations"]
	if !ok {
		ctx.GetLogger().Error("error getting workload metadata annotations")
		return gitDetailFromDeployment{}, common.ErrorNotFound("error: workload metadata annotations not found")
	}
	workloadAnnotationsMap := map[string]string{}
	for key, value := range workloadAnnotations.(map[string]any) {
		workloadAnnotationsMap[key] = value.(string)
	}

	// Strategy 1: Try Nudgebee annotations first
	githubRepo := workloadAnnotationsMap["ci.nudgebee.com/git.repo"]
	orgBranch := workloadAnnotationsMap["ci.nudgebee.com/git.branch"]
	filepath := workloadAnnotationsMap["ci.nudgebee.com/helm.values.filePath"]

	// Strategy 2: If Nudgebee annotation not found, check for ArgoCD
	if githubRepo == "" {
		argoCDTrackingID := workloadAnnotationsMap["argocd.argoproj.io/tracking-id"]
		if argoCDTrackingID != "" {
			ctx.GetLogger().Info("Nudgebee annotation not found, trying ArgoCD detection", "tracking_id", argoCDTrackingID)

			// Extract ArgoCD app name from tracking ID (format: "app-name:group/Kind:namespace/resource-name")
			parts := strings.Split(argoCDTrackingID, ":")
			if len(parts) > 0 {
				argoCDAppName := parts[0]

				// Get ArgoCD configuration and query for repo details
				valuesRepo, valuesPath, targetRevision, err := getGitRepoFromArgoCD(ctx, request.Recommendation.CloudAccountId, argoCDAppName)
				if err != nil {
					ctx.GetLogger().Error("Failed to get repo from ArgoCD", "error", err, "app", argoCDAppName)
				} else if valuesRepo != "" {
					githubRepo = valuesRepo
					if targetRevision != "" {
						orgBranch = targetRevision
					}
					if valuesPath != "" && filepath == "" {
						// Use first values file from ArgoCD if available
						filepath = valuesPath
					}
					ctx.GetLogger().Info("Detected GitHub repo from ArgoCD", "repo", githubRepo, "app", argoCDAppName, "branch", orgBranch)
				}
			}
		}
	}

	// Strategy 3: Check cloud_resource_attributes for manually configured git details
	// Supports both ci.nudgebee.com/* (for PR creation) and workloads.nudgebee.com/* (for workload tracking)
	if githubRepo == "" {
		ctx.GetLogger().Info("No annotations found, checking cloud_resource_attributes for manual mapping")

		// Get cloud_resource_id from k8s_workloads
		var workloadResourceId string
		err := dbms.Db.Get(&workloadResourceId,
			`SELECT cloud_resource_id::varchar FROM k8s_workloads
			 WHERE tenant_id = $1 AND cloud_account_id = $2 AND kind = $3
			 AND namespace = $4 AND name = $5 AND is_active = true`,
			ctx.GetSecurityContext().GetTenantId(), request.Recommendation.CloudAccountId,
			controllerKind, controllerNamespace, conrtollerName)

		if err == nil && workloadResourceId != "" {
			// Fetch attributes from cloud_resource_attributes (both ci.nudgebee.com and workloads.nudgebee.com)
			var attributes []struct {
				Name  string `db:"name"`
				Value string `db:"value"`
			}
			err := dbms.Db.Select(&attributes,
				`SELECT name, value FROM cloud_resource_attributes
				 WHERE resource_id = $1 AND (name LIKE 'ci.nudgebee.com/%' OR name LIKE 'workloads.nudgebee.com/%')`,
				workloadResourceId)

			if err == nil {
				for _, attr := range attributes {
					switch attr.Name {
					// CI annotations (preferred for PR creation)
					case "ci.nudgebee.com/git.repo":
						githubRepo = attr.Value
					case "ci.nudgebee.com/git.branch":
						orgBranch = attr.Value
					case "ci.nudgebee.com/helm.values.filePath":
						filepath = attr.Value
					// Workload annotations (fallback)
					case "workloads.nudgebee.com/git.repo":
						if githubRepo == "" { // Only use if ci.nudgebee.com not set
							githubRepo = attr.Value
						}
					}
				}
				if githubRepo != "" {
					ctx.GetLogger().Info("Found git details from cloud_resource_attributes", "repo", githubRepo)
				}
			}
		}
	}

	// If still no repo found, return error
	if githubRepo == "" {
		return gitDetailFromDeployment{}, common.ErrorNotFound("error: github repo not found in workload annotations, ArgoCD configuration, or manual mapping")
	}

	// split the url to get org and repo
	orgRepo := strings.Split(githubRepo, "/")
	if len(orgRepo) != 5 {
		return gitDetailFromDeployment{}, common.ErrorNotFound("error: invalid github repo url - " + githubRepo)
	}

	// Set defaults
	if orgBranch == "" {
		orgBranch = "main"
	}
	if filepath == "" {
		filepath = "values.yaml"
	}

	recommendationMap, _ := request.Recommendation.Recommendation.Object().(map[string]any)
	sha1, _ := recommendationMap["sha1"].(string)

	return gitDetailFromDeployment{
		Token:       password,
		Username:    username,
		Org:         orgRepo[3],
		Repo:        orgRepo[4],
		BaseBranch:  orgBranch,
		Annotations: workloadAnnotationsMap,
		FilePath:    filepath,
		Sha1:        sha1,
	}, nil
}

// getGitDetailsFromSecurityRecommendation extracts git details for security recommendations
// Security recommendations have resource_id = NULL, so we look up the workload directly
// using workload_name and namespace from the recommendation JSON
func getGitDetailsFromSecurityRecommendation(ctx AccountAdapterContext, request ApplyRecommendationRequest) (gitDetailFromDeployment, error) {
	dbms, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		return gitDetailFromDeployment{}, err
	}

	if len(request.ProviderConfig) == 0 || request.ProviderConfig["name"] == nil || request.ProviderConfig["name"].(string) == "" {
		return gitDetailFromDeployment{}, common.ErrorNotFound("error: provider config not found")
	}

	_, _, username, password, err := getGitCredentials(ctx, request.ProviderConfig["name"].(string))
	if err != nil {
		return gitDetailFromDeployment{}, err
	}

	// Extract workload_name and namespace from request data (passed from UI)
	workloadName, ok := request.Data["workload_name"].(string)
	if !ok || workloadName == "" {
		ctx.GetLogger().Error("error: workload_name not found or invalid in request data")
		return gitDetailFromDeployment{}, common.ErrorBadRequest("error: workload_name not found or invalid in request data")
	}

	namespace, ok := request.Data["namespace"].(string)
	if !ok || namespace == "" {
		ctx.GetLogger().Error("error: namespace not found or invalid in request data")
		return gitDetailFromDeployment{}, common.ErrorBadRequest("error: namespace not found or invalid in request data")
	}

	// Query for workloads matching the name and namespace (any kind - Deployment, StatefulSet, DaemonSet, etc.)
	rows, err := dbms.Db.Queryx(
		`SELECT meta::varchar, kind, cloud_resource_id::varchar FROM k8s_workloads
		 WHERE tenant_id = $1 AND cloud_account_id = $2 AND namespace = $3 AND name = $4 AND is_active = true
		 LIMIT 1`,
		ctx.GetSecurityContext().GetTenantId(), request.Recommendation.CloudAccountId, namespace, workloadName)
	if err != nil {
		return gitDetailFromDeployment{}, err
	}
	defer func() {
		err := rows.Close()
		if err != nil {
			slog.Error("Error closing rows", "error", err)
		}
	}()

	var meta string
	var workloadKind string
	var workloadResourceId string
	found := false
	for rows.Next() {
		err := rows.Scan(&meta, &workloadKind, &workloadResourceId)
		if err != nil {
			return gitDetailFromDeployment{}, err
		}
		found = true
	}

	if !found {
		ctx.GetLogger().Error("workload not found", "workload_name", workloadName, "namespace", namespace)
		return gitDetailFromDeployment{}, common.ErrorNotFound("error: workload not found for security recommendation")
	}

	workloadMetadata := make(map[string]any)
	err = common.UnmarshalJson([]byte(meta), &workloadMetadata)
	if err != nil {
		ctx.GetLogger().Error("error unmarshalling workload metadata")
		return gitDetailFromDeployment{}, err
	}

	workloadAnnotationsMap := map[string]string{}
	workloadMetadataConfig, ok := workloadMetadata["config"]
	if ok {
		workloadAnnotations := workloadMetadataConfig.(map[string]any)["annotations"]
		if workloadAnnotations != nil {
			for key, value := range workloadAnnotations.(map[string]any) {
				workloadAnnotationsMap[key] = value.(string)
			}
		}
	}

	// For security recommendations, we need the SOURCE CODE repo (where Dockerfile is),
	// not the CI/Helm repo. Priority order:
	// 1. workloads.nudgebee.com/git.repo (source code repo - preferred for security fixes)
	// 2. ci.nudgebee.com/git.repo (CI repo - fallback)
	// 3. ArgoCD detection
	// 4. cloud_resource_attributes

	// Strategy 1: Try workloads.nudgebee.com annotations first (source code repo)
	githubRepo := workloadAnnotationsMap["workloads.nudgebee.com/git.repo"]
	orgBranch := workloadAnnotationsMap["workloads.nudgebee.com/git.branch"]
	filepath := "" // For security, we let the code agent find the Dockerfile

	// Strategy 2: Fall back to ci.nudgebee.com if no workloads annotation
	if githubRepo == "" {
		githubRepo = workloadAnnotationsMap["ci.nudgebee.com/git.repo"]
		orgBranch = workloadAnnotationsMap["ci.nudgebee.com/git.branch"]
	}

	// Strategy 3: If still not found, check for ArgoCD
	if githubRepo == "" {
		argoCDTrackingID := workloadAnnotationsMap["argocd.argoproj.io/tracking-id"]
		if argoCDTrackingID != "" {
			ctx.GetLogger().Info("Annotations not found, trying ArgoCD detection for security recommendation", "tracking_id", argoCDTrackingID)

			parts := strings.Split(argoCDTrackingID, ":")
			if len(parts) > 0 {
				argoCDAppName := parts[0]

				valuesRepo, _, targetRevision, err := getGitRepoFromArgoCD(ctx, request.Recommendation.CloudAccountId, argoCDAppName)
				if err != nil {
					ctx.GetLogger().Error("Failed to get repo from ArgoCD", "error", err, "app", argoCDAppName)
				} else if valuesRepo != "" {
					githubRepo = valuesRepo
					if targetRevision != "" {
						orgBranch = targetRevision
					}
					ctx.GetLogger().Info("Detected GitHub repo from ArgoCD for security recommendation", "repo", githubRepo, "app", argoCDAppName, "branch", orgBranch)
				}
			}
		}
	}

	// Strategy 4: Check cloud_resource_attributes for manually configured git details
	if githubRepo == "" && workloadResourceId != "" {
		ctx.GetLogger().Info("No annotations found, checking cloud_resource_attributes for security recommendation")

		var attributes []struct {
			Name  string `db:"name"`
			Value string `db:"value"`
		}
		err := dbms.Db.Select(&attributes,
			`SELECT name, value FROM cloud_resource_attributes
			 WHERE resource_id = $1 AND (name LIKE 'ci.nudgebee.com/%' OR name LIKE 'workloads.nudgebee.com/%')`,
			workloadResourceId)

		if err == nil {
			// First pass: look for workloads.nudgebee.com (source code repo - preferred)
			for _, attr := range attributes {
				switch attr.Name {
				case "workloads.nudgebee.com/git.repo":
					githubRepo = attr.Value
				case "workloads.nudgebee.com/git.branch":
					orgBranch = attr.Value
				}
			}
			// Second pass: fall back to ci.nudgebee.com if workloads not found
			if githubRepo == "" {
				for _, attr := range attributes {
					switch attr.Name {
					case "ci.nudgebee.com/git.repo":
						githubRepo = attr.Value
					case "ci.nudgebee.com/git.branch":
						orgBranch = attr.Value
					}
				}
			}
			if githubRepo != "" {
				ctx.GetLogger().Info("Found git details from cloud_resource_attributes for security recommendation", "repo", githubRepo)
			}
		}
	}

	// If still no repo found, return error
	if githubRepo == "" {
		return gitDetailFromDeployment{}, common.ErrorNotFound("error: github repo not found in workload annotations (workloads.nudgebee.com/git.repo or ci.nudgebee.com/git.repo), ArgoCD configuration, or cloud_resource_attributes for security recommendation")
	}

	// split the url to get org and repo
	orgRepo := strings.Split(githubRepo, "/")
	if len(orgRepo) != 5 {
		return gitDetailFromDeployment{}, common.ErrorNotFound("error: invalid github repo url - " + githubRepo)
	}

	// Set defaults
	if orgBranch == "" {
		orgBranch = "main"
	}
	// For security recommendations, filepath is not needed - the code agent will find the Dockerfile

	return gitDetailFromDeployment{
		Token:       password,
		Username:    username,
		Org:         orgRepo[3],
		Repo:        orgRepo[4],
		BaseBranch:  orgBranch,
		Annotations: workloadAnnotationsMap,
		FilePath:    filepath, // Empty for security - agent finds the Dockerfile
	}, nil
}

type githubAdapter struct {
}

func (k *githubAdapter) ApplyRecommendation(ctx AccountAdapterContext, request ApplyRecommendationRequest, existingRecommendations []models.RecommendationResolution, recommendResolutionId string) (ApplyRecommendationResponse, error) {
	updateExistingPR := false
	var existingPRNumber int
	githubBody := ""
	githubTitle := ""
	switch {
	case request.Recommendation.Category == "RightSizing" && request.Recommendation.RuleName == "pod_right_sizing":
		if request.Resource.Id == "" {
			return ApplyRecommendationResponse{}, fmt.Errorf("rule is not supported for empty respurce id")
		}

		// Get git details from the recommendation
		gitDetail, err := getGitDetailsFromRecommendation(ctx, request)
		if err != nil {
			return ApplyRecommendationResponse{}, err
		}

		// Use code agent to apply rightsizing - handles multi-container pods intelligently
		err = ApplyRightsizingRecommendationUsingCodeAgent(ctx, request, gitDetail, recommendResolutionId)
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
			return ApplyRecommendationResponse{}, fmt.Errorf("rule is not supported for empty respurce id")
		}

		// Get git details from the recommendation
		gitDetail, err := getGitDetailsFromRecommendation(ctx, request)
		if err != nil {
			return ApplyRecommendationResponse{}, err
		}

		// Use code agent to apply replica rightsizing - handles replica count updates intelligently
		err = ApplyRightsizingRecommendationUsingCodeAgent(ctx, request, gitDetail, recommendResolutionId)
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

	case request.Recommendation.Category == "Security" && request.Recommendation.RuleName == "image_scan":
		// Get git details from security recommendation - extracts workload_name and namespace from recommendation JSON
		// Security recommendations have resource_id = NULL, so we can't use the regular getGitDetailsFromRecommendation
		gitDetail, err := getGitDetailsFromSecurityRecommendation(ctx, request)
		if err != nil {
			return ApplyRecommendationResponse{}, err
		}

		// Use code agent with SecurityAuditorAgent to fix security vulnerability
		err = ApplySecurityRecommendationUsingCodeAgent(ctx, request, gitDetail, recommendResolutionId)
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
		gitDetail, err := getGitDetailsFromRecommendation(ctx, request)
		if err != nil {
			return ApplyRecommendationResponse{}, err
		}

		ReferenceLink := ""
		ReferenceLink = fmt.Sprintf("%s/investigate?id=%s&accountId=%s", os.Getenv("BASE_URL"), request.Recommendation.Id, request.Recommendation.CloudAccountId)
		if request.ReferenceLink != nil {
			ReferenceLink = *request.ReferenceLink
		}

		githubBody = githubBody + fmt.Sprintf("\nFor more details. Please visit [Nudgebee](%s)", ReferenceLink)
		go func() {
			dbms, err := database.GetDatabaseManager(database.Metastore)
			if err != nil {
				ctx.GetLogger().Error("failed recommendation resolution db connection", "error", err)
				return
			}
			// checkout code repo
			dir, err := checkoutCodeRepo(ctx, request, gitDetail)
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
			branchName, err := commitCodeForEvent(ctx, dir, request, gitDetail, updateExistingPR)
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

			// raise PR
			resp, err := raisePrForCodeRepo(ctx, dir, branchName, gitDetail, updateExistingPR, existingPRNumber, githubBody, request.ResolverType, githubTitle)
			if err != nil {
				ctx.GetLogger().Error("Error raising Pull Request", "error", err)
				_, err = dbms.Db.Exec(`UPDATE event_resolution SET status = $1, updated_at = $2, status_message = $4 WHERE id = $3`,
					models.RecommendationResolutionStatusFailed, time.Now(), recommendResolutionId, "Failed at raising PR")
				if err != nil {
					ctx.GetLogger().Error("error updating recommendation resolution at raise pr", "error", err)
				}
				return
			}
			prResponse := map[string]any{}
			err = common.UnmarshalJson([]byte(resp), &prResponse)
			if err != nil {
				ctx.GetLogger().Error("Error unmarshalling PR response", "error", err)
				_, err = dbms.Db.Exec(`UPDATE event_resolution SET status = $1, updated_at = $2, status_message = $4 WHERE id = $3`,
					models.RecommendationResolutionStatusFailed, time.Now(), recommendResolutionId, "Failed at unmarshalling PR Response")
				if err != nil {
					ctx.GetLogger().Error("error updating recommendation resolution", "error", err)
				}
				return
			}
			_, err = dbms.Db.Exec(`UPDATE event_resolution SET status = $1, updated_at = $2, type_reference_id = $5, status_message = $4 WHERE id = $3`,
				models.RecommendationStatusInProgress, time.Now(), recommendResolutionId, "PR raised successfully", prResponse["html_url"].(string))
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

//nolint:unused
func commitCodeGithub(ctx AccountAdapterContext, request ApplyRecommendationRequest, recommendResolutionId string, gitDetail gitDetailFromDeployment, githubBody string, updateExistingPR bool, existingPRNumber int, githubTitle string) {
	go func() {
		dbms, err := database.GetDatabaseManager(database.Metastore)
		if err != nil {
			ctx.GetLogger().Error("failed recommendation resolution db connection", "error", err)
			return
		}
		// checkout code repo
		dir, err := checkoutCodeRepo(ctx, request, gitDetail)
		tableName := "recommendation_resolution"
		if request.IsEventResolution {
			tableName = "event_resolution"
		}
		if err != nil {
			ctx.GetLogger().Error("Error doing checkout", "error", err)
			_, err = dbms.Db.Exec(fmt.Sprintf(`UPDATE %s SET status = $1, updated_at = $2, status_message = $4 WHERE id = $3`, tableName),
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
				_, err = dbms.Db.Exec(fmt.Sprintf(`UPDATE %s SET status = $1, updated_at = $2, status_message = $4 WHERE id = $3`, tableName),
					models.RecommendationResolutionStatusFailed, time.Now(), recommendResolutionId, "Failed at Remove temp dir")
				if err != nil {
					ctx.GetLogger().Error("error updating recommendation resolution at remove dir", "error", err)
				}
			}
		}()

		// update code
		branchName, err := commitCode(ctx, dir, request, gitDetail, updateExistingPR)
		if err != nil {
			message := "Failed at committing the Code"
			status := models.RecommendationResolutionStatusFailed
			if err.Error() == "No Changes Found" {
				message = "No Changes Found"
				status = models.RecommendationResolutionStatusSuccess
			}
			ctx.GetLogger().Error("Error committing the code", "error", err)
			_, err = dbms.Db.Exec(fmt.Sprintf(`UPDATE %s SET status = $1, updated_at = $2, status_message = $4 WHERE id = $3`, tableName),
				status, time.Now(), recommendResolutionId, message)
			if err != nil {
				ctx.GetLogger().Error("error updating recommendation resolution at commit code", "error", err)
			}
			return
		}

		// raise PR
		resp, err := raisePrForCodeRepo(ctx, dir, branchName, gitDetail, updateExistingPR, existingPRNumber, githubBody, request.ResolverType, githubTitle)
		if err != nil {
			ctx.GetLogger().Error("Error raising Pull Request", "error", err)
			_, err = dbms.Db.Exec(fmt.Sprintf(`UPDATE %s SET status = $1, updated_at = $2, status_message = $4 WHERE id = $3`, tableName),
				models.RecommendationResolutionStatusFailed, time.Now(), recommendResolutionId, "Failed at raising PR")
			if err != nil {
				ctx.GetLogger().Error("error updating recommendation resolution at raise pr", "error", err)
			}
			return
		}
		prResponse := map[string]any{}
		err = common.UnmarshalJson([]byte(resp), &prResponse)
		if err != nil {
			ctx.GetLogger().Error("Error unmarshalling PR response", "error", err)
			_, err = dbms.Db.Exec(`UPDATE $5 SET status = $1, updated_at = $2, status_message = $4 WHERE id = $3`,
				models.RecommendationResolutionStatusFailed, time.Now(), recommendResolutionId, "Failed at unmarshalling PR Response", tableName)
			if err != nil {
				ctx.GetLogger().Error("error updating recommendation resolution", "error", err)
			}
			return
		}
		_, err = dbms.Db.Exec(fmt.Sprintf(`UPDATE %s SET status = $1, updated_at = $2, type_reference_id = $5, status_message = $4 WHERE id = $3`, tableName),
			models.RecommendationStatusInProgress, time.Now(), recommendResolutionId, "PR raised successfully", prResponse["html_url"].(string))
		if err != nil {
			ctx.GetLogger().Error("error updating recommendation resolution", "error", err)
		}
	}()

}

func readFile(fileName string) ([]string, error) {
	file, err := os.Open(fileName)
	if err != nil {
		return nil, err
	}
	defer func() {
		err := file.Close()
		if err != nil {
			slog.Error("Error closing file", "error", err)
		}
	}()

	var lines []string
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		lines = append(lines, scanner.Text())
	}
	return lines, scanner.Err()
}

func writeFile(fileName string, lines []string) error {
	file, err := os.Create(fileName)
	if err != nil {
		return err
	}
	defer func() {
		err := file.Close()
		if err != nil {
			slog.Error("Error closing file", "error", err)
		}
	}()

	writer := bufio.NewWriter(file)
	for _, line := range lines {
		_, _ = fmt.Fprintln(writer, line)
	}
	return writer.Flush()
}

func readUpdateCodeFile(fileName string, lineNumber int, newLine string, oldLine string) error {
	lines, err := readFile(fileName)
	if err != nil {
		fmt.Printf("Error reading file: %v\n", err)
		return nil
	}

	// Update the specific line
	if lineNumber > 0 && lineNumber <= len(lines) {
		if strings.TrimSpace(lines[lineNumber-1]) == strings.TrimSpace(oldLine) {
			lines[lineNumber-1] = newLine
		} else {
			fmt.Println("Warning: The line to be replaced doesn't match the diff")
		}
	} else {
		fmt.Println("Error: Line number out of range")
		return nil
	}

	// Write the updated content back to the file
	err = writeFile(fileName, lines)
	if err != nil {
		fmt.Printf("Error writing file: %v\n", err)
		return nil
	}
	return nil
}
func (k *githubAdapter) GetRecommendationResolutionStatus(ctx AccountAdapterContext, recommendation models.Recommendation, resolutionReferenceId string, applyRequestPayload models.Json, resolutionStatusMessage string) (GetRecommendationResolutionStatusResponse, error) {
	// get PR status from Github Api and update the status
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
	if providerConfigMap["name"] == nil || providerConfigMap["name"] == "" {
		ctx.GetLogger().Error("error getting provider name")
		return GetRecommendationResolutionStatusResponse{}, common.ErrorNotFound("error: provider name not found")
	}

	// get PR id, repo and org from the PR url
	prUrl := resolutionReferenceId

	// Handle case where PR URL is not yet available (async PR creation in progress)
	if prUrl == "" {
		ctx.GetLogger().Info("PR URL not yet available, resolution still in progress", "recommendation", recommendation.Id)
		return GetRecommendationResolutionStatusResponse{
			Status:        RecommendationResolutionStatusInProgress,
			StatusMessage: "PR creation in progress",
		}, nil
	}

	_, authType, _, password, err := getGitCredentials(ctx, providerConfigMap["name"].(string))
	if err != nil {
		return GetRecommendationResolutionStatusResponse{}, err
	}

	// Get the actual GitHub token based on auth type
	var githubToken string
	if authType == "application" {
		// For GitHub App, password is the installation ID - need to get installation token
		ctx.GetLogger().Info("Using GitHub App authentication", "installation_id", password)

		// Use a fresh context with timeout for GitHub API call to avoid "context canceled" errors
		// when the parent cron job context is cancelled. This ensures the GitHub API call
		// can complete even if the HTTP request context is cancelled after sending response.
		span := trace.SpanFromContext(ctx.GetContext())
		tokenCtx, cancel := context.WithTimeout(trace.ContextWithSpan(context.Background(), span), 30*time.Second)
		defer cancel()

		githubToken, err = common.GetGithubAppInstallationToken(tokenCtx, password)
		if err != nil {
			ctx.GetLogger().Error("Failed to get GitHub App installation token", "error", err)
			return GetRecommendationResolutionStatusResponse{
				Status:        RecommendationResolutionStatusFailed,
				StatusMessage: fmt.Sprintf("GitHub authentication failed: %s", err.Error()),
			}, nil
		}
	} else {
		// For token auth, use the password directly as the token
		githubToken = password
	}

	prUrlParts := strings.Split(prUrl, "/")
	// Valid GitHub PR URL format: https://github.com/org/repo/pull/123
	// After split: ["https:", "", "github.com", "org", "repo", "pull", "123"]
	// Minimum length should be 7
	if len(prUrlParts) < 7 {
		ctx.GetLogger().Error("Invalid GitHub PR URL format", "url", prUrl, "recommendation", recommendation.Id, "parts_count", len(prUrlParts))
		return GetRecommendationResolutionStatusResponse{
			Status:        RecommendationResolutionStatusFailed,
			StatusMessage: fmt.Sprintf("Invalid GitHub PR URL format: %s", prUrl),
		}, nil
	}
	prId := prUrlParts[len(prUrlParts)-1]
	repo := prUrlParts[len(prUrlParts)-3]
	org := prUrlParts[len(prUrlParts)-4]

	resp, err := common.HttpGet(fmt.Sprintf("https://api.github.com/repos/%s/%s/pulls/%s", org, repo, prId), common.HttpWithHeaders(map[string]string{
		"Authorization": fmt.Sprintf("Bearer %s", githubToken),
	}))
	if err != nil {
		ctx.GetLogger().Error("Error getting PR status", "error", err)
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
		ctx.GetLogger().Error("Error getting PR status", "status", resp.StatusCode, "data", string(jsonBodyBytes))
		return GetRecommendationResolutionStatusResponse{
			Status:        RecommendationResolutionStatusFailed,
			StatusMessage: string(jsonBodyBytes),
		}, nil
	}

	prStatus := RecommendationResolutionStatusInProgress
	prMessage := "PR is open and awaiting review/merge"
	if jsonBody["state"] == "closed" {
		if jsonBody["merged_at"] != nil {
			prStatus = RecommendationResolutionStatusSuccess
			prMessage = "PR was successfully merged"
		} else {
			prStatus = RecommendationResolutionStatusFailed
			prMessage = "PR was closed without merging"
		}
	}

	return GetRecommendationResolutionStatusResponse{
		Status:        prStatus,
		StatusMessage: prMessage,
	}, nil
}

//nolint:unused
func extractNumberFromURL(link string) (string, error) {
	parsedURL, err := url.Parse(link)
	if err != nil {
		return "", err
	}

	re := regexp.MustCompile(`/(\d+)$`)
	matches := re.FindStringSubmatch(parsedURL.Path)
	if len(matches) < 2 {
		return "", fmt.Errorf("no number found in URL")
	}

	return matches[1], nil
}

func ApplyRightsizingRecommendationUsingCodeAgent(ctx AccountAdapterContext, request ApplyRecommendationRequest, gitDetail gitDetailFromDeployment, recommendResolutionId string) error {
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
		updateStatus := func(status models.RecommendationResolutionStatus, statusMessage string, prUrl string) {
			query := fmt.Sprintf(`UPDATE %s SET status = $1, updated_at = $2, status_message = $3 WHERE id = $4`, tableName)
			params := []any{status, time.Now(), statusMessage, recommendResolutionId}

			// If PR URL is provided, also update type_reference_id and PR lifecycle columns
			if prUrl != "" {
				query = fmt.Sprintf(`UPDATE %s SET status = $1, updated_at = $2, status_message = $3, type_reference_id = $5, pr_lifecycle_state = $6, pr_iteration_count = $7, last_pr_check_at = $8 WHERE id = $4`, tableName)
				params = append(params, prUrl, "created", 0, time.Now())
			}

			_, err := dbms.Db.ExecContext(context.Background(), query, params...)
			if err != nil {
				ctx.GetLogger().Error("error updating recommendation resolution status", "error", err, "status", status)
			}
		}

		// Build structured prompt with clear instructions
		// Using @agent_code_2 to invoke the code agent
		recommendationJSON, _ := common.MarshalJson(request.Recommendation)
		requestDataJSON, _ := common.MarshalJson(request.Data)

		queryText := fmt.Sprintf(`Please apply the following Kubernetes resource rightsizing recommendations.

**Repository**: %s/%s
**Branch**: %s
**Helm Values File**: %s

**User Requested Values** (use these exact values for the update):
%s

**Recommendation Context**:
%s

**Instructions**:
1. View the Helm values file to understand the current resource configuration
2. If you need to understand the template mapping, search for templates (deployment.yaml, Chart.yaml). If no templates are found after 2-3 searches, this is likely a values-only repository - proceed with direct modification of the values file.
3. Identify the correct YAML path in the values file for each container
4. Update only the specified CPU/memory values at the correct paths
5. Preserve existing formatting and structure
6. **CRITICAL - CPU Limits**: If the recommendation specifies CPU limit as null, empty, or omitted, you MUST remove the CPU limit line entirely or leave it unset. DO NOT set CPU limit to match the request value. Only set CPU limit if explicitly provided with a non-null value in the recommendation.

**PR Description Requirements**:
When creating the PR, ensure the description includes:
1. **Summary**: Brief explanation of the rightsizing changes being applied
2. **Changes Table**: Before/after comparison of CPU and memory values for each container
3. **Motivation**: Explain the cost optimization and performance benefits

Use this format for the changes table:
| Container | Resource | Before | After | Change |
|-----------|----------|--------|-------|--------|
| <name> | CPU Request | <old> | <new> | <diff> |
| <name> | Memory Limit | <old> | <new> | <diff> |

Make minimal, precise changes only.`,
			gitDetail.Org,
			gitDetail.Repo,
			gitDetail.BaseBranch,
			gitDetail.FilePath,
			string(requestDataJSON),
			string(recommendationJSON),
		)

		// Wrap the prompt in a JSON envelope so agent_code_2 receives explicit
		// intent flags (mode + raise_pr). agent_code_2 must not infer intent
		// from the prompt; the entrypoint declares it.
		repoURL := fmt.Sprintf("https://github.com/%s/%s", gitDetail.Org, gitDetail.Repo)
		codeAgentPayload := map[string]any{
			"query":             queryText,
			"git_repo":          repoURL,
			"mode":              "fix",
			"raise_pr":          true,
			"recommendation_id": request.Recommendation.Id,
			"account_id":        request.Recommendation.CloudAccountId,
		}
		codeAgentPayloadJSON, _ := common.MarshalJson(codeAgentPayload)
		prompt := "@agent_code_2 " + string(codeAgentPayloadJSON)

		// Construct the request payload - simple pattern like datadog_webhook
		// Pass recommendation metadata via Config for PR description link generation
		chatRequest := llm.ConversationApiRequest{
			Query:     prompt,
			AccountId: request.Recommendation.CloudAccountId,
			UserId:    ctx.GetSecurityContext().GetUserId(),
			Async:     false,
			Source:    "recommendation",
			Config: map[string]any{
				"recommendation_id": request.Recommendation.Id,
				"account_id":        request.Recommendation.CloudAccountId,
				"git_repo":          fmt.Sprintf("https://github.com/%s/%s", gitDetail.Org, gitDetail.Repo),
			},
		}

		// Create a new context with trace propagation for LLM server call
		// We use context.Background() as the base since the HTTP request context is already cancelled
		// But we copy the trace span from the original context to maintain trace continuity
		span := trace.SpanFromContext(ctx.GetContext())
		llmCtx := trace.ContextWithSpan(context.Background(), span)

		// Call code agent with trace-propagated context
		// The trace will be automatically propagated via OpenTelemetry's otelhttp client
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

		// Parse the response to extract PR information
		var agentResponse map[string]any
		err = common.UnmarshalJson([]byte(response.Response[0]), &agentResponse)
		if err != nil {
			ctx.GetLogger().Error("recommendation_resolution: failed to parse agent response", "error", err, "response", response.Response[0])
			updateStatus(models.RecommendationResolutionStatusFailed, "Failed to parse code agent response", "")
			return
		}

		// Extract PR information from response
		// Try multiple possible response formats
		var prUrl string
		var prNumber any

		// Check for automated_fix_pr_info structure
		if prInfo, ok := agentResponse["automated_fix_pr_info"].(map[string]any); ok && prInfo != nil {
			if url, ok := prInfo["url"].(string); ok {
				prUrl = url
			}
			if num, ok := prInfo["number"]; ok {
				prNumber = num
			}
		}

		// Fallback to fix_pr structure
		if prUrl == "" {
			if prInfo, ok := agentResponse["fix_pr"].(map[string]any); ok && prInfo != nil {
				if url, ok := prInfo["url"].(string); ok {
					prUrl = url
				}
				if num, ok := prInfo["number"]; ok {
					prNumber = num
				}
			}
		}

		// Fallback to direct pr_url field
		if prUrl == "" {
			if url, ok := agentResponse["pr_url"].(string); ok {
				prUrl = url
			}
		}

		// Check execution status
		executionStatus, _ := agentResponse["execution_status"].(string)

		// Determine success or failure
		// Priority: PR URL presence indicates success, regardless of execution_status field
		if prUrl != "" {
			// Success: PR was created — also store PR metadata in data JSONB for lifecycle tracking
			ctx.GetLogger().Info("recommendation_resolution: PR created successfully", "pr_url", prUrl, "pr_number", prNumber)
			updateStatus(models.RecommendationResolutionStatusInProgress, "PR raised successfully by code agent", prUrl)

			// Store PR metadata for cron-based lifecycle tracking
			repoURL := fmt.Sprintf("https://github.com/%s/%s", gitDetail.Org, gitDetail.Repo)
			prMeta := map[string]any{
				"pr_url":    prUrl,
				"pr_number": prNumber,
				"repo_url":  repoURL,
				"branch":    gitDetail.BaseBranch,
				"provider":  "github",
				"org":       gitDetail.Org,
				"repo":      gitDetail.Repo,
			}
			if branchName, ok := agentResponse["branch"].(string); ok {
				prMeta["pr_branch"] = branchName
			}
			prMetaJSON, marshalErr := common.MarshalJson(prMeta)
			if marshalErr == nil {
				_, _ = dbms.Db.ExecContext(context.Background(),
					fmt.Sprintf(`UPDATE %s SET data = $1 WHERE id = $2`, tableName),
					string(prMetaJSON), recommendResolutionId)
			}
		} else if executionStatus == "success" {
			// Agent succeeded but no PR was created (might have applied changes directly)
			ctx.GetLogger().Warn("recommendation_resolution: code agent succeeded but no PR URL found")
			updateStatus(models.RecommendationResolutionStatusSuccess, "Code agent applied changes but no PR was created", "")
		} else {
			// Failure: No PR and no success status
			errorMsg := "Code agent execution failed"
			if summary, ok := agentResponse["execution_summary"].(string); ok && summary != "" {
				errorMsg = summary
			} else if msg, ok := agentResponse["error"].(string); ok && msg != "" {
				errorMsg = msg
			} else if executionStatus != "" {
				errorMsg = fmt.Sprintf("Code agent execution status: %s", executionStatus)
			}

			// Append detailed analysis fields from the agent response
			errorMsg = appendAgentResponseDetails(errorMsg, agentResponse)

			ctx.GetLogger().Error("recommendation_resolution: code agent failed", "error", errorMsg, "response", agentResponse)
			updateStatus(models.RecommendationResolutionStatusFailed, errorMsg, "")
		}
	}()

	return nil
}

// ApplySecurityRecommendationUsingCodeAgent invokes the code-analysis agent's SecurityAuditorAgent
// to fix container image security vulnerabilities (CVEs) and create a PR.
func ApplySecurityRecommendationUsingCodeAgent(ctx AccountAdapterContext, request ApplyRecommendationRequest, gitDetail gitDetailFromDeployment, recommendResolutionId string) error {
	// Run asynchronously to avoid blocking the request
	go func() {
		// Recover from any panics to prevent crashing the application
		defer func() {
			if r := recover(); r != nil {
				ctx.GetLogger().Error("recommendation_resolution: panic recovered in security code agent goroutine", "panic", r, "recommendation_id", recommendResolutionId)

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
						fmt.Sprintf("Panic during security code agent execution: %v", r),
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
		updateStatus := func(status models.RecommendationResolutionStatus, statusMessage string, prUrl string) {
			query := fmt.Sprintf(`UPDATE %s SET status = $1, updated_at = $2, status_message = $3 WHERE id = $4`, tableName)
			params := []any{status, time.Now(), statusMessage, recommendResolutionId}

			// If PR URL is provided, also update type_reference_id and PR lifecycle columns
			if prUrl != "" {
				query = fmt.Sprintf(`UPDATE %s SET status = $1, updated_at = $2, status_message = $3, type_reference_id = $5, pr_lifecycle_state = $6, pr_iteration_count = $7, last_pr_check_at = $8 WHERE id = $4`, tableName)
				params = append(params, prUrl, "created", 0, time.Now())
			}

			_, err := dbms.Db.ExecContext(context.Background(), query, params...)
			if err != nil {
				ctx.GetLogger().Error("error updating recommendation resolution status", "error", err, "status", status)
			}
		}

		// Extract CVE data from recommendation JSONB
		recData, ok := request.Recommendation.Recommendation.Object().(map[string]any)
		if !ok || recData == nil {
			ctx.GetLogger().Error("recommendation_resolution: failed to parse recommendation data as map")
			updateStatus(models.RecommendationResolutionStatusFailed, "Failed to parse recommendation data", "")
			return
		}

		// Format CVE logs for the agent (matches E2E test format)
		cveLogs := formatCVELogs(recData)

		// Build structured prompt with clear instructions
		recommendationJSON, _ := common.MarshalJson(request.Recommendation)

		// Build the query text for the agent
		// Extract workload context for disambiguation (may be empty for some flows)
		workloadName, _ := request.Data["workload_name"].(string)
		workloadNamespace, _ := request.Data["namespace"].(string)

		queryText := fmt.Sprintf(`Please fix the following container image security vulnerability.

**Repository**: %s/%s
**Branch**: %s
**Workload**: %s (namespace: %s)

**Vulnerability Details**:
%s

**Recommendation Context**:
%s

**Instructions**:
1. **Find the Dockerfile** - Image names are NOT written inside Dockerfiles. Use these strategies:
   a. Use file_find to list ALL Dockerfiles in the repository
   b. Match directory names against parts of the image name AND the workload name (strip the registry prefix and tag, then look for directories matching the remaining name)
   c. Search CI/CD workflow files for the image name - the workflow that builds it will reference the Dockerfile path
   d. Do NOT search inside Dockerfiles for the image name using grep/ripgrep - it won't be there
   e. VERIFY your chosen Dockerfile: check that its base image and contents are consistent with the service type (e.g., a Python service should not have a Go Dockerfile)
2. **Read the Dockerfile** and check the FROM statement to identify the base image
3. **Trace the base image chain**:
   - If the base image is from a custom/internal registry (not a well-known public registry), search for its Dockerfile in the repo
   - Use ripgrep to search for the base image name across all Dockerfiles and CI/CD files
   - If the base image Dockerfile is found in the repo, fix the vulnerability THERE (root cause fix)
   - If NOT found, proceed with workaround fix in the service Dockerfile
4. **Determine fix location**: Check if the vulnerable package is explicitly installed in the Dockerfile or comes from the base image
5. Implement the fix with minimal changes
6. Create a PR with clear description of the security fix

**CRITICAL - Preserving Existing Code**:
- Keep existing lines (like COPY commands) intact when adding new commands
- Add the new RUN command AFTER the relevant existing command
- Do NOT delete functional lines when inserting new code
- If upgrading a package, add the upgrade command, don't replace unrelated lines

**PR Description Requirements**:
When creating the PR, ensure the description includes:
1. **Summary**: Brief explanation of the CVE being fixed
2. **Vulnerability**: CVE ID, severity, and affected package
3. **Fix**: Description of the fix applied`,
			gitDetail.Org,
			gitDetail.Repo,
			gitDetail.BaseBranch,
			workloadName,
			workloadNamespace,
			cveLogs,
			string(recommendationJSON),
		)

		// Construct JSON payload for code agent with explicit git_repo field
		// NOTE: Do NOT pass "agent" field - let the Orchestrator handle the full flow:
		// 1. Orchestrator clones repo with credentials via repo_clone tool
		// 2. Router selects SecurityAuditorAgent based on security-related prompt content
		// If we pass "agent", it bypasses Orchestrator and the specialist agent won't have repo access
		repoURL := fmt.Sprintf("https://github.com/%s/%s", gitDetail.Org, gitDetail.Repo)
		codeAgentPayload := map[string]any{
			"query":             queryText,
			"git_repo":          repoURL,
			"mode":              "fix",
			"raise_pr":          true,
			"recommendation_id": request.Recommendation.Id,
			"account_id":        request.Recommendation.CloudAccountId,
		}
		codeAgentPayloadJSON, _ := common.MarshalJson(codeAgentPayload)

		// Prepend @agent_code_2 directive to invoke the code agent
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
			ctx.GetLogger().Error("recommendation_resolution: failed to get chat completion request for security fix", "error", err)
			updateStatus(models.RecommendationResolutionStatusFailed, fmt.Sprintf("Failed to execute security code agent: %s", err.Error()), "")
			return
		}

		if response == nil || len(response.Response) == 0 {
			ctx.GetLogger().Warn("recommendation_resolution: security chat completion returned empty response")
			updateStatus(models.RecommendationResolutionStatusFailed, "Security code agent returned empty response", "")
			return
		}

		// Parse the response to extract PR information
		var agentResponse map[string]any
		err = common.UnmarshalJson([]byte(response.Response[0]), &agentResponse)
		if err != nil {
			ctx.GetLogger().Error("recommendation_resolution: failed to parse security agent response", "error", err, "response", response.Response[0])
			updateStatus(models.RecommendationResolutionStatusFailed, "Failed to parse security code agent response", "")
			return
		}

		// Extract PR information from response
		// Try multiple possible response formats (same as rightsizing)
		var prUrl string
		var prNumber any

		// Check for automated_fix_pr_info structure
		if prInfo, ok := agentResponse["automated_fix_pr_info"].(map[string]any); ok && prInfo != nil {
			if url, ok := prInfo["url"].(string); ok {
				prUrl = url
			}
			if num, ok := prInfo["number"]; ok {
				prNumber = num
			}
		}

		// Fallback to fix_pr structure
		if prUrl == "" {
			if prInfo, ok := agentResponse["fix_pr"].(map[string]any); ok && prInfo != nil {
				if url, ok := prInfo["url"].(string); ok {
					prUrl = url
				}
				if num, ok := prInfo["number"]; ok {
					prNumber = num
				}
			}
		}

		// Fallback to direct pr_url field
		if prUrl == "" {
			if url, ok := agentResponse["pr_url"].(string); ok {
				prUrl = url
			}
		}

		// Check execution status
		executionStatus, _ := agentResponse["execution_status"].(string)

		// Determine success or failure
		// Priority: PR URL presence indicates success, regardless of execution_status field
		if prUrl != "" {
			// Success: PR was created — also store PR metadata in data JSONB for lifecycle tracking
			ctx.GetLogger().Info("recommendation_resolution: security PR created successfully", "pr_url", prUrl, "pr_number", prNumber)
			updateStatus(models.RecommendationResolutionStatusInProgress, "Security fix PR raised successfully by code agent", prUrl)

			// Store PR metadata for cron-based lifecycle tracking
			repoURL := fmt.Sprintf("https://github.com/%s/%s", gitDetail.Org, gitDetail.Repo)
			prMeta := map[string]any{
				"pr_url":    prUrl,
				"pr_number": prNumber,
				"repo_url":  repoURL,
				"branch":    gitDetail.BaseBranch,
				"provider":  "github",
				"org":       gitDetail.Org,
				"repo":      gitDetail.Repo,
			}
			if branchName, ok := agentResponse["branch"].(string); ok {
				prMeta["pr_branch"] = branchName
			}
			prMetaJSON, marshalErr := common.MarshalJson(prMeta)
			if marshalErr == nil {
				_, _ = dbms.Db.ExecContext(context.Background(),
					fmt.Sprintf(`UPDATE %s SET data = $1 WHERE id = $2`, tableName),
					string(prMetaJSON), recommendResolutionId)
			}
		} else if executionStatus == "success" {
			// Agent succeeded but no PR was created (might have applied changes directly)
			ctx.GetLogger().Warn("recommendation_resolution: security code agent succeeded but no PR URL found")
			updateStatus(models.RecommendationResolutionStatusSuccess, "Security code agent applied changes but no PR was created", "")
		} else {
			// Failure: No PR and no success status
			errorMsg := "Security code agent execution failed"
			if summary, ok := agentResponse["execution_summary"].(string); ok && summary != "" {
				errorMsg = summary
			} else if msg, ok := agentResponse["error"].(string); ok && msg != "" {
				errorMsg = msg
			} else if executionStatus != "" {
				errorMsg = fmt.Sprintf("Security code agent execution status: %s", executionStatus)
			}

			// Append detailed analysis fields from the agent response
			errorMsg = appendAgentResponseDetails(errorMsg, agentResponse)

			ctx.GetLogger().Error("recommendation_resolution: security code agent failed", "error", errorMsg, "response", agentResponse)
			updateStatus(models.RecommendationResolutionStatusFailed, errorMsg, "")
		}
	}()

	return nil
}

// appendAgentResponseDetails enriches an error message with detailed analysis fields
// from the code agent response (error_message, root_cause_analysis, description).
func appendAgentResponseDetails(baseMsg string, agentResponse map[string]any) string {
	var details []string

	if errMsg, ok := agentResponse["error_message"].(string); ok && errMsg != "" {
		details = append(details, fmt.Sprintf("Error: %s", errMsg))
	}
	if rca, ok := agentResponse["root_cause_analysis"].(string); ok && rca != "" {
		details = append(details, fmt.Sprintf("Root Cause: %s", rca))
	}
	if desc, ok := agentResponse["description"].(string); ok && desc != "" {
		details = append(details, fmt.Sprintf("Description: %s", desc))
	}

	if len(details) == 0 {
		return baseMsg
	}
	return baseMsg + "\n\n" + strings.Join(details, "\n\n")
}

// formatCVELogs formats recommendation data into the CVE log format expected by SecurityAuditorAgent
func formatCVELogs(recData map[string]any) string {
	var sb strings.Builder

	// Extract fields from recommendation JSONB
	// Support both old field names and actual Trivy scan field names
	cveId := getStringFromMultipleKeys(recData, []string{"VulnerabilityID", "cve_id", "cve"}, "Unknown CVE")
	severity := getStringFromMultipleKeys(recData, []string{"Severity", "severity"}, "Unknown")
	imageName := getStringFromMultipleKeys(recData, []string{"image_name", "Image", "image"}, "Unknown image")
	packageName := getStringFromMultipleKeys(recData, []string{"PkgName", "PkgID", "package_name", "package"}, "Unknown package")
	installedVersion := getStringFromMultipleKeys(recData, []string{"InstalledVersion", "installed_version"}, "Unknown")
	fixedVersion := getStringFromMultipleKeys(recData, []string{"FixedVersion", "fixed_version"}, "Unknown")
	description := getStringFromMultipleKeys(recData, []string{"Description", "description", "Title"}, "")
	status := getStringFromMultipleKeys(recData, []string{"Status", "status"}, "fixed")

	fmt.Fprintf(&sb, "%s: %s vulnerability in %s@%s\n", cveId, severity, packageName, installedVersion)
	fmt.Fprintf(&sb, "Image: %s\n", imageName)
	fmt.Fprintf(&sb, "Severity: %s\n", strings.ToUpper(severity))
	fmt.Fprintf(&sb, "Status: %s (fix available)\n", status)
	fmt.Fprintf(&sb, "Installed Version: %s\n", installedVersion)
	fmt.Fprintf(&sb, "Fixed Version: %s\n", fixedVersion)
	fmt.Fprintf(&sb, "Package: %s\n", packageName)

	if description != "" {
		fmt.Fprintf(&sb, "\nDescription: %s\n", description)
	}

	return sb.String()
}

// getStringFromMultipleKeys tries multiple keys in order and returns the first non-empty value found
func getStringFromMultipleKeys(m map[string]any, keys []string, defaultVal string) string {
	for _, key := range keys {
		if v, ok := m[key].(string); ok && v != "" {
			return v
		}
	}
	return defaultVal
}
