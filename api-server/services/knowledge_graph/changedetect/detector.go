package changedetect

import (
	"context"
	"log/slog"
	"sort"
	"time"

	"github.com/jmoiron/sqlx"

	"nudgebee/services/config"
	"nudgebee/services/internal/database"
	"nudgebee/services/knowledge_graph/core"
	kgqueue "nudgebee/services/knowledge_graph/queue"
	"nudgebee/services/knowledge_graph/sources"
	"nudgebee/services/security"
)

// MessageSource is the Source stamped on messages the detector publishes.
const MessageSource = "inventory-change"

// rolloutSettle is how old a rollout must be before its workload is re-read: the
// deploy time is written from the change event, and the collector's discovery
// pass writes the new spec some time after.
const rolloutSettle = 5 * time.Minute

// runTimeout bounds one run so a slow database cannot stack runs up behind the
// advisory lock.
const runTimeout = 8 * time.Minute

// maxRefsPerAccount mirrors the applier's limit (resourcepatch): past it an
// account rebuild is cheaper than patching resource by resource, so the
// detector asks for the rebuild directly.
const maxRefsPerAccount = 50

// deleteCandidates names the graph nodes whose inventory row is gone.
// core.ResourcePatchRepository implements it.
type deleteCandidates interface {
	CloudDeleteCandidates(tenantID, accountID string, nodeSources []string, limit int) ([]string, error)
	K8sWorkloadDeleteCandidates(tenantID, accountID string, specificTypes []string, limit int) ([]string, error)
	K8sNodeDeleteCandidates(tenantID, accountID, specificType string, limit int) ([]string, error)
}

// Detector runs one detection pass over every tenant with an enabled
// knowledge-graph filter. It holds no resource knowledge of its own: what a new
// or removed resource means comes from the cloud and Kubernetes patch policies.
type Detector struct {
	dbManager  *database.DatabaseManager
	logger     *slog.Logger
	publish    func(ctx context.Context, msg kgqueue.KGUpdateMessage) error
	candidates deleteCandidates
	clouds     map[string]sources.CloudPatchPolicy
	k8s        sources.K8sPatchPolicy
	// createsEnabled turns creatable new resources into refs; when false every
	// new graph resource rebuilds its account (kg_inventory_change_creates_enabled).
	createsEnabled bool
}

// NewDetector builds a detector backed by the metastore.
func NewDetector(dbManager *database.DatabaseManager, logger *slog.Logger) (*Detector, error) {
	if logger == nil {
		logger = slog.Default()
	}
	k8sPolicy, err := sources.NewK8sPatchPolicy(logger)
	if err != nil {
		return nil, err
	}
	return &Detector{
		dbManager:      dbManager,
		logger:         logger,
		publish:        kgqueue.PublishKGUpdateMessage,
		candidates:     core.NewResourcePatchRepository(dbManager, logger),
		clouds:         sources.NewCloudPatchPolicies(logger),
		k8s:            k8sPolicy,
		createsEnabled: config.Config.KGInventoryChangeCreatesEnabled,
	}, nil
}

// runStats is the one summary line a run logs.
type runStats struct {
	tenants, accounts, firstSightings, rebuilds, refs, deletes, creates, published, publishFailures, capped, errors int
}

