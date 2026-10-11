package user

import (
	"database/sql"

	"nudgebee/services/audit"
	"nudgebee/services/common"
	"nudgebee/services/internal/database"
	"nudgebee/services/security"

	"github.com/google/uuid"

	"github.com/jmoiron/sqlx"
	"github.com/lib/pq"
)

// tokenSuffixLen is how much of a key admins see: 16 of its 224 random bits.
const tokenSuffixLen = 4

// tokenSuffix is the tail stored in user_auths.token_suffix; "" for a value too short to share.
func tokenSuffix(token string) string {
	if len(token) <= tokenSuffixLen {
		return ""
	}
	return token[len(token)-tokenSuffixLen:]
}

const (
	auditViaOwner = "owner"
	auditViaAdmin = "admin"
)

// keyAuditInput targets the key, not its owner; owner_id stays in the data for per-user views.
// A delete records old data, any other change new data; the reserved fields override data.
func keyAuditInput(actorID string, eventType audit.EventType, action audit.EventAction, key keyScope, via string, data map[string]any) audit.ChangeInput {
	row := map[string]any{}
	for k, v := range data {
		row[k] = v
	}
	row["key_id"], row["owner_id"], row["actor_id"], row["via"] = key.KeyID, key.UserID, actorID, via
	in := audit.ChangeInput{EventCategory: audit.EventCategoryUser, EventType: eventType, EventAction: action,
		TargetID: key.KeyID, TableName: "user_auths"}
	if action == audit.EventActionDelete {
		in.OldData = row
	} else {
		in.NewData = row
	}
	return in
}

const (
	defaultTokenPage = 50
	maxTokenPage     = 100
)

// tokenFilter selects keys in one tenant; OwnerID "" means every owner, Limit 0 means no paging.
type tokenFilter struct {
	TenantID, OwnerID, Kind string
	Limit, Offset           int
}

func clampPage(limit, offset int) (int, int) {
	if limit <= 0 {
		limit = defaultTokenPage
	}
	if limit > maxTokenPage {
		limit = maxTokenPage
	}
	if offset < 0 {
		offset = 0
	}
	return limit, offset
}

// tokenWhere is shared by list and count so the total always matches the rows paged through.
const tokenWhere = `WHERE ua.tenant_id = $1 AND ua.provider_type = 'credentials' AND ua.provider = 'token'
	   AND (NULLIF($2, '') IS NULL OR ua."user" = NULLIF($2, '')::uuid)
	   AND ($3 = '' OR ua.kind = $3)`

func countTokens(q sqlx.Queryer, f tokenFilter) (int, error) {
	var n int
	err := sqlx.Get(q, &n, `SELECT count(*) FROM user_auths ua `+tokenWhere, f.TenantID, f.OwnerID, f.Kind)
	return n, err
}

// listTokens never reads credential or token_sha256.
func listTokens(q sqlx.Queryer, f tokenFilter) ([]UserAuthToken, error) {
	tokens := []UserAuthToken{}
	if err := sqlx.Select(q, &tokens,
		`SELECT ua.id::text AS id, ua.name, ua.provider, ua.kind, ua.status, ua.created_at, ua.accessed_at,
		        ua.expires_at, ua.scopes, ua.token_suffix, ua."user"::text AS owner_id,
		        COALESCE(NULLIF(u.display_name, ''), u.username, '') AS owner_name
		   FROM user_auths ua
		   LEFT JOIN users u ON u.id = ua."user"
		  `+tokenWhere+`
		  ORDER BY ua.created_at DESC, ua.id
		  LIMIT NULLIF($4, 0) OFFSET $5`, f.TenantID, f.OwnerID, f.Kind, f.Limit, f.Offset); err != nil {
		return nil, err
	}
	if len(tokens) == 0 {
		return tokens, nil
	}
	keyIDs := make([]string, len(tokens))
	for i, t := range tokens {
		keyIDs[i] = t.Id
	}
	limits, err := loadLimitsForKeys(q, f.TenantID, keyIDs)
	if err != nil {
		return nil, err
	}
	policies, err := loadGatewayPoliciesForKeys(q, keyIDs)
	if err != nil {
		return nil, err
	}
	spend, err := loadMonthSpendForKeys(q, f.TenantID, keyIDs)
	if err != nil {
		return nil, err
	}
	lastUse, err := loadLastUseForKeys(q, f.TenantID, keyIDs)
	if err != nil {
		return nil, err
	}
	for i := range tokens {
		id := tokens[i].Id
		tokens[i].Limits = limits[id]
		tokens[i].GatewayPolicy = policies[id]
		if u, ok := spend[id]; ok {
			tokens[i].Usage = &u
		}
		tokens[i].LastProvider, tokens[i].LastModel = lastUse[id].Provider, lastUse[id].Model
	}
	return tokens, nil
}

