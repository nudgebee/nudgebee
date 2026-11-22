package core

import (
	"encoding/json"
	"fmt"
	"strings"

	"nudgebee/llm/config"
	nbprompts "nudgebee/llm/prompts"
	toolcore "nudgebee/llm/tools/core"

	"github.com/tmc/langchaingo/llms"
)

// Structured plan for ReAct4 (update_notebook_v2, #41443).
//
// The plan is never stored as planner-owned mutable state: like the v1
// notebook, it is recomputed on every Plan() call from the successful
// update_notebook_v2 steps of the current turn, so a suspend/resume or a retry
// rebuilds exactly the same plan. One plan per message: a new turn starts empty
// and sees the previous turn's plan only through the rendered notebook in the
// human message.

// react4PlanStepArgument is the optional planner-reserved argument that tags a
// tool call with the plan step it serves. It rides on the same schema
// decoration as _thought and is stripped before validation and tool-id hashing.
const react4PlanStepArgument = "_plan_step"

// maxPlanGateRejections bounds how often one turn's final answer is sent back
// for open plan steps before it is accepted with those steps left unresolved.
const maxPlanGateRejections = 2

// planCheckpointIteration is the earliest round at which an investigation
// that has started branching out (a sub-agent run or a second kind of tool)
// is required to record its plan. Before that, discovery runs unplanned.
const planCheckpointIteration = 3

// planStaleRounds is how long a step may stay in progress without a plan
// update before the per-call note asks for one.
const planStaleRounds = 3

// planSubAgentRunsPerStep is how many sub-agent runs one step may start before
// the note asks for direct checks instead (fan-out drove task counts up 3-6x).
const planSubAgentRunsPerStep = 2

// planBudgetFraction is the share of the round budget after which the note
// asks to close open steps and answer.
const planBudgetFraction = 0.7

// planHypothesisRounds is how many rounds after the plan was created an
// investigation's plan should name the causes it is checking. Dev runs showed
// the Scope step used as a catch-all for eight rounds, with hypotheses written
// only after the fact.
const planHypothesisRounds = 2

// resolveNotebookV2Enabled reports whether this run uses the structured plan:
// flag on, notebook enabled, a top-level run, and the built-in react_4 base
// prompt (custom agents keep their compact base and v1 notebook).
func resolveNotebookV2Enabled(request NBAgentRequest, agent NBAgent) bool {
	if !config.Config.NotebookV2Enabled || agent == nil || !ResolveAgentNotebookEnabled(agent) {
		return false
	}
	isTopLevel := request.ParentAgentId == "" || request.ParentAgentId == request.AgentId
	return isTopLevel && react4BasePromptName(agent) == nbprompts.PromptReact4Base
}

// isInvestigationRun matches the predicate the memory and knowledge paths use:
// event-triggered runs do not reliably carry an investigation-shaped query.
func isInvestigationRun(request NBAgentRequest) bool {
	return request.ConversationSource == ConversationSourceInvestigation ||
		request.QueryConfig.EventId != "" ||
		IsInvestigationRequestTask(request.Query)
}

// planFromSteps applies every successful update_notebook_v2 call in order.
func planFromSteps(steps []NBAgentPlannerToolActionStep) []toolcore.PlanStep {
	var plan []toolcore.PlanStep
	for i := range steps {
		step := &steps[i]
		if step.Action.Tool != toolcore.NotebookV2ToolName || step.Status != ToolStatusSuccess {
			continue
		}
		patches, err := toolcore.ParsePlanPatches(step.Action.ToolInput)
		if err != nil {
			continue
		}
		plan = toolcore.ApplyPlanPatches(plan, patches)
	}
	return plan
}

// lastPlanUpdateIgnored returns what the most recent successful plan call
// asked for but did not get (a status that belongs to the other kind of step).
// The tool replied "Plan updated" without knowing the plan, so this is how the
// model hears about it.
func lastPlanUpdateIgnored(steps []NBAgentPlannerToolActionStep) []string {
	var plan []toolcore.PlanStep
	var ignored []string
	for i := range steps {
		step := &steps[i]
		if step.Action.Tool != toolcore.NotebookV2ToolName || step.Status != ToolStatusSuccess {
			continue
		}
		patches, err := toolcore.ParsePlanPatches(step.Action.ToolInput)
		if err != nil {
			continue
		}
		plan, ignored = toolcore.ApplyPlanPatchesReport(plan, patches)
	}
	return ignored
}

