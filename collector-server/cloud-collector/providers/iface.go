package providers

import (
	"context"
	"log/slog"
	"nudgebee/collector/cloud/security"
	"strings"
	"time"
)

type CloudProviderContext interface {
	GetContext() context.Context
	GetLogger() *slog.Logger
	GetSecurityContext() *security.SecurityContext
}

type QueryMetricsRequest struct {
	StartDate       *time.Time          `json:"start_date"`
	EndDate         *time.Time          `json:"end_date"`
	ResourceIds     []string            `json:"resource_ids"`
	ResourceType    string              `json:"resource_type"`
	ServiceName     string              `json:"service_name" validate:"required"`
	Region          string              `json:"region"`
	MetricNamespace string              `json:"metric_namespace"`
	MetricNames     []string            `json:"metric_names"`
	Step            time.Duration       `json:"step"`
	Dimensions      []map[string]string `json:"dimensions,omitempty"` // Added field for metric dimensions
	Statistics      []string            `json:"statistics"`
}

type Account struct {
	ID              string  `json:"id" mapstructure:"id"` // Nudgebee cloud account UUID
	AssumeRole      *string `json:"assume_role" mapstructure:"assume_role"`
	AccessKey       *string `json:"access_key" mapstructure:"access_key"`
	AccessSecret    *string `json:"access_secret" mapstructure:"access_secret"`
	Region          *string `json:"region" mapstructure:"region"`
	Data            *string `json:"data" mapstructure:"data"`
	AccountNumber   string  `json:"account_number" mapstructure:"account_number" validate:"required"`
	AccountName     string  `json:"account_name" mapstructure:"account_name" validate:"required"`
	CloudProvider   string  `json:"cloud_provider" mapstructure:"cloud_provider"`
	ParentAccountId *string `json:"parent_account_id" mapstructure:"parent_account_id"`
}

type GetUsageReportResponse struct {
	Items []UsageReportItem `json:"items"`
	Dates []time.Time       `json:"dates"`
}

type UsageReportCostCategory string

type UsageReportItem struct {
	ProductCode        string                  `json:"product_code" validate:"required"`
	ProductServiceCode string                  `json:"product_service_code" validate:"required"`
	ResourceRegionCode string                  `json:"resource_region_code"`
	ResourceType       string                  `json:"resource_type" validate:"required"`
	ResourceName       string                  `json:"resource_name"`
	ResourceId         string                  `json:"resource_id"`
	ResourceArn        string                  `json:"resource_arn"`
	ResourceOperation  string                  `json:"resource_operation"`
	ResourceTags       map[string][]string     `json:"resource_tags"`
	CostCategory       UsageReportCostCategory `json:"cost_category" validate:"required"`
	CostSubCategory    string                  `json:"cost_sub_category"`
	CostCurrency       string                  `json:"cost_currency"`
	Cost               float64                 `json:"cost"`
	ChargeType         string                  `json:"charge_type"`
	PublisherType      string                  `json:"publisher_type"`
	PricingModel       string                  `json:"pricing_model"`
	StartDate          time.Time               `json:"start_date" validate:"required"`
	EndDate            time.Time               `json:"end_date" validate:"required"`
}

const (
	UsageReportItemTypeUsage   UsageReportCostCategory = "Usage"
	UsageReportItemTypeTax     UsageReportCostCategory = "Tax"
	UsageReportItemTypeUnknown UsageReportCostCategory = "Unknown"
)

type MetricItem struct {
	Name        string      `json:"name"`
	Statistics  string      `json:"statistics"`
	ResourceId  string      `json:"resource_id"`
	Values      []float64   `json:"values"`
	Timestamps  []time.Time `json:"timestamps"`
	Region      string      `json:"region"`
	ServiceName string      `json:"service_name"`
}

type QueryMetricsResponse struct {
	Items     []MetricItem  `json:"items"`
	StartDate time.Time     `json:"start_date"`
	EndDate   time.Time     `json:"end_date"`
	Step      time.Duration `json:"step"`
}

