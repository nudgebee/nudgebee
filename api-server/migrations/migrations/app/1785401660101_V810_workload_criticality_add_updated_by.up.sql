-- Reconcile workload_criticality with the schema V776 was finalized to.
--
-- V776's committed form defines an `updated_by uuid` column (the operator who last set the row),
-- which the user-override upsert in the app writes to. Any environment that applied an EARLIER
-- draft of V776 (before it was rewritten) has the table without `updated_by`, so that upsert fails
-- with: pq: column "updated_by" of relation "workload_criticality" does not exist (42703).
-- golang-migrate records V776 as applied and never re-runs it, so this forward migration is the
-- only way to converge those environments.
--
-- Idempotent by design: a no-op where V776 already created `updated_by`, a fix where it did not.
ALTER TABLE public.workload_criticality
    ADD COLUMN IF NOT EXISTS updated_by uuid NULL;
