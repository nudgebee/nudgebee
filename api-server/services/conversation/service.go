package conversation

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jmoiron/sqlx"
	"golang.org/x/sync/errgroup"

	"nudgebee/services/internal/database"
	"nudgebee/services/security"
)

// epoch is the cursor value used when the caller didn't provide `since` —
// causes the delta queries to return everything for the conversation.
var epoch = time.Unix(0, 0).UTC()

// GetConversationDelta returns a conversation shell plus messages/agents/tool_calls
// updated strictly after `req.Since`. When `req.Since` is nil it acts as a full
// fetch (cursor = epoch), suitable for initial page load.
//
// The shell query resolves the conversation_id and serves as the access guard
// (filtering by account_id). The 3 delta queries then run in parallel against
// independent pooled connections — each is bounded by conversation_id and
// `updated_at > $cursor`, so all 3 return small result sets.
//
// We deliberately do NOT wrap these in a REPEATABLE READ transaction. The
// snapshot-consistency hazard (a tool_call referencing an agent the client
// hasn't seen yet) is masked by the client's idempotent merge-by-id: if a row
// arrives without its parent in the same response, the parent shows up on the
// next poll and the merge resolves itself. The parallel-query latency win is
// worth it on high-latency DB connections — 6 sequential round-trips collapse
// into 2 RT waves (shell, then parallel deltas).
func GetConversationDelta(rc *security.RequestContext, req GetConversationDeltaRequest) (*GetConversationDeltaResponse, error) {
	if req.AccountId == "" {
		return nil, errors.New("account_id is required")
	}
	if req.ConversationId == "" && req.SessionId == "" {
		return nil, errors.New("one of conversation_id or session_id is required")
	}
	tenantId := rc.GetSecurityContext().GetTenantId()
	// Fail-fast: every query below filters by tenant_id, so an empty value
	// would silently drop the tenant predicate and let any matching account_id
	// through. Block before we touch the DB.
	if tenantId == "" {
		return nil, errors.New("unauthorized: tenant_id missing from request context")
	}
	if !rc.GetSecurityContext().HasAccountAccess(req.AccountId, security.SecurityAccessTypeRead) {
		return nil, errors.New("unauthorized")
	}

	since := epoch
	if req.Since != nil {
		since = *req.Since
	}

	dbms, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		return nil, fmt.Errorf("failed to get database manager: %w", err)
	}

	ctx := rc.GetContext()

	// 1. Resolve the shell first (gives us conversation_id and acts as the
	//    tenant+account access guard).
	shell, err := fetchShell(ctx, dbms.Db, tenantId, req)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch shell: %w", err)
	}
	if shell == nil {
		// No conversation matched the (tenant, account, conversation_id|session_id)
		// tuple — it was never persisted yet (e.g. an event-analysis shell INSERT
		// that's still queued in the worker), or it belongs to another account.
		// Return empty (not nil) slices: nil slices marshal to JSON `null`, which
		// violates the action's non-null list output type ("expecting not null
		// value for field tool_calls"); `[]` reads as "0 rows so far", which is
		// also the correct answer during the create-then-poll window — so the UI
		// poller doesn't flash a transient FAILED state.
		return &GetConversationDeltaResponse{
			Messages:  []Message{},
			Agents:    []Agent{},
			ToolCalls: []ToolCall{},
			Cursor:    since,
		}, nil
	}

	// 2. Run the 3 delta queries in parallel. Every query joins the root
	//    llm_conversations table and filters by tenant_id — defense-in-depth
	//    against a leak if upstream auth changes break the HasAccountAccess
	//    guard. errgroup cancels the others on the first error so we don't
	//    waste round-trips when one fails.
	g, gctx := errgroup.WithContext(ctx)
	var (
		messages  []Message
		agents    []Agent
		toolCalls []ToolCall
	)
	g.Go(func() error {
		m, err := fetchMessages(gctx, dbms.Db, tenantId, shell.Id, since)
		if err != nil {
			return fmt.Errorf("failed to fetch messages: %w", err)
		}
		messages = m
		return nil
	})
	g.Go(func() error {
		a, err := fetchAgents(gctx, dbms.Db, tenantId, shell.Id, since)
		if err != nil {
			return fmt.Errorf("failed to fetch agents: %w", err)
		}
		agents = a
		return nil
	})
	g.Go(func() error {
		t, err := fetchToolCalls(gctx, dbms.Db, tenantId, shell.Id, since)
		if err != nil {
			return fmt.Errorf("failed to fetch tool calls: %w", err)
		}
		toolCalls = t
		return nil
	})
	if err := g.Wait(); err != nil {
		return nil, err
	}

	return &GetConversationDeltaResponse{
		Conversation: shell,
		Messages:     messages,
		Agents:       agents,
		ToolCalls:    toolCalls,
		Cursor:       computeCursor(since, shell.UpdatedAt, messages, agents, toolCalls),
	}, nil
}

