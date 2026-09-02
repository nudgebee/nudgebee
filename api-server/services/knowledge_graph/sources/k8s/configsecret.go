package k8s

import (
	"context"
	"fmt"
	"sort"

	"nudgebee/services/knowledge_graph/core"
	"nudgebee/services/relay"
)

// Secret reference forms recorded on the USES_SECRET edge's `ref_kinds`
// property. "volume" is the form CIS 5.4.1 prefers; the two env forms expose
// the secret's values to anything that can read the container's environment.
const (
	secretRefKindVolume  = "volume"
	secretRefKindEnv     = "env"
	secretRefKindEnvFrom = "env_from"
)

// workloadSpecRefsKey returns the lookup key for a (kind, namespace, name)
// triple — same shape used elsewhere in this file.
func workloadSpecRefsKey(kind, namespace, name string) string {
	return fmt.Sprintf("%s/%s/%s", kind, namespace, name)
}

// fetchWorkloadConfigSecretRefs queries each workload-template-bearing kind
// from the relay (Deployments, StatefulSets, DaemonSets, ReplicaSets, Jobs,
// CronJobs) plus bare Pods, and returns a map keyed by "kind/namespace/name"
// with the ConfigMap and Secret names each workload's pod template references.
//
// The kg's existing k8s_workloads table strips volume.configMap /
// volume.secret / envFrom source info, only preserving the volume name and
// PVC refs. So the pod-template walk has to happen against the relay-
// returned spec, not against the workload row.
//
// One relay call per kind (apps/v1 for Deployments / StatefulSets /
// DaemonSets / ReplicaSets, batch/v1 for Jobs / CronJobs, core/v1 for Pods).
// Errors on individual kinds are logged and skipped so a single missing RBAC
// permission doesn't blank the whole map.
//
// The Pod fetch lists every pod cluster-wide — the heaviest call in this set —
// but only *ownerless* pods are kept (see the ownerReferences skip below), so
// pods managed by a controller cost a scan and nothing more.
func (s *K8sSource) fetchWorkloadConfigSecretRefs(ctx context.Context, req *core.SourceBuildRequest) map[string]workloadSpecRefs {
	result := make(map[string]workloadSpecRefs)
	if req.CloudAccountID == "" {
		return result
	}

	type kindFetch struct {
		Kind     string
		Group    string
		Resource string
	}
	fetches := []kindFetch{
		{"Deployment", "apps", "deployments"},
		{"StatefulSet", "apps", "statefulsets"},
		{"DaemonSet", "apps", "daemonsets"},
		{"ReplicaSet", "apps", "replicasets"},
		{"Job", "batch", "jobs"},
		{"CronJob", "batch", "cronjobs"},
		{"Pod", "", "pods"},
	}

	for _, f := range fetches {
		relayRequest := relay.RelayExecuteRequest{
			NoSinks: false,
			Cache:   false,
			Body: relay.ActionExecuteBody{
				AccountID:  req.CloudAccountID,
				ActionName: "get_resource",
				ActionParams: map[string]interface{}{
					"group":          f.Group,
					"version":        "v1",
					"resource_type":  f.Resource,
					"all_namespaces": true,
				},
			},
		}
		resp, err := relay.Execute(relayRequest)
		if err != nil {
			s.logger.Warn("workload-spec relay fetch failed",
				"kind", f.Kind, "error", err)
			continue
		}
		items, err := s.parseRelayDataArray(resp, f.Kind)
		if err != nil {
			s.logger.Warn("workload-spec relay parse failed",
				"kind", f.Kind, "error", err)
			continue
		}

		count := 0
		for _, raw := range items {
			obj, ok := raw.(map[string]interface{})
			if !ok {
				continue
			}
			md, _ := obj["metadata"].(map[string]interface{})
			if md == nil {
				continue
			}
			name, _ := md["name"].(string)
			namespace, _ := md["namespace"].(string)
			if name == "" || namespace == "" {
				continue
			}

			// Controller-managed pods are skipped: their refs are already
			// emitted against the owning ReplicaSet / Job / DaemonSet, so
			// keeping them here would duplicate every edge at pod level.
			// Only bare pods (kubectl run, static manifests) are unreachable
			// any other way, and those are exactly what we want.
			//
			// The skip is Pod-only on purpose. ReplicaSets are owned by
			// Deployments and Jobs by CronJobs, but both are first-class
			// workload nodes in the graph and have carried their own edges
			// since this map was introduced — filtering them would silently
			// delete existing edges.
			if f.Kind == "Pod" && hasOwnerReferences(md) {
				continue
			}

			// Spec → template → spec → {volumes, containers, init_containers}
			// CronJob nests one more layer: spec → jobTemplate → spec → template → spec
			// A bare Pod has no template — its spec *is* the pod spec.
			podSpec := s.extractPodTemplateSpec(obj, f.Kind)
			if podSpec == nil {
				continue
			}
			configs, secrets, secretRefKinds := s.refsFromPodSpec(podSpec)
			if len(configs) == 0 && len(secrets) == 0 {
				continue
			}
			result[workloadSpecRefsKey(f.Kind, namespace, name)] = workloadSpecRefs{
				ConfigMaps:     configs,
				Secrets:        secrets,
				SecretRefKinds: secretRefKinds,
			}
			count++
		}
		s.logger.Info("collected workload pod-template refs",
			"kind", f.Kind, "workloads_with_refs", count)
	}

	return result
}

