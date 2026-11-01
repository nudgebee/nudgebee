package observability

import (
	"fmt"
	"nudgebee/services/integrations"
	"nudgebee/services/security"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Splunk Enterprise cluster-utilisation query builders.
//
// Unlike OpenObserve, none of the Prometheus builders in prometheus_queries.go can be
// reused here: Splunk cannot execute PromQL at all (see splunk_enterprise_workload_metrics.go),
// so every utilisation key has to be rendered as SPL. That puts this file in the same
// category as datadog_queries.go / newrelic_queries.go / solarwinds_queries.go.
//
// Query shape (see buildSplunkUtilisationQuery), verified against Splunk Enterprise 10.4.3:
//
//	| mstats avg("<family>") AS series_value WHERE index="<idx>" span=<step>s BY <series>
//	| stats sum(series_value) AS nb_value BY _time | <drop partial bucket> | stats <reducer>(nb_value)
//
// Four details are load-bearing and each was confirmed empirically:
//
//   - `span` is mandatory. Without it mstats aggregates across time as well as across
//     series, so `sum(allocatable_cpu)` over a 3-node/181-sample window returns 15204
//     rather than the cluster's 84 cores.
//   - Each series is averaged within its bucket before series are summed. Summing in
//     mstats adds every sample in the bucket, inflating the figure by step / collection
//     interval.
//   - The value column must be aliased `nb_value`; convertSplunkMetricRows reads that
//     column and skips any row without it.
//   - The index must appear as the quoted literal index="<idx>", because
//     validateSplunkEnterpriseMetricQuery matches it that way before the search runs.
//
// Percentiles stay in SPL. `stats perc50()/perc90()/max()` operate over the spanned
// buckets, so unlike the OpenObserve path there is no need to pull the series into Go and
// reduce it there.
//
// Families are DISCOVERED from the metrics catalog rather than assumed. Splunk exposes a
// real catalog (`| mcatalog values(metric_name)`), so this is a lookup instead of the blind
// probing OpenObserve forced; the reason for discovering at all is identical, though. What
// lands in a metrics index depends entirely on the collector writing to it -- the OTel
// kubeletstats receiver, the Splunk OTel distribution and a hand-rolled HEC pipeline all
// use different names -- so assuming one renders an empty card that reads as "idle".

// splunkUtilisationRole is the abstract quantity a candidate family list resolves.
type splunkUtilisationRole string

const (
	seRoleCPUCapacity splunkUtilisationRole = "cpu_capacity"
	seRoleCPUUsage    splunkUtilisationRole = "cpu_usage"
	seRoleMemCapacity splunkUtilisationRole = "mem_capacity"
	seRoleMemUsage    splunkUtilisationRole = "mem_usage"
	seRoleCPURequest  splunkUtilisationRole = "cpu_request"
	seRoleCPULimit    splunkUtilisationRole = "cpu_limit"
	seRoleMemRequest  splunkUtilisationRole = "mem_request"
	seRoleMemLimit    splunkUtilisationRole = "mem_limit"
	// Pod-scoped usage, as the Utilization page charts it. Distinct from the node roles
	// above: those describe the machines, these describe what the workloads on them use.
	seRolePodCPUUsage splunkUtilisationRole = "pod_cpu_usage"
	seRolePodMemUsage splunkUtilisationRole = "pod_mem_usage"
	// Per-CONTAINER usage, for the rightsizing keys. Pod-level families cannot stand in:
	// a pod's total is not any one container's usage, and a recommendation is per container.
	seRoleContainerCPUUsage      splunkUtilisationRole = "container_cpu_usage"
	seRoleContainerMemWorkingSet splunkUtilisationRole = "container_mem_working_set"
)

// SplunkUtilisationCandidates is the match order per role, most specific first. Package
// level so tests can override it. Names follow the Splunk OTel Collector's Kubernetes
// conventions, with the dotted and underscored spellings both covered because the HEC and
// OTLP paths disagree on separators.
var SplunkUtilisationCandidates = map[splunkUtilisationRole][]string{
	seRoleCPUCapacity: {"k8s.node.allocatable_cpu", "k8s.node.allocatable.cpu", "kube.node.allocatable_cpu"},
	seRoleCPUUsage:    {"k8s.node.cpu.usage", "k8s.node.cpu.utilization", "k8s.node.cpu_usage"},
	seRoleMemCapacity: {"k8s.node.allocatable_memory", "k8s.node.allocatable.memory", "kube.node.allocatable_memory"},
	seRoleMemUsage:    {"k8s.node.memory.usage", "k8s.node.memory.working_set", "k8s.node.memory_usage"},
	seRoleCPURequest:  {"k8s.container.cpu_request", "k8s.container.cpu.request"},
	seRoleCPULimit:    {"k8s.container.cpu_limit", "k8s.container.cpu.limit"},
	seRoleMemRequest:  {"k8s.container.memory_request", "k8s.container.memory.request"},
	seRoleMemLimit:    {"k8s.container.memory_limit", "k8s.container.memory.limit"},
	seRolePodCPUUsage: {"k8s.pod.cpu.usage", "k8s.pod.cpu.utilization", "container.cpu.usage", "k8s.container.cpu.usage"},
	seRolePodMemUsage: {"k8s.pod.memory.usage", "k8s.pod.memory.working_set", "container.memory.usage", "k8s.container.memory.usage"},
	// Working set, not usage, as the Prometheus key reads container_memory_working_set_bytes:
	// usage includes reclaimable page cache and would size a memory request too high.
	seRoleContainerCPUUsage:      {"container.cpu.usage", "k8s.container.cpu.usage"},
	seRoleContainerMemWorkingSet: {"container.memory.working_set", "k8s.container.memory.working_set"},
}

// splunkUtilisationSpec says which family a utilisation key reads and how the spanned
// buckets are reduced to the single figure the card shows. An empty reducer means the key
// wants the whole series (the trend sparklines).
type splunkUtilisationSpec struct {
	Role    splunkUtilisationRole
	Reducer string
	// PerPod keys return one series per namespace/pod/container instead of one summed
	// series, and honour the request's Aggregate; see buildSplunkPerPodQuery.
	PerPod bool
}

// splunkUtilisationKeys are the keys this provider serves. Anything else returns an
// explicit unsupported error rather than falling through to the Prometheus builders --
// those emit PromQL, which Splunk rejects at the validator with a message about
// generating commands that says nothing about the real cause.
var splunkUtilisationKeys = map[UtilisationKey]splunkUtilisationSpec{
	KeyCPUTotal:      {Role: seRoleCPUCapacity, Reducer: "latest"},
	KeyCPUReal:       {Role: seRoleCPUUsage, Reducer: "avg"},
	KeyP50CPU:        {Role: seRoleCPUUsage, Reducer: "perc50"},
	KeyP90CPU:        {Role: seRoleCPUUsage, Reducer: "perc90"},
	KeyMaxUsageCPU:   {Role: seRoleCPUUsage, Reducer: "max"},
	KeyCPUUsageTrend: {Role: seRoleCPUUsage, Reducer: ""},
	KeyMemTotal:      {Role: seRoleMemCapacity, Reducer: "latest"},
	KeyMemReal:       {Role: seRoleMemUsage, Reducer: "latest"},
	KeyP50Mem:        {Role: seRoleMemUsage, Reducer: "perc50"},
	KeyP90Mem:        {Role: seRoleMemUsage, Reducer: "perc90"},
	KeyMaxUsageMem:   {Role: seRoleMemUsage, Reducer: "max"},
	KeyMemUsageTrend: {Role: seRoleMemUsage, Reducer: ""},
	KeyCPURequest:    {Role: seRoleCPURequest, Reducer: "latest"},
	KeyCPULimit:      {Role: seRoleCPULimit, Reducer: "latest"},
	KeyMemoryRequest: {Role: seRoleMemRequest, Reducer: "latest"},
	KeyMemoryLimit:   {Role: seRoleMemLimit, Reducer: "latest"},
	// The Utilization page's usage lines. Gauges like the rest, so they follow the same
	// instant-vs-range rule: one figure for an instant read, the series for a range.
	KeyCPUUsage:    {Role: seRolePodCPUUsage, Reducer: "latest"},
	KeyMemoryUsage: {Role: seRolePodMemUsage, Reducer: "latest"},

	// The per-container keys vertical rightsizing reads, in the Prometheus builder's
	// shape: one series per (namespace, pod, container), labelled canonically, so usage
	// joins against its spec on those three.
	KeyContainerCPUUsageByPod:         {Role: seRoleContainerCPUUsage, PerPod: true},
	KeyContainerMemoryWorkingSetByPod: {Role: seRoleContainerMemWorkingSet, PerPod: true},
	KeyContainerCPURequestByPod:       {Role: seRoleCPURequest, PerPod: true},
	KeyContainerCPULimitByPod:         {Role: seRoleCPULimit, PerPod: true},
	KeyContainerMemoryRequestByPod:    {Role: seRoleMemRequest, PerPod: true},
	KeyContainerMemoryLimitByPod:      {Role: seRoleMemLimit, PerPod: true},
}

// splunkUtilisationRefusals are keys deliberately not served, with the reason. Answering
// them from the nearest family would be worse than no answer, because rightsizing would
// act on it.
var splunkUtilisationRefusals = map[UtilisationKey]string{
	// The cgroup high-water mark. The OTel collector's kubeletstats receiver reports
	// usage and working set, not a max; substituting working set would set a memory
	// limit below the peak it has to cover. Rightsizing treats this key as optional.
	KeyContainerMemoryMaxUsageByPod: "splunk enterprise: no Splunk OTel metric carries the container memory high-water mark",
	// Needs the limit joined against a per-container OOMKilled terminated reason; the
	// k8s_cluster receiver publishes restarts but not the reason.
	KeyContainerOOMKilledLimitByPod: "splunk enterprise: no Splunk OTel metric carries a container's OOMKilled termination reason",
}

// splunkUtilisationSeriesColumn holds each series' per-bucket value before the series are
// added together into nb_value.
const splunkUtilisationSeriesColumn = "series_value"

// buildSplunkUtilisationQuery renders one key as
//
//	| mstats avg("<family>") AS series_value WHERE ... span=<step>s BY <series dims>
//	| stats sum(series_value) AS nb_value BY _time
//	| addinfo | where _time + <step> <= info_max_time | fields _time nb_value
//	[| stats <reducer>(nb_value) AS nb_value]
//
// Each series is reduced to one value per bucket BEFORE series are added. `mstats sum()`
// across a span adds every SAMPLE in the bucket, which inflated every figure by
// step / collection interval -- 4x at a 60s step against a 15s collector, 240x at 3600s
// (measured on Splunk 10.4.3). It was only right when a bucket held one sample per series.
//
// The final bucket is dropped until it is complete. It is usually part-filled, so `latest`
// on an instant read would jump between a partial and a full figure depending on how many
// samples had landed.
//
// scope narrows the search to a namespace, workload, pod or container. It is empty for the
// cluster keys, which are cluster-wide by definition. Omitting it for the scoped keys was a
// real bug: cpu_request on a single pod's page answered with the whole cluster's request
// total, which looks entirely plausible and is wrong.
//
// The output is ONE series (grouped by _time only), like the Prometheus equivalents'
// `sum(rate(...))`. Callers read payload[0], so a per-pod output would chart one arbitrary
// pod of a workload and silently drop the rest.
func buildSplunkUtilisationQuery(family, index string, stepSeconds int, reducer string, scope []LabelMatcher) (string, error) {
	where := []string{fmt.Sprintf(`index="%s"`, index)}
	for _, m := range scope {
		matcher, err := buildSplunkMetricMatcher(m)
		if err != nil {
			return "", err
		}
		where = append(where, matcher)
	}

	base := fmt.Sprintf(`| mstats avg("%s") AS %s WHERE %s span=%ds BY %s`+
		` | stats sum(%s) AS %s BY _time`+
		` | addinfo | where _time + %d <= info_max_time | fields _time %s`,
		family, splunkUtilisationSeriesColumn, strings.Join(where, " AND "), stepSeconds, strings.Join(splunkUtilisationSeriesDims(family), ", "),
		splunkUtilisationSeriesColumn, splunkEnterpriseValueColumn,
		stepSeconds, splunkEnterpriseValueColumn)
	if reducer == "" {
		return base, nil
	}
	return fmt.Sprintf("%s | stats %s(%s) AS %s",
		base, reducer, splunkEnterpriseValueColumn, splunkEnterpriseValueColumn), nil
}

// splunkPerPodDims identify one series of a per-container key. Namespace is included at
// every scope: at node and cluster scope two namespaces can each run a `worker-0`.
var splunkPerPodDims = []string{"k8s.namespace.name", "k8s.pod.name", "k8s.container.name"}

// buildSplunkPerPodQuery renders a per-container key: one series per namespace, pod and
// container at the caller's step, with the still-filling final bucket dropped.
//
// With an Aggregate each series collapses to one point over the window. The buckets are
// already Step wide, so `count` of them is exactly the contract's definition of count
// ("Step-wide intervals with data", metrics_aggregate_count.go) and needs no conversion,
// and a percentile over them matches Prometheus's quantile_over_time((expr)[window:step]).
// exactperc rather than perc: perc is an approximation, and the rightsizing floor and
// percentiles are compared against thresholds.
//
// Without an Aggregate an instant read keeps the latest complete bucket per series, as
// the gauge keys do; a range read returns the series.
func buildSplunkPerPodQuery(family, index string, stepSeconds int, scope []LabelMatcher, agg ParsedAggregate, instant bool) (string, error) {
	where := []string{fmt.Sprintf(`index="%s"`, index)}
	for _, m := range scope {
		matcher, err := buildSplunkMetricMatcher(m)
		if err != nil {
			return "", err
		}
		where = append(where, matcher)
	}
	dims := strings.Join(splunkPerPodDims, ", ")

	q := fmt.Sprintf(`| mstats avg("%s") AS %s WHERE %s span=%ds BY %s`+
		` | addinfo | where _time + %d <= info_max_time | fields _time %s %s`,
		family, splunkEnterpriseValueColumn, strings.Join(where, " AND "), stepSeconds, dims,
		stepSeconds, strings.Join(splunkPerPodDims, " "), splunkEnterpriseValueColumn)

	var stage string
	switch {
	case agg.IsQuantile():
		stage = "exactperc" + strconv.FormatFloat(agg.Quantile*100, 'f', -1, 64)
	case agg.Name != "":
		// avg, sum, max, min and count are all SPL stats functions of the same name.
		stage = agg.Name
	case instant:
		stage = "latest"
	default:
		return q, nil
	}
	return fmt.Sprintf("%s | stats %s(%s) AS %s BY %s",
		q, stage, splunkEnterpriseValueColumn, splunkEnterpriseValueColumn, dims), nil
}

// splunkUtilisationSeriesDims names the dimensions that identify one series of a family:
// a node for node families, a pod for pod families, a container otherwise. Namespace is
// included for pods because pod names are only unique within a namespace.
//
// mstats drops a sample that lacks a BY dimension, so these must be dimensions the
// collector actually attaches -- the Splunk OTel Collector's kubeletstats and k8s_cluster
// receivers attach all of them to the families SplunkUtilisationCandidates lists.
func splunkUtilisationSeriesDims(family string) []string {
	f := strings.ToLower(family)
	switch {
	case strings.HasPrefix(f, "k8s.node."), strings.HasPrefix(f, "kube.node."):
		return []string{"k8s.node.name"}
	case strings.HasPrefix(f, "k8s.pod."):
		return []string{"k8s.namespace.name", "k8s.pod.name"}
	default:
		return []string{"k8s.namespace.name", "k8s.pod.name", "k8s.container.name"}
	}
}

// splunkUtilisationScope turns the request's target into mstats matchers, mirroring the
// dimensions buildSplunkWorkloadCPUQuery already uses.
//
// A pod is matched exactly; a workload is matched as a pod-name PREFIX, because pod names
// carry a replica-hash suffix and that is the only form that works for every workload kind.
// A request with no namespace is cluster-wide and gets no matchers at all.
func splunkUtilisationScope(meta RequestMetadata) []LabelMatcher {
	var scope []LabelMatcher
	// A node request carries no namespace; without this it was answered cluster-wide.
	if meta.Kind == "node" {
		if meta.NodeName == "" {
			return nil
		}
		return []LabelMatcher{{Label: "k8s.node.name", Operator: "_eq", Value: meta.NodeName}}
	}
	if meta.Namespace == "" {
		return nil
	}
	scope = append(scope, LabelMatcher{Label: "k8s.namespace.name", Operator: "_eq", Value: meta.Namespace})
	// An exact pod list (rightsizing supplies one) is scope in its own right and beats
	// the prefix, which also matches a sibling workload ("services-server" matches
	// "services-server-worker-…"). An empty list is a real answer: match nothing, on a
	// name Kubernetes cannot produce (RFC 1123 forbids underscores), rather than falling
	// back to the prefix and sizing this workload on its neighbours' pods.
	if meta.PodNamesSupplied {
		if len(meta.PodNames) == 0 {
			scope = append(scope, LabelMatcher{Label: "k8s.pod.name", Operator: "_eq", Value: "__no_pods_resolved__"})
		} else {
			scope = append(scope, LabelMatcher{Label: "k8s.pod.name", Operator: "_in", Value: strings.Join(meta.PodNames, ",")})
		}
	} else if meta.Name != "" {
		if meta.Kind == "pod" {
			scope = append(scope, LabelMatcher{Label: "k8s.pod.name", Operator: "_eq", Value: meta.Name})
		} else {
			scope = append(scope, LabelMatcher{Label: "k8s.pod.name", Operator: "_prefix", Value: meta.Name + "-"})
		}
	}
	if meta.ContainerName != "" {
		scope = append(scope, LabelMatcher{Label: "k8s.container.name", Operator: "_eq", Value: meta.ContainerName})
	}
	return scope
}

// fetchSplunkEnterpriseUtilisation serves the utilisation keys from the account's Splunk
// metrics index, discovering each family from the catalog first.
func fetchSplunkEnterpriseUtilisation(ctx *security.RequestContext, req GetUtilisationTrendRequest, meta RequestMetadata, integrationSource string, instant bool) (OutputMetricQuery, error) {
	cfg, err := integrations.GetSplunkEnterpriseConfig(ctx, req.AccountId)
	if err != nil {
		return OutputMetricQuery{}, fmt.Errorf("failed to get Splunk Enterprise config: %w", err)
	}
	index, err := splunkEnterpriseMetricIndex(cfg)
	if err != nil {
		return OutputMetricQuery{}, err
	}

	available, err := (&SplunkEnterpriseMetricSource{}).FetchMetricList(ctx, FetchMetricsListRequest{
		AccountId: req.AccountId,
		StartTime: req.StartTime,
		EndTime:   req.EndTime,
	})
	if err != nil {
		return OutputMetricQuery{}, fmt.Errorf("splunk enterprise: failed to read the metrics catalog: %w", err)
	}
	resolved := resolveSplunkUtilisationFamilies(available)

	step := splunkUtilisationStep(meta, req)
	scope := splunkUtilisationScope(meta)
	queries := map[string]string{}
	results := make([]QueryResult, 0, len(meta.RequestedMetrics))

	for _, key := range meta.RequestedMetrics {
		if reason, refused := splunkUtilisationRefusals[UtilisationKey(key)]; refused {
			results = append(results, splunkUtilisationUnavailable(key, reason))
			continue
		}
		spec, ok := splunkUtilisationKeys[UtilisationKey(key)]
		if !ok {
			results = append(results, splunkUtilisationUnavailable(key,
				fmt.Sprintf("splunk enterprise: utilisation key %q is not served by this provider", key)))
			continue
		}
		family, found := resolved[spec.Role]
		if !found {
			results = append(results, splunkUtilisationUnavailable(key, fmt.Sprintf(
				"splunk enterprise: no metric in index %q carries %q (looked for: %s)",
				index, key, strings.Join(SplunkUtilisationCandidates[spec.Role], ", "))))
			continue
		}
		// The family name is interpolated into the SPL, and it comes from the index's own
		// catalog rather than from our candidate list -- matching is case-insensitive, so the
		// string sent is whatever the index reported. Hold it to the same guard
		// buildSplunkMStatsQuery applies before any metric name reaches a search.
		if !isSafeSplunkMetricName(family) {
			results = append(results, splunkUtilisationError(key,
				fmt.Sprintf("splunk enterprise: invalid or unsafe metric name %q", family)))
			continue
		}
		var spl string
		var buildErr error
		if spec.PerPod {
			spl, buildErr = buildSplunkPerPodQuery(family, index, step, scope, meta.Aggregate, instant)
		} else {
			spl, buildErr = buildSplunkUtilisationQuery(family, index, step, splunkUtilisationReducer(spec, meta.Aggregate, instant), scope)
		}
		if buildErr != nil {
			results = append(results, splunkUtilisationError(key, buildErr.Error()))
			continue
		}
		queries[key] = spl
	}

	if len(queries) > 0 {
		out, fetchErr := FetchMetricsQuery(ctx, FetchMetricsRequest{
			AccountId:            req.AccountId,
			MetricProvider:       "splunk_enterprise",
			MetricProviderSource: integrationSource,
			Queries:              queries,
			StartTime:            req.StartTime,
			EndTime:              req.EndTime,
			StepInterval:         step,
		})
		if fetchErr != nil {
			return OutputMetricQuery{}, fetchErr
		}
		results = append(results, out.Results...)
	}

	return OutputMetricQuery{Results: results}, nil
}

// splunkUtilisationReducer drops the reducing stage for gauge keys on a range request.
//
// The Trend popup asks for cpu_usage_trend alongside cpu_total, cpu_request and cpu_limit
// with instant=false and draws a line per key. "latest" answers "what is this gauge now",
// which over a range collapses those three lines to a single point and renders the chart
// as one line plus three dots. Returning the spanned series instead gives each a real line.
//
// The statistical reducers are left alone: avg, perc50, perc90 and max describe the whole
// window by definition, so one number is the right answer whether or not the caller asked
// for an instant.
func splunkUtilisationReducer(spec splunkUtilisationSpec, agg ParsedAggregate, instant bool) string {
	// count is the one aggregate the key cannot encode. Every other statistic this family
	// serves has its own key -- p50_cpu, p90_cpu, max_usage_cpu, cpu_real -- so the spec's
	// reducer IS the requested statistic and the caller's aggregate must not override it;
	// answering p50_cpu with an average would return a mean under a name promising a
	// median. There is no count_cpu key, so a count request has nowhere else to go.
	//
	// Without this the reducer was whatever the key hardcoded, and a count came back as the
	// metric's own value: cpu_total answered count=12.0 -- the core count -- where the
	// Prometheus control answered 15915. Anything sizing on count was wrong by three orders
	// of magnitude, and doubling the step did not halve the answer (contract rule 4).
	if agg.Name == "count" {
		return "count"
	}
	if !instant && spec.Reducer == "latest" {
		return ""
	}
	return spec.Reducer
}

// resolveSplunkUtilisationFamilies picks, for each role, the first candidate the index
// actually reports. Matching is case-insensitive because Splunk preserves whatever case
// the writer used and collectors are inconsistent about it.
func resolveSplunkUtilisationFamilies(available []OutputMetrics) map[splunkUtilisationRole]string {
	present := make(map[string]string, len(available))
	for _, m := range available {
		present[strings.ToLower(strings.TrimSpace(m.Metric))] = m.Metric
	}

	resolved := map[splunkUtilisationRole]string{}
	roles := make([]string, 0, len(SplunkUtilisationCandidates))
	for role := range SplunkUtilisationCandidates {
		roles = append(roles, string(role))
	}
	sort.Strings(roles) // deterministic, so a logged resolution is reproducible

	for _, r := range roles {
		role := splunkUtilisationRole(r)
		for _, candidate := range SplunkUtilisationCandidates[role] {
			if actual, ok := present[strings.ToLower(candidate)]; ok {
				resolved[role] = actual
				break
			}
		}
	}
	return resolved
}

// splunkUtilisationStep keeps the spanned buckets at the resolution the picker implies.
//
// meta.Step is a duration string that promAggWindow fills in ("60s"). It is parsed as a
// duration rather than by trimming the unit so a later switch to "5m" there keeps working
// instead of silently falling back to the default and coarsening every percentile. The
// bare-integer branch covers callers that set the field without a unit.
func splunkUtilisationStep(meta RequestMetadata, req GetUtilisationTrendRequest) int {
	if meta.Step != "" {
		// Guard on the SECONDS, not the duration. A sub-second step parses fine and is
		// > 0, but truncates to 0 -- which would emit span=0s and fail the search.
		if d, err := time.ParseDuration(meta.Step); err == nil {
			if seconds := int(d.Seconds()); seconds > 0 {
				return seconds
			}
		}
		if v, err := strconv.Atoi(meta.Step); err == nil && v > 0 {
			return v
		}
	}
	if req.StepInterval > 0 {
		return req.StepInterval
	}
	return 60
}

func splunkUtilisationError(key, msg string) QueryResult {
	return QueryResult{QueryKey: key, Error: &msg}
}

// splunkUtilisationUnavailable answers a key this provider cannot serve: empty payload, a
// note saying why, and NO error.
//
// The distinction is the whole point. "This query failed" and "there is no such metric
// here" are different answers, and only the first belongs on Error. Across api-server 91
// call sites read Payload and only 62 read Error, so a capability gap reported as an error
// is read by the majority as a workload that used nothing -- which once flagged a live
// workload as abandoned. Elasticsearch already answers its absent HTTP keys this way.
//
// A genuine failure -- an unsafe metric name, a query that would not build -- keeps
// splunkUtilisationError, because a caller must not read that as "no data".
func splunkUtilisationUnavailable(key, note string) QueryResult {
	return QueryResult{QueryKey: key, Note: note}
}
