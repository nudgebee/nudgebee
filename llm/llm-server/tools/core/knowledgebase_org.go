package core

import (
	"bytes"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/jmoiron/sqlx"
	"github.com/lib/pq"

	"nudgebee/llm/audit"
	"nudgebee/llm/common"
	"nudgebee/llm/config"
	"nudgebee/llm/security"
)

// Knowledge base visibility values (llm_knowledgebases.visibility).
const (
	KBVisibilityAccount          = "account"
	KBVisibilityOrganisation     = "organisation"
	KBVisibilitySelectedAccounts = "selected_accounts"
)

const errKBOrgAdminOnly = "only a tenant admin can manage organisation knowledge bases"

// kbOwner is who a knowledge base belongs to: one account, or — AccountId
// empty — the organisation identified by TenantId.
type kbOwner struct {
	AccountId string
	TenantId  string
}

func (o kbOwner) isOrg() bool { return o.AccountId == "" }

// label names the owner in rag-server paths and logs.
func (o kbOwner) label() string {
	if o.isOrg() {
		return o.TenantId
	}
	return o.AccountId
}

// predicate limits llm_knowledgebases rows to this owner with placeholder $n
// and returns the one argument it binds. For an account it is exactly the
// "account_id = $n" every write used before organisation KBs existed.
func (o kbOwner) predicate(n int) (string, any) {
	if o.isOrg() {
		return fmt.Sprintf("account_id IS NULL AND tenant_id = $%d", n), o.TenantId
	}
	return fmt.Sprintf("account_id = $%d", n), o.AccountId
}

// ValidKBVisibility reports whether v is a visibility a caller may request.
func ValidKBVisibility(v string) bool {
	return v == KBVisibilityAccount || v == KBVisibilityOrganisation || v == KBVisibilitySelectedAccounts
}

// canManageOrgKBs reports whether the caller may create or change the
// organisation knowledge bases of tenantId.
func canManageOrgKBs(sc *security.RequestContext, tenantId string, access security.SecurityAccessType) bool {
	s := sc.GetSecurityContext()
	if !s.HasTenantAccess(access) {
		return false
	}
	return s.IsSuperAdmin() || s.GetTenantId() == tenantId
}

// resolveKBForWrite reads who owns kbId and checks the caller may change it:
// an account KB needs `access` on that account (and must be requested under
// it); an organisation KB needs a tenant admin of its tenant, and accountId
// may be empty.
func resolveKBForWrite(sc *security.RequestContext, dbms *common.DatabaseManager, accountId, kbId string, access security.SecurityAccessType) (kbOwner, error) {
	if kbId == "" {
		return kbOwner{}, errors.New(errKBIDRequired)
	}
	var row struct {
		AccountId string `db:"account_id"`
		TenantId  string `db:"tenant_id"`
	}
	err := dbms.Db.GetContext(sc.GetContext(), &row,
		`SELECT COALESCE(account_id::text, '') AS account_id, tenant_id::text AS tenant_id FROM llm_knowledgebases WHERE id = $1`, kbId)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return kbOwner{}, errors.New(errKBNotFound)
		}
		return kbOwner{}, fmt.Errorf("kb: failed to read knowledgebase owner: %w", err)
	}
	if row.AccountId == "" {
		if !canManageOrgKBs(sc, row.TenantId, access) {
			return kbOwner{}, errors.New(errKBOrgAdminOnly)
		}
		return kbOwner{TenantId: row.TenantId}, nil
	}
	if accountId == "" {
		return kbOwner{}, errors.New(errKBAccountIDRequired)
	}
	if !sc.GetSecurityContext().HasAccountAccess(accountId, access) {
		return kbOwner{}, errors.New(errKBUnauthorized)
	}
	if row.AccountId != accountId {
		return kbOwner{}, errors.New(errKBNotFound)
	}
	return kbOwner{AccountId: accountId, TenantId: row.TenantId}, nil
}