type ResourceStatus string
type Resource struct {
	Id          string              `json:"id" mapstructure:"id" validate:"required"`
	Name        string              `json:"name" mapstructure:"name" validate:"required"`
	Type        string              `json:"type" mapstructure:"type" validate:"required"`
	Arn         string              `json:"arn" mapstructure:"arn"`
	ServiceName string              `json:"service_name" mapstructure:"service_name" validate:"required"`
	Status      ResourceStatus      `json:"status" mapstructure:"status" validate:"required"`
	Region      string              `json:"region" mapstructure:"region"`
	Tags        map[string][]string `json:"tags" mapstructure:"tags"`
	Meta        map[string]any      `json:"meta" mapstructure:"meta"`
	CreatedAt   time.Time           `json:"created_at" mapstructure:"created_at" validate:"required"`
}

const (
	ResourceStatusActive   ResourceStatus = "Active"
	ResourceStatusInactive ResourceStatus = "Inactive"
	ResourceStatusDeleted  ResourceStatus = "Deleted"
	ResourceStatusUnknown  ResourceStatus = "Unknown"
)

type RecommendationSeverity string

const (
	RecommendationSeverityCritical RecommendationSeverity = "Critical"
	RecommendationSeverityHigh     RecommendationSeverity = "High"
	RecommendationSeverityMedium   RecommendationSeverity = "Medium"
	RecommendationSeverityLow      RecommendationSeverity = "Low"
	RecommendationSeverityInfo     RecommendationSeverity = "Info"
)

func RecommendationSeverityFromString(severity string) RecommendationSeverity {
	switch strings.ToLower(severity) {
	case "critical":
		return RecommendationSeverityCritical
	case "high":
		return RecommendationSeverityHigh
	case "medium":
		return RecommendationSeverityMedium
	case "low":
		return RecommendationSeverityLow
	case "info", "informational":
		return RecommendationSeverityInfo
	default:
		return RecommendationSeverityInfo
	}
}

type RecommendationCategory string

const (
	RecommendationCategoryRightSizing   RecommendationCategory = "RightSizing"
	RecommendationCategorySecurity      RecommendationCategory = "Security"
	RecommendationCategoryInfraUpgrade  RecommendationCategory = "InfraUpgrade"
	RecommendationCategoryConfiguration RecommendationCategory = "Configuration"
)

type RecommendationAction string

const (
	RecommendationActionModify RecommendationAction = "Modify"
	RecommendationActionDelete RecommendationAction = "Delete"
)

type Recommendation struct {
	CategoryName        RecommendationCategory `json:"category_name" valdiate:"required"`
	RuleName            string                 `json:"rule_name" valdiate:"required"`
	Severity            RecommendationSeverity `json:"severity" valdiate:"required"`
	Savings             float64                `json:"savings"`
	Action              RecommendationAction   `json:"action"`
	Data                map[string]any         `json:"data"`
	ResourceServiceName string                 `json:"resource_service_name"  valdiate:"required"`
	ResourceId          string                 `json:"resource_id"  valdiate:"required"`
	ResourceType        string                 `json:"resource_type"  valdiate:"required"`
	ResourceRegion      string                 `json:"resource_region"  valdiate:"required"`
	ExternalResourceId  string                 `json:"external_resource_id,omitempty"` // Optional: actual resource's external_resource_id for linking to cloud_resourses
	DedupeGroup         string                 `json:"dedupe_group,omitempty"`         // Optional: collapses alternative recommendations for the same opportunity in aggregations (e.g. 8 Savings Plan variants for one workload)
}

type ListRecommendationsRequest struct {
	ServiceName string `json:"service_name"`
}

type ListSupportedRecommendationsResponse struct {
	ServiceName  string `json:"service_name"`
	CategoryName string `json:"category_name" validate:"required"`
	RuleName     string `json:"rule_name" validate:"required"`
}

type ListEventRequest struct {
	ServiceNames  []string   `json:"service_names"`
	StartDate     *time.Time `json:"start_date"`
	EndDate       *time.Time `json:"end_date"`
	ResourceIds   []string   `json:"resource_ids"`
	ExcludeEvents []string   `json:"exclude_events"`
}

