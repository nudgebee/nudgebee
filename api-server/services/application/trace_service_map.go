package application

import (
	"nudgebee/services/security"
	"nudgebee/services/traces"
)

// Legacy type aliases for backward compatibility
type ServiceApplication = traces.ServiceApplication
type ServiceApplicationId = traces.ServiceApplicationId
type ServiceCategory = traces.ServiceCategory
type Instance = traces.Instance
type UpstreamLink = traces.UpstreamLink
type DownstreamLink = traces.DownstreamLink
type LinkDrillDown = traces.LinkDrillDown
type TimeRange = traces.TimeRange
type ErrorSummary = traces.ErrorSummary
type StatusCodeStat = traces.StatusCodeStat
type OperationStat = traces.OperationStat
type FilterHints = traces.FilterHints
type ServiceMap = traces.ServiceMap
type TraceSpan = traces.TraceSpan
type SpanAttributes = traces.SpanAttributes
type ServiceDependency = traces.ServiceDependency
type ServiceMapConfig = traces.ServiceMapConfig
type TraceQueryParams = traces.TraceQueryParams

// Legacy constants for backward compatibility
const (
	DefaultQueryLimit         = traces.DefaultQueryLimit
	WorkloadQueryLimit        = traces.WorkloadQueryLimit
	MaxTraceIDsForExpansion   = traces.MaxTraceIDsForExpansion
	MaxTraceIDsInDrillDown    = traces.MaxTraceIDsInDrillDown
	DefaultFallbackDuration   = traces.DefaultFallbackDuration
	NanosecondsToMilliseconds = traces.NanosecondsToMilliseconds
)

// Legacy variables for backward compatibility
var DefaultApplicationTypes = traces.DefaultApplicationTypes

// Legacy builder aliases
type TraceServiceMapBuilder = traces.TraceServiceMapBuilder

func NewTraceServiceMapBuilder() *TraceServiceMapBuilder {
	return traces.NewTraceServiceMapBuilder()
}

func NewTraceServiceMapBuilderWithConfig(config *ServiceMapConfig) *TraceServiceMapBuilder {
	return traces.NewTraceServiceMapBuilderWithConfig(config)
}

// DefaultServiceMapConfig returns default configuration
func DefaultServiceMapConfig() *ServiceMapConfig {
	return traces.DefaultServiceMapConfig()
}

// FetchTracesAndBuildServiceMap fetches traces using existing query infrastructure and builds a service map
func FetchTracesAndBuildServiceMap(requestContext *security.RequestContext, params TraceQueryParams) (*ServiceMap, error) {
	return traces.FetchTracesAndBuildServiceMap(requestContext, params)
}
