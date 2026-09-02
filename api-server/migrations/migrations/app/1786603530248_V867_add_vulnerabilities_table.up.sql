-- Creates the shared vulnerabilities table and links recommendation to it.
--
-- DDL ONLY, on purpose. This migration does NOT backfill vulnerabilities, does
-- NOT populate recommendation.vulnerability_id, and does NOT touch
-- recommendation.recommendation. All three are deliberate — see below.
--
-- No package_version column: it's the version installed on ONE resource, not
-- a property of the vulnerability itself. The same CVE+package can show up
-- with many different installed versions across a fleet (some patched
-- further than others) and they're still the same vulnerability — keying on
-- version would fragment one real CVE into several rows. The installed
-- version is per-finding data and lives in recommendation.recommendation
-- instead (alongside fixed_version, the actionable remediation detail).
--
-- Why there is no backfill here
-- --------------------------------------------------------------------------
-- Atlas applies each file in ONE transaction (`--tx-mode file`; the
-- `-- atlas:txmode none` directive is a paid-tier feature the pinned Community
-- v0.36.0 silently ignores), and the ALTER TABLE below takes ACCESS EXCLUSIVE on
-- recommendation for the whole runtime. So anything this file does to every row
-- is a full stall of every reader and writer of recommendation.
--
-- A bulk `UPDATE recommendation SET vulnerability_id = …` was measured on dev
-- (Cloud SQL; 721,946 rows, 4.4 GB heap+toast, 21 indexes):
--
--   1.2 ms/row warm, 3.1 ms/row cold — ~75 buffers touched per row
--
-- 75 buffers/row is index maintenance: the table has 21 indexes and sits at the
-- default fillfactor=100, so its pages have no room for a second tuple version,
-- every update is non-HOT, and each one writes into every index. Confirmed by
-- measuring with and without the vulnerability_id index present — 77.6 vs 75.3
-- buffers/row, so ordering the index build after a backfill would NOT have made
-- the updates HOT. The cost is irreducible without changing the table's indexes
-- or fillfactor.
--
-- At dev's 579k security findings that is 12–30 minutes of held ACCESS EXCLUSIVE.
-- On a 6M-row production table it is hours. Not viable, at any batch size, inside
-- a migration.
--
-- Why not backfilling is safe
-- --------------------------------------------------------------------------
--   1. The read path never requires a link. Every CVE field is read through a
--      fallback to the raw recommendation.recommendation payload when
--      vulnerability_id IS NULL — VulnerabilityRecommendationSQL in
--      api-server/services/query/metadata.go, the vuln_id / package_name /
--      package_id column Defs, and llm-server/tools/tool_security.go. Pinned by
--      api-server/services/query/vulnerability_fallback_test.go. Because this
--      migration also leaves the payload untrimmed, that fallback data is intact.
--      An unlinked row renders exactly as it does today.
--   2. It self-heals with no operator action, on every install including on-prem.
--      Both writers upsert `vulnerability_id = EXCLUDED.vulnerability_id`
--      (scan_orchestrator/persist.go, vmpackage/persist.go), and scans re-run on a
--      cycle — images every imageScanFreshDays (7), VMs daily. So within about a
--      week every finding for a live image or host is linked, incrementally, with
--      no lock window at all.
--   3. What never converges is findings whose image or host is gone. Those are
--      already status='Archive' historical rows (47% of dev's image_scan rows),
--      and case 1 serves them correctly and indefinitely.
--
-- Net effect: the dedup/storage win arrives over the first week instead of during
-- a maintenance window, and nothing is ever incorrect in the meantime.
--
-- Expected runtime of THIS file on a 6M-row recommendation table: roughly 4–6
-- minutes, effectively all of it the two scans at the end (VALIDATE CONSTRAINT
-- ~1–2 min, CREATE INDEX ~2–3 min; measured 14 s for the index at dev's 722k
-- rows). The rest is metadata-only.

CREATE TABLE IF NOT EXISTS "public"."vulnerabilities" (
    "id"               uuid NOT NULL DEFAULT gen_random_uuid(),
    "created_at"       timestamp NOT NULL DEFAULT now(),
    "updated_at"       timestamp NOT NULL DEFAULT now(),
    "source"           text NOT NULL,
    "vuln_id"          text NOT NULL,
    "package_name"     text NOT NULL,
    "package_arch"     text,
    "package_arch_key" text NOT NULL GENERATED ALWAYS AS (COALESCE("package_arch", '')) STORED,
    "package_type"     text,
    "fixed_version"    text,
    "severity"         text,
    "cvss_score"       double precision,
    "cvss_vector"      text,
    "description"      text,
    "data_source"      text,
    "details"          jsonb NOT NULL DEFAULT '{}'::jsonb,
    PRIMARY KEY ("id")
);

CREATE UNIQUE INDEX IF NOT EXISTS "uq_vulnerabilities_identity"
    ON "public"."vulnerabilities" ("source", "vuln_id", "package_name", "package_arch_key");
CREATE INDEX IF NOT EXISTS "idx_vulnerabilities_vuln_id" ON "public"."vulnerabilities" ("vuln_id");

DROP TRIGGER IF EXISTS "set_public_vulnerabilities_updated_at" ON "public"."vulnerabilities";
CREATE TRIGGER "set_public_vulnerabilities_updated_at"
BEFORE UPDATE ON "public"."vulnerabilities"
FOR EACH ROW EXECUTE PROCEDURE "public"."set_current_timestamp_updated_at"();
COMMENT ON TRIGGER "set_public_vulnerabilities_updated_at" ON "public"."vulnerabilities"
IS 'trigger to set value of column "updated_at" to current timestamp on row update';

-- Nullable, no default → metadata-only, so this is instant regardless of table
-- size. It is the one statement here that takes ACCESS EXCLUSIVE on
-- recommendation, and it holds it only until this file commits.
ALTER TABLE "public"."recommendation"
    ADD COLUMN IF NOT EXISTS "vulnerability_id" uuid;

-- ON DELETE SET NULL, not CASCADE: vulnerabilities rows are heavily shared
-- (dev: 456,587 findings collapse into 20,708 rows, ~22 findings/row on
-- average). A CASCADE would let deleting one shared vulnerability silently
-- mass-delete every linked recommendation, including rows carrying user
-- state (dismissed/snoozed/in-progress). The read path already falls back
-- to the raw recommendation payload when the join misses (vulnerability_id
-- IS NULL), so SET NULL degrades gracefully instead.
--
-- Added NOT VALID then VALIDATEd as two statements rather than one ADD
-- CONSTRAINT: the NOT VALID half is a catalog write, and VALIDATE takes only
-- SHARE UPDATE EXCLUSIVE, so neither blocks readers or writers the way a
-- validating ADD CONSTRAINT would. ADD CONSTRAINT has no IF NOT EXISTS, hence
-- the catalog guard — this file must stay re-runnable.
DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint
        WHERE conname = 'recommendation_vulnerability_id_fkey'
          AND conrelid = 'public.recommendation'::regclass
    ) THEN
        ALTER TABLE "public"."recommendation"
            ADD CONSTRAINT "recommendation_vulnerability_id_fkey"
            FOREIGN KEY ("vulnerability_id") REFERENCES "public"."vulnerabilities"("id")
            ON UPDATE restrict ON DELETE set null
            NOT VALID;
    END IF;
