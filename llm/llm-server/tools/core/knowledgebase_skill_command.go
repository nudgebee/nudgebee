package core

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/lib/pq"

	"nudgebee/llm/common"
	"nudgebee/llm/security"
)

// SkillCommandMaxChars caps the text a "/skill-name" message puts in front of
// the user's question. Longer skills are cut, with a pointer to load the rest.
const SkillCommandMaxChars = 16000

// SkillCommand is the skill a "/skill-name" chat message names.
type SkillCommand struct {
	ID   string `db:"id"`
	Name string `db:"name"`
	Data string `db:"data"`
}

// GetSkillsForCommand returns, for each of names, the user-defined text skill
// the account can see whose name matches it, ignoring case and with spaces
// typed as hyphens ("db-failover" finds "DB failover"). The account's own skill
// wins over an organisation one. Keyed by the lower-cased name; names that
// match nothing are absent.
func GetSkillsForCommand(sc *security.RequestContext, accountId string, names []string) (map[string]SkillCommand, error) {
	if sc == nil || sc.GetSecurityContext() == nil {
		return nil, errors.New("GetSkillsForCommand: security context is required")
	}
	keys := make([]string, 0, len(names))
	for _, n := range names {
		if n = strings.ToLower(strings.TrimSpace(n)); n != "" {
			keys = append(keys, n)
		}
	}
	if accountId == "" || len(keys) == 0 {
		return nil, nil
	}
	if !sc.GetSecurityContext().HasAccountAccess(accountId, security.SecurityAccessTypeRead) {
		return nil, errors.New("you don't have permission to access this resource")
	}
	dbms, err := common.GetDatabaseManager(common.Metastore)
	if err != nil {
		return nil, fmt.Errorf("GetSkillsForCommand: failed to get database manager: %w", err)
	}
	scope, scopeArgs := KBAccountScope("kb", 1, 3, VisibleOrgKnowledgebaseIDs(sc.GetContext(), accountId))
	var rows []struct {
		Key string `db:"key"`
		SkillCommand
	}
	err = dbms.Db.Select(&rows, `
		SELECT DISTINCT ON (k.key) k.key, kb.id, kb.name, COALESCE(kb.data, '') AS data
		FROM unnest($2::text[]) AS k(key)
		JOIN llm_knowledgebases kb
		  ON LOWER(BTRIM(kb.name)) = k.key OR REPLACE(LOWER(BTRIM(kb.name)), ' ', '-') = k.key
		WHERE `+scope+`
		  AND COALESCE(kb.kb_type, 'manual') = 'manual'
		  AND COALESCE(kb.data_format, '') <> 'pdf'
		  AND kb.status = 'active'
		  AND kb.enabled
		ORDER BY k.key, (kb.account_id IS NULL), kb.name`,
		append([]any{accountId, pq.Array(keys)}, scopeArgs...)...)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("GetSkillsForCommand: query failed: %w", err)
	}
	out := make(map[string]SkillCommand, len(rows))
	for _, r := range rows {
		out[r.Key] = r.SkillCommand
	}
	return out, nil
}
