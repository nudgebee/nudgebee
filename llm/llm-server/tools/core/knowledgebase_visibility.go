package core

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"github.com/lib/pq"

	"nudgebee/llm/common"
)

// Organisation knowledge bases (account_id NULL) belong to a tenant. Which of
// them an account may see follows the same rule as rag-server's live-KB gate
// (llm/rag-server/rag/core/utils/db_query.py), pinned by the shared fixture
// llm/rag-server/tests/fixtures/kb_visibility_cases.json:
//
//   - visibility "organisation": every account in the tenant, unless the
//     account switched it off (override enabled = false);
//   - visibility "selected_accounts": only accounts switched on;
//   - never when the account has its own manual KB with the same non-empty name
//     (lowercased), even while that KB is switched off — unless the
//     organisation KB is an integration's (#40605);
//   - never while the organisation KB itself is switched off.
//
// Account-owned KBs are untouched: reads keep their kb.account_id = $1 clause
// and add the organisation ids only when there are some.

// kbVisibilityRow is one llm_knowledgebases row as the visibility rule sees it.
// AccountId is nil for an organisation KB; OverrideEnabled is the querying
// account's switch for it (nil = none).
type kbVisibilityRow struct {
	Id              string  `db:"id" json:"id"`
	AccountId       *string `db:"account_id" json:"account_id"`
	IntegrationId   *string `db:"integration_id" json:"integration_id"`
	Enabled         bool    `db:"enabled" json:"enabled"`
	Name            *string `db:"name" json:"name"`
	Visibility      string  `db:"visibility" json:"visibility"`
	OverrideEnabled *bool   `db:"override_enabled" json:"override_enabled"`
}

func (r kbVisibilityRow) collectionName() string {
	if r.IntegrationId != nil && *r.IntegrationId != "" {
		return *r.IntegrationId + "_knowledge_base"
	}
	return "kb_" + r.Id
}

func lowerName(name *string) string {
	if name == nil {
		return ""
	}
	return strings.ToLower(*name)
}

// visibleKBCollections applies the visibility rule to rows already in scope
// (not archived, integration enabled, owned by the account or in its tenant)
// and returns the collection names the account may search. It mirrors
// _live_kb_names_from_rows in rag-server; accountId "" is a tenant-only query.
func visibleKBCollections(rows []kbVisibilityRow, accountId string) map[string]bool {
	isOwn := func(r kbVisibilityRow) bool { return r.AccountId != nil && *r.AccountId == accountId }
	ownNames := map[string]bool{}
	if accountId != "" {
		for _, r := range rows {
			if isOwn(r) && (r.IntegrationId == nil || *r.IntegrationId == "") && lowerName(r.Name) != "" {
				ownNames[lowerName(r.Name)] = true
			}
		}
	}
	names := map[string]bool{}
	for _, r := range rows {
		manual := r.IntegrationId == nil || *r.IntegrationId == ""
		switch {
		case r.AccountId != nil && !manual:
			// A per-account integration row is the pre-#40605 shape: an
			// integration's knowledge base is its organisation row.
		case accountId != "" && r.AccountId != nil && !isOwn(r):
			// Another account's manual KB is never visible.
		case r.AccountId == nil:
			if orgKBVisible(r, ownNames) {
				names[r.collectionName()] = true
			}
		case r.Enabled:
			names[r.collectionName()] = true
		}
	}
	return names
}

func orgKBVisible(r kbVisibilityRow, ownNames map[string]bool) bool {
	// An account's own manual KB shadows an organisation KB of the same name,
	// but not an integration's: its name comes from the integration, not from
	// someone choosing to replace it.
	manual := r.IntegrationId == nil || *r.IntegrationId == ""
	if !r.Enabled || (manual && lowerName(r.Name) != "" && ownNames[lowerName(r.Name)]) {
		return false
	}
	switch r.Visibility {
	case "organisation":
		return r.OverrideEnabled == nil || *r.OverrideEnabled
	case "selected_accounts":
		return r.OverrideEnabled != nil && *r.OverrideEnabled
	}
	return false
}

// visibilityDatabase is separate from knowledgeDatabase so tests that mock the
// knowledge queries do not also receive this one.
var visibilityDatabase = common.GetDatabaseManager

// VisibleOrgKnowledgebaseIDs returns the ids of the organisation knowledge
// bases accountId may read. It is a variable so tests that mock the database
// for unrelated queries can stub it out.
var VisibleOrgKnowledgebaseIDs = loadVisibleOrgKnowledgebaseIDs

// loadVisibleOrgKnowledgebaseIDs reads the tenant's organisation KBs, this
// account's switches and its own manual KB names in one query. Any failure
// returns no ids: organisation KBs are hidden rather than shown to the wrong
// account, and the account's own KBs are unaffected.
func loadVisibleOrgKnowledgebaseIDs(ctx context.Context, accountId string) []string {
	rows := loadOrgKBVisibilityRows(ctx, accountId)
	visible := visibleKBCollections(rows, accountId)
	var ids []string
	for _, r := range rows {
		if r.AccountId == nil && visible[r.collectionName()] {
			ids = append(ids, r.Id)
		}
	}
	return ids
}

