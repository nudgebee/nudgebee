package user

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"nudgebee/services/audit"
	"nudgebee/services/common"
	"nudgebee/services/config"
	"nudgebee/services/internal/database"
	"nudgebee/services/internal/database/models"
	"nudgebee/services/security"
	"nudgebee/services/tenant"
	"strings"

	"github.com/jmoiron/sqlx"
)

// createOrGetUser inserts a user or returns the existing one if username conflicts.
// Returns the user model and whether it was newly created.
func createOrGetUser(dbms *database.DatabaseManager, displayName, status, username string, createdBy *string) (models.User, bool, error) {
	var user models.User
	err := dbms.Db.QueryRowx(
		`INSERT INTO users (display_name, status, username, created_by, updated_by)
		VALUES ($1, $2, $3, $4, $4)
		ON CONFLICT ON CONSTRAINT users_username_key DO NOTHING
		RETURNING id, username, display_name, status, created_at, updated_at, created_by, updated_by`,
		displayName, status, username, createdBy,
	).StructScan(&user)
	if err == sql.ErrNoRows {
		// User already exists, fetch it
		err = dbms.Db.QueryRowx(
			`SELECT id, username, display_name, status FROM users WHERE username = $1`, username,
		).StructScan(&user)
		if err != nil {
			return models.User{}, false, err
		}
		return user, false, nil
	}
	if err != nil {
		return models.User{}, false, err
	}
	return user, true, nil
}

// createUserTenant creates a tenant_users association, ignoring if already exists.
func createUserTenant(dbms *database.DatabaseManager, userId, tenantId string, createdBy *string) error {
	_, err := dbms.Db.Exec(
		`INSERT INTO tenant_users ("user", tenant, created_by, updated_by) VALUES ($1, $2, $3, $3)
		ON CONFLICT ON CONSTRAINT tenant_users_tenant_user_key DO NOTHING`,
		userId, tenantId, createdBy,
	)
	return err
}

// createUserRole creates a user role, ignoring if already exists.
func createUserRole(dbms *database.DatabaseManager, userId, role, entityType, entityId string, createdBy *string, tenantId string) error {
	_, err := dbms.Db.Exec(
		`INSERT INTO user_roles (entity_type, role, entity_id, user_id, created_by, tenant_id)
		VALUES ($1, $2, $3, $4, $5, $6)
		ON CONFLICT ON CONSTRAINT user_roles_role_user_id_entity_type_entity_id_key DO NOTHING`,
		entityType, role, entityId, userId, createdBy, tenantId,
	)
	return err
}

// sendTenantInvitationEmail sends an invitation email and sets the invited tenant
// as the user's default. Replaces the RPC user_tenant_onboard_email event trigger.
func sendTenantInvitationEmail(ctx *security.RequestContext, userId, tenantId string) {
	// Detach from HTTP request context so this goroutine survives after the response is sent
	ctx = security.NewRequestContext(context.WithoutCancel(ctx.GetContext()), ctx.GetSecurityContext(), ctx.GetLogger(), ctx.GetTracer(), ctx.GetMeter())

	userObj, err := GetUserById(ctx, userId)
	if err != nil {
		ctx.GetLogger().Error("invitation: user not found", "error", err, "user_id", userId)
		return
	}
	tenantObj, err := tenant.GetTenant(ctx, tenantId)
	if err != nil {
		ctx.GetLogger().Error("invitation: tenant not found", "error", err, "tenant_id", tenantId)
		return
	}

	// Set the newly invited tenant as the user's default so they land on it after login
	manager, dbErr := database.GetDatabaseManager(database.Metastore)
	if dbErr != nil {
		ctx.GetLogger().Error("invitation: error getting database manager", "error", dbErr)
	} else {
		tx, txErr := manager.Db.Beginx()
		if txErr != nil {
			ctx.GetLogger().Error("invitation: error starting transaction", "error", txErr)
		} else {
			if _, err := tx.Exec(`UPDATE tenant_users SET is_default = false WHERE tenant != $1 AND "user" = $2`, tenantObj.Id, userObj.Id); err != nil {
				_ = tx.Rollback()
				ctx.GetLogger().Error("invitation: error clearing default tenant", "error", err)
				return
			}
			if _, err := tx.Exec(`UPDATE tenant_users SET is_default = true WHERE tenant = $1 AND "user" = $2`, tenantObj.Id, userObj.Id); err != nil {
				_ = tx.Rollback()
				ctx.GetLogger().Error("invitation: error setting default tenant", "error", err)
				return
			}
			if err := tx.Commit(); err != nil {
				ctx.GetLogger().Error("invitation: error committing default tenant update", "error", err)
			}
		}
	}

	message := map[string]any{
		"kind":      "email",
		"email":     []string{userObj.Username},
		"subject":   config.Config.BrandingName + " invitation notification",
		"tenant_id": tenantObj.Id,
		"type":      "invite",
		"parameters": map[string]any{
			"texts": map[string]any{
				"login_button": "Go to " + config.Config.BrandingName,
				"title":        "User Invitation",
				"organization": map[string]any{
					"id":            tenantObj.Id,
					"name":          tenantObj.Name,
					"currency_code": "$",
				},
			},
			"links": map[string]any{
				"login_button": config.Config.BaseUrl,
			},
		},
	}
	if pubErr := common.MqPublish(config.Config.RabbitMqNotificationsExchange, config.Config.RabbitMqNotificationsQueue, message); pubErr != nil {
		ctx.GetLogger().Error("invitation: error publishing message to queue", "error", pubErr)
	}
}

