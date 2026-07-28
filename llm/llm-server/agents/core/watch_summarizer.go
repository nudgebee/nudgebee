package core

import (
	"context"
	"fmt"
	"nudgebee/llm/agents/prompts_repo"
	"nudgebee/llm/common"
	"nudgebee/llm/security"
	"nudgebee/llm/watch"
	"strings"
	"text/template"
	"time"

	"github.com/jmoiron/sqlx"
	"github.com/tmc/langchaingo/llms"
)

// summarizerDBTimeout caps the conversation-framing + tool-call lookups
// that feed the watch terminal summary. The summarizer runs inside the
// dispatcher's worker pool — an unresponsive DB connection would
// otherwise pin a worker indefinitely and starve other watches.
const summarizerDBTimeout = 5 * time.Second

// llmSummarizer is the agents/core-side implementation of
// watch.Summarizer. It loads the conversation context (user query,
// agent's tool calls, agent's prior reply) directly from the
// llm_conversation_* tables, fills the watch_completion_summary prompt
// template, and runs it through GenerateAndTrackLLMContent so the cost
// of the summary call lands in the parent conversation's token-usage
// books rather than vanishing as untracked overhead.
type llmSummarizer struct {
	db *sqlx.DB
}

func newLlmSummarizer(_ *security.RequestContext, _ watch.Watch) (watch.Summarizer, error) {
	dbm, err := common.GetDatabaseManager(common.Metastore)
	if err != nil {
		return nil, fmt.Errorf("watch.summarizer: get db: %w", err)
	}
	return &llmSummarizer{db: dbm.Db}, nil
}

// Summarize runs the prompt and returns the LLM's natural-language
// followup. Returns ("", nil) if the LLM produces empty content — the
// executor treats that as "fall back to raw template" rather than as
// an error, so a model hiccup never blocks the terminal transition.
func (s *llmSummarizer) Summarize(ctx *security.RequestContext, w watch.Watch, status watch.Status, rawSummary, rawError string) (string, error) {
	// Pass tenant explicitly to both lookups so the SQL filters by it —
	// defense-in-depth alongside the UUID-keyed lookup. Watches always
	// have a tenant set (Manager.Create rejects uuid.Nil), but the helpers
	// fail-fast on empty anyway so a future caller can't bypass scoping
	// by accident.
	tenantID := w.TenantID.String()
	userQuery, agentReply, err := s.loadConversationFraming(ctx.GetContext(), tenantID, w.ConversationID.String())
	if err != nil {
		ctx.GetLogger().Warn("watch.summarizer: load conversation framing failed; continuing with empty context", "error", err, "watch_id", w.ID.String())
	}
	agentActions, err := s.loadAgentActions(ctx.GetContext(), tenantID, w.ConversationID.String())
	if err != nil {
		ctx.GetLogger().Warn("watch.summarizer: load agent actions failed; continuing with empty actions", "error", err, "watch_id", w.ID.String())
	}

	prompt, err := s.renderPrompt(w, status, rawSummary, rawError, userQuery, agentReply, agentActions)
	if err != nil {
		return "", fmt.Errorf("watch.summarizer: render prompt: %w", err)
	}

	// Gemini (and several other providers) require the first non-system
	// message to be Human-roled — sending a single System message yields
	// "got system message role, want human". Use a tiny System framer +
	// a Human turn carrying the rendered prompt body.
	//
	// Route through GenerateAndTrackLLMContent so the summarization cost
	// shows up in the parent conversation's llm_conversation_token_usage
	// rows. The userId/agentId fields stay empty so the tracker records
	// this as a system-attributed call (no chat user owns it). messageId
	// is left empty too — there is no specific message that "owns" the
	// summary, it's a follow-up to whatever the final response was.
	userID := ""
	if w.UserID != nil {
		userID = w.UserID.String()
	}
	resp, err := GenerateAndTrackLLMContent(
		ctx,
		userID,
		w.AccountID.String(),
		w.ConversationID.String(), // real conversation — bill the summary to it
		"",                        // no owning message
		"watch_completion_summary",
		false, // trackContent — short summary, no need to persist the raw output
		[]llms.MessageContent{
			{Role: llms.ChatMessageTypeSystem, Parts: []llms.ContentPart{llms.TextContent{Text: "You are summarising a background watch outcome for the user. Reply with the message body only — no preamble."}}},
			{Role: llms.ChatMessageTypeHuman, Parts: []llms.ContentPart{llms.TextContent{Text: prompt}}},
		},
		false, // cleanupMarkdown — preserve formatting the prompt asked for
	)
	if err != nil {
		return "", fmt.Errorf("watch.summarizer: llm call: %w", err)
	}
	if resp == nil || len(resp.Choices) == 0 {
		return "", nil
	}
	return strings.TrimSpace(resp.Choices[0].Content), nil
}

