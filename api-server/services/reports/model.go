package reports

type TenantReportRequestTenant struct {
	TenantId string   `json:"tenant_id" mapstructure:"tenant_id" validate:"required"`
	UserId   []string `json:"user_id" mapstructure:"user_id"`
}

type TenantReportRequest struct {
	Tenants []TenantReportRequestTenant `json:"tenants" mapstructure:"tenants" validate:"required"`
}