func CreateUser(context *security.RequestContext, userRequest UserCreateRequest) (UserCreateResponse, error) {
	err := common.ValidateStruct(userRequest)
	if err != nil {
		return UserCreateResponse{}, err
	}

	if !context.GetSecurityContext().IsSuperAdmin() {
		if context.GetSecurityContext().GetUserId() == "" {
			return UserCreateResponse{}, fmt.Errorf("unauthorized")
		}

		if context.GetSecurityContext().GetTenantId() == "" {
			return UserCreateResponse{}, fmt.Errorf("unauthorized")
		}
	}

	if !common.IsValidUserEmail(userRequest.Username) {
		return UserCreateResponse{}, fmt.Errorf("user: not a valid username")
	}

	displayName := userRequest.Firstname
	if userRequest.Lastname != "" {
		displayName = userRequest.Firstname + " " + userRequest.Lastname
	}

	role := userRequest.Role
	if role != "" && !security.IsValidTenantRole(role) {
		return UserCreateResponse{}, fmt.Errorf("user: not a valid Role")
	}

	context.GetLogger().Info("CreateUser", "username", userRequest.Username, "displayName", displayName)
	dbms, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		return UserCreateResponse{}, err
	}

	var createdBy *string
	if context.GetSecurityContext().GetUserId() != "" {
		uid := context.GetSecurityContext().GetUserId()
		createdBy = &uid
	}

	userModel, isNew, err := createOrGetUser(dbms, displayName, "inactive", userRequest.Username, createdBy)
	if err != nil {
		return UserCreateResponse{}, err
	}
	if !isNew {
		context.GetLogger().Info("user already exists", "username", userRequest.Username)
	} else {
		audit.LogChange(context, audit.ChangeInput{
			EventCategory: audit.EventCategoryUser,
			EventType:     audit.EventTypeUserCreate,
			EventAction:   audit.EventActionCreate,
			TargetID:      userModel.Id,
			TableName:     "users",
			NewData:       map[string]any{"id": userModel.Id, "username": userRequest.Username, "display_name": displayName, "status": "inactive"},
		})
	}
	context.GetLogger().Info("CreateUserResponse", "userId", userModel.Id)

	var userTenantId string
	if context.GetSecurityContext().GetTenantId() != "" {
		if !context.GetSecurityContext().IsTenantAdmin() && !context.GetSecurityContext().IsSuperAdmin() {
			return UserCreateResponse{}, common.ErrorUnauthorized("Only tenant admins can add users to a tenant")
		}
		context.GetLogger().Info("AssignUser to tenant", "userId", userModel.Id, "tenantId", context.GetSecurityContext().GetTenantId())
		err = createUserTenant(dbms, userModel.Id, context.GetSecurityContext().GetTenantId(), createdBy)
		if err != nil {
			return UserCreateResponse{}, err
		}
		audit.LogChange(context, audit.ChangeInput{
			EventCategory: audit.EventCategoryTenant,
			EventType:     audit.EventTypeTenantUserCreate,
			EventAction:   audit.EventActionCreate,
			TargetID:      userModel.Id,
			TableName:     "tenant_users",
			NewData:       map[string]any{"user": userModel.Id, "tenant": context.GetSecurityContext().GetTenantId()},
		})

		// assign role to user
		if role != "" {
			createdById := context.GetSecurityContext().GetUserId()
			err = createUserRole(dbms, userModel.Id, role, "tenant", context.GetSecurityContext().GetTenantId(), &createdById, context.GetSecurityContext().GetTenantId())
			context.GetLogger().Info("CreateUserRole", "userId", userModel.Id, "role", role)
			if err != nil {
				return UserCreateResponse{}, err
			}
			audit.LogChange(context, audit.ChangeInput{
				EventCategory: audit.EventCategoryRole,
				EventType:     audit.EventTypeRoleUserCreate,
				EventAction:   audit.EventActionCreate,
				TargetID:      userModel.Id,
				TableName:     "user_roles",
				NewData:       map[string]any{"user_id": userModel.Id, "role": role, "entity_type": "tenant", "entity_id": context.GetSecurityContext().GetTenantId()},
			})
		}
		userTenantId = context.GetSecurityContext().GetTenantId()

		// Send invitation email (replaces RPC user_tenant_onboard_email trigger)
		go sendTenantInvitationEmail(context, userModel.Id, userTenantId)
	} else if context.GetSecurityContext().IsSuperAdmin() && userRequest.Tenantname != "" {
		// create and assign user to new tenant
		resp, err := tenant.CreateTenant(context, tenant.TenantCreateRequest{
			TenantName: userRequest.Tenantname,
			UserId:     userModel.Id,
		})
		if err != nil {
			context.GetLogger().Error("Error creating tenant", "error", err)
		}
		userTenantId = resp.Id
	}

	return UserCreateResponse{
		Id:       userModel.Id,
		Status:   "Ok",
		Message:  "User created successfully",
		TenantId: userTenantId,
	}, nil

}

