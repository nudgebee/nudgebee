package event

import "time"

// EventFilterType defines the types of filters available for events
type EventFilterType string

const (
	EventFilterTypeNamespace      EventFilterType = "namespace"       // subject_namespace
	EventFilterTypeWorkload       EventFilterType = "workload"        // subject_owner
	EventFilterTypeSubjectType    EventFilterType = "subject_type"    // subject_type
	EventFilterTypeAggregationKey EventFilterType = "aggregation_key" // aggregation_key (event types)
	EventFilterTypeSource         EventFilterType = "source"          // source
	EventFilterTypePriority       EventFilterType = "priority"        // priority
	EventFilterTypeNBStatus       EventFilterType = "nb_status"       // nb_status (triage status)
	EventFilterTypeCluster        EventFilterType = "cluster"         // cluster
	EventFilterTypeLabelKey       EventFilterType = "label_key"       // JSONB label keys
	EventFilterTypeLabelValue     EventFilterType = "label_value"     // JSONB label values for a specific key
)

// validFilterTypes maps filter types for validation
var validFilterTypes = map[EventFilterType]bool{
	EventFilterTypeNamespace:      true,
	EventFilterTypeWorkload:       true,
	EventFilterTypeSubjectType:    true,
	EventFilterTypeAggregationKey: true,
	EventFilterTypeSource:         true,
	EventFilterTypePriority:       true,
	EventFilterTypeNBStatus:       true,
	EventFilterTypeCluster:        true,
	EventFilterTypeLabelKey:       true,
	EventFilterTypeLabelValue:     true,
}

// IsValidFilterType checks if the filter type is valid
func IsValidFilterType(ft EventFilterType) bool {
	return validFilterTypes[ft]
}

// GetEventFilterValuesRequest represents the request to get filter values
type GetEventFilterValuesRequest struct {
	// AccountID is optional - if not provided, aggregate across all user-accessible accounts
	AccountID *string `json:"account_id,omitempty" mapstructure:"account_id"`

	// FilterTypes is the list of filter types to fetch (required, at least one)
	FilterTypes []EventFilterType `json:"filter_types" mapstructure:"filter_types" validate:"required,min=1"`

	// LabelKey is required when filter_types includes "label_value"
	LabelKey *string `json:"label_key,omitempty" mapstructure:"label_key"`

	// TimeRange for filtering events (optional)
	StartTime *time.Time `json:"start_time,omitempty" mapstructure:"start_time"`
	EndTime   *time.Time `json:"end_time,omitempty" mapstructure:"end_time"`

	// Limit maximum number of values per filter type (default: 500, max: 1000)
	Limit *int `json:"limit,omitempty" mapstructure:"limit"`

	// IncludeCount includes event counts per filter value (default: false)
	// When false, queries use SELECT DISTINCT which is faster
	IncludeCount *bool `json:"include_count,omitempty" mapstructure:"include_count"`
}

// FilterValueItem represents a single filter value with optional count
type FilterValueItem struct {
	Value string `json:"value"`
	Count int64  `json:"count,omitempty"`
}

// FilterResult represents the result for a single filter type
type FilterResult struct {
	FilterType EventFilterType   `json:"filter_type"`
	Values     []FilterValueItem `json:"values"`
	Total      int               `json:"total"`
}

// GetEventFilterValuesResponse represents the response
type GetEventFilterValuesResponse struct {
	Filters   []FilterResult `json:"filters"`
	AccountID *string        `json:"account_id,omitempty"`
}
