package cloud

type CloudExecuteCliCommandRequest struct {
	AccountID string `json:"account_id" validate:"required"`
	TenantID  string `json:"tenant_id" validate:"required"`
	UserID    string `json:"user_id" validate:"required"`
	Command   string `json:"command" validate:"required"`
}
