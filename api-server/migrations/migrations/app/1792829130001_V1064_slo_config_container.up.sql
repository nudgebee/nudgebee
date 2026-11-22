-- The container a container-scoped forecast SLO (memory_saturation) watches; NULL for
-- every other SLO type. Outside the unique key on purpose: one memory SLO per workload
-- (see docs/architecture-decisions.md, "Memory forecast SLO").
--
-- A nullable column without a default is a catalogue-only change in Postgres: no
-- rewrite, no backfill, and the ACCESS EXCLUSIVE lock is held only for that change.
ALTER TABLE "public"."slo_config" ADD COLUMN IF NOT EXISTS "container" text;
