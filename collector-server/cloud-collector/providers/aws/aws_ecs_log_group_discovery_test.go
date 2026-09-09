package aws

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// ECS log-group auto-discovery read only containerDefinitions[0]. A task whose
// first container is a sidecar with no awslogs driver reported "failed to
// auto-discover log group name from task definition ..." and the event got no
// logs at all — the whole reason ECS events have never carried a single log
// line. The parser now scans every container definition.

func awslogsContainer(group string) map[string]any {
	return map[string]any{
		"LogConfiguration": map[string]any{
			"LogDriver": "awslogs",
			"Options":   map[string]any{"awslogs-group": group},
		},
	}
}

func TestParseLogGroupFromTaskDefMeta(t *testing.T) {
	tests := []struct {
		name string
		meta map[string]any
		want string
	}{
		{
			name: "first container carries the awslogs group",
			meta: map[string]any{"ContainerDefinitions": []any{
				awslogsContainer("/ecs/nudgebee-api"),
			}},
			want: "/ecs/nudgebee-api",
		},
		{
			name: "sidecar first, app container second",
			meta: map[string]any{"ContainerDefinitions": []any{
				map[string]any{"Name": "envoy-proxy"},
				awslogsContainer("/ecs/nudgebee-api"),
			}},
			want: "/ecs/nudgebee-api",
		},
		{
			name: "first container logs to a non-awslogs driver",
			meta: map[string]any{"ContainerDefinitions": []any{
				map[string]any{"LogConfiguration": map[string]any{
					"LogDriver": "awsfirelens",
					"Options":   map[string]any{"Name": "datadog"},
				}},
				awslogsContainer("/ecs/nudgebee-api"),
			}},
			want: "/ecs/nudgebee-api",
		},
		{
			name: "first match wins when several containers declare a group",
			meta: map[string]any{"ContainerDefinitions": []any{
				awslogsContainer("/ecs/first"),
				awslogsContainer("/ecs/second"),
			}},
			want: "/ecs/first",
		},
		{
			name: "no container declares an awslogs group",
			meta: map[string]any{"ContainerDefinitions": []any{
				map[string]any{"Name": "envoy-proxy"},
			}},
			want: "",
		},
		{
			name: "empty awslogs-group is not a match",
			meta: map[string]any{"ContainerDefinitions": []any{
				awslogsContainer(""),
			}},
			want: "",
		},
		{
			name: "no container definitions at all",
			meta: map[string]any{"ContainerDefinitions": []any{}},
			want: "",
		},
		{
			name: "ContainerDefinitions missing",
			meta: map[string]any{},
			want: "",
		},
		{
			name: "ContainerDefinitions is not a list",
			meta: map[string]any{"ContainerDefinitions": "nope"},
			want: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, parseLogGroupFromTaskDefMeta(tt.meta))
		})
	}
}
