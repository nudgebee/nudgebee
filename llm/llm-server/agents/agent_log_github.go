package agents

import (
	"bytes"
	"errors"
	"fmt"
	"nudgebee/llm/agents/core"
	"nudgebee/llm/common"
	"nudgebee/llm/security"
	toolcore "nudgebee/llm/tools/core"
	"nudgebee/llm/utils"
	"strings"

	"github.com/google/go-github/v66/github"
	"github.com/tmc/langchaingo/llms"
)

func init() {
}

const LogGithubAgentName = "loggithub"

// based on error logs, generate diff if possible
// or provide code-based analysis
type LogGithubAgent struct {
}

func (l LogGithubAgent) GetName() string {
	return LogGithubAgentName
}

func (l LogGithubAgent) GetNameAliases() []string {
	return []string{"Github Code Generator"}
}

func (l LogGithubAgent) GetDescription() string {
	return `Analyzes error logs and file content from GitHub repositories to identify root causes, generate minimal code diffs for resolution, or provide code-based analysis. Use this agent to debug, review, or automate fixes by supplying error logs and relevant file context. Returns Git diffs or detailed explanations for automation, code review, or troubleshooting.`
}

func (l LogGithubAgent) GetSupportedTools(ctx *security.RequestContext) []toolcore.NBTool {
	return []toolcore.NBTool{}
}

func (l LogGithubAgent) GetSystemPrompt(ctx *security.RequestContext, query core.NBAgentRequest) core.NBAgentPrompt {

	instructions := []string{
		"**Input Analysis:** You will receive a set of error logs and the content of a relevant source code file.",
		"**Root Cause Identification:** Analyze both the error logs and the source code file to pinpoint the underlying cause of the error(s).",
		"**Git Diff Generation:** If you can identify a code-level fix, generate a Git diff that represents the minimal changes required to resolve the error(s).",
		"**Explanation:** Provide a detailed explanation of the Git diff, including:",
		"   - The issue being addressed.",
		"   - Why the change is necessary.",
		"   - How the change resolves the issue.",
		"   - Any potential side effects or trade-offs.",
		"**No Action:** If, after analysis, you determine that no code change is needed, do not generate a Git diff. Instead, provide an explanation of why no action is necessary. Also highlight the word where you mention no changes are required",
		"**JSON or Explanation Output:**",
		"   - If generating a Git diff, return a JSON object with the `gitDiff`, `explanation`, and `file_path` fields.",
		"   - If no Git diff is needed, return a text explanation of the analysis and why no code change is necessary.",
		"**Error Handling:** If you encounter problems parsing the logs or the code, state the nature of the parsing issue in your explanation.",
		"**Logs**",
		"{{ .log }}",
		"**File**",
		"{{ .file }}",
	}
	constraints := []string{
		"You are a highly experienced software engineer specializing in debugging and code analysis.",
		"Your goal is to analyze logs and provide code-level fixes or a clear explanation of why no fix is required.",
		"If no code-level fix is identified, do not generate a Git diff, only provide an explanation.",
		"Do not include any extra information outside of the JSON or text explanation.",
	}
	examples := []core.NBAgentPromptExample{
		{
			Question:    "Analyze the given logs and file.",
			Answer:      "{\n\"gitDiff\": \"Generated Git diff\",\n\"explanation\": \"Explanation of Diff, which can be used in Pull Request\", \"file_path\":\"path/to/file\"}",
			Explanation: "If able to generate gitDiff, it will return a json with 'gitDiff', 'explanation' and file_path keys.",
		},
		{
			Question:    "Analyze the given logs and file.",
			Answer:      "Explanation of the error and why no code-level fix is needed.",
			Explanation: "If unable to generate a diff, it will only return explanation.",
		},
	}
	return core.NBAgentPrompt{
		Role:         "a highly experienced software engineer specializing in debugging and code analysis",
		Instructions: instructions,
		Constraints:  constraints,
		Examples:     examples,
		OutputFormat: "JSON",
		Variables:    []string{"log", "file"},
	}
}

