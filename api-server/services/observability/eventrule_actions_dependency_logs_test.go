package observability

import (
	"log/slog"
	"testing"

	"nudgebee/services/eventrule/playbooks"

	"github.com/stretchr/testify/assert"
)

func TestResolveCallee(t *testing.T) {
	// Labels taken verbatim from a dev OtelDemoGRPCClientErrorRate event: the
	// alert is about flagd, every enricher ran against fraud-detection, and not
	// one of the 934 log payloads collected for this alert class mentioned flagd.
	otelDemo := map[string]string{
		"rpc_service":         "flagd.evaluation.v2.Service",
		"rpc_method":          "EventStream",
		"downstream_services": "[kafka flagd kube-dns]",
		"target_service":      "fraud-detection",
	}

	tests := []struct {
		name  string
		event playbooks.PlaybookEvent
		want  string
	}{
		{"grpc client alert resolves the callee",
			playbooks.PlaybookEvent{Labels: otelDemo, SubjectOwner: "fraud-detection"}, "flagd"},
		{"no labels at all",
			playbooks.PlaybookEvent{}, ""},
		{"callee that is not a known downstream is not queried",
			playbooks.PlaybookEvent{Labels: map[string]string{
				"rpc_service":         "unrelated.v1.Service",
				"downstream_services": "[kafka flagd]",
			}}, ""},
		{"callee equal to the subject is just the workload query that already ran",
			playbooks.PlaybookEvent{Labels: map[string]string{"rpc_service": "fraud-detection.v1.Service"},
				SubjectOwner: "fraud-detection"}, ""},
		{"bare callee label with no knowledge-graph list still resolves",
			playbooks.PlaybookEvent{Labels: map[string]string{"callee": "redis-master"},
				SubjectOwner: "cart"}, "redis-master"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, resolveCallee(tc.event))
		})
	}
}

func TestParseServiceListLabel(t *testing.T) {
	assert.Equal(t, map[string]bool{"kafka": true, "flagd": true, "kube-dns": true},
		parseServiceListLabel("[kafka flagd kube-dns]"))
	assert.Equal(t, map[string]bool{"a": true, "b": true}, parseServiceListLabel("a, b"))
	assert.Empty(t, parseServiceListLabel("[]"))
	assert.Empty(t, parseServiceListLabel(""))
}

func TestImagePullBackoffDoesNotGetWorkloadLogs(t *testing.T) {
	// The failing pod never started a container, so a workload-scoped query can
	// only return the healthy replicas still serving the old image: 130 of 133
	// such payloads on dev contained no reference to the failing pod.
	assert.True(t, noWorkloadLogAggKeys["image_pull_backoff_reporter"])
	assert.False(t, noWorkloadLogAggKeys["report_crash_loop"])
}

func TestFetchCalleeLogsViaKubectlRefusesNonK8sTargets(t *testing.T) {
	// The agent fallback interpolates the callee into a kubectl command, so it
	// must screen the same way the subject path does: a CI "namespace" like
	// "nudgebee/nudgebee-enterprise" is not a Kubernetes object.
	a := &dependencyLogsAction{}
	ctx := playbooks.NewPlaybookActionContext("t", "acct", slog.Default(), playbooks.PlaybookEvent{})
	for _, tc := range []struct{ callee, ns string }{
		{"cart", "nudgebee/nudgebee-enterprise"},
		{"a b", "demo"},
		{"", "demo"},
		{"cart", ""},
	} {
		resp, err := a.fetchCalleeLogsViaKubectl(ctx, tc.callee, tc.ns)
		assert.Nil(t, resp, "%s/%s", tc.ns, tc.callee)
		assert.NoError(t, err)
	}
}

func TestFetchCalleeLogsViaKubectlSkipsCloudSources(t *testing.T) {
	// Cloud accounts have no K8s agent to ask.
	a := &dependencyLogsAction{}
	ctx := playbooks.NewPlaybookActionContext("t", "acct", slog.Default(),
		playbooks.PlaybookEvent{Source: "aws_cloudwatch_webhook"})
	resp, err := a.fetchCalleeLogsViaKubectl(ctx, "cart", "demo")
	assert.Nil(t, resp)
	assert.NoError(t, err)
}
