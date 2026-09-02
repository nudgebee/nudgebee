package triage

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"nudgebee/services/internal/database"
	"nudgebee/services/security"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
	"github.com/lib/pq"
)

// CriticalityDiscoveryStats summarizes one account's discovery sweep.
type CriticalityDiscoveryStats struct {
	Account   string
	Scanned   int // active workloads examined
	Candidate int // workloads the deterministic pass flagged as topologically important
	Demoted   int // candidates the LLM actively demoted to low (a low row is still written)
	NoOpinion int // candidates the LLM answered medium for — kept their deterministic tier
	Untiered  int // candidates that resolved to medium and so got no row at all
	Tiered    int // rows actually written (critical/high/low) after the LLM precision review
	Failed    int // classified rows whose write failed — counted separately, never as Tiered

	// Skipped marks a sweep that declined to write OR prune because its inputs were not observed.
	// Existing rows are left exactly as they were; this is a hold, not a failure.
	Skipped    bool
	SkipReason string

	Elapsed time.Duration
}

// DiscoverWorkloadCriticalityAllAccounts is the cron entrypoint: it sweeps every k8s account that has
// active workloads and refreshes their derived criticality. It is meant to run right AFTER the nightly
// knowledge-graph build (its inputs — fan-in, ingress-backing — only change when the graph rebuilds),
// and on account first-connect for a day-one baseline. Idempotent and safe to re-run; user overrides
// are never overwritten (see upsertDerivedCriticality).
func DiscoverWorkloadCriticalityAllAccounts(ctx *security.RequestContext) error {
	dbms, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		return err
	}
	db := dbms.Db
	c := ctx.GetContext()

	var accounts []struct {
		Account string `db:"account"`
		Tenant  string `db:"tenant"`
	}
	if err := db.SelectContext(c, &accounts, `
		SELECT DISTINCT w.cloud_account_id::text AS account, w.tenant_id::text AS tenant
		FROM k8s_workloads w
		JOIN cloud_accounts ca ON ca.id = w.cloud_account_id AND ca.status != 'disabled'
		WHERE w.is_active`); err != nil {
		return err
	}

	for _, a := range accounts {
		st, err := DiscoverWorkloadCriticality(c, db, a.Account, a.Tenant)
		if err != nil {
			ctx.GetLogger().Error("criticality discovery: account sweep failed",
				"account", a.Account, "error", err)
			continue
		}
		if st.Skipped {
			ctx.GetLogger().Warn("criticality discovery: account sweep skipped, existing rows kept",
				"account", st.Account, "reason", st.SkipReason, "scanned", st.Scanned,
				"candidates", st.Candidate, "elapsed_ms", st.Elapsed.Milliseconds())
			continue
		}
		ctx.GetLogger().Info("criticality discovery: account swept",
			"account", st.Account, "scanned", st.Scanned, "candidates", st.Candidate,
			"demoted", st.Demoted, "no_opinion", st.NoOpinion, "untiered", st.Untiered,
			"tiered", st.Tiered, "elapsed_ms", st.Elapsed.Milliseconds())
	}
	return nil
}

// accountGraphFact holds the per-workload topology facts computed in one batched query.
type accountGraphFact struct {
	image          string
	customerFacing bool
	fanIn          int
}

// candidateRow is a workload the deterministic pass flagged as topologically important, carried into
// the LLM precision review along with its deterministic verdict (used as the fallback if the LLM is
// unavailable or omits it).
type candidateRow struct {
	crid, namespace  string
	detLevel, detRat string
	detConf          float64
	signals          map[string]interface{}
}

