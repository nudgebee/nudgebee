package tenant

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"nudgebee/services/audit"
	"nudgebee/services/common"
	"nudgebee/services/config"
	"nudgebee/services/eventrule"
	"nudgebee/services/internal/database"
	"nudgebee/services/internal/database/models"
	"nudgebee/services/security"
	"slices"
	"strconv"
	"time"

	bigcache_store "github.com/allegro/bigcache/v3"
	"github.com/eko/gocache/lib/v4/cache"
	"github.com/eko/gocache/store/bigcache/v4"
	"github.com/jmoiron/sqlx"
	"github.com/samber/lo"
)

const FEATURE_RBACK_K8S_ACCESS = "RBAC_K8S"
const FEATURE_EVENT_AUTO_AI_SUMMARY = "EVENT_AUTO_AI_SUMMARY"
const FEATURE_ANOMALY_DETECTION = "ANOMALY_DETECTION"
const FEATURE_ANOMALY_DETECTION_ERROR_RATE = "ANOMALY_DETECTION_ERROR_RATE"
const FEATURE_TRACES_KNOWLEDGE_GRAPH = "TRACES_SERVICE_MAP_KNOWLEDGE_GRAPH"
const FEATURE_TICKETS_ADD_EVENT_COMMENTS = "TICKETS_ADD_EVENT_COMMENTS"
const FEATURE_VERTICAL_RIGHTSIZING = "VERTICAL_RIGHTSIZING"
const FEATURE_WEBHOOK_LLM_RESOLUTION = "WEBHOOK_LLM_RESOLUTION"
const FEATURE_SERVER_ORCHESTRATED_SCANNERS = "SERVER_ORCHESTRATED_SCANNERS"

var featureFlagCache *cache.Cache[[]byte]

func init() {
	defaultConfig := bigcache_store.DefaultConfig(time.Duration(config.Config.CacheExpirationMinutes) * time.Minute)
	defaultConfig.HardMaxCacheSize = config.Config.CacheInMemorySizeMb
	defaultConfig.MaxEntriesInWindow = config.Config.CacheInMemoryMaxEntries
	cacheClientLocal, _ := bigcache_store.New(context.Background(), defaultConfig)
	bigcacheStore := bigcache.NewBigcache(cacheClientLocal)
	featureFlagCache = cache.New[[]byte](bigcacheStore)
}

func ListTenants(context *security.RequestContext) ([]models.Tenant, error) {
	manager, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		return []models.Tenant{}, err
	}
	rows, err := manager.Db.Queryx("SELECT id, name FROM tenant")
	if err != nil {
		return []models.Tenant{}, err
	}
	defer func() {
		err := rows.Close()
		if err != nil {
			context.GetLogger().Error("Error closing rows", "error", err)
		}
	}()

	tenants := []models.Tenant{}
	for rows.Next() {
		var tenant models.Tenant
		err := rows.StructScan(&tenant)
		if err != nil {
			return []models.Tenant{}, err
		}
		tenants = append(tenants, tenant)
	}

	if err := rows.Err(); err != nil {
		return []models.Tenant{}, err
	}

	return tenants, nil
}

func ListTenantsWithActiveAccounts(context *security.RequestContext) ([]models.Tenant, error) {
	manager, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		return []models.Tenant{}, err
	}
	rows, err := manager.Db.Queryx("SELECT DISTINCT t.id, t.name FROM tenant t JOIN cloud_accounts ca ON t.id = ca.tenant JOIN agent ag ON ca.id = ag.cloud_account_id WHERE ca.cloud_provider = 'K8s' AND ag.status = 'CONNECTED'")
	if err != nil {
		return []models.Tenant{}, err
	}
	defer func() {
		err := rows.Close()
		if err != nil {
			context.GetLogger().Error("Error closing rows", "error", err)
		}
	}()

	tenants := []models.Tenant{}
	for rows.Next() {
		var tenant models.Tenant
		err := rows.StructScan(&tenant)
		if err != nil {
			return []models.Tenant{}, err
		}
		tenants = append(tenants, tenant)
	}

	if err := rows.Err(); err != nil {
		return []models.Tenant{}, err
	}

	return tenants, nil
}

func GetTenant(context *security.RequestContext, id string) (models.Tenant, error) {
	manager, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		return models.Tenant{}, err
	}
	row := manager.Db.QueryRowx("select * from tenant where id = $1", id)
	if row.Err() != nil {
		return models.Tenant{}, row.Err()
	}
	tenant := models.Tenant{}
	err = row.StructScan(&tenant)
	if err != nil {
		return models.Tenant{}, err
	}
	return tenant, nil
}

func GetTenantByName(context *security.RequestContext, name string) (models.Tenant, error) {
	manager, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		return models.Tenant{}, err
	}
	row := manager.Db.QueryRowx("SELECT * FROM tenant WHERE name = $1", name)
	if row.Err() != nil {
		return models.Tenant{}, row.Err()
	}
	t := models.Tenant{}
	err = row.StructScan(&t)
	if err != nil {
		return models.Tenant{}, err
	}
	return t, nil
}

func GetTenantAttributes(context *security.RequestContext) ([]models.TenantAttributes, error) {
	manager, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		return []models.TenantAttributes{}, err
	}

	var tenantAttributes []models.TenantAttributes
	err = manager.Db.Select(&tenantAttributes, "SELECT * FROM tenant_attrs WHERE tenant_id = $1", context.GetSecurityContext().GetTenantId())
	if err != nil {
		return []models.TenantAttributes{}, err
	}

	return tenantAttributes, nil
}

func CreateTenant(context *security.RequestContext, request TenantCreateRequest) (models.Tenant, error) {
	context.GetLogger().Info(`creating tenant`, "request", slog.AnyValue(request))
	if !context.GetSecurityContext().IsSuperAdmin() {
		return models.Tenant{}, common.ErrorUnauthorized("Not Allowed")
	}
	err := common.ValidateStruct(request)
	if err != nil {
		return models.Tenant{}, err
	}

	if request.TenantType == "" {
		request.TenantType = "Customer"
	}

	dbms, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		return models.Tenant{}, err
	}

	tx, err := dbms.Db.Beginx()
	if err != nil {
		return models.Tenant{}, err
	}
	defer func() { _ = tx.Rollback() }()

	// Insert tenant
	var tenantId string
	err = tx.QueryRowx(
		`INSERT INTO tenant (created_by, updated_by, name, type) VALUES ($1, $1, $2, $3) RETURNING id`,
		request.UserId, request.TenantName, request.TenantType,
	).Scan(&tenantId)
	if err != nil {
		return models.Tenant{}, err
	}

	context.GetLogger().Info(`CreateTenantResponse`, "tenantId", tenantId)

	// Insert tenant_users (owner relationship)
	_, err = tx.Exec(
		`INSERT INTO tenant_users (tenant, "user", is_owner, created_by, updated_by) VALUES ($1, $2, true, $2, $2)`,
		tenantId, request.UserId,
	)
	if err != nil {
		return models.Tenant{}, err
	}

	// Insert admin role for user
	_, err = tx.Exec(
		`INSERT INTO user_roles (tenant_id, created_by, entity_id, entity_type, role, user_id) VALUES ($1, $2, $3, 'tenant', $4, $2)`,
		tenantId, request.UserId, tenantId, security.AUTH_TENANT_ADMIN_ROLE,
	)
	if err != nil {
		return models.Tenant{}, err
	}

	if err = tx.Commit(); err != nil {
		return models.Tenant{}, err
	}

	context.GetLogger().Info(`CreateUserRole`, "tenantId", tenantId, "userId", request.UserId)

	// Audit: tenant created
	audit.LogChange(context, audit.ChangeInput{
		EventCategory: audit.EventCategoryTenant,
		EventType:     audit.EventTypeTenantCreate,
		EventAction:   audit.EventActionCreate,
		TargetID:      tenantId,
		TableName:     "tenant",
		NewData:       map[string]any{"id": tenantId, "name": request.TenantName, "type": request.TenantType, "created_by": request.UserId},
	})
	// Audit: tenant_users owner mapping
	audit.LogChange(context, audit.ChangeInput{
		EventCategory: audit.EventCategoryTenant,
		EventType:     audit.EventTypeTenantUserCreate,
		EventAction:   audit.EventActionCreate,
		TargetID:      request.UserId,
		TenantID:      tenantId,
		TableName:     "tenant_users",
		NewData:       map[string]any{"tenant": tenantId, "user": request.UserId, "is_owner": true},
	})
	// Audit: admin role assigned
	audit.LogChange(context, audit.ChangeInput{
		EventCategory: audit.EventCategoryRole,
		EventType:     audit.EventTypeRoleUserCreate,
		EventAction:   audit.EventActionCreate,
		TargetID:      request.UserId,
		TenantID:      tenantId,
		TableName:     "user_roles",
		NewData:       map[string]any{"tenant_id": tenantId, "user_id": request.UserId, "role": security.AUTH_TENANT_ADMIN_ROLE, "entity_type": "tenant", "entity_id": tenantId},
	})

	err = security.InvalidateCacheForUser(request.UserId)
	if err != nil {
		context.GetLogger().Error("Error invalidating cache for user", "error", err, "userId", request.UserId)
	}
	go func() {
		err := eventrule.LoadEventActions(context)
		if err != nil {
			context.GetLogger().Error("Error to load agent playbook", "error", err)
		}
	}()

	newTenant, err := GetTenant(context, tenantId)
	if err != nil {
		return models.Tenant{}, err
	}

	if err := audit.PublishAuditEvent(context, audit.Audit{
		UserId:        request.UserId,
		TenantId:      tenantId,
		EventTime:     time.Now(),
		EventCategory: audit.EventCategoryTenant,
		EventType:     audit.EventTypeTenantCreate,
		EventState:    newTenant,
		EventActor:    audit.EventActorApiService,
		EventTarget:   "tenant",
		EventAction:   audit.EventActionCreate,
		EventStatus:   audit.EventStatusSuccess,
	}); err != nil {
		context.GetLogger().Error("failed to publish audit event", "error", err)
	}

	return newTenant, nil

}

func IsFeatureEnabled(ctx *security.RequestContext, tenantId string, feature string) bool {
	key := feature + ":" + tenantId
	cachedValue, err := featureFlagCache.Get(context.Background(), key)
	if cachedValue != nil {
		return string(cachedValue) == "true"
	}
	if err != nil {
		ctx.GetLogger().Info("tenant: unable to fetch cached value, will be updating cache", "feature", feature, "tenantId", tenantId)
	}

	tenants, err := ListTenantWithFeature(ctx, feature)
	if err != nil {
		if ctx != nil {
			ctx.GetLogger().Error("Error fetching tenant with feature", "error", err)
		}
		return false
	}

	featureContain := slices.Contains(tenants, tenantId)
	err = featureFlagCache.Set(context.Background(), feature+":"+tenantId, []byte(strconv.FormatBool(featureContain)))
	if err != nil {
		ctx.GetLogger().Error("tenant: unable to set cached value", "error", err, "feature", feature, "tenant", tenantId)
	}
	return featureContain
}

