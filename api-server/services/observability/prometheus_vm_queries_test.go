package observability

import (
	"errors"
	"strings"
	"testing"

	"nudgebee/services/security"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBuildPrometheusVMQueries_ScopedAndValid(t *testing.T) {
	meta := RequestMetadata{Kind: "vm", VMInstance: "abc123", RateWindow: "5m"}
	queries := buildPrometheusVMQueries(meta, vmKeyList())

	require.Len(t, queries, len(vmKeyList()), "every VM key must render")
	for key, q := range queries {
		_, err := promQLParser.ParseExpr(q)
		require.NoError(t, err, "%s: %s", key, q)
		assert.Contains(t, q, `instance="abc123"`, "%s must be scoped to the VM", key)
		assert.NotContains(t, q, "__CLUSTER__")
	}
}

func TestBuildPrometheusVMQueries_NoInstanceRendersNothing(t *testing.T) {
	// An unresolved VM must not fall back to an unscoped selector, which would sum
	// every VM that shares the store.
	assert.Empty(t, buildPrometheusVMQueries(RequestMetadata{Kind: "vm"}, vmKeyList()))
}

func TestBuildPrometheusVMQueries_EscapesInstance(t *testing.T) {
	q := buildPrometheusVMQueries(RequestMetadata{VMInstance: `a"} or vector(1) #`}, []string{string(KeyCPUUsage)})[string(KeyCPUUsage)]
	_, err := promQLParser.ParseExpr(q)
	require.NoError(t, err)
	assert.Contains(t, q, `instance="a\"} or vector(1) #"`)
}

func TestBuildPrometheusVMQueries_PerServiceGroupedByContainerID(t *testing.T) {
	q := buildPrometheusVMQueries(RequestMetadata{VMInstance: "i"}, []string{string(KeyVMServiceCPUUsage), string(KeyVMServiceMemoryUsage), string(KeyVMServiceDiskUsed)})
	for key, query := range q {
		assert.Contains(t, query, "sum by (container_id)", key)
	}
}

func TestBuildPrometheusVMQueries_UnsupportedKeysOmitted(t *testing.T) {
	q := buildPrometheusVMQueries(RequestMetadata{VMInstance: "i"}, []string{string(KeyCPURequest), string(KeyPodCount), string(KeyCPUUsage)})
	assert.Contains(t, q, string(KeyCPUUsage))
	assert.NotContains(t, q, string(KeyCPURequest))
	assert.NotContains(t, q, string(KeyPodCount))
}

func TestVMInstanceProbes_PriorityAndSafety(t *testing.T) {
	probes := vmInstanceProbes(RequestMetadata{VMInstanceID: "i-0abc", VMIP: "10.0.0.5", VMHostname: "web-1"}, "60m")
	require.Len(t, probes, 5, "three node-agent identity probes, then the two node_exporter fallbacks")
	assert.Contains(t, probes[0], "node_cloud_info")
	assert.Contains(t, probes[1], "node_net_interface_ip")
	assert.Contains(t, probes[2], "node_info")

	// Characters outside an id/IP/hostname are refused, not escaped.
	assert.Empty(t, vmInstanceProbes(RequestMetadata{VMInstanceID: `x"} or up`, VMIP: "1.1.1.1 or 2", VMHostname: "a{b}"}, "60m"))
	assert.Empty(t, vmInstanceProbes(RequestMetadata{}, "60m"))

	for _, p := range probes {
		_, err := promQLParser.ParseExpr(p)
		require.NoError(t, err, p)
	}
}

func vmProbeResult(instances ...string) OutputMetricQuery {
	payload := make([]Result, 0, len(instances))
	for _, i := range instances {
		payload = append(payload, Result{Metric: map[string]string{"instance": i}})
	}
	return OutputMetricQuery{Results: []QueryResult{{QueryKey: "vm_instance_probe", Payload: payload}}}
}

func TestPickVMInstance(t *testing.T) {
	assert.Equal(t, "h1", pickVMInstance(vmProbeResult("h1")))
	assert.Equal(t, "h1", pickVMInstance(vmProbeResult("h1", "h1")), "same instance across series is still one VM")
	assert.Empty(t, pickVMInstance(vmProbeResult()), "no match")
	assert.Empty(t, pickVMInstance(vmProbeResult("h1", "h2")), "an identifier shared by two VMs must not pick one")
	assert.False(t, strings.Contains(pickVMInstance(OutputMetricQuery{}), "x"))
}

func TestParseRequestMetadata_VMFields(t *testing.T) {
	m, err := parseRequestMetadata(map[string]any{
		"kind": "vm", "vm_instance_id": "i-1", "vm_ip": "10.0.0.5", "vm_hostname": "web",
		"metrics": []string{string(KeyCPUUsage)},
	})
	require.NoError(t, err)
	assert.Equal(t, "vm", m.Kind)
	assert.Equal(t, "i-1", m.VMInstanceID)
	assert.Equal(t, "10.0.0.5", m.VMIP)
	assert.Equal(t, "web", m.VMHostname)
}

func TestVMProbeLookback(t *testing.T) {
	const hour = int64(60 * 60 * 1000)
	tests := []struct {
		name       string
		start, end int64
		want       string
	}{
		{"no window", 0, 0, "60m"},
		{"inverted window", 10 * hour, 5 * hour, "60m"},
		{"shorter than the floor", 0 + 1, 1 + 30*60*1000, "60m"},
		{"6h", 100 * hour, 106 * hour, "360m"},
		{"24h", 100 * hour, 124 * hour, "1440m"},
		{"rounds a partial minute up", 0 + 1, 1 + 61*60*1000 + 1, "62m"},
		{"capped at 30 days", 0 + 1, 1 + 90*24*hour, "43200m"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, vmProbeLookback(tt.start, tt.end))
		})
	}
}

