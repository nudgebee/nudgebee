package core

import (
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"nudgebee/llm/common"
	"nudgebee/llm/security"
)

// Answers cite the steps they rest on as [Tool - E12](#task-E12). The E-number
// only means something inside the run that produced it: it restarts at E1 for
// every agent and every turn, and the UI cannot map it back to a stored step.
// Before an answer is saved, each such link is rewritten to point at the
// tool-call row the step wrote, [Tool - E12](#task-<row id>), which stays valid
// wherever the answer is shown or copied later. A citation that matches no
// single row is left as plain text rather than pointed at a guess.

var (
	stepCitationLink = regexp.MustCompile(`(?i)\[([^\[\]]*)\]\(#task-(E\d+)\)`)
	anyStepLink      = regexp.MustCompile(`\[([^\[\]]*)\]\(#task-([^)\s]*)\)`)
	rowIDTarget      = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)
	displayIDNumber  = regexp.MustCompile(`^E(\d+)$`)
	// A citation written without a link: [Kubectl - E4], [Shell Execute - E1, E9],
	// [Logs - E3, Metrics - E5]. Only the bracket's content is matched here; the
	// caller checks that no "(" follows, which would make it a markdown link.
	bareCitation     = regexp.MustCompile(`\[([^\[\]]+)\]`)
	bareCitationBody = regexp.MustCompile(`(?i)^\s*(?:[^,\[\]]*?\s*-\s*)?E\d+(?:\s*,\s*(?:[^,\[\]]*?\s*-\s*)?E\d+)*\s*$`)
	bareCitationPart = regexp.MustCompile(`(?i)^\s*(?:(.*?)\s*-\s*)?(E\d+)\s*$`)
	// Fenced blocks and inline code: a bracket inside quotes output, not a citation.
	markdownCode = regexp.MustCompile("(?s)```.*?```|`[^`\n]*`")
)

// ToolCallRowRef identifies one stored tool-call row.
type ToolCallRowRef struct {
	ID        string    `db:"id"`
	ToolID    string    `db:"tool_id"`
	AgentID   string    `db:"agent_id"`
	MessageID string    `db:"message_id"`
	CreatedAt time.Time `db:"created_at"`
}

type toolCallRowFinder interface {
	FindToolCallRows(conversationID string, toolIDs []string) ([]ToolCallRowRef, error)
}

// citationRowFinder returns the DAO's row lookup, or nil when the DAO in use
// (a test double) has none; citations are then left as they are.
func citationRowFinder() toolCallRowFinder {
	finder, _ := GetConversationDao().(toolCallRowFinder)
	return finder
}

// nextStepCountSeed is where a planner's step counter resumes: past the highest
// E-number already handed out, not just past the number of steps. The two
// differ once a step was dropped as a duplicate or replaced by its retry.
func nextStepCountSeed(steps []NBAgentPlannerToolActionStep) int {
	seed := len(steps)
	for _, s := range steps {
		if m := displayIDNumber.FindStringSubmatch(s.Action.DisplayID); m != nil {
			if n, err := strconv.Atoi(m[1]); err == nil && n > seed {
				seed = n
			}
		}
	}
	return seed
}

// withDisplayIDPrefix labels a tool result with the step id the answer should
// cite, so the model copies the number instead of counting results.
func withDisplayIDPrefix(displayID, content string) string {
	if displayID == "" {
		return content
	}
	return "[" + displayID + "] " + content
}

// StripUnresolvedStepLinks turns every step link that does not point at a
// tool-call row into its plain label. A sub-agent's answer reaches its parent as
// a tool result: links it already carries to its own rows stay valid evidence
// for the parent to cite, but an E-number would be read as one of the parent's
// own steps.
func StripUnresolvedStepLinks(text string) string {
	return anyStepLink.ReplaceAllStringFunc(text, func(link string) string {
		m := anyStepLink.FindStringSubmatch(link)
		if rowIDTarget.MatchString(m[2]) {
			return link
		}
		return m[1]
	})
}

