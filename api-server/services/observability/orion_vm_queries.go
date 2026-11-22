package observability

import (
	"fmt"
	"strconv"
	"strings"
)

// Orion's VM data model.
//
// A machine in Orion is a row in Orion.Nodes, identified by an integer NodeID.
// Its history lives in statistics entities keyed on that NodeID:
//
//	Orion.CPULoad            AvgLoad (PERCENT, 0-100), AvgMemoryUsed (bytes),
//	                         TotalMemory (bytes), ObservationTimestamp (UTC), Weight
//	Orion.VolumeUsageHistory AvgDiskUsed (bytes), DiskSize (bytes), per VolumeID
//
// Two conversions matter, because the utilisation keys are not in Orion's units:
//   - cpu_usage is CORES, Orion stores a percentage -> (AvgLoad / 100) * CPUCount.
//   - memory is bytes on both sides, so it passes through.
//
// Verified against a live SolarWinds Platform 2026.4.0: a node reporting AvgLoad 16
// with CPUCount 4 renders 0.64 cores.
//
// Orion keeps three retention tiers in one view (detail, hourly, daily) and marks
// each row's span in Weight. It stores Min/Avg/Max only, never percentiles, so the
// p50/p90 keys are not rendered here and completeUtilisationResults notes them.

// orionTimeParams are the placeholders every time-scoped query carries.
// OrionMetricSource binds them from the request window; they are never interpolated
// here, so a window can never be pasted into the statement text.
const (
	orionParamStart = "@start"
	orionParamEnd   = "@end"
)

// Column aliases the row-to-series converter understands. Any other column in a
// result becomes a label on the series.
const (
	orionColTime  = "nb_time"
	orionColValue = "nb_value"
)

// orionNodeFilter is the identity clause. NodeID is an integer parsed from the
// resolver's answer, so it is safe to render into the statement.
func orionNodeFilter(nodeID string) string {
	return fmt.Sprintf("NodeID = %s", nodeID)
}

// buildOrionVMQueries renders the keys one machine can answer from Orion. Keys it
// cannot answer are left out, and completeUtilisationResults reports each with a
// note. With no resolved node it renders nothing: an unscoped query would return
// every machine in the Orion install.
//
// Not rendered, and why:
//   - disk_read_bytes / disk_write_bytes: Orion.Volumes carries DiskReads,
//     DiskWrites and TotalDiskIOPS as current values only, with no history entity,
//     and they count operations rather than bytes.
//   - vm_service_* (4 keys): Orion has no per-service breakdown of a host.
func buildOrionVMQueries(meta RequestMetadata, metrics []string) map[string]string {
	queries := make(map[string]string)
	if meta.VMInstance == "" {
		return queries
	}
	node := orionNodeFilter(meta.VMInstance)

	// The core count lives on the node row, not the statistics row, so anything
	// needing it joins. Memory is already in bytes on the statistics row.
	joinedSeries := func(expr string) string {
		return fmt.Sprintf(
			`SELECT c.ObservationTimestamp AS %s, %s AS %s `+
				`FROM Orion.CPULoad c JOIN Orion.Nodes n ON c.NodeID = n.NodeID `+
				`WHERE c.%s AND c.ObservationTimestamp >= %s AND c.ObservationTimestamp <= %s `+
				`ORDER BY c.ObservationTimestamp`,
			orionColTime, expr, orionColValue, node, orionParamStart, orionParamEnd)
	}

	timeSeries := func(expr string) string {
		return fmt.Sprintf(
			`SELECT ObservationTimestamp AS %s, %s AS %s FROM Orion.CPULoad `+
				`WHERE %s AND ObservationTimestamp >= %s AND ObservationTimestamp <= %s `+
				`ORDER BY ObservationTimestamp`,
			orionColTime, expr, orionColValue, node, orionParamStart, orionParamEnd)
	}

	// Disk is per volume in Orion; the keys want one figure for the machine, so the
	// volumes are summed per observation.
	volumeSeries := func(expr string) string {
		return fmt.Sprintf(
			`SELECT ObservationTimestamp AS %s, SUM(%s) AS %s FROM Orion.VolumeUsageHistory `+
				`WHERE %s AND ObservationTimestamp >= %s AND ObservationTimestamp <= %s `+
				`GROUP BY ObservationTimestamp ORDER BY ObservationTimestamp`,
			orionColTime, expr, orionColValue, node, orionParamStart, orionParamEnd)
	}

	for _, metricKey := range metrics {
		switch UtilisationKey(metricKey) {
		case KeyCPUUsage, KeyCPUUsageLine:
			queries[metricKey] = joinedSeries("(c.AvgLoad / 100.0) * n.CPUCount")
		case KeyCPUTotal:
			queries[metricKey] = joinedSeries("n.CPUCount")
		case KeyMemoryUsage, KeyMemoryUsageLine:
			queries[metricKey] = timeSeries("AvgMemoryUsed")
		case KeyMemTotal:
			queries[metricKey] = timeSeries("TotalMemory")
		case KeyDiskUsed:
			queries[metricKey] = volumeSeries("AvgDiskUsed")
		case KeyDiskTotal:
			queries[metricKey] = volumeSeries("DiskSize")
		}
	}
	return queries
}

