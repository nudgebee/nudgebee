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

// DigestPeriod identifies one (tenant, week) digest slot.
type DigestPeriod struct {
	TenantID    string    `db:"tenant_id"`
	PeriodStart time.Time `db:"period_start"`
	PeriodEnd   time.Time `db:"period_end"`
}

// syntheticKeyPattern marks a failure class as test / rule-exercising traffic.
//
// Matched against the aggregation key ONLY, never the event title. Titles embed
// resource paths, and a namespace carrying a non-production suffix appears
// verbatim in the titles of real incidents in it — matching on titles flagged
// nine genuine classes as synthetic over 60 days of dev data. The key is
// operator-chosen and carries no resource path, so it matches only the alerts
// that announce themselves.
//
// Duration is deliberately not a signal. The previous heuristic treated traffic
// confined to a single day as synthetic, which flagged a real RabbitMQ queue
// backlog as noise — genuine incidents are usually single-day too.
const syntheticKeyPattern = `(test|dummy|ignore|synthetic|placeholder|sanity[ _-]?check)`

// analysisStages are the pipeline stages an event passes through. An event is
// "complete" only when all four recorded a COMPLETED row — counting rows instead
// reports ~4x the event volume, since every event yields one row per stage.
const analysisStages = `('summary','investigation','log_analysis','detailed_response')`

// DigestMetrics is the headline counter set rendered as chips in the UI.
type DigestMetrics struct {
	// Pipeline health: did the analysis actually run, and finish?
	EventsAnalysed int `db:"events_analysed" json:"events_analysed"`
	EventsComplete int `db:"events_complete" json:"events_complete"`
	CompletionPct  int `db:"completion_pct"  json:"completion_pct"`
	FailedEvents   int `db:"failed_events"   json:"failed_events"`

	// RCA coverage, reported separately because rca_analysis is not one of the
	// four stages "fully analysed" counts. It runs on a small fraction of events
	// — 10 of ~600 over 60 days on dev — so folding it into the completion rate
	// would report ~2% complete and hide the four stages that do run. Surfaced so
	// a reader can see the deep-analysis stage is barely firing.
	RcaEvents int `db:"rca_events" json:"rca_events"`
	RcaPct    int `db:"rca_pct"    json:"rca_pct"`

	// Signal and noise. Noise is what this account should stop paying to analyse:
	// operator-suppressed alerts plus test traffic. Recurrences are NOT noise —
	// folding them in here is what previously made the recurrence counter unusable.
	RealEvents       int `db:"real_events"       json:"real_events"`
	SyntheticEvents  int `db:"synthetic_events"  json:"synthetic_events"`
	SuppressedEvents int `db:"suppressed_events" json:"suppressed_events"`
	NoisePct         int `db:"noise_pct"         json:"noise_pct"`

	// Recurrence, measured over real events only.
	NewIncidents  int `db:"new_incidents"  json:"new_incidents"`
	Recurrences   int `db:"recurrences"    json:"recurrences"`
	RecurrencePct int `db:"recurrence_pct" json:"recurrence_pct"`

	// Shape of the week.
	FailureClasses int `db:"failure_classes" json:"failure_classes"`
	Services       int `db:"services"        json:"services"`
	P1Pct          int `db:"p1_pct"          json:"p1_pct"`

	// NoiseClasses names every class excluded as noise, with its volume. Filled by
	// a second query. Exposed rather than merely counted so a class wrongly matched
	// by syntheticKeyPattern is visible in the digest instead of silently dropped.
	NoiseClasses []NoiseClass `db:"-" json:"noise_classes,omitempty"`

	// Learnings is filled separately — it comes from the memory layer, not events,
	// so it has no column in the metrics query.
	Learnings int `db:"-" json:"learnings"`
}

// NoiseClass is one excluded class and why it was excluded.
type NoiseClass struct {
	AggregationKey string `db:"aggregation_key"  json:"aggregation_key"`
	AccountID      string `db:"cloud_account_id" json:"cloud_account_id"`
	AccountName    string `db:"account_name"     json:"account_name,omitempty"`
	Events         int    `db:"events"          json:"events"`
	Reason         string `db:"reason"          json:"reason"`
	Pct            int    `db:"pct"             json:"pct"`
}

