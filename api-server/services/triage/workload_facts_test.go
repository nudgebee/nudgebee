package triage

import (
	"strings"
	"testing"

	"nudgebee/services/internal/database/models"
)

func TestParseWorkloadLabels(t *testing.T) {
	t.Run("standard app labels + declared tier", func(t *testing.T) {
		var f workloadFacts
		parseWorkloadLabels(`{"app.kubernetes.io/name":"checkout","app.kubernetes.io/part-of":"shop","app.kubernetes.io/component":"api","tier":"1"}`, &f)
		if f.appName != "checkout" || f.partOf != "shop" || f.component != "api" {
			t.Fatalf("unexpected identity labels: %+v", f)
		}
		if f.declaredTier != "tier=1" {
			t.Fatalf("declaredTier = %q, want tier=1", f.declaredTier)
		}
	})

	t.Run("falls back to bare app label", func(t *testing.T) {
		var f workloadFacts
		parseWorkloadLabels(`{"app":"legacy"}`, &f)
		if f.appName != "legacy" {
			t.Fatalf("appName = %q, want legacy", f.appName)
		}
	})

	t.Run("empty / malformed is safe", func(t *testing.T) {
		var f workloadFacts
		parseWorkloadLabels("", &f)
		parseWorkloadLabels("not json", &f)
		if f.appName != "" || f.declaredTier != "" {
			t.Fatalf("expected zero facts, got %+v", f)
		}
	})
}

func TestRenderWorkloadFacts(t *testing.T) {
	t.Run("with graph facts", func(t *testing.T) {
		var b strings.Builder
		renderWorkloadFacts(&b, workloadFacts{
			found: true, graphKnown: true, kind: "Deployment", image: "postgres:17.6",
			appName: "postgresql", customerFacing: true, fanIn: 5,
		})
		out := b.String()
		for _, want := range []string{
			"workload_kind: Deployment",
			"image: postgres:17.6",
			"app=postgresql",
			"customer_facing(observed ingress/LB-backed): true",
			"dependency_fan_in(observed services depending on it): 5",
		} {
			if !strings.Contains(out, want) {
				t.Errorf("rendered facts missing %q\n%s", want, out)
			}
		}
	})

	t.Run("no graph => topology lines omitted, not shown as false/zero", func(t *testing.T) {
		var b strings.Builder
		renderWorkloadFacts(&b, workloadFacts{found: true, graphKnown: false, kind: "Deployment"})
		out := b.String()
		if strings.Contains(out, "customer_facing") || strings.Contains(out, "dependency_fan_in") {
			t.Errorf("topology lines must be omitted when graph unknown\n%s", out)
		}
	})

	t.Run("not found => nothing rendered", func(t *testing.T) {
		var b strings.Builder
		renderWorkloadFacts(&b, workloadFacts{found: false})
		if b.String() != "" {
			t.Errorf("expected empty render, got %q", b.String())
		}
	})
}

func TestBuildEventContextIncludesFacts(t *testing.T) {
	s := func(v string) *string { return &v }
	ev := &models.Event{
		Title:            "PostgreSQL down",
		AggregationKey:   s("KubePodCrashLooping"),
		SubjectOwner:     s("postgresql"),
		SubjectNamespace: s("demo"),
	}
	facts := workloadFacts{found: true, graphKnown: true, kind: "Deployment", image: "postgres:17.6", customerFacing: false, fanIn: 4}
	out := buildEventContext(ev, facts)
	if !strings.Contains(out, "aggregation_key: KubePodCrashLooping") {
		t.Errorf("missing base alert context:\n%s", out)
	}
	if !strings.Contains(out, "dependency_fan_in(observed services depending on it): 4") {
		t.Errorf("missing workload facts:\n%s", out)
	}
}

// TestBuildEventContextOmitsCluster guards the cross-cluster reasoning-leak fix: the class key has
// no cluster component, so a verdict minted from one cluster is served to every cluster in the
// tenant. The concrete cluster name must NOT be fed to the classifier (it previously got echoed
// into the cached reasoning and surfaced verbatim in a different cluster's RCA). The namespace,
// which IS part of the class key, must still be present.
func TestBuildEventContextOmitsCluster(t *testing.T) {
	s := func(v string) *string { return &v }
	ev := &models.Event{
		Title:            "Job nudgebee-agent/popeye-scan-00947d98 failed",
		AggregationKey:   s("job_failure"),
		SubjectNamespace: s("nudgebee-agent"),
		Cluster:          s("k8s-dev"),
	}
	out := buildEventContext(ev, workloadFacts{})
	if strings.Contains(out, "k8s-dev") {
		t.Errorf("cluster identity must not appear in classifier context (cross-cluster verdict cache):\n%s", out)
	}
	if !strings.Contains(out, "namespace nudgebee-agent") {
		t.Errorf("namespace (part of the class key) must remain in context:\n%s", out)
	}
}
