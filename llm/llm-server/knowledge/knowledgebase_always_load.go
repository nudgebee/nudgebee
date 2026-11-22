package knowledge

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"unicode/utf8"

	"nudgebee/llm/common"
	"nudgebee/llm/security"
)

// AlwaysLoadMaxChars caps the text of a skill marked "always load" on an agent
// link. The text sits in that agent's prompt on every run, so it is kept to a
// short setup description rather than a full document (#40899).
const AlwaysLoadMaxChars = 4000

var (
	errAlwaysLoadWildcard = errors.New("always load can't be set on the All agents link; use Account Context for text every agent should see")
	errAlwaysLoadKind     = errors.New("always load is only available for user-defined text skills")
	errAlwaysLoadTooLong  = fmt.Errorf("always load is only available for skills of up to %d characters; shorten the skill first", AlwaysLoadMaxChars)
)

// IsAlwaysLoadRuleError reports whether err is a refused always-load request
// (All-agents link, not a text skill, too long) — a caller error, not a fault.
func IsAlwaysLoadRuleError(err error) bool {
	return errors.Is(err, errAlwaysLoadWildcard) || errors.Is(err, errAlwaysLoadKind) || errors.Is(err, errAlwaysLoadTooLong)
}

// alwaysLoadEligible reports why a skill can't be always-loaded, or nil.
func alwaysLoadEligible(kbType, dataFormat, data string) error {
	if kbType != KBTypeManual || dataFormat == string(KBFormatPDF) {
		return errAlwaysLoadKind
	}
	if utf8.RuneCountInString(data) > AlwaysLoadMaxChars {
		return errAlwaysLoadTooLong
	}
	return nil
}

// validateAlwaysLoad checks a link can be marked always-load: never the
// All-agents link, and only a user-defined text skill within the size cap.
func validateAlwaysLoad(dbms *common.DatabaseManager, kbId, agentId string) error {
	if agentId == KBAgentWildcard {
		return errAlwaysLoadWildcard
	}
	var kb struct {
		KBType     string `db:"kb_type"`
		DataFormat string `db:"data_format"`
		Data       string `db:"data"`
	}
	err := dbms.Db.Get(&kb, `
		SELECT COALESCE(kb_type, 'manual') AS kb_type,
		       COALESCE(data_format, '') AS data_format,
		       CASE WHEN data_format = 'pdf' THEN '' ELSE COALESCE(data, '') END AS data
		FROM llm_knowledgebases WHERE id = $1`, kbId)
	if err != nil {
		return fmt.Errorf("validateAlwaysLoad: failed to read knowledge base: %w", err)
	}
	return alwaysLoadEligible(kb.KBType, kb.DataFormat, kb.Data)
}

// validateAlwaysLoadEdit refuses an edit that would make a skill already
// always-loaded on some agent ineligible (too long, or turned into a PDF).
func validateAlwaysLoadEdit(dbms *common.DatabaseManager, kbId, kbType, dataFormat, data string) error {
	var marked bool
	err := dbms.Db.Get(&marked, `SELECT EXISTS(SELECT 1 FROM llm_kb_agent_mappings WHERE kb_id = $1 AND always_load)`, kbId)
	if err != nil {
		return fmt.Errorf("validateAlwaysLoadEdit: failed to read agent links: %w", err)
	}
	if !marked {
		return nil
	}
	if err := alwaysLoadEligible(kbType, dataFormat, data); err != nil {
		return fmt.Errorf("this skill is always loaded on an agent: %w", err)
	}
	return nil
}

// AlwaysLoadSkill is one skill marked always-load on an agent link.
type AlwaysLoadSkill struct {
	ID      string `json:"id" db:"id"`
	Name    string `json:"name" db:"name"`
	AgentId string `json:"agent_id" db:"agent_id"`
	Data    string `json:"data" db:"data"`
}

