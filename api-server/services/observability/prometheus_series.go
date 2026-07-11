package observability

import (
	"fmt"
	"net/url"
	"nudgebee/services/security"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"golang.org/x/sync/errgroup"
)

// seriesMatchCandidate is one (namespace-label, workload-label) convention to probe
// when discovering which metric families exist for a workload. The set deliberately
// names only the small, stable space of namespace/workload IDENTIFYING labels — it
// hardcodes NO metric family names (families are always read back from real series).
// A workload commonly emits under several of these at once (e.g. eBPF observer metrics
// under destination_workload_name AND scraped app/runtime metrics under job/pod), so
// every candidate is tried and the results unioned — no single selector sees them all.
type seriesMatchCandidate struct {
	NamespaceLabel string
	WorkloadLabel  string
	MatchType      string // matchTypeExact | matchTypePrefix
}

const (
	matchTypeExact  = "exact"
	matchTypePrefix = "prefix"
)

// SeriesMatchCandidates is the default candidate set, package-level so it can be
// overridden in tests (and, as a follow-up, wired to config). Ordering only affects
// the deterministic output sort; all candidates are always probed.
var SeriesMatchCandidates = []seriesMatchCandidate{
	// eBPF / service-mesh observer metrics: pod/namespace label the OBSERVER, the
	// target workload is carried by destination_workload_* (two spellings seen in the wild).
	{NamespaceLabel: "destination_workload_namespace", WorkloadLabel: "destination_workload_name", MatchType: matchTypeExact},
	{NamespaceLabel: "actual_destination_workload_namespace", WorkloadLabel: "actual_destination_workload_name", MatchType: matchTypeExact},
	// Scraped app / runtime / custom metrics: standard k8s namespace label, workload
	// identified by the scrape job or a controller/selector label.
	{NamespaceLabel: "namespace", WorkloadLabel: "job", MatchType: matchTypeExact},
	{NamespaceLabel: "namespace", WorkloadLabel: "deployment", MatchType: matchTypeExact},
	{NamespaceLabel: "namespace", WorkloadLabel: "statefulset", MatchType: matchTypeExact},
	{NamespaceLabel: "namespace", WorkloadLabel: "daemonset", MatchType: matchTypeExact},
	{NamespaceLabel: "namespace", WorkloadLabel: "service", MatchType: matchTypeExact},
	{NamespaceLabel: "namespace", WorkloadLabel: "workload", MatchType: matchTypeExact},
	{NamespaceLabel: "namespace", WorkloadLabel: "app", MatchType: matchTypeExact},
	{NamespaceLabel: "namespace", WorkloadLabel: "app_kubernetes_io_name", MatchType: matchTypeExact},
	{NamespaceLabel: "namespace", WorkloadLabel: "container", MatchType: matchTypeExact},
	// Pod names carry a replica-hash suffix, so the workload value is a PREFIX of the pod.
	{NamespaceLabel: "namespace", WorkloadLabel: "pod", MatchType: matchTypePrefix},
}

const (
	// seriesMatchDefaultLimit caps how many metric families a single candidate lookup
	// returns before the result is flagged truncated. Because the lookup asks for the
	// distinct __name__ VALUES (not series), this bound is on the metric-catalog size —
	// far smaller and stabler than series cardinality — so it essentially never trips.
	seriesMatchDefaultLimit = 500
	// seriesMatchMaxLimit is the hard ceiling regardless of a caller-supplied limit.
	seriesMatchMaxLimit = 2000
	// seriesMatchDefaultLookback is the time window probed when the caller gives none.
	seriesMatchDefaultLookback = time.Hour
	// seriesMatchMaxConcurrency bounds how many candidate lookups run at once so a wide
	// candidate set can't fan out an unbounded burst of relay/Prometheus requests.
	seriesMatchMaxConcurrency = 6
)

