package flow_sources

import (
	"log/slog"
	"strings"
	"testing"

	"nudgebee/services/knowledge_graph/core"
)

// TestTracesInferNodeType_KindCasingInsensitive pins the casing contract.
//
// The traces kind lookup used to be case-sensitive while eBPF's was not, so a
// kind in any casing other than PascalCase fell through to NodeTypeService —
// silently typing a Deployment as a logical service with nothing in the
// classification recording that it had happened. No producer sends other
// casing today (applicationKindFor returns only "Node"/"Service", and the
// Datadog path reads k8s_workloads.kind, which is PascalCase in every row), so
// this is a latent trap rather than a live defect, and the test is what keeps
// it closed.
func TestTracesInferNodeType_KindCasingInsensitive(t *testing.T) {
	s := NewTracesFlowSource(slog.Default())

	expected := map[string]core.NodeType{
		"Service":         core.NodeTypeService,
		"Deployment":      core.NodeTypeWorkload,
		"StatefulSet":     core.NodeTypeWorkload,
		"DaemonSet":       core.NodeTypeWorkload,
		"Pod":             core.NodeTypeWorkload,
		"Runner":          core.NodeTypeWorkload,
		"Database":        core.NodeTypeDatabase,
		"ExternalService": core.NodeTypeExternalService,
		"Node":            core.NodeTypeNode,
	}

	for canonical, want := range expected {
		for _, variant := range []string{canonical, strings.ToLower(canonical), strings.ToUpper(canonical)} {
			got, _ := s.inferNodeType(variant, nil)
			if got != want {
				t.Errorf("inferNodeType(%q) = %s, want %s — casing must not change the classification",
					variant, got, want)
			}
		}
	}

	// An unknown kind still defaults to the logical service type, whatever its
	// casing. That default is load-bearing: matching probes Workload and
	// K8sService before anything is materialised.
	for _, unknown := range []string{"Sidecar", "sidecar", "SIDECAR", ""} {
		if got, _ := s.inferNodeType(unknown, nil); got != core.NodeTypeService {
			t.Errorf("inferNodeType(%q) = %s, want %s for an unknown kind", unknown, got, core.NodeTypeService)
		}
	}

	// The appType override is skipped for an external service so an observed
	// external endpoint is not reclassified as a Database by a stray tag. That
	// guard reads the same normalised kind as the lookup, so casing must not
	// let an external service slip into it.
	for _, variant := range []string{"ExternalService", "externalservice", "EXTERNALSERVICE"} {
		got, matched := s.inferNodeType(variant, []string{"postgres"})
		if got != core.NodeTypeExternalService {
			t.Errorf("inferNodeType(%q, [postgres]) = %s, want %s — the appType override must not "+
				"apply to an external service", variant, got, core.NodeTypeExternalService)
		}
		if matched != "" {
			t.Errorf("inferNodeType(%q, [postgres]) reported matched appType %q, want none", variant, matched)
		}
	}

	// A non-external kind still gets the appType override, in any casing.
	if got, matched := s.inferNodeType("deployment", []string{"Redis"}); got != core.NodeTypeCache || matched != "Redis" {
		t.Errorf("inferNodeType(deployment, [Redis]) = (%s, %q), want (%s, \"Redis\")",
			got, matched, core.NodeTypeCache)
	}
}
