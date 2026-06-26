package opencostengine

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	_ "github.com/lib/pq"
	"github.com/opencost/opencost/core/pkg/clustercache"
	"github.com/opencost/opencost/core/pkg/log"
	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	v1 "k8s.io/api/core/v1"
	policyv1 "k8s.io/api/policy/v1"
	storagev1 "k8s.io/api/storage/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
)

// NudgebeeClusterCache is a ClusterCache implementation that uses a Postgres database as its backend.
type NudgebeeClusterCache struct {
	db             *DB
	clusterID      string
	relayAuthToken string
	relayEndpoint  string
	httpClient     *http.Client
}

// NewNudgebeeClusterCache creates a new PostgresClusterCache.
func NewNudgebeeClusterCache(db *DB, clusterId string, relayEndpoint, relayAuthToken string) (*NudgebeeClusterCache, error) {
	if clusterId == "" {
		return nil, fmt.Errorf("clusterId cannot be empty")
	}
	return &NudgebeeClusterCache{
		db:             db,
		clusterID:      clusterId,
		relayAuthToken: relayAuthToken,
		relayEndpoint:  relayEndpoint,
		// Reused across every relay call so connections are pooled instead of a
		// fresh client (and socket) per request.
		httpClient: &http.Client{Timeout: 20 * time.Second},
	}, nil
}

// Run is a no-op for the PostgresClusterCache.
func (pcc *NudgebeeClusterCache) Run() {}

// Stop is a no-op for the PostgresClusterCache.
func (pcc *NudgebeeClusterCache) Stop() {}

// execute relay api-calls for k8s apis
func (pcc *NudgebeeClusterCache) executeK8sApi(resourceType, apiVersion, apiGroup string, allNamespaces bool) (string, error) {
	if pcc.clusterID == "" {
		return "", fmt.Errorf("clusterID cannot be empty")
	}
	requestEndpoint := pcc.relayEndpoint + "/request"
	requestPayload := map[string]any{
		"cache":    false,
		"no_sinks": true,
		"body": map[string]any{
			"account_id":  pcc.clusterID,
			"action_name": "get_resource",
			"action_params": map[string]any{
				"resource_type":  resourceType,
				"version":        apiVersion,
				"group":          apiGroup,
				"all_namespaces": allNamespaces,
			},
		},
	}

	jsonData, err := json.Marshal(requestPayload)
	if err != nil {
		return "", err
	}

	req, err := http.NewRequestWithContext(context.Background(), "POST", requestEndpoint, bytes.NewBuffer(jsonData))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("X-SECRET-KEY", pcc.relayAuthToken)
	req.Header.Set("Authorization", "Bearer "+pcc.relayAuthToken)

	resp, err := pcc.httpClient.Do(req)
	if err != nil {
		return "", err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != 200 {
		return "", fmt.Errorf("unexpected status code: %d", resp.StatusCode)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	var responseBody map[string]any
	err = json.Unmarshal(body, &responseBody)
	if err != nil {
		return "", err
	}

	// Fail closed on an API error rather than masking it as empty (which would
	// silently zero out a cluster's costs). Use comma-ok everywhere so a malformed
	// relay response returns an error instead of panicking the (now in-process)
	// service. A genuinely empty result is the only case that returns "[]".
	if errs, ok := responseBody["errors"]; ok && errs != nil {
		return "", fmt.Errorf("relay k8s api returned errors: %v", errs)
	}
	if errMsg, ok := responseBody["error"].(string); ok && errMsg != "" {
		return "", fmt.Errorf("relay k8s api returned error: %s", errMsg)
	}
	responseData, ok := responseBody["data"].(map[string]any)
	if !ok {
		return "", fmt.Errorf("relay k8s api: missing/invalid 'data'")
	}
	findings, ok := responseData["findings"].([]any)
	if !ok {
		return "", fmt.Errorf("relay k8s api: missing/invalid 'findings'")
	}
	if len(findings) == 0 {
		return "[]", nil
	}
	finding, ok := findings[0].(map[string]any)
	if !ok {
		return "", fmt.Errorf("relay k8s api: finding is not a map")
	}
	evidences, ok := finding["evidence"].([]any)
	if !ok {
		return "", fmt.Errorf("relay k8s api: missing/invalid 'evidence'")
	}
	if len(evidences) == 0 {
		return "[]", nil
	}
	evidence, ok := evidences[0].(map[string]any)
	if !ok {
		return "", fmt.Errorf("relay k8s api: evidence is not a map")
	}
	data, ok := evidence["data"].(string)
	if !ok {
		return "", fmt.Errorf("relay k8s api: missing/invalid evidence 'data'")
	}
	var anyData []any
	if err := json.Unmarshal([]byte(data), &anyData); err != nil {
		return "", fmt.Errorf("relay k8s api: unmarshal evidence data: %w", err)
	}
	if len(anyData) == 0 {
		return "[]", nil
	}
	finalData, ok := anyData[0].(map[string]any)
	if !ok {
		return "", fmt.Errorf("relay k8s api: final data is not a map")
	}
	finalStr, ok := finalData["data"].(string)
	if !ok {
		return "", fmt.Errorf("relay k8s api: missing/invalid final 'data'")
	}
	return finalStr, nil
}

// GetAllNamespaces returns all the cached namespaces.
func (pcc *NudgebeeClusterCache) GetAllNamespaces() []*clustercache.Namespace {
	if pcc.clusterID == "" {
		return nil
	}
	rows, err := pcc.db.Connection().Query("SELECT name FROM k8s_namespaces WHERE cloud_account_id = $1  and is_active=true", pcc.clusterID)
	if err != nil {
		log.Errorf("Error getting namespaces from postgres: %s", err)
		return nil
	}
	defer func() { _ = rows.Close() }()

	var namespaces []*clustercache.Namespace
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			log.Errorf("Error scanning namespace: %s", err)
			continue
		}
		namespaces = append(namespaces, &clustercache.Namespace{
			Name:        name,
			Labels:      map[string]string{},
			Annotations: map[string]string{},
		})
	}

	if err := rows.Err(); err != nil {
		log.Errorf("Error iterating namespaces: %s", err)
	}

	return namespaces
}

