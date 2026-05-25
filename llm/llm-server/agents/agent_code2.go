package agents

import (
	"bufio"
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/url"
	"nudgebee/llm/agents/core"
	"nudgebee/llm/common"
	"nudgebee/llm/config"
	"nudgebee/llm/security"
	"nudgebee/llm/services_server"
	toolcore "nudgebee/llm/tools/core"
	"nudgebee/llm/utils"
	"nudgebee/llm/workspace"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"time"

	"github.com/tmc/langchaingo/llms"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	kube_labels "k8s.io/apimachinery/pkg/labels"
	kube_watch "k8s.io/apimachinery/pkg/watch"
	kubernetes "k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
)

const AgentCode2 = "agent_code_2"

// Mode constants mirror llm/code-analysis. Kept inline (not imported) because
// llm-server doesn't take a Go-module dependency on llm/code-analysis.
// agent_code_2 only translates the upstream RaisePr flag into the mode field
// it sends to /analyze — it does NOT classify or override the user's intent.
const (
	codeAgentModeExplore = "explore"
	codeAgentModeFix     = "fix"
)

// Environment variable names for passing large payloads to code-analysis-agent
const (
	envCodeAgentLogs   = "CODE_AGENT_LOGS"
	envCodeAgentPrompt = "CODE_AGENT_PROMPT"
)

// irrelevantAnalysisMarker is the phrase emitted by the code-analysis service when
// the analysis is not relevant to the user's query. Must match the output in llm/code-analysis.
const irrelevantAnalysisMarker = "may not be directly addressing your specific issue"

// codeAgentConversationFailures is a cache namespace for tracking conversations
// where agent_code_2 already failed or returned an irrelevant analysis.
// Entries expire after 24 hours to prevent unbounded memory growth.
const codeAgentFailuresCacheNS = "code_agent_conv_failures"

func init() {
	common.CacheCreateNamespace(codeAgentFailuresCacheNS, common.CacheNamespaceWithExpiration(24*time.Hour))

	toolDescription := "Expert AI agent for Deep Code Analysis, Debugging, and Root Cause Analysis (RCA). Correlates logs with source code to find bugs and propose fixes. Requires Git repository access (GitHub or GitLab)."
	toolInput := `Accepts JSON or plain text input.

JSON format (all fields except 'query' are optional):
{
  "query": "Analyze the database insertion error and suggest fixes",
  "errors": ["Error log line 1", "Error log line 2"],
  "git_repo": "https://github.com/owner/repo",
  "git_commit": "abc123def456",
  "target_branch": "prod",
  "namespace": "default",
  "workload": "my-deployment",
  "raise_pr": false
}

Plain text format:
"Analyze why the service is crashing and create a PR to fix it"

The 'query' field (or plain text) is REQUIRED and describes the analytical task. Use this when simple shell commands are insufficient for diagnosing an issue. Set 'target_branch' to the branch the PR should be opened against (e.g. 'prod', 'main', 'release/1.x'); when omitted, the repository default branch is used.`
	toolOutput := "Structured JSON containing: 'root_cause' (summary), 'affected_files' (array with paths/line numbers), 'suggested_fixes' (remediation steps), 'analysis_details' (comprehensive explanation), 'source_details' (repo and commit), and optional 'pr_url' if raise_pr was enabled."

	core.RegisterNBAgentFactoryAndTool(AgentCode2, func(accountId string) (core.NBAgent, error) {
		return newCodeAgent(accountId), nil
	}, toolDescription, toolInput, toolOutput)
}

func evaluateCodeUsingCli(ctx *security.RequestContext, request CodeAgent2Request, creds []GitCredentials, provider string) (string, error) {
	logger := ctx.GetLogger()

	// Validate that the local execution path is configured
	if config.Config.LlmServerCodeAgentLocalExecPath == "" {
		return "", errors.New("LlmServerCodeAgentLocalExecPath is not configured. Set LLM_SERVER_AGENT_CODEAGENT_LOCAL_EXEC_PATH environment variable or switch to pod mode by setting LLM_SERVER_AGENT_CODEAGENT_MODE=remote-cli")
	}

	args := []string{config.Config.LlmServerCodeAgentLocalExecPath, "--analyze"}

	if request.GitRepo != "" {
		args = append(args, "--repo", request.GitRepo)
	}

	if request.EventId != "" {
		args = append(args, "--eventid", request.EventId)
	}

	if request.RecommendationId != "" {
		args = append(args, "--recommendationid", request.RecommendationId)
	}

	if request.AccountId != "" {
		args = append(args, "--accountid", request.AccountId)
	}

	if request.RaisePr {
		args = append(args, "--raisepr", "true")
	}

	env := os.Environ()

	// Pass large payloads via environment variables to avoid ARG_MAX limits
	if len(request.Errors) > 0 {
		env = append(env, envCodeAgentLogs+"="+strings.Join(request.Errors, "\n"))
	}
	if request.Query != "" {
		env = append(env, envCodeAgentPrompt+"="+request.Query)
	}
	// Get Git token based on auth type and provider
	if len(creds) > 0 {
		gitToken := ""
		switch creds[0].AuthType {
		case "token":
			gitToken = creds[0].Password
		case "application":
			// For GitHub App, get installation token (only applicable for GitHub)
			if provider == "github" {
				installationID := int64(0)
				if _, err := fmt.Sscanf(creds[0].Password, "%d", &installationID); err == nil {
					// Use the URL from credentials for GitHub Enterprise support
					apiUrl := creds[0].Url
					token, err := utils.GetGithubAppInstallationToken(ctx.GetContext(), apiUrl, installationID)
					if err == nil {
						gitToken = token
					}
				}
			} else {
				// For GitLab, use password directly as token
				gitToken = creds[0].Password
			}
		}
		if gitToken != "" {
			// Set appropriate environment variable based on provider
			if provider == "gitlab" {
				env = append(env, "GITLAB_TOKEN="+gitToken)
			} else {
				env = append(env, "GITHUB_TOKEN="+gitToken)
			}
		}
	}

	cmd := exec.Command(args[0], args[1:]...)
	cmd.Env = env

	var out bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &stderr

	err := cmd.Run()
	if err != nil {
		return "", errors.New(err.Error() + ": " + stderr.String())
	}
	// --- START: Modified section ---

	// This struct is used to unmarshal *only* the fields we care about
	type logEntry struct {
		LogType string `json:"log_type"`
		Event   string `json:"event"`
	}

	fullOutput := out.String()
	// Save logs to file
	// os.WriteFile("agent_logs.out", []byte(fullOutput), 0644)
	// Scan the output line by line
	scanner := bufio.NewScanner(strings.NewReader(fullOutput))
	var finalAnswerLine string

	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}

		var entry logEntry
		// Try to unmarshal the line into our struct
		if json.Unmarshal(line, &entry) == nil {
			// Log all valid JSON entries continuously
			logger.Debug("code analysis log", "output", string(line))

			// Check if this is our final answer
			if entry.LogType == "RESULT" && entry.Event == "final_answer" {
				finalAnswerLine = string(line)
			}
		} else {
			// Log non-JSON lines as well
			logger.Debug("code analysis output", "output", string(line))
		}
	}

	// If we found a final answer, return it
	if finalAnswerLine != "" {
		return finalAnswerLine, nil
	}

	// If we finished scanning and found no matching line,
	// return the *entire* original stdout output as requested.
	return fullOutput, nil
}

func getKubeClient(qps float32, burst int) (*kubernetes.Clientset, error) {
	// Try to get in-cluster config
	config, err := rest.InClusterConfig()
	if err != nil {
		// Fallback to kubeconfig file
		kubeconfig := os.Getenv("KUBECONFIG")
		if kubeconfig == "" {
			kubeconfig = clientcmd.RecommendedHomeFile
		}
		config, err = clientcmd.BuildConfigFromFlags("", kubeconfig)
		if err != nil {
			return nil, fmt.Errorf("failed to create kubernetes config: %w", err)
		}
	}

	config.QPS = qps
	config.Burst = burst

	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		return nil, fmt.Errorf("failed to create kubernetes client: %w", err)
	}
	return clientset, nil
}

func evaluateCodeUsingPod(ctx *security.RequestContext, agentRequest core.NBAgentRequest, request CodeAgent2Request, creds []GitCredentials, provider string) (string, error) {
	logger := ctx.GetLogger()
	// Default values for pod execution
	namespace := config.Config.LlmServerCodeAgentNamespace
	image := config.Config.LlmServerCodeAgentImage
	qps := float32(100)
	burst := 200

	clientset, err := getKubeClient(qps, burst)
	if err != nil {
		return "", err
	}

	podName := fmt.Sprintf("nb-code-agent-%d", time.Now().UnixNano())

	args := []string{"/app/code-analysis-agent", "--analyze"}

	if request.GitRepo != "" {
		args = append(args, "--repo", request.GitRepo)
	}

	if request.EventId != "" {
		args = append(args, "--eventid", request.EventId)
	}

	if request.RecommendationId != "" {
		args = append(args, "--recommendationid", request.RecommendationId)
	}

	if request.AccountId != "" {
		args = append(args, "--accountid", request.AccountId)
	}

	if request.GitRepo == "" && request.Agent == "" {
		args = append(args, "--agent", "code_agent")
	} else if request.Agent != "" {
		args = append(args, "--agent", request.Agent)
	}

	if request.RaisePr {
		args = append(args, "--raisepr", "true")
	}

	args = append(args, "--conversationid", agentRequest.SessionId)
	envVars := []corev1.EnvVar{}

	// Pass large payloads via environment variables to avoid ARG_MAX limits
	if len(request.Errors) > 0 {
		logs := strings.Join(request.Errors, "\n")
		envVars = append(envVars, corev1.EnvVar{
			Name:  envCodeAgentLogs,
			Value: logs,
		})
	}
	if request.Query != "" {
		envVars = append(envVars, corev1.EnvVar{
			Name:  envCodeAgentPrompt,
			Value: request.Query,
		})
	}

	// Get Git token based on auth type and provider
	if len(creds) > 0 {
		gitToken := ""
		switch creds[0].AuthType {
		case "token":
			gitToken = creds[0].Password
		case "application":
			// For GitHub App, get installation token (only applicable for GitHub)
			if provider == "github" {
				installationID := int64(0)
				if _, err := fmt.Sscanf(creds[0].Password, "%d", &installationID); err == nil {
					// Use the URL from credentials for GitHub Enterprise support
					apiUrl := creds[0].Url
					token, err := utils.GetGithubAppInstallationToken(ctx.GetContext(), apiUrl, installationID)
					if err == nil {
						gitToken = token
					}
				}
			} else {
				// For GitLab, use password directly as token
				gitToken = creds[0].Password
			}
		}
		if gitToken != "" {
			// Set appropriate environment variable based on provider
			tokenEnvName := "GITHUB_TOKEN"
			if provider == "gitlab" {
				tokenEnvName = "GITLAB_TOKEN"
			}
			envVars = append(envVars, corev1.EnvVar{
				Name:  tokenEnvName,
				Value: gitToken,
			})
		}
	}

	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      podName,
			Namespace: namespace,
			Labels: kube_labels.Set{
				"app":             "code-analysis-agent",
				"account_id":      agentRequest.AccountId,
				"conversation_id": agentRequest.ConversationId,
				"message_id":      agentRequest.MessageId,
				"job":             podName,
			},
		},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{
				{
					Name:            "cli-container",
					Image:           image,
					ImagePullPolicy: "Always",
					Command:         args,
					Env:             envVars,
					VolumeMounts: []corev1.VolumeMount{
						{
							Name:      "nudgebee-secret-volume",
							MountPath: "/etc/secrets/nudgebee",
							ReadOnly:  true,
						},
					},
				},
			},
			Volumes: []corev1.Volume{
				{
					Name: "nudgebee-secret-volume",
					VolumeSource: corev1.VolumeSource{
						Secret: &corev1.SecretVolumeSource{
							SecretName: config.Config.LlmServerCodeAgentSecret,
						},
					},
				},
			},
			RestartPolicy: corev1.RestartPolicyNever,
		},
	}

	if config.Config.LlmServerCodeAgentImagePullSecret != "" {
		pod.Spec.ImagePullSecrets = []corev1.LocalObjectReference{
			{Name: config.Config.LlmServerCodeAgentImagePullSecret},
		}
	}

	// Create the pod
	_, err = clientset.CoreV1().Pods(namespace).Create(ctx.GetContext(), pod, metav1.CreateOptions{})
	if err != nil {
		return "", fmt.Errorf("failed to create pod: %w", err)
	}

	// Watch for pod completion
	watcher, err := clientset.CoreV1().Pods(namespace).Watch(ctx.GetContext(), metav1.ListOptions{
		FieldSelector: fmt.Sprintf("metadata.name=%s", podName),
		Watch:         true,
	})
	if err != nil {
		return "", fmt.Errorf("failed to set up pod watcher: %w", err)
	}
	defer watcher.Stop()

	for event := range watcher.ResultChan() {
		if event.Type == kube_watch.Modified || event.Type == kube_watch.Added {
			p, ok := event.Object.(*corev1.Pod)
			if !ok {
				continue
			}
			if p.Status.Phase == corev1.PodSucceeded || p.Status.Phase == corev1.PodFailed {
				break
			}
		}
	}

	// Get logs
	podLogs, err := clientset.CoreV1().Pods(namespace).GetLogs(podName, &corev1.PodLogOptions{}).Stream(ctx.GetContext())
	if err != nil {
		return "", fmt.Errorf("failed to get pod logs: %w", err)
	}
	defer func() {
		if err := podLogs.Close(); err != nil {
			// Log the error, but don't return it as the main result
			logger.Error("error closing pod logs stream", "error", err, "pod", podName)
		}
	}()

	buf := new(bytes.Buffer)
	if _, err := io.Copy(buf, podLogs); err != nil {
		return "", fmt.Errorf("failed to copy pod logs to buffer: %w", err)
	}

	// Determine if the pod should be deleted
	var finalPod *corev1.Pod
	// Re-fetch the pod to get its final status after logs are collected
	finalPod, err = clientset.CoreV1().Pods(namespace).Get(ctx.GetContext(), podName, metav1.GetOptions{})
	if err != nil {
		logger.Error("error getting final pod status", "pod", podName, "error", err)
	} else {
		switch finalPod.Status.Phase {
		case corev1.PodSucceeded:
			// Delete the pod if it succeeded
			deletePolicy := metav1.DeletePropagationBackground
			if err := clientset.CoreV1().Pods(namespace).Delete(ctx.GetContext(), podName, metav1.DeleteOptions{
				PropagationPolicy: &deletePolicy,
			}); err != nil {
				logger.Error("error deleting successful pod", "pod", podName, "error", err)
			}
		case corev1.PodFailed:
			logger.Warn("pod failed and is being kept for debugging", "pod", podName)
		}
	}

	return buf.String(), nil
}

