package core

import (
	"context"
	"encoding/xml"
	"fmt"
	"log/slog"
	"nudgebee/llm/agents/prompts_repo"
	"nudgebee/llm/common"
	"nudgebee/llm/config"
	"nudgebee/llm/security"
	toolcore "nudgebee/llm/tools/core"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/samber/lo"
	"github.com/tmc/langchaingo/llms"
	"github.com/tmc/langchaingo/prompts"
)

const defaultMaxRefinementAttempts2 = 3

var (
	rewooPlannerXMLRegex2                        = regexp.MustCompile("(?s)```xml\n(.*?)\n```")
	rewooPlannerPlanResponseRegex2               = regexp.MustCompile(`(?s)(<plan_response>.*?</plan_response>)`)
	rewooPlannerThoughtsRegex2                   = regexp.MustCompile(`(?s)<thought>(.*?)</thought>`)
	rewooPlannerStep2Regex2                      = regexp.MustCompile(`(?s)<step>(.*?)</step>`)
	rewooPlannerIDRegex2                         = regexp.MustCompile(`(?s)<id>(.*?)</id>`)
	rewooPlannerToolRegex2                       = regexp.MustCompile(`(?s)<tool>(.*?)</tool>`)
	rewooPlannerQueryRegex2                      = regexp.MustCompile(`(?s)<query>(.*?)</query>`)
	rewooPlannerReasonRegex2                     = regexp.MustCompile(`(?s)<reason>(.*?)</reason>`)
	rewooPlannerDependencyRegex2                 = regexp.MustCompile(`(?s)<dependency>(.*?)</dependency>`)
	rewooPlannerCondition2PromptRegex2           = regexp.MustCompile(`(?s)<condition><prompt>(.*?)</prompt>`)
	rewooPlannerCondition2ExpectedResponseRegex2 = regexp.MustCompile(`(?s)<condition><expectedResponse>(.*?)</expectedResponse>`)
	rewooPlannerCondition2AllowedResponsesRegex2 = regexp.MustCompile(`(?s)<condition><allowedResponses>(.*?)</allowedResponses>`)
)

// NBAgent is an Agent driven by Tools.
type rewooPlannerCondition2 struct {
	Prompt           string   `xml:"prompt" json:"prompt"`
	ExpectedResponse string   `xml:"expectedResponse" json:"expectedResponse"`
	AllowedResponses []string `xml:"allowedResponses" json:"allowedResponses"`
}

type rewooPlannerStep2 struct {
	ID         string                  `xml:"id" json:"id"`
	Tool       string                  `xml:"tool" json:"tool"`
	Query      string                  `xml:"query" json:"query"`
	Reason     string                  `xml:"reason" json:"reason"`
	Dependency []string                `xml:"dependency" json:"dependency"`
	Condition  *rewooPlannerCondition2 `xml:"condition" json:"condition"`
	Followup   *FollowupRequest        `json:"followup,omitempty"`
}

func (s *rewooPlannerStep2) UnmarshalJSON(data []byte) error {
	type Alias rewooPlannerStep2
	aux := &struct {
		Plan string `json:"plan"`
		*Alias
	}{
		Alias: (*Alias)(s),
	}
	if err := common.UnmarshalJson(data, &aux); err != nil {
		return err
	}
	if s.Reason == "" && aux.Plan != "" {
		s.Reason = aux.Plan
	}
	return nil
}

type rewooPlan2 struct {
	XMLName xml.Name            `xml:"plan_response"`
	Thought string              `xml:"thought"`
	Steps   []rewooPlannerStep2 `xml:"plan>step"`
}

type rewooReviewResponse2 struct {
	XMLName     xml.Name            `xml:"review_response"`
	Thought     string              `xml:"thought"`
	Action      string              `xml:"action"`
	UpdatedPlan []rewooPlannerStep2 `xml:"updated_plan>step"`
}

type StepStatus string

const (
	StepStatusPending   StepStatus = "pending"
	StepStatusRunning   StepStatus = "running"
	StepStatusCompleted StepStatus = "completed"
	StepStatusFailed    StepStatus = "failed"
	StepStatusSkipped   StepStatus = "skipped"
	StepStatusWaiting   StepStatus = "waiting"
)

type PlannerNode struct {
	Step       rewooPlannerStep2                  `json:"step"`
	Status     StepStatus                         `json:"status"`
	Output     string                             `json:"output"`
	References []toolcore.NBToolResponseReference `json:"references"`
	Iteration  int                                `json:"iteration"`
}

type ReWooPlanner2 struct {
	ctx                       *security.RequestContext
	prompt                    prompts.FormatPrompter
	nbAgent                   NBAgent
	request                   NBAgentRequest
	isFiveWhysMode            bool
	fiveWhysCounter           int
	isInvestigationRequest    bool
	refinementAttempts        int
	maxRefinementAttempts     int
	refinementFeedback        string
	systemMessage             string
	extraMessages             []prompts.MessageFormatter
	solver                    *ReWooSolver
	critique                  *ReWooCritiquer
	planCritiquer             *ReWooPlanCritiquer
	planRefinementAttempts    int
	maxPlanRefinementAttempts int
	maxPlanSteps              int
	tools                     []toolcore.NBTool
	// New state fields
	executionGraph     map[string]*PlannerNode
	isGraphInitialized bool
	plannerAgentID     string
	currentIteration   int
	Notebook           string
	compressionTracker *CompressionTracker
}

// saveCritique persists critique decisions to the database for debugging/analysis.
func (o *ReWooPlanner2) saveCritique(critiqueType, input, critiquedContent, feedback, decision string) {
	conversationDAO := GetConversationDao()
	var feedbackPtr *string
	if feedback != "" {
		feedbackPtr = &feedback
	}
	err := conversationDAO.SaveCritique(&ConversationCritique{
		ConversationID:   o.request.ConversationId,
		MessageID:        o.request.MessageId,
		AccountID:        o.request.AccountId,
		AgentName:        o.nbAgent.GetName(),
		CritiqueType:     critiqueType,
		Input:            input,
		CritiquedContent: critiquedContent,
		Feedback:         feedbackPtr,
		Decision:         decision,
	})
	if err != nil {
		o.getLogger().Error("rewooagent2: failed to save critique", "error", err, "type", critiqueType, "decision", decision)
	}
}

func (o *ReWooPlanner2) storeGeneratedPlan(input, observation string, actions []NBAgentPlannerToolAction) (string, error) {
	plansJson := []map[string]any{}
	for _, action := range actions {
		planMap := map[string]any{
			"id":         action.ToolID,
			"tool":       action.Tool,
			"query":      action.ToolInput,
			"reason":     action.Log,
			"dependency": action.Dependency,
		}
		// Add condition to planMap if it's not empty
		if action.Condition.Expression != "" || action.Condition.Prompt != "" || action.Condition.ExpectedResponse != "" || len(action.Condition.AllowedResponses) > 0 {
			conditionMap := make(map[string]any)
			if action.Condition.Expression != "" {
				conditionMap["expression"] = action.Condition.Expression
			}
			if action.Condition.Prompt != "" {
				conditionMap["prompt"] = action.Condition.Prompt
			}
			if action.Condition.ExpectedResponse != "" {
				conditionMap["expectedResponse"] = action.Condition.ExpectedResponse
			}
			if len(action.Condition.AllowedResponses) > 0 {
				conditionMap["allowedResponses"] = action.Condition.AllowedResponses
			}
			planMap["condition"] = conditionMap
		}
		plansJson = append(plansJson, planMap)
	}
	plansJsonBytes, err := common.MarshalJson(plansJson)
	if err != nil {
		o.getLogger().Error("rewooagent: unable to serialize json", "error", err)
	}

	conversationDAO := GetConversationDao()
	agentId, err := conversationDAO.SaveConversationAgentCall(o.request.ConversationId, o.request.MessageId, o.request.AccountId, o.request.UserId, plannerDummyTool, o.request.ParentAgentId, input, observation, string(plansJsonBytes), o.request.ConversationContext, o.request.QueryConfig)
	if err != nil {
		o.getLogger().Error("rewooagent: failed to save agent call to DB", "error", err.Error())
	}
	err = conversationDAO.UpdateConversationAgentResponse(agentId.String(), string(plansJsonBytes), AgentExecutionStatusSuccess, "", "", "", "")
	if err != nil {
		o.getLogger().Error("rewooagent: failed to update agent call to DB", "error", err.Error())
	}

	return agentId.String(), nil
}

func (o *ReWooPlanner2) generatePlanSummary() string {
	var sb strings.Builder
	sb.WriteString("\n\n### Plan Execution Summary:\n")

	nodes := make([]*PlannerNode, 0, len(o.executionGraph))
	for _, node := range o.executionGraph {
		nodes = append(nodes, node)
		slices.SortFunc(nodes, func(a, b *PlannerNode) int {
			if a.Iteration != b.Iteration {
				return a.Iteration - b.Iteration
			}
			return strings.Compare(a.Step.ID, b.Step.ID)
		})
		fmt.Fprintf(&sb, "- Step %s (%s): %s\n", node.Step.ID, node.Step.Tool, node.Status)
		if node.Status == StepStatusSkipped {
			sb.WriteString("  Reason: Skipped due to unmet dependencies or review.\n")
		}
		if node.Status == StepStatusFailed {
			fmt.Fprintf(&sb, "  Error: %s\n", node.Output)
		}
	}
	return sb.String()
}

// finalizePendingSteps transitions any lingering Pending or Waiting nodes in
// the execution graph to Skipped with the supplied reason, then persists the
// plan so the UI reflects the terminal state accurately. Waiting nodes are
// included because once the planner terminates, no user will respond to them.
// Running/Completed/Failed/Skipped nodes are untouched. Called from Plan() via
// a deferred hook when a terminal finish action is returned (see #28243).
func (o *ReWooPlanner2) finalizePendingSteps(reason string) {
	skipped := 0
	for id, node := range o.executionGraph {
		if node.Status == StepStatusPending || node.Status == StepStatusWaiting {
			prevStatus := node.Status
			node.Status = StepStatusSkipped
			if node.Output == "" {
				node.Output = reason
			}
			skipped++
			o.getLogger().Info("rewooagent2: finalizing unexecuted step as skipped", "id", id, "tool", node.Step.Tool, "previous_status", prevStatus)
		}
	}
	if skipped == 0 {
		return
	}
	if err := o.updateStoredPlan(); err != nil {
		o.getLogger().Error("rewooagent2: failed to persist finalized plan", "error", err, "skipped_count", skipped)
	}
}

func (o *ReWooPlanner2) updateStoredPlan() error {
	if o.plannerAgentID == "" {
		return nil
	}

	plansJson := []map[string]any{}
	// Reconstruct plan JSON from graph
	// We need to maintain order, but graph is map.
	// For now, let's just dump all nodes. Better would be to topological sort or keep an order list.
	// Since UI might just show list, order matters.
	// Let's sort by ID to be deterministic, though not ideal if ID order != execution order.
	// Assuming IDs are E1, E2...

	// Better: Use the original step order if possible, or just dump.
	// Let's collect and sort by ID numeric suffix.

	nodes := make([]*PlannerNode, 0, len(o.executionGraph))
	for _, node := range o.executionGraph {
		nodes = append(nodes, node)
	}

	slices.SortFunc(nodes, func(a, b *PlannerNode) int {
		return strings.Compare(a.Step.ID, b.Step.ID)
	})

	for _, node := range nodes {
		action := node.Step
		planMap := map[string]any{
			"id":         action.ID,
			"tool":       action.Tool,
			"query":      action.Query,
			"reason":     action.Reason,
			"dependency": action.Dependency,
			"status":     string(node.Status),
			"iteration":  node.Iteration,
			"references": node.References,
		}
		if action.Condition != nil {
			conditionMap := make(map[string]any)
			if action.Condition.Prompt != "" {
				conditionMap["prompt"] = action.Condition.Prompt
			}
			if action.Condition.ExpectedResponse != "" {
				conditionMap["expectedResponse"] = action.Condition.ExpectedResponse
			}
			if len(action.Condition.AllowedResponses) > 0 {
				conditionMap["allowedResponses"] = action.Condition.AllowedResponses
			}
			if len(conditionMap) > 0 {
				planMap["condition"] = conditionMap
			}
		}
		plansJson = append(plansJson, planMap)
	}

	plansJsonBytes, err := common.MarshalJson(plansJson)
	if err != nil {
		return err
	}

	conversationDAO := GetConversationDao()
	// Update the response with the new plan structure. Status remains success as this is just an update.
	return conversationDAO.UpdateConversationAgentResponse(o.plannerAgentID, string(plansJsonBytes), AgentExecutionStatusSuccess, "", "", "", "")
}

