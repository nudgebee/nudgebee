package ml

type VerticalRightsizeBody struct {
	AccountID             string   `json:"account_id"`
	TenantID              string   `json:"tenant_id"`
	Namespace             string   `json:"namespace,omitempty"`
	ResourceNames         []string `json:"resource_names,omitempty"`
	PersistRecommendation bool     `json:"persist_recommendation"`
	BatchByNamespace      bool     `json:"batch_by_namespace"`
	MaxRecommendations    int      `json:"max_recommendations,omitempty"`
}

type VerticalRightSizeResponse struct {
	AccountID       string `json:"account_id"`
	TenantID        string `json:"tenant_id"`
	DatabaseStored  bool   `json:"database_stored"`
	Recommendations []any  `json:"recommendations"` // We don't need full details if we just log count
}
