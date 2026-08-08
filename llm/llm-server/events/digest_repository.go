package events

import (
	"encoding/json"
	"fmt"
	"time"

	"nudgebee/llm/common"
	"nudgebee/llm/security"
)

// DigestStatus values for event_analysis_digest.status.
const (
	DigestStatusGenerated = "generated"
	DigestStatusPartial   = "partial"
	DigestStatusFailed    = "failed"
)

// Digest source values. An on-demand row is provisional — the gap scan keeps
// re-selecting it so the next scheduled run regenerates and supersedes it.
const (
	DigestSourceScheduled = "scheduled"
	DigestSourceOnDemand  = "on_demand"
)

// DigestLookbackWeeks bounds how far back the generator will fill gaps. A missed
// cron tick self-heals within this window; anything older is treated as history
// and left alone (a deliberate backfill is a separate decision).
const DigestLookbackWeeks = 3

// DigestPeriod identifies one (account, week) digest slot.
type DigestPeriod struct {
	TenantID       string    `db:"tenant_id"`
	CloudAccountID string    `db:"cloud_account_id"`
	PeriodStart    time.Time `db:"period_start"`
	PeriodEnd      time.Time `db:"period_end"`
}

// DigestMetrics is the headline counter set rendered as chips in the UI.
type DigestMetrics struct {
	Analyses       int `db:"analyses"        json:"analyses"`
	Failed         int `db:"failed"          json:"failed"`
	FailureClasses int `db:"failure_classes" json:"failure_classes"`
	EventsAnalysed int `db:"events_analysed" json:"events_analysed"`
	NewIncidents   int `db:"new_incidents"   json:"new_incidents"`
	Recurring      int `db:"recurring"       json:"recurring"`
	Services       int `db:"services"        json:"services"`
	P1Pct          int `db:"p1_pct"          json:"p1_pct"`
	NoisePct       int `db:"noise_pct"       json:"noise_pct"`
	NoiseEvents    int `db:"noise_events"    json:"noise_events"`
	// Learnings is filled separately — it comes from the memory layer, not events,
	// so it has no column in the metrics query.
	Learnings int `db:"-" json:"learnings"`
}

// DigestClass is one failure class within the period, used both as the map-stage
// input and as the stored top_classes rollup.
type DigestClass struct {
	AggregationKey  string `json:"aggregation_key" db:"aggregation_key"`
	Events          int    `json:"events" db:"events"`
	NewIncidents    int    `json:"new_incidents" db:"new_incidents"`
	WorstRecurrence int    `json:"worst_recurrence" db:"worst_recurrence"`
	Services        int    `json:"services" db:"services"`
	Priority        string `json:"priority" db:"priority"`
	// Owner resolves through ownership_rules; empty when no rule matches.
	Owner string `json:"owner,omitempty" db:"owner"`
	// Title and Source let the map stage judge whether a class is real traffic or
	// synthetic — an untagged test burst carries the giveaway in its title and
	// webhook source, not in any classification column.
	Title  string `json:"title,omitempty"  db:"title"`
	Source string `json:"source,omitempty" db:"source"`
	// ActiveDays and SpanHours describe the shape of the traffic. A test burst is
	// confined to one day; a real failure class recurs across days. This is the
	// hard evidence that bounds the map stage's synthetic judgement.
	ActiveDays int     `json:"active_days" db:"active_days"`
	SpanHours  float64 `json:"span_hours"  db:"span_hours"`
}