// Plan decides what action to take using a stateful graph-based approach.
func (o *ReWooPlanner2) Plan(
	ctx context.Context,
	intermediateSteps []NBAgentPlannerToolActionStep,
	input string,
) (actions []NBAgentPlannerToolAction, finish *NBAgentPlannerFinishAction, err error) {
	o.getLogger().Info("rewooagent2: starting plan iteration", "steps_count", len(intermediateSteps))

	// Ensure that when the planner terminates (returns a finish action other than WAITING),
	// any leftover Pending nodes are promoted to Skipped and the stored plan is refreshed.
	// Without this the UI shows executed plans with lingering "pending" rows — see #28243.
	defer func() {
		if finish == nil {
			return
		}
		if finish.Status == ConversationStatusWaiting || finish.Status == ConversationStatusWaitingForClientTool {
			return
		}
		o.finalizePendingSteps("Skipped because the investigation finished before this step was executed.")
	}()

	// Reset any 'running' steps to 'pending' at the start of a planning cycle ONLY if we are starting fresh.
	// This prevents deadlocks where blocked pending steps wait for running steps that were interrupted.
	if len(intermediateSteps) == 0 && !o.isGraphInitialized {
		for id, node := range o.executionGraph {
			if node.Status == StepStatusRunning {
				o.getLogger().Info("rewooagent2: resetting orphaned running step to pending", "id", id)
				node.Status = StepStatusPending
			}
		}
	}

	// 1. SYNC STATE: Update graph with results from the last execution batch
	if len(intermediateSteps) > 0 {
		o.syncState(intermediateSteps)
	}

	// 1.0.5 ORPHAN RECOVERY: Any node still "running" after syncState was never reported back
	// (e.g., a worker goroutine panicked or doIterationParallel returned early on a WAITING
	// path while sibling workers were still in flight). Reset to pending so they are retried
	// rather than marked failed — marking failed would cascade skips to dependent nodes.
	for id, node := range o.executionGraph {
		if node.Status == StepStatusRunning {
			o.getLogger().Warn("rewooagent2: resetting orphaned running step to pending for retry", "id", id)
			node.Status = StepStatusPending
		}
	}

	// 1.1 HANDLE TOOL CONFIRMATIONS & CONFIGS (Follow-ups)
	for _, node := range o.executionGraph {
		if node.Status == StepStatusWaiting {
			// Check Confirmations
			if confirmation, ok := o.request.QueryConfig.ToolConfirmations[node.Step.Tool]; ok {
				if strings.EqualFold(confirmation, "yes") || strings.EqualFold(confirmation, "y") || strings.EqualFold(confirmation, "confirm") {
					o.getLogger().Info("rewooagent2: user confirmed tool execution, moving node back to pending", "id", node.Step.ID, "tool", node.Step.Tool)
					node.Status = StepStatusPending
				} else if strings.EqualFold(confirmation, "no") || strings.EqualFold(confirmation, "n") || strings.EqualFold(confirmation, "cancel") {
					o.getLogger().Info("rewooagent2: user declined tool execution, skipping node", "id", node.Step.ID, "tool", node.Step.Tool)
					node.Status = StepStatusSkipped
					node.Output = "User declined tool execution confirmation."
				}
			}
			// Check Configs
			if config, ok := o.request.QueryConfig.ToolConfigs[node.Step.Tool]; ok && config != "" {
				o.getLogger().Info("rewooagent2: tool configuration provided, moving node back to pending", "id", node.Step.ID, "tool", node.Step.Tool)
				node.Status = StepStatusPending
			}
		}
	}

	// 2. FAST PATH / INITIALIZATION
	if !o.isGraphInitialized {
		t0 := time.Now()
		// if its tool call, then go thru planner.. else skip planner in first attempt
		// Solver will handle task.. and if even if solver fails, it will return missing info
		isToolCallTask, err := o.isToolCallTask(ctx, input)
		if err != nil {
			o.getLogger().Error("rewooagent2: classification failed", "error", err)
		}
		if !isToolCallTask {
			isToolCallTask = len(o.extraMessages) > 0
		}

		if !isToolCallTask {
			isToolCallTask = len(o.extraMessages) > 0
		}
		o.getLogger().Info("rewooagent2: task classification complete", "duration", time.Since(t0).String(), "isToolTask", isToolCallTask)

		if !isToolCallTask {
			o.getLogger().Info("rewooagent2: simple task detected, attempting direct solution")
			solverStart := time.Now()
			finish, missingInfo, updatedNotebook, err := o.solver.Solve(ctx, input, intermediateSteps, o.Notebook, "")
			o.getLogger().Info("rewooagent2: solver call complete", "duration", time.Since(solverStart).String())
			if updatedNotebook != "" {
				if o.Notebook != "" && !strings.Contains(updatedNotebook, o.Notebook) {
					o.Notebook += "\n" + updatedNotebook
				} else {
					o.Notebook = updatedNotebook
				}
			}
			if err == nil && finish != nil {
				return nil, finish, nil
			}
			if missingInfo != "" {
				o.getLogger().Info("rewooagent2: fast path failed (missing info), falling back to full planning", "info", missingInfo)
			} else if err != nil {
				o.getLogger().Warn("rewooagent2: fast path solver error, falling back to full planning", "error", err)
			}
		}

		err = o.initializeGraph(ctx, input, intermediateSteps)
		if err != nil {
			return nil, nil, err
		}
	}

	// 3. DYNAMIC REVIEW: If something failed or we finished a batch, we might want to revise.
	// Special case: if a load_skills step just completed, force a skill-aware review so the
	// remaining plan can be updated with the domain expertise that was loaded.
	if len(intermediateSteps) > 0 {
		reviewInput := input
		for _, step := range intermediateSteps {
			if strings.EqualFold(step.Action.Tool, "load_skills") && step.Status != ToolStatusFailure {
				o.getLogger().Info("rewooagent2: skill loaded, enriching plan review with domain expertise",
					"toolId", step.Action.ToolID, "observation_len", len(step.Observation))
				reviewInput = fmt.Sprintf("Goal: %s\n\nSKILL LOADED: Expert domain guidance was loaded in step %s. Review the remaining plan and update it to incorporate this expertise — the loaded skill content is available in the scratchpad. Adjust tool queries, add/remove steps, or reorder as needed based on the expert guidance.",
					input, step.Action.ToolID)
				break
			}
		}
		reviewStart := time.Now()
		err := o.reviewAndRefinePlan(ctx, reviewInput, intermediateSteps)
		o.getLogger().Info("rewooagent2: plan review complete", "duration", time.Since(reviewStart).String())
		if err != nil {
			o.getLogger().Error("rewooagent2: plan review failed, continuing with current plan", "error", err)
		}
		// Propagate skips again in case the plan was updated and new steps depend on failed ones
		o.propagateSkips()
	}

	// 4. BATCH SELECTION: Get next unblocked steps
	nextBatch := o.getRunnableBatch()

	if len(nextBatch) > 0 {
		o.getLogger().Info("rewooagent2: returning next batch of independent steps", "count", len(nextBatch))
		return nextBatch, nil, nil
	}

	// Check if we are blocked by waiting tools
	var waitingTools []any
	var waitingClientTools []any
	for _, node := range o.executionGraph {
		if node.Status == StepStatusWaiting {
			toolInfo := map[string]any{
				"tool_name":  node.Step.Tool,
				"tool_input": node.Step.Query,
				"tool_id":    node.Step.ID,
			}

			// Try to identify if it's a client tool
			isClientTool := false
			if toolObj := o.findNBTool(node.Step.Tool); toolObj != nil {
				if _, ok := toolObj.(*toolcore.ClientToolWrapper); ok {
					isClientTool = true
				}
			}

			if isClientTool {
				waitingClientTools = append(waitingClientTools, toolInfo)
			} else {
				waitingTools = append(waitingTools, toolInfo)
			}
		}
	}

	if len(waitingClientTools) > 0 {
		o.getLogger().Info("rewooagent2: blocked by waiting client tools", "count", len(waitingClientTools))
		return nil, &NBAgentPlannerFinishAction{
			Status: ConversationStatusWaitingForClientTool,
			Data:   fmt.Sprintf("Waiting for %d client tool(s)", len(waitingClientTools)),
			AdditionalDetails: map[string]any{
				"client_tools": waitingClientTools,
			},
		}, nil
	}

	if len(waitingTools) > 0 {
		o.getLogger().Info("rewooagent2: blocked by waiting server-side tools (e.g. sub-agent configuration)", "count", len(waitingTools))

		// If we have a followup request stored in the node, return it
		var followup FollowupRequest
		firstWaitingData := fmt.Sprintf("Waiting for %d tool(s)", len(waitingTools))

		for _, node := range o.executionGraph {
			if node.Status == StepStatusWaiting {
				// Prefer server-side waiting tools for the primary followup
				isClientTool := false
				if toolObj := o.findNBTool(node.Step.Tool); toolObj != nil {
					if _, ok := toolObj.(*toolcore.ClientToolWrapper); ok {
						isClientTool = true
					}
				}
				if !isClientTool && node.Step.Followup != nil {
					followup = *node.Step.Followup
					if node.Output != "" {
						firstWaitingData = node.Output
					}
					break
				}
			}
		}

		return nil, &NBAgentPlannerFinishAction{
			Status:   ConversationStatusWaiting,
			Data:     firstWaitingData,
			Followup: followup,
		}, nil
	}

	// 5. COMPLETION OR DEADLOCK RECOVERY
	if o.allStepsDone() {
		o.getLogger().Info("rewooagent2: all steps completed, calling solver for final answer")

		planSummary := o.generatePlanSummary()

		// STRUCTURAL RECOVERY:
		// If there were failed steps, programmatically force the solver to acknowledge them
		// and either pivot or provide a detailed status report.
		hasFailures := false
		for _, node := range o.executionGraph {
			if node.Status == StepStatusFailed {
				hasFailures = true
				break
			}
		}
		if hasFailures {
			planSummary += "\n\nSYSTEM ALERT: One or more technical steps FAILED. Do not simply summarize the error. 1. Review the failures and the gathered metadata in the Notebook. 2. If the failures were due to incorrect resource names, missing parameters, or wrong regions, use <missing_information> to request a NEW plan with corrected inputs. 3. ONLY if no recovery is possible after reviewing all metadata, provide a final answer synthesizing what was successfully found."
		}

		// Use original query as input and summary as context to avoid confusion
		solverPhaseStart := time.Now()
		finish, missingInfo, updatedNotebook, err := o.solver.Solve(ctx, o.request.Query, intermediateSteps, o.Notebook, planSummary)
		o.getLogger().Info("rewooagent2: solver phase complete", "duration", time.Since(solverPhaseStart).String())
		if updatedNotebook != "" {
			if o.Notebook != "" && !strings.Contains(updatedNotebook, o.Notebook) {
				o.Notebook += "\n" + updatedNotebook
			} else {
				o.Notebook = updatedNotebook
			}
		}
		if err != nil {
			summaryFinish, summaryErr := o.summarizeAfterError(ctx, err, intermediateSteps)
			return nil, summaryFinish, summaryErr
		}

		if finish != nil {
			// Skip critique when data quality is insufficient — tools already failed repeatedly,
			// and the critique would reject the answer for suggesting commands to the user,
			// which is exactly what we want in this case.
			scratchpadForCheck := ConstructScratchPad(intermediateSteps, ScratchpadContext{Ctx: o.ctx, Request: o.request, Tracker: o.compressionTracker})
			hasInsufficientData := hasToolFailureMajority(scratchpadForCheck)

			if (o.request.EnableCritique || o.isInvestigationRequest) && !hasInsufficientData {
				availableTools := FilterTools(o.nbAgent.GetSupportedTools(o.ctx), o.request.Capabilities)
				toolDescriptions := reActPromptToolDescriptions(availableTools)

				critiqueStart := time.Now()
				refine, feedback, critErr := o.critique.Critique(ctx, o.request.Query+planSummary, intermediateSteps, finish, o.Notebook, lo.Ternary(o.isInvestigationRequest, "investigation", "query"), toolDescriptions)
				o.getLogger().Info("rewooagent2: critique complete", "duration", time.Since(critiqueStart).String(), "refine", refine)

				// Store critique decision
				if critErr == nil {
					decision := lo.Ternary(refine, "refine", "accept")
					o.saveCritique("answer_critique", o.request.Query, finish.Data, feedback, decision)
				}

				if critErr == nil && refine && o.refinementAttempts >= o.maxRefinementAttempts {
					o.getLogger().Warn("rewooagent2: critique rejected answer but max refinements reached, returning best-effort answer", "refinementAttempts", o.refinementAttempts, "maxRefinementAttempts", o.maxRefinementAttempts)
				} else if critErr == nil && refine {
					o.getLogger().Info("rewooagent2: critique rejected answer", "feedback", feedback, "refinementAttempts", o.refinementAttempts, "maxRefinementAttempts", o.maxRefinementAttempts)

					// Pass feedback to reviewer to update plan
					refineInput := fmt.Sprintf("Goal: %s\nCRITIQUE FEEDBACK: The previous answer was rejected because: %s\nPlease update the plan to address this.", input, feedback)
					// We force a review even if no failures
					if err := o.reviewAndRefinePlan(ctx, refineInput, intermediateSteps); err != nil {
						o.getLogger().Error("rewooagent2: failed to refine plan based on critique", "error", err)
					} else {
						// If plan was updated, resume execution loop
						if !o.allStepsDone() {
							o.getLogger().Info("rewooagent2: plan updated based on critique, resuming execution")
							// Recursively call Plan or return next batch?
							// Since we are inside Plan, we can just return the next batch
							nextBatch = o.getRunnableBatch()
							if len(nextBatch) > 0 {
								return nextBatch, nil, nil
							}
						} else {
							o.getLogger().Warn("rewooagent2: critique rejected but reviewer added no steps, returning original answer")
						}
					}
				}
			} else {
				reason := ""
				if hasInsufficientData {
					reason = "skipped: tool failure majority"
				} else {
					reason = "skipped: critique not enabled and not investigation"
				}
				o.getLogger().Info("rewooagent2: skipping critique", "enableCritique", o.request.EnableCritique, "isInvestigation", o.isInvestigationRequest, "reason", reason)
				o.saveCritique("answer_critique", o.request.Query, "", reason, "skipped")
			}
			return nil, finish, nil
		}
		if missingInfo != "" {
			// If the solver is requesting full artifact/log file content, suppress it.
			// These files are too large for the context window and the solver should
			// work with the available summaries. Force a finish with available data.
			if isSolverArtifactRequest(missingInfo) {
				o.getLogger().Warn("rewooagent2: solver requested full artifact/log content, suppressing to avoid context bloat — returning best-effort answer", "info", missingInfo)
				// Try to surface any Preview content already embedded in the scratchpad
				// (the tool's analysis the user actually wants). Only fall back to a
				// generic message if no preview is available — never expose raw shell
				// commands or internal file paths to the user.
				if preview := extractPreviewFromSteps(intermediateSteps); preview != "" {
					return nil, &NBAgentPlannerFinishAction{
						Data: preview,
						Log:  "Solver artifact request suppressed; returning embedded preview",
					}, nil
				}
				return nil, &NBAgentPlannerFinishAction{
					Data: "Analysis is based on the summarized information collected from the available data.",
					Log:  "Solver artifact request suppressed",
				}, nil
			}

			// If solver needs more info, we MUST try to get it instead of giving up.
			o.getLogger().Info("rewooagent2: solver requested more info, attempting to refine plan", "info", missingInfo)

			if o.planRefinementAttempts < o.maxPlanRefinementAttempts {
				o.planRefinementAttempts++
				refineInput := fmt.Sprintf("SOLVER BLOCKED: The solver needs more information to answer the user's request: %s.\nPlease UPDATE the plan to gather this specific information.", missingInfo)

				if err := o.reviewAndRefinePlan(ctx, refineInput, intermediateSteps); err != nil {
					o.getLogger().Error("rewooagent2: failed to refine plan for missing info", "error", err)
				} else {
					// Check if new steps were added
					nextBatch = o.getRunnableBatch()
					if len(nextBatch) > 0 {
						o.getLogger().Info("rewooagent2: plan updated for missing info, resuming execution")
						return nextBatch, nil, nil
					} else {
						o.getLogger().Warn("rewooagent2: solver requested info but planner added no steps")
					}
				}
			} else {
				o.getLogger().Warn("rewooagent2: max plan refinement attempts reached, returning missing info")
			}

			// Fallback: max refinements exhausted and solver still needs info.
			// Rather than returning the raw missing-info instructions as the user-facing answer
			// (which reads as step-by-step instructions rather than a proper response), ask
			// the solver one final time to synthesize a best-effort answer from what was collected.
			o.getLogger().Warn("rewooagent2: asking solver for best-effort final answer after exhausting refinements", "missingInfo", missingInfo)
			bestEffortCtx := fmt.Sprintf(
				"IMPORTANT: No further tool calls are possible. You MUST now provide a <final_answer> using ONLY the information already collected in the scratchpad. "+
					"Do NOT ask the user to run any commands or check anything themselves. Do NOT include raw file names, shell commands, or tool invocations in your answer. "+
					"Synthesize the findings from the completed steps into a clear, user-facing response. "+
					"If the task is incomplete, state what was found and that the system could not complete the full investigation. "+
					"The last piece of data that was needed but could not be obtained: %s",
				missingInfo,
			)
			if forcedFinish, _, _, solveErr := o.solver.Solve(ctx, o.request.Query, intermediateSteps, o.Notebook, bestEffortCtx); solveErr == nil && forcedFinish != nil {
				return nil, forcedFinish, nil
			}
			// Ultimate fallback if the forced solve also fails
			return nil, &NBAgentPlannerFinishAction{Data: missingInfo, Log: "Missing info requested by solver"}, nil
		}
	} else {
		// DEADLOCK DETECTED: No runnable steps, not waiting for client, but not all done.
		o.getLogger().Warn("rewooagent2: deadlock detected - no runnable steps but not all steps done", "graph", o.dumpGraphState())

		if o.refinementAttempts < o.maxRefinementAttempts {
			refineInput := fmt.Sprintf("DEADLOCK ALERT: The current plan is stuck. No steps can be executed because their dependencies (e.g., %s) are missing, failed, or unreachable. Please FIX the plan IDs or dependencies to resume execution.", o.dumpGraphState())

			if err := o.reviewAndRefinePlan(ctx, refineInput, intermediateSteps); err != nil {
				o.getLogger().Error("rewooagent2: failed to refine plan after deadlock", "error", err)
			} else {
				// Try getting a batch again after refinement
				nextBatch = o.getRunnableBatch()
				if len(nextBatch) > 0 {
					o.getLogger().Info("rewooagent2: deadlock resolved via plan refinement", "batch_size", len(nextBatch))
					return nextBatch, nil, nil
				}
			}
		}
	}

	deadlockErr := fmt.Errorf("rewooagent2: deadlock - no runnable steps but not all steps done. Graph state: %s", o.dumpGraphState())
	summaryFinish, summaryErr := o.summarizeAfterError(ctx, deadlockErr, intermediateSteps)
	return nil, summaryFinish, summaryErr
}

