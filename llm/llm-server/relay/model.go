package relay

import "time"

type ActionExecuteBody struct {
	AccountID    string         `json:"account_id" validate:"required"`
	ActionName   string         `json:"action_name" validate:"required"`
	ActionParams map[string]any `json:"action_params"`
	Origin       string         `json:"origin,omitempty"`
	Timeout      time.Duration  `json:"timeout,omitempty"`
	AgentType    string         `json:"-"`
	// NoAgent marks an action the relay serves itself, without any agent
	// (direct-mode MCP), so Execute must not gate it on k8s agent connectivity.
	NoAgent bool `json:"-"`
	// Quiet logs Execute's transport and non-200 failures at Debug. Set by a
	// caller that records the failure itself and logs once when it matters
	// (MCP integration health, #40994).
	Quiet bool `json:"-"`
}

type RelayExecuteRequest struct {
	Body    ActionExecuteBody `json:"body" validate:"required"`
	NoSinks bool              `json:"no_sinks,omitempty"`
	Cache   bool              `json:"cache,omitempty"`
}
