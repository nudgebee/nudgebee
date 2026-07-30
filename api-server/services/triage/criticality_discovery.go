package triage

import (
	"context"
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
	Demoted   int // candidates the LLM demoted to medium (no row written)
	Tiered    int // rows written (critical/high/low) after the LLM precision review
	Elapsed   time.Duration
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
		ctx.GetLogger().Info("criticality discovery: account swept",
			"account", st.Account, "scanned", st.Scanned, "candidates", st.Candidate,
			"demoted", st.Demoted, "tiered", st.Tiered, "elapsed_ms", st.Elapsed.Milliseconds())
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
// it recognizes (demo/test/e2e/benchmark/docs/tooling → medium/low) and confirming the genuine ones.
// Workloads the deterministic pass didn't flag are left un-rowed (medium default), so the LLM only
// ever sees the handful of candidates — cheap and targeted. Topology facts for the whole account are
// computed in ONE set-based query. User overrides are never touched. Idempotent across re-runs.
func DiscoverWorkloadCriticality(ctx context.Context, db *sqlx.DB, account, tenant string) (CriticalityDiscoveryStats, error) {
	start := time.Now()
	st := CriticalityDiscoveryStats{Account: account}

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

	// Stage 2 — LLM precision review of the candidates. On failure, fall back to the deterministic
	// verdicts (empty map ⇒ every candidate keeps its fact_signal tier).
	verdicts, err := classifyWorkloadsLLM(ctx, tenant, account, llmItems)
	if err != nil {
		slog.WarnContext(ctx, "criticality LLM review failed; keeping deterministic candidates",
			"account", account, "error", err)
		verdicts = map[string]llmCriticalityVerdict{}
	}

	tieredCRIDs := make([]string, 0, len(candidates))
	for _, c := range candidates {
		level, source, rationale, conf := c.detLevel, CriticalitySourceFact, c.detRat, c.detConf
		if v, ok := verdicts[c.crid]; ok {
			level, source, rationale, conf = v.Criticality, CriticalitySourceLLM, v.Reason, 0.75
		}
		if level == CriticalityMedium {
			st.Demoted++ // LLM judged this candidate not actually important → medium default, no row
			continue
		}
		upsertAutoCriticality(ctx, db, tenant, account, c.crid, c.namespace, source, level, rationale, conf, c.signals)
		tieredCRIDs = append(tieredCRIDs, c.crid)
		st.Tiered++
	}

	// Remove auto rows for workloads no longer tiered (dropped below threshold, demoted by the LLM,
	// or topology changed). Only auto sources are pruned — user overrides are left untouched. Keeps
	// re-runs fully idempotent.
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

	st.Elapsed = time.Since(start)
	return st, nil
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
