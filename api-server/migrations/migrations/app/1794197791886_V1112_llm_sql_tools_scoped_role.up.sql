-- A least-privilege path for llm-server's metastore SQL tools (events_execute,
-- anomaly_execute, recommendation_execute, recommendation_resolution_execute,
-- security_execute and the structured event tools). Part of #43649, see #39979.
--
-- Those tools run model-written SQL. llm-server now runs it inside its read-only
-- transaction as role nb_llm_sql_tools, with search_path = nb_llm_tools and
-- app.account_id set to the conversation's account (common_metadata_sql.go).
-- Each view below has the name of the table it reads, so the tools' SQL resolves
-- to these views unchanged. Each one returns only that account's rows and only
-- the columns the tools use. The role can read these views and nothing else.
-- With app.account_id unset, every scoped view returns no rows.
--
-- security_barrier is off on purpose: a barrier view per table stops Postgres
-- joining them efficiently (7.5 s instead of 11 ms on dev for one tool). The
-- tools instead wrap their view in OFFSET 0 (sqlBindTable), which keeps the
-- model's WHERE above the account filter so it never sees other accounts' rows.
--
-- A tool view in llm-server that starts using a new column or table needs it
-- added here (in a new migration). TestMetastoreSQLToolsScopedRole_DB, run by
-- migrations-validate, fails when one is missing.
--
-- Creating the role needs CREATEROLE. Where the migration user lacks it, the
-- role is skipped with a NOTICE, and llm-server refuses these tools until it
-- exists (LLM_SERVER_AGENT_SQL_SCOPED_ROLE=false opts out). To enable it, as a
-- user with CREATEROLE:
--   CREATE ROLE nb_llm_sql_tools NOLOGIN;
--   GRANT nb_llm_sql_tools TO <the user llm-server connects as>;
--   GRANT USAGE ON SCHEMA nb_llm_tools TO nb_llm_sql_tools;
--   GRANT SELECT ON ALL TABLES IN SCHEMA nb_llm_tools TO nb_llm_sql_tools;
--
-- Views only: no lock beyond ACCESS SHARE on the base tables, no data change.

DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'nb_llm_sql_tools') THEN
        IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = current_user AND (rolcreaterole OR rolsuper)) THEN
            RAISE NOTICE 'nb_llm_sql_tools not created: % lacks CREATEROLE; llm-server SQL tools keep running as %', current_user, current_user;
            RETURN;
        END IF;
        CREATE ROLE nb_llm_sql_tools NOLOGIN;
    END IF;
    -- llm-server switches to the role with SET ROLE, which needs membership
    -- (with the SET option on PostgreSQL 16+, which a plain GRANT gives).
    GRANT nb_llm_sql_tools TO CURRENT_USER;
EXCEPTION WHEN insufficient_privilege THEN
    RAISE NOTICE 'nb_llm_sql_tools not set up for %: %', current_user, SQLERRM;
END $$;

CREATE SCHEMA IF NOT EXISTS nb_llm_tools;

CREATE OR REPLACE VIEW nb_llm_tools.events WITH (security_barrier = false) AS
SELECT id, created_at, updated_at, finding_id, title, description, source, aggregation_key, failure,
       finding_type, category, priority, subject_type, subject_name, subject_namespace, subject_node,
       subject_owner, subject_owner_kind, ends_at, starts_at, fingerprint, evidences, cloud_account_id,
       status, labels, nb_status, urgency, computed_priority, computed_score, score_factors,
       score_confidence, cluster, service_key, snoozed_until
FROM public.events
WHERE cloud_account_id = NULLIF(current_setting('app.account_id', true), '')::uuid;

CREATE OR REPLACE VIEW nb_llm_tools.anomaly WITH (security_barrier = false) AS
SELECT id, created_at, updated_at, account_id, name, namespace, reference_value, current_value,
       anomaly_type, is_anomaly, evaluated_at, pod_name, config_id
FROM public.anomaly
WHERE account_id = NULLIF(current_setting('app.account_id', true), '')::uuid;

CREATE OR REPLACE VIEW nb_llm_tools.recommendation WITH (security_barrier = false) AS
SELECT id, tenant_id, cloud_account_id, resource_id, vulnerability_id, recommendation, rule_name,
       account_object_id, estimated_savings, created_at, updated_at, recommendation_action, category,
       note, severity, status, dismissed_reason, is_dismissed, snoozed_until, updated_by, finops_score,
       finops_band, finops_score_breakdown, dedupe_group
FROM public.recommendation
WHERE cloud_account_id = NULLIF(current_setting('app.account_id', true), '')::uuid;

-- No account column: scoped through its recommendation.
CREATE OR REPLACE VIEW nb_llm_tools.recommendation_resolution WITH (security_barrier = false) AS
SELECT rr.id, rr.recommendation_id, rr.type, rr.type_reference_id, rr.resolver_type, rr.status,
       rr.status_message, rr.pr_lifecycle_state, rr.created_at, rr.updated_at
FROM public.recommendation_resolution rr
WHERE rr.recommendation_id IN (
    SELECT r.id FROM public.recommendation r
    WHERE r.cloud_account_id = NULLIF(current_setting('app.account_id', true), '')::uuid);

CREATE OR REPLACE VIEW nb_llm_tools.cloud_resourses WITH (security_barrier = false) AS
SELECT id, name, meta, service_name
FROM public.cloud_resourses
WHERE account = NULLIF(current_setting('app.account_id', true), '')::uuid;

CREATE OR REPLACE VIEW nb_llm_tools.k8s_pods WITH (security_barrier = false) AS
SELECT tenant_id, cloud_account_id, workload_name, workload_type, namespace, meta, is_active
FROM public.k8s_pods
WHERE cloud_account_id = NULLIF(current_setting('app.account_id', true), '')::uuid;

-- Names only: cloud_accounts also holds credentials.
CREATE OR REPLACE VIEW nb_llm_tools.cloud_accounts WITH (security_barrier = false) AS
SELECT id, account_name, cloud_provider
FROM public.cloud_accounts
WHERE id = NULLIF(current_setting('app.account_id', true), '')::uuid;

CREATE OR REPLACE VIEW nb_llm_tools.tenant WITH (security_barrier = false) AS
SELECT t.id, t.name
FROM public.tenant t
WHERE t.id = (
    SELECT ca.tenant FROM public.cloud_accounts ca
    WHERE ca.id = NULLIF(current_setting('app.account_id', true), '')::uuid);

-- The shared CVE catalog: no tenant data, so not scoped.
CREATE OR REPLACE VIEW nb_llm_tools.vulnerabilities AS
SELECT id, vuln_id, package_name, severity, description, details
FROM public.vulnerabilities;

DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'nb_llm_sql_tools') THEN
        GRANT USAGE ON SCHEMA nb_llm_tools TO nb_llm_sql_tools;
        GRANT SELECT ON ALL TABLES IN SCHEMA nb_llm_tools TO nb_llm_sql_tools;
    END IF;
EXCEPTION WHEN insufficient_privilege THEN
    RAISE NOTICE 'nb_llm_sql_tools not granted the nb_llm_tools views: %', SQLERRM;
END $$;
