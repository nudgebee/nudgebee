package core

type RefinementResponse struct {
	Question         string                 `json:"question"`
	RefinedQuestion  string                 `json:"refined_question"`
	Timeframe        string                 `json:"timeframe"`
	Resources        []ConversationResource `json:"resources"`
	RefinedResources []ConversationResource `json:"refined_resources"`
}

type ConversationResource struct {
	Type      string `json:"type"`
	Name      string `json:"name"`
	Namespace string `json:"namespace"`
}
