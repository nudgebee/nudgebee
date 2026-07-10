package k8s

// extractK8sTimestamps extracts creation and deployment timestamps from Kubernetes metadata
func (s *K8sSource) extractK8sTimestamps(properties map[string]interface{}, metaMap map[string]interface{}) {
	// Extract deployment revision from annotations
	if config, ok := metaMap["config"].(map[string]interface{}); ok {
		if annotations, ok := config["annotations"].(map[string]interface{}); ok {
			if revision, ok := annotations["deployment.kubernetes.io/revision"].(string); ok && revision != "" {
				properties["deployment_revision"] = revision
			}
		}
	}

	// Extract timestamps from status_info.conditions
	if statusInfo, ok := metaMap["status_info"].(map[string]interface{}); ok {
		// Get observed generation
		if observedGeneration, ok := statusInfo["observedGeneration"].(float64); ok {
			properties["observed_generation"] = int(observedGeneration)
		}

		// Extract most recent timestamps from conditions
		if conditions, ok := statusInfo["conditions"].([]interface{}); ok && len(conditions) > 0 {
			var createdAt, lastDeployedTime string

			for _, cond := range conditions {
				if condMap, ok := cond.(map[string]interface{}); ok {
					// lastTransitionTime represents when the condition first occurred (creation/initial deployment)
					if lastTransitionTime, ok := condMap["lastTransitionTime"].(string); ok && lastTransitionTime != "" {
						if createdAt == "" || lastTransitionTime < createdAt {
							createdAt = lastTransitionTime
						}
					}

					// lastUpdateTime represents the most recent update to this condition (latest deployment)
					if lastUpdateTime, ok := condMap["lastUpdateTime"].(string); ok && lastUpdateTime != "" {
						if lastDeployedTime == "" || lastUpdateTime > lastDeployedTime {
							lastDeployedTime = lastUpdateTime
						}
					}
				}
			}

			// Set the timestamps. last_status_update_time reflects the most
			// recent K8s condition update (any condition), distinct from
			// last_deployed_time which is sourced from configuration_change
			// events and set above in createNodeFromWorkload.
			if createdAt != "" {
				properties["created_at"] = createdAt
			}
			if lastDeployedTime != "" {
				properties["last_status_update_time"] = lastDeployedTime
			}

			// If we only have one of them, use it for both
			if createdAt == "" && lastDeployedTime != "" {
				properties["created_at"] = lastDeployedTime
			}
			if lastDeployedTime == "" && createdAt != "" {
				properties["last_status_update_time"] = createdAt
			}
		}
	}
}

// extractK8sMetadata extracts only essential K8s metadata fields based on workload kind
// This prevents storing large metadata blobs and keeps only what's needed
func (s *K8sSource) extractK8sMetadata(properties map[string]interface{}, metaMap map[string]interface{}, kind string) {
	// Extract common fields for all workloads
	s.extractCommonK8sFields(properties, metaMap)

	// Extract kind-specific fields
	s.extractKindSpecificFields(properties, metaMap, kind)

	// Extract resource usage (CPU/memory) - important for capacity planning
	s.extractResourceUsage(properties, metaMap)

	// Extract status information
	s.extractK8sStatus(properties, metaMap)
}

// extractCommonK8sFields extracts fields common to all K8s workloads
func (s *K8sSource) extractCommonK8sFields(properties map[string]interface{}, metaMap map[string]interface{}) {
	// Extract labels (important for selectors and filtering) - try both config and metadata
	if config, ok := metaMap["config"].(map[string]interface{}); ok {
		if labels, ok := config["labels"].(map[string]interface{}); ok {
			properties["labels"] = labels
		}
		if annotations, ok := config["annotations"].(map[string]interface{}); ok {
			properties["annotations"] = annotations
		}
	}

	// Fallback to metadata if not found in config
	if metadata, ok := metaMap["metadata"].(map[string]interface{}); ok {
		if _, hasLabels := properties["labels"]; !hasLabels {
			if labels, ok := metadata["labels"].(map[string]interface{}); ok {
				properties["labels"] = labels
			}
		}
		if _, hasAnnotations := properties["annotations"]; !hasAnnotations {
			if annotations, ok := metadata["annotations"].(map[string]interface{}); ok {
				properties["annotations"] = annotations
			}
		}
	}
}

