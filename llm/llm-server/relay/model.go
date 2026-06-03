package relay

import "time"

type ActionExecuteBody struct {
	AccountID    string         `json:"account_id" validate:"required"`
	ActionName   string         `json:"action_name" validate:"required"`
	ActionParams map[string]any `json:"action_params"`
	Origin       string         `json:"origin,omitempty"`
	Timeout      time.Duration  `json:"timeout,omitempty"`
	AgentType    string         `json:"-"`
}

type RelayExecuteRequest struct {
	Body    ActionExecuteBody `json:"body" validate:"required"`
	NoSinks bool              `json:"no_sinks,omitempty"`
	Cache   bool              `json:"cache,omitempty"`
}
