-- triage_signal_class: per-(tenant, signal-class) cached LLM categorization verdict.
-- The triage scorer reads this on the hot path (a single indexed lookup) and applies a
-- deterministic policy; the verdict itself is minted asynchronously off the hot path, once
-- per class, and reused for every event in that class. class_key is
--   lower(aggregation_key) | finding_type | coalesce(subject_owner, subject_namespace)
-- which collapses per-instance fingerprint fragmentation while separating conflated buckets.

CREATE TABLE IF NOT EXISTS triage_signal_class (
    id            uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id     uuid NOT NULL,
    class_key     text NOT NULL,

    -- LLM verdict (the only thing the model produces; deterministic policy turns it into a score)
    category             text,
    intrinsic            text,        -- critical | high | medium | low | info
    blast                text,        -- control_plane | customer_facing | data_durability | monitoring_backbone | single_workload | expected_change
    recurrence_semantics text,        -- escalating | neutral | noise
    env_sensitivity      text,        -- none | partial | full
    band_floor           text,        -- P0 | P1 | P2 | P3
    band_ceiling         text,
    confidence           numeric NOT NULL DEFAULT 0,
    reasoning            text,

    -- lifecycle / audit
    source_model  text,                              -- model id that produced the verdict ('fallback' if none)
    status        text NOT NULL DEFAULT 'minting',   -- minting (claimed, verdict pending) | active
    hit_count     bigint NOT NULL DEFAULT 0,
    created_at    timestamp without time zone NOT NULL DEFAULT now(),
    updated_at    timestamp without time zone NOT NULL DEFAULT now(),

    CONSTRAINT uq_triage_signal_class_tenant_key UNIQUE (tenant_id, class_key)
);
