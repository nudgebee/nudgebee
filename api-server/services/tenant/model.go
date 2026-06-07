package tenant

import (
	"nudgebee/services/audit"
	"nudgebee/services/security"
)

type TenantCreateRequest struct {
	TenantName string `json:"tenant_name" mapstructure:"tenant_name" validate:"required"`
	TenantType string `json:"tenant_type" mapstructure:"tenant_type"`
	Role       string `json:"role" mapstructure:"role"`
	UserId     string `json:"user_id" mapstructure:"user_id" validate:"required"`
}

type TenantUserRoleUpsertRequest struct {
	UserId   string `json:"user_id" mapstructure:"user_id"`
	Username string `json:"username" mapstructure:"username"`
	Role     string `json:"role" mapstructure:"role"`
}

type TenantUserRoleUpsertResponse struct {
	Status  string `json:"status" mapstructure:"status"`
	Message string `json:"message" mapstructure:"message"`
}

type TenantGroupRoleUpsertRequest struct {
	GroupId string `json:"group_id" mapstructure:"group_id" validate:"required"`
	Role    string `json:"role" mapstructure:"role"`
}

type TenantGroupRoleUpsertResponse struct {
	Status  string `json:"status" mapstructure:"status"`
	Message string `json:"message" mapstructure:"message"`
}

type AccountRole struct {
	AccountId string `json:"account_id" mapstructure:"account_id" validate:"required"`
	Role      string `json:"role" mapstructure:"role" validate:"required"`
}

type AccountNamespaceRole struct {
	AccountId string `json:"account_id" mapstructure:"account_id" validate:"required"`
	Role      string `json:"role" mapstructure:"role" validate:"required"`
	Namespace string `json:"namespace" mapstructure:"namespace" validate:"required"`
}

type K8sAccountNamespaceUserRoleUpsertRequest struct {
	UserId                   string                 `json:"user_id" mapstructure:"user_id" validate:"required"`
	K8sAccountNamespaceRoles []AccountNamespaceRole `json:"k8saccount_namespace_roles" mapstructure:"k8saccount_namespace_roles" validate:"required"`
}

type AccountNamespaceGroupRoleUpsertRequest struct {
	GroupId                  string                 `json:"group_id" mapstructure:"group_id" validate:"required"`
	K8sAccountNamespaceRoles []AccountNamespaceRole `json:"k8saccount_namespace_roles" mapstructure:"k8saccount_namespace_roles" validate:"required"`
}

type AccountUserRoleUpsertRequest struct {
	UserId       string        `json:"user_id" mapstructure:"user_id" validate:"required"`
	AccountRoles []AccountRole `json:"account_roles" mapstructure:"account_roles" validate:"required"`
}

type AccountUserRoleUpsertResponse struct {
	Status  string `json:"status" mapstructure:"status"`
	Message string `json:"message" mapstructure:"message"`
}

type AccountGroupRoleUpsertRequest struct {
	GroupId      string        `json:"group_id" mapstructure:"group_id" validate:"required"`
	AccountRoles []AccountRole `json:"account_roles" mapstructure:"account_roles" validate:"required"`
}

type AccountGroupRoleUpsertResponse struct {
	Status  string `json:"status" mapstructure:"status"`
	Message string `json:"message" mapstructure:"message"`
}

type ValidateAccessRequestArgs struct {
	AccountId     string `json:"account_id" mapstructure:"account_id"`
	K8sObjectType string `json:"k8s_object_type" mapstructure:"k8s_object_type"`
	K8sObjectName string `json:"k8s_object_name" mapstructure:"k8s_object_name"`

	// Add more fields as we start supporting them

	// TicketConfigId string `json:"ticket_config_id" mapstructure:"ticket_config_id"`
	// NotificationConfigId string `json:"notification_config_id" mapstructure:"notification_config_id"`

}

type ValidateAccessRequest struct {
	UserId string `json:"user_id" mapstructure:"user_id" validate:"required"`

	Access []struct {
		TenantId   string                      `json:"tenant_id" mapstructure:"tenant_id" validate:"required"`
		Permission security.SecurityAccessType `json:"permission" mapstructure:"permission" validate:"required"`
		Category   audit.EventCategory         `json:"category" mapstructure:"category" validate:"required"`
		Args       ValidateAccessRequestArgs   `json:"args" mapstructure:"args" validate:"required"`
	} `json:"access" mapstructure:"access" validate:"required"`
}

