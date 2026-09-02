-- Weekly rollup of event-log analyses, one row per (cloud_account_id, period_start).
-- The row IS the job's state: the generator asks "which weeks have no row?" and
-- fills the gaps, so a missed cron tick self-heals on the next run instead of
-- leaving a permanent hole. UNIQUE makes re-runs safe.
CREATE TABLE IF NOT EXISTS event_analysis_digest (
    id               uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id        uuid        NOT NULL,
    cloud_account_id uuid        NOT NULL,
    period_start     date        NOT NULL,
    period_end       date        NOT NULL,

    -- Headline counters rendered as chips: analyses, failed, failure_classes,
    -- events_analysed, new_incidents, recurring, noise_pct, services, p1_pct.
    metrics          jsonb       NOT NULL DEFAULT '{}'::jsonb,
    -- Ranked aggregation_key rollup (top 20 + counts) so a tenant-level view can
    -- merge across accounts and re-rank without re-reading the raw events. Wider
    -- than the set that gets an LLM summary, which is the head of this list.
    top_classes      jsonb       NOT NULL DEFAULT '[]'::jsonb,
    -- Per-failure-class findings from the map stage, kept so a failed synthesis
    -- can be retried without re-summarising every analysis.
    class_summaries  jsonb       NOT NULL DEFAULT '[]'::jsonb,
    -- Synthesised briefing: patterns, guardrails, action items.
    summary          text,

    status           text        NOT NULL DEFAULT 'generated',
    -- Who produced this row. An on-demand run is provisional: the gap scan
    -- re-selects it so the next scheduled tick regenerates and supersedes it,
    -- keeping the scheduled output authoritative.
    source           text        NOT NULL DEFAULT 'scheduled',
    error_message    text,
    generated_at     timestamptz NOT NULL DEFAULT now(),
    updated_at       timestamptz NOT NULL DEFAULT now(),

    CONSTRAINT event_analysis_digest_status_check
        CHECK (status IN ('generated', 'partial', 'failed')),
    CONSTRAINT event_analysis_digest_period_check
        CHECK (period_end > period_start),
    CONSTRAINT event_analysis_digest_source_check
        CHECK (source IN ('scheduled', 'on_demand')),
    CONSTRAINT event_analysis_digest_account_period_key
        UNIQUE (cloud_account_id, period_start)
);

-- No (cloud_account_id, period_start) index here: the UNIQUE constraint above
-- already creates one, and a btree scans backwards as cheaply as forwards, so it
-- serves the tab's "newest weeks first" ordering too.

-- Tenant-wide rollup across accounts for a period.
CREATE INDEX IF NOT EXISTS idx_event_analysis_digest_tenant_period
    ON event_analysis_digest (tenant_id, period_start DESC);

-- No index for the gap scan: it LEFT JOINs on (cloud_account_id, period_start)
-- and only then filters on status/source, so the planner resolves the join with
-- the UNIQUE constraint's index. A partial index keyed on period_start alone
-- cannot serve that lookup and would be write overhead for no read benefit.