const shellQuery = `
SELECT
    c.id::text                AS id,
    c.session_id              AS session_id,
    c.account_id::text        AS account_id,
    c.tenant_id::text         AS tenant_id,
    c.user_id::text           AS user_id,
    u.display_name            AS user_display_name,
    c.created_at              AS created_at,
    c.updated_at              AS updated_at,
    c.source                  AS source,
    c.context                 AS context,
    c.status                  AS status,
    c.title                   AS title
FROM llm_conversations c
LEFT JOIN users u ON u.id = c.user_id
WHERE c.tenant_id = $1::uuid
  AND c.account_id = $2
  AND ($3::uuid IS NULL OR c.id = $3::uuid)
  AND ($4::text IS NULL OR c.session_id = $4::text)
LIMIT 1
`

func fetchShell(ctx context.Context, db *sqlx.DB, tenantId string, req GetConversationDeltaRequest) (*ConversationShell, error) {
	var convId, sessionId interface{}
	if req.ConversationId != "" {
		convId = req.ConversationId
	}
	if req.SessionId != "" {
		sessionId = req.SessionId
	}

	var shell ConversationShell
	err := db.GetContext(ctx, &shell, shellQuery, tenantId, req.AccountId, convId, sessionId)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &shell, nil
}

// Each delta query joins the root llm_conversations table and filters by
// tenant_id — even though shell.Id has already been vetted by the shell query,
// the explicit filter in SQL is the defense-in-depth review feedback asked for.
// llm_conversations is keyed by primary-key on id, so the extra join is one
// row lookup per query — negligible.
const messagesQuery = `
SELECT
    m.id::text                  AS id,
    m.user_id::text             AS user_id,
    mu.display_name             AS user_display_name,
    m.created_at                AS created_at,
    m.updated_at                AS updated_at,
    m.message                   AS message,
    m.message_type              AS message_type,
    m.response                  AS response,
    m.role                      AS role,
    m.status                    AS status,
    m.parent_agent_id::text     AS parent_agent_id,
    m.message_config            AS message_config,
    m.ack_message               AS ack_message,
    -- Correlated json_agg subquery (not a LEFT JOIN — avoids message-row
    -- fan-out). Scoped by message_id + conversation_id; m is already
    -- tenant-vetted by the c.tenant_id filter below, so no extra account
    -- plumbing is needed. COALESCE so the column is never NULL.
    COALESCE((
        SELECT json_agg(json_build_object(
            'id', a.id::text,
            'mime_type', a.mime_type,
            'size_bytes', a.size_bytes,
            'description', a.description,
            'created_at', a.created_at,
            'data', a.data
        ) ORDER BY a.created_at)
        FROM llm_conversation_attachment_refs r
        JOIN llm_conversation_attachments a ON a.id = r.attachment_id
        WHERE r.message_id = m.id
          AND r.conversation_id = m.conversation_id
    ), '[]') AS attachments
FROM llm_conversation_messages m
JOIN llm_conversations c ON c.id = m.conversation_id
LEFT JOIN users mu ON mu.id = m.user_id
WHERE c.tenant_id = $1::uuid
  AND m.conversation_id = $2::uuid
  AND m.message_type IN ('generation', 'followup')
  AND m.updated_at > $3
ORDER BY m.created_at ASC
`

