package adapter

import (
	"fmt"
	"math"
	"nudgebee/services/internal/database"
)

// Schedulability guard for a rightsizing apply.
//
// validateContainerResources catches a value that is impossible for any cluster
// (a unit bug). This catches a value that is impossible for *this* cluster: a
// memory request larger than anything the scheduler could place. Applying one
// does not fail loudly — the workload is patched, the new pod goes Pending, and
// the old one is gone, so the service stays down until someone reads the
// scheduler's message. Measured on dev: the stored pod_right_sizing row for the
// prometheus container recommends 10Gi on a four-node cluster whose largest free
// block is 5.35Gi, so the fix we offer as the answer to an OOM kill is one the
// cluster cannot accept.
//
// The test is deliberately "fits nowhere", not "does not fit on its current
// node". A resize replaces the pod, and the replacement goes through the
// scheduler against every node — so a value that does not fit where the pod
// happens to be running now may still schedule fine somewhere else, and
// blocking that would be wrong. Only a value that fits on no node at all is
// certainly unschedulable, whatever the pod's affinity, taints or volumes say.
// That makes this a lower bound: it never refuses a value the cluster could
// have taken, and it does not claim to catch every value the cluster will
// reject.

// nodeMemory is one node's memory budget, in bytes.
type nodeMemory struct {
	name        string
	allocatable float64
	committed   float64
}

// free is what the scheduler has left to give on this node.
func (n nodeMemory) free() float64 {
	if n.allocatable <= n.committed {
		return 0
	}
	return n.allocatable - n.committed
}

// containerRequest is one container's memory request as the inventory sees it.
type containerRequest struct {
	node      string
	namespace string
	workload  string
	container string
	bytes     float64
}

// loadNodeMemory reads every active node's allocatable memory for the account.
// k8s_nodes.memory_allocatable is stored in MiB.
func loadNodeMemory(accountId string) (map[string]*nodeMemory, error) {
	dbms, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		return nil, err
	}
	var rows []nodeRow
	if err := dbms.Db.Select(&rows, `
		SELECT name, coalesce(memory_allocatable, 0) AS memory_allocatable
		FROM k8s_nodes
		WHERE cloud_account_id = $1 AND is_active AND name <> ''`, accountId); err != nil {
		return nil, err
	}
	return nodeMemoryFromRows(rows), nil
}

// nodeRow is one k8s_nodes row as loadNodeMemory reads it.
type nodeRow struct {
	Name              string `db:"name"`
	MemoryAllocatable int64  `db:"memory_allocatable"`
}

// nodeMemoryFromRows turns the node rows into the map the check works on, or
// into nothing at all when any node's allocatable is unknown. Leaving just that
// node out would make the cluster look smaller than it is — it may be the one
// node large enough — and could produce a refusal on a cluster that had room.
// So a partial inventory is returned as no inventory, which the caller reads as
// unknown rather than as a verdict.
func nodeMemoryFromRows(rows []nodeRow) map[string]*nodeMemory {
	nodes := make(map[string]*nodeMemory, len(rows))
	for _, r := range rows {
		if r.MemoryAllocatable <= 0 {
			return nil
		}
		nodes[r.Name] = &nodeMemory{name: r.Name, allocatable: float64(r.MemoryAllocatable) * MiB}
	}
	return nodes
}

// loadContainerRequests reads the per-container memory requests of every active
// pod in the account, with the node each one sits on. This is the same figure
// the scheduler adds up, and it reconciles with the independently-collected
// noisy_neighbours evidence to the byte on dev.
//
// It reads the whole account because what we need is every node's total, not
// one pod's. That is a cluster-sized scan, which is affordable here only
// because an apply is a rare, interactive action — do not move this onto a
// path that runs per event or per page load without caching it first.
func loadContainerRequests(accountId string) ([]containerRequest, error) {
	dbms, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		return nil, err
	}
	var rows []struct {
		Node      string  `db:"node_name"`
		Namespace string  `db:"namespace"`
		Workload  *string `db:"workload_name"`
		Container *string `db:"container"`
		Memory    *string `db:"memory"`
	}
	if err := dbms.Db.Select(&rows, `
		SELECT p.node_name,
		       p.namespace,
		       p.workload_name,
		       cs->>'name'                                  AS container,
		       cs->'resources'->'requests'->>'memory'       AS memory
		FROM k8s_pods p,
		     -- The lateral expands before WHERE runs, so a row whose
		     -- containerStatuses is not an array would abort the whole query and
		     -- take the guard offline for the account. The agent writes this
		     -- field, and on-prem installs run versions we do not control, so the
		     -- shape is checked rather than assumed.
		     jsonb_array_elements(
		         CASE
		             WHEN jsonb_typeof(p.meta->'status_info'->'containerStatuses') = 'array'
		             THEN p.meta->'status_info'->'containerStatuses'
		             ELSE '[]'::jsonb
		         END
		     ) cs
		WHERE p.cloud_account_id = $1 AND p.is_active AND p.node_name <> ''`, accountId); err != nil {
		return nil, err
	}
	out := make([]containerRequest, 0, len(rows))
	for _, r := range rows {
		// A container with no memory request reserves nothing, so it belongs in
		// the list at zero rather than being dropped — the workload's own total
		// has to count it to stay comparable with the new one.
		var bytes float64
		if r.Memory != nil {
			if b, ok := parseMemoryQuantityToBytes(*r.Memory); ok {
				bytes = b
			}
		}
		cr := containerRequest{node: r.Node, namespace: r.Namespace, bytes: bytes}
		if r.Workload != nil {
			cr.workload = *r.Workload
		}
		if r.Container != nil {
			cr.container = *r.Container
		}
		out = append(out, cr)
	}
	return out, nil
}