func DeleteUser(context *security.RequestContext, userId string) (UserDeleteResponse, error) {

	if userId == "" {
		return UserDeleteResponse{}, common.ErrorBadRequest("User id is required")
	}

	if context.GetSecurityContext().GetUserId() != "" && context.GetSecurityContext().GetTenantId() != "" {
		context.GetLogger().Info(fmt.Sprintf(`Deleting user %s by %s tenant %s`, userId, context.GetSecurityContext().GetUserId(), context.GetSecurityContext().GetTenantId()))
	} else if context.GetSecurityContext().IsSuperAdmin() {
		context.GetLogger().Info(fmt.Sprintf(`Deleting user %s by admin`, userId))
	} else {
		return UserDeleteResponse{}, common.ErrorUnauthorized("Unauthorized")
	}

	// remove all in single transaction
	databaseManager, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		return UserDeleteResponse{}, err
	}

	tx, err := databaseManager.Db.Begin()
	if err != nil {
		return UserDeleteResponse{}, err
	}

	context.GetLogger().Info("Deleting tenant user mapping", "delete_user", userId)
	_, err = tx.Exec("delete from tenant_users tu where tu.user = $1", userId)
	if err != nil {
		context.GetLogger().Error("Error deleting tenant_users", "error", err)
		err = tx.Rollback()
		if err != nil {
			context.GetLogger().Error("Error rolling back transaction", "error", err)
		}
		return UserDeleteResponse{}, err
	}

	context.GetLogger().Info("Deleting user auths", "delete_user", userId)
	_, err = tx.Exec("delete from user_auths ua where ua.user  = $1", userId)
	if err != nil {
		context.GetLogger().Error("Error deleting user_auths", "error", err)
		err = tx.Rollback()
		if err != nil {
			context.GetLogger().Error("Error rolling back transaction", "error", err)
		}
		return UserDeleteResponse{}, err
	}

	context.GetLogger().Info("Deleting user roles", "delete_user", userId)
	_, err = tx.Exec("delete from user_roles ur where ur.user_id = $1", userId)
	if err != nil {
		context.GetLogger().Error("Error deleting user_roles", "error", err)
		err = tx.Rollback()
		if err != nil {
			context.GetLogger().Error("Error rolling back transaction", "error", err)
		}
		return UserDeleteResponse{}, err
	}

	context.GetLogger().Info("Deleting user groups", "delete_user", userId)
	_, err = tx.Exec("delete from usergroup_users ugu where ugu.user  =  $1", userId)
	if err != nil {
		context.GetLogger().Error("Error deleting usergroup_users", "error", err)
		err = tx.Rollback()
		if err != nil {
			context.GetLogger().Error("Error rolling back transaction", "error", err)
		}
		return UserDeleteResponse{}, err
	}

	// review other mappings and move to another admin user

	err = tx.Commit()
	if err != nil {
		return UserDeleteResponse{}, err
	}

	// Audit cascade deletions and the user itself
	audit.LogChange(context, audit.ChangeInput{
		EventCategory: audit.EventCategoryTenant,
		EventType:     audit.EventTypeTenantUserDelete,
		EventAction:   audit.EventActionDelete,
		TargetID:      userId,
		TableName:     "tenant_users",
		OldData:       map[string]any{"user": userId},
	})
	audit.LogChange(context, audit.ChangeInput{
		EventCategory: audit.EventCategoryUser,
		EventType:     audit.EventTypeUserLoginDelete,
		EventAction:   audit.EventActionDelete,
		TargetID:      userId,
		TableName:     "user_auths",
		OldData:       map[string]any{"user": userId},
	})
	audit.LogChange(context, audit.ChangeInput{
		EventCategory: audit.EventCategoryRole,
		EventType:     audit.EventTypeRoleUserDelete,
		EventAction:   audit.EventActionDelete,
		TargetID:      userId,
		TableName:     "user_roles",
		OldData:       map[string]any{"user_id": userId},
	})
	audit.LogChange(context, audit.ChangeInput{
		EventCategory: audit.EventCategoryGroup,
		EventType:     audit.EventTypeGroupUserDelete,
		EventAction:   audit.EventActionDelete,
		TargetID:      userId,
		TableName:     "usergroup_users",
		OldData:       map[string]any{"user": userId},
	})
	audit.LogChange(context, audit.ChangeInput{
		EventCategory: audit.EventCategoryUser,
		EventType:     audit.EventTypeUserDelete,
		EventAction:   audit.EventActionDelete,
		TargetID:      userId,
		TableName:     "users",
		OldData:       map[string]any{"id": userId},
	})

	return UserDeleteResponse{
		Id: userId,
	}, nil
}

func GetUserById(context *security.RequestContext, id string) (models.User, error) {
	manager, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		return models.User{}, err
	}
	row := manager.Db.QueryRowx("SELECT id, username, display_name, status, created_at, updated_at, created_by, updated_by FROM users WHERE id = $1", id)
	if row.Err() != nil {
		return models.User{}, row.Err()
	}
	tenant := models.User{}
	err = row.StructScan(&tenant)
	if err != nil {
		return models.User{}, err
	}
	return tenant, nil
}

func GetUserByTenant(context *security.RequestContext, tenantId string) ([]models.User, error) {
	users := []models.User{}
	manager, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		return users, err
	}
	rows, err := manager.Db.Queryx(`
        SELECT u.id, u.username, u.display_name, u.status, u.created_at, u.updated_at, u.created_by, u.updated_by
        FROM users u
        WHERE u.status = 'active'
        AND u.id IN (
            SELECT tu.user
            FROM tenant_users tu
            WHERE tu.tenant = $1
        )`, tenantId)
	if err != nil {
		context.GetLogger().Error("error getting users by tenant", "tenantId", tenantId, "error", err)
		return users, err
	}
	defer func() {
		err := rows.Close()
		if err != nil {
			context.GetLogger().Error("error closing rows", "error", err)
		}
	}()

	for rows.Next() {
		u := models.User{}
		err = rows.StructScan(&u)
		if err != nil {
			context.GetLogger().Error("error scanning user row", "tenantId", tenantId, "error", err)
			return users, err
		}
		users = append(users, u)
	}
	return users, nil
}

