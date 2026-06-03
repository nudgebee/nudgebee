package insight

type InsightSource string

const (
	InsightSourceMetric         InsightSource = "Metric"
	InsightSourceRecommendation InsightSource = "Recommendation"
	InsightSourceQuery          InsightSource = "Query"
	InsightSourceEvent          InsightSource = "Event"
	InsightSourcePrometheus     InsightSource = "Prometheus"
	InsightSourceSecurity       InsightSource = "Security"
	InsightSourceSpends         InsightSource = "Spends"
)

type InsightType string

const (
	InsightTypeDiff             InsightType = "Diff"
	InsightTypeAddition         InsightType = "Addition"
	InsightTypeColumnDiff       InsightType = "Column Diff"
	InsightTypeRatio            InsightType = "Ratio"
	InsightTypePrometheus       InsightType = "Prometheus"
	InsightTypeEventAggregation InsightType = "EventAggregation"
	InsightTypeTraceAggregation InsightType = "TraceAggregation"
)

type InsightStatus string

const (
	InsightStatusOpen   InsightStatus = "Open"
	InsightStatusClosed InsightStatus = "Closed"
)

type InsightRangeUnit string

const (
	InsightRangeUnitHour  InsightRangeUnit = "HOUR"
	InsightRangeUnitDay   InsightRangeUnit = "DAY"
	InsightRangeUnitWeek  InsightRangeUnit = "WEEK"
	InsightRangeUnitMonth InsightRangeUnit = "MONTH"
)

type InsightCategory string

const (
	Troubleshooting InsightCategory = "Troubleshooting"
	Optimization    InsightCategory = "Optimization"
	Security        InsightCategory = "Security"
	Ops             InsightCategory = "Ops"
)

type InsightSeverity string

const (
	InsightSeverityLow      InsightSeverity = "Low"
	InsightSeverityMedium   InsightSeverity = "Medium"
	InsightSeverityHigh     InsightSeverity = "High"
	InsightSeverityCritical InsightSeverity = "Critical"
)

type InsightFilters struct {
	Column   string      `json:"column"`
	Value    interface{} `json:"value"`
	Operator string      `json:"operator"`
}

type InsightUIFilters struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

type InsightRule struct {
	UniqueID             string             `json:"unique_id"`
	InsightFormat        string             `json:"insight_format"`
	Type                 InsightType        `json:"type"`
	Source               InsightSource      `json:"source"`
	Range                int                `json:"range"`
	RangeUnit            InsightRangeUnit   `json:"range_unit"`
	Threshold            float64            `json:"threshold"`
	GroupedBy            []string           `json:"grouped_by"`
	Filters              []InsightFilters   `json:"filters"`
	Distinct             string             `json:"distinct"`
	ResourceIDColumnName string             `json:"resource_id_column_name"`
	With                 string             `json:"with"`
	ViewName             string             `json:"view_name"`
	AggregateColumn      string             `json:"aggregate_column"`
	AccountColumnName    string             `json:"account_column"`
	Query                string             `json:"query"`
	InsightCategory      InsightCategory    `json:"category"`
	InsightSubCategory   string             `json:"subcategory"`
	InsightUIFilters     []InsightUIFilters `json:"ui_filters"`
	Severity             InsightSeverity    `json:"severity"`
	Instant              bool               `json:"instant"`
	CloudProviders       []string           `json:"cloud_providers"`
	RedirectURL          string             `json:"redirect_url,omitempty"`
}

type RelevantApplications struct {
	Name      string `json:"name"`
	Namespace string `json:"namespace"`
	// EventID is the representative (most recent) event id for this application.
	// Populated only for Event-source (EventAggregation) insights.
	EventID string `json:"event_id,omitempty"`
}

type Insight struct {
	Title        string                 `json:"title"`
	Type         InsightCategory        `json:"type"`
	Source       InsightSource          `json:"source"`
	AccountID    string                 `json:"account_id"`
	Tenant       string                 `json:"tenant"`
	UniqueID     string                 `json:"unique_id"`
	ResourceID   string                 `json:"resource_id,omitempty"`
	Status       InsightStatus          `json:"status"`
	Severity     InsightSeverity        `json:"severity"`
	Rule         InsightRule            `json:"rule"`
	Applications []RelevantApplications `json:"applications,omitempty"`
}

type InsightListRequest struct {
	AccountId string `json:"account_id"`
}

type InsightListResponse struct {
	Title        string                 `json:"title"`
	Source       string                 `json:"source"`
	Rule         InsightRule            `json:"rule"`
	Applications []RelevantApplications `json:"applications,omitempty"`
	Type         InsightCategory        `json:"type"`
}