// Reasons a citation is left as plain text.
const (
	citationLinked       = "linked"
	citationUnknownStep  = "unknown_step"  // no step of this run has that E-number
	citationAmbiguous    = "ambiguous"     // the E-number names more than one call
	citationNoRow        = "no_row"        // the step wrote no tool-call row
	citationAmbiguousRow = "ambiguous_row" // several rows fit and none is clearly it
	citationMalformed    = "malformed"     // a step link in no shape we can resolve
)

// linkStepCitations rewrites the E-number citations in an agent's answer to
// links to the tool-call rows of the cited steps. invocations are the steps of
// this agent run. A lookup failure leaves the answer unchanged.
func linkStepCitations(ctx *security.RequestContext, finder toolCallRowFinder, conversationID, agentID, messageID, agentName, accountID, text string, invocations []ToolInvocation) string {
	if finder == nil || agentID == "" {
		return text
	}
	original := text
	text, bare := expandBareCitations(text, func(id string) bool { return hasDisplayID(invocations, id) })
	if !anyStepLink.MatchString(text) {
		return text
	}

	// E-number → the tool ids it names (a row id is looked up per tool id).
	stepToolIDs := map[string][]string{}
	for _, inv := range invocations {
		toolID := inv.SourceToolID
		if toolID == "" {
			toolID = inv.Call.ID
		}
		if inv.DisplayID == "" || toolID == "" {
			continue
		}
		if !slices.Contains(stepToolIDs[inv.DisplayID], toolID) {
			stepToolIDs[inv.DisplayID] = append(stepToolIDs[inv.DisplayID], toolID)
		}
	}

	var wanted []string
	for _, m := range stepCitationLink.FindAllStringSubmatch(text, -1) {
		if ids := stepToolIDs[strings.ToUpper(m[2])]; len(ids) == 1 && !slices.Contains(wanted, ids[0]) {
			wanted = append(wanted, ids[0])
		}
	}
	rowIDs := map[string]string{}
	rowProblem := map[string]string{}
	if len(wanted) > 0 {
		rows, err := finder.FindToolCallRows(conversationID, wanted)
		if err != nil {
			ctx.GetLogger().Error("citations: unable to look up tool-call rows, answer keeps step numbers", "agent", agentName, "error", err)
			return original
		}
		rowIDs, rowProblem = pickToolCallRows(rows, wanted, agentID, messageID)
	}

	outcomes := map[string]int{}
	linked := stepCitationLink.ReplaceAllStringFunc(text, func(link string) string {
		m := stepCitationLink.FindStringSubmatch(link)
		label, displayID := m[1], strings.ToUpper(m[2])
		ids := stepToolIDs[displayID]
		reason := ""
		switch {
		case len(ids) == 0:
			reason = citationUnknownStep
		case len(ids) > 1:
			reason = citationAmbiguous
		case rowIDs[ids[0]] != "":
			outcomes[citationLinked]++
			return "[" + label + "](#task-" + rowIDs[ids[0]] + ")"
		default:
			reason = rowProblem[ids[0]]
		}
		outcomes[reason]++
		ctx.GetLogger().Warn("citations: step citation left as plain text", "agent", agentName, "step", displayID, "reason", reason, "tool_ids", ids)
		return label
	})

	// Whatever step link is left is in a shape no step id matches (a list of
	// ids, stray text in the href); it would ship as a dead link.
	linked = anyStepLink.ReplaceAllStringFunc(linked, func(link string) string {
		m := anyStepLink.FindStringSubmatch(link)
		if rowIDTarget.MatchString(m[2]) {
			return link
		}
		outcomes[citationMalformed]++
		ctx.GetLogger().Warn("citations: step citation left as plain text", "agent", agentName, "href", m[2], "reason", citationMalformed)
		return m[1]
	})

	if len(outcomes) == 0 {
		return linked
	}
	for outcome, n := range outcomes {
		common.MetricsCitationLinks(agentName, outcome, accountID, n)
	}
	ctx.GetLogger().Info("citations: linked step citations to tool-call rows", "agent", agentName, "linked", outcomes[citationLinked], "outcomes", outcomes, "bare_citations_expanded", bare)
	return linked
}

