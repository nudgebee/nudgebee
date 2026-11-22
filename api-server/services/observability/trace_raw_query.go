package observability

import "strings"

// Raw trace queries.
//
// A "raw" trace query is TracesV3Request.Query: a query written in the provider's own
// language (Elasticsearch DSL or KQL, OpenObserve SQL, TraceQL, NRQL, DQL, SPL, Cloud Trace
// filter syntax, ClickHouse SQL, …) rather than built from the canonical where clause.
//
// Running one is BASIC behaviour, not a capability a reader opts into: every trace provider
// executes a raw query, in the span listing and in every other operation that can express
// one. There is deliberately no per-provider switch and no dispatch-layer gate — a reader
// gets this by building its query through the shared helpers (finalizeESTraceQueryBody,
// esTraceFilters, openObserveRawSubquery, …) like every other reader.
//
// Before #41426, Elasticsearch and OpenObserve accepted a raw query and silently discarded
// it, returning unfiltered traces labelled with the caller's query.

// hasRawTraceQuery reports whether this request carries a raw provider query.
func hasRawTraceQuery(req TracesV3Request) bool {
	return strings.TrimSpace(req.Query) != ""
}

// rawTraceQueryUnsupportedSource is implemented by a trace source that cannot run a raw
// query AT ALL — not one that merely hasn't wired it up. The only such source is
// SolarWinds, whose backend exposes no span or trace read endpoint for a query to be sent
// to, so there is nothing to implement rather than something left undone.
//
// It exists so that one reader's hard limitation is stated once and used in both places it
// matters: the error it returns when a caller supplies a query, and the capability the API
// advertises. A new reader does NOT implement this; it implements the query instead.
type rawTraceQueryUnsupportedSource interface {
	RawTraceQueryUnsupportedReason() string
}

// rawTraceQueryUnsupportedReason returns why this source cannot run a raw trace query, or
// "" when it can — which is every source but one.
func rawTraceQueryUnsupportedReason(source any) string {
	if s, ok := source.(rawTraceQueryUnsupportedSource); ok {
		return s.RawTraceQueryUnsupportedReason()
	}
	return ""
}