type EventStatus string

const (
	EventStatusFiring   EventStatus = "FIRING"
	EventStatusResolved EventStatus = "RESOLVED"
	EventStatusClosed   EventStatus = "CLOSED"
)

type EventSeverity string

const (
	EventSeverityDebug  EventSeverity = "DEBUG"
	EventSeverityInfo   EventSeverity = "INFO"
	EventSeverityLow    EventSeverity = "LOW"
	EventSeverityMedium EventSeverity = "MEDIUM"
	EventSeverityHigh   EventSeverity = "HIGH"
)

func EventStatusFromString(status string) EventStatus {
	switch strings.ToUpper(status) {
	case "FIRING", "OPEN", "ACTIVE":
		return EventStatusFiring
	case "RESOLVED":
		return EventStatusResolved
	case "CLOSED", "CLOSE":
		return EventStatusClosed
	default:
		return EventStatusClosed
	}
}

func EventSeverityFromString(severity string) EventSeverity {
	switch strings.ToUpper(severity) {
	case "CRITICAL":
		return EventSeverityHigh
	case "ERROR", "HIGH":
		return EventSeverityHigh
	case "WARNING", "MEDIUM":
		return EventSeverityMedium
	case "INFO", "INFORMATIONAL":
		return EventSeverityInfo
	case "LOW":
		return EventSeverityLow
	case "DEBUG":
		return EventSeverityDebug
	default:
		return EventSeverityInfo // Default or handle as unknown
	}
}

type EventEvidenceType string

const (
	EventEvidenceTypeJson     EventEvidenceType = "json"
	EventEvidenceTypeMarkdown EventEvidenceType = "mmarkdown"
	EventEvidenceTypeTable    EventEvidenceType = "table"
	EventEvidenceTypeGz       EventEvidenceType = "gz"
	EventEvidenceTypeSvg      EventEvidenceType = "svg"
	EventEvidenceTypeLog      EventEvidenceType = "log"
	EventEvidenceTypeText     EventEvidenceType = "text"
	EventEvidenceTypeHtml     EventEvidenceType = "html"
	EventEvidenceTypeCsv      EventEvidenceType = "csv"
	EventEvidenceTypePdf      EventEvidenceType = "pdf"
)

type EventEvidence struct {
	Type           EventEvidenceType `json:"type"`
	Insight        []string          `json:"insight"`
	Data           string            `json:"data"`
	AdditionalInfo map[string]string `json:"additional_info"`
}

type Event struct {
	Title               string            `json:"title"`
	Description         string            `json:"description"`
	EventName           string            `json:"event_name"`
	Date                time.Time         `json:"date"`
	Username            string            `json:"username"`
	EventSource         string            `json:"event_source"`
	EventId             string            `json:"event_id"`
	FindingId           string            `json:"finding_id,omitempty"` // Source-native per-firing ID; if empty, computed from EventId+Date
	EventStatus         EventStatus       `json:"status"`
	EventSeverity       EventSeverity     `json:"severity"`
	ResourceType        string            `json:"resource_type"`
	ResourceId          string            `json:"resource_id"`
	ResourceRegion      string            `json:"resource_region"`
	ResourceServiceName string            `json:"resource_service_name"`
	Raw                 map[string]any    `json:"raw_event"`
	AdditionalContext   []EventEvidence   `json:"evidences,omitempty"`
	Labels              map[string]string `json:"labels,omitempty"`
}

type ListResourcesResponse struct {
	Items []Resource `json:"items"`
}

type ListRecommendationsResponse struct {
	Items []Recommendation `json:"items"`
}

type ListEventResponse struct {
	Items   []Event        `json:"items"`
	Summary []EventSummary `json:"summaries"`
}

type ListEventRules struct {
	Items []EventRule `json:"items"`
}

type EventDefinitionSeverity string

const (
	EventDefinitionSeverityWarning  EventDefinitionSeverity = "warning"
	EventDefinitionSeverityCritical EventDefinitionSeverity = "critical"
)