// openPlanSteps returns the steps that still need work.
func openPlanSteps(plan []toolcore.PlanStep) []toolcore.PlanStep {
	var open []toolcore.PlanStep
	for _, s := range plan {
		if toolcore.PlanStepIsOpen(s) {
			open = append(open, s)
		}
	}
	return open
}

// activePlanStepID returns the most recently listed step being worked on, used
// to attribute a tool call that carries no _plan_step.
func activePlanStepID(plan []toolcore.PlanStep) string {
	for i := len(plan) - 1; i >= 0; i-- {
		if toolcore.PlanStatusIsActive(plan[i].Status) {
			return plan[i].ID
		}
	}
	return ""
}

// plannerIterations counts the model turns that produced tool calls so far
// (parallel siblings share a TurnID, so they count once).
func plannerIterations(steps []NBAgentPlannerToolActionStep) int {
	seen := map[string]struct{}{}
	for i := range steps {
		key := steps[i].Action.TurnID
		if key == "" {
			key = fmt.Sprintf("step-%d", i)
		}
		seen[key] = struct{}{}
	}
	return len(seen)
}

// renderPlanMarkdown renders the plan for the existing string consumers
// (critique, memory, the persisted notebook row, the next turn's human message).
func renderPlanMarkdown(plan []toolcore.PlanStep) string {
	if len(plan) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("## Plan\n")
	children := map[string][]toolcore.PlanStep{}
	for _, s := range plan {
		if s.Parent != "" {
			children[s.Parent] = append(children[s.Parent], s)
		}
	}
	phase := ""
	for _, s := range plan {
		if s.Parent != "" {
			continue
		}
		if s.Phase != "" && s.Phase != phase {
			phase = s.Phase
			fmt.Fprintf(&b, "### %s\n", phase)
		}
		writePlanLine(&b, s, "")
		for _, c := range children[s.ID] {
			writePlanLine(&b, c, "  ")
		}
	}
	return strings.TrimRight(b.String(), "\n")
}

func writePlanLine(b *strings.Builder, s toolcore.PlanStep, indent string) {
	// Hypotheses use the tree format the answer critiquer's completion check
	// parses: "H1 [High][SUPPORTED] title"; tasks keep "[DONE] s1: title".
	if s.Kind == toolcore.PlanStepKindHypothesis {
		fmt.Fprintf(b, "%s- %s [%s][%s] %s", indent, strings.ToUpper(s.ID), priorLabel(s.Prior), strings.ToUpper(s.Status), s.Title)
	} else {
		fmt.Fprintf(b, "%s- [%s] %s: %s", indent, strings.ToUpper(s.Status), s.ID, s.Title)
	}
	if s.Finding != "" {
		fmt.Fprintf(b, " — %s", s.Finding)
	}
	b.WriteString("\n")
}

func priorLabel(prior string) string {
	switch prior {
	case "high":
		return "High"
	case "low":
		return "Low"
	case "medium":
		return "Med"
	}
	return "Med"
}

// planSignals is what the per-call note and the plan requirement read besides
// the plan itself.
type planSignals struct {
	investigation bool
	// roundsLeft is the executor's remaining round budget; -1 when unknown.
	roundsLeft int
	// isSubAgent reports whether a tool runs a sub-agent.
	isSubAgent func(tool string) bool
	// redirectStep is the step index a review redirect was anchored at, or -1.
	redirectStep int
	// finalReviewDone is set once the last allowed review has run.
	finalReviewDone bool
}