func loadLimitsForKeys(q sqlx.Queryer, tenantID string, keyIDs []string) (map[string][]TokenLimit, error) {
	var rows []struct {
		APIKeyID string  `db:"api_key_id"`
		Metric   string  `db:"metric"`
		Period   string  `db:"period"`
		Value    float64 `db:"value"`
		Model    string  `db:"model"`
	}
	if err := sqlx.Select(q, &rows,
		`SELECT api_key_id, metric, period, limit_value AS value, model
		   FROM llm_gateway_rate_limits
		  WHERE tenant_id = $1 AND api_key_id = ANY($2) AND enabled = true`, tenantID, pq.Array(keyIDs)); err != nil {
		return nil, err
	}
	out := map[string][]TokenLimit{}
	for _, r := range rows {
		out[r.APIKeyID] = append(out[r.APIKeyID], TokenLimit{Metric: r.Metric, Period: r.Period, Value: r.Value, Model: r.Model})
	}
	return out, nil
}

// loadGatewayPoliciesForKeys matches rows by key and owner, as the gateway does (see replaceKeyGatewayPolicy).
func loadGatewayPoliciesForKeys(q sqlx.Queryer, keyIDs []string) (map[string]*GatewayPolicy, error) {
	var rows []policyEntry
	if err := sqlx.Select(q, &rows,
		`SELECT p.api_key_id, p.dimension, p.value
		   FROM user_auths ua
		   JOIN llm_gateway_access_policies p ON p.api_key_id = ua.id::text AND p.user_id = ua."user"::text
		  WHERE ua.id = ANY($1::uuid[])
		  ORDER BY p.value`, pq.Array(keyIDs)); err != nil {
		return nil, err
	}
	return gatewayPoliciesByKey(rows), nil
}

// loadMonthSpendForKeys uses the UTC calendar month, matching the limiter's month bucket.
func loadMonthSpendForKeys(q sqlx.Queryer, tenantID string, keyIDs []string) (map[string]TokenUsage, error) {
	var rows []struct {
		APIKeyID string  `db:"api_key_id"`
		Requests int64   `db:"requests"`
		Tokens   int64   `db:"tokens"`
		CostUsd  float64 `db:"cost_usd"`
	}
	if err := sqlx.Select(q, &rows,
		`SELECT api_key_id, count(*) AS requests, COALESCE(sum(total_tokens),0) AS tokens,
		        COALESCE(sum(cost_usd),0) AS cost_usd
		   FROM llm_gateway_usage
		  WHERE tenant_id = $1 AND api_key_id = ANY($2)
		    AND created_at >= date_trunc('month', now() AT TIME ZONE 'UTC') AT TIME ZONE 'UTC'
		  GROUP BY api_key_id`, tenantID, pq.Array(keyIDs)); err != nil {
		return nil, err
	}
	out := map[string]TokenUsage{}
	for _, r := range rows {
		out[r.APIKeyID] = TokenUsage{Requests: r.Requests, Tokens: r.Tokens, CostUsd: r.CostUsd}
	}
	return out, nil
}

type keyLastUse struct {
	APIKeyID string `db:"api_key_id"`
	Provider string `db:"provider"`
	Model    string `db:"model"`
}

// loadLastUseForKeys reads one row per key through idx_llm_gw_usage_api_key; never a tenant-wide scan.
func loadLastUseForKeys(q sqlx.Queryer, tenantID string, keyIDs []string) (map[string]keyLastUse, error) {
	var rows []keyLastUse
	if err := sqlx.Select(q, &rows,
		`SELECT k.id AS api_key_id, l.provider, l.model
		   FROM unnest($2::text[]) AS k(id)
		   JOIN LATERAL (SELECT provider, model FROM llm_gateway_usage
		                  WHERE tenant_id = $1 AND api_key_id = k.id
		                  ORDER BY created_at DESC LIMIT 1) l ON true`, tenantID, pq.Array(keyIDs)); err != nil {
		return nil, err
	}
	out := map[string]keyLastUse{}
	for _, r := range rows {
		out[r.APIKeyID] = r
	}
	return out, nil
}