// Run performs one pass. Errors are logged, never returned: the next run retries
// whatever this one could not finish.
func (d *Detector) Run(reqCtx *security.RequestContext) {
	ctx, cancel := context.WithTimeout(reqCtx.GetContext(), runTimeout)
	defer cancel()
	reqCtx = security.NewRequestContext(ctx, reqCtx.GetSecurityContext(), reqCtx.GetLogger(), nil, nil)
	start := time.Now()

	scopes, err := d.tenantScopes(reqCtx)
	if err != nil {
		d.logger.Error("kg_detector: failed to list tenants", "error", err)
		return
	}
	tenantIDs := sortedKeys(scopes)

	// Each tenant is probed in a short transaction of its own, under that
	// tenant's detector lock. One transaction for the whole pass sat idle in
	// transaction across every tenant's probes and publishes, holding read locks
	// on the inventory tables that a migration then queued behind (#41195).
	var probedAt map[string]time.Time
	ready := false
	_, err = d.dbManager.DoInTransaction(func(tx *sqlx.Tx) (any, error) {
		if exists, err := marksTableExists(ctx, tx); err != nil || !exists {
			return nil, err
		}
		ready = true
		// Visit the tenants whose accounts were probed longest ago first. A run that
		// hits its time budget leaves the rest with older marks, so the next run
		// starts with them: every tenant is reached in turn, however many there are.
		probed, err := (&repository{ctx: ctx, tx: tx}).tenantsProbedAt(tenantIDs)
		if err != nil {
			d.logger.Warn("kg_detector: failed to load probe order, using tenant id order", "error", err)
			return nil, nil
		}
		probedAt = probed
		return nil, nil
	})
	if err != nil || !ready {
		d.logger.Warn("kg_detector: inventory marks table not available yet, skipping run", "error", err)
		return
	}

	kgService := core.NewService(reqCtx, d.logger, d.dbManager)
	stats := runStats{}
	for _, tenantID := range leastRecentlyProbedFirst(tenantIDs, probedAt) {
		if ctx.Err() != nil {
			d.logger.Warn("kg_detector: run timed out, remaining tenants wait for the next run")
			break
		}
		stats.tenants++
		if _, err := d.dbManager.DoInTransaction(func(tx *sqlx.Tx) (any, error) {
			locked, err := tryLock(ctx, tx, tenantID)
			if err != nil {
				return nil, err
			}
			if !locked {
				d.logger.Info("kg_detector: skipping tenant", "tenant_id", tenantID, "reason", errNoLock)
				return nil, nil
			}
			d.runTenant(ctx, reqCtx, &repository{ctx: ctx, tx: tx}, kgService, tenantID, scopes[tenantID], &stats)
			return nil, nil
		}); err != nil {
			d.logger.Error("kg_detector: failed to probe tenant", "tenant_id", tenantID, "error", err)
			stats.errors++
		}
	}

	d.logger.Info("kg_detector: run complete",
		"tenants", stats.tenants,
		"accounts", stats.accounts,
		"first_sightings", stats.firstSightings,
		"account_rebuilds", stats.rebuilds,
		"resource_refs", stats.refs,
		"delete_refs", stats.deletes,
		"create_refs", stats.creates,
		"messages", stats.published,
		"publish_failures", stats.publishFailures,
		"capped", stats.capped,
		"errors", stats.errors,
		"duration", time.Since(start).String())
}

// tenantScopes maps each tenant with an enabled filter to the account ids its
// filters name; a nil list means every active account.
func (d *Detector) tenantScopes(reqCtx *security.RequestContext) (map[string][]string, error) {
	filters, err := core.NewFilterRepository(d.dbManager, d.logger).GetAllEnabledFilters(reqCtx)
	if err != nil {
		return nil, err
	}
	scopes := make(map[string][]string)
	unbounded := make(map[string]bool)
	for _, f := range filters {
		tenantID := f.TenantID.String()
		if len(f.AccountIDs) == 0 {
			unbounded[tenantID] = true
		}
		scopes[tenantID] = append(scopes[tenantID], f.AccountIDs...)
	}
	for tenantID := range unbounded {
		scopes[tenantID] = nil
	}
	return scopes, nil
}

// accountResult is the outcome of probing one account.
type accountResult struct {
	marks   *Marks
	rebuild bool
	refs    []kgqueue.KGResourceRef
	changed bool // marks moved; saved only once the message went out
}

