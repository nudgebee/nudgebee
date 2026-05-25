package account

import "time"

type StoreUsageReportResponse struct {
	Count               int                                         `json:"count"`
	ResourceCount       int                                         `json:"resource_count"`
	SpendCount          int                                         `json:"spend_count"`
	Recommendations     map[string]StoreUsageRecommendationResponse `json:"recommendations"`
	DiscoveredResources map[string]StoreUsageResourceResponse       `json:"discovered_resources"`
	Duration            time.Duration                               `json:"duration"`
}

type StoreUsageResourceResponse struct {
	Data    StoreResourcesResponse `json:"data"`
	Regions []string               `json:"regions"`
	Error   string                 `json:"error"`
}

type StoreUsageRecommendationResponse struct {
	Data  StoreRecommendationResponse `json:"data"`
	Error string                      `json:"error"`
}

type StoreResourcesResponse struct {
	Count    int           `json:"count"`
	Arns     []string      `json:"arns"`
	Duration time.Duration `json:"duration"`
	Errors   []string      `json:"errors"`
}

type StoreRecommendationResponse struct {
	Count    int           `json:"count"`
	Duration time.Duration `json:"duration"`
	Errors   []string      `json:"errors"`
}

type StoreEventResponse struct {
	Count    int           `json:"count"`
	Duration time.Duration `json:"duration"`
	Start    time.Time     `json:"start"`
	End      time.Time     `json:"end"`
}

type AgentStatus string

const (
	AgentStatusConnected    AgentStatus = "CONNECTED"
	AgentStatusDisconnected AgentStatus = "NOT_CONNECTED"
)

type StoreMetricesRequest struct {
	ServiceName string   `json:"service_name" validate:"required"`
	Regions     []string `json:"regions"`
	StartDate   string   `json:"start_date"`
	EndDate     string   `json:"end_date"`
}

type StoreMetricesResponse struct {
	Count    int           `json:"count"`
	Duration time.Duration `json:"duration"`
}