// planRequirement returns what the plan must record before more calls, or "".
// It is stated in the per-call note; a response that ignores it is sent back
// once (see Plan).
func planRequirement(plan []toolcore.PlanStep, steps []NBAgentPlannerToolActionStep, sig planSignals) (key, text string) {
	if sig.redirectStep >= 0 && sig.redirectStep <= len(steps) && !hasPlanCall(steps[sig.redirectStep:]) {
		return fmt.Sprintf("redirect-%d", sig.redirectStep),
			"The review challenged your answer. In this response, update the plan with " + toolcore.NotebookV2ToolName +
				": set the challenged hypothesis back to testing (or add a step for the one check the review asked for), " +
				"or close it inconclusive with what is missing and soften the answer. Do not widen the investigation."
	}
	if len(plan) == 0 && sig.investigation && plannerIterations(steps) >= planCheckpointIteration && branchingOut(steps, sig) {
		return "checkpoint",
			"Discovery is done and the work is branching out. In this response, record your plan with " +
				toolcore.NotebookV2ToolName + ": a done Scope step with what discovery found, and the causes you are " +
				"checking as hypotheses, with the one you test next marked testing. Send it alongside your next calls."
	}
	return "", ""
}

// planNote is appended to the last message of every Plan() call while v2 is
// active, so the model always sees one current plan without full-plan tool
// results piling up in the replayed history. Only the tail changes, so the
// cached prefix holds.
func planNote(plan []toolcore.PlanStep, steps []NBAgentPlannerToolActionStep, sig planSignals) string {
	var notes []string
	if len(plan) > 0 {
		notes = append(notes, "Current plan (update it with "+toolcore.NotebookV2ToolName+" as you go):\n"+renderPlanMarkdown(plan))
	}
	if ignored := lastPlanUpdateIgnored(steps); len(ignored) > 0 {
		notes = append(notes, "Your last plan update was not fully applied: "+strings.Join(ignored, "; ")+".")
	}
	if _, req := planRequirement(plan, steps, sig); req != "" {
		notes = append(notes, "Required now: "+req)
	}
	if len(plan) > 0 {
		if n := planFollowUp(plan, steps, sig); n != "" {
			notes = append(notes, n)
		}
	}
	return strings.Join(notes, "\n\n")
}

// planFollowUp picks the single most useful nudge for a plan in progress.
func planFollowUp(plan []toolcore.PlanStep, steps []NBAgentPlannerToolActionStep, sig planSignals) string {
	open := openPlanSteps(plan)
	used := plannerIterations(steps)
	switch {
	case sig.finalReviewDone:
		return "That was the last review. At most one more round of direct checks, no sub-agents, then answer."
	case len(open) > 0 && sig.roundsLeft >= 0 && float64(used) >= planBudgetFraction*float64(used+sig.roundsLeft):
		return fmt.Sprintf("Rounds left: %d. Close the open steps (a hypothesis inconclusive, a task blocked, with the reason, if unverified) and answer; "+
			"start no new sub-agents.", sig.roundsLeft)
	case len(open) == 0 && callsSinceLastPlanUpdate(steps) > 0:
		return "Every step is closed, but you are still investigating. Add a step for the work you are doing now " +
			"(a hypothesis you are testing, or a task), or answer."
	case len(open) == 0:
		return "Your plan is resolved: write the answer now."
	}
	if id := activePlanStepID(plan); id != "" {
		if runs := subAgentRunsForStep(steps, id, sig); runs >= planSubAgentRunsPerStep {
			return fmt.Sprintf("Step %s has already started %d sub-agent runs. Check it directly with your own tools, or close it.", displayStepID(plan, id), runs)
		}
		if rounds := roundsSinceLastPlanCall(steps); rounds >= planStaleRounds {
			return fmt.Sprintf("Step %s has been in progress for %d rounds without a plan update. Close it with what it showed, "+
				"add any new cause you are now checking as a hypothesis, and mark the one you are testing as testing.", displayStepID(plan, id), rounds)
		}
	}
	if sig.investigation && roundsSincePlanCreated(steps) >= planHypothesisRounds && !hasHypothesis(plan) {
		return "The plan names no hypothesis yet. Record the causes you are checking as hypothesis steps, " +
			"and mark the one you are testing now as testing, before more calls."
	}
	return ""
}

func hasPlanCall(steps []NBAgentPlannerToolActionStep) bool {
	for i := range steps {
		if steps[i].Action.Tool == toolcore.NotebookV2ToolName {
			return true
		}
	}
	return false
}