func TestVMInstanceProbes_UseTheLookback(t *testing.T) {
	probes := vmInstanceProbes(RequestMetadata{VMIP: "10.0.0.5"}, "1440m")
	require.Len(t, probes, 2)
	for _, p := range probes {
		assert.Contains(t, p, "[1440m]")
		_, err := promQLParser.ParseExpr(p)
		require.NoError(t, err)
	}
}

// ----- resolveVMInstance: the lookup must say why it failed --------------------

func stubVMProbe(t *testing.T, fn func(probe string) (OutputMetricQuery, error)) *[]string {
	t.Helper()
	var seen []string
	orig := vmProbeFetch
	t.Cleanup(func() { vmProbeFetch = orig })
	vmProbeFetch = func(_ *security.RequestContext, req FetchMetricsRequest) (OutputMetricQuery, error) {
		probe := req.Queries["vm_instance_probe"]
		seen = append(seen, probe)
		return fn(probe)
	}
	return &seen
}

func vmMeta() RequestMetadata {
	return RequestMetadata{Kind: "vm", VMInstanceID: "i-0abc", VMIP: "10.0.0.5", VMHostname: "web-1"}
}

func lookupVM(t *testing.T, meta RequestMetadata) vmLookup {
	t.Helper()
	ctx := security.NewRequestContextForSuperAdmin(nil, nil, nil)
	return resolveVMInstance(ctx, GetUtilisationTrendRequest{AccountId: "acc"}, "prometheus", "user", meta)
}

func TestResolveVMInstance_FoundOnFirstIdentifierStops(t *testing.T) {
	seen := stubVMProbe(t, func(string) (OutputMetricQuery, error) { return vmProbeResult("hash1"), nil })
	got := lookupVM(t, vmMeta())
	assert.Equal(t, vmLookup{Instance: "hash1"}, got)
	assert.Len(t, *seen, 1, "the strongest identifier matched, so the weaker ones must not be queried")
	assert.Contains(t, (*seen)[0], "node_cloud_info")
}

func TestResolveVMInstance_FallsBackToWeakerIdentifiers(t *testing.T) {
	stubVMProbe(t, func(probe string) (OutputMetricQuery, error) {
		if strings.Contains(probe, "node_net_interface_ip") {
			return vmProbeResult("by-ip"), nil
		}
		return vmProbeResult(), nil
	})
	assert.Equal(t, "by-ip", lookupVM(t, vmMeta()).Instance)
}