// evaluateCodeUsingWorkspace calls the workspace pod's native /analyze endpoint
// instead of launching a new pod per request.
func evaluateCodeUsingWorkspace(ctx *security.RequestContext, agentRequest core.NBAgentRequest, request CodeAgent2Request, creds []GitCredentials, provider string) (codeAnalysisResult, error) {
	logger := ctx.GetLogger()

	// Resolve git token from credentials
	gitToken := ""
	if len(creds) > 0 {
		switch creds[0].AuthType {
		case "token":
			gitToken = creds[0].Password
		case "application":
			if provider == "github" {
				installationID := int64(0)
				if _, err := fmt.Sscanf(creds[0].Password, "%d", &installationID); err == nil {
					apiUrl := creds[0].Url
					token, err := utils.GetGithubAppInstallationToken(ctx.GetContext(), apiUrl, installationID)
					if err == nil {
						gitToken = token
					}
				}
			} else {
				gitToken = creds[0].Password
			}
		}
	}
	// Fallback to env var when no credentials are provided (e.g. local testing)
	if gitToken == "" {
		if provider == "gitlab" {
			gitToken = os.Getenv("GITLAB_TOKEN")
		} else {
			gitToken = os.Getenv("GITHUB_TOKEN")
		}
	}

	// Build the request body matching the code-analysis server's AgenticAnalyzeRequest
	tenantId := ""
	if ctx.GetSecurityContext() != nil {
		tenantId = ctx.GetSecurityContext().GetTenantId()
	}

	// Fall back to agentRequest.AccountId when the tool input didn't include account_id
	if request.AccountId == "" {
		request.AccountId = agentRequest.AccountId
	}

	workloadName := agentRequest.QueryConfig.Workload
	if workloadName == "" {
		workloadName = request.Workload
	}
	if workloadName == "" {
		workloadName = "unknown"
	}
	workloadNamespace := agentRequest.QueryConfig.Namespace
	if workloadNamespace == "" {
		workloadNamespace = request.Namespace
	}
	if workloadNamespace == "" {
		workloadNamespace = "unknown"
	}

	// When errors are empty (e.g. plain text tool input), use the query as logs
	logs := strings.Join(request.Errors, "\n")
	if logs == "" {
		logs = request.Query
	}

	// Branch to clone from and base the PR against. Sent only when the caller
	// has an actual branch name; otherwise leave it empty so the orchestrator
	// resolves the repo's default branch via `git symbolic-ref refs/remotes/origin/HEAD`.
	// Never fall back to GitCommit here — it's a SHA, not a branch, and passing
	// it through caused `gh pr create --base <SHA>` to fail with "Base ref must be a branch".
	branch := request.TargetBranch

	// Mode is derived from the entrypoint signal (RaisePr). Callers that want a
	// fix+PR set raise_pr=true in their query JSON (e.g. recommendation-apply,
	// event-resolution); chat mentions don't, and stay in explore mode.
	mode := codeAgentModeExplore
	if request.RaisePr {
		mode = codeAgentModeFix
	}

	analyzeRequest := map[string]any{
		"cloud_account_id":   request.AccountId,
		"tenant":             tenantId,
		"workload_name":      workloadName,
		"workload_namespace": workloadNamespace,
		"workload_kind":      "Deployment",
		"logs":               logs,
		"prompt":             request.Query,
		"git_repository": map[string]any{
			"url":      request.GitRepo,
			"branch":   branch,
			"provider": provider,
		},
		"mode":              mode,
		"raise_pr":          request.RaisePr,
		"event_id":          request.EventId,
		"recommendation_id": request.RecommendationId,
		"account_id":        request.AccountId,
		"conversation_id":   agentRequest.SessionId,
		"message_id":        agentRequest.MessageId,
	}

	if request.Agent != "" {
		analyzeRequest["agent_id"] = request.Agent
	}

	// Add git credentials
	if gitToken != "" {
		analyzeRequest["git_credentials"] = map[string]any{
			"type":  "token",
			"token": gitToken,
		}
	}

	// Pre-flight: verify workspace pod is reachable before dispatching analysis
	healthWm := workspace.NewWorkspaceManagerWithTimeout(10 * time.Second)
	if _, healthErr := healthWm.CallAPI(ctx, agentRequest.AccountId, "GET", "/health", nil, nil); healthErr != nil {
		logger.Warn("code: workspace health check failed, attempting recovery", "error", healthErr)
		recoveryWm := workspace.NewWorkspaceManagerWithTimeout(60 * time.Second)
		if _, recoveryErr := recoveryWm.CallAPIOrLazyCreate(ctx, agentRequest.AccountId, "GET", "/health", nil, nil); recoveryErr != nil {
			return codeAnalysisResult{}, fmt.Errorf("workspace pod not healthy after recovery attempt: %w", recoveryErr)
		}
		logger.Info("code: workspace pod recovered successfully")
	}

	// Use workspace manager with short timeout for the initial async POST
	wm := workspace.NewWorkspaceManagerWithTimeout(60 * time.Second)

	logger.Info("code: executing analysis via workspace", "account_id", agentRequest.AccountId, "repo", request.GitRepo, "target_branch", branch)

	// Step 1: POST /analyze — code-analysis returns 202 with analysis_id
	respBytes, err := wm.CallAPIOrLazyCreate(ctx, agentRequest.AccountId, "POST", "/analyze", nil, analyzeRequest)
	if err != nil {
		return codeAnalysisResult{}, fmt.Errorf("workspace /analyze call failed: %w", err)
	}

	var asyncResp map[string]any
	if err := json.Unmarshal(respBytes, &asyncResp); err != nil {
		return codeAnalysisResult{}, fmt.Errorf("workspace /analyze returned invalid JSON: %w", err)
	}

	// Backward compat: if response already has agent_response, it's a sync response
	if _, hasResult := asyncResp["agent_response"]; hasResult {
		return extractAgentResponseWithTokenUsage(respBytes), nil
	}

	if errMsg, _ := asyncResp["error"].(string); errMsg != "" {
		return codeAnalysisResult{}, fmt.Errorf("workspace /analyze failed: %s", errMsg)
	}

	analysisID, _ := asyncResp["analysis_id"].(string)
	status, _ := asyncResp["status"].(string)
	if analysisID == "" || status != "running" {
		return codeAnalysisResult{}, fmt.Errorf("unexpected workspace /analyze response: status=%q analysis_id=%q", status, analysisID)
	}

	// Step 2: Poll /status/{id} every 5s until completed or failed
	logger.Info("code: analysis accepted, polling for progress", "analysis_id", analysisID)
	statusEndpoint := fmt.Sprintf("/status/%s", url.PathEscape(analysisID))
	pollWm := workspace.NewWorkspaceManagerWithTimeout(30 * time.Second)
	lastProgress := ""

	const maxConsecutiveErrors = 12 // 12 * 5s = 60s of consecutive failures before giving up
	const maxPollDuration = 30 * time.Minute
	consecutiveErrors := 0
	pollDeadline := time.Now().Add(maxPollDuration)

	for {
		select {
		case <-ctx.GetContext().Done():
			return codeAnalysisResult{}, fmt.Errorf("analysis timed out while polling for results")
		case <-time.After(5 * time.Second):
		}

		if time.Now().After(pollDeadline) {
			return codeAnalysisResult{}, fmt.Errorf("analysis polling exceeded maximum duration of %v", maxPollDuration)
		}

		statusBytes, err := pollWm.CallAPI(ctx, agentRequest.AccountId, "GET", statusEndpoint, nil, nil)
		if err != nil {
			consecutiveErrors++
			logger.Warn("code: failed to poll analysis status", "error", err, "analysis_id", analysisID,
				"consecutive_errors", consecutiveErrors, "max_consecutive_errors", maxConsecutiveErrors)
			if consecutiveErrors >= maxConsecutiveErrors {
				return codeAnalysisResult{}, fmt.Errorf("analysis polling abandoned after %d consecutive errors: %w", consecutiveErrors, err)
			}
			continue
		}
		consecutiveErrors = 0

		var statusResp map[string]any
		if err := json.Unmarshal(statusBytes, &statusResp); err != nil {
			logger.Warn("code: failed to parse status response", "error", err)
			continue
		}

		// Update progress in DB if changed
		progress, _ := statusResp["progress"].(string)
		if progress != "" && progress != lastProgress {
			lastProgress = progress
			if agentRequest.MessageId != "" {
				core.GetConversationDao().UpdateConversationMessageAsync(
					agentRequest.MessageId, progress, core.ConversationStatusInProgress,
				)
			}
		}

		pollStatus, _ := statusResp["status"].(string)
		switch pollStatus {
		case "completed":
			result, ok := statusResp["result"]
			if !ok {
				return codeAnalysisResult{}, fmt.Errorf("analysis completed but no result returned")
			}
			resultBytes, err := json.Marshal(result)
			if err != nil {
				return codeAnalysisResult{}, fmt.Errorf("failed to marshal analysis result: %w", err)
			}
			logger.Info("code: analysis completed", "analysis_id", analysisID)

			// Fire-and-forget cleanup of cloned repos
			go func() {
				cleanupCtx := security.NewRequestContext(
					context.Background(),
					ctx.GetSecurityContext(),
					ctx.GetLogger(),
					ctx.GetTracer(),
					ctx.GetMeter(),
				)
				cleanupCmd := fmt.Sprintf("rm -rf /tmp/code-analysis-%s-*", agentRequest.SessionId)
				if _, cleanupErr := wm.ExecuteCommand(cleanupCtx, agentRequest.AccountId, "", cleanupCmd, nil); cleanupErr != nil {
					logger.Warn("code: workspace cleanup failed", "error", cleanupErr)
				}
			}()

			return extractAgentResponseWithTokenUsage(resultBytes), nil
		case "failed":
			errMsg, _ := statusResp["error"].(string)
			return codeAnalysisResult{}, fmt.Errorf("analysis failed: %s", errMsg)
		}
		// status == "running" → keep polling
	}
}

// codeAnalysisTokenUsage holds token usage data extracted from the code-analysis service response.
type codeAnalysisTokenUsage struct {
	PromptTokens        int
	CompletionTokens    int
	TotalTokens         int
	CachedContentTokens int
	CacheCreationTokens int // Anthropic cache_creation tokens / Gemini new-cache write — billed at provider creation rate
	ThinkingTokens      int // Gemini ThoughtsTokenCount — billed at output rate, otherwise silently $0
	Model               string
	Provider            string
}