// GetAllNodes returns all the cached nodes.
func (pcc *NudgebeeClusterCache) GetAllNodes() []*clustercache.Node {
	if pcc.clusterID == "" {
		return nil
	}
	rows, err := pcc.db.Connection().Query("SELECT name, labels, cpu_capacity, memory_capacity, cpu_allocatable, memory_allocatable, meta, conditions FROM k8s_nodes WHERE cloud_account_id = $1 and is_active = true", pcc.clusterID)
	if err != nil {
		log.Errorf("Error getting nodes from postgres: %s", err)
		return nil
	}
	defer func() { _ = rows.Close() }()

	var nodes []*clustercache.Node
	for rows.Next() {
		var name string
		var conditions *string
		var labels []byte
		var meta []byte
		var cpuCapacity, cpuAllocatable float64
		var memoryCapacity, memoryAllocatable int64

		if err := rows.Scan(&name, &labels, &cpuCapacity, &memoryCapacity, &cpuAllocatable, &memoryAllocatable, &meta, &conditions); err != nil {
			log.Errorf("Error scanning node: %s", err)
			continue
		}

		var labelsMap map[string]string
		if len(labels) > 0 {
			if err := json.Unmarshal(labels, &labelsMap); err != nil {
				log.Errorf("Error unmarshalling node labels: %s", err)
			}
		}

		var metaMap map[string]any
		if len(meta) > 0 {
			if err := json.Unmarshal(meta, &metaMap); err != nil {
				log.Errorf("Error unmarshalling node meta: %s", err)
			}
		}

		annotations := make(map[string]string)
		systemInfo := v1.NodeSystemInfo{}
		addresses := []v1.NodeAddress{}

		specProviderId := ""

		if nodeInfo, ok := metaMap["node_info"].(map[string]any); ok {
			if annotationsMap, ok := nodeInfo["annotations"].(map[string]any); ok {
				for k, v := range annotationsMap {
					if str, ok := v.(string); ok {
						annotations[k] = str
					}
				}
			}

			if addressesses, ok := nodeInfo["addresses"].([]any); ok {
				for _, addressAny := range addressesses {
					if addr, ok := addressAny.(string); ok {
						addresses = append(addresses, v1.NodeAddress{
							Type:    v1.NodeInternalIP,
							Address: addr,
						})
					}
				}
			}
		}

		// `system` and `provider_id` are root-level keys of metaMap, not nested
		// under node_info — read them independently so a missing/!map node_info
		// doesn't skip them. Comma-ok everywhere: a non-string provider_id would
		// otherwise panic the in-process service.
		if system, ok := metaMap["system"].(map[string]any); ok {
			if bootId, ok := system["boot_id"].(string); ok {
				systemInfo.BootID = bootId
			}
			if osImage, ok := system["os_image"].(string); ok {
				systemInfo.OSImage = osImage
			}
			if machine, ok := system["machine_id"].(string); ok {
				systemInfo.MachineID = machine
			}
			if systemUUID, ok := system["system_uuid"].(string); ok {
				systemInfo.SystemUUID = systemUUID
			}
			if architecture, ok := system["architecture"].(string); ok {
				systemInfo.Architecture = architecture
			}
			if kernelVersion, ok := system["kernel_version"].(string); ok {
				systemInfo.KernelVersion = kernelVersion
			}
			if kubeletVersion, ok := system["kubelet_version"].(string); ok {
				systemInfo.KubeletVersion = kubeletVersion
			}
			if operatingSystem, ok := system["operating_system"].(string); ok {
				systemInfo.OperatingSystem = operatingSystem
			}
			if containerRuntimeVersion, ok := system["container_runtime_version"].(string); ok {
				systemInfo.ContainerRuntimeVersion = containerRuntimeVersion
			}
		}

		if providerID, ok := metaMap["provider_id"].(string); ok {
			specProviderId = providerID
		}

		nodePhase := v1.NodeRunning

		conditionsArr := []v1.NodeCondition{}
		if conditions != nil && *conditions != "" {
			conditionsStr := *conditions
			// Ready:False
			for _, conditionStr := range strings.Split(conditionsStr, ",") {
				conditionStrSplits := strings.Split(strings.TrimSpace(conditionStr), ":")
				// Skip malformed entries lacking the "Type:Status" colon rather
				// than panicking on conditionStrSplits[1].
				if len(conditionStrSplits) < 2 {
					continue
				}
				condition := v1.NodeCondition{
					Type:   v1.NodeConditionType(conditionStrSplits[0]),
					Status: v1.ConditionStatus(conditionStrSplits[1]),
				}

				conditionsArr = append(conditionsArr, condition)
			}
		}

		if specProviderId == "" {
			for k := range labelsMap {
				if strings.Contains(k, "civo") {
					specProviderId = "civo://" + uuid.NewString()
					break
				} else if strings.Contains(k, "cloud.google.com") {
					specProviderId = fmt.Sprintf("gce://%s/%s/%s", "nb-random-project", labelsMap["topology.kubernetes.io/zone"], labelsMap["kubernetes.io/hostname"])
					break
				} else if strings.Contains(k, "eks.amazonaws.com") {
					specProviderId = fmt.Sprintf("aws://%s/%s", labelsMap["topology.kubernetes.io/zone"], labelsMap["kubernetes.io/hostname"])
					break
				}
			}
		}

		nodes = append(nodes, &clustercache.Node{
			Name:           name,
			Labels:         labelsMap,
			Annotations:    annotations,
			SpecProviderID: specProviderId,
			Status: v1.NodeStatus{
				Capacity: v1.ResourceList{
					v1.ResourceCPU:    *resource.NewMilliQuantity(int64(cpuCapacity*1000), resource.DecimalSI),
					v1.ResourceMemory: *resource.NewQuantity(memoryCapacity*1024*1024, resource.BinarySI),
				},
				Allocatable: v1.ResourceList{
					v1.ResourceCPU:    *resource.NewMilliQuantity(int64(cpuAllocatable*1000), resource.DecimalSI),
					v1.ResourceMemory: *resource.NewQuantity(memoryAllocatable*1024*1024, resource.BinarySI),
				},
				Phase:           nodePhase,
				Conditions:      conditionsArr,
				NodeInfo:        systemInfo,
				Addresses:       addresses,
				DaemonEndpoints: v1.NodeDaemonEndpoints{},
				Images:          []v1.ContainerImage{},
				VolumesInUse:    []v1.UniqueVolumeName{},
				VolumesAttached: []v1.AttachedVolume{},
			},
		})
	}

	if err := rows.Err(); err != nil {
		log.Errorf("Error iterating nodes: %s", err)
	}

	return nodes
}