type LogGithubAgentRequest struct {
	Query                string           `json:"query" validate:"required"`
	Errors               []string         `json:"errors" validate:"required"`
	Files                []map[string]any `json:"files" validate:"required"`
	GitRepo              string           `json:"git_repo" validate:"required"`
	GitCommit            string           `json:"git_commit"`
	InvestigationContext string           `json:"investigation_context,omitempty"`
}

type diffResponse struct {
	GitDiff     string `json:"gitDiff"`
	Explanation string `json:"explanation"`
	FilePath    string `json:"file_path"`
}

func (l LogGithubAgent) GetPlannerType() core.AgentPlannerType {
	return core.AgentPlannerTypeCustom
}

func (l LogGithubAgent) Execute(ctx *security.RequestContext, query core.NBAgentRequest) (core.NBAgentResponse, error) {
	logGithubAnalysisRequest := LogGithubAgentRequest{}
	err := common.UnmarshalJson([]byte(query.Query), &logGithubAnalysisRequest)
	if err != nil {
		ctx.GetLogger().Error("loganalysisgithub: failed to parse request", "error", err.Error(), "request", query.Query)
		return core.NBAgentResponse{}, err
	}
	err = common.ValidateStruct(logGithubAnalysisRequest)
	if err != nil {
		return core.NBAgentResponse{}, err
	}
	if len(logGithubAnalysisRequest.Errors) == 0 {
		return core.NBAgentResponse{}, common.ErrorNotFound("loganalysisgithub: no errors found")
	}
	if len(logGithubAnalysisRequest.Files) == 0 {
		return core.NBAgentResponse{}, common.ErrorNotFound("loganalysisgithub: no files found")
	}

	githubCredentials, actualRepo, err := l.getGitCredentials(ctx, logGithubAnalysisRequest.GitRepo, query.AccountId)
	if err != nil {
		ctx.GetLogger().Error("loganalysisgithub: unable to get git creds", "error", err)
		return core.NBAgentResponse{}, err
	}

	if len(githubCredentials) == 0 {
		return core.NBAgentResponse{}, common.ErrorNotFound("loganalysisgithub: no credentials found")

	}

	var path, gitFileAndContent string

	// check multiple credentials
	for _, githubCredential := range githubCredentials {
		path, gitFileAndContent, err = l.searchFileAndGetContentFromGithub(ctx, githubCredential.Url, githubCredential.AuthType, githubCredential.Username, githubCredential.Password, actualRepo, logGithubAnalysisRequest.Files)
		if path != "" {
			break
		}
	}

	if err != nil {
		ctx.GetLogger().Error("loganalysisgithub: unable to search for file content", "error", err.Error())
		return core.NBAgentResponse{}, err
	}

	systemMessage, err := core.GetPromptTemplate(l.GetSystemPrompt(ctx, query), query, l.GetPlannerType()).Format(map[string]any{
		"log":  strings.Join(logGithubAnalysisRequest.Errors, ","),
		"file": gitFileAndContent,
	})
	if err != nil {
		ctx.GetLogger().Error("loganalysisgithub: to get prompt", "error", err.Error())
		return core.NBAgentResponse{}, err
	}

	// Prepend investigation context if available so the LLM understands the root cause
	// before analyzing code, leading to more targeted fixes.
	if logGithubAnalysisRequest.InvestigationContext != "" {
		systemMessage = fmt.Sprintf("## Investigation Context (Root Cause Analysis)\n%s\n\n%s",
			logGithubAnalysisRequest.InvestigationContext, systemMessage)
	}

	messageHistory := []llms.MessageContent{llms.TextParts(llms.ChatMessageTypeHuman, systemMessage)}

	completion, err := core.GenerateAndTrackLLMContent(ctx, query.UserId, query.AccountId, query.ConversationId, query.MessageId, query.AgentId, false, messageHistory, true, llms.WithTemperature(0.0))
	if err != nil {
		ctx.GetLogger().Error("loganalysisgithub: unable to generate content", "error", err)
		return core.NBAgentResponse{}, err
	}

	if completion == nil || len(completion.Choices) == 0 || strings.TrimSpace(completion.Choices[0].Content) == "" {
		ctx.GetLogger().Error("loganalysisgithub: LLM returned empty response")
		return core.NBAgentResponse{}, errors.New("loganalysisgithub: LLM returned empty response")
	}

	finalResponse := strings.TrimSpace(completion.Choices[0].Content)

	diffResponse1 := diffResponse{}
	err = common.UnmarshalJson([]byte(finalResponse), &diffResponse1)
	if err != nil {
		ctx.GetLogger().Info("unable to serialize json", "content", finalResponse)
		return core.NBAgentResponse{Response: []string{`{"explanation": "` + finalResponse + `"}`}, Status: core.ConversationStatusCompleted}, nil
	}

	diffResponse1.FilePath = path
	finalResponse1, err := common.MarshalJson(diffResponse1)
	if err != nil {
		ctx.GetLogger().Info("unable to serialize json", "content", finalResponse)
		return core.NBAgentResponse{Response: []string{`{"explanation": "` + finalResponse + `"}`}}, nil
	}

	return core.NBAgentResponse{Response: []string{string(finalResponse1)}}, nil
}