// requestedMemoryByContainer pulls the new memory request per container out of
// the agent_task payload. Values are the normalized quantities the agent will
// patch, so they parse with the same parser the rest of this file uses.
func requestedMemoryByContainer(containers []any) map[string]float64 {
	out := map[string]float64{}
	for _, c := range containers {
		cm, ok := c.(map[string]any)
		if !ok {
			continue
		}
		name := asString(cm["container_name"])
		if name == "" {
			continue
		}
		raw := asString(cm["memory_request"])
		if raw == "" {
			continue
		}
		if b, ok := parseMemoryQuantityToBytes(raw); ok {
			out[name] = b
		}
	}
	return out
}

// humanBytes renders a byte count the way the rest of the apply path writes
// quantities, so the refusal message and the payload agree.
func humanBytes(b float64) string {
	return fmt.Sprintf("%v", applyMemoryUnit(b))
}

// wholeMi rounds a byte count down to whole Mi. A refusal quotes the most that
// would fit in this unit — the one both forms take — so the figure itself is a
// value that passes. humanBytes rounds to the nearest tenth of a Gi and can read
// slightly high, which is fine for describing a node and wrong for a ceiling.
func wholeMi(b float64) int64 {
	return int64(math.Floor(b / MiB))
}

// validateSchedulableMemory refuses an apply whose memory request fits on no
// node in the cluster.
//
// It fails open on purpose. Missing nodes, missing pod inventory or a database
// error all mean "unknown", and unknown must not block a change the user asked
// for — a guard that refuses when its own data is stale is worse than no guard.
// Only a complete picture that leaves no room produces an error.
func validateSchedulableMemory(ctx AccountAdapterContext, accountId, namespace, workload string, containers []any) error {
	if accountId == "" {
		return nil
	}
	newRequests := requestedMemoryByContainer(containers)
	if len(newRequests) == 0 {
		return nil
	}

	nodes, err := loadNodeMemory(accountId)
	if err != nil || len(nodes) == 0 {
		ctx.GetLogger().Info("schedulable: no usable node inventory, skipping headroom check",
			"account", accountId, "error", err)
		return nil
	}
	requests, err := loadContainerRequests(accountId)
	if err != nil {
		ctx.GetLogger().Info("schedulable: no usable pod inventory, skipping headroom check",
			"account", accountId, "error", err)
		return nil
	}

	err, known := checkMemoryHeadroom(nodes, requests, namespace, workload, newRequests)
	if !known {
		ctx.GetLogger().Info("schedulable: workload not found in pod inventory, skipping headroom check",
			"account", accountId, "namespace", namespace, "workload", workload)
		return nil
	}
	return err
}

