package planners

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"nudgebee/code-analysis-agent/common"
)

// Abstention: when the agent cannot ground an answer in code, it reports honest
// "insufficient evidence" — what it examined and what it could not confirm —
// instead of the LLM-fabricated root cause the forced-submit path used to emit.
// This is the anti-hallucination contract chosen for the first-run case: a
// confident wrong RCA is worse than an honest "couldn't confirm."

// sortedReadFiles returns the files opened with file_view this run, sorted.
func (p *ReActPlanner) sortedReadFiles() []string {
	p.mu.Lock()
	out := make([]string, 0, len(p.readFiles))
	for f := range p.readFiles {
		out = append(out, f)
	}
	p.mu.Unlock()
	sort.Strings(out)
	return out
}

// shouldAbstainForced reports whether a forced (out-of-budget) termination should
// abstain rather than fabricate. It fires on the clear no-evidence cases and
// leaves grounded flows (which have real reads to summarize) untouched.
func (p *ReActPlanner) shouldAbstainForced() bool {
	p.mu.Lock()
	neverRead := len(p.readFiles) == 0
	p.mu.Unlock()
	if neverRead {
		// Never opened a file: any "root cause in code" would be invented.
		return true
	}
	// Explore mode: if the ledger can't produce a grounded answer+citation, don't
	// ask the LLM to synthesize one from scratch.
	if p.goal != nil && p.goal.Mode == "explore" && !p.canTerminateFromLedger() {
		return true
	}
	return false
}

// buildInsufficientEvidenceData constructs the structured honest-abstention
// answer. It carries an explicit marker and low confidence so the orchestrator
// can surface "couldn't confirm in code" rather than treating it as a finding.
func (p *ReActPlanner) buildInsufficientEvidenceData(reason string) map[string]any {
	read := p.sortedReadFiles()
	answer := "Insufficient evidence: the cause could not be confirmed in the code within the investigation budget."

	// Report what was examined and why the run stopped; the structured marker
	// below is the machine-readable signal. No prose beyond the facts of this run.
	caveats := make([]string, 0, 2)
	if len(read) > 0 {
		caveats = append(caveats, "Files examined: "+joinLimited(read, 20))
	} else {
		caveats = append(caveats, "No source files were opened and read.")
	}
	if reason != "" {
		caveats = append(caveats, reason)
	}

	return map[string]any{
		"title":                 "Insufficient evidence to determine the cause",
		"answer":                answer,
		"description":           answer,
		"insufficient_evidence": true,
		"confidence_score":      "low",
		"requires_fix":          false,
		"citations":             []any{},
		"caveats":               caveats,
	}
}

// finalizeInsufficientEvidence records the abstention as the planner's terminal
// answer. Status stays "completed" (the run finished with an honest answer) and
// the insufficient_evidence marker in the data is the signal downstream.
func (p *ReActPlanner) finalizeInsufficientEvidence(_ context.Context, result *PlannerResult, stepNumber int, reason string) {
	data := p.buildInsufficientEvidenceData(reason)
	p.lastSubmitAnalysisData = data

	step := Step{
		Number:      stepNumber,
		Thought:     "Insufficient grounded evidence — abstaining instead of fabricating an answer.",
		Action:      "submit_analysis",
		ActionInput: data,
		Status:      "completed",
		Observation: "Submitted insufficient-evidence answer (abstained).",
	}
	result.Steps = append(result.Steps, step)
	p.currentSteps = append(p.currentSteps, step)
	result.Status = "completed"
	result.FinalAnswer = p.extractFinalAnswer(&step)

	if p.logger != nil {
		p.mu.Lock()
		filesRead := len(p.readFiles)
		p.mu.Unlock()
		p.logger.Log(common.EventPlanningComplete, "Abstained: insufficient grounded evidence", map[string]any{
			"reason":      reason,
			"files_read":  filesRead,
			"step_number": stepNumber,
		})
	}
}

func joinLimited(items []string, max int) string {
	if len(items) <= max {
		return strings.Join(items, ", ")
	}
	return strings.Join(items[:max], ", ") + fmt.Sprintf(", … (+%d more)", len(items)-max)
}