// FetchMetricSeries discovers, for (namespace, workload), the metric __name__ families
// that actually have series — grouped by the (namespace-label, workload-label) that found
// them. It issues ONE /api/v1/label/__name__/values lookup PER candidate (concurrently),
// each server-side filtered by that candidate's selector, and asks the engine for the
// distinct __name__ VALUES directly rather than enumerating series and capping on series
// count. This is deliberate: a single high-cardinality family (e.g. cAdvisor container_*)
// collapses to one __name__ but explodes into thousands of series, so a series-count cap
// would crowd a workload's low-cardinality families (e.g. nb_llm_*) out of the budget and
// reproduce the "N/A" bug. Querying families sidesteps that entirely — and is also cheaper,
// since the engine reads its label index instead of materialising every series. No metric
// family name is assumed here; families are always read back from the engine.
func (s *PrometheusMetricSource) FetchMetricSeries(ctx *security.RequestContext, req FetchMetricSeriesRequest) (MetricSeriesResult, error) {
	limit := req.Limit
	if limit <= 0 {
		limit = seriesMatchDefaultLimit
	}
	if limit > seriesMatchMaxLimit {
		limit = seriesMatchMaxLimit
	}

	start, end := req.StartTime, req.EndTime
	if end <= 0 {
		end = time.Now().Unix()
	}
	if start <= 0 {
		start = end - int64(seriesMatchDefaultLookback.Seconds())
	}

	selectors, err := buildSeriesMatchSelectors(req.Namespace, req.Workload, SeriesMatchCandidates)
	if err != nil {
		return MetricSeriesResult{}, err
	}
	if len(selectors) == 0 {
		return MetricSeriesResult{}, fmt.Errorf("prometheus: no valid series-match selectors for workload %q", req.Workload)
	}

	// Probe every candidate concurrently. Each candidate is an independent label
	// convention, so a transient failure on one does not invalidate the others: we
	// collect per-candidate results and only surface an error if EVERY candidate failed
	// (otherwise a flaky lookup on, say, the unused `statefulset` candidate would discard
	// the families the `pod` candidate found). errgroup here is used purely for bounded
	// concurrency + wait — each goroutine records its own outcome and returns nil so one
	// failure never cancels the siblings.
	families := make([][]string, len(selectors))
	truncated := make([]bool, len(selectors))
	errs := make([]error, len(selectors))

	eg := new(errgroup.Group)
	eg.SetLimit(seriesMatchMaxConcurrency)
	for i := range selectors {
		i := i
		eg.Go(func() error {
			fam, trunc, ferr := s.fetchCandidateFamilies(ctx, req.AccountId, selectors[i], start, end, limit)
			families[i], truncated[i], errs[i] = fam, trunc, ferr
			return nil
		})
	}
	_ = eg.Wait() // goroutines never return an error; per-candidate errors live in errs.

	var firstErr error
	failures := 0
	for i, ferr := range errs {
		if ferr != nil {
			failures++
			if firstErr == nil {
				firstErr = ferr
			}
			ctx.GetLogger().Warn("series-match: candidate lookup failed",
				"selector", selectors[i], "account_id", req.AccountId, "error", ferr)
		}
	}
	// Every candidate failed → the backend is unreachable/erroring; surface it rather
	// than returning a misleading empty result.
	if failures == len(selectors) {
		return MetricSeriesResult{}, firstErr
	}

	return assembleSeriesResult(SeriesMatchCandidates, families, truncated), nil
}

// fetchCandidateFamilies issues one /api/v1/label/__name__/values lookup for a single
// candidate selector and returns the distinct metric families that have series matching
// it. `truncated` is set when the engine capped the family list at `limit` (signalled via
// the `warnings` field, with a len>=limit backstop for engines that omit the warning).
func (s *PrometheusMetricSource) fetchCandidateFamilies(ctx *security.RequestContext, accountID, selector string, start, end int64, limit int) ([]string, bool, error) {
	var path strings.Builder
	path.WriteString("prometheus-v2/api/v1/label/__name__/values?match[]=")
	path.WriteString(url.QueryEscape(selector))
	path.WriteString("&start=" + strconv.FormatInt(start, 10))
	path.WriteString("&end=" + strconv.FormatInt(end, 10))
	path.WriteString("&limit=" + strconv.Itoa(limit))

	resp, err := relayProxyRequest(ctx, accountID, nil, path.String())
	if err != nil {
		return nil, false, err
	}
	if status, ok := resp["status"].(string); ok && status == "error" {
		errType, _ := resp["errorType"].(string)
		errMsg, _ := resp["error"].(string)
		return nil, false, fmt.Errorf("prometheus: label-values query failed (%s): %s", errType, errMsg)
	}

	fam, trunc := parseFamilyValues(resp["data"], resp["warnings"], limit)
	return fam, trunc, nil
}

// parseFamilyValues extracts the metric family names from a /label/__name__/values
// response `data` array (a list of strings), dropping non-string/empty entries, and
// reports whether the engine truncated the list at `limit`.
func parseFamilyValues(data any, warnings any, limit int) ([]string, bool) {
	list, _ := data.([]any)
	families := make([]string, 0, len(list))
	for _, raw := range list {
		if name, ok := raw.(string); ok && name != "" {
			families = append(families, name)
		}
	}
	truncated := warningsIndicateLimit(warnings) || (limit > 0 && len(families) >= limit)
	return families, truncated
}