func TestResolveVMInstance_QueryErrorIsReportedNotSwallowed(t *testing.T) {
	// A failing query rides on the result, not on the returned error. Reporting it as
	// "no machine found" would hide a forager or datasource fault.
	msg := "prometheus: request to /api/v1/query via forager failed: datasource a63b4384 not found"
	stubVMProbe(t, func(string) (OutputMetricQuery, error) {
		return OutputMetricQuery{Results: []QueryResult{{QueryKey: "vm_instance_probe", Error: &msg}}}, nil
	})
	got := lookupVM(t, vmMeta())
	assert.Empty(t, got.Instance)
	assert.Contains(t, got.Reason, "could not be queried")
	assert.Contains(t, got.Reason, "datasource a63b4384 not found")
}

func TestResolveVMInstance_TopLevelErrorIsReported(t *testing.T) {
	stubVMProbe(t, func(string) (OutputMetricQuery, error) { return OutputMetricQuery{}, errors.New("no metrics provider") })
	assert.Contains(t, lookupVM(t, vmMeta()).Reason, "no metrics provider")
}

func TestResolveVMInstance_NoMatchNamesTheIdentifiersTried(t *testing.T) {
	stubVMProbe(t, func(string) (OutputMetricQuery, error) { return vmProbeResult(), nil })
	got := lookupVM(t, vmMeta())
	assert.Empty(t, got.Instance)
	assert.Contains(t, got.Reason, "instance id i-0abc")
	assert.Contains(t, got.Reason, "IP 10.0.0.5")
	assert.Contains(t, got.Reason, "hostname web-1")
	assert.Contains(t, got.Reason, "node-agent or node_exporter")
}

func TestResolveVMInstance_AmbiguousIdentifierIsReported(t *testing.T) {
	stubVMProbe(t, func(string) (OutputMetricQuery, error) { return vmProbeResult("h1", "h2"), nil })
	got := lookupVM(t, vmMeta())
	assert.Empty(t, got.Instance)
	assert.Contains(t, got.Reason, "not unique")
	assert.Contains(t, got.Reason, "h1")
}

func TestResolveVMInstance_NoIdentifiers(t *testing.T) {
	seen := stubVMProbe(t, func(string) (OutputMetricQuery, error) { t.Fatal("must not query"); return OutputMetricQuery{}, nil })
	got := lookupVM(t, RequestMetadata{Kind: "vm"})
	assert.Empty(t, got.Instance)
	assert.Contains(t, got.Reason, "no usable instance id")
	assert.Empty(t, *seen)
}

func TestVMNotFoundResult_AnswersEveryKeyWithTheReason(t *testing.T) {
	out := vmNotFoundResult([]string{"cpu_usage_line", "mem_total"}, "because")
	require.Len(t, out.Results, 2)
	for _, r := range out.Results {
		assert.Equal(t, "because", r.Note)
		assert.NotNil(t, r.Payload, "payload must serialise as [] not null")
	}
}

func TestBuildPrometheusVMFleetQueries_AllKeysRenderAndParse(t *testing.T) {
	queries := buildPrometheusVMFleetQueries(vmFleetKeyList(), "5m")
	require.Len(t, queries, len(vmFleetKeyList()), "every fleet key must render")
	for key, q := range queries {
		_, err := promQLParser.ParseExpr(q)
		require.NoError(t, err, "%s: %s", key, q)
		assert.NotContains(t, q, "job=", "%s must not depend on the job label, which is the customer's scrape job when scraped", key)
		assert.NotContains(t, q, "__CLUSTER__")
	}
}

func TestBuildPrometheusVMFleetQueries_GroupedPerInstance(t *testing.T) {
	queries := buildPrometheusVMFleetQueries([]string{
		string(KeyVMFleetCPUUsed), string(KeyVMFleetCPUCores), string(KeyVMFleetMemUsed), string(KeyVMFleetMemTotal),
	}, "")
	for key, q := range queries {
		assert.Contains(t, q, "by (instance)", "%s must answer per machine, not summed across the fleet", key)
	}
	assert.Contains(t, queries[string(KeyVMFleetCPUUsed)], "[5m]", "an empty rate window falls back to 5m")
	assert.Contains(t, queries[string(KeyVMFleetCPUUsed)], `mode!="idle"`)
}

