-- Lock window: ADD CONSTRAINT ... UNIQUE takes an ACCESS EXCLUSIVE lock on
-- cloud_accounts while it builds the index (blocking reads); the table holds one
-- row per account, so this is expected to take well under a second. The three
-- new tables are created empty.

-- cloud_accounts.id is already the primary key, so (id, tenant) is trivially
-- unique; the constraint exists only so child tables can reference both columns
-- and have the database reject an account from another tenant.
DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint
        WHERE conrelid = 'public.cloud_accounts'::regclass
          AND conname = 'cloud_accounts_id_tenant_unique'
    ) THEN
        ALTER TABLE public.cloud_accounts
            ADD CONSTRAINT cloud_accounts_id_tenant_unique UNIQUE (id, tenant);
    END IF;
END
$$;

CREATE TABLE IF NOT EXISTS public.llm_tool_approval_rules (
    id              UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id       UUID        NOT NULL REFERENCES public.tenant(id) ON DELETE CASCADE,
    -- NULL = the rule applies to every account in the tenant, including
    -- accounts added later (it may then pin an integration via
    -- tool_config_id). Set = the rule applies to that one account only.
    account_id      UUID,
    name            TEXT        NOT NULL,
    enabled         BOOLEAN     NOT NULL DEFAULT TRUE,
    tool_name       TEXT        NOT NULL,
    tool_config_id  TEXT,
    match_kind      TEXT        NOT NULL CHECK (match_kind IN ('tool', 'exact_command', 'command_regex', 'arguments')),
    command_pattern TEXT,
    working_dir     TEXT,
    conditions      JSONB       NOT NULL DEFAULT '[]'::jsonb,
    -- FALSE: the rule waives confirmation only when a signed-in person made the
    -- request. TRUE: it also applies to runs with no person (system jobs,
    -- token-only API calls, unlinked chat users).
    allow_unattended BOOLEAN    NOT NULL DEFAULT FALSE,
    revision        INTEGER     NOT NULL DEFAULT 1 CHECK (revision > 0),
    created_by      UUID        REFERENCES public.users(id) ON DELETE SET NULL,
    updated_by      UUID        REFERENCES public.users(id) ON DELETE SET NULL,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT llm_tool_approval_rules_account_tenant_fk
        FOREIGN KEY (account_id, tenant_id)
        REFERENCES public.cloud_accounts(id, tenant)
        ON DELETE CASCADE,
    CONSTRAINT llm_tool_approval_rules_match_shape CHECK (
        (match_kind = 'tool' AND command_pattern IS NULL AND conditions = '[]'::jsonb)
        OR (match_kind IN ('exact_command', 'command_regex') AND command_pattern IS NOT NULL AND conditions = '[]'::jsonb)
        OR (match_kind = 'arguments' AND command_pattern IS NULL AND jsonb_typeof(conditions) = 'array' AND jsonb_array_length(conditions) > 0)
    )
);

-- Names are unique per account, and separately among the tenant's
-- all-accounts rules (a plain UNIQUE would let all-accounts rules share a
-- name, since NULLs never collide).
CREATE UNIQUE INDEX IF NOT EXISTS llm_tool_approval_rules_account_name_unique
    ON public.llm_tool_approval_rules (tenant_id, account_id, name)
    WHERE account_id IS NOT NULL;
CREATE UNIQUE INDEX IF NOT EXISTS llm_tool_approval_rules_all_accounts_name_unique
    ON public.llm_tool_approval_rules (tenant_id, name)
    WHERE account_id IS NULL;

CREATE INDEX IF NOT EXISTS llm_tool_approval_rules_lookup_idx
    ON public.llm_tool_approval_rules (tenant_id, account_id, lower(tool_name))
    WHERE enabled = TRUE;

COMMENT ON TABLE public.llm_tool_approval_rules IS
    'Tenant-admin managed rules that waive interactive confirmation for matching tool invocations. Authorization and validation still apply.';

CREATE TABLE IF NOT EXISTS public.llm_tool_approval_rule_events (
    id             UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id      UUID        NOT NULL REFERENCES public.tenant(id) ON DELETE CASCADE,
    account_id     UUID, -- NULL = an all-accounts rule
    rule_id        UUID        NOT NULL,
    rule_revision  INTEGER     NOT NULL,
    action         TEXT        NOT NULL CHECK (action IN ('create', 'update', 'delete')),
    actor_id       UUID        REFERENCES public.users(id) ON DELETE SET NULL,
    rule_name      TEXT        NOT NULL,
    tool_name      TEXT        NOT NULL,
    tool_config_id TEXT,
    match_kind     TEXT        NOT NULL,
    enabled        BOOLEAN     NOT NULL,
    -- What this revision approved, so a decision's (rule_id, rule_revision)
    -- stays auditable after the rule is edited or deleted. Admin-written
    -- policy, not secrets.
    command_pattern  TEXT,
    working_dir      TEXT,
    conditions       JSONB       NOT NULL DEFAULT '[]'::jsonb,
    allow_unattended BOOLEAN     NOT NULL DEFAULT FALSE,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT NOW()
    -- No foreign key to cloud_accounts: this is audit history and must
    -- survive the account's deletion.
);

CREATE INDEX IF NOT EXISTS llm_tool_approval_rule_events_tenant_created_idx
    ON public.llm_tool_approval_rule_events (tenant_id, created_at DESC);

COMMENT ON TABLE public.llm_tool_approval_rule_events IS
    'Transactional mutation ledger for tool approval rules, with a snapshot of what each revision approved.';

CREATE TABLE IF NOT EXISTS public.llm_tool_approval_decisions (
    id                UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id         UUID        NOT NULL REFERENCES public.tenant(id) ON DELETE CASCADE,
    account_id        UUID        NOT NULL,
    rule_id           UUID        NOT NULL,
    rule_revision     INTEGER     NOT NULL,
    rule_name         TEXT        NOT NULL,
    match_kind        TEXT        NOT NULL,
    tool_name         TEXT        NOT NULL,
    tool_config_id    TEXT,
    input_fingerprint TEXT        NOT NULL,
    user_id           TEXT        NOT NULL DEFAULT '',
    conversation_id   TEXT        NOT NULL DEFAULT '',
    message_id        TEXT        NOT NULL DEFAULT '',
    agent_id          TEXT        NOT NULL DEFAULT '',
    tool_call_id      TEXT        NOT NULL DEFAULT '',
    created_at        TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    -- No foreign key to cloud_accounts: decisions are the audit trail for
    -- writes that ran without a prompt and must survive the account.
    CONSTRAINT llm_tool_approval_decisions_invocation_unique UNIQUE
        (tenant_id, conversation_id, message_id, agent_id, tool_call_id, input_fingerprint, rule_id, rule_revision)
);

COMMENT ON TABLE public.llm_tool_approval_decisions IS
    'Durable, idempotent record that a specific invocation had interactive confirmation waived by a specific immutable rule revision. Contains a fingerprint, never raw tool input.';