// IsFeatureExplicitlyEnabled returns true ONLY when the tenant has a
// feature_flag row with status='enabled'. Missing rows count as disabled.
// Mirror of IsFeatureEnabledByDefault for the opposite default — use this for
// features rolled out tenant-by-tenant where every "off" should stay off
// without an explicit row.
func IsFeatureExplicitlyEnabled(ctx *security.RequestContext, tenantId string, feature string) bool {
	key := feature + ":explicit:" + tenantId
	cachedValue, err := featureFlagCache.Get(context.Background(), key)
	if cachedValue != nil {
		return string(cachedValue) == "true"
	}
	if err != nil && ctx != nil {
		ctx.GetLogger().Info("tenant: unable to fetch cached value, will be updating cache", "feature", feature, "tenantId", tenantId)
	}

	databaseManager, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		if ctx != nil {
			ctx.GetLogger().Error("tenant: unable to get database manager", "error", err)
		}
		return false
	}

	var status string
	err = databaseManager.Db.Get(&status, "SELECT status FROM feature_flag WHERE feature_id = $1 AND tenant_id = $2::uuid AND account_id IS NULL", feature, tenantId)
	if err != nil {
		// No row found — explicit-enable means stay off.
		_ = featureFlagCache.Set(context.Background(), key, []byte("false"))
		return false
	}

	enabled := status == "enabled"
	_ = featureFlagCache.Set(context.Background(), key, []byte(strconv.FormatBool(enabled)))
	return enabled
}

// IsFeatureEnabledByDefault returns true unless the feature is explicitly disabled for the tenant.
// Use this for features that should be on by default and only turned off when explicitly set to 'disabled'.
func IsFeatureEnabledByDefault(ctx *security.RequestContext, tenantId string, feature string) bool {
	key := feature + ":default:" + tenantId
	cachedValue, err := featureFlagCache.Get(context.Background(), key)
	if cachedValue != nil {
		return string(cachedValue) == "true"
	}
	if err != nil && ctx != nil {
		ctx.GetLogger().Info("tenant: unable to fetch cached value, will be updating cache", "feature", feature, "tenantId", tenantId)
	}

	databaseManager, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		if ctx != nil {
			ctx.GetLogger().Error("tenant: unable to get database manager", "error", err)
		}
		return true
	}

	var status string
	err = databaseManager.Db.Get(&status, "SELECT status FROM feature_flag WHERE feature_id = $1 AND tenant_id = $2::uuid AND account_id IS NULL", feature, tenantId)
	if err != nil {
		// No row found or error — default to enabled
		_ = featureFlagCache.Set(context.Background(), key, []byte("true"))
		return true
	}

	enabled := status != "disabled"
	_ = featureFlagCache.Set(context.Background(), key, []byte(strconv.FormatBool(enabled)))
	return enabled
}

func ListTenantWithFeature(context *security.RequestContext, feature string) ([]string, error) {
	databaseManager, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		return []string{}, err
	}
	rows, err := databaseManager.Db.Queryx("SELECT distinct tenant_id FROM feature_flag WHERE feature_id = $1 and status = 'enabled' ", feature)
	if err != nil {
		return []string{}, err
	}
	defer func() {
		err := rows.Close()
		if err != nil {
			context.GetLogger().Error("Error closing rows", "error", err)
		}
	}()

	rowsMap := make([]string, 0)
	for rows.Next() {
		var row string
		err = rows.Scan(&row)
		if err != nil {
			return nil, err
		}
		rowsMap = append(rowsMap, row)
	}
	return rowsMap, nil
}

func commonTenantRoleValidation(ctx *security.RequestContext, manager *database.DatabaseManager, role string) error {
	if role != "" && !security.IsValidTenantRole(role) {
		return common.ErrorBadRequest("Invalid role")
	}
	return nil
}

func UpsertTenantUserRole(ctx *security.RequestContext, request TenantUserRoleUpsertRequest) (TenantUserRoleUpsertResponse, error) {
	ctx.GetLogger().Info(`upsert user role`, "request", slog.AnyValue(request))
	if !ctx.GetSecurityContext().IsTenantAdmin() {
		return TenantUserRoleUpsertResponse{}, common.ErrorUnauthorized("Not Allowed")
	}

	// resolve username to user_id if user_id is not provided
	if request.UserId == "" && request.Username != "" {
		manager, err := database.GetDatabaseManager(database.Metastore)
		if err != nil {
			return TenantUserRoleUpsertResponse{}, err
		}
		var userId string
		err = manager.Db.Get(&userId, "SELECT id FROM users WHERE username = $1", request.Username)
		if err != nil {
			return TenantUserRoleUpsertResponse{}, common.ErrorBadRequest("User not found: " + request.Username)
		}
		request.UserId = userId
	}

	if request.UserId == "" {
		return TenantUserRoleUpsertResponse{}, common.ErrorBadRequest("user_id or username is required")
	}

	manager, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		return TenantUserRoleUpsertResponse{}, err
	}

	err = commonTenantRoleValidation(ctx, manager, request.Role)
	if err != nil {
		return TenantUserRoleUpsertResponse{}, err
	}

	tenantUserRow := manager.Db.QueryRowx("SELECT count(*) as cnt FROM tenant_users WHERE tenant = $1 AND \"user\" = $2", ctx.GetSecurityContext().GetTenantId(), request.UserId)
	if tenantUserRow.Err() != nil {
		ctx.GetLogger().Error("Error fetching user", "error", tenantUserRow.Err())
		return TenantUserRoleUpsertResponse{}, common.ErrorUnauthorized("User not found in tenant")
	}
	tenantUse := map[string]any{}
	err = tenantUserRow.MapScan(tenantUse)
	if err != nil {
		ctx.GetLogger().Error("Error scanning user", "error", err)
		return TenantUserRoleUpsertResponse{}, common.ErrorUnauthorized("User not found in tenant")
	}
	if tenantUse["cnt"].(int64) == 0 {
		return TenantUserRoleUpsertResponse{}, common.ErrorUnauthorized("User not found in tenant")
	}

	defer func() {
		ctx.GetLogger().Info("Invalidating cache for user")
		err := ctx.GetSecurityContext().InvalidateCache()
		if err != nil {
			slog.Error("Error invalidating cache", "error", err, "user_id", request.UserId)
		}
	}()

	// remove all roles and return success
	if request.Role == "" {
		_, err = manager.Db.Exec("DELETE FROM user_roles WHERE entity_type = 'tenant' AND user_id = $1", request.UserId)
		if err != nil {
			ctx.GetLogger().Error("Error deleting group role", "error", err)
			return TenantUserRoleUpsertResponse{}, common.ErrorInternal("Error updating group role")
		}
		audit.LogChange(ctx, audit.ChangeInput{
			EventCategory: audit.EventCategoryRole,
			EventType:     audit.EventTypeRoleUserDelete,
			EventAction:   audit.EventActionDelete,
			TargetID:      request.UserId,
			TableName:     "user_roles",
			OldData:       map[string]any{"user_id": request.UserId, "entity_type": "tenant"},
		})
		return TenantUserRoleUpsertResponse{
			Status:  "success",
			Message: "Group role updated successfully",
		}, nil
	}

	// get existing roles
	tenantUserRolesRows, err := manager.Db.Queryx("SELECT * FROM user_roles WHERE entity_id = $1 AND entity_type = 'tenant' AND user_id = $2", ctx.GetSecurityContext().GetTenantId(), request.UserId)
	if tenantUserRolesRows.Err() != nil || err != nil {
		return TenantUserRoleUpsertResponse{}, common.ErrorUnauthorized("User not found in tenant roles")
	}
	defer func() {
		err := tenantUserRolesRows.Close()
		if err != nil {
			ctx.GetLogger().Error("Error closing tenantUserRolesRows", "error", err)
		}
	}()

	tenantUserRoles := []map[string]any{}
	for tenantUserRolesRows.Next() {
		tenantUserRole := map[string]any{}
		err = tenantUserRolesRows.MapScan(tenantUserRole)
		if err != nil {
			ctx.GetLogger().Error("Error scanning user role", "error", err)
			return TenantUserRoleUpsertResponse{}, common.ErrorUnauthorized("User not found in tenant roles")
		}
		tenantUserRoles = append(tenantUserRoles, tenantUserRole)
	}

	// if existing roles are empty, insert new role
	if len(tenantUserRoles) == 0 {
		_, err := manager.Db.Exec("INSERT INTO user_roles (created_by, entity_id, entity_type, role, user_id, tenant_id) VALUES ($1, $2, $3, $4, $5, $6)", ctx.GetSecurityContext().GetUserId(), ctx.GetSecurityContext().GetTenantId(), "tenant", request.Role, request.UserId, ctx.GetSecurityContext().GetTenantId())
		if err != nil {
			ctx.GetLogger().Error("Error inserting user role", "error", err)
			return TenantUserRoleUpsertResponse{}, common.ErrorInternal("Error updating user role")
		}
		audit.LogChange(ctx, audit.ChangeInput{
			EventCategory: audit.EventCategoryRole,
			EventType:     audit.EventTypeRoleUserCreate,
			EventAction:   audit.EventActionCreate,
			TargetID:      request.UserId,
			TableName:     "user_roles",
			NewData:       map[string]any{"user_id": request.UserId, "role": request.Role, "entity_type": "tenant", "entity_id": ctx.GetSecurityContext().GetTenantId()},
		})
		return TenantUserRoleUpsertResponse{
			Status:  "success",
			Message: "User role updated successfully",
		}, nil
	}

	// if existing role same as new role, return success
	if len(tenantUserRoles) == 1 && tenantUserRoles[0]["role"] == request.Role {
		return TenantUserRoleUpsertResponse{
			Status:  "success",
			Message: "User role updated successfully",
		}, nil
	}

	// update existing role
	_, err = manager.Db.Exec("UPDATE user_roles SET role = $1 WHERE entity_id = $2 AND entity_type = 'tenant' AND user_id = $3", request.Role, ctx.GetSecurityContext().GetTenantId(), request.UserId)
	if err != nil {
		ctx.GetLogger().Error("Error updating user role", "error", err)
		return TenantUserRoleUpsertResponse{}, common.ErrorInternal("Error updating user role")
	}

	audit.LogChange(ctx, audit.ChangeInput{
		EventCategory: audit.EventCategoryRole,
		EventType:     audit.EventTypeRoleUserUpdate,
		EventAction:   audit.EventActionUpdate,
		TargetID:      request.UserId,
		TableName:     "user_roles",
		NewData:       map[string]any{"user_id": request.UserId, "role": request.Role, "entity_type": "tenant", "entity_id": ctx.GetSecurityContext().GetTenantId()},
	})

	return TenantUserRoleUpsertResponse{
		Status:  "success",
		Message: "User role updated successfully",
	}, nil
}

