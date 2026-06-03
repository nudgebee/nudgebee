package tools

import (
	"errors"
	"fmt"
	"log/slog"
	"nudgebee/llm/common"
	"nudgebee/llm/services_server"
	"nudgebee/llm/tools/core"
	"strings"
)

const ToolServiceDependencyGraph = "service_dependency_graph_execute"

func init() {
	core.RegisterNBToolFactory(ToolServiceDependencyGraph, func(accountId string) (core.NBTool, error) {
		return ServiceDependencyGraphTool{
			accountId: accountId,
		}, nil
	})
}

type ServiceDependencyGraphTool struct {
	accountId string
}

func (m ServiceDependencyGraphTool) Name() string {
	return ToolServiceDependencyGraph
}

func (m ServiceDependencyGraphTool) GetType() core.NBToolType {
	return core.NBToolTypeTool
}

func (m ServiceDependencyGraphTool) Description() string {
	return `Generates Upstream/Downstream dependency graph for Deployments/Statefulsets/Pods/Daemonsets/Jobs, input={"name":"<>","namespace":"<>","type":"<>"}`
}

func (m ServiceDependencyGraphTool) InputSchema() core.ToolSchema {
	return core.ToolSchema{
		Type: core.ToolSchemaTypeObject,
		Properties: map[string]core.ToolSchemaProperty{
			"command": {
				Type:        core.ToolSchemaTypeString,
				Description: "Sql query to Execute",
			},
			"name": {
				Type:        core.ToolSchemaTypeString,
				Description: "Name of the service",
			},
			"namespace": {
				Type:        core.ToolSchemaTypeString,
				Description: "Namespace of the service",
			},
			"type": {
				Type:        core.ToolSchemaTypeString,
				Description: "Type of the service",
			},
		},
		Required: []string{"command"},
	}
}

type serviceDependencyGraphToolInput struct {
	Name      string `json:"name"`
	Namespace string `json:"namespace"`
	Type      string `json:"type"`
}

func (m ServiceDependencyGraphTool) Call(nbRequestContext core.NbToolContext, input core.NBToolCallRequest) (core.NBToolResponse, error) {
	nbRequestContext.Ctx.GetLogger().Info("traces: executing getTraces tool call", "input", slog.AnyValue(input))

	depInput := serviceDependencyGraphToolInput{}

	if strings.HasPrefix(input.Command, "{") {
		err := common.UnmarshalJson([]byte(input.Command), &depInput)
		if err != nil {
			return core.NBToolResponse{}, err
		}
	} else {
		depInput.Name = input.Command
		if namespace, ok := input.Arguments["namespace"]; ok {
			depInput.Namespace = namespace.(string)
		}
		if serviceType, ok := input.Arguments["type"]; ok {
			depInput.Type = serviceType.(string)
		}
	}

	if depInput.Name == "" || strings.Contains(depInput.Name, " ") {
		return core.NBToolResponse{}, errors.New(`invalid input, format = {"name":"<>","namespace":"<>","type":"<>"}`)
	}
	if depInput.Namespace == "" {
		depInput.Namespace = "default"
	}
	if depInput.Type == "" {
		depInput.Type = "deployment"
	}

	// if type is pod then identify owner of that pod and then pass that
	if strings.EqualFold(depInput.Type, "pod") {
		podNameForLookup := depInput.Name
		podNamespaceForLookup := depInput.Namespace

		// Attempt to find the ultimate workload owner (e.g., Deployment, StatefulSet)
		ownerName, ownerKind, err := getWorkloadOwnerForPod(nbRequestContext, podNameForLookup, podNamespaceForLookup)
		if err != nil {
			nbRequestContext.Ctx.GetLogger().Warn("servicedependencygraph: could not resolve pod owner, proceeding with original pod type", "pod", podNameForLookup, "namespace", podNamespaceForLookup, "error", err)
			// Keep original depInput.Type = "pod" and depInput.Name = podNameForLookup
			depInput.Type = strings.ToLower(depInput.Type) // Ensure original type "pod" is lowercase
		} else {
			nbRequestContext.Ctx.GetLogger().Info("servicedependencygraph: resolved pod owner", "pod", podNameForLookup, "owner_name", ownerName, "owner_kind", ownerKind)
			depInput.Name = ownerName
			depInput.Type = strings.ToLower(ownerKind) // Ensure resolved type is lowercase
		}
	}

	if strings.EqualFold(depInput.Type, "configmap") || strings.EqualFold(depInput.Type, "secret") {
		resp, err := getServiceDependenciesForResource(nbRequestContext, depInput.Type, depInput.Name, depInput.Namespace)
		if err != nil {
			return core.NBToolResponse{}, err
		}
		return core.NBToolResponse{
			Data:       resp,
			Type:       core.NBToolResponseTypeJson,
			Status:     core.NBToolResponseStatusSuccess,
			References: []core.NBToolResponseReference{core.GetNudgebeeUIReferenceForClusterDetails(nbRequestContext, []string{"monitoring", "service-map"}, "Service Map", nil, "")},
		}, err
	}

	resp, err := services_server.GetServiceDependencyGraph(*nbRequestContext.Ctx, nbRequestContext.AccountId, depInput.Namespace, depInput.Name)
	if err != nil {
		return core.NBToolResponse{}, err
	}

	response, _ := common.MarshalJson(resp)

	query, err := resp.ServiceMapDebug()
	if err != nil {
		nbRequestContext.Ctx.GetLogger().Error("servicedependencygraph: failed to get debug info", "error", err)
	}

	return core.NBToolResponse{
		Data:       string(response),
		Type:       core.NBToolResponseTypeJson,
		Status:     core.NBToolResponseStatusSuccess,
		References: []core.NBToolResponseReference{core.GetNudgebeeUIReferenceForClusterDetails(nbRequestContext, []string{"monitoring", "service-map"}, "Service Map", nil, query)},
	}, err
}