// GetAllPods returns all the cached pods.
func (pcc *NudgebeeClusterCache) GetAllPods() []*clustercache.Pod {
	if pcc.clusterID == "" {
		return nil
	}
	rows, err := pcc.db.Connection().Query("SELECT cloud_resource_id::text, name, namespace, labels, status, node_name, meta, workload_name FROM k8s_pods WHERE cloud_account_id = $1 and is_active=true", pcc.clusterID)
	if err != nil {
		log.Errorf("Error getting pods from postgres: %s", err)
		return nil
	}
	defer func() { _ = rows.Close() }()

	var pods []*clustercache.Pod
	for rows.Next() {
		var id, name, namespace, status, nodeName string
		var labels, meta []byte
		var workloadName *string

		if err := rows.Scan(&id, &name, &namespace, &labels, &status, &nodeName, &meta, &workloadName); err != nil {
			log.Errorf("Error scanning pod: %s", err)
			continue
		}

		labelsMap := map[string]string{}
		if len(labels) > 0 {
			if err := json.Unmarshal(labels, &labelsMap); err != nil {
				log.Errorf("Error unmarshalling pod labels: %s", err)
			}
		}
		if workloadName != nil {
			labelsMap["nb-app"] = *workloadName
		}

		var metaMap map[string]any
		if len(meta) > 0 {
			if err := json.Unmarshal(meta, &metaMap); err != nil {
				log.Errorf("Error unmarshalling pod meta: %s", err)
			}
		}

		podIp := ""
		annotations := make(map[string]string)
		ownerReferences := []metav1.OwnerReference{}
		containerStatuses := []v1.ContainerStatus{}
		containers := []clustercache.Container{}
		volumes := []v1.Volume{}
		restartPolicy := v1.RestartPolicyAlways

		if podconfig, ok := metaMap["config"].(map[string]any); ok {
			if ip, ok := podconfig["ip"].(string); ok {
				podIp = ip
			}

			if annotationsMap, ok := podconfig["annotations"].(map[string]any); ok {
				for k, v := range annotationsMap {
					if str, ok := v.(string); ok {
						annotations[k] = str
					}
				}
			}

			if ownerList, ok := podconfig["owner"].([]any); ok {
				for _, ownerAny := range ownerList {
					owner := metav1.OwnerReference{}
					if ownerMap, ok := ownerAny.(map[string]any); ok {
						if apiVersion, ok := ownerMap["api_version"].(string); ok {
							owner.APIVersion = apiVersion
						}
						if kind, ok := ownerMap["kind"].(string); ok {
							owner.Kind = kind
						}
						if name, ok := ownerMap["name"].(string); ok {
							owner.Name = name
						}
						if uid, ok := ownerMap["uid"].(string); ok {
							owner.UID = types.UID(uid)
						}
						if controller, ok := ownerMap["controller"].(bool); ok {
							owner.Controller = &controller
						}
					}
					ownerReferences = append(ownerReferences, owner)
				}
			}

			if containersMap, ok := podconfig["containers"].([]any); ok {
				for _, containerAny := range containersMap {
					container := clustercache.Container{}
					if containerMap, ok := containerAny.(map[string]any); ok {
						if name, ok := containerMap["name"].(string); ok {
							container.Name = name
						}
						if resources, ok := containerMap["resources"].(map[string]any); ok {
							if requests, ok := resources["requests"].(map[string]any); ok {
								container.Resources.Requests = v1.ResourceList{}
								if cpu, ok := requests["cpu"].(string); ok {
									container.Resources.Requests[v1.ResourceCPU] = resource.MustParse(cpu)
								}
								if memory, ok := requests["memory"].(string); ok {
									container.Resources.Requests[v1.ResourceMemory] = resource.MustParse(memory)
								}
							}
							if limits, ok := resources["limits"].(map[string]any); ok {
								container.Resources.Limits = v1.ResourceList{}
								if cpu, ok := limits["cpu"].(string); ok {
									container.Resources.Limits[v1.ResourceCPU] = resource.MustParse(cpu)
								}
								if memory, ok := limits["memory"].(string); ok {
									container.Resources.Limits[v1.ResourceMemory] = resource.MustParse(memory)
								}
							}
						}
					}
					containers = append(containers, container)
				}
			}

		}

		if podStatus, ok := metaMap["status_info"].(map[string]any); ok {
			if phase, ok := podStatus["phase"].(string); ok {
				status = phase
			}
			if containerStatusesMap, ok := podStatus["containerStatuses"].([]any); ok {
				for _, containerStatusAny := range containerStatusesMap {
					containerStatus := v1.ContainerStatus{}
					if containerStatusMap, ok := containerStatusAny.(map[string]any); ok {
						if name, ok := containerStatusMap["name"].(string); ok {
							containerStatus.Name = name
						}
						if image, ok := containerStatusMap["image"].(string); ok {
							containerStatus.Image = image
						}
						if imageID, ok := containerStatusMap["imageID"].(string); ok {
							containerStatus.ImageID = imageID
						}
						if containerID, ok := containerStatusMap["containerID"].(string); ok {
							containerStatus.ContainerID = containerID
						}
						if restartCount, ok := containerStatusMap["restartCount"].(float64); ok {
							containerStatus.RestartCount = int32(restartCount)
						}
						if started, ok := containerStatusMap["started"].(bool); ok {
							containerStatus.Started = &started
						}
						if ready, ok := containerStatusMap["ready"].(bool); ok {
							containerStatus.Ready = ready
						}

						if state, ok := containerStatusMap["state"].(map[string]any); ok {
							if running, ok := state["running"].(map[string]any); ok {
								if startedAt, ok := running["started_at"].(string); ok {
									startedAtTime, _ := time.Parse(time.RFC3339, startedAt)
									containerStatus.State.Running = &v1.ContainerStateRunning{
										StartedAt: metav1.Time{
											Time: startedAtTime,
										},
									}
								}
							}
						}
					}
					containerStatuses = append(containerStatuses, containerStatus)
				}
			}
		}

		pods = append(pods, &clustercache.Pod{
			Name:            name,
			Namespace:       namespace,
			Labels:          labelsMap,
			UID:             types.UID(id),
			OwnerReferences: ownerReferences,
			Annotations:     annotations,
			Spec: clustercache.PodSpec{
				NodeName:      nodeName,
				Containers:    containers,
				Volumes:       volumes,
				RestartPolicy: restartPolicy,
			},
			Status: clustercache.PodStatus{
				Phase:             v1.PodPhase(status),
				PodIP:             podIp,
				ContainerStatuses: containerStatuses,
			},
		})
	}

	if err := rows.Err(); err != nil {
		log.Errorf("Error iterating pods: %s", err)
	}

	return pods
}