// SwitchedOffOrgKnowledgebaseIDs returns the organisation knowledge bases
// accountId would see had it not switched them off. Only the KB list shows
// them (read-only, so the account can switch them back on); every other read
// keeps using VisibleOrgKnowledgebaseIDs.
var SwitchedOffOrgKnowledgebaseIDs = loadSwitchedOffOrgKnowledgebaseIDs

func loadSwitchedOffOrgKnowledgebaseIDs(ctx context.Context, accountId string) []string {
	return switchedOffOrgKBIDs(loadOrgKBVisibilityRows(ctx, accountId), accountId)
}

// switchedOffOrgKBIDs keeps the organisation-wide KBs that the visibility
// rule would show accountId if its switch-off row were removed.
func switchedOffOrgKBIDs(rows []kbVisibilityRow, accountId string) []string {
	var ids []string
	for _, r := range rows {
		if r.AccountId != nil || r.Visibility != "organisation" || r.OverrideEnabled == nil || *r.OverrideEnabled {
			continue
		}
		withoutSwitch := r
		withoutSwitch.OverrideEnabled = nil
		probe := make([]kbVisibilityRow, 0, len(rows))
		for _, other := range rows {
			if other.Id != r.Id {
				probe = append(probe, other)
			}
		}
		if visibleKBCollections(append(probe, withoutSwitch), accountId)[r.collectionName()] {
			ids = append(ids, r.Id)
		}
	}
	return ids
}

func loadOrgKBVisibilityRows(ctx context.Context, accountId string) []kbVisibilityRow {
	if accountId == "" {
		return nil
	}
	dbms, err := visibilityDatabase(common.Metastore)
	if err != nil {
		slog.Warn("kb: organisation visibility unavailable", "error", err, "account_id", accountId)
		return nil
	}
	var rows []kbVisibilityRow
	err = dbms.Db.SelectContext(ctx, &rows, `
		SELECT kb.id, kb.account_id::text AS account_id, kb.integration_id::text AS integration_id,
		       kb.enabled, kb.name, kb.visibility, o.enabled AS override_enabled
		FROM llm_knowledgebases kb
		JOIN cloud_accounts ca ON ca.id = $1 AND ca.tenant = kb.tenant_id
		LEFT JOIN integrations i ON i.id = kb.integration_id
		LEFT JOIN llm_kb_account_overrides o ON o.kb_id = kb.id AND o.account_id = $1
		WHERE kb.status != 'archived'
		  AND (kb.integration_id IS NULL OR i.status = 'enabled')
		  AND (kb.account_id IS NULL OR (kb.account_id = $1 AND kb.integration_id IS NULL))`, accountId)
	// An integration's knowledge base is its organisation row (#40605); a
	// per-account integration row is the old shape and is not loaded.
	if err != nil {
		slog.Warn("kb: organisation visibility query failed", "error", err, "account_id", accountId)
		return nil
	}
	return rows
}

// KBAccountScope returns the WHERE predicate that limits table alias `alias`
// to the KBs accountId may read, plus the extra argument it needs. accountParam
// is the placeholder number already bound to accountId; nextParam is the next
// free one. With no visible organisation KBs the predicate is exactly
// "<alias>.account_id = $accountParam" and no argument is added, so existing
// queries are unchanged.
func KBAccountScope(alias string, accountParam, nextParam int, orgIDs []string) (string, []any) {
	col := "account_id"
	idCol := "id"
	if alias != "" {
		col = alias + ".account_id"
		idCol = alias + ".id"
	}
	if len(orgIDs) == 0 {
		return fmt.Sprintf("%s = $%d", col, accountParam), nil
	}
	return fmt.Sprintf("(%s = $%d OR %s = ANY($%d::uuid[]))", col, accountParam, idCol, nextParam), []any{pq.Array(orgIDs)}
}

// KBMappingsJoin is the agent-mapping source for mapping-driven reads. A manual
// organisation KB visible to the account counts as mapped to all agents
// there: it gets a virtual wildcard row (agent_id "*") for the account, so it
// resolves exactly like a KB the account mapped to "All agents", and the
// per-account switch is how an account opts out. An integration's organisation
// KB (#40605) does not: like the per-account integration KBs it replaced, it
// reaches an agent only through mappings the account made, which are keyed by
// account. orgParam is the placeholder already bound to orgIDs by
// KBAccountScope. With no organisation KBs the join is the plain mapping table,
// so account-only queries are unchanged.
func KBMappingsJoin(accountParam, orgParam int, orgIDs []string) string {
	if len(orgIDs) == 0 {
		return "INNER JOIN llm_kb_agent_mappings m ON kb.id = m.kb_id"
	}
	return fmt.Sprintf(`INNER JOIN (
			SELECT kb_id, agent_id, account_id FROM llm_kb_agent_mappings WHERE account_id = $%d
			UNION
			SELECT id, '%s', $%d::uuid FROM llm_knowledgebases WHERE id = ANY($%d::uuid[]) AND integration_id IS NULL
		) m ON kb.id = m.kb_id`, accountParam, KBAgentWildcard, accountParam, orgParam)
}