// codeAnalysisResult bundles the agent response with optional token usage data.
type codeAnalysisResult struct {
	AgentResponse string
	TokenUsage    *codeAnalysisTokenUsage
}

// parseTokenUsageMap extracts token usage fields from a map[string]any.
func parseTokenUsageMap(tuRaw map[string]any) *codeAnalysisTokenUsage {
	if tuRaw == nil {
		return nil
	}
	tu := &codeAnalysisTokenUsage{}
	if v, ok := tuRaw["prompt_tokens"].(float64); ok {
		tu.PromptTokens = int(v)
	}
	if v, ok := tuRaw["completion_tokens"].(float64); ok {
		tu.CompletionTokens = int(v)
	}
	if v, ok := tuRaw["total_tokens"].(float64); ok {
		tu.TotalTokens = int(v)
	}
	if v, ok := tuRaw["cached_content_tokens"].(float64); ok {
		tu.CachedContentTokens = int(v)
	}
	if v, ok := tuRaw["cache_creation_tokens"].(float64); ok {
		tu.CacheCreationTokens = int(v)
	}
	// Accept both naming conventions: `thinking_tokens` (our DB column) and
	// `thoughts_token_count` (Gemini SDK field name) so the Python service
	// can emit either as it migrates.
	if v, ok := tuRaw["thinking_tokens"].(float64); ok {
		tu.ThinkingTokens = int(v)
	} else if v, ok := tuRaw["thoughts_token_count"].(float64); ok {
		tu.ThinkingTokens = int(v)
	}
	if v, ok := tuRaw["model"].(string); ok {
		tu.Model = v
	}
	if v, ok := tuRaw["provider"].(string); ok {
		tu.Provider = v
	}
	return tu
}

// codeAnalysisThinkingModelPattern matches Gemini families that produce
// thinking tokens. Used to warn when the code-analysis Python service hasn't
// started emitting `thinking_tokens` for a thinking-class model — silently
// undercounts cost otherwise.
func isCodeAnalysisThinkingModel(model string) bool {
	if model == "" {
		return false
	}
	m := strings.ToLower(model)
	return strings.HasPrefix(m, "gemini-2.5") || strings.HasPrefix(m, "gemini-3")
}

// extractAgentResponseWithTokenUsage extracts agent_response and token_usage from a
// code-analysis response. Falls back to the raw response if parsing fails.
func extractAgentResponseWithTokenUsage(respBytes []byte) codeAnalysisResult {
	var resp map[string]any
	if err := json.Unmarshal(respBytes, &resp); err != nil {
		return codeAnalysisResult{AgentResponse: string(respBytes)}
	}

	tuRaw, _ := resp["token_usage"].(map[string]any)
	tu := parseTokenUsageMap(tuRaw)

	// Extract agent_response
	agentResp := string(respBytes)
	if agentResponse, ok := resp["agent_response"]; ok && agentResponse != nil {
		if responseBytes, err := json.Marshal(agentResponse); err == nil {
			agentResp = string(responseBytes)
		}
	}

	return codeAnalysisResult{AgentResponse: agentResp, TokenUsage: tu}
}

// recordCodeAnalysisTokenUsage inserts a token usage record for code-analysis work.
func recordCodeAnalysisTokenUsage(query core.NBAgentRequest, tu *codeAnalysisTokenUsage, latency float64) {
	if tu == nil || tu.TotalTokens == 0 {
		return
	}

	provider := tu.Provider
	if provider == "" {
		provider = config.Config.LlmProvider
	}
	model := tu.Model
	if model == "" {
		model = config.Config.LlmModel
	}

	var agentUUID *string
	if query.AgentId != "" {
		agentUUID = &query.AgentId
	}

	latencyPtr := &latency
	// Defensive: if the model is in the thinking class but the Python service
	// didn't emit `thinking_tokens`, cost will silently undercount by
	// output_rate × thinking_tokens. Warn so the gap is visible while the
	// cross-service emission catches up (#30262 sub-item).
	if tu.ThinkingTokens == 0 && isCodeAnalysisThinkingModel(model) && tu.CompletionTokens > 0 {
		slog.Warn("code: code-analysis response missing thinking_tokens for thinking-class model; cost will undercount",
			"model", model,
			"provider", provider,
			"prompt_tokens", tu.PromptTokens,
			"completion_tokens", tu.CompletionTokens,
			"conversation_id", query.ConversationId)
	}
	// cache_ttl_minutes is no longer written — see trackTokenUsage rationale.
	// Storage cost lives in llm_cache_lifecycle; per-call rows hold per-token
	// costs only.
	record := &core.TokenUsageRecord{
		ConversationID:      query.ConversationId,
		MessageID:           query.MessageId,
		AgentID:             agentUUID,
		AgentName:           AgentCode2,
		AccountID:           query.AccountId,
		UserID:              query.UserId,
		LLMProvider:         provider,
		LLMModel:            model,
		InputTokens:         tu.PromptTokens,
		OutputTokens:        tu.CompletionTokens,
		CachedInputTokens:   tu.CachedContentTokens,
		CacheCreationTokens: tu.CacheCreationTokens,
		IsCacheHit:          tu.CachedContentTokens > 0,
		LatencySeconds:      latencyPtr,
		RequestStatus:       "success",
	}
	// Thinking tokens stored only when non-zero — distinguishes "model didn't
	// think" from "service didn't emit it". Mirrors trackTokenUsage:2159-2162.
	if tu.ThinkingTokens > 0 {
		tt := tu.ThinkingTokens
		record.ThinkingTokens = &tt
	}

	if err := core.GetConversationDao().InsertTokenUsage(record); err != nil {
		slog.Error("code: failed to insert token usage",
			"error", err,
			"conversation_id", query.ConversationId,
			"message_id", query.MessageId,
			"account_id", query.AccountId,
		)
	}
}

func newCodeAgent(accountId string) CodeAgent2 {
	return CodeAgent2{
		accountId: accountId,
	}

}

// based on error logs, generate diff if possible
// or provide code-based analysis
type CodeAgent2 struct {
	accountId string
}

func (l CodeAgent2) GetName() string {
	return AgentCode2
}

func (l CodeAgent2) GetNameAliases() []string {
	return []string{"code_analyzer", "code_debugger", "code_error_analyzer", "code_rca_agent"}
}

func (l CodeAgent2) GetDescription() string {
	desc := "Expert AI agent for Deep Code Analysis, Debugging, and Root Cause Analysis (RCA).\n" +
		"Use this agent when the user asks to:\n" +
		"* Debug errors or find the root cause of an issue.\n" +
		"* Analyze service failures by correlating logs with source code.\n" +
		"* Identify bugs and propose code fixes or create Pull Requests (PRs)."

	if config.Config.LlmServerShellToolEnabled {
		desc += "\n\n**Do NOT use for:**\n" +
			"* Simple file lookups or running basic shell commands (use 'shell_execute').\n" +
			"* Checking network connectivity or infrastructure state (use 'shell_execute')."
	}

	return desc
}

func (l CodeAgent2) GetSupportedTools(ctx *security.RequestContext) []toolcore.NBTool {
	return []toolcore.NBTool{}
}

func (l CodeAgent2) GetSystemPrompt(ctx *security.RequestContext, query core.NBAgentRequest) core.NBAgentPrompt {

	instructions := []string{
		"Your primary goal is to analyze error logs and correlate them with source code to identify root causes and debug issues.",
		"You execute a sophisticated multi-agent system (orchestrator, router, and specialist agents) that uses ReAct planning to analyze repositories.",
		"The analysis includes: cloning the repository, searching for relevant code, correlating logs with code patterns, identifying root causes, and optionally proposing fixes.",
		"If 'raise_pr' is enabled, the system will automatically create a pull request (GitHub) or merge request (GitLab) with the proposed fixes after review.",
		"You have access to Git repositories (GitHub and GitLab) and can analyze code across multiple languages and frameworks.",
		"Provide structured analysis results including: root cause summary, affected files/lines, suggested fixes, and reproduction steps if available.",
	}
	constraints := []string{
		"Requires Git repository access via configured credentials (token or GitHub/GitLab App).",
		"Analysis is performed by spawning either a CLI process (local mode) or Kubernetes pod (cluster mode).",
		"Can automatically detect Git repository from Kubernetes workload annotations if not explicitly provided.",
		"Respect repository size limits and analysis timeouts configured in the system.",
		"Only create PRs/MRs when explicitly requested via 'raise_pr' flag or when the feature is enabled for the tenant.",
	}
	examples := []core.NBAgentPromptExample{}
	return core.NBAgentPrompt{
		Role:         "an expert Root Cause Analysis and Debugging Assistant with deep knowledge of software engineering and error diagnosis",
		Instructions: instructions,
		Constraints:  constraints,
		Examples:     examples,
		OutputFormat: "Structured JSON with analysis results, root causes, affected code locations, and optional PR details",
		Variables:    []string{"query", "errors", "git_repo", "git_commit", "target_branch", "event_id"},
	}
}

type CodeAgent2Request struct {
	Query            string           `json:"query" validate:"required"`
	Errors           []string         `json:"errors"`
	Files            []map[string]any `json:"files"`
	GitRepo          string           `json:"git_repo"`      // Accepts GitHub or GitLab URLs (JSON key kept for backward compatibility)
	GitCommit        string           `json:"git_commit"`    // Git commit hash (JSON key kept for backward compatibility)
	TargetBranch     string           `json:"target_branch"` // Base branch for the PR (e.g. "prod", "main"). Empty → repo default branch.
	Agent            string           `json:"agent"`
	RaisePr          bool             `json:"raise_pr"`
	EventId          string           `json:"event_id"`
	RecommendationId string           `json:"recommendation_id"`
	AccountId        string           `json:"account_id"`
	Namespace        string           `json:"namespace"`
	Workload         string           `json:"workload"`

	// PR followup fields — used when re-executing to address CI failures or review comments
	Followup bool   `json:"followup"`
	PRURL    string `json:"pr_url"`
	PRBranch string `json:"pr_branch"`
}

func (l CodeAgent2) GetPlannerType() core.AgentPlannerType {
	return core.AgentPlannerTypeCustom
}

