package user

import "github.com/lib/pq"

type UserCreateRequest struct {
	Username   string `json:"username" mapstructure:"username" validate:"required"`
	Firstname  string `json:"firstname" mapstructure:"firstname" validate:"required"`
	Lastname   string `json:"lastname" mapstructure:"lastname"`
	Role       string `json:"role" mapstructure:"role"`
	Tenantname string `json:"tenantname" mapstructure:"tenantname"`
}

type UserCreateResponse struct {
	Id       string `json:"id" mapstructure:"id"`
	Status   string `json:"status" mapstructure:"status"`
	Message  string `json:"message" mapstructure:"message"`
	TenantId string `json:"tenant_id" mapstructure:"tenant_id"`
}

type UserDeleteRequest struct {
	Id string `json:"id" mapstructure:"id"`
}

type UserDeleteResponse struct {
	Id string `json:"id" mapstructure:"id"`
}

type UserTokenCreateRequest struct {
	Name string `json:"name" mapstructure:"name"`
	// Kind is platform | gateway | all; empty defaults to all.
	Kind string `json:"kind" mapstructure:"kind"`
	// ExpiresAt is RFC3339; nil or "" means the key never expires.
	ExpiresAt *string `json:"expires_at" mapstructure:"expires_at"`
	// Limits need a gateway-reaching key; 400 on a platform key.
	Limits []TokenLimit `json:"limits" mapstructure:"limits"`
	// Scopes restrict a platform or all key's app-API calls to `<module>:<Read|Write|Execute>`
	// keys; empty means the owner's full access. 400 on a gateway key.
	Scopes []string `json:"scopes" mapstructure:"scopes"`
	// GatewayPolicy restricts a gateway or scoped all key's providers, models and endpoint
	// families; nil means unrestricted. 400 on a platform or unscoped all key.
	GatewayPolicy *GatewayPolicy `json:"gateway_policy" mapstructure:"gateway_policy"`
}

// GatewayPolicy narrows what an AI Gateway key may reach. A nil list leaves that
// dimension unrestricted; an empty list is rejected, since it would allow nothing.
type GatewayPolicy struct {
	Providers []string `json:"providers" mapstructure:"providers"`
	Models    []string `json:"models" mapstructure:"models"`
	Endpoints []string `json:"endpoints" mapstructure:"endpoints"`
}

type UserTokenCreateResponse struct {
	Name      string   `json:"name" mapstructure:"name"`
	Token     string   `json:"token" mapstructure:"token"`
	Kind      string   `json:"kind" mapstructure:"kind"`
	ExpiresAt *string  `json:"expires_at" mapstructure:"expires_at"`
	Scopes    []string `json:"scopes" mapstructure:"scopes"`
	// GatewayPolicy echoes the stored policy; nil when the key is unrestricted.
	GatewayPolicy *GatewayPolicy `json:"gateway_policy" mapstructure:"gateway_policy"`
}

type UserTokenDeleteRequest struct {
	// Id is preferred. Name is kept for back-compat with clients that predate the
	// kind column, which made (user, name) ambiguous.
	Id   string `json:"id" mapstructure:"id"`
	Name string `json:"name" mapstructure:"name"`
}

type UserTokenDeleteResponse struct {
	Name   string `json:"name" mapstructure:"name"`
	Status string `json:"status" mapstructure:"status"`
}

type UserTokenUpdateRequest struct {
	Id        string       `json:"id" mapstructure:"id"`
	ExpiresAt *string      `json:"expires_at" mapstructure:"expires_at"`
	Limits    []TokenLimit `json:"limits" mapstructure:"limits"`
	// Omitted (nil) leaves the policy alone; an object replaces it, and one with no lists
	// makes the key unrestricted again.
	GatewayPolicy *GatewayPolicy `json:"gateway_policy" mapstructure:"gateway_policy"`
}

type UserTokenUpdateResponse struct {
	Id     string `json:"id" mapstructure:"id"`
	Status string `json:"status" mapstructure:"status"`
}

type UserAuthTokenResponse struct {
	Tokens []UserAuthToken `json:"tokens" mapstructure:"tokens"`
}

type UserAuthToken struct {
	Id         string         `json:"id" db:"id"`
	Name       string         `json:"name" db:"name"`
	Provider   string         `json:"provider" db:"provider"`
	Kind       string         `json:"kind" db:"kind"`
	Status     string         `json:"status" db:"status"`
	CreatedAt  string         `json:"created_at" db:"created_at"`
	AccessedAt *string        `json:"accessed_at" db:"accessed_at"`
	ExpiresAt  *string        `json:"expires_at" db:"expires_at"`
	Scopes     pq.StringArray `json:"scopes" db:"scopes"`
	// GatewayPolicy is folded from llm_gateway_access_policies; nil when unrestricted.
	GatewayPolicy *GatewayPolicy `json:"gateway_policy"`
	Limits        []TokenLimit   `json:"limits"`
	Usage         *TokenUsage    `json:"usage"`
}

