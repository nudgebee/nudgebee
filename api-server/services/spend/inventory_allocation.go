package spend

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"math"
	"nudgebee/services/internal/database"
	"nudgebee/services/security"
	"strings"
	"time"

	"k8s.io/apimachinery/pkg/api/resource"
)

// Cost allocation computed from the Kubernetes inventory in Postgres, for clusters
// that have no Prometheus for OpenCost to read.
//
// The central OpenCost engine (cost-server) reaches every cluster's metrics through
// relay-server's /prometheus facade — the agent's `globalConfig.prometheus_url`.
// A customer who runs the Elastic Agent instead of Prometheus has no such endpoint,
// so OpenCost returns nothing and the cluster shows no Kubernetes cost at all. Not
// a partial number: zero rows.
//
// Everything the dominant cost components need is already in our own database,
// written by the k8s agent's discovery: node instance type and its hourly on-demand
// price (k8s_nodes.cost, resolved from cloud_resource_details), node capacity, pod
// lifetimes, and each pod's container resource requests. This computes allocation
// from those directly, and emits the exact payload shape the collector's
// process_spend already consumes, so nothing downstream changes.
//
// What it deliberately does NOT produce, because the inventory cannot know it:
//
//   - usage (cpuCoreUsageAverage, ramByteUsageAverage) and therefore efficiency —
//     left at zero, which process_spend skips rather than storing as a real zero.
//   - storage, network egress, load balancer and GPU cost — left at zero. A cluster
//     with heavy PV or cross-zone traffic will read low by that amount.
//   - usage-based attribution for containers with no resource requests. Their share
//     of the node stays unattributed at pod level; the node row still carries the
//     full node cost, so cluster totals remain right even when pod-level attribution
//     is incomplete.
//
// Allocation is requests-based: a pod is charged for what it reserved on its node,
// which is what the node's owner is actually paying for.

const (
	// OpenCost's default component prices (its configs/default.json). Used only for
	// their RATIO. The node's real hourly price comes from cloud_resource_details;
	// splitting it between CPU and RAM needs a proportion, and this is the same one
	// OpenCost derives when it knows a total node price but no component prices
	// (costmodel.go: ramPrice = nodePrice / ramMultiple; cpuPrice = ramPrice *
	// cpuToRAMRatio). Mirroring its arithmetic keeps cpuCost/ramCost comparable with
	// what the Prometheus-backed path would have produced for the same node.
	openCostDefaultCPUPricePerCoreHr = 0.031611
	openCostDefaultRAMPricePerGiBHr  = 0.004237

	mibPerGiB   = 1024.0
	bytesPerGiB = 1024.0 * 1024.0 * 1024.0

	// Fallback when the collector's step is absent or unparseable. The spends table
	// is keyed (tenant, cloud_account, cloud_resource_id, date) with date truncated
	// to midnight, so a day-sized interval is the natural unit: anything finer would
	// have several intervals collapse onto one row, each overwriting the last.
	defaultAllocationStep = 24 * time.Hour
)

// allocationWindow mirrors OpenCost's window object. The collector parses `start`
// with datetime.fromisoformat and truncates it to midnight for the spends row, so
// the explicit +00:00 offset (rather than a "Z" suffix) is deliberate — it parses
// on every Python 3 version, "Z" only from 3.11.
type allocationWindow struct {
	Start string `json:"start"`
	End   string `json:"end"`
}

