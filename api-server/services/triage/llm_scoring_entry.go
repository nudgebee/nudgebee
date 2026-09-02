package triage

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"nudgebee/services/config"
	"nudgebee/services/internal/database/models"
	"nudgebee/services/llm"
	"nudgebee/services/security"

	"github.com/jmoiron/sqlx"
)

// systemUserID is used for the off-hot-path mint call to llm-server.
const systemUserID = "00000000-0000-0000-0000-000000000000"

// mintTimeout bounds a background (re-)mint's LLM call so a hung llm-server can't leak the
// goroutine. DB cleanup after a failed/timed-out mint runs on a fresh context, not this one.
const mintTimeout = 2 * time.Minute

// classificationPromptVersion stamps every minted verdict. Bump it whenever classificationPrompt
// changes in a way that should re-classify existing classes (new categories, recalibrated severity
// rules, etc.). When feature_llm_triage_remint_enabled is on, a served verdict minted under an older
// version is lazily re-minted. Cosmetic prompt edits should NOT bump it.
//
// v2: the classifier now receives observed workload facts (ingress-backing, dependency fan-in,
// operator labels) and grounding guidance, so blast/intrinsic are decided from topology facts
// instead of guessed from the workload name.
// v3: reasoning is constrained to be class-generic (no cluster/node/pod/instance/timestamp) and the
// concrete cluster name is dropped from the classifier context, so a cached verdict served across
// clusters no longer carries the minting instance's cluster/job identity into downstream consumers.
// v4: workload_criticality is no longer rendered into the prompt. It is a per-WORKLOAD fact and this
// verdict is cached per signal class (shared by every workload of the same name, tenant-wide), so
// baking it in served one workload's tier to all of them and made operator overrides invisible to
// scoring. It is now applied deterministically at score time instead.
const classificationPromptVersion = 4

// ComputeScoreLLM scores an event via its cached per-class verdict + the deterministic policy.
// On cache miss it scores with a conservative fallback verdict and (best-effort, off the hot
// path) triggers minting of the real verdict for subsequent events of the class. It never
// blocks on llm-server — the hot path is a single indexed cache lookup.
func ComputeScoreLLM(ctx context.Context, db *sqlx.DB, event *models.Event) (*ScoreResult, error) {
	classKey := ClassKey(event)
	tenantID := ""
	if event.Tenant != nil {
		tenantID = *event.Tenant
	}

	var verdict *SignalVerdict
	lookup := "fallback"
	if tenantID != "" {
		if v, ok := getSignalVerdict(ctx, db, tenantID, classKey); ok {
			verdict, lookup = v, "cache"
			// Lazily re-mint a verdict minted under an older prompt version (flag-gated). The
			// stale verdict keeps serving until the re-mint promotes its replacement. A pinned
			// class never reaches here (resolveHumanOverride short-circuits before ComputeScoreLLM),
			// so a human pin is never re-minted.
			if config.Config.FeatureLLMTriageRemintEnabled && v.PromptVersion < classificationPromptVersion {
				triggerRemint(db, event, tenantID, classKey)
				lookup = "cache_stale_remint"
			}
		}
	}
	if verdict == nil {
		verdict = fallbackVerdict(event)
		if tenantID != "" {
			triggerMint(db, event, tenantID, classKey)
		}
	}

	// Deterministic guardrails correct the cached verdict for event-level facts it can't see
	// (e.g. a provider lifecycle/state-change notification the LLM mis-rated as a control-plane
	// incident). Applied to a copy — the shared cached verdict is never mutated.
	verdict, guardrails := applyDeterministicGuardrails(verdict, event)

	envCategory := "unknown"
	if event.CloudAccountId != nil {
		envCategory = getEnvironmentCategory(ctx, db, *event.CloudAccountId)
	}
	recurrenceCount := getOccurrenceCount(ctx, db, event.Id)
	tier := resolveEventCriticality(ctx, db, event)

	score, priority, factors := computeVerdictScore(verdict, envCategory, recurrenceCount, tier)
	factors["class_key"] = classKey
	factors["verdict_lookup"] = lookup
	if len(guardrails) > 0 {
		factors["guardrails"] = guardrails
	}

	// Correlation-aware cascade dampening: consume the existing event_correlations so a likely
	// root cause stays prominent and downstream/co-occurring symptoms are quieted — collapsing a
	// cascade toward one item instead of flooding the queue with its symptoms.
	if event.Id != "" {
		if adj, corrType := correlationDampening(ctx, db, event.Id); adj != 0 {
			score = clamp(score+adj, 0, 100)
			priority = scoreToPriority(score)
			factors["correlation_type"] = corrType
			factors["correlation_adjustment"] = adj
		}
	}

	return &ScoreResult{
		Score:      score,
		Priority:   priority,
		Factors:    factors,
		Confidence: verdict.Confidence,
	}, nil
}

