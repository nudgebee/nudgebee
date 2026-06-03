package account

import "time"

type DwQueryRecommendationRequest struct {
	AccountId          string `json:"account_id" mapstructure:"account_id" validate:"required"`
	QueryNormalizedMd5 string `json:"query_normalized_md5" mapstructure:"query_normalized_md5" validate:"required"`
	ResourceId         string `json:"resource_id" mapstructure:"resource_id"`
	AccountType        string `json:"account_type" mapstructure:"account_type"`
}

type DwQueryRecommendationResponse struct {
	Data DwQueryRecommendationDataResponse `json:"data" mapstructure:"data"`
}

type DwQueryRecommendationDataResponse struct {
	Recommendation string `json:"recommendation" mapstructure:"recommendation"`
}

type DwQueryProfileRequest struct {
	AccountId          string `json:"account_id" mapstructure:"account_id" validate:"required"`
	QueryNormalizedMd5 string `json:"query_normalized_md5" mapstructure:"query_normalized_md5" validate:"required"`
}

type DwQueryProfileResponse struct {
	Data []any `json:"data" mapstructure:"data" validate:"required"`
}

type AccountDeleteRequest struct {
	Id        string `json:"id" mapstructure:"id" validate:"required"`
	OnlyClean bool   `json:"only_clean" mapstructure:"only_clean"`
}

type AccountDeleteResponse struct {
	Id string `json:"id" mapstructure:"id" validate:"required"`
}

type AccountCreateRequest struct {
	AccountAccess       string         `json:"account_access,omitempty" mapstructure:"account_access" omitempty:"true"`
	SsmAccess           bool           `json:"ssm_access,omitempty" mapstructure:"ssm_access" omitempty:"true"`
	AccountEmail        string         `json:"account_email,omitempty" mapstructure:"account_email" omitempty:"true"`
	AccountName         string         `json:"account_name" mapstructure:"account_name" validate:"required"`
	AccountPurpose      string         `json:"account_purpose,omitempty" mapstructure:"account_purpose" omitempty:"true"`
	AccountType         string         `json:"account_type,omitempty" mapstructure:"account_type" omitempty:"true"`
	AccountUrl          string         `json:"account_url,omitempty" mapstructure:"account_url" omitempty:"true"`
	AssumeRole          string         `json:"assume_role,omitempty" mapstructure:"assume_role" omitempty:"true"`
	BillingSource       string         `json:"billing_source,omitempty" mapstructure:"billing_source" omitempty:"true"`
	Budget              float32        `json:"budget,omitempty" mapstructure:"budget" omitempty:"true"`
	CloudProvider       string         `json:"cloud_provider" mapstructure:"cloud_provider" validate:"required"`
	CreatedAt           *time.Time     `json:"created_at,omitempty" mapstructure:"created_at" omitempty:"true"`
	CreatedBy           string         `json:"created_by,omitempty" mapstructure:"created_by" omitempty:"true"`
	UpdatedBy           string         `json:"updated_by,omitempty" mapstructure:"updated_by" omitempty:"true"`
	Data                map[string]any `json:"data,omitempty" mapstructure:"data" omitempty:"true"`
	Region              string         `json:"region,omitempty" mapstructure:"region" omitempty:"true"`
	StartDate           *time.Time     `json:"start_date,omitempty" mapstructure:"start_date" omitempty:"true"`
	AccessKey           string         `json:"access_key,omitempty" mapstructure:"access_key" omitempty:"true"`
	AccessSecret        string         `json:"access_secret,omitempty" mapstructure:"access_secret" omitempty:"true"`
	Username            string         `json:"username,omitempty" mapstructure:"username" omitempty:"true"`
	Password            string         `json:"password,omitempty" mapstructure:"password" omitempty:"true"`
	Port                string         `json:"port,omitempty" mapstructure:"port" omitempty:"true"`
	AccountNumber       string         `json:"account_number,omitempty" mapstructure:"account_number" omitempty:"true"`
	Tenant              string         `json:"tenant" mapstructure:"tenant"`
	AgentAccessKey      string         `json:"agent_access_key,omitempty" mapstructure:"agent_access_key" omitempty:"true"`
	AgentAccessSecret   string         `json:"agent_access_secret,omitempty" mapstructure:"agent_access_secret" omitempty:"true"`
	AgentAccessSecretV2 string         `json:"access_secret_v2,omitempty" mapstructure:"access_secret_v2" omitempty:"true"`
	ExternalId          string         `json:"external_id,omitempty" mapstructure:"external_id" omitempty:"true"`
	ParentAccountId     string         `json:"parent_account_id,omitempty" mapstructure:"parent_account_id" omitempty:"true"`
}

type RegenerateAgentKeysRequest struct {
	AccountId string `json:"account_id" mapstructure:"account_id" validate:"required"`
	AgentType string `json:"agent_type,omitempty" mapstructure:"agent_type"`
}