// kbNameTaken reports whether name is already used by another KB of the same
// owner (excluding excludeId). Names are compared trimmed, case-insensitively,
// the way name lookups and the account-wins rule compare them.
func kbNameTaken(dbms *common.DatabaseManager, owner kbOwner, name, excludeId string) (bool, error) {
	pred, arg := owner.predicate(1)
	var taken bool
	err := dbms.Db.Get(&taken, `SELECT EXISTS(SELECT 1 FROM llm_knowledgebases WHERE `+pred+
		` AND LOWER(BTRIM(name)) = LOWER(BTRIM($2)) AND id::text <> $3)`, arg, name, excludeId)
	if err != nil {
		return false, fmt.Errorf("kb: failed to check name: %w", err)
	}
	return taken, nil
}

// tenantAccountIds returns which of ids are accounts of tenantId.
func tenantAccountIds(dbms *common.DatabaseManager, tenantId string, ids []string) ([]string, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	var found []string
	err := dbms.Db.Select(&found,
		`SELECT id::text FROM cloud_accounts WHERE tenant = $1 AND id::text = ANY($2::text[])`, tenantId, pq.Array(ids))
	if err != nil {
		return nil, fmt.Errorf("kb: failed to check accounts: %w", err)
	}
	return found, nil
}

// replaceSelectedAccounts makes accountIds the accounts a selected_accounts KB
// is switched on for: any other override row of the KB is removed.
func replaceSelectedAccounts(tx *sqlx.Tx, kbId, tenantId, userId string, accountIds []string) error {
	if _, err := tx.Exec(`DELETE FROM llm_kb_account_overrides WHERE kb_id = $1`, kbId); err != nil {
		return fmt.Errorf("kb: failed to clear account overrides: %w", err)
	}
	for _, id := range accountIds {
		if _, err := tx.Exec(`
			INSERT INTO llm_kb_account_overrides (kb_id, account_id, tenant_id, enabled, updated_by, updated_at)
			VALUES ($1, $2, $3, true, NULLIF($4, '')::uuid, NOW())`, kbId, id, tenantId, userId); err != nil {
			return fmt.Errorf("kb: failed to switch on account %s: %w", id, err)
		}
	}
	return nil
}

// orgKBAccounts validates the account list for a visibility: required and
// limited to the tenant for selected_accounts, ignored otherwise.
func orgKBAccounts(dbms *common.DatabaseManager, tenantId, visibility string, accountIds []string) ([]string, error) {
	if visibility != KBVisibilitySelectedAccounts {
		return nil, nil
	}
	ids := compactIDs(accountIds)
	if len(ids) == 0 {
		return nil, errors.New("selected_accounts needs at least one account")
	}
	found, err := tenantAccountIds(dbms, tenantId, ids)
	if err != nil {
		return nil, err
	}
	if len(found) != len(ids) {
		return nil, errors.New("every selected account must belong to the organisation")
	}
	return ids, nil
}

func compactIDs(ids []string) []string {
	var out []string
	for _, id := range ids {
		id = strings.TrimSpace(id)
		if id != "" && !slices.Contains(out, id) {
			out = append(out, id)
		}
	}
	return out
}