// FindPendingDigestPeriods returns the (account, week) slots inside the lookback
// window that still need a scheduled run: no row yet, a failed or partial
// attempt, or a provisional on-demand row awaiting its scheduled replacement.
//
// This is the recovery mechanism: the scheduler has no memory of past ticks, so
// "did this week run?" is answered by the presence of a generated row rather
// than by scheduler state. A pod restart over the cron boundary costs at most
// one extra tick, not a missing week.
func FindPendingDigestPeriods(ctx *security.RequestContext) ([]DigestPeriod, error) {
	dbms, err := common.GetDatabaseManager(common.Metastore)
	if err != nil {
		return nil, fmt.Errorf("FindPendingDigestPeriods: db manager: %w", err)
	}

	query := `
		WITH weeks AS (
			-- Week boundary pinned to UTC so it matches the Go side's
			-- currentWeekStart(); a database running in a local timezone would
			-- otherwise truncate to a different Monday, and the scheduler would
			-- generate weeks the on-demand handler never looks up.
			SELECT generate_series(
				date_trunc('week', now() AT TIME ZONE 'UTC')::date - ($1::int * 7),
				date_trunc('week', now() AT TIME ZONE 'UTC')::date - 7,
				interval '7 days'
			)::date AS period_start
		),
		active AS (
			SELECT DISTINCT ela.cloud_account_id, e.tenant, w.period_start
			FROM weeks w
			JOIN event_log_analysis ela
			  ON ela.recorded_at >= w.period_start
			 AND ela.recorded_at <  w.period_start + 7
			JOIN events e ON e.id = ela.event_id
			WHERE ela.cloud_account_id IS NOT NULL
		)
		SELECT a.tenant::text          AS tenant_id,
		       a.cloud_account_id::text AS cloud_account_id,
		       a.period_start           AS period_start,
		       (a.period_start + 7)     AS period_end
		FROM active a
		LEFT JOIN event_analysis_digest d
		       ON d.cloud_account_id = a.cloud_account_id
		      AND d.period_start     = a.period_start
		WHERE d.id IS NULL OR d.status IN ($2, $3) OR d.source = $4
		ORDER BY a.period_start, a.cloud_account_id`

	var periods []DigestPeriod
	if err := dbms.Db.SelectContext(ctx.GetContext(), &periods, query, DigestLookbackWeeks, DigestStatusFailed, DigestStatusPartial, DigestSourceOnDemand); err != nil {
		return nil, fmt.Errorf("FindPendingDigestPeriods: query: %w", err)
	}
	return periods, nil
}

// GetDigestMetrics computes the headline counters for one (account, week).
//
// Events are de-duplicated before the join: event_log_analysis holds ~4 rows per
// event (one per analysis_type), so joining event_duplicates straight onto it
// multiplies the new/recurring counts by the number of analysis types.
func GetDigestMetrics(ctx *security.RequestContext, p DigestPeriod) (DigestMetrics, error) {
	var m DigestMetrics
	dbms, err := common.GetDatabaseManager(common.Metastore)
	if err != nil {
		return m, fmt.Errorf("GetDigestMetrics: db manager: %w", err)
	}

	query := `
		WITH ana AS (
			SELECT event_id, event_aggregation_key, status
			FROM event_log_analysis
			WHERE cloud_account_id = $1::uuid
			  AND recorded_at >= $2 AND recorded_at < $3
		),
		ev AS (
			SELECT DISTINCT event_id, event_aggregation_key FROM ana
		),
		-- Noise is anything triage already decided is not a real incident: an
		-- active suppression rule matched it, or it was classified duplicate /
		-- false-positive. Counting these as signal lets a burst of synthetic
		-- test alerts dominate P1 share and the top-pattern list.
		noise AS (
			SELECT ev.event_id FROM ev
			JOIN event_triage_rule_matches m ON m.event_id = ev.event_id AND m.action = 'suppress'
			UNION
			SELECT ev.event_id FROM ev
			JOIN event_classification c ON c.event_id = ev.event_id
			WHERE c.classification IN ('duplicate', 'false_positive')
		),
		sig AS (
			SELECT ev.* FROM ev WHERE ev.event_id NOT IN (SELECT event_id FROM noise)
		)
		SELECT
			(SELECT count(*) FROM ana WHERE status = 'COMPLETED')          AS analyses,
			(SELECT count(*) FROM ana WHERE status = 'FAILED')             AS failed,
			(SELECT count(*) FROM ev)                                      AS events_analysed,
			(SELECT count(*) FROM noise)                                   AS noise_events,
			COALESCE(round(100.0 * (SELECT count(*) FROM noise)
				/ NULLIF((SELECT count(*) FROM ev), 0)), 0)                AS noise_pct,
			count(DISTINCT sig.event_aggregation_key)                      AS failure_classes,
			count(*) FILTER (WHERE d.occurrence_number = 1)                AS new_incidents,
			count(*) FILTER (WHERE d.occurrence_number > 1)                AS recurring,
			count(DISTINCT e.service_key)                                  AS services,
			COALESCE(round(100.0 * count(*) FILTER (WHERE e.priority IN ('HIGH','CRITICAL'))
				/ NULLIF(count(*), 0)), 0)                                 AS p1_pct
		FROM sig
		LEFT JOIN event_duplicates d ON d.event_id = sig.event_id
		LEFT JOIN events           e ON e.id       = sig.event_id`

	if err := dbms.Db.GetContext(ctx.GetContext(), &m, query, p.CloudAccountID, p.PeriodStart, p.PeriodEnd); err != nil {
		return m, fmt.Errorf("GetDigestMetrics: query: %w", err)
	}
	return m, nil
}