// DiscoverWorkloadCriticality sweeps every active workload in one account and assigns criticality in
// two stages: (1) a cheap deterministic pass over topology/labels finds the RECALL set — every
// workload that looks important (ingress/LB-backed or high fan-in); (2) an LLM precision review of
// only those candidates applies semantic judgement the topology can't — DEMOTING the false positives
// it recognizes (demo/test/e2e/benchmark/docs/tooling → low) and confirming the genuine ones.
// Workloads the deterministic pass didn't flag are left un-rowed (medium default), so the LLM only
// ever sees the handful of candidates — cheap and targeted. Topology facts for the whole account are
// computed in ONE set-based query. User overrides are never touched. Idempotent across re-runs.
//
// The sweep is WRITE-OR-HOLD, never write-partial: if its inputs were not actually observed (no
// knowledge graph, or the LLM review failed outright on an account that already has rows), it skips
// BOTH the write and the prune and returns Skipped. Rewriting the table from unobserved inputs is
// what turned a transient graph or llm-server outage into permanent data loss.
func DiscoverWorkloadCriticality(ctx context.Context, db *sqlx.DB, account, tenant string) (CriticalityDiscoveryStats, error) {
	start := time.Now()
	st := CriticalityDiscoveryStats{Account: account}
	skip := func(reason string) (CriticalityDiscoveryStats, error) {
		st.Skipped, st.SkipReason, st.Elapsed = true, reason, time.Since(start)
		return st, nil
	}

	graph, err := fetchAccountGraphFacts(ctx, db, account)
	if err != nil {
		return st, err
	}

	type wlRow struct {
		CloudResourceID string `db:"cloud_resource_id"`
		Namespace       string `db:"namespace"`
		Name            string `db:"name"`
		Kind            string `db:"kind"`
		Labels          string `db:"labels"`
	}
	var rows []wlRow
	if err := db.SelectContext(ctx, &rows, `
		SELECT cloud_resource_id::text AS cloud_resource_id, namespace, name, kind,
		       COALESCE(labels::text, '{}') AS labels
		FROM k8s_workloads
		WHERE is_active AND cloud_account_id = $1`, account); err != nil {
		return st, err
	}

	// An account with active workloads but no graph facts at all means the knowledge graph is missing
	// or mid-rebuild — NOT that nothing is important. Recall still runs (an operator-declared tier=
	// label needs no graph), but the topology-derived half of the answer is unobserved, so the prune
	// below must not act on it.
	graphUnobserved := len(rows) > 0 && len(graph) == 0

	// Stage 1 — deterministic recall: collect the candidates (topology-flagged workloads). Only these
	// go to the LLM, keeping the sweep fast and the tiering precise.
	var candidates []candidateRow
	var llmItems []llmWorkload
	for _, r := range rows {
		st.Scanned++
		f := workloadFacts{found: true, kind: r.Kind}
		parseWorkloadLabels(r.Labels, &f)
		if g, ok := graph[r.CloudResourceID]; ok {
			f.graphKnown = true
			f.image, f.customerFacing, f.fanIn = g.image, g.customerFacing, g.fanIn
		}
		lvl, conf, rat, ok := deriveCriticalityFromFacts(f)
		if !ok {
			continue // not topologically important → medium default, no LLM call
		}
		candidates = append(candidates, candidateRow{
			crid: r.CloudResourceID, namespace: r.Namespace,
			detLevel: lvl, detRat: rat, detConf: conf,
			signals: map[string]interface{}{
				"customer_facing": f.customerFacing, "fan_in": f.fanIn,
				"image": f.image, "app": f.appName,
			},
		})
		llmItems = append(llmItems, llmWorkload{
			CloudResourceID: r.CloudResourceID, Name: r.Name, Namespace: r.Namespace, Kind: r.Kind,
			Image: f.image, AppLabel: f.appName, CustomerFacing: f.customerFacing, GraphKnown: f.graphKnown, FanIn: f.fanIn,
		})
	}
	st.Candidate = len(candidates)

	// Stage 2 — LLM precision review of the candidates.
	verdicts, err := classifyWorkloads(ctx, tenant, account, llmItems)
	if err != nil {
		// The recall stage alone tiers every candidate `high`. Applying that to an account that
		// already has a reviewed tiering would flip every LLM-assigned `low` to `high` for the night
		// and flip it back tomorrow, so hold instead. Only an account with nothing to lose falls
		// back to the deterministic verdicts.
		hasRows, rowErr := hasAutoCriticalityRows(ctx, db, account)
		if rowErr != nil {
			return st, rowErr
		}
		if hasRows {
			slog.WarnContext(ctx, "criticality LLM review failed; holding existing rows",
				"account", account, "candidates", st.Candidate, "error", err)
			return skip("llm review failed")
		}
		slog.WarnContext(ctx, "criticality LLM review failed; seeding from deterministic candidates",
			"account", account, "candidates", st.Candidate, "error", err)
		verdicts = map[string]llmCriticalityVerdict{}
	}

	tieredCRIDs := make([]string, 0, len(candidates))
	var firstWriteErr error
	for _, c := range candidates {
		v, reviewed := verdicts[c.crid]
		r := resolveCandidateVerdict(c, v, reviewed)
		switch r.outcome {
		case outcomeDemoted:
			st.Demoted++
		case outcomeNoOpinion:
			st.NoOpinion++
		}
		level, source, rationale, conf := r.level, r.source, r.rationale, r.confidence
		if level == CriticalityMedium {
			st.Untiered++ // resolved to the medium default → no row
			continue
		}
		// The crid is retained either way: a workload we merely FAILED to refresh must not look
		// "no longer tiered" to the prune below, or a transient write error would also destroy the
		// last good row for that workload.
		tieredCRIDs = append(tieredCRIDs, c.crid)
		if err := upsertAutoCriticality(ctx, db, tenant, account, c.crid, c.namespace, source, level, rationale, conf, c.signals); err != nil {
			st.Failed++
			if firstWriteErr == nil {
				firstWriteErr = err
			}
			continue
		}
		st.Tiered++
	}

	// Remove auto rows for workloads no longer tiered (dropped below threshold, demoted by the LLM,
	// or topology changed). Only auto sources are pruned — user overrides are left untouched. Keeps
	// re-runs fully idempotent.
	//
	// The prune is `cloud_resource_id <> ALL($2)`, which with an empty array is TRUE for every row:
	// a sweep that reaches here having tiered nothing deletes the account's entire derived
	// criticality. That is only ever correct when this run actually OBSERVED that nothing qualifies.
	// When the graph was missing, or nothing was flagged while rows exist, hold instead — a
	// mid-rebuild graph must not read as "no workload matters anymore".
	prunable, holdReason, err := canPrune(ctx, db, account, graphUnobserved, len(tieredCRIDs))
	if err != nil {
		return st, err
	}
	if !prunable {
		st.Skipped, st.SkipReason = true, holdReason
	} else {
		tieredUUIDs := make([]uuid.UUID, 0, len(tieredCRIDs))
		for _, id := range tieredCRIDs {
			if parsed, err := uuid.Parse(id); err == nil {
				tieredUUIDs = append(tieredUUIDs, parsed)
			}
		}
		// Compare against the uuid column directly (no ::text cast) so the delete uses the index.
		if _, err := db.ExecContext(ctx, `
			DELETE FROM workload_criticality
			WHERE cloud_account_id = $1 AND source IN ('fact_signal', 'llm_inferred')
			  AND cloud_resource_id <> ALL($2)`,
			account, pq.Array(tieredUUIDs)); err != nil {
			st.Elapsed = time.Since(start)
			return st, err
		}
	}

	st.Elapsed = time.Since(start)
	if firstWriteErr != nil {
		// Surfaced as an error, not a warn: a sweep that classifies workloads and persists none of
		// them is a broken sweep, and the counters alone read as success.
		return st, fmt.Errorf("%d of %d criticality rows failed to persist (first error: %w)",
			st.Failed, st.Failed+st.Tiered, firstWriteErr)
	}
	return st, nil
}