// branchingOut reports whether the run has started a sub-agent or used a
// second kind of tool, the signal that discovery has turned into checking.
func branchingOut(steps []NBAgentPlannerToolActionStep, sig planSignals) bool {
	kinds := map[string]bool{}
	for i := range steps {
		tool := steps[i].Action.Tool
		if isNotebookToolName(tool) {
			continue
		}
		if sig.isSubAgent != nil && sig.isSubAgent(tool) {
			return true
		}
		kinds[tool] = true
	}
	return len(kinds) >= 2
}

func subAgentRunsForStep(steps []NBAgentPlannerToolActionStep, id string, sig planSignals) int {
	n := 0
	for i := range steps {
		if steps[i].Action.PlanStep == id && sig.isSubAgent != nil && sig.isSubAgent(steps[i].Action.Tool) {
			n++
		}
	}
	return n
}

// roundsSinceLastPlanCall counts model turns after the latest plan call.
func roundsSinceLastPlanCall(steps []NBAgentPlannerToolActionStep) int {
	for i := len(steps) - 1; i >= 0; i-- {
		if steps[i].Action.Tool == toolcore.NotebookV2ToolName {
			return plannerIterations(steps[i+1:])
		}
	}
	return plannerIterations(steps)
}

// displayStepID shows hypotheses as H1 (the critiquer's format); tasks as-is.
func displayStepID(plan []toolcore.PlanStep, id string) string {
	for _, s := range plan {
		if s.ID == id && s.Kind == toolcore.PlanStepKindHypothesis {
			return strings.ToUpper(id)
		}
	}
	return id
}

func hasHypothesis(plan []toolcore.PlanStep) bool {
	for _, s := range plan {
		if s.Kind == toolcore.PlanStepKindHypothesis {
			return true
		}
	}
	return false
}

// roundsSincePlanCreated counts model turns after the first plan call.
func roundsSincePlanCreated(steps []NBAgentPlannerToolActionStep) int {
	for i := range steps {
		if steps[i].Action.Tool == toolcore.NotebookV2ToolName {
			return plannerIterations(steps[i+1:])
		}
	}
	return 0
}

// callsSinceLastPlanUpdate counts investigation calls after the last plan call.
func callsSinceLastPlanUpdate(steps []NBAgentPlannerToolActionStep) int {
	n := 0
	for i := len(steps) - 1; i >= 0; i-- {
		if steps[i].Action.Tool == toolcore.NotebookV2ToolName {
			return n
		}
		if !isNotebookToolName(steps[i].Action.Tool) {
			n++
		}
	}
	return n
}

// planGateFeedback is the message that sends a final answer back while the
// plan is incomplete. Empty when the answer may go out.
func planGateFeedback(plan []toolcore.PlanStep) string {
	// Only steps the model itself opened block an answer: an investigation that
	// resolved without needing a plan answers like a plain ReAct run.
	open := openPlanSteps(plan)
	if len(open) == 0 {
		return ""
	}
	names := make([]string, 0, len(open))
	for _, s := range open {
		names = append(names, fmt.Sprintf("%s %q (%s, %s; close as %s)", s.ID, s.Title, s.Kind, s.Status, toolcore.PlanClosingStatuses(s.Kind)))
	}
	return "Your plan still has open steps: " + strings.Join(names, "; ") + ". " +
		"Before answering, close each one with " + toolcore.NotebookV2ToolName + " and a one-line finding: " +
		"a task as done, skipped (no longer matters) or blocked (what stopped you); " +
		"a hypothesis as supported or refuted (the observation that decided it) or inconclusive (what is missing). " +
		"Do not paste the plan into your answer."
}

// unresolvedPlanLine is appended to an answer accepted after the gate budget
// ran out, so the reader knows which checks did not finish.
func unresolvedPlanLine(plan []toolcore.PlanStep) string {
	open := openPlanSteps(plan)
	if len(open) == 0 {
		return ""
	}
	titles := make([]string, 0, len(open))
	for _, s := range open {
		titles = append(titles, s.Title)
	}
	return "\n\n_Not verified: " + strings.Join(titles, "; ") + "._"
}

