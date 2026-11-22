package knowledge

import (
	"database/sql"
	"encoding/json"
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

// SkillFile is one stored file of a multi-file skill.
type SkillFile struct {
	SkillID   string `db:"id"`
	SkillName string `db:"name"`
	Path      string `db:"path"`
	Content   string `db:"content"`
}

// ErrSkillFileNotFound means the skill is visible but has no such file.
var ErrSkillFileNotFound = errors.New("skill file not found")

// GetSkillFile returns a file of an active, enabled text skill the account can
// see, by the skill's name (matched like /skill-name) and the file's path
// relative to the skill folder ("references/api.md"). nil, nil when no such
// skill is visible; ErrSkillFileNotFound when the skill has no such file.
func GetSkillFile(sc *security.RequestContext, accountId, skillName, filePath string) (*SkillFile, error) {
	if sc == nil || sc.GetSecurityContext() == nil {
		return nil, errors.New("GetSkillFile: security context is required")
	}
	key := strings.ToLower(strings.TrimSpace(skillName))
	filePath = strings.TrimPrefix(strings.TrimSpace(filePath), "./")
	if accountId == "" || key == "" || !safeSkillPath(filePath) {
		return nil, nil
	}
	if !sc.GetSecurityContext().HasAccountAccess(accountId, security.SecurityAccessTypeRead) {
		return nil, errors.New("you don't have permission to access this resource")
	}
	dbms, err := common.GetDatabaseManager(common.Metastore)
	if err != nil {
		return nil, fmt.Errorf("GetSkillFile: failed to get database manager: %w", err)
	}
	scope, scopeArgs := KBAccountScope("kb", 1, 4, VisibleOrgKnowledgebaseIDs(sc.GetContext(), accountId))
	var rows []struct {
		SkillFile
		HasFile bool `db:"has_file"`
	}
	err = dbms.Db.Select(&rows, `
		SELECT kb.id, kb.name, $3 AS path, COALESCE(kb.files->>$3, '') AS content, COALESCE(jsonb_exists(kb.files, $3), false) AS has_file
		FROM llm_knowledgebases kb
		WHERE `+scope+`
		  AND (LOWER(BTRIM(kb.name)) = $2 OR REPLACE(LOWER(BTRIM(kb.name)), ' ', '-') = $2)
		  AND COALESCE(kb.kb_type, 'manual') = 'manual'
		  AND kb.status = 'active'
		  AND kb.enabled
		ORDER BY (kb.account_id IS NULL), kb.name
		LIMIT 1`,
		append([]any{accountId, key, filePath}, scopeArgs...)...)
	if err != nil {
		return nil, fmt.Errorf("GetSkillFile: query failed: %w", err)
	}
	if len(rows) == 0 {
		return nil, nil
	}
	if !rows[0].HasFile {
		return nil, ErrSkillFileNotFound
	}
	return &rows[0].SkillFile, nil
}

// SkillFolder is everything stored for a synced skill, for copying into a
// workspace: SKILL.md (the stored text, as is) and its other files.
type SkillFolder struct {
	SkillID    string
	SkillName  string
	SkillMD    string
	Files      map[string]string
	ContentSHA string
}

// GetSkillFolder returns the stored folder of an active, enabled skill synced
// from a repository that the account can see (same lookup and scope as
// GetSkillFile). nil, nil when no such skill is visible; a hand-made skill has
// no folder and is also nil.
func GetSkillFolder(sc *security.RequestContext, accountId, skillName string) (*SkillFolder, error) {
	if sc == nil || sc.GetSecurityContext() == nil {
		return nil, errors.New("GetSkillFolder: security context is required")
	}
	key := strings.ToLower(strings.TrimSpace(skillName))
	if accountId == "" || key == "" {
		return nil, nil
	}
	if !sc.GetSecurityContext().HasAccountAccess(accountId, security.SecurityAccessTypeRead) {
		return nil, errors.New("you don't have permission to access this resource")
	}
	dbms, err := common.GetDatabaseManager(common.Metastore)
	if err != nil {
		return nil, fmt.Errorf("GetSkillFolder: failed to get database manager: %w", err)
	}
	scope, scopeArgs := KBAccountScope("kb", 1, 3, VisibleOrgKnowledgebaseIDs(sc.GetContext(), accountId))
	var rows []struct {
		ID         string `db:"id"`
		Name       string `db:"name"`
		Data       string `db:"data"`
		Files      []byte `db:"files"`
		ContentSHA string `db:"content_sha"`
	}
	err = dbms.Db.Select(&rows, `
		SELECT kb.id, kb.name, COALESCE(kb.data, '') AS data, COALESCE(kb.files, '{}'::jsonb) AS files,
		       COALESCE(kb.skill_meta->>'content_sha', kb.skill_meta->>'body_sha', '') AS content_sha
		FROM llm_knowledgebases kb
		WHERE `+scope+`
		  AND (LOWER(BTRIM(kb.name)) = $2 OR REPLACE(LOWER(BTRIM(kb.name)), ' ', '-') = $2)
		  AND kb.source_id IS NOT NULL
		  AND kb.status = 'active'
		  AND kb.enabled
		ORDER BY kb.name
		LIMIT 1`,
		append([]any{accountId, key}, scopeArgs...)...)
	if err != nil {
		return nil, fmt.Errorf("GetSkillFolder: query failed: %w", err)
	}
	if len(rows) == 0 {
		return nil, nil
	}
	r := rows[0]
	files := map[string]string{}
	if err := json.Unmarshal(r.Files, &files); err != nil {
		return nil, fmt.Errorf("GetSkillFolder: files: %w", err)
	}
	return &SkillFolder{SkillID: r.ID, SkillName: r.Name, SkillMD: r.Data, Files: files, ContentSHA: r.ContentSHA}, nil
}