// Outcomes of reconciling one candidate's deterministic verdict with the LLM's review.
const (
	outcomeKept      = "kept"       // no LLM verdict at all — deterministic stands
	outcomeNoOpinion = "no_opinion" // LLM answered medium — deterministic stands
	outcomeConfirmed = "confirmed"  // LLM confirmed or promoted (high/critical)
	outcomeDemoted   = "demoted"    // LLM actively demoted to low
)

// resolvedVerdict is what actually gets persisted for one candidate.
type resolvedVerdict struct {
	level, source, rationale string
	confidence               float64
	outcome                  string
}

// resolveCandidateVerdict reconciles a candidate's deterministic verdict with the LLM's review.
// `reviewed` distinguishes an absent verdict from a zero-valued one.
//
// The rule that matters: `medium` from the classifier is NO OPINION, not a demotion. Every workload
// reaching the classifier already carries a measured topology signal (ingress/LB-backing at 0.9
// confidence, or a large dependency fan-in), and the classifier is asked for precision on top of that
// signal — not to re-derive it. Treating its medium as a demotion let an "I can't tell" answer delete
// a measured fact, which is how genuinely ingress-backed workloads ended up at the medium default.
// A real demotion has to be stated as `low`.
func resolveCandidateVerdict(c candidateRow, v llmCriticalityVerdict, reviewed bool) resolvedVerdict {
	deterministic := resolvedVerdict{
		level: c.detLevel, source: CriticalitySourceFact, rationale: c.detRat,
		confidence: c.detConf, outcome: outcomeKept,
	}
	if !reviewed || !isValidCriticality(v.Criticality) {
		return deterministic
	}
	if v.Criticality == CriticalityMedium {
		deterministic.outcome = outcomeNoOpinion
		return deterministic
	}
	outcome := outcomeConfirmed
	if v.Criticality == CriticalityLow {
		outcome = outcomeDemoted
	}
	return resolvedVerdict{
		level: v.Criticality, source: CriticalitySourceLLM, rationale: v.Reason,
		confidence: llmVerdictConfidence, outcome: outcome,
	}
}

