package api

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"slices"
	"strings"
	"time"

	"nudgebee/services/common"
	"nudgebee/services/internal/database"
	"nudgebee/services/security"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// AccountTenantFloor refuses, before any handler runs, a request whose body
// names a cloud account or integration of another tenant. Handlers take
// account ids from the request in many shapes, so this is a common check
// under all of them: an id in account_id, cloud_account_id, account_ids or
// cloud_account_ids, anywhere in the body but session_variables, must belong
// to the request's tenant.
//
// It checks tenancy only, not the caller's role on the account: that varies
// per action (read or write, which module) and stays with the handler. It
// skips requests with no tenant (server-internal callers) and super admins.
// A value that is not a uuid is not an account id and is left alone.
const (
	maxFloorBodyBytes    = 32 << 20
	tenantAccountIdsNS   = "tenant_account_ids"
	tenantAccountIdsTTL  = 5 * time.Minute
	accountFloorRefusal  = "access denied for account"
	accountFloorFailNote = "unable to verify account"
)

// accountIdKeys are compared lowercased with '_' removed: handlers decode JSON
// and mapstructure case-insensitively, so ACCOUNT_ID and accountId reach the
// same field as account_id.
var accountIdKeys = map[string]bool{
	"accountid": true, "cloudaccountid": true,
	"accountids": true, "cloudaccountids": true,
}

func isAccountIdKey(key string) bool {
	return accountIdKeys[strings.ReplaceAll(strings.ToLower(key), "_", "")]
}

func init() {
	common.CacheCreateNamespace(tenantAccountIdsNS, common.CacheNamespaceWithExpiration(tenantAccountIdsTTL))
}

func AccountTenantFloor() gin.HandlerFunc {
	return func(c *gin.Context) {
		// Public routes (webhooks) carry a provider's payload, not ours.
		if c.GetBool("isPublic") || c.Request.Method != http.MethodPost || c.Request.Body == nil ||
			!strings.Contains(c.GetHeader("Content-Type"), "json") {
			c.Next()
			return
		}
		original := c.Request.Body
		body, err := io.ReadAll(io.LimitReader(original, maxFloorBodyBytes+1))
		// The handler reads what was consumed here followed by anything not read.
		c.Request.Body = struct {
			io.Reader
			io.Closer
		}{io.MultiReader(bytes.NewReader(body), original), original}
		if err != nil || len(body) > maxFloorBodyBytes {
			c.Next()
			return
		}

		var payload map[string]any
		dec := json.NewDecoder(bytes.NewReader(body))
		dec.UseNumber()
		if dec.Decode(&payload) != nil {
			c.Next()
			return
		}
		sessionVars, _ := payload["session_variables"].(map[string]any)
		tenantId := c.GetHeader("x-tenant-id")
		if tenantId == "" {
			tenantId = sessionString(sessionVars, "tenant_id")
		}
		if tenantId == "" || slices.ContainsFunc(sessionAllowedRoles(sessionVars), func(r string) bool {
			return r == security.AUTH_SUPER_ADMIN_FULL_ROLE || r == security.AUTH_SUPER_ADMIN_READONLY_ROLE
		}) {
			c.Next()
			return
		}

		ids := map[string]bool{}
		for k, v := range payload {
			if !strings.EqualFold(k, "session_variables") {
				collectAccountIds(k, v, ids)
			}
		}
		for id := range ids {
			ok, err := isTenantAccount(tenantId, id)
			if err != nil {
				slog.Error("account floor: account lookup failed", "error", err, "tenant_id", tenantId)
				c.AbortWithStatusJSON(http.StatusServiceUnavailable, common.ErrorActionInternal(accountFloorFailNote))
				return
			}
			if !ok {
				slog.Warn("account floor: request names another tenant's account", "path", c.Request.URL.Path, "tenant_id", tenantId, "account_id", id)
				c.AbortWithStatusJSON(http.StatusForbidden, common.ErrorActionForbidden(accountFloorRefusal))
				return
			}
		}
		c.Next()
	}
}

// collectAccountIds gathers the uuid values under an account id key, at any depth.
func collectAccountIds(key string, v any, into map[string]bool) {
	switch t := v.(type) {
	case map[string]any:
		for k, child := range t {
			collectAccountIds(k, child, into)
		}
	case []any:
		for _, child := range t {
			collectAccountIds(key, child, into)
		}
	case string:
		if isAccountIdKey(key) {
			if id, err := uuid.Parse(t); err == nil {
				into[id.String()] = true
			}
		}
	}
}

// isTenantAccount reports whether accountId belongs to tenantId: from a short
// per-tenant cache of the tenant's account ids, and on a miss from the id's
// own row (a primary-key lookup), so an account onboarded a moment ago
// (cloud-collector inserts them directly) is accepted and a stream of foreign
// ids costs one indexed read each.
//
// Checks both cloud_accounts and integrations: KG's account_ids filter (and
// the UI's own Account picker) treats SaaS integrations - PagerDuty, Slack,
// Jira, GitHub, GitLab - as "accounts" alongside real cloud accounts, so an
// account_id-shaped request key can legitimately carry either id space
// (PagerDuty-backed KG queries send integration ids). Add a table here whenever a new id space starts
// appearing under an account_id key.
var isTenantAccount = func(tenantId, accountId string) (bool, error) {
	if _, err := uuid.Parse(tenantId); err != nil {
		return false, nil // not a tenant id: nothing it names can be its account
	}
	cached, ok := common.CacheGet(tenantAccountIdsNS, tenantId)
	if ok {
		var ids []string
		if json.Unmarshal(cached, &ids) == nil && slices.Contains(ids, accountId) {
			return true, nil
		}
	}
	dbms, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		return false, err
	}
	if !ok {
		var ids []string
		if err := dbms.Db.Select(&ids, `
			SELECT id::text FROM cloud_accounts WHERE tenant = $1::uuid
			UNION ALL
			SELECT id::text FROM integrations WHERE tenant_id = $1::uuid
		`, tenantId); err != nil {
			return false, err
		}
		if raw, err := json.Marshal(ids); err == nil {
			_ = common.CacheSet(tenantAccountIdsNS, tenantId, raw)
		}
		return slices.Contains(ids, accountId), nil
	}
	var owner []string
	if err := dbms.Db.Select(&owner, `
		SELECT tenant::text FROM cloud_accounts WHERE id = $1::uuid
		UNION ALL
		SELECT tenant_id::text FROM integrations WHERE id = $1::uuid
	`, accountId); err != nil {
		return false, err
	}
	if len(owner) == 1 && owner[0] == tenantId {
		_ = common.CacheDelete(tenantAccountIdsNS, tenantId) // a new account/integration: reload the list next time
		return true, nil
	}
	return false, nil
}
