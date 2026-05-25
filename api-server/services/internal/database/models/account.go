package models

import (
	"time"
)

type Resource struct {
	Id                 string     `json:"id" mapstructure:"id" validate:"required" db:"id"`
	CreatedAt          *time.Time `json:"created_at" mapstructure:"created_at"  db:"created_at"`
	CreatedBy          *string    `json:"created_by" mapstructure:"created_by"  db:"created_by"`
	UpdatedAt          *time.Time `json:"updated_at" mapstructure:"updated_at"  db:"updated_at"`
	UpdatedBy          *string    `json:"updated_by" mapstructure:"updated_by"  db:"updated_by"`
	ResourceId         *string    `json:"resource_id" mapstructure:"resource_id" validate:"required" db:"resourse_id"`
	Name               *string    `json:"name" mapstructure:"name" validate:"required" db:"name"`
	Type               *string    `json:"type" mapstructure:"type" validate:"required" db:"type"`
	Status             string     `json:"status" mapstructure:"status" validate:"required" db:"status"`
	ResourseCreatedOn  *time.Time `json:"resourse_created_on,omitempty" mapstructure:"resourse_created_on" omitempty:"true" db:"resourse_created_on"`
	Account            string     `json:"account" mapstructure:"account" validate:"required" db:"account"`
	ClodProvider       string     `json:"cloud_provider" mapstructure:"cloud_provider" validate:"required" db:"cloud_provider"`
	Region             string     `json:"region" mapstructure:"region" validate:"required" db:"region"`
	Arn                *string    `json:"arn" mapstructure:"arn"  db:"arn"`
	Tenant             string     `json:"tenant" mapstructure:"tenant" validate:"required" db:"tenant"`
	Tags               Json       `json:"tags" mapstructure:"tags"  db:"tags"`
	Meta               Json       `json:"meta" mapstructure:"meta"  db:"meta"`
	ServiceName        *string    `json:"service_name" mapstructure:"service_name" db:"service_name"`
	FirstSeen          *time.Time `json:"first_seen,omitempty" mapstructure:"first_seen" omitempty:"true" db:"first_seen"`
	LastSeen           *time.Time `json:"last_seen,omitempty" mapstructure:"last_seen" omitempty:"true" db:"last_seen"`
	IsActive           *bool      `json:"is_active" mapstructure:"is_active" validate:"required" db:"is_active"`
	ExternalResourceId string     `json:"external_resource_id" mapstructure:"external_resource_id"  db:"external_resource_id"`
}

