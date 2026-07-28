-- Reconcile workload_criticality's unique index with the form V776 was finalized to.
--
-- V776's committed form creates a PLAIN unique index on (cloud_account_id, cloud_resource_id).
-- An environment that applied an EARLIER draft of V776 has it as a PARTIAL index instead:
--
--     CREATE UNIQUE INDEX uq_workload_criticality_resource
--         ON workload_criticality (cloud_account_id, cloud_resource_id)
--         WHERE cloud_resource_id IS NOT NULL;
--
-- Postgres cannot infer a partial index as an ON CONFLICT arbiter unless the statement repeats the
-- index predicate, so BOTH upserts against this table fail at plan time — every row, every run:
--
--     pq: there is no unique or exclusion constraint matching the ON CONFLICT specification (42P10)
--
-- Effect on a drifted environment: the nightly discovery sweep classifies workloads and persists
-- nothing, and the operator set/reset on the Service Criticality tab fails. Observed in dev — no row
-- written since 2026-07-04, while the sweep kept reporting rows as "tiered".
--
-- V776 is recorded as applied and never re-runs, so this forward migration is the only way to
-- converge those environments — the same situation V789 fixed for the missing `updated_by` column.
--
-- Idempotent: a no-op where the index is already plain, a rebuild where it is partial. Deliberately
-- not CONCURRENTLY — the table holds one row per non-default workload (tens per account), so the
-- brief exclusive lock is immaterial, and Atlas Community ignores the no-transaction directive.
DO $$
BEGIN
    IF EXISTS (
        SELECT 1
        FROM pg_index i
        JOIN pg_class c ON c.oid = i.indexrelid
        WHERE c.relname = 'uq_workload_criticality_resource'
          AND i.indpred IS NOT NULL   -- partial: the drifted form
    ) THEN
        DROP INDEX public.uq_workload_criticality_resource;
    END IF;
END $$;

CREATE UNIQUE INDEX IF NOT EXISTS uq_workload_criticality_resource
    ON public.workload_criticality (cloud_account_id, cloud_resource_id);