func (l LogGithubAgent) searchFileAndGetContentFromGithub(ctx *security.RequestContext, githubApiUrl string, githubAuthType string, githubUsername string, githubPassword string, githubRepo string, files []map[string]any) (string, string, error) {

	// use github apis to search for file in the given repo & return all the possible files based on file name
	client, err := utils.CreateGithubClient(ctx.GetContext(), githubApiUrl, githubAuthType, githubUsername, githubPassword)
	if err != nil {
		ctx.GetLogger().Error("loganalysisgithub: unable to create github client", "error", err)
		return "", "", err
	}

	repoSplits := strings.Split(githubRepo, "/")
	// github ui's code search is different from api, so using filename instead of path:
	codeSearchExpression := "filename:" + files[0]["file_name"].(string) + " repo:" + githubRepo

	codeResult, response, err := client.Search.Code(ctx.GetContext(), codeSearchExpression, nil)
	if err != nil {
		ctx.GetLogger().Error("unable to search repo", "error", err, "repo", githubRepo, "file", files[0]["file_name"])
		return "", "", err
	}
	if len(codeResult.CodeResults) == 0 {
		ctx.GetLogger().Error("unable to find file in", "error", err, "repo", githubRepo, "file", files[0]["file_name"], "statusCode", response.StatusCode)
		return "", "", errors.New("loganalysisgithub: file not found")
	}

	//find closest match
	filePath := files[0]["file_path"].(string)
	filePathSplits := strings.Split(filePath, "/")
	resultsToCheck := codeResult.CodeResults
	stepsToCheck := 2
	// Track if we've already tried matching with the full path
	hasTriedFullPath := false

	for len(resultsToCheck) > 1 {
		// Safety check to prevent negative slice index
		startIndex := len(filePathSplits) - stepsToCheck
		if startIndex < 0 {
			startIndex = 0
			// If we've already tried with the full path  break the loop
			if hasTriedFullPath {
				break
			}
			hasTriedFullPath = true
		}

		filePathToMatch := strings.Join(filePathSplits[startIndex:], "/")
		resultsToCheck2 := []*github.CodeResult{}
		for _, cr := range resultsToCheck {
			if strings.HasSuffix(*cr.Path, filePathToMatch) {
				resultsToCheck2 = append(resultsToCheck2, cr)
			}
		}
		// If no matches found using this prefix, break to avoid losing all results
		if len(resultsToCheck2) == 0 {
			break
		}

		resultsToCheck = resultsToCheck2
		stepsToCheck++
	}

	if len(resultsToCheck) == 0 {
		return "", "", errors.New("loganalysisgithub: multiple matching files, unable to identify single file")
	}

	fileContent, _, err := client.Repositories.DownloadContents(ctx.GetContext(), repoSplits[0], repoSplits[1], *resultsToCheck[0].Path, nil)
	if err != nil {
		return "", "", err
	}
	defer func() {
		if err := fileContent.Close(); err != nil {
			ctx.GetLogger().Error("loganalysisgithub: unable to close file content", "error", err)
		}
	}()

	buf := new(bytes.Buffer)
	if _, err := buf.ReadFrom(fileContent); err != nil {
		return "", "", errors.New("loganalysisgithub: unable to readfile from github")
	}

	return *resultsToCheck[0].Path, buf.String(), nil
}