// orionVMKeyList is the set of keys buildOrionVMQueries answers.
func orionVMKeyList() []string {
	return []string{
		string(KeyCPUUsage), string(KeyCPUUsageLine), string(KeyCPUTotal),
		string(KeyMemoryUsage), string(KeyMemoryUsageLine), string(KeyMemTotal),
		string(KeyDiskUsed), string(KeyDiskTotal),
	}
}

// buildOrionVMFleetQueries renders the VM list's gauges: one row per machine Orion
// monitors, not scoped to one VM. The caller matches them to its own VMs through
// the identity keys, so the labels must be the ones joinFleetMetrics reads —
// `instance` on every series, plus `local_ipv4` on the identity series and
// `hostname` on the hostname series.
//
// These read current values from Orion.Nodes rather than history: the list shows
// "now", and a node's last poll is already on its row. CPULoad is -2 on a node Orion
// has never polled, so those are excluded rather than drawn as negative load.
func buildOrionVMFleetQueries(metrics []string) map[string]string {
	queries := make(map[string]string)

	const polled = "WHERE n.CPULoad >= 0 AND n.CPUCount IS NOT NULL"
	fleet := func(expr string) string {
		return fmt.Sprintf(
			`SELECT n.NodeID AS instance, %s AS %s FROM Orion.Nodes n %s`,
			expr, orionColValue, polled)
	}

	for _, metricKey := range metrics {
		switch UtilisationKey(metricKey) {
		case KeyVMFleetCPUUsed:
			queries[metricKey] = fleet("(n.CPULoad / 100.0) * n.CPUCount")
		case KeyVMFleetCPUCores:
			queries[metricKey] = fleet("n.CPUCount")
		case KeyVMFleetMemUsed:
			queries[metricKey] = fleet("n.MemoryUsed")
		case KeyVMFleetMemTotal:
			queries[metricKey] = fleet("n.TotalMemory")

		// Identity and hostname carry no measurement: the value is a constant 1, the
		// way node_cloud_info and node_info do, and the labels are the payload.
		// Orion has no cloud instance id, so only local_ipv4 is published.
		case KeyVMFleetIdentity:
			queries[metricKey] = fmt.Sprintf(
				`SELECT n.NodeID AS instance, n.IPAddress AS local_ipv4, 1 AS %s FROM Orion.Nodes n %s AND n.IPAddress IS NOT NULL`,
				orionColValue, polled)
		case KeyVMFleetHostname:
			queries[metricKey] = fmt.Sprintf(
				`SELECT n.NodeID AS instance, n.SysName AS hostname, 1 AS %s FROM Orion.Nodes n %s AND n.SysName IS NOT NULL AND n.SysName <> ''`,
				orionColValue, polled)
		}
	}
	return queries
}

// orionVMFleetKeyList is the set of keys buildOrionVMFleetQueries answers.
func orionVMFleetKeyList() []string {
	return []string{
		string(KeyVMFleetCPUUsed), string(KeyVMFleetCPUCores), string(KeyVMFleetMemUsed),
		string(KeyVMFleetMemTotal), string(KeyVMFleetIdentity), string(KeyVMFleetHostname),
	}
}

// orionNodeProbes renders one lookup per identifier the request supplied, strongest
// first. Orion has no cloud instance id, so the IP and the names are all there is.
//
// Every value is bound as a SWQL parameter rather than rendered, because a hostname
// or IP arrives from the request. The returned parameter map is passed with the
// query.
//
// A machine's IP is NOT unique in Orion — a node added twice, or a host and its
// management interface, share one address (seen on the test bed) — so the caller
// must treat more than one match as no match.
func orionNodeProbes(meta RequestMetadata) []orionProbe {
	var probes []orionProbe
	add := func(value, column string) {
		if v := strings.TrimSpace(value); v != "" {
			probes = append(probes, orionProbe{
				Query:  fmt.Sprintf(`SELECT NodeID AS instance FROM Orion.Nodes WHERE %s = @value`, column),
				Params: map[string]any{"value": v},
				Using:  column,
			})
		}
	}
	add(meta.VMIP, "IPAddress")
	add(meta.VMHostname, "SysName")
	add(meta.VMHostname, "DNS")
	add(meta.VMHostname, "Caption")
	return probes
}

// orionProbe is one identity lookup.
type orionProbe struct {
	Query  string
	Params map[string]any
	Using  string
}

// orionNodeIDValid reports whether a resolved instance is an Orion NodeID. Query
// builders render it into statement text, so nothing else may reach them.
func orionNodeIDValid(s string) bool {
	if s == "" {
		return false
	}
	_, err := strconv.ParseUint(s, 10, 63)
	return err == nil
}
