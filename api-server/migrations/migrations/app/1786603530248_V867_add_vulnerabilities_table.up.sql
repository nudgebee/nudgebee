-- No package_version column: it's the version installed on ONE resource, not
-- a property of the vulnerability itself. The same CVE+package can show up
-- with many different installed versions across a fleet (some patched
-- further than others) and they're still the same vulnerability — keying on
-- version would fragment one real CVE into several rows. The installed
-- version is per-finding data and lives in recommendation.recommendation
-- instead (alongside fixed_version, the actionable remediation detail).
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

-- ON DELETE SET NULL, not CASCADE: vulnerabilities rows are heavily shared
-- (dev: 456,587 findings collapse into 20,708 rows, ~22 findings/row on
-- average). A CASCADE would let deleting one shared vulnerability silently
-- mass-delete every linked recommendation, including rows carrying user
-- state (dismissed/snoozed/in-progress). The read path already falls back
-- to the raw recommendation payload when the join misses (vulnerability_id
-- IS NULL), so SET NULL degrades gracefully instead.
ALTER TABLE "public"."recommendation"
    ADD COLUMN IF NOT EXISTS "vulnerability_id" uuid
        REFERENCES "public"."vulnerabilities"("id") ON UPDATE restrict ON DELETE set null;

CREATE INDEX IF NOT EXISTS "idx_recommendation_vulnerability_id"
    ON "public"."recommendation" ("vulnerability_id");

-- Backfill: dedup existing VM package vulnerability findings into vulnerabilities.
-- Multiple installed versions of the same CVE+package collapse into the ONE
-- row ON CONFLICT keeps (first-inserted wins) — expected, since version isn't
-- part of the identity; CVSS/description/etc. are the same regardless of
-- which version triggered the finding.
INSERT INTO vulnerabilities
    (source, vuln_id, package_name, package_arch, package_type,
     fixed_version, severity, cvss_score, cvss_vector, description, data_source, details)
