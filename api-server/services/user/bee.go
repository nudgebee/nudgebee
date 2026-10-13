package user

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"nudgebee/services/audit"
	"nudgebee/services/common"
	"nudgebee/services/internal/database"
	"nudgebee/services/internal/database/models"
	"nudgebee/services/security"
	"regexp"
	"strings"
)

// beeNamePattern is a bee's canonical name: snake_case, used in mentions, tool
// names and audit rows, so it never changes after the account is created.
var beeNamePattern = regexp.MustCompile(`^[a-z][a-z0-9_]{2,39}$`)

// beeUsernamePrefix marks a bee's username. It is deliberately not an email
// address: no identity provider asserts it and no mailer will send to it, so a
// bee's username can never sign in or be emailed even if a check elsewhere is
// missed.
const beeUsernamePrefix = "bee:"

type BeeCreateRequest struct {
	Name        string `json:"name" mapstructure:"name" validate:"required"`
	DisplayName string `json:"display_name" mapstructure:"display_name" validate:"required"`
}

type BeeCreateResponse struct {
	Id          string `json:"id" mapstructure:"id"`
	Username    string `json:"username" mapstructure:"username"`
	Name        string `json:"name" mapstructure:"name"`
	DisplayName string `json:"display_name" mapstructure:"display_name"`
	TenantId    string `json:"tenant_id" mapstructure:"tenant_id"`
}

// beeUsername builds the account's username, bee:<canonical name>.<random>: the
// suffix because usernames are unique across tenants and the same bee can be
// installed more than once.
func beeUsername(name string) (string, error) {
	suffix := make([]byte, 4)
	if _, err := rand.Read(suffix); err != nil {
		return "", err
	}
	return fmt.Sprintf("%s%s.%s", beeUsernamePrefix, name, hex.EncodeToString(suffix)), nil
}

// CreateBeeUser creates a bee account in the caller's tenant: active, with no role,
// no invitation and no way to sign in. Its rights come only from custom role
// assignments; the database refuses built-in roles and user groups for a bee.
func CreateBeeUser(context *security.RequestContext, request BeeCreateRequest) (BeeCreateResponse, error) {
	if err := common.ValidateStruct(request); err != nil {
		return BeeCreateResponse{}, err
	}
	sc := context.GetSecurityContext()
	tenantId := sc.GetTenantId()
	if tenantId == "" {
		return BeeCreateResponse{}, common.ErrorUnauthorized("a bee account belongs to a tenant")
	}
	// Tenant admins only: a bee acts with whatever roles it is later given, so
	// creating one is as sensitive as assigning a tenant role.
	if !mayAssignTenantRole(sc) {
		return BeeCreateResponse{}, common.ErrorUnauthorized("Creating a bee account requires a tenant admin")
	}
	if !beeNamePattern.MatchString(request.Name) {
		return BeeCreateResponse{}, common.ErrorBadRequest("bee name must match ^[a-z][a-z0-9_]{2,39}$")
	}
	displayName := strings.TrimSpace(request.DisplayName)
	if displayName == "" {
		return BeeCreateResponse{}, common.ErrorBadRequest("display_name is required")
	}

	username, err := beeUsername(request.Name)
	if err != nil {
		return BeeCreateResponse{}, err
	}
	var createdBy *string
	if uid := sc.GetUserId(); uid != "" {
		createdBy = &uid
	}

	dbms, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		return BeeCreateResponse{}, err
	}
	tx, err := dbms.Db.Beginx()
	if err != nil {
		return BeeCreateResponse{}, err
	}
	defer func() { _ = tx.Rollback() }()

	var userId string
	if err := tx.Get(&userId,
		`INSERT INTO users (display_name, status, username, kind, created_by, updated_by)
		 VALUES ($1, 'active', $2, $3, $4, $4)
		 RETURNING id`,
		displayName, username, models.UserKindBee, createdBy); err != nil {
		return BeeCreateResponse{}, err
	}
	if _, err := tx.Exec(
		`INSERT INTO tenant_users ("user", tenant, created_by, updated_by) VALUES ($1, $2, $3, $3)`,
		userId, tenantId, createdBy); err != nil {
		return BeeCreateResponse{}, err
	}
	if err := tx.Commit(); err != nil {
		return BeeCreateResponse{}, err
	}

	audit.LogChange(context, audit.ChangeInput{
		EventCategory: audit.EventCategoryUser,
		EventType:     audit.EventTypeUserCreate,
		EventAction:   audit.EventActionCreate,
		TargetID:      userId,
		TableName:     "users",
		NewData:       map[string]any{"id": userId, "username": username, "display_name": displayName, "status": "active", "kind": models.UserKindBee},
	})
	audit.LogChange(context, audit.ChangeInput{
		EventCategory: audit.EventCategoryTenant,
		EventType:     audit.EventTypeTenantUserCreate,
		EventAction:   audit.EventActionCreate,
		TargetID:      userId,
		TableName:     "tenant_users",
		NewData:       map[string]any{"user": userId, "tenant": tenantId},
	})

	return BeeCreateResponse{
		Id:          userId,
		Username:    username,
		Name:        request.Name,
		DisplayName: displayName,
		TenantId:    tenantId,
	}, nil
}