func fetchMessages(ctx context.Context, db *sqlx.DB, tenantId, conversationId string, since time.Time) ([]Message, error) {
	messages := []Message{}
	if err := db.SelectContext(ctx, &messages, messagesQuery, tenantId, conversationId, since); err != nil {
		return nil, err
	}
	// json_agg can't be struct-scanned by sqlx; unmarshal the raw blob.
	// COALESCE in the query guarantees AttachmentsRaw is at least "[]", which
	// Unmarshal handles correctly into an empty slice.
	for i := range messages {
		if err := json.Unmarshal(messages[i].AttachmentsRaw, &messages[i].Attachments); err != nil {
			return nil, fmt.Errorf("fetchMessages: decode attachments for message %s: %w", messages[i].Id, err)
		}
	}
	return messages, nil
}

// agents and tool_calls don't carry conversation_id today, so we hop via
// llm_conversation_messages. Indexes used:
//   - idx_llm_conv_messages_conversation_id  (V646)
//   - idx_llm_conv_agent_message_updated     (V731)
//   - idx_llm_conv_tool_calls_agent_updated  (V731)
//
// If conversation_id is later denormalized onto these tables, both queries
// become a single-table lookup with no join. The llm_conversations join is
// for tenant_id defense-in-depth and resolves via primary key, so it's
// effectively free.
const agentsQuery = `
SELECT
    a.id::text              AS id,
    a.message_id::text      AS message_id,
    a.agent_name            AS agent_name,
    a.response              AS response,
    a.response_summary      AS response_summary,
    a.query                 AS query,
    a.thought               AS thought,
    a.parent_agent_id::text AS parent_agent_id,
    a.status                AS status,
    a.references            AS references,
    a.created_at            AS created_at,
    a.updated_at            AS updated_at
FROM llm_conversation_agent a
JOIN llm_conversation_messages m ON m.id = a.message_id
JOIN llm_conversations c ON c.id = m.conversation_id
WHERE c.tenant_id = $1::uuid
  AND m.conversation_id = $2::uuid
  AND a.updated_at > $3
ORDER BY a.created_at ASC
`

func fetchAgents(ctx context.Context, db *sqlx.DB, tenantId, conversationId string, since time.Time) ([]Agent, error) {
	agents := []Agent{}
	if err := db.SelectContext(ctx, &agents, agentsQuery, tenantId, conversationId, since); err != nil {
		return nil, err
	}
	return agents, nil
}

const toolCallsQuery = `
SELECT
    t.id::text             AS id,
    t.agent_id::text       AS agent_id,
    t.tool_name            AS tool_name,
    t.parameters           AS parameters,
    t.response             AS response,
    t.thought              AS thought,
    t.tool_type            AS tool_type,
    t.child_agent_id::text AS child_agent_id,
    t.references           AS references,
    t.tool_id              AS tool_id,
    t.status               AS status,
    t.created_at           AS created_at,
    t.updated_at           AS updated_at
FROM llm_conversation_tool_calls t
JOIN llm_conversation_agent a ON a.id = t.agent_id
JOIN llm_conversation_messages m ON m.id = a.message_id
JOIN llm_conversations c ON c.id = m.conversation_id
WHERE c.tenant_id = $1::uuid
  AND m.conversation_id = $2::uuid
  AND t.updated_at > $3
ORDER BY t.created_at ASC
`

func fetchToolCalls(ctx context.Context, db *sqlx.DB, tenantId, conversationId string, since time.Time) ([]ToolCall, error) {
	toolCalls := []ToolCall{}
	if err := db.SelectContext(ctx, &toolCalls, toolCallsQuery, tenantId, conversationId, since); err != nil {
		return nil, err
	}
	return toolCalls, nil
}

// computeCursor returns max(updated_at) across the shell + all returned rows.
// On a fully-empty delta (no changes since `since`) it returns `since` so the
// client's cursor stays put.
func computeCursor(since, shellUpdated time.Time, messages []Message, agents []Agent, toolCalls []ToolCall) time.Time {
	cursor := since
	if shellUpdated.After(cursor) {
		cursor = shellUpdated
	}
	for _, m := range messages {
		if m.UpdatedAt.After(cursor) {
			cursor = m.UpdatedAt
		}
	}
	for _, a := range agents {
		if a.UpdatedAt.After(cursor) {
			cursor = a.UpdatedAt
		}
	}
	for _, t := range toolCalls {
		if t.UpdatedAt.After(cursor) {
			cursor = t.UpdatedAt
		}
	}
	return cursor
}