type Account struct {
	Id                string     `json:"id" mapstructure:"id" validate:"required" db:"id"`
	CloudProvider     string     `json:"cloud_provider" mapstructure:"cloud_provider" validate:"required" db:"cloud_provider"`
	AccountNumber     *string    `json:"account_number" mapstructure:"account_number" validate:"required" db:"account_number"`
	AccountName       string     `json:"account_name" mapstructure:"account_name" validate:"required" db:"account_name"`
	CreatedAt         *time.Time `json:"created_at" mapstructure:"created_at" validate:"required" db:"created_at"`
	CreatedBy         string     `json:"created_by" mapstructure:"created_by" validate:"required" db:"created_by"`
	UpdatedAt         *time.Time `json:"updated_at" mapstructure:"updated_at" validate:"required" db:"updated_at"`
	UpdatedBy         string     `json:"updated_by" mapstructure:"updated_by" validate:"required" db:"updated_by"`
	BillingSource     *string    `json:"billing_source" mapstructure:"billing_source"  db:"billing_source"`
	StartDate         *time.Time `json:"start_date" mapstructure:"start_date"  db:"start_date"`
	Tenant            string     `json:"tenant" mapstructure:"tenant" validate:"required" db:"tenant"`
	AssumeRole        *string    `json:"assume_role" mapstructure:"assume_role"  db:"assume_role"`
	Region            *string    `json:"region" mapstructure:"region"  db:"region"`
	Status            string     `json:"status" mapstructure:"status" validate:"required" db:"status"`
	AccountUrl        *string    `json:"account_url" mapstructure:"account_url"  db:"account_url"`
	Budget            float32    `json:"budget" mapstructure:"budget"  db:"budget"`
	SyncedAt          *time.Time `json:"synced_at" mapstructure:"synced_at"  db:"synced_at"`
	SyncStatus        *string    `json:"sync_status" mapstructure:"sync_status"  db:"sync_status"`
	AccountAccess     *string    `json:"account_access" mapstructure:"account_access"  db:"account_access"`
	AccountPurpose    *string    `json:"account_purpose" mapstructure:"account_purpose"  db:"account_purpose"`
	Data              *string    `json:"data" mapstructure:"data"  db:"data"`
	AccessKey         *string    `json:"access_key" mapstructure:"access_key"  db:"access_key"`
	AccessSecret      *string    `json:"access_secret" mapstructure:"access_secret" db:"access_secret"`
	AccountType       string     `json:"account_type" mapstructure:"account_type"  db:"account_type"`
	AgentAccessKey    *string    `json:"agent_access_key" mapstructure:"agent_access_key" db:"agent_access_key"`
	AgentAccessSecret *string    `json:"agent_access_secret" mapstructure:"agent_access_secret" db:"agent_access_secret"`
	AgentSyncedAt     *time.Time `json:"agent_synced_at" mapstructure:"agent_synced_at" db:"agent_synced_at"`
	SyncStatusMessage *string    `json:"sync_status_message" mapstructure:"sync_status_message" db:"sync_status_message"`
	ExternalId        *string    `json:"external_id" mapstructure:"external_id" db:"external_id"`
	EtlAttempt        int        `json:"etl_attempt" mapstructure:"etl_attempt" db:"etl_attempt"`
	ParentAccountId   *string    `json:"parent_account_id" mapstructure:"parent_account_id" db:"parent_account_id"`
	AccessSecretV2    *string    `json:"access_secret_v2" mapstructure:"access_secret" db:"access_secret_v2"`
	AccountEnv        string     `json:"account_env" mapstructure:"account_env" db:"account_env"`
}

type Agent struct {
	Id               string     `json:"id" mapstructure:"id" validate:"required" db:"id"`
	CreatedAt        *time.Time `json:"created_at" mapstructure:"created_at" validate:"required" db:"created_at"`
	UpdatedAt        *time.Time `json:"updated_at" mapstructure:"updated_at" validate:"required" db:"updated_at"`
	Tenant           string     `json:"tenant" mapstructure:"tenant" validate:"required" db:"tenant"`
	CloudAccountId   string     `json:"cloud_account_id" mapstructure:"cloud_account_id" validate:"required" db:"cloud_account_id"`
	Type             string     `json:"type" mapstructure:"type" validate:"required" db:"type"`
	Status           string     `json:"status" mapstructure:"status" validate:"required" db:"status"`
	LastConnectedAt  *time.Time `json:"last_connected_at" mapstructure:"last_connected_at" db:"last_connected_at"`
	AccessKey        *string    `json:"access_key" mapstructure:"access_key" db:"access_key"`
	AccessSecret     *string    `json:"access_secret" mapstructure:"access_secret" db:"access_secret"`
	StatusMessage    *string    `json:"status_message" mapstructure:"status_message" db:"status_message"`
	LastSyncedAt     *time.Time `json:"last_synced_at" mapstructure:"last_synced_at" db:"last_synced_at"`
	Version          *string    `json:"version" mapstructure:"version" db:"version"`
	K8sVersion       *string    `json:"k8s_version" mapstructure:"k8s_version" db:"k8s_version"`
	ConnectionStatus *string    `json:"connection_status" mapstructure:"connection_status" db:"connection_status"`
	K8sProvider      *string    `json:"k8s_provider" mapstructure:"k8s_provider" db:"k8s_provider"`
	AccessSecretV2   *string    `json:"access_secret_v2" mapstructure:"access_secret" db:"access_secret_v2"`
}

type AccountNodeCount struct {
	CloudAccountId string `json:"cloud_account_id"`
	Count          int    `json:"count"`
}
