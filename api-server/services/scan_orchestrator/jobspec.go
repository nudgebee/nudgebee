// Package scan_orchestrator drives the agent's generic schedule_k8s_job /
// wait_for_k8s_job / get_k8s_job_logs primitives to run scanners (Trivy,
// Popeye, KRR, kube-bench, cert-scanner, Nova helm-upgrade, k8s-version
// upgrade) and parse their output into recommendation rows.
//
// The agent has NO knowledge of which scanner this is — it just runs the Job.
// All scanner-specific logic (image, args, security context, parser) lives
// here. Adding a new scanner is one entry in scanners.go's ScannerCatalog.
package scan_orchestrator

// JobSpec mirrors github.com/nudgebee/nudgebee-agent/pkg/scanners.JobSpec
// byte-for-byte. We keep an api-server copy so the orchestrator stays
// independent of the agent's Go module — it ships through the relay as
// JSON in `action_params.spec`.
//
// Field set is intentionally narrow: only what scanners need at the time of
// the Robusta cutover. The agent enforces hygiene (namespace clamp, TTL,
// BackoffLimit, concurrency cap, log size cap) regardless of what the spec
// says — see nudgebee-agent/pkg/scanners/primitives.go.
type JobSpec struct {
	NamePrefix     string            `json:"name_prefix"`
	Image          string            `json:"image"`
	Command        []string          `json:"command,omitempty"`
	Args           []string          `json:"args,omitempty"`
	Env            map[string]string `json:"env,omitempty"`
	ServiceAccount string            `json:"service_account,omitempty"`

	Privileged  bool `json:"privileged,omitempty"`
	HostPID     bool `json:"host_pid,omitempty"`
	HostNetwork bool `json:"host_network,omitempty"`

	// NodeName pins the Job's pod to a node (bypassing the scheduler). image_scanner
	// sets it to the node where the target image is already pulled so a `trivy fs`
	// rootfs scan can reuse the node-local image and skip the registry.
	NodeName string `json:"node_name,omitempty"`

	// ImagePullPolicy overrides the main container's pull policy. image_scanner
	// sets "IfNotPresent" so the node-local image copy is reused, not re-pulled.
	ImagePullPolicy string `json:"image_pull_policy,omitempty"`

	// RunAsUser sets the main container's securityContext.runAsUser. Pointer so an
	// explicit 0 (root) survives JSON omitempty. image_scanner runs trivy as root.
	RunAsUser *int64 `json:"run_as_user,omitempty"`

	// InitContainers run before the main container (serialized corev1.Container
	// shapes, same map-based modeling as Volumes). image_scanner stages the trivy
	// binary into a shared emptyDir via one init container.
	InitContainers []map[string]any `json:"init_containers,omitempty"`

	// Volumes / VolumeMounts use the agent's serialized k8s.io/api/core/v1
	// shapes. We model them as raw JSON-friendly maps instead of importing
	// k8s types into api-server (which today doesn't depend on client-go).
	// kube-bench needs hostPath mounts; image_scanner needs a shared emptyDir.
	Volumes      []map[string]any `json:"volumes,omitempty"`
	VolumeMounts []map[string]any `json:"volume_mounts,omitempty"`

	// ImagePullSecretsFrom names the workload pod whose image-pull secrets the
	// agent should make available to the scan Job, so image_scanner can pull a
	// private image. The agent honors it only when run with auto-copy enabled;
	// otherwise it's ignored, so sending it is always safe.
	ImagePullSecretsFrom *PodRef `json:"image_pull_secrets_from,omitempty"`

	TimeoutHintSeconds int `json:"timeout_hint_seconds,omitempty"`
}

// PodRef identifies a pod by namespace+name. Mirrors the agent's scanners.PodRef.
type PodRef struct {
	Namespace string `json:"namespace"`
	Name      string `json:"name"`
}
