package eventrule

import "nudgebee/services/eventrule/playbooks"

type EventConfig struct {
	Annotations struct {
		Description string `json:"description"`
		Summary     string `json:"summary"`
		Runbook     string `json:"runbook"`
	} `json:"annotations"`
	Expr   string `json:"expr"`
	Labels struct {
		Severity string `json:"severity"`
	} `json:"labels"`
	Alert                string                 `json:"alert"`
	Duration             string                 `json:"duration"`
	AccountID            string                 `json:"accountId"`
	Source               string                 `json:"source"`
	Category             string                 `json:"category"`
	Severity             string                 `json:"severity"`
	Enabled              bool                   `json:"enabled"`
	TriggerParams        []map[string]any       `json:"trigger_params"`
	ActionParams         []map[string]any       `json:"action_params"`
	AlertType            string                 `json:"alert_type"`
	MetricProvider       string                 `json:"metric_provider"`
	MetricProviderSource string                 `json:"metric_provider_source"`
	ProviderConfig       map[string]interface{} `json:"provider_config"`
}

type DisableEventConfig struct {
	Alert     string `json:"alert"`
	AccountID string `json:"accountId"`
	TenantID  string `json:"tenantId"`
	Id        string `json:"id"`
	Enable    bool   `json:"enable"`
	Namespace string `json:"namespace"`
	Group     string `json:"group"`
}

type ListAgentPlaybookRequest struct {
	CloudAccountId string `json:"account_id"`
	AlertName      string `json:"alert_name"`
}

type PlaybookActionExecutionResponse struct {
	Response        playbooks.PlaybookActionResponse `json:"response"`
	Error           error                            `json:"error"`
	ActionName      string                           `json:"action_name"`
	ActionArgs      map[string]any                   `json:"action_args"`
	ActionTitle     string                           `json:"action_title"`
	ActionCondition string                           `json:"action_condition"`
	DurationSeconds float64                          `json:"duration_seconds"`
}