// DigestClass is one failure class within the period, used both as the map-stage
// input and as the stored top_classes rollup.
type DigestClass struct {
	AggregationKey string `json:"aggregation_key" db:"aggregation_key"`
	// AccountID and AccountName make every class answer "which account is this?".
	// AccountName also carries the environment — k8s-prod, dev-aws — far more
	// reliably than cloud_accounts.account_env, where 497 of 513 accounts sit on
	// a default of non_prod and a dozen contradict it via account_purpose.
	AccountID    string `json:"cloud_account_id" db:"cloud_account_id"`
	AccountName  string `json:"account_name"     db:"account_name"`
	Events       int    `json:"events" db:"events"`
	NewIncidents int    `json:"new_incidents" db:"new_incidents"`
	// Recurrences counts this class's events that repeat a known fingerprint, and
	// WorstRecurrence is the highest occurrence number reached. Both were
	// unreachable while duplicate-classified events were filtered out as noise.
	Recurrences     int    `json:"recurrences" db:"recurrences"`
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
	// ActiveDays and SpanHours describe the shape of the traffic — a burst versus
	// a class recurring across days.
	ActiveDays int     `json:"active_days" db:"active_days"`
	SpanHours  float64 `json:"span_hours"  db:"span_hours"`
	// Namespaces and Clusters are the map stage's only evidence for which
	// environment a class belongs to. cloud_accounts.account_env cannot answer it —
	// 497 of 513 accounts sit on the default `non_prod`, and a dozen contradict
	// that via account_purpose — and one account routinely spans prod and demo
	// namespaces, so an account-level field would be too coarse even if correct.
	Namespaces string `json:"namespaces,omitempty" db:"namespaces"`
	Clusters   string `json:"clusters,omitempty"   db:"clusters"`
}

// digestMetricsShapeKey is a counter only the current metrics shape writes.
//
// A row missing it was generated before the counters were reworked and carries
// keys the UI no longer reads, so it would render as zeros. Treating it as
// pending makes the scheduler reissue it — the same convergent mechanism that
// recovers a missed tick, reused so no backfill script or migration is needed.
const digestMetricsShapeKey = "events_complete"

// digestFindingShapeKey is the newest field the map stage writes.
//
// Tracks the latest addition rather than the first: a row carrying `label`
// necessarily carries every earlier field too, since they come from one prompt
// version. Pointing it at the newest field is what makes a prompt change
// self-heal instead of needing a manual delete.
const digestFindingShapeKey = "label"

