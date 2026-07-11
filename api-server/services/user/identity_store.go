package user

import (
	"database/sql"
	"fmt"
	"strings"
	"time"

	"nudgebee/services/common"
	core "nudgebee/services/integrations/core"
	"nudgebee/services/internal/database"
	"nudgebee/services/security"

	"github.com/jmoiron/sqlx"
	"github.com/lib/pq"
)

// identityKeyExpr is the logical identity of an external account, independent of
// which integration instance produced it: the email if present, else the lowered
// external user id. A person who appears across N integrations of the same type
// (e.g. 9 PagerDuty connections) within one cloud account shares one identity key,
// so the read queries collapse them to one row per account and map/unmap act on
// all of that account's instances at once. Qualified to the `iua` alias, which
// every query in this file uses for integration_user_accounts.
const identityKeyExpr = `COALESCE(NULLIF(lower(iua.external_email), ''), lower(iua.external_user_id))`

// syncedAccount is one (account scope, external user) tuple to upsert.
type syncedAccount struct {
	accountId string
	acc       core.ExternalUser
}

// syncUpsertChunkSize bounds rows per statement: 500 rows × 8 params = 4000, well
// under lib/pq's 65535-parameter cap.
const syncUpsertChunkSize = 500

// upsertSyncedAccounts batch-inserts/updates external accounts in the pool with a
// single multi-row statement per chunk, instead of one round-trip per row. Against a
// remote/high-latency database this is the difference between seconds and minutes
// (a directory of N users was previously N sequential ~500ms round-trips). Like the
// per-row form it never touches mapping columns (mapped_user_id / mapped_via /
// mapped_by) — those are owned by autoMatchByEmail and the manual-mapping RPCs, so a
// re-sync can't clobber an existing link. Returns the number of rows written across
// successfully-committed chunks.
func upsertSyncedAccounts(db *sqlx.DB, tenantId, integrationType, integrationId string, items []syncedAccount) (int, error) {
	if tenantId == "" {
		return 0, fmt.Errorf("upsertSyncedAccounts: tenantId is required")
	}
	written := 0
	for start := 0; start < len(items); start += syncUpsertChunkSize {
		end := start + syncUpsertChunkSize
		if end > len(items) {
			end = len(items)
		}
		chunk := items[start:end]

		var b strings.Builder
		b.WriteString(`INSERT INTO integration_user_accounts
			(tenant_id, account_id, integration_type, integration_id, external_user_id,
			 external_username, external_email, external_display_name, last_synced_at)
		VALUES `)
		args := make([]any, 0, len(chunk)*8)
		for i, it := range chunk {
			if i > 0 {
				b.WriteString(",")
			}
			p := i * 8
			fmt.Fprintf(&b, "($%d, NULLIF($%d,'')::uuid, $%d, NULLIF($%d,'')::uuid, $%d, $%d, NULLIF($%d,''), $%d, now())",
				p+1, p+2, p+3, p+4, p+5, p+6, p+7, p+8)
			args = append(args, tenantId, it.accountId, integrationType, integrationId, it.acc.ID,
				it.acc.Username, it.acc.Email, it.acc.DisplayName)
		}
		b.WriteString(`
		ON CONFLICT (tenant_id, COALESCE(account_id, '00000000-0000-0000-0000-000000000000'::uuid), integration_type, integration_id, external_user_id)
		DO UPDATE SET
			external_username     = EXCLUDED.external_username,
			external_email        = EXCLUDED.external_email,
			external_display_name = EXCLUDED.external_display_name,
			is_active             = true,
			last_synced_at        = now(),
			updated_at            = now()`)

		if _, err := db.Exec(b.String(), args...); err != nil {
			return written, err
		}
		written += len(chunk)
	}
	return written, nil
}

// integrationRecentlySynced reports whether this integration instance was synced
// within `within` (max last_synced_at across its rows). Lets a sync skip an
// integration already refreshed by a recent run, so a manual re-trigger or an
// overlapping cron tick doesn't redo the (potentially large) fetch + upsert.
func integrationRecentlySynced(db *sqlx.DB, tenantId, integrationId string, within time.Duration) (bool, error) {
	if tenantId == "" {
		return false, fmt.Errorf("integrationRecentlySynced: tenantId is required")
	}
	var last sql.NullTime
	err := db.QueryRowx(
		`SELECT max(last_synced_at) FROM integration_user_accounts WHERE tenant_id = $1::uuid AND integration_id = $2::uuid`,
		tenantId, integrationId,
	).Scan(&last)
	if err != nil {
		return false, err
	}
	return last.Valid && time.Since(last.Time) < within, nil
}