func (s *llmSummarizer) renderPrompt(w watch.Watch, status watch.Status, rawSummary, rawError, userQuery, agentReply, agentActions string) (string, error) {
	tmplText := prompts_repo.GetPrompt(prompts_repo.PromptWatchCompletionSummary)
	if tmplText == "" {
		return "", fmt.Errorf("watch.summarizer: empty prompt template")
	}
	tmpl, err := template.New("watch_summary").Option("missingkey=zero").Parse(tmplText)
	if err != nil {
		return "", fmt.Errorf("watch.summarizer: parse template: %w", err)
	}
	predicate := fmt.Sprintf("%s: %s", string(w.PredicateKind), w.PredicateExpr)
	if w.PredicateNegate {
		predicate = "NOT " + predicate
	}
	rawOutcome := pickRawOutcome(w, status, rawSummary, rawError)
	data := map[string]string{
		"user_query":        truncatePromptInput(userQuery, 1500),
		"agent_actions":     truncatePromptInput(agentActions, 2000),
		"agent_reply":       truncatePromptInput(agentReply, 1500),
		"source_command":    truncatePromptInput(extractSourceCommand(w.SourceConfig), 500),
		"predicate_summary": truncatePromptInput(predicate, 300),
		"terminal_status":   string(status),
		"raw_outcome":       truncatePromptInput(rawOutcome, 2000),
	}
	var buf strings.Builder
	if err := tmpl.Execute(&buf, data); err != nil {
		return "", fmt.Errorf("watch.summarizer: execute template: %w", err)
	}
	return buf.String(), nil
}

// pickRawOutcome chooses the most informative raw text for the LLM to
// summarise: COMPLETED -> the matched evidence (final_result), failure
// modes -> the error if present, falling back to the most recent
// poll's stdout when neither has anything more useful.
func pickRawOutcome(w watch.Watch, status watch.Status, rawSummary, rawError string) string {
	switch status {
	case watch.StatusCompleted:
		if strings.TrimSpace(rawSummary) != "" {
			return rawSummary
		}
		if w.FinalResult != nil {
			return *w.FinalResult
		}
	case watch.StatusFailed, watch.StatusExpired:
		if strings.TrimSpace(rawError) != "" {
			return rawError
		}
		if w.Error != nil && *w.Error != "" {
			return *w.Error
		}
	}
	if w.LastPollResult != nil {
		return *w.LastPollResult
	}
	return ""
}

// extractSourceCommand pulls a human-readable command string out of the
// stored source_config JSON. Best-effort: if the JSON shape is unfamiliar
// we fall back to the raw payload so the prompt at least has something.
func extractSourceCommand(sourceConfig string) string {
	// The source_config JSON has shape like:
	//   {"tool_name":"shell_execute","tool_input":{"command":"kubectl ..."}}
	// We don't json.Unmarshal here to keep this dependency-free and
	// because string-search of "command":"..." is cheap and tolerant of
	// schema drift.
	idx := strings.Index(sourceConfig, `"command":"`)
	if idx < 0 {
		return sourceConfig
	}
	rest := sourceConfig[idx+len(`"command":"`):]
	end := strings.Index(rest, `"`)
	for end > 0 && rest[end-1] == '\\' {
		next := strings.Index(rest[end+1:], `"`)
		if next < 0 {
			break
		}
		end += 1 + next
	}
	if end < 0 {
		return sourceConfig
	}
	return rest[:end]
}

func truncatePromptInput(s string, maxChars int) string {
	if len(s) <= maxChars {
		return s
	}
	return s[:maxChars] + "…(truncated)"
}