func CreateUserAuthToken(context *security.RequestContext, tokenCreateRequest UserTokenCreateRequest) (UserTokenCreateResponse, error) {
	// validate if user has access to tenant, user is active
	if context.GetSecurityContext().GetUserId() == "" || context.GetSecurityContext().GetTenantId() == "" {
		return UserTokenCreateResponse{}, common.ErrorUnauthorized("Unauthorized")
	}

	if tokenCreateRequest.Name == "" {
		return UserTokenCreateResponse{}, common.ErrorBadRequest("Name is required")
	}

	user, err := GetUserById(context, context.GetSecurityContext().GetUserId())
	if err != nil {
		return UserTokenCreateResponse{}, err
	}

	if user.Status != "active" {
		return UserTokenCreateResponse{}, common.ErrorUnauthorized("User is not active")
	}

	databaseManager, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		return UserTokenCreateResponse{}, err
	}

	// if token with same name exists return error.
	var existingId string
	err = databaseManager.Db.QueryRowx(`SELECT id FROM user_auths WHERE name = $1 AND tenant_id = $2 AND "user" = $3 and provider_type = 'credentials' and provider = 'token'`, tokenCreateRequest.Name, context.GetSecurityContext().GetTenantId(), context.GetSecurityContext().GetUserId()).Scan(&existingId)
	if err != nil && err != sql.ErrNoRows {
		return UserTokenCreateResponse{}, err
	}
	if existingId != "" {
		return UserTokenCreateResponse{}, common.ErrorConflict("Token with same name already exists")
	}

	// generate random string token && store hashed value
	token, err := common.GenerateRandomHexString(32)
	if err != nil {
		return UserTokenCreateResponse{}, err
	}
	hashedToken, err := common.HashPassword(token)
	if err != nil {
		return UserTokenCreateResponse{}, err
	}

	// store hashed value
	_, err = databaseManager.Db.Exec(`INSERT INTO user_auths ("user", tenant_id, name, credential, provider_type, provider, status) VALUES ($1, $2, $3, $4, $5, $6, $7)`, context.GetSecurityContext().GetUserId(), context.GetSecurityContext().GetTenantId(), tokenCreateRequest.Name, hashedToken, "credentials", "token", "active")

	if err != nil {
		return UserTokenCreateResponse{}, err
	}

	audit.LogChange(context, audit.ChangeInput{
		EventCategory: audit.EventCategoryUser,
		EventType:     audit.EventTypeUserLoginCreate,
		EventAction:   audit.EventActionCreate,
		TargetID:      context.GetSecurityContext().GetUserId(),
		TableName:     "user_auths",
		NewData:       map[string]any{"user": context.GetSecurityContext().GetUserId(), "name": tokenCreateRequest.Name, "provider_type": "credentials", "provider": "token"},
	})

	// return token
	return UserTokenCreateResponse{
		Token: token,
		Name:  tokenCreateRequest.Name,
	}, nil
}

func DeleteUserAuthToken(context *security.RequestContext, tokenDeleteRequest UserTokenDeleteRequest) (UserTokenDeleteResponse, error) {
	if context.GetSecurityContext().GetUserId() == "" || context.GetSecurityContext().GetTenantId() == "" {
		return UserTokenDeleteResponse{}, common.ErrorUnauthorized("Unauthorized")
	}

	if tokenDeleteRequest.Name == "" {
		return UserTokenDeleteResponse{}, common.ErrorBadRequest("Name is required")
	}

	databaseManager, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		return UserTokenDeleteResponse{}, err
	}

	_, err = databaseManager.Db.Exec(`DELETE FROM user_auths WHERE name = $1 AND tenant_id = $2 AND "user" = $3  and provider_type = 'credentials' and provider = 'token'`, tokenDeleteRequest.Name, context.GetSecurityContext().GetTenantId(), context.GetSecurityContext().GetUserId())
	if err != nil {
		return UserTokenDeleteResponse{}, err
	}

	audit.LogChange(context, audit.ChangeInput{
		EventCategory: audit.EventCategoryUser,
		EventType:     audit.EventTypeUserLoginDelete,
		EventAction:   audit.EventActionDelete,
		TargetID:      context.GetSecurityContext().GetUserId(),
		TableName:     "user_auths",
		OldData:       map[string]any{"user": context.GetSecurityContext().GetUserId(), "name": tokenDeleteRequest.Name, "provider_type": "credentials", "provider": "token"},
	})

	return UserTokenDeleteResponse{Name: tokenDeleteRequest.Name, Status: "deleted"}, nil
}

func ListUserTenantRoles(context *security.RequestContext, request UserTenantRolesRequest) (UserTenantRolesResponse, error) {
	t, err := tenant.GetTenant(context, request.TenantId)
	if err != nil {
		return UserTenantRolesResponse{}, fmt.Errorf("tenant not found: %s", request.TenantId)
	}

	// Verify caller has authority over this tenant
	if !context.GetSecurityContext().IsSuperAdmin() && context.GetSecurityContext().GetTenantId() != t.Id {
		return UserTenantRolesResponse{}, fmt.Errorf("unauthorized: caller does not have access to tenant %s", request.TenantId)
	}

	manager, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		return UserTenantRolesResponse{}, err
	}

	var roles []UserTenantRole
	err = manager.Db.Select(&roles, `
		SELECT ur.entity_id, ur.entity_type, ur.role
		FROM user_roles ur
		JOIN users u ON ur.user_id = u.id
		WHERE u.username = $1 AND ur.tenant_id = $2
	`, request.Username, t.Id)
	if err != nil {
		return UserTenantRolesResponse{}, err
	}

	var groupRoles []UserTenantRole
	err = manager.Db.Select(&groupRoles, `
		SELECT gr.entity_id, gr.entity_type, gr.role
		FROM group_roles gr
		JOIN user_groups ug ON gr.group_id = ug.id
		JOIN usergroup_users ugu ON ugu."group" = ug.id
		JOIN users u ON ugu."user" = u.id
		WHERE u.username = $1 AND ug.tenant = $2
	`, request.Username, t.Id)
	if err != nil {
		return UserTenantRolesResponse{}, err
	}

	roles = append(roles, groupRoles...)

	for i := range roles {
		if roles[i].EntityType == "tenant" {
			roles[i].EntityId = ""
		}
	}

	return UserTenantRolesResponse{
		Roles:      roles,
		TenantName: t.Name,
	}, nil
}