// SetKnowledgebaseVisibility moves a manual knowledge base between account and
// organisation scope, or changes which accounts an organisation KB reaches.
// Tenant admins only. The row changes first; then the search collection is
// re-scoped in place (rag-server's live-KB gate reads the row, so narrowing
// takes effect even if the re-scope lags).
//
//   - visibility "organisation" / "selected_accounts": the KB becomes (or stays)
//     an organisation KB; accountIds lists the accounts for selected_accounts.
//   - visibility "account": the KB becomes an account KB of targetAccountId.
func SetKnowledgebaseVisibility(sc *security.RequestContext, kbId, visibility string, accountIds []string, targetAccountId string) (Knowledgebase, error) {
	if !ValidKBVisibility(visibility) {
		return Knowledgebase{}, fmt.Errorf("invalid visibility %q", visibility)
	}
	dbms, err := common.GetDatabaseManager(common.Metastore)
	if err != nil {
		return Knowledgebase{}, fmt.Errorf("kb: failed to get database manager: %w", err)
	}
	var kb struct {
		Name       string `db:"name"`
		AccountId  string `db:"account_id"`
		TenantId   string `db:"tenant_id"`
		Visibility string `db:"visibility"`
		KBType     string `db:"kb_type"`
		Status     string `db:"status"`
	}
	err = dbms.Db.Get(&kb, `SELECT name, COALESCE(account_id::text, '') AS account_id, tenant_id::text AS tenant_id,
		visibility, COALESCE(kb_type, 'manual') AS kb_type, status FROM llm_knowledgebases WHERE id = $1`, kbId)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Knowledgebase{}, errors.New(errKBNotFound)
		}
		return Knowledgebase{}, fmt.Errorf("kb: failed to read knowledgebase: %w", err)
	}
	if !canManageOrgKBs(sc, kb.TenantId, security.SecurityAccessTypeUpdate) {
		return Knowledgebase{}, errors.New(errKBOrgAdminOnly)
	}
	if kb.KBType == KBTypeIntegration {
		return Knowledgebase{}, errors.New("integration knowledge bases cannot change scope")
	}
	if kb.Status == string(KBStatusProcessing) {
		// Indexing captured the current owner; re-scoping now could miss the
		// collection it is about to create.
		return Knowledgebase{}, errors.New("the knowledge base is still being indexed; change its scope once indexing finishes")
	}

	target := kbOwner{TenantId: kb.TenantId}
	if visibility == KBVisibilityAccount {
		targetAccountId = strings.TrimSpace(targetAccountId)
		if targetAccountId == "" {
			return Knowledgebase{}, errors.New("target_account_id is required to move a knowledge base to an account")
		}
		found, err := tenantAccountIds(dbms, kb.TenantId, []string{targetAccountId})
		if err != nil {
			return Knowledgebase{}, err
		}
		if len(found) != 1 {
			return Knowledgebase{}, errors.New("the target account must belong to the organisation")
		}
		target.AccountId = targetAccountId
	}
	selected, err := orgKBAccounts(dbms, kb.TenantId, visibility, accountIds)
	if err != nil {
		return Knowledgebase{}, err
	}
	if kb.AccountId != target.AccountId {
		taken, err := kbNameTaken(dbms, target, kb.Name, kbId)
		if err != nil {
			return Knowledgebase{}, err
		}
		if taken {
			return Knowledgebase{}, errors.New(errKBAlreadyExists)
		}
	}

	userId := sc.GetSecurityContext().GetUserId()
	_, err = dbms.DoInTransaction(func(tx *sqlx.Tx) (any, error) {
		if _, err := tx.Exec(`UPDATE llm_knowledgebases
			SET account_id = NULLIF($1, '')::uuid, visibility = $2, updated_by = NULLIF($3, '')::uuid, updated_at = NOW()
			WHERE id = $4`, target.AccountId, visibility, userId, kbId); err != nil {
			return nil, fmt.Errorf("kb: failed to change visibility: %w", err)
		}
		if visibility == KBVisibilityAccount {
			// An account KB has no per-account switches.
			if _, err := tx.Exec(`DELETE FROM llm_kb_account_overrides WHERE kb_id = $1`, kbId); err != nil {
				return nil, fmt.Errorf("kb: failed to clear account overrides: %w", err)
			}
			return nil, nil
		}
		if visibility == KBVisibilitySelectedAccounts {
			return nil, replaceSelectedAccounts(tx, kbId, kb.TenantId, userId, selected)
		}
		// organisation: keep switch-offs, drop the switch-ons a previous
		// selected_accounts list left behind (they mean nothing here).
		if _, err := tx.Exec(`DELETE FROM llm_kb_account_overrides WHERE kb_id = $1 AND enabled`, kbId); err != nil {
			return nil, fmt.Errorf("kb: failed to clear account overrides: %w", err)
		}
		return nil, nil
	})
	if err != nil {
		return Knowledgebase{}, err
	}

	var rescopeErr error
	if kb.AccountId != target.AccountId {
		if err := setKBCollectionScope(kbId, target); err != nil {
			// The row is saved and narrowing already holds, but search keeps the
			// old scope until this succeeds. Repeating the same request retries it.
			slog.Error("kb: failed to re-scope search collection", "error", err, "kb_id", kbId)
			rescopeErr = fmt.Errorf("the scope was saved but search was not updated; repeat the change to retry: %w", err)
		}
	}

	if kb.AccountId != "" {
		invalidateSkillContentCache(kb.AccountId, kb.Name)
	}
	invalidateOrgKnowledgeCaches(kb.TenantId)

	if auditErr := audit.CreateAudit(sc, &audit.AuditRequest{Audits: []audit.Audit{{
		AccountId:     target.AccountId,
		EventTime:     time.Now().UTC(),
		EventCategory: "knowledgebase",
		EventType:     "KB_SET_VISIBILITY",
		EventActor:    audit.EventActor(userId),
		EventTarget:   kbId,
		EventAction:   audit.EventActionUpdate,
		EventStatus:   audit.EventStatusSuccess,
		TransactionId: sc.GetTraceId(),
		EventState:    map[string]any{"visibility": visibility, "account_id": target.AccountId, "account_ids": selected},
		EventAttr: map[string]any{
			"kb_id": kbId, "kb_name": kb.Name, "visibility": visibility, "previous_visibility": kb.Visibility,
			"account_id": target.AccountId, "previous_account_id": kb.AccountId, "account_ids": selected,
		},
		TenantId: kb.TenantId,
		UserId:   userId,
	}}}); auditErr != nil {
		slog.Error("kb: failed to create audit entry", "error", auditErr, "kb_id", kbId)
	}

	updated, err := getKBForOwner(dbms, kbId)
	if rescopeErr != nil {
		return updated, rescopeErr
	}
	return updated, err
}