func (d *Detector) runTenant(ctx context.Context, reqCtx *security.RequestContext, repo *repository, kgService *core.Service, tenantID string, accountIDs []string, stats *runStats) {
	accounts, err := kgService.ListActiveAccounts(reqCtx, tenantID, accountIDs)
	if err != nil {
		d.logger.Warn("kg_detector: failed to list accounts", "tenant_id", tenantID, "error", err)
		stats.errors++
		return
	}
	ids := make([]string, 0, len(accounts))
	for _, a := range accounts {
		ids = append(ids, a.ID)
	}
	saved, err := repo.loadMarks(ids)
	if err != nil {
		d.logger.Warn("kg_detector: failed to load marks", "tenant_id", tenantID, "error", err)
		stats.errors++
		return
	}

	// Same order inside a tenant, so one large tenant cannot starve its own
	// later accounts either.
	accountProbedAt := make(map[string]time.Time, len(saved))
	for id, m := range saved {
		accountProbedAt[id] = m.ProbedAt
	}
	byID := make(map[string]core.AccountRef, len(accounts))
	for _, a := range accounts {
		byID[a.ID] = a
	}
	ordered := leastRecentlyProbedFirst(ids, accountProbedAt)
	accounts = accounts[:0]
	for _, id := range ordered {
		accounts = append(accounts, byID[id])
	}

	results := make([]*accountResult, 0, len(accounts))
	for _, a := range accounts {
		if ctx.Err() != nil {
			break
		}
		res, err := d.probeAccount(repo, tenantID, a, saved[a.ID], stats)
		if err != nil {
			d.logger.Warn("kg_detector: failed to probe account", "tenant_id", tenantID, "account_id", a.ID, "error", err)
			stats.errors++
			continue
		}
		if res != nil {
			stats.accounts++
			results = append(results, res)
		}
	}

	msg := kgqueue.KGUpdateMessage{TenantID: tenantID, Source: MessageSource}
	for _, r := range results {
		if r.rebuild {
			msg.AccountIDs = append(msg.AccountIDs, r.marks.AccountID)
		} else {
			msg.Resources = append(msg.Resources, r.refs...)
		}
	}
	published := true
	if len(msg.AccountIDs) > 0 || len(msg.Resources) > 0 {
		stats.rebuilds += len(msg.AccountIDs)
		stats.refs += len(msg.Resources)
		if err := d.publish(ctx, msg); err != nil {
			d.logger.Error("kg_detector: failed to publish, marks kept for retry", "tenant_id", tenantID, "error", err)
			stats.publishFailures++
			published = false
		} else {
			stats.published++
			d.logger.Info("kg_detector: requested knowledge graph update",
				"tenant_id", tenantID, "account_ids", msg.AccountIDs, "resource_refs", len(msg.Resources))
		}
	}

	toSave := make([]*Marks, 0, len(results))
	for _, r := range results {
		if !r.changed || published {
			toSave = append(toSave, r.marks)
		}
	}
	if err := repo.saveMarks(toSave); err != nil {
		d.logger.Warn("kg_detector: failed to save marks", "tenant_id", tenantID, "error", err)
		stats.errors++
	}
}

// probeAccount returns nil for accounts of providers the graph has no inventory
// policy for.
func (d *Detector) probeAccount(repo *repository, tenantID string, account core.AccountRef, prev *Marks, stats *runStats) (*accountResult, error) {
	next := &Marks{AccountID: account.ID, TenantID: tenantID}
	if prev != nil {
		copied := *prev
		copied.TenantID = tenantID
		next = &copied
	}
	source := sources.SourceForCloudProvider(account.CloudProvider)
	if policy, ok := d.clouds[source]; ok {
		return d.probeCloudAccount(repo, policy, tenantID, account.ID, prev, next, stats)
	}
	if d.k8s != nil && source == d.k8s.Source() {
		return d.probeK8sAccount(repo, tenantID, account.ID, prev, next, stats)
	}
	return nil, nil
}

// candidateCap bounds how many new rows or rolled-out workloads one account may
// produce before the detector stops looking at them one by one.
func candidateCap() int {
	if limit := config.Config.KGInventoryChangeCandidateCap; limit > 0 {
		return limit
	}
	return 5000
}

// finish turns what a probe found into the account's result: a rebuild absorbs
// every ref, and too many refs become a rebuild.
func (r *accountResult) finish(stats *runStats) *accountResult {
	if !r.rebuild && len(r.refs) > maxRefsPerAccount {
		stats.capped++
		r.rebuild = true
	}
	if r.rebuild {
		r.refs = nil
		r.changed = true
	}
	return r
}

// leastRecentlyProbedFirst orders ids so that those never probed come first,
// then those probed longest ago; ties go by id so the order is stable.
func leastRecentlyProbedFirst(ids []string, probedAt map[string]time.Time) []string {
	out := make([]string, len(ids))
	copy(out, ids)
	sort.SliceStable(out, func(i, j int) bool {
		ti, iSeen := probedAt[out[i]]
		tj, jSeen := probedAt[out[j]]
		if iSeen != jSeen {
			return !iSeen
		}
		if !ti.Equal(tj) {
			return ti.Before(tj)
		}
		return out[i] < out[j]
	})
	return out
}

func sortedKeys(m map[string][]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