type EventRule struct {
	Name        string                  `json:"name" validate:"required"`
	Description string                  `json:"description"`
	Summary     string                  `json:"summary" validate:"required"`
	Expr        string                  `json:"expr" validate:"required"`
	Source      string                  `json:"source" validate:"required"`
	Category    string                  `json:"category"`
	Duration    time.Duration           `json:"duration"`
	Labels      map[string]string       `json:"labels"`
	Severity    EventDefinitionSeverity `json:"severity" validate:"required"`
}

type EventSummary struct {
	ServiceName      string `json:"service_name"`
	Region           string `json:"region"`
	ResourcesCreated int    `json:"resources_created"`
	ResourceDeleted  int    `json:"resources_deleted"`
	ResourceUpdated  int    `json:"resources_updated"`
}

type ApplyCommandRequest struct {
	ServiceName string         `json:"service_name" validate:"required"`
	Region      string         `json:"region" validate:"required"`
	ResourceId  string         `json:"resource_id" validate:"required"`
	Command     string         `json:"command" validate:"required"`
	Args        map[string]any `json:"args"`
}

type ApplyCommandResponse struct {
	Success bool   `json:"success"`
	Message string `json:"message"`
}

// QueryLogsRequest defines the input for querying logs.
type QueryLogsRequest struct {
	Region        string     `json:"region"`
	LogGroupName  string     `json:"log_group_name"`
	ServiceName   string     `json:"service_name"`
	ResourceId    string     `json:"resource_id"`
	QueryString   string     `json:"query_string"`
	StartTime     *time.Time `json:"start_time"`
	EndTime       *time.Time `json:"end_time"`
	Limit         *int64     `json:"limit"`
	LogMetricName string     `json:"log_metric_name"` // GCP: log-based metric ID whose filter to resolve and apply
	FilterPattern string     `json:"filter_pattern"`  // AWS: CloudWatch metric filter pattern for FilterLogEvents API
}

// LogLabel represents a single field in a log event.
type LogLabel struct {
	Label string `json:"label"`
	Value string `json:"value"`
}

type LogMessage struct {
	Message   string     `json:"message"`
	Timestamp int64      `json:"timestamp"`
	Labels    []LogLabel `json:"labels"`
}

// LogQueryStatistics provides statistics about the executed log query.
type LogQueryStatistics struct {
	RecordsMatched float64 `json:"records_matched"`
	RecordsScanned float64 `json:"records_scanned"`
	BytesScanned   float64 `json:"bytes_scanned"`
}

// QueryLogsResponse defines the output from querying logs.
type QueryLogsResponse struct {
	QueryId    string             `json:"query_id"` // ID of the query that was run
	Results    []LogMessage       `json:"results"`
	Status     string             `json:"status"` // e.g., Complete, Failed, Cancelled, Running, Scheduled
	Statistics LogQueryStatistics `json:"statistics"`
}

type ListResourceRequest struct {
	ServiceName string            `json:"service_name"`
	ResourceIds []string          `json:"resource_ids"`
	Regions     []string          `json:"regions"`
	Labels      map[string]string `json:"labels"`
}

type QueryServiceMapResourceRequest struct {
	ServiceName string `json:"service_name"`
	Resource    string `json:"resource"`
}

type QueryServiceMapRequest struct {
	Region    string `json:"region"`
	Resources []QueryServiceMapResourceRequest
}

type ServiceApplicationLink struct {
	Id            ServiceApplicationId `json:"Id"`
	Status        int                  `json:"Status,omitempty"`
	Latency       float64              `json:"Latency,omitempty"`
	RequestCount  float64              `json:"RequestCount,omitempty"`
	FailureCount  float64              `json:"FailureCount,omitempty"`
	Protocol      string               `json:"Protocol,omitempty"`
	BytesSent     float64              `json:"BytesSent,omitempty"`
	BytesReceived float64              `json:"BytesReceived,omitempty"`
}

