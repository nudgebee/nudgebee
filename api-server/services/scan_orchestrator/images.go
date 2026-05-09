package scan_orchestrator

import "os"

// Pinned scanner images. Bumps live here — a single edit + api-server deploy.
// Versions match Robusta today (env_vars.py:132-139); bumping any pin requires
// re-validating the parser (parser_*.go) against the new output schema.
const (
	TrivyImage           = "aquasec/trivy:0.58.0"
	PopeyeImage          = "derailed/popeye:v0.21.5"
	KubeBenchImage       = "aquasec/kube-bench:v0.10.4"
	CertScannerImage     = "robustadev/cert-scanner:v0.1.0"
	// Latest Nova image as of PR-C verification (verified pullable from the
	// ECR-backed mirror; the previous April-2025 pin had been aged out and
	// produced ImagePullBackOff). Bump from ECR repo `864186153326/nova` —
	// `aws ecr describe-images --repository-name nova --query 'sort_by(...)'`
	// to find the latest. Verify it's actually pullable from
	// registry.dev.nudgebee.pollux.in (the public mirror) before pinning.
	defaultNovaImagePin  = "registry.dev.nudgebee.pollux.in/nova:2026-03-07T11-56-14_c1432955300a4a231c9d6a364513ec680898f595"
	defaultK8sVersionPin = "registry.dev.nudgebee.pollux.in/nova:2026-03-07T11-56-14_c1432955300a4a231c9d6a364513ec680898f595"
)

// NovaImage returns the Nova image to run for helm_chart_upgrade. Mirrors
// Robusta's NOVA_IMAGE composition (env_vars.py:139): an internal registry with
// a date-pinned digest, overridable via NOVA_IMAGE env. Resolved lazily so
// tests can stub the env per-call.
func NovaImage() string {
	if v := os.Getenv("NOVA_IMAGE"); v != "" {
		return v
	}
	return defaultNovaImagePin
}

// K8sVersionUpgradeImage shares the Nova family today; kept separate so it can
// diverge without editing every helm reference.
func K8sVersionUpgradeImage() string {
	if v := os.Getenv("K8S_VERSION_UPGRADE_IMAGE"); v != "" {
		return v
	}
	return defaultK8sVersionPin
}