type ValidateAccessResponseAccess struct {
	Allowed bool   `json:"allowed" mapstructure:"allowed"`
	Message string `json:"message" mapstructure:"message"`
}

type ValidateAccessResponse struct {
	Access []ValidateAccessResponseAccess `json:"access" mapstructure:"access"`
}

type GetK8sRolesRequest struct {
	UserId         string   `json:"user_id" mapstructure:"user_id" validate:"required"`
	TenantId       string   `json:"tenant_id" mapstructure:"tenant_id" validate:"required"`
	AccountId      string   `json:"account_id" mapstructure:"account_id" validate:"required"`
	K8sObjectType  string   `json:"k8s_object_type" mapstructure:"k8s_object_type" validate:"required"`
	K8sObjectNames []string `json:"k8s_object_name" mapstructure:"k8s_object_name" validate:"required"`
}

type GetK8sRolesResponse struct {
	Enabled     bool                                        `json:"enabled" mapstructure:"enabled"`
	ObjectRoles map[string][]security.K8sRbacPermissionType `json:"object_roles" mapstructure:"object_roles"`
}

type GetK8sObjectNamesRequest struct {
	UserId        string                         `json:"user_id" mapstructure:"user_id" validate:"required"`
	TenantId      string                         `json:"tenant_id" mapstructure:"tenant_id" validate:"required"`
	AccountId     string                         `json:"account_id" mapstructure:"account_id" validate:"required"`
	K8sObjectType string                         `json:"k8s_object_type" mapstructure:"k8s_object_type" validate:"required"`
	K8sPermission security.K8sRbacPermissionType `json:"k8s_permission" mapstructure:"k8s_permission" validate:"required"`
}

type GetK8sObjectNamesResponse struct {
	Enabled     bool     `json:"enabled" mapstructure:"enabled"`
	ObjectNames []string `json:"object_names" mapstructure:"object_names"`
}

type GetSecurityContextRequest struct {
	TenantId string `json:"tenant_id" mapstructure:"tenant_id" validate:"required"`
	UserId   string `json:"user_id" mapstructure:"user_id" validate:"required"`
}

type GetSecurityContextResponse struct {
	Context *security.SecurityContext `json:"context" mapstructure:"context"`
}

type AttributeObject struct {
	Name     string `json:"name" mapstructure:"name" validate:"required"`
	Value    string `json:"value" mapstructure:"value" validate:"required"`
	TenantId string `json:"tenant_id,omitempty" mapstructure:"tenant_id"`
}

type TenantAttributeUpsertRequest struct {
	Object []AttributeObject `json:"object" mapstructure:"object" validate:"required"`
}

type ManageGroupUsersRequest struct {
	GroupId         string   `json:"group_id" mapstructure:"group_id" validate:"required"`
	AddUsernames    []string `json:"add_usernames" mapstructure:"add_usernames"`
	RemoveUsernames []string `json:"remove_usernames" mapstructure:"remove_usernames"`
}

type ManageGroupUsersResponse struct {
	Status  string `json:"status" mapstructure:"status"`
	Message string `json:"message" mapstructure:"message"`
}

type DeleteFeatureRequest struct {
	Name          string `json:"name" mapstructure:"name" validate:"required"`
	DeleteFeature bool   `json:"delete_feature" mapstructure:"delete_feature" validate:"required"`
}

type TenantUpdateNameRequest struct {
	Name string `json:"name" mapstructure:"name" validate:"required"`
}

type TenantUpdateNameResponse struct {
	Status  string `json:"status" mapstructure:"status"`
	Message string `json:"message" mapstructure:"message"`
}

type TenantAttributeDeleteRequest struct {
	Names []string `json:"names" mapstructure:"names" validate:"required"`
}

type TenantAttributeDeleteResponse struct {
	Status       string `json:"status" mapstructure:"status"`
	AffectedRows int64  `json:"affected_rows" mapstructure:"affected_rows"`
}

