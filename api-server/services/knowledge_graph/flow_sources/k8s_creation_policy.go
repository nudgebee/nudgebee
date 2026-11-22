package flow_sources

import (
	"context"
	"log/slog"
	"time"

	"nudgebee/services/internal/database"
	"nudgebee/services/knowledge_graph/core"
)

// k8sCreationContext is the per-account state the node-creation paths need
// while resolving one account's observations. Passed explicitly rather than
// held on the flow source so that nothing leaks between accounts or between
// builds.
type k8sCreationContext struct {
	// inventoryAuthoritative is true when this account has K8s inventory, so
	// k8s_source is the oracle for which workloads exist and a flow source
	// must not invent one (#41619).
	//
	// It is false only when the account has no active k8s_workloads row at
	// all: an eBPF-only install, where nothing but observed traffic can
	// describe the cluster. Gating a flow source there would leave the
	// account with no workload nodes whatsoever — on the test environment
	// `iteration-test-onboarding-e2e` is exactly this shape: 0 inventory
	// rows, 0 k8s-sourced nodes, 761 flow-sourced ones.
	inventoryAuthoritative bool

	// skippedKinds counts observations refused for an unmaterialisable kind,
	// reported once per account instead of once per observation.
	skippedKinds map[string]int

	// inventoryMisses dedupes the review rows written for refused workloads.
	inventoryMisses map[string]bool
}

// strictCreationContext is the conservative context: the inventory is treated
// as authoritative, so nothing workload-shaped is created. Used to normalise a
// nil context at the top of every creation path — a nil map write or a nil
// deref there would panic the whole build, and defaulting to "create nothing"
// is the safe direction.
func strictCreationContext() *k8sCreationContext {
	return &k8sCreationContext{
		inventoryAuthoritative: true,
		skippedKinds:           make(map[string]int),
		inventoryMisses:        make(map[string]bool),
	}
}

// newK8sCreationContext resolves the per-account creation policy.
//
// The authority signal is read from the inventory TABLE, deliberately not from
// how many k8s-sourced nodes this build produced. A k8s source that failed or
// was excluded from this build produces no nodes either, and treating that as
// "eBPF-only" would let a transient collector outage mass-create orphan
// workloads across a cluster that has a perfectly good inventory. Rows in
// k8s_workloads survive such an outage, so they answer the question that is
// actually being asked: has this account ever been described by a collector?
//
// On any error the account is treated as authoritative — the conservative
// direction, since the cost of wrongly gating is one build's edges while the
// cost of wrongly creating is a node no sweep can retire.
func newK8sCreationContext(logger *slog.Logger, accountID string) *k8sCreationContext {
	cc := &k8sCreationContext{
		inventoryAuthoritative: true,
		skippedKinds:           make(map[string]int),
		inventoryMisses:        make(map[string]bool),
	}
	if accountID == "" {
		return cc
	}
	dbManager, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		logger.Warn("k8s creation policy: no db manager, treating inventory as authoritative",
			"cloud_account_id", accountID, "error", err)
		return cc
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var hasInventory bool
	err = dbManager.Db.QueryRowContext(ctx,
		`SELECT EXISTS (SELECT 1 FROM k8s_workloads WHERE cloud_account_id = $1 AND is_active)`,
		accountID).Scan(&hasInventory)
	if err != nil {
		logger.Warn("k8s creation policy: inventory probe failed, treating inventory as authoritative",
			"cloud_account_id", accountID, "error", err)
		return cc
	}
	cc.inventoryAuthoritative = hasInventory
	if !hasInventory {
		logger.Info("k8s creation policy: account has no K8s inventory, flow sources may create workload nodes",
			"cloud_account_id", accountID)
	}
	return cc
}

// inventoryOwnedWorkloadTypes are the node types k8s_source actually EMITS
// from k8s_workloads. A flow source may create one only when that table has
// nothing for the account (see k8sCreationContext.inventoryAuthoritative).
//
// Workload is the only member, and the distinction is not cosmetic. NodeTypeJob
// and NodeTypeCronJob look like they belong here — k8s_workloads has rows for
// both kinds — but sources/k8s/workload.go registers Job and CronJob with
// `NodeType: core.NodeTypeWorkload`, so a Job inventory row becomes a Workload
// node and nothing static ever emits NodeTypeJob or NodeTypeCronJob. The test
// database confirms it: of 33,429 Workload nodes 33,429 are k8s-sourced across
// 145 accounts, while all 77 Job and all 10 CronJob nodes are flow-sourced and
// not one is k8s-sourced. Gating them would therefore have deleted every Job
// and CronJob in every graph rather than just the stale ones. They are declared
// in flowProducedInfraTypes instead.
//
// Distinct from isK8sAuthoritativeType, which is unconditional: Node /
// Namespace / Cluster are never created by a flow source whatever the
// inventory looks like, because the observations that reach that path are
// mislabelled pod names rather than a cluster the collector missed.
var inventoryOwnedWorkloadTypes = map[core.NodeType]bool{
	core.NodeTypeWorkload: true,
}

// flowMayCreate reports whether a flow source may materialise nodeType for an
// account with this inventory status.
func flowMayCreate(t core.NodeType, inventoryAuthoritative bool) bool {
	if isK8sAuthoritativeType(t) {
		return false
	}
	if inventoryOwnedWorkloadTypes[t] {
		return !inventoryAuthoritative
	}
	return true
}

// flowAuthoritativeProperty marks a node a flow source created because nothing
// else could describe it. The sweep refuses to retire these on sync version.
//
// Why they must be exempt: the eBPF service map only reports a workload that
// carried traffic in the window, so a workload that is merely quiet disappears
// from it exactly like one that was deleted. With no inventory there is no
// oracle to tell those apart, and #41003 measured the consequence on dev — 78
// of 87 legitimate `kind=Service` nodes were stale beyond 7 days. Sweeping
// these would delete most of a real graph and make the rest flap in and out
// every hour. A deleted workload therefore lingers in an inventory-less
// account; that is the honest outcome, not an oversight.
const flowAuthoritativeProperty = core.FlowAuthoritativeProperty