func UpsertTenantGroupRole(ctx *security.RequestContext, request TenantGroupRoleUpsertRequest) (TenantGroupRoleUpsertResponse, error) {
	ctx.GetLogger().Info(`upsert group role`, "request", slog.AnyValue(request))
	if !ctx.GetSecurityContext().IsTenantAdmin() {
		return TenantGroupRoleUpsertResponse{}, common.ErrorUnauthorized("Not Allowed")
	}
	// validate request
	err := common.ValidateStruct(request)
	if err != nil {
		return TenantGroupRoleUpsertResponse{}, err
	}

	manager, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		return TenantGroupRoleUpsertResponse{}, err
	}

	err = commonTenantRoleValidation(ctx, manager, request.Role)
	if err != nil {
		return TenantGroupRoleUpsertResponse{}, err
	}

	tenantGroupRow := manager.Db.QueryRowx("SELECT * FROM user_groups WHERE tenant = $1 AND id = $2", ctx.GetSecurityContext().GetTenantId(), request.GroupId)
	if tenantGroupRow.Err() != nil {
		return TenantGroupRoleUpsertResponse{}, common.ErrorUnauthorized("Group not found in tenant")
	}
	tenantGroup := map[string]any{}
	err = tenantGroupRow.MapScan(tenantGroup)
	if err != nil {
		ctx.GetLogger().Error("Error scanning group", "error", err)
		return TenantGroupRoleUpsertResponse{}, common.ErrorUnauthorized("Group not found in tenant")
	}

	defer func() {
		ctx.GetLogger().Info("Invalidating cache for tenant, because group is updated", "group", request.GroupId)
		err := security.InvalidateCacheForTenant(ctx.GetSecurityContext().GetTenantId())
		if err != nil {
			slog.Error("Error invalidating cache", "error", err, "tenant_id", ctx.GetSecurityContext().GetTenantId())
		}
	}()

	// remove all roles and return success
	if request.Role == "" {
		_, err = manager.Db.Exec("DELETE FROM group_roles WHERE entity_type = 'tenant' AND group_id = $1", request.GroupId)
		if err != nil {
			ctx.GetLogger().Error("Error deleting group role", "error", err)
			return TenantGroupRoleUpsertResponse{}, common.ErrorInternal("Error updating group role")
		}
		return TenantGroupRoleUpsertResponse{
			Status:  "success",
			Message: "Group role updated successfully",
		}, nil
	}

	// get existing roles
	tenantGroupRolesRows, err := manager.Db.Queryx("SELECT * FROM group_roles WHERE entity_id = $1 AND entity_type = 'tenant' AND group_id = $2", ctx.GetSecurityContext().GetTenantId(), request.GroupId)
	if tenantGroupRolesRows.Err() != nil || err != nil {
		return TenantGroupRoleUpsertResponse{}, common.ErrorUnauthorized("Group not found in tenant roles")
	}
	defer func() {
		err := tenantGroupRolesRows.Close()
		if err != nil {
			ctx.GetLogger().Error("Error closing tenantGroupRolesRows", "error", err)
		}
	}()

	tenantGroupRoles := []map[string]any{}
	for tenantGroupRolesRows.Next() {
		tenantGroupRole := map[string]any{}
		err = tenantGroupRolesRows.MapScan(tenantGroupRole)
		if err != nil {
			ctx.GetLogger().Error("Error scanning group role", "error", err)
			return TenantGroupRoleUpsertResponse{}, common.ErrorUnauthorized("Group not found in tenant roles")
		}
		tenantGroupRoles = append(tenantGroupRoles, tenantGroupRole)
	}

	// if existing roles are empty, insert new role
	if len(tenantGroupRoles) == 0 {
		_, err := manager.Db.Exec("INSERT INTO group_roles (created_by, entity_id, entity_type, role, group_id) VALUES ($1, $2, $3, $4, $5)", ctx.GetSecurityContext().GetUserId(), ctx.GetSecurityContext().GetTenantId(), "tenant", request.Role, request.GroupId)
		if err != nil {
			ctx.GetLogger().Error("Error inserting group role", "error", err)
			return TenantGroupRoleUpsertResponse{}, common.ErrorInternal("Error updating group role")
		}
		return TenantGroupRoleUpsertResponse{
			Status:  "success",
			Message: "Group role updated successfully",
		}, nil
	}

	// if existing role same as new role, return success
	if len(tenantGroupRoles) == 1 && tenantGroupRoles[0]["role"] == request.Role {
		return TenantGroupRoleUpsertResponse{
			Status:  "success",
			Message: "Group role updated successfully",
		}, nil
	}

	// update existing role
	_, err = manager.Db.Exec("UPDATE group_roles SET role = $1 WHERE entity_id = $2 AND entity_type = 'tenant' AND group_id = $3", request.Role, ctx.GetSecurityContext().GetTenantId(), request.GroupId)
	if err != nil {
		ctx.GetLogger().Error("Error updating group role", "error", err)
		return TenantGroupRoleUpsertResponse{}, common.ErrorInternal("Error updating group role")
	}

	return TenantGroupRoleUpsertResponse{
		Status:  "success",
		Message: "Group role updated successfully",
	}, nil
}

func commonAccountRoleValidation(ctx *security.RequestContext, manager *database.DatabaseManager, accountRoles []AccountRole) error {
	if !ctx.GetSecurityContext().IsTenantAdmin() {
		return common.ErrorUnauthorized("Not Allowed")
	}

	for _, role := range accountRoles {
		if role.Role != security.AUTH_ACCOUNT_READ_ADMIN_ROLE && role.Role != security.AUTH_ACCOUNT_ADMIN_ROLE {
			return common.ErrorBadRequest("Invalid role")
		}
	}

	// check if account exists for same tenant
	for _, role := range accountRoles {
		accountRow := manager.Db.QueryRowx("SELECT count(*) FROM cloud_accounts WHERE tenant = $1 AND id = $2", ctx.GetSecurityContext().GetTenantId(), role.AccountId)
		if accountRow.Err() != nil {
			ctx.GetLogger().Error("Error fetching account", "error", accountRow.Err())
			return common.ErrorUnauthorized("Account not found in tenant")
		}
		account := map[string]any{}
		err := accountRow.MapScan(account)
		if err != nil {
			ctx.GetLogger().Error("Error scanning account", "error", err)
			return common.ErrorUnauthorized("Account not found in tenant")
		}
		if account["count"].(int64) == 0 {
			return common.ErrorUnauthorized("Account not found in tenant")
		}
	}

	return nil
}

func UpsertAccountUserRole(ctx *security.RequestContext, request AccountUserRoleUpsertRequest) (AccountUserRoleUpsertResponse, error) {
	ctx.GetLogger().Info(`upsert user account role`, "request", slog.AnyValue(request))

	manager, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		return AccountUserRoleUpsertResponse{}, err
	}

	// validate request
	err = common.ValidateStruct(request)
	if err != nil {
		return AccountUserRoleUpsertResponse{}, err
	}

	err = commonAccountRoleValidation(ctx, manager, request.AccountRoles)
	if err != nil {
		return AccountUserRoleUpsertResponse{}, err
	}

	// check if user is part of tenant
	userRow := manager.Db.QueryRowx("SELECT count(*) FROM tenant_users WHERE tenant = $1 AND \"user\" = $2", ctx.GetSecurityContext().GetTenantId(), request.UserId)
	if userRow.Err() != nil {
		ctx.GetLogger().Error("Error fetching user", "error", userRow.Err())
		return AccountUserRoleUpsertResponse{}, common.ErrorUnauthorized("User not found in tenant")
	}
	user := map[string]any{}
	err = userRow.MapScan(user)
	if err != nil {
		ctx.GetLogger().Error("Error scanning user", "error", err)
		return AccountUserRoleUpsertResponse{}, common.ErrorUnauthorized("User not found in tenant")
	}
	if user["count"].(int64) == 0 {
		return AccountUserRoleUpsertResponse{}, common.ErrorUnauthorized("User not found in tenant")
	}

	defer func() {
		ctx.GetLogger().Info("Invalidating cache for user, because account role is updated")
		err := ctx.GetSecurityContext().InvalidateCache()
		if err != nil {
			slog.Error("Error invalidating cache", "error", err, "user_id", request.UserId)
		}
	}()

	// if empty then remove all roles
	_, err = manager.Db.Exec("DELETE FROM user_roles WHERE entity_type = $2 AND user_id = $1", request.UserId, security.RBAC_ENTITY_TYPE_ACCOUNT)
	if err != nil {
		ctx.GetLogger().Error("Error deleting user role", "error", err)
		return AccountUserRoleUpsertResponse{}, common.ErrorInternal("Error updating user role")
	}
	if len(request.AccountRoles) == 0 {
		return AccountUserRoleUpsertResponse{
			Status:  "success",
			Message: "User role updated successfully",
		}, nil
	}

	// insert new role
	rolesToInsert := lo.Map(request.AccountRoles, func(role AccountRole, i int) map[string]any {
		return map[string]any{
			"created_by":  ctx.GetSecurityContext().GetUserId(),
			"entity_id":   role.AccountId,
			"entity_type": security.RBAC_ENTITY_TYPE_ACCOUNT,
			"role":        role.Role,
			"user_id":     request.UserId,
			"tenant_id":   ctx.GetSecurityContext().GetTenantId(),
		}
	})
	_, err = manager.Db.NamedExec("INSERT INTO user_roles (created_by, entity_id, entity_type, role, user_id, tenant_id) VALUES (:created_by, :entity_id, :entity_type, :role, :user_id, :tenant_id)", rolesToInsert)
	if err != nil {
		ctx.GetLogger().Error("Error inserting user role", "error", err)
		return AccountUserRoleUpsertResponse{}, common.ErrorInternal("Error updating user role")
	}
	for _, role := range request.AccountRoles {
		audit.LogChange(ctx, audit.ChangeInput{
			EventCategory: audit.EventCategoryRole,
			EventType:     audit.EventTypeRoleUserCreate,
			EventAction:   audit.EventActionCreate,
			TargetID:      request.UserId,
			AccountID:     role.AccountId,
			TableName:     "user_roles",
			NewData:       map[string]any{"user_id": request.UserId, "role": role.Role, "entity_type": "account", "entity_id": role.AccountId},
		})
	}
	return AccountUserRoleUpsertResponse{
		Status:  "success",
		Message: "User role updated successfully",
	}, nil
}

