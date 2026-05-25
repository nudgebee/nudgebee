package conversation

import "time"

// GetConversationDeltaRequest carries the inputs for ai_get_conversation_v3.
//
// One of ConversationId / SessionId must be set. Since is the cursor for delta
// fetches; nil/zero value means "return everything for this conversation"
// (initial load).
type GetConversationDeltaRequest struct {
	AccountId      string     `json:"account_id"`
	ConversationId string     `json:"conversation_id,omitempty"`
	SessionId      string     `json:"session_id,omitempty"`
	Since          *time.Time `json:"since,omitempty"`
}

// GetConversationDeltaResponse is the flat-array response shape. Frontend
// merges arrays into local state keyed by id and rebuilds the message → agent
// → tool_call tree client-side.
type GetConversationDeltaResponse struct {
	Conversation *ConversationShell `json:"conversation"`
	Messages     []Message          `json:"messages"`
	Agents       []Agent            `json:"agents"`
	ToolCalls    []ToolCall         `json:"tool_calls"`
	// Cursor is max(updated_at) across all rows in this response. Frontend
	// stores it and sends it back as `since` on the next poll.
	Cursor time.Time `json:"cursor"`
}

type ConversationShell struct {
	Id              string    `json:"id" db:"id"`
	SessionId       string    `json:"session_id" db:"session_id"`
	AccountId       string    `json:"account_id" db:"account_id"`
	TenantId        string    `json:"tenant_id" db:"tenant_id"`
	UserId          *string   `json:"user_id" db:"user_id"`
	UserDisplayName *string   `json:"user_display_name" db:"user_display_name"`
	CreatedAt       time.Time `json:"created_at" db:"created_at"`
	UpdatedAt       time.Time `json:"updated_at" db:"updated_at"`
	Source          *string   `json:"source" db:"source"`
	Context         *string   `json:"context" db:"context"`
	Status          *string   `json:"status" db:"status"`
	Title           *string   `json:"title" db:"title"`
}

type Message struct {
	Id              string    `json:"id" db:"id"`
	UserId          *string   `json:"user_id" db:"user_id"`
	UserDisplayName *string   `json:"user_display_name" db:"user_display_name"`
	CreatedAt       time.Time `json:"created_at" db:"created_at"`
	UpdatedAt       time.Time `json:"updated_at" db:"updated_at"`
	Message         string    `json:"message" db:"message"`
	MessageType     *string   `json:"message_type" db:"message_type"`
	Response        *string   `json:"response" db:"response"`
	Role            *string   `json:"role" db:"role"`
	Status          *string   `json:"status" db:"status"`
	ParentAgentId   *string   `json:"parent_agent_id" db:"parent_agent_id"`
	MessageConfig   *string   `json:"message_config" db:"message_config"`
	AckMessage      *string   `json:"ack_message" db:"ack_message"`
}

type Agent struct {
	Id              string    `json:"id" db:"id"`
	MessageId       string    `json:"message_id" db:"message_id"`
	AgentName       *string   `json:"agent_name" db:"agent_name"`
	Response        *string   `json:"response" db:"response"`
	ResponseSummary *string   `json:"response_summary" db:"response_summary"`
	Query           *string   `json:"query" db:"query"`
	Thought         *string   `json:"thought" db:"thought"`
	ParentAgentId   *string   `json:"parent_agent_id" db:"parent_agent_id"`
	Status          *string   `json:"status" db:"status"`
	References      *string   `json:"references" db:"references"`
	CreatedAt       time.Time `json:"created_at" db:"created_at"`
	UpdatedAt       time.Time `json:"updated_at" db:"updated_at"`
}

type ToolCall struct {
	Id           string    `json:"id" db:"id"`
	AgentId      string    `json:"agent_id" db:"agent_id"`
	ToolName     string    `json:"tool_name" db:"tool_name"`
	Parameters   *string   `json:"parameters" db:"parameters"`
	Response     *string   `json:"response" db:"response"`
	Thought      *string   `json:"thought" db:"thought"`
	ToolType     *string   `json:"tool_type" db:"tool_type"`
	ChildAgentId *string   `json:"child_agent_id" db:"child_agent_id"`
	References   *string   `json:"references" db:"references"`
	ToolId       *string   `json:"tool_id" db:"tool_id"`
	Status       *string   `json:"status" db:"status"`
	CreatedAt    time.Time `json:"created_at" db:"created_at"`
	UpdatedAt    time.Time `json:"updated_at" db:"updated_at"`
}