// GetAllServices returns all the cached services.
func (pcc *NudgebeeClusterCache) GetAllServices() []*clustercache.Service {
	serviceData, err := pcc.executeK8sApi("services", "v1", "", true)
	if err != nil {
		log.Errorf("Error getting services from relay: %s", err)
		return nil
	}

	var serviceList v1.ServiceList
	err = json.Unmarshal([]byte(serviceData), &serviceList.Items)
	if err != nil {
		log.Errorf("Error unmarshalling services: %s", err)
		return nil
	}

	var services []*clustercache.Service
	for _, service := range serviceList.Items {
		s := clustercache.TransformService(&service)
		services = append(services, s)
	}

	return services
}

// GetAllDaemonSets returns all the cached DaemonSets.
func (pcc *NudgebeeClusterCache) GetAllDaemonSets() []*clustercache.DaemonSet {
	if pcc.clusterID == "" {
		return nil
	}
	rows, err := pcc.db.Connection().Query("SELECT name, namespace, labels, meta FROM k8s_workloads WHERE cloud_account_id = $1 AND kind = 'DaemonSet'", pcc.clusterID)
	if err != nil {
		log.Errorf("Error getting daemonsets from postgres: %s", err)
		return nil
	}
	defer func() { _ = rows.Close() }()

	var daemonSets []*clustercache.DaemonSet
	for rows.Next() {
		var name, namespace string
		var labels []byte
		var meta []byte

		if err := rows.Scan(&name, &namespace, &labels, &meta); err != nil {
			log.Errorf("Error scanning daemonset: %s", err)
			continue
		}

		var labelsMap map[string]string
		if err := json.Unmarshal(labels, &labelsMap); err != nil {
			log.Errorf("Error unmarshalling daemonset labels: %s", err)
		}

		var metaMap map[string]any
		if err := json.Unmarshal(meta, &metaMap); err != nil {
			log.Errorf("Error unmarshalling daemonset meta: %s", err)
		}

		containers := []v1.Container{}

		if deploymentConfigs, ok := metaMap["config"].(map[string]any); ok {
			if containersList, ok := deploymentConfigs["containers"].([]any); ok {
				for _, c := range containersList {
					if containerMap, ok := c.(map[string]any); ok {
						container := v1.Container{}
						if name, ok := containerMap["name"].(string); ok {
							container.Name = name
						}

						if resources, ok := containerMap["resources"].(map[string]any); ok {
							if requests, ok := resources["requests"].(map[string]any); ok {
								container.Resources.Requests = v1.ResourceList{}
								if cpu, ok := requests["cpu"].(string); ok {
									container.Resources.Requests[v1.ResourceCPU] = resource.MustParse(cpu)
								}
								if memory, ok := requests["memory"].(string); ok {
									container.Resources.Requests[v1.ResourceMemory] = resource.MustParse(memory)
								}
							}
							if limits, ok := resources["limits"].(map[string]any); ok {
								container.Resources.Limits = v1.ResourceList{}
								if cpu, ok := limits["cpu"].(string); ok {
									container.Resources.Limits[v1.ResourceCPU] = resource.MustParse(cpu)
								}
								if memory, ok := limits["memory"].(string); ok {
									container.Resources.Limits[v1.ResourceMemory] = resource.MustParse(memory)
								}
							}
						}

						if image, ok := containerMap["image"].(string); ok {
							container.Image = image
						}

						container.Args = []string{}
						if args, ok := containerMap["args"].([]any); ok {
							for _, arg := range args {
								if str, ok := arg.(string); ok {
									container.Args = append(container.Args, str)
								}
							}
						}

						container.Env = []v1.EnvVar{}
						if env, ok := containerMap["env"].([]any); ok {
							for _, e := range env {
								if envMap, ok := e.(map[string]any); ok {
									envVar := v1.EnvVar{}
									if name, ok := envMap["name"].(string); ok {
										envVar.Name = name
									}
									if value, ok := envMap["value"].(string); ok {
										envVar.Value = value
									}
									container.Env = append(container.Env, envVar)
								}
							}
						}

						container.Ports = []v1.ContainerPort{}
						if ports, ok := containerMap["ports"].([]any); ok {
							for _, p := range ports {
								if portMap, ok := p.(map[string]any); ok {
									containerPort := v1.ContainerPort{}
									if name, ok := portMap["name"].(string); ok {
										containerPort.Name = name
									}
									if cp, ok := portMap["container_port"].(float64); ok {
										containerPort.ContainerPort = int32(cp)
										container.Ports = append(container.Ports, containerPort)
									}
								} else if port2, ok := p.(float64); ok {
									containerPort := v1.ContainerPort{
										ContainerPort: int32(port2),
									}
									container.Ports = append(container.Ports, containerPort)
								}
							}
						}

						//volume_mounts
						container.VolumeMounts = []v1.VolumeMount{}
						if volumeMounts, ok := containerMap["volume_mounts"].([]any); ok {
							for _, vm := range volumeMounts {
								if vmMap, ok := vm.(map[string]any); ok {
									volumeMount := v1.VolumeMount{}
									if name, ok := vmMap["name"].(string); ok {
										volumeMount.Name = name
									}
									if mountPath, ok := vmMap["mount_path"].(string); ok {
										volumeMount.MountPath = mountPath
									}
									container.VolumeMounts = append(container.VolumeMounts, volumeMount)
								}
							}
						}

						// liveness_probe
						if livenessProbe, ok := containerMap["liveness_probe"].(map[string]any); ok {
							probe := &v1.Probe{}
							if v, ok := i32(livenessProbe, "initial_delay_seconds"); ok {
								probe.InitialDelaySeconds = v
							}
							if v, ok := i32(livenessProbe, "timeout_seconds"); ok {
								probe.TimeoutSeconds = v
							}
							if v, ok := i32(livenessProbe, "period_seconds"); ok {
								probe.PeriodSeconds = v
							}
							if v, ok := i32(livenessProbe, "success_threshold"); ok {
								probe.SuccessThreshold = v
							}
							if v, ok := i32(livenessProbe, "failure_threshold"); ok {
								probe.FailureThreshold = v
							}
							container.LivenessProbe = probe
						}

						// readiness_probe
						if readinessProbe, ok := containerMap["readiness_probe"].(map[string]any); ok {
							probe := &v1.Probe{}
							if v, ok := i32(readinessProbe, "initial_delay_seconds"); ok {
								probe.InitialDelaySeconds = v
							}
							if v, ok := i32(readinessProbe, "timeout_seconds"); ok {
								probe.TimeoutSeconds = v
							}
							if v, ok := i32(readinessProbe, "period_seconds"); ok {
								probe.PeriodSeconds = v
							}
							if v, ok := i32(readinessProbe, "success_threshold"); ok {
								probe.SuccessThreshold = v
							}
							if v, ok := i32(readinessProbe, "failure_threshold"); ok {
								probe.FailureThreshold = v
							}
							container.ReadinessProbe = probe
						}

						containers = append(containers, container)
					}
				}
			}
		}

		daemonSets = append(daemonSets, &clustercache.DaemonSet{
			Name:           name,
			Namespace:      namespace,
			Labels:         labelsMap,
			SpecContainers: containers,
		})
	}

	if err := rows.Err(); err != nil {
		log.Errorf("Error iterating daemonsets: %s", err)
	}

	return daemonSets
}