// expandBareCitations turns citations the model wrote without a link into one
// step link per number, so they are linked to rows like any other:
// "[Shell Execute - E1, E9]" becomes "[Shell Execute - E1](#task-E1), [Shell
// Execute - E9](#task-E9)"; a number without a label takes the one before it.
// Brackets that hold anything else or a number this run has no step for (an
// error code like [HTTP - E404]), and markdown links and images, are kept.
func expandBareCitations(text string, isStep func(string) bool) (string, int) {
	matches := bareCitation.FindAllStringSubmatchIndex(text, -1)
	if len(matches) == 0 {
		return text, 0
	}
	code := markdownCode.FindAllStringIndex(text, -1)
	inCode := func(pos int) bool {
		for _, c := range code {
			if pos >= c[0] && pos < c[1] {
				return true
			}
		}
		return false
	}
	var b strings.Builder
	last, expanded := 0, 0
	for _, m := range matches {
		start, end := m[0], m[1]
		body := text[m[2]:m[3]]
		isLink := end < len(text) && text[end] == '('
		isImage := start > 0 && text[start-1] == '!'
		if isLink || isImage || inCode(start) || !bareCitationBody.MatchString(body) || !allSteps(body, isStep) {
			continue
		}
		b.WriteString(text[last:start])
		expanded++
		label := ""
		for i, part := range strings.Split(body, ",") {
			pm := bareCitationPart.FindStringSubmatch(part)
			if pm == nil {
				continue
			}
			if pm[1] != "" {
				label = pm[1]
			}
			step := strings.ToUpper(pm[2])
			shown := step
			if label != "" {
				shown = label + " - " + step
			}
			if i > 0 {
				b.WriteString(", ")
			}
			b.WriteString("[" + shown + "](#task-" + step + ")")
		}
		last = end
	}
	b.WriteString(text[last:])
	return b.String(), expanded
}

// allSteps reports whether every number in a citation list names a step.
func allSteps(body string, isStep func(string) bool) bool {
	for _, part := range strings.Split(body, ",") {
		pm := bareCitationPart.FindStringSubmatch(part)
		if pm == nil || !isStep(strings.ToUpper(pm[2])) {
			return false
		}
	}
	return true
}

func hasDisplayID(invocations []ToolInvocation, id string) bool {
	for _, inv := range invocations {
		if inv.DisplayID == id {
			return true
		}
	}
	return false
}

// pickToolCallRows chooses, per tool id, the row the answer's step wrote. A
// tool id repeats when the same call ran in another turn or agent, so rows of
// this agent win, then the current message, then the latest. A row of another
// agent is used only when it is the only one: a turn-cache hit can reuse a
// result another agent's call stored.
func pickToolCallRows(rows []ToolCallRowRef, toolIDs []string, agentID, messageID string) (map[string]string, map[string]string) {
	byToolID := map[string][]ToolCallRowRef{}
	for _, r := range rows {
		byToolID[r.ToolID] = append(byToolID[r.ToolID], r)
	}
	picked := map[string]string{}
	problem := map[string]string{}
	for _, toolID := range toolIDs {
		candidates := byToolID[toolID]
		var own []ToolCallRowRef
		for _, r := range candidates {
			if r.AgentID == agentID {
				own = append(own, r)
			}
		}
		switch {
		case len(own) > 0:
			sort.SliceStable(own, func(i, j int) bool {
				iCurrent, jCurrent := own[i].MessageID == messageID, own[j].MessageID == messageID
				if iCurrent != jCurrent {
					return iCurrent
				}
				return own[i].CreatedAt.After(own[j].CreatedAt)
			})
			picked[toolID] = own[0].ID
		case len(candidates) == 1:
			picked[toolID] = candidates[0].ID
		case len(candidates) == 0:
			problem[toolID] = citationNoRow
		default:
			problem[toolID] = citationAmbiguousRow
		}
	}
	return picked, problem
}