// GetDigestClasses returns the period's failure classes ranked by volume, with
// the owning team resolved through ownership_rules where one matches.
//
// limit caps the map stage: one LLM call is made per class returned, so the tail
// of single-event classes is intentionally dropped rather than summarised.
func GetDigestClasses(ctx *security.RequestContext, p DigestPeriod, limit int) ([]DigestClass, error) {
	dbms, err := common.GetDatabaseManager(common.Metastore)
	if err != nil {
		return nil, fmt.Errorf("GetDigestClasses: db manager: %w", err)
	}

	query := `
		WITH ev AS (
			SELECT DISTINCT event_id, event_aggregation_key
			FROM event_log_analysis
			WHERE cloud_account_id = $1::uuid
			  AND recorded_at >= $2 AND recorded_at < $3
		),
		-- Same signal/noise split as GetDigestMetrics: a suppressed or
		-- duplicate-classified event must not surface as a failure pattern, or
		-- the map stage spends an LLM call summarising test traffic.
		noise AS (
			SELECT ev.event_id FROM ev
			JOIN event_triage_rule_matches m ON m.event_id = ev.event_id AND m.action = 'suppress'
			UNION
			SELECT ev.event_id FROM ev
			JOIN event_classification c ON c.event_id = ev.event_id
			WHERE c.classification IN ('duplicate', 'false_positive')
		),
		sig AS (
			SELECT ev.* FROM ev WHERE ev.event_id NOT IN (SELECT event_id FROM noise)
		)
		-- count(DISTINCT sig.event_id), not count(*): the ownership_rules join is
		-- one-to-many when two enabled rules match the same namespace, which would
		-- otherwise multiply this class's event and new-incident counts.
		SELECT sig.event_aggregation_key                         AS aggregation_key,
		       count(DISTINCT sig.event_id)                      AS events,
		       count(DISTINCT sig.event_id)
		           FILTER (WHERE d.occurrence_number = 1)        AS new_incidents,
		       COALESCE(max(d.occurrence_number), 0)             AS worst_recurrence,
		       count(DISTINCT e.service_key)                     AS services,
		       -- Severity order, not max(): priorities are strings, and
		       -- lexicographically 'MEDIUM' > 'HIGH' > 'CRITICAL', so max() would
		       -- label a class carrying CRITICAL events as MEDIUM.
		       COALESCE((array_agg(e.priority ORDER BY CASE e.priority
		           WHEN 'CRITICAL' THEN 1
		           WHEN 'HIGH'     THEN 2
		           WHEN 'MEDIUM'   THEN 3
		           WHEN 'LOW'      THEN 4
		           WHEN 'INFO'     THEN 5
		           WHEN 'DEBUG'    THEN 6
		           ELSE 7 END))[1], '')                            AS priority,
		       COALESCE(max(g.name), '')                         AS owner,
		       COALESCE(max(e.title), '')                        AS title,
		       COALESCE(max(e.source), '')                       AS source,
		       count(DISTINCT e.created_at::date)                AS active_days,
		       COALESCE(round(EXTRACT(EPOCH FROM (max(e.created_at) - min(e.created_at)))
		           / 3600.0, 1), 0)                              AS span_hours
		FROM sig
		LEFT JOIN event_duplicates d ON d.event_id = sig.event_id
		LEFT JOIN events           e ON e.id       = sig.event_id
		LEFT JOIN ownership_rules  o
		       ON o.enabled
		      AND o.cloud_account_id = $1::uuid
		      AND o.owner_type       = 'group'
		      AND o.match_scope      = 'namespace'
		      AND o.match_value      = e.subject_namespace
		LEFT JOIN user_groups      g ON g.id = o.owner_id
		GROUP BY 1
		ORDER BY events DESC, aggregation_key
		LIMIT $4`

	var classes []DigestClass
	if err := dbms.Db.SelectContext(ctx.GetContext(), &classes, query, p.CloudAccountID, p.PeriodStart, p.PeriodEnd, limit); err != nil {
		return nil, fmt.Errorf("GetDigestClasses: query: %w", err)
	}
	return classes, nil
}