func (o *ReWooPlanner2) dumpGraphState() string {
	var sb strings.Builder
	for id, node := range o.executionGraph {
		if node.Status != StepStatusCompleted && node.Status != StepStatusSkipped {
			fmt.Fprintf(&sb, "[ID: %s, Status: %s, Deps: %v] ", id, node.Status, node.Step.Dependency)
		}
	}
	return sb.String()
}

func (o *ReWooPlanner2) getLogger() *slog.Logger {
	if o.ctx != nil && o.ctx.GetLogger() != nil {
		return o.ctx.GetLogger()
	}
	return slog.Default()
}

func (o *ReWooPlanner2) syncState(steps []NBAgentPlannerToolActionStep) {
	hasFailure := false
	for _, step := range steps {
		if node, ok := o.executionGraph[step.Action.ToolID]; ok {
			node.Output = step.Observation
			switch step.Status {
			case ToolStatusFailure:
				node.Status = StepStatusFailed
				hasFailure = true
				o.getLogger().Warn("rewooagent2: node execution failed", "id", step.Action.ToolID, "tool", step.Action.Tool)
			case ToolStatusWaitingForClient, ToolStatusWaiting:
				node.Status = StepStatusWaiting
				node.Step.Followup = step.Followup
				o.getLogger().Info("rewooagent2: node waiting for client execution or sub-agent input", "id", step.Action.ToolID, "tool", step.Action.Tool, "status", step.Status)
			default:
				node.Status = StepStatusCompleted
			}
			o.getLogger().Debug("rewooagent2: synced node status", "id", step.Action.ToolID, "status", node.Status)
		} else {
			// Restore node from history if it's missing from the graph (e.g., from previous turns)
			status := StepStatusCompleted
			switch step.Status {
			case ToolStatusFailure:
				status = StepStatusFailed
				hasFailure = true
			case ToolStatusWaitingForClient, ToolStatusWaiting:
				status = StepStatusWaiting
			}

			var condition *rewooPlannerCondition2
			if step.Action.Condition.Prompt != "" || step.Action.Condition.ExpectedResponse != "" {
				condition = &rewooPlannerCondition2{
					Prompt:           step.Action.Condition.Prompt,
					ExpectedResponse: step.Action.Condition.ExpectedResponse,
					AllowedResponses: step.Action.Condition.AllowedResponses,
				}
			}

			o.executionGraph[step.Action.ToolID] = &PlannerNode{
				Step: rewooPlannerStep2{
					ID:         step.Action.ToolID,
					Tool:       step.Action.Tool,
					Query:      step.Action.ToolInput,
					Reason:     step.Action.Log,
					Dependency: step.Action.Dependency,
					Condition:  condition,
					Followup:   step.Followup,
				},
				Status:     status,
				Output:     step.Observation,
				References: step.References,
				Iteration:  o.currentIteration, // Match current state if restored
			}
			o.getLogger().Info("rewooagent2: restored node from history", "id", step.Action.ToolID, "status", status)
		}
	}

	// If there were failures, propagate "Skipped" status to all dependent nodes
	if hasFailure {
		o.propagateSkips()
	}

	o.isGraphInitialized = true

	if err := o.updateStoredPlan(); err != nil {
		o.getLogger().Error("rewooagent2: failed to update plan status in DB", "error", err)
	}
}

func (o *ReWooPlanner2) propagateSkips() {
	changed := true
	for changed {
		changed = false
		for id, node := range o.executionGraph {
			if node.Status == StepStatusPending {
				for _, depID := range node.Step.Dependency {
					if depNode, ok := o.executionGraph[depID]; ok {
						// Only skip if the dependency failed or was skipped in the SAME or LATER iteration.
						// If it happened in an EARLIER iteration, we assume the current step is a recovery attempt.
						shouldSkip := (depNode.Status == StepStatusSkipped || depNode.Status == StepStatusFailed) &&
							node.Iteration <= depNode.Iteration

						if shouldSkip {
							node.Status = StepStatusSkipped
							node.Output = fmt.Sprintf("Skipped because dependency %s failed or was skipped", depID)
							if o.ctx != nil && o.getLogger() != nil {
								o.getLogger().Info("rewooagent2: cascading skip", "node", id, "reason", depID)
							}
							changed = true
							break
						}
					}
				}
			}
		}
	}
}