// loadConversationFraming pulls the user's first question + the agent's
// most recent reply for this conversation. Both queries are tenant-scoped
// via a JOIN to llm_conversations (the messages table itself does not
// carry tenant_id; the parent conversation row does). Defense-in-depth
// alongside the UUID lookup. Runs under a dedicated 5s timeout so the
// dispatcher's worker pool cannot be pinned by an unresponsive DB
// connection.
func (s *llmSummarizer) loadConversationFraming(ctx context.Context, tenantID, conversationID string) (userQuery, agentReply string, err error) {
	if tenantID == "" {
		return "", "", fmt.Errorf("watch.summarizer: tenant_id is required")
	}
	dbCtx, cancel := context.WithTimeout(ctx, summarizerDBTimeout)
	defer cancel()
	// The agent's user-facing exchange lives on the `generation`-typed row:
	// the user's prompt is in `message`, the agent's reply in `response`.
	// (There is no `'question'` or `'response'` message_type — the latter
	// is declared in the enum but never inserted. Mirror the responder.go
	// pattern, which is the canonical reader for these columns.)
	//
	// `userQuery` = earliest generation row's `message` (the prompt the
	// watch was registered in response to).
	// `agentReply` = latest generation row's `response` (whatever the
	// agent's most recent reply was, which is where the watch-update
	// block will land too — same target as the responder).
	row := s.db.QueryRowContext(dbCtx, `
		SELECT COALESCE(m.message,'')
		FROM llm_conversation_messages m
		JOIN llm_conversations c ON c.id = m.conversation_id
		WHERE m.conversation_id = $1 AND c.tenant_id = $2
		  AND m.message_type = 'generation'
		ORDER BY m.created_at ASC
		LIMIT 1
	`, conversationID, tenantID)
	if err = row.Scan(&userQuery); err != nil {
		// No generation row yet — empty user query is acceptable; the
		// summarizer prompt falls back to the agent's action list.
		userQuery = ""
	}
	row = s.db.QueryRowContext(dbCtx, `
		SELECT COALESCE(m.response,'')
		FROM llm_conversation_messages m
		JOIN llm_conversations c ON c.id = m.conversation_id
		WHERE m.conversation_id = $1 AND c.tenant_id = $2
		  AND m.message_type = 'generation' AND m.response IS NOT NULL
		ORDER BY m.created_at DESC
		LIMIT 1
	`, conversationID, tenantID)
	if rerr := row.Scan(&agentReply); rerr != nil {
		agentReply = ""
	}
	return userQuery, agentReply, nil
}

// loadAgentActions returns a compact line-per-tool summary of every
// tool call in the conversation. We deliberately project just
// (tool_name, parameters) rather than dumping full responses because
// summary prompts are short — including raw observations would blow
// the token budget without adding signal beyond "what was done".
// Tenant-scoped via JOIN to llm_conversations + bounded timeout for
// the same reasons as loadConversationFraming.
func (s *llmSummarizer) loadAgentActions(ctx context.Context, tenantID, conversationID string) (string, error) {
	if tenantID == "" {
		return "", fmt.Errorf("watch.summarizer: tenant_id is required")
	}
	dbCtx, cancel := context.WithTimeout(ctx, summarizerDBTimeout)
	defer cancel()
	rows, err := s.db.QueryContext(dbCtx, `
		SELECT t.tool_name, COALESCE(t.parameters,'')
		FROM llm_conversation_tool_calls t
		JOIN llm_conversations c ON c.id = t.conversation_id
		WHERE t.conversation_id = $1 AND c.tenant_id = $2
		ORDER BY t.created_at ASC
	`, conversationID, tenantID)
	if err != nil {
		return "", fmt.Errorf("query tool calls: %w", err)
	}
	// rows.Close errors are non-actionable here (the data has already been
	// scanned by the loop below) but errcheck flags the bare defer. Wrap so
	// we silence the linter without losing the underlying close semantics.
	defer func() { _ = rows.Close() }()

	var b strings.Builder
	for rows.Next() {
		var name, params string
		if err := rows.Scan(&name, &params); err != nil {
			return "", fmt.Errorf("scan tool call: %w", err)
		}
		b.WriteString("- ")
		b.WriteString(name)
		if p := truncatePromptInput(strings.ReplaceAll(params, "\n", " "), 200); p != "" {
			b.WriteString(" :: ")
			b.WriteString(p)
		}
		b.WriteString("\n")
	}
	if err := rows.Err(); err != nil {
		return "", fmt.Errorf("iterate tool calls: %w", err)
	}
	return b.String(), nil
}