// GetClassAnalysisText returns the analysis prose for one failure class, newest
// first. This is the map stage's input — the code-level detail (file, symbol,
// config key) lives in these bodies, not in any structured column.
//
// log_analysis carries the deepest text (~5.5k chars) and summary the shortest,
// so the two are concatenated per row and the caller bounds the total.
func GetClassAnalysisText(ctx *security.RequestContext, p DigestPeriod, aggregationKey string, limit int) ([]string, error) {
	dbms, err := common.GetDatabaseManager(common.Metastore)
	if err != nil {
		return nil, fmt.Errorf("GetClassAnalysisText: db manager: %w", err)
	}

	query := `
		SELECT trim(both E'\n' FROM
		           COALESCE(analysis, '') || E'\n' || COALESCE(summary, '')) AS body
		FROM event_log_analysis
		WHERE cloud_account_id       = $1::uuid
		  AND recorded_at           >= $2 AND recorded_at < $3
		  AND event_aggregation_key  = $4
		  AND status                 = 'COMPLETED'
		  AND analysis_type IN ('log_analysis', 'rca_analysis', 'investigation', 'detailed_response')
		  AND COALESCE(analysis, summary, '') <> ''
		ORDER BY recorded_at DESC
		LIMIT $5`

	var bodies []string
	if err := dbms.Db.SelectContext(ctx.GetContext(), &bodies, query, p.CloudAccountID, p.PeriodStart, p.PeriodEnd, aggregationKey, limit); err != nil {
		return nil, fmt.Errorf("GetClassAnalysisText: query: %w", err)
	}
	return bodies, nil
}

// CountLearningsCaptured returns how many collective memories this period's
// event analyses produced.
//
// The link is derivable rather than stored: a memory carries its source
// conversation on metadata._conversation_id, and an event-analysis conversation
// carries the event fingerprint in its session_id. No column on the memory row
// duplicates it.
func CountLearningsCaptured(ctx *security.RequestContext, p DigestPeriod) (int, error) {
	dbms, err := common.GetDatabaseManager(common.Metastore)
	if err != nil {
		return 0, fmt.Errorf("CountLearningsCaptured: db manager: %w", err)
	}

	query := `
		SELECT count(*)
		FROM llm_memory_collective m
		JOIN llm_conversations c
		  ON c.id::text = m.metadata->>'_conversation_id'
		WHERE m.created_at >= $1 AND m.created_at < $2
		  AND c.account_id = $3::uuid
		  AND (c.session_id LIKE $4 || '%' OR c.session_id LIKE $5 || '%')`

	var n int
	if err := dbms.Db.GetContext(ctx.GetContext(), &n, query, p.PeriodStart, p.PeriodEnd, p.CloudAccountID,
		SessionIdPrefixEvent, SessionIdPrefixEventRCA); err != nil {
		return 0, fmt.Errorf("CountLearningsCaptured: query: %w", err)
	}
	return n, nil
}

// UpsertDigest writes the digest for one (account, week), replacing any prior
// attempt. Keyed on the same (cloud_account_id, period_start) the gap scan uses,
// so a retry after a failed run overwrites rather than duplicating.
func UpsertDigest(
	ctx *security.RequestContext,
	p DigestPeriod,
	metrics DigestMetrics,
	classes []DigestClass,
	classSummaries any,
	summary string,
	status string,
	errMessage string,
	source string,
) error {
	dbms, err := common.GetDatabaseManager(common.Metastore)
	if err != nil {
		return fmt.Errorf("UpsertDigest: db manager: %w", err)
	}

	metricsJSON, err := json.Marshal(metrics)
	if err != nil {
		return fmt.Errorf("UpsertDigest: marshal metrics: %w", err)
	}
	// Both array columns are checked on the *marshalled output* rather than the
	// input. A nil slice yields the JSON scalar `null`, which satisfies NOT NULL
	// but is not an array, so any later jsonb_array_elements over the column
	// errors on that row. Checking the output covers every route to `null` with
	// one rule: an untyped nil (storeDigestFailure), a nil slice returned by sqlx
	// for an empty result set, and a typed nil hidden inside the `any` parameter.
	classesJSON, err := json.Marshal(classes)
	if err != nil {
		return fmt.Errorf("UpsertDigest: marshal top_classes: %w", err)
	}
	if string(classesJSON) == "null" {
		classesJSON = []byte("[]")
	}
	summariesJSON, err := json.Marshal(classSummaries)
	if err != nil {
		return fmt.Errorf("UpsertDigest: marshal class_summaries: %w", err)
	}
	if string(summariesJSON) == "null" {
		summariesJSON = []byte("[]")
	}

	query := `
		INSERT INTO event_analysis_digest (
			tenant_id, cloud_account_id, period_start, period_end,
			metrics, top_classes, class_summaries, summary, status, error_message, source
		) VALUES ($1::uuid, $2::uuid, $3, $4, $5::jsonb, $6::jsonb, $7::jsonb, NULLIF($8,''), $9, NULLIF($10,''), $11)
		ON CONFLICT (cloud_account_id, period_start) DO UPDATE SET
			metrics         = EXCLUDED.metrics,
			top_classes     = EXCLUDED.top_classes,
			class_summaries = EXCLUDED.class_summaries,
			summary         = EXCLUDED.summary,
			status          = EXCLUDED.status,
			error_message   = EXCLUDED.error_message,
			source          = EXCLUDED.source,
			generated_at    = now(),
			updated_at      = now()`

	if _, err := dbms.Db.ExecContext(ctx.GetContext(), query,
		p.TenantID, p.CloudAccountID, p.PeriodStart, p.PeriodEnd,
		metricsJSON, classesJSON, summariesJSON, summary, status, errMessage, source,
	); err != nil {
		return fmt.Errorf("UpsertDigest: exec: %w", err)
	}
	return nil
}