// basic keyword based searching
func (o *ReWooPlanner2) isToolCallTask(ctx context.Context, input string) (bool, error) {
	if o.isGraphInitialized && !IsConversationalQuery(strings.ToLower(input)) {
		return true, nil
	}
	o.getLogger().Info("rewooagent2: classifying task", "input", input)

	lowerInput := strings.ToLower(input)

	// 1. Keyword Match: Greetings, Identity, Help
	// Priority to conversational matches (e.g., "what are you")
	if IsConversationalQuery(lowerInput) {
		return false, nil
	}

	// 2. check investigation matches
	if IsInvestigationRequestTask(lowerInput) {
		return true, nil
	}

	// 3. check data retrieval matches
	if IsDataRetrievalOrActionRequest(lowerInput) {
		return true, nil
	}

	return false, nil
}

func (o *ReWooPlanner2) initializeGraph(ctx context.Context, input string, intermediateSteps []NBAgentPlannerToolActionStep) error {
	o.getLogger().Info("rewooagent2: initializing execution graph")

	var actions []NBAgentPlannerToolAction
	var thoughts string
	var err error

	// 1. Initial Planning & Structural Refinement Loop
	var plannerFinish *NBAgentPlannerFinishAction
	for o.refinementAttempts < o.maxRefinementAttempts {
		actions, plannerFinish, thoughts, err = o.generateInitialPlan(ctx, input, intermediateSteps)

		// If the planner returned a non-XML response (parsed as finish instead of plan actions),
		// treat it as a format error and retry — the model understood the task but used the
		// wrong output format (JSON, markdown, etc. instead of XML <plan_response>).
		if err == nil && len(actions) == 0 && plannerFinish != nil {
			o.getLogger().Warn("rewooagent2: planner returned non-XML response instead of a plan, requesting format correction",
				"response_preview", plannerFinish.Data[:min(300, len(plannerFinish.Data))])
			input = fmt.Sprintf("Goal: %s\n\nFORMAT ERROR: You returned a non-XML response. Your ENTIRE output MUST be a ```xml ... ``` block with a <plan_response> root element containing <thought> and <plan> with <step> elements. Do NOT output JSON, markdown, or plain text. Generate the XML plan now.", input)
			o.refinementAttempts++
			continue
		}

		if err == nil {
			break
		}

		if strings.Contains(err.Error(), "structural errors") {
			o.getLogger().Warn("rewooagent2: initial plan has structural errors, requesting immediate fix", "error", err)
			input = fmt.Sprintf("Goal: %s\n\nSTRUCTURAL ERROR: The plan you generated is invalid: %s. Please generate a NEW plan with correct dependency IDs.", input, err.Error())
			o.refinementAttempts++
			continue
		}

		if strings.Contains(err.Error(), "could not parse plan") {
			o.getLogger().Warn("rewooagent2: initial plan could not be parsed, requesting immediate fix", "error", err)
			input = fmt.Sprintf("Goal: %s\n\nPARSING ERROR: Your response was malformed and could not be parsed: %s. Please provide the FULL and CORRECTED XML response now.", input, err.Error())
			o.refinementAttempts++
			continue
		}

		// Other hard errors
		return err
	}

	// Final check if loop exited without success
	if err != nil {
		if strings.Contains(err.Error(), "structural errors") {
			o.getLogger().Error("rewooagent2: failed to generate structurally sound plan after multiple attempts", "error", err)
			// Proceed anyway with the "Safety Kept" (blocked) plan, deadlock recovery will catch it later if it runs.
		} else if strings.Contains(err.Error(), "could not parse plan") {
			o.getLogger().Error("rewooagent2: failed to parse plan after multiple attempts", "error", err)
			return err
		}
	}

	// 2. Validate the initial plan if critique is enabled
	if o.request.EnableCritique || o.isInvestigationRequest {
		availableTools := FilterAndInjectDefaultTools(o.request.AccountId, o.nbAgent, o.systemMessage+o.request.SkillListsMenu, o.nbAgent.GetSupportedTools(o.ctx), o.request.Capabilities)
		toolNames := reActPromptToolNames(availableTools)
		toolDescriptions := reActPromptToolDescriptions(availableTools)

		// Convert actions to simple string for critiquer
		var planStr strings.Builder
		for _, a := range actions {
			fmt.Fprintf(&planStr, "- Step %s (%s): %s (Reason: %s)\n", a.ToolID, a.Tool, a.ToolInput, a.Log)
		}

		reject, feedback, err := o.planCritiquer.CritiquePlan(ctx, input, planStr.String(), toolNames, toolDescriptions, o.maxPlanSteps)

		// Store plan critique decision
		if err == nil {
			decision := lo.Ternary(reject, "reject", "accept")
			o.saveCritique("plan_critique", input, planStr.String(), feedback, decision)
		}

		if err == nil && reject {
			o.getLogger().Info("rewooagent2: initial plan rejected by critiquer, refining", "feedback", feedback)
			refineInput := fmt.Sprintf("Goal: %s\nPLAN CRITIQUE FEEDBACK: The initial plan was rejected because: %s\nPlease generate an improved plan.", input, feedback)

			// Update actions using refined plan logic
			refinedActions, _, refinedThoughts, err := o.generateInitialPlan(ctx, refineInput, intermediateSteps)
			if err == nil {
				actions = refinedActions
				thoughts = refinedThoughts
			}
		}
	}

	plannerId, err := o.storeGeneratedPlan(input, thoughts, actions)
	if err != nil {
		o.getLogger().Error("rewooagent2: unable to save generated plan", "error", err)
	} else {
		o.plannerAgentID = plannerId
	}

	for _, action := range actions {
		if action.Tool == plannerDummyTool {
			continue
		}
		o.executionGraph[action.ToolID] = &PlannerNode{
			Step: rewooPlannerStep2{
				ID:         action.ToolID,
				Tool:       action.Tool,
				Query:      action.ToolInput,
				Reason:     action.Log,
				Dependency: action.Dependency,
			},
			Status:     StepStatusPending,
			References: []toolcore.NBToolResponseReference{},
			Iteration:  0,
		}
	}
	o.getLogger().Info("rewooagent2: initialized execution graph", "plannerId", plannerId)
	o.isGraphInitialized = true
	return nil
}

func (o *ReWooPlanner2) generateInitialPlan(ctx context.Context, input string, intermediateSteps []NBAgentPlannerToolActionStep) ([]NBAgentPlannerToolAction, *NBAgentPlannerFinishAction, string, error) {
	// This uses the old parsing and LLM logic to get the first plan
	// Only pass variables that change per-call here.
	// today, conversation_context are set once in PartialVariables at prompt construction
	// so the system message stays byte-for-byte identical across requests, enabling
	// LLM provider caching (Google AI / Anthropic).
	// notebook is per-call because o.Notebook is mutated when the LLM emits <update_notebook>.
	// task_context (QueryContext) is per-request and is set in PartialVariables at construction.
	fullInputs := map[string]any{
		"input":    input,
		"notebook": o.Notebook,
	}

	prompt, err := o.prompt.FormatPrompt(fullInputs)
	if err != nil {
		return nil, nil, "", err
	}

	mcList := []llms.MessageContent{}
	for _, msg := range prompt.Messages() {
		mcList = append(mcList, llms.MessageContent{
			Role:  msg.GetType(),
			Parts: []llms.ContentPart{llms.TextContent{Text: msg.GetContent()}},
		})
	}

	// Attach images from the current request to the last human message
	mcList = AppendImagesToLastHumanMessage(mcList, o.request.Images)

	result, err := GenerateAndTrackLLMContent(o.ctx, o.request.UserId, o.request.AccountId, o.request.ConversationId, o.request.MessageId, o.request.AgentId, false, mcList, true,
		llms.WithTemperature(0.0))
	if err != nil {
		return nil, nil, "", err
	}

	actions, finish, thoughts, err := o.parseOutput(result, intermediateSteps)
	return actions, finish, thoughts, err
}

func (o *ReWooPlanner2) findNBTool(name string) toolcore.NBTool {
	for _, t := range o.tools {
		if strings.EqualFold(t.Name(), name) {
			return t
		}
	}
	return nil
}

func (o *ReWooPlanner2) isSerializationRequiredTool(tool toolcore.NBTool) bool {
	if tool == nil {
		return false
	}

	// 1. Determine if this tool (or its inner tools) requires configuration
	var configurableTools []toolcore.NBTool
	if _, ok := tool.(toolcore.NBToolConfig); ok {
		configurableTools = append(configurableTools, tool)
	}

	// If it's an agent tool, check its supported tools
	if agentTool, ok := tool.(AgentTool); ok {
		agent := agentTool.GetAgent(o.ctx)
		if agent != nil {
			innerTools := agent.GetSupportedTools(o.ctx)
			for _, it := range innerTools {
				if _, ok := it.(toolcore.NBToolConfig); ok {
					configurableTools = append(configurableTools, it)
				}
			}
		}
	}

	if len(configurableTools) == 0 {
		return false // No configuration required at all
	}

	// 2. Check if all configurable tools are already resolved in QueryConfig
	if o.request.QueryConfig.ToolConfigs != nil {
		toolConfigs := o.request.QueryConfig.ToolConfigs
		allResolved := true
		for _, ct := range configurableTools {
			if val := toolConfigs[ct.Name()]; val == "" {
				allResolved = false
				break
			}
		}
		if allResolved {
			return false // All choices already made
		}
	}

	// 3. Check if any of the UNRESOLVED tools have multiple configs
	// We only need to serialize if there's an actual choice to be made that hasn't been made yet.
	for _, ct := range configurableTools {
		// Skip if already resolved
		if o.request.QueryConfig.ToolConfigs != nil {
			if val := o.request.QueryConfig.ToolConfigs[ct.Name()]; val != "" {
				continue
			}
		}

		configs, err := toolcore.ListToolConfigs(o.ctx, o.request.AccountId, ct)
		if err != nil {
			o.getLogger().Error("rewooagent2: failed to list tool configs", "tool", ct.Name(), "error", err)
			return true // Be safe
		}
		if len(configs) > 1 {
			return true // Found at least one tool that will trigger a prompt
		}
	}

	return false
}

// extractPreviewFromSteps scans completed tool observations for a saved-artifact "Preview:" section.
// Only considers successful steps whose observation matches the overflow wrapper pattern
// ("Output large ... Saved to ...\nPreview: ...") emitted when a tool saves large output to a file.
// This avoids mistaking a failed or ordinary observation for an artifact preview.
func extractPreviewFromSteps(steps []NBAgentPlannerToolActionStep) string {
	for i := len(steps) - 1; i >= 0; i-- {
		step := steps[i]
		if step.Status == ToolStatusFailure || step.Status == ToolStatusWaiting || step.Status == ToolStatusWaitingForClient {
			continue
		}
		obs := step.Observation
		if !strings.Contains(obs, "Output large (") || !strings.Contains(obs, "Saved to ") {
			continue
		}
		if idx := strings.Index(obs, "\nPreview: "); idx != -1 {
			return strings.TrimSpace(obs[idx+len("\nPreview: "):])
		}
	}
	return ""
}

