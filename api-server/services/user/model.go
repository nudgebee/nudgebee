package user

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
}

type UserTokenCreateResponse struct {
	Name  string `json:"name" mapstructure:"name"`
	Token string `json:"token" mapstructure:"token"`
}

type UserTokenDeleteRequest struct {
	Name string `json:"name" mapstructure:"name"`
}

type UserTokenDeleteResponse struct {
	Name   string `json:"name" mapstructure:"name"`
	Status string `json:"status" mapstructure:"status"`
}

type UserAuthTokenResponse struct {
	Tokens []UserAuthToken `json:"tokens" mapstructure:"tokens"`
}

type UserAuthToken struct {
	Id         string `json:"id" db:"id"`
	Name       string `json:"name" db:"name"`
	Provider   string `json:"provider" db:"provider"`
	Status     string `json:"status" db:"status"`
	CreatedAt  string `json:"created_at" db:"created_at"`
	AccessedAt string `json:"accessed_at" db:"accessed_at"`
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