// reconcileIntegration prunes rows for one integration whose external user is no
// longer returned by the source (e.g. removed from the GitHub org, deactivated in
// Slack). `seen` is the set of external_user_ids fetched this run — callers MUST
// only invoke this after a successful, non-empty fetch, so a transient failure or
// a quirky empty response can't wipe valid data. Manual mappings are tombstoned
// (is_active=false, preserved so they reactivate on re-add); everything else is
// hard-deleted.
func reconcileIntegration(db *sqlx.DB, tenantId, integrationId string, seen []string) error {
	if len(seen) == 0 {
		return nil
	}
	if _, err := db.Exec(`
		UPDATE integration_user_accounts
		SET is_active = false, updated_at = now()
		WHERE tenant_id = $1::uuid AND integration_id = $2::uuid
		  AND mapped_via = $3 AND is_active = true
		  AND NOT (external_user_id = ANY($4))`,
		tenantId, integrationId, MappedViaManual, pq.Array(seen)); err != nil {
		return err
	}
	_, err := db.Exec(`
		DELETE FROM integration_user_accounts
		WHERE tenant_id = $1::uuid AND integration_id = $2::uuid
		  AND mapped_via IS DISTINCT FROM $3
		  AND NOT (external_user_id = ANY($4))`,
		tenantId, integrationId, MappedViaManual, pq.Array(seen))
	return err
}

// sweepDisabledIntegrations prunes rows whose integration is no longer enabled
// (disabled or deleted) for the synced types. enabledIntegrationIds is the set of
// integration ids enumerated this run; callers MUST only invoke this when that
// enumeration succeeded for all types, so a transient DB read error can't wipe
// valid data. Manual mappings are tombstoned; everything else is hard-deleted.
func sweepDisabledIntegrations(db *sqlx.DB, tenantId string, types, enabledIntegrationIds []string) error {
	if _, err := db.Exec(`
		UPDATE integration_user_accounts
		SET is_active = false, updated_at = now()
		WHERE tenant_id = $1::uuid AND integration_type = ANY($2)
		  AND mapped_via = $3 AND is_active = true
		  AND NOT (integration_id = ANY($4))`,
		tenantId, pq.Array(types), MappedViaManual, pq.Array(enabledIntegrationIds)); err != nil {
		return err
	}
	_, err := db.Exec(`
		DELETE FROM integration_user_accounts
		WHERE tenant_id = $1::uuid AND integration_type = ANY($2)
		  AND mapped_via IS DISTINCT FROM $3
		  AND NOT (integration_id = ANY($4))`,
		tenantId, pq.Array(types), MappedViaManual, pq.Array(enabledIntegrationIds))
	return err
}

// autoMatchByEmail links still-unmapped (or previously auto-mapped) accounts to
// the internal user whose username (= email) matches the account email,
// case-insensitively. Manual mappings (mapped_via = 'manual') are never touched.
// Returns the number of rows newly/again auto-mapped.
func autoMatchByEmail(db *sqlx.DB, tenantId string) (int64, error) {
	// First clear stale auto-mappings whose justification no longer holds — the
	// external email changed to a non-matching value, or the mapped user is no
	// longer in this tenant. Manual mappings are untouched. Without this, an old
	// auto-mapping would persist and could point at the wrong / removed user.
	if _, err := db.Exec(`
		UPDATE integration_user_accounts iua
		SET mapped_user_id = NULL,
		    mapped_via      = NULL,
		    updated_at      = now()
		WHERE iua.tenant_id = $1::uuid
		  AND iua.mapped_via = $2
		  AND NOT EXISTS (
		      SELECT 1 FROM users u
		      JOIN tenant_users tu ON tu."user" = u.id AND tu.tenant = $1::uuid
		      WHERE u.id = iua.mapped_user_id
		        AND iua.external_email IS NOT NULL
		        AND lower(iua.external_email) = lower(u.username)
		  )`,
		tenantId, MappedViaAutoEmail); err != nil {
		return 0, err
	}

	res, err := db.Exec(`
		UPDATE integration_user_accounts iua
		SET mapped_user_id = u.id,
		    mapped_via      = $2,
		    mapped_by       = NULL,
		    updated_at      = now()
		FROM users u
		JOIN tenant_users tu ON tu."user" = u.id AND tu.tenant = $1::uuid
		WHERE iua.tenant_id = $1::uuid
		  AND iua.is_active = true
		  AND iua.external_email IS NOT NULL
		  AND lower(iua.external_email) = lower(u.username)
		  AND (iua.mapped_via IS NULL OR iua.mapped_via = $2)
		  AND iua.mapped_user_id IS DISTINCT FROM u.id`,
		tenantId, MappedViaAutoEmail)
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	return n, nil
}