type GitCredentials struct {
	Username string              `json:"username"`
	Url      string              `json:"url"`
	Password string              `json:"password"`
	AuthType string              `json:"auth_type"`
	Projects []map[string]string `json:"projects"`
	Provider string              `json:"provider"` // "github" or "gitlab"
}

func (l LogGithubAgent) getGitCredentials(ctx *security.RequestContext, repo string, accountId string) ([]GitCredentials, string, error) {

	credentials := []GitCredentials{}

	repoSplits := strings.Split(repo, "/")
	actualRepo := repoSplits[len(repoSplits)-2] + "/" + repoSplits[len(repoSplits)-1]

	dbms, err := common.GetDatabaseManager(common.Metastore)
	if err != nil {
		return credentials, actualRepo, err
	}

	// Query integrations table for github type
	query := `
		SELECT
			i.id,
			MAX(CASE WHEN icv.name = 'username' THEN icv.value END) as username,
			MAX(CASE WHEN icv.name = 'url' THEN icv.value END) as url,
			MAX(CASE WHEN icv.name = 'password' THEN icv.value END) as password,
			MAX(CASE WHEN icv.name = 'auth_type' THEN icv.value END) as auth_type,
			MAX(CASE WHEN icv.name = 'projects' THEN icv.value END) as projects
		FROM integrations i
		JOIN integration_config_values icv ON i.id = icv.integration_id
		WHERE i.tenant_id IN (SELECT tenant FROM cloud_accounts WHERE id = $1)
		  AND i.type = 'github'
		  AND i.status = 'enabled'
		GROUP BY i.id
	`

	rows, err := dbms.Db.Queryx(query, accountId)
	if err != nil {
		ctx.GetLogger().Error("unable to query integrations for github config", "error", err)
		return credentials, actualRepo, err
	}
	defer func() {
		if err := rows.Close(); err != nil {
			ctx.GetLogger().Error("loganalysisgithub: unable to close rows", "error", err)
		}
	}()

	for rows.Next() {
		var integrationID string
		var username *string
		var url *string
		var password *string
		var auth_type *string
		var projects *string

		err := rows.Scan(&integrationID, &username, &url, &password, &auth_type, &projects)
		if err != nil {
			ctx.GetLogger().Error("unable to scan integration row", "error", err)
			continue
		}

		// Skip if required fields are missing
		if username == nil || url == nil || password == nil {
			ctx.GetLogger().Warn("skipping integration with missing credentials", "integration_id", integrationID)
			continue
		}

		projectsMap := []map[string]string{}
		if projects != nil && *projects != "" {
			err = common.UnmarshalJson([]byte(*projects), &projectsMap)
			if err != nil {
				ctx.GetLogger().Warn("unable to parse projects JSON", "error", err, "integration_id", integrationID)
				continue
			}
		}

		// currently, for new repos github/jira projects are not getting refreshed causing issue in getting repo data
		// for now disabling this check.. will enable it once we have better way to refresh projects

		// found := false
		// for _, p := range projectsMap {
		// 	if p["key"] == actualRepo {
		// 		found = true
		// 		break
		// 	}
		// }

		// if !found {
		// 	continue
		// }

		decryptedPassword, err := common.Decrypt(*password)
		if err != nil {
			ctx.GetLogger().Error("error decrypting password", "error", err)
			return credentials, actualRepo, common.ErrorInternal("error: unable to process request")
		}

		authType := "token"
		if auth_type != nil {
			authType = *auth_type
		}

		credentials = append(credentials, GitCredentials{
			Username: *username,
			Url:      *url,
			Password: decryptedPassword,
			AuthType: authType,
			Projects: projectsMap,
		})
	}

	return credentials, actualRepo, nil
}
