package agents

import (
	"context"
	"encoding/json"
	"fmt"
	"nudgebee/llm/agents/core"
	"nudgebee/llm/common"
	"nudgebee/llm/security"
	"nudgebee/llm/tools"
	toolcore "nudgebee/llm/tools/core"
	"nudgebee/llm/workspace"
	"slices"
	"strings"
	"time"

	"github.com/tmc/langchaingo/llms"
)

func init() {
}

const KubectlLogAgentName = "kubectl_log"

func newKubectlLogAgent(accountId string) KubectlLogAgent {
	return KubectlLogAgent{ // This agent is typically called by the generic 'logs' agent.
		accountId: accountId,
	}
}

type KubectlLogAgent struct {
	accountId string
}

func (l KubectlLogAgent) GetName() string {
	return KubectlLogAgentName
}

func (l KubectlLogAgent) GetNameAliases() []string {
	return []string{"Kubectl Logs"}
}

func (l KubectlLogAgent) GetDescription() string {
	return `Retrieves Kubernetes pod/deployment logs using kubectl. Input should be a natural language question about logs.`
}

func (l KubectlLogAgent) GetSystemPrompt(ctx *security.RequestContext, query core.NBAgentRequest) core.NBAgentPrompt {
	instructions := []string{
		"**Understand the Request:** Carefully analyze the user's request to identify the specific log information they need.",
		"**Namespace:** Always include the namespace in the command. If the user does not specify a namespace, ask for clarification.",
		"**Filtering:**",
		"   - If the user asks for error logs, use `grep -i -E '(error|exception)'` to filter the output.",
		"   - Always get last 100 lines of logs using `--tail 100`.",
		"   - Use  `-p` to get previous logs in case of crashloop.",
		"**Resource Discovery** - Use resource_search tool for searching pods/workloads",
		"**Always try resource_search when:**",
		"   - User mentions app/service names without exact k8s resource details",
		"   - kubectl commands fail with 'not found' or 'no resources found'",
		"   - Resource names seem ambiguous or could have variations",
		"**Multiple Containers:** If there are multiple containers, use `-c <container_name>`. Check using `kubectl get pods <pod_name> -n <namespace> -o jsonpath='{.spec.containers[*].name}'`",
		"**Formatting**: Summarize the relevant logs for the user. Focus on errors, warnings, and any unusual patterns in the logs and provide context where necessary ie log reference as evidence.",
	}

	constraints := []string{
		"You MUST use the `kubectl_execute` tool to get the log data.",
		"If there are multiple PODS then select any one for logs",
		"Always specify the namespace in the command.",
		"If the namespace is not specified, check all namespaces",
		"If no limit is specified use last 100 lines `--tail=100`",
		"**IMPORTANT: Use resource_search tool proactively** - Don't wait for kubectl to fail first",
		"**When in doubt about resource names:** Always use resource_search tool to find exact matches",
		"**Never guess resource names:** Use resource_search tool to get accurate suggestions",
		"Do NOT use the `--until-time` flag as it is not supported. Use `--since-time` only.",
	}

	toolUsage := map[string][]string{
		tools.ToolExecuteKubectlCommand: {
			"Use this tool to execute kubectl command.",
			"Input: valid kubectl command",
			"Output: the data returned by the kubectl command.",
		},
		ResourceSearchAgentName: {
			"Use this tool to search for pod/workload either thru partial match or generate suggestions.",
			"Input: search query in natural language",
			"Output: resource suggestions and search strategies",
			"Examples: Can you search pods maching `pod1`",
		},
	}
	examples := []core.NBAgentPromptExample{
		{
			Question: "get error logs from pod nginx in ingress namespace",
			AnswerSteps: []core.NBAgentPromptExampleAnswerStep{
				{
					Tool:        tools.ToolResourceSearch,
					Input:       "get me list of pods matching nginx in ingress namespace",
					Explanation: "Use resource_search tool to find the exact pod name",
				},
				{
					Tool:        tools.ToolExecuteKubectlCommand,
					Input:       "kubectl logs nginx-7bb7cd8f5-5x9j2 --namespace ingress --tail 100 | grep -i 'error|exception'",
					Explanation: "Gets logs from the pod nginx-7bb7cd8f5-5x9j2 in ingress namespace",
				},
			},
			Explanation: "Gets logs from a pod.",
		},
		{
			Question: "logs previous terminated ruby container logs from pod web-1 in ingress namespace",
			Answer:   "kubectl logs -p -c ruby web-1 --namespace ingress --tail 100",
			AnswerSteps: []core.NBAgentPromptExampleAnswerStep{
				{
					Tool:        tools.ToolExecuteKubectlCommand,
					Input:       "kubectl logs -p -c ruby web-1 --namespace ingress --tail 100",
					Explanation: "get logs using kubectl for previous terminated ruby container",
				},
			},
			Explanation: "Gets previous container logs.",
		},
		{
			Question:    "get error logs from deployment nginx in ingress namespace",
			Answer:      "kubectl logs deployment/nginx -n ingress --tail 100 | grep -i -E '(error|exception)'",
			Explanation: "Gets error logs using grep.",
		},
	}

	return core.NBAgentPrompt{
		Role:         "a Kubernetes expert, specialized in log retrieval using kubectl.",
		Instructions: instructions,
		Constraints:  constraints,
		ToolUsage:    toolUsage,
		Examples:     examples,
	}
}

