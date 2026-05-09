-- Stage 3 / Wave 2 of the Robusta deprecation plan.
--
-- Default OFF. The api-server's scan_orchestrator runs popeye_scan,
-- trivy_cis_scan, kube_bench_scan, and helm_chart_upgrade via the agent's
-- generic schedule_k8s_job / wait_for_k8s_job / get_k8s_job_logs primitives
-- only when this flag is explicitly 'enabled' for the tenant.
-- tenant.IsFeatureExplicitlyEnabled treats missing rows as disabled, so the
-- legacy path stays the default until a tenant is opted in.
INSERT INTO "public"."feature"("description", "value")
VALUES (
  E'Run Popeye, Trivy CIS, kube-bench and Helm chart upgrade scans on demand without waiting for an agent upgrade. Recommendations refresh on the same schedule as today.',
  E'SERVER_ORCHESTRATED_SCANNERS'
)
ON CONFLICT (value) DO NOTHING;

-- Seed the canary tenant ON so verification happens on real workload immediately.
INSERT INTO "public"."feature_flag" ("id", "feature_id", "tenant_id", "status")
VALUES (gen_random_uuid(), 'SERVER_ORCHESTRATED_SCANNERS',
        '890cad87-c452-4aa7-b84a-742cee0454a1'::uuid, 'enabled')
ON CONFLICT DO NOTHING;