SELECT
    'vm_package_vulnerability',
    r.recommendation->>'vuln_id',
    r.recommendation#>>'{package,name}',
    NULLIF(r.recommendation#>>'{package,arch}', ''),
    r.recommendation#>>'{package,type}',
    NULLIF(r.recommendation->>'fixed_version', ''),
    r.severity,
    NULLIF(r.recommendation->>'cvss_v3_score', '')::double precision,
    NULLIF(r.recommendation->>'cvss_v3_vector', ''),
    NULLIF(r.recommendation->>'description', ''),
    NULLIF(r.recommendation->>'data_source', ''),
    jsonb_build_object(
        'fix_state', r.recommendation->'fix_state', 'fix_channel', r.recommendation->'fix_channel',
        'epss', r.recommendation->'epss', 'kev', r.recommendation->'kev',
        'risk', r.recommendation->'risk', 'advisory_ids', r.recommendation->'advisory_ids')
FROM recommendation r
WHERE r.category = 'Security' AND r.rule_name = 'vm_package_vulnerability'
  AND r.recommendation->>'vuln_id' IS NOT NULL AND r.recommendation#>>'{package,name}' IS NOT NULL
ON CONFLICT (source, vuln_id, package_name, package_arch_key) DO NOTHING;

-- Backfill: dedup existing container image scan (Trivy) findings into vulnerabilities.
INSERT INTO vulnerabilities
    (source, vuln_id, package_name, package_arch, package_type,
     fixed_version, severity, cvss_score, cvss_vector, description, data_source, details)
SELECT
    'image_scan',
    r.recommendation->>'VulnerabilityID',
    r.recommendation->>'PkgName',
    NULL, NULL,
    NULLIF(r.recommendation->>'FixedVersion', ''),
    r.severity,
    COALESCE(NULLIF(r.recommendation#>>'{CVSS,nvd,V3Score}', '')::double precision,
             NULLIF(r.recommendation#>>'{CVSS,redhat,V3Score}', '')::double precision,
             NULLIF(r.recommendation#>>'{CVSS,ghsa,V3Score}', '')::double precision),
    COALESCE(r.recommendation#>>'{CVSS,nvd,V3Vector}', r.recommendation#>>'{CVSS,redhat,V3Vector}',
             r.recommendation#>>'{CVSS,ghsa,V3Vector}'),
    COALESCE(NULLIF(r.recommendation->>'Description', ''), r.recommendation->>'Title'),
    COALESCE(r.recommendation#>>'{DataSource,ID}', r.recommendation#>>'{DataSource,Name}'),
    jsonb_build_object(
        'title', r.recommendation->'Title', 'cwe_ids', r.recommendation->'CweIDs',
        'vendor_ids', r.recommendation->'VendorIDs', 'references', r.recommendation->'References',
        'primary_url', r.recommendation->'PrimaryURL', 'data_source', r.recommendation->'DataSource',
        'cvss', r.recommendation->'CVSS', 'published_date', r.recommendation->'PublishedDate',
        'last_modified_date', r.recommendation->'LastModifiedDate', 'status', r.recommendation->'Status',
        'severity_source', r.recommendation->'SeveritySource', 'vendor_severity', r.recommendation->'VendorSeverity',
        'pkg_identifier', r.recommendation->'PkgIdentifier', 'pkg_id', r.recommendation->'PkgID',
        'layer', r.recommendation->'Layer')
FROM recommendation r
WHERE r.category = 'Security' AND r.rule_name = 'image_scan'
  AND r.recommendation->>'VulnerabilityID' IS NOT NULL AND r.recommendation->>'PkgName' IS NOT NULL
ON CONFLICT (source, vuln_id, package_name, package_arch_key) DO NOTHING;

-- Back-populate the FK on recommendation from the now-deduped vulnerabilities rows.
UPDATE recommendation r SET vulnerability_id = v.id
FROM vulnerabilities v
WHERE r.category = 'Security' AND r.rule_name = 'vm_package_vulnerability' AND r.vulnerability_id IS NULL
  AND v.source = 'vm_package_vulnerability'
  AND v.vuln_id = r.recommendation->>'vuln_id'
  AND v.package_name = r.recommendation#>>'{package,name}'
  AND v.package_arch_key = COALESCE(NULLIF(r.recommendation#>>'{package,arch}', ''), '');

UPDATE recommendation r SET vulnerability_id = v.id
FROM vulnerabilities v
WHERE r.category = 'Security' AND r.rule_name = 'image_scan' AND r.vulnerability_id IS NULL
  AND v.source = 'image_scan'
  AND v.vuln_id = r.recommendation->>'VulnerabilityID'
  AND v.package_name = r.recommendation->>'PkgName'
  AND v.package_arch_key = '';

-- recommendation.recommendation keeps only the per-finding fixed_version/
-- fix_state (VM) or FixedVersion (image_scan) plus the installed
-- package_version/InstalledVersion — everything else just got backfilled
-- above and is reachable via vulnerability_id.
--
-- package_version: COALESCE prefers the flat key over the old nested
-- {package:{version:...}} path so this statement stays idempotent on a
-- retry — once a row is trimmed the nested path is gone, and without the
-- COALESCE a second pass would read NULL from it and wipe out the value a
-- prior (possibly partial, --tx-mode none) run already set.
UPDATE recommendation r SET recommendation =
    jsonb_build_object(
        'fixed_version', r.recommendation->'fixed_version',
        'fix_state', r.recommendation->'fix_state',
        'package_version', COALESCE(r.recommendation->'package_version', r.recommendation#>'{package,version}'))
WHERE r.category = 'Security' AND r.rule_name = 'vm_package_vulnerability' AND r.vulnerability_id IS NOT NULL;

UPDATE recommendation r SET recommendation =
    jsonb_build_object(
        'image_name', r.recommendation->'image_name',
        'FixedVersion', r.recommendation->'FixedVersion',
        'InstalledVersion', r.recommendation->'InstalledVersion')
WHERE r.category = 'Security' AND r.rule_name = 'image_scan' AND r.vulnerability_id IS NOT NULL;