// GetAllDeployments returns all the cached deployments.
func (pcc *NudgebeeClusterCache) GetAllDeployments() []*clustercache.Deployment {
	if pcc.clusterID == "" {
		return nil
	}
	rows, err := pcc.db.Connection().Query("SELECT name, namespace, labels, meta FROM k8s_workloads WHERE cloud_account_id = $1 AND kind = 'Deployment' and is_active=true", pcc.clusterID)
	if err != nil {
		log.Errorf("Error getting deployments from postgres: %s", err)
		return nil
	}
	defer func() { _ = rows.Close() }()

	var deployments []*clustercache.Deployment
	for rows.Next() {
		var name, namespace string
		var labels, meta []byte

		if err := rows.Scan(&name, &namespace, &labels, &meta); err != nil {
			log.Errorf("Error scanning deployment: %s", err)
			continue
		}

		var labelsMap map[string]string
		if err := json.Unmarshal(labels, &labelsMap); err != nil {
			log.Errorf("Error unmarshalling deployment labels: %s", err)
		}

		var metaMap map[string]any
		if err := json.Unmarshal(meta, &metaMap); err != nil {
			log.Errorf("Error unmarshalling deployment meta: %s", err)
		}

		var replicas, statusAvailableReplicas int32
		if statusInfo, ok := metaMap["status_info"].(map[string]any); ok {
			if r, ok := statusInfo["replicas"].(float64); ok {
				replicas = int32(r)
			}
			if r, ok := statusInfo["readyReplicas"].(float64); ok {
				statusAvailableReplicas = int32(r)
			}
		}

		annotations := make(map[string]string)
		matchLabels := make(map[string]string)
		specSelector := metav1.LabelSelector{
			MatchLabels: map[string]string{
				"nb-app": name,
			},
		}
		deploymentStrategy := appsv1.DeploymentStrategy{}
		podSpec := clustercache.PodSpec{}

		if deploymentConfigs, ok := metaMap["config"].(map[string]any); ok {
			if annotationsMap, ok := deploymentConfigs["annotations"].(map[string]any); ok {
				for k, v := range annotationsMap {
					if str, ok := v.(string); ok {
						annotations[k] = str
					}
				}
			}

			if containersList, ok := deploymentConfigs["containers"].([]any); ok {
				for _, c := range containersList {
					if containerMap, ok := c.(map[string]any); ok {
						container := clustercache.Container{}
						if name, ok := containerMap["name"].(string); ok {
							container.Name = name
						}

						if resources, ok := containerMap["resources"].(map[string]any); ok {
							if requests, ok := resources["requests"].(map[string]any); ok {
								container.Resources.Requests = v1.ResourceList{}
								if cpu, ok := requests["cpu"].(string); ok {
									container.Resources.Requests[v1.ResourceCPU] = resource.MustParse(cpu)
								}
								if memory, ok := requests["memory"].(string); ok {
									container.Resources.Requests[v1.ResourceMemory] = resource.MustParse(memory)
								}
							}
							if limits, ok := resources["limits"].(map[string]any); ok {
								container.Resources.Limits = v1.ResourceList{}
								if cpu, ok := limits["cpu"].(string); ok {
									container.Resources.Limits[v1.ResourceCPU] = resource.MustParse(cpu)
								}
								if memory, ok := limits["memory"].(string); ok {
									container.Resources.Limits[v1.ResourceMemory] = resource.MustParse(memory)
								}
							}
						}
						podSpec.Containers = append(podSpec.Containers, container)
					}
				}
			}

			if volumes, ok := deploymentConfigs["volumes"].([]any); ok {
				for _, v := range volumes {
					if volumeMap, ok := v.(map[string]any); ok {
						volume := v1.Volume{}
						if name, ok := volumeMap["name"].(string); ok {
							volume.Name = name
						}
						if secret, ok := volumeMap["secret"].(map[string]any); ok {
							if secretName, ok := secret["secret_name"].(string); ok {
								volume.Secret = &v1.SecretVolumeSource{
									SecretName: secretName,
								}
							}
						}
						if configMap, ok := volumeMap["config_map"].(map[string]any); ok {
							if configMapName, ok := configMap["config_map_name"].(string); ok {
								volume.ConfigMap = &v1.ConfigMapVolumeSource{
									LocalObjectReference: v1.LocalObjectReference{
										Name: configMapName,
									},
								}
							}
						}
						if _, ok := volumeMap["empty_dir"].(map[string]any); ok {
							volume.EmptyDir = &v1.EmptyDirVolumeSource{}
						}
						if _, ok := volumeMap["host_path"].(map[string]any); ok {
							volume.HostPath = &v1.HostPathVolumeSource{}
						}
						if pvc, ok := volumeMap["persistent_volume_claim"].(map[string]any); ok {
							volume.PersistentVolumeClaim = &v1.PersistentVolumeClaimVolumeSource{
								ClaimName: pvc["claim_name"].(string),
								ReadOnly:  false,
							}
						}
						if _, ok := volumeMap["csi"].(map[string]any); ok {
							volume.CSI = &v1.CSIVolumeSource{}
						}
						if _, ok := volumeMap["git_repo"].(map[string]any); ok {
							volume.GitRepo = &v1.GitRepoVolumeSource{}
						}
						if _, ok := volumeMap["downward_api"].(map[string]any); ok {
							volume.DownwardAPI = &v1.DownwardAPIVolumeSource{}
						}

						podSpec.Volumes = append(podSpec.Volumes, volume)
					}
				}
			}
		}

		deployments = append(deployments, &clustercache.Deployment{
			Name:                    name,
			Namespace:               namespace,
			Labels:                  labelsMap,
			SpecReplicas:            &replicas,
			StatusAvailableReplicas: statusAvailableReplicas,
			Annotations:             annotations,
			MatchLabels:             matchLabels,
			SpecSelector:            &specSelector,
			SpecStrategy:            deploymentStrategy,
			PodSpec:                 podSpec,
		})
	}

	if err := rows.Err(); err != nil {
		log.Errorf("Error iterating deployments: %s", err)
	}

	return deployments
}