// hasOwnerReferences reports whether an object's metadata carries a non-empty
// ownerReferences list — i.e. something else in the cluster created it.
// Accepts both snake_case and camelCase relay shapes.
func hasOwnerReferences(md map[string]interface{}) bool {
	for _, k := range []string{"owner_references", "ownerReferences"} {
		if refs, ok := md[k].([]interface{}); ok && len(refs) > 0 {
			return true
		}
	}
	return false
}

// extractPodTemplateSpec navigates the workload object to the pod spec.
// All apps/v1 + batch/v1.Job have spec.template.spec; CronJob nests it
// under spec.jobTemplate.spec.template.spec; a bare Pod's spec is already
// the pod spec. Returns nil when missing.
func (s *K8sSource) extractPodTemplateSpec(obj map[string]interface{}, kind string) map[string]interface{} {
	spec, _ := obj["spec"].(map[string]interface{})
	if spec == nil {
		return nil
	}
	if kind == "Pod" {
		return spec
	}
	if kind == "CronJob" {
		// Both snake_case ("job_template") and camelCase ("jobTemplate") in the wild.
		jt, _ := spec["job_template"].(map[string]interface{})
		if jt == nil {
			jt, _ = spec["jobTemplate"].(map[string]interface{})
		}
		if jt == nil {
			return nil
		}
		spec, _ = jt["spec"].(map[string]interface{})
		if spec == nil {
			return nil
		}
	}
	tmpl, _ := spec["template"].(map[string]interface{})
	if tmpl == nil {
		return nil
	}
	podSpec, _ := tmpl["spec"].(map[string]interface{})
	return podSpec
}

// refsFromPodSpec walks a pod spec and returns the de-duplicated ConfigMap
// and Secret names referenced via volumes[], containers[].envFrom[],
// containers[].env[].valueFrom, and the init_containers equivalents.
// Accepts both snake_case and camelCase relay shapes.
//
// secretRefKinds maps each returned Secret name to the sorted set of forms
// the spec used to consume it: "volume" (mounted as a file — the form CIS
// 5.4.1 prefers), "env" (a single key via env[].valueFrom.secretKeyRef), or
// "env_from" (every key at once via envFrom[].secretRef). One secret can
// appear under several. ConfigMaps get no equivalent — the distinction only
// carries security meaning for Secrets.
func (s *K8sSource) refsFromPodSpec(podSpec map[string]interface{}) (configNames, secretNames []string, secretRefKinds map[string][]string) {
	pickList := func(parent map[string]interface{}, keys ...string) []interface{} {
		for _, k := range keys {
			if v, ok := parent[k].([]interface{}); ok {
				return v
			}
		}
		return nil
	}
	pickMap := func(parent map[string]interface{}, keys ...string) map[string]interface{} {
		for _, k := range keys {
			if v, ok := parent[k].(map[string]interface{}); ok {
				return v
			}
		}
		return nil
	}
	pickString := func(parent map[string]interface{}, keys ...string) string {
		for _, k := range keys {
			if v, ok := parent[k].(string); ok && v != "" {
				return v
			}
		}
		return ""
	}

	configSet := make(map[string]struct{})
	secretSet := make(map[string]struct{})
	refKindSet := make(map[string]map[string]struct{})

	addConfig := func(name string) {
		if name != "" {
			configSet[name] = struct{}{}
		}
	}
	addSecret := func(name, refKind string) {
		if name == "" {
			return
		}
		secretSet[name] = struct{}{}
		if refKindSet[name] == nil {
			refKindSet[name] = make(map[string]struct{})
		}
		refKindSet[name][refKind] = struct{}{}
	}

	for _, vol := range pickList(podSpec, "volumes") {
		volMap, ok := vol.(map[string]interface{})
		if !ok {
			continue
		}
		if cm := pickMap(volMap, "config_map", "configMap"); cm != nil {
			addConfig(pickString(cm, "name"))
		}
		if sec := pickMap(volMap, "secret"); sec != nil {
			addSecret(pickString(sec, "secret_name", "secretName"), secretRefKindVolume)
		}
		// projected: { sources: [{ configMap: ..., secret: ... }] }
		if proj := pickMap(volMap, "projected"); proj != nil {
			for _, src := range pickList(proj, "sources") {
				srcMap, ok := src.(map[string]interface{})
				if !ok {
					continue
				}
				if cm := pickMap(srcMap, "config_map", "configMap"); cm != nil {
					addConfig(pickString(cm, "name"))
				}
				if sec := pickMap(srcMap, "secret"); sec != nil {
					addSecret(pickString(sec, "name"), secretRefKindVolume)
				}
			}
		}
	}

	walkContainers := func(containers []interface{}) {
		for _, c := range containers {
			cm, ok := c.(map[string]interface{})
			if !ok {
				continue
			}
			for _, ef := range pickList(cm, "env_from", "envFrom") {
				efm, ok := ef.(map[string]interface{})
				if !ok {
					continue
				}
				if cmRef := pickMap(efm, "config_map_ref", "configMapRef"); cmRef != nil {
					addConfig(pickString(cmRef, "name"))
				}
				if sRef := pickMap(efm, "secret_ref", "secretRef"); sRef != nil {
					addSecret(pickString(sRef, "name"), secretRefKindEnvFrom)
				}
			}
			for _, e := range pickList(cm, "env") {
				em, ok := e.(map[string]interface{})
				if !ok {
					continue
				}
				vf := pickMap(em, "value_from", "valueFrom")
				if vf == nil {
					continue
				}
				if cmRef := pickMap(vf, "config_map_key_ref", "configMapKeyRef"); cmRef != nil {
					addConfig(pickString(cmRef, "name"))
				}
				if sRef := pickMap(vf, "secret_key_ref", "secretKeyRef"); sRef != nil {
					addSecret(pickString(sRef, "name"), secretRefKindEnv)
				}
			}
		}
	}
	walkContainers(pickList(podSpec, "containers"))
	walkContainers(pickList(podSpec, "init_containers", "initContainers"))

	configNames = make([]string, 0, len(configSet))
	for n := range configSet {
		configNames = append(configNames, n)
	}
	secretNames = make([]string, 0, len(secretSet))
	for n := range secretSet {
		secretNames = append(secretNames, n)
	}
	secretRefKinds = make(map[string][]string, len(refKindSet))
	for name, kinds := range refKindSet {
		out := make([]string, 0, len(kinds))
		for k := range kinds {
			out = append(out, k)
		}
		// Sorted so the edge property is stable across builds — an unsorted
		// map walk would rewrite the same edge with reordered values every
		// hour and churn knowledge_graph_edge for no reason.
		sort.Strings(out)
		secretRefKinds[name] = out
	}
	return configNames, secretNames, secretRefKinds
}

