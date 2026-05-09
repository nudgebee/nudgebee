package model

type ListWorkflowExecutionRequest struct {
	NextPageToken string                  `json:"next_page_token,omitempty"`
	Limit         int                     `json:"limit,omitempty"`
	Status        WorkflowExecutionStatus `json:"status,omitempty"`
	TriggerType   string                  `json:"trigger_type,omitempty"`
	TriggeredBy   string                  `json:"triggered_by,omitempty"`
	OrderBy       string                  `json:"order_by,omitempty"`
	OrderDir      string                  `json:"order_dir,omitempty"`
	Tags          map[string]string       `json:"tags,omitempty"`
}

type UpdateWorkflowExecutionRequest struct {
	Status string `json:"status"`
}