// alwaysLoadCacheKey caches an account's always-load links in the KB mapping
// namespace, so every write that already clears that namespace or calls
// invalidateKBMappingCache (KB create/update/enable/delete, map/unmap,
// organisation KB changes) clears it too. Most accounts have no marked links;
// caching the empty list keeps the per-agent-run lookup off the database.
func alwaysLoadCacheKey(accountId string) string {
	return "always_load:" + accountId
}

func invalidateAlwaysLoadCache(accountId string) {
	if err := common.CacheDelete(CacheNamespaceLlmKbMapping, alwaysLoadCacheKey(accountId)); err != nil {
		// Usually just "not cached yet"; the entry also expires with the namespace TTL.
		slog.Debug("kb: always-load cache not invalidated", "error", err, "account_id", accountId)
	}
}

// ListAlwaysLoadSkills returns the enabled skills marked always-load on any of
// agentNames for the account, ordered by agentNames then skill name. A skill
// linked under several of the names is returned once, at its first name.
// Indexing status is not checked: the text is read from the row, not from
// search, so a skill applies as soon as it is saved.
func ListAlwaysLoadSkills(sc *security.RequestContext, accountId string, agentNames []string) ([]AlwaysLoadSkill, error) {
	if sc == nil || sc.GetSecurityContext() == nil {
		return nil, errors.New("ListAlwaysLoadSkills: security context is required")
	}
	if accountId == "" || len(agentNames) == 0 {
		return nil, nil
	}
	if !sc.GetSecurityContext().HasAccountAccess(accountId, security.SecurityAccessTypeRead) {
		return nil, errors.New("you don't have permission to access this resource")
	}
	all, err := accountAlwaysLoadSkills(sc, accountId)
	if err != nil {
		return nil, err
	}
	return selectAlwaysLoadSkills(all, agentNames), nil
}

// accountAlwaysLoadSkills returns every always-load link of the account, from
// the cache when present.
func accountAlwaysLoadSkills(sc *security.RequestContext, accountId string) ([]AlwaysLoadSkill, error) {
	key := alwaysLoadCacheKey(accountId)
	if data, ok := common.CacheGet(CacheNamespaceLlmKbMapping, key); ok {
		var cached []AlwaysLoadSkill
		if err := json.Unmarshal(data, &cached); err == nil {
			return cached, nil
		}
	}
	dbms, err := common.GetDatabaseManager(common.Metastore)
	if err != nil {
		return nil, fmt.Errorf("accountAlwaysLoadSkills: failed to get database manager: %w", err)
	}
	scope, scopeArgs := KBAccountScope("kb", 1, 2, VisibleOrgKnowledgebaseIDs(sc.GetContext(), accountId))
	rows := []AlwaysLoadSkill{}
	err = dbms.Db.Select(&rows, `
		SELECT kb.id, kb.name, m.agent_id, COALESCE(kb.data, '') AS data
		FROM llm_kb_agent_mappings m
		INNER JOIN llm_knowledgebases kb ON kb.id = m.kb_id
		WHERE m.account_id = $1
		  AND m.always_load
		  AND kb.enabled
		  AND COALESCE(kb.data_format, '') <> 'pdf'
		  AND `+scope+`
		ORDER BY kb.name`,
		append([]any{accountId}, scopeArgs...)...)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("accountAlwaysLoadSkills: query failed: %w", err)
	}
	if data, err := json.Marshal(rows); err == nil {
		if err := common.CacheSet(CacheNamespaceLlmKbMapping, key, data); err != nil {
			slog.Warn("kb: failed to cache always-load skills", "error", err, "account_id", accountId)
		}
	}
	return rows, nil
}

// selectAlwaysLoadSkills keeps the rows linked under agentNames, ordered by
// agentNames (rows are already sorted by skill name), each skill once.
func selectAlwaysLoadSkills(all []AlwaysLoadSkill, agentNames []string) []AlwaysLoadSkill {
	var out []AlwaysLoadSkill
	seen := make(map[string]bool)
	for _, name := range agentNames {
		for _, r := range all {
			if r.AgentId != name || seen[r.ID] {
				continue
			}
			seen[r.ID] = true
			out = append(out, r)
		}
	}
	return out
}
