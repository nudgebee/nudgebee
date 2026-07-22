package scan_orchestrator

import (
	"strings"
	"testing"
)

// nova has no --force-exit-zero and calls klog.Exit on ANY error (including a
// transient K8s "list: stream error ... closed connection" while listing
// helm-release Secrets). With the agent-enforced BackoffLimit=0, that non-zero
// exit marks the Job Failed and fires a customer-facing job_failure event for a
// retryable internal scan. The spec must wrap nova so the pod always exits 0;
// a transient failure then surfaces as an empty-stdout "no output" scan, not a
// Failed Job. Lock the shape so a refactor can't silently reintroduce the bug.
func TestHelmChartUpgradeSpec_ExitsZeroOnNovaFailure(t *testing.T) {
	spec := ScannerCatalog["helm_chart_upgrade"].BuildSpec(ScanAccount{
		AccountID: "acc",
		TenantID:  "ten",
	}, nil)

	// Must run through a shell so nova's non-zero exit can be swallowed.
	if len(spec.Command) < 1 || (spec.Command[0] != "/bin/sh" && spec.Command[0] != "sh") {
		t.Fatalf("Command = %v; want a shell wrapper (/bin/sh -c) so nova's exit code can be forced to 0", spec.Command)
	}

	cmd := strings.Join(spec.Command, " ") + " " + strings.Join(spec.Args, " ")

	// Still runs `nova find`.
	if !strings.Contains(cmd, "nova find") {
		t.Errorf("Command/Args = %q; want it to still run `nova find`", cmd)
	}

	// Forces a zero exit regardless of nova's result. Accept either the explicit
	// `; exit 0` we ship or an `|| true` equivalent.
	if !strings.Contains(cmd, "exit 0") && !strings.Contains(cmd, "|| true") {
		t.Errorf("Command/Args = %q; want the shell to force a 0 exit (`; exit 0` or `|| true`) "+
			"so a transient nova failure does not mark the Job Failed", cmd)
	}
}
