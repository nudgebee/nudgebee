package observability

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"

	"nudgebee/services/security"
)

// Self-hosted VM metrics.
//
// A VM account has no Kubernetes agent: its metrics are written by node-agent to a
// customer-run Prometheus-compatible store. node-agent names its series differently
// from node-exporter and cAdvisor, and labels them with an opaque `instance` (a hash
// of machine id and system UUID), so none of the Kubernetes node builders apply.
//
// What node-agent exports, and therefore what a VM can answer:
//
//	node_resources_cpu_usage_seconds_total{mode}   CPU time per mode
//	node_resources_cpu_logical_cores               core count
//	node_resources_memory_{total,available}_bytes  memory
//	node_resources_disk_{read,written}_bytes_total{device}   disk I/O
//	container_resources_cpu_usage_seconds_total{container_id} per service CPU
//	container_resources_memory_rss_bytes{container_id}        per service memory
//	container_resources_disk_{used,size}_bytes{container_id,device,mount_point,volume}
//
// A "service" is node-agent's container_id: a systemd unit on a VM
// (/system.slice/ssh.service) or a container. Its app_id label is the Kubernetes
// application id and is empty on a VM, so it cannot identify anything there.
//
// A VM that only runs node_exporter (or whose Prometheus scrapes node-agent's
// /metrics) is answered too: each query is "node-agent series or node_exporter
// series", the same pattern the Kubernetes node builders use. Per-service numbers
// are node-agent only, since nothing else carries per-systemd-unit usage.
//
// Disk used/total comes from node_filesystem_* (node-agent in standalone mode and
// node_exporter both export it under that name), falling back to the sum of the
// volumes the monitored services use for an agent that does not export it yet.

// vmMetricsSupported reports whether provider can answer VM queries. Only the
// PromQL-compatible stores holding node-agent / node_exporter series can; every
// other provider's builders target Kubernetes pods and nodes.
func vmMetricsSupported(provider string) bool {
	switch provider {
	case "prometheus", "victoria_metrics", "chronosphere", "cubeapm":
		return true
	// Orion is not Prometheus-compatible: it answers the VM keys from its own
	// statistics tables over SWQL (buildOrionVMQueries), not from node-agent or
	// node_exporter series.
	case "orion":
		return true
	}
	return false
}

// vmMetricsUnsupportedNote is the reason shown for a provider vmMetricsSupported refuses.
func vmMetricsUnsupportedNote(provider string) string {
	return fmt.Sprintf("VM metrics are not supported for the %s metrics provider; they need a Prometheus-compatible store holding node-agent or node_exporter series, or a SolarWinds Platform (Orion) server", provider)
}

// vmIdentifierSafe bounds what is interpolated into the resolution queries. The
// values come from the request, so anything outside the characters an instance id,
// IP address or hostname can hold is refused rather than escaped.
var vmIdentifierSafe = regexp.MustCompile(`^[A-Za-z0-9._:/-]{1,255}$`)

// vmProbeFetch runs one lookup query. A seam so tests need no metrics backend.
var vmProbeFetch = FetchMetricsQuery

// vmLookup is the outcome of resolving a VM to its `instance` label. Exactly one of
// the two fields is set: the instance when the VM was found, otherwise the reason it
// was not, in words fit to show the user.
type vmLookup struct {
	Instance string
	Reason   string
}

