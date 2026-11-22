package knowledge

import (
	"log/slog"

	"github.com/google/uuid"

	"nudgebee/llm/common"
)

// A conversation whose workspace holds a synced skill's folder (its scripts)
// asks before any code runs: the scripts are third-party code, and a copy or an
// inline rewrite of them must not run unasked either.
//
// The conversation's own tool-call record says whether that happened: copying
// a folder is a load_skills call with a "<skill>/" name, recorded before the
// tool runs. No separate state to keep in step, nothing that can expire while
// the files are there, and a lookup that fails asks rather than lets code run.

// ConversationHasSkillScripts reports whether the conversation asked
// load_skills for a skill folder ("<skill>/"), in any turn, whatever came of
// it. When the lookup fails it answers yes. A variable for tests.
var ConversationHasSkillScripts = func(accountId, conversationId string) bool {
	if accountId == "" || conversationId == "" {
		return false
	}
	if _, err := uuid.Parse(conversationId); err != nil {
		return false // not a stored conversation, so no folder was copied for it
	}
	dbms, err := common.GetDatabaseManager(common.Metastore)
	if err != nil {
		slog.Warn("skill scripts: lookup failed, asking for approval", "conversation_id", conversationId, "error", err)
		return true
	}
	var found bool
	// Matches '/"' anywhere in the arguments: a folder name ends in "/" under
	// skill_name or a legacy alias. A stray match only asks for approval.
	err = dbms.Db.Get(&found, `
		SELECT EXISTS (
			SELECT 1 FROM llm_conversation_tool_calls
			WHERE tool_name = 'load_skills' AND conversation_id = $1::uuid AND account_id = $2
			  AND position('/"' IN parameters::text) > 0)`,
		conversationId, accountId)
	if err != nil {
		slog.Warn("skill scripts: lookup failed, asking for approval", "conversation_id", conversationId, "error", err)
		return true
	}
	return found
}