func SyncUserRoles(context *security.RequestContext, request UserSyncRolesRequest) (UserSyncRolesResponse, error) {
	// 1. Resolve tenant by id
	t, err := tenant.GetTenant(context, request.TenantId)
	if err != nil {
		return UserSyncRolesResponse{}, fmt.Errorf("tenant not found: %s", request.TenantId)
	}

	// Verify caller has authority over this tenant
	if !context.GetSecurityContext().IsSuperAdmin() && context.GetSecurityContext().GetTenantId() != t.Id {
		return UserSyncRolesResponse{}, fmt.Errorf("unauthorized: caller does not have access to tenant %s", request.TenantId)
	}

	manager, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		return UserSyncRolesResponse{}, err
	}

	// 2. Resolve user by username
	var userId string
	err = manager.Db.Get(&userId, "SELECT id FROM users WHERE username = $1", request.Username)
	if err != nil {
		return UserSyncRolesResponse{}, fmt.Errorf("user not found: %s", request.Username)
	}

	// 3. Fetch current tenant-entity roles
	type currentRole struct {
		Id   string `db:"id"`
		Role string `db:"role"`
	}
	var currentRoles []currentRole
	err = manager.Db.Select(&currentRoles,
		`SELECT id, role FROM user_roles WHERE user_id = $1 AND tenant_id = $2 AND entity_type = 'tenant'`,
		userId, t.Id)
	if err != nil {
		return UserSyncRolesResponse{}, err
	}

	// 4. Diff: compute roles to add and roles to remove
	currentRoleNames := make(map[string]string) // role name → role id
	for _, r := range currentRoles {
		currentRoleNames[r.Role] = r.Id
	}

	var rolesToAdd []string
	for _, r := range request.TargetRoles {
		if _, exists := currentRoleNames[r]; !exists {
			rolesToAdd = append(rolesToAdd, r)
		}
	}

	var roleIdsToRemove []string
	if request.RemoveOldRoles {
		targetSet := make(map[string]bool)
		for _, r := range request.TargetRoles {
			targetSet[r] = true
		}
		for _, r := range currentRoles {
			if !targetSet[r.Role] {
				roleIdsToRemove = append(roleIdsToRemove, r.Id)
			}
		}
	}

	// 5. Execute inserts and deletes in a single transaction
	tx, err := manager.Db.Beginx()
	if err != nil {
		return UserSyncRolesResponse{}, err
	}

	added := 0
	for _, role := range rolesToAdd {
		_, err := tx.Exec(
			`INSERT INTO user_roles (user_id, role, entity_type, entity_id, created_by, tenant_id)
			 VALUES ($1, $2, 'tenant', $3, $4, $5)
			 ON CONFLICT (role, user_id, entity_type, entity_id) DO NOTHING`,
			userId, role, t.Id, userId, t.Id)
		if err != nil {
			if rbErr := tx.Rollback(); rbErr != nil {
				slog.Error("failed to rollback transaction", "error", rbErr)
			}
			return UserSyncRolesResponse{}, fmt.Errorf("failed to add role %s: %w", role, err)
		}
		added++
	}

	removed := 0
	if len(roleIdsToRemove) > 0 {
		query, args, err := sqlx.In("DELETE FROM user_roles WHERE id IN (?)", roleIdsToRemove)
		if err != nil {
			if rbErr := tx.Rollback(); rbErr != nil {
				slog.Error("failed to rollback transaction", "error", rbErr)
			}
			return UserSyncRolesResponse{}, err
		}
		query = tx.Rebind(query)
		result, err := tx.Exec(query, args...)
		if err != nil {
			if rbErr := tx.Rollback(); rbErr != nil {
				slog.Error("failed to rollback transaction", "error", rbErr)
			}
			return UserSyncRolesResponse{}, err
		}
		rowsAffected, _ := result.RowsAffected()
		removed = int(rowsAffected)
	}

	if err := tx.Commit(); err != nil {
		return UserSyncRolesResponse{}, err
	}

	for _, role := range rolesToAdd {
		audit.LogChange(context, audit.ChangeInput{
			EventCategory: audit.EventCategoryRole,
			EventType:     audit.EventTypeRoleUserCreate,
			EventAction:   audit.EventActionCreate,
			TargetID:      userId,
			TableName:     "user_roles",
			NewData:       map[string]any{"user_id": userId, "role": role, "entity_type": "tenant", "entity_id": t.Id},
		})
	}
	for _, roleId := range roleIdsToRemove {
		audit.LogChange(context, audit.ChangeInput{
			EventCategory: audit.EventCategoryRole,
			EventType:     audit.EventTypeRoleUserDelete,
			EventAction:   audit.EventActionDelete,
			TargetID:      userId,
			TableName:     "user_roles",
			OldData:       map[string]any{"id": roleId, "user_id": userId},
		})
	}

	return UserSyncRolesResponse{
		Added:   added,
		Removed: removed,
	}, nil
}

func ListUserAuthTokens(context *security.RequestContext) (UserAuthTokenResponse, error) {
	if context.GetSecurityContext().GetUserId() == "" || context.GetSecurityContext().GetTenantId() == "" {
		return UserAuthTokenResponse{}, common.ErrorUnauthorized("Unauthorized")
	}

	databaseManager, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		return UserAuthTokenResponse{}, err
	}

	rows, err := databaseManager.Db.Queryx(`SELECT id, name, provider, status, created_at, accessed_at FROM user_auths WHERE tenant_id = $1 AND "user" = $2 and provider_type = 'credentials' and provider = 'token'`, context.GetSecurityContext().GetTenantId(), context.GetSecurityContext().GetUserId())
	if err != nil {
		return UserAuthTokenResponse{}, err
	}
	defer func() {
		err := rows.Close()
		if err != nil {
			context.GetLogger().Error("error closing rows", "error", err)
		}
	}()

	var tokens []UserAuthToken
	for rows.Next() {
		var token UserAuthToken
		err := rows.StructScan(&token)
		if err != nil {
			return UserAuthTokenResponse{}, err
		}
		tokens = append(tokens, token)
	}

	return UserAuthTokenResponse{Tokens: tokens}, nil
}

func createTenantWithId(db *sqlx.DB, tenantId, name, userId string) error {
	tx, err := db.Beginx()
	if err != nil {
		return err
	}

	_, err = tx.Exec(
		`INSERT INTO tenant (id, name, created_by, updated_by) VALUES ($1, $2, $3, $4) ON CONFLICT (id) DO NOTHING`,
		tenantId, name, userId, userId)
	if err != nil {
		if rbErr := tx.Rollback(); rbErr != nil {
			slog.Error("failed to rollback transaction", "error", rbErr)
		}
		return err
	}

	_, err = tx.Exec(
		`INSERT INTO tenant_users (tenant, "user", is_owner, is_default, created_by, updated_by) VALUES ($1, $2, true, true, $3, $4)
		 ON CONFLICT ON CONSTRAINT tenant_users_tenant_user_key DO NOTHING`,
		tenantId, userId, userId, userId)
	if err != nil {
		if rbErr := tx.Rollback(); rbErr != nil {
			slog.Error("failed to rollback transaction", "error", rbErr)
		}
		return err
	}

	return tx.Commit()
}

