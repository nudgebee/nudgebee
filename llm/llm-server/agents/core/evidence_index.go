package core

import (
	"fmt"
	"strings"

	"nudgebee/llm/config"
	"nudgebee/llm/security"

	"github.com/google/uuid"
)

// fsEvidenceIndexMaxFiles bounds the evidence index injected into the prompt so a
// long conversation cannot grow the (uncached) human message without limit.
const fsEvidenceIndexMaxFiles = 20

// FileEvidenceRef is a workspace file a tool saved during the conversation — the
// durable pointer half of the FS evidence layer. The raw bytes live in the
// ephemeral workspace file; this record survives in the DB and is what a later
// message/iteration uses to recall (grep) instead of re-fetching. See
// WORKSPACE_FS_EVIDENCE_RECALL_SPEC.md.
type FileEvidenceRef struct {
	MessageId   string
	ToolName    string
	File        string
	Description string
}

// ListConversationFileRefs returns the workspace files saved by tools across a
// conversation (Type:"file" references on tool calls), most-recent first, capped
// at limit. Sources the file_refs the fetch tools already persist so a later
// message or iteration can grep them by their EXACT name instead of re-fetching
// or guessing.
func (chat *ConversationDao) ListConversationFileRefs(accountId, conversationId string, limit int) ([]FileEvidenceRef, error) {
	if _, err := uuid.Parse(conversationId); err != nil {
		return nil, nil
	}
	// references is a text column holding a JSON array; COALESCE+NULLIF default
	// empty/NULL to '[]' before the ::jsonb cast so jsonb_array_elements always
	// gets an array, and unrolls it.
	query := `
		SELECT t.message_id, t.tool_name, ref->>'url' AS file, COALESCE(ref->>'description', '') AS descr
		FROM llm_conversation_tool_calls t
		CROSS JOIN LATERAL jsonb_array_elements(COALESCE(NULLIF(t."references", ''), '[]')::jsonb) ref
		WHERE t.conversation_id = $1 AND t.account_id = $2
		  AND ref->>'type' = 'file' AND COALESCE(ref->>'url', '') <> ''
		ORDER BY t.created_at DESC
		LIMIT $3`
	rows, err := chat.dbManager.Db.Queryx(query, conversationId, accountId, limit)
	if err != nil {
		return nil, fmt.Errorf("evidence index: failed to list file refs: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var refs []FileEvidenceRef
	for rows.Next() {
		var r FileEvidenceRef
		if err := rows.Scan(&r.MessageId, &r.ToolName, &r.File, &r.Description); err != nil {
			return nil, fmt.Errorf("evidence index: failed to scan file ref: %w", err)
		}
		refs = append(refs, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("evidence index: error iterating file refs: %w", err)
	}
	return refs, nil
}

// buildEvidenceIndex renders the file list into a compact, always-visible block
// that names the EXACT saved filenames — the fix for the model inventing
// plausible-but-wrong names instead of using the real file_ref. Returns "" when
// there are no files (renders nothing).
func buildEvidenceIndex(refs []FileEvidenceRef) string {
	if len(refs) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("**Evidence already gathered (workspace files).** The raw output of earlier tool calls is saved to these EXACT files. To reuse it, `grep`/`head` the filename below via shell_execute — do NOT re-run the tool and do NOT guess a filename:\n")
	for _, r := range refs {
		if r.Description != "" {
			fmt.Fprintf(&b, "- `%s` (%s): %s\n", r.File, r.ToolName, r.Description)
		} else {
			fmt.Fprintf(&b, "- `%s` (%s)\n", r.File, r.ToolName)
		}
	}
	return b.String()
}

// fetchEvidenceIndex builds the evidence-index block for a request when the FS
// evidence-recall flag is on. Best-effort — a DB error yields an empty index so
// the feature can never block planning. This is the cross-message half of the
// FS evidence layer: a new message sees the files earlier messages saved.
func fetchEvidenceIndex(ctx *security.RequestContext, request NBAgentRequest) string {
	if !config.Config.LlmServerFsEvidenceRecallEnabled || request.ConversationId == "" {
		return ""
	}
	refs, err := GetConversationDao().ListConversationFileRefs(request.AccountId, request.ConversationId, fsEvidenceIndexMaxFiles)
	if err != nil {
		if ctx != nil {
			ctx.GetLogger().Warn("evidence index: failed to load file refs", "error", err)
		}
		return ""
	}
	// Telemetry: injection frequency + file count, queryable via logs to gauge
	// adoption (pair with the offline "grep target not in references" metric).
	if len(refs) > 0 && ctx != nil {
		ctx.GetLogger().Info("evidence index: injected", "files", len(refs), "conversation_id", request.ConversationId)
	}
	return buildEvidenceIndex(refs)
}