// Key returns the string representation in K8s format "namespace:kind:name"
// For ARNs, extracts the resource identifier to avoid colon conflicts
func (a ServiceApplicationLink) Key() string {
	name := a.Id.Name

	// Extract resource identifier from ARN (strip arn:aws:service:region:account:resource/ prefix)
	if strings.HasPrefix(name, "arn:") {
		parts := strings.SplitN(name, ":", 6)
		if len(parts) == 6 {
			// parts[5] contains "loadbalancer/app/Demo-Frontend-ALB/9ef0c75b824fa80c"
			// or "targetgroup/Demo-Frontend-TG/6d4076c4e9596e84"
			name = parts[5]
		}
	}

	return strings.Join([]string{a.Id.Namespace, a.Id.Kind, name}, ":")
}

// ToUpstreamLink converts ServiceApplicationLink to UpstreamLink with string Id (K8s format)
func (s ServiceApplicationLink) ToUpstreamLink() UpstreamLink {
	return UpstreamLink{
		Id:            s.Id.Key(), // Convert to string format "name:kind:namespace"
		Status:        s.Status,
		Latency:       s.Latency,
		RequestCount:  s.RequestCount,
		FailureCount:  s.FailureCount,
		Protocol:      s.Protocol,
		BytesSent:     s.BytesSent,
		BytesReceived: s.BytesReceived,
	}
}

// ToDownstreamLink converts ServiceApplicationLink to DownstreamLink with object Id (K8s format)
func (s ServiceApplicationLink) ToDownstreamLink() DownstreamLink {
	return DownstreamLink(s)
}

// UpstreamLink represents an upstream dependency with Id as string (K8s format)
type UpstreamLink struct {
	Id            string  `json:"Id"`
	Status        int     `json:"Status,omitempty"`
	Latency       float64 `json:"Latency,omitempty"`
	RequestCount  float64 `json:"RequestCount,omitempty"`
	FailureCount  float64 `json:"FailureCount,omitempty"`
	Protocol      string  `json:"Protocol,omitempty"`
	BytesSent     float64 `json:"BytesSent,omitempty"`
	BytesReceived float64 `json:"BytesReceived,omitempty"`
}

// DownstreamLink represents a downstream dependency with Id as object (K8s format)
type DownstreamLink struct {
	Id            ServiceApplicationId `json:"Id"`
	Status        int                  `json:"Status,omitempty"`
	Latency       float64              `json:"Latency,omitempty"`
	RequestCount  float64              `json:"RequestCount,omitempty"`
	FailureCount  float64              `json:"FailureCount,omitempty"`
	Protocol      string               `json:"Protocol,omitempty"`
	BytesSent     float64              `json:"BytesSent,omitempty"`
	BytesReceived float64              `json:"BytesReceived,omitempty"`
}

type ServiceApplicationId struct {
	Name      string `json:"name"`
	Kind      string `json:"kind"`
	Namespace string `json:"namespace"`
}

// Key returns the string representation in K8s format "namespace:kind:name"
// For ARNs, extracts the resource identifier to avoid colon conflicts
func (a ServiceApplicationId) Key() string {
	name := a.Name

	// Extract resource identifier from ARN (strip arn:aws:service:region:account:resource/ prefix)
	if strings.HasPrefix(name, "arn:") {
		parts := strings.SplitN(name, ":", 6)
		if len(parts) == 6 {
			// parts[5] contains "loadbalancer/app/Demo-Frontend-ALB/9ef0c75b824fa80c"
			// or "targetgroup/Demo-Frontend-TG/6d4076c4e9596e84"
			name = parts[5]
		}
	}

	return strings.Join([]string{a.Namespace, a.Kind, name}, ":")
}

type ServiceMapApplication struct {
	Id          ServiceApplicationId `json:"Id"`
	Upstreams   []UpstreamLink       `json:"Upstreams"`
	Downstreams []DownstreamLink     `json:"Downstreams"`
	Status      string               `json:"Status"`
}

type QueryServiceMapResponse struct {
	Applications []ServiceMapApplication `json:"applications"`
	Errors       []string                `json:"errors"`
}

