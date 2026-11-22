// Package resourcepatch applies a change to one cloud or Kubernetes resource to
// the knowledge graph without rebuilding its account.
//
// It is a second write path next to the account build, so it only writes what it
// can prove the build would write: a rebuilt node must have exactly the stored
// node's id and type, and the resource type must allow the change. What each type
// allows is not decided here. Each resource type declares it next to its builder
// (a sources.PatchSpec), each cloud assembles those into a
// sources.CloudPatchPolicy, and Kubernetes provides a sources.K8sPatchPolicy. This
// package only runs the flow. Anything it cannot prove is reported as
// NeedsRebuild and becomes an account-scoped build request; the hourly
// tenant-wide build remains the reconciliation pass.
//
// What each kind of change does:
//   - update: the node's properties are rebuilt from its inventory rows (stored
//     keys enrichers wrote survive), and the node's own outgoing edges are
//     rewritten when its type declares an edge signature;
//   - delete: when the inventory no longer has the resource, the node and every
//     edge touching it are tombstoned;
//   - create: for a type declared creatable, the node and its own outgoing edges
//     are added. Edges other resources point at it, and edges that need live
//     cloud calls, arrive with the next account build.
package resourcepatch

import (
	"context"
	"fmt"
	"log/slog"
	"sort"

	"nudgebee/services/internal/database"
	"nudgebee/services/knowledge_graph/core"
	"nudgebee/services/knowledge_graph/sources"
)

// Op values carried on a resource ref. The op is a hint: what is written is
// decided by the inventory, so a late or out-of-order event cannot resurrect a
// deleted resource or delete a live one.
const (
	OpUpsert = "upsert"
	OpDelete = "delete"
)

// maxRefsPerAccount bounds the work one message can cause. Past it, one account
// rebuild is cheaper than patching resource by resource.
const maxRefsPerAccount = 50

// Ref names one changed resource.
type Ref struct {
	AccountID string
	// ProviderResourceID identifies the resource: cloud_resourses.resourse_id,
	// its ARN or cloud_resourses.id; a k8s workload's cloud_resource_id; or, for
	// NodeType "Node", the Kubernetes node name.
	ProviderResourceID string
	NodeType           string
	Op                 string
}

// Outcome is what happened to one ref.
type Outcome string

const (
	// Applied means the graph now reflects the inventory for this resource.
	Applied Outcome = "applied"
	// NothingToDo means the graph already agrees with the inventory: the node is
	// already gone, or the resource is not something the graph holds.
	NothingToDo Outcome = "nothing_to_do"
	// NeedsRebuild means the per-resource path cannot prove it is correct here.
	NeedsRebuild Outcome = "needs_rebuild"
)

// Result is the outcome for one ref plus a reason for logs.
type Result struct {
	Outcome Outcome
	Reason  string
}

func rebuild(reason string) Result     { return Result{Outcome: NeedsRebuild, Reason: reason} }
func nothingToDo(reason string) Result { return Result{Outcome: NothingToDo, Reason: reason} }
func applied(format string, args ...interface{}) Result {
	return Result{Outcome: Applied, Reason: fmt.Sprintf(format, args...)}
}

// Applier applies resource refs.
type Applier struct {
	store  store
	logger *slog.Logger
	clouds map[string]sources.CloudPatchPolicy
	k8s    sources.K8sPatchPolicy
}

// NewApplier builds an applier backed by the metastore, with every registered
// cloud and Kubernetes policy. It is not safe for concurrent use; create one per
// message.
func NewApplier(dbManager *database.DatabaseManager, logger *slog.Logger) (*Applier, error) {
	if logger == nil {
		logger = slog.Default()
	}
	k8sPolicy, err := sources.NewK8sPatchPolicy(logger)
	if err != nil {
		return nil, err
	}
	return newApplier(newDBStore(dbManager, logger), sources.NewCloudPatchPolicies(logger), k8sPolicy, logger), nil
}

func newApplier(st store, clouds map[string]sources.CloudPatchPolicy, k8sPolicy sources.K8sPatchPolicy, logger *slog.Logger) *Applier {
	return &Applier{store: st, logger: logger, clouds: clouds, k8s: k8sPolicy}
}

