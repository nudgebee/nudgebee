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

	// Volumes / VolumeMounts use the agent's serialized k8s.io/api/core/v1
	// shapes. We model them as raw JSON-friendly maps instead of importing
	// k8s types into api-server (which today doesn't depend on client-go).
	// kube-bench is the only built-in scanner that needs hostPath mounts;
	// add more shapes here as needed.
	Volumes      []map[string]any `json:"volumes,omitempty"`
	VolumeMounts []map[string]any `json:"volume_mounts,omitempty"`

	TimeoutHintSeconds int `json:"timeout_hint_seconds,omitempty"`
}
