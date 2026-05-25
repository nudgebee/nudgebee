package planners

import (
	"fmt"
	"strings"
)

// Goal is the planner's externalised representation of "what does a complete
// answer look like for this run." It is constructed once at Plan() start from
// the query and the request mode, and injected into the system prompt so the
// LLM can self-evaluate progress against a concrete termination criterion.
//
// Why this exists: the old ReAct loop terminated only when the LLM voluntarily
// emitted submit_analysis. In explore mode, with no concrete fix target, the
// LLM had no internal model of "done" and routinely wandered to the iteration
// ceiling. By making the goal first-class — query, contract, sub-questions —
// the planner can both (a) tell the model what the answer must look like up
// front, and (b) let a periodic reflection step measure progress.
type Goal struct {
	// Query is the original user question, unmodified.
	Query string

	// Mode is the planner's effective mode ("explore", "fix", or "" for the
	// generic specialist path). Drives the contract text below.
	Mode string

	// Contract describes the shape of a successful submit_analysis call in this
	// mode. Rendered into the system prompt verbatim. The validator inside
	// submit_analysis enforces the same contract at submit time; this string is
	// the agent-facing description of it.
	Contract string

	// TerminationCriterion is a one-line "you're done when..." statement the
	// LLM can self-check against. Kept separate from Contract so reflection
	// prompts can quote it without dumping the full schema.
	TerminationCriterion string
}

// BuildGoal constructs the Goal for a (query, mode) pair. Mode comes from
// tools.ModeFromContext(ctx); pass "" if not set and we'll fall through to a
// generic specialist contract.
func BuildGoal(query, mode string) *Goal {
	switch mode {
	case "explore":
		return &Goal{
			Query: query,
			Mode:  mode,
			Contract: strings.TrimSpace(`
EXPLORE MODE — submit_analysis contract:
  - answer: required, 1–3 plain-prose sentences, no markdown.
  - citations: required, at least one entry. Each citation must have:
      - file_path  (repo-relative path)
      - line_start (positive integer)
      - snippet    (the actual lines being cited)
      - line_end   (optional; defaults to line_start)
      - note       (optional one-liner on why this citation matters)
  - caveats, follow_up_suggestions, confidence_score: optional.
  - title, description: derived from answer if you don't set them.
You MUST NOT include implementation_instructions, fixed_code, or git_diff in
explore mode — this is a read-only Q&A; mutation fields are ignored and may
cause downstream consumers to render a misleading "fix proposed" banner.
`),
			TerminationCriterion: "You're done when you can write a 1–3 sentence plain-prose answer to the query, with each claim backed by at least one citation pointing to a real file/line/snippet.",
		}
	case "fix":
		return &Goal{
			Query: query,
			Mode:  mode,
			Contract: strings.TrimSpace(`
FIX MODE — submit_analysis contract:
  - title: required, specific to what was found.
  - description: required, root cause + recommended change.
  - requires_fix: bool — true ONLY if you've identified a specific file_path
    AND have implementation_instructions to produce the change.
  - implementation_instructions: required when requires_fix=true. List of
    {step, file_path, action: replace|write|edit, line_number, old_string,
    new_string, purpose} entries.
  - root_cause_analysis: required, brief causal chain.
  - confidence_score: High|Medium|Low.
  - file_path, line_number, original_code, fixed_code: required when fixing
    a specific location.
`),
			TerminationCriterion: "You're done when you can name the root cause, the exact file and lines to change, and a confidence score — or when you've conclusively determined no fix is needed (requires_fix=false).",
		}
	}
	// Generic specialist path (no orchestrator-set mode). Used by CodeFixer,
	// ErrorRCA when running standalone, etc. — these have their own
	// per-specialist contracts enforced by submit_analysis. Keep the
	// prompt-side guidance soft so we don't conflict with the specialist's
	// own system prompt instructions.
	return &Goal{
		Query: query,
		Mode:  mode,
		Contract: strings.TrimSpace(`
submit_analysis contract (specialist):
  - title and description: required.
  - Include the fields appropriate to your specialist role
    (execution_status for code-fixer, comment_responses for PR-followup,
    implementation_instructions for RCA-with-fix, etc.).
  - confidence_score: optional but encouraged.
`),
		TerminationCriterion: "You're done when you've produced a complete report appropriate to your specialist role and called submit_analysis with the required fields.",
	}
}

// ToPromptBlock renders the goal as a system-prompt section. Placed near the
// top of the system prompt so the LLM sees the contract before tool listings.
func (g *Goal) ToPromptBlock() string {
	if g == nil {
		return ""
	}
	var b strings.Builder
	b.WriteString("## TASK GOAL\n\n")
	fmt.Fprintf(&b, "Original query: %s\n\n", g.Query)
	if g.TerminationCriterion != "" {
		fmt.Fprintf(&b, "Termination criterion: %s\n\n", g.TerminationCriterion)
	}
	if g.Contract != "" {
		b.WriteString(g.Contract)
		b.WriteString("\n\n")
	}
	b.WriteString("Plan your investigation around this goal. Do not run tools just to satisfy curiosity — each tool call should advance you toward the termination criterion. When the criterion is met, call submit_analysis and stop.\n")
	return b.String()
}