// allocationEntry is one OpenCost allocation record.
//
// Every field here is REQUIRED, including the ones this path always leaves at zero.
// The collector iterates its sum_columns/avg_columns lists and indexes each one
// directly (`k8s_data[key]` in get_metric_data), so a missing key is a KeyError that
// fails the whole ingest — not a skipped metric.
type allocationEntry struct {
	Name       string            `json:"name,omitempty"`
	Properties map[string]string `json:"properties,omitempty"`
	Window     allocationWindow  `json:"window"`

	// sum_columns
	CPUCores                   float64 `json:"cpuCores"`
	CPUCoreHours               float64 `json:"cpuCoreHours"`
	CPUCost                    float64 `json:"cpuCost"`
	CPUCostAdjustment          float64 `json:"cpuCostAdjustment"`
	GPUCount                   float64 `json:"gpuCount"`
	GPUHours                   float64 `json:"gpuHours"`
	GPUCost                    float64 `json:"gpuCost"`
	GPUCostAdjustment          float64 `json:"gpuCostAdjustment"`
	NetworkTransferBytes       float64 `json:"networkTransferBytes"`
	NetworkReceiveBytes        float64 `json:"networkReceiveBytes"`
	NetworkCost                float64 `json:"networkCost"`
	NetworkCrossZoneCost       float64 `json:"networkCrossZoneCost"`
	NetworkCrossRegionCost     float64 `json:"networkCrossRegionCost"`
	NetworkInternetCost        float64 `json:"networkInternetCost"`
	NetworkCostAdjustment      float64 `json:"networkCostAdjustment"`
	LoadBalancerCost           float64 `json:"loadBalancerCost"`
	LoadBalancerCostAdjustment float64 `json:"loadBalancerCostAdjustment"`
	PVBytes                    float64 `json:"pvBytes"`
	PVByteHours                float64 `json:"pvByteHours"`
	PVCost                     float64 `json:"pvCost"`
	PVCostAdjustment           float64 `json:"pvCostAdjustment"`
	RAMBytes                   float64 `json:"ramBytes"`
	RAMByteHours               float64 `json:"ramByteHours"`
	RAMCost                    float64 `json:"ramCost"`
	RAMCostAdjustment          float64 `json:"ramCostAdjustment"`
	ExternalCost               float64 `json:"externalCost"`
	SharedCost                 float64 `json:"sharedCost"`
	TotalCost                  float64 `json:"totalCost"`

	// avg_columns
	CPUCoreRequestAverage float64 `json:"cpuCoreRequestAverage"`
	CPUCoreUsageAverage   float64 `json:"cpuCoreUsageAverage"`
	CPUEfficiency         float64 `json:"cpuEfficiency"`
	RAMByteRequestAverage float64 `json:"ramByteRequestAverage"`
	RAMByteUsageAverage   float64 `json:"ramByteUsageAverage"`
	RAMEfficiency         float64 `json:"ramEfficiency"`
	TotalEfficiency       float64 `json:"totalEfficiency"`
}

// inventoryNode is one row of k8s_nodes, in the units that table stores:
// cpu_capacity in cores, memory_capacity in MiB, cost in USD per hour.
type inventoryNode struct {
	Name          string
	CPUCores      float64
	CPUAllocCores float64
	RAMMiB        float64
	RAMAllocMiB   float64
	HourlyCost    float64
	Created       time.Time
	LastSeen      time.Time
	Active        bool
}

type inventoryPod struct {
	Namespace string
	Name      string
	NodeName  string
	CPUCores  float64
	RAMBytes  float64
	Created   time.Time
	LastSeen  time.Time
	Active    bool
}

// nodePricing is a node's hourly price split into per-core and per-GiB components.
type nodePricing struct {
	CPUPerCoreHr float64
	RAMPerGiBHr  float64
}

// splitNodePrice divides a node's total hourly price between CPU and RAM, following
// OpenCost's own derivation so both paths agree on the split for a given node.
//
// Returns a zero split for a node with no price or no capacity, which yields zero
// cost rather than a non-finite value — neither NaN nor ±Inf can be serialised as
// JSON, so either would fail the ingest.
func splitNodePrice(cpuCores, ramGiB, hourlyCost float64) nodePricing {
	cpuToRAMRatio := openCostDefaultCPUPricePerCoreHr / openCostDefaultRAMPricePerGiBHr
	ramMultiple := cpuCores*cpuToRAMRatio + ramGiB
	// Non-finite values are checked explicitly rather than relying on the range
	// checks below. NaN fails every comparison, so it sails through `hourlyCost <= 0`;
	// +Inf passes the range checks outright. Both then produce a non-finite cost,
	// which json.Marshal rejects with UnsupportedValueError — failing the collector's
	// ingest for the whole account, not just this node. Postgres float8 stores NaN
	// and ±Infinity, so both are reachable from k8s_nodes.cost.
	if !isFinite(hourlyCost) || !isFinite(ramMultiple) || ramMultiple <= 0 || hourlyCost <= 0 {
		return nodePricing{}
	}
	ramPrice := hourlyCost / ramMultiple
	return nodePricing{
		CPUPerCoreHr: ramPrice * cpuToRAMRatio,
		RAMPerGiBHr:  ramPrice,
	}
}

// isFinite reports whether f can be serialised as JSON. NaN and ±Inf cannot, and
// json.Marshal fails the whole payload rather than the offending field.
func isFinite(f float64) bool {
	return !math.IsNaN(f) && !math.IsInf(f, 0)
}

// parseAllocationStep reads the collector's step string ("1d", "24h", "1h"). Go's
// ParseDuration has no day unit, so "d" is expanded first.
func parseAllocationStep(step string) time.Duration {
	s := strings.TrimSpace(strings.ToLower(step))
	if s == "" {
		return defaultAllocationStep
	}
	if days, err := parseDaySuffix(s); err == nil {
		return days
	}
	if d, err := time.ParseDuration(s); err == nil && d > 0 {
		return d
	}
	return defaultAllocationStep
}

