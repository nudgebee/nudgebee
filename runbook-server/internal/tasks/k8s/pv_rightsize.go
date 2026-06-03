package k8s

import (
	"encoding/json"
	"errors"
	"fmt"
	"nudgebee/runbook/common"
	"nudgebee/runbook/internal/tasks/types"
	"nudgebee/runbook/services/relay"
	"nudgebee/runbook/services/ticket"
	"strconv"
	"strings"

	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

type PVRightsizeTask struct{}

func (t *PVRightsizeTask) GetName() string {
	return "k8s.pv_rightsize"
}

func (t *PVRightsizeTask) GetDescription() string {
	return "Resize a Kubernetes Persistent Volume to match actual usage."
}

func (t *PVRightsizeTask) GetDisplayName() string {
	return "PV Rightsize"
}

func (t *PVRightsizeTask) Execute(taskCtx types.TaskContext, params map[string]any) (any, error) {
	taskCtx.GetLogger().Debug("Executing PVRightsizeTask", "params", params)

	if paramsStr, err := common.MarshalJson(params); err == nil {
		taskCtx.GetLogger().Info("params", "params", paramsStr)
	}

	// 1. Extract Parameters
	accountId := taskCtx.GetAccountID()
	if id, ok := params["account_id"].(string); ok && id != "" {
		accountId = id
	}

	namespace, _ := params["namespace"].(string)
	name, _ := params["name"].(string) // PVC name
	kind, _ := params["kind"].(string)
	if kind == "" {
		kind = "PersistentVolumeClaim"
	}

	if namespace == "" || name == "" {
		return nil, errors.New("namespace and name are required")
	}

	if !k8sNameRegex.MatchString(namespace) {
		return nil, fmt.Errorf("invalid namespace format: %s", namespace)
	}
	if !k8sNameRegex.MatchString(name) {
		return nil, fmt.Errorf("invalid name format: %s", name)
	}

	if strings.ToLower(kind) != "persistentvolumeclaim" && strings.ToLower(kind) != "pvc" {
		return nil, fmt.Errorf("pv rightsizing task is only applicable for PersistentVolumeClaim (PVC) resources, got: %s", kind)
	}

	changeByStr, changeByProvided := params["change_by"].(string)
	changeToStr, changeToProvided := params["change_to"].(string)
	maxStr, _ := params["max"].(string)

	if !changeByProvided && !changeToProvided {
		return nil, errors.New("either 'change_by' or 'change_to' must be provided")
	}
	if changeByProvided && changeToProvided {
		return nil, errors.New("cannot provide both 'change_by' and 'change_to'")
	}

	// 2. Fetch PVC Resource
	cmd := fmt.Sprintf("kubectl get pvc %s -n %s -o json", name, namespace)
	requestContext := taskCtx.GetNewRequestContext()
	resp, err := relay.ExecuteRelayJob(requestContext, accountId, relay.RelayJobKubectl, "", cmd, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch PVC: %w", err)
	}

	respMap, err := parseKubectlResponse(resp)
	if err != nil {
		return nil, fmt.Errorf("failed to parse kubectl get response for PVC: %w", err)
	}

	obj := &unstructured.Unstructured{Object: respMap}

	currentStorageStr, found, err := unstructured.NestedString(obj.Object, "spec", "resources", "requests", "storage")
	if !found || err != nil {
		return nil, errors.New("unable to find current storage request in PVC spec")
	}
	currentStorage, err := resource.ParseQuantity(currentStorageStr)
	if err != nil {
		return nil, fmt.Errorf("failed to parse current storage quantity '%s': %w", currentStorageStr, err)
	}

	var newStorage resource.Quantity
	changed := false

	if changeToProvided {
		newStorage, changed, err = t.applyAbsoluteStorage(currentStorage, changeToStr)
	} else { // changeByProvided
		newStorage, changed, err = t.applyRateStorage(currentStorage, changeByStr)
	}

	if err != nil {
		return nil, err // MarkFailedWithReason equivalent
	}

	// Constraint Checks
	if changed {
		// 1. Prevent Shrinking
		if newStorage.Cmp(currentStorage) < 0 {
			return nil, fmt.Errorf("task skipped: new storage size %s is smaller than current size %s (shrinking PVCs is generally not supported)", newStorage.String(), currentStorage.String())
		}

		// 2. Max Threshold
		if maxStr != "" {
			maxStorage, err := resource.ParseQuantity(maxStr)
			if err != nil {
				return nil, fmt.Errorf("failed to parse max storage quantity '%s': %w", maxStr, err)
			}
			if newStorage.Cmp(maxStorage) > 0 {
				return nil, fmt.Errorf("task skipped: calculated storage size %s exceeds maximum allowed size %s", newStorage.String(), maxStr)
			}
		}
	}

	if !changed {
		return map[string]any{"status": "skipped", "reason": "no storage change calculated or required"}, nil
	}

	// 4. Construct Patch Object (JSON Patch)
	patch := []map[string]any{
		{
			"op":    "replace",
			"path":  "/spec/resources/requests/storage",
			"value": newStorage.String(),
		},
	}

	// Add Traceability Annotation
	resolverType := "AutoRunbook"
	resolverID := taskCtx.GetWorkflowID()
	moduleSuffix := "workflow"

	if id, ok := params["recommendation_id"].(string); ok {
		resolverType = "AutoOptimize"
		moduleSuffix = "optimizer"
		resolverID = id
	}
	if id, ok := params["recommendation_optimizer_id"].(string); ok {
		resolverID = id
	}
	if id, ok := params["recommendation_task_id"].(string); ok {
		resolverID = id
	}

	annoKey, annoVal, err := GetTraceabilityAnnotation(taskCtx, resolverType, resolverID, moduleSuffix)
	if err != nil {
		taskCtx.GetLogger().Warn("Failed to generate traceability annotation", "error", err)
	} else {
		// For JSON Patch, we need to add/replace annotation
		// We assume annotations might not exist, so we use "add" for the path if it's potentially new,
		// but "add" handles replacement in JSON patch if it exists too.
		// However, to be safe and simple given this is a specific key:
		// JSON Patch for map keys needs proper escaping.
		// A safer way for JSON patch is to check if we can just patch the whole annotation map, but that's destructive.
		// Given PVRightsize uses JSON Patch (array of ops), adding a key to a map is:
		// { "op": "add", "path": "/metadata/annotations/workloads.nudgebee.com~1v1.optimizer", "value": "..." }
		// Note ~1 escaping for /.

		escapedKey := strings.ReplaceAll(annoKey, "/", "~1")
		patch = append(patch, map[string]any{
			"op":    "add", // "add" creates or replaces member
			"path":  fmt.Sprintf("/metadata/annotations/%s", escapedKey),
			"value": annoVal,
		})
	}

	patchBytes, _ := json.Marshal(patch)
	patchStr := strings.ReplaceAll(string(patchBytes), "'", "'\\''")

	if taskCtx.IsDryRun() {
		return map[string]any{
			"status":      "dry_run",
			"old_storage": currentStorage.String(),
			"new_storage": newStorage.String(),
			"patch":       patch,
		}, nil
	}

	// 5. GitOps mode: open a GitHub Issue describing the resize instead of
	// patching the cluster directly. Routes through ticket-server's
	// tickets_insert_one mutation so the GitHub integration is reused as a
	// ticket integration (same path as tickets.create).
	description := fmt.Sprintf("Resize PVC %s from %s to %s", name, currentStorage.String(), newStorage.String())

	if cfg, ok := params["gitops_config"].(map[string]any); ok {
		if enabled, _ := cfg["enabled"].(bool); enabled {
			integrationID, _ := cfg["integration_id"].(string)
			repository, _ := cfg["repository_name"].(string)
			ticketType, _ := cfg["ticket_type"].(string)
			if ticketType == "" {
				ticketType = "bug"
			}
			source, _ := cfg["source"].(string)
			if source == "" {
				source = "kubernetes"
			}

			ticketResp, err := ticket.CreateTicket(requestContext, ticket.CreateTicketRequest{
				AccountId:     accountId,
				IntegrationId: integrationID,
				ProjectKey:    repository,
				ReferenceId:   taskCtx.GetWorkflowRunID(),
				TicketType:    ticketType,
				Source:        source,
				Title:         fmt.Sprintf("Resize PVC %s/%s to %s", namespace, name, newStorage.String()),
				Description:   buildPVCIssueBody(name, namespace, currentStorage.String(), newStorage.String(), maxStr, taskCtx),
			})
			if err != nil {
				return nil, fmt.Errorf("failed to create github issue for PVC resize: %w", err)
			}
			return map[string]any{
				"status":       "issue_created",
				"old_storage":  currentStorage.String(),
				"new_storage":  newStorage.String(),
				"patch":        patch,
				"issue_url":    ticketResp.URL,
				"issue_number": ticketResp.TicketId,
				"description":  description,
			}, nil
		}
	}

	// 5. Apply Patch
	// Note: PVC resizing might require additional steps depending on the StorageClass and Kubernetes version.
	// This patch only updates the PVC object's request.
	cmdPatch := fmt.Sprintf("kubectl patch pvc %s -n %s --patch '%s' --type=json", name, namespace, patchStr)

	resp, err = relay.ExecuteRelayJob(requestContext, accountId, relay.RelayJobKubectl, "", cmdPatch, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to apply PVC patch: %w", err)
	}

	// Check output of patch command
	if respStr, ok := resp.(string); ok {
		kubectlResp := map[string]any{}
		if err := common.UnmarshalJson([]byte(respStr), &kubectlResp); err != nil { // Handle unmarshal error
			return nil, fmt.Errorf("failed to parse kubectl patch response: %w", err)
		}
		if stderr, ok := kubectlResp["stderr"].(string); ok && stderr != "" {
			return nil, fmt.Errorf("kubectl patch error: %s", stderr)
		}
	}

	return map[string]any{"status": "success", "old_storage": currentStorage.String(), "new_storage": newStorage.String(), "patch": patch}, nil
}

func (t *PVRightsizeTask) applyAbsoluteStorage(currentStorage resource.Quantity, changeToStr string) (resource.Quantity, bool, error) {
	targetStorage, err := resource.ParseQuantity(changeToStr)
	if err != nil {
		return currentStorage, false, fmt.Errorf("failed to parse target storage quantity '%s': %w", changeToStr, err)
	}

	if currentStorage.Cmp(targetStorage) == 0 {
		return currentStorage, false, errors.New("task skipped: current and new storage request are the same")
	}

	return targetStorage, true, nil
}

// applyRateStorage assumes changeByStr is a percentage (e.g., "10%" or "10").
// If no '%' is present, it's treated as an absolute increase/decrease in MiB or GiB, for simplicity.
// For now, let's treat it as a percentage.
func (t *PVRightsizeTask) applyRateStorage(currentStorage resource.Quantity, changeByStr string) (resource.Quantity, bool, error) {
	changeByStr = strings.TrimSpace(changeByStr)
	percentage := 0.0

	if trimmed, ok := strings.CutSuffix(changeByStr, "%"); ok {
		changeByStr = trimmed
		pct, err := strconv.ParseFloat(changeByStr, 64)
		if err != nil {
			return currentStorage, false, fmt.Errorf("failed to parse percentage '%s': %w", changeByStr, err)
		}
		percentage = pct / 100.0
	} else {
		// If not percentage, assume it's a raw number for simplicity.
		// For consistency with other tasks, let's make it a percentage only.
		// Revisit if PV_Rightsize in Python implies absolute change in units.
		// For now, we assume change_by in PVConfigModel is an integer (percentage)
		pct, err := strconv.ParseFloat(changeByStr, 64)
		if err != nil {
			return currentStorage, false, fmt.Errorf("failed to parse change_by value '%s': %w", changeByStr, err)
		}
		percentage = pct / 100.0
	}

	newVal := float64(currentStorage.Value()) * (1.0 + percentage)
	newStorage := resource.NewQuantity(int64(newVal), currentStorage.Format)

	if currentStorage.Cmp(*newStorage) == 0 {
		return currentStorage, false, errors.New("task skipped: current and new storage request are the same after calculation")
	}

	return *newStorage, true, nil
}

func (t *PVRightsizeTask) InputSchema() *types.Schema {
	return &types.Schema{
		Properties: map[string]types.Property{
			"account_id": {
				Type:  types.PropertyTypeAccount,
				Title: "Account",
				Order: 1,
			},
			"namespace": {
				Type:      types.PropertyTypeString,
				Required:  true,
				Title:     "Namespace",
				Order:     2,
				DependsOn: []string{"account_id"},
			},
			"kind": {
				Type:     types.PropertyTypeString,
				Required: true,
				Default:  "PersistentVolumeClaim",
				Title:    "Kind",
				Order:    3,
				ReadOnly: true,
			},
			"name": {
				Type:      types.PropertyTypeString,
				Required:  true,
				Title:     "Name",
				Order:     4,
				DependsOn: []string{"account_id", "namespace", "kind"},
			},
			"change_by": {
				Type:        types.PropertyTypeString,
				Title:       "Change By",
				Description: "Percentage to change storage by (e.g., '10%'). Only positive values are supported for increase.",
				Order:       5,
			},
			"change_to": {
				Type:        types.PropertyTypeString,
				Title:       "Change To",
				Description: "Target absolute storage size (e.g., '20Gi').",
				Order:       6,
			},
			"max": {
				Type:        types.PropertyTypeString,
				Title:       "Max",
				Description: "Maximum storage size allowed (e.g., '100Gi').",
				Order:       7,
			},
			"gitops_config": {
				Type:        types.PropertyTypeObject,
				Title:       "Create GitHub Issue",
				Description: "If enabled, opens a GitHub Issue describing the requested PVC resize instead of patching the cluster directly.",
				Required:    true,
				Order:       8,
				Schema: &types.Schema{
					Properties: map[string]types.Property{
						"enabled": {
							Type:        types.PropertyTypeBoolean,
							Title:       "Create GitHub Issue",
							Description: "If true, opens a GitHub Issue describing the requested PVC resize instead of patching the cluster directly.",
							Default:     false,
							Order:       1,
						},
						"integration_id": {
							Type:         types.PropertyTypeTicket,
							Title:        "GitHub Config",
							SubType:      "github",
							Description:  "GitHub integration used to create the issue.",
							Order:        2,
							VisibleWhen:  &types.VisibleWhen{Field: "enabled", Value: []string{"true"}},
							RequiredWhen: &types.RequiredWhen{Field: "enabled", Value: []string{"true"}},
							DependsOn:    []string{"enabled"},
						},
						"repository_name": {
							Type:         types.PropertyTypeString,
							Title:        "GitHub Repository",
							SubType:      "github_repository",
							Description:  "Repository where the issue should be opened (e.g. 'org/repo' or full URL).",
							Order:        3,
							VisibleWhen:  &types.VisibleWhen{Field: "enabled", Value: []string{"true"}},
							RequiredWhen: &types.RequiredWhen{Field: "enabled", Value: []string{"true"}},
							DependsOn:    []string{"enabled", "integration_id"},
						},
					},
				},
			},
		},
	}
}

func (t *PVRightsizeTask) OutputSchema() *types.Schema {
	return &types.Schema{
		Properties: map[string]types.Property{
			"status":        {Type: types.PropertyTypeString},
			"old_storage":   {Type: types.PropertyTypeString},
			"new_storage":   {Type: types.PropertyTypeString},
			"patch":         {Type: types.PropertyTypeObject},
			"resolution_id": {Type: types.PropertyTypeString},
			"issue_url":     {Type: types.PropertyTypeString},
			"issue_number":  {Type: types.PropertyTypeNumber},
		},
	}
}

// buildPVCIssueBody renders the markdown body for the GitHub issue raised
// when GitOps mode is enabled on a PV Rightsize task.
func buildPVCIssueBody(name, namespace, oldSize, newSize, maxSize string, taskCtx types.TaskContext) string {
	var b strings.Builder
	b.WriteString("## PVC Resize Request\n\n")
	fmt.Fprintf(&b, "**Namespace:** `%s`  \n", namespace)
	fmt.Fprintf(&b, "**PVC:** `%s`  \n", name)
	fmt.Fprintf(&b, "**Current storage:** `%s`  \n", oldSize)
	fmt.Fprintf(&b, "**Target storage:** `%s`  \n", newSize)
	if maxSize != "" {
		fmt.Fprintf(&b, "**Max allowed:** `%s`  \n", maxSize)
	}
	b.WriteString("\n## Suggested change\n\n")
	b.WriteString("Update `spec.resources.requests.storage` on the PVC manifest (or the matching `volumeClaimTemplates` entry on the parent StatefulSet, or the corresponding Helm values entry).\n\n")
	b.WriteString("```yaml\n")
	b.WriteString("spec:\n")
	b.WriteString("  resources:\n")
	b.WriteString("    requests:\n")
	fmt.Fprintf(&b, "      storage: %s\n", newSize)
	b.WriteString("```\n\n")
	b.WriteString("---\nRaised by Nudgebee.")
	if link := getWorkflowBaseLink(taskCtx); link != "" {
		fmt.Fprintf(&b, " [Workflow run](%s)", link)
	}
	b.WriteString("\n")
	return b.String()
}
