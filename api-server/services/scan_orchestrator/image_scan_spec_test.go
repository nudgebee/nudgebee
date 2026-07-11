package scan_orchestrator

import (
	"strings"
	"testing"
)

// The image_scanner must build a filesystem-scan spec, NOT a `trivy image`
// registry pull — that's what lets it scan private-registry images without
// credentials (it reuses the node-local image). Lock the shape.
func TestBuildImageScanSpec_FsScanShape(t *testing.T) {
	spec := buildImageScanSpec(ScanAccount{
		AccountID:       "acc",
		TenantID:        "ten",
		TargetImage:     "registry.private.example.com/nudgebee-app:v1",
		TargetNode:      "gke-node-abc",
		TargetNamespace: "app",
		TargetPodName:   "web-1",
	}, nil)

	// The agent is pointed at the workload pod so it can source the pull secret.
	if spec.ImagePullSecretsFrom == nil ||
		spec.ImagePullSecretsFrom.Namespace != "app" || spec.ImagePullSecretsFrom.Name != "web-1" {
		t.Errorf("ImagePullSecretsFrom = %+v; want {app, web-1}", spec.ImagePullSecretsFrom)
	}

	// Main container is the target image itself, reused from the node.
	if spec.Image != "registry.private.example.com/nudgebee-app:v1" {
		t.Errorf("Image = %q; want the target image as the main container", spec.Image)
	}
	if spec.ImagePullPolicy != "IfNotPresent" {
		t.Errorf("ImagePullPolicy = %q; want IfNotPresent", spec.ImagePullPolicy)
	}
	if spec.NodeName != "gke-node-abc" {
		t.Errorf("NodeName = %q; want the image's node (pin to reuse node-local image)", spec.NodeName)
	}
	if spec.RunAsUser == nil || *spec.RunAsUser != 0 {
		t.Errorf("RunAsUser = %v; want 0 (root, to read the whole rootfs)", spec.RunAsUser)
	}

	// trivy is run directly (no `sh -c`) so it works on distroless target images.
	if len(spec.Command) != 1 || !strings.HasSuffix(spec.Command[0], "/trivy") {
		t.Errorf("Command = %v; want the staged trivy binary run directly (no shell)", spec.Command)
	}
	// The trivy subcommand must be `fs`, never `image` (which would pull from the registry).
	cmd := strings.Join(spec.Command, " ") + " " + strings.Join(spec.Args, " ")
	if !strings.Contains(cmd, "trivy fs") {
		t.Errorf("Command/Args = %q; want a `trivy fs` rootfs scan", cmd)
	}
	if strings.Contains(cmd, "trivy image") || (len(spec.Args) > 0 && spec.Args[0] == "image") {
		t.Errorf("Command/Args = %q; must not pull from the registry via `trivy image`", cmd)
	}

	// Init container stages the trivy binary into the shared volume.
	if len(spec.InitContainers) != 1 {
		t.Fatalf("InitContainers = %d; want 1 (stage trivy binary)", len(spec.InitContainers))
	}
	if spec.InitContainers[0]["image"] != TrivyImage() {
		t.Errorf("init container image = %v; want the trivy scanner image", spec.InitContainers[0]["image"])
	}
	// Two emptyDirs: the shared binary volume and a writable /tmp for trivy's cache.
	if len(spec.Volumes) != 2 || len(spec.VolumeMounts) != 2 {
		t.Errorf("want shared binary + /tmp emptyDir volumes; got %d volumes, %d mounts",
			len(spec.Volumes), len(spec.VolumeMounts))
	}
}

// Missing image must surface a visible placeholder rather than scanning "/".
func TestBuildImageScanSpec_MissingImagePlaceholder(t *testing.T) {
	spec := buildImageScanSpec(ScanAccount{AccountID: "acc", TenantID: "ten"}, nil)
	if spec.Image != "{{IMAGE}}" {
		t.Errorf("Image = %q; want the {{IMAGE}} placeholder when TargetImage is empty", spec.Image)
	}
}