func (p KubectlLogAgent) GetSupportedTools(ctx *security.RequestContext) []toolcore.NBTool {
	// Add resource search tool
	toolsList := []toolcore.NBTool{tools.KubectlExecuteTool{}}
	if resourceSearchTool, ok := toolcore.GetNBTool(p.accountId, ResourceSearchAgentName); ok {
		toolsList = append(toolsList, resourceSearchTool)
	}
	return toolsList
}

func (l KubectlLogAgent) GetPlannerType() core.AgentPlannerType {
	return core.AgentPlannerTypeCustom
}

type kubectlLogIntent struct {
	ResourceName  string `json:"resource_name"`
	ResourceType  string `json:"resource_type"`
	Namespace     string `json:"namespace"`
	Container     string `json:"container"`
	Tail          int    `json:"tail"`
	IsPrevious    bool   `json:"is_previous"`
	FilterPattern string `json:"filter_pattern"`
}

func (l KubectlLogAgent) Execute(ctx *security.RequestContext, request core.NBAgentRequest) (core.NBAgentResponse, error) {
	var effortLog []string
	var agentStepResponses []core.ToolInvocation

	// 1. Parse intent
	intent, err := l.parseLogIntent(ctx, request)
	if err != nil {
		effortLog = append(effortLog, fmt.Sprintf("Critical: Failed to parse initial intent: %v", err))
		return core.NBAgentResponse{
			Response:          []string{fmt.Sprintf("Failed to parse log retrieval intent.\n\n### Effort Summary:\n* %s", strings.Join(effortLog, "\n* "))},
			Status:            core.ConversationStatusFailed,
			AgentStepResponse: agentStepResponses,
		}, nil
	}

	var finalLogs string
	var references []toolcore.NBToolResponseReference
	effortLog = append(effortLog, fmt.Sprintf("Parsed initial intent: resource=%s, ns=%s, container=%s", intent.ResourceName, intent.Namespace, intent.Container))

	// Internal recovery loop: max 2 attempts to resolve resource or handle "multiple found" errors
	for i := 0; i < 2; i++ {
		// 2. Resolve resource if details missing or previous attempt failed
		if intent.ResourceName == "" || strings.Contains(intent.ResourceName, "*") || (i > 0 && intent.Namespace == "") {
			resolved, resolvedRefs, resolveErr := l.resolveResource(ctx, request, intent)
			if resolveErr == nil && resolved.Name != "" {
				intent.ResourceName = resolved.Name
				if resolved.Namespace != "" {
					intent.Namespace = resolved.Namespace
				}
				if resolved.Type != "" {
					intent.ResourceType = resolved.Type
				}
				references = append(references, resolvedRefs...)
				effortLog = append(effortLog, fmt.Sprintf("Attempt %d: Resolved resource via discovery: %s in namespace %s (type: %s)", i+1, resolved.Name, resolved.Namespace, resolved.Type))
			} else {
				effortLog = append(effortLog, fmt.Sprintf("Attempt %d: Resource discovery failed: %v", i+1, resolveErr))
			}
		}

		// 3. Build command — but bail if we still have no resource to query
		if intent.ResourceName == "" {
			effortLog = append(effortLog, fmt.Sprintf("Attempt %d: No resource name resolved, skipping kubectl execution", i+1))
			if i == 0 {
				continue // Give second attempt a chance to resolve
			}
			break // No point retrying without a resource
		}
		cmd := l.buildKubectlCommand(intent)
		effortLog = append(effortLog, fmt.Sprintf("Attempt %d: Executing command: %s", i+1, cmd))

		// 4. Execute kubectl
		tool, found := toolcore.GetNBTool(l.accountId, tools.ToolExecuteKubectlCommand)
		if !found {
			return core.NBAgentResponse{}, fmt.Errorf("kubectl tool not found")
		}

		toolCtx := toolcore.NewNbToolContext(ctx, tool, l.accountId, request.UserId, request.ConversationId, request.MessageId, request.AgentId, cmd, nil, request.QueryContext, request.QueryConfig, "")
		toolResp, toolErr := tool.Call(toolCtx, toolcore.NBToolCallRequest{Command: cmd})

		// Record step
		agentStepResponses = append(agentStepResponses, core.ToolInvocation{
			Call:       llms.ToolCall{Type: "function", FunctionCall: &llms.FunctionCall{Name: tools.ToolExecuteKubectlCommand, Arguments: cmd}},
			Response:   llms.ToolCallResponse{Name: tools.ToolExecuteKubectlCommand, Content: toolResp.Data},
			References: toolResp.References,
		})

		// Check for errors even if toolErr is nil (e.g. NotFound in stdout)
		errMsg := strings.ToLower(toolResp.Data)
		hasError := toolErr != nil || strings.Contains(errMsg, "error:") || strings.Contains(errMsg, "notfound") || strings.Contains(errMsg, "not found")

		if hasError {
			effortLog = append(effortLog, fmt.Sprintf("Attempt %d Result: Failed with error: %s", i+1, toolResp.Data))

			// 4a. Handle common failures by refining intent and retrying
			if strings.Contains(errMsg, "a container name must be specified") || strings.Contains(errMsg, "choose one of") {
				containers, _ := l.getPodContainers(ctx, request, intent.ResourceName, intent.Namespace)
				if len(containers) > 0 {
					picked := containers[0]
					for _, c := range containers {
						cLow := strings.ToLower(c)
						if !strings.Contains(cLow, "istio") && !strings.Contains(cLow, "linkerd") && !strings.Contains(cLow, "proxy") && !strings.Contains(cLow, "sidecar") {
							picked = c
							break
						}
					}
					intent.Container = picked
					effortLog = append(effortLog, fmt.Sprintf("Attempt %d Recovery: Multi-container pod, auto-selected container: %s", i+1, picked))
					continue
				}
			}

			if strings.Contains(errMsg, "multiple") || strings.Contains(errMsg, "not found") || strings.Contains(errMsg, "no resources") {
				effortLog = append(effortLog, fmt.Sprintf("Attempt %d Recovery: Resource not found, clearing details for broader search", i+1))
				intent.Namespace = ""
				continue
			}

			if i == 0 {
				// Don't waste an LLM refinement call on infrastructure errors
				if !isRetryableLogQueryError(toolResp.Data) {
					effortLog = append(effortLog, "Attempt 1: Non-retryable infrastructure error, skipping LLM refinement")
				} else {
					ctx.GetLogger().Info("kubectl_log: attempting LLM-driven intent refinement after error", "error", toolResp.Data)
					refinedIntent, refineErr := l.refineLogIntentWithError(ctx, request, intent, toolResp.Data)
					if refineErr == nil {
						intent = refinedIntent
						effortLog = append(effortLog, "Attempt 1 Recovery: Refined intent using LLM correction")
						continue
					}
				}
			}

			failMsg := fmt.Sprintf("Failed to retrieve kubectl logs after %d attempts.\n\n### Effort Summary:\n* %s",
				i+1, strings.Join(effortLog, "\n* "))
			return core.NBAgentResponse{Response: []string{failMsg}, Status: core.ConversationStatusFailed, AgentStepResponse: agentStepResponses}, nil
		}

		// Success
		finalLogs = toolResp.Data
		references = append(references, toolResp.References...)
		break
	}

	if finalLogs == "" {
		failMsg := fmt.Sprintf("Unable to retrieve logs after multiple attempts. Please verify pod name and namespace.\n\n### Effort Summary:\n* %s",
			strings.Join(effortLog, "\n* "))
		return core.NBAgentResponse{Response: []string{failMsg}, Status: core.ConversationStatusFailed, AgentStepResponse: agentStepResponses}, nil
	}

	// 5. Save raw logs to workspace
	var finalReferences []toolcore.NBToolResponseReference
	logFileName := fmt.Sprintf("logs_kubectl_%d.txt", time.Now().UnixNano())
	wm := workspace.NewWorkspaceManager()
	if err := wm.SaveFile(ctx, l.accountId, request.ConversationId, logFileName, finalLogs); err == nil {
		finalReferences = append(finalReferences, toolcore.NBToolResponseReference{
			Text:        logFileName,
			Url:         logFileName,
			Type:        "file",
			Description: "Raw log data from kubectl",
		})
		effortLog = append(effortLog, fmt.Sprintf("System: Raw logs saved to workspace as %s", logFileName))
		ctx.GetLogger().Info("kubectl_log: logs saved", "file", logFileName, "total_steps", len(effortLog))
	} else {
		ctx.GetLogger().Warn("kubectl_log: failed to save logs to workspace", "error", err)
	}

	// Add other collected references
	finalReferences = append(finalReferences, references...)

	// 6. Post-process (Smart Tail: Raw Tail + Errors Spotlight)
	isErrorQuery := strings.Contains(strings.ToLower(request.Query), "error") || strings.Contains(strings.ToLower(request.Query), "fail")
	lines := strings.Split(finalLogs, "\n")
	errorLines := tools.GetErrorLinesFromLogStringOrDefault(finalLogs, false)

	tailCount := 50
	if isErrorQuery && len(errorLines) > 0 {
		tailCount = 20
	}

	tailStart := 0
	if len(lines) > tailCount {
		tailStart = len(lines) - tailCount
	}
	rawTail := strings.Join(lines[tailStart:], "\n")

	processedLogs := ""
	if rawTail != "" {
		processedLogs += fmt.Sprintf("--- RECENT LOG TAIL ---\n%s\n\n", rawTail)
	}
	if len(errorLines) > 0 {
		processedLogs += fmt.Sprintf("--- DETECTED ERROR HIGHLIGHTS ---\n%s", strings.Join(errorLines, "\n"))
	}

	if len(processedLogs) > 10000 {
		processedLogs = processedLogs[:5000] + "\n\n... [TRUNCATED] ...\n\n" + processedLogs[len(processedLogs)-5000:]
	}

	// 7. Final response synthesis
	finalAnswer, err := l.generateFinalResponse(ctx, request, intent, processedLogs)
	if err != nil {
		finalAnswer = fmt.Sprintf("Retrieved logs for %s in %s. Found %d lines matching filters.\n\nLogs:\n%s", intent.ResourceName, intent.Namespace, len(errorLines), processedLogs)
	}

	return core.NBAgentResponse{
		Response:          []string{finalAnswer},
		AgentName:         l.GetName(),
		Status:            core.ConversationStatusCompleted,
		AgentStepResponse: agentStepResponses,
		References:        finalReferences,
	}, nil
}