func (o *ReWooPlanner2) getRunnableBatch() []NBAgentPlannerToolAction {
	var batch []NBAgentPlannerToolAction
	// Track tool names that should be serialized to avoid redundant config prompts or race conditions
	serializedInBatch := make(map[string]bool)
	// Cache serialization requirement per tool name to avoid redundant expensive calls
	shouldSerializeTool := make(map[string]bool)

	for id, node := range o.executionGraph {
		if node.Status == StepStatusPending {
			depsMet := true
			for _, depID := range node.Step.Dependency {
				if strings.EqualFold(depID, "none") || strings.EqualFold(depID, "null") || depID == "" {
					continue
				}
				if depNode, ok := o.executionGraph[depID]; ok {
					if depNode.Status == StepStatusCompleted {
						continue
					}
					// Allow execution if the dependency failed or was skipped but this step is from a later iteration (recovery attempt)
					if (depNode.Status == StepStatusFailed || depNode.Status == StepStatusSkipped) && node.Iteration > depNode.Iteration {
						continue
					}
					depsMet = false
					break
				} else {
					// Dependency not in graph? Assume it's an external step or completed previously
					// For now, let's be strict
					o.getLogger().Warn("rewooagent2: dependency not found in graph", "node", id, "dep", depID)
					depsMet = false
					break
				}
			}

			if depsMet {
				// SERIALIZATION LOGIC:
				// Some tools (like postgres) often require interactive config. Running them in parallel
				// can cause multiple redundant prompts to the user. We only allow one instance
				// of these tools per batch if multiple configs are available and not yet resolved.
				needsSerialization, cached := shouldSerializeTool[node.Step.Tool]
				if !cached {
					toolObj := o.findNBTool(node.Step.Tool)
					needsSerialization = o.isSerializationRequiredTool(toolObj)
					shouldSerializeTool[node.Step.Tool] = needsSerialization
				}

				if needsSerialization {
					if serializedInBatch[node.Step.Tool] {
						continue // Skip this one for now, will run in next batch
					}
					serializedInBatch[node.Step.Tool] = true
				}

				// Apply macro substitution to the query before execution
				query := common.SubstituteDateMacros(node.Step.Query)

				batch = append(batch, NBAgentPlannerToolAction{
					ToolID:     node.Step.ID,
					Tool:       node.Step.Tool,
					ToolInput:  query,
					Log:        node.Step.Reason,
					Dependency: node.Step.Dependency,
				})
				node.Status = StepStatusRunning
			}
		}
	}
	if len(batch) > 0 {
		o.isGraphInitialized = true
	}
	return batch
}

func (o *ReWooPlanner2) allStepsDone() bool {
	for _, node := range o.executionGraph {
		if node.Status == StepStatusPending || node.Status == StepStatusRunning || node.Status == StepStatusWaiting {
			return false
		}
	}
	return true
}

func (o *ReWooPlanner2) reviewAndRefinePlan(ctx context.Context, input string, intermediateSteps []NBAgentPlannerToolActionStep) error {
	// Only review if there was a failure or we are at a checkpoint
	hasFailure := false
	for _, step := range intermediateSteps {
		if step.Status == ToolStatusFailure {
			hasFailure = true
			break
		}
	}

	// Determine if we should run the reviewer.
	// Skip review when all steps succeeded and few steps remain — saves 8-14 sec per batch.
	// "Pivot after success" cases (e.g. found ARN, now check Health) are handled by
	// the solver→missingInfo→review path which passes "SOLVER BLOCKED" / "CRITIQUE FEEDBACK" in input.
	remainingCount := 0
	for _, node := range o.executionGraph {
		if node.Status == StepStatusPending {
			remainingCount++
		}
	}
	isCritiqueFeedback := strings.Contains(input, "CRITIQUE FEEDBACK") || strings.Contains(input, "SOLVER BLOCKED")
	isSkillLoaded := strings.Contains(input, "SKILL LOADED")

	shouldReview := hasFailure || isCritiqueFeedback || isSkillLoaded || remainingCount > 3
	if !shouldReview {
		o.getLogger().Info("rewooagent2: skipping plan review (all steps succeeded, few remaining)",
			"hasFailure", hasFailure, "remainingCount", remainingCount)
		return nil
	}

	if o.refinementAttempts >= o.maxRefinementAttempts {
		o.getLogger().Warn("rewooagent2: max refinement attempts reached, skipping further review")
		return nil
	}

	if hasFailure {
		input += "\n\nSYSTEM ALERT: One or more steps in the plan FAILED. You MUST update the plan to fix these errors (e.g., correct syntax, try a different region/cluster). Do not ignore the failures."
	}

	o.getLogger().Info("rewooagent2: reviewing plan", "hasFailure", hasFailure, "isCritique", strings.Contains(input, "CRITIQUE FEEDBACK"))

	// Split reviewer prompt into system (stable rules) + human (dynamic context)
	// so the system prefix is cacheable across reviewer iterations.
	reviewerPrompt := prompts_repo.GetPrompt(prompts_repo.PromptPlannerRewoo2Reviewer)
	reviewerDynamic := `<task_input>
{{.input}}
</task_input>

<question_type>
{{.question_type}}
</question_type>

<current_progress>
{{.scratchpad}}
</current_progress>

<remaining_plan>
{{.remaining_plan}}
</remaining_plan>

<notebook_content>
{{.notebook}}
</notebook_content>`
	tmpl := prompts.NewChatPromptTemplate([]prompts.MessageFormatter{
		prompts.NewSystemMessagePromptTemplate(reviewerPrompt, []string{"tool_names", "time_handling_rules"}),
		prompts.NewHumanMessagePromptTemplate(reviewerDynamic, []string{"input", "scratchpad", "remaining_plan", "notebook", "question_type"}),
	})

	remainingSteps := []map[string]any{}
	for id, node := range o.executionGraph {
		if node.Status == StepStatusPending {
			remainingSteps = append(remainingSteps, map[string]any{
				"id":         id,
				"tool":       node.Step.Tool,
				"query":      node.Step.Query,
				"dependency": node.Step.Dependency,
			})
		}
	}
	remainingPlanJson, _ := common.MarshalJson(remainingSteps)

	fullInputs := map[string]any{
		"input":               input,
		"scratchpad":          ConstructScratchPad(intermediateSteps, ScratchpadContext{Ctx: o.ctx, Request: o.request, Tracker: o.compressionTracker}),
		"remaining_plan":      string(remainingPlanJson),
		"notebook":            o.Notebook,
		"tool_names":          reActPromptToolNames(o.tools),
		"question_type":       lo.Ternary(o.isInvestigationRequest, "investigation", "query"),
		"time_handling_rules": prompts_repo.GetPrompt(prompts_repo.PromptSharedTimeHandlingRules),
	}

	formattedPrompt, err := tmpl.FormatPrompt(fullInputs)
	if err != nil {
		return err
	}

	mcList := []llms.MessageContent{}
	for _, msg := range formattedPrompt.Messages() {
		mcList = append(mcList, llms.MessageContent{
			Role:  msg.GetType(),
			Parts: []llms.ContentPart{llms.TextContent{Text: msg.GetContent()}},
		})
	}

	result, err := GenerateAndTrackLLMContent(o.ctx, o.request.UserId, o.request.AccountId, o.request.ConversationId, o.request.MessageId, o.request.AgentId, false, mcList, true,
		llms.WithTemperature(0.0))
	if err != nil {
		return err
	}

	if len(result.Choices) == 0 {
		return fmt.Errorf("rewooagent2: no choices returned from LLM during plan review")
	}

	output := result.Choices[0].Content

	// Update notebook if tag is present
	if strings.Contains(output, "<update_notebook>") {
		notebookContent := common.XmlExtractTagContent(output, "update_notebook")
		if notebookContent != "" {
			o.Notebook = notebookContent
			if o.ctx != nil && o.getLogger() != nil {
				o.getLogger().Info("rewooagent2: notebook updated by reviewer", "content_length", len(notebookContent))
			}
		} else {
			o.getLogger().Info("rewooagent2: notebook tag found but content is empty")
		}
	} else {
		o.getLogger().Info("rewooagent2: no notebook update tag in reviewer output",
			"output_preview", output[:min(200, len(output))],
			"current_notebook_len", len(o.Notebook))
	}

	// Robust XML extraction using regex
	reviewXml := common.XmlRegexExtract(output, regexp.MustCompile("(?s)```xml\n(.*?)\n```"))
	if reviewXml == "" {
		if strings.HasPrefix(output, "<thought>") || strings.HasPrefix(output, "<review_response>") {
			reviewXml = output
		}
	}

	if reviewXml == "" {
		return fmt.Errorf("rewooagent2: unable to find XML block in reviewer output")
	}

	reviewXml = common.XmlSanitize(reviewXml, "review_response")

	var review rewooReviewResponse2
	err = xml.Unmarshal([]byte(reviewXml), &review)
	if err != nil {
		o.getLogger().Error("rewooagent2: unable to unmarshal review XML, attempting regex fallback", "error", err.Error(), "data", reviewXml)

		// Fallback: extract action and thought via regex if full unmarshal fails
		review.Action = common.XmlExtractTagContent(reviewXml, "action")
		review.Thought = common.XmlExtractTagContent(reviewXml, "thought")

		if review.Action == "update" {
			// Try to extract steps manually
			stepMatches := rewooPlannerStep2Regex2.FindAllStringSubmatch(reviewXml, -1)
			for _, sm := range stepMatches {
				stepContent := sm[1]
				step := rewooPlannerStep2{
					ID:     common.XmlRegexExtract(stepContent, rewooPlannerIDRegex2),
					Tool:   common.XmlRegexExtract(stepContent, rewooPlannerToolRegex2),
					Query:  common.XmlExtractCDATA(common.XmlRegexExtract(stepContent, rewooPlannerQueryRegex2)),
					Reason: common.XmlExtractCDATA(common.XmlRegexExtract(stepContent, rewooPlannerReasonRegex2)),
				}
				dependency := common.XmlRegexExtract(stepContent, rewooPlannerDependencyRegex2)
				if dependency != "" {
					// Split by comma, newline, or semicolon
					f := func(c rune) bool {
						return c == ',' || c == '\n' || c == ';' || c == '\r' || c == ' '
					}

					parts := strings.FieldsFunc(dependency, f)
					for _, p := range parts {
						p = strings.TrimSpace(p)
						if p != "" {
							step.Dependency = append(step.Dependency, p)
						}
					}
				}
				if step.ID != "" && step.Tool != "" {
					review.UpdatedPlan = append(review.UpdatedPlan, step)
				}
			}
		}

		if review.Action == "" || (strings.ToLower(review.Action) == "update" && len(review.UpdatedPlan) == 0) {
			reason := ""
			if !strings.Contains(output, "<review_response>") {
				reason = "missing <review_response> root tag"
			} else if !strings.Contains(output, "<action>") {
				reason = "missing <action> tag"
			} else if strings.Contains(output, "update") && !strings.Contains(output, "<updated_plan>") {
				reason = "action is 'update' but <updated_plan> tag is missing"
			} else if err != nil {
				reason = err.Error()
			} else {
				reason = "action was 'update' but no valid steps were parsed"
			}

			if o.refinementAttempts < o.maxRefinementAttempts {
				o.refinementAttempts++
				o.getLogger().Warn("rewooagent2: reviewer output could not be parsed, retrying", "error", reason)
				refineInput := fmt.Sprintf("Goal: %s\n\nPARSING ERROR in your previous review response: %s. Please provide the FULL and CORRECTED XML response now.", input, reason)
				return o.reviewAndRefinePlan(ctx, refineInput, intermediateSteps)
			}
			return fmt.Errorf("rewooagent2: failed to parse reviewer output: %s", reason)
		}
	}

	o.getLogger().Info("rewooagent2: reviewer decision", "action", review.Action, "thought", review.Thought)

	// Store plan review decision
	o.saveCritique("plan_review", input, review.Thought, review.Action, strings.ToLower(review.Action))

	switch strings.ToLower(review.Action) {
	case "complete":
		// Mark all remaining steps as skipped
		for _, node := range o.executionGraph {
			if node.Status == StepStatusPending {
				node.Status = StepStatusSkipped
			}
		}
	case "update":
		o.refinementAttempts++
		o.currentIteration++

		// 1. Remove pending or running steps to make room for the update.
		// We keep Completed, Failed, and Skipped steps as they provide context and anchors for dependencies.
		for id, node := range o.executionGraph {
			if node.Status == StepStatusPending || node.Status == StepStatusRunning {
				delete(o.executionGraph, id)
			}
		}

		// 2. Add metadata step to explain the change
		metaID := fmt.Sprintf("!update_v%d", o.currentIteration)
		o.executionGraph[metaID] = &PlannerNode{
			Step: rewooPlannerStep2{
				ID:         metaID,
				Tool:       "plan_update",
				Reason:     review.Thought, // The reviewer's explanation
				Dependency: []string{},     // Ensure empty list, not null
			},
			Status:    StepStatusCompleted,
			Iteration: o.currentIteration,
		}

		// 3. Add new steps with structural validation
		updatedActions := lo.Map(review.UpdatedPlan, func(s rewooPlannerStep2, _ int) NBAgentPlannerToolAction {
			return NBAgentPlannerToolAction{
				ToolID:     s.ID,
				Tool:       s.Tool,
				ToolInput:  s.Query,
				Log:        s.Reason,
				Dependency: s.Dependency,
			}
		})

		// Validate structure immediately
		var structuralErr error
		updatedActions, structuralErr = o.validateAndCorrectActions(updatedActions)
		if structuralErr != nil && o.refinementAttempts < o.maxRefinementAttempts {
			o.getLogger().Warn("rewooagent2: reviewer generated structurally unsound update, retrying", "error", structuralErr)
			refineInput := fmt.Sprintf("Goal: %s\n\nSTRUCTURAL ERROR in your previous update: %s. Please provide a corrected update with valid dependency IDs.", input, structuralErr.Error())
			return o.reviewAndRefinePlan(ctx, refineInput, intermediateSteps)
		}

		for _, action := range updatedActions {
			step := rewooPlannerStep2{
				ID:         action.ToolID,
				Tool:       action.Tool,
				Query:      action.ToolInput,
				Reason:     action.Log,
				Dependency: action.Dependency,
			}

			// Check if this step ID already exists and is in a terminal state (Completed only)
			if existingNode, exists := o.executionGraph[step.ID]; exists {
				if existingNode.Status == StepStatusCompleted {
					o.getLogger().Info("rewooagent2: skipping overwrite of already completed step", "id", step.ID)
					continue
				}
			}

			o.executionGraph[step.ID] = &PlannerNode{
				Step:      step,
				Status:    StepStatusPending,
				Iteration: o.currentIteration,
			}
		}

		if err := o.updateStoredPlan(); err != nil {
			o.getLogger().Error("rewooagent2: failed to update stored plan", "error", err)
		}
	case "continue":
		// Do nothing, proceed with original plan
	}

	return nil
}