type FeatureFlagUpsertItem struct {
	FeatureId string `json:"feature_id" mapstructure:"feature_id" validate:"required"`
	Status    string `json:"status" mapstructure:"status" validate:"required"`
	AccountId string `json:"account_id" mapstructure:"account_id"`
}

type FeatureFlagUpsertRequest struct {
	Features []FeatureFlagUpsertItem `json:"features" mapstructure:"features" validate:"required"`
}

type FeatureFlagUpsertResponse struct {
	Status  string `json:"status" mapstructure:"status"`
	Message string `json:"message" mapstructure:"message"`
}

type UserGroupCreateRequest struct {
	Name        string `json:"name" mapstructure:"name" validate:"required"`
	Description string `json:"description" mapstructure:"description"`
}

type UserGroupCreateResponse struct {
	Id string `json:"id" mapstructure:"id"`
}

type UserGroupUpdateRequest struct {
	Id          string  `json:"id" mapstructure:"id" validate:"required"`
	Name        string  `json:"name" mapstructure:"name"`
	Description string  `json:"description" mapstructure:"description"`
	Role        *string `json:"role" mapstructure:"role"`
}

type UserGroupUpdateResponse struct {
	Status  string `json:"status" mapstructure:"status"`
	Message string `json:"message" mapstructure:"message"`
}

type IntegrationUpdateStatusByPkRequest struct {
	Id     string `json:"id" mapstructure:"id" validate:"required"`
	Status string `json:"status" mapstructure:"status" validate:"required"`
}

type IntegrationUpdateStatusByPkResponse struct {
	Id string `json:"id" mapstructure:"id"`
}

type NotificationRuleDeleteRequest struct {
	Id string `json:"id" mapstructure:"id" validate:"required"`
}

type NotificationRuleDeleteResponse struct {
	Id string `json:"id" mapstructure:"id"`
}

type NotificationChannelMappingCreateRequest struct {
	AccountId string `json:"account_id" mapstructure:"account_id"`
	Platform  string `json:"platform" mapstructure:"platform" validate:"required"`
	TeamId    string `json:"team_id" mapstructure:"team_id"`
	ChannelId string `json:"channel_id" mapstructure:"channel_id" validate:"required"`
}

type NotificationChannelMappingCreateResponse struct {
	Id        string `json:"id" mapstructure:"id" db:"id"`
	AccountId string `json:"account_id" mapstructure:"account_id" db:"account_id"`
	Platform  string `json:"platform" mapstructure:"platform" db:"platform"`
	TeamId    string `json:"team_id" mapstructure:"team_id" db:"team_id"`
	ChannelId string `json:"channel_id" mapstructure:"channel_id" db:"channel_id"`
	CreatedBy string `json:"created_by" mapstructure:"created_by" db:"created_by"`
	CreatedAt string `json:"created_at" mapstructure:"created_at" db:"created_at"`
}

type NotificationChannelMappingDeleteRequest struct {
	Id string `json:"id" mapstructure:"id" validate:"required"`
}

type NotificationChannelMappingDeleteResponse struct {
	Id string `json:"id" mapstructure:"id"`
}

type NotificationChannelMappingUpdateRequest struct {
	Id        string `json:"id" mapstructure:"id" validate:"required"`
	AccountId string `json:"account_id" mapstructure:"account_id"`
	TeamId    string `json:"team_id" mapstructure:"team_id"`
	ChannelId string `json:"channel_id" mapstructure:"channel_id"`
}

type NotificationChannelMappingUpdateResponse struct {
	Id        string `json:"id" mapstructure:"id" db:"id"`
	AccountId string `json:"account_id" mapstructure:"account_id" db:"account_id"`
	TeamId    string `json:"team_id" mapstructure:"team_id" db:"team_id"`
	ChannelId string `json:"channel_id" mapstructure:"channel_id" db:"channel_id"`
}

// Priority 3: Application Group models