// resolveEventCriticality looks up the business criticality of the workload this event is about.
//
// This runs per event, on the scoring path, and that placement is the point. Criticality used to
// reach scoring only as text in the classifier prompt — but that verdict is cached per signal class
// (keyed on aggregation_key + finding_type + subject_owner, tenant-wide), so one workload's tier was
// served to every workload sharing the NAME, and a tier change never invalidated the cache. Resolving
// it here makes it per-workload again and makes an operator's override take effect on the very next
// event instead of never.
//
// Cost is one indexed lookup on (cloud_account_id, cloud_resource_id) against a table holding tens of
// rows per account. The ~70% of events that already carry a cloud_resource_id use it directly and
// never touch k8s_workloads at all; only the name-based remainder pays for the extra resolution.
// Best-effort: an unresolvable workload scores exactly as it does today.
func resolveEventCriticality(ctx context.Context, db *sqlx.DB, event *models.Event) workloadTier {
	if db == nil || event == nil || event.CloudAccountId == nil {
		return workloadTier{}
	}
	crid := ""
	if event.CloudResourceId != nil {
		crid = *event.CloudResourceId
	}
	if crid == "" {
		// No reliable key on the event — fall back to resolving the workload by name.
		var f workloadFacts
		if crid, f = resolveEventWorkload(ctx, db, event); !f.found || crid == "" {
			return workloadTier{}
		}
	}
	wc, ok := resolveWorkloadCriticality(ctx, db, *event.CloudAccountId, crid)
	if !ok {
		return workloadTier{}
	}
	return workloadTier{level: wc.Criticality, source: wc.Source}
}

// correlationDampening returns the cascade adjustment for an event from its strongest recorded
// correlation (>=0.5), or 0 if uncorrelated. See correlationTypeAdjustment for the mapping.
func correlationDampening(ctx context.Context, db *sqlx.DB, eventID string) (int, string) {
	var c struct {
		CorrelationType  string  `db:"correlation_type"`
		CorrelationScore float64 `db:"correlation_score"`
	}
	err := db.GetContext(ctx, &c, `
		SELECT correlation_type, correlation_score
		FROM event_correlations
		WHERE event_id = $1
		ORDER BY correlation_score DESC
		LIMIT 1`, eventID)
	if err != nil || c.CorrelationScore < 0.5 {
		return 0, c.CorrelationType
	}
	return correlationTypeAdjustment(c.CorrelationType), c.CorrelationType
}

// getOccurrenceCount returns the per-fingerprint occurrence number from event_duplicates
// (Phase 1 recurrence signal). First occurrence / no record -> 1.
func getOccurrenceCount(ctx context.Context, db *sqlx.DB, eventID string) int {
	if eventID == "" {
		return 1
	}
	var n int
	if err := db.GetContext(ctx, &n, `SELECT occurrence_number FROM event_duplicates WHERE event_id = $1`, eventID); err != nil {
		return 1
	}
	if n < 1 {
		return 1
	}
	return n
}