func (o *ReWooPlanner2) Marshal() ([]byte, error) {
	data := map[string]any{
		"executionGraph":         o.executionGraph,
		"isGraphInitialized":     o.isGraphInitialized,
		"plannerAgentID":         o.plannerAgentID,
		"currentIteration":       o.currentIteration,
		"notebook":               o.Notebook,
		"planRefinementAttempts": o.planRefinementAttempts,
		"refinementAttempts":     o.refinementAttempts,
	}
	return common.MarshalJson(data)
}

func (o *ReWooPlanner2) Unmarshal(data []byte) error {
	var state struct {
		ExecutionGraph         map[string]*PlannerNode `json:"executionGraph"`
		IsGraphInitialized     bool                    `json:"isGraphInitialized"`
		PlannerAgentID         string                  `json:"plannerAgentID"`
		CurrentIteration       int                     `json:"currentIteration"`
		Notebook               string                  `json:"notebook"`
		PlanRefinementAttempts int                     `json:"planRefinementAttempts"`
		RefinementAttempts     int                     `json:"refinementAttempts"`
	}
	err := common.UnmarshalJson(data, &state)
	if err != nil {
		return err
	}
	o.executionGraph = state.ExecutionGraph
	o.isGraphInitialized = state.IsGraphInitialized
	o.plannerAgentID = state.PlannerAgentID
	o.currentIteration = state.CurrentIteration
	o.Notebook = state.Notebook
	o.planRefinementAttempts = state.PlanRefinementAttempts
	o.refinementAttempts = state.RefinementAttempts
	return nil
}

func (o *ReWooPlanner2) GetTools() []toolcore.NBTool {
	return o.tools
}

func (o *ReWooPlanner2) GetNotebook() string {
	return o.Notebook
}

func (o *ReWooPlanner2) findTool(name string) (string, bool) {
	name = strings.TrimSpace(name)
	if name == plannerDummyTool || strings.EqualFold(name, "none") || strings.EqualFold(name, "null") || strings.EqualFold(name, "plan_update") {
		return name, true
	}
	// Check exact match first
	for _, t := range o.tools {
		if t.Name() == name {
			return name, true
		}
	}
	// Check case-insensitive match
	for _, t := range o.tools {
		if strings.EqualFold(t.Name(), name) {
			return t.Name(), true
		}
	}
	// Check aliases (e.g., model hallucinated "gcp_cloud_logging" but actual tool is "gcp")
	for _, t := range o.tools {
		if aliased, ok := t.(interface{ GetNameAliases() []string }); ok {
			for _, alias := range aliased.GetNameAliases() {
				if strings.EqualFold(alias, name) {
					return t.Name(), true
				}
			}
		}
	}

	return "", false
}

func (o *ReWooPlanner2) validateAndCorrectActions(actions []NBAgentPlannerToolAction) ([]NBAgentPlannerToolAction, error) {
	// Map lowercase IDs to their original case to ensure executionGraph lookup works
	lowToOrigIDs := make(map[string]string)
	for _, a := range actions {
		lowToOrigIDs[strings.ToLower(a.ToolID)] = a.ToolID
	}

	// ALSO include IDs from the existing execution graph (completed/failed/skipped steps)
	// so that new steps can correctly depend on them.
	for id := range o.executionGraph {
		lowToOrigIDs[strings.ToLower(id)] = id
	}

	var structuralErrors []string

	for i := range actions {
		// 1. Correct tool name
		correctName, found := o.findTool(actions[i].Tool)
		if found {
			actions[i].Tool = correctName
		} else {
			o.getLogger().Warn("rewooagent2: tool generated by planner not found in allowed tools", "tool", actions[i].Tool)
		}

		// 2. Validate and fuzzy-fix dependencies
		validDeps := []string{}
		for _, dep := range actions[i].Dependency {
			lowDep := strings.ToLower(dep)
			if origID, exists := lowToOrigIDs[lowDep]; exists {
				validDeps = append(validDeps, origID)
				continue
			} else if lowDep == "none" || lowDep == "null" || lowDep == "" {
				validDeps = append(validDeps, dep)
				continue
			}

			// Try fuzzy matching (including executionGraph)
			f := func(c rune) bool {
				return c == ',' || c == '\n' || c == ';' || c == '\r' || c == ' '
			}
			parts := strings.FieldsFunc(dep, f)
			fuzzyMatched := []string{}
			for _, p := range parts {
				p = strings.TrimSpace(p)
				if p == "" {
					continue
				}
				lowP := strings.ToLower(p)
				if origID, exists := lowToOrigIDs[lowP]; exists {
					fuzzyMatched = append(fuzzyMatched, origID)
				}
			}

			if len(fuzzyMatched) > 0 {
				validDeps = append(validDeps, fuzzyMatched...)
				o.getLogger().Info("rewooagent2: fuzzy-fixed dependency IDs", "original", dep, "fixed", fuzzyMatched)
			} else {
				structuralErrors = append(structuralErrors, fmt.Sprintf("Step %s depends on %s which does not exist in the plan", actions[i].ToolID, dep))
				validDeps = append(validDeps, dep)
			}
		}
		actions[i].Dependency = validDeps
	}

	if len(structuralErrors) > 0 {
		return actions, fmt.Errorf("structural errors: %s", strings.Join(structuralErrors, "; "))
	}
	return actions, nil
}

func (o *ReWooPlanner2) parseOutput(contentResp *llms.ContentResponse, previousSteps []NBAgentPlannerToolActionStep) ([]NBAgentPlannerToolAction, *NBAgentPlannerFinishAction, string, error) {
	action, finish, thoughts, err := o.parseOutputInternal(contentResp, previousSteps)
	if _, ok := o.nbAgent.(NBAgentExecutorLlmResponseHandler); ok {
		handler := o.nbAgent.(NBAgentExecutorLlmResponseHandler)
		action, finish, err = handler.UpdateExecutorLlmResponse(action, finish, err)
		return action, finish, thoughts, err
	}
	return action, finish, thoughts, err
}

