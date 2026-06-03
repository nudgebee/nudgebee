-- Retire the SERVER_ORCHESTRATED_SCANNERS feature flag.
--
-- The scan_orchestrator path (popeye_scan, trivy_cis_scan, kube_bench_scan,
-- helm_chart_upgrade, plus image_scanner via runImageScannerServerOrchestrated)
-- is now the only path; the agent's per-scanner named actions were removed
-- post-PR #34 of the agent (b376656), so the legacy agent_task fallback no
-- longer ran anywhere.
DELETE FROM "public"."feature_flag" WHERE "feature_id" = 'SERVER_ORCHESTRATED_SCANNERS';
DELETE FROM "public"."feature"      WHERE "value"      = 'SERVER_ORCHESTRATED_SCANNERS';
