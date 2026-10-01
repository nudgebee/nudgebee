package k8s

import (
	"fmt"
	"strings"
	"sync"

	"nudgebee/services/knowledge_graph/core"
)

// Declaring Kubernetes resources in a connected cluster by hand.
//
// Same contract as the cloud builders (sources/manual_node.go): the declared
// node is built from a synthetic k8s_workloads row by createNodeFromWorkload,
// the function the account build uses, so it gets the unique key and id the
// collector writes for the same workload and is adopted, not duplicated, when
// the cluster reports it. Services go through the same function: they are a
// workload kind too (service.go).

// k8sManualKinds are the declarable kinds. ReplicaSets belong to their
// Deployment, Jobs and Pods come and go too fast to be worth declaring.
var k8sManualKinds = []string{"Deployment", "StatefulSet", "DaemonSet", "CronJob", "Service"}

type k8sManualNodeBuilder struct {
	src *K8sSource

	once  sync.Once
	kinds []core.ManualNodeKind
	byST  map[string]string // specific type -> workload kind
}

func (b *k8sManualNodeBuilder) load() {
	b.once.Do(func() {
		b.byST = map[string]string{}
		for _, kind := range k8sManualKinds {
			declared := workloadKindFor(kind)
			if !containsType(core.ManualKubernetesNodeTypes, declared.NodeType) {
				continue
			}
			st := workloadSpecificType(kind)
			b.byST[st] = kind
			b.kinds = append(b.kinds, core.ManualNodeKind{
				SpecificType: st,
				NodeType:     declared.NodeType,
				KeyFields: []core.ManualNodeField{
					{Name: core.ManualNodeFieldName, Label: "Name", Hint: fmt.Sprintf("The %s's name, as kubectl shows it", kind), Required: true},
					{Name: core.ManualNodeFieldNamespace, Label: "Namespace", Hint: "The namespace it runs in", Required: true},
				},
				Properties: core.SchemaProperties(st),
			})
		}
	})
}

func containsType(list []core.NodeType, nt core.NodeType) bool {
	for _, t := range list {
		if t == nt {
			return true
		}
	}
	return false
}

// Kinds implements core.ManualNodeBuilder.
func (b *k8sManualNodeBuilder) Kinds() []core.ManualNodeKind {
	b.load()
	return b.kinds
}

// Build implements core.ManualNodeBuilder.
func (b *k8sManualNodeBuilder) Build(in core.ManualNodeInput) (*core.DbNode, error) {
	b.load()
	kind, ok := b.byST[in.SpecificType]
	if !ok {
		return nil, fmt.Errorf("resources of type %q cannot be declared in a Kubernetes account", in.SpecificType)
	}
	name, namespace := strings.TrimSpace(in.Name), strings.TrimSpace(in.Namespace)
	if name == "" {
		return nil, fmt.Errorf("name is required")
	}
	if namespace == "" {
		return nil, fmt.Errorf("namespace is required")
	}
	if in.Network != nil {
		return nil, fmt.Errorf("%s is not placed in a network; leave the network empty", in.SpecificType)
	}
	// No id: the change detector only proposes deletes for nodes whose
	// nb_resource_id is a real inventory row.
	row := &K8sWorkloadRow{
		Kind:        kind,
		Namespace:   namespace,
		Name:        name,
		ClusterName: in.AccountName,
		IsActive:    true,
		Meta:        []byte("{}"),
	}
	node := b.src.createNodeFromWorkload(row, nil, &core.SourceBuildRequest{TenantID: in.TenantID, CloudAccountID: in.CloudAccountID})
	if node == nil {
		return nil, fmt.Errorf("resources of type %q cannot be declared in a Kubernetes account", in.SpecificType)
	}
	return node, nil
}

func init() {
	src, _ := NewK8sSource(K8sSourceConfig{}, nil)
	core.RegisterManualNodeBuilder("K8s", &k8sManualNodeBuilder{src: src})
}
