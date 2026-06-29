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
const classificationPromptVersion = 1

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

	envCategory := "unknown"
	if event.CloudAccountId != nil {
		envCategory = getEnvironmentCategory(ctx, db, *event.CloudAccountId)
	}
	recurrenceCount := getOccurrenceCount(ctx, db, event.Id)

	score, priority, factors := computeVerdictScore(verdict, envCategory, recurrenceCount)
	factors["class_key"] = classKey
	factors["verdict_lookup"] = lookup

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
		verdict, err := mintSignalVerdict(ctx, event)
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
		}
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
		verdict, err := mintSignalVerdict(ctx, event)
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
		}
	}()
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
func mintSignalVerdict(ctx context.Context, event *models.Event) (*SignalVerdict, error) {
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

	sc := security.NewRequestContextForTenantAdmin(tenantID, slog.Default(), nil, nil)
	resp, err := llm.ChatCompletion(sc, llm.ConversationApiRequest{
		Query:     classificationPrompt(event),
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
func classificationPrompt(event *models.Event) string {
	return "@llm You are a Kubernetes/cloud alert TRIAGE CLASSIFIER. Classify the single alert below into a structured verdict. Do NOT investigate, call tools, or ask questions.\n\n" +
		"## Alert\n" + buildEventContext(event) + "\n" +
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
		"min_priority = LEAST-severe bound, max_priority = MOST-severe bound (P0 most severe, then P1, P2, P3 least). confidence MUST be a number 0.0-1.0; reasoning MUST be <= 200 characters."
}

// buildEventContext renders the compact alert context the classifier reasons over.
func buildEventContext(event *models.Event) string {
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
	fmt.Fprintf(&b, "- subject: %s %s in namespace %s (cluster %s)\n",
		get(event.SubjectOwnerKind), scope, get(event.SubjectNamespace), get(event.Cluster))
	if d := get(event.Description); d != "" {
		if len(d) > 300 {
			d = d[:300]
		}
		fmt.Fprintf(&b, "- description: %s\n", d)
	}
	return b.String()
}

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