func UpsertAccountGroupRole(ctx *security.RequestContext, request AccountGroupRoleUpsertRequest) (AccountUserRoleUpsertResponse, error) {
	ctx.GetLogger().Info(`upsert group account role`, "request", slog.AnyValue(request))

	manager, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		return AccountUserRoleUpsertResponse{}, err
	}

	// validate request
	err = common.ValidateStruct(request)
	if err != nil {
		return AccountUserRoleUpsertResponse{}, err
	}

	err = commonAccountRoleValidation(ctx, manager, request.AccountRoles)
	if err != nil {
		return AccountUserRoleUpsertResponse{}, err
	}

	// check if group is part of tenant
	groupRow := manager.Db.QueryRowx("SELECT count(*) FROM user_groups WHERE tenant = $1 AND id = $2", ctx.GetSecurityContext().GetTenantId(), request.GroupId)
	if groupRow.Err() != nil {
		ctx.GetLogger().Error("Error fetching group", "error", groupRow.Err())
		return AccountUserRoleUpsertResponse{}, common.ErrorUnauthorized("Group not found in tenant")
	}
	group := map[string]any{}
	err = groupRow.MapScan(group)
	if err != nil {
		ctx.GetLogger().Error("Error scanning group", "error", err)
		return AccountUserRoleUpsertResponse{}, common.ErrorUnauthorized("Group not found in tenant")
	}
	if group["count"].(int64) == 0 {
		return AccountUserRoleUpsertResponse{}, common.ErrorUnauthorized("Group not found in tenant")
	}

	defer func() {
		ctx.GetLogger().Info("Invalidating cache for tenant, because account group role is updated", "group", request.GroupId)
		err := security.InvalidateCacheForTenant(ctx.GetSecurityContext().GetTenantId())
		if err != nil {
			ctx.GetLogger().Error("Error invalidating cache", "error", err)
		}
	}()

	// remove all roles
	_, err = manager.Db.Exec("DELETE FROM group_roles WHERE entity_type = $2 AND group_id = $1", request.GroupId, security.RBAC_ENTITY_TYPE_ACCOUNT)
	if err != nil {
		ctx.GetLogger().Error("Error deleting group role", "error", err)
		return AccountUserRoleUpsertResponse{}, common.ErrorInternal("Error updating group role")
	}

	// if new roles are empty, return success
	if len(request.AccountRoles) == 0 {
		return AccountUserRoleUpsertResponse{
			Status:  "success",
			Message: "Group role updated successfully",
		}, nil
	}

	// insert new role
	rolesToInsert := lo.Map(request.AccountRoles, func(role AccountRole, i int) map[string]any {
		return map[string]any{
			"created_by":  ctx.GetSecurityContext().GetUserId(),
			"entity_id":   role.AccountId,
			"entity_type": security.RBAC_ENTITY_TYPE_ACCOUNT,
			"role":        role.Role,
			"group_id":    request.GroupId,
		}
	})
	_, err = manager.Db.NamedExec("INSERT INTO group_roles (created_by, entity_id, entity_type, role, group_id) VALUES (:created_by, :entity_id, :entity_type, :role, :group_id)", rolesToInsert)
	if err != nil {
		ctx.GetLogger().Error("Error inserting group role", "error", err)
		return AccountUserRoleUpsertResponse{}, common.ErrorInternal("Error updating group role")
	}
	return AccountUserRoleUpsertResponse{
		Status:  "success",
		Message: "Group role updated successfully",
	}, nil
}

func commonK8sAccountNamespaceRoleValidation(ctx *security.RequestContext, manager *database.DatabaseManager, accountRoles []AccountNamespaceRole) error {
	if !ctx.GetSecurityContext().IsTenantAdmin() {
		return common.ErrorUnauthorized("Not Allowed")
	}

	for _, role := range accountRoles {
		if role.Role != security.AUTH_K8S_NAMESPACE_ADMIN_ROLE && role.Role != security.AUTH_K8S_NAMESPACE_READ_ADMIN_ROLE {
			return common.ErrorBadRequest("Invalid role")
		}
	}

	// check if account exists for same tenant
	for _, role := range accountRoles {
		// validate account
		accountRow := manager.Db.QueryRowx("SELECT count(*) FROM cloud_accounts WHERE tenant = $1 AND id = $2", ctx.GetSecurityContext().GetTenantId(), role.AccountId)
		if accountRow.Err() != nil {
			ctx.GetLogger().Error("Error fetching account", "error", accountRow.Err())
			return common.ErrorUnauthorized("Account not found in tenant")
		}
		account := map[string]any{}
		err := accountRow.MapScan(account)
		if err != nil {
			ctx.GetLogger().Error("Error scanning account", "error", err)
			return common.ErrorUnauthorized("Account not found in tenant")
		}
		if account["count"].(int64) == 0 {
			return common.ErrorUnauthorized("Account not found in tenant")
		}

		// validate namespace
		namespaceRow := manager.Db.QueryRowx("SELECT count(*) FROM k8s_namespaces WHERE cloud_account_id = $1 AND name = $2", role.AccountId, role.Namespace)
		if namespaceRow.Err() != nil {
			ctx.GetLogger().Error("Error fetching namespace", "error", namespaceRow.Err())
			return common.ErrorUnauthorized("Namespace not found in account")
		}
		namespace := map[string]any{}
		err = namespaceRow.MapScan(namespace)
		if err != nil {
			ctx.GetLogger().Error("Error scanning namespace", "error", err)
			return common.ErrorUnauthorized("Namespace not found in account")
		}
		if namespace["count"].(int64) == 0 {
			return common.ErrorUnauthorized("Namespace not found in account")
		}
	}

	return nil
}

func UpsertK8sAccountNamespaceUserRole(ctx *security.RequestContext, request K8sAccountNamespaceUserRoleUpsertRequest) (AccountUserRoleUpsertResponse, error) {
	ctx.GetLogger().Info(`upsert user account namespace role`, "request", slog.AnyValue(request))

	manager, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		return AccountUserRoleUpsertResponse{}, err
	}

	// validate request
	err = common.ValidateStruct(request)
	if err != nil {
		return AccountUserRoleUpsertResponse{}, err
	}

	err = commonK8sAccountNamespaceRoleValidation(ctx, manager, request.K8sAccountNamespaceRoles)
	if err != nil {
		return AccountUserRoleUpsertResponse{}, err
	}

	// check if user is part of tenant
	userRow := manager.Db.QueryRowx("SELECT count(*) FROM tenant_users WHERE tenant = $1 AND \"user\" = $2", ctx.GetSecurityContext().GetTenantId(), request.UserId)
	if userRow.Err() != nil {
		ctx.GetLogger().Error("Error fetching user", "error", userRow.Err())
		return AccountUserRoleUpsertResponse{}, common.ErrorUnauthorized("User not found in tenant")
	}
	user := map[string]any{}
	err = userRow.MapScan(user)
	if err != nil {
		ctx.GetLogger().Error("Error scanning user", "error", err)
		return AccountUserRoleUpsertResponse{}, common.ErrorUnauthorized("User not found in tenant")
	}
	if user["count"].(int64) == 0 {
		return AccountUserRoleUpsertResponse{}, common.ErrorUnauthorized("User not found in tenant")
	}

	defer func() {
		ctx.GetLogger().Info("Invalidating cache for user, because namespace role is updated")
		err := ctx.GetSecurityContext().InvalidateCache()
		if err != nil {
			slog.Error("Error invalidating cache", "error", err, "user_id", request.UserId)
		}
	}()

	// if empty then remove all roles
	_, err = manager.Db.Exec("DELETE FROM user_roles WHERE entity_type = $2 AND user_id = $1", request.UserId, security.RBAC_ENTITY_TYPE_K8S_NAMESPACE)
	if err != nil {
		ctx.GetLogger().Error("Error deleting user role", "error", err)
		return AccountUserRoleUpsertResponse{}, common.ErrorInternal("Error updating user role")
	}
	if len(request.K8sAccountNamespaceRoles) == 0 {
		return AccountUserRoleUpsertResponse{
			Status:  "success",
			Message: "User role updated successfully",
		}, nil
	}

	// insert new role
	rolesToInsert := lo.Map(request.K8sAccountNamespaceRoles, func(role AccountNamespaceRole, i int) map[string]any {
		return map[string]any{
			"created_by":  ctx.GetSecurityContext().GetUserId(),
			"entity_id":   role.AccountId + ":" + role.Namespace,
			"entity_type": security.RBAC_ENTITY_TYPE_K8S_NAMESPACE,
			"role":        role.Role,
			"user_id":     request.UserId,
			"tenant_id":   ctx.GetSecurityContext().GetTenantId(),
		}
	})
	_, err = manager.Db.NamedExec("INSERT INTO user_roles (created_by, entity_id, entity_type, role, user_id, tenant_id) VALUES (:created_by, :entity_id, :entity_type, :role, :user_id, :tenant_id)", rolesToInsert)
	if err != nil {
		ctx.GetLogger().Error("Error inserting user role", "error", err)
		return AccountUserRoleUpsertResponse{}, common.ErrorInternal("Error updating user role")
	}
	for _, role := range request.K8sAccountNamespaceRoles {
		audit.LogChange(ctx, audit.ChangeInput{
			EventCategory: audit.EventCategoryRole,
			EventType:     audit.EventTypeRoleUserCreate,
			EventAction:   audit.EventActionCreate,
			TargetID:      request.UserId,
			AccountID:     role.AccountId,
			TableName:     "user_roles",
			NewData:       map[string]any{"user_id": request.UserId, "role": role.Role, "entity_type": "k8s_namespace", "entity_id": role.AccountId + ":" + role.Namespace},
		})
	}
	return AccountUserRoleUpsertResponse{
		Status:  "success",
		Message: "User role updated successfully",
	}, nil
}

func UpsertK8sAccountNamespaceGroupRole(ctx *security.RequestContext, request AccountNamespaceGroupRoleUpsertRequest) (AccountUserRoleUpsertResponse, error) {
	ctx.GetLogger().Info(`upsert group account namespace role`, "request", slog.AnyValue(request))

	manager, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		return AccountUserRoleUpsertResponse{}, err
	}

	// validate request
	err = common.ValidateStruct(request)
	if err != nil {
		return AccountUserRoleUpsertResponse{}, err
	}

	err = commonK8sAccountNamespaceRoleValidation(ctx, manager, request.K8sAccountNamespaceRoles)
	if err != nil {
		return AccountUserRoleUpsertResponse{}, err
	}

	// check if group is part of tenant
	groupRow := manager.Db.QueryRowx("SELECT count(*) FROM user_groups WHERE tenant = $1 AND id = $2", ctx.GetSecurityContext().GetTenantId(), request.GroupId)
	if groupRow.Err() != nil {
		ctx.GetLogger().Error("Error fetching group", "error", groupRow.Err())
		return AccountUserRoleUpsertResponse{}, common.ErrorUnauthorized("Group not found in tenant")
	}
	group := map[string]any{}
	err = groupRow.MapScan(group)
	if err != nil {
		ctx.GetLogger().Error("Error scanning group", "error", err)
		return AccountUserRoleUpsertResponse{}, common.ErrorUnauthorized("Group not found in tenant")
	}
	if group["count"].(int64) == 0 {
		return AccountUserRoleUpsertResponse{}, common.ErrorUnauthorized("Group not found in tenant")
	}

	defer func() {
		ctx.GetLogger().Info("Invalidating cache for tenant, because group role is updated", "group", request.GroupId)
		err := security.InvalidateCacheForTenant(ctx.GetSecurityContext().GetTenantId())
		if err != nil {
			ctx.GetLogger().Error("Error invalidating cache", "error", err)
		}
	}()

	// remove all roles
	_, err = manager.Db.Exec("DELETE FROM group_roles WHERE entity_type = $2 AND group_id = $1", request.GroupId, security.RBAC_ENTITY_TYPE_K8S_NAMESPACE)
	if err != nil {
		ctx.GetLogger().Error("Error deleting group role", "error", err)
		return AccountUserRoleUpsertResponse{}, common.ErrorInternal("Error updating group role")
	}

	// if new roles are empty, return success
	if len(request.K8sAccountNamespaceRoles) == 0 {
		return AccountUserRoleUpsertResponse{
			Status:  "success",
			Message: "Group role updated successfully",
		}, nil
	}

	// insert new role
	rolesToInsert := lo.Map(request.K8sAccountNamespaceRoles, func(role AccountNamespaceRole, i int) map[string]any {
		return map[string]any{
			"created_by":  ctx.GetSecurityContext().GetUserId(),
			"entity_id":   role.AccountId + ":" + role.Namespace,
			"entity_type": security.RBAC_ENTITY_TYPE_K8S_NAMESPACE,
			"role":        role.Role,
			"group_id":    request.GroupId,
		}
	})
	_, err = manager.Db.NamedExec("INSERT INTO group_roles (created_by, entity_id, entity_type, role, group_id) VALUES (:created_by, :entity_id, :entity_type, :role, :group_id)", rolesToInsert)
	if err != nil {
		ctx.GetLogger().Error("Error inserting group role", "error", err)
		return AccountUserRoleUpsertResponse{}, common.ErrorInternal("Error updating group role")
	}
	return AccountUserRoleUpsertResponse{
		Status:  "success",
		Message: "Group role updated successfully",
	}, nil
}