// Read-only admins may look; only full admins may revoke. Custom roles never reach either.
func canListTenantTokens(sc *security.SecurityContext) bool {
	return sc.IsTenantAdmin() || sc.IsTenantReadAdmin() || sc.IsSuperAdmin() || sc.IsSuperAdminReadonly()
}

func canRevokeTenantTokens(sc *security.SecurityContext) bool {
	return sc.IsTenantAdmin() || sc.IsSuperAdmin()
}

func ListTenantTokens(ctx *security.RequestContext, req TenantTokenListRequest) (TenantTokenListResponse, error) {
	sc := ctx.GetSecurityContext()
	if sc.GetTenantId() == "" || !canListTenantTokens(sc) {
		return TenantTokenListResponse{}, common.ErrorUnauthorized("Unauthorized")
	}
	if req.UserID != "" {
		if _, err := uuid.Parse(req.UserID); err != nil {
			return TenantTokenListResponse{}, common.ErrorBadRequest("invalid user_id")
		}
	}
	databaseManager, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		return TenantTokenListResponse{}, err
	}
	limit, offset := clampPage(req.Limit, req.Offset)
	f := tokenFilter{TenantID: sc.GetTenantId(), OwnerID: req.UserID, Kind: req.Kind, Limit: limit, Offset: offset}
	total, err := countTokens(databaseManager.Db, f)
	if err != nil {
		return TenantTokenListResponse{}, err
	}
	tokens, err := listTokens(databaseManager.Db, f)
	if err != nil {
		return TenantTokenListResponse{}, err
	}
	return TenantTokenListResponse{Tokens: tokens, Total: total}, nil
}

var errTokenNotFound = common.ErrorNotFound("token not found")

func DeleteTenantToken(ctx *security.RequestContext, req TenantTokenDeleteRequest) (TenantTokenDeleteResponse, error) {
	sc := ctx.GetSecurityContext()
	if sc.GetTenantId() == "" || sc.GetUserId() == "" || !canRevokeTenantTokens(sc) {
		return TenantTokenDeleteResponse{}, common.ErrorUnauthorized("Unauthorized")
	}
	if _, err := uuid.Parse(req.Id); err != nil {
		return TenantTokenDeleteResponse{}, errTokenNotFound
	}
	databaseManager, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		return TenantTokenDeleteResponse{}, err
	}
	tx, err := databaseManager.Db.Beginx()
	if err != nil {
		return TenantTokenDeleteResponse{}, err
	}
	defer func() { _ = tx.Rollback() }()

	var key keyScope
	var name, kind string
	err = tx.QueryRowx(`SELECT `+keyScopeColumns+`, name, kind FROM user_auths
		WHERE id = $1 AND tenant_id = $2 AND provider_type = 'credentials' AND provider = 'token'
		FOR UPDATE`, req.Id, sc.GetTenantId()).Scan(append(key.scanTargets(), &name, &kind)...)
	if err == sql.ErrNoRows {
		return TenantTokenDeleteResponse{}, errTokenNotFound
	}
	if err != nil {
		return TenantTokenDeleteResponse{}, err
	}
	if _, err := tx.Exec(`DELETE FROM user_auths WHERE id = $1`, key.KeyID); err != nil {
		return TenantTokenDeleteResponse{}, err
	}
	if err := clearKeyRows(tx, key); err != nil {
		return TenantTokenDeleteResponse{}, err
	}
	if err := tx.Commit(); err != nil {
		return TenantTokenDeleteResponse{}, err
	}
	audit.LogChange(ctx, keyAuditInput(sc.GetUserId(), audit.EventTypeUserLoginDelete, audit.EventActionDelete,
		key, auditViaAdmin, map[string]any{"name": name, "kind": kind}))
	return TenantTokenDeleteResponse{Id: key.KeyID, Name: name, OwnerId: key.UserID, Status: "deleted"}, nil
}

// clearKeyRows removes what a revoked key leaves behind: its quota rows and its gateway policy.
// Quota rows go by key id alone, since the key's tenant may have moved with its owner.
func clearKeyRows(tx *sqlx.Tx, key keyScope) error {
	if _, err := tx.Exec(`DELETE FROM llm_gateway_rate_limits WHERE api_key_id = $1`, key.KeyID); err != nil {
		return err
	}
	return replaceKeyGatewayPolicy(tx, key, nil)
}