// Digest is a stored digest row as served to the UI.
type Digest struct {
	ID             string    `db:"id"              json:"id"`
	TenantID       string    `db:"tenant_id"       json:"tenant_id"`
	CloudAccountID string    `db:"cloud_account_id" json:"cloud_account_id"`
	PeriodStart    time.Time `db:"period_start"    json:"period_start"`
	PeriodEnd      time.Time `db:"period_end"      json:"period_end"`
	// json.RawMessage, not []byte: encoding/json base64-encodes a []byte field,
	// which would ship these jsonb columns to the UI as opaque strings instead of
	// the objects they are.
	Metrics        json.RawMessage `db:"metrics"         json:"metrics"`
	TopClasses     json.RawMessage `db:"top_classes"     json:"top_classes"`
	ClassSummaries json.RawMessage `db:"class_summaries" json:"class_summaries"`
	Summary        string          `db:"summary"         json:"summary"`
	Status         string          `db:"status"          json:"status"`
	// Source distinguishes a scheduled row from a provisional on-demand one.
	Source       string    `db:"source"          json:"source"`
	ErrorMessage string    `db:"error_message"   json:"error_message,omitempty"`
	GeneratedAt  time.Time `db:"generated_at"    json:"generated_at"`
}

const digestSelectCols = `id::text, tenant_id::text, cloud_account_id::text,
	period_start, period_end, metrics, top_classes, class_summaries,
	COALESCE(summary, '') AS summary, status, source,
	COALESCE(error_message, '') AS error_message, generated_at`

// GetDigest returns one account-week digest, including its class summaries.
func GetDigest(ctx *security.RequestContext, accountID string, periodStart time.Time) (Digest, error) {
	var d Digest
	dbms, err := common.GetDatabaseManager(common.Metastore)
	if err != nil {
		return d, fmt.Errorf("GetDigest: db manager: %w", err)
	}
	query := `SELECT ` + digestSelectCols + `
		FROM event_analysis_digest
		WHERE cloud_account_id = $1::uuid AND period_start = $2`
	if err := dbms.Db.GetContext(ctx.GetContext(), &d, query, accountID, periodStart); err != nil {
		return d, fmt.Errorf("GetDigest: query: %w", err)
	}
	return d, nil
}

// ListDigests returns an account's digests newest first, for the history tab.
//
// class_summaries is excluded: it is the largest column and the list view only
// renders headline counters and the briefing.
func ListDigests(ctx *security.RequestContext, accountID string, limit int) ([]Digest, error) {
	dbms, err := common.GetDatabaseManager(common.Metastore)
	if err != nil {
		return nil, fmt.Errorf("ListDigests: db manager: %w", err)
	}
	query := `
		SELECT id::text, tenant_id::text, cloud_account_id::text,
		       period_start, period_end, metrics, top_classes,
		       '[]'::jsonb AS class_summaries,
		       COALESCE(summary, '') AS summary, status, source,
		       COALESCE(error_message, '') AS error_message, generated_at
		FROM event_analysis_digest
		WHERE cloud_account_id = $1::uuid
		ORDER BY period_start DESC
		LIMIT $2`
	var out []Digest
	if err := dbms.Db.SelectContext(ctx.GetContext(), &out, query, accountID, limit); err != nil {
		return nil, fmt.Errorf("ListDigests: query: %w", err)
	}
	return out, nil
}