// triggerMint claims the class and, if claimed, mints its verdict asynchronously.
func triggerMint(db *sqlx.DB, event *models.Event, tenantID, classKey string) {
	if !claimSignalClass(context.Background(), db, tenantID, classKey) {
		return // another event/replica is already minting this class
	}
	go func() {
		// A mint must never crash the process. If anything in the mint path panics (e.g. a failed
		// security-context build), recover and release the claim so the class can re-mint later.
		defer func() {
			if r := recover(); r != nil {
				slog.Error("signal-class mint panicked; recovered and released claim",
					"class_key", classKey, "panic", r)
				releaseClaim(context.Background(), db, tenantID, classKey)
			}
		}()
		// Detached but BOUNDED: cap the LLM call so a hung llm-server can't leak this goroutine.
		ctx, cancel := context.WithTimeout(context.Background(), mintTimeout)
		defer cancel()
		verdict, err := mintSignalVerdict(ctx, db, event)
		if err != nil {
			// Release the claim so a future event re-attempts (e.g. once the minter is wired
			// or after a transient llm-server failure). Until then, fallback scoring is used.
			// Cleanup runs on a fresh context — the mint context may already be timed out.
			releaseClaim(context.Background(), db, tenantID, classKey)
			slog.WarnContext(ctx, "signal-class mint failed; released claim",
				"class_key", classKey, "error", err)
			return
		}
		if err := saveSignalVerdict(context.Background(), db, tenantID, classKey, verdict); err != nil {
			if errors.Is(err, ErrMintSuperseded) {
				// Benign: another goroutine released or already promoted this class.
				slog.DebugContext(ctx, "signal-class mint superseded; discarding result",
					"class_key", classKey)
				return
			}
			slog.WarnContext(ctx, "failed to persist minted verdict",
				"class_key", classKey, "error", err)
			return
		}
		// Verdict is now active — refresh events of this class that were scored with the
		// conservative fallback before the mint completed, so they pick up the real verdict.
		rescoreFreshlyMintedClass(context.Background(), db, tenantID, classKey)
	}()
}

// triggerRemint flips a stale active verdict to 'reminting' (CAS) and, if it won the claim, mints
// a fresh verdict asynchronously. The old verdict keeps serving until the new one is promoted; on
// failure the row is reverted to active so the old verdict is preserved (never lost to a transient
// llm-server error).
func triggerRemint(db *sqlx.DB, event *models.Event, tenantID, classKey string) {
	if !claimRemint(context.Background(), db, tenantID, classKey, classificationPromptVersion) {
		return // another serve already claimed the re-mint, or it's no longer stale
	}
	go func() {
		// A re-mint must never crash the process; recover and revert so the old verdict keeps serving.
		defer func() {
			if r := recover(); r != nil {
				slog.Error("signal-class re-mint panicked; recovered and reverted",
					"class_key", classKey, "panic", r)
				revertRemint(context.Background(), db, tenantID, classKey)
			}
		}()
		// Bounded LLM call (see triggerMint); DB revert/persist use a fresh context.
		ctx, cancel := context.WithTimeout(context.Background(), mintTimeout)
		defer cancel()
		verdict, err := mintSignalVerdict(ctx, db, event)
		if err != nil {
			revertRemint(context.Background(), db, tenantID, classKey)
			slog.WarnContext(ctx, "signal-class re-mint failed; kept existing verdict",
				"class_key", classKey, "error", err)
			return
		}
		if err := saveSignalVerdict(context.Background(), db, tenantID, classKey, verdict); err != nil {
			if errors.Is(err, ErrMintSuperseded) {
				slog.DebugContext(ctx, "signal-class re-mint superseded; discarding result", "class_key", classKey)
				return
			}
			revertRemint(context.Background(), db, tenantID, classKey)
			slog.WarnContext(ctx, "failed to persist re-minted verdict; reverted to active",
				"class_key", classKey, "error", err)
			return
		}
		// New verdict promoted — refresh any events still carrying the fallback for this class.
		rescoreFreshlyMintedClass(context.Background(), db, tenantID, classKey)
	}()
}

// rescoreMaxOnMint bounds how many fallback events one mint refreshes, so a burst of new classes
// (e.g. when the flag is first enabled for a tenant) can't trigger an unbounded re-score storm.
const rescoreMaxOnMint = 30