// ApplyAll applies every ref and returns the accounts that need an account
// rebuild instead. Refs without an account id are skipped: the caller knows
// which account a message's refs belong to. Within an account deletes run before
// upserts, so a resource deleted and re-created under the same key ends up active.
func (a *Applier) ApplyAll(ctx context.Context, tenantID string, refs []Ref) []string {
	byAccount, order := a.groupByAccount(tenantID, refs)
	rebuildSet := make(map[string]bool)

	// Every patch runs under the tenant's build lock, so no build can be claimed
	// between the running-build check and the write (see ExcludeBuilds). The lock
	// is taken once per ref, not once around them all: it is a transaction, and
	// held across every ref it sat idle in transaction while the refs were
	// applied, so a migration on the tables it had read queued behind it (#41195).
	for _, accountID := range order {
		accountRefs := byAccount[accountID]
		if len(accountRefs) > maxRefsPerAccount {
			a.logger.Info("resourcepatch: too many refs for one account, rebuilding it instead",
				"tenant_id", tenantID, "account_id", accountID, "refs", len(accountRefs))
			rebuildSet[accountID] = true
			continue
		}

		sort.SliceStable(accountRefs, func(i, j int) bool {
			return accountRefs[i].Op == OpDelete && accountRefs[j].Op != OpDelete
		})
		for _, ref := range accountRefs {
			if ctx.Err() != nil {
				rebuildSet[accountID] = true
				break
			}
			skip := false
			err := a.store.ExcludeBuilds(tenantID, func(runningCovers func(string) (bool, error)) error {
				running, err := runningCovers(accountID)
				if err != nil {
					return err
				}
				if running {
					a.logger.Info("resourcepatch: build running, folding refs into a rebuild",
						"tenant_id", tenantID, "account_id", accountID)
					rebuildSet[accountID] = true
					skip = true
					return nil
				}
				res := a.Apply(tenantID, ref)
				a.logger.Info("resourcepatch: ref processed",
					"tenant_id", tenantID,
					"account_id", accountID,
					"provider_resource_id", ref.ProviderResourceID,
					"node_type", ref.NodeType,
					"op", ref.Op,
					"outcome", res.Outcome,
					"reason", res.Reason)
				if res.Outcome == NeedsRebuild {
					rebuildSet[accountID] = true
				}
				return nil
			})
			if err != nil {
				// The lock could not be taken or released cleanly, or the running-build
				// check failed. Anything already written is idempotent under a rebuild,
				// so rebuild the account.
				a.logger.Warn("resourcepatch: could not hold the tenant build lock or check running builds, rebuilding the account",
					"tenant_id", tenantID, "account_id", accountID, "error", err)
				rebuildSet[accountID] = true
				skip = true
			}
			if skip {
				break
			}
		}
	}

	out := make([]string, 0, len(rebuildSet))
	for _, accountID := range order {
		if rebuildSet[accountID] {
			out = append(out, accountID)
		}
	}
	return out
}

func (a *Applier) groupByAccount(tenantID string, refs []Ref) (map[string][]Ref, []string) {
	byAccount := make(map[string][]Ref)
	order := make([]string, 0)
	for _, ref := range refs {
		if ref.AccountID == "" {
			a.logger.Warn("resourcepatch: ref has no account id, cannot apply",
				"tenant_id", tenantID, "provider_resource_id", ref.ProviderResourceID)
			continue
		}
		if _, seen := byAccount[ref.AccountID]; !seen {
			order = append(order, ref.AccountID)
		}
		byAccount[ref.AccountID] = append(byAccount[ref.AccountID], ref)
	}
	return byAccount, order
}

// Apply applies one ref whose AccountID is set.
func (a *Applier) Apply(tenantID string, ref Ref) Result {
	if ref.AccountID == "" || ref.ProviderResourceID == "" {
		return rebuild("ref is missing account or provider id")
	}
	if ref.NodeType == string(core.NodeTypeNode) {
		return a.applyKubernetesNode(tenantID, ref)
	}

	matches, err := a.store.FindActiveNodesByProviderID(tenantID, ref.AccountID, ref.ProviderResourceID)
	if err != nil {
		return rebuild("node lookup failed: " + err.Error())
	}
	switch {
	case len(matches) > 1:
		return rebuild("provider id matches several nodes")
	case len(matches) == 0 && ref.Op == OpDelete:
		return nothingToDo("no active node for provider id")
	case len(matches) == 0:
		return a.createCloudResource(tenantID, ref)
	}

	stored := matches[0]
	if policy, ok := a.clouds[stored.Source]; ok {
		return a.updateCloudResource(tenantID, ref, policy, stored)
	}
	if a.k8s != nil && stored.Source == a.k8s.Source() {
		return a.updateWorkload(tenantID, ref, stored)
	}
	return rebuild("node from source " + stored.Source + " is not patchable")
}
