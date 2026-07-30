-- workload_criticality: the single source of truth for how business-critical a workload is.
--
-- Triage scoring today infers a workload's blast radius per-alert from the alert text (the
-- LLM signal-class verdict's `blast` field). That is re-derived on every alert class and is
-- not something an operator can see or curate. This table makes criticality a first-class,
-- persistent, curatable attribute of the workload itself — populated from observed facts
-- (ingress-backing, dependency fan-in), harvested labels, and operator declarations — so the
-- triage classifier can consume a grounded/authoritative signal and other consumers (RCA, UI)
-- can read one consistent answer.
--
-- Grain: exactly one row per workload, keyed on cloud_resource_id (== k8s_workloads.cloud_resource_id),
-- so the row joins the workload inventory directly. namespace is stored as display/filter metadata.

CREATE TABLE IF NOT EXISTS public.workload_criticality (
    id                  uuid        NOT NULL DEFAULT gen_random_uuid(),
    tenant_id           uuid        NOT NULL,
    cloud_account_id    uuid        NOT NULL,
    cloud_resource_id   uuid        NOT NULL,       -- == k8s_workloads.cloud_resource_id
    namespace           text        NULL,           -- display/filter metadata

    -- The tier. Named enum kept as text (validated in app code) to avoid a pg enum migration.
    criticality         text        NOT NULL,                    -- critical | high | medium | low

    -- Provenance. 'user' rows are authoritative and sticky; the discovery job never overwrites them.
    source              text        NOT NULL,                    -- user | fact_signal | llm_inferred
    confidence          real        NULL,
    signals             jsonb       NULL,                        -- {customer_facing, fan_in, image, labels, ...}
    rationale           text        NULL,                        -- one-line "why", shown in the review UI

    -- 'proposed' rows are surfaced for review but (per the scoring policy) may still contribute at
    -- reduced weight; 'active' rows are confirmed/authoritative.
    status              text        NOT NULL DEFAULT 'active',   -- active | proposed
    is_user_override    boolean     NOT NULL DEFAULT false,

    updated_by          uuid        NULL,                        -- operator who last set it (NULL for auto rows)
    created_at          timestamptz NOT NULL DEFAULT now(),
    updated_at          timestamptz NOT NULL DEFAULT now(),

    CONSTRAINT workload_criticality_pkey PRIMARY KEY (id)
);

-- Exactly one criticality row per workload.
CREATE UNIQUE INDEX IF NOT EXISTS uq_workload_criticality_resource
    ON public.workload_criticality (cloud_account_id, cloud_resource_id);

-- Namespace filtering on the review screen.
CREATE INDEX IF NOT EXISTS idx_workload_criticality_ns
    ON public.workload_criticality (cloud_account_id, namespace);
