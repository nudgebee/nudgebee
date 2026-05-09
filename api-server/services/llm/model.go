package llm

type ConversationApiRequest struct {
	Query          string         `json:"query" validate:"required"`
	ConversationId string         `json:"conversation_id,omitempty"`
	SessionId      string         `json:"session_id,omitempty"`
	AccountId      string         `json:"account_id" validate:"required"`
	UserId         string         `json:"user_id,omitempty"`
	MessageId      string         `json:"message_id,omitempty"`
	AgentId        string         `json:"agent_id,omitempty"`
	Async          bool           `json:"async,omitempty"`
	Config         map[string]any `json:"config,omitempty"`
	Source         string         `json:"source,omitempty"`
}

type ChatCompletionResponse struct {
	Response       []string `json:"response"`
	Query          string   `json:"query"`
	AgentName      string   `json:"agent_name"`
	ConversationId string   `json:"conversation_id"`
	SessionId      string   `json:"session_id"`
	Status         string   `json:"status"`
}