type ApplicationGroupMappingItem struct {
	NamespaceName   string `json:"namespace_name" mapstructure:"namespace_name" validate:"required"`
	WorkloadName    string `json:"workload_name" mapstructure:"workload_name" validate:"required"`
	WorkloadKind    string `json:"workload_kind" mapstructure:"workload_kind" validate:"required"`
	AccountId       string `json:"account_id" mapstructure:"account_id" validate:"required"`
	CloudResourceId string `json:"cloud_resource_id" mapstructure:"cloud_resource_id"`
}

type ApplicationGroupCreateRequest struct {
	Name        string                        `json:"name" mapstructure:"name" validate:"required"`
	Description string                        `json:"description" mapstructure:"description"`
	Mappings    []ApplicationGroupMappingItem `json:"mappings" mapstructure:"mappings" validate:"required"`
}

type ApplicationGroupCreateResponse struct {
	Id string `json:"id" mapstructure:"id"`
}

type ApplicationGroupUpdateRequest struct {
	Id          string                        `json:"id" mapstructure:"id" validate:"required"`
	Name        string                        `json:"name" mapstructure:"name" validate:"required"`
	Description string                        `json:"description" mapstructure:"description"`
	Mappings    []ApplicationGroupMappingItem `json:"mappings" mapstructure:"mappings" validate:"required"`
}

type ApplicationGroupUpdateResponse struct {
	Id string `json:"id" mapstructure:"id"`
}

// Priority 3: Cloud Resource Attributes models

type CloudResourceAttributeItem struct {
	ResourceId string `json:"resource_id" mapstructure:"resource_id" validate:"required"`
	AccountId  string `json:"account_id" mapstructure:"account_id" validate:"required"`
	Name       string `json:"name" mapstructure:"name" validate:"required"`
	Value      string `json:"value" mapstructure:"value"`
	Labels     string `json:"labels" mapstructure:"labels"`
}

type CloudResourceAttributesUpsertRequest struct {
	Objects []CloudResourceAttributeItem `json:"objects" mapstructure:"objects" validate:"required"`
}

type CloudResourceAttributesUpsertResponse struct {
	AffectedRows int `json:"affected_rows" mapstructure:"affected_rows"`
}

// Priority 4: Tenant Onboarding models

type TenantOnboardingDeleteByUsernameRequest struct {
	Username string `json:"username" mapstructure:"username" validate:"required"`
}

type TenantOnboardingDeleteByUsernameResponse struct {
	AffectedRows int `json:"affected_rows" mapstructure:"affected_rows"`
}

type TenantOnboardingInsertRequest struct {
	Username                    string `json:"username" mapstructure:"username" validate:"required"`
	VerificationToken           string `json:"verification_token" mapstructure:"verification_token" validate:"required"`
	VerificationTokenExpiration string `json:"verification_token_expiration" mapstructure:"verification_token_expiration" validate:"required"`
	TenantName                  string `json:"tenant_name" mapstructure:"tenant_name"`
	UserDisplayname             string `json:"user_displayname" mapstructure:"user_displayname"`
}

type TenantOnboardingInsertResponse struct {
	Id string `json:"id" mapstructure:"id"`
}

type TenantOnboardingUpdateStatusRequest struct {
	Id        string `json:"id" mapstructure:"id" validate:"required"`
	Status    string `json:"status" mapstructure:"status" validate:"required"`
	UpdatedAt string `json:"updated_at" mapstructure:"updated_at" validate:"required"`
}

type TenantOnboardingUpdateStatusResponse struct {
	Id string `json:"id" mapstructure:"id"`
}

type TenantOnboardingGetByTokenRequest struct {
	Token string `json:"token" mapstructure:"token" validate:"required"`
}

type TenantOnboardingRecord struct {
	Id                          string  `json:"id" db:"id"`
	VerificationStatus          *string `json:"verification_status" db:"verification_status"`
	VerificationTokenExpiration *string `json:"verification_token_expiration" db:"verification_token_expiration"`
	Username                    *string `json:"username" db:"username"`
	TenantName                  *string `json:"tenant_name" db:"tenant_name"`
	UserDisplayname             *string `json:"user_displayname" db:"user_displayname"`
}

type TenantDeleteRequest struct {
	Id string `json:"id" mapstructure:"id" validate:"required"`
}

type TenantDeleteResponse struct {
	Id string `json:"id"`
}