// FindPendingDigestPeriods returns the (tenant, week) slots inside the lookback
// window that still need a scheduled run: no row yet, a failed or partial
// attempt, a provisional on-demand row awaiting its scheduled replacement, or a
// row still carrying a superseded metrics, briefing or finding shape.
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
			-- A tenant-week is a candidate when any of its accounts saw analyses.
			SELECT DISTINCT ca.tenant, w.period_start
			FROM weeks w
			JOIN event_log_analysis ela
			  ON ela.recorded_at >= w.period_start
			 AND ela.recorded_at <  w.period_start + 7
			JOIN cloud_accounts ca ON ca.id = ela.cloud_account_id
		)
		SELECT a.tenant::text      AS tenant_id,
		       a.period_start      AS period_start,
		       (a.period_start + 7) AS period_end
		FROM active a
		LEFT JOIN event_analysis_digest d
		       ON d.tenant_id    = a.tenant
		      AND d.period_start = a.period_start
		WHERE d.id IS NULL OR d.status IN ($2, $3) OR d.source = $4
		   -- jsonb_exists(), not the ? operator: ? is also a placeholder marker to
		   -- several drivers, and the operator form is silently mis-parsed.
		   OR NOT jsonb_exists(d.metrics, $5)
		   -- A generated row with no structured briefing predates the structured
		   -- reduce stage and would render as an empty review.
		   OR (d.status = $6 AND d.briefing IS NULL)
		   -- Findings written before the map stage returned structure carry a
		   -- markdown string instead of typed fields, so the UI has no headline
		   -- to show and no verdicts to read.
		   OR (d.status = $6
		       AND jsonb_array_length(d.class_summaries) > 0
		       AND NOT jsonb_exists(d.class_summaries->0, $7))
		ORDER BY a.period_start, a.tenant`

	var periods []DigestPeriod
	if err := dbms.Db.SelectContext(ctx.GetContext(), &periods, query, DigestLookbackWeeks,
		DigestStatusFailed, DigestStatusPartial, DigestSourceOnDemand, digestMetricsShapeKey,
		DigestStatusGenerated, digestFindingShapeKey); err != nil {
		return nil, fmt.Errorf("FindPendingDigestPeriods: query: %w", err)
	}
	return periods, nil
}

// digestEventCTE resolves a tenant-week to its distinct, deduplicated events.
//
// Two collapses, both required, both previously done ad hoc per query:
//
//  1. event_log_analysis holds one row per analysis stage, so a raw count reports
//     roughly four times the event volume.
//  2. A webhook fan-out delivers the same alert once per affected subject, which
//     lands as several event rows sharing an account, title, subject and second —
//     13 copies of one RabbitMQ alert in a single second on dev. Those are one
//     incident recorded repeatedly, not a recurrence, and they inflate every
//     count derived from them.
//
// The title fallback to event_id matters: DISTINCT ON treats NULLs as equal, so
// without it two unrelated untitled events in the same account and second would
// collapse into one.
//
// $1 is the tenant, $2/$3 the period bounds. Every query that counts anything
// starts from this CTE — a dedupe applied in the metrics query but not the class
// query would make the scoreboard disagree with the incidents beneath it.
const digestEventCTE = `
	ana AS (
		SELECT DISTINCT ela.event_id, ela.event_aggregation_key, ela.cloud_account_id
		FROM event_log_analysis ela
		JOIN cloud_accounts ca ON ca.id = ela.cloud_account_id
		WHERE ca.tenant = $1::uuid
		  AND ela.recorded_at >= $2 AND ela.recorded_at < $3
	),
	ev AS (
		SELECT DISTINCT ON (
			a.cloud_account_id,
			COALESCE(NULLIF(e.title, ''), a.event_id::text),
			COALESCE(e.subject_name, ''),
			e.created_at
		)
			a.event_id, a.event_aggregation_key, a.cloud_account_id
		FROM ana a
		JOIN events e ON e.id = a.event_id
		ORDER BY a.cloud_account_id,
		         COALESCE(NULLIF(e.title, ''), a.event_id::text),
		         COALESCE(e.subject_name, ''),
		         e.created_at,
		         a.event_id
	)`

// digestNoiseCTE is the signal/noise split shared by the metrics and class
// queries, so both agree on what is excluded.
//
// Noise is suppressed-by-rule plus test traffic. Duplicate-classified events are
// deliberately NOT noise: a duplicate is a recurrence, and recurrence is the
// signal the digest exists to surface. Folding it in here meant 137 of 141
// recurrences were removed before being counted, so every digest reported zero.
const digestNoiseCTE = `
	synth AS (
		SELECT event_id FROM ev WHERE event_aggregation_key ~* '` + syntheticKeyPattern + `'
	),
	suppressed AS (
		SELECT ev.event_id FROM ev
		JOIN event_triage_rule_matches m ON m.event_id = ev.event_id AND m.action = 'suppress'
	),
	noise AS (
		SELECT event_id FROM synth
		UNION
		SELECT event_id FROM suppressed
	),
	sig AS (
		SELECT ev.* FROM ev WHERE ev.event_id NOT IN (SELECT event_id FROM noise)
	)`

// GetDigestMetrics computes the headline counters for one tenant-week.
func GetDigestMetrics(ctx *security.RequestContext, p DigestPeriod) (DigestMetrics, error) {
	var m DigestMetrics
	dbms, err := common.GetDatabaseManager(common.Metastore)
	if err != nil {
		return m, fmt.Errorf("GetDigestMetrics: db manager: %w", err)
	}

	query := `
		WITH ` + digestEventCTE + `,
		-- One row per event: how many of the four stages completed. This is what
		-- "fully analysed" means; counting ana rows reports stage-writes, not events.
		stages AS (
			SELECT ela.event_id,
			       count(DISTINCT ela.analysis_type) FILTER (
			           WHERE ela.status = 'COMPLETED' AND ela.analysis_type IN ` + analysisStages + `
			       ) AS done
			FROM event_log_analysis ela
			JOIN ev ON ev.event_id = ela.event_id
			GROUP BY ela.event_id
		),
		failed AS (
			SELECT DISTINCT ela.event_id
			FROM event_log_analysis ela
			JOIN ev ON ev.event_id = ela.event_id
			WHERE ela.status = 'FAILED'
		),
		rca AS (
			SELECT DISTINCT ela.event_id
			FROM event_log_analysis ela
			JOIN ev ON ev.event_id = ela.event_id
			WHERE ela.analysis_type = 'rca_analysis' AND ela.status = 'COMPLETED'
		),` + digestNoiseCTE + `
		SELECT
			(SELECT count(*) FROM ev)                                      AS events_analysed,
			(SELECT count(*) FROM stages WHERE done = 4)                   AS events_complete,
			COALESCE(round(100.0 * (SELECT count(*) FROM stages WHERE done = 4)
				/ NULLIF((SELECT count(*) FROM ev), 0))::int, 0)           AS completion_pct,
			(SELECT count(*) FROM failed)                                  AS failed_events,
			(SELECT count(*) FROM rca)                                     AS rca_events,
			COALESCE(round(100.0 * (SELECT count(*) FROM rca)
				/ NULLIF((SELECT count(*) FROM ev), 0))::int, 0)           AS rca_pct,
			(SELECT count(*) FROM sig)                                     AS real_events,
			(SELECT count(*) FROM synth)                                   AS synthetic_events,
			(SELECT count(*) FROM suppressed)                              AS suppressed_events,
			COALESCE(round(100.0 * (SELECT count(*) FROM noise)
				/ NULLIF((SELECT count(*) FROM ev), 0))::int, 0)           AS noise_pct,
			count(*) FILTER (WHERE d.occurrence_number = 1)                AS new_incidents,
			count(*) FILTER (WHERE d.occurrence_number > 1)                AS recurrences,
			COALESCE(round(100.0 * count(*) FILTER (WHERE d.occurrence_number > 1)
				/ NULLIF(count(*), 0))::int, 0)                            AS recurrence_pct,
			-- Distinct on (account, key): the same key in two accounts is two
			-- classes, because their recurrence sequences are independent.
			count(DISTINCT (sig.cloud_account_id, sig.event_aggregation_key)) AS failure_classes,
			count(DISTINCT e.service_key)                                  AS services,
			COALESCE(round(100.0 * count(*) FILTER (WHERE e.priority IN ('HIGH','CRITICAL'))
				/ NULLIF(count(*), 0))::int, 0)                            AS p1_pct
		FROM sig
		LEFT JOIN event_duplicates d ON d.event_id = sig.event_id
		LEFT JOIN events           e ON e.id       = sig.event_id`

	if err := dbms.Db.GetContext(ctx.GetContext(), &m, query, p.TenantID, p.PeriodStart, p.PeriodEnd); err != nil {
		return m, fmt.Errorf("GetDigestMetrics: query: %w", err)
	}

	noise, err := getNoiseClasses(ctx, p)
	if err != nil {
		return m, err
	}
	m.NoiseClasses = noise
	return m, nil
}

// getNoiseClasses lists the classes excluded as noise, biggest first.
//
// Named rather than merely counted: syntheticKeyPattern is a heuristic, and a
// class it matches wrongly would otherwise vanish from the digest with no trace.
// Listing it lets a reader spot the mistake.
func getNoiseClasses(ctx *security.RequestContext, p DigestPeriod) ([]NoiseClass, error) {
	dbms, err := common.GetDatabaseManager(common.Metastore)
	if err != nil {
		return nil, fmt.Errorf("getNoiseClasses: db manager: %w", err)
	}

	query := `
		WITH ` + digestEventCTE + `,` + digestNoiseCTE + `
		SELECT ev.event_aggregation_key       AS aggregation_key,
		       ev.cloud_account_id::text      AS cloud_account_id,
		       -- Fall back to the id: an unnamed account would otherwise render as
		       -- "some_key in  — 10 events", naming nothing at all.
		       COALESCE(NULLIF(ca.account_name, ''), ev.cloud_account_id::text) AS account_name,
		       count(*)                       AS events,
		       CASE WHEN bool_or(s.event_id IS NOT NULL) THEN 'test traffic'
		            ELSE 'suppressed by rule' END AS reason,
		       COALESCE(round(100.0 * count(*)
		           / NULLIF((SELECT count(*) FROM ev), 0))::int, 0) AS pct
		FROM ev
		JOIN noise n ON n.event_id = ev.event_id
		LEFT JOIN synth s ON s.event_id = ev.event_id
		LEFT JOIN cloud_accounts ca ON ca.id = ev.cloud_account_id
		GROUP BY ev.event_aggregation_key, ev.cloud_account_id, ca.account_name
		ORDER BY events DESC, aggregation_key`

	var classes []NoiseClass
	if err := dbms.Db.SelectContext(ctx.GetContext(), &classes, query,
		p.TenantID, p.PeriodStart, p.PeriodEnd); err != nil {
		return nil, fmt.Errorf("getNoiseClasses: query: %w", err)
	}
	return classes, nil
}

// GetDigestClasses returns the period's failure classes across every account in
// the tenant, ranked by volume, with the owning team resolved through
// ownership_rules where one matches.
//
// Grouped by (account, aggregation key), never the key alone. The same key in
// two accounts is two different incidents with independent recurrence
// sequences — event_duplicates.occurrence_number restarts at 1 per account, so
// merging them would splice unrelated histories together. On dev, 13 keys
// carrying 41% of event volume appear in more than one account.
//
// Unbounded: every real class is returned and summarised. Ranking and dropping
// here removed whole accounts from the review, because the busiest account's
// classes filled any volume-ordered cut.
func GetDigestClasses(ctx *security.RequestContext, p DigestPeriod) ([]DigestClass, error) {
	dbms, err := common.GetDatabaseManager(common.Metastore)
	if err != nil {
		return nil, fmt.Errorf("GetDigestClasses: db manager: %w", err)
	}

	query := `
		WITH ` + digestEventCTE + `,` + digestNoiseCTE + `
		-- count(DISTINCT sig.event_id), not count(*): the ownership_rules join is
		-- one-to-many when two enabled rules match the same namespace, which would
		-- otherwise multiply this class's event and new-incident counts.
		SELECT sig.event_aggregation_key                         AS aggregation_key,
		       sig.cloud_account_id::text                        AS cloud_account_id,
		       COALESCE(NULLIF(max(ca.account_name), ''), sig.cloud_account_id::text) AS account_name,
		       count(DISTINCT sig.event_id)                      AS events,
		       count(DISTINCT sig.event_id)
		           FILTER (WHERE d.occurrence_number = 1)        AS new_incidents,
		       count(DISTINCT sig.event_id)
		           FILTER (WHERE d.occurrence_number > 1)        AS recurrences,
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
		       COALESCE(string_agg(DISTINCT NULLIF(e.subject_namespace, ''), ', '), '') AS namespaces,
		       COALESCE(string_agg(DISTINCT NULLIF(e.cluster, ''), ', '), '')           AS clusters,
		       count(DISTINCT e.created_at::date)                AS active_days,
		       COALESCE(round(EXTRACT(EPOCH FROM (max(e.created_at) - min(e.created_at)))
		           / 3600.0, 1), 0)                              AS span_hours
		FROM sig
		LEFT JOIN event_duplicates d ON d.event_id = sig.event_id
		LEFT JOIN events           e ON e.id       = sig.event_id
		LEFT JOIN cloud_accounts   ca ON ca.id     = sig.cloud_account_id
		LEFT JOIN ownership_rules  o
		       ON o.enabled
		      AND o.cloud_account_id = sig.cloud_account_id
		      AND o.owner_type       = 'group'
		      AND o.match_scope      = 'namespace'
		      AND o.match_value      = e.subject_namespace
		LEFT JOIN user_groups      g ON g.id = o.owner_id
		GROUP BY sig.event_aggregation_key, sig.cloud_account_id
		ORDER BY events DESC, aggregation_key`

	var classes []DigestClass
	if err := dbms.Db.SelectContext(ctx.GetContext(), &classes, query,
		p.TenantID, p.PeriodStart, p.PeriodEnd); err != nil {
		return nil, fmt.Errorf("GetDigestClasses: query: %w", err)
	}
	return classes, nil
}

// PriorClass is one failure class's history in the weeks before this period.
type PriorClass struct {
	AggregationKey string    `db:"aggregation_key" json:"aggregation_key"`
	AccountID      string    `db:"cloud_account_id" json:"cloud_account_id"`
	AccountName    string    `db:"account_name"    json:"account_name,omitempty"`
	Weeks          int       `db:"weeks"           json:"weeks"`
	Events         int       `db:"events"          json:"events"`
	LastSeen       time.Time `db:"last_seen"       json:"last_seen"`
}

// GetPriorClasses returns what this tenant's earlier digests already reported,
// so the reduce stage can say which classes are carried over and for how long.
//
// Reads the stored top_classes rollup rather than re-querying events: the prior
// weeks were already reduced, and re-deriving them would silently disagree with
// what those digests actually said.
func GetPriorClasses(ctx *security.RequestContext, p DigestPeriod, weeks int) ([]PriorClass, error) {
	dbms, err := common.GetDatabaseManager(common.Metastore)
	if err != nil {
		return nil, fmt.Errorf("GetPriorClasses: db manager: %w", err)
	}

	query := `
		SELECT c->>'aggregation_key'                      AS aggregation_key,
		       COALESCE(c->>'cloud_account_id', '')       AS cloud_account_id,
		       COALESCE(NULLIF(c->>'account_name', ''), c->>'cloud_account_id', '') AS account_name,
		       count(DISTINCT d.period_start)::int        AS weeks,
		       COALESCE(sum((c->>'events')::int), 0)::int AS events,
		       max(d.period_start)                        AS last_seen
		FROM event_analysis_digest d,
		     LATERAL jsonb_array_elements(d.top_classes) c
		WHERE d.tenant_id      = $1::uuid
		  AND d.status         = $4
		  AND d.period_start  <  $2::date
		  -- Cast rather than relying on inference. $2 resolves to a date today
		  -- because its first use compares against a date column, and subtracting
		  -- an integer from a date is valid — but that is a property of predicate
		  -- order, not of the query's intent. Reordering these two lines would
		  -- leave $2 typed from the arithmetic instead, where no
		  -- timestamptz-minus-integer operator exists and the lookback would fail.
		  AND d.period_start  >= $2::date - ($3::int * 7)
		  AND c->>'aggregation_key' IS NOT NULL
		GROUP BY c->>'aggregation_key', c->>'cloud_account_id', c->>'account_name'
		ORDER BY weeks DESC, events DESC`

	var prior []PriorClass
	if err := dbms.Db.SelectContext(ctx.GetContext(), &prior, query,
		p.TenantID, p.PeriodStart, weeks, DigestStatusGenerated); err != nil {
		return nil, fmt.Errorf("GetPriorClasses: query: %w", err)
	}
	return prior, nil
}

// ClassRef identifies one failure class inside one account.
type ClassRef struct {
	AccountID      string `db:"cloud_account_id"`
	AggregationKey string `db:"aggregation_key"`
}

// Key is the identity used to compare classes across weeks. Account first,
// because the same aggregation key in two accounts is two different classes.
func (c ClassRef) Key() string { return c.AccountID + "\x00" + c.AggregationKey }

// GetPeriodClassKeys returns every real (account, class) seen this period, with
// no limit.
//
// Kept separate from GetDigestClasses even though both are now unbounded: this
// one skips the ownership and events joins, so carry-over stays cheap, and the
// two cannot drift into disagreeing about what the period contained.
func GetPeriodClassKeys(ctx *security.RequestContext, p DigestPeriod) ([]ClassRef, error) {
	dbms, err := common.GetDatabaseManager(common.Metastore)
	if err != nil {
		return nil, fmt.Errorf("GetPeriodClassKeys: db manager: %w", err)
	}

	query := `
		WITH ` + digestEventCTE + `,` + digestNoiseCTE + `
		SELECT DISTINCT cloud_account_id::text AS cloud_account_id,
		       event_aggregation_key           AS aggregation_key
		FROM sig`

	var refs []ClassRef
	if err := dbms.Db.SelectContext(ctx.GetContext(), &refs, query,
		p.TenantID, p.PeriodStart, p.PeriodEnd); err != nil {
		return nil, fmt.Errorf("GetPeriodClassKeys: query: %w", err)
	}
	return refs, nil
}

// GetClassAnalysisText returns the analysis prose for one failure class in one
// account, newest first. This is the map stage's input — the code-level detail
// (file, symbol, config key) lives in these bodies, not in any structured column.
func GetClassAnalysisText(ctx *security.RequestContext, p DigestPeriod, accountID, aggregationKey string, limit int) ([]string, error) {
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
	if err := dbms.Db.SelectContext(ctx.GetContext(), &bodies, query,
		accountID, p.PeriodStart, p.PeriodEnd, aggregationKey, limit); err != nil {
		return nil, fmt.Errorf("GetClassAnalysisText: query: %w", err)
	}
	return bodies, nil
}

// CountLearningsCaptured returns how many collective memories this period's
// event analyses produced across the tenant.
//
// The link is derivable rather than stored: a memory carries its source
// conversation on metadata._conversation_id, and an event-analysis conversation
// carries the event fingerprint in its session_id.
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
		JOIN cloud_accounts ca ON ca.id = c.account_id
		WHERE m.created_at >= $1 AND m.created_at < $2
		  AND ca.tenant = $3::uuid
		  AND (c.session_id LIKE $4 || '%' OR c.session_id LIKE $5 || '%')`

	var n int
	if err := dbms.Db.GetContext(ctx.GetContext(), &n, query, p.PeriodStart, p.PeriodEnd, p.TenantID,
		SessionIdPrefixEvent, SessionIdPrefixEventRCA); err != nil {
		return 0, fmt.Errorf("CountLearningsCaptured: query: %w", err)
	}
	return n, nil
}