func ManageGroupUsers(ctx *security.RequestContext, request ManageGroupUsersRequest) (ManageGroupUsersResponse, error) {
	ctx.GetLogger().Info("manage group users", "request", slog.AnyValue(request))
	if !ctx.GetSecurityContext().IsTenantAdmin() {
		return ManageGroupUsersResponse{}, common.ErrorUnauthorized("Not Allowed")
	}

	err := common.ValidateStruct(request)
	if err != nil {
		return ManageGroupUsersResponse{}, err
	}

	manager, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		return ManageGroupUsersResponse{}, err
	}

	// validate group belongs to tenant
	groupRow := manager.Db.QueryRowx("SELECT count(*) FROM user_groups WHERE tenant = $1 AND id = $2", ctx.GetSecurityContext().GetTenantId(), request.GroupId)
	if groupRow.Err() != nil {
		return ManageGroupUsersResponse{}, common.ErrorUnauthorized("Group not found in tenant")
	}
	groupCount := map[string]any{}
	err = groupRow.MapScan(groupCount)
	if err != nil || groupCount["count"].(int64) == 0 {
		return ManageGroupUsersResponse{}, common.ErrorUnauthorized("Group not found in tenant")
	}

	// resolve add_usernames to user_ids
	if len(request.AddUsernames) > 0 {
		addUserIds := []string{}
		for _, username := range request.AddUsernames {
			var userId string
			err = manager.Db.Get(&userId, "SELECT id FROM users WHERE username = $1", username)
			if err != nil {
				return ManageGroupUsersResponse{}, common.ErrorBadRequest("User not found: " + username)
			}
			addUserIds = append(addUserIds, userId)
		}

		currentUserId := ctx.GetSecurityContext().GetUserId()
		userGroupObjs := lo.Map(addUserIds, func(userId string, _ int) map[string]any {
			return map[string]any{
				"group_id":   request.GroupId,
				"user_id":    userId,
				"created_by": currentUserId,
				"updated_by": currentUserId,
			}
		})
		_, err = manager.Db.NamedExec("INSERT INTO usergroup_users (\"group\", \"user\", \"created_by\", \"updated_by\") VALUES (:group_id, :user_id, :created_by, :updated_by) ON CONFLICT (\"user\", \"group\") DO NOTHING", userGroupObjs)
		if err != nil {
			ctx.GetLogger().Error("Error inserting user group users", "error", err)
			return ManageGroupUsersResponse{}, common.ErrorInternal("Error adding users to group")
		}
		for _, userId := range addUserIds {
			// Invalidate security cache for affected users (replaces pre_audit_hook)
			if err := security.InvalidateCacheForUser(userId); err != nil {
				ctx.GetLogger().Error("error invalidating cache for user", "error", err, "user_id", userId)
			}
			audit.LogChange(ctx, audit.ChangeInput{
				EventCategory: audit.EventCategoryGroup,
				EventType:     audit.EventTypeGroupUserCreate,
				EventAction:   audit.EventActionCreate,
				TargetID:      userId,
				TableName:     "usergroup_users",
				NewData:       map[string]any{"group": request.GroupId, "user": userId},
			})
		}
	}

	// resolve remove_usernames to user_ids
	if len(request.RemoveUsernames) > 0 {
		removeUserIds := []string{}
		for _, username := range request.RemoveUsernames {
			var userId string
			err = manager.Db.Get(&userId, "SELECT id FROM users WHERE username = $1", username)
			if err != nil {
				return ManageGroupUsersResponse{}, common.ErrorBadRequest("User not found: " + username)
			}
			removeUserIds = append(removeUserIds, userId)
		}

		query, args, err := sqlx.In("DELETE FROM usergroup_users WHERE \"group\" = ? AND \"user\" IN (?)", request.GroupId, removeUserIds)
		if err != nil {
			ctx.GetLogger().Error("Error building delete query", "error", err)
			return ManageGroupUsersResponse{}, common.ErrorInternal("Error removing users from group")
		}
		query = manager.Db.Rebind(query)
		_, err = manager.Db.Exec(query, args...)
		if err != nil {
			ctx.GetLogger().Error("Error deleting user group users", "error", err)
			return ManageGroupUsersResponse{}, common.ErrorInternal("Error removing users from group")
		}
		for _, userId := range removeUserIds {
			if err := security.InvalidateCacheForUser(userId); err != nil {
				ctx.GetLogger().Error("error invalidating cache for user", "error", err, "user_id", userId)
			}
			audit.LogChange(ctx, audit.ChangeInput{
				EventCategory: audit.EventCategoryGroup,
				EventType:     audit.EventTypeGroupUserDelete,
				EventAction:   audit.EventActionDelete,
				TargetID:      userId,
				TableName:     "usergroup_users",
				OldData:       map[string]any{"group": request.GroupId, "user": userId},
			})
		}
	}

	return ManageGroupUsersResponse{
		Status:  "success",
		Message: "Group users updated successfully",
	}, nil
}

func UpsertTenantAttributes(ctx *security.RequestContext, request TenantAttributeUpsertRequest) ([]models.TenantAttributes, error) {
	if !ctx.GetSecurityContext().IsTenantAdmin() {
		return nil, common.ErrorUnauthorized("Not Allowed")
	}
	tenantId := ctx.GetSecurityContext().GetTenantId()
	for i := range request.Object {
		request.Object[i].TenantId = tenantId
	}
	// validate request
	err := common.ValidateStruct(request)
	if err != nil {
		return nil, err
	}

	dbms, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		return nil, err
	}

	tx, err := dbms.Db.Beginx()
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()

	for _, attr := range request.Object {
		_, err = tx.Exec(
			`INSERT INTO tenant_attrs (name, value, tenant_id) VALUES ($1, $2, $3)
			ON CONFLICT ON CONSTRAINT tenant_attrs_tenant_id_name_key DO UPDATE SET value = EXCLUDED.value, updated_at = now()`,
			attr.Name, attr.Value, attr.TenantId,
		)
		if err != nil {
			return nil, err
		}
	}

	if err = tx.Commit(); err != nil {
		return nil, err
	}
	return GetTenantAttributes(ctx)
}

func GetTenantAttributesByName(context *security.RequestContext, name string) ([]models.TenantAttributes, error) {
	manager, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		return []models.TenantAttributes{}, err
	}

	var tenantAttributes []models.TenantAttributes
	err = manager.Db.Select(&tenantAttributes, "SELECT * FROM tenant_attrs WHERE tenant_id = $1 AND name = $2", context.GetSecurityContext().GetTenantId(), name)
	if err != nil {
		return []models.TenantAttributes{}, err
	}

	return tenantAttributes, nil
}

// GetTenantAttributesByNames fetches multiple tenant attributes for a specific tenant in a single query.
// Returns a map keyed by attribute name for O(1) lookup. This avoids N+1 query
// patterns when loading multiple config attributes (e.g., 8 queries → 1).
func GetTenantAttributesByNames(ctx *security.RequestContext, tenantId string, names []string) (map[string]models.TenantAttributes, error) {
	result := make(map[string]models.TenantAttributes, len(names))
	if len(names) == 0 {
		return result, nil
	}

	manager, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		return result, err
	}

	query, args, err := sqlx.In("SELECT * FROM tenant_attrs WHERE tenant_id = ? AND name IN (?)", tenantId, names)
	if err != nil {
		return result, err
	}
	query = manager.Db.Rebind(query)

	var attrs []models.TenantAttributes
	if err := manager.Db.Select(&attrs, query, args...); err != nil {
		return result, err
	}

	for _, a := range attrs {
		result[a.Name] = a
	}
	return result, nil
}

// GetTenantAttributeValueByTenantId queries tenant_attrs directly by tenant ID string and attribute name.
// Returns the value string and true if found, or empty string and false if not found.
func GetTenantAttributeValueByTenantId(ctx *security.RequestContext, tenantId string, name string) (string, bool, error) {
	manager, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		return "", false, err
	}

	var value string
	err = manager.Db.Get(&value, "SELECT value FROM tenant_attrs WHERE tenant_id = $1::uuid AND name = $2", tenantId, name)
	if err != nil {
		if err.Error() == "sql: no rows in result set" {
			return "", false, nil
		}
		return "", false, err
	}

	return value, true, nil
}

func UpdateTenantName(ctx *security.RequestContext, request TenantUpdateNameRequest) (TenantUpdateNameResponse, error) {
	if !ctx.GetSecurityContext().IsTenantAdmin() {
		return TenantUpdateNameResponse{}, common.ErrorUnauthorized("Not Allowed")
	}
	err := common.ValidateStruct(request)
	if err != nil {
		return TenantUpdateNameResponse{}, err
	}

	manager, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		return TenantUpdateNameResponse{}, err
	}

	result, err := manager.Db.Exec("UPDATE tenant SET name = $1 WHERE id = $2", request.Name, ctx.GetSecurityContext().GetTenantId())
	if err != nil {
		ctx.GetLogger().Error("Error updating tenant name", "error", err)
		return TenantUpdateNameResponse{}, common.ErrorInternal("Error updating tenant name")
	}

	affected, _ := result.RowsAffected()
	if affected == 0 {
		return TenantUpdateNameResponse{}, common.ErrorBadRequest("Tenant not found")
	}

	audit.LogChange(ctx, audit.ChangeInput{
		EventCategory: audit.EventCategoryTenant,
		EventType:     audit.EventTypeTenantUpdate,
		EventAction:   audit.EventActionUpdate,
		TargetID:      ctx.GetSecurityContext().GetTenantId(),
		TableName:     "tenant",
		NewData:       map[string]any{"id": ctx.GetSecurityContext().GetTenantId(), "name": request.Name},
	})

	return TenantUpdateNameResponse{
		Status:  "success",
		Message: "Tenant name updated successfully",
	}, nil
}