func (l CodeAgent2) Execute(ctx *security.RequestContext, query core.NBAgentRequest) (core.NBAgentResponse, error) {
	// Message-scoped retry guard: if a previous call in this message already failed
	// or returned an irrelevant analysis, skip immediately to avoid wasting tokens/time.
	// Scoped to message (not conversation) so new user messages can retry with better input.
	guardKey := query.ConversationId + ":" + query.MessageId
	if prevReason, ok := common.CacheGet(codeAgentFailuresCacheNS, guardKey); ok {
		reason := string(prevReason)
		ctx.GetLogger().Info("code: skipping — previous analysis in this message was not useful",
			"conversation_id", query.ConversationId, "message_id", query.MessageId, "reason", reason)
		return core.NBAgentResponse{}, fmt.Errorf("code analysis already attempted in this message: %s", reason)
	}

	codeAgentRequest := CodeAgent2Request{}
	err := common.UnmarshalJson([]byte(query.Query), &codeAgentRequest)
	if err != nil {
		// query is not a valid json, pass query directly for analysis
		ctx.GetLogger().Info("code: query is not valid JSON, using as plain text", "unmarshal_error", err, "query_length", len(query.Query))
		codeAgentRequest.Query = query.Query
	} else if codeAgentRequest.Query == "" {
		// JSON unmarshal succeeded but Query field is empty, use the original query text
		ctx.GetLogger().Info("code: JSON unmarshal succeeded but Query field is empty, using original query", "query_length", len(query.Query))
		codeAgentRequest.Query = query.Query
	}

	// Final check: ensure Query field is not empty after all fallback logic
	if codeAgentRequest.Query == "" {
		ctx.GetLogger().Error("code: Query field is required but empty after all processing",
			"original_query_length", len(query.Query),
			"has_errors", len(codeAgentRequest.Errors) > 0,
			"has_github_repo", codeAgentRequest.GitRepo != "",
			"unmarshal_error", err)
		return core.NBAgentResponse{}, errors.New("query is required: provide either a 'query' field in JSON input or a plain text description of the analysis task")
	}

	err = common.ValidateStruct(codeAgentRequest)
	if err != nil {
		ctx.GetLogger().Error("code: validation failed", "error", err, "query_length", len(codeAgentRequest.Query), "has_errors", len(codeAgentRequest.Errors) > 0)
		return core.NBAgentResponse{}, err
	}

	// PR followup mode — skip repo detection and use workspace /analyze with followup fields
	if codeAgentRequest.Followup && codeAgentRequest.PRURL != "" {
		ctx.GetLogger().Info("code: PR followup mode", "pr_url", codeAgentRequest.PRURL, "pr_branch", codeAgentRequest.PRBranch)
		return l.executeFollowup(ctx, query, codeAgentRequest)
	}

	// Extract event_id, recommendation_id, account_id, and git_repo from QueryConfig
	eventId := query.QueryConfig.EventId
	recommendationId := query.QueryConfig.RecommendationId
	accountId := query.QueryConfig.AccountId

	// RaisePr is intentionally NOT hardcoded here. The entrypoint that built
	// the request (recommendation-apply, event resolution, PR followup, frontend
	// chat) is the source of truth — agent_code_2 must pass it through unchanged.
	// Hardcoding `RaisePr = true` here was the cause of PR #29338, where a
	// pure exploration question ("what is the default Postgres connection
	// limit?") got promoted to fix-mode and produced a spurious PR.

	// Check if user selected a repo via followup response
	if codeAgentRequest.GitRepo == "" && query.QueryConfig.ToolConfigs != nil {
		if selectedRepo, ok := query.QueryConfig.ToolConfigs["git_repo"]; ok && selectedRepo != "" {
			codeAgentRequest.GitRepo = selectedRepo
			ctx.GetLogger().Info("code: using git_repo from followup selection", "repo", selectedRepo)
		}
	}

	// Check Config for explicit git_repo before auto-detecting
	if codeAgentRequest.GitRepo == "" && query.QueryConfig.GitRepo != "" {
		codeAgentRequest.GitRepo = query.QueryConfig.GitRepo
		ctx.GetLogger().Info("code: using git_repo from request config", "repo", query.QueryConfig.GitRepo)
	}

	// When input is plain text, try to extract a git URL embedded in the text
	if codeAgentRequest.GitRepo == "" {
		if repoURL := extractGitURLFromText(codeAgentRequest.Query); repoURL != "" {
			codeAgentRequest.GitRepo = repoURL
			ctx.GetLogger().Info("code: extracted git_repo from plain text input", "repo", repoURL)
		}
	}

	// Try to detect GitHub repo if not provided
	if codeAgentRequest.GitRepo == "" {
		ctx.GetLogger().Info("code: git_repo not provided, attempting to detect from annotations and ArgoCD")

		// Extract k8s info from QueryConfig first (higher priority)
		var k8sInfoList []map[string]string
		var namespace, workloadName string

		if query.QueryConfig.Namespace != "" && query.QueryConfig.Workload != "" {
			namespace = query.QueryConfig.Namespace
			workloadName = query.QueryConfig.Workload
			k8sInfoList = append(k8sInfoList, map[string]string{
				"pod_name":      "",
				"namespace":     namespace,
				"workload_name": workloadName,
			})
		} else if codeAgentRequest.Namespace != "" && codeAgentRequest.Workload != "" {
			// Fallback: use namespace/workload from tool input JSON when QueryConfig is empty
			// (QueryConfig comes from the original user request and is empty when agent_code_2 is invoked as a tool)
			namespace = codeAgentRequest.Namespace
			workloadName = codeAgentRequest.Workload
			k8sInfoList = append(k8sInfoList, map[string]string{
				"pod_name":      "",
				"namespace":     namespace,
				"workload_name": workloadName,
			})
			ctx.GetLogger().Info("code: using namespace/workload from tool input", "namespace", namespace, "workload", workloadName)
		}

		// If no k8s info from QueryConfig, try to extract from query using LLM
		if len(k8sInfoList) == 0 && codeAgentRequest.Query != "" {
			agentId := query.ParentAgentId
			k8sInfoList, err = l.extractK8sInfo(ctx, query.AccountId, query.ConversationId, query.MessageId, agentId, codeAgentRequest.Query, query.UserId)
			if err != nil {
				ctx.GetLogger().Error("code: failed to extract k8s info", "error", err.Error())
			}
			// Extract first k8s info for ArgoCD detection
			if len(k8sInfoList) > 0 {
				namespace = k8sInfoList[0]["namespace"]
				workloadName = k8sInfoList[0]["workload_name"]
			}
		}

		// Strategy 1: Try GetSourceCodeRepo (includes both Nudgebee annotations AND ArgoCD detection)
		if (namespace != "" && workloadName != "") || eventId != "" {
			ctx.GetLogger().Info("code: attempting source code detection via GetSourceCodeRepo",
				"namespace", namespace, "workload", workloadName, "eventId", eventId)

			sourceCodeInfo := services_server.GetSourceCodeRepo(ctx, query.AccountId, services_server.SourceCodeAnnotationOptions{
				EventId:      eventId,
				WorkloadName: workloadName,
				Namespace:    namespace,
			})

			// Check if we got repo info from any source (Nudgebee annotations, ArgoCD, or both)
			if sourceCodeInfo.CodeRepo != "" {
				codeAgentRequest.GitRepo = sourceCodeInfo.CodeRepo
				ctx.GetLogger().Info("code: detected git repo from Nudgebee annotations", "repo", sourceCodeInfo.CodeRepo, "provider", detectGitProvider(sourceCodeInfo.CodeRepo))

				if sourceCodeInfo.CodeRepoCommitHash != "" {
					codeAgentRequest.GitCommit = sourceCodeInfo.CodeRepoCommitHash
				}
			} else if sourceCodeInfo.ValuesRepoURL != "" {
				// If CodeRepo is empty but we have ArgoCD values repo, use that
				codeAgentRequest.GitRepo = sourceCodeInfo.ValuesRepoURL
				ctx.GetLogger().Info("code: detected git repo from ArgoCD values repo", "repo", sourceCodeInfo.ValuesRepoURL, "argocd_app", sourceCodeInfo.ArgoCDApp, "provider", detectGitProvider(sourceCodeInfo.ValuesRepoURL))

				// Store additional ArgoCD metadata for context
				if sourceCodeInfo.TargetRevision != "" {
					ctx.GetLogger().Info("code: ArgoCD target revision", "revision", sourceCodeInfo.TargetRevision)
				}
				if len(sourceCodeInfo.ValuesFiles) > 0 {
					ctx.GetLogger().Info("code: ArgoCD values files", "files", sourceCodeInfo.ValuesFiles, "path", sourceCodeInfo.ValuesPath)
				}
			}

			// Enhance the query with Helm values file context if available
			if len(sourceCodeInfo.ValuesFiles) > 0 || sourceCodeInfo.HelmChartName != "" {
				var contextParts []string

				// Add Helm chart information
				if sourceCodeInfo.HelmChartName != "" {
					contextParts = append(contextParts, fmt.Sprintf("This workload is deployed using Helm chart '%s' from '%s'.",
						sourceCodeInfo.HelmChartName, sourceCodeInfo.HelmChartRepo))
					ctx.GetLogger().Info("code: Helm chart detected",
						"chart_repo", sourceCodeInfo.HelmChartRepo,
						"chart_name", sourceCodeInfo.HelmChartName,
						"release_name", sourceCodeInfo.HelmReleaseName)
				}

				// Add values file information
				if len(sourceCodeInfo.ValuesFiles) > 0 {
					valuesFilePaths := make([]string, len(sourceCodeInfo.ValuesFiles))
					for i, vf := range sourceCodeInfo.ValuesFiles {
						// Extract filename from $values/path/to/file.yaml format
						vf = strings.TrimPrefix(vf, "$values/")
						if sourceCodeInfo.ValuesPath != "" {
							valuesFilePaths[i] = sourceCodeInfo.ValuesPath + "/" + vf
						} else {
							valuesFilePaths[i] = vf
						}
					}

					branchInfo := ""
					if sourceCodeInfo.TargetRevision != "" {
						branchInfo = fmt.Sprintf(" (branch: %s)", sourceCodeInfo.TargetRevision)
					}

					contextParts = append(contextParts, fmt.Sprintf("Configuration values are in: %s from repository %s%s.",
						strings.Join(valuesFilePaths, ", "),
						sourceCodeInfo.ValuesRepoURL,
						branchInfo))
				}

				// Append context to the query
				if len(contextParts) > 0 {
					var builder strings.Builder
					builder.WriteString(codeAgentRequest.Query)
					builder.WriteString("\n\nDeployment Configuration:\n")
					builder.WriteString(strings.Join(contextParts, " "))
					codeAgentRequest.Query = builder.String()
					ctx.GetLogger().Info("code: enhanced query with deployment context")
				}
			}
		}

		// Strategy 2: Fallback to old method (GetSourceCodeAnnotations) if GetSourceCodeRepo didn't find anything
		if codeAgentRequest.GitRepo == "" {
			ctx.GetLogger().Info("code: falling back to direct annotation lookup")
			meta, err := l.GetSourceCodeAnnotations(ctx, query, k8sInfoList, eventId)
			if err != nil {
				ctx.GetLogger().Info("code: unable to get source code annotations", "error", err)
			}

			// Extract GitHub repo from annotations
			if meta != nil {
				// Try workloads.nudgebee.com prefix first
				if repo, exists := meta["workloads.nudgebee.com/git.repo"]; exists && repo != "" {
					codeAgentRequest.GitRepo = repo
					ctx.GetLogger().Info("code: detected git repo from fallback annotations", "repo", repo, "provider", detectGitProvider(repo))

					if commit, exists := meta["workloads.nudgebee.com/git.hash"]; exists {
						codeAgentRequest.GitCommit = commit
					}
				} else if repo, exists := meta["ci.nudgebee.com/git.repo"]; exists && repo != "" {
					// Fallback to ci.nudgebee.com prefix
					codeAgentRequest.GitRepo = repo
					ctx.GetLogger().Info("code: detected git repo from ci annotations", "repo", repo, "provider", detectGitProvider(repo))

					if commit, exists := meta["ci.nudgebee.com/git.hash"]; exists {
						codeAgentRequest.GitCommit = commit
					}
				}
			}
		}

		// Strategy 3: Final fallback - Try to extract Git repo from user question using LLM
		if codeAgentRequest.GitRepo == "" && codeAgentRequest.Query != "" {
			ctx.GetLogger().Info("code: attempting to extract git repo from user question")
			agentId := query.ParentAgentId
			extractedRepo, _, err := l.extractGitRepoFromQuery(ctx, query.AccountId, query.ConversationId, query.MessageId, agentId, codeAgentRequest.Query, query.UserId)
			if err != nil {
				ctx.GetLogger().Error("code: failed to extract git repo from query", "error", err.Error())
			} else if extractedRepo != "" && isValidGitURL(extractedRepo) {
				codeAgentRequest.GitRepo = extractedRepo
				ctx.GetLogger().Info("code: extracted git repo from LLM query extraction", "repo", extractedRepo)
			} else if extractedRepo != "" {
				ctx.GetLogger().Warn("code: LLM extracted repo is not a valid git URL, discarding", "extracted", extractedRepo)
			}
		}
	}

	// Assign extracted IDs to request
	codeAgentRequest.EventId = eventId
	codeAgentRequest.RecommendationId = recommendationId
	codeAgentRequest.AccountId = accountId

	var creds []GitCredentials
	creds, repoUrl, provider, err := l.getGitCredentials(ctx, codeAgentRequest.GitRepo, query.AccountId)

	if codeAgentRequest.GitRepo == "" && repoUrl != "" {
		codeAgentRequest.GitRepo = repoUrl
	}
	if err != nil {
		ctx.GetLogger().Error("code: unable to get git creds", "error", err)
		return core.NBAgentResponse{}, err
	}
	if len(creds) == 0 {
		return core.NBAgentResponse{}, errors.New("git credentials are required but none were found for github or gitlab")
	}

	// If repo is still unknown, check if there are multiple projects to ask the user
	if codeAgentRequest.GitRepo == "" {
		var allProjectURLs []string
		for _, cred := range creds {
			for _, project := range cred.Projects {
				if repoURL := resolveProjectRepoURL(project, cred); repoURL != "" {
					allProjectURLs = append(allProjectURLs, repoURL)
				}
			}
		}
		if len(allProjectURLs) > 1 {
			// Try auto-detection: fuzzy match workload name against project URLs
			detectedWorkload := codeAgentRequest.Workload
			if detectedWorkload == "" {
				detectedWorkload = query.QueryConfig.Workload
			}
			if matched := fuzzyMatchRepo(detectedWorkload, allProjectURLs); matched != "" {
				ctx.GetLogger().Info("code: auto-resolved repository from workload name",
					"workload", detectedWorkload, "matched_repo", matched, "candidates", len(allProjectURLs))
				codeAgentRequest.GitRepo = matched
			}

			if codeAgentRequest.GitRepo == "" {
				if matched := l.selectRepoFromConversationContext(ctx, query, codeAgentRequest.Query, allProjectURLs); matched != "" {
					codeAgentRequest.GitRepo = matched
				}
			}

			// If auto-detection failed, ask the user via followup
			if codeAgentRequest.GitRepo == "" {
				ctx.GetLogger().Info("code: asking user to select repository", "count", len(allProjectURLs))
				return core.NBAgentResponse{
					Response: []string{"I found multiple repositories in your git integration. Which repository should I analyze?"},
					Status:   core.ConversationStatusWaiting,
					FollowupRequest: core.FollowupRequest{
						Question:        "Which repository should I analyze?",
						FollowupType:    core.FollowupTypeToolConfig,
						FollowupOptions: allProjectURLs,
						AgentName:       l.GetName(),
						ToolName:        "git_repo",
					},
				}, nil
			}

			// Repo was resolved from the candidate list (fuzzy or context LLM) — refresh
			// creds/repoUrl/provider so downstream uses provider-aware credentials and
			// logging instead of the stale empty-repo values from the first call.
			refreshedCreds, refreshedRepoUrl, refreshedProvider, refreshErr := l.getGitCredentials(ctx, codeAgentRequest.GitRepo, query.AccountId)
			if refreshErr != nil {
				ctx.GetLogger().Error("code: unable to refresh git creds after repo auto-resolution", "error", refreshErr, "repo", codeAgentRequest.GitRepo)
				return core.NBAgentResponse{}, refreshErr
			}
			if len(refreshedCreds) == 0 {
				return core.NBAgentResponse{}, errors.New("git credentials are required but none were found for the auto-resolved repository")
			}
			creds = refreshedCreds
			repoUrl = refreshedRepoUrl
			provider = refreshedProvider
		}
	}

	ctx.GetLogger().Info("code: using git provider", "provider", provider, "repo", repoUrl)
	finalOutput := ""

	if config.Config.LlmServerWorkspaceEnabled {
		// execute via workspace /analyze endpoint
		startTime := time.Now()
		wsResult, err := evaluateCodeUsingWorkspace(ctx, query, codeAgentRequest, creds, provider)
		latency := time.Since(startTime).Seconds()
		if err != nil {
			ctx.GetLogger().Error("code: failed to execute via workspace", "error", err.Error())
			return core.NBAgentResponse{}, err
		}
		ctx.GetLogger().Info("Workspace /analyze Output", "output_length", len(wsResult.AgentResponse))

		// Record token usage from code-analysis service (fire-and-forget)
		go recordCodeAnalysisTokenUsage(query, wsResult.TokenUsage, latency)

		// workspace path returns agent_response directly — parse and enrich
		var actualResponse map[string]any
		if err := json.Unmarshal([]byte(wsResult.AgentResponse), &actualResponse); err != nil {
			return core.NBAgentResponse{
				Response: []string{wsResult.AgentResponse},
			}, nil
		}
		actualResponse["source_details"] = map[string]any{
			"workloads.nudgebee.com/git.hash": codeAgentRequest.GitCommit,
			"workloads.nudgebee.com/git.repo": codeAgentRequest.GitRepo,
		}
		jsonResponse, err := json.Marshal(actualResponse)
		if err != nil {
			return core.NBAgentResponse{}, err
		}
		responseStr := string(jsonResponse)
		handleAnalysisResult(ctx, query.ConversationId, query.MessageId, responseStr)
		go trackPRInResolution(ctx, query, responseStr, codeAgentRequest.GitRepo, provider)
		return core.NBAgentResponse{
			Response: []string{responseStr},
		}, nil
	} else if config.Config.LlmServerCodeAgentMode == "local" {
		// execute command for local testing
		cliOutput, err := evaluateCodeUsingCli(ctx, codeAgentRequest, creds, provider)
		if err != nil {
			ctx.GetLogger().Error("code: failed to analyze request", "error", err)
			return core.NBAgentResponse{}, err
		}
		ctx.GetLogger().Info("CLI Command Output", "output", cliOutput)
		finalOutput = cliOutput
	} else {
		// execute command using pod
		podOutput, err := evaluateCodeUsingPod(ctx, query, codeAgentRequest, creds, provider)
		if err != nil {
			ctx.GetLogger().Error("code: failed to execute CLI command in pod", "error", err.Error())
			return core.NBAgentResponse{}, err
		}
		for output := range strings.SplitSeq(podOutput, "\n") {
			if !strings.Contains(output, `"event":"final_answer"`) {
				continue
			}
			podOutput = output
			break
		}

		ctx.GetLogger().Info("CLI Command Output from Pod", "output", podOutput)
		finalOutput = podOutput
	}

	var actualResponse map[string]any
	var cliTokenUsage *codeAnalysisTokenUsage
	logAnalysisResponse := map[string]any{}
	err = json.Unmarshal([]byte(finalOutput), &logAnalysisResponse)
	if err != nil {
		// If unmarshaling fails, return the raw output as a message
		return core.NBAgentResponse{
			Response: []string{finalOutput},
		}, nil
	}
	if data, ok := logAnalysisResponse["data"].(map[string]any); ok {
		if result, ok := data["result"].(map[string]any); ok {
			if agentResponse, ok := result["agent_response"].(map[string]any); ok {
				actualResponse = agentResponse
			} else if analysisResult, ok := result["analysis_result"].(map[string]any); ok {
				actualResponse = analysisResult
			}
			// Extract token usage from CLI/pod response
			if tuRaw, ok := result["token_usage"].(map[string]any); ok {
				cliTokenUsage = parseTokenUsageMap(tuRaw)
			}
		}
	}
	// Record token usage from CLI/pod path (fire-and-forget)
	go recordCodeAnalysisTokenUsage(query, cliTokenUsage, 0)
	if actualResponse == nil {
		// If we couldn't find the expected structure, return the whole parsed response
		return core.NBAgentResponse{
			Response: []string{finalOutput},
		}, nil
	}

	actualResponse["source_details"] = map[string]any{
		"workloads.nudgebee.com/git.hash": codeAgentRequest.GitCommit,
		"workloads.nudgebee.com/git.repo": codeAgentRequest.GitRepo,
	}
	jsonResponse, err := json.Marshal(actualResponse)
	if err != nil {
		return core.NBAgentResponse{}, err
	}
	responseStr := string(jsonResponse)
	handleAnalysisResult(ctx, query.ConversationId, query.MessageId, responseStr)
	go trackPRInResolution(ctx, query, responseStr, codeAgentRequest.GitRepo, provider)
	return core.NBAgentResponse{
		Response: []string{responseStr},
	}, nil
}