// UpsertDigest writes the digest for one tenant-week, replacing any prior
// attempt. Keyed on the same (tenant_id, period_start) the gap scan uses, so a
// retry after a failed run overwrites rather than duplicating.
func UpsertDigest(
	ctx *security.RequestContext,
	p DigestPeriod,
	metrics DigestMetrics,
	classes []DigestClass,
	classSummaries any,
	briefing any,
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
	// errors on that row.
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
	// briefing is an object, not an array, and is genuinely absent on a failed
	// week — so `null` is left as SQL NULL rather than coerced. The gap scan
	// treats a NULL briefing as a row still owing a structured reduce.
	briefingJSON, err := json.Marshal(briefing)
	if err != nil {
		return fmt.Errorf("UpsertDigest: marshal briefing: %w", err)
	}

	query := `
		INSERT INTO event_analysis_digest (
			tenant_id, period_start, period_end,
			metrics, top_classes, class_summaries, briefing, summary, status, error_message, source
		) VALUES ($1::uuid, $2, $3, $4::jsonb, $5::jsonb, $6::jsonb,
		          NULLIF($7,'null')::jsonb, NULLIF($8,''), $9, NULLIF($10,''), $11)
		ON CONFLICT (tenant_id, period_start) DO UPDATE SET
			metrics         = EXCLUDED.metrics,
			top_classes     = EXCLUDED.top_classes,
			class_summaries = EXCLUDED.class_summaries,
			briefing        = EXCLUDED.briefing,
			summary         = EXCLUDED.summary,
			status          = EXCLUDED.status,
			error_message   = EXCLUDED.error_message,
			source          = EXCLUDED.source,
			generated_at    = now(),
			updated_at      = now()`

	if _, err := dbms.Db.ExecContext(ctx.GetContext(), query,
		p.TenantID, p.PeriodStart, p.PeriodEnd,
		metricsJSON, classesJSON, summariesJSON, string(briefingJSON),
		summary, status, errMessage, source,
	); err != nil {
		return fmt.Errorf("UpsertDigest: exec: %w", err)
	}
	return nil
}