// checkMemoryHeadroom is the decision itself, with no I/O: given the cluster's
// nodes, every pod's container requests, and the new requests for one workload,
// say whether the new pod could be scheduled anywhere. known=false means the
// inputs do not support a verdict and the caller must not refuse.
func checkMemoryHeadroom(
	nodes map[string]*nodeMemory,
	requests []containerRequest,
	namespace, workload string,
	newRequests map[string]float64,
) (err error, known bool) {
	// What the cluster has already promised, per node, and what this workload's
	// own pod currently holds on the node it runs on. The pod's existing
	// reservation is released when it is replaced, so on its own node that
	// amount is available to the new spec and must not be counted twice.
	ownByNode := map[string]float64{}
	ownCurrent := map[string]float64{}
	for _, r := range requests {
		n, ok := nodes[r.node]
		if !ok {
			continue
		}
		n.committed += r.bytes
		if r.namespace == namespace && r.workload == workload {
			ownByNode[r.node] += r.bytes
			// Replicas of one workload carry the same spec, so any replica is
			// representative — except mid-rollout, when old and new pods differ.
			// Take the smallest, because it under-states the new pod's total and
			// so errs toward letting an apply through. Taking the largest would
			// let a half-finished rollout produce a refusal, and a guard that
			// blocks a change the cluster could have taken is the one failure
			// this design does not accept.
			if cur, seen := ownCurrent[r.container]; !seen || r.bytes < cur {
				ownCurrent[r.container] = r.bytes
			}
		}
	}
	if len(ownCurrent) == 0 {
		// The workload has no placed pod in the inventory — it may never have
		// run, its pods may be Pending, or collection may be behind. Without its
		// current shape we cannot total the new pod, and we cannot tell which of
		// a node's reservations are its own, so free memory is not a yardstick
		// we can trust here. A node's size still is.
		return checkLargestNode(nodes, workload, newRequests)
	}

	// The new pod's total request: every container it has today, with the ones
	// being changed swapped for their new values. A pod is scheduled as a whole,
	// so the total is what has to fit — not the one container being edited.
	newTotal := 0.0
	for name, cur := range ownCurrent {
		if next, changed := newRequests[name]; changed {
			newTotal += next
			continue
		}
		newTotal += cur
	}
	// A container the payload adds that the running pod does not have yet.
	for name, next := range newRequests {
		if _, exists := ownCurrent[name]; !exists {
			newTotal += next
		}
	}
	if newTotal <= 0 {
		return nil, true
	}

	// Does any node have room, counting back this workload's own reservation on
	// the node that already hosts it?
	best := ""
	bestFree := -1.0
	for _, n := range nodes {
		free := n.free() + ownByNode[n.name]
		if free >= newTotal {
			return nil, true
		}
		if free > bestFree {
			bestFree, best = free, n.name
		}
	}

	target := resizeTarget(workload, newRequests)
	bestNode := nodes[best]
	if bestNode == nil {
		// No node survived the inventory filter, so there is nothing to compare
		// against and nothing to report. Callers already skip on an empty node
		// set; this keeps the function total rather than relying on that.
		return nil, false
	}
	// What the containers being changed may ask for: the roomiest node's free
	// block less what the pod's unchanged containers take out of it.
	changed := 0.0
	for _, next := range newRequests {
		changed += next
	}
	advice := "Add capacity"
	if fits := wholeMi(bestFree - (newTotal - changed)); fits >= 1 {
		advice = fmt.Sprintf("Lower the request to %dMi or less, or add capacity", fits)
		if len(newRequests) > 1 {
			advice = fmt.Sprintf("Lower the requests to %dMi or less in total, or add capacity", fits)
		}
	}
	return fmt.Errorf(
		"%s needs %s of memory but no node in this cluster has room for it — the largest free block is %s on %s (%s allocatable, %s already reserved by other pods). %s",
		target,
		humanBytes(newTotal),
		humanBytes(bestFree),
		best,
		humanBytes(bestNode.allocatable),
		humanBytes(bestNode.committed-ownByNode[best]),
		advice,
	), true
}

// resizeTarget is what a refusal names: the container when only one is being
// changed, otherwise the workload.
func resizeTarget(workload string, newRequests map[string]float64) string {
	if len(newRequests) == 1 {
		for name := range newRequests {
			return name
		}
	}
	return workload
}

// checkLargestNode is the verdict left when the workload's own pods are not in
// the inventory. The containers being changed are part of the new pod whatever
// else it holds, so if their requests alone exceed the allocatable memory of
// every node, none of the nodes the cluster has can take it — and that holds
// however stale or incomplete the pod inventory is. Anything smaller stays
// unknown: it might fit once the workload's own reservation is handed back, and
// we cannot see that reservation.
func checkLargestNode(nodes map[string]*nodeMemory, workload string, newRequests map[string]float64) (err error, known bool) {
	requested := 0.0
	for _, next := range newRequests {
		requested += next
	}
	var largest *nodeMemory
	for _, n := range nodes {
		// Name breaks a tie so the refusal names the same node every time.
		if largest == nil || n.allocatable > largest.allocatable ||
			(n.allocatable == largest.allocatable && n.name < largest.name) {
			largest = n
		}
	}
	if largest == nil || requested <= largest.allocatable {
		return nil, false
	}
	// Stated as a requirement, not as a value that will schedule: below this we
	// have only ruled out that no node is large enough, not that one has the
	// room free.
	advice := fmt.Sprintf("The request has to be %dMi or less to fit a node here; lower it, or add a larger node", wholeMi(largest.allocatable))
	if len(newRequests) > 1 {
		advice = fmt.Sprintf("Together the requests have to be %dMi or less to fit a node here; lower them, or add a larger node", wholeMi(largest.allocatable))
	}
	return fmt.Errorf(
		"%s needs %s of memory but no node in this cluster is that large — the largest is %s, with %s allocatable. %s",
		resizeTarget(workload, newRequests),
		humanBytes(requested),
		largest.name,
		humanBytes(largest.allocatable),
		advice,
	), true
}
