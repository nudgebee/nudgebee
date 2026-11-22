package observability

// Ordering of the "By Traces" listing.
//
// A reader whose By Traces page is cut on TRACES has to rank traces before it knows which
// ones the page holds, and a streaming reader can only rank them by the order its index
// already returns. Asking such a reader for a different order cannot be honoured: the rank
// decides page membership, so re-sorting afterwards would reorder one page while the rows
// that belong on it by the requested order sit on another.
//
// Readers that reduce a page of SPANS to traces (queryRootSpansViaSpans → pickRootSpans)
// have no such constraint and do not implement this, because they do not rank traces at
// all — they sort whatever representatives the span page happened to yield. That is a
// weaker guarantee, not a stronger one, which is why "does not implement this"
// must not be read as "sorts By Traces correctly".
//
// Stated once here and used in both places it matters: the error a reader returns when a
// caller asks for another order, and the capability the API advertises so a client does not
// ask in the first place (#41495).

// byTraceNaturalOrderOnlySource is implemented by a trace source whose By Traces view can
// only be listed in its index's natural order. The only such source is Splunk Enterprise,
// whose page is ranked by each trace's newest span in one streaming pass.
//
// A new reader does NOT implement this unless its By Traces page is genuinely cut on
// traces and ranked by one order; reducing a span page to roots is not that.
type byTraceNaturalOrderOnlySource interface {
	// ByTraceNaturalOrderOnlyReason returns the operator-facing explanation, or "" when
	// the source can order its By Traces view freely.
	ByTraceNaturalOrderOnlyReason() string
}

// byTraceNaturalOrderOnlyReason returns why this source's By Traces view accepts only its
// natural order, or "" when it accepts any — which is every source but one.
func byTraceNaturalOrderOnlyReason(source any) string {
	if s, ok := source.(byTraceNaturalOrderOnlySource); ok {
		return s.ByTraceNaturalOrderOnlyReason()
	}
	return ""
}