// rescoreFreshlyMintedClass refreshes a class's recent fallback-scored events once a real verdict is
// active, so events scored before the mint completed stop carrying a stale 'unclassified' score
// forever (the cause of "the same alert shows P3 then P1 then P0"). It re-runs the normal
// ProcessEvent path, which re-reads the now-cached verdict — so the re-scored events are consistent
// with freshly-ingested ones (same additive rules, same band clamp). It does NOT emit lifecycle /
// notification events, so it only corrects the stored score and never re-pages. Safe to re-run:
// detectAndRecordDuplicate returns the STORED occurrence for an already-recorded event, so this
// re-run cannot inflate the occurrence number and trip the occurrence>1 auto-duplicate rule on
// the chain's first event (the bug that used to mark an original as a duplicate of itself).
// Bounded + async
// (called from the mint goroutine on a fresh context); matches the class by the class_key stored in
// score_factors (exact, no re-derivation).
func rescoreFreshlyMintedClass(ctx context.Context, db *sqlx.DB, tenantID, classKey string) {
	if tenantID == "" || classKey == "" {
		return
	}
	var ids []string
	if err := db.SelectContext(ctx, &ids, `
		SELECT id::text FROM events
		WHERE tenant = $1
		  AND score_factors->>'class_key' = $2
		  AND COALESCE(score_factors->>'verdict_source', '') = 'fallback'
		  AND nb_status IN ('OPEN', 'ACKNOWLEDGED', 'INVESTIGATING', 'ACTION_REQUIRED')
		  AND updated_at > now() - interval '24 hours'
		ORDER BY updated_at DESC
		LIMIT $3`, tenantID, classKey, rescoreMaxOnMint); err != nil {
		slog.WarnContext(ctx, "rescore-on-mint: failed to load fallback events", "class_key", classKey, "error", err)
		return
	}
	if len(ids) == 0 {
		return
	}
	rescored := 0
	for _, id := range ids {
		var ev models.Event
		if err := db.Unsafe().GetContext(ctx, &ev, `SELECT * FROM events WHERE id = $1`, id); err != nil {
			slog.WarnContext(ctx, "rescore-on-mint: failed to load event", "event_id", id, "error", err)
			continue
		}
		if err := ProcessEvent(ctx, db, &ev); err != nil {
			slog.WarnContext(ctx, "rescore-on-mint: ProcessEvent failed", "event_id", id, "error", err)
			continue
		}
		rescored++
	}
	slog.InfoContext(ctx, "rescore-on-mint: refreshed fallback events with minted verdict",
		"class_key", classKey, "candidates", len(ids), "rescored", rescored)
}

// releaseClaim removes a still-minting placeholder so the class can be re-claimed later.
func releaseClaim(ctx context.Context, db *sqlx.DB, tenantID, classKey string) {
	if _, err := db.ExecContext(ctx,
		`DELETE FROM triage_signal_class WHERE tenant_id = $1 AND class_key = $2 AND status = 'minting'`,
		tenantID, classKey); err != nil {
		slog.WarnContext(ctx, "failed to release signal-class claim", "class_key", classKey, "error", err)
	}
}

// mintSignalVerdict asks the LLM to categorize a never-seen signal class into a SignalVerdict.
// Runs OFF the hot path. It reuses the EXISTING generic `@llm` agent via the EXISTING
// llm.ChatCompletion client — no new endpoint, no new agent, no agentic investigation, one
// LLM call. The full classification instructions live in the query (since `@llm` has no domain
// system prompt). The returned JSON verdict is parsed and persisted per signal-class.
func mintSignalVerdict(ctx context.Context, db *sqlx.DB, event *models.Event) (*SignalVerdict, error) {
	tenantID := ""
	if event.Tenant != nil {
		tenantID = *event.Tenant
	}
	accountID := ""
	if event.CloudAccountId != nil {
		accountID = *event.CloudAccountId
	}
	if tenantID == "" || accountID == "" {
		return nil, fmt.Errorf("mint requires tenant and account")
	}

	// Gather the workload's observed facts to ground the classifier prompt (labels, topology, and
	// the resolved workload_criticality — an operator override wins, else the live derivation).
	// Population of the workload_criticality table is owned by the discovery job, not this hot-ish
	// path, so the inventory is complete rather than alert-gated.
	facts := fetchWorkloadFacts(ctx, db, event)

	sc := security.NewRequestContextForTenantAdmin(tenantID, slog.Default(), nil, nil)
	resp, err := llm.ChatCompletion(sc, llm.ConversationApiRequest{
		Query:     classificationPrompt(event, facts),
		AccountId: accountID,
		UserId:    systemUserID,
		Async:     false,
		Source:    "triage_classification",
	})
	if err != nil {
		return nil, err
	}
	if resp == nil || len(resp.Response) == 0 {
		return nil, fmt.Errorf("empty classify response")
	}
	return parseVerdict(resp.Response[0])
}

