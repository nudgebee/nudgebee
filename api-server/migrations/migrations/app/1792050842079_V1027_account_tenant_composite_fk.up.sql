-- A row's tenant_id must be the tenant that owns its account.
--
-- The tenant comes from the caller's session and the account from the request,
-- so a browser tab left on tenant B after another tab switched to A could write
-- rows pairing tenant A with one of tenant B's accounts. The app now refuses
-- those requests; this makes the database refuse them too.
--
-- For each table that already has a foreign key from its account column to
-- cloud_accounts(id), add FOREIGN KEY (<account col>, tenant_id) REFERENCES
-- cloud_accounts(id, tenant). The UNIQUE (id, tenant) constraint those keys
-- need is added first when it is not there yet (same name and definition as
-- V1021, which skips it when present); id is the primary key, so it always holds.
--
-- * NOT VALID: new and changed rows are checked; existing rows are not scanned.
--   Validating here would fail the whole migration — and on-prem installs, whose
--   migration hook runs once — on any bad row already present. Clean the rows,
--   then VALIDATE CONSTRAINT separately.
-- * ON DELETE copies the table's existing account key (CASCADE, RESTRICT or
--   NO ACTION), so deleting an account behaves exactly as before and the
--   account delete plan (TestAccountDeletePlanCoversForeignKeys_DB) still holds.
--   ON UPDATE is NO ACTION: cloud_accounts.id and .tenant are never updated.
-- * Rows with a NULL account or tenant are not checked (MATCH SIMPLE), so
--   tenant-wide rows (account_id IS NULL) are unaffected.
-- * Tables with no account foreign key today are left alone: adding one would
--   change what deleting an account does, and several hold rows for accounts
--   that no longer exist. High-volume collector tables keyed by
--   cloud_account_id (k8s_*, recommendation, cloud_resource_metrics, ...) are
--   left alone too.
--
-- Lock window: each ALTER takes SHARE ROW EXCLUSIVE on its table and on
-- cloud_accounts until commit. That blocks writers, not readers, and needs no
-- table scan, so the migration holds the locks for well under a second. The
-- lock_timeout makes the migration fail fast rather than queue writers behind a
-- long-running transaction. Adding the UNIQUE (id, tenant) constraint, when it
-- is missing, takes ACCESS EXCLUSIVE on cloud_accounts while it indexes that
-- small table, briefly blocking its readers too. Tables and columns that do not exist on this
-- install are skipped, and re-running is a no-op.
SET LOCAL lock_timeout = '10s';

-- Every ADD FOREIGN KEY below takes this same lock on cloud_accounts anyway.
-- Taking it first fixes the order (parent, then each child), so the migration
-- cannot deadlock with a transaction that writes a child table and then
-- cloud_accounts.
LOCK TABLE public.cloud_accounts IN SHARE ROW EXCLUSIVE MODE;

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

DO $$
DECLARE
    spec text[];
    tbl text;
    col text;
    con text;
    rel regclass;
    on_delete text;
BEGIN
    FOREACH spec SLICE 1 IN ARRAY ARRAY[
        ['application_group_mapping', 'account_id'],
        ['auto_optimize_resource_map', 'account_id'],
        ['auto_pilot', 'account_id'],
        ['auto_pilot_approval_policy', 'account_id'],
        ['auto_pilot_approvals', 'account_id'],
        ['auto_pilot_task', 'account_id'],
        ['auto_playbook', 'account_id'],
        ['auto_playbook_executions', 'account_id'],
        ['auto_playbook_task', 'account_id'],
        ['billing_usage_cost', 'account_id'],
        ['configuration_store', 'account_id'],
        ['dashboard_bindings', 'account_id'],
        ['dashboards', 'account_id'],
        ['dw_queries', 'account_id'],
        ['event_incoming_webhooks', 'account_id'],
        ['feature_flag', 'account_id'],
        ['integration_user_accounts', 'account_id'],
        ['integrations_cloud_accounts', 'cloud_account_id'],
        ['llm_conversations', 'account_id'],
        ['llm_functions', 'account_id'],
        ['llm_global_contexts', 'account_id'],
        ['llm_kb_account_overrides', 'account_id'],
        ['llm_knowledgebases', 'account_id'],
        ['llm_rags', 'account_id'],
        ['notification_channel_account_mappings', 'account_id'],
        ['notification_rule_accounts', 'account_id'],
        ['notification_rules', 'account_id'],
        ['ownership_rules', 'cloud_account_id'],
        ['resource_owners', 'cloud_account_id'],
        ['runbook_task_output', 'account_id'],
        ['upgrade_plan', 'account_id'],
        ['upgrade_plan_audit', 'account_id'],
        ['upgrade_plan_steps', 'account_id'],
        ['user_history', 'account_id'],
        ['workflow_templates', 'account_id'],
        ['workflows', 'account_id']
    ]
    LOOP
        tbl := spec[1];
        col := spec[2];
        con := tbl || '_account_tenant_fkey';
        rel := to_regclass('public.' || quote_ident(tbl));
        IF rel IS NULL THEN
            CONTINUE;
        END IF;
        -- Both columns must exist as uuid, like cloud_accounts(id, tenant).
        IF (SELECT count(*) FROM pg_attribute
            WHERE attrelid = rel AND attname IN (col, 'tenant_id')
              AND atttypid = 'uuid'::regtype AND NOT attisdropped) <> 2 THEN
            CONTINUE;
        END IF;
        IF EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = rel AND conname = con) THEN
            CONTINUE;
        END IF;
        -- The existing single-column key from this account column, if any: copy
        -- its delete action. No such key on this install → leave the table alone.
        SELECT CASE c.confdeltype WHEN 'c' THEN 'CASCADE' WHEN 'r' THEN 'RESTRICT' ELSE 'NO ACTION' END
          INTO on_delete
          FROM pg_constraint c
          JOIN pg_attribute a ON a.attrelid = c.conrelid AND a.attnum = c.conkey[1]
         WHERE c.conrelid = rel AND c.contype = 'f' AND c.confrelid = 'public.cloud_accounts'::regclass
           AND array_length(c.conkey, 1) = 1 AND a.attname = col
         ORDER BY c.confdeltype = 'c' DESC
         LIMIT 1;
        IF on_delete IS NULL THEN
            CONTINUE;
        END IF;
        EXECUTE format(
            'ALTER TABLE public.%I ADD CONSTRAINT %I FOREIGN KEY (%I, tenant_id) REFERENCES public.cloud_accounts (id, tenant) ON DELETE %s NOT VALID',
            tbl, con, col, on_delete);
    END LOOP;
END $$;