// FindUndeliveredDigests returns complete tenant-weeks that have not yet been
// pushed to notification channels, oldest first so a tenant that missed two
// weeks receives them in the order they happened.
//
// Delivery is keyed on notified_at rather than on status, because status alone
// cannot distinguish "generated for the first time" from "regenerated". The gap
// scan deliberately re-queues rows whose shape key is superseded, so every
// prompt change rewrites the whole lookback window; keying off status would
// re-send all of it. UpsertDigest never writes notified_at, so a regenerated
// week stays delivered.
//
// Three filters beyond notified_at, each load-bearing:
//   - status = generated — a partial row is still owed a synthesis, and sending
//     it would push a review whose briefing is about to change.
//   - source = scheduled — an on-demand row is a user's provisional preview.
//     Delivering it would both surprise the channel and consume the week's one
//     delivery, so the authoritative scheduled run would then stay silent.
//   - a non-empty class_summaries — an empty week has no findings to report.
//     api-server's daily report skips publishing on an empty payload for the
//     same reason. The row is still marked delivered by the caller so it does
//     not linger in this scan forever.
func FindUndeliveredDigests(ctx *security.RequestContext, limit int) ([]Digest, error) {
	dbms, err := common.GetDatabaseManager(common.Metastore)
	if err != nil {
		return nil, fmt.Errorf("FindUndeliveredDigests: db manager: %w", err)
	}
	// The period_start floor keeps a long outage from replaying ancient weeks
	// into a channel: anything older than the lookback window is history the
	// generator itself will not refill.
	query := `SELECT ` + digestSelectCols + `
		FROM event_analysis_digest
		WHERE notified_at IS NULL
		  AND status = $1
		  AND source = $2
		  AND period_start >= (CURRENT_DATE - ($3::int * 7))
		ORDER BY period_start ASC
		LIMIT $4`
	out := []Digest{}
	if err := dbms.Db.SelectContext(ctx.GetContext(), &out, query,
		DigestStatusGenerated, DigestSourceScheduled, DigestLookbackWeeks, limit); err != nil {
		return nil, fmt.Errorf("FindUndeliveredDigests: query: %w", err)
	}
	return out, nil
}