type AccountCreateResponse struct {
	Id           string `json:"id" mapstructure:"id" validate:"required"`
	AccessKey    string `json:"access_key" mapstructure:"access_key"`
	AccessSecret string `json:"access_secret" mapstructure:"access_secret"`
	Warning      string `json:"warning,omitempty" mapstructure:"warning"`
}

type CloudUpdateCloudformationPermissionsRequest struct {
	AccountId string `json:"account_id" mapstructure:"account_id" validate:"required"`
}

type CloudUpdateCloudformationPermissionsResponse struct {
	Url             *string `json:"url"`
	StackName       *string `json:"stack_name"`
	TemplateVersion *string `json:"template_version"`
	LatestVersion   *string `json:"latest_version"`
	NeedsUpdate     *bool   `json:"needs_update"`
}

type AwsOnBoardResponse struct {
	Url                  string `json:"url" mapstructure:"url" validate:"required"`
	BucketName           string `json:"bucket_name" mapstructure:"bucket_name" validate:"required"`
	ExternalId           string `json:"external_id" mapstructure:"external_id"`
	AutoDetectionEnabled bool   `json:"auto_detection_enabled" mapstructure:"auto_detection_enabled"`
}

type AwsOnboardStatusRequest struct {
	ExternalId string `json:"external_id" mapstructure:"external_id" validate:"required"`
}

type AwsOnboardStatusResponse struct {
	Status        string `json:"status"`
	AccountId     string `json:"account_id,omitempty"`
	AccountName   string `json:"account_name,omitempty"`
	AccountNumber string `json:"account_number,omitempty"`
	IsReconnected bool   `json:"is_reconnected,omitempty"`
}

type AgentRegenerateKeyResponse struct {
	AccountId    string `json:"account_id" mapstructure:"account_id" validate:"required"`
	AccessKey    string `json:"access_key" mapstructure:"access_key" validate:"required"`
	AccessSecret string `json:"access_secret" mapstructure:"access_secret"`
}

type AzureEventGridOnboardRequest struct {
	AccountId string `json:"account_id" mapstructure:"account_id" validate:"required"`
}

type AzureEventGridOnboardResponse struct {
	Url        string `json:"url" mapstructure:"url" validate:"required"`
	ExternalId string `json:"external_id" mapstructure:"external_id" validate:"required"`
	WebhookUrl string `json:"webhook_url" mapstructure:"webhook_url" validate:"required"`
}

type AwsEventBridgeOnboardRequest struct {
	AccountId string `json:"account_id" mapstructure:"account_id" validate:"required"`
}

type AwsEventBridgeOnboardResponse struct {
	Url        string `json:"url" mapstructure:"url" validate:"required"`
	ExternalId string `json:"external_id" mapstructure:"external_id" validate:"required"`
}

type GCPOnBoardResponse struct {
	Url        string `json:"url" mapstructure:"url" validate:"required"`
	BucketName string `json:"bucket_name" mapstructure:"bucket_name" validate:"required"`
}

type GcpPubSubOnboardRequest struct {
	AccountId string `json:"account_id" mapstructure:"account_id" validate:"required"`
}

type GcpPubSubOnboardResponse struct {
	DeploymentManagerUrl string `json:"deployment_manager_url" mapstructure:"deployment_manager_url" validate:"required"`
	ExternalId           string `json:"external_id" mapstructure:"external_id" validate:"required"`
	PubSubProjectId      string `json:"pubsub_project_id" mapstructure:"pubsub_project_id" validate:"required"`
	SubscriptionName     string `json:"subscription_name" mapstructure:"subscription_name" validate:"required"`
	TemplateYamlUrl      string `json:"template_yaml_url" mapstructure:"template_yaml_url" validate:"required"`
}

type GcpMonitoringWebhookSetupRequest struct {
	AccountId  string `json:"account_id" mapstructure:"account_id" validate:"required"`
	WebhookUrl string `json:"webhook_url" mapstructure:"webhook_url" validate:"required"`
}

type GcpMonitoringWebhookSetupResponse struct {
	ChannelName string `json:"channel_name" mapstructure:"channel_name"`
}

type GcpCheckMonitoringPermissionRequest struct {
	AccountId string `json:"account_id" mapstructure:"account_id" validate:"required"`
}

type GcpCheckMonitoringPermissionResponse struct {
	HasPermission bool   `json:"has_permission" mapstructure:"has_permission"`
	ErrorDetail   string `json:"error_detail,omitempty" mapstructure:"error_detail"`
}

