-- Memory Architecture Phase 2: collective memory (L7).
-- Tenant-scoped knowledge shared across ALL users in the tenant: architecture
-- facts, known-good configs, common troubleshooting playbooks, runbook
-- cross-references. Unlike the other memory layers, this has no user_id —
-- two users in the same tenant see the same collective rows.
-- Populated by auto-extraction (from agent investigations) + human curation
-- (admins can promote or author entries via the management UI).

CREATE TABLE IF NOT EXISTS llm_memory_collective (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),

    -- No user_id: collective knowledge is tenant-wide by definition.
    tenant_id       VARCHAR(255) NOT NULL,

    -- NULL = applies cross-module (e.g. "our Slack incident channel is
    -- #ops-firefight"). Module-scoped entries ("for the k8s agent: our
    -- clusters live in us-east-1") narrow surfaces into relevant prompts.
    agent_module    VARCHAR(32),

    -- Closed vocabulary: architectural_fact, configuration_insight,
    -- dependency_mapping, troubleshooting, runbook_index. Kept narrow so
    -- the renderer can group entries into sectioned prompt blocks.
    entry_kind      VARCHAR(64) NOT NULL,

    -- Short headline ("payments service depends on stripe-webhook queue").
    -- VARCHAR(512) matches the patterns table — keeps headline size bounded
    -- so it fits on a single line in the prompt.
    subject         VARCHAR(512) NOT NULL,

    -- Full expansion of the entry. TEXT because entries can include
    -- multi-paragraph context, runbook links, code snippets.
    body            TEXT NOT NULL,

    -- Structured tags and refs the renderer may use (linked runbook ids,
    -- source URLs, last validation time). JSONB keeps the shape flexible.
    metadata        JSONB NOT NULL DEFAULT '{}',

    -- Optional backref to the event that produced this row (auto-extracted
    -- entries). Not a FK: the event log is partitioned and FKs into
    -- partitioned tables need the partition key too — not worth the
    -- complexity for a weak audit link. Kept as a plain UUID.
    source_event_id UUID,

    -- 0.00–1.00. Default 0.7 for auto-extracted entries (same as the
    -- classifier's baseline) — lower than explicit prefs because we're
    -- inferring from one or two observations. Human-curated entries bump
    -- this to 1.00 when admins accept them.
    confidence      NUMERIC(3,2) NOT NULL DEFAULT 0.7,

    -- Admin user_id when the row was created or edited by a human.
    -- NULL = auto-extracted (agent-generated). Lets us display provenance
    -- in the admin UI and filter curated vs auto entries separately.
    curated_by      VARCHAR(255),

    created_at      TIMESTAMP NOT NULL DEFAULT NOW(),
    updated_at      TIMESTAMP NOT NULL DEFAULT NOW()
);

-- One row per (tenant, module, kind, subject). Re-extraction upserts instead
-- of inserting duplicates. Functional unique index + COALESCE for the same
-- NULL-collapsing reason as preferences / patterns.
CREATE UNIQUE INDEX IF NOT EXISTS uq_collective_tenant_mod_kind_subject
    ON llm_memory_collective (tenant_id, COALESCE(agent_module, ''), entry_kind, subject);

-- Compose-path lookup: "load collective entries for this tenant + module,
-- grouped by kind". Covers the "give me architectural facts for the k8s
-- agent" query directly.
CREATE INDEX IF NOT EXISTS idx_collective_lookup
    ON llm_memory_collective (tenant_id, agent_module, entry_kind);

-- Keyword search across headline AND body so "what do we know about
-- payments?" hits entries whether the word appears in the subject line or
-- deeper in the body. GIN on a concatenated tsvector keeps it one index.
CREATE INDEX IF NOT EXISTS idx_collective_text_search
    ON llm_memory_collective USING GIN (to_tsvector('english', subject || ' ' || body));