// getWorkloadOwnerForPod tries to find the top-level workload owner (like Deployment, StatefulSet, Job, CronJob) for a given pod.
func getWorkloadOwnerForPod(nbRequestContext core.NbToolContext, podName string, namespace string) (ownerName string, ownerKind string, err error) {
	// Step 1: Get the pod's immediate ownerReferences
	kubectlCmdPod := fmt.Sprintf("kubectl get pod %s -n %s -o json", podName, namespace)
	apiResposeRaw, execErr := ExecuteContainerJob(nbRequestContext, RelayJobKubectl, kubectlCmdPod, nbRequestContext.AccountId, map[string]any{}, false)
	if execErr != nil {
		return "", "", fmt.Errorf("kubectl get pod %s -n %s failed: %w", podName, namespace, execErr)
	}

	podDetailsRawMap := map[string]any{}
	err = common.UnmarshalJson([]byte(apiResposeRaw.(string)), &podDetailsRawMap)
	if err != nil || podDetailsRawMap == nil {
		nbRequestContext.Ctx.GetLogger().Error("servicedependencygraph: failed to unmarshal pod details", "error", err, "data", apiResposeRaw)
		return "", "", fmt.Errorf("failed to unmarshal pod details")
	}
	podDetailsAny := podDetailsRawMap["stdout"]
	if podDetailsAny == nil {
		nbRequestContext.Ctx.GetLogger().Error("servicedependencygraph: failed to unmarshal pod details", "error", err, "data", apiResposeRaw)
		return "", "", fmt.Errorf("podDetails is not found in response")
	}
	var podInfo struct {
		Metadata struct {
			OwnerReferences []struct {
				Kind string `json:"kind"`
				Name string `json:"name"`
			} `json:"ownerReferences"`
		} `json:"metadata"`
	}

	podDetailsStr, ok := podDetailsAny.(string)
	if !ok || podDetailsStr == "" {
		return "", "", fmt.Errorf("kubectl get pod %s -n %s returned non-string or empty response", podName, namespace)
	}

	if errUnmarshal := common.UnmarshalJson([]byte(podDetailsStr), &podInfo); errUnmarshal != nil {
		// It's possible kubectl command failed and podDetailsStr is an error message from kubectl stderr
		return "", "", fmt.Errorf("failed to unmarshal pod details for %s -n %s (kubectl output might be an error: %s): %w", podName, namespace, podDetailsStr, errUnmarshal)
	}

	if len(podInfo.Metadata.OwnerReferences) == 0 {
		// Pod has no owners, might be a static pod or directly created
		nbRequestContext.Ctx.GetLogger().Info("servicedependencygraph: pod has no owner references", "pod", podName, "namespace", namespace)
		return podName, "Pod", nil // Return itself as owner
	}

	immediateOwner := podInfo.Metadata.OwnerReferences[0]
	currentOwnerName := immediateOwner.Name
	currentOwnerKind := immediateOwner.Kind

	// Step 2: If the immediate owner is a ReplicaSet, try to find its Deployment owner
	if strings.EqualFold(currentOwnerKind, "ReplicaSet") {
		kubectlCmdRS := fmt.Sprintf("kubectl get replicaset %s -n %s -o json", currentOwnerName, namespace)
		rsApiCall, rsExecErr := ExecuteContainerJob(nbRequestContext, RelayJobKubectl, kubectlCmdRS, nbRequestContext.AccountId, map[string]any{}, false)
		if rsExecErr != nil {
			// Failed to get ReplicaSet details, return the ReplicaSet itself.
			return currentOwnerName, currentOwnerKind, fmt.Errorf("failed to get replicaset %s details, using ReplicaSet as owner: %w", currentOwnerName, rsExecErr)
		}

		rsApiCallStr, ok := rsApiCall.(string)
		if !ok {
			return currentOwnerName, currentOwnerKind, fmt.Errorf("failed to get replicaset %s details, using ReplicaSet as owner: %w", currentOwnerName, rsExecErr)
		}

		rsApiCallMap := map[string]any{}
		err = common.UnmarshalJson([]byte(rsApiCallStr), &rsApiCallMap)
		if err != nil {
			return currentOwnerName, currentOwnerKind, fmt.Errorf("failed to unmarshal replicaset %s details: %w", currentOwnerName, err)
		}

		rsDetailsRaw := rsApiCallMap["stdout"]
		if rsDetailsRaw == nil {
			return currentOwnerName, currentOwnerKind, fmt.Errorf("failed to unmarshal replicaset %s details: %w", currentOwnerName, err)
		}

		var rsInfo struct {
			Metadata struct {
				OwnerReferences []struct {
					Kind string `json:"kind"`
					Name string `json:"name"`
				} `json:"ownerReferences"`
			} `json:"metadata"`
		}
		rsDetailsStr, ok := rsDetailsRaw.(string)
		if !ok || rsDetailsStr == "" {
			return currentOwnerName, currentOwnerKind, fmt.Errorf("kubectl get rs %s returned non-string or empty response: type %T", currentOwnerName, rsDetailsRaw)
		}

		if errUnmarshalRS := common.UnmarshalJson([]byte(rsDetailsStr), &rsInfo); errUnmarshalRS != nil {
			return currentOwnerName, currentOwnerKind, fmt.Errorf("failed to unmarshal replicaset %s details (kubectl output might be an error: %s): %w", currentOwnerName, rsDetailsStr, errUnmarshalRS)
		}

		if len(rsInfo.Metadata.OwnerReferences) > 0 {
			higherLevelOwner := rsInfo.Metadata.OwnerReferences[0]
			// Typically, a ReplicaSet is owned by a Deployment.
			if strings.EqualFold(higherLevelOwner.Kind, "Deployment") {
				return higherLevelOwner.Name, higherLevelOwner.Kind, nil
			}
		}
		// If ReplicaSet has no further owner or owner is not a Deployment, return the ReplicaSet itself.
		return currentOwnerName, currentOwnerKind, nil
	}

	// Step 3: If the immediate owner is a Job, try to find its CronJob owner
	if strings.EqualFold(currentOwnerKind, "Job") {
		kubectlCmdJob := fmt.Sprintf("kubectl get job %s -n %s -o json", currentOwnerName, namespace)
		jobDetailsRaw, jobExecErr := ExecuteContainerJob(nbRequestContext, RelayJobKubectl, kubectlCmdJob, nbRequestContext.AccountId, map[string]any{}, false)
		if jobExecErr != nil {
			// Failed to get Job details, return the Job itself.
			return currentOwnerName, currentOwnerKind, fmt.Errorf("failed to get job %s details, using Job as owner: %w", currentOwnerName, jobExecErr)
		}

		var jobInfo struct {
			Metadata struct {
				OwnerReferences []struct {
					Kind string `json:"kind"`
					Name string `json:"name"`
				} `json:"ownerReferences"`
			} `json:"metadata"`
		}
		jobDetailsStr, ok := jobDetailsRaw.(string)
		if !ok || jobDetailsStr == "" {
			return currentOwnerName, currentOwnerKind, fmt.Errorf("kubectl get job %s returned non-string or empty response: type %T", currentOwnerName, jobDetailsRaw)
		}

		if errUnmarshalJob := common.UnmarshalJson([]byte(jobDetailsStr), &jobInfo); errUnmarshalJob != nil {
			return currentOwnerName, currentOwnerKind, fmt.Errorf("failed to unmarshal job %s details (kubectl output might be an error: %s): %w", currentOwnerName, jobDetailsStr, errUnmarshalJob)
		}

		if len(jobInfo.Metadata.OwnerReferences) > 0 {
			higherLevelOwner := jobInfo.Metadata.OwnerReferences[0]
			// Typically, a Job can be owned by a CronJob.
			if strings.EqualFold(higherLevelOwner.Kind, "CronJob") {
				return higherLevelOwner.Name, higherLevelOwner.Kind, nil
			}
		}
		// If Job has no further owner or owner is not a CronJob, return the Job itself.
		return currentOwnerName, currentOwnerKind, nil
	}

	// For other kinds (StatefulSet, DaemonSet, etc.), the immediate owner is usually the top-level workload.
	return currentOwnerName, currentOwnerKind, nil
}