func getGroupIdByNameAndTenant(db *sqlx.DB, groupName, tenantId string) (string, error) {
	var groupId string
	err := db.Get(&groupId, `SELECT id FROM user_groups WHERE name = $1 AND tenant = $2`, groupName, tenantId)
	if err != nil {
		return "", err
	}
	return groupId, nil
}

func addUserToGroup(db *sqlx.DB, userId, groupId string) error {
	_, err := db.Exec(
		`INSERT INTO usergroup_users ("user", "group", created_by, updated_by) VALUES ($1, $2, $3, $4)
		 ON CONFLICT DO NOTHING`,
		userId, groupId, userId, userId)
	return err
}

func generateOrgName(displayName, username string) string {
	if displayName != "" {
		return strings.Split(displayName, " ")[0] + "'s Org"
	}
	if username != "" {
		return strings.Split(username, "@")[0] + "'s Org"
	}
	return "New Org"
}

func CreateUserAuth(context *security.RequestContext, request UserCreateAuthRequest) (UserCreateAuthResponse, error) {
	err := common.ValidateStruct(request)
	if err != nil {
		return UserCreateAuthResponse{}, err
	}

	if !context.GetSecurityContext().IsSuperAdmin() {
		if context.GetSecurityContext().GetUserId() == "" {
			return UserCreateAuthResponse{}, fmt.Errorf("unauthorized")
		}
		if context.GetSecurityContext().GetTenantId() == "" {
			return UserCreateAuthResponse{}, fmt.Errorf("unauthorized")
		}
	}

	manager, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		return UserCreateAuthResponse{}, err
	}

	tenantId := context.GetSecurityContext().GetTenantId()

	var authId string
	err = manager.Db.QueryRowx(
		`INSERT INTO user_auths ("user", tenant_id, provider, provider_type, account_id, name, credential, status, accessed_at, expires_at)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
		 RETURNING id`,
		request.UserId, sql.NullString{String: tenantId, Valid: tenantId != ""},
		request.Provider, request.ProviderType, request.AccountId,
		request.Name, request.Credential, request.Status,
		sql.NullString{String: request.AccessedAt, Valid: request.AccessedAt != ""},
		sql.NullString{String: request.ExpiresAt, Valid: request.ExpiresAt != ""},
	).Scan(&authId)
	if err != nil {
		return UserCreateAuthResponse{}, err
	}

	var userStatus string
	err = manager.Db.Get(&userStatus, `SELECT status FROM users WHERE id = $1`, request.UserId)
	if err != nil {
		return UserCreateAuthResponse{}, err
	}

	audit.LogChange(context, audit.ChangeInput{
		EventCategory: audit.EventCategoryUser,
		EventType:     audit.EventTypeUserLoginCreate,
		EventAction:   audit.EventActionCreate,
		TargetID:      authId,
		UserID:        request.UserId,
		TableName:     "user_auths",
		NewData:       map[string]any{"id": authId, "user": request.UserId, "provider": request.Provider, "provider_type": request.ProviderType, "name": request.Name},
	})

	return UserCreateAuthResponse{
		Id:         authId,
		Name:       request.Name,
		UserStatus: userStatus,
		UserID:     request.UserId,
	}, nil
}

func UpdateUserStatus(context *security.RequestContext, request UserUpdateStatusRequest) (UserUpdateStatusResponse, error) {
	err := common.ValidateStruct(request)
	if err != nil {
		return UserUpdateStatusResponse{}, err
	}

	if !context.GetSecurityContext().IsSuperAdmin() {
		if context.GetSecurityContext().GetUserId() == "" {
			return UserUpdateStatusResponse{}, fmt.Errorf("unauthorized")
		}
		if context.GetSecurityContext().GetTenantId() == "" {
			return UserUpdateStatusResponse{}, fmt.Errorf("unauthorized")
		}
	}

	manager, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		return UserUpdateStatusResponse{}, err
	}

	_, err = manager.Db.Exec(`UPDATE users SET status = $1 WHERE id = $2`, request.Status, request.UserId)
	if err != nil {
		return UserUpdateStatusResponse{}, err
	}

	audit.LogChange(context, audit.ChangeInput{
		EventCategory: audit.EventCategoryUser,
		EventType:     audit.EventTypeUserUpdate,
		EventAction:   audit.EventActionUpdate,
		TargetID:      request.UserId,
		TableName:     "users",
		NewData:       map[string]any{"id": request.UserId, "status": request.Status},
	})

	return UserUpdateStatusResponse{Id: request.UserId}, nil
}

func UpdateUserProfile(ctx *security.RequestContext, request UserUpdateProfileRequest) (UserUpdateProfileResponse, error) {
	err := common.ValidateStruct(request)
	if err != nil {
		return UserUpdateProfileResponse{}, err
	}

	if !ctx.GetSecurityContext().IsTenantAdmin() {
		return UserUpdateProfileResponse{}, common.ErrorUnauthorized("Not Allowed")
	}

	manager, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		return UserUpdateProfileResponse{}, err
	}

	// Update display_name and status
	_, err = manager.Db.Exec(
		`UPDATE users SET display_name = $1, status = $2 WHERE username = $3`,
		request.DisplayName, request.Status, request.Username,
	)
	if err != nil {
		ctx.GetLogger().Error("Error updating user profile", "error", err)
		return UserUpdateProfileResponse{}, common.ErrorInternal("Error updating user profile")
	}

	audit.LogChange(ctx, audit.ChangeInput{
		EventCategory: audit.EventCategoryUser,
		EventType:     audit.EventTypeUserUpdate,
		EventAction:   audit.EventActionUpdate,
		TargetID:      request.Username,
		TableName:     "users",
		NewData:       map[string]any{"username": request.Username, "display_name": request.DisplayName, "status": request.Status},
	})

	// Always sync the tenant role. Empty role means "remove the user's direct
	// tenant role" — UpsertTenantUserRole's empty branch DELETEs the row.
	_, err = tenant.UpsertTenantUserRole(ctx, tenant.TenantUserRoleUpsertRequest{
		Username: request.Username,
		Role:     request.Role,
	})
	if err != nil {
		ctx.GetLogger().Error("Error updating user role", "error", err)
		return UserUpdateProfileResponse{}, common.ErrorInternal("Error updating user role")
	}

	return UserUpdateProfileResponse{
		Status:  "success",
		Message: "User profile updated successfully",
	}, nil
}

