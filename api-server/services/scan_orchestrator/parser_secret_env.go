package scan_orchestrator

import (
	"encoding/json"
	"fmt"
	"sort"
)

// SecretEnvExposureRuleName is the rule_name the UI keys on. New rule (no
// Robusta/collector ancestor), so the name follows the repo's snake_case
// convention rather than a legacy label.
const SecretEnvExposureRuleName = "secret_env_exposure"

// SecretEnvExposure is one workload's worth of Secret-as-environment-variable
// findings, already rolled up from its pods.
type SecretEnvExposure struct {
	Namespace string
	Kind      string
	Name      string
	// Findings is the recommendation payload shape KubernetesBestPractices.jsx
	// renders: an array of {namespace, kind, name, container, message, ...}.
	// buildRow reads namespace/kind/name for the table columns and renders
	// `message` + `container` in the Recommendation cell.
	Findings []map[string]any
}

// secretEnvRef is one env var sourced from a Secret. Secret *values* are never
// read — only the Secret's name and the key being referenced.
type secretEnvRef struct {
	Container string
	EnvVar    string // empty for envFrom (the whole Secret is projected)
	Secret    string
	Key       string // empty for envFrom
	Source    string // "env" | "env_from"
}

// IdentifySecretEnvExposures walks every pod spec for Secrets consumed as
// environment variables — `env[].valueFrom.secretKeyRef` and
// `envFrom[].secretRef`, across containers and initContainers — and rolls the
// findings up to the pod's owning workload.
//
// Roll-up matters: without it a 3-replica Deployment produces three rows whose
// account_object_ids churn on every rollout (pod names carry a fresh hash), so
// the table would fill with duplicates that never stabilise. Pods owned by a
// ReplicaSet are resolved one hop further to the owning Deployment, which is
// why replicaSets is fetched alongside pods. Ownerless pods (`kubectl run`, a
// static manifest) report against themselves — they are exactly the case the
// Knowledge Graph misses today (#35872).
//
// Both inputs are the snake-cased lists the agent's get_resource returns;
// camelCase is tolerated for older agent builds without SnakeKeysDeep, same as
// IdentifyUnusedPVs.
func IdentifySecretEnvExposures(pods, replicaSets []map[string]any) []SecretEnvExposure {
	// ReplicaSet "<ns>/<name>" → its own owner, so pod → RS → Deployment
	// resolves in one lookup.
	rsOwner := map[string][2]string{} // value = {kind, name}
	for _, rs := range replicaSets {
		md := getMapField(rs, "metadata")
		if md == nil {
			continue
		}
		ns, _ := md["namespace"].(string)
		name, _ := md["name"].(string)
		if name == "" {
			continue
		}
		if kind, owner := firstOwnerRef(md); owner != "" {
			rsOwner[fmt.Sprintf("%s/%s", ns, name)] = [2]string{kind, owner}
		}
	}

	grouped := map[string]*SecretEnvExposure{}
	// Replicas of the same workload yield identical findings; dedupe on the
	// rendered message so the Recommendation cell doesn't repeat itself.
	seenFinding := map[string]bool{}

	for _, pod := range pods {
		md := getMapField(pod, "metadata")
		if md == nil {
			continue
		}
		ns, _ := md["namespace"].(string)
		podName, _ := md["name"].(string)
		if podName == "" {
			continue
		}
		if isTerminalPodPhase(pod) {
			continue
		}
		spec := getMapField(pod, "spec")
		if spec == nil {
			continue
		}
		refs := secretEnvRefsFromPodSpec(spec)
		if len(refs) == 0 {
			continue
		}

		kind, name := resolveWorkloadOwner(md, ns, podName, rsOwner)
		key := formatAccountObjectID(ns, kind, name)
		entry, ok := grouped[key]
		if !ok {
			entry = &SecretEnvExposure{Namespace: ns, Kind: kind, Name: name}
			grouped[key] = entry
		}
		for _, r := range refs {
			message := secretEnvMessage(r)
			dedupeKey := key + "|" + r.Container + "|" + message
			if seenFinding[dedupeKey] {
				continue
			}
			seenFinding[dedupeKey] = true
			entry.Findings = append(entry.Findings, map[string]any{
				"namespace":   ns,
				"kind":        kind,
				"name":        name,
				"container":   r.Container,
				"message":     message,
				"secret_name": r.Secret,
				"secret_key":  r.Key,
				"env_var":     r.EnvVar,
				"source":      r.Source,
			})
		}
	}

	// Map iteration is random; sort so a re-scan with unchanged cluster state
	// produces byte-identical payloads and the UPSERT is a genuine no-op.
	keys := make([]string, 0, len(grouped))
	for k := range grouped {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := make([]SecretEnvExposure, 0, len(keys))
	for _, k := range keys {
		e := grouped[k]
		sort.Slice(e.Findings, func(i, j int) bool {
			mi, _ := e.Findings[i]["message"].(string)
			mj, _ := e.Findings[j]["message"].(string)
			if mi != mj {
				return mi < mj
			}
			ci, _ := e.Findings[i]["container"].(string)
			cj, _ := e.Findings[j]["container"].(string)
			return ci < cj
		})
		out = append(out, *e)
	}
	return out
}

// isTerminalPodPhase reports whether a pod has finished. A Succeeded/Failed pod
// has no running container, so the environment variable no longer exists
// anywhere — the exposure is historical, not live. Kubernetes rarely garbage
// collects terminated pods (terminated-pod-gc-threshold defaults to 12500), so
// without this filter a one-shot pod would sit in the findings table
// indefinitely.
//
// A pod with no status yet (or an unrecognised phase) is reported: better a
// findable row than a silent skip.
func isTerminalPodPhase(pod map[string]any) bool {
	status := getMapField(pod, "status")
	if status == nil {
		return false
	}
	phase, _ := status["phase"].(string)
	return phase == "Succeeded" || phase == "Failed"
}

// resolveWorkloadOwner maps a pod to the workload that should carry the
// finding. ReplicaSet owners are followed one hop to their own owner
// (Deployment) when known; every other owner kind is reported directly, and an
// ownerless pod reports as itself.
func resolveWorkloadOwner(podMeta map[string]any, namespace, podName string, rsOwner map[string][2]string) (kind, name string) {
	ownerKind, ownerName := firstOwnerRef(podMeta)
	if ownerName == "" {
		return "Pod", podName
	}
	if ownerKind == "ReplicaSet" {
		if up, ok := rsOwner[fmt.Sprintf("%s/%s", namespace, ownerName)]; ok {
			return up[0], up[1]
		}
	}
	return ownerKind, ownerName
}

// firstOwnerRef returns the owning controller's kind and name. A pod may carry
// several ownerReferences (service meshes, backup tools and custom operators
// all add them), but at most one has controller=true and only that one names
// the workload the pod actually belongs to — so prefer it, and fall back to the
// first named reference when nothing is marked as the controller.
func firstOwnerRef(meta map[string]any) (kind, name string) {
	refs := getListField(meta, "owner_references", "ownerReferences")
	var fallbackKind, fallbackName string
	for _, o := range refs {
		om, ok := o.(map[string]any)
		if !ok || om == nil {
			continue
		}
		k, _ := om["kind"].(string)
		n, _ := om["name"].(string)
		if n == "" {
			continue
		}
		if isController, _ := om["controller"].(bool); isController {
			return k, n
		}
		if fallbackName == "" {
			fallbackKind, fallbackName = k, n
		}
	}
	return fallbackKind, fallbackName
}

// secretEnvRefsFromPodSpec collects every Secret-sourced env var in a pod spec.
// Mirrors the reference walk in the Knowledge Graph's refsFromPodSpec
// (knowledge_graph/sources/k8s/configsecret.go), but keeps the consumption
// detail the graph edge throws away — which container, which env var, which
// key — because that detail is the whole point of the finding.
//
// Volume-mounted Secrets are deliberately not collected: mounting as a file is
// the remediation CIS 5.4.1 recommends, not the problem.
func secretEnvRefsFromPodSpec(podSpec map[string]any) []secretEnvRef {
	var out []secretEnvRef

	walk := func(containers []any) {
		for _, c := range containers {
			cm, ok := c.(map[string]any)
			if !ok || cm == nil {
				continue
			}
			containerName, _ := cm["name"].(string)

			for _, ef := range getListField(cm, "env_from", "envFrom") {
				efm, ok := ef.(map[string]any)
				if !ok || efm == nil {
					continue
				}
				sRef := getMapField(efm, "secret_ref", "secretRef")
				if sRef == nil {
					continue
				}
				if secretName, _ := sRef["name"].(string); secretName != "" {
					out = append(out, secretEnvRef{
						Container: containerName,
						Secret:    secretName,
						Source:    "env_from",
					})
				}
			}

			for _, e := range getListField(cm, "env") {
				em, ok := e.(map[string]any)
				if !ok || em == nil {
					continue
				}
				vf := getMapField(em, "value_from", "valueFrom")
				if vf == nil {
					continue
				}
				sRef := getMapField(vf, "secret_key_ref", "secretKeyRef")
				if sRef == nil {
					continue
				}
				secretName, _ := sRef["name"].(string)
				if secretName == "" {
					continue
				}
				key, _ := sRef["key"].(string)
				envVar, _ := em["name"].(string)
				out = append(out, secretEnvRef{
					Container: containerName,
					EnvVar:    envVar,
					Secret:    secretName,
					Key:       key,
					Source:    "env",
				})
			}
		}
	}

	walk(getListField(podSpec, "containers"))
	walk(getListField(podSpec, "init_containers", "initContainers"))
	return out
}

// secretEnvMessage renders the human-readable finding line shown in the
// Recommendation column. Names and keys only — never a Secret's value.
func secretEnvMessage(r secretEnvRef) string {
	if r.Source == "env_from" {
		return fmt.Sprintf("All keys of Secret %q are injected as environment variables (envFrom)", r.Secret)
	}
	if r.Key == "" {
		return fmt.Sprintf("Environment variable %q is sourced from Secret %q", r.EnvVar, r.Secret)
	}
	return fmt.Sprintf("Environment variable %q is sourced from Secret %q (key %q)", r.EnvVar, r.Secret, r.Key)
}

// ParseSecretEnvExposures turns the rolled-up exposures into Recommendation
// rows.
//
//	rule_name = secret_env_exposure, category = Configuration, severity = Medium,
//	recommendation_action = Modify, account_object_id = "<ns>/<Kind>/<name>",
//	recommendation = JSON array of findings.
//
// Category is Configuration, not Security, for two reasons: the Security tab
// filters on rule_name 'k8s-cis-1.23' so a Security row would render nowhere,
// and certificate_expiry — the closest existing analogue — is Configuration
// too. Severity is Medium deliberately: injecting Secrets as env vars is
// near-universal in public Helm charts, and Critical/High would page every
// tenant through SendSecurityPostureAlert on day one.
func ParseSecretEnvExposures(exposures []SecretEnvExposure, account ScanAccount) ([]Recommendation, error) {
	out := make([]Recommendation, 0, len(exposures))
	for _, e := range exposures {
		if len(e.Findings) == 0 {
			continue
		}
		body, err := json.Marshal(e.Findings)
		if err != nil {
			return nil, fmt.Errorf("secret_env_exposure: encode recommendation: %w", err)
		}
		out = append(out, Recommendation{
			CloudAccountID:       account.AccountID,
			TenantID:             account.TenantID,
			Category:             "Configuration",
			RuleName:             SecretEnvExposureRuleName,
			RecommendationAction: "Modify",
			Recommendation:       string(body),
			Severity:             "Medium",
			Status:               "Open",
			AccountObjectID:      formatAccountObjectID(e.Namespace, e.Kind, e.Name),
		})
	}
	return out, nil
}

// getListField returns the first []any found at the given snake_case /
// camelCase aliases. Companion to getMapField in parser_unused_pvc.go.
func getListField(m map[string]any, keys ...string) []any {
	if m == nil {
		return nil
	}
	for _, k := range keys {
		if v, ok := m[k].([]any); ok {
			return v
		}
	}
	return nil
}
