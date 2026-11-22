package test

import (
	"testing"

	"nudgebee/services/knowledge_graph/core"
)

// The declare form offers each real type's fields in the shape its collector
// stores them, and never a field only the collector can know. Runs in package
// test, where every source's schemas are registered.
func TestManualNodeSchema_RealTypes(t *testing.T) {
	offered := func(specificType string) map[string]core.PropertyShape {
		out := map[string]core.PropertyShape{}
		for _, p := range core.SchemaProperties(specificType) {
			out[p.Name] = p.Shape
		}
		return out
	}
	cases := []struct {
		specificType string
		want         map[string]core.PropertyShape
		hidden       []string
	}{
		{
			specificType: "KubernetesDeployment",
			want: map[string]core.PropertyShape{
				"annotations": core.PropertyShapeMap, "container_images": core.PropertyShapeList,
				"container_names": core.PropertyShapeList, "replicas": core.PropertyShapeScalar,
			},
			hidden: []string{"k8s_workload_id", "kind", "created_at", "phase", "ready_replicas", "container_count", "last_deployed_time"},
		},
		{
			specificType: "KubernetesService",
			want: map[string]core.PropertyShape{
				"annotations": core.PropertyShapeMap, "selector": core.PropertyShapeMap,
				"external_ips": core.PropertyShapeList, "service_type": core.PropertyShapeScalar,
			},
			hidden: []string{"ports", "cluster_ip", "cluster_ips", "node_ports", "uid", "creation_timestamp"},
		},
		{
			specificType: "ALBLoadBalancer",
			want:         map[string]core.PropertyShape{"subnets": core.PropertyShapeList, "security_groups": core.PropertyShapeList},
			hidden:       []string{"availability_zone", "instances", "is_public_entry", "created_time"},
		},
		{
			specificType: "EC2Instance",
			want:         map[string]core.PropertyShape{"instance_type": core.PropertyShapeScalar},
			hidden:       []string{"security_groups", "instance_state", "launch_time"},
		},
		{
			specificType: "RDSInstance",
			want:         map[string]core.PropertyShape{"subnet_ids": core.PropertyShapeList, "vpc_security_group_ids": core.PropertyShapeList},
		},
	}
	for _, c := range cases {
		got := offered(c.specificType)
		for name, shape := range c.want {
			if s, ok := got[name]; !ok || s != shape {
				t.Errorf("%s: %s offered as %q (offered: %v), want %q", c.specificType, name, s, ok, shape)
			}
		}
		for _, name := range c.hidden {
			if _, ok := got[name]; ok {
				t.Errorf("%s: %s must not be offered", c.specificType, name)
			}
		}
	}
}