// ListIntegrationAccountsForUser returns every external account linked to the
// given internal user, scoped to the caller's tenant.
func ListIntegrationAccountsForUser(ctx *security.RequestContext, userId string) ([]IntegrationAccountDto, error) {
	if userId == "" {
		return nil, common.ErrorBadRequest("user_id is required")
	}
	tenantId := ctx.GetSecurityContext().GetTenantId()
	if tenantId == "" && !ctx.GetSecurityContext().IsSuperAdmin() {
		return nil, common.ErrorUnauthorized("Unauthorized")
	}

	manager, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		return nil, err
	}

	// DISTINCT ON collapses the same person across multiple integration instances
	// of one type within an account into a single representative row (newest sync
	// wins). Rows are grouped per account, so an identity scoped to N accounts
	// shows once per account.
	var rows []IntegrationUserAccount
	err = manager.Db.Select(&rows, `
		SELECT DISTINCT ON (iua.account_id, iua.integration_type, `+identityKeyExpr+`)
		       iua.id, iua.tenant_id, iua.account_id, ca.account_name AS account_name,
		       iua.integration_type, iua.integration_id, iua.external_user_id,
		       iua.external_username, iua.external_email, iua.external_display_name,
		       iua.mapped_user_id, iua.mapped_via, iua.mapped_by, iua.last_synced_at, iua.created_at, iua.updated_at
		FROM integration_user_accounts iua
		LEFT JOIN cloud_accounts ca ON ca.id = iua.account_id
		WHERE iua.tenant_id = $1::uuid AND iua.mapped_user_id = $2::uuid AND iua.is_active = true
		ORDER BY iua.account_id, iua.integration_type, `+identityKeyExpr+`, iua.last_synced_at DESC NULLS LAST`, tenantId, userId)
	if err != nil {
		return nil, err
	}

	out := make([]IntegrationAccountDto, 0, len(rows))
	for _, r := range rows {
		out = append(out, toIntegrationAccountDto(r))
	}
	return out, nil
}

// ListUnmappedAccounts returns external accounts of a given integration type that
// are not yet linked to any internal user — the candidate pool for manual mapping.
func ListUnmappedAccounts(ctx *security.RequestContext, integrationType string) ([]IntegrationAccountDto, error) {
	tenantId := ctx.GetSecurityContext().GetTenantId()
	if tenantId == "" && !ctx.GetSecurityContext().IsSuperAdmin() {
		return nil, common.ErrorUnauthorized("Unauthorized")
	}

	manager, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		return nil, err
	}

	// DISTINCT ON dedupes the same unmatched person across integration instances
	// per account, so the manual-map picker shows each (account, identity) once.
	query := `
		SELECT DISTINCT ON (iua.account_id, iua.integration_type, ` + identityKeyExpr + `)
		       iua.id, iua.tenant_id, iua.account_id, ca.account_name AS account_name,
		       iua.integration_type, iua.integration_id, iua.external_user_id,
		       iua.external_username, iua.external_email, iua.external_display_name,
		       iua.mapped_user_id, iua.mapped_via, iua.mapped_by, iua.last_synced_at, iua.created_at, iua.updated_at
		FROM integration_user_accounts iua
		LEFT JOIN cloud_accounts ca ON ca.id = iua.account_id
		WHERE iua.tenant_id = $1::uuid AND iua.mapped_user_id IS NULL AND iua.is_active = true`
	args := []any{tenantId}
	if integrationType != "" {
		query += ` AND iua.integration_type = $2`
		args = append(args, integrationType)
	}
	query += ` ORDER BY iua.account_id, iua.integration_type, ` + identityKeyExpr + `, iua.last_synced_at DESC NULLS LAST`

	var rows []IntegrationUserAccount
	if err := manager.Db.Select(&rows, query, args...); err != nil {
		return nil, err
	}

	out := make([]IntegrationAccountDto, 0, len(rows))
	for _, r := range rows {
		out = append(out, toIntegrationAccountDto(r))
	}
	return out, nil
}