func DeleteUserAuth(context *security.RequestContext, request UserDeleteAuthRequest) (UserDeleteAuthResponse, error) {
	err := common.ValidateStruct(request)
	if err != nil {
		return UserDeleteAuthResponse{}, err
	}

	if !context.GetSecurityContext().IsSuperAdmin() {
		if context.GetSecurityContext().GetUserId() == "" {
			return UserDeleteAuthResponse{}, fmt.Errorf("unauthorized")
		}
		if context.GetSecurityContext().GetTenantId() == "" {
			return UserDeleteAuthResponse{}, fmt.Errorf("unauthorized")
		}
	}

	manager, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		return UserDeleteAuthResponse{}, err
	}

	_, err = manager.Db.Exec(`DELETE FROM user_auths WHERE id = $1`, request.Id)
	if err != nil {
		return UserDeleteAuthResponse{}, err
	}

	audit.LogChange(context, audit.ChangeInput{
		EventCategory: audit.EventCategoryUser,
		EventType:     audit.EventTypeUserLoginDelete,
		EventAction:   audit.EventActionDelete,
		TargetID:      request.Id,
		TableName:     "user_auths",
		OldData:       map[string]any{"id": request.Id},
	})

	return UserDeleteAuthResponse(request), nil
}

func UpdateDefaultTenant(context *security.RequestContext, request UserUpdateDefaultTenantRequest) (UserUpdateDefaultTenantResponse, error) {
	err := common.ValidateStruct(request)
	if err != nil {
		return UserUpdateDefaultTenantResponse{}, err
	}

	if !context.GetSecurityContext().IsSuperAdmin() {
		if context.GetSecurityContext().GetUserId() == "" {
			return UserUpdateDefaultTenantResponse{}, fmt.Errorf("unauthorized")
		}
		if context.GetSecurityContext().GetTenantId() == "" {
			return UserUpdateDefaultTenantResponse{}, fmt.Errorf("unauthorized")
		}
	}

	manager, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		return UserUpdateDefaultTenantResponse{}, err
	}

	// Non-super-admins can only flip THEIR OWN default tenant. Otherwise
	// any authenticated user could move another user's default tenant by
	// sending that user's username in the request body.
	if !context.GetSecurityContext().IsSuperAdmin() {
		var targetUserId string
		err = manager.Db.Get(&targetUserId,
			`SELECT id FROM users WHERE username = $1`,
			request.Username)
		if err != nil {
			return UserUpdateDefaultTenantResponse{}, fmt.Errorf("unauthorized")
		}
		if targetUserId != context.GetSecurityContext().GetUserId() {
			return UserUpdateDefaultTenantResponse{}, fmt.Errorf("unauthorized: cannot update default tenant for another user")
		}
	}

	// Verify user is a member of the target tenant before changing defaults
	var exists bool
	err = manager.Db.Get(&exists,
		`SELECT EXISTS(SELECT 1 FROM tenant_users WHERE tenant = $1 AND "user" IN (SELECT id FROM users WHERE username = $2))`,
		request.TenantId, request.Username)
	if err != nil {
		return UserUpdateDefaultTenantResponse{}, err
	}
	if !exists {
		return UserUpdateDefaultTenantResponse{}, fmt.Errorf("user %s is not a member of tenant %s", request.Username, request.TenantId)
	}

	tx, err := manager.Db.Beginx()
	if err != nil {
		return UserUpdateDefaultTenantResponse{}, err
	}

	_, err = tx.Exec(
		`UPDATE tenant_users SET is_default = false WHERE tenant != $1 AND "user" IN (SELECT id FROM users WHERE username = $2)`,
		request.TenantId, request.Username)
	if err != nil {
		if rbErr := tx.Rollback(); rbErr != nil {
			slog.Error("failed to rollback transaction", "error", rbErr)
		}
		return UserUpdateDefaultTenantResponse{}, err
	}

	result, err := tx.Exec(
		`UPDATE tenant_users SET is_default = true WHERE tenant = $1 AND "user" IN (SELECT id FROM users WHERE username = $2)`,
		request.TenantId, request.Username)
	if err != nil {
		if rbErr := tx.Rollback(); rbErr != nil {
			slog.Error("failed to rollback transaction", "error", rbErr)
		}
		return UserUpdateDefaultTenantResponse{}, err
	}

	if err := tx.Commit(); err != nil {
		return UserUpdateDefaultTenantResponse{}, err
	}

	rowsAffected, _ := result.RowsAffected()
	if rowsAffected > 0 {
		audit.LogChange(context, audit.ChangeInput{
			EventCategory: audit.EventCategoryTenant,
			EventType:     audit.EventTypeTenantUserUpdate,
			EventAction:   audit.EventActionUpdate,
			TargetID:      request.Username,
			TenantID:      request.TenantId,
			TableName:     "tenant_users",
			NewData:       map[string]any{"tenant": request.TenantId, "username": request.Username, "is_default": true},
		})
	}
	return UserUpdateDefaultTenantResponse{Updated: int(rowsAffected)}, nil
}