// TokenUsage is a gateway key's consumption in the current calendar month.
type TokenUsage struct {
	Requests int64   `json:"requests"`
	Tokens   int64   `json:"tokens"`
	CostUsd  float64 `json:"cost_usd"`
}

type UserTenantRolesRequest struct {
	Username string `json:"username" mapstructure:"username" validate:"required"`
	TenantId string `json:"tenant_id" mapstructure:"tenant_id" validate:"required"`
}

type UserTenantRole struct {
	EntityId   string `json:"entity_id" db:"entity_id"`
	EntityType string `json:"entity_type" db:"entity_type"`
	Role       string `json:"role" db:"role"`
}

type UserTenantRolesResponse struct {
	Roles      []UserTenantRole `json:"roles"`
	TenantName string           `json:"tenant_name"`
}

type UserSyncRolesRequest struct {
	Username       string   `json:"username" mapstructure:"username" validate:"required"`
	TenantId       string   `json:"tenant_id" mapstructure:"tenant_id" validate:"required"`
	TargetRoles    []string `json:"target_roles" mapstructure:"target_roles" validate:"required"`
	RemoveOldRoles bool     `json:"remove_old_roles" mapstructure:"remove_old_roles"`
}

type UserSyncRolesResponse struct {
	Added   int `json:"added"`
	Removed int `json:"removed"`
}

type UserUpdateAccessedRequest struct {
	AuthId   string `json:"auth_id" mapstructure:"auth_id"`
	Username string `json:"username" mapstructure:"username"`
	TenantId string `json:"tenant_id" mapstructure:"tenant_id" validate:"required"`
	// TokenSha256, when set (auth_id path only), lazily backfills the sha256 of a
	// pre-existing Personal Access Token so the gateway can resolve it by value.
	TokenSha256 string `json:"token_sha256" mapstructure:"token_sha256"`
}

type UserUpdateAccessedResponse struct {
	Updated int `json:"updated"`
}

type UserOnboardRequest struct {
	Username       string   `json:"username" mapstructure:"username" validate:"required"`
	DisplayName    string   `json:"display_name" mapstructure:"display_name" validate:"required"`
	Status         string   `json:"status" mapstructure:"status"`
	TenantName     string   `json:"tenant_name" mapstructure:"tenant_name"`
	TenantId       string   `json:"tenant_id" mapstructure:"tenant_id"`
	Role           string   `json:"role" mapstructure:"role"`
	Groups         []string `json:"groups" mapstructure:"groups"`
	ExistingUserId string   `json:"existing_user_id" mapstructure:"existing_user_id"`
}

type UserOnboardResponse struct {
	Id      string `json:"id"`
	Status  string `json:"status"`
	Message string `json:"message"`
}

type UserCreateAuthRequest struct {
	UserId       string `json:"user" mapstructure:"user" validate:"required"`
	Provider     string `json:"provider" mapstructure:"provider" validate:"required"`
	ProviderType string `json:"provider_type" mapstructure:"provider_type" validate:"required"`
	AccountId    string `json:"account_id" mapstructure:"account_id" validate:"required"`
	Name         string `json:"name" mapstructure:"name" validate:"required"`
	Credential   string `json:"credential" mapstructure:"credential"`
	Status       string `json:"status" mapstructure:"status"`
	AccessedAt   string `json:"accessed_at" mapstructure:"accessed_at"`
	ExpiresAt    string `json:"expires_at" mapstructure:"expires_at"`
}

type UserCreateAuthResponse struct {
	Id         string `json:"id"`
	Name       string `json:"name"`
	UserStatus string `json:"user_status"`
	UserID     string `json:"user_id"`
}

type UserUpdateStatusRequest struct {
	UserId string `json:"id" mapstructure:"id" validate:"required"`
	Status string `json:"status" mapstructure:"status" validate:"required"`
}

type UserUpdateStatusResponse struct {
	Id string `json:"id"`
}

type UserDeleteAuthRequest struct {
	Id string `json:"id" mapstructure:"id" validate:"required"`
}

type UserDeleteAuthResponse struct {
	Id string `json:"id"`
}

type UserUpdateDefaultTenantRequest struct {
	TenantId string `json:"tenant_id" mapstructure:"tenant_id" validate:"required"`
	Username string `json:"username" mapstructure:"username" validate:"required"`
}

type UserUpdateDefaultTenantResponse struct {
	Updated int `json:"updated"`
}

type UserUpdateProfileRequest struct {
	Username    string  `json:"username" mapstructure:"username" validate:"required"`
	DisplayName string  `json:"display_name" mapstructure:"display_name"`
	Status      string  `json:"status" mapstructure:"status"`
	Role        *string `json:"role" mapstructure:"role"`
}

type UserUpdateProfileResponse struct {
	Status  string `json:"status"`
	Message string `json:"message"`
}