// createWorkloadConfigSecretEdges emits Workload → USES_CONFIG → ConfigMap
// and Workload → USES_SECRET → K8sSecret edges. The refs map is built once
// per build by fetchWorkloadConfigSecretRefs (relay round-trip per workload
// kind). Silently skips references to ConfigMaps / Secrets that aren't in
// the per-build lookup — e.g. cross-namespace mounts or auto-mounted SA
// tokens whose target object lives in kube-system.
func (s *K8sSource) createWorkloadConfigSecretEdges(
	workloads []K8sWorkloadRow,
	workloadNodes map[string]*core.DbNode,
	configMapByKey, secretByKey map[string]*core.DbNode,
	specRefs map[string]workloadSpecRefs,
	req *core.SourceBuildRequest,
) []*core.DbEdge {
	if len(workloadNodes) == 0 {
		return nil
	}
	if len(configMapByKey) == 0 && len(secretByKey) == 0 {
		return nil
	}
	if len(specRefs) == 0 {
		return nil
	}
	edges := make([]*core.DbEdge, 0)
	for i := range workloads {
		w := &workloads[i]
		workloadKey := fmt.Sprintf("%s/%s/%s/%s", w.ClusterName, w.Kind, w.Namespace, w.Name)
		wNode, ok := workloadNodes[workloadKey]
		if !ok || wNode == nil {
			continue
		}
		refs, ok := specRefs[workloadSpecRefsKey(w.Kind, w.Namespace, w.Name)]
		if !ok {
			continue
		}
		for _, cmName := range refs.ConfigMaps {
			cmNode, ok := configMapByKey[fmt.Sprintf("%s/%s", w.Namespace, cmName)]
			if !ok {
				continue
			}
			edges = append(edges, core.NewEdge(
				wNode.ID, cmNode.ID,
				core.RelationshipUsesConfig,
				map[string]interface{}{"connection_type": "configmap", "configmap_name": cmName},
				req.TenantID, req.CloudAccountID, "k8s",
			))
		}
		for _, secName := range refs.Secrets {
			sNode, ok := secretByKey[fmt.Sprintf("%s/%s", w.Namespace, secName)]
			if !ok {
				continue
			}
			props := map[string]interface{}{"connection_type": "secret", "secret_name": secName}
			// ref_kinds answers "how is this secret consumed" — the
			// question a security reviewer asks of a Secret→workload link.
			// Omitted rather than set empty when unknown, so pre-existing
			// edges and new ones stay distinguishable.
			if kinds := refs.SecretRefKinds[secName]; len(kinds) > 0 {
				props["ref_kinds"] = kinds
			}
			edges = append(edges, core.NewEdge(
				wNode.ID, sNode.ID,
				core.RelationshipUsesSecret,
				props,
				req.TenantID, req.CloudAccountID, "k8s",
			))
		}
	}
	return edges
}
