package scan_orchestrator

// Scanner declares one server-orchestrated scanner. BuildSpec returns the
// JobSpec that gets shipped to the agent (image, args, security context),
// Parse turns the Job's stdout into recommendation rows, RuleName is the
// `recommendation.rule_name` we write so the existing UI surfaces them.
//
// Adding a new scanner = one entry in ScannerCatalog. No agent change.
//
// NOTE: certificate_scanner and k8s_version_upgrade aren't Job-based — Robusta
// implemented them in-process by calling the K8s API directly (CertManager CRD
// list / kube-proxy version check). They don't fit this catalog and ship via
// a separate api-server path that uses the agent's existing get_resource /
// kubectl_command_executor primitives. See follow-up PR-C-2 for that.
type Scanner struct {
	Name      string
	BuildSpec func(account ScanAccount, params map[string]any) JobSpec
	Parse     func(stdout string, account ScanAccount) ([]Recommendation, error)
	RuleName  string
	// CronExpression is the legacy Robusta cron tag (kept for parity with what
	// production rows already store in agent.connection_status -> 'schedule_jobs').
	// Empty means "not cron-scheduled" (e.g. image_scanner runs per-image on demand).
	CronExpression string
}

// ScanAccount is the per-tenant context BuildSpec / Parse get. Kept narrow so
// the catalog stays a pure-data table — anything hairy goes inside Parse.
type ScanAccount struct {
	AccountID         string
	TenantID          string
	ServiceAccount    string // resolved by orchestrator from chart config
	K8sVersionCurrent string // optional; populated for k8s_version_upgrade
}

// Recommendation is the orchestrator's output row. Mirrors the columns the
// existing recommendation table accepts (cloud_account_id, tenant_id, rule_name,
// recommendation, severity, status, account_object_id, etc.). Persistence is
// the orchestrator's job (cron handler) — Parse just produces the rows.
type Recommendation struct {
	CloudAccountID       string  `json:"cloud_account_id"`
	TenantID             string  `json:"tenant_id"`
	Category             string  `json:"category"`
	RuleName             string  `json:"rule_name"`
	RecommendationAction string  `json:"recommendation_action"`
	Recommendation       string  `json:"recommendation"` // JSON-encoded payload
	Severity             string  `json:"severity"`
	Status               string  `json:"status"`
	AccountObjectID      string  `json:"account_object_id"`
	ResourceID           string  `json:"resource_id,omitempty"`
	EstimatedSavings     float64 `json:"estimated_savings,omitempty"`
}