func (o *ReWooPlanner2) parseOutputInternal(contentResp *llms.ContentResponse, previousSteps []NBAgentPlannerToolActionStep) ([]NBAgentPlannerToolAction, *NBAgentPlannerFinishAction, string, error) {
	if len(contentResp.Choices) == 0 {
		return nil, nil, "", fmt.Errorf("rewooagent2: no choices returned from LLM")
	}
	choice := contentResp.Choices[0]
	output := choice.Content
	output = strings.TrimSpace(output)

	// Update notebook if tag is present
	if strings.Contains(output, "<update_notebook>") {
		notebookContent := common.XmlExtractTagContent(output, "update_notebook")
		if notebookContent != "" {
			o.Notebook = notebookContent
			if o.ctx != nil && o.getLogger() != nil {
				o.getLogger().Info("rewooagent2: notebook updated during initial planning", "content_length", len(notebookContent))
			}
		} else {
			o.getLogger().Info("rewooagent2: notebook tag found but content is empty during initial planning")
		}
	} else {
		o.getLogger().Info("rewooagent2: no notebook update tag in planner output",
			"output_preview", output[:min(200, len(output))],
			"current_notebook_len", len(o.Notebook))
	}

	// sometimes, planner generates same plan with exact same keys
	// which causes issues in downstream as expect plankeys to be unique for each iteration
	existingPlanKeys := lo.Map(previousSteps, func(step NBAgentPlannerToolActionStep, _ int) string {
		return strings.ToLower(step.Action.ToolID)
	})
	newPlanKeyMap := map[string]string{}

	// Check for XML plan
	var thoughts string
	xmlPlanContent := common.XmlRegexExtract(output, rewooPlannerXMLRegex2)

	if xmlPlanContent == "" {
		// Fallback if not in ```xml block, try to parse directly if it starts with <plan> or <thought>
		if strings.HasPrefix(output, "<thought>") || strings.HasPrefix(output, "<plan>") || strings.HasPrefix(output, "<plan_response>") {
			xmlPlanContent = output
		} else {
			// If no XML block and not starting with XML, assume it's a final answer or unparseable
			return nil, &NBAgentPlannerFinishAction{
					Log:  output,
					Data: output,
				},
				"", nil
		}
	}

	// Surgically extract just the <plan_response> element to strip any leading <thought> tags
	// that Gemini's native thinking mode may inject before the root element.
	if planResponseMatch := rewooPlannerPlanResponseRegex2.FindString(xmlPlanContent); planResponseMatch != "" {
		xmlPlanContent = planResponseMatch
	}

	xmlPlanContent = common.XmlSanitize(xmlPlanContent, "plan_response")

	var plan rewooPlan2
	err := xml.Unmarshal([]byte(xmlPlanContent), &plan)
	if err != nil {
		// Attempt 1: fix bare & characters and retry
		escapedContent := common.XmlEscapeAmpersands(xmlPlanContent)
		if escapedContent != xmlPlanContent {
			o.getLogger().Info("rewooagent: retrying XML unmarshal after escaping ampersands")
			if retryErr := xml.Unmarshal([]byte(escapedContent), &plan); retryErr == nil {
				err = nil
				xmlPlanContent = escapedContent
			}
		}
	}
	if err != nil {
		// Attempt 2: fix mismatched closing tag identified in the error message and retry
		if fixedContent, fixed := common.XmlFixMismatchedTag(xmlPlanContent, err.Error()); fixed {
			o.getLogger().Info("rewooagent: retrying XML unmarshal after fixing mismatched tag", "error", err.Error())
			if retryErr := xml.Unmarshal([]byte(fixedContent), &plan); retryErr == nil {
				err = nil
				xmlPlanContent = fixedContent
			}
		}
	}
	// If unmarshal succeeded but found no steps, the model likely omitted the required <plan>
	// wrapper (xml:"plan>step" requires <plan><step> nesting) or used wrong sub-tags like
	// <description>. Treat this the same as a parse failure so the regex fallback runs.
	if err == nil && len(plan.Steps) == 0 {
		o.getLogger().Warn("rewooagent: unmarshal succeeded but no steps found, attempting regex fallback (model may have omitted <plan> wrapper or used wrong step schema)")
		err = fmt.Errorf("unmarshal produced no steps")
	}

	if err != nil {
		o.getLogger().Error("rewooagent: unable to unmarshal XML plan, attempting regex fallback", "error", err.Error(), "data", xmlPlanContent)

		// Fallback: Try to extract thoughts and actions using regex if XML unmarshalling fails
		thoughts = common.XmlRegexExtract(xmlPlanContent, rewooPlannerThoughtsRegex2)

		// Attempt to extract steps using regex
		stepMatches := rewooPlannerStep2Regex2.FindAllStringSubmatch(xmlPlanContent, -1)

		extractedActions := []NBAgentPlannerToolAction{}
		for _, sm := range stepMatches {
			stepContent := sm[1]

			action := NBAgentPlannerToolAction{}
			action.ToolID = common.XmlRegexExtract(stepContent, rewooPlannerIDRegex2)
			action.Tool = common.XmlRegexExtract(stepContent, rewooPlannerToolRegex2)
			action.ToolInput = common.XmlExtractCDATA(common.XmlRegexExtract(stepContent, rewooPlannerQueryRegex2))
			action.Log = common.XmlExtractCDATA(common.XmlRegexExtract(stepContent, rewooPlannerReasonRegex2))
			dependency := common.XmlRegexExtract(stepContent, rewooPlannerDependencyRegex2)
			if dependency != "" {
				// Split by comma, newline, or semicolon
				f := func(c rune) bool {
					return c == ',' || c == '\n' || c == ';' || c == '\r' || c == ' '
				}
				parts := strings.FieldsFunc(dependency, f)
				for _, p := range parts {
					p = strings.TrimSpace(p)
					if p != "" {
						action.Dependency = append(action.Dependency, p)
					}
				}
			}

			actionCondition := NBAgentPlannerToolActionCondition{}
			actionCondition.Prompt = common.XmlRegexExtract(stepContent, rewooPlannerCondition2PromptRegex2)
			actionCondition.ExpectedResponse = common.XmlRegexExtract(stepContent, rewooPlannerCondition2ExpectedResponseRegex2)
			allowedResponses := common.XmlRegexExtract(stepContent, rewooPlannerCondition2AllowedResponsesRegex2)
			if allowedResponses != "" {
				actionCondition.AllowedResponses = strings.Split(allowedResponses, ",")
			}
			action.Condition = actionCondition

			if action.Tool != "" { // Only add if a tool was extracted
				extractedActions = append(extractedActions, action)
			}
		}

		// Thought-only output (no extractedActions) is not a valid plan; fall through to error.
		if len(extractedActions) > 0 {
			o.getLogger().Info("rewooagent: successfully extracted plan details using regex fallback")
			var err error
			extractedActions, err = o.validateAndCorrectActions(extractedActions)
			return extractedActions, nil, thoughts, err
		}

		// If regex fallback also fails, return a schema-specific diagnostic error
		// so the retry message tells the model exactly what to fix.
		reason := ""
		if !strings.Contains(output, "<plan_response>") {
			reason = "missing <plan_response> root tag"
		} else if !strings.Contains(output, "</plan_response>") {
			reason = "missing closing </plan_response> tag"
		} else if strings.Contains(output, "<step>") && strings.Contains(output, "<description>") && !strings.Contains(output, "<tool>") {
			reason = "steps used wrong schema: found <description> but expected <id>, <tool>, <query>, <reason> inside each <step>. Each <step> MUST include required fields: <id>, <tool>, <query>, <reason>. Optional: <dependency>, <condition>"
		} else if strings.Contains(output, "<step>") && !strings.Contains(output, "<plan>") {
			reason = "steps are missing the required <plan> wrapper — structure must be <plan_response><thought>...</thought><plan><step>...</step></plan></plan_response>"
		} else if !strings.Contains(output, "<thought>") {
			reason = "missing <thought> tag inside <plan_response>"
		} else if !strings.Contains(output, "<plan>") {
			reason = "missing <plan> wrapper tag"
		} else if strings.Contains(output, "<step>") && !strings.Contains(output, "</step>") {
			reason = "missing one or more closing </step> tags"
		} else {
			reason = err.Error()
		}

		return nil, nil, "", fmt.Errorf("could not parse plan: %s", reason)
	}

	thoughts = plan.Thought // Use thought from the XML plan

	actions := []NBAgentPlannerToolAction{}
	for _, p := range plan.Steps {
		if strings.EqualFold(p.Tool, "none") {
			o.getLogger().Info("rewooagent: skipping plan with 'none' tool", "plan", slog.AnyValue(p))
			continue
		}

		toolPlanId := p.ID
		if toolPlanId == "" {
			toolPlanId = "E" + fmt.Sprintf("%d", len(actions)+1)
		}

		// if toolId exists in existing plans, then create new toolKey
		if slices.Contains(existingPlanKeys, strings.ToLower(toolPlanId)) {
			newPlanKeyMap[toolPlanId] = toolPlanId + "_" + fmt.Sprintf("%d", len(newPlanKeyMap)+1)
			toolPlanId = newPlanKeyMap[toolPlanId]
		}
		existingPlanKeys = append(existingPlanKeys, strings.ToLower(toolPlanId))

		dependency := []string{}
		for _, d := range p.Dependency {
			// Split by comma, newline, or semicolon
			f := func(c rune) bool {
				return c == ',' || c == '\n' || c == ';' || c == '\r' || c == ' '
			}
			parts := strings.FieldsFunc(d, f)
			for _, sd := range parts {
				sd = strings.TrimSpace(sd)
				if sd == "" {
					continue
				}

				// update dependency to newly created key
				if mappedKey, ok := newPlanKeyMap[sd]; ok {
					sd = mappedKey
				}
				dependency = append(dependency, sd)
			}
		}

		actionCondition := NBAgentPlannerToolActionCondition{}
		if p.Condition != nil {
			actionCondition.Prompt = p.Condition.Prompt
			actionCondition.ExpectedResponse = p.Condition.ExpectedResponse
			actionCondition.AllowedResponses = p.Condition.AllowedResponses
		}

		actions = append(actions, NBAgentPlannerToolAction{
			ToolID:     toolPlanId,
			Tool:       p.Tool,
			ToolInput:  p.Query,
			Log:        p.Reason, // This is 'reason' from XML
			Dependency: dependency,
			Condition:  actionCondition,
		})
	}

	if len(actions) == 0 {
		// A thought alone is not a valid plan — the model must also produce at least one step.
		// Treat this as retryable so the model gets another chance to produce the full XML.
		outputPreview := output
		if len(outputPreview) > 300 {
			outputPreview = outputPreview[:300] + "...[truncated]"
		}
		o.getLogger().Error("rewooagent2: unable to parse plan, no steps extracted", "has_thought", thoughts != "", "output_preview", outputPreview)
		return nil, nil, "", fmt.Errorf("could not parse plan: no steps extracted from model output (thought present: %v)", thoughts != "")
	}

	actions, err = o.validateAndCorrectActions(actions)

	return actions, nil, thoughts, err
}

func reWooCreatePrompt2(ctx *security.RequestContext, agentPrompt string, toolsIn []toolcore.NBTool, conversationContext string, previousMessages []prompts.MessageFormatter, request NBAgentRequest, nbAgent NBAgent, previousResponse string, isInvestigationRequest bool, maxPlanSteps int) (prompts.ChatPromptTemplate, []toolcore.NBTool) {
	// Create a copy of tools to avoid modifying the original slice
	tools := make([]toolcore.NBTool, len(toolsIn))
	copy(tools, toolsIn)

	messageFormatters := []prompts.MessageFormatter{}
	// Only declare template variables that are actually referenced in planner_rewoo_2_base.txt.
	// Dynamic vars (today, history, conversation_context, task_context, notebook, question_type, input)
	// have been moved to the human message to keep the system prefix stable for caching.
	messageFormatters = append(messageFormatters, prompts.NewSystemMessagePromptTemplate(prompts_repo.GetPrompt(prompts_repo.PromptPlannerRewoo2Base),
		[]string{
			"tool_names",
			"tool_descriptions",
			"max_plan_steps",
			"workspace_enabled",
			"shell_tool_enabled",
			"delegate_agent_enabled",
			"context_management_rules",
			"time_handling_rules",
			"code_analysis_rules",
			"security_rules",
		}))

	// Agent prompt as system message so it falls within the cacheable prefix
	// for Global/Account cache scopes (which only cache system messages).
	messageFormatters = append(messageFormatters, prompts.NewSystemMessagePromptTemplate("<task_instructions>"+agentPrompt+"</task_instructions>", []string{}))
	agentAdditionalPrompt, configuredTools, _ := AgentAdditionalInstructionsAndToolsAndConfigs(ctx, request.AccountId, nbAgent.GetName())
	if agentAdditionalPrompt != "" {
		messageFormatters = append(messageFormatters, prompts.NewSystemMessagePromptTemplate("<task_preferences>"+agentAdditionalPrompt+"</task_preferences>", []string{}))
	}

	if len(request.ClientTools) > 0 {
		// Inject client tools at the beginning
		var clientTools []toolcore.NBTool
		clientToolNames := []string{}
		for _, ct := range request.ClientTools {
			clientTools = append(clientTools, toolcore.NewClientToolWrapper(ct))
			clientToolNames = append(clientToolNames, fmt.Sprintf("'%s'", ct.Name))
		}
		tools = append(clientTools, tools...)

		priorityInstruction := fmt.Sprintf(
			`IMPORTANT: The user has provided "Local Tools" which execute directly on their machine: %s.
			If the user's request involves their local environment (files, shell, local processes), you MUST prioritize these Local Tools over server-side counterparts.`,
			strings.Join(clientToolNames, ", "))

		messageFormatters = append(messageFormatters, prompts.NewSystemMessagePromptTemplate(priorityInstruction, []string{}))
	}

	if len(configuredTools) > 0 {
		for _, ct := range configuredTools {
			if t, ok := toolcore.GetNBTool(request.AccountId, ct); ok {
				tools = append(tools, t)
			} else {
				ctx.GetLogger().Warn("reactagent2: configured tool not found, skipping", "tool", ct)
			}
		}
	}

	// AccountPrompt is intentionally NOT injected as a system message here:
	// it is only set by the event-analysis path (api/event_analyzer.go) and
	// stays empty for interactive chat. Putting it in the cacheable system
	// prefix means the same (account, agent, model) cache key alternates
	// between "<global_preferences>…</global_preferences>"-present and -absent
	// prefixes — busting the Account-scope cache on every flip between chat
	// and event-driven RCAs. It is rendered into the human-message
	// global_preferences_block instead (see dynamicPrompt below) so it still
	// reaches the LLM without drifting the cacheable prefix.

	// Move static critical instructions and adherence rules to a System message
	// so they are cached at the Account/Global level.
	adherencePrompt := `
<critical_rules>
- **SKILL LISTS:** The context might contain a <skill-lists> section which lists available "skills" or "knowledge bases" (e.g. name and description). These are NOT loaded by default. You MUST examine this list before creating your plan. If a skill's description is relevant to the user's question, you MUST create a step using the load_skills tool with the skill's name to retrieve its content. You can load multiple skills in a single step by providing a comma-separated list of names. Always check <skill-lists> before deciding that you don't have enough information to form a plan.
- **CRITICAL - User Request Adherence Protocol:** Your primary allegiance is to the user's specific request provided in the 'question'. The details, tools, and metrics mentioned in that context MUST take precedence over your general troubleshooting protocols. Before creating a plan, apply this mental checklist:
    1.  **Extract Tool Mentions:** Have I identified every tool explicitly named by the user (e.g., 'Prometheus', 'kubectl', 'traces')? My plan MUST use these tools for the relevant steps (if applicable).
    2.  **Extract Entity & Metric Mentions:** Have I identified every specific metric (e.g., 'pg_stat_activity_count'), resource name, or condition mentioned by the user? My tool queries MUST incorporate these exact details (if applicable).
    3.  **Follow the User's Flow:** Does the user's request outline a specific sequence of steps? My plan must follow this sequence as closely as possible within the step limit.
    4.  **Ignore Acknowledgment-Only Messages:** Short acknowledgment or confirmation messages from the user (e.g., "ok", "yes", "sure", "got it") are conversational filler — do NOT treat them as plan direction or constraints. However, DO follow substantive user corrections or direction changes (e.g., "can you try again", "focus on the database", "skip metrics", "use Prometheus instead").
- **CRITICAL - Output Format:** Your response MUST be a single XML object with root element <plan_response> containing <thought> and <plan>. Each <step> inside <plan> MUST include these required sub-elements: <id>, <tool>, <query>, <reason>. Optional elements <dependency> and <condition> may also be present. DO NOT use <description> or any other non-standard tags inside <step>. DO NOT output JSON. DO NOT output plain text.
</critical_rules>`
	messageFormatters = append(messageFormatters, prompts.NewSystemMessagePromptTemplate(adherencePrompt, []string{}))

	// Move ALL dynamic parts (task, history, context) to the final Human message.
	// This ensures the System message prefix is 100% stable across all conversations in the account.
	// global_preferences_block is rendered here (see comment above where AccountPrompt
	// is no longer added as a system message) so its presence/absence does not flip
	// the cacheable prefix between event-analysis and interactive chat paths.
	dynamicPrompt := `
{{.kb_prestep_content}}
{{.skill_lists_menu}}
{{.global_preferences_block}}
<task_context>
**Today's Date:** {{.today}}
**Current Task Context:** {{.task_context}}
**Previous Conversation Context:** {{.conversation_context}}
**Previous Messages (History):**
{{.history}}
</task_context>

<notebook_content>
{{.notebook}}
</notebook_content>

<question_type>
{{.question_type}}
</question_type>

<task>
{{.input}}
</task>
`
	messageFormatters = append(messageFormatters, prompts.NewHumanMessagePromptTemplate(dynamicPrompt, []string{
		"today",
		"task_context",
		"conversation_context",
		"history",
		"notebook",
		"question_type",
		"input",
		"global_preferences_block",
		"kb_prestep_content",
		"skill_lists_menu",
	}))

	// Filter and inject tools. SkillListsMenu is appended to the detection
	// string so load_skills is still injected when the menu lives in the human
	// message (KB pre-step path) rather than the system prompt (legacy path).
	tools = FilterAndInjectDefaultTools(request.AccountId, nbAgent, agentPrompt+request.SkillListsMenu, tools, request.Capabilities)

	tmpl := prompts.NewChatPromptTemplate(messageFormatters)

	previousMessageStr := messageFormatterToString(previousMessages)

	tmpl.PartialVariables = map[string]any{
		// System message template vars (stable — cached across conversations)
		"tool_names":               reActPromptToolNames(tools),
		"tool_descriptions":        reActPromptToolDescriptions(tools),
		"max_plan_steps":           strconv.Itoa(maxPlanSteps),
		"workspace_enabled":        config.Config.LlmServerWorkspaceEnabled,
		"shell_tool_enabled":       config.Config.LlmServerShellToolEnabled && HasShellTool(tools),
		"delegate_agent_enabled":   HasDelegateAgentTool(tools),
		"context_management_rules": prompts_repo.GetPrompt(prompts_repo.PromptContextContinuity),
		"time_handling_rules":      prompts_repo.GetPrompt(prompts_repo.PromptSharedTimeHandlingRules),
		"code_analysis_rules":      prompts_repo.GetPrompt(prompts_repo.PromptSharedCodeAnalysisRules),
		"security_rules":           prompts_repo.GetPrompt(prompts_repo.PromptSharedSecurityRules),
		"previous_response":        previousResponse,
		// Human message template vars (dynamic — change per conversation/call)
		"today":                    time.Now().Format("January 02, 2006"),
		"conversation_context":     conversationContext,
		"history":                  previousMessageStr,
		"task_context":             request.QueryContext,
		"question_type":            lo.Ternary(isInvestigationRequest, "investigation", "query"),
		"notebook":                 "", // default; overridden per-call in fullInputs with o.Notebook
		"global_preferences_block": renderGlobalPreferencesBlock(request.AccountPrompt),
		// KB pre-step output — empty on the legacy path; populated into the human
		// message (not the cacheable system prefix) when KB pre-step is enabled.
		"kb_prestep_content": request.KBPrestepContent,
		"skill_lists_menu":   request.SkillListsMenu,
	}
	return tmpl, tools
}

