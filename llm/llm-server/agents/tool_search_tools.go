package agents

import (
	"fmt"
	"html"
	"sort"
	"strings"

	"nudgebee/llm/agents/core"
	"nudgebee/llm/common"
	"nudgebee/llm/security"
	toolcore "nudgebee/llm/tools/core"
)

// SearchToolsToolName is the on-demand capability-discovery tool. It lets a
// trimmed orchestrator find specialist agents / tools that are NOT preloaded in
// its context and learn how to invoke them (via delegate_agent). This is the
// discovery half of the tool-context-reduction design (see
// docs/tool-context-reduction.md); delegate_agent is the invocation half.
const SearchToolsToolName = "search_tools"

// searchToolsMaxResults caps how many capabilities are returned so the
// observation stays small — discovery should narrow, not dump the catalog.
const searchToolsMaxResults = 15

func init() {
	// search_tools is always registered. It is a read-only discovery tool with no
	// execution authority, and only agents that list it in their supported set (the
	// trim/lean orchestrators) can invoke it — the dispatch auth gate rejects it for
	// every other agent. So always-on registration widens no production agent's
	// surface; it only makes the tool resolvable for the agents that opt in.
	toolcore.RegisterNBToolFactory(SearchToolsToolName, func(accountId string) (toolcore.NBTool, error) {
		return &searchToolsTool{accountId: accountId}, nil
	})
}

// searchToolsTool searches the account's enabled agent + tool catalog by a
// natural-language query and returns matching capabilities with their
// descriptions and how to invoke them. It is deliberately read-only.
type searchToolsTool struct {
	accountId string
}

func (t *searchToolsTool) Name() string { return SearchToolsToolName }

func (t *searchToolsTool) GetType() toolcore.NBToolType { return toolcore.NBToolTypeTool }

func (t *searchToolsTool) Description() string {
	return `Discover specialist capabilities (agents and tools) that are NOT already in your tool list, by natural-language query. Returns each match's name, what it does, and how to call it.
Use when a task needs a domain you don't hold a tool for (e.g. "query the postgres database", "inspect a helm release", "search github", "scan an image") — search for it, then invoke the returned capability by name via ` + "`delegate_agent`" + ` (pass its name in "tools").
Input: {"query": <what you need to do>}. Returns at most a handful of ranked matches; refine the query if nothing fits.`
}

func (t *searchToolsTool) InputSchema() toolcore.ToolSchema {
	return toolcore.ToolSchema{
		Type: toolcore.ToolSchemaTypeObject,
		Properties: map[string]toolcore.ToolSchemaProperty{
			"query": {
				Type:        toolcore.ToolSchemaTypeString,
				Description: "Natural-language description of the capability you need (e.g. 'query mysql', 'list helm releases').",
			},
		},
		Required: []string{"query"},
	}
}

// searchToolCandidate is one searchable capability, flattened from either the
// agent catalog or the tool registry into a common shape so ranking/rendering
// stay independent of the source.
type searchToolCandidate struct {
	name        string
	kind        string // "agent" or "tool"
	description string
	aliases     []string
	inputUsage  string // compact leaf-tool param summary; empty for agents (NL input)
}

func (t *searchToolsTool) Call(ctx toolcore.NbToolContext, input toolcore.NBToolCallRequest) (toolcore.NBToolResponse, error) {
	// Defense-in-depth read gate. GetEnabledNBTools self-guards on account read
	// access and returns empty otherwise, but ListAgents (the other enumeration
	// source) has no such guard — so enforce it here before enumerating either.
	if !ctx.Ctx.GetSecurityContext().HasAccountAccess(t.accountId, security.SecurityAccessTypeRead) {
		ctx.Ctx.GetLogger().Warn("tool: search_tools unauthorized — no account read access", "account_id", t.accountId)
		common.MetricsToolOperationsTotal(t.Name(), "error", ctx.AccountId)
		return toolcore.NBToolResponse{
			Status: toolcore.NBToolResponseStatusError,
			Data:   "not authorized to search capabilities for this account",
		}, nil
	}

	query := parseSearchToolsQuery(input)
	if query == "" {
		common.MetricsToolOperationsTotal(t.Name(), "error", ctx.AccountId)
		return toolcore.NBToolResponse{
			Status: toolcore.NBToolResponseStatusError,
			Data:   "query is required",
		}, nil
	}

	ctx.Ctx.GetLogger().Info("tool: search_tools called", "query", query)

	candidates := t.collectCandidates(ctx)
	matched := scoreAndRankSearchTools(query, candidates, searchToolsMaxResults)

	if len(matched) == 0 {
		common.MetricsToolOperationsTotal(t.Name(), "not_found", ctx.AccountId)
		return toolcore.NBToolResponse{
			Status: toolcore.NBToolResponseStatusSuccess,
			Data:   "No matching capabilities found. Try a broader query, or use the tools already in your list.",
			Type:   toolcore.NBToolResponseTypeText,
		}, nil
	}

	ctx.Ctx.GetLogger().Info("tool: search_tools success", "query", query, "match_count", len(matched))
	common.MetricsToolOperationsTotal(t.Name(), "success", ctx.AccountId)
	return toolcore.NBToolResponse{
		Status: toolcore.NBToolResponseStatusSuccess,
		Data:   renderSearchToolsOutput(matched),
		Type:   toolcore.NBToolResponseTypeText,
	}, nil
}