// ScannerCatalog is the source of truth for Job-based scanners.
//
// Phase 2a (this PR) ships popeye with its real Go parser plus stub Parse for
// the four other Job-based scanners. The stubs return the raw stdout in a
// single Recommendation row so the cron path is verifiable end-to-end (job
// scheduled, polled, fetched, persisted) without blocking on schema reverse
// engineering. Each Phase 2b PR replaces one stub with the real parser using
// fixtures captured from a real run against the dev cluster.
var ScannerCatalog = map[string]Scanner{
	"popeye_scan": {
		Name: "popeye_scan",
		BuildSpec: func(_ ScanAccount, _ map[string]any) JobSpec {
			// --force-exit-zero matches Robusta's playbook: popeye exits non-zero
			// when it finds issues, which would trip BackoffLimit=0 and mark the
			// Job Failed before we can fetch the (perfectly valid) JSON report.
			return JobSpec{
				NamePrefix:         "popeye-scan",
				Image:              PopeyeImage(),
				Args:               []string{"-A", "-o", "json", "--force-exit-zero"},
				ServiceAccount:     "{{SCANNER_SA}}",
				TimeoutHintSeconds: 300,
			}
		},
		Parse: ParsePopeye,
		// PopeyeRuleNameLabel is just the catalog label; per-row rule_name is
		// derived from the popeye linter (collector pattern: "<linter>_misconfigurations").
		RuleName:       PopeyeRuleNameLabel,
		CronExpression: "0 12 * * 1",
	},
	"trivy_cis_scan": {
		Name: "trivy_cis_scan",
		BuildSpec: func(_ ScanAccount, _ map[string]any) JobSpec {
			// Mirrors robusta/playbooks/nudgebee_playbooks/trivy_cis_scan.py:75-83.
			// Stdout (no -o flag) so get_k8s_job_logs picks up the JSON directly
			// without the pod-file-copy dance Robusta did.
			return JobSpec{
				NamePrefix: "trivy-cis-scan",
				Image:      TrivyImage(),
				Args: []string{
					"k8s",
					"--no-progress",
					"--compliance=k8s-cis-1.23",
					"--report=all",
					"--format=json",
					"--disable-node-collector",
					"--skip-java-db-update",
					"--skip-check-update",
					"--timeout=3600s",
				},
				ServiceAccount:     "{{SCANNER_SA}}",
				TimeoutHintSeconds: 600,
			}
		},
		Parse:          ParseTrivyCIS,
		RuleName:       TrivyCISRuleName,
		CronExpression: "0 8 * * 1",
	},
	"kube_bench_scan": {
		Name: "kube_bench_scan",
		BuildSpec: func(_ ScanAccount, _ map[string]any) JobSpec {
			// Mirrors robusta/playbooks/nudgebee_playbooks/kube_bench.py: needs
			// HostPID + the eleven hostPath volumes kube-bench reads from the
			// node, all read-only. No Privileged required (Robusta ran without).
			return JobSpec{
				NamePrefix:         "kube-bench-scan",
				Image:              KubeBenchImage(),
				Command:            []string{"kube-bench"},
				Args:               []string{"--json"},
				HostPID:            true,
				ServiceAccount:     "{{SCANNER_SA}}",
				Volumes:            kubeBenchVolumes(),
				VolumeMounts:       kubeBenchVolumeMounts(),
				TimeoutHintSeconds: 300,
			}
		},
		Parse:          ParseKubeBench,
		RuleName:       KubeBenchRuleName,
		CronExpression: "0 12 * * 1",
	},
	"image_scanner": {
		Name: "image_scanner",
		BuildSpec: func(_ ScanAccount, params map[string]any) JobSpec {
			// Phase 2a uses the simple `trivy image --format json --quiet <IMAGE>`
			// path — i.e. trivy scans an image registry, not the Pod's filesystem.
			// Robusta's image_scanner.py also offers a fs-scan path via an init
			// container that copies the trivy binary into a shared emptyDir; that
			// requires init-container support our JobSpec doesn't model yet, so
			// it's deferred. The simple path covers what most customers want.
			image, _ := params["image"].(string)
			if image == "" {
				image = "{{IMAGE}}" // surfaces the missing-param failure clearly in logs
			}
			return JobSpec{
				NamePrefix:         "trivy-image-scan",
				Image:              TrivyImage(),
				Args:               []string{"image", "--format", "json", "--quiet", image},
				ServiceAccount:     "{{SCANNER_SA}}",
				TimeoutHintSeconds: 300,
			}
		},
		Parse:    ParseImageScan,
		RuleName: ImageScanRuleName,
		// image_scanner is on-demand (per-image); not cron-scheduled.
	},
	"helm_chart_upgrade": {
		Name: "helm_chart_upgrade",
		BuildSpec: func(_ ScanAccount, _ map[string]any) JobSpec {
			// Nova "find" lists installed Helm releases and reports upgrade
			// candidates. Mirrors playbooks/nudgebee_playbooks/helm_chart_upgrade.py.
			return JobSpec{
				NamePrefix:         "helm-chart-upgrade",
				Image:              NovaImage(),
				Command:            []string{"./nova"},
				Args:               []string{"find"},
				ServiceAccount:     "{{SCANNER_SA}}",
				TimeoutHintSeconds: 3000, // Robusta uses 50 minutes
			}
		},
		Parse:          ParseHelmChartUpgrade,
		RuleName:       HelmChartUpgradeRuleName,
		CronExpression: "0 12 * * *",
	},
}

// kubeBenchVolumes returns the 11 hostPath volumes kube-bench reads to assess
// CIS compliance. Read-only; the agent enforces namespace/TTL clamps so the
// blast radius stays bounded.
func kubeBenchVolumes() []map[string]any {
	mounts := []struct{ name, path string }{
		{"var-lib-etcd", "/var/lib/etcd"},
		{"var-lib-kubelet", "/var/lib/kubelet"},
		{"var-lib-kube-scheduler", "/var/lib/kube-scheduler"},
		{"var-lib-kube-controller-manager", "/var/lib/kube-controller-manager"},
		{"etc-systemd", "/etc/systemd"},
		{"lib-systemd", "/lib/systemd"},
		{"srv-kubernetes", "/srv/kubernetes"},
		{"etc-kubernetes", "/etc/kubernetes"},
		{"usr-bin", "/usr/local/mount-from-host/bin"},
		{"etc-cni-netd", "/etc/cni/net.d/"},
		{"opt-cni-bin", "/opt/cni/bin/"},
	}
	out := make([]map[string]any, 0, len(mounts))
	for _, m := range mounts {
		out = append(out, map[string]any{
			"name":     m.name,
			"hostPath": map[string]any{"path": m.path},
		})
	}
	return out
}

func kubeBenchVolumeMounts() []map[string]any {
	mounts := []struct{ name, path string }{
		{"var-lib-etcd", "/var/lib/etcd"},
		{"var-lib-kubelet", "/var/lib/kubelet"},
		{"var-lib-kube-scheduler", "/var/lib/kube-scheduler"},
		{"var-lib-kube-controller-manager", "/var/lib/kube-controller-manager"},
		{"etc-systemd", "/etc/systemd"},
		{"lib-systemd", "/lib/systemd"},
		{"srv-kubernetes", "/srv/kubernetes"},
		{"etc-kubernetes", "/etc/kubernetes"},
		{"usr-bin", "/usr/local/mount-from-host/bin"},
		{"etc-cni-netd", "/etc/cni/net.d/"},
		{"opt-cni-bin", "/opt/cni/bin/"},
	}
	out := make([]map[string]any, 0, len(mounts))
	for _, m := range mounts {
		out = append(out, map[string]any{
			"name":      m.name,
			"mountPath": m.path,
			"readOnly":  true,
		})
	}
	return out
}