func parseDaySuffix(s string) (time.Duration, error) {
	if !strings.HasSuffix(s, "d") {
		return 0, fmt.Errorf("not a day duration")
	}
	var n int
	if _, err := fmt.Sscanf(strings.TrimSuffix(s, "d"), "%d", &n); err != nil || n <= 0 {
		return 0, fmt.Errorf("not a day duration")
	}
	return time.Duration(n) * 24 * time.Hour, nil
}

type allocationInterval struct {
	Start time.Time
	End   time.Time
}

// splitAllocationIntervals divides the sync window into step-sized intervals. The
// final interval is truncated at the window end rather than extended past it, so a
// window that is not a whole number of steps cannot bill for time it did not cover.
func splitAllocationIntervals(start, end time.Time, step time.Duration) []allocationInterval {
	if !end.After(start) || step <= 0 {
		return nil
	}
	var out []allocationInterval
	for cur := start; cur.Before(end); cur = cur.Add(step) {
		next := cur.Add(step)
		if next.After(end) {
			next = end
		}
		out = append(out, allocationInterval{Start: cur, End: next})
	}
	return out
}

// overlapHours returns how many hours [aStart,aEnd] and [bStart,bEnd] share.
func overlapHours(aStart, aEnd, bStart, bEnd time.Time) float64 {
	start := aStart
	if bStart.After(start) {
		start = bStart
	}
	end := aEnd
	if bEnd.Before(end) {
		end = bEnd
	}
	if !end.After(start) {
		return 0
	}
	return end.Sub(start).Hours()
}

// podRequests sums a pod's container resource requests from the stored pod spec.
//
// Quantities are Kubernetes quantity strings and must be parsed as such: memory is
// routinely stored in forms like "1395864371200m" (milli-bytes, ~1.3 GiB), so
// stripping the suffix would be wrong by a factor of 1000.
func podRequests(meta []byte) (cpuCores, ramBytes float64) {
	var parsed struct {
		Config struct {
			Containers []struct {
				Resources struct {
					Requests map[string]string `json:"requests"`
				} `json:"resources"`
			} `json:"containers"`
		} `json:"config"`
	}
	if err := json.Unmarshal(meta, &parsed); err != nil {
		return 0, 0
	}
	for _, c := range parsed.Config.Containers {
		if v, ok := c.Resources.Requests["cpu"]; ok && v != "" {
			if q, err := resource.ParseQuantity(v); err == nil {
				cpuCores += float64(q.MilliValue()) / 1000.0
			}
		}
		if v, ok := c.Resources.Requests["memory"]; ok && v != "" {
			if q, err := resource.ParseQuantity(v); err == nil {
				ramBytes += float64(q.Value())
			}
		}
	}
	return cpuCores, ramBytes
}

// loadInventoryNodes reads the account's nodes. Nodes with no resolved price are
// kept: they still carry capacity, and a zero-cost node is a visible signal that
// cloud_resource_details has no price for that instance type, which is more useful
// than the node vanishing from the report.
func loadInventoryNodes(ctx *security.RequestContext, accountId string) ([]inventoryNode, error) {
	dbms, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		return nil, err
	}
	rows, err := dbms.Db.QueryxContext(ctx.GetContext(), `
		SELECT name,
		       COALESCE(cpu_capacity, 0)        AS cpu_capacity,
		       COALESCE(cpu_allocatable, 0)     AS cpu_allocatable,
		       COALESCE(memory_capacity, 0)     AS memory_capacity,
		       COALESCE(memory_allocatable, 0)  AS memory_allocatable,
		       COALESCE(cost, 0)                AS cost,
		       node_creation_time,
		       COALESCE(is_active, false)       AS is_active,
		       updated_at
		FROM k8s_nodes
		WHERE cloud_account_id = $1`, accountId)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var out []inventoryNode
	for rows.Next() {
		var (
			n         inventoryNode
			created   *time.Time
			updatedAt *time.Time
		)
		if err := rows.Scan(&n.Name, &n.CPUCores, &n.CPUAllocCores, &n.RAMMiB, &n.RAMAllocMiB,
			&n.HourlyCost, &created, &n.Active, &updatedAt); err != nil {
			return nil, err
		}
		if created != nil {
			n.Created = *created
		}
		if updatedAt != nil {
			n.LastSeen = *updatedAt
		}
		out = append(out, n)
	}
	return out, rows.Err()
}