// collectCandidates flattens the account's enabled agents and tools into a
// deduplicated candidate list. Agents come first (richer descriptions +
// aliases); leaf tools not already covered by an agent of the same name are
// appended with a compact input-schema summary.
func (t *searchToolsTool) collectCandidates(ctx toolcore.NbToolContext) []searchToolCandidate {
	seen := make(map[string]bool)
	var candidates []searchToolCandidate

	for _, a := range core.ListAgents(ctx.Ctx, t.accountId, true) {
		if a.Name == "" || a.Status != core.AgentStatusEnabled {
			continue
		}
		lower := strings.ToLower(a.Name)
		if seen[lower] {
			continue
		}
		seen[lower] = true
		kind := "agent"
		if a.ExecutorType == core.AgentPlannerTypeTool {
			kind = "tool"
		}
		candidates = append(candidates, searchToolCandidate{
			name:        a.Name,
			kind:        kind,
			description: a.Description,
			aliases:     a.Aliases,
		})
	}

	for _, tool := range toolcore.GetEnabledNBTools(ctx.Ctx, t.accountId) {
		if tool == nil || tool.Name() == "" {
			continue
		}
		lower := strings.ToLower(tool.Name())
		if seen[lower] {
			continue
		}
		// Don't advertise discovery of the discovery tool itself.
		if lower == SearchToolsToolName {
			continue
		}
		seen[lower] = true
		candidates = append(candidates, searchToolCandidate{
			name:        tool.Name(),
			kind:        "tool",
			description: tool.Description(),
			inputUsage:  summarizeToolInputSchema(tool.InputSchema()),
		})
	}

	return candidates
}

// parseSearchToolsQuery extracts the query from structured Arguments or the raw
// Command, mirroring how the sibling search_skills tool is lenient about shape.
func parseSearchToolsQuery(input toolcore.NBToolCallRequest) string {
	if input.Arguments != nil {
		if q, ok := input.Arguments["query"].(string); ok && strings.TrimSpace(q) != "" {
			return strings.TrimSpace(q)
		}
	}
	return strings.TrimSpace(input.Command)
}

// scoreAndRankSearchTools ranks candidates by token overlap between the query
// and each candidate's name/aliases/description. A candidate matches if it
// shares at least one query token; name/alias hits are weighted above
// description hits so "postgres" surfaces the postgres capability before a tool
// that merely mentions postgres in prose. Ordering is deterministic (score
// desc, then name asc) so repeated calls are stable.
func scoreAndRankSearchTools(query string, candidates []searchToolCandidate, limit int) []searchToolCandidate {
	tokens := toolcore.TokenizeForSkillSelection(query)
	if len(tokens) == 0 {
		return nil
	}

	type scored struct {
		cand  searchToolCandidate
		score int
	}
	var hits []scored
	for _, c := range candidates {
		nameHay := strings.ToLower(c.name + " " + strings.Join(c.aliases, " "))
		descHay := strings.ToLower(c.description)
		score := 0
		for _, tok := range tokens {
			if strings.Contains(nameHay, tok) {
				score += 3
			}
			if strings.Contains(descHay, tok) {
				score++
			}
		}
		if score > 0 {
			hits = append(hits, scored{cand: c, score: score})
		}
	}

	sort.SliceStable(hits, func(i, j int) bool {
		if hits[i].score != hits[j].score {
			return hits[i].score > hits[j].score
		}
		return strings.ToLower(hits[i].cand.name) < strings.ToLower(hits[j].cand.name)
	})

	if limit > 0 && len(hits) > limit {
		hits = hits[:limit]
	}
	out := make([]searchToolCandidate, 0, len(hits))
	for _, h := range hits {
		out = append(out, h.cand)
	}
	return out
}

// renderSearchToolsOutput formats matches into a compact, injection-safe block.
// A single leading instruction tells the planner these capabilities are reached
// via delegate_agent (they are not in its own tool list), so it never emits a
// discovered name as a direct action the executor would reject.
func renderSearchToolsOutput(matched []searchToolCandidate) string {
	var b strings.Builder
	b.WriteString("Matching capabilities. These are NOT in your tool list — to use one, call `delegate_agent` with {\"prompt\": <what to do>, \"tools\": [\"<name>\"]}.\n")
	for _, c := range matched {
		fmt.Fprintf(&b, "\n<capability name=%q kind=%q>\n  <description>%s</description>",
			c.name, c.kind, html.EscapeString(strings.TrimSpace(c.description)))
		if c.inputUsage != "" {
			fmt.Fprintf(&b, "\n  <input>%s</input>", html.EscapeString(c.inputUsage))
		}
		b.WriteString("\n</capability>")
	}
	return b.String()
}

// summarizeToolInputSchema renders a leaf tool's parameters as a compact,
// deterministic one-liner (e.g. "command (required), namespace"). Required
// params are ordered first; within each group, alphabetical for stable output.
func summarizeToolInputSchema(schema toolcore.ToolSchema) string {
	if len(schema.Properties) == 0 {
		return ""
	}
	required := make(map[string]bool, len(schema.Required))
	for _, r := range schema.Required {
		required[r] = true
	}
	var req, opt []string
	for name := range schema.Properties {
		if required[name] {
			req = append(req, name)
		} else {
			opt = append(opt, name)
		}
	}
	sort.Strings(req)
	sort.Strings(opt)

	parts := make([]string, 0, len(req)+len(opt))
	for _, name := range req {
		parts = append(parts, name+" (required)")
	}
	parts = append(parts, opt...)
	return strings.Join(parts, ", ")
}