// ValidateCloudCredentialsRequest contains cloud credentials to validate
type ValidateCloudCredentialsRequest struct {
	CloudProvider string `json:"cloud_provider" mapstructure:"cloud_provider" validate:"required"`

	// Azure fields
	TenantID       string `json:"tenant_id,omitempty" mapstructure:"tenant_id"`
	ClientID       string `json:"client_id,omitempty" mapstructure:"client_id"`
	ClientSecret   string `json:"client_secret,omitempty" mapstructure:"client_secret"`
	SubscriptionID string `json:"subscription_id,omitempty" mapstructure:"subscription_id"`

	// GCP fields
	CredentialsJSON string `json:"credentials_json,omitempty" mapstructure:"credentials_json"`
	ProjectID       string `json:"project_id,omitempty" mapstructure:"project_id"`

	// GCP billing data fields (optional — validated only when provided)
	BillingProjectID string `json:"billing_project_id,omitempty" mapstructure:"billing_project_id"`
	BillingDatasetID string `json:"billing_dataset_id,omitempty" mapstructure:"billing_dataset_id"`
	BillingTableID   string `json:"billing_table_id,omitempty" mapstructure:"billing_table_id"`

	// AWS fields — exactly one of (AssumeRole) or (AccessKey + AccessSecret) must be set.
	AssumeRole   string `json:"assume_role,omitempty" mapstructure:"assume_role"`
	ExternalID   string `json:"external_id,omitempty" mapstructure:"external_id"`
	AccessKey    string `json:"access_key,omitempty" mapstructure:"access_key"`
	AccessSecret string `json:"access_secret,omitempty" mapstructure:"access_secret"`
	Region       string `json:"region,omitempty" mapstructure:"region"`
}

// AwsCurInfo mirrors common.AWSCurInfo from the collector — the discovered
// Cost & Usage Report definition. Surfaced to the frontend so users can see
// which CUR was picked, and persisted on cloud_accounts.data so the
// collector can read from it without re-running DescribeReportDefinitions.
type AwsCurInfo struct {
	BucketName  string `json:"bucketName" mapstructure:"bucketName"`
	Region      string `json:"region" mapstructure:"region"`
	Prefix      string `json:"prefix" mapstructure:"prefix"`
	ReportName  string `json:"reportName" mapstructure:"reportName"`
	Compression string `json:"compression" mapstructure:"compression"`
	TimeUnit    string `json:"timeUnit" mapstructure:"timeUnit"`
	Versioning  string `json:"versioning" mapstructure:"versioning"`
	Format      string `json:"format" mapstructure:"format"`
}

// PermissionStatus represents the status of a single permission check
type PermissionStatus struct {
	Permission  string `json:"permission" mapstructure:"permission"`
	HasAccess   bool   `json:"hasAccess" mapstructure:"hasAccess"`
	ErrorDetail string `json:"errorDetail,omitempty" mapstructure:"errorDetail,omitempty"`
}

// ValidateCloudCredentialsResponse contains validation results
type ValidateCloudCredentialsResponse struct {
	Success            bool               `json:"success" mapstructure:"success"`
	Provider           string             `json:"provider" mapstructure:"provider"`
	MissingPermissions []string           `json:"missingPermissions,omitempty" mapstructure:"missingPermissions,omitempty"`
	PermissionDetails  []PermissionStatus `json:"permissionDetails" mapstructure:"permissionDetails"`
	ErrorMessage       string             `json:"errorMessage,omitempty" mapstructure:"errorMessage,omitempty"`
	// AWS-only: account number discovered via sts:GetCallerIdentity.
	AccountNumber string `json:"accountNumber,omitempty" mapstructure:"accountNumber,omitempty"`
	// AWS-only: details of the auto-picked CUR report.
	Cur *AwsCurInfo `json:"cur,omitempty" mapstructure:"cur,omitempty"`
}

// GcpListProjectsRequest is the input for discovering GCP projects
type GcpListProjectsRequest struct {
	CredentialsJSON string `json:"credentials_json" mapstructure:"credentials_json" validate:"required"`
}

// GcpProjectInfo represents a single discovered GCP project
type GcpProjectInfo struct {
	ProjectID string `json:"project_id" mapstructure:"project_id"`
	Name      string `json:"name" mapstructure:"name"`
	State     string `json:"state" mapstructure:"state"`
}

// GcpListProjectsResponse contains discovered GCP projects
type GcpListProjectsResponse struct {
	Projects []GcpProjectInfo `json:"projects" mapstructure:"projects"`
}

// GcpBulkOnboardRequest is the input for onboarding multiple GCP projects at once
type GcpBulkOnboardRequest struct {
	AccountName      string   `json:"account_name" mapstructure:"account_name" validate:"required"`
	CredentialsJSON  string   `json:"credentials_json" mapstructure:"credentials_json" validate:"required"`
	ProjectIDs       []string `json:"project_ids" mapstructure:"project_ids" validate:"required"`
	BillingProjectID string   `json:"billing_project_id,omitempty" mapstructure:"billing_project_id"`
	BillingDatasetID string   `json:"billing_dataset_id,omitempty" mapstructure:"billing_dataset_id"`
	BillingTableID   string   `json:"billing_table_id,omitempty" mapstructure:"billing_table_id"`
}

