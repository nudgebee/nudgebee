-- Restore the SERVER_ORCHESTRATED_SCANNERS feature row. The per-tenant
-- feature_flag rows that were granted before the retirement (3 tenants at
-- removal time) are not reconstructable from here; re-grant explicitly if
-- a rollback is needed.
INSERT INTO "public"."feature" ("description", "value")
VALUES (
  E'Run Popeye, Trivy CIS, kube-bench and Helm chart upgrade scans on demand without waiting for an agent upgrade. Recommendations refresh on the same schedule as today.',
  E'SERVER_ORCHESTRATED_SCANNERS'
)
ON CONFLICT (value) DO NOTHING;