// GetAllStatefulSets returns all the cached StatefulSets.
func (pcc *NudgebeeClusterCache) GetAllStatefulSets() []*clustercache.StatefulSet {
	if pcc.clusterID == "" {
		return nil
	}
	rows, err := pcc.db.Connection().Query("SELECT name, namespace, labels, meta FROM k8s_workloads WHERE cloud_account_id = $1 AND kind = 'StatefulSet' and is_active=true", pcc.clusterID)
	if err != nil {
		log.Errorf("Error getting statefulsets from postgres: %s", err)
		return nil
	}
	defer func() { _ = rows.Close() }()

	var statefulSets []*clustercache.StatefulSet
	for rows.Next() {
		var name, namespace string
		var labels, meta []byte

		if err := rows.Scan(&name, &namespace, &labels, &meta); err != nil {
			log.Errorf("Error scanning statefulset: %s", err)
			continue
		}

		var labelsMap map[string]string
		if err := json.Unmarshal(labels, &labelsMap); err != nil {
			log.Errorf("Error unmarshalling statefulset labels: %s", err)
		}

		var metaMap map[string]any
		if err := json.Unmarshal(meta, &metaMap); err != nil {
			log.Errorf("Error unmarshalling statefulset meta: %s", err)
		}

		annotations := make(map[string]string)
		specSelector := metav1.LabelSelector{
			MatchLabels: map[string]string{
				"nb-app": name,
			},
		}
		podSpec := clustercache.PodSpec{}

		var replicas int32
		if statusInfo, ok := metaMap["status_info"].(map[string]any); ok {
			if r, ok := statusInfo["replicas"].(float64); ok {
				replicas = int32(r)
			}
		}

		if deploymentConfigs, ok := metaMap["config"].(map[string]any); ok {
			if annotationsMap, ok := deploymentConfigs["annotations"].(map[string]any); ok {
				for k, v := range annotationsMap {
					if str, ok := v.(string); ok {
						annotations[k] = str
					}
				}
			}

			if containersList, ok := deploymentConfigs["containers"].([]any); ok {
				for _, c := range containersList {
					if containerMap, ok := c.(map[string]any); ok {
						container := clustercache.Container{}
						if name, ok := containerMap["name"].(string); ok {
							container.Name = name
						}

						if resources, ok := containerMap["resources"].(map[string]any); ok {
							if requests, ok := resources["requests"].(map[string]any); ok {
								container.Resources.Requests = v1.ResourceList{}
								if cpu, ok := requests["cpu"].(string); ok {
									container.Resources.Requests[v1.ResourceCPU] = resource.MustParse(cpu)
								}
								if memory, ok := requests["memory"].(string); ok {
									container.Resources.Requests[v1.ResourceMemory] = resource.MustParse(memory)
								}
							}
							if limits, ok := resources["limits"].(map[string]any); ok {
								container.Resources.Limits = v1.ResourceList{}
								if cpu, ok := limits["cpu"].(string); ok {
									container.Resources.Limits[v1.ResourceCPU] = resource.MustParse(cpu)
								}
								if memory, ok := limits["memory"].(string); ok {
									container.Resources.Limits[v1.ResourceMemory] = resource.MustParse(memory)
								}
							}
						}
						podSpec.Containers = append(podSpec.Containers, container)
					}
				}
			}

			if volumes, ok := deploymentConfigs["volumes"].([]any); ok {
				for _, v := range volumes {
					if volumeMap, ok := v.(map[string]any); ok {
						volume := v1.Volume{}
						if name, ok := volumeMap["name"].(string); ok {
							volume.Name = name
						}
						if secret, ok := volumeMap["secret"].(map[string]any); ok {
							if secretName, ok := secret["secret_name"].(string); ok {
								volume.Secret = &v1.SecretVolumeSource{
									SecretName: secretName,
								}
							}
						}
						if configMap, ok := volumeMap["config_map"].(map[string]any); ok {
							if configMapName, ok := configMap["config_map_name"].(string); ok {
								volume.ConfigMap = &v1.ConfigMapVolumeSource{
									LocalObjectReference: v1.LocalObjectReference{
										Name: configMapName,
									},
								}
							}
						}
						if _, ok := volumeMap["empty_dir"].(map[string]any); ok {
							volume.EmptyDir = &v1.EmptyDirVolumeSource{}
						}
						if _, ok := volumeMap["host_path"].(map[string]any); ok {
							volume.HostPath = &v1.HostPathVolumeSource{}
						}
						if pvc, ok := volumeMap["persistent_volume_claim"].(map[string]any); ok {
							volume.PersistentVolumeClaim = &v1.PersistentVolumeClaimVolumeSource{
								ClaimName: pvc["claim_name"].(string),
								ReadOnly:  false,
							}
						}
						if _, ok := volumeMap["csi"].(map[string]any); ok {
							volume.CSI = &v1.CSIVolumeSource{}
						}
						if _, ok := volumeMap["git_repo"].(map[string]any); ok {
							volume.GitRepo = &v1.GitRepoVolumeSource{}
						}
						if _, ok := volumeMap["downward_api"].(map[string]any); ok {
							volume.DownwardAPI = &v1.DownwardAPIVolumeSource{}
						}

						podSpec.Volumes = append(podSpec.Volumes, volume)
					}
				}
			}
		}

		statefulSets = append(statefulSets, &clustercache.StatefulSet{
			Name:         name,
			Namespace:    namespace,
			Labels:       labelsMap,
			SpecReplicas: &replicas,
			Annotations:  annotations,
			PodSpec:      podSpec,
			SpecSelector: &specSelector,
		})
	}

	if err := rows.Err(); err != nil {
		log.Errorf("Error iterating statefulsets: %s", err)
	}

	return statefulSets
}

