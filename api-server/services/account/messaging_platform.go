package account

import (
	"encoding/json"
	"fmt"
	"nudgebee/services/audit"
	"nudgebee/services/common"
	"nudgebee/services/internal/database"
	"nudgebee/services/security"
)

// MessagingPlatform represents a messaging platform record
type MessagingPlatform struct {
	Id        string          `json:"id" db:"id"`
	Username  string          `json:"username" db:"username"`
	TeamName  string          `json:"team_name" db:"team_name"`
	CreatedAt string          `json:"created_at" db:"created_at"`
	TeamId    string          `json:"team_id" db:"team_id"`
	Channels  json.RawMessage `json:"channels" db:"channels"`
	Platform  string          `json:"platform" db:"platform"`
}

// MessagingPlatformListRequest is the input for listing messaging platforms
type MessagingPlatformListRequest struct {
	Platform string `json:"platform,omitempty" mapstructure:"platform"`
}

// MessagingPlatformListResponse wraps the list result
type MessagingPlatformListResponse struct {
	Data []MessagingPlatform `json:"data"`
}

// MessagingPlatformUpdateRequest is the input for updating a messaging platform's channels
type MessagingPlatformUpdateRequest struct {
	Id       string `json:"id" mapstructure:"id" validate:"required"`
	Channels any    `json:"channels" mapstructure:"channels" validate:"required"`
}

// MessagingPlatformUpdateResponse returns affected rows
type MessagingPlatformUpdateResponse struct {
	AffectedRows int `json:"affected_rows"`
}

// MessagingPlatformDeleteRequest is the input for deleting a messaging platform
type MessagingPlatformDeleteRequest struct {
	Id string `json:"id" mapstructure:"id" validate:"required"`
}

// MessagingPlatformDeleteResponse returns the deleted platform id
type MessagingPlatformDeleteResponse struct {
	Id string `json:"id"`
}

// ListMessagingPlatforms returns messaging platforms filtered by tenant and optionally by platform type
func ListMessagingPlatforms(ctx *security.RequestContext, request MessagingPlatformListRequest) (MessagingPlatformListResponse, error) {
	tenantId := ctx.GetSecurityContext().GetTenantId()
	if tenantId == "" {
		return MessagingPlatformListResponse{}, fmt.Errorf("unauthorized: missing tenant")
	}

	dbms, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		return MessagingPlatformListResponse{}, fmt.Errorf("failed to get database: %w", err)
	}

	var platforms []MessagingPlatform
	if request.Platform != "" {
		err = dbms.Db.Select(&platforms,
			"SELECT id, COALESCE(username, '') AS username, COALESCE(team_name, '') AS team_name, COALESCE(created_at::text, '') AS created_at, COALESCE(team_id, '') AS team_id, COALESCE(channels, '[]') AS channels, COALESCE(platform, '') AS platform FROM messaging_platforms WHERE tenant_id = $1 AND platform = $2 ORDER BY team_name",
			tenantId, request.Platform)
	} else {
		err = dbms.Db.Select(&platforms,
			"SELECT id, COALESCE(username, '') AS username, COALESCE(team_name, '') AS team_name, COALESCE(created_at::text, '') AS created_at, COALESCE(team_id, '') AS team_id, COALESCE(channels, '[]') AS channels, COALESCE(platform, '') AS platform FROM messaging_platforms WHERE tenant_id = $1 ORDER BY team_name",
			tenantId)
	}
	if err != nil {
		return MessagingPlatformListResponse{}, fmt.Errorf("failed to query messaging platforms: %w", err)
	}

	if platforms == nil {
		platforms = []MessagingPlatform{}
	}

	return MessagingPlatformListResponse{Data: platforms}, nil
}

// UpdateMessagingPlatform updates the channels field of a messaging platform
func UpdateMessagingPlatform(ctx *security.RequestContext, request MessagingPlatformUpdateRequest) (MessagingPlatformUpdateResponse, error) {
	err := common.ValidateStruct(request)
	if err != nil {
		return MessagingPlatformUpdateResponse{}, err
	}

	tenantId := ctx.GetSecurityContext().GetTenantId()
	if tenantId == "" {
		return MessagingPlatformUpdateResponse{}, fmt.Errorf("unauthorized: missing tenant")
	}

	dbms, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		return MessagingPlatformUpdateResponse{}, fmt.Errorf("failed to get database: %w", err)
	}

	channelsJSON, err := common.MarshalJson(request.Channels)
	if err != nil {
		return MessagingPlatformUpdateResponse{}, fmt.Errorf("failed to marshal channels: %w", err)
	}

	result, err := dbms.Db.Exec(
		"UPDATE messaging_platforms SET channels = $1, updated_at = now(), updated_by = $2 WHERE id = $3 AND tenant_id = $4",
		string(channelsJSON), ctx.GetSecurityContext().GetUserId(), request.Id, tenantId)
	if err != nil {
		return MessagingPlatformUpdateResponse{}, fmt.Errorf("failed to update messaging platform: %w", err)
	}

	affected, _ := result.RowsAffected()
	if affected > 0 {
		audit.LogChange(ctx, audit.ChangeInput{
			EventCategory: audit.EventCategoryNotifications,
			EventType:     audit.EventTypeMessagingPlatformUpdate,
			EventAction:   audit.EventActionUpdate,
			TargetID:      request.Id,
			TableName:     "messaging_platforms",
			NewData:       map[string]any{"id": request.Id, "channels": request.Channels},
		})
	}
	return MessagingPlatformUpdateResponse{AffectedRows: int(affected)}, nil
}

// DeleteMessagingPlatform deletes a messaging platform by id, scoped to tenant
func DeleteMessagingPlatform(ctx *security.RequestContext, request MessagingPlatformDeleteRequest) (MessagingPlatformDeleteResponse, error) {
	err := common.ValidateStruct(request)
	if err != nil {
		return MessagingPlatformDeleteResponse{}, err
	}

	tenantId := ctx.GetSecurityContext().GetTenantId()
	if tenantId == "" {
		return MessagingPlatformDeleteResponse{}, fmt.Errorf("unauthorized: missing tenant")
	}

	dbms, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		return MessagingPlatformDeleteResponse{}, fmt.Errorf("failed to get database: %w", err)
	}

	result, err := dbms.Db.Exec(
		"DELETE FROM messaging_platforms WHERE id = $1 AND tenant_id = $2",
		request.Id, tenantId)
	if err != nil {
		return MessagingPlatformDeleteResponse{}, fmt.Errorf("failed to delete messaging platform: %w", err)
	}

	affected, _ := result.RowsAffected()
	if affected == 0 {
		return MessagingPlatformDeleteResponse{}, fmt.Errorf("messaging platform not found or unauthorized")
	}

	audit.LogChange(ctx, audit.ChangeInput{
		EventCategory: audit.EventCategoryNotifications,
		EventType:     audit.EventTypeMessagingPlatformDelete,
		EventAction:   audit.EventActionDelete,
		TargetID:      request.Id,
		TableName:     "messaging_platforms",
		OldData:       map[string]any{"id": request.Id},
	})

	return MessagingPlatformDeleteResponse(request), nil
}