type ListMetricsRequest struct {
	ServiceName  string `json:"service_name" validate:"required"`
	ResourceType string `json:"resource_type"`
	Region       string `json:"region"`
	ResourceId   string `json:"resource_id"` // Optional: used by Azure for dynamic metric discovery
}

type ListMetricsResponse struct {
	Metrics []AvailableMetric `json:"metrics"`
}

type AvailableMetric struct {
	Name       string            `json:"name"`
	Namespace  string            `json:"namespace"`
	Statistics []string          `json:"statistics,omitempty"`
	Attributes map[string]string `json:"attributes,omitempty"`
}

type CloudProvider interface {
	Name() string
	QueryServiceMap(ctx CloudProviderContext, account Account, query QueryServiceMapRequest) (QueryServiceMapResponse, error)
	QueryLogs(ctx CloudProviderContext, account Account, query QueryLogsRequest) (QueryLogsResponse, error)
	GetUsageReport(ctx CloudProviderContext, account Account, month time.Month, year int) (GetUsageReportResponse, error)
	QueryMetrices(ctx CloudProviderContext, account Account, qury QueryMetricsRequest) (QueryMetricsResponse, error)
	ListMetrics(ctx CloudProviderContext, account Account, request ListMetricsRequest) (ListMetricsResponse, error)
	ListResources(ctx CloudProviderContext, account Account, query ListResourceRequest) (ListResourcesResponse, error)
	ListSupportedRecommendations(ctx CloudProviderContext) []ListSupportedRecommendationsResponse
	ListRecommendations(ctx CloudProviderContext, account Account, filter ListRecommendationsRequest, existingResources []Resource) (ListRecommendationsResponse, error)
	ListEvents(ctx CloudProviderContext, account Account, query ListEventRequest) (ListEventResponse, error)
	ApplyRecommendation(ctx CloudProviderContext, account Account, recommendation Recommendation) error
	ApplyCommand(ctx CloudProviderContext, account Account, command ApplyCommandRequest) (ApplyCommandResponse, error)
	ExecuteCliCommand(ctx CloudProviderContext, account Account, command string) (string, error)
	ListEventRules(ctx CloudProviderContext, account Account) (ListEventRules, error)
	// QueryDatabasePerformance fetches database performance insights (AWS RDS, GCP Cloud SQL, Azure SQL Database)
	QueryDatabasePerformance(ctx CloudProviderContext, account Account, request DatabasePerformanceRequest) (DatabasePerformanceResponse, error)
}

// UsageReportPeriod identifies a single billing period (month) for which a
// usage/cost report is available.
type UsageReportPeriod struct {
	Month time.Month
	Year  int
}

// UsageReportPeriodDiscoverer is an optional capability implemented only by
// providers that can enumerate the historical billing periods already present
// in their usage-report storage (e.g. AWS CUR folders in S3). It is kept
// separate from CloudProvider so providers without this capability (Azure, GCP)
// don't need to implement it.
type UsageReportPeriodDiscoverer interface {
	// GetAvailableUsageReportPeriods returns the billing periods that have data
	// available, limited to at most the last maxMonths trailing months
	// (including the current month).
	GetAvailableUsageReportPeriods(ctx CloudProviderContext, account Account, maxMonths int) ([]UsageReportPeriod, error)
}

// ProcessedEventHandler defines an interface for components that handle events
// after they have been processed by a provider (e.g., from SQS).
type ProcessedEventHandler interface {
	// ProcessEvent handles the storage and notification of a processed event.
	ProcessEvent(pCtx CloudProviderContext, event Event, originatingAccount Account) error
	// GetAccountFromCloudProviderAccountId fetches the account details (including credentials/role for actions)
	// for a given cloud provider account number (e.g., AWS Account ID).
	GetAccountFromCloudProviderAccountId(pCtx CloudProviderContext, awsAccountNumber string) (Account, error)
	// GetAccountFromExternalId fetches the account details using external_id (nudgebeeAccountToken) and account number
	// This provides tenant-safe account resolution for events with token-based routing (GCP, Azure)
	GetAccountFromExternalId(pCtx CloudProviderContext, externalId string, accountNumber string) (Account, error)
}