func DeleteTenantAttributes(ctx *security.RequestContext, request TenantAttributeDeleteRequest) (TenantAttributeDeleteResponse, error) {
	if !ctx.GetSecurityContext().IsTenantAdmin() {
		return TenantAttributeDeleteResponse{}, common.ErrorUnauthorized("Not Allowed")
	}
	err := common.ValidateStruct(request)
	if err != nil {
		return TenantAttributeDeleteResponse{}, err
	}

	manager, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		return TenantAttributeDeleteResponse{}, err
	}

	query, args, err := sqlx.In("DELETE FROM tenant_attrs WHERE tenant_id = ? AND name IN (?)", ctx.GetSecurityContext().GetTenantId(), request.Names)
	if err != nil {
		return TenantAttributeDeleteResponse{}, common.ErrorInternal("Error building query")
	}
	query = manager.Db.Rebind(query)

	result, err := manager.Db.Exec(query, args...)
	if err != nil {
		ctx.GetLogger().Error("Error deleting tenant attributes", "error", err)
		return TenantAttributeDeleteResponse{}, common.ErrorInternal("Error deleting tenant attributes")
	}

	affected, _ := result.RowsAffected()
	return TenantAttributeDeleteResponse{
		Status:       "success",
		AffectedRows: affected,
	}, nil
}

func UpsertFeatureFlags(ctx *security.RequestContext, request FeatureFlagUpsertRequest) (FeatureFlagUpsertResponse, error) {
	if !ctx.GetSecurityContext().IsTenantAdmin() {
		return FeatureFlagUpsertResponse{}, common.ErrorUnauthorized("Not Allowed")
	}
	err := common.ValidateStruct(request)
	if err != nil {
		return FeatureFlagUpsertResponse{}, err
	}

	manager, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		return FeatureFlagUpsertResponse{}, err
	}

	tenantId := ctx.GetSecurityContext().GetTenantId()

	tx, err := manager.Db.Beginx()
	if err != nil {
		return FeatureFlagUpsertResponse{}, common.ErrorInternal("Error starting transaction")
	}
	defer func() { _ = tx.Rollback() }()

	for _, f := range request.Features {
		if f.AccountId != "" {
			// Account-level feature flag
			_, err = tx.Exec(
				`INSERT INTO feature_flag (feature_id, status, tenant_id, account_id)
				 VALUES ($1, $2, $3, $4)
				 ON CONFLICT (feature_id, tenant_id, account_id) DO UPDATE SET status = $2`,
				f.FeatureId, f.Status, tenantId, f.AccountId,
			)
			if err != nil {
				ctx.GetLogger().Error("Error upserting account feature flag", "error", err)
				return FeatureFlagUpsertResponse{}, common.ErrorInternal("Error upserting account feature flag")
			}
		} else {
			// Tenant-level feature flag
			result, err := tx.Exec(
				"UPDATE feature_flag SET status = $1 WHERE feature_id = $2 AND tenant_id = $3 AND account_id IS NULL",
				f.Status, f.FeatureId, tenantId,
			)
			if err != nil {
				ctx.GetLogger().Error("Error updating feature flag", "error", err)
				return FeatureFlagUpsertResponse{}, common.ErrorInternal("Error updating feature flag")
			}

			affected, _ := result.RowsAffected()
			if affected == 0 {
				_, err = tx.Exec(
					"INSERT INTO feature_flag (feature_id, status, tenant_id) VALUES ($1, $2, $3)",
					f.FeatureId, f.Status, tenantId,
				)
				if err != nil {
					ctx.GetLogger().Error("Error inserting feature flag", "error", err)
					return FeatureFlagUpsertResponse{}, common.ErrorInternal("Error inserting feature flag")
				}
			}
		}
	}

	if err = tx.Commit(); err != nil {
		ctx.GetLogger().Error("Error committing feature flag transaction", "error", err)
		return FeatureFlagUpsertResponse{}, common.ErrorInternal("Error committing feature flag transaction")
	}

	// Invalidate cached feature flag entries so changes take effect immediately
	for _, f := range request.Features {
		_ = featureFlagCache.Delete(context.Background(), f.FeatureId+":"+tenantId)
		_ = featureFlagCache.Delete(context.Background(), f.FeatureId+":default:"+tenantId)
	}

	return FeatureFlagUpsertResponse{
		Status:  "success",
		Message: "Feature flags updated successfully",
	}, nil
}

func CreateUserGroup(ctx *security.RequestContext, request UserGroupCreateRequest) (UserGroupCreateResponse, error) {
	if !ctx.GetSecurityContext().IsTenantAdmin() {
		return UserGroupCreateResponse{}, common.ErrorUnauthorized("Not Allowed")
	}
	err := common.ValidateStruct(request)
	if err != nil {
		return UserGroupCreateResponse{}, err
	}

	manager, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		return UserGroupCreateResponse{}, err
	}

	var id string
	err = manager.Db.QueryRowx(
		"INSERT INTO user_groups (name, description, tenant, owner) VALUES ($1, $2, $3, $4) RETURNING id",
		request.Name, request.Description, ctx.GetSecurityContext().GetTenantId(), ctx.GetSecurityContext().GetUserId(),
	).Scan(&id)
	if err != nil {
		ctx.GetLogger().Error("Error creating user group", "error", err)
		return UserGroupCreateResponse{}, common.ErrorInternal("Error creating user group")
	}

	audit.LogChange(ctx, audit.ChangeInput{
		EventCategory: audit.EventCategoryGroup,
		EventType:     audit.EventTypeGroupCreate,
		EventAction:   audit.EventActionCreate,
		TargetID:      id,
		TableName:     "user_groups",
		NewData:       map[string]any{"id": id, "name": request.Name, "description": request.Description, "owner": ctx.GetSecurityContext().GetUserId()},
	})

	return UserGroupCreateResponse{
		Id: id,
	}, nil
}

func UpdateUserGroup(ctx *security.RequestContext, request UserGroupUpdateRequest) (UserGroupUpdateResponse, error) {
	if !ctx.GetSecurityContext().IsTenantAdmin() {
		return UserGroupUpdateResponse{}, common.ErrorUnauthorized("Not Allowed")
	}
	err := common.ValidateStruct(request)
	if err != nil {
		return UserGroupUpdateResponse{}, err
	}

	manager, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		return UserGroupUpdateResponse{}, err
	}

	// Validate group belongs to tenant
	var count int64
	err = manager.Db.Get(&count, "SELECT count(*) FROM user_groups WHERE tenant = $1 AND id = $2", ctx.GetSecurityContext().GetTenantId(), request.Id)
	if err != nil || count == 0 {
		return UserGroupUpdateResponse{}, common.ErrorBadRequest("Group not found in tenant")
	}

	// Update name and description
	_, err = manager.Db.Exec(
		"UPDATE user_groups SET name = $1, description = $2 WHERE id = $3",
		request.Name, request.Description, request.Id,
	)
	if err != nil {
		ctx.GetLogger().Error("Error updating user group", "error", err)
		return UserGroupUpdateResponse{}, common.ErrorInternal("Error updating user group")
	}

	audit.LogChange(ctx, audit.ChangeInput{
		EventCategory: audit.EventCategoryGroup,
		EventType:     audit.EventTypeGroupUpdate,
		EventAction:   audit.EventActionUpdate,
		TargetID:      request.Id,
		TableName:     "user_groups",
		NewData:       map[string]any{"id": request.Id, "name": request.Name, "description": request.Description},
	})

	// Update role if provided
	if request.Role != "" {
		_, err = UpsertTenantGroupRole(ctx, TenantGroupRoleUpsertRequest{
			GroupId: request.Id,
			Role:    request.Role,
		})
		if err != nil {
			ctx.GetLogger().Error("Error updating group role", "error", err)
			return UserGroupUpdateResponse{}, common.ErrorInternal("Error updating group role")
		}
	}

	return UserGroupUpdateResponse{
		Status:  "success",
		Message: "User group updated successfully",
	}, nil
}

func UpdateIntegrationStatusByPk(ctx *security.RequestContext, request IntegrationUpdateStatusByPkRequest) (IntegrationUpdateStatusByPkResponse, error) {
	if !ctx.GetSecurityContext().IsTenantAdmin() {
		return IntegrationUpdateStatusByPkResponse{}, common.ErrorUnauthorized("Not Allowed")
	}
	err := common.ValidateStruct(request)
	if err != nil {
		return IntegrationUpdateStatusByPkResponse{}, err
	}

	manager, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		return IntegrationUpdateStatusByPkResponse{}, err
	}

	// Verify integration belongs to tenant
	var count int64
	err = manager.Db.Get(&count, "SELECT count(*) FROM integrations WHERE tenant_id = $1 AND id = $2", ctx.GetSecurityContext().GetTenantId(), request.Id)
	if err != nil || count == 0 {
		return IntegrationUpdateStatusByPkResponse{}, common.ErrorBadRequest("Integration not found")
	}

	_, err = manager.Db.Exec("UPDATE integrations SET status = $1 WHERE id = $2", request.Status, request.Id)
	if err != nil {
		ctx.GetLogger().Error("Error updating integration status", "error", err)
		return IntegrationUpdateStatusByPkResponse{}, common.ErrorInternal("Error updating integration status")
	}

	return IntegrationUpdateStatusByPkResponse{Id: request.Id}, nil
}

func DeleteNotificationRule(ctx *security.RequestContext, request NotificationRuleDeleteRequest) (NotificationRuleDeleteResponse, error) {
	if !ctx.GetSecurityContext().IsTenantAdmin() {
		return NotificationRuleDeleteResponse{}, common.ErrorUnauthorized("Not Allowed")
	}
	err := common.ValidateStruct(request)
	if err != nil {
		return NotificationRuleDeleteResponse{}, err
	}

	manager, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		return NotificationRuleDeleteResponse{}, err
	}

	result, err := manager.Db.Exec("DELETE FROM notification_rules WHERE id = $1 AND tenant_id = $2", request.Id, ctx.GetSecurityContext().GetTenantId())
	if err != nil {
		ctx.GetLogger().Error("Error deleting notification rule", "error", err)
		return NotificationRuleDeleteResponse{}, common.ErrorInternal("Error deleting notification rule")
	}

	affected, _ := result.RowsAffected()
	if affected == 0 {
		return NotificationRuleDeleteResponse{}, common.ErrorBadRequest("Notification rule not found")
	}

	return NotificationRuleDeleteResponse(request), nil
}

func CreateNotificationChannelMapping(ctx *security.RequestContext, request NotificationChannelMappingCreateRequest) (NotificationChannelMappingCreateResponse, error) {
	if !ctx.GetSecurityContext().IsTenantAdmin() {
		return NotificationChannelMappingCreateResponse{}, common.ErrorUnauthorized("Not Allowed")
	}
	err := common.ValidateStruct(request)
	if err != nil {
		return NotificationChannelMappingCreateResponse{}, err
	}

	manager, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		return NotificationChannelMappingCreateResponse{}, err
	}

	var resp NotificationChannelMappingCreateResponse
	err = manager.Db.QueryRowx(
		`INSERT INTO notification_channel_account_mappings (account_id, platform, team_id, channel_id, created_by, tenant_id)
		 VALUES ($1, $2, $3, $4, $5, $6)
		 RETURNING id, account_id, platform, team_id, channel_id, created_by, created_at`,
		request.AccountId, request.Platform, request.TeamId, request.ChannelId,
		ctx.GetSecurityContext().GetUserId(), ctx.GetSecurityContext().GetTenantId(),
	).StructScan(&resp)
	if err != nil {
		ctx.GetLogger().Error("Error creating notification channel mapping", "error", err)
		return NotificationChannelMappingCreateResponse{}, common.ErrorInternal("Error creating notification channel mapping")
	}

	return resp, nil
}