// classificationPrompt builds the single-shot `@llm` query: the validated triage-classifier
// instructions + the alert context. `@llm` routes to a generic direct-LLM agent (no tools, no
// investigation), so all guidance must be inline.
func classificationPrompt(event *models.Event, facts workloadFacts) string {
	return "@llm You are a Kubernetes/cloud alert TRIAGE CLASSIFIER. Classify the single alert below into a structured verdict. Do NOT investigate, call tools, or ask questions.\n\n" +
		"## Alert\n" + buildEventContext(event, facts) + "\n" +
		groundingGuidance +
		"## Output\nReturn ONLY a JSON object (no markdown fence, no prose) with EXACTLY these keys:\n" +
		`{"category":"<one fixed value below>","intrinsic":"critical|high|medium|low|info","blast":"control_plane|customer_facing|data_durability|monitoring_backbone|single_workload|expected_change","recurrence_semantics":"escalating|neutral|noise","env_sensitivity":"none|partial|full","min_priority":"P3","max_priority":"P0","confidence":0.0,"reasoning":"<=200 chars"}` + "\n\n" +
		"## category — pick EXACTLY ONE from this fixed list; do NOT invent new labels:\n" +
		"Compute (pods/workloads: crashloop, OOM, restarts, image-pull, scheduling), Storage (disk/PV/volume capacity), Network (connectivity, DNS, target-down, ingress), ControlPlane (apiserver/scheduler/controller-manager/kubelet/node health), Observability (prometheus/vmagent/victoria-metrics/alertmanager/metrics pipeline), Database (DB performance, deadlock, capacity, slow queries), Application (app error rate, latency, API failures, SLO), Change (config change, deploy, upgrade, lifecycle), Other.\n\n" +
		"## intrinsic — be STRICT; do NOT default to high. Most warnings are MEDIUM:\n" +
		"critical = control-plane/cluster-wide outage, data loss, or a full customer-facing outage.\n" +
		"high = a single PRODUCTION service is DOWN / crashlooping / OOMKilled, OR a customer-facing error rate is clearly elevated, OR disk is nearly full, OR a node is NotReady. Reserve high for genuine breakage.\n" +
		"medium = DEGRADED-but-working — elevated-but-non-breaking errors, latency, capacity/threshold WARNINGS, queue backlogs, deadlocks, slow queries, low cache-hit. This is the DEFAULT for warnings.\n" +
		"low = minor, dev/test-only, single-pod transient, or informational-leaning. info = purely informational or expected lifecycle.\n\n" +
		"## blast — assign NARROWLY:\n" +
		"control_plane = ONLY actual k8s control-plane components (apiserver/scheduler/controller-manager/kubelet/node). customer_facing = ONLY a user-facing request path with user-visible impact (NOT internal queues, jobs, or infra). data_durability = disk/PV/DB/storage at risk. monitoring_backbone = the observability/alerting stack. single_workload = everything else (DEFAULT). expected_change = planned maintenance/lifecycle/labs.\n\n" +
		"## recurrence_semantics:\n" +
		"escalating = a crash/OOM/restart/disk-filling/persistent failure that worsens with repetition. noise = a chronic, frequently-recurring, typically-tolerated condition (queue backlog, low cache-hit, slow queries, flapping) — DAMPEN these. neutral = recurrence carries no signal.\n\n" +
		"## env_sensitivity = how much a non-production discount applies: none (control-plane down, data loss) | partial (a crashing service in dev still matters) | full (dev slow-query, test-env SLO, lab).\n" +
		"min_priority = LEAST-severe bound, max_priority = MOST-severe bound (P0 most severe, then P1, P2, P3 least). confidence MUST be a number 0.0-1.0; reasoning MUST be <= 200 characters.\n" +
		"## reasoning — describe the alert CLASS generically. This verdict is CACHED and reused for every alert of the same kind across the whole tenant, so reasoning MUST NOT name a specific cluster, node, pod, job/instance suffix (e.g. 'popeye-scan-00947d98' -> 'a scan job'), IP, or timestamp. State why the class has this severity, not what one instance was."
}

