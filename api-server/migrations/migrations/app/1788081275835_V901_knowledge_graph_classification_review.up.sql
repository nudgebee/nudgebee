-- Source-agnostic log of "near-miss" classifications — cases where a
-- classifier (v1: traces.detectApplicationType) tried every rule it has and
-- still wasn't confident enough to override a node's type, but saw a signal
-- along the way that a confidence guard rejected. Purely additive: does not
-- affect knowledge_graph_node.properties.type_evidence / creation_evidence,
-- which continue to reflect only confident classifications. Written
-- best-effort (see core.RecordUncertainClassification) — a write failure
-- here must never fail or slow a knowledge graph build.
--
-- One row per (tenant_id, source, classification_kind, candidate_name,
-- candidate_namespace, candidate_cluster); a repeat sighting overwrites
-- reason_code/candidate_type/evidence with the latest occurrence and
-- increments occurrence_count. No history array is kept — latest-wins,
-- since evidence from an already-fixed classifier gap stops being useful
-- once the gap is fixed.
--
-- candidate_cluster is part of the unique key because the recorder fires once
-- per K8s account (i.e. per cluster): a tenant running the same
-- namespace/service name in two clusters is the normal case, and those are
-- two independent near-misses to investigate, not one row whose cluster gets
-- overwritten by whichever cluster the rebuild happened to process last.
--
-- candidate_namespace and candidate_cluster are NOT NULL DEFAULT '' rather
-- than nullable: Postgres UNIQUE treats NULL <> NULL, so a nullable key
-- column would let two "no namespace" / "no cluster" sightings insert as
-- duplicate rows instead of colliding onto one.
--
-- classification_kind distinguishes different KINDS of uncertain decision
-- for the same resource (e.g. "node_type" vs a future "specific_type" or
-- "node_match") so they track as independent rows instead of clobbering
-- each other.

CREATE TABLE IF NOT EXISTS public.knowledge_graph_classification_review (
    id                  UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id           UUID NOT NULL,
    source              TEXT NOT NULL,
    classification_kind TEXT NOT NULL,
    candidate_type      TEXT,
    candidate_name      TEXT NOT NULL,
    candidate_namespace TEXT NOT NULL DEFAULT '',
    candidate_cluster   TEXT NOT NULL DEFAULT '',
    reason_code         TEXT NOT NULL,
    reason_description  TEXT,
    evidence            JSONB NOT NULL DEFAULT '{}'::jsonb,
    occurrence_count    INTEGER NOT NULL DEFAULT 1,
    first_seen_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_seen_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    created_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (tenant_id, source, classification_kind, candidate_name, candidate_namespace, candidate_cluster)
);

-- Only one supplementary index: (tenant_id) and (tenant_id, source,
-- classification_kind) lookups are already served by the UNIQUE constraint's
-- own index via leftmost-prefix matching, so separate indexes for those would
-- just add write overhead with no query benefit. last_seen_at isn't part of
-- the unique key, so this is the only access pattern that needs its own index
-- (e.g. "what's been seen recently for this tenant").
CREATE INDEX IF NOT EXISTS idx_kg_classification_review_last_seen ON public.knowledge_graph_classification_review (tenant_id, last_seen_at);
