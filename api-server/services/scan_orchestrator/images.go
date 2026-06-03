package scan_orchestrator

import "os"

// Scanner images. The chart (deploy/kubernetes/services-server/values.yaml +
// the umbrella nudgebee chart) is the source of truth — set <SCANNER>_IMAGE
// env vars there to bump. Defaults below match the base chart so local/dev
// runs without env pull the same digest production uses.
//
// trivy / popeye / kube-bench / cert-scanner are mirrored to ghcr.io/nudgebee
// by .github/workflows/scanner-images-mirror.yaml. Nova is published directly
// by nova/.github/workflows/build-image.yaml (date_sha tag on every main build,
// v* on releases). CI workflows (nudgebee-build-* and services-server-*) auto-
// bump the Nova tag from GHCR.
const (
	defaultTrivyImage       = "ghcr.io/nudgebee/trivy:0.58.0"
	defaultPopeyeImage      = "ghcr.io/nudgebee/popeye:v0.11.1-nudgebee.1"
	defaultKubeBenchImage   = "ghcr.io/nudgebee/kube-bench:v0.10.4"
	defaultCertScannerImage = "ghcr.io/nudgebee/cert-scanner:v0.1.0"

	// Nova and the k8s-version-upgrade scanner share a single Nova image today;
	// the helpers are split so they can diverge without editing every reference.
	defaultNovaTag           = "2026-05-12T15-41-58_3b33692e2739d958a11eb43346c4240a70fd15cd"
	defaultNovaImage         = "ghcr.io/nudgebee/nova:" + defaultNovaTag
	defaultK8sVersionUpgrade = "ghcr.io/nudgebee/nova:" + defaultNovaTag
)

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func TrivyImage() string       { return envOr("TRIVY_IMAGE", defaultTrivyImage) }
func PopeyeImage() string      { return envOr("POPEYE_IMAGE", defaultPopeyeImage) }
func KubeBenchImage() string   { return envOr("KUBE_BENCH_IMAGE", defaultKubeBenchImage) }
func CertScannerImage() string { return envOr("CERT_SCANNER_IMAGE", defaultCertScannerImage) }
func NovaImage() string        { return envOr("NOVA_IMAGE", defaultNovaImage) }
func K8sVersionUpgradeImage() string {
	return envOr("K8S_VERSION_UPGRADE_IMAGE", defaultK8sVersionUpgrade)
}