// handleAnalysisResult stores irrelevant results in the cache so retries within
// the same message are skipped, or clears previous failures on success.
func handleAnalysisResult(ctx *security.RequestContext, conversationId, messageId, responseStr string) {
	guardKey := conversationId + ":" + messageId
	if isIrrelevantAnalysis(responseStr) {
		ctx.GetLogger().Info("code: analysis was not relevant to user query, storing for retry guard",
			"conversation_id", conversationId, "message_id", messageId)
		_ = common.CacheSet(codeAgentFailuresCacheNS, guardKey,
			[]byte("analysis was not relevant to the user's issue"))
	} else {
		// Genuine success — clear any previous failure so the same message can recover
		_ = common.CacheDelete(codeAgentFailuresCacheNS, guardKey)
	}
}

// isIrrelevantAnalysis checks if the code-analysis response indicates the analysis
// was not relevant to the user's query. This is determined by the code-analysis
// service's relevance check (not a failure — the analysis ran but was off-topic).
// NOTE: The marker phrase must match the relevance check output in llm/code-analysis.
func isIrrelevantAnalysis(response string) bool {
	return strings.Contains(response, irrelevantAnalysisMarker)
}

// trackPRInResolution inserts an event_resolution row when agent_code_2 creates a PR.
// This enables the pr-lifecycle-check cron to detect the PR and trigger automated
// follow-up for CI failures and review comments. Runs as fire-and-forget.
func trackPRInResolution(ctx *security.RequestContext, query core.NBAgentRequest, responseStr string, gitRepo string, provider string) {
	defer func() {
		if r := recover(); r != nil {
			slog.Error("code: panic in trackPRInResolution", "recover", r)
		}
	}()

	var response map[string]any
	if err := json.Unmarshal([]byte(responseStr), &response); err != nil {
		return
	}

	prInfoRaw, ok := response["automated_fix_pr_info"]
	if !ok || prInfoRaw == nil {
		return
	}
	prMap, ok := prInfoRaw.(map[string]any)
	if !ok {
		return
	}

	prURL, _ := prMap["url"].(string)
	if prURL == "" {
		return
	}

	prNumber, _ := prMap["number"].(float64)
	branch, _ := prMap["branch"].(string)

	// Parse org/repo from git URL (e.g. "https://github.com/nudgebee/nudgebee-infra" → "nudgebee", "nudgebee-infra")
	org, repo := parseOrgRepo(gitRepo)

	tenantId := ""
	if ctx.GetSecurityContext() != nil {
		tenantId = ctx.GetSecurityContext().GetTenantId()
	}

	// Build metadata matching prMetadata struct in api-server/services/account/adapter/pr_lifecycle.go
	metadata := map[string]any{
		"pr_url":     prURL,
		"pr_number":  int(prNumber),
		"repo_url":   gitRepo,
		"branch":     branch,
		"pr_branch":  branch,
		"provider":   provider,
		"org":        org,
		"repo":       repo,
		"tenant_id":  tenantId,
		"account_id": query.AccountId,
	}
	metadataJSON, err := json.Marshal(metadata)
	if err != nil {
		ctx.GetLogger().Error("code: failed to marshal PR metadata for resolution", "error", err)
		return
	}

	dbms, err := common.GetDatabaseManager(common.Metastore)
	if err != nil {
		ctx.GetLogger().Error("code: failed to get DB for PR resolution", "error", err)
		return
	}

	// Resolve the event id to link the PR against. event_resolution.event_id
	// is NOT NULL, so we always need a value. resolvePRTrackingEventId returns
	// hadEventAnchor=true when the request points at a specific event (explicit
	// QueryConfig.EventId, or session_id of the form `event-<fp>`). In that
	// case, an empty result means we lost an event we should have found — bail
	// rather than write a mislinked row that breaks the UI's event lookup. With
	// hadEventAnchor=false the request has no event signal at all (Slack
	// instant-notification flow, plain user chat), so falling back to the
	// conversation id is honest: the row is conversation-scoped and the
	// pr-lifecycle cron picks it up via meta.AccountID/TenantID.
	eventId, hadEventAnchor := resolvePRTrackingEventId(ctx, dbms.Db, query)
	if eventId == "" {
		if hadEventAnchor {
			ctx.GetLogger().Warn("code: event anchor present but lookup failed, skipping event_resolution insert",
				"pr_url", prURL,
				"session_id", query.SessionId,
				"conversation_id", query.ConversationId,
				"account_id", query.AccountId,
			)
			return
		}
		if query.ConversationId == "" {
			ctx.GetLogger().Warn("code: no event anchor and no conversation id, skipping event_resolution insert",
				"pr_url", prURL,
				"session_id", query.SessionId,
				"account_id", query.AccountId,
			)
			return
		}
		eventId = query.ConversationId
	}

	_, err = dbms.Db.Exec(
		`INSERT INTO event_resolution (id, event_id, type, data, status, type_reference_id, resolver_type, resolver_id, status_message, pr_lifecycle_state, pr_iteration_count)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)`,
		common.GenerateUUID(),
		eventId,
		"PullRequest",
		string(metadataJSON),
		"InProgress",
		prURL,
		"NBLLM",
		query.ConversationId,
		"PR raised successfully",
		"created",
		0,
	)
	if err != nil {
		ctx.GetLogger().Error("code: failed to insert PR resolution row", "error", err, "pr_url", prURL)
		return
	}

	ctx.GetLogger().Info("code: PR resolution row created for lifecycle tracking",
		"pr_url", prURL, "event_id", eventId, "conversation_id", query.ConversationId)
}

// prTrackingEventLookup is the narrow DB interface resolvePRTrackingEventId
// needs. sqlx.DB satisfies this; tests provide a fake.
type prTrackingEventLookup interface {
	QueryRow(query string, args ...interface{}) *sql.Row
	Get(dest interface{}, query string, args ...interface{}) error
}