func NewReWooAgent2(ctx *security.RequestContext, request NBAgentRequest, nbAgent NBAgent, systemMessage string, extraMessages []prompts.MessageFormatter, initialNotebook string) (*ReWooPlanner2, error) {
	if request.ConversationContext == "" {
		request.ConversationContext = "No additional context provided."
	}

	// Propagate cache scope from agent (mirrors planner_react_2.go:252-264)
	cacheScope := CacheScopeConversation
	if cacheProvider, ok := nbAgent.(NBAgentCacheScopeProvider); ok {
		cacheScope = cacheProvider.GetCacheScope()
	}
	// ClientTools (Local Tools) are session-scoped: their names and the
	// "prioritize Local Tools" system instruction are unique to a chat
	// session. Caching at Account/Global scope would force a new cache for
	// every conversation that brings different ClientTools. Downgrade to
	// Conversation scope so the cache key matches reality.
	if len(request.ClientTools) > 0 && cacheScope != CacheScopeConversation {
		ctx.GetLogger().Debug("rewooagent2: downgrading cache scope to conversation due to client tools",
			"agent", nbAgent.GetName(), "from", cacheScope)
		cacheScope = CacheScopeConversation
	}
	ctx = security.NewRequestContext(
		context.WithValue(
			context.WithValue(ctx.GetContext(), ContextKeyCacheScope, cacheScope),
			ContextKeyCapabilities, request.Capabilities,
		),
		ctx.GetSecurityContext(),
		ctx.GetLogger(),
		ctx.GetTracer(),
		ctx.GetMeter(),
	)

	isInvestigationRequest := IsInvestigationRequestTask(request.Query)

	solver, err := NewReWooSolver(ctx, request, nbAgent, systemMessage)
	if err != nil {
		return nil, err
	}

	critique, err := NewReWooCritiquer(ctx, request, nbAgent, systemMessage)
	if err != nil {
		ctx.GetLogger().Error("rewooagent: failed to create critiquer", "error", err)
		return nil, err
	}

	planCritiquer, err := NewReWooPlanCritiquer(ctx, request, nbAgent)
	if err != nil {
		ctx.GetLogger().Error("rewooagent: failed to create plan critiquer", "error", err)
		return nil, err
	}

	maxPlanSteps := config.Config.PlannerRewooInfoMaxSteps
	maxRefinementAttempts := defaultMaxRefinementAttempts2
	if isInvestigationRequest {
		maxPlanSteps = config.Config.PlannerRewooInvestigationMaxSteps
		if maxPlanSteps < 20 {
			maxPlanSteps = 20
		}
		maxRefinementAttempts = 5
	}

	prompt, tools := reWooCreatePrompt2(ctx, systemMessage, nbAgent.GetSupportedTools(ctx), request.ConversationContext, extraMessages, request, nbAgent, "", isInvestigationRequest, maxPlanSteps)

	ctx.GetLogger().Info("rewooagent2: initializing planner",
		"initial_notebook_len", len(initialNotebook),
		"has_initial_notebook", initialNotebook != "")

	return &ReWooPlanner2{
		ctx:                       ctx,
		prompt:                    prompt,
		nbAgent:                   nbAgent,
		request:                   request,
		isInvestigationRequest:    isInvestigationRequest,
		isFiveWhysMode:            isInvestigationRequest,
		fiveWhysCounter:           0,
		refinementAttempts:        0,
		maxRefinementAttempts:     maxRefinementAttempts,
		planRefinementAttempts:    0,
		maxPlanRefinementAttempts: 4, // Allow enough refinements for multi-step recovery (e.g. logs fail → kubectl → logs with full name)
		refinementFeedback:        "",
		systemMessage:             systemMessage,
		extraMessages:             extraMessages,
		solver:                    solver,
		critique:                  critique,
		planCritiquer:             planCritiquer,
		maxPlanSteps:              maxPlanSteps,
		tools:                     tools,
		executionGraph:            make(map[string]*PlannerNode),
		isGraphInitialized:        false,
		Notebook:                  initialNotebook,
		compressionTracker:        NewCompressionTracker(),
	}, nil
}

// summarizeAfterError is called when the solver fails. It summarizes the progress and returns a final action.
func (o *ReWooPlanner2) summarizeAfterError(ctx context.Context, solverErr error, intermediateSteps []NBAgentPlannerToolActionStep) (*NBAgentPlannerFinishAction, error) {
	o.getLogger().Error("rewooagent: solver failed, attempting to summarize progress", "error", solverErr)

	var stepsSummary strings.Builder
	stepsSummary.WriteString("Here is a summary of the steps attempted:\n")
	for i, step := range intermediateSteps {
		if step.Action.Tool == plannerDummyTool {
			continue
		}
		status := "Success"
		if step.Observation == "" {
			status = "Unknown/NoOutput"
		}
		fmt.Fprintf(&stepsSummary, "%d. Tool: %s, Reason: %s, Status: %s\n", i+1, step.Action.Tool, step.Action.Log, status)
	}

	summaryPrompt := fmt.Sprintf(`An internal error occurred in the AI agent's reasoning process. Your task is to provide a clear, human-readable summary of the work done so far.

- Original Question: %s
- Steps Log:
%s
- The final reasoning step failed with the error: %s

Please provide:
1. A concise summary of what was attempted and what was found.
2. Under a '### Recommended Next Steps' section, provide specific CLI commands or manual investigation steps the user can run themselves to continue the investigation. Include exact commands (kubectl, aws, curl, etc.) where applicable.`, o.request.Query, stepsSummary.String(), solverErr.Error())

	mcList := []llms.MessageContent{
		{Role: llms.ChatMessageTypeHuman, Parts: []llms.ContentPart{llms.TextContent{Text: summaryPrompt}}},
	}

	result, err := GenerateAndTrackLLMContent(o.ctx, o.request.UserId, o.request.AccountId, o.request.ConversationId, o.request.MessageId, o.request.AgentId, false, mcList, true,
		llms.WithTemperature(0.0), WithThinkingLevel(ThinkingLevelFastTask))

	if err != nil {
		o.getLogger().Error("rewooagent: summarization call failed after solver failure", "error", err)
		finalErrStr := fmt.Sprintf("The agent's reasoning process failed, and a subsequent attempt to summarize the progress also failed. The last known error was: %s", solverErr.Error())
		return &NBAgentPlannerFinishAction{
				Log:  finalErrStr,
				Data: finalErrStr,
			},
			nil
	}

	if len(result.Choices) == 0 {
		return &NBAgentPlannerFinishAction{
				Log:  "No choices returned from LLM during summarization",
				Data: "The agent encountered an error and was unable to generate a summary of its progress.",
			},
			nil
	}

	finalSummary := result.Choices[0].Content

	return &NBAgentPlannerFinishAction{
			Log:  finalSummary,
			Data: finalSummary,
		},
		nil
}

// isSolverArtifactRequest returns true when the solver's missing-info request is
// specifically asking for full artifact/log file content that was saved to the workspace.
// In such cases we suppress the plan refinement to prevent context bloat — the solver
// should work with the summary/preview that is already in the scratchpad.
func isSolverArtifactRequest(missingInfo string) bool {
	lower := strings.ToLower(missingInfo)
	artifactKeywords := []string{
		"saved to",
		"read the file",
		"read_file",
		"cat logs_",
		"full content",
		"full log",
		"entire file",
		"complete log",
		"log file",
		"logs_",
	}
	for _, kw := range artifactKeywords {
		if strings.Contains(lower, kw) {
			return true
		}
	}
	return false
}