// buildEventContext renders the compact alert context the classifier reasons over, including any
// observed workload facts (topology, labels) so blast/intrinsic are grounded in reality.
func buildEventContext(event *models.Event, facts workloadFacts) string {
	get := func(p *string) string {
		if p == nil {
			return ""
		}
		return *p
	}
	var b strings.Builder
	fmt.Fprintf(&b, "- title: %q\n", event.Title)
	fmt.Fprintf(&b, "- aggregation_key: %s\n", get(event.AggregationKey))
	fmt.Fprintf(&b, "- source: %s\n", get(event.Source))
	fmt.Fprintf(&b, "- finding_type: %s\n", get(event.FindingType))
	fmt.Fprintf(&b, "- priority(source): %s\n", get(event.Priority))
	scope := get(event.SubjectOwner)
	if scope == "" {
		scope = get(event.SubjectNamespace)
	}
	// Cluster is deliberately omitted: it is NOT part of the class key (signal_class.go), so a
	// verdict minted from one cluster is served to every cluster in the tenant. Feeding the concrete
	// cluster name only invited the classifier to echo it into the (cross-cluster) cached reasoning.
	fmt.Fprintf(&b, "- subject: %s %s in namespace %s\n",
		get(event.SubjectOwnerKind), scope, get(event.SubjectNamespace))
	if d := get(event.Description); d != "" {
		if len(d) > 300 {
			d = d[:300]
		}
		fmt.Fprintf(&b, "- description: %s\n", d)
	}
	renderWorkloadFacts(&b, facts)
	return b.String()
}

// groundingGuidance instructs the classifier to prefer the observed workload facts (rendered by
// renderWorkloadFacts) over the workload name when deciding blast/intrinsic. Kept general — it
// teaches how to read the facts, never a specific workload or incident.
const groundingGuidance = "## Grounding — PREFER OBSERVED FACTS over the workload's name:\n" +
	"- customer_facing=true means the workload is actually ingress/LoadBalancer-backed (a user-facing request path); set blast=customer_facing unless it is genuinely a control-plane component. A false value means it is NOT directly user-facing.\n" +
	"- dependency_fan_in is how many services are observed to depend on this workload; a large value means it is a shared dependency — raise blast above single_workload (a datastore/queue/cache with many dependents leans data_durability or control_plane). A handful of callers is ordinary; reserve the escalation for genuine hubs.\n" +
	"- declared_criticality_label is the operator's OWN tiering declaration — treat it as authoritative for intrinsic/blast.\n" +
	"- image/labels identify what the workload IS (e.g. a database, cache, gateway) — use them, not just the name.\n" +
	"- When these fact lines are ABSENT, they are simply unobserved — do NOT infer low importance from their absence; fall back to the alert semantics.\n\n"

// parseVerdict extracts the JSON verdict from the model text (tolerating markdown fences and a
// string-valued confidence) and maps min_priority/max_priority -> BandFloor/BandCeiling.
func parseVerdict(text string) (*SignalVerdict, error) {
	s := strings.TrimSpace(text)
	// strip ```json ... ``` fences if present
	if i := strings.Index(s, "{"); i >= 0 {
		if j := strings.LastIndex(s, "}"); j >= i {
			s = s[i : j+1]
		}
	}
	var m map[string]interface{}
	if err := json.Unmarshal([]byte(s), &m); err != nil {
		return nil, fmt.Errorf("parse verdict JSON: %w", err)
	}
	str := func(k string) string {
		if v, ok := m[k].(string); ok {
			return strings.TrimSpace(v)
		}
		return ""
	}
	conf := 0.5
	switch c := m["confidence"].(type) {
	case float64:
		conf = c
	case string:
		switch strings.ToLower(strings.TrimSpace(c)) {
		case "high":
			conf = 0.9
		case "medium":
			conf = 0.6
		case "low":
			conf = 0.3
		}
	}
	v := &SignalVerdict{
		Category:            str("category"),
		Intrinsic:           strings.ToLower(str("intrinsic")),
		Blast:               strings.ToLower(str("blast")),
		RecurrenceSemantics: strings.ToLower(str("recurrence_semantics")),
		EnvSensitivity:      strings.ToLower(str("env_sensitivity")),
		BandFloor:           strings.ToUpper(str("min_priority")), // least-severe bound
		BandCeiling:         strings.ToUpper(str("max_priority")), // most-severe bound
		Confidence:          conf,
		Reasoning:           str("reasoning"),
		SourceModel:         "llm_classify",
		PromptVersion:       classificationPromptVersion,
	}
	if v.Intrinsic == "" || v.BandFloor == "" || v.BandCeiling == "" {
		return nil, fmt.Errorf("verdict missing required fields: %q", s)
	}
	return v, nil
}