// resolvePRTrackingEventId returns the events.id that a PR-tracking row should
// point at, plus a flag indicating whether the request had an event anchor.
// Resolution order:
//
//  1. query.QueryConfig.EventId — explicit event origin (most common).
//  2. query.SessionId parsed as `event-<fingerprint>` → most recent
//     events.id for that fingerprint on this account. Covers the
//     investigation-session flow where QueryConfig.EventId isn't threaded
//     through but the conversation is rooted on an event.
//  3. query.ConversationId → llm_conversations.session_id → same fingerprint
//     lookup as above. Belt-and-braces for paths that don't set SessionId
//     on the agent request.
//
// hadEventAnchor=true means the request *should* have resolved to an event:
// either QueryConfig.EventId was set, or the session_id (direct or via
// conversation lookup) had the `event-` prefix. If we still couldn't resolve,
// callers MUST NOT fall back to the conversation id — writing
// llm_conversations.id into an event_id column silently poisons the UI
// lookup and the pr-lifecycle cron.
//
// hadEventAnchor=false means the request had no event signal at all (Slack
// InstantNotification flow, plain user chat). Callers may legitimately use
// the conversation id as the row's anchor: there is no event UUID to lose,
// and the pr-lifecycle cron resolves tenant/account from metadata.
func resolvePRTrackingEventId(ctx *security.RequestContext, db prTrackingEventLookup, query core.NBAgentRequest) (string, bool) {
	if query.QueryConfig.EventId != "" {
		return query.QueryConfig.EventId, true
	}
	hadAnchor := strings.HasPrefix(query.SessionId, "event-")
	if id := lookupEventIdBySessionId(ctx, db, query.SessionId, query.AccountId); id != "" {
		return id, true
	}
	if query.ConversationId != "" {
		var sessionId string
		err := db.Get(&sessionId,
			`SELECT session_id FROM llm_conversations WHERE id = $1`,
			query.ConversationId,
		)
		if err == nil && sessionId != "" {
			if strings.HasPrefix(sessionId, "event-") {
				hadAnchor = true
			}
			if id := lookupEventIdBySessionId(ctx, db, sessionId, query.AccountId); id != "" {
				return id, true
			}
		}
	}
	return "", hadAnchor
}

// lookupEventIdBySessionId extracts an events.id from a session id of the
// form `event-<fingerprint>`. Picks the most recent event with that
// fingerprint for the account — that is the occurrence the active
// investigation is about (deduped events share fingerprints, and the LLM
// context was assembled from the latest one). Returns "" for non-matching
// session formats or when the lookup fails.
func lookupEventIdBySessionId(ctx *security.RequestContext, db prTrackingEventLookup, sessionId, accountId string) string {
	const prefix = "event-"
	if !strings.HasPrefix(sessionId, prefix) {
		return ""
	}
	fingerprint := strings.TrimPrefix(sessionId, prefix)
	if fingerprint == "" || accountId == "" {
		return ""
	}
	var eventId string
	err := db.Get(&eventId,
		`SELECT id::text FROM events
		 WHERE fingerprint = $1 AND cloud_account_id = $2
		 ORDER BY created_at DESC LIMIT 1`,
		fingerprint, accountId,
	)
	if err != nil {
		ctx.GetLogger().Debug("code: no event found for session fingerprint",
			"session_id", sessionId, "account_id", accountId, "error", err)
		return ""
	}
	return eventId
}

// parseOrgRepo extracts org and repo name from a git URL.
// e.g. "https://github.com/nudgebee/nudgebee-infra" → ("nudgebee", "nudgebee-infra")
func parseOrgRepo(gitURL string) (string, string) {
	gitURL = strings.TrimSuffix(gitURL, ".git")
	parsed, err := url.Parse(gitURL)
	if err != nil {
		return "", ""
	}
	parts := strings.Split(strings.Trim(parsed.Path, "/"), "/")
	if len(parts) >= 2 {
		return parts[len(parts)-2], parts[len(parts)-1]
	}
	return "", ""
}

// fuzzyMatchRepo attempts to match a workload name against a list of project URLs.
// It filters out infra repos and uses substring matching on the repo path components.
// Returns the best-matching URL, or empty string if no confident match is found.
func fuzzyMatchRepo(workloadName string, projectURLs []string) string {
	if workloadName == "" {
		return ""
	}

	workloadLower := strings.ToLower(workloadName)
	// Normalize: "llm-server" → "llm", "server", "llm-server"
	workloadParts := strings.Split(workloadLower, "-")

	var nonInfraURLs []string
	for _, u := range projectURLs {
		uLower := strings.ToLower(u)
		if strings.Contains(uLower, "infra") || strings.Contains(uLower, "infrastructure") || strings.Contains(uLower, "devops") || strings.Contains(uLower, "helm-charts") {
			continue
		}
		nonInfraURLs = append(nonInfraURLs, u)
	}

	// Try exact substring match of workload name against repo path
	var matches []string
	for _, u := range nonInfraURLs {
		repoPath := strings.ToLower(u)
		// Extract repo name from URL: "https://github.com/org/repo-name" → "repo-name"
		parts := strings.Split(strings.TrimSuffix(repoPath, ".git"), "/")
		repoName := ""
		if len(parts) > 0 {
			repoName = parts[len(parts)-1]
		}

		// Match: workload name contains repo name, or repo name contains workload name
		if repoName != "" && (strings.Contains(workloadLower, repoName) || strings.Contains(repoName, workloadLower)) {
			matches = append(matches, u)
			continue
		}
		// Match: any workload part (e.g., "llm" from "llm-server") matches repo name
		for _, part := range workloadParts {
			if len(part) >= 3 && strings.Contains(repoName, part) {
				matches = append(matches, u)
				break
			}
		}
	}

	if len(matches) == 1 {
		return matches[0]
	}

	// Multiple matches — ambiguous, return empty to trigger user selection
	if len(matches) > 1 {
		return ""
	}

	// No matches but only one non-infra repo — safe to use
	if len(nonInfraURLs) == 1 {
		return nonInfraURLs[0]
	}

	// No confident match — return empty to trigger user selection
	return ""
}

// resolveProjectRepoURL extracts and constructs a full repository URL from a project map entry.
func resolveProjectRepoURL(project map[string]string, cred GitCredentials) string {
	// Try "repository" key first
	if repoUrl, exists := project["repository"]; exists && repoUrl != "" {
		return repoUrl
	}
	// Try "repo" key
	if repoUrl, exists := project["repo"]; exists && repoUrl != "" {
		return repoUrl
	}
	// Try "key" key and construct full URL
	repoKey, exists := project["key"]
	if !exists || repoKey == "" {
		return ""
	}
	if strings.HasPrefix(repoKey, "https://") || strings.HasPrefix(repoKey, "http://") {
		return repoKey
	}
	baseURL := cred.Url
	switch cred.Provider {
	case "gitlab":
		if baseURL == "" {
			baseURL = "https://gitlab.com"
		}
		return strings.TrimSuffix(baseURL, "/") + "/" + repoKey
	case "github":
		if baseURL == "" {
			baseURL = "https://github.com"
		}
		baseURL = strings.Replace(baseURL, "api.github.com", "https://github.com", 1)
		if !strings.HasPrefix(baseURL, "https://") && !strings.HasPrefix(baseURL, "http://") {
			baseURL = "https://" + baseURL
		}
		return strings.TrimSuffix(baseURL, "/") + "/" + repoKey
	default:
		return "https://github.com/" + repoKey
	}
}

func (l CodeAgent2) getGitCredentials(ctx *security.RequestContext, repo string, accountId string) ([]GitCredentials, string, string, error) {

	credentials := []GitCredentials{}
	actualRepo := repo
	detectedProvider := ""

	// Detect provider from repo URL if provided
	if repo != "" {
		detectedProvider = detectGitProvider(repo)
		repoSplits := strings.Split(repo, "/")
		if len(repoSplits) >= 2 {
			actualRepo = repoSplits[len(repoSplits)-2] + "/" + repoSplits[len(repoSplits)-1]
		}
	}

	dbms, err := common.GetDatabaseManager(common.Metastore)
	if err != nil {
		return credentials, actualRepo, detectedProvider, err
	}

	// Determine preferred provider for ordering results
	preferredProvider := detectedProvider
	if preferredProvider == "" {
		preferredProvider = "github" // Default preference
	}

	rows, err := dbms.Db.Queryx(`
		SELECT
			i.type as provider,
			MAX(CASE WHEN icv.name = 'username' THEN icv.value END) as username,
			MAX(CASE WHEN icv.name = 'url' THEN icv.value END) as url,
			MAX(CASE WHEN icv.name = 'password' THEN icv.value END) as password,
			BOOL_OR(CASE WHEN icv.name = 'password' THEN icv.is_encrypted ELSE false END) as password_is_encrypted,
			MAX(CASE WHEN icv.name = 'auth_type' THEN icv.value END) as auth_type,
			MAX(CASE WHEN icv.name = 'projects' THEN icv.value END) as projects
		FROM integrations i
		JOIN integration_config_values icv ON i.id = icv.integration_id
		WHERE i.tenant_id IN (SELECT tenant FROM cloud_accounts WHERE id = $1)
		  AND i.status = 'enabled'
		  AND i.type IN ('github', 'gitlab')
		GROUP BY i.id, i.type
		ORDER BY CASE WHEN i.type = $2 THEN 0 ELSE 1 END
	`, accountId, preferredProvider)
	if err != nil {
		ctx.GetLogger().Error("unable to query integrations for git config", "error", err)
		return credentials, actualRepo, detectedProvider, err
	}
	defer func() {
		if err := rows.Close(); err != nil {
			ctx.GetLogger().Error("code: unable to close rows", "error", err)
		}
	}()

	for rows.Next() {
		var provider string
		var username string
		var url string
		var password string
		var passwordIsEncrypted bool
		var authType *string
		var projects *string

		err := rows.Scan(&provider, &username, &url, &password, &passwordIsEncrypted, &authType, &projects)
		if err != nil {
			return credentials, actualRepo, detectedProvider, err
		}

		// Skip if required fields are missing
		if username == "" || url == "" || password == "" {
			ctx.GetLogger().Warn("skipping integration with missing credentials", "provider", provider)
			continue
		}

		// Parse projects JSON if present
		projectsMap := []map[string]string{}
		if projects != nil && *projects != "" {
			err = common.UnmarshalJson([]byte(*projects), &projectsMap)
			if err != nil {
				ctx.GetLogger().Warn("unable to parse projects JSON", "error", err, "provider", provider)
				// Continue with empty projects instead of skipping entire credential
			}
		}

		// Decrypt password if it's encrypted
		decryptedPassword := password
		if passwordIsEncrypted && password != "" {
			decryptedPassword, err = common.Decrypt(password)
			if err != nil {
				ctx.GetLogger().Error("error decrypting password", "error", err)
				return credentials, actualRepo, detectedProvider, common.ErrorInternal("error: unable to process request")
			}
		}

		// Default auth_type to "token" if not specified
		finalAuthType := "token"
		if authType != nil && *authType != "" {
			finalAuthType = *authType
		}

		credentials = append(credentials, GitCredentials{
			Username: username,
			Url:      url,
			Password: decryptedPassword,
			AuthType: finalAuthType,
			Projects: projectsMap,
			Provider: provider,
		})
	}

	// If repo is empty, collect all available project URLs from credentials
	if actualRepo == "" && len(credentials) > 0 {
		var allProjectURLs []string
		var firstProvider string
		for _, cred := range credentials {
			for _, project := range cred.Projects {
				if repoUrl := resolveProjectRepoURL(project, cred); repoUrl != "" {
					allProjectURLs = append(allProjectURLs, repoUrl)
					if firstProvider == "" {
						firstProvider = cred.Provider
					}
				}
			}
		}

		if len(allProjectURLs) == 1 {
			// Single repo — use it directly
			actualRepo = allProjectURLs[0]
			detectedProvider = firstProvider
			ctx.GetLogger().Info("code: using only available repository from credentials", "repo", actualRepo, "provider", detectedProvider)
		} else if len(allProjectURLs) > 1 {
			// Multiple repos — don't pick blindly, let the caller handle selection
			ctx.GetLogger().Info("code: multiple repositories found in credentials, caller should ask user", "count", len(allProjectURLs), "repos", allProjectURLs)
		}
	}

	// If provider still not detected, try to detect from actualRepo
	if detectedProvider == "" && actualRepo != "" {
		detectedProvider = detectGitProvider(actualRepo)
	}

	return credentials, actualRepo, detectedProvider, nil
}