// extractKindSpecificFields extracts essential fields based on workload kind
func (s *K8sSource) extractKindSpecificFields(properties map[string]interface{}, metaMap map[string]interface{}, kind string) {
	// serviceAccountName is read from the collector-flattened path
	// (meta.config.service_account) regardless of Kind, so resolve it before
	// the per-Kind switch.
	if san := extractServiceAccountName(metaMap, kind); san != "" {
		properties["service_account_name"] = san
	}

	spec, hasSpec := metaMap["spec"].(map[string]interface{})
	if !hasSpec {
		return
	}

	switch kind {
	case "Deployment", "StatefulSet", "DaemonSet", "ReplicaSet":
		// Replica count (important for scaling)
		if replicas, ok := spec["replicas"].(float64); ok {
			properties["replicas"] = int(replicas)
		}
		// Strategy (important for deployments)
		if strategy, ok := spec["strategy"].(map[string]interface{}); ok {
			if strategyType, ok := strategy["type"].(string); ok {
				properties["strategy_type"] = strategyType
			}
		}

	case "Pod":
		// Node assignment (important for scheduling)
		if nodeName, ok := spec["nodeName"].(string); ok {
			properties["node_name"] = nodeName
		}
		// Restart policy
		if restartPolicy, ok := spec["restartPolicy"].(string); ok {
			properties["restart_policy"] = restartPolicy
		}
		// Pod / host IPs from status. These
		// are indexed in QueryablePropertiesMap[Pod] for scheduling / network
		// filtering but were never emitted; read them from the already-parsed
		// status object (no new API call). Guarded — absent on unscheduled Pods.
		if status, ok := metaMap["status"].(map[string]interface{}); ok {
			if podIP, ok := status["podIP"].(string); ok && podIP != "" {
				properties["pod_ip"] = podIP
			}
			if hostIP, ok := status["hostIP"].(string); ok && hostIP != "" {
				properties["host_ip"] = hostIP
			}
		}

	case "Service", "K8sService":
		// Service type and IPs (critical for networking)
		if clusterIP, ok := spec["clusterIP"].(string); ok {
			properties["cluster_ip"] = clusterIP
		}
		if serviceType, ok := spec["type"].(string); ok {
			properties["service_type"] = serviceType
		}
		// Ports (important for connectivity)
		if ports, ok := spec["ports"].([]interface{}); ok && len(ports) > 0 {
			properties["service_ports"] = ports
		}

	case "Ingress":
		// Ingress class (important for routing)
		if ingressClass, ok := spec["ingressClassName"].(string); ok {
			properties["ingress_class"] = ingressClass
		}

		// Extract load balancer hostname from status (for connecting to AWS ELB)
		if status, ok := metaMap["status"].(map[string]interface{}); ok {
			if loadBalancer, ok := status["loadBalancer"].(map[string]interface{}); ok {
				if ingresses, ok := loadBalancer["ingress"].([]interface{}); ok && len(ingresses) > 0 {
					if firstIngress, ok := ingresses[0].(map[string]interface{}); ok {
						// AWS ELBs use "hostname" field
						if hostname, ok := firstIngress["hostname"].(string); ok && hostname != "" {
							properties["load_balancer_hostname"] = hostname
						}
						// Some ingress controllers use "ip" field instead
						if ip, ok := firstIngress["ip"].(string); ok && ip != "" {
							properties["load_balancer_ip"] = ip
						}
					}
				}
			}
		}

		// Extract ingress rules (hosts and paths)
		if rules, ok := spec["rules"].([]interface{}); ok && len(rules) > 0 {
			hosts := make([]string, 0)
			for _, rule := range rules {
				if ruleMap, ok := rule.(map[string]interface{}); ok {
					if host, ok := ruleMap["host"].(string); ok && host != "" {
						hosts = append(hosts, host)
					}
				}
			}
			if len(hosts) > 0 {
				properties["ingress_hosts"] = hosts
			}
		}

	case "PersistentVolumeClaim":
		// Storage request (important for capacity)
		if resources, ok := spec["resources"].(map[string]interface{}); ok {
			if requests, ok := resources["requests"].(map[string]interface{}); ok {
				if storage, ok := requests["storage"].(string); ok {
					properties["storage_request"] = storage
				}
			}
		}
		// Storage class (important for provisioning)
		if storageClass, ok := spec["storageClassName"].(string); ok {
			properties["storage_class"] = storageClass
		}

	case "ConfigMap", "Secret":
		// Just count of keys (don't store actual data)
		if data, ok := spec["data"].(map[string]interface{}); ok {
			properties["key_count"] = len(data)
		}
	}
}