// GetAllReplicaSets returns all the cached ReplicaSets.
func (pcc *NudgebeeClusterCache) GetAllReplicaSets() []*clustercache.ReplicaSet {
	rsData, err := pcc.executeK8sApi("replicasets", "v1", "apps", true)
	if err != nil {
		log.Errorf("Error getting replicasets from relay: %s", err)
		return nil
	}

	var rsList appsv1.ReplicaSetList
	err = json.Unmarshal([]byte(rsData), &rsList.Items)
	if err != nil {
		log.Errorf("Error unmarshalling replicasets: %s", err)
		return nil
	}

	var rss []*clustercache.ReplicaSet
	for _, rs := range rsList.Items {
		r := clustercache.TransformReplicaSet(&rs)
		rss = append(rss, r)
	}

	return rss
}

// GetAllPersistentVolumes returns all the cached persistent volumes.
func (pcc *NudgebeeClusterCache) GetAllPersistentVolumes() []*clustercache.PersistentVolume {
	pvData, err := pcc.executeK8sApi("persistentvolumes", "v1", "", true)
	if err != nil {
		log.Errorf("Error getting persistentvolumes from relay: %s", err)
		return nil
	}

	var pvList v1.PersistentVolumeList
	err = json.Unmarshal([]byte(pvData), &pvList.Items)
	if err != nil {
		log.Errorf("Error unmarshalling persistentvolumes: %s", err)
		return nil
	}

	var persistentVolumes []*clustercache.PersistentVolume
	for _, pv := range pvList.Items {
		p := clustercache.TransformPersistentVolume(&pv)
		persistentVolumes = append(persistentVolumes, p)
	}

	return persistentVolumes
}

