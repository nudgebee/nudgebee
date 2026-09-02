package api

import (
	"testing"

	"nudgebee/llm/events"
	"nudgebee/llm/security"

	"github.com/stretchr/testify/assert"
)

func TestResolveEventWorkload(t *testing.T) {
	ctx := security.NewRequestContextForSuperAdmin()

	tests := []struct {
		name          string
		event         events.Event
		parsedLabels  map[string]any
		wantNamespace string
		wantWorkload  string
	}{
		{
			// Regression: pod events always carry triage labels, so the
			// subject_owner fallback in parsedLabels never fires — SubjectOwner
			// must be consulted directly when the owner kind is a stable workload.
			name: "pod owned by deployment resolves via subject owner",
			event: events.Event{
				SubjectType:      "pod",
				SubjectName:      "llm-gateway-67cd44c5b4-l7j5v",
				SubjectNamespace: "nudgebee",
				SubjectOwner:     "llm-gateway",
				SubjectOwnerKind: "deployment",
			},
			parsedLabels:  map[string]any{"repo_url": "https://github.com/org/repo"},
			wantNamespace: "nudgebee",
			wantWorkload:  "llm-gateway",
		},
		{
			name: "pod owned by statefulset resolves via subject owner",
			event: events.Event{
				SubjectType:      "pod",
				SubjectNamespace: "db",
				SubjectOwner:     "postgres",
				SubjectOwnerKind: "StatefulSet",
			},
			wantNamespace: "db",
			wantWorkload:  "postgres",
		},
		{
			name: "pod owned by job resolves via subject owner",
			event: events.Event{
				SubjectType:      "pod",
				SubjectNamespace: "batch",
				SubjectOwner:     "nightly-sync",
				SubjectOwnerKind: "job",
			},
			wantNamespace: "batch",
			wantWorkload:  "nightly-sync",
		},
		{
			name: "pod owned by replicaset is not trusted",
			event: events.Event{
				SubjectType:      "pod",
				SubjectNamespace: "nudgebee",
				SubjectOwner:     "llm-gateway-67cd44c5b4",
				SubjectOwnerKind: "replicaset",
			},
			wantNamespace: "",
			wantWorkload:  "",
		},
		{
			name: "pod with empty owner kind is not trusted",
			event: events.Event{
				SubjectType:      "pod",
				SubjectNamespace: "nudgebee",
				SubjectOwner:     "llm-gateway-67cd44c5b4",
			},
			wantNamespace: "",
			wantWorkload:  "",
		},
		{
			name: "non-pod subject uses owner without kind check",
			event: events.Event{
				SubjectType:      "replicaset",
				SubjectNamespace: "nudgebee",
				SubjectOwner:     "api-server",
			},
			wantNamespace: "nudgebee",
			wantWorkload:  "api-server",
		},
		{
			name: "deployment subject uses its own name",
			event: events.Event{
				SubjectType:      "deployment",
				SubjectName:      "api-server",
				SubjectNamespace: "nudgebee",
			},
			wantNamespace: "nudgebee",
			wantWorkload:  "api-server",
		},
		{
			name: "untrusted pod owner falls back to parsed labels",
			event: events.Event{
				SubjectType:      "pod",
				SubjectNamespace: "nudgebee",
				SubjectOwner:     "llm-gateway-67cd44c5b4",
				SubjectOwnerKind: "replicaset",
			},
			parsedLabels: map[string]any{
				"subject_owner":     "llm-gateway",
				"subject_namespace": "nudgebee",
			},
			wantNamespace: "nudgebee",
			wantWorkload:  "llm-gateway",
		},
		{
			name:  "app_id label resolves namespace and workload",
			event: events.Event{SubjectType: "pod"},
			parsedLabels: map[string]any{
				"labels": map[string]any{"app_id": "/k8s/nudgebee/llm-gateway"},
			},
			wantNamespace: "nudgebee",
			wantWorkload:  "llm-gateway",
		},
		{
			name:          "nothing resolvable returns empty",
			event:         events.Event{SubjectType: "pod", SubjectName: "orphan-pod"},
			parsedLabels:  map[string]any{"some_label": "value"},
			wantNamespace: "",
			wantWorkload:  "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			namespace, workload := resolveEventWorkload(ctx, tt.event, tt.parsedLabels)
			assert.Equal(t, tt.wantNamespace, namespace)
			assert.Equal(t, tt.wantWorkload, workload)
		})
	}
}
