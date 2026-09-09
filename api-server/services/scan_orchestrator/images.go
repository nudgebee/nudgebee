package scan_orchestrator

import "os"

// Scanner images. The chart (deploy/kubernetes/services-server/values.yaml +
// the umbrella nudgebee chart) is the source of truth — set <SCANNER>_IMAGE
// env vars there to bump. Defaults below match the base chart so local/dev
// runs without env pull the same digest production uses.
//
// trivy and kube-bench are mirrors of the upstream Aqua images, republished
// under ghcr.io/nudgebee so installs pull from one registry; popeye is our
// fork. Nova is published by nova/.github/workflows/build-image.yaml (date_sha
// tag on every main build, v* on releases), and release.yaml auto-bumps the
// Nova and Popeye tags from GHCR at package time.
//
// Only Job-based scanners belong here. certificate_scanner and
// k8s_version_upgrade run in-process against the agent's get_resource
// primitive (runcert.go / runk8sversion.go) and pull no image — they had
// CERT_SCANNER_IMAGE / K8S_VERSION_UPGRADE_IMAGE pins that nothing ever read.
//
// Every tag below is resolved against its registry by
// .github/workflows/scanner-images-verify.yaml, so a pin that 404s fails CI
// instead of surfacing as an ImagePullBackOff at scan time.
const (
	defaultTrivyImage     = "ghcr.io/nudgebee/trivy:0.58.0"
	defaultPopeyeImage    = "ghcr.io/nudgebee/popeye:v0.11.1-nudgebee.1"
	defaultKubeBenchImage = "ghcr.io/nudgebee/kube-bench:v0.10.4"
	defaultNovaImage      = "ghcr.io/nudgebee/nova:2026-05-22T07-15-34_decaacbbf56e1789c3299a89f790b8e824ab741c"
)

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func TrivyImage() string     { return envOr("TRIVY_IMAGE", defaultTrivyImage) }
func PopeyeImage() string    { return envOr("POPEYE_IMAGE", defaultPopeyeImage) }
func KubeBenchImage() string { return envOr("KUBE_BENCH_IMAGE", defaultKubeBenchImage) }
func NovaImage() string      { return envOr("NOVA_IMAGE", defaultNovaImage) }