// planStats derives the notebook breadcrumb counts from the plan.
func planStats(plan []toolcore.PlanStep) notebookStats {
	stats := notebookStats{HasPlanSection: len(plan) > 0}
	for _, s := range plan {
		switch s.Status {
		case toolcore.PlanStatusDone, toolcore.PlanStatusSupported, toolcore.PlanStatusRefuted, toolcore.PlanStatusInconclusive:
			stats.DoneCount++
		case toolcore.PlanStatusInProgress, toolcore.PlanStatusTesting:
			stats.DoingCount++
		case toolcore.PlanStatusPending, toolcore.PlanStatusOpen:
			stats.TodoCount++
		case toolcore.PlanStatusBlocked:
			stats.BlockedCount++
		case toolcore.PlanStatusSkipped:
			stats.SkipCount++
		}
		if s.Finding != "" {
			stats.HasFindings = true
		}
	}
	stats.TotalMarkers = len(plan)
	return stats
}

// planStateJSON is what the UI reads from the notebook row's state column.
func planStateJSON(plan []toolcore.PlanStep) string {
	data, err := json.Marshal(struct {
		Version int                 `json:"version"`
		Steps   []toolcore.PlanStep `json:"steps"`
	}{Version: 1, Steps: plan})
	if err != nil {
		return ""
	}
	return string(data)
}

// withReact4PlanStepSchemas adds the optional _plan_step argument to every tool
// except the notebook tools themselves.
func withReact4PlanStepSchemas(tools []llms.Tool) []llms.Tool {
	out := make([]llms.Tool, len(tools))
	for i, tool := range tools {
		out[i] = tool
		if tool.Function == nil || isNotebookToolName(tool.Function.Name) {
			continue
		}
		definition := *tool.Function
		parameters, ok := definition.Parameters.(map[string]any)
		if !ok {
			out[i].Function = &definition
			continue
		}
		parametersCopy := make(map[string]any, len(parameters))
		for key, value := range parameters {
			parametersCopy[key] = value
		}
		properties, _ := parameters["properties"].(map[string]any)
		propertiesCopy := make(map[string]any, len(properties)+1)
		for key, value := range properties {
			propertiesCopy[key] = value
		}
		propertiesCopy[react4PlanStepArgument] = map[string]any{
			"type":        "string",
			"description": "Id of the plan step this call serves (e.g. h1). Omit when it serves the step in progress.",
		}
		parametersCopy["properties"] = propertiesCopy
		definition.Parameters = parametersCopy
		out[i].Function = &definition
	}
	return out
}

// extractReact4PlanStep removes _plan_step from the tool input.
func extractReact4PlanStep(input string) (string, string) {
	var args map[string]json.RawMessage
	if err := json.Unmarshal([]byte(input), &args); err != nil {
		return input, ""
	}
	raw, ok := args[react4PlanStepArgument]
	if !ok {
		return input, ""
	}
	delete(args, react4PlanStepArgument)
	clean, err := json.Marshal(args)
	if err != nil {
		return input, ""
	}
	var stepID string
	_ = json.Unmarshal(raw, &stepID)
	return string(clean), strings.ToLower(strings.TrimSpace(stepID))
}

// attributePlanSteps fills PlanStep on actions that did not name one, using the
// plan as it stands after any update_notebook_v2 calls in the same completion
// (a model that starts a step and calls for it in one response attributes the
// call to that step). Unknown ids are dropped. The projected plan is local on
// purpose: o.plan holds only executed updates and is rebuilt from the steps at
// the start of every Plan call (ApplyPlanPatches copies, so it is never touched).
func attributePlanSteps(plan []toolcore.PlanStep, actions []NBAgentPlannerToolAction) {
	for _, a := range actions {
		if a.Tool != toolcore.NotebookV2ToolName {
			continue
		}
		if patches, err := toolcore.ParsePlanPatches(a.ToolInput); err == nil {
			plan = toolcore.ApplyPlanPatches(plan, patches)
		}
	}
	known := make(map[string]bool, len(plan))
	for _, s := range plan {
		known[s.ID] = true
	}
	active := activePlanStepID(plan)
	for i := range actions {
		if isNotebookToolName(actions[i].Tool) {
			actions[i].PlanStep = ""
			continue
		}
		if actions[i].PlanStep != "" && !known[actions[i].PlanStep] {
			actions[i].PlanStep = ""
		}
		if actions[i].PlanStep == "" && active != "" {
			actions[i].PlanStep = active
			actions[i].PlanStepInferred = true
		}
	}
}