// resolveVMInstance finds the `instance` label node-agent gave this VM, trying the
// strongest identifier first: the cloud instance id, then the private IP, then the
// hostname. A single instant query per identifier; the first hit wins. When nothing
// is found the builders emit no queries rather than match every VM in the store, and
// the reason says why: a failed query is reported as such, not as "no VM".
func resolveVMInstance(ctx *security.RequestContext, req GetUtilisationTrendRequest, provider, source string, meta RequestMetadata) vmLookup {
	probes := vmInstanceProbes(meta, vmProbeLookback(req.StartTime, req.EndTime))
	if len(probes) == 0 {
		return vmLookup{Reason: "this VM has no usable instance id, IP address or hostname to look it up by"}
	}

	var firstErr error
	var ambiguous []string
	for _, probe := range probes {
		out, err := vmProbeFetch(ctx, FetchMetricsRequest{
			AccountId:            req.AccountId,
			MetricProvider:       provider,
			MetricProviderSource: source,
			Queries:              map[string]string{"vm_instance_probe": probe},
			StartTime:            req.StartTime,
			EndTime:              req.EndTime,
			Instant:              true,
		})
		if err == nil {
			err = vmProbeResultError(out)
		}
		if err != nil {
			ctx.GetLogger().Warn("observability: VM instance probe failed",
				"account_id", req.AccountId, "query", probe, "error", err)
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		instances := distinctVMInstances(out)
		switch len(instances) {
		case 1:
			return vmLookup{Instance: instances[0]}
		case 0:
		default:
			ambiguous = instances
		}
	}

	switch {
	case ambiguous != nil:
		return vmLookup{Reason: fmt.Sprintf("more than one machine in the metrics store matches this VM (%s); its identifier is not unique", strings.Join(ambiguous, ", "))}
	case firstErr != nil:
		return vmLookup{Reason: "the metrics store could not be queried: " + firstErr.Error()}
	}
	return vmLookup{Reason: "no machine matching " + vmIdentifiersTried(meta) + " is writing to the metrics store; check that node-agent or node_exporter is running on this VM and writing to it"}
}

// vmProbeResultError returns the error a failing query carries on its result. A query
// that fails does not fail FetchMetricsQuery: the error rides on the result, so
// ignoring it makes a broken lookup look like a VM that is simply absent.
func vmProbeResultError(out OutputMetricQuery) error {
	for _, r := range out.Results {
		if r.Error != nil && *r.Error != "" {
			return fmt.Errorf("%s", *r.Error)
		}
	}
	return nil
}

// vmIdentifiersTried names the identifiers a lookup used, for the not-found reason.
func vmIdentifiersTried(meta RequestMetadata) string {
	var parts []string
	for _, id := range []struct{ label, value string }{
		{"instance id", meta.VMInstanceID}, {"IP", meta.VMIP}, {"hostname", meta.VMHostname},
	} {
		if id.value != "" && vmIdentifierSafe.MatchString(id.value) {
			parts = append(parts, id.label+" "+id.value)
		}
	}
	return strings.Join(parts, " / ")
}

const (
	vmProbeMinLookback = time.Hour
	vmProbeMaxLookback = 30 * 24 * time.Hour
)

// vmProbeLookback is how far back the instance lookup may look for the VM's identity
// series. The probe is evaluated at the end of the window, so it has to reach back
// across the whole window: a VM that was stopped for the last hour of a 24h range
// still has data inside it, and a fixed 1h lookback would resolve nothing and hide
// all of it. Never less than an hour, and capped so a wild range cannot ask the store
// to scan months.
func vmProbeLookback(startMs, endMs int64) string {
	d := vmProbeMinLookback
	if startMs > 0 && endMs > startMs {
		if window := time.Duration(endMs-startMs) * time.Millisecond; window > d {
			d = window
		}
	}
	if d > vmProbeMaxLookback {
		d = vmProbeMaxLookback
	}
	// Whole minutes: coarse enough to read, fine enough to cover the window.
	return fmt.Sprintf("%dm", int64((d+time.Minute-1)/time.Minute))
}

// vmInstanceProbes renders one lookup query per identifier the request supplied, in
// priority order, each looking back lookback (a Prometheus duration). node-agent's
// identity series come first; node_uname_info (node_exporter) is the fallback for a
// VM that has no node-agent series, matched by hostname then by the scrape address.
func vmInstanceProbes(meta RequestMetadata, lookback string) []string {
	var probes []string
	add := func(value, format string) {
		if value != "" && vmIdentifierSafe.MatchString(value) {
			probes = append(probes, fmt.Sprintf(format, escapePromQLString(value), lookback))
		}
	}
	add(meta.VMInstanceID, `last_over_time(node_cloud_info{instance_id="%s"}[%s])`)
	add(meta.VMIP, `last_over_time(node_net_interface_ip{ip="%s"}[%s])`)
	add(meta.VMHostname, `last_over_time(node_info{hostname="%s"}[%s])`)
	add(meta.VMHostname, `last_over_time(node_uname_info{nodename="%s"}[%s])`)
	if meta.VMIP != "" && vmIdentifierSafe.MatchString(meta.VMIP) {
		// node_exporter's instance is "<ip>:<port>"; PromQL regex matchers are anchored.
		addr := escapePromQLString(regexp.QuoteMeta(meta.VMIP))
		probes = append(probes, fmt.Sprintf(`last_over_time(node_uname_info{instance=~"%s:.*"}[%s])`, addr, lookback))
	}
	return probes
}

// distinctVMInstances lists the distinct `instance` labels in a probe result.
func distinctVMInstances(out OutputMetricQuery) []string {
	seen := map[string]bool{}
	var instances []string
	for _, r := range out.Results {
		for _, series := range r.Payload {
			if instance := series.Metric["instance"]; instance != "" && !seen[instance] {
				seen[instance] = true
				instances = append(instances, instance)
			}
		}
	}
	sort.Strings(instances)
	return instances
}

// pickVMInstance returns the single `instance` of a probe result, or "" when the result
// is empty or names more than one. Two instances for one identifier means the
// identifier is not unique (a reused IP, a duplicated hostname); picking one would show
// another machine's numbers.
func pickVMInstance(out OutputMetricQuery) string {
	if instances := distinctVMInstances(out); len(instances) == 1 {
		return instances[0]
	}
	return ""
}

// buildPrometheusVMQueries renders the utilisation keys a VM can answer. Keys it
// cannot answer are left out, and completeUtilisationResults reports each with a
// note. With no resolved instance it renders nothing: an unscoped selector would
// match every VM sharing the store.
func buildPrometheusVMQueries(meta RequestMetadata, metrics []string) map[string]string {
	queries := make(map[string]string)
	if meta.VMInstance == "" {
		return queries
	}
	inst := fmt.Sprintf(`instance="%s"`, escapePromQLString(meta.VMInstance))

	rateWindow := meta.RateWindow
	if rateWindow == "" {
		rateWindow = "5m"
	}

	// node-agent series, then the node_exporter equivalent: when the first side has
	// data the second is dropped, so a VM never double counts.
	either := func(agent, exporter string) string { return agent + " or " + exporter }
	fsFilter := `fstype!~"tmpfs|devtmpfs|overlay|squashfs|ramfs"`

	cpu := either(
		fmt.Sprintf(`sum(rate(node_resources_cpu_usage_seconds_total{mode!="idle", %s}[%s]))`, inst, rateWindow),
		fmt.Sprintf(`sum(rate(node_cpu_seconds_total{mode!="idle", %s}[%s]))`, inst, rateWindow))
	memUsed := either(
		fmt.Sprintf(`sum(node_resources_memory_total_bytes{%s} - node_resources_memory_available_bytes{%s})`, inst, inst),
		fmt.Sprintf(`sum(node_memory_MemTotal_bytes{%s} - node_memory_MemAvailable_bytes{%s})`, inst, inst))

	for _, metricKey := range metrics {
		switch UtilisationKey(metricKey) {
		case KeyCPUUsage, KeyCPUUsageLine:
			queries[metricKey] = cpu
		case KeyCPUTotal:
			queries[metricKey] = either(
				fmt.Sprintf(`max(node_resources_cpu_logical_cores{%s})`, inst),
				fmt.Sprintf(`count(count by (cpu) (node_cpu_seconds_total{%s}))`, inst))
		case KeyMemoryUsage, KeyMemoryUsageLine:
			queries[metricKey] = memUsed
		case KeyMemTotal:
			queries[metricKey] = either(
				fmt.Sprintf(`max(node_resources_memory_total_bytes{%s})`, inst),
				fmt.Sprintf(`max(node_memory_MemTotal_bytes{%s})`, inst))
		case KeyDiskReadBytes:
			queries[metricKey] = either(
				fmt.Sprintf(`sum(rate(node_resources_disk_read_bytes_total{%s}[%s]))`, inst, rateWindow),
				fmt.Sprintf(`sum(rate(node_disk_read_bytes_total{%s}[%s]))`, inst, rateWindow))
		case KeyDiskWriteBytes:
			queries[metricKey] = either(
				fmt.Sprintf(`sum(rate(node_resources_disk_written_bytes_total{%s}[%s]))`, inst, rateWindow),
				fmt.Sprintf(`sum(rate(node_disk_written_bytes_total{%s}[%s]))`, inst, rateWindow))

		// Whole-disk figures from the filesystem metrics, one series per device (a
		// device mounted twice is counted once). The fallback is the volumes the VM's
		// services use, for an agent that exports no filesystem metrics.
		case KeyDiskTotal:
			queries[metricKey] = either(
				fmt.Sprintf(`sum(max by (device) (node_filesystem_size_bytes{%s, %s}))`, inst, fsFilter),
				fmt.Sprintf(`sum(max by (device) (container_resources_disk_size_bytes{%s}))`, inst))
		case KeyDiskUsed:
			queries[metricKey] = either(
				fmt.Sprintf(`sum(max by (device) (node_filesystem_size_bytes{%s, %s}) - max by (device) (node_filesystem_avail_bytes{%s, %s}))`, inst, fsFilter, inst, fsFilter),
				fmt.Sprintf(`sum(max by (device) (container_resources_disk_used_bytes{%s}))`, inst))

		// One series per service.
		case KeyVMServiceCPUUsage:
			queries[metricKey] = fmt.Sprintf(`sum by (container_id) (rate(container_resources_cpu_usage_seconds_total{%s}[%s]))`, inst, rateWindow)
		case KeyVMServiceMemoryUsage:
			queries[metricKey] = fmt.Sprintf(`sum by (container_id) (container_resources_memory_rss_bytes{%s})`, inst)
		case KeyVMServiceDiskUsed:
			queries[metricKey] = fmt.Sprintf(`sum by (container_id) (container_resources_disk_used_bytes{%s})`, inst)
		case KeyVMServiceGit:
			queries[metricKey] = fmt.Sprintf(`max by (container_id, git_repo, git_branch) (container_info{%s, git_repo!=""})`, inst)
		}
	}
	return queries
}

// vmKeyList is the set of keys buildPrometheusVMQueries answers, for tests and docs.
func vmKeyList() []string {
	return []string{
		string(KeyCPUUsage), string(KeyCPUUsageLine), string(KeyCPUTotal),
		string(KeyMemoryUsage), string(KeyMemoryUsageLine), string(KeyMemTotal),
		string(KeyDiskReadBytes), string(KeyDiskWriteBytes), string(KeyDiskTotal), string(KeyDiskUsed),
		string(KeyVMServiceCPUUsage), string(KeyVMServiceMemoryUsage), string(KeyVMServiceDiskUsed), string(KeyVMServiceGit),
	}
}

// vmNotFoundResult answers every requested key with the reason the VM could not be
// resolved, so the page can show it instead of an unexplained empty chart.
func vmNotFoundResult(keys []string, reason string) OutputMetricQuery {
	results := make([]QueryResult, 0, len(keys))
	for _, key := range keys {
		results = append(results, QueryResult{QueryKey: key, Payload: []Result{}, Note: reason})
	}
	return OutputMetricQuery{Results: results}
}

// The fleet queries select by metric name only. The names are node-agent's own,
// and `job` is not stable: it is set by node-agent's remote writer but is the
// customer's scrape job name when their Prometheus scrapes /metrics instead.

// vmFleetLookback keeps a host in the answer across a few missed writes (a store restart,
// the agent's spool draining) without listing machines that stopped long ago.
const vmFleetLookback = "10m"

// buildPrometheusVMFleetQueries renders the per-`instance` queries behind the VM list's
// CPU and memory gauges. They are not scoped to one VM: every agent writing to the store
// answers, and the caller matches instances to its VMs with the identity keys
// (node_cloud_info's instance_id and local_ipv4, node_info's hostname).
func buildPrometheusVMFleetQueries(metrics []string, rateWindow string) map[string]string {
	if rateWindow == "" {
		rateWindow = "5m"
	}
	queries := make(map[string]string)
	for _, metricKey := range metrics {
		switch UtilisationKey(metricKey) {
		case KeyVMFleetCPUUsed:
			queries[metricKey] = fmt.Sprintf(`sum by (instance) (rate(node_resources_cpu_usage_seconds_total{mode!="idle"}[%s]))`, rateWindow)
		case KeyVMFleetCPUCores:
			queries[metricKey] = fmt.Sprintf(`max by (instance) (last_over_time(node_resources_cpu_logical_cores[%s]))`, vmFleetLookback)
		case KeyVMFleetMemUsed:
			queries[metricKey] = fmt.Sprintf(`sum by (instance) (last_over_time(node_resources_memory_total_bytes[%s]) - last_over_time(node_resources_memory_available_bytes[%s]))`,
				vmFleetLookback, vmFleetLookback)
		case KeyVMFleetMemTotal:
			queries[metricKey] = fmt.Sprintf(`max by (instance) (last_over_time(node_resources_memory_total_bytes[%s]))`, vmFleetLookback)
		case KeyVMFleetIdentity:
			queries[metricKey] = fmt.Sprintf(`last_over_time(node_cloud_info[%s])`, vmFleetLookback)
		case KeyVMFleetHostname:
			queries[metricKey] = fmt.Sprintf(`last_over_time(node_info[%s])`, vmFleetLookback)
		}
	}
	return queries
}

// vmFleetKeyList is the set of keys buildPrometheusVMFleetQueries answers, for tests.
func vmFleetKeyList() []string {
	return []string{
		string(KeyVMFleetCPUUsed), string(KeyVMFleetCPUCores), string(KeyVMFleetMemUsed),
		string(KeyVMFleetMemTotal), string(KeyVMFleetIdentity), string(KeyVMFleetHostname),
	}
}