func TestBuildPrometheusVMFleetQueries_IgnoresOtherKeys(t *testing.T) {
	q := buildPrometheusVMFleetQueries([]string{string(KeyCPUUsage), string(KeyVMServiceCPUUsage), string(KeyVMFleetIdentity)}, "5m")
	assert.Len(t, q, 1)
	assert.Contains(t, q, string(KeyVMFleetIdentity))
}

func TestVMInstanceProbes_NodeExporterFallbackAfterNodeAgent(t *testing.T) {
	probes := vmInstanceProbes(RequestMetadata{VMIP: "10.0.0.5", VMHostname: "web-1"}, "60m")
	require.Len(t, probes, 4)
	assert.Contains(t, probes[0], "node_net_interface_ip")
	assert.Contains(t, probes[1], "node_info")
	assert.Contains(t, probes[2], `node_uname_info{nodename="web-1"}`)
	assert.Contains(t, probes[3], `node_uname_info{instance=~"10\\.0\\.0\\.5:.*"}`, "dots in the IP must be escaped in the regex")
	for _, p := range probes {
		_, err := promQLParser.ParseExpr(p)
		require.NoError(t, err, p)
	}
}

func TestResolveVMInstance_NodeExporterOnlyVM(t *testing.T) {
	stubVMProbe(t, func(probe string) (OutputMetricQuery, error) {
		if strings.Contains(probe, "node_uname_info{nodename=") {
			return vmProbeResult("10.0.0.5:9100"), nil
		}
		return vmProbeResult(), nil
	})
	assert.Equal(t, "10.0.0.5:9100", lookupVM(t, vmMeta()).Instance)
}

func TestBuildPrometheusVMQueries_NodeExporterAlternatives(t *testing.T) {
	q := buildPrometheusVMQueries(RequestMetadata{VMInstance: "i"}, vmKeyList())
	for key, want := range map[string]string{
		string(KeyCPUUsage):       "node_cpu_seconds_total",
		string(KeyCPUTotal):       "node_cpu_seconds_total",
		string(KeyMemoryUsage):    "node_memory_MemAvailable_bytes",
		string(KeyMemTotal):       "node_memory_MemTotal_bytes",
		string(KeyDiskReadBytes):  "node_disk_read_bytes_total",
		string(KeyDiskWriteBytes): "node_disk_written_bytes_total",
	} {
		assert.Contains(t, q[key], want, key)
		assert.Contains(t, q[key], " or ", "%s must keep the node-agent series first", key)
	}
}

func TestBuildPrometheusVMQueries_DiskFromFilesystemMetricsWithFallback(t *testing.T) {
	q := buildPrometheusVMQueries(RequestMetadata{VMInstance: "i"}, []string{string(KeyDiskTotal), string(KeyDiskUsed)})
	total, used := q[string(KeyDiskTotal)], q[string(KeyDiskUsed)]

	assert.True(t, strings.HasPrefix(total, "sum(max by (device) (node_filesystem_size_bytes"), "real filesystem size comes first: %s", total)
	assert.Contains(t, total, "container_resources_disk_size_bytes", "agents without filesystem metrics keep the service-volume figure")
	assert.Contains(t, used, "node_filesystem_avail_bytes")
	assert.Contains(t, used, "container_resources_disk_used_bytes")
	assert.Contains(t, total, `fstype!~"tmpfs|devtmpfs|overlay|squashfs|ramfs"`, "pseudo filesystems must not inflate capacity")
}

func TestVMMetricsSupported(t *testing.T) {
	for _, p := range []string{"prometheus", "victoria_metrics", "chronosphere", "cubeapm"} {
		assert.True(t, vmMetricsSupported(p), p)
	}
	for _, p := range []string{"datadog", "newrelic", "dynatrace", "solarwinds", "openobserve", "splunk_enterprise", "ES", ""} {
		assert.False(t, vmMetricsSupported(p), p)
	}
	assert.Contains(t, vmMetricsUnsupportedNote("datadog"), "datadog")
}