func (l KubectlLogAgent) parseLogIntent(ctx *security.RequestContext, request core.NBAgentRequest) (kubectlLogIntent, error) {
	systemPrompt := `Extract Kubernetes log retrieval parameters from the user's query and context. 
Return ONLY a JSON object with the following fields: 
- resource_name: Name of pod or deployment (string)
- resource_type: "pod", "deployment", "statefulset", etc (string)
- namespace: Namespace (string)
- container: Specific container name if mentioned (string)
- tail: Number of lines to retrieve, default 100 (int)
- is_previous: true if requesting previously crashed logs (bool)
- filter_pattern: Regex pattern for grep if looking for errors/warnings (string)

Default tail to 100. If searching for errors, set filter_pattern to "(error|exception|fail|timeout|warn)".
`
	messages := []llms.MessageContent{
		llms.TextParts(llms.ChatMessageTypeSystem, systemPrompt),
	}
	if request.ConversationContext != "" {
		messages = append(messages, llms.TextParts(llms.ChatMessageTypeSystem, fmt.Sprintf("Context:\n%s", request.ConversationContext)))
	}
	messages = append(messages, llms.TextParts(llms.ChatMessageTypeHuman, request.Query))

	liteCtx := security.NewRequestContext(
		context.WithValue(ctx.GetContext(), core.ContextKeyUseLiteModel, true),
		ctx.GetSecurityContext(),
		ctx.GetLogger(),
		ctx.GetTracer(),
		ctx.GetMeter(),
	)

	res, err := core.GenerateAndTrackLLMContent(liteCtx, request.UserId, request.AccountId, request.ConversationId, request.MessageId, request.AgentId, false, messages, true)
	if err != nil {
		return kubectlLogIntent{Tail: 100}, nil
	}

	var intent kubectlLogIntent
	if err := common.ExtractAndUnmarshalJSON([]byte(res.Choices[0].Content), &intent); err != nil {
		return kubectlLogIntent{Tail: 100}, nil
	}
	if intent.Tail == 0 {
		intent.Tail = 100
	}
	return intent, nil
}