// detectGitProvider determines the Git provider from a repository URL
// Supports both cloud-hosted and self-hosted instances
func detectGitProvider(repoURL string) string {
	if repoURL == "" {
		return "github" // Default for backward compatibility
	}

	lowerURL := strings.ToLower(repoURL)

	// Check for GitLab-specific patterns first (more specific)
	// 1. gitlab.com (cloud)
	// 2. GitLab-specific URL pattern "/-/" in path (e.g., /-/merge_requests/)
	if strings.Contains(lowerURL, "gitlab.com") ||
		strings.Contains(lowerURL, "/-/") {
		return "gitlab"
	}

	// Parse URL to check hostname for self-hosted GitLab
	if strings.HasPrefix(lowerURL, "http://") || strings.HasPrefix(lowerURL, "https://") {
		if parsed, err := url.Parse(lowerURL); err == nil {
			host := strings.ToLower(parsed.Host)
			if strings.HasPrefix(host, "gitlab.") || strings.Contains(host, "gitlab") {
				return "gitlab"
			}
		}
	}

	// Check for GitHub patterns
	if strings.Contains(lowerURL, "github.com") ||
		strings.Contains(lowerURL, "github:") {
		return "github"
	}

	// Default to GitHub for backward compatibility
	return "github"
}

// K8s info extraction methods copied from LogAnalysisAgent
func (l CodeAgent2) extractK8sInfo(ctx *security.RequestContext, accountId string, conversationId string, messageId string, agentId string, logData string, userId string) ([]map[string]string, error) {
	logger := ctx.GetLogger()
	logger.Debug("Extracting K8s info from log data", "data_length", len(logData))

	// Use the constant from log analysis agent
	const PROMPT_CHAIN_LOG_EXTRACT_K8S_INFO = `Extract all Kubernetes resource information from the provided log data. If multiple resources are mentioned, include them all.

Return the result as a valid JSON array with the following format:
[
  {
    "namespace": "<namespace>",
    "pod_name": "<pod_name>",
    "workload_name": "<workload_name>"
  }
]

- Include at least the "namespace" and "pod_name" fields if they can be confidently determined.
- Only include "workload_name" if it is clearly identifiable.
- Do not confuse the workload name with the workload type (e.g., Deployment, StatefulSet).
- If no valid Kubernetes resources are found, return an empty array.
- If resource identification is uncertain, omit the entry entirely.

DO NOT ASSUME THE K8S INFO IF NOT MENTIONED IN THE LOG.

Log data: %v`

	// Prepare the prompt for the LLM
	messageHistory := []llms.MessageContent{
		llms.TextParts(llms.ChatMessageTypeHuman, fmt.Sprintf(PROMPT_CHAIN_LOG_EXTRACT_K8S_INFO, logData)),
	}

	// Generate completion with temperature 0 for more deterministic results
	completion, err := core.GenerateAndTrackLLMContent(ctx, userId, accountId, conversationId, messageId, agentId, false, messageHistory, true, llms.WithTemperature(0.0))
	if err != nil {
		return nil, fmt.Errorf("failed to extract k8s info from log data: %w", err)
	}

	llmResponse := completion.Choices[0].Content
	logger.Debug("Received LLM response for K8s info extraction", "response_length", len(llmResponse))

	// First try to extract JSON using a more robust approach
	jsonString := l.extractJSONFromText(llmResponse)
	if jsonString == "" {
		logger.Warn("No JSON array found in LLM response, returning empty result")
		return []map[string]string{}, nil
	}

	// Parse the JSON array
	var k8sInfoList []map[string]string
	err = common.UnmarshalJson([]byte(jsonString), &k8sInfoList)
	if err != nil {
		logger.Error("Failed to parse JSON from LLM response", "error", err, "json_string", jsonString)
		return nil, fmt.Errorf("failed to parse k8s info: %w", err)
	}

	return k8sInfoList, nil
}

// extractGitURLFromText extracts a git repository URL from plain text input using regex.
// Handles URLs like https://github.com/owner/repo, git@github.com:owner/repo, etc.
func extractGitURLFromText(text string) string {
	pattern := regexp.MustCompile(`(?:https?://|git@)[^\s,;'"]+`)
	matches := pattern.FindAllString(text, -1)
	for _, match := range matches {
		// Strip trailing punctuation that's likely not part of the URL
		match = strings.TrimRight(match, `.,;:!?)\"`)
		lower := strings.ToLower(match)
		if strings.Contains(lower, "github.com") || strings.Contains(lower, "gitlab.com") || strings.Contains(lower, "bitbucket.org") {
			return match
		}
	}
	return ""
}

// isValidGitURL checks if a string looks like a valid git repository URL.
func isValidGitURL(s string) bool {
	return strings.HasPrefix(s, "https://") || strings.HasPrefix(s, "http://") || strings.HasPrefix(s, "git@")
}

// validateRepoSelection normalizes an LLM repo-selection response and returns
// the matching candidate URL, or "" if the response is empty, "UNCERTAIN", or
// not in the candidate set. Pure function — kept separate from the LLM call so
// it can be unit tested without mocks.
func validateRepoSelection(llmResponse string, candidates []string) string {
	resp := strings.TrimSpace(llmResponse)
	resp = strings.Trim(resp, "\"'`")
	if resp == "" {
		return ""
	}
	if strings.EqualFold(resp, "UNCERTAIN") || strings.EqualFold(resp, "NONE") {
		return ""
	}
	for _, c := range candidates {
		if c == resp {
			return c
		}
	}
	// Tolerate a trailing slash mismatch from the LLM.
	respTrim := strings.TrimRight(resp, "/")
	for _, c := range candidates {
		if strings.TrimRight(c, "/") == respTrim {
			return c
		}
	}
	return ""
}

// truncateForPrompt clips a string to maxLen runes (UTF-8 safe) with a marker.
func truncateForPrompt(s string, maxLen int) string {
	const marker = " [...]"
	markerRunes := len([]rune(marker))
	runes := []rune(s)
	if maxLen <= 0 || len(runes) <= maxLen {
		return s
	}
	if maxLen <= markerRunes {
		return string(runes[:maxLen])
	}
	return string(runes[:maxLen-markerRunes]) + marker
}

// selectRepoFromConversationContext uses recent conversation messages plus the
// current query to pick a repository from the candidate set. It mirrors the
// pattern used by plannerExecutor.selectConfigUsingLLM for normal-agent
// configs: feed prior context + candidates to the LLM, validate the response
// against the candidate set, and return "" on uncertainty so callers can fall
// back to the existing followup.
//
// Returns the selected URL, or "" on any failure / uncertainty.
func (l CodeAgent2) selectRepoFromConversationContext(
	ctx *security.RequestContext,
	query core.NBAgentRequest,
	currentQuery string,
	candidates []string,
) string {
	logger := ctx.GetLogger()
	if len(candidates) <= 1 || query.ConversationId == "" {
		return ""
	}

	const (
		maxHistoryMessages   = 6
		maxPerMessageRunes   = 2000
		maxCurrentQueryRunes = 4000
	)

	chatHistory, err := core.GetConversationDao().LoadConversationMessages(
		query.AccountId, query.ConversationId, "", "", maxHistoryMessages+1,
	)
	if err != nil {
		logger.Warn("code: failed to load conversation history for repo selection", "error", err)
		return ""
	}
	// LoadConversationMessages returns DESC by created_at — reverse to chronological
	// and drop the current message so we don't bias the LLM with its own input.
	var history []map[string]string
	for i := len(chatHistory) - 1; i >= 0; i-- {
		m := chatHistory[i]
		if m["id"] == query.MessageId {
			continue
		}
		history = append(history, m)
	}
	if len(history) > maxHistoryMessages {
		history = history[len(history)-maxHistoryMessages:]
	}
	if len(history) == 0 {
		return ""
	}

	var historyBuilder strings.Builder
	for _, m := range history {
		role := m["role"]
		content := truncateForPrompt(m["content"], maxPerMessageRunes)
		response := truncateForPrompt(m["response"], maxPerMessageRunes)
		if content != "" {
			fmt.Fprintf(&historyBuilder, "- [%s] %s\n", role, content)
		}
		if response != "" {
			fmt.Fprintf(&historyBuilder, "  [ai-response] %s\n", response)
		}
	}

	var candidatesBuilder strings.Builder
	for i, c := range candidates {
		fmt.Fprintf(&candidatesBuilder, "%d. %s\n", i+1, c)
	}

	const promptTemplate = `You are selecting the most likely Git repository to analyze for a follow-up code-analysis request.

Candidate repositories (you MUST pick one of these verbatim, or reply UNCERTAIN):
%s
Recent conversation (oldest first):
%s
Current user request:
%s

Pick the single repository URL most clearly indicated by the conversation. Strong signals: a prior message names an "owner/repo" or full URL that matches a candidate, or references an issue/PR/commit in a candidate repository. If there is no clear signal, reply with the single word UNCERTAIN.

Reply with ONLY the chosen URL (exactly as listed above) or the single word UNCERTAIN. No explanation.`

	prompt := fmt.Sprintf(
		promptTemplate,
		candidatesBuilder.String(),
		historyBuilder.String(),
		truncateForPrompt(currentQuery, maxCurrentQueryRunes),
	)

	messages := []llms.MessageContent{
		llms.TextParts(llms.ChatMessageTypeHuman, prompt),
	}

	completion, err := core.GenerateAndTrackLLMContent(
		ctx, query.UserId, query.AccountId, query.ConversationId, query.MessageId,
		query.AgentId, false, messages, true, llms.WithTemperature(0.0),
	)
	if err != nil {
		logger.Warn("code: repo context selection LLM call failed", "error", err)
		return ""
	}
	if completion == nil || len(completion.Choices) == 0 {
		logger.Warn("code: repo context selection returned empty response")
		return ""
	}

	selected := validateRepoSelection(completion.Choices[0].Content, candidates)
	if selected == "" {
		logger.Info("code: repo context selection uncertain, will ask user",
			"raw_response", strings.TrimSpace(completion.Choices[0].Content),
			"candidates", len(candidates))
		return ""
	}
	logger.Info("code: repo context selection picked candidate from conversation",
		"repo", selected, "candidates", len(candidates), "history_messages", len(history))
	return selected
}

// extractGitRepoFromQuery attempts to extract Git repository URL (GitHub or GitLab) from user query using LLM
func (l CodeAgent2) extractGitRepoFromQuery(ctx *security.RequestContext, accountId string, conversationId string, messageId string, agentId string, query string, userId string) (string, string, error) {
	logger := ctx.GetLogger()
	logger.Debug("Extracting Git repo from user query", "query_length", len(query))

	const PROMPT_EXTRACT_GIT_REPO = `Extract the Git repository URL from the provided text. Look for:
- GitHub URLs (e.g., https://github.com/owner/repo)
- GitLab URLs (e.g., https://gitlab.com/group/project)
- Self-hosted GitLab instances (URLs containing "gitlab" in hostname, e.g., https://gitlab.company.com/group/project)
- Repository references in owner/repo or group/project format

Return ONLY the repository URL in one of these formats:
- "https://github.com/owner/repo" for GitHub
- "https://gitlab.com/group/project" for GitLab (or full URL for self-hosted instances)
- "owner/repo" if the provider is unclear (will default to GitHub)

If no Git repository is mentioned or can be confidently identified, return an empty string.

DO NOT make assumptions or guess repository names.

Text: %v`

	// Prepare the prompt for the LLM
	messageHistory := []llms.MessageContent{
		llms.TextParts(llms.ChatMessageTypeHuman, fmt.Sprintf(PROMPT_EXTRACT_GIT_REPO, query)),
	}

	// Generate completion with temperature 0 for more deterministic results
	completion, err := core.GenerateAndTrackLLMContent(ctx, userId, accountId, conversationId, messageId, agentId, false, messageHistory, true, llms.WithTemperature(0.0))
	if err != nil {
		return "", "", fmt.Errorf("failed to extract git repo from query: %w", err)
	}

	llmResponse := strings.TrimSpace(completion.Choices[0].Content)
	logger.Debug("Received LLM response for Git repo extraction", "response", llmResponse)

	// Clean up the response and validate it
	if llmResponse == "" || strings.ToLower(llmResponse) == "none" || strings.ToLower(llmResponse) == "empty" {
		return "", "", nil
	}

	// Detect provider from the response
	provider := detectGitProvider(llmResponse)

	// Basic validation and normalization
	if strings.Contains(llmResponse, "github.com") {
		return llmResponse, "github", nil
	}
	if strings.Contains(llmResponse, "gitlab") {
		return llmResponse, "gitlab", nil
	}

	// If it's in owner/repo format without explicit provider, default to github
	if strings.Count(llmResponse, "/") >= 1 && !strings.Contains(llmResponse, "://") {
		llmResponse = "https://github.com/" + llmResponse
		return llmResponse, "github", nil
	}

	return llmResponse, provider, nil
}

