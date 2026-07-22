package user

import (
	"database/sql"
	"time"
)

// Supported external integration types for identity mapping. These match the
// `integrations.type` values and the `integration_type` column on
// integration_user_accounts.
const (
	IdentityTypeSlack      = "slack"
	IdentityTypeGithub     = "github"
	IdentityTypePagerDuty  = "pagerduty"
	IdentityTypeZenDuty    = "zenduty"
	IdentityTypeServiceNow = "servicenow"
	IdentityTypeGitlab     = "gitlab"
	IdentityTypeJira       = "jira"
	IdentityTypeMsTeams    = "ms_teams"
)

// mappedVia values record how an account became linked to an internal user.
const (
	MappedViaAutoEmail = "auto_email"
	MappedViaManual    = "manual"
)

// IntegrationUserAccount is a single external integration account discovered by
// the Identity Sync job, optionally linked to an internal Nudgebee user.
type IntegrationUserAccount struct {
	Id                  string         `db:"id" json:"id"`
	TenantId            string         `db:"tenant_id" json:"tenant_id"`
	AccountId           sql.NullString `db:"account_id" json:"account_id"`
	AccountName         sql.NullString `db:"account_name" json:"account_name"`
	IntegrationType     string         `db:"integration_type" json:"integration_type"`
	IntegrationId       sql.NullString `db:"integration_id" json:"-"`
	IntegrationName     sql.NullString `db:"integration_name" json:"-"`
	ExternalUserId      string         `db:"external_user_id" json:"external_user_id"`
	ExternalUsername    sql.NullString `db:"external_username" json:"external_username"`
	ExternalEmail       sql.NullString `db:"external_email" json:"external_email"`
	ExternalDisplayName sql.NullString `db:"external_display_name" json:"external_display_name"`
	Profile             []byte         `db:"profile" json:"-"`
	MappedUserId        sql.NullString `db:"mapped_user_id" json:"mapped_user_id"`
	MappedVia           sql.NullString `db:"mapped_via" json:"mapped_via"`
	MappedBy            sql.NullString `db:"mapped_by" json:"-"`
	MappedByName        sql.NullString `db:"mapped_by_name" json:"-"`
	LastSyncedAt        sql.NullTime   `db:"last_synced_at" json:"last_synced_at"`
	CreatedAt           time.Time      `db:"created_at" json:"created_at"`
	UpdatedAt           time.Time      `db:"updated_at" json:"updated_at"`
}

// ---- RPC request/response types (wired in actions_user.go) ----

type ListIntegrationAccountsRequest struct {
	UserId string `json:"user_id" mapstructure:"user_id"`
}

type ListUnmappedAccountsRequest struct {
	IntegrationType string `json:"integration_type" mapstructure:"integration_type"`
}

type IntegrationAccountDto struct {
	Id              string `json:"id"`
	AccountId       string `json:"account_id"`
	AccountName     string `json:"account_name"`
	IntegrationType string `json:"integration_type"`
	// IntegrationId/IntegrationName identify the specific configured integration
	// instance (e.g. one of two ServiceNow connections). Empty for legacy rows
	// synced before instances were tracked. The UI keys per-instance mapping on
	// these so two instances of the same type are independently mappable.
	IntegrationId   string `json:"integration_id"`
	IntegrationName string `json:"integration_name"`
	ExternalUserId  string `json:"external_user_id"`
	Username        string `json:"username"`
	Email           string `json:"email"`
	DisplayName     string `json:"display_name"`
	MappedUserId    string `json:"mapped_user_id"`
	MappedVia       string `json:"mapped_via"`
	// MappedByName is the username of the admin who manually mapped this account
	// (empty for auto-email mappings). LastSyncedAt is RFC3339, empty if never synced.
	MappedByName string `json:"mapped_by_name"`
	LastSyncedAt string `json:"last_synced_at"`
}

type CreateAccountMappingRequest struct {
	// MappingId is the integration_user_accounts row id (NOT a cloud account_id).
	MappingId string `json:"mapping_id" mapstructure:"mapping_id" validate:"required"`
	UserId    string `json:"user_id" mapstructure:"user_id" validate:"required"`
}

type DeleteAccountMappingRequest struct {
	// MappingId is the integration_user_accounts row id (NOT a cloud account_id).
	MappingId string `json:"mapping_id" mapstructure:"mapping_id" validate:"required"`
}

type AccountMappingResponse struct {
	Id     string `json:"id"`
	Status string `json:"status"`
}

func toIntegrationAccountDto(a IntegrationUserAccount) IntegrationAccountDto {
	lastSynced := ""
	if a.LastSyncedAt.Valid {
		lastSynced = a.LastSyncedAt.Time.Format(time.RFC3339)
	}
	return IntegrationAccountDto{
		Id:              a.Id,
		AccountId:       a.AccountId.String,
		AccountName:     a.AccountName.String,
		IntegrationType: a.IntegrationType,
		IntegrationId:   a.IntegrationId.String,
		IntegrationName: a.IntegrationName.String,
		ExternalUserId:  a.ExternalUserId,
		Username:        a.ExternalUsername.String,
		Email:           a.ExternalEmail.String,
		DisplayName:     a.ExternalDisplayName.String,
		MappedUserId:    a.MappedUserId.String,
		MappedVia:       a.MappedVia.String,
		MappedByName:    a.MappedByName.String,
		LastSyncedAt:    lastSynced,
	}
}