func (l KubectlLogAgent) refineLogIntentWithError(ctx *security.RequestContext, request core.NBAgentRequest, currentIntent kubectlLogIntent, errorMsg string) (kubectlLogIntent, error) {
	systemPrompt := `The previous attempt to retrieve Kubernetes logs failed with an error. 
Analyze the error message and original user request to provide a CORRECTED JSON object for log retrieval.

ERROR MESSAGE:
{{.error}}

CURRENT PARAMETERS:
{{.current_intent}}

Return ONLY a JSON object with these fields:
- resource_name: Name of pod or deployment
- resource_type: "pod", "deployment", etc
- namespace: Namespace
- container: Specific container name (Crucial: check if error mentions available containers)
- tail: Number of lines (int)
- is_previous: bool
- filter_pattern: Regex pattern for grep (Keep simple if previous attempt failed)

INSTRUCTIONS:
1. If the error says a container is required and lists them, pick the most relevant one.
2. If the error is a syntax error, adjust the resource naming or flags.
3. Keep resource_name and namespace if they seem correct, only change what caused the error.
`
	intentJSON, _ := json.Marshal(currentIntent)
	prompt := strings.ReplaceAll(systemPrompt, "{{.error}}", errorMsg)
	prompt = strings.ReplaceAll(prompt, "{{.current_intent}}", string(intentJSON))

	messages := []llms.MessageContent{
		llms.TextParts(llms.ChatMessageTypeSystem, prompt),
		llms.TextParts(llms.ChatMessageTypeHuman, request.Query),
	}

	liteCtx := security.NewRequestContext(
		context.WithValue(ctx.GetContext(), core.ContextKeyUseLiteModel, true),
		ctx.GetSecurityContext(),
		ctx.GetLogger(),
		ctx.GetTracer(),
		ctx.GetMeter(),
	)

	res, err := core.GenerateAndTrackLLMContent(liteCtx, request.UserId, request.AccountId, request.ConversationId, request.MessageId, request.AgentId, false, messages, true)
	if err != nil {
		return currentIntent, err
	}

	var refinedIntent kubectlLogIntent
	if err := common.ExtractAndUnmarshalJSON([]byte(res.Choices[0].Content), &refinedIntent); err != nil {
		return currentIntent, err
	}

	if refinedIntent.Tail == 0 {
		refinedIntent.Tail = currentIntent.Tail
	}
	if refinedIntent.ResourceName == "" {
		refinedIntent.ResourceName = currentIntent.ResourceName
	}

	return refinedIntent, nil
}