// MarkDigestDelivered stamps a digest as pushed to notification channels.
//
// Called after a successful publish, and also for a week deliberately not sent
// (no findings) so it stops appearing in the scan. The WHERE clause re-checks
// notified_at so two publishers racing on the same row produce one delivery
// rather than two: the loser updates zero rows.
func MarkDigestDelivered(ctx *security.RequestContext, id string) (bool, error) {
	dbms, err := common.GetDatabaseManager(common.Metastore)
	if err != nil {
		return false, fmt.Errorf("MarkDigestDelivered: db manager: %w", err)
	}
	res, err := dbms.Db.ExecContext(ctx.GetContext(),
		`UPDATE event_analysis_digest SET notified_at = now(), updated_at = now()
		 WHERE id = $1::uuid AND notified_at IS NULL`, id)
	if err != nil {
		return false, fmt.Errorf("MarkDigestDelivered: exec: %w", err)
	}
	rows, err := res.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("MarkDigestDelivered: rows affected: %w", err)
	}
	return rows > 0, nil
}

// ReleaseDigestDelivery undoes MarkDigestDelivered so a failed publish is
// retried on the next tick.
//
// The claim is taken before publishing, because a crash between a successful
// publish and its mark would otherwise re-send the week. That ordering alone
// would trade one rare duplicate for a permanent silent loss whenever the
// exchange is unreachable — and an unreachable exchange fails every tenant at
// once, not one. Releasing the claim on a failed publish keeps both properties:
// no duplicate from a crash, no lost week from an outage.
func ReleaseDigestDelivery(ctx *security.RequestContext, id string) error {
	dbms, err := common.GetDatabaseManager(common.Metastore)
	if err != nil {
		return fmt.Errorf("ReleaseDigestDelivery: db manager: %w", err)
	}
	if _, err := dbms.Db.ExecContext(ctx.GetContext(),
		`UPDATE event_analysis_digest SET notified_at = NULL, updated_at = now()
		 WHERE id = $1::uuid`, id); err != nil {
		return fmt.Errorf("ReleaseDigestDelivery: exec: %w", err)
	}
	return nil
}