// loadInventoryPods reads the pods that overlap the window at all, so a long-dead
// pod does not have its spec parsed for a window it cannot appear in.
func loadInventoryPods(ctx *security.RequestContext, accountId string, start, end time.Time) ([]inventoryPod, error) {
	dbms, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		return nil, err
	}
	rows, err := dbms.Db.QueryxContext(ctx.GetContext(), `
		SELECT namespace, name, COALESCE(node_name, ''), creation_time,
		       last_seen, COALESCE(is_active, false), COALESCE(meta::text, '{}')
		FROM k8s_pods
		WHERE cloud_account_id = $1
		  AND (creation_time IS NULL OR creation_time <= $3)
		  AND (is_active = true OR last_seen IS NULL OR last_seen >= $2)`,
		accountId, start, end)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var out []inventoryPod
	for rows.Next() {
		var (
			p        inventoryPod
			created  *time.Time
			lastSeen *time.Time
			meta     string
		)
		if err := rows.Scan(&p.Namespace, &p.Name, &p.NodeName, &created, &lastSeen, &p.Active, &meta); err != nil {
			return nil, err
		}
		if created != nil {
			p.Created = *created
		}
		if lastSeen != nil {
			p.LastSeen = *lastSeen
		}
		p.CPUCores, p.RAMBytes = podRequests([]byte(meta))
		out = append(out, p)
	}
	return out, rows.Err()
}

// activeRange returns the interval a resource was alive for, treating a still-active
// resource as alive to the end of the window rather than to its last heartbeat —
// `last_seen`/`updated_at` lag the present by one discovery cycle, and billing a
// running node only up to its last sync would undercount every window.
func activeRange(created, lastSeen time.Time, active bool, windowEnd time.Time) (time.Time, time.Time) {
	end := lastSeen
	if active || end.IsZero() {
		end = windowEnd
	}
	return created, end
}

// mergeAllocationEntries adds a second run of the same pod into the first. Levels
// (cores, bytes, and the request averages derived from them) are taken at their
// maximum rather than summed: two sequential instances of one pod did not reserve
// twice the memory, they reserved it twice over. Hours and costs are additive
// because they are quantities of consumption.
func mergeAllocationEntries(a, b allocationEntry) allocationEntry {
	out := a
	out.CPUCores = math.Max(a.CPUCores, b.CPUCores)
	out.RAMBytes = math.Max(a.RAMBytes, b.RAMBytes)
	out.CPUCoreRequestAverage = math.Max(a.CPUCoreRequestAverage, b.CPUCoreRequestAverage)
	out.RAMByteRequestAverage = math.Max(a.RAMByteRequestAverage, b.RAMByteRequestAverage)
	out.CPUCoreHours = a.CPUCoreHours + b.CPUCoreHours
	out.RAMByteHours = a.RAMByteHours + b.RAMByteHours
	out.CPUCost = a.CPUCost + b.CPUCost
	out.RAMCost = a.RAMCost + b.RAMCost
	out.TotalCost = a.TotalCost + b.TotalCost
	return out
}