func (l KubectlLogAgent) resolveResource(ctx *security.RequestContext, request core.NBAgentRequest, intent kubectlLogIntent) (struct{ Name, Namespace, Type string }, []toolcore.NBToolResponseReference, error) {
	searchQuery := intent.ResourceName
	if searchQuery == "" || strings.Contains(searchQuery, "*") {
		searchQuery = request.Query
	}

	tool, ok := toolcore.GetNBTool(l.accountId, ResourceSearchAgentName)
	if !ok {
		return struct{ Name, Namespace, Type string }{}, nil, fmt.Errorf("search tool not found")
	}

	toolCtx := toolcore.NewNbToolContext(ctx, tool, l.accountId, request.UserId, request.ConversationId, request.MessageId, request.AgentId, searchQuery, nil, request.QueryContext, request.QueryConfig, "")
	resp, err := tool.Call(toolCtx, toolcore.NBToolCallRequest{Command: searchQuery})

	var searchResp tools.K8sResourceSearchResponse
	var refs []toolcore.NBToolResponseReference
	if err == nil {
		refs = resp.References
		if err := common.UnmarshalJson([]byte(resp.Data), &searchResp); err != nil {
			ctx.GetLogger().Warn("kubectl_log: failed to unmarshal resource search response", "error", err)
		}
	}

	loggableTypes := []string{"pod", "deployment", "statefulset", "daemonset", "job"}

	if len(searchResp.Resources) > 0 {
		var best tools.K8sResourceInfo
		foundLoggable := false
		for _, r := range searchResp.Resources {
			rType := strings.ToLower(r.Type)
			if !slices.Contains(loggableTypes, rType) {
				continue
			}
			if !foundLoggable {
				best = r
				foundLoggable = true
			}
			if rType == "deployment" || rType == "statefulset" || rType == "daemonset" {
				best = r
				break
			}
		}
		if foundLoggable {
			return struct{ Name, Namespace, Type string }{Name: best.Name, Namespace: best.Namespace, Type: best.Type}, refs, nil
		}
	}

	ctx.GetLogger().Info("kubectl_log: performing aggressive global pod search", "query", searchQuery)
	kTool, _ := toolcore.GetNBTool(l.accountId, tools.ToolExecuteKubectlCommand)
	safeQuery := strings.ReplaceAll(searchQuery, ";", "")
	safeQuery = strings.ReplaceAll(safeQuery, "|", "")
	safeQuery = strings.ReplaceAll(safeQuery, "&", "")
	safeQuery = strings.ReplaceAll(safeQuery, "`", "")

	searchCmd := fmt.Sprintf("kubectl get pods -A --no-headers | grep -i %s", safeQuery)
	kCtx := toolcore.NewNbToolContext(ctx, kTool, l.accountId, request.UserId, request.ConversationId, request.MessageId, request.AgentId, searchCmd, nil, request.QueryContext, request.QueryConfig, "")

	kResp, kErr := kTool.Call(kCtx, toolcore.NBToolCallRequest{Command: searchCmd})
	if kErr == nil && kResp.Status == toolcore.NBToolResponseStatusSuccess && kResp.Data != "" {
		stdout := l.extractStdout(kResp.Data)
		lines := strings.Split(stdout, "\n")
		for _, line := range lines {
			fields := strings.Fields(line)
			if len(fields) >= 2 {
				ctx.GetLogger().Info("kubectl_log: found matching pod globally", "name", fields[1], "ns", fields[0])
				return struct{ Name, Namespace, Type string }{Name: fields[1], Namespace: fields[0], Type: "pod"}, append(refs, kResp.References...), nil
			}
		}
	}

	return struct{ Name, Namespace, Type string }{}, refs, fmt.Errorf("no loggable resources found")
}

