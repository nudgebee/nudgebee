package watch

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
)

// responderDBTimeout caps the SELECT-then-UPDATE pair the responder runs.
// The responder is called from the dispatcher's worker pool on every
// terminal transition; a hung DB connection would otherwise pin a worker
// indefinitely and starve other watches' terminal handling.
const responderDBTimeout = 5 * time.Second

// statusEmoji is purely cosmetic — gives the rendered block a glanceable
// status indicator that matches the watch chip's tone in the UI.
func statusEmoji(s Status) string {
	switch s {
	case StatusCompleted:
		return "✅"
	case StatusFailed:
		return "❌"
	case StatusExpired:
		return "⌛"
	case StatusCancelled:
		return "🚫"
	default:
		return "📡"
	}
}

// renderWatchUpdateBlock formats the LLM-generated summary with a
// consistent visual frame. Markdown only — the existing chat renderer
// handles `---`, bold, and blockquote without any UI changes. The
// HTML-comment marker on the first line is the idempotency token: the
// responder skips append if it sees the marker already present in the
// target message (defends against dispatcher misfires that re-call
// terminate for the same watch).
func renderWatchUpdateBlock(w Watch, status Status, summary string) string {
	marker := fmt.Sprintf("<!-- watch-update:%s -->", w.ID.String())
	// Neutralize any HTML-comment sequences in the summary before embedding it.
	// The summary is derived from untrusted observation data (command stdout,
	// logs, an LLM rephrasing of them). If it contained another watch's literal
	// marker `<!-- watch-update:<uuid> -->`, that watch's idempotency check
	// (strings.Contains(existingResponse, marker)) would see its marker already
	// present and silently skip its real append. Stripping `<!--`/`-->` also
	// closes a general stored-HTML-comment injection vector into the message.
	body := neutralizeHTMLComments(strings.TrimSpace(summary))
	if body == "" {
		body = "(no summary available)"
	}
	return fmt.Sprintf(
		"\n\n---\n\n%s\n**%s Watch update — %s**\n\n> %s\n",
		marker,
		statusEmoji(status),
		string(status),
		strings.ReplaceAll(body, "\n", "\n> "),
	)
}

// neutralizeHTMLComments defuses HTML-comment delimiters in untrusted summary
// text so it can't forge the responder's idempotency marker or inject a raw
// comment into the stored message. Replaces the opening/closing tokens with a
// visually-similar but inert form rather than deleting content.
func neutralizeHTMLComments(s string) string {
	// HTML-escape the comment-open `<` so the result can neither match the
	// literal `<!-- watch-update:UUID -->` marker substring nor open a real
	// HTML comment. `&lt;` renders as a literal `<` in the markdown view, so
	// the user still sees the intended characters.
	s = strings.ReplaceAll(s, "<!--", "&lt;!--")
	s = strings.ReplaceAll(s, "-->", "--&gt;")
	return s
}

// AppendWatchUpdateToConversation appends the rendered watch-update
// block to the most recent response message of the watch's parent
// conversation. Idempotent: re-running with the same watch.ID is a
// no-op. Returns nil if the conversation has no response message yet
// (rare — only happens if the watch terminates before the agent's reply
// is persisted, in which case we silently skip rather than error).
func (m *Manager) AppendWatchUpdateToConversation(ctx context.Context, w Watch, status Status, summary string) error {
	db, err := m.dbHandle()
	if err != nil {
		return fmt.Errorf("watch.responder: db handle: %w", err)
	}
	return appendWatchUpdate(ctx, db, w, status, summary)
}