// KBAccountOverride is one account's switch for an organisation knowledge base.
type KBAccountOverride struct {
	AccountId   string    `json:"account_id" db:"account_id"`
	AccountName string    `json:"account_name" db:"account_name"`
	Enabled     bool      `json:"enabled" db:"enabled"`
	UpdatedAt   time.Time `json:"updated_at" db:"updated_at"`
}

// SetKBAccountOverride switches an organisation knowledge base on or off for
// one account. An admin of that account may do it; for a selected_accounts KB
// only a tenant admin may (switching on is how an account gets onto the list).
func SetKBAccountOverride(sc *security.RequestContext, accountId, kbId string, enabled bool) error {
	if accountId == "" {
		return errors.New(errKBAccountIDRequired)
	}
	if kbId == "" {
		return errors.New(errKBIDRequired)
	}
	if !sc.GetSecurityContext().HasAccountAccess(accountId, security.SecurityAccessTypeUpdate) {
		return errors.New(errKBUnauthorized)
	}
	dbms, err := common.GetDatabaseManager(common.Metastore)
	if err != nil {
		return fmt.Errorf("kb: failed to get database manager: %w", err)
	}
	var kb struct {
		Name       string `db:"name"`
		TenantId   string `db:"tenant_id"`
		Visibility string `db:"visibility"`
	}
	err = dbms.Db.Get(&kb, `SELECT kb.name, kb.tenant_id::text AS tenant_id, kb.visibility
		FROM llm_knowledgebases kb JOIN cloud_accounts ca ON ca.id = $2 AND ca.tenant = kb.tenant_id
		WHERE kb.id = $1 AND kb.account_id IS NULL`, kbId, accountId)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return errors.New(errKBNotFound)
		}
		return fmt.Errorf("kb: failed to read knowledgebase: %w", err)
	}
	if kb.Visibility == KBVisibilitySelectedAccounts && !canManageOrgKBs(sc, kb.TenantId, security.SecurityAccessTypeUpdate) {
		return errors.New(errKBOrgAdminOnly)
	}
	userId := sc.GetSecurityContext().GetUserId()
	if _, err := dbms.Db.Exec(`
		INSERT INTO llm_kb_account_overrides (kb_id, account_id, tenant_id, enabled, updated_by, updated_at)
		VALUES ($1, $2, $3, $4, NULLIF($5, '')::uuid, NOW())
		ON CONFLICT (kb_id, account_id) DO UPDATE SET enabled = EXCLUDED.enabled, updated_by = EXCLUDED.updated_by, updated_at = NOW()`,
		kbId, accountId, kb.TenantId, enabled, userId); err != nil {
		return fmt.Errorf("kb: failed to save account switch: %w", err)
	}
	invalidateSkillContentCache(accountId, kb.Name)
	invalidateOrgKnowledgeCaches(kb.TenantId)
	if auditErr := audit.CreateAudit(sc, &audit.AuditRequest{Audits: []audit.Audit{{
		AccountId:     accountId,
		EventTime:     time.Now().UTC(),
		EventCategory: "knowledgebase",
		EventType:     "KB_SET_ACCOUNT_OVERRIDE",
		EventActor:    audit.EventActor(userId),
		EventTarget:   kbId,
		EventAction:   audit.EventActionUpdate,
		EventStatus:   audit.EventStatusSuccess,
		TransactionId: sc.GetTraceId(),
		EventState:    map[string]any{"enabled": enabled},
		EventAttr:     map[string]any{"kb_id": kbId, "kb_name": kb.Name, "enabled": enabled},
		TenantId:      kb.TenantId,
		UserId:        userId,
	}}}); auditErr != nil {
		slog.Error("kb: failed to create audit entry", "error", auditErr, "kb_id", kbId)
	}
	return nil
}