func (l KubectlLogAgent) getPodContainers(ctx *security.RequestContext, request core.NBAgentRequest, podName, namespace string) ([]string, error) {
	if namespace == "" {
		namespace = "default"
	}
	cmd := fmt.Sprintf("kubectl get pod %s -n %s -o jsonpath='{.spec.containers[*].name}'", podName, namespace)

	tool, _ := toolcore.GetNBTool(l.accountId, tools.ToolExecuteKubectlCommand)
	toolCtx := toolcore.NewNbToolContext(ctx, tool, l.accountId, request.UserId, request.ConversationId, request.MessageId, request.AgentId, cmd, nil, request.QueryContext, request.QueryConfig, "")

	resp, err := tool.Call(toolCtx, toolcore.NBToolCallRequest{Command: cmd})
	if err != nil {
		return nil, err
	}

	stdout := l.extractStdout(resp.Data)
	containers := strings.Fields(strings.Trim(stdout, "'\" "))
	return containers, nil
}

func (l KubectlLogAgent) extractStdout(toolResponse string) string {
	resultsMap := map[string]any{}
	err := common.UnmarshalJson([]byte(toolResponse), &resultsMap)
	if err != nil {
		return toolResponse
	}
	data := ""
	if resultsMap["stdout"] != nil {
		data = resultsMap["stdout"].(string)
	}
	return data
}