// buildInventoryAllocation computes node and pod allocations for [start,end] and
// renders them in OpenCost's payload shape.
func buildInventoryAllocation(ctx *security.RequestContext, accountId string, startSec, endSec int64, step string) (json.RawMessage, json.RawMessage, error) {
	start := time.Unix(startSec, 0).UTC()
	end := time.Unix(endSec, 0).UTC()
	intervals := splitAllocationIntervals(start, end, parseAllocationStep(step))
	if len(intervals) == 0 {
		return json.RawMessage("[]"), json.RawMessage("[]"), nil
	}

	nodes, err := loadInventoryNodes(ctx, accountId)
	if err != nil {
		return nil, nil, fmt.Errorf("load nodes: %w", err)
	}
	pods, err := loadInventoryPods(ctx, accountId, start, end)
	if err != nil {
		return nil, nil, fmt.Errorf("load pods: %w", err)
	}

	nodesByName := make(map[string]inventoryNode, len(nodes))
	pricing := make(map[string]nodePricing, len(nodes))
	for _, n := range nodes {
		nodesByName[n.Name] = n
		pricing[n.Name] = splitNodePrice(n.CPUCores, n.RAMMiB/mibPerGiB, n.HourlyCost)
	}

	nodePayload := make([]map[string]allocationEntry, 0, len(intervals))
	podPayload := make([]map[string]allocationEntry, 0, len(intervals))

	for _, iv := range intervals {
		window := allocationWindow{
			Start: iv.Start.Format("2006-01-02T15:04:05-07:00"),
			End:   iv.End.Format("2006-01-02T15:04:05-07:00"),
		}

		nodeEntries := map[string]allocationEntry{}
		for _, n := range nodes {
			aStart, aEnd := activeRange(n.Created, n.LastSeen, n.Active, iv.End)
			hours := overlapHours(iv.Start, iv.End, aStart, aEnd)
			if hours <= 0 {
				continue
			}
			p := pricing[n.Name]
			ramGiB := n.RAMMiB / mibPerGiB
			entry := allocationEntry{
				Name:         n.Name,
				Window:       window,
				CPUCores:     n.CPUCores,
				CPUCoreHours: n.CPUCores * hours,
				CPUCost:      p.CPUPerCoreHr * n.CPUCores * hours,
				RAMBytes:     ramGiB * bytesPerGiB,
				RAMByteHours: ramGiB * bytesPerGiB * hours,
				RAMCost:      p.RAMPerGiBHr * ramGiB * hours,
			}
			entry.TotalCost = entry.CPUCost + entry.RAMCost
			nodeEntries[n.Name] = entry
		}
		nodePayload = append(nodePayload, nodeEntries)

		podEntries := map[string]allocationEntry{}
		for _, pod := range pods {
			node, ok := nodesByName[pod.NodeName]
			if !ok {
				// A pod whose node is gone from inventory cannot be priced. Its cost
				// stays on the node row, which still carries the full node price.
				continue
			}
			aStart, aEnd := activeRange(pod.Created, pod.LastSeen, pod.Active, iv.End)
			hours := overlapHours(iv.Start, iv.End, aStart, aEnd)
			if hours <= 0 {
				continue
			}
			p := pricing[node.Name]
			ramGiB := pod.RAMBytes / bytesPerGiB
			entry := allocationEntry{
				Window:     window,
				Properties: map[string]string{"namespace": pod.Namespace, "pod": pod.Name, "node": node.Name},
				CPUCores:   pod.CPUCores,
				// Requests are all this path knows; usage and efficiency stay zero,
				// and process_spend skips non-positive values rather than storing them.
				CPUCoreRequestAverage: pod.CPUCores,
				CPUCoreHours:          pod.CPUCores * hours,
				CPUCost:               p.CPUPerCoreHr * pod.CPUCores * hours,
				RAMBytes:              pod.RAMBytes,
				RAMByteRequestAverage: pod.RAMBytes,
				RAMByteHours:          pod.RAMBytes * hours,
				RAMCost:               p.RAMPerGiBHr * ramGiB * hours,
			}
			entry.TotalCost = entry.CPUCost + entry.RAMCost

			// One name can appear more than once in a window: a StatefulSet pod keeps
			// its name across a restart, so the window holds the old instance and the
			// new one. Overwriting charged only the last, silently dropping the hours
			// the earlier instance ran. Accumulate so the pod is billed for all of it.
			key := pod.Namespace + "/" + pod.Name
			if prev, seen := podEntries[key]; seen {
				entry = mergeAllocationEntries(prev, entry)
			}
			podEntries[key] = entry
		}
		podPayload = append(podPayload, podEntries)
	}

	// Pod-level attribution is only as good as the resource requests recorded in
	// k8s_pods.meta, and that coverage varies wildly per account — measured across
	// dev on 2026-08-27, 7 of 20 accounts had NO pod with a CPU request stored, so
	// their node cost is real while their per-pod showback is empty. Report the ratio
	// so that shows up as a number rather than as a confusing UI.
	var nodeTotal, podTotal float64
	for _, iv := range nodePayload {
		for _, e := range iv {
			nodeTotal += e.TotalCost
		}
	}
	podsWithRequests := 0
	for _, iv := range podPayload {
		for _, e := range iv {
			podTotal += e.TotalCost
			if e.CPUCores > 0 || e.RAMBytes > 0 {
				podsWithRequests++
			}
		}
	}
	attributed := 0.0
	if nodeTotal > 0 {
		attributed = 100 * podTotal / nodeTotal
	}
	slog.Info("inventory allocation computed",
		"account_id", accountId, "intervals", len(intervals),
		"node_cost", nodeTotal, "pod_cost", podTotal,
		"attributed_pct", math.Round(attributed*10)/10,
		"pod_entries_with_requests", podsWithRequests)

	nodeJSON, err := json.Marshal(nodePayload)
	if err != nil {
		return nil, nil, err
	}
	podJSON, err := json.Marshal(podPayload)
	if err != nil {
		return nil, nil, err
	}
	return nodeJSON, podJSON, nil
}