func appendWatchUpdate(ctx context.Context, db *sqlx.DB, w Watch, status Status, summary string) error {
	if w.TenantID == uuid.Nil {
		return fmt.Errorf("watch.responder: tenant_id is required")
	}
	block := renderWatchUpdateBlock(w, status, summary)
	marker := fmt.Sprintf("<!-- watch-update:%s -->", w.ID.String())

	dbCtx, cancel := context.WithTimeout(ctx, responderDBTimeout)
	defer cancel()

	// Locate the most recent response message in this conversation.
	// We target the latest response rather than a specific message_id
	// because the Watch row does not store the parent message_id (would
	// require a schema change) and "latest response" matches typical
	// usage where the watch is a follow-up to whatever the agent last
	// said.
	// The agent's user-facing reply is stored in the `response` column of
	// the `generation`-typed row that holds the user's prompt in `message`.
	// (There is no separate `response`-typed row — same physical row carries
	// both halves of the turn.) Target the most recent generation that has
	// a non-null response so we land on the latest agent reply, even if
	// the conversation later got route/followup-typed rows appended.
	//
	// Tenant-scoped via JOIN to llm_conversations (the messages table itself
	// does not carry tenant_id; the parent conversation row does). Defense-
	// in-depth alongside the conversation_id keying — a future ID-generation
	// change can't silently allow cross-tenant appends.
	var (
		messageID        uuid.UUID
		existingResponse string
	)
	// Prefer the explicit parent_message_id captured at watch-create time
	// (see Watch.ParentMessageID). That column points at the exact agent
	// reply the watch was registered against — the only correct target if
	// the user kept chatting while the watch was running, in which case
	// the "latest generation" fallback would land on an unrelated reply.
	// Fall back to the latest-generation lookup for legacy rows whose
	// parent_message_id is NULL (e.g. created before the V735 column landed).
	if w.ParentMessageID != nil {
		row := db.QueryRowContext(dbCtx, `
			SELECT m.id, COALESCE(m.response, '')
			FROM llm_conversation_messages m
			JOIN llm_conversations c ON c.id = m.conversation_id
			WHERE m.id = $1 AND m.conversation_id = $2 AND c.tenant_id = $3
		`, *w.ParentMessageID, w.ConversationID, w.TenantID)
		if err := row.Scan(&messageID, &existingResponse); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				// The parent message vanished (conversation/message deleted
				// between watch registration and termination) or belongs to
				// a different tenant — silent skip, same posture as the
				// fallback path.
				return nil
			}
			slog.Error("watch.responder: lookup of parent_message_id failed",
				"watch_id", w.ID.String(), "tenant_id", w.TenantID.String(),
				"parent_message_id", w.ParentMessageID.String(), "error", err)
			return fmt.Errorf("watch.responder: parent message lookup failed: %w", err)
		}
	} else {
		row := db.QueryRowContext(dbCtx, `
			SELECT m.id, m.response
			FROM llm_conversation_messages m
			JOIN llm_conversations c ON c.id = m.conversation_id
			WHERE m.conversation_id = $1 AND c.tenant_id = $2 AND m.message_type = 'generation' AND m.response IS NOT NULL
			ORDER BY m.created_at DESC
			LIMIT 1
		`, w.ConversationID, w.TenantID)
		if err := row.Scan(&messageID, &existingResponse); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				// Agent's reply hasn't been persisted yet (or the conversation
				// belongs to a different tenant — defense-in-depth) — skip
				// silently. The notifier path is unaffected; only the in-thread
				// append is a no-op.
				return nil
			}
			// Any other error (connection drop, timeout, scan failure) is a
			// real failure: return it so the caller logs it and increments
			// nb_llm_watch_responder_failures instead of silently dropping the
			// in-thread follow-up.
			slog.Error("watch.responder: lookup of target message failed",
				"watch_id", w.ID.String(), "tenant_id", w.TenantID.String(), "error", err)
			return fmt.Errorf("watch.responder: target message lookup failed: %w", err)
		}
	}

	// Idempotency: if the marker is already present, do nothing. This is the
	// fast path for the SAME-watch-fires-twice case (dispatcher retry / lost
	// MarkTerminal race) and avoids burning a transaction on a no-op.
	if strings.Contains(existingResponse, marker) {
		return nil
	}

	// Atomic check-and-append: the UPDATE re-reads `response` server-side and
	// concatenates the rendered block ONLY if no marker for THIS watch is
	// present. Critically, the predicate checks `position($marker IN response)
	// = 0` against the current row state, not the snapshot we SELECTed above
	// — so two distinct watches racing on the same message both succeed
	// (different markers) and neither overwrites the other. Without this the
	// SELECT-then-UPDATE pair was a TOCTOU window: two watches read identical
	// `existingResponse`, both built `existing + block(self)`, second UPDATE
	// silently dropped the first watch's append.
	//
	// Tenant scope re-asserted via JOIN so a forged message_id can't write
	// into another tenant's row.
	res, err := db.ExecContext(dbCtx, `
		UPDATE llm_conversation_messages AS m
		SET response = COALESCE(m.response, '') || $1, updated_at = now()
		FROM llm_conversations c
		WHERE c.id = m.conversation_id
		  AND m.id = $2
		  AND c.tenant_id = $3
		  AND position($4 IN COALESCE(m.response, '')) = 0
	`, block, messageID, w.TenantID, marker)
	if err != nil {
		return fmt.Errorf("watch.responder: append failed: %w", err)
	}
	if rows, _ := res.RowsAffected(); rows == 0 {
		// Either someone else (this same watch on a retry) wrote our marker
		// concurrently, or the row vanished. Both are no-op cases for the
		// responder; the notifier path is unaffected.
		return nil
	}
	return nil
}