// Digest is a stored digest row as served to the UI.
type Digest struct {
	ID          string    `db:"id"              json:"id"`
	TenantID    string    `db:"tenant_id"       json:"tenant_id"`
	PeriodStart time.Time `db:"period_start" json:"period_start"`
	PeriodEnd   time.Time `db:"period_end"      json:"period_end"`
	// json.RawMessage, not []byte: encoding/json base64-encodes a []byte field,
	// which would ship these jsonb columns to the UI as opaque strings instead of
	// the objects they are.
	Metrics        json.RawMessage `db:"metrics"         json:"metrics"`
	TopClasses     json.RawMessage `db:"top_classes"     json:"top_classes"`
	ClassSummaries json.RawMessage `db:"class_summaries" json:"class_summaries"`
	Briefing       json.RawMessage `db:"briefing"        json:"briefing"`
	Summary        string          `db:"summary"         json:"summary"`
	Status         string          `db:"status"          json:"status"`
	// Source distinguishes a scheduled row from a provisional on-demand one.
	Source       string    `db:"source"          json:"source"`
	ErrorMessage string    `db:"error_message"   json:"error_message,omitempty"`
	GeneratedAt  time.Time `db:"generated_at"    json:"generated_at"`
}

const digestSelectCols = `id::text, tenant_id::text,
	period_start, period_end, metrics, top_classes, class_summaries,
	COALESCE(briefing, 'null'::jsonb) AS briefing,
	COALESCE(summary, '') AS summary, status, source,
	COALESCE(error_message, '') AS error_message, generated_at`

