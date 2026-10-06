package tools

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"nudgebee/llm/common"
	"nudgebee/llm/security"
	"nudgebee/llm/tools/core"
)

// EventAccountNoteKey is the result field telling the model that an event
// was found on another account.
const EventAccountNoteKey = "event_account_note"

// eventOwnerLookup is the account an event belongs to, within the caller's
// tenant ("" when there is none). A variable so tests can stub it.
var eventOwnerLookup = func(ctx *security.RequestContext, eventID string) (string, error) {
	sc := ctx.GetSecurityContext()
	if sc == nil {
		return "", nil
	}
	dbms, err := common.GetDatabaseManager(common.Metastore)
	if err != nil {
		return "", err
	}
	row, err := dbms.QueryRow(`SELECT e.cloud_account_id::text FROM events e
		JOIN cloud_accounts ca ON ca.id = e.cloud_account_id
		WHERE e.id = $1::uuid AND ca.tenant::text = $2`, eventID, sc.GetTenantId())
	if err != nil {
		return "", err
	}
	var account string
	if err := row.Scan(&account); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", nil // not found in this tenant
		}
		return "", err
	}
	return account, nil
}

// onEventAccount runs an event-id lookup on the account the event belongs
// to. An event id is unique, so a lookup by id needs no account to be named:
// when the event is on another account the caller can read, the call runs
// there, the step is labelled with that account, and the result says so.
// A call already scoped to another account (a named or handed target) and
// an event the caller cannot read are left as they are; a call scoped to the
// conversation's own account, named or not, follows the event.
func onEventAccount(nbCtx core.NbToolContext, eventID string, call func(core.NbToolContext) (core.NBToolResponse, error)) (core.NBToolResponse, error) {
	if nbCtx.Ctx == nil || nbCtx.DataAccountID() != nbCtx.AccountId {
		return call(nbCtx)
	}
	owner, err := eventOwnerLookup(nbCtx.Ctx, eventID)
	if err != nil {
		nbCtx.Ctx.GetLogger().Warn("event owner lookup failed; reading the conversation's account", "error", err, "event_id", eventID)
		return call(nbCtx)
	}
	if owner == "" || owner == nbCtx.AccountId {
		return call(nbCtx)
	}
	sc := nbCtx.Ctx.GetSecurityContext()
	if sc == nil || !sc.HasAccountAccess(owner, security.SecurityAccessTypeRead) {
		return call(nbCtx)
	}
	// Only move when the step can say where it ran.
	described := AccountTarget(owner)
	if described == nil || described.Name == "" {
		return call(nbCtx)
	}
	target := &core.ExecutionTarget{Provider: described.Provider, Name: described.Name, AccountID: owner}
	moved := nbCtx
	moved.TargetAccountId = owner
	resp, err := call(moved)
	resp.AdditionalDetails = core.WithExecutedTarget(resp.AdditionalDetails, target)
	if err == nil && resp.Status != core.NBToolResponseStatusError {
		resp.Data = withEventAccountNote(resp.Data, eventAccountNote(eventID, target))
	}
	return resp, err
}

func eventAccountNote(eventID string, target *core.ExecutionTarget) string {
	return fmt.Sprintf("Event %s belongs to the %s %q, not this conversation's account. Say so in the answer, and pass target {\"provider\":%q,\"name\":%q} on follow-up calls about it (logs, metrics, kubectl).",
		eventID, target.Kind(), target.Name, target.Provider, target.Name)
}

// withEventAccountNote adds the note to a JSON result under
// EventAccountNoteKey (to the object, or once, to the first object of an
// array), keeping it valid JSON with numbers and text unchanged; other text
// gets the note as a first line.
func withEventAccountNote(data, note string) string {
	trimmed := strings.TrimSpace(data)
	encodedNote, _ := json.Marshal(note)
	var object map[string]json.RawMessage
	if json.Unmarshal([]byte(trimmed), &object) == nil && object != nil {
		object[EventAccountNoteKey] = encodedNote
		if out, err := encodeExact(object); err == nil {
			return out
		}
	}
	var rows []map[string]json.RawMessage
	if json.Unmarshal([]byte(trimmed), &rows) == nil && len(rows) > 0 {
		for _, row := range rows {
			if row != nil { // skip JSON null elements
				row[EventAccountNoteKey] = encodedNote
				break // once is enough
			}
		}
		if out, err := encodeExact(rows); err == nil {
			return out
		}
	}
	return note + "\n" + data
}

// encodeExact re-encodes values kept as raw JSON, so numbers keep their
// precision and text is not HTML-escaped.
func encodeExact(v any) (string, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return "", err
	}
	return strings.TrimSuffix(buf.String(), "\n"), nil
}
