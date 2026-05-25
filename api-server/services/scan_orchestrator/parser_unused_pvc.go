package scan_orchestrator

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// UnusedPVCRuleName matches the collector's RuleName.UNUSED_PVC
// (event_handler.py:332) — the UI keys on this exact rule_name (see
// /optimize/unused-volume → app/src/api1/recommendation/index.ts).
const UnusedPVCRuleName = "unused_pvc"

// UnusedPVCSavingFactor is the same 10%-of-capacity heuristic the collector's
// handle_abandoned_pv used (event_handler.py:2027). A PV sitting unclaimed
// costs ~its provisioned size in monthly storage spend; saving estimate is
// 10% of that as a conservative monthly cost on hyperdisk-balanced rates.
// Adjusting this factor is future per-tenant attr work — out of scope here.
const UnusedPVCSavingFactor = 0.10

// IdentifyUnusedPVs mirrors Robusta's list_unclaimed_persistent_volumes
// (robusta/playbooks/nudgebee_playbooks/unused_pv.py:23-47): a PV is unused
// when it is in Released/Available phase, OR when it is Bound to a PVC that
// no pod references via spec.volumes[].persistent_volume_claim.claim_name.
//
// All inputs are the snake-cased lists the agent's get_resource returns
// (kube/snake.go converts camelCase → snake_case before sending).
func IdentifyUnusedPVs(pods, pvcs, pvs []map[string]any) []map[string]any {
	// Build pvc → []pod map from pod.spec.volumes[].persistent_volume_claim.claim_name.
	// Every map/slice access uses comma-ok to defend against malformed agent
	// payloads — the relay's payload is external data and a single missing
	// nested key would otherwise crash the cron path for every account.
	pvcUsedBy := map[string][]string{} // key = "<ns>/<pvc_name>"
	for _, pod := range pods {
		podMeta, ok := pod["metadata"].(map[string]any)
		if !ok || podMeta == nil {
			continue
		}
		podNS, _ := podMeta["namespace"].(string)
		podName, _ := podMeta["name"].(string)
		spec, ok := pod["spec"].(map[string]any)
		if !ok || spec == nil {
			continue
		}
		volumes, ok := spec["volumes"].([]any)
		if !ok {
			continue
		}
		for _, v := range volumes {
			vm, ok := v.(map[string]any)
			if !ok {
				continue
			}
			pvcSpec, ok := vm["persistent_volume_claim"].(map[string]any)
			if !ok || pvcSpec == nil {
				// Tolerate camelCase agents (older builds without SnakeKeysDeep).
				pvcSpec, ok = vm["persistentVolumeClaim"].(map[string]any)
				if !ok || pvcSpec == nil {
					continue
				}
			}
			claim, _ := pvcSpec["claim_name"].(string)
			if claim == "" {
				claim, _ = pvcSpec["claimName"].(string)
			}
			if claim == "" {
				continue
			}
			key := fmt.Sprintf("%s/%s", podNS, claim)
			pvcUsedBy[key] = append(pvcUsedBy[key], podName)
		}
	}

	// Walk PVs once: anything in Released/Available is unused; anything
	// Bound goes into claimedPV so we can look it up by PVC key.
	unused := []map[string]any{}
	claimedPV := map[string]map[string]any{} // key = "<ns>/<pvc>"
	for _, pv := range pvs {
		status, ok := pv["status"].(map[string]any)
		if !ok || status == nil {
			continue
		}
		phase, ok := status["phase"].(string)
		if !ok {
			continue
		}
		switch phase {
		case "Released", "Available":
			unused = append(unused, pv)
		case "Bound":
			spec, ok := pv["spec"].(map[string]any)
			if !ok || spec == nil {
				continue
			}
			claim := getMapField(spec, "claim_ref", "claimRef")
			if claim == nil {
				continue
			}
			cName, _ := claim["name"].(string)
			cNS, _ := claim["namespace"].(string)
			if cName == "" {
				continue
			}
			claimedPV[fmt.Sprintf("%s/%s", cNS, cName)] = pv
		}
	}

	// Bound PVCs whose PVC has no pod referencing it → that PV is dead weight.
	for _, pvc := range pvcs {
		pStatus, ok := pvc["status"].(map[string]any)
		if !ok || pStatus == nil {
			continue
		}
		if phase, _ := pStatus["phase"].(string); phase != "Bound" {
			continue
		}
		md, ok := pvc["metadata"].(map[string]any)
		if !ok || md == nil {
			continue
		}
		ns, _ := md["namespace"].(string)
		name, _ := md["name"].(string)
		if name == "" {
			continue
		}
		key := fmt.Sprintf("%s/%s", ns, name)
		if len(pvcUsedBy[key]) > 0 {
			continue
		}
		if pv, ok := claimedPV[key]; ok && pv != nil {
			unused = append(unused, pv)
		}
	}
	return unused
}