// GetDigest returns one tenant-week digest, including its class summaries.
func GetDigest(ctx *security.RequestContext, tenantID string, periodStart time.Time) (Digest, error) {
	var d Digest
	dbms, err := common.GetDatabaseManager(common.Metastore)
	if err != nil {
		return d, fmt.Errorf("GetDigest: db manager: %w", err)
	}
	query := `SELECT ` + digestSelectCols + `
		FROM event_analysis_digest
		WHERE tenant_id = $1::uuid AND period_start = $2`
	if err := dbms.Db.GetContext(ctx.GetContext(), &d, query, tenantID, periodStart); err != nil {
		return d, fmt.Errorf("GetDigest: query: %w", err)
	}
	return d, nil
}

// ListDigests returns a tenant's digests newest first, for the history tab.
//
// class_summaries is excluded: it is the largest column and the list view only
// renders headline counters and the briefing.
func ListDigests(ctx *security.RequestContext, tenantID string, limit int) ([]Digest, error) {
	dbms, err := common.GetDatabaseManager(common.Metastore)
	if err != nil {
		return nil, fmt.Errorf("ListDigests: db manager: %w", err)
	}
	query := `
		SELECT id::text, tenant_id::text,
		       period_start, period_end, metrics, top_classes,
		       '[]'::jsonb AS class_summaries,
		       'null'::jsonb AS briefing,
		       COALESCE(summary, '') AS summary, status, source,
		       COALESCE(error_message, '') AS error_message, generated_at
		FROM event_analysis_digest
		WHERE tenant_id = $1::uuid
		ORDER BY period_start DESC
		LIMIT $2`
	// Non-nil so an empty history marshals as [] rather than null. The UI treats
	// a non-array payload as a failed request, so a tenant with no digests yet
	// would otherwise be told the digest service is unavailable.
	out := []Digest{}
	if err := dbms.Db.SelectContext(ctx.GetContext(), &out, query, tenantID, limit); err != nil {
		return nil, fmt.Errorf("ListDigests: query: %w", err)
	}
	return out, nil
}