// llmVerdictConfidence is the confidence stamped on an LLM-assigned tier.
const llmVerdictConfidence = 0.75

// canPrune decides whether this run observed enough to be allowed to delete the account's stale
// derived rows. Returning false is a HOLD: rows written this run stand, nothing is removed.
//
// Two ways a run is not entitled to prune:
//   - the knowledge graph produced nothing, so the topology half of every verdict is unknown; or
//   - it tiered nothing at all while the account still has derived rows, which means the recall
//     inputs changed shape rather than every workload ceasing to matter overnight.
//
// A run that tiered at least one workload is always allowed to prune — it demonstrably saw the
// account, so the rows it did not re-tier really are stale.
func canPrune(ctx context.Context, db *sqlx.DB, account string, graphUnobserved bool, tiered int) (bool, string, error) {
	if graphUnobserved {
		return false, "no knowledge-graph facts for any workload", nil
	}
	if tiered > 0 {
		return true, "", nil
	}
	hasRows, err := hasAutoCriticalityRows(ctx, db, account)
	if err != nil {
		return false, "", err
	}
	if hasRows {
		return false, "nothing tiered while derived rows exist", nil
	}
	return true, "", nil
}

// hasAutoCriticalityRows reports whether an account already has derived (non-user) criticality rows.
// It is the "do we have something to lose?" check the write-or-hold guards branch on: an account with
// existing rows holds them through an unobserved sweep, an account with none is safe to seed.
func hasAutoCriticalityRows(ctx context.Context, db *sqlx.DB, account string) (bool, error) {
	var exists bool
	err := db.GetContext(ctx, &exists, `
		SELECT EXISTS (
			SELECT 1 FROM workload_criticality
			WHERE cloud_account_id = $1 AND source IN ('fact_signal', 'llm_inferred')
		)`, account)
	return exists, err
}

// fetchAccountGraphFacts computes customer-facing + dependency fan-in + image for every Workload node
// in an account in a single query, keyed by cloud_resource_id (properties->>'nb_resource_id'). This
// replaces N per-workload graph lookups with one set-based pass so discovery scales to large accounts.
func fetchAccountGraphFacts(ctx context.Context, db *sqlx.DB, account string) (map[string]accountGraphFact, error) {
	type row struct {
		CRID           string `db:"crid"`
		Image          string `db:"image"`
		CustomerFacing bool   `db:"customer_facing"`
		FanIn          int    `db:"fan_in"`
	}
	var rows []row
	if err := db.SelectContext(ctx, &rows, `
		WITH wl AS (
			SELECT id, (properties->>'nb_resource_id') AS crid, COALESCE(properties->>'primary_image','') AS image
			FROM knowledge_graph_node
			WHERE node_type = 'Workload' AND is_active AND cloud_account_id = $1
			  AND (properties->>'nb_resource_id') IS NOT NULL
		),
		cf AS (
			SELECT DISTINCT e.source_node_id AS id
			FROM knowledge_graph_edge e
			JOIN knowledge_graph_edge r
			  ON r.destination_node_id = e.destination_node_id
			 AND r.relationship_type IN ('ROUTES_TO_SERVICE','ROUTES_TO','ROUTES_THROUGH') AND r.is_active
			 AND r.cloud_account_id = $1
			JOIN knowledge_graph_node rs ON rs.id = r.source_node_id AND rs.node_type IN ('Ingress','LoadBalancer')
			 AND rs.cloud_account_id = $1
			WHERE e.relationship_type = 'EXPOSES' AND e.is_active AND e.cloud_account_id = $1
		),
		fi AS (
			SELECT e.destination_node_id AS id, count(*) AS n
			FROM knowledge_graph_edge e
			JOIN knowledge_graph_node s ON s.id = e.source_node_id AND s.node_type = 'Workload'
			 AND s.cloud_account_id = $1
			WHERE e.relationship_type = 'CALLS' AND e.is_active AND e.cloud_account_id = $1
			GROUP BY e.destination_node_id
		)
		SELECT wl.crid, wl.image,
		       (cf.id IS NOT NULL) AS customer_facing,
		       COALESCE(fi.n, 0) AS fan_in
		FROM wl
		LEFT JOIN cf ON cf.id = wl.id
		LEFT JOIN fi ON fi.id = wl.id`, account); err != nil {
		return nil, err
	}
	out := make(map[string]accountGraphFact, len(rows))
	for _, r := range rows {
		out[r.CRID] = accountGraphFact{image: r.Image, customerFacing: r.CustomerFacing, fanIn: r.FanIn}
	}
	return out, nil
}
