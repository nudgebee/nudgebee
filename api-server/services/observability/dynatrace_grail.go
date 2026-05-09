package observability

import (
	"nudgebee/services/integrations"
)

// ---------------------------------------------------------------------------
// Grail DQL executor — thin wrapper around integrations.ExecuteDQLQuery.
//
// The canonical implementation lives in integrations/dynatrace_grail.go to
// avoid a circular dependency (observability already imports integrations).
// ---------------------------------------------------------------------------

// grailResult is a package-local alias for integrations.GrailResult so that
// all existing observability callers compile without change.
type grailResult = integrations.GrailResult

// grailMetadata is a package-local alias for integrations.GrailMetadata.
type grailMetadata = integrations.GrailMetadata

// grailMetricMeta is a package-local alias for integrations.GrailMetricMeta.
type grailMetricMeta = integrations.GrailMetricMeta

// executeDQLQuery runs an async Dynatrace Grail DQL query and returns the
// full result (Records + Metadata).  Delegates to integrations.ExecuteDQLQuery.
func executeDQLQuery(baseURL, bearerToken, query string) (*grailResult, error) {
	return integrations.ExecuteDQLQuery(baseURL, bearerToken, query)
}

// grailStr safely extracts a string value from a Grail DQL record map.
// Delegates to integrations.GrailStr.
func grailStr(record map[string]any, key string) string {
	return integrations.GrailStr(record, key)
}

// escapeDQLValue escapes a value for safe use inside DQL string literals.
// Delegates to integrations.EscapeDQLValue.
func escapeDQLValue(v any) string {
	return integrations.EscapeDQLValue(v)
}