func UpdateUserAccessed(context *security.RequestContext, request UserUpdateAccessedRequest) (UserUpdateAccessedResponse, error) {
	err := common.ValidateStruct(request)
	if err != nil {
		return UserUpdateAccessedResponse{}, err
	}

	if request.AuthId == "" && request.Username == "" {
		return UserUpdateAccessedResponse{}, common.ErrorBadRequest("at least one of auth_id or username must be provided")
	}

	if !context.GetSecurityContext().IsSuperAdmin() {
		return UserUpdateAccessedResponse{}, fmt.Errorf("unauthorized: requires admin access")
	}

	manager, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		return UserUpdateAccessedResponse{}, err
	}

	var result sql.Result
	if request.AuthId != "" {
		result, err = manager.Db.Exec(
			`UPDATE user_auths SET accessed_at = NOW(), tenant_id = $1 WHERE id = $2`,
			request.TenantId, request.AuthId)
	} else {
		result, err = manager.Db.Exec(
			`UPDATE user_auths SET accessed_at = NOW(), tenant_id = $1 WHERE "user" IN (SELECT id FROM users WHERE username = $2)`,
			request.TenantId, request.Username)
	}
	if err != nil {
		return UserUpdateAccessedResponse{}, err
	}

	rowsAffected, _ := result.RowsAffected()
	return UserUpdateAccessedResponse{Updated: int(rowsAffected)}, nil
}

func OnboardUser(context *security.RequestContext, request UserOnboardRequest) (UserOnboardResponse, error) {
	err := common.ValidateStruct(request)
	if err != nil {
		return UserOnboardResponse{}, err
	}

	if !context.GetSecurityContext().IsSuperAdmin() {
		return UserOnboardResponse{}, fmt.Errorf("unauthorized: requires admin access")
	}

	if !common.IsValidUserEmail(request.Username) {
		return UserOnboardResponse{}, fmt.Errorf("user: not a valid username/email")
	}

	if request.Role != "" && !security.IsValidTenantRole(request.Role) {
		return UserOnboardResponse{}, fmt.Errorf("user: not a valid role: %s", request.Role)
	}

	if request.Status == "" {
		request.Status = "active"
	}

	// 1. Create or resolve existing user
	var userId string
	if request.ExistingUserId != "" {
		userId = request.ExistingUserId
	} else {
		nameParts := strings.SplitN(request.DisplayName, " ", 2)
		firstname := nameParts[0]
		if firstname == "" {
			firstname = request.Username
		}
		displayName := firstname
		if len(nameParts) > 1 && nameParts[1] != "" {
			displayName = firstname + " " + nameParts[1]
		}

		dbms, err := database.GetDatabaseManager(database.Metastore)
		if err != nil {
			return UserOnboardResponse{}, err
		}

		userModel, _, err := createOrGetUser(dbms, displayName, request.Status, request.Username, nil)
		if err != nil {
			return UserOnboardResponse{}, err
		}
		userId = userModel.Id
	}

	// 2. Resolve or create tenant
	var tenantId string

	onboardDbms, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		return UserOnboardResponse{}, err
	}

	if request.TenantName != "" {
		t, err := tenant.GetTenantByName(context, request.TenantName)
		if err == nil {
			tenantId = t.Id
			err = createUserTenant(onboardDbms, userId, tenantId, &userId)
			if err != nil {
				return UserOnboardResponse{}, err
			}
		} else {
			resp, err := tenant.CreateTenant(context, tenant.TenantCreateRequest{
				TenantName: request.TenantName,
				UserId:     userId,
			})
			if err != nil {
				return UserOnboardResponse{}, fmt.Errorf("error creating tenant: %w", err)
			}
			tenantId = resp.Id
		}
	} else if request.TenantId != "" {
		t, err := tenant.GetTenant(context, request.TenantId)
		if err == nil {
			tenantId = t.Id
			err = createUserTenant(onboardDbms, userId, tenantId, &userId)
			if err != nil {
				return UserOnboardResponse{}, err
			}
		} else {
			orgName := generateOrgName(request.DisplayName, request.Username)
			err = createTenantWithId(onboardDbms.Db, request.TenantId, orgName, userId)
			if err != nil {
				return UserOnboardResponse{}, err
			}
			tenantId = request.TenantId

			err = createUserRole(onboardDbms, userId, security.AUTH_TENANT_ADMIN_ROLE, "tenant", tenantId, &userId, tenantId)
			if err != nil {
				context.GetLogger().Error("Error creating tenant admin role", "error", err)
			}
		}
	} else {
		orgName := generateOrgName(request.DisplayName, request.Username)
		resp, err := tenant.CreateTenant(context, tenant.TenantCreateRequest{
			TenantName: orgName,
			UserId:     userId,
		})
		if err != nil {
			return UserOnboardResponse{}, fmt.Errorf("error creating tenant: %w", err)
		}
		tenantId = resp.Id
	}

	// 3. Assign role (if provided)
	if request.Role != "" && tenantId != "" {
		err := createUserRole(onboardDbms, userId, request.Role, "tenant", tenantId, &userId, tenantId)
		if err != nil {
			context.GetLogger().Error("Error creating user role", "error", err)
		}
	}

	// 4. Add to groups (if provided)
	if len(request.Groups) > 0 && tenantId != "" {
		manager, err := database.GetDatabaseManager(database.Metastore)
		if err != nil {
			context.GetLogger().Error("Error getting database manager for groups", "error", err)
		} else {
			for _, groupName := range request.Groups {
				groupId, err := getGroupIdByNameAndTenant(manager.Db, groupName, tenantId)
				if err != nil {
					context.GetLogger().Warn("Group not found, skipping", "group", groupName)
					continue
				}

				err = addUserToGroup(manager.Db, userId, groupId)
				if err != nil {
					context.GetLogger().Error("Error adding user to group", "error", err, "group", groupName)
				} else {
					audit.LogChange(context, audit.ChangeInput{
						EventCategory: audit.EventCategoryGroup,
						EventType:     audit.EventTypeGroupUserCreate,
						EventAction:   audit.EventActionCreate,
						TargetID:      userId,
						TableName:     "usergroup_users",
						NewData:       map[string]any{"user": userId, "group": groupId},
					})
				}
			}
		}
	}

	// 5. Invalidate security cache
	_ = security.InvalidateCacheForUser(userId)

	return UserOnboardResponse{
		Id:      userId,
		Status:  "Ok",
		Message: "User onboarded successfully",
	}, nil
}