// ListKBAccountOverrides lists the per-account switches of an organisation
// knowledge base. Tenant admins only.
func ListKBAccountOverrides(sc *security.RequestContext, kbId string) ([]KBAccountOverride, error) {
	if kbId == "" {
		return nil, errors.New(errKBIDRequired)
	}
	dbms, err := common.GetDatabaseManager(common.Metastore)
	if err != nil {
		return nil, fmt.Errorf("kb: failed to get database manager: %w", err)
	}
	var tenantId string
	err = dbms.Db.Get(&tenantId, `SELECT tenant_id::text FROM llm_knowledgebases WHERE id = $1 AND account_id IS NULL`, kbId)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, errors.New(errKBNotFound)
		}
		return nil, fmt.Errorf("kb: failed to read knowledgebase: %w", err)
	}
	if !canManageOrgKBs(sc, tenantId, security.SecurityAccessTypeRead) {
		return nil, errors.New(errKBOrgAdminOnly)
	}
	out := []KBAccountOverride{}
	err = dbms.Db.Select(&out, `
		SELECT o.account_id::text AS account_id, COALESCE(ca.account_name, '') AS account_name, o.enabled, o.updated_at
		FROM llm_kb_account_overrides o LEFT JOIN cloud_accounts ca ON ca.id = o.account_id
		WHERE o.kb_id = $1 ORDER BY ca.account_name`, kbId)
	if err != nil {
		return nil, fmt.Errorf("kb: failed to list account overrides: %w", err)
	}
	return out, nil
}

// getKBForOwner returns the KB row without its data, for API responses.
func getKBForOwner(dbms *common.DatabaseManager, kbId string) (Knowledgebase, error) {
	var kb Knowledgebase
	err := dbms.Db.Get(&kb, `
		SELECT kb.id, kb.tenant_id, COALESCE(kb.account_id::text, '') AS account_id, kb.visibility, kb.name,
		       COALESCE(kb.description, '') as description, kb.data_format, kb.data_filename,
		       COALESCE(kb.data_size_bytes, 0) as data_size_bytes, kb.status, kb.enabled, kb.kb_type, kb.kb_source,
		       kb.integration_id, COALESCE(kb.note_category, '') as note_category, kb.context_tags,
		       kb.created_at, kb.updated_at, kb.document_count, kb.last_loaded_at, kb.error_message
		FROM llm_knowledgebases kb WHERE kb.id = $1`, kbId)
	if err != nil {
		return Knowledgebase{}, fmt.Errorf("kb: failed to read knowledgebase: %w", err)
	}
	return kb, nil
}

