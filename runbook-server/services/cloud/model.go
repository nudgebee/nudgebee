package cloud

type CloudExecuteCliCommandRequest struct {
	AccountID string `json:"account_id" validate:"required"`
	Command   string `json:"command" validate:"required"`
}