// CreateAccountMapping manually links an external account to an internal user.
// Tenant-admin only. The mapping is marked 'manual' so the sync job won't undo it.
func CreateAccountMapping(ctx *security.RequestContext, request CreateAccountMappingRequest) (AccountMappingResponse, error) {
	if err := common.ValidateStruct(request); err != nil {
		return AccountMappingResponse{}, err
	}
	if !ctx.GetSecurityContext().IsTenantAdmin() && !ctx.GetSecurityContext().IsSuperAdmin() {
		return AccountMappingResponse{}, common.ErrorUnauthorized("Only tenant admins can map integration accounts")
	}
	tenantId := ctx.GetSecurityContext().GetTenantId()
	if tenantId == "" {
		return AccountMappingResponse{}, common.ErrorBadRequest("tenant_id is required")
	}

	manager, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		return AccountMappingResponse{}, err
	}

	// Map every instance sharing the selected row's (account, identity) — e.g. all
	// PagerDuty rows for the same person in that cloud account — not just the
	// representative row. A different cloud account's row for the same person is a
	// separate mapping and is untouched.
	mappedBy := ctx.GetSecurityContext().GetUserId()
	res, err := manager.Db.Exec(`
		WITH target AS (
			SELECT account_id, integration_type, `+identityKeyExpr+` AS ikey
			FROM integration_user_accounts iua
			WHERE iua.id = $1::uuid AND iua.tenant_id = $2::uuid
		)
		UPDATE integration_user_accounts iua
		SET mapped_user_id = $3::uuid, mapped_via = $4, mapped_by = NULLIF($5,'')::uuid, updated_at = now()
		FROM target
		WHERE iua.tenant_id = $2::uuid
		  AND iua.account_id IS NOT DISTINCT FROM target.account_id
		  AND iua.integration_type = target.integration_type
		  AND `+identityKeyExpr+` = target.ikey`,
		request.MappingId, tenantId, request.UserId, MappedViaManual, mappedBy)
	if err != nil {
		return AccountMappingResponse{}, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return AccountMappingResponse{}, common.ErrorBadRequest("account not found in this tenant")
	}

	return AccountMappingResponse{Id: request.MappingId, Status: fmt.Sprintf("mapped to %s", request.UserId)}, nil
}

// DeleteAccountMapping clears the internal-user link on an external account.
func DeleteAccountMapping(ctx *security.RequestContext, request DeleteAccountMappingRequest) (AccountMappingResponse, error) {
	if err := common.ValidateStruct(request); err != nil {
		return AccountMappingResponse{}, err
	}
	if !ctx.GetSecurityContext().IsTenantAdmin() && !ctx.GetSecurityContext().IsSuperAdmin() {
		return AccountMappingResponse{}, common.ErrorUnauthorized("Only tenant admins can unmap integration accounts")
	}
	tenantId := ctx.GetSecurityContext().GetTenantId()
	if tenantId == "" {
		return AccountMappingResponse{}, common.ErrorBadRequest("tenant_id is required")
	}

	manager, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		return AccountMappingResponse{}, err
	}

	// Clear the link on every instance sharing this row's (account, identity) so an
	// unmapped person can't reappear via a sibling integration's row in the same
	// account. Other accounts' mappings for the same person are left intact.
	res, err := manager.Db.Exec(`
		WITH target AS (
			SELECT account_id, integration_type, `+identityKeyExpr+` AS ikey
			FROM integration_user_accounts iua
			WHERE iua.id = $1::uuid AND iua.tenant_id = $2::uuid
		)
		UPDATE integration_user_accounts iua
		SET mapped_user_id = NULL, mapped_via = NULL, mapped_by = NULL, updated_at = now()
		FROM target
		WHERE iua.tenant_id = $2::uuid
		  AND iua.account_id IS NOT DISTINCT FROM target.account_id
		  AND iua.integration_type = target.integration_type
		  AND `+identityKeyExpr+` = target.ikey`,
		request.MappingId, tenantId)
	if err != nil {
		return AccountMappingResponse{}, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return AccountMappingResponse{}, common.ErrorBadRequest("account not found in this tenant")
	}

	return AccountMappingResponse{Id: request.MappingId, Status: "unmapped"}, nil
}