// setKBCollectionScope moves a manual KB's search collection to its new owner
// in place (rag-server /kb/scope). A KB with no collection yet is a no-op there.
func setKBCollectionScope(kbId string, owner kbOwner) error {
	ragServerURL := config.Config.RAGServerUrl
	if ragServerURL == "" {
		return errors.New("rag-server is not configured")
	}
	payload := map[string]any{"kb_id": kbId}
	if owner.isOrg() {
		payload["tenant_id"] = owner.TenantId
	} else {
		payload["account_id"] = owner.AccountId
	}
	body, err := common.MarshalJson(payload)
	if err != nil {
		return fmt.Errorf("kb: failed to encode scope request: %w", err)
	}
	req, err := http.NewRequest(http.MethodPost, ragServerURL+"/api/v1/kb/scope", bytes.NewBuffer(body))
	if err != nil {
		return fmt.Errorf("kb: failed to build scope request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	addRAGAuth(req)
	resp, err := ragClient.Do(req)
	if err != nil {
		return fmt.Errorf("kb: scope request failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("kb: scope request failed (status %d): %s", resp.StatusCode, string(msg))
	}
	return nil
}

// OrgKnowledgeCacheInvalidator tells every llm-server replica that the
// organisation knowledge bases of a tenant changed. Set by the api package
// (it owns the cache-invalidation fanout); the local caches are always
// cleared directly as well.
var OrgKnowledgeCacheInvalidator func(tenantId string)

// ClearKnowledgeBaseCaches drops every cached skill body and per-agent skill
// menu on this replica. Organisation KB changes are rare admin actions, and
// those caches are keyed per account, so the whole namespace goes.
func ClearKnowledgeBaseCaches() {
	if err := common.CacheClear(CacheNamespaceLlmSkillContent); err != nil {
		slog.Error("kb: failed to clear skill content cache", "error", err)
	}
	if err := common.CacheClear(CacheNamespaceLlmKbMapping); err != nil {
		slog.Error("kb: failed to clear KB mapping cache", "error", err)
	}
}

// invalidateIfOrgKBNameClash clears the organisation caches when an account
// KB is created, renamed or deleted under the name of one of its tenant's
// organisation KBs: the account KB shadows that organisation KB, so the
// account's cached mappings and skill bodies for it are stale.
func invalidateIfOrgKBNameClash(dbms *common.DatabaseManager, accountId string, names ...string) {
	var lowered []string
	for _, n := range names {
		if n = strings.ToLower(strings.TrimSpace(n)); n != "" {
			lowered = append(lowered, n)
		}
	}
	if dbms == nil || accountId == "" || len(lowered) == 0 {
		return
	}
	var tenantIds []string
	if err := dbms.Db.Select(&tenantIds, `
		SELECT DISTINCT kb.tenant_id::text FROM llm_knowledgebases kb
		JOIN cloud_accounts ca ON ca.id = $1 AND ca.tenant = kb.tenant_id
		WHERE kb.account_id IS NULL AND LOWER(BTRIM(kb.name)) = ANY($2)`, accountId, pq.Array(lowered)); err != nil {
		slog.Warn("kb: organisation name-clash check failed", "error", err, "account_id", accountId)
		return
	}
	for _, tenantId := range tenantIds {
		invalidateOrgKnowledgeCaches(tenantId)
	}
}

func invalidateOrgKnowledgeCaches(tenantId string) {
	ClearKnowledgeBaseCaches()
	if OrgKnowledgeCacheInvalidator != nil {
		OrgKnowledgeCacheInvalidator(tenantId)
	}
}