// ParseUnusedPVs turns the unused PV list into Recommendation rows. Output
// matches collector's handle_abandoned_pv (event_handler.py:2021-2046):
//
//	rule_name = unused_pvc, category = RightSizing, severity = High,
//	recommendation_action = Modify, account_object_id = "<ns>/<pv_name>",
//	recommendation = json.dumps(pv), estimated_savings = 10% * capacity_gb.
//
// PV namespace is typically empty (PVs are cluster-scoped), so the
// account_object_id usually starts with "/" — preserved verbatim from the
// collector so existing rows UPSERT in place.
func ParseUnusedPVs(unused []map[string]any, account ScanAccount) ([]Recommendation, error) {
	out := make([]Recommendation, 0, len(unused))
	seen := map[string]bool{} // dedupe by account_object_id within one scan
	for _, pv := range unused {
		md, ok := pv["metadata"].(map[string]any)
		if !ok || md == nil {
			continue
		}
		name, _ := md["name"].(string)
		if name == "" {
			continue
		}
		ns, _ := md["namespace"].(string) // usually empty (PV is cluster-scoped)
		accountObjectID := fmt.Sprintf("%s/%s", ns, name)
		if seen[accountObjectID] {
			continue
		}
		seen[accountObjectID] = true

		// Capacity comes from spec.capacity.storage (e.g. "50Gi"). Missing /
		// malformed -> savings degrades to 0; still emit the row so the PV
		// shows up in the UI.
		var storage string
		if spec, ok := pv["spec"].(map[string]any); ok && spec != nil {
			if cap, ok := spec["capacity"].(map[string]any); ok && cap != nil {
				storage, _ = cap["storage"].(string)
			}
		}
		capacityGB := parseSizeToGB(storage)
		savings := UnusedPVCSavingFactor * capacityGB

		// The agent's kube handler snake_cases every key before returning
		// the PV. The UI's KubernetesUnusedVolumes.jsx reads camelCase
		// (spec.claimRef, metadata.creationTimestamp) — match that contract
		// so legacy + canary rows render identically.
		body, err := json.Marshal(camelKeysDeep(pv))
		if err != nil {
			return nil, fmt.Errorf("unused_pvc: encode recommendation: %w", err)
		}
		rec := Recommendation{
			CloudAccountID:       account.AccountID,
			TenantID:             account.TenantID,
			Category:             "RightSizing",
			RuleName:             UnusedPVCRuleName,
			RecommendationAction: "Modify",
			Recommendation:       string(body),
			Severity:             "High",
			Status:               "Open",
			AccountObjectID:      accountObjectID,
			EstimatedSavings:     savings,
		}
		out = append(out, rec)
	}
	return out, nil
}

// memoryQuantityRe matches K8s/Robusta human-readable size strings:
// "50Gi", "512MiB", "1.5 TB", "1024" (raw bytes). Mirrors collector's
// MEMORY_PATTERN in middleware/utils.py.
var memoryQuantityRe = regexp.MustCompile(`^\s*([0-9]+(?:\.[0-9]+)?)\s*([A-Za-z]*)\s*$`)

// parseSizeToGB mirrors collector's parse_size_to_gb (middleware/utils.py).
// Empty / unparseable input returns 0 — same behaviour as the Python side
// would surface (savings = 0 for that PV).
func parseSizeToGB(s string) float64 {
	if s == "" {
		return 0
	}
	m := memoryQuantityRe.FindStringSubmatch(s)
	if m == nil {
		// Try raw bytes
		if v, err := strconv.ParseFloat(strings.TrimSpace(s), 64); err == nil {
			return v / (1 << 30)
		}
		return 0
	}
	value, err := strconv.ParseFloat(m[1], 64)
	if err != nil {
		return 0
	}
	unit := strings.ToUpper(m[2])
	if unit == "" {
		// raw bytes
		return value / (1 << 30)
	}
	if !strings.HasSuffix(unit, "B") {
		unit += "B"
	}
	switch unit {
	case "B":
		return value / (1 << 30)
	case "KB":
		return value * 1e3 / (1 << 30)
	case "KIB":
		return value / (1 << 20)
	case "MB":
		return value * 1e6 / (1 << 30)
	case "MIB":
		return value / (1 << 10)
	case "GB":
		return value * 1e9 / (1 << 30)
	case "GIB":
		return value
	case "TB":
		return value * 1e12 / (1 << 30)
	case "TIB":
		return value * 1024
	}
	return 0
}

// getMapField returns the first non-nil map[string]any value found at the
// given snake_case / camelCase aliases. Same shape as the Stage 2.2
// playbooks/event_subject_helpers.go helper — kept local so this package
// stays self-contained.
func getMapField(m map[string]any, keys ...string) map[string]any {
	if m == nil {
		return nil
	}
	for _, k := range keys {
		if v, ok := m[k].(map[string]any); ok {
			return v
		}
	}
	return nil
}