func getServiceDependenciesForResource(nbRequestContext core.NbToolContext, resourceType, resourceName, namespace string) (string, error) {
	// Common command execution logic
	executeKubectlCommand := func(command string) (string, error) {
		apiResponse, err := ExecuteContainerJob(nbRequestContext, RelayJobKubectl, command, nbRequestContext.AccountId, map[string]any{}, false)
		if err != nil {
			return "", fmt.Errorf("failed to execute kubectl command: %w", err)
		}

		responseStr, ok := apiResponse.(string)
		if !ok {
			return "", fmt.Errorf("unexpected response type from kubectl command: expected string, got %T", apiResponse)
		}

		var responseData map[string]any
		if err := common.UnmarshalJson([]byte(responseStr), &responseData); err != nil {
			return "", fmt.Errorf("failed to unmarshal JSON response: %w", err)
		}

		stdout, ok := responseData["stdout"].(string)
		if !ok {
			return "", fmt.Errorf("'stdout' not found or not a string in response")
		}
		return stdout, nil
	}

	// Logic for ConfigMaps and Secrets
	if resourceType == "configmap" || resourceType == "secret" {
		// Find all pods in the namespace
		podsCmd := fmt.Sprintf("kubectl get pods -n %s -o json", namespace)
		podsJSON, err := executeKubectlCommand(podsCmd)
		if err != nil {
			return "", fmt.Errorf("failed to get pods in namespace %s: %w", namespace, err)
		}

		var pods struct {
			Items []struct {
				Metadata struct {
					Name      string `json:"name"`
					Namespace string `json:"namespace"`
				} `json:"metadata"`
				Spec struct {
					Volumes []struct {
						ConfigMap struct {
							Name string `json:"name"`
						} `json:"configMap"`
						Secret struct {
							SecretName string `json:"secretName"`
						} `json:"secret"`
					} `json:"volumes"`
					Containers []struct {
						EnvFrom []struct {
							ConfigMapRef struct {
								Name string `json:"name"`
							} `json:"configMapRef"`
							SecretRef struct {
								Name string `json:"name"`
							} `json:"secretRef"`
						} `json:"envFrom"`
					} `json:"containers"`
				} `json:"spec"`
			} `json:"items"`
		}

		if err := common.UnmarshalJson([]byte(podsJSON), &pods); err != nil {
			return "", fmt.Errorf("failed to unmarshal pods JSON: %w", err)
		}

		// Find dependent pods
		var dependentPods []string
		for _, pod := range pods.Items {
			// Check volumes
			for _, volume := range pod.Spec.Volumes {
				if resourceType == "configmap" && volume.ConfigMap.Name == resourceName {
					dependentPods = append(dependentPods, pod.Metadata.Name)
					break // Move to the next pod
				}
				if resourceType == "secret" && volume.Secret.SecretName == resourceName {
					dependentPods = append(dependentPods, pod.Metadata.Name)
					break // Move to the next pod
				}
			}

			// Check environment variables
			for _, container := range pod.Spec.Containers {
				for _, envFrom := range container.EnvFrom {
					if resourceType == "configmap" && envFrom.ConfigMapRef.Name == resourceName {
						dependentPods = append(dependentPods, pod.Metadata.Name)
						break // Move to the next container
					}
					if resourceType == "secret" && envFrom.SecretRef.Name == resourceName {
						dependentPods = append(dependentPods, pod.Metadata.Name)
						break // Move to the next container
					}
				}
			}
		}

		// Remove duplicates
		uniquePods := make(map[string]struct{})
		for _, podName := range dependentPods {
			uniquePods[podName] = struct{}{}
		}

		var resultPods []string
		for podName := range uniquePods {
			resultPods = append(resultPods, podName)
		}
		// Return the dependent pods as a JSON string
		responseJSON, err := common.MarshalJson(resultPods)
		if err != nil {
			return "", fmt.Errorf("failed to marshal dependent pods to JSON: %w", err)
		}

		return string(responseJSON), nil
	}

	return "", fmt.Errorf("unsupported resource type: %s", resourceType)
}
