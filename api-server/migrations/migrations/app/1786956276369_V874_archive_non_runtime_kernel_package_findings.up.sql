-- Archives existing vm_package_vulnerability findings whose vulnerable
-- packages are entirely non-runtime kernel packages (build headers, tools,
-- devel/doc/debuginfo/buildinfo, abi lists) — headers, tools, docs, and
-- similar packages that never execute. Grype has no concept of "kernel line"
-- (its Distro type only carries Type/Version/Codename/Channels/IDLike), so a
-- non-runtime package sharing the kernel source's advisory identity inherits
-- that source's entire CVE history, unbounded, since kernel CVEs are
-- routinely left with no upper-bound fixed version in the tracker data
-- grype ingests. See #36279: on one dev host, linux-tools-common alone
-- (never executes; it's perf/cpupower/etc.) carried 2,481 of 16,266 open
-- findings, spanning CVE years 2012-2026.
--
-- The code fix (vmpackage/match.go's isNonRuntimeKernelPackage) stops NEW
-- matches for these packages; this is the one-time cleanup of rows written
-- before that fix. Going forward, persistFindings' per-resource
-- archive-then-replace on every rescan means these rows would eventually
-- self-heal too, but daily rescans mean waiting up to a full cycle per host,
-- and a decommissioned or infrequently-scanned resource might never rescan
-- again — hence this migration instead of waiting it out.
--
-- The exclusion predicate below mirrors isNonRuntimeKernelPackage exactly.
-- If that function's pattern list changes, this predicate does not
-- automatically follow — it is a point-in-time cleanup, not a
-- standing invariant enforced by a trigger or constraint.
--
-- Lock behavior: this is a plain UPDATE, not DDL — it takes ROW EXCLUSIVE on
-- the table (does not block readers, or writers of other rows) and row-level
-- locks only on matched rows. rule_name and category are both indexed
-- (recommendation_rulename, recommendation_category), and status = 'Open'
-- is highly selective, so this should not require a full table scan. Matched
-- row count was not measured against a production table from here; the dev
-- sample above (2,481 rows for one package on one host) is a lower bound on
-- what a real fleet will match, not an upper one.
--
-- status = 'Open', not status != 'Archive': the status enum also has
-- Dismissed/InProgress/Assigned, which are user-set triage states, not
-- scanner-owned. This sweep must only touch the scanner-owned 'Open' state —
-- overwriting a user's Dismissed/InProgress row into Archive would silently
-- discard their triage decision, exactly the class of bug V853's header
-- documents for this same table.
--
-- Idempotent: matched rows move to status = 'Archive', which the WHERE
-- clause excludes, so a re-run is a no-op.
UPDATE recommendation
SET status = 'Archive', updated_at = now()
WHERE category = 'Security'
  AND rule_name = 'vm_package_vulnerability'
  AND status = 'Open'
  AND jsonb_array_length(recommendation -> 'vulnerable_packages') > 0
  AND NOT EXISTS (
    SELECT 1
    FROM jsonb_array_elements_text(recommendation -> 'vulnerable_packages') AS pkg
    WHERE NOT (
      (lower(pkg) = 'linux' OR lower(pkg) LIKE 'linux-%' OR lower(pkg) = 'kernel' OR lower(pkg) LIKE 'kernel-%')
      AND (
        lower(pkg) = 'linux-libc-dev'
        OR lower(pkg) LIKE 'linux-source%'
        OR lower(pkg) LIKE 'linux-udebs%'
        OR lower(pkg) LIKE '%-headers%'
        OR lower(pkg) LIKE '%-tools%'
        OR lower(pkg) LIKE '%-devel%'
        OR lower(pkg) LIKE '%-doc%'
        OR lower(pkg) LIKE '%-debuginfo%'
        OR lower(pkg) LIKE '%-buildinfo%'
        OR lower(pkg) LIKE '%-abi-whitelists%'
        OR lower(pkg) LIKE '%-abi-stablelists%'
      )
    )
  );