func DeleteNotificationChannelMapping(ctx *security.RequestContext, request NotificationChannelMappingDeleteRequest) (NotificationChannelMappingDeleteResponse, error) {
	if !ctx.GetSecurityContext().IsTenantAdmin() {
		return NotificationChannelMappingDeleteResponse{}, common.ErrorUnauthorized("Not Allowed")
	}
	err := common.ValidateStruct(request)
	if err != nil {
		return NotificationChannelMappingDeleteResponse{}, err
	}

	manager, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		return NotificationChannelMappingDeleteResponse{}, err
	}

	result, err := manager.Db.Exec(
		"DELETE FROM notification_channel_account_mappings WHERE id = $1 AND tenant_id = $2",
		request.Id, ctx.GetSecurityContext().GetTenantId(),
	)
	if err != nil {
		ctx.GetLogger().Error("Error deleting notification channel mapping", "error", err)
		return NotificationChannelMappingDeleteResponse{}, common.ErrorInternal("Error deleting notification channel mapping")
	}

	affected, _ := result.RowsAffected()
	if affected == 0 {
		return NotificationChannelMappingDeleteResponse{}, common.ErrorBadRequest("Notification channel mapping not found")
	}

	return NotificationChannelMappingDeleteResponse(request), nil
}

func UpdateNotificationChannelMapping(ctx *security.RequestContext, request NotificationChannelMappingUpdateRequest) (NotificationChannelMappingUpdateResponse, error) {
	if !ctx.GetSecurityContext().IsTenantAdmin() {
		return NotificationChannelMappingUpdateResponse{}, common.ErrorUnauthorized("Not Allowed")
	}
	err := common.ValidateStruct(request)
	if err != nil {
		return NotificationChannelMappingUpdateResponse{}, err
	}

	manager, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		return NotificationChannelMappingUpdateResponse{}, err
	}

	var resp NotificationChannelMappingUpdateResponse
	err = manager.Db.QueryRowx(
		`UPDATE notification_channel_account_mappings
		 SET account_id = $1, team_id = $2, channel_id = $3
		 WHERE id = $4 AND tenant_id = $5
		 RETURNING id, account_id, team_id, channel_id`,
		request.AccountId, request.TeamId, request.ChannelId,
		request.Id, ctx.GetSecurityContext().GetTenantId(),
	).StructScan(&resp)
	if err != nil {
		ctx.GetLogger().Error("Error updating notification channel mapping", "error", err)
		return NotificationChannelMappingUpdateResponse{}, common.ErrorInternal("Error updating notification channel mapping")
	}

	return resp, nil
}

func DeleteFeature(context *security.RequestContext, request DeleteFeatureRequest) (map[string]string, error) {
	manager, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		return map[string]string{
			"status": "error",
		}, err
	}
	tx, err := manager.Db.Begin()
	if err != nil {
		return map[string]string{
			"status": "error",
		}, err
	}
	defer func() { _ = tx.Rollback() }() // Defer a rollback in case anything fails.

	_, err = tx.Exec("DELETE from feature_flag WHERE feature_id = $1 and tenant_id = $2;", request.Name, context.GetSecurityContext().GetTenantId())
	if err != nil {
		context.GetLogger().Error("Error deleting feature_flag", "error", err)
		return map[string]string{
			"status": "error",
		}, err
	}

	if request.DeleteFeature {
		_, err = tx.Exec("DELETE from feature WHERE value = $1;", request.Name)
		if err != nil {
			context.GetLogger().Error("Error deleting feature", "error", err)
			return map[string]string{
				"status": "error",
			}, err
		}
	}

	if err = tx.Commit(); err != nil {
		context.GetLogger().Error("tenant: error committing transaction", "error", err)
		return map[string]string{
			"status": "error",
		}, err
	}

	return map[string]string{
		"status": "success",
	}, nil
}

// Priority 3: Application Group operations

func CreateApplicationGroup(ctx *security.RequestContext, request ApplicationGroupCreateRequest) (ApplicationGroupCreateResponse, error) {
	err := common.ValidateStruct(request)
	if err != nil {
		return ApplicationGroupCreateResponse{}, err
	}

	if !ctx.GetSecurityContext().IsTenantAdmin() {
		return ApplicationGroupCreateResponse{}, common.ErrorUnauthorized("Not Allowed")
	}

	manager, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		return ApplicationGroupCreateResponse{}, err
	}

	tenantId := ctx.GetSecurityContext().GetTenantId()
	userId := ctx.GetSecurityContext().GetUserId()

	tx, err := manager.Db.Beginx()
	if err != nil {
		return ApplicationGroupCreateResponse{}, common.ErrorInternal("Error starting transaction")
	}
	defer func() { _ = tx.Rollback() }()

	// Insert application group
	var groupId string
	err = tx.QueryRowx(
		`INSERT INTO application_group (name, description, tenant_id, created_by, updated_by, created_at, updated_at) VALUES ($1, $2, $3, $4, $4, now(), now()) RETURNING id`,
		request.Name, request.Description, tenantId, userId,
	).Scan(&groupId)
	if err != nil {
		ctx.GetLogger().Error("Error creating application group", "error", err)
		return ApplicationGroupCreateResponse{}, common.ErrorInternal("Error creating application group")
	}

	// Insert mappings
	for _, m := range request.Mappings {
		_, err = tx.Exec(
			`INSERT INTO application_group_mapping (group_id, namespace_name, workload_name, workload_kind, account_id, cloud_resource_id, tenant_id) VALUES ($1, $2, $3, $4, $5, $6, $7)`,
			groupId, m.NamespaceName, m.WorkloadName, m.WorkloadKind, m.AccountId, m.CloudResourceId, tenantId,
		)
		if err != nil {
			ctx.GetLogger().Error("Error inserting application group mapping", "error", err)
			return ApplicationGroupCreateResponse{}, common.ErrorInternal("Error inserting application group mapping")
		}
	}

	if err = tx.Commit(); err != nil {
		ctx.GetLogger().Error("Error committing transaction", "error", err)
		return ApplicationGroupCreateResponse{}, common.ErrorInternal("Error committing transaction")
	}

	return ApplicationGroupCreateResponse{Id: groupId}, nil
}

func UpdateApplicationGroup(ctx *security.RequestContext, request ApplicationGroupUpdateRequest) (ApplicationGroupUpdateResponse, error) {
	err := common.ValidateStruct(request)
	if err != nil {
		return ApplicationGroupUpdateResponse{}, err
	}

	if !ctx.GetSecurityContext().IsTenantAdmin() {
		return ApplicationGroupUpdateResponse{}, common.ErrorUnauthorized("Not Allowed")
	}

	manager, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		return ApplicationGroupUpdateResponse{}, err
	}

	tenantId := ctx.GetSecurityContext().GetTenantId()
	userId := ctx.GetSecurityContext().GetUserId()

	tx, err := manager.Db.Beginx()
	if err != nil {
		return ApplicationGroupUpdateResponse{}, common.ErrorInternal("Error starting transaction")
	}
	defer func() { _ = tx.Rollback() }()

	// Update application group
	_, err = tx.Exec(
		`UPDATE application_group SET name = $1, description = $2, updated_by = $3, updated_at = now() WHERE id = $4 AND tenant_id = $5`,
		request.Name, request.Description, userId, request.Id, tenantId,
	)
	if err != nil {
		ctx.GetLogger().Error("Error updating application group", "error", err)
		return ApplicationGroupUpdateResponse{}, common.ErrorInternal("Error updating application group")
	}

	// Delete existing mappings
	_, err = tx.Exec(`DELETE FROM application_group_mapping WHERE group_id = $1`, request.Id)
	if err != nil {
		ctx.GetLogger().Error("Error deleting old application group mappings", "error", err)
		return ApplicationGroupUpdateResponse{}, common.ErrorInternal("Error deleting old application group mappings")
	}

	// Insert new mappings
	for _, m := range request.Mappings {
		_, err = tx.Exec(
			`INSERT INTO application_group_mapping (group_id, namespace_name, workload_name, workload_kind, account_id, cloud_resource_id, tenant_id) VALUES ($1, $2, $3, $4, $5, $6, $7)`,
			request.Id, m.NamespaceName, m.WorkloadName, m.WorkloadKind, m.AccountId, m.CloudResourceId, tenantId,
		)
		if err != nil {
			ctx.GetLogger().Error("Error inserting application group mapping", "error", err)
			return ApplicationGroupUpdateResponse{}, common.ErrorInternal("Error inserting application group mapping")
		}
	}

	if err = tx.Commit(); err != nil {
		ctx.GetLogger().Error("Error committing transaction", "error", err)
		return ApplicationGroupUpdateResponse{}, common.ErrorInternal("Error committing transaction")
	}

	return ApplicationGroupUpdateResponse{Id: request.Id}, nil
}

// Priority 3: Cloud Resource Attributes operations

func UpsertCloudResourceAttributes(ctx *security.RequestContext, request CloudResourceAttributesUpsertRequest) (CloudResourceAttributesUpsertResponse, error) {
	err := common.ValidateStruct(request)
	if err != nil {
		return CloudResourceAttributesUpsertResponse{}, err
	}

	if !ctx.GetSecurityContext().IsTenantAdmin() {
		return CloudResourceAttributesUpsertResponse{}, common.ErrorUnauthorized("Not Allowed")
	}

	manager, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		return CloudResourceAttributesUpsertResponse{}, err
	}

	tenantId := ctx.GetSecurityContext().GetTenantId()
	affectedRows := 0

	for _, obj := range request.Objects {
		result, err := manager.Db.Exec(
			`INSERT INTO cloud_resource_attributes (resource_id, account_id, name, value, labels, tenant_id)
			 VALUES ($1, $2, $3, $4, $5::jsonb, $6)
			 ON CONFLICT ON CONSTRAINT cloudresourceattributes_resourceid_source
			 DO UPDATE SET value = $4, last_seen_at = now()`,
			obj.ResourceId, obj.AccountId, obj.Name, obj.Value, obj.Labels, tenantId,
		)
		if err != nil {
			ctx.GetLogger().Error("Error upserting cloud resource attribute", "error", err, "name", obj.Name)
			return CloudResourceAttributesUpsertResponse{}, common.ErrorInternal("Error upserting cloud resource attribute")
		}
		rows, _ := result.RowsAffected()
		affectedRows += int(rows)
	}

	return CloudResourceAttributesUpsertResponse{AffectedRows: affectedRows}, nil
}

// Priority 4: Tenant Onboarding operations (used during signup, no user auth required)

