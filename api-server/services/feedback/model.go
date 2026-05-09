package feedback

import "time"

type ConversationFeedbackRequest struct {
	SessionId             string `json:"session_id" validate:"required" db:"session_id,omitempty"`
	Module                string `json:"module" validate:"required" db:"module,omitempty"`
	Question              string `json:"question" validate:"required" db:"question,omitempty"`
	LlmResponse           string `json:"llm_response" validate:"required" db:"llm_response,omitempty"`
	UserCorrectedResponse string `json:"user_corrected_response" validate:"required" db:"user_corrected_response,omitempty"`
	AdditionalNotes       string `json:"additional_notes" validate:"required" db:"additional_notes,omitempty"`
	ConversationId        string `json:"conversation_id" validate:"required" db:"conversation_id,omitempty"`
	CloudAccountId        string `json:"cloud_account_id" validate:"required" db:"cloud_account_id,omitempty"`
	Useful                bool   `json:"useful" validate:"required" db:"useful,omitempty"`
}

type SaveOrDeleteConversationRequest struct {
	ConversationId string `json:"conversation_id" validate:"required" db:"conversation_id,omitempty"`
}

type DeleteConversationRequest struct {
	ConversationId string `json:"conversation_id" validate:"required" db:"conversation_id,omitempty"`
}

type LLMConversations struct {
	Id        string    `json:"id" validate:"required" db:"id"`
	UserId    string    `json:"user_id" validate:"required" db:"user_id"`
	Status    string    `json:"status" validate:"required" db:"status"`
	SessionId string    `json:"session_id" db:"session_id,omitempty"`
	Source    string    `json:"source" db:"source,omitempty"`
	AccountId string    `json:"account_id" db:"account_id,omitempty"`
	Context   string    `json:"context" db:"context,omitempty"`
	TenantId  string    `json:"tenant_id" db:"tenant_id,omitempty"`
	Title     string    `json:"title" db:"title,omitempty"`
	UpdatedAt time.Time `json:"updated_at" db:"updated_at"`
	CreatedAt time.Time `json:"created_at" db:"created_at"`
}