func (l KubectlLogAgent) buildKubectlCommand(intent kubectlLogIntent) string {
	resource := intent.ResourceName
	if intent.ResourceType != "" && !strings.Contains(resource, "/") {
		resource = intent.ResourceType + "/" + resource
	}

	cmd := fmt.Sprintf("kubectl logs %s", resource)
	if intent.Namespace != "" {
		cmd += fmt.Sprintf(" -n %s", intent.Namespace)
	}

	if intent.Container != "" {
		cmd += fmt.Sprintf(" -c %s", intent.Container)
	}

	if intent.IsPrevious {
		cmd += " -p"
	}

	cmd += fmt.Sprintf(" --tail %d", intent.Tail)
	return cmd
}

func (l KubectlLogAgent) generateFinalResponse(ctx *security.RequestContext, request core.NBAgentRequest, intent kubectlLogIntent, logs string) (string, error) {
	systemPrompt := fmt.Sprintf(`You are a Kubernetes SRE expert. Analyze the provided logs for %s in namespace %s. 
Your goal is to provide a highly technical and detailed summary that answers the user's question.

CRITICAL INSTRUCTIONS:
- Limit your response to 500 words.
- Include specific timestamps from the logs.
- Mention specific API paths, HTTP status codes, or internal function names found in the logs.
- If errors are present, provide the exact error signature or stack trace snippet.
- Do not provide a generic "everything is fine" response if there are technical events occurring.
- Use professional Markdown formatting (tables, code blocks).

User Question: %s`, intent.ResourceName, intent.Namespace, request.Query)

	messages := []llms.MessageContent{
		llms.TextParts(llms.ChatMessageTypeSystem, systemPrompt),
		llms.TextParts(llms.ChatMessageTypeHuman, fmt.Sprintf("Logs:\n%s", logs)),
	}

	res, err := core.GenerateAndTrackLLMContent(ctx, request.UserId, request.AccountId, request.ConversationId, request.MessageId, request.AgentId, false, messages, true)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(res.Choices[0].Content), nil
}

func (l KubectlLogAgent) UpdateToolResponseForPlanner(toolRequest core.NBAgentPlannerToolAction, toolResponse string) string {
	return toolResponse
}