// extractResourceUsage extracts CPU and memory usage/limits (aggregated, not per-container)
func (s *K8sSource) extractResourceUsage(properties map[string]interface{}, metaMap map[string]interface{}) {
	var totalCPURequests, totalMemoryRequests, totalCPULimits, totalMemoryLimits float64
	var containerNames []string
	var containerImages []string
	var primaryImage string

	// Try config first
	var containers []interface{}
	if config, ok := metaMap["config"].(map[string]interface{}); ok {
		if containersList, ok := config["containers"].([]interface{}); ok {
			containers = containersList
		}
	}

	// Process containers - only extract aggregated resources, not full container details
	if len(containers) > 0 {
		for i, container := range containers {
			if containerMap, ok := container.(map[string]interface{}); ok {
				// Collect container names only
				if name, ok := containerMap["name"].(string); ok {
					containerNames = append(containerNames, name)
				}

				// Collect all container images (needed for ECR relationship matching)
				if image, ok := containerMap["image"].(string); ok && image != "" {
					containerImages = append(containerImages, image)
					// Get first container's image as primary
					if i == 0 {
						primaryImage = image
					}
				}

				// Aggregate resource requests/limits
				if resources, ok := containerMap["resources"].(map[string]interface{}); ok {
					if requests, ok := resources["requests"].(map[string]interface{}); ok {
						if cpu, ok := requests["cpu"].(string); ok {
							totalCPURequests += s.parseResourceValue(cpu)
						}
						if memory, ok := requests["memory"].(string); ok {
							totalMemoryRequests += s.parseResourceValue(memory)
						}
					}
					if limits, ok := resources["limits"].(map[string]interface{}); ok {
						if cpu, ok := limits["cpu"].(string); ok {
							totalCPULimits += s.parseResourceValue(cpu)
						}
						if memory, ok := limits["memory"].(string); ok {
							totalMemoryLimits += s.parseResourceValue(memory)
						}
					}
				}
			}
		}

		// Store only aggregated data, not full container objects
		properties["container_count"] = len(containers)
		if len(containerNames) > 0 {
			properties["container_names"] = containerNames
		}
		if primaryImage != "" {
			properties["primary_image"] = primaryImage
		}
		// Store all container images for ECR relationship matching
		if len(containerImages) > 0 {
			properties["container_images"] = containerImages
		}

		// Add aggregated resource totals
		if totalCPURequests > 0 {
			properties["total_cpu_requests"] = totalCPURequests
		}
		if totalMemoryRequests > 0 {
			properties["total_memory_requests"] = s.formatBytesToHumanReadable(totalMemoryRequests)
		}
		if totalCPULimits > 0 {
			properties["total_cpu_limits"] = totalCPULimits
		}
		if totalMemoryLimits > 0 {
			properties["total_memory_limits"] = s.formatBytesToHumanReadable(totalMemoryLimits)
		}
	}
}

// extractK8sStatus extracts essential status information
func (s *K8sSource) extractK8sStatus(properties map[string]interface{}, metaMap map[string]interface{}) {
	status, hasStatus := metaMap["status"].(map[string]interface{})
	if !hasStatus {
		return
	}

	// Phase (important for operational state)
	if phase, ok := status["phase"].(string); ok && phase != "" {
		properties["phase"] = phase
	}

	// Ready status (important for health checks) - extract from conditions, not store all conditions
	if conditions, ok := status["conditions"].([]interface{}); ok && len(conditions) > 0 {
		for _, cond := range conditions {
			if condMap, ok := cond.(map[string]interface{}); ok {
				if condType, ok := condMap["type"].(string); ok && condType == "Ready" {
					if condStatus, ok := condMap["status"].(string); ok {
						properties["ready"] = (condStatus == "True")
					}
				}
			}
		}
	}

	// Available replicas (important for deployment health)
	if availableReplicas, ok := status["availableReplicas"].(float64); ok {
		properties["available_replicas"] = int(availableReplicas)
	}
	if readyReplicas, ok := status["readyReplicas"].(float64); ok {
		properties["ready_replicas"] = int(readyReplicas)
	}
}