// BulkOnboardAccountResult represents the result for a single project in bulk onboard
type BulkOnboardAccountResult struct {
	ProjectID string `json:"project_id" mapstructure:"project_id"`
	AccountID string `json:"account_id" mapstructure:"account_id"`
	Status    string `json:"status" mapstructure:"status"`
	Error     string `json:"error,omitempty" mapstructure:"error,omitempty"`
}

// GcpBulkOnboardResponse contains results of bulk GCP onboarding
type GcpBulkOnboardResponse struct {
	Accounts []BulkOnboardAccountResult `json:"accounts" mapstructure:"accounts"`
	ParentID string                     `json:"parent_id" mapstructure:"parent_id"`
}

// AccountAttr represents a single cloud account attribute for upsert
type AccountAttr struct {
	CloudAccountId string `json:"cloud_account_id" mapstructure:"cloud_account_id" validate:"required"`
	Name           string `json:"name" mapstructure:"name" validate:"required"`
	Value          string `json:"value" mapstructure:"value"`
}

// AccountAttrUpsertRequest is the input for upserting cloud account attributes
type AccountAttrUpsertRequest struct {
	Objects []AccountAttr `json:"objects" mapstructure:"objects" validate:"required"`
}

// AccountAttrUpsertResponse returns affected rows count
type AccountAttrUpsertResponse struct {
	AffectedRows int `json:"affected_rows"`
}

// AccountUpdateRequest is the input for updating a cloud account's status, name, or data
type AccountUpdateRequest struct {
	Id          string         `json:"id" mapstructure:"id" validate:"required"`
	Status      string         `json:"status,omitempty" mapstructure:"status"`
	AccountName string         `json:"account_name,omitempty" mapstructure:"account_name"`
	Data        map[string]any `json:"data,omitempty" mapstructure:"data"`
}

// AccountUpdateResponse returns affected rows count
type AccountUpdateResponse struct {
	AffectedRows int `json:"affected_rows"`
}

// AzureListSubscriptionsRequest is the input for discovering Azure subscriptions
type AzureListSubscriptionsRequest struct {
	TenantID     string `json:"tenant_id" mapstructure:"tenant_id" validate:"required"`
	ClientID     string `json:"client_id" mapstructure:"client_id" validate:"required"`
	ClientSecret string `json:"client_secret" mapstructure:"client_secret" validate:"required"`
}

// AzureSubscriptionInfo represents a single discovered Azure subscription
type AzureSubscriptionInfo struct {
	SubscriptionID string `json:"subscription_id" mapstructure:"subscription_id"`
	DisplayName    string `json:"display_name" mapstructure:"display_name"`
	State          string `json:"state" mapstructure:"state"`
}

// AzureListSubscriptionsResponse contains discovered Azure subscriptions
type AzureListSubscriptionsResponse struct {
	Subscriptions []AzureSubscriptionInfo `json:"subscriptions" mapstructure:"subscriptions"`
}

// AzureBulkOnboardSubInput represents a single subscription to onboard
type AzureBulkOnboardSubInput struct {
	SubscriptionID string `json:"subscription_id" mapstructure:"subscription_id" validate:"required"`
	DisplayName    string `json:"display_name" mapstructure:"display_name"`
}

// AzureBulkOnboardRequest is the input for onboarding multiple Azure subscriptions at once
type AzureBulkOnboardRequest struct {
	AccountName   string                     `json:"account_name" mapstructure:"account_name" validate:"required"`
	TenantID      string                     `json:"tenant_id" mapstructure:"tenant_id" validate:"required"`
	ClientID      string                     `json:"client_id" mapstructure:"client_id" validate:"required"`
	ClientSecret  string                     `json:"client_secret" mapstructure:"client_secret" validate:"required"`
	Subscriptions []AzureBulkOnboardSubInput `json:"subscriptions" mapstructure:"subscriptions" validate:"required"`
}

// AzureBulkOnboardAccountResult represents the result for a single subscription in bulk onboard
type AzureBulkOnboardAccountResult struct {
	SubscriptionID string `json:"subscription_id" mapstructure:"subscription_id"`
	AccountID      string `json:"account_id" mapstructure:"account_id"`
	Status         string `json:"status" mapstructure:"status"`
	Error          string `json:"error,omitempty" mapstructure:"error,omitempty"`
}

// AzureBulkOnboardResponse contains results of bulk Azure onboarding
type AzureBulkOnboardResponse struct {
	Accounts []AzureBulkOnboardAccountResult `json:"accounts" mapstructure:"accounts"`
	ParentID string                          `json:"parent_id" mapstructure:"parent_id"`
}