// assembleSeriesResult groups the per-candidate family lists into the output, dropping
// candidates that matched nothing, deduping + sorting each family list, and producing a
// deterministic candidate ordering. `truncated` is the OR across all candidate lookups.
// families[i]/truncated[i] correspond positionally to candidates[i].
func assembleSeriesResult(candidates []seriesMatchCandidate, families [][]string, truncated []bool) MetricSeriesResult {
	matches := make([]MetricSeriesMatch, 0, len(candidates))
	anyTruncated := false
	for i, c := range candidates {
		if i < len(truncated) && truncated[i] {
			anyTruncated = true
		}
		if i >= len(families) || len(families[i]) == 0 {
			continue
		}
		// label-values already returns distinct values, but dedup defensively so a
		// non-conforming engine can't surface duplicates.
		seen := make(map[string]struct{}, len(families[i]))
		uniq := make([]string, 0, len(families[i]))
		for _, f := range families[i] {
			if _, dup := seen[f]; dup {
				continue
			}
			seen[f] = struct{}{}
			uniq = append(uniq, f)
		}
		sort.Strings(uniq)
		matches = append(matches, MetricSeriesMatch{
			NamespaceLabel: c.NamespaceLabel,
			WorkloadLabel:  c.WorkloadLabel,
			MatchType:      c.MatchType,
			Families:       uniq,
		})
	}

	sort.Slice(matches, func(i, j int) bool {
		if matches[i].NamespaceLabel != matches[j].NamespaceLabel {
			return matches[i].NamespaceLabel < matches[j].NamespaceLabel
		}
		return matches[i].WorkloadLabel < matches[j].WorkloadLabel
	})
	return MetricSeriesResult{Matches: matches, Truncated: anyTruncated}
}

// buildSeriesMatchSelectors renders one PromQL selector string per candidate. The
// namespace matcher is omitted when no namespace is supplied (workload-only discovery,
// still bounded by limit). Label names are validated and values escaped so a workload
// like `a"} or x` cannot smuggle extra matchers. The prefix match renders an anchored
// regex (Prometheus/VM regex matchers are fully anchored), so `pod=~"<workload>.*"` has
// exactly prefix semantics — the server-side filter alone is authoritative.
func buildSeriesMatchSelectors(namespace, workload string, candidates []seriesMatchCandidate) ([]string, error) {
	if workload == "" {
		return nil, fmt.Errorf("prometheus: workload is required for series-match")
	}
	selectors := make([]string, 0, len(candidates))
	for _, c := range candidates {
		if !promqlLabelNameRe.MatchString(c.WorkloadLabel) {
			return nil, fmt.Errorf("prometheus: invalid workload label name %q", c.WorkloadLabel)
		}
		var matchers []string
		if namespace != "" {
			if !promqlLabelNameRe.MatchString(c.NamespaceLabel) {
				return nil, fmt.Errorf("prometheus: invalid namespace label name %q", c.NamespaceLabel)
			}
			matchers = append(matchers, fmt.Sprintf("%s=\"%s\"", c.NamespaceLabel, escapePromQLString(namespace)))
		}
		switch c.MatchType {
		case matchTypePrefix:
			regexVal := regexp.QuoteMeta(workload) + ".*"
			matchers = append(matchers, fmt.Sprintf("%s=~\"%s\"", c.WorkloadLabel, escapePromQLString(regexVal)))
		default:
			matchers = append(matchers, fmt.Sprintf("%s=\"%s\"", c.WorkloadLabel, escapePromQLString(workload)))
		}
		selectors = append(selectors, "{"+strings.Join(matchers, ",")+"}")
	}
	return selectors, nil
}

// warningsIndicateLimit reports whether Prometheus/VM signalled the result was capped
// by the limit parameter (e.g. "results truncated due to limit"). VictoriaMetrics honors
// `limit` but omits the warning, so the len>=limit backstop in parseFamilyValues carries
// the truncation signal there.
func warningsIndicateLimit(warnings any) bool {
	list, ok := warnings.([]any)
	if !ok {
		return false
	}
	for _, w := range list {
		if s, ok := w.(string); ok && strings.Contains(strings.ToLower(s), "limit") {
			return true
		}
	}
	return false
}