// GetAllPersistentVolumeClaims returns all the cached persistent volume claims.
func (pcc *NudgebeeClusterCache) GetAllPersistentVolumeClaims() []*clustercache.PersistentVolumeClaim {
	pvcData, err := pcc.executeK8sApi("persistentvolumeclaims", "v1", "", true)
	if err != nil {
		log.Errorf("Error getting persistentvolumeclaims from relay: %s", err)
		return nil
	}

	var pvcList v1.PersistentVolumeClaimList
	err = json.Unmarshal([]byte(pvcData), &pvcList.Items)
	if err != nil {
		log.Errorf("Error unmarshalling persistentvolumeclaims: %s", err)
		return nil
	}

	var pvcs []*clustercache.PersistentVolumeClaim
	for _, pvc := range pvcList.Items {
		p := clustercache.TransformPersistentVolumeClaim(&pvc)
		pvcs = append(pvcs, p)
	}

	return pvcs
}

// GetAllStorageClasses returns all the cached storage classes.
func (pcc *NudgebeeClusterCache) GetAllStorageClasses() []*clustercache.StorageClass {
	scData, err := pcc.executeK8sApi("storageclasses", "v1", "storage.k8s.io", true)
	if err != nil {
		log.Errorf("Error getting storageclasses from relay: %s", err)
		return nil
	}

	var scList storagev1.StorageClassList
	err = json.Unmarshal([]byte(scData), &scList.Items)
	if err != nil {
		log.Errorf("Error unmarshalling storageclasses: %s", err)
		return nil
	}

	var scs []*clustercache.StorageClass
	for _, sc := range scList.Items {
		s := clustercache.TransformStorageClass(&sc)
		scs = append(scs, s)
	}

	return scs
}

// GetAllJobs returns all the cached jobs.
func (pcc *NudgebeeClusterCache) GetAllJobs() []*clustercache.Job {
	if pcc.clusterID == "" {
		return nil
	}
	rows, err := pcc.db.Connection().Query("SELECT name, namespace, meta FROM k8s_workloads WHERE cloud_account_id = $1 AND kind = 'Job'", pcc.clusterID)
	if err != nil {
		log.Errorf("Error getting jobs from postgres: %s", err)
		return nil
	}
	defer func() { _ = rows.Close() }()

	var jobs []*clustercache.Job
	for rows.Next() {
		var name, namespace string
		var meta []byte

		if err := rows.Scan(&name, &namespace, &meta); err != nil {
			log.Errorf("Error scanning job: %s", err)
			continue
		}

		var metaMap map[string]any
		if err := json.Unmarshal(meta, &metaMap); err != nil {
			log.Errorf("Error unmarshalling job meta: %s", err)
		}

		var completions int32
		if c, ok := metaMap["completions"].(float64); ok {
			completions = int32(c)
		}

		jobs = append(jobs, &clustercache.Job{
			Name:      name,
			Namespace: namespace,
			Status: batchv1.JobStatus{
				Succeeded: completions,
			},
		})
	}

	if err := rows.Err(); err != nil {
		log.Errorf("Error iterating jobs: %s", err)
	}

	return jobs
}

// GetAllPodDisruptionBudgets returns all cached pod disruption budgets.
func (pcc *NudgebeeClusterCache) GetAllPodDisruptionBudgets() []*clustercache.PodDisruptionBudget {
	pdbData, err := pcc.executeK8sApi("poddisruptionbudgets", "v1", "policy", true)
	if err != nil {
		log.Errorf("Error getting poddisruptionbudgets from relay: %s", err)
		return nil
	}

	var pdbList policyv1.PodDisruptionBudgetList
	err = json.Unmarshal([]byte(pdbData), &pdbList.Items)
	if err != nil {
		log.Errorf("Error unmarshalling poddisruptionbudgets: %s", err)
		return nil
	}

	var pdbs []*clustercache.PodDisruptionBudget
	for _, pdb := range pdbList.Items {
		p := clustercache.TransformPodDisruptionBudget(&pdb)
		pdbs = append(pdbs, p)
	}

	return pdbs
}

// GetAllReplicationControllers returns all cached replication controllers.
func (pcc *NudgebeeClusterCache) GetAllReplicationControllers() []*clustercache.ReplicationController {
	rcData, err := pcc.executeK8sApi("replicationcontrollers", "v1", "", true)
	if err != nil {
		log.Errorf("Error getting replicationcontrollers from relay: %s", err)
		return nil
	}

	var rcList v1.ReplicationControllerList
	err = json.Unmarshal([]byte(rcData), &rcList.Items)
	if err != nil {
		log.Errorf("Error unmarshalling replicationcontrollers: %s", err)
		return nil
	}

	var rcs []*clustercache.ReplicationController
	for _, rc := range rcList.Items {
		r := clustercache.TransformReplicationController(&rc)
		rcs = append(rcs, r)
	}

	return rcs
}

// i32 reads a JSON number (unmarshalled as float64) from a map as int32;
// JSON numbers cannot be type-asserted to int32 directly.
func i32(m map[string]any, k string) (int32, bool) {
	if v, ok := m[k].(float64); ok {
		return int32(v), true
	}
	return 0, false
}