func DeleteTenantOnboardingByUsername(ctx *security.RequestContext, request TenantOnboardingDeleteByUsernameRequest) (TenantOnboardingDeleteByUsernameResponse, error) {
	err := common.ValidateStruct(request)
	if err != nil {
		return TenantOnboardingDeleteByUsernameResponse{}, err
	}

	manager, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		return TenantOnboardingDeleteByUsernameResponse{}, err
	}

	result, err := manager.Db.Exec(`DELETE FROM tenant_onboarding WHERE username = $1`, request.Username)
	if err != nil {
		ctx.GetLogger().Error("Error deleting tenant onboarding records", "error", err)
		return TenantOnboardingDeleteByUsernameResponse{}, common.ErrorInternal("Error deleting tenant onboarding records")
	}

	rows, _ := result.RowsAffected()
	return TenantOnboardingDeleteByUsernameResponse{AffectedRows: int(rows)}, nil
}

func InsertTenantOnboarding(ctx *security.RequestContext, request TenantOnboardingInsertRequest) (TenantOnboardingInsertResponse, error) {
	err := common.ValidateStruct(request)
	if err != nil {
		return TenantOnboardingInsertResponse{}, err
	}

	manager, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		return TenantOnboardingInsertResponse{}, err
	}

	var id string
	err = manager.Db.QueryRowx(
		`INSERT INTO tenant_onboarding (username, verification_token, verification_token_expiration, tenant_name, user_displayname) VALUES ($1, $2, $3, $4, $5) RETURNING id`,
		request.Username, request.VerificationToken, request.VerificationTokenExpiration, request.TenantName, request.UserDisplayname,
	).Scan(&id)
	if err != nil {
		ctx.GetLogger().Error("Error inserting tenant onboarding record", "error", err)
		return TenantOnboardingInsertResponse{}, common.ErrorInternal("Error inserting tenant onboarding record")
	}

	return TenantOnboardingInsertResponse{Id: id}, nil
}

func UpdateTenantOnboardingStatus(ctx *security.RequestContext, request TenantOnboardingUpdateStatusRequest) (TenantOnboardingUpdateStatusResponse, error) {
	err := common.ValidateStruct(request)
	if err != nil {
		return TenantOnboardingUpdateStatusResponse{}, err
	}

	manager, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		return TenantOnboardingUpdateStatusResponse{}, err
	}

	_, err = manager.Db.Exec(
		`UPDATE tenant_onboarding SET verification_status = $1, updated_at = $2 WHERE id = $3`,
		request.Status, request.UpdatedAt, request.Id,
	)
	if err != nil {
		ctx.GetLogger().Error("Error updating tenant onboarding status", "error", err)
		return TenantOnboardingUpdateStatusResponse{}, common.ErrorInternal("Error updating tenant onboarding status")
	}

	return TenantOnboardingUpdateStatusResponse{Id: request.Id}, nil
}

func GetTenantOnboardingByToken(ctx *security.RequestContext, request TenantOnboardingGetByTokenRequest) ([]TenantOnboardingRecord, error) {
	err := common.ValidateStruct(request)
	if err != nil {
		return nil, err
	}

	manager, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		return nil, err
	}

	var records []TenantOnboardingRecord
	err = manager.Db.Select(&records,
		`SELECT id, verification_status, verification_token_expiration, username, tenant_name, user_displayname FROM tenant_onboarding WHERE verification_token = $1`,
		request.Token,
	)
	if err != nil {
		ctx.GetLogger().Error("Error querying tenant onboarding by token", "error", err)
		return nil, common.ErrorInternal("Error querying tenant onboarding records")
	}

	return records, nil
}

type tenantTableDetails struct {
	name           string
	tenantColumn   string
	whereSubclause string
}

func deleteTenantTables(ctx *security.RequestContext, tx *sql.Tx, tables []tenantTableDetails, tenantId string) error {
	for _, table := range tables {
		// Check if table exists before attempting delete
		var exists bool
		err := tx.QueryRow("SELECT EXISTS (SELECT 1 FROM information_schema.tables WHERE table_name = $1)", table.name).Scan(&exists)
		if err != nil {
			ctx.GetLogger().Error("Error checking table existence", "table", table.name, "error", err)
			return err
		}
		if !exists {
			ctx.GetLogger().Info("Skipping non-existent table", "table", table.name)
			continue
		}

		ctx.GetLogger().Info("Deleting tenant table", "table", table.name, "tenant_id", tenantId)
		if table.whereSubclause != "" {
			_, err = tx.Exec(fmt.Sprintf("DELETE FROM %s WHERE %s", table.name, table.whereSubclause), tenantId)
		} else {
			col := table.tenantColumn
			if col == "" {
				col = "tenant_id"
			}
			_, err = tx.Exec(fmt.Sprintf("DELETE FROM %s WHERE %s = $1", table.name, col), tenantId)
		}
		if err != nil {
			ctx.GetLogger().Error("Error deleting tenant table", "table", table.name, "error", err)
			return err
		}
	}
	return nil
}

// GetAccountIdsForTenant returns all cloud_account IDs belonging to a tenant.
func GetAccountIdsForTenant(ctx *security.RequestContext, tenantId string) ([]string, error) {
	manager, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		return nil, err
	}
	var accountIds []string
	err = manager.Db.Select(&accountIds, "SELECT id FROM cloud_accounts WHERE tenant = $1", tenantId)
	if err != nil {
		return nil, err
	}
	return accountIds, nil
}

// DeleteTenant deletes tenant-specific data (tables with tenant/tenant_id columns)
// and the tenant row itself. Account deletion must be done separately before calling this.
func DeleteTenant(ctx *security.RequestContext, request TenantDeleteRequest) (TenantDeleteResponse, error) {
	ctx.GetLogger().Info("deleting tenant", "tenant_id", request.Id)

	if !ctx.GetSecurityContext().IsSuperAdmin() {
		return TenantDeleteResponse{}, common.ErrorUnauthorized("Not Allowed")
	}

	err := common.ValidateStruct(request)
	if err != nil {
		return TenantDeleteResponse{}, err
	}

	// verify tenant exists
	tenantRecord, err := GetTenant(ctx, request.Id)
	if err != nil {
		return TenantDeleteResponse{}, fmt.Errorf("tenant not found: %s", request.Id)
	}

	manager, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		return TenantDeleteResponse{}, err
	}

	tx, err := manager.Db.Begin()
	if err != nil {
		return TenantDeleteResponse{}, err
	}
	defer func() { _ = tx.Rollback() }()

	// Delete from tenant-specific tables in dependency order
	// All tables with FK constraints to the tenant table must be deleted before tenant itself
	tenantTables := []tenantTableDetails{
		// auto pilot
		{name: "auto_pilot_approvals"},
		{name: "auto_pilot_reviewee"},
		{name: "auto_pilot_reviewers"},
		{name: "auto_pilot_task"},
		{name: "auto_pilot_approval_policy"},
		{name: "auto_pilot"},
		{name: "auto_playbook_executions"},
		{name: "auto_playbook_task"},
		{name: "auto_playbook"},
		// billing
		{name: "billing_subscriptions"},
		{name: "billing_usage_cost"},
		{name: "billing_usage_events"},
		{name: "billing_usage"},
		{name: "billing"},
		// cloud resources
		{name: "cloud_resource_attributes"},
		{name: "cloud_resource_metrics"},
		{name: "cloud_resource_relationships"},
		{name: "cloud_resourses", tenantColumn: "tenant"},
		{name: "cloud_accounts", tenantColumn: "tenant"},
		{name: "cloud_account_onboarding_errors"},
		{name: "active_resources"},
		{name: "auto_optimize_resource_map"},
		// configuration
		{name: "configuration_store"},
		{name: "jira_configurations", tenantColumn: "tenant"},
		{name: "ms_teams_channels"},
		{name: "messaging_platforms"},
		{name: "integration_config_values", whereSubclause: "integration_id IN (SELECT id FROM integrations WHERE tenant_id = $1)"},
		{name: "integrations_cloud_accounts", whereSubclause: "integration_id IN (SELECT id FROM integrations WHERE tenant_id = $1)"},
		{name: "integrations"},
		// data warehouse
		{name: "dw_pipe_usage"},
		{name: "dw_pipe"},
		{name: "dw_queries"},
		{name: "dw_tables"},
		// events/notifications
		{name: "event_incoming_webhooks"},
		{name: "events", tenantColumn: "tenant"},
		{name: "event_history"},
		{name: "sent_notifications"},
		{name: "notification_channel_account_mappings"},
		{name: "notification_rule_mappings"},
		{name: "notification_rules"},
		{name: "notifications", tenantColumn: "tenant"},
		// knowledge graph
		{name: "knowledge_graph_edge"},
		{name: "knowledge_graph_metadata"},
		{name: "knowledge_graph_node"},
		{name: "knowledge_graph_tenant_filters"},
		// llm
		{name: "llm_conversations"},
		{name: "llm_global_contexts"},
		{name: "llm_knowledgebases"},
		{name: "llm_rags"},
		// marketplace
		{name: "marketplace_customers"},
		// recommendations
		{name: "recommendation"},
		// runbooks
		{name: "runbook_task_output"},
		// spends
		{name: "spends", tenantColumn: "tenant"},
		// tickets
		{name: "tickets", tenantColumn: "tenant"},
		// upgrade plans
		{name: "upgrade_plan_steps"},
		{name: "upgrade_plan_audit"},
		{name: "upgrade_plan"},
		// workflows
		{name: "workflows"},
		{name: "workflow_templates"},
		// business units
		{name: "businessunit_funding", tenantColumn: "tenant"},
		{name: "business_unit", tenantColumn: "tenant"},
		// application groups
		{name: "application_group_mapping"},
		{name: "application_group"},
		// agents
		{name: "agent", tenantColumn: "tenant"},
		// feature flags
		{name: "feature_flag"},
		// user history
		{name: "user_history"},
		// projects
		{name: "projects", tenantColumn: "tenant"},
		// funding/users/groups
		{name: "funding_sources", tenantColumn: "tenant"},
		{name: "user_groups", tenantColumn: "tenant"},
		{name: "tenant_users", tenantColumn: "tenant"},
		{name: "tenant_attrs"},
		// tenant itself - must be last
		{name: "tenant", tenantColumn: "id"},
	}

	err = deleteTenantTables(ctx, tx, tenantTables, request.Id)
	if err != nil {
		return TenantDeleteResponse{}, err
	}

	err = tx.Commit()
	if err != nil {
		return TenantDeleteResponse{}, err
	}

	ctx.GetLogger().Info("tenant deleted successfully", "tenant_id", request.Id)

	if err := audit.PublishAuditEvent(ctx, audit.Audit{
		TenantId:      request.Id,
		UserId:        ctx.GetSecurityContext().GetUserId(),
		EventTime:     time.Now(),
		EventCategory: audit.EventCategoryTenant,
		EventType:     audit.EventTypeTenantDelete,
		EventState:    tenantRecord,
		EventActor:    audit.EventActorApiService,
		EventTarget:   "tenant",
		EventAction:   audit.EventActionDelete,
		EventStatus:   audit.EventStatusSuccess,
	}); err != nil {
		ctx.GetLogger().Error("failed to publish audit event", "error", err)
	}

	return TenantDeleteResponse(request), nil
}