END $$;

-- Every value is NULL at this point, so this can only succeed; it still costs one
-- pass over the heap (~1–2 min at 6M rows). Validating now rather than leaving the
-- constraint NOT VALID forever means the planner can rely on it and there is no
-- lingering "unvalidated" state for someone to discover later.
--
-- Needs no idempotency guard: VALIDATE CONSTRAINT short-circuits on an already
-- validated constraint (postgres checks !convalidated in ATExecValidateConstraint
-- before scanning), so a re-run costs nothing rather than repeating the scan.
-- Measured server-side on dev: 0.765 ms against the already-validated constraint
-- vs 1,576 ms for one real full heap scan in the same session.
ALTER TABLE "public"."recommendation"
    VALIDATE CONSTRAINT "recommendation_vulnerability_id_fkey";

-- Plain CREATE INDEX, NOT CONCURRENTLY. migrations-lint.yaml rejects
-- CONCURRENTLY in executable SQL unconditionally, and it is right to: the
-- `-- atlas:txmode none` directive is a paid-tier Atlas feature that the pinned
-- Community v0.36.0 silently IGNORES, so a CONCURRENTLY build fails with
-- "cannot run inside a transaction block" (proved by the V868 CI failure on
-- commit d91deefa, which carried the directive on line 1 and still failed).
--
-- Full (not partial) index: the column is entirely NULL when this runs, so a
-- partial `WHERE vulnerability_id IS NOT NULL` index would build faster here, but
-- it would also diverge from the index already present on dev and buys nothing
-- once rows start linking. Measured 14 s at dev's 722k rows; budget ~2–3 min at 6M.
--
-- On a very large deployment you may still prefer to build it out of band first:
--   CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_recommendation_vulnerability_id
--       ON recommendation (vulnerability_id);
-- Where that has been done this statement is a no-op.
CREATE INDEX IF NOT EXISTS "idx_recommendation_vulnerability_id"
    ON "public"."recommendation" ("vulnerability_id");

-- TODO(vulnerabilities-cleanup): two follow-ups, both deliberately deferred and
-- both requiring the measurements above to be re-taken at the target scale first.
--
-- 1. Linking historical rows. Scans converge live findings within ~a week (see
--    header), so this is only needed if someone wants archived/no-longer-running
--    findings linked too, or wants the dedup sooner. It cannot be a migration —
--    measured 1.2–3.1 ms/row, i.e. hours on a 6M-row table under ACCESS EXCLUSIVE.
--    It would have to be a batched, resumable, out-of-band job.
-- 2. Trimming the redundant CVE fields out of recommendation.recommendation.
--    Pure storage reclaim; the read path reconstructs the legacy shape from
--    vulnerabilities and falls back to the raw payload, so nothing depends on it.
--    Strictly more expensive than (1): it rewrites the jsonb itself, and because
--    recommendation is indexed on the expression recommendation->>'image_name'
--    every such UPDATE is non-HOT. The table GROWS before it shrinks and needs
--    pg_repack (not VACUUM FULL, which takes ACCESS EXCLUSIVE) to reclaim.
--    Must not run for any row that is not linked yet — the trim removes exactly
--    the payload the fallback reads. Guard with `recommendation ? 'vuln_id'` /
--    `recommendation ? 'VulnerabilityID'` so it is a no-op on trimmed rows.
--
-- Until BOTH are done everywhere, the fallbacks in
-- api-server/services/query/metadata.go and llm-server/tools/tool_security.go must
-- stay; vulnerability_fallback_test.go pins them.