// extractJSONFromText attempts to extract a JSON array from text using multiple methods
func (l CodeAgent2) extractJSONFromText(text string) string {
	// First try to find JSON array using brackets
	startIdx := strings.Index(text, "[")
	endIdx := strings.LastIndex(text, "]")

	if startIdx != -1 && endIdx != -1 && startIdx < endIdx {
		return strings.TrimSpace(text[startIdx : endIdx+1])
	}

	// If that fails, try with regex for more complex cases
	re := regexp.MustCompile(`\[\s*(?:\{[^{}]*\}(?:\s*,\s*\{[^{}]*\})*)\s*\]`)
	jsonString := strings.TrimSpace(re.FindString(text))

	return jsonString
}

func (l CodeAgent2) GetSourceCodeAnnotations(ctx *security.RequestContext, request core.NBAgentRequest, k8sInfo []map[string]string, eventId string) (map[string]string, error) {
	if len(k8sInfo) == 0 && eventId == "" {
		return nil, nil
	}

	ctx.GetLogger().Info("Getting source code annotations", "pod_count", len(k8sInfo), "eventId", eventId)

	// Get the database connection
	dbManager, err := common.GetDatabaseManager(common.Metastore)
	if err != nil {
		return nil, err
	}

	// First try to get annotations by eventId if available
	if eventId != "" {
		ctx.GetLogger().Info("Attempting to get annotations using eventId", "eventId", eventId)

		workloadName := request.QueryConfig.Workload
		namespace := request.QueryConfig.Namespace

		if workloadName == "" {
			rows, err := dbManager.Db.Queryx("select subject_owner, subject_namespace from events where id = $1", eventId)
			if err != nil {
				ctx.GetLogger().Warn("failed to query event for workload extraction", "error", err)
			}
			defer func() {
				if err := rows.Close(); err != nil {
					ctx.GetLogger().Error("code: unable to close rows", "error", err)
				}
			}()
			for rows.Next() {
				err := rows.Scan(&workloadName, &namespace)
				if err != nil {
					ctx.GetLogger().Warn("failed to scan event row for workload extraction", "error", err)
				}
			}
		}

		annotations, err := services_server.GetSourceCodeAnnotations(ctx, dbManager, request.AccountId, services_server.SourceCodeAnnotationOptions{
			EventId:      eventId,
			WorkloadName: workloadName,
			Namespace:    namespace,
		})
		if err == nil && len(annotations) > 0 {
			ctx.GetLogger().Info("Successfully retrieved annotations using eventId", "count", len(annotations))
			return annotations, nil
		}
		ctx.GetLogger().Info("No annotations found using eventId, falling back to pod/workload names")
	}

	var k8sInfoObjects []map[string]string
	for _, info := range k8sInfo {
		obj := map[string]string{
			"pod_name":      info["pod_name"],
			"namespace":     info["namespace"],
			"workload_name": info["workload_name"],
		}
		k8sInfoObjects = append(k8sInfoObjects, obj)
	}
	for _, i := range k8sInfoObjects {
		annotations, err := services_server.GetSourceCodeAnnotations(ctx, dbManager, request.AccountId, services_server.SourceCodeAnnotationOptions{
			PodName:      i["pod_name"],
			WorkloadName: i["workload_name"],
			Namespace:    i["namespace"],
		})
		if err != nil {
			ctx.GetLogger().Info("Failed to get source code annotations", "error", err, "pod_name", i["pod_name"], "workload_name", i["workload_name"])
		}
		if len(annotations) > 0 && (annotations["workloads.nudgebee.com/git.repo"] != "" || annotations["workloads.nudgebee.com/git.hash"] != "" || annotations["ci.nudgebee.com/git.hash"] != "" || annotations["ci.nudgebee.com/git.repo"] != "") {
			return annotations, nil
		}
	}
	return nil, nil
}

// executeFollowup handles PR followup mode — calls workspace /analyze with followup fields
// to address CI failures and review comments on agent-created PRs.
func (l CodeAgent2) executeFollowup(ctx *security.RequestContext, query core.NBAgentRequest, request CodeAgent2Request) (core.NBAgentResponse, error) {
	logger := ctx.GetLogger()

	// Get git credentials for the repo
	creds, repoUrl, provider, err := l.getGitCredentials(ctx, request.GitRepo, query.AccountId)
	if err != nil {
		logger.Error("code followup: unable to get git creds", "error", err)
		return core.NBAgentResponse{}, err
	}
	if request.GitRepo == "" && repoUrl != "" {
		request.GitRepo = repoUrl
	}
	if len(creds) == 0 {
		return core.NBAgentResponse{}, errors.New("git credentials are required for PR followup")
	}

	// Resolve git token from credentials (same pattern as evaluateCodeUsingWorkspace)
	gitToken := ""
	if len(creds) > 0 {
		switch creds[0].AuthType {
		case "token":
			gitToken = creds[0].Password
		case "application":
			if provider == "github" {
				installationID := int64(0)
				if _, err := fmt.Sscanf(creds[0].Password, "%d", &installationID); err == nil {
					token, err := utils.GetGithubAppInstallationToken(ctx.GetContext(), creds[0].Url, installationID)
					if err == nil {
						gitToken = token
					}
				}
			} else {
				gitToken = creds[0].Password
			}
		}
	}
	if gitToken == "" {
		if provider == "gitlab" {
			gitToken = os.Getenv("GITLAB_TOKEN")
		} else {
			gitToken = os.Getenv("GITHUB_TOKEN")
		}
	}

	tenantId := ""
	if ctx.GetSecurityContext() != nil {
		tenantId = ctx.GetSecurityContext().GetTenantId()
	}
	if request.AccountId == "" {
		request.AccountId = query.AccountId
	}

	// Build workspace /analyze request with followup fields
	analyzeRequest := map[string]any{
		"cloud_account_id":   request.AccountId,
		"tenant":             tenantId,
		"workload_name":      "unknown",
		"workload_namespace": "unknown",
		"workload_kind":      "Deployment",
		"logs":               request.Query,
		"prompt":             request.Query,
		"git_repository": map[string]any{
			"url":      request.GitRepo,
			"branch":   request.PRBranch,
			"provider": provider,
		},
		// PR-lifecycle followup is fix-mode by definition: the cron only fires
		// to iterate on an existing NB-raised PR (CI failure or unresolved
		// review comment), never for exploration.
		"mode":            codeAgentModeFix,
		"raise_pr":        true,
		"conversation_id": query.SessionId,
		"message_id":      query.MessageId,
		// Followup-specific fields
		"followup":  true,
		"pr_url":    request.PRURL,
		"pr_branch": request.PRBranch,
	}

	if gitToken != "" {
		analyzeRequest["git_credentials"] = map[string]any{
			"type":  "token",
			"token": gitToken,
		}
	}

	// Pre-flight: verify workspace pod is reachable
	healthWm := workspace.NewWorkspaceManagerWithTimeout(10 * time.Second)
	if _, healthErr := healthWm.CallAPI(ctx, query.AccountId, "GET", "/health", nil, nil); healthErr != nil {
		logger.Warn("code followup: workspace health check failed, attempting recovery", "error", healthErr)
		recoveryWm := workspace.NewWorkspaceManagerWithTimeout(60 * time.Second)
		if _, recoveryErr := recoveryWm.CallAPIOrLazyCreate(ctx, query.AccountId, "GET", "/health", nil, nil); recoveryErr != nil {
			return core.NBAgentResponse{}, fmt.Errorf("workspace pod not healthy after recovery attempt: %w", recoveryErr)
		}
		logger.Info("code followup: workspace pod recovered successfully")
	}

	wm := workspace.NewWorkspaceManagerWithTimeout(60 * time.Second)
	logger.Info("code followup: executing via workspace", "account_id", query.AccountId, "pr_url", request.PRURL)

	// POST /analyze — code-analysis returns 202 with analysis_id
	respBytes, err := wm.CallAPIOrLazyCreate(ctx, query.AccountId, "POST", "/analyze", nil, analyzeRequest)
	if err != nil {
		return core.NBAgentResponse{}, fmt.Errorf("workspace /analyze followup call failed: %w", err)
	}

	var asyncResp map[string]any
	if err := json.Unmarshal(respBytes, &asyncResp); err != nil {
		result := extractAgentResponseWithTokenUsage(respBytes)
		return core.NBAgentResponse{Response: []string{result.AgentResponse}}, nil
	}

	// Sync response (backward compat)
	if _, hasResult := asyncResp["agent_response"]; hasResult {
		result := extractAgentResponseWithTokenUsage(respBytes)
		return core.NBAgentResponse{Response: []string{result.AgentResponse}}, nil
	}

	if errMsg, _ := asyncResp["error"].(string); errMsg != "" {
		return core.NBAgentResponse{}, fmt.Errorf("workspace /analyze followup failed: %s", errMsg)
	}

	analysisID, _ := asyncResp["analysis_id"].(string)
	status, _ := asyncResp["status"].(string)
	if analysisID == "" || status != "running" {
		return core.NBAgentResponse{}, fmt.Errorf("unexpected workspace /analyze followup response: status=%q analysis_id=%q", status, analysisID)
	}

	// Poll /status/{id} every 5s until completed or failed
	logger.Info("code followup: analysis accepted, polling for progress", "analysis_id", analysisID)
	statusEndpoint := fmt.Sprintf("/status/%s", url.PathEscape(analysisID))
	pollWm := workspace.NewWorkspaceManagerWithTimeout(30 * time.Second)
	lastProgress := ""
	const maxConsecutiveErrors = 12
	const maxPollDuration = 30 * time.Minute
	consecutiveErrors := 0
	pollDeadline := time.Now().Add(maxPollDuration)

	for {
		select {
		case <-ctx.GetContext().Done():
			return core.NBAgentResponse{}, fmt.Errorf("followup analysis timed out while polling for results")
		case <-time.After(5 * time.Second):
		}

		if time.Now().After(pollDeadline) {
			return core.NBAgentResponse{}, fmt.Errorf("followup analysis polling exceeded maximum duration of %v", maxPollDuration)
		}

		statusBytes, err := pollWm.CallAPI(ctx, query.AccountId, "GET", statusEndpoint, nil, nil)
		if err != nil {
			consecutiveErrors++
			logger.Warn("code followup: failed to poll status", "error", err, "analysis_id", analysisID,
				"consecutive_errors", consecutiveErrors, "max_consecutive_errors", maxConsecutiveErrors)
			if consecutiveErrors >= maxConsecutiveErrors {
				return core.NBAgentResponse{}, fmt.Errorf("followup polling abandoned after %d consecutive errors: %w", consecutiveErrors, err)
			}
			continue
		}
		consecutiveErrors = 0

		var statusResp map[string]any
		if err := json.Unmarshal(statusBytes, &statusResp); err != nil {
			logger.Warn("code followup: failed to parse status response", "error", err)
			continue
		}

		// Update progress in DB if changed
		progress, _ := statusResp["progress"].(string)
		if progress != "" && progress != lastProgress {
			lastProgress = progress
			if query.MessageId != "" {
				core.GetConversationDao().UpdateConversationMessageAsync(
					query.MessageId, progress, core.ConversationStatusInProgress,
				)
			}
		}

		pollStatus, _ := statusResp["status"].(string)
		switch pollStatus {
		case "completed":
			result, ok := statusResp["result"]
			if !ok {
				return core.NBAgentResponse{}, fmt.Errorf("followup analysis completed but no result returned")
			}
			resultBytes, err := json.Marshal(result)
			if err != nil {
				return core.NBAgentResponse{}, fmt.Errorf("failed to marshal followup result: %w", err)
			}
			logger.Info("code followup: analysis completed", "analysis_id", analysisID)
			caResult := extractAgentResponseWithTokenUsage(resultBytes)
			return core.NBAgentResponse{Response: []string{caResult.AgentResponse}}, nil
		case "failed":
			errMsg, _ := statusResp["error"].(string)
			return core.NBAgentResponse{}, fmt.Errorf("followup analysis failed: %s", errMsg)
		}
		// status == "running" → keep polling
	}
}
