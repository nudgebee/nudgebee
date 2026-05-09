package k8s_upgrade

import (
	"bytes"
	"compress/gzip"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"nudgebee/services/cloud"
	"nudgebee/services/relay"
	"nudgebee/services/security"
	"regexp"
	"strings"
	"time"
)

func performNodeHealthCheck(accountID string) ([]NodeHealth, error) {
	items, err := getKubernetesResources(accountID, "nodes", nil, true)
	if err != nil {
		return nil, err
	}

	results := make([]NodeHealth, 0, len(items))
	for _, raw := range items {
		obj, ok := raw.(map[string]any)
		if !ok {
			continue
		}

		meta, ok := obj["metadata"].(map[string]any)
		if !ok {
			continue
		}
		status, ok := obj["status"].(map[string]any)
		if !ok {
			continue
		}

		health := NodeHealth{
			Name:     getSafeString(meta, "name"),
			PoolName: getString(meta, "labels", "pool"),
		}

		if nodeInfo, ok := status["nodeInfo"].(map[string]any); ok {
			health.Version = getSafeString(nodeInfo, "kubeletVersion")
		}

		if conditions, ok := status["conditions"].([]any); ok {
			for _, c := range conditions {
				if cond, ok := c.(map[string]any); ok {
					typ := getSafeString(cond, "type")
					if typ == "Ready" || typ == "MemoryPressure" || typ == "DiskPressure" || typ == "PIDPressure" || typ == "NetworkUnavailable" {
						health.Conditions = append(health.Conditions, cond)
					}
				}
			}
		}

		results = append(results, health)
	}
	return results, nil
}

func performWorkloadHealthCheck(accountID string, namespaces []string, allNamespaces bool) ([]WorkloadHealth, error) {
	results := make([]WorkloadHealth, 0)

	// Fetch and process Deployments
	deployments, err := getKubernetesResources(accountID, "deployments", namespaces, allNamespaces)
	if err != nil {
		return nil, fmt.Errorf("failed to get deployments: %w", err)
	}
	for _, raw := range deployments {
		obj, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		meta, ok := obj["metadata"].(map[string]any)
		if !ok {
			continue
		}
		spec, ok := obj["spec"].(map[string]any)
		if !ok {
			continue
		}
		status, ok := obj["status"].(map[string]any)
		if !ok {
			continue
		}

		results = append(results, WorkloadHealth{
			Name:      getSafeString(meta, "name"),
			Namespace: getSafeString(meta, "namespace"),
			Type:      "Deployment",
			Replicas:  getSafeInt(spec, "replicas"),
			Available: getSafeInt(status, "availableReplicas"),
		})
	}

	// Fetch and process StatefulSets
	statefulsets, err := getKubernetesResources(accountID, "statefulsets", namespaces, allNamespaces)
	if err != nil {
		return nil, fmt.Errorf("failed to get statefulsets: %w", err)
	}
	for _, raw := range statefulsets {
		obj, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		meta, ok := obj["metadata"].(map[string]any)
		if !ok {
			continue
		}
		spec, ok := obj["spec"].(map[string]any)
		if !ok {
			continue
		}
		status, ok := obj["status"].(map[string]any)
		if !ok {
			continue
		}

		results = append(results, WorkloadHealth{
			Name:      getSafeString(meta, "name"),
			Namespace: getSafeString(meta, "namespace"),
			Type:      "StatefulSet",
			Replicas:  getSafeInt(spec, "replicas"),
			Available: getSafeInt(status, "readyReplicas"),
		})
	}

	return results, nil
}

func performServiceHealthCheck(accountID string, namespaces []string, allNamespaces bool) ([]ServiceHealth, error) {
	items, err := getKubernetesResources(accountID, "services", namespaces, allNamespaces)
	if err != nil {
		return nil, err
	}

	results := make([]ServiceHealth, 0, len(items))
	for _, raw := range items {
		obj, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		meta, ok := obj["metadata"].(map[string]any)
		if !ok {
			continue
		}
		spec, ok := obj["spec"].(map[string]any)
		if !ok {
			continue
		}

		selector := make(map[string]string)
		if sel, ok := spec["selector"].(map[string]any); ok {
			for k, v := range sel {
				if str, ok := v.(string); ok {
					selector[k] = str
				}
			}
		}

		results = append(results, ServiceHealth{
			Name:      getSafeString(meta, "name"),
			Namespace: getSafeString(meta, "namespace"),
			Selector:  selector,
			Type:      getSafeString(spec, "type"),
			Status:    getSafeString(meta, "status"),
		})
	}
	return results, nil
}

func performPersistentVolumeCheck(accountID string) ([]PersistentVolumeInfo, error) {
	items, err := getKubernetesResources(accountID, "persistentvolumes", nil, true)
	if err != nil {
		return nil, err
	}

	results := make([]PersistentVolumeInfo, 0, len(items))
	for _, raw := range items {
		obj, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		meta, ok := obj["metadata"].(map[string]any)
		if !ok {
			continue
		}
		spec, ok := obj["spec"].(map[string]any)
		if !ok {
			continue
		}
		status, ok := obj["status"].(map[string]any)
		if !ok {
			continue
		}

		claim := ""
		if claimRef, ok := spec["claimRef"].(map[string]any); ok {
			claim = fmt.Sprintf("%s/%s", getSafeString(claimRef, "namespace"), getSafeString(claimRef, "name"))
		}

		results = append(results, PersistentVolumeInfo{
			Name:   getSafeString(meta, "name"),
			Claim:  claim,
			Status: getSafeString(status, "phase"),
		})
	}
	return results, nil
}

func getSafeString(m map[string]any, key string) string {
	if m == nil {
		return ""
	}
	if val, ok := m[key].(string); ok {
		return val
	}
	return ""
}

func getSafeInt(m map[string]any, key string) int {
	if m == nil {
		return 0
	}
	if val, ok := m[key].(float64); ok {
		return int(val)
	}
	return 0
}

func getString(m map[string]any, key string, subkey string) string {
	if m == nil {
		return ""
	}
	if sub, ok := m[key].(map[string]any); ok {
		if val, ok := sub[subkey].(string); ok {
			return val
		}
	}
	return ""
}

func DoPreFlightCheck(ctx *security.RequestContext, accountID string) (HealthCheck, error) {
	nodes, err := performNodeHealthCheck(accountID)
	if err != nil {
		return HealthCheck{}, fmt.Errorf("node health check failed: %w", err)
	}

	workloads, err := performWorkloadHealthCheck(accountID, []string{}, true)
	if err != nil {
		return HealthCheck{}, fmt.Errorf("workload health check failed: %w", err)
	}

	services, err := performServiceHealthCheck(accountID, []string{}, true)
	if err != nil {
		return HealthCheck{}, fmt.Errorf("service health check failed: %w", err)
	}

	pvs, err := performPersistentVolumeCheck(accountID)
	if err != nil {
		return HealthCheck{}, fmt.Errorf("persistent volume check failed: %w", err)
	}

	daemonSets, err := performDaemonSetHealthCheck(accountID, []string{}, true)
	if err != nil {
		ctx.GetLogger().Warn("daemonset health check failed during pre-flight, continuing", "error", err)
	}

	// H3: Include LB and node group checks in pre/post-flight.
	// These require cloud provider attributes and may not be available for all clusters,
	// so failures are non-fatal.
	request := HealthCheckRequest{AccountID: accountID}
	loadBalancers, err := performLoadBalancerHealthCheck(ctx, request)
	if err != nil {
		ctx.GetLogger().Warn("load balancer check failed during pre-flight, continuing", "error", err)
	}

	nodeGroups, err := performNodeGroupConfigurationCheck(ctx, request)
	if err != nil {
		ctx.GetLogger().Warn("node group check failed during pre-flight, continuing", "error", err)
	}

	return HealthCheck{
		Nodes:             nodes,
		Workloads:         workloads,
		Services:          services,
		PersistentVolumes: pvs,
		DaemonSets:        daemonSets,
		LoadBalancers:     loadBalancers,
		NodeGroups:        nodeGroups,
	}, nil
}

func DoPostFlightCheck(ctx *security.RequestContext, accountID string) (HealthCheck, error) {
	summary, err := DoPreFlightCheck(ctx, accountID)
	if err != nil {
		return HealthCheck{}, err
	}

	return summary, nil
}

// getKubernetesResources fetches Kubernetes resources using kubectl_command_executor via relay
func getKubernetesResources(accountID, resourceType string, namespaces []string, allNamespaces bool) ([]any, error) {
	if allNamespaces || len(namespaces) == 0 {
		command := fmt.Sprintf("kubectl get %s -o json", resourceType)
		if allNamespaces {
			command = fmt.Sprintf("kubectl get %s --all-namespaces -o json", resourceType)
		}
		return executeKubectlGetJSON(accountID, command, resourceType)
	}

	// kubectl --namespace only accepts a single namespace, so fetch per-namespace and merge
	nsRegex := regexp.MustCompile(`^[a-z0-9]([a-z0-9\-]*[a-z0-9])?$`)
	var allItems []any
	for _, ns := range namespaces {
		if !nsRegex.MatchString(ns) {
			continue // skip invalid namespace names to prevent command injection
		}
		command := fmt.Sprintf("kubectl get %s --namespace=%s -o json", resourceType, ns)
		items, err := executeKubectlGetJSON(accountID, command, resourceType)
		if err != nil {
			// Namespace might not exist, skip
			continue
		}
		allItems = append(allItems, items...)
	}
	return allItems, nil
}

// executeKubectlGetJSON executes a kubectl get command via relay and parses the JSON output
func executeKubectlGetJSON(accountID, command, resourceType string) ([]any, error) {
	relayRequest := relay.RelayExecuteRequest{
		Body: relay.ActionExecuteBody{
			AccountID:  accountID,
			ActionName: "kubectl_command_executor",
			ActionParams: map[string]any{
				"command": command,
			},
			Origin: "services-server",
		},
		NoSinks: true,
		Cache:   false,
	}

	relayResponse, _, err := relay.ExecuteAndExtractResponse(relayRequest)
	if err != nil {
		return nil, fmt.Errorf("failed to execute kubectl_command_executor relay request: %w", err)
	}

	var relayData struct {
		Stdout string `json:"stdout"`
	}
	dataStr, ok := relayResponse["data"].(string)
	if !ok {
		return nil, fmt.Errorf("unexpected data format: %T", relayResponse["data"])
	}
	if err := json.Unmarshal([]byte(dataStr), &relayData); err != nil {
		return nil, fmt.Errorf("failed to unmarshal relay data: %w", err)
	}

	if relayData.Stdout == "" {
		return nil, nil
	}

	var kubectlResponse struct {
		Items []any `json:"items"`
	}

	if err := json.Unmarshal([]byte(relayData.Stdout), &kubectlResponse); err != nil {
		return nil, fmt.Errorf("failed to parse kubectl JSON output for %s: %w", resourceType, err)
	}

	return kubectlResponse.Items, nil
}

func performLoadBalancerHealthCheck(ctx *security.RequestContext, request HealthCheckRequest) ([]LoadBalancerInfo, error) {
	if request.AccountID == "" {
		return []LoadBalancerInfo{}, fmt.Errorf("account ID is required")
	}

	attributes, err := getCloudAccountAttributes(ctx, request.AccountID)
	if err != nil {
		ctx.GetLogger().Error("Failed to get cloud account attributes", "error", err)
		return []LoadBalancerInfo{}, fmt.Errorf("cluster provider details are not available")
	}

	services, err := getLoadBalancerDetails(request.AccountID)
	if err != nil {
		ctx.GetLogger().Error("Failed to get LoadBalancer services", "error", err)
		return []LoadBalancerInfo{}, fmt.Errorf("failed to get LoadBalancer services: %w", err)
	}

	var finalServices []map[string]interface{}
	if cloudAccountID, exists := attributes["cloud_account_id"]; exists && cloudAccountID != "" {
		enhancedServices, err := addTargetGroupHealthToServices(ctx, cloudAccountID, services)
		if err != nil {
			ctx.GetLogger().Error("Failed to add TargetGroup health information", "error", err)
			finalServices = services
		} else {
			finalServices = enhancedServices
		}
	} else {
		finalServices = services
	}

	// Convert to LoadBalancerInfo structs
	var loadBalancers []LoadBalancerInfo
	for _, service := range finalServices {
		lb := LoadBalancerInfo{
			ServiceName:      getStringValue(service, "service_name"),
			Namespace:        getStringValue(service, "namespace"),
			Type:             getStringValue(service, "type"),
			HostName:         getStringValue(service, "hostname"),
			LoadBalancerName: getStringValue(service, "elb_name"),
			CheckError:       getStringValue(service, "check_error"),
		}

		// Add instances data if available
		if instanceHealth, ok := service["instance_health"]; ok {
			lb.Instances = instanceHealth
		} else if targetGroups, ok := service["target_groups"]; ok {
			lb.Instances = targetGroups
		}

		loadBalancers = append(loadBalancers, lb)
	}

	return loadBalancers, nil
}

func getStringValue(m map[string]interface{}, key string) string {
	if m == nil {
		return ""
	}
	if val, ok := m[key].(string); ok {
		return val
	}
	return ""
}

func getLoadBalancerDetails(accountID string) ([]map[string]interface{}, error) {
	items, err := getKubernetesResources(accountID, "services", nil, true)
	if err != nil {
		return nil, err
	}

	var loadBalancers []map[string]interface{}

	for _, raw := range items {
		obj, ok := raw.(map[string]interface{})
		if !ok {
			continue
		}

		meta, _ := obj["metadata"].(map[string]interface{})
		spec, _ := obj["spec"].(map[string]interface{})
		if spec == nil || getSafeString(spec, "type") != "LoadBalancer" {
			continue
		}

		lb := map[string]interface{}{
			"service_name": getSafeString(meta, "name"),
			"namespace":    getSafeString(meta, "namespace"),
			"type":         "LoadBalancer",
		}

		// Status (hostname/IP)
		if status, ok := obj["status"].(map[string]interface{}); ok {
			if lbStatus, ok := status["loadBalancer"].(map[string]interface{}); ok {
				if ingressArray, ok := lbStatus["ingress"].([]interface{}); ok && len(ingressArray) > 0 {
					if ingress, ok := ingressArray[0].(map[string]interface{}); ok {
						if hostname := getSafeString(ingress, "hostname"); hostname != "" {
							lb["hostname"] = hostname
							lb["elb_name"] = hostname
						}
						if ip := getSafeString(ingress, "ip"); ip != "" {
							lb["ip"] = ip
						}
					}
				}
			}
		}

		// AWS-specific annotations
		if annotations, ok := meta["annotations"].(map[string]interface{}); ok {
			awsAnnotations := make(map[string]string)
			for k, v := range annotations {
				if val, ok := v.(string); ok &&
					(strings.HasPrefix(k, "service.beta.kubernetes.io/aws-load-balancer") ||
						strings.HasPrefix(k, "alb.ingress.kubernetes.io/")) {
					awsAnnotations[k] = val
				}
			}
			if len(awsAnnotations) > 0 {
				lb["aws_annotations"] = awsAnnotations
			}
		}

		loadBalancers = append(loadBalancers, lb)
	}

	return loadBalancers, nil
}

func addTargetGroupHealthToServices(ctx *security.RequestContext, accountID string, services []map[string]interface{}) ([]map[string]interface{}, error) {
	var enhancedServices []map[string]interface{}

	for _, service := range services {
		enhancedService := cloneMap(service)

		hostname, ok := service["hostname"].(string)
		if !ok || hostname == "" {
			enhancedServices = append(enhancedServices, enhancedService)
			continue
		}

		// Determine LB type
		lbType := "classic"
		if annotations, ok := service["aws_annotations"].(map[string]string); ok {
			if t, exists := annotations["service.beta.kubernetes.io/aws-load-balancer-type"]; exists && t != "" {
				lbType = t
			}
		}
		enhancedService["lb_type"] = lbType

		// Extract region
		region := extractRegionFromHostname(hostname)
		if region == "" {
			ctx.GetLogger().Warn("Could not extract region from hostname", "hostname", hostname)
			enhancedService["check_error"] = "Could not extract region from hostname"
			enhancedServices = append(enhancedServices, enhancedService)
			continue
		}
		enhancedService["region"] = region

		if lbType == "classic" {
			instanceHealth, err := getClassicELBInstanceHealth(ctx, accountID, hostname, region)
			if err != nil {
				ctx.GetLogger().Warn("Failed to get Classic ELB instance health", "hostname", hostname, "error", err)
				enhancedService["check_error"] = fmt.Sprintf("Failed to get Classic ELB health: %v", err)
			} else {
				enhancedService["instance_health"] = instanceHealth
			}
		} else {
			elbArn, err := getALBNLBArn(ctx, accountID, hostname, region)
			if err != nil {
				ctx.GetLogger().Warn("Failed to get ALB/NLB ARN", "hostname", hostname, "error", err)
				enhancedService["check_error"] = fmt.Sprintf("Failed to get ELB details: %v", err)
				enhancedServices = append(enhancedServices, enhancedService)
				continue
			}

			if elbArn != "" {
				enhancedService["elb_arn"] = elbArn

				targetGroups, err := getTargetGroupsForELB(ctx, accountID, elbArn, region)
				if err != nil {
					ctx.GetLogger().Warn("Failed to get target groups for ELB", "elb_arn", elbArn, "error", err)
					enhancedService["check_error"] = fmt.Sprintf("Failed to get target groups: %v", err)
				} else {
					for i, tg := range targetGroups {
						tgArn, _ := tg["arn"].(string)
						if tgArn == "" {
							continue
						}
						healthStatus, err := getTargetGroupHealth(ctx, accountID, tgArn, region)
						if err != nil {
							ctx.GetLogger().Warn("Failed to get target group health", "tg_arn", tg["arn"], "error", err)
							targetGroups[i]["check_error"] = fmt.Sprintf("Failed to get health: %v", err)
						} else {
							targetGroups[i]["target_health"] = healthStatus
						}
					}
					enhancedService["target_groups"] = targetGroups
				}
			}
		}

		enhancedServices = append(enhancedServices, enhancedService)
	}

	return enhancedServices, nil
}

func cloneMap(m map[string]interface{}) map[string]interface{} {
	c := make(map[string]interface{}, len(m))
	for k, v := range m {
		c[k] = v
	}
	return c
}

func extractRegionFromHostname(hostname string) string {
	parts := strings.Split(hostname, ".")
	for i := 0; i < len(parts)-1; i++ {
		if parts[i+1] == "elb" {
			return parts[i]
		}
	}
	return ""
}

// extractELBNameFromHostname extracts the Classic ELB name from a hostname.
// Classic ELB hostname format: {name}-{numeric-hash}.{region}.elb.amazonaws.com
// e.g., "my-elb-1234567890.us-east-1.elb.amazonaws.com" -> "my-elb"
func extractELBNameFromHostname(hostname string) string {
	if hostname == "" {
		return ""
	}
	// Get the subdomain part before the first dot
	subdomain := strings.SplitN(hostname, ".", 2)[0]
	// The last hyphen-delimited segment is the numeric hash; remove it
	lastHyphen := strings.LastIndex(subdomain, "-")
	if lastHyphen <= 0 {
		return subdomain
	}
	return subdomain[:lastHyphen]
}

func calcHealthyPercentage(total, healthy int) float64 {
	if total == 0 {
		return 0
	}
	return float64(healthy) / float64(total) * 100
}

func getClassicELBInstanceHealth(ctx *security.RequestContext, accountID, hostname, region string) (map[string]interface{}, error) {
	command := fmt.Sprintf("aws elb describe-instance-health --region %s --load-balancer-name %s --output json",
		region, extractELBNameFromHostname(hostname))

	var response struct {
		InstanceStates []struct {
			InstanceId  string `json:"InstanceId"`
			State       string `json:"State"`
			ReasonCode  string `json:"ReasonCode"`
			Description string `json:"Description"`
		} `json:"InstanceStates"`
	}

	if err := runAWSCommand(ctx, accountID, command, &response); err != nil {
		return nil, err
	}

	instances := make([]map[string]interface{}, 0, len(response.InstanceStates))
	counts := map[string]int{"InService": 0, "OutOfService": 0, "Unknown": 0}

	for _, inst := range response.InstanceStates {
		instances = append(instances, map[string]interface{}{
			"instance_id": inst.InstanceId,
			"state":       inst.State,
			"reason_code": inst.ReasonCode,
			"description": inst.Description,
		})
		if _, ok := counts[inst.State]; ok {
			counts[inst.State]++
		} else {
			counts["Unknown"]++
		}
	}

	return map[string]interface{}{
		"instances":          instances,
		"total_instances":    len(response.InstanceStates),
		"health_counts":      counts,
		"healthy_percentage": calcHealthyPercentage(len(response.InstanceStates), counts["InService"]),
	}, nil
}

func getALBNLBArn(ctx *security.RequestContext, accountID, hostname, region string) (string, error) {
	command := fmt.Sprintf(
		"aws elbv2 describe-load-balancers --region %s --query 'LoadBalancers[?DNSName==`%s`].LoadBalancerArn' --output text",
		region, hostname,
	)
	result, err := executeAWSCommand(ctx, accountID, command)
	if err != nil {
		return "", err
	}
	elbArn := strings.TrimSpace(result)
	if elbArn == "" || elbArn == "None" {
		return "", fmt.Errorf("no ALB/NLB found with hostname: %s", hostname)
	}
	return elbArn, nil
}

func getTargetGroupsForELB(ctx *security.RequestContext, accountID, elbArn, region string) ([]map[string]interface{}, error) {
	command := fmt.Sprintf("aws elbv2 describe-target-groups --region %s --load-balancer-arn %s --output json", region, elbArn)

	var response struct {
		TargetGroups []struct {
			TargetGroupArn  string `json:"TargetGroupArn"`
			TargetGroupName string `json:"TargetGroupName"`
			Protocol        string `json:"Protocol"`
			Port            int    `json:"Port"`
			HealthCheckPath string `json:"HealthCheckPath"`
		} `json:"TargetGroups"`
	}

	if err := runAWSCommand(ctx, accountID, command, &response); err != nil {
		return nil, err
	}

	targetGroups := make([]map[string]interface{}, 0, len(response.TargetGroups))
	for _, tg := range response.TargetGroups {
		targetGroups = append(targetGroups, map[string]interface{}{
			"arn":               tg.TargetGroupArn,
			"name":              tg.TargetGroupName,
			"protocol":          tg.Protocol,
			"port":              tg.Port,
			"health_check_path": tg.HealthCheckPath,
		})
	}

	return targetGroups, nil
}

func getTargetGroupHealth(ctx *security.RequestContext, accountID, targetGroupArn, region string) (map[string]interface{}, error) {
	command := fmt.Sprintf("aws elbv2 describe-target-health --region %s --target-group-arn %s --output json", region, targetGroupArn)

	var response struct {
		TargetHealthDescriptions []struct {
			Target struct {
				Id   string `json:"Id"`
				Port int    `json:"Port"`
			} `json:"Target"`
			TargetHealth struct {
				State       string `json:"State"`
				Reason      string `json:"Reason"`
				Description string `json:"Description"`
			} `json:"TargetHealth"`
		} `json:"TargetHealthDescriptions"`
	}

	if err := runAWSCommand(ctx, accountID, command, &response); err != nil {
		return nil, err
	}

	targets := make([]map[string]interface{}, 0, len(response.TargetHealthDescriptions))
	counts := map[string]int{"healthy": 0, "unhealthy": 0, "initial": 0, "draining": 0, "unavailable": 0}

	for _, tgt := range response.TargetHealthDescriptions {
		targets = append(targets, map[string]interface{}{
			"target_id":   tgt.Target.Id,
			"port":        tgt.Target.Port,
			"state":       tgt.TargetHealth.State,
			"reason":      tgt.TargetHealth.Reason,
			"description": tgt.TargetHealth.Description,
		})
		if _, ok := counts[tgt.TargetHealth.State]; ok {
			counts[tgt.TargetHealth.State]++
		} else {
			counts["unavailable"]++
		}
	}

	return map[string]interface{}{
		"targets":            targets,
		"total_targets":      len(response.TargetHealthDescriptions),
		"health_counts":      counts,
		"healthy_percentage": calcHealthyPercentage(len(response.TargetHealthDescriptions), counts["healthy"]),
	}, nil
}

func runAWSCommand[T any](ctx *security.RequestContext, accountID, command string, out *T) error {
	result, err := executeAWSCommand(ctx, accountID, command)
	if err != nil {
		return fmt.Errorf("AWS CLI failed: %w", err)
	}
	if err := json.Unmarshal([]byte(result), out); err != nil {
		return fmt.Errorf("failed to parse AWS CLI output: %w", err)
	}
	return nil
}

func executeAWSCommand(ctx *security.RequestContext, accountID, command string) (string, error) {
	response, err := cloud.ExecuteCli(ctx, cloud.CloudExecuteCliCommandRequest{
		AccountID: accountID,
		Command:   command,
	})
	if err != nil {
		return "", err
	}

	if data, ok := response["data"].(string); ok {
		return data, nil
	}
	return "", fmt.Errorf("unexpected response format from cloud CLI")
}

type AWSListNodeGroupsResponse struct {
	NodeGroups []string `json:"nodegroups"`
}

type AWSDescribeNodeGroupResponse struct {
	Nodegroup struct {
		NodegroupName string   `json:"nodegroupName"`
		Status        string   `json:"status"`
		InstanceTypes []string `json:"instanceTypes"`
		AmiType       string   `json:"amiType"`
		CapacityType  string   `json:"capacityType"`
		ScalingConfig struct {
			MinSize     int `json:"minSize"`
			MaxSize     int `json:"maxSize"`
			DesiredSize int `json:"desiredSize"`
		} `json:"scalingConfig"`
		DiskSize       int    `json:"diskSize"`
		Version        string `json:"version"`
		ReleaseVersion string `json:"releaseVersion"`
		RemoteAccess   struct {
			Ec2SshKey string `json:"ec2SshKey"`
		} `json:"remoteAccess"`
		Subnets        []string          `json:"subnets"`
		Tags           map[string]string `json:"tags"`
		LaunchTemplate map[string]any    `json:"launchTemplate"`
		Taints         []map[string]any  `json:"taints"`
		Labels         map[string]string `json:"labels"`
	} `json:"nodegroup"`
}

func performNodeGroupConfigurationCheck(ctx *security.RequestContext, request HealthCheckRequest) ([]NodeGroups, error) {
	if request.AccountID == "" {
		return nil, fmt.Errorf("account ID is required")
	}

	attributes, err := getCloudAccountAttributes(ctx, request.AccountID)
	if err != nil {
		ctx.GetLogger().Error("Failed to get cloud account attributes", "error", err)
		return nil, fmt.Errorf("cluster provider details are not available")
	}

	cloudAccountID, okI := attributes["cloud_account_id"]
	clusterName, okN := attributes["k8s_provider_cluster_name"]

	if !okI || !okN || clusterName == "" || cloudAccountID == "" {
		return nil, fmt.Errorf("cluster provider details are not available")
	}

	return getNodeGroupDetails(ctx, request.AccountID, cloudAccountID, clusterName, attributes)
}

func getNodeGroupDetails(ctx *security.RequestContext, accountID, cloudAccountID, clusterName string, attributes map[string]string) ([]NodeGroups, error) {
	accountDetails, err := GetAccountDetails(accountID)
	if err != nil {
		return nil, fmt.Errorf("failed to get account details: %w", err)
	}

	switch accountDetails.K8sProvider {
	case "EKS":
		return getAWSNodeGroupDetails(ctx, accountID, cloudAccountID, clusterName)
	case "GKE":
		return getGCPNodePoolDetails(ctx, accountID, cloudAccountID, clusterName, attributes)
	case "AKS":
		return getAzureNodePoolDetails(ctx, accountID, cloudAccountID, clusterName, attributes)
	default:
		return nil, fmt.Errorf("unsupported cloud provider: %s", accountDetails.K8sProvider)
	}
}

func getAWSNodeGroupDetails(ctx *security.RequestContext, accountID, cloudAccountID, clusterName string) ([]NodeGroups, error) {
	if clusterName == "" {
		return nil, fmt.Errorf("cluster name is required for AWS node group query")
	}

	// list nodegroups
	var listResp AWSListNodeGroupsResponse
	listCmd := fmt.Sprintf("aws eks list-nodegroups --cluster-name %s --output json", clusterName)
	if err := runAWSCommand(ctx, cloudAccountID, listCmd, &listResp); err != nil {
		return nil, fmt.Errorf("failed to list node groups: %w", err)
	}

	// fetch cluster nodes
	nodes, err := getKubernetesResources(accountID, "nodes", nil, true)
	if err != nil {
		return nil, fmt.Errorf("failed to get kubernetes nodes: %w", err)
	}

	var nodeGroups []NodeGroups
	for _, ngName := range listResp.NodeGroups {
		ng, err := describeAWSNodeGroup(ctx, cloudAccountID, clusterName, ngName)
		if err != nil {
			ctx.GetLogger().Warn("Failed to describe node group", "nodegroup", ngName, "error", err)
			nodeGroups = append(nodeGroups, NodeGroups{Name: ngName, CheckError: err.Error()})
			continue
		}

		nodeGroups = append(nodeGroups, mapAWSNodeGroup(ng, getNodesForNodeGroup(nodes, ng.Nodegroup.NodegroupName)))
	}
	return nodeGroups, nil
}

func describeAWSNodeGroup(ctx *security.RequestContext, cloudAccountID, clusterName, ngName string) (*AWSDescribeNodeGroupResponse, error) {
	var resp AWSDescribeNodeGroupResponse
	cmd := fmt.Sprintf("aws eks describe-nodegroup --cluster-name %s --nodegroup-name %s --output json", clusterName, ngName)
	if err := runAWSCommand(ctx, cloudAccountID, cmd, &resp); err != nil {
		return nil, fmt.Errorf("failed to describe node group: %w", err)
	}
	return &resp, nil
}

func mapAWSNodeGroup(resp *AWSDescribeNodeGroupResponse, nodes []NodeInfo) NodeGroups {
	ng := resp.Nodegroup
	instanceType := firstOrDefault(ng.InstanceTypes)
	taintsAndLabels := map[string]any{}
	if len(ng.Taints) > 0 {
		taintsAndLabels["taints"] = ng.Taints
	}
	if len(ng.Labels) > 0 {
		taintsAndLabels["labels"] = ng.Labels
	}

	return NodeGroups{
		Name:              ng.NodegroupName,
		Status:            ng.Status,
		InstanceType:      instanceType,
		AmiType:           ng.AmiType,
		CapacityType:      ng.CapacityType,
		MinSize:           ng.ScalingConfig.MinSize,
		MaxSize:           ng.ScalingConfig.MaxSize,
		DesiredSize:       ng.ScalingConfig.DesiredSize,
		DiskSize:          ng.DiskSize,
		KubernetesVersion: ng.Version,
		ReleaseVersion:    ng.ReleaseVersion,
		RemoteAccess:      ng.RemoteAccess.Ec2SshKey != "",
		Subnets:           ng.Subnets,
		Tags:              ng.Tags,
		LaunchTemplate:    ng.LaunchTemplate,
		TaintsAndLabels:   taintsAndLabels,
		Nodes:             nodes,
	}
}

func firstOrDefault(arr []string) string {
	if len(arr) > 0 {
		return arr[0]
	}
	return ""
}

func getNodesForNodeGroup(kubernetesNodes []any, nodeGroupName string) []NodeInfo {
	var result []NodeInfo
	for _, raw := range kubernetesNodes {
		kubeNode, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		meta, _ := kubeNode["metadata"].(map[string]any)
		status, _ := kubeNode["status"].(map[string]any)
		labels, _ := meta["labels"].(map[string]any)
		if !belongsToNodeGroup(labels, nodeGroupName) {
			continue
		}

		nodeInfo := NodeInfo{
			Name:             getSafeString(meta, "name"),
			InstanceID:       getSafeString(labels, "node.kubernetes.io/instance-id"),
			InstanceType:     getSafeString(labels, "node.kubernetes.io/instance-type"),
			AvailabilityZone: pickFirstNonEmpty(getSafeString(labels, "failure-domain.beta.kubernetes.io/zone"), getSafeString(labels, "topology.kubernetes.io/zone")),
			KubeletVersion:   getSafeString(getMap(status, "nodeInfo"), "kubeletVersion"),
		}
		nodeInfo.Ready, nodeInfo.Status = getNodeReadyStatus(getSlice(status, "conditions"))
		result = append(result, nodeInfo)
	}
	return result
}

func belongsToNodeGroup(labels map[string]any, nodeGroupName string) bool {
	if labels == nil {
		return false
	}
	if ng, ok := labels["eks.amazonaws.com/nodegroup"].(string); ok && ng == nodeGroupName {
		return true
	}
	if ng, ok := labels["alpha.eksctl.io/nodegroup-name"].(string); ok && ng == nodeGroupName {
		return true
	}
	return false
}

func getNodeReadyStatus(conditions []any) (bool, string) {
	for _, c := range conditions {
		cond, ok := c.(map[string]any)
		if !ok {
			continue
		}
		if getSafeString(cond, "type") == "Ready" {
			if getSafeString(cond, "status") == "True" {
				return true, "Ready"
			}
			return false, "NotReady"
		}
	}
	return false, "Unknown"
}

func getMap(m map[string]any, key string) map[string]any {
	if m == nil {
		return nil
	}
	if v, ok := m[key].(map[string]any); ok {
		return v
	}
	return nil
}

func getSlice(m map[string]any, key string) []any {
	if m == nil {
		return nil
	}
	if v, ok := m[key].([]any); ok {
		return v
	}
	return nil
}

func pickFirstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

func getAzureNodePoolDetails(ctx *security.RequestContext, accountID, cloudAccountID, clusterName string, attributes map[string]string) ([]NodeGroups, error) {
	resourceGroup := attributes["k8s_provider_resource_group"]
	if resourceGroup == "" {
		return nil, fmt.Errorf("resource group is required for AKS node pool query")
	}

	// List node pools
	listCmd := fmt.Sprintf("az aks nodepool list --resource-group %s --cluster-name %s --output json", resourceGroup, clusterName)
	result, err := executeAWSCommand(ctx, cloudAccountID, listCmd)
	if err != nil {
		return nil, fmt.Errorf("failed to list AKS node pools: %w", err)
	}

	var pools []map[string]any
	if err := json.Unmarshal([]byte(result), &pools); err != nil {
		return nil, fmt.Errorf("failed to parse AKS node pool list: %w", err)
	}

	// Fetch cluster nodes for matching
	nodes, err := getKubernetesResources(accountID, "nodes", nil, true)
	if err != nil {
		ctx.GetLogger().Warn("Failed to get kubernetes nodes for AKS pool matching", "error", err)
	}

	var nodeGroups []NodeGroups
	for _, pool := range pools {
		name := getSafeString(pool, "name")
		ng := NodeGroups{
			Name:              name,
			Status:            getSafeString(pool, "provisioningState"),
			InstanceType:      getSafeString(pool, "vmSize"),
			KubernetesVersion: getSafeString(pool, "currentOrchestratorVersion"),
		}

		if count, ok := pool["count"].(float64); ok {
			ng.DesiredSize = int(count)
		}
		if minCount, ok := pool["minCount"].(float64); ok {
			ng.MinSize = int(minCount)
		}
		if maxCount, ok := pool["maxCount"].(float64); ok {
			ng.MaxSize = int(maxCount)
		}
		if osDiskSize, ok := pool["osDiskSizeGb"].(float64); ok {
			ng.DiskSize = int(osDiskSize)
		}
		if osType, ok := pool["osType"].(string); ok {
			ng.AmiType = osType // reuse AmiType for OS type
		}

		// Match K8s nodes to this pool via agentpool label
		ng.Nodes = getNodesForAKSPool(nodes, name)

		nodeGroups = append(nodeGroups, ng)
	}
	return nodeGroups, nil
}

func getNodesForAKSPool(kubernetesNodes []any, poolName string) []NodeInfo {
	var result []NodeInfo
	for _, raw := range kubernetesNodes {
		kubeNode, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		meta, _ := kubeNode["metadata"].(map[string]any)
		status, _ := kubeNode["status"].(map[string]any)
		labels, _ := meta["labels"].(map[string]any)

		if labels == nil {
			continue
		}
		if ag, ok := labels["agentpool"].(string); !ok || ag != poolName {
			continue
		}

		nodeInfo := NodeInfo{
			Name:             getSafeString(meta, "name"),
			InstanceType:     getSafeString(labels, "node.kubernetes.io/instance-type"),
			AvailabilityZone: pickFirstNonEmpty(getSafeString(labels, "topology.kubernetes.io/zone"), getSafeString(labels, "failure-domain.beta.kubernetes.io/zone")),
			KubeletVersion:   getSafeString(getMap(status, "nodeInfo"), "kubeletVersion"),
		}
		nodeInfo.Ready, nodeInfo.Status = getNodeReadyStatus(getSlice(status, "conditions"))
		result = append(result, nodeInfo)
	}
	return result
}

func getGCPNodePoolDetails(ctx *security.RequestContext, accountID, cloudAccountID, clusterName string, attributes map[string]string) ([]NodeGroups, error) {
	region := attributes["k8s_provider_region"]
	zone := attributes["k8s_provider_zone"]
	projectID := attributes["k8s_provider_project_id"]

	if projectID == "" {
		return nil, fmt.Errorf("project ID is required for GKE node pool query")
	}

	// GKE clusters can be regional or zonal
	locationFlag := ""
	if region != "" {
		locationFlag = fmt.Sprintf("--region=%s", region)
	} else if zone != "" {
		locationFlag = fmt.Sprintf("--zone=%s", zone)
	} else {
		return nil, fmt.Errorf("region or zone is required for GKE node pool query")
	}

	// List node pools
	listCmd := fmt.Sprintf("gcloud container node-pools list --cluster=%s %s --project=%s --format=json", clusterName, locationFlag, projectID)
	result, err := executeAWSCommand(ctx, cloudAccountID, listCmd)
	if err != nil {
		return nil, fmt.Errorf("failed to list GKE node pools: %w", err)
	}

	var pools []map[string]any
	if err := json.Unmarshal([]byte(result), &pools); err != nil {
		return nil, fmt.Errorf("failed to parse GKE node pool list: %w", err)
	}

	// Fetch cluster nodes for matching
	nodes, err := getKubernetesResources(accountID, "nodes", nil, true)
	if err != nil {
		ctx.GetLogger().Warn("Failed to get kubernetes nodes for GKE pool matching", "error", err)
	}

	var nodeGroups []NodeGroups
	for _, pool := range pools {
		name := getSafeString(pool, "name")
		ng := NodeGroups{
			Name:              name,
			Status:            getSafeString(pool, "status"),
			KubernetesVersion: getSafeString(pool, "version"),
		}

		// Parse config for machine type and disk size
		if config, ok := pool["config"].(map[string]any); ok {
			ng.InstanceType = getSafeString(config, "machineType")
			if diskSize, ok := config["diskSizeGb"].(float64); ok {
				ng.DiskSize = int(diskSize)
			}
			ng.AmiType = getSafeString(config, "imageType")
		}

		// Parse autoscaling
		if autoscaling, ok := pool["autoscaling"].(map[string]any); ok {
			if minCount, ok := autoscaling["minNodeCount"].(float64); ok {
				ng.MinSize = int(minCount)
			}
			if maxCount, ok := autoscaling["maxNodeCount"].(float64); ok {
				ng.MaxSize = int(maxCount)
			}
		}

		// Parse initial node count as desired
		if initialCount, ok := pool["initialNodeCount"].(float64); ok {
			ng.DesiredSize = int(initialCount)
		}

		// Match K8s nodes to this pool via cloud.google.com/gke-nodepool label
		ng.Nodes = getNodesForGKEPool(nodes, name)
		// Update desired size from actual node count if available
		if len(ng.Nodes) > 0 {
			ng.DesiredSize = len(ng.Nodes)
		}

		nodeGroups = append(nodeGroups, ng)
	}
	return nodeGroups, nil
}

func getNodesForGKEPool(kubernetesNodes []any, poolName string) []NodeInfo {
	var result []NodeInfo
	for _, raw := range kubernetesNodes {
		kubeNode, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		meta, _ := kubeNode["metadata"].(map[string]any)
		status, _ := kubeNode["status"].(map[string]any)
		labels, _ := meta["labels"].(map[string]any)

		if labels == nil {
			continue
		}
		if np, ok := labels["cloud.google.com/gke-nodepool"].(string); !ok || np != poolName {
			continue
		}

		nodeInfo := NodeInfo{
			Name:             getSafeString(meta, "name"),
			InstanceType:     getSafeString(labels, "node.kubernetes.io/instance-type"),
			AvailabilityZone: pickFirstNonEmpty(getSafeString(labels, "topology.kubernetes.io/zone"), getSafeString(labels, "failure-domain.beta.kubernetes.io/zone")),
			KubeletVersion:   getSafeString(getMap(status, "nodeInfo"), "kubeletVersion"),
		}
		nodeInfo.Ready, nodeInfo.Status = getNodeReadyStatus(getSlice(status, "conditions"))
		result = append(result, nodeInfo)
	}
	return result
}

// ===================================================================
// Phase 1: Server-side checks (moved from agent to reduce agent load)
// ===================================================================

const apiDeprecationRegistryURL = "https://raw.githubusercontent.com/nudgebee/nudgebee-compatibility-registry/master/api_deprecations.json"

// performAPIDeprecationCheck fetches API deprecation data from the registry and
// cross-references with APIs available in the cluster to identify in-use deprecated/deleted APIs.
// This replaces the agent-side kubepug Job approach (which created up to 10 K8s Jobs per scan).
func performAPIDeprecationCheck(accountID, currentVersion, targetVersion string) (*APIDeprecationResult, error) {
	// 1. Fetch deprecation registry from GitHub
	registry, err := fetchAPIDeprecationRegistry()
	if err != nil {
		return nil, fmt.Errorf("failed to fetch API deprecation registry: %w", err)
	}

	// 2. Fetch currently available api-resources from the cluster via relay
	clusterAPIs, err := getClusterAPIResources(accountID)
	if err != nil {
		return nil, fmt.Errorf("failed to get cluster API resources: %w", err)
	}

	// 3. Cross-reference: find which deprecated/deleted APIs for the target version are in use
	result := &APIDeprecationResult{
		TargetVersion:    targetVersion,
		ResourcesScanned: len(clusterAPIs),
	}

	// Collect deprecated/deleted APIs for all versions between current and target (inclusive)
	versionsToCheck := getVersionRange(currentVersion, targetVersion)
	for _, ver := range versionsToCheck {
		versionData, ok := registry[ver]
		if !ok {
			continue
		}
		for _, api := range versionData.Deleted {
			apiKey := fmt.Sprintf("%s/%s/%s", api.Group, api.Version, api.Kind)
			finding := APIDeprecationFinding{
				APIDeprecation: api,
				InUse:          clusterAPIs[apiKey],
			}
			result.DeletedAPIs = append(result.DeletedAPIs, finding)
		}
		for _, api := range versionData.Deprecated {
			apiKey := fmt.Sprintf("%s/%s/%s", api.Group, api.Version, api.Kind)
			finding := APIDeprecationFinding{
				APIDeprecation: api,
				InUse:          clusterAPIs[apiKey],
			}
			result.DeprecatedAPIs = append(result.DeprecatedAPIs, finding)
		}
	}

	result.TotalDeleted = len(result.DeletedAPIs)
	result.TotalDeprecated = len(result.DeprecatedAPIs)
	return result, nil
}

// fetchAPIDeprecationRegistry fetches the API deprecation database from the compatibility registry
func fetchAPIDeprecationRegistry() (map[string]struct {
	Deleted    []APIDeprecation `json:"deleted"`
	Deprecated []APIDeprecation `json:"deprecated"`
}, error) {
	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Get(apiDeprecationRegistryURL)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch API deprecation registry: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("API deprecation registry returned status %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read API deprecation registry response: %w", err)
	}

	var registry map[string]struct {
		Deleted    []APIDeprecation `json:"deleted"`
		Deprecated []APIDeprecation `json:"deprecated"`
	}
	if err := json.Unmarshal(body, &registry); err != nil {
		return nil, fmt.Errorf("failed to parse API deprecation registry: %w", err)
	}

	return registry, nil
}

// getClusterAPIResources fetches the list of API resources available in the cluster
// and returns a set of "group/version/kind" keys for quick lookup.
func getClusterAPIResources(accountID string) (map[string]bool, error) {
	command := "kubectl api-resources -o wide --no-headers"

	relayRequest := relay.RelayExecuteRequest{
		Body: relay.ActionExecuteBody{
			AccountID:  accountID,
			ActionName: "kubectl_command_executor",
			ActionParams: map[string]any{
				"command": command,
			},
			Origin: "services-server",
		},
		NoSinks: true,
		Cache:   false,
	}

	relayResponse, _, err := relay.ExecuteAndExtractResponse(relayRequest)
	if err != nil {
		return nil, fmt.Errorf("failed to execute api-resources relay request: %w", err)
	}

	var relayData struct {
		Stdout string `json:"stdout"`
	}
	dataStr, ok := relayResponse["data"].(string)
	if !ok {
		return nil, fmt.Errorf("unexpected data format: %T", relayResponse["data"])
	}
	if err := json.Unmarshal([]byte(dataStr), &relayData); err != nil {
		return nil, fmt.Errorf("failed to unmarshal relay data: %w", err)
	}

	// Parse api-resources output: columns are NAME [SHORTNAMES] APIVERSION NAMESPACED KIND [VERBS...]
	// SHORTNAMES may be empty, causing column count to vary. Use NAMESPACED ("true"/"false")
	// as an anchor to determine the layout.
	apiSet := make(map[string]bool)
	for _, line := range strings.Split(relayData.Stdout, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 4 {
			continue
		}

		// Find NAMESPACED column (always "true" or "false")
		namespacedIdx := -1
		for i, f := range fields {
			if f == "true" || f == "false" {
				namespacedIdx = i
				break
			}
		}
		if namespacedIdx < 1 || namespacedIdx+1 >= len(fields) {
			continue
		}

		apiVersion := fields[namespacedIdx-1]
		kind := fields[namespacedIdx+1]
		group := ""
		version := apiVersion
		if parts := strings.SplitN(apiVersion, "/", 2); len(parts) == 2 {
			group = parts[0]
			version = parts[1]
		}
		apiSet[fmt.Sprintf("%s/%s/%s", group, version, kind)] = true
	}

	return apiSet, nil
}

// getVersionRange returns minor versions between current and target (inclusive of target)
// e.g., getVersionRange("1.28", "1.30") returns ["1.29", "1.30"]
func getVersionRange(current, target string) []string {
	var currentMajor, currentMinor, targetMajor, targetMinor int
	_, _ = fmt.Sscanf(current, "%d.%d", &currentMajor, &currentMinor)
	_, _ = fmt.Sscanf(target, "%d.%d", &targetMajor, &targetMinor)

	if currentMajor != targetMajor {
		// Different major versions — return just the target
		return []string{fmt.Sprintf("%d.%d", targetMajor, targetMinor)}
	}

	var versions []string
	for minor := currentMinor + 1; minor <= targetMinor; minor++ {
		versions = append(versions, fmt.Sprintf("%d.%d", targetMajor, minor))
	}
	return versions
}

// performHelmCompatibilityCheck fetches all Helm release secrets from the cluster via relay,
// decodes them, extracts kubeVersion constraints, and checks compatibility with the target version.
// This replaces the agent-side approach that listed ALL namespaces then ALL secrets per namespace.
func performHelmCompatibilityCheck(accountID, targetVersion string) (*HelmCompatibilityResult, error) {
	// 1. Single kubectl call to get all Helm secrets across all namespaces
	items, err := getHelmReleaseSecrets(accountID)
	if err != nil {
		return nil, fmt.Errorf("failed to get Helm release secrets: %w", err)
	}

	result := &HelmCompatibilityResult{
		TargetVersion: targetVersion,
	}

	// Track latest revision per release to avoid duplicates
	latestRevisions := make(map[string]int)
	releaseSecrets := make(map[string]map[string]any)

	for _, raw := range items {
		obj, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		meta, ok := obj["metadata"].(map[string]any)
		if !ok {
			continue
		}
		labels, _ := meta["labels"].(map[string]any)
		releaseName := getSafeString(labels, "name")
		namespace := getSafeString(meta, "namespace")
		key := namespace + "/" + releaseName

		// Parse revision from secret name (e.g., "sh.helm.release.v1.myrelease.v3")
		secretName := getSafeString(meta, "name")
		revision := parseHelmRevision(secretName)

		if revision > latestRevisions[key] {
			latestRevisions[key] = revision
			releaseSecrets[key] = obj
		}
	}

	// 2. Decode and check each unique release
	for key, obj := range releaseSecrets {
		parts := strings.SplitN(key, "/", 2)
		namespace := parts[0]
		releaseName := parts[1]

		data, _ := obj["data"].(map[string]any)
		releaseData := getSafeString(data, "release")
		if releaseData == "" {
			continue
		}

		// Decode: base64 → gzip → JSON
		chartMeta, err := decodeHelmRelease(releaseData)
		if err != nil {
			// Can't decode, mark as unknown
			result.Releases = append(result.Releases, HelmReleaseCompatibility{
				HelmRelease: HelmRelease{
					Name:      releaseName,
					Namespace: namespace,
					Status:    "unknown",
				},
				Compatible: "unknown",
				Reason:     fmt.Sprintf("failed to decode release: %v", err),
			})
			result.Unknown++
			continue
		}

		release := HelmRelease{
			Name:         releaseName,
			Namespace:    namespace,
			ChartName:    chartMeta.ChartName,
			ChartVersion: chartMeta.ChartVersion,
			AppVersion:   chartMeta.AppVersion,
			KubeVersion:  chartMeta.KubeVersion,
			Status:       chartMeta.Status,
		}

		// 3. Check kubeVersion constraint against target version
		compat := checkKubeVersionConstraint(chartMeta.KubeVersion, targetVersion)
		result.Releases = append(result.Releases, HelmReleaseCompatibility{
			HelmRelease: release,
			Compatible:  compat.Compatible,
			Reason:      compat.Reason,
		})

		switch compat.Compatible {
		case "yes":
			result.Compatible++
		case "no":
			result.Incompatible++
		default:
			result.Unknown++
		}
	}

	result.TotalReleases = len(result.Releases)
	return result, nil
}

// getHelmReleaseSecrets fetches all Helm release secrets across all namespaces
func getHelmReleaseSecrets(accountID string) ([]any, error) {
	command := "kubectl get secrets -l owner=helm --all-namespaces -o json"

	relayRequest := relay.RelayExecuteRequest{
		Body: relay.ActionExecuteBody{
			AccountID:  accountID,
			ActionName: "kubectl_command_executor",
			ActionParams: map[string]any{
				"command": command,
			},
			Origin: "services-server",
		},
		NoSinks: true,
		Cache:   false,
	}

	relayResponse, _, err := relay.ExecuteAndExtractResponse(relayRequest)
	if err != nil {
		return nil, fmt.Errorf("failed to execute helm secrets relay request: %w", err)
	}

	var relayData struct {
		Stdout string `json:"stdout"`
	}
	dataStr, ok := relayResponse["data"].(string)
	if !ok {
		return nil, fmt.Errorf("unexpected data format: %T", relayResponse["data"])
	}
	if err := json.Unmarshal([]byte(dataStr), &relayData); err != nil {
		return nil, fmt.Errorf("failed to unmarshal relay data: %w", err)
	}

	if relayData.Stdout == "" {
		return nil, nil
	}

	var kubectlResponse struct {
		Items []any `json:"items"`
	}
	if err := json.Unmarshal([]byte(relayData.Stdout), &kubectlResponse); err != nil {
		return nil, fmt.Errorf("failed to parse kubectl JSON output: %w", err)
	}

	return kubectlResponse.Items, nil
}

// parseHelmRevision extracts the revision number from a Helm secret name
// e.g., "sh.helm.release.v1.myrelease.v3" → 3
func parseHelmRevision(secretName string) int {
	parts := strings.Split(secretName, ".")
	if len(parts) < 2 {
		return 0
	}
	lastPart := parts[len(parts)-1]
	if !strings.HasPrefix(lastPart, "v") {
		return 0
	}
	var rev int
	_, _ = fmt.Sscanf(lastPart[1:], "%d", &rev)
	return rev
}

type helmChartMeta struct {
	ChartName    string
	ChartVersion string
	AppVersion   string
	KubeVersion  string
	Status       string
}

// decodeHelmRelease decodes a Helm release from a Kubernetes secret's data field.
// The encoding chain is: K8s base64 → Helm base64 → gzip → JSON
// When kubectl returns secrets as JSON, .data values are base64-encoded by Kubernetes.
// Inside that, Helm stores releases as base64(gzip(json)).
func decodeHelmRelease(encoded string) (*helmChartMeta, error) {
	// Step 1: base64 decode (Kubernetes secret .data encoding)
	decoded, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return nil, fmt.Errorf("k8s base64 decode failed: %w", err)
	}

	// Step 2: base64 decode (Helm's encoding layer)
	helmDecoded, err := base64.StdEncoding.DecodeString(string(decoded))
	if err != nil {
		return nil, fmt.Errorf("helm base64 decode failed: %w", err)
	}

	// Step 3: gzip decompress
	gzReader, err := gzip.NewReader(bytes.NewReader(helmDecoded))
	if err != nil {
		return nil, fmt.Errorf("gzip reader creation failed: %w", err)
	}
	defer func() { _ = gzReader.Close() }()

	decompressed, err := io.ReadAll(gzReader)
	if err != nil {
		return nil, fmt.Errorf("gzip decompression failed: %w", err)
	}

	// Step 3: Parse JSON to extract chart metadata
	var release struct {
		Chart struct {
			Metadata struct {
				Name        string `json:"name"`
				Version     string `json:"version"`
				AppVersion  string `json:"appVersion"`
				KubeVersion string `json:"kubeVersion"`
			} `json:"metadata"`
		} `json:"chart"`
		Info struct {
			Status string `json:"status"`
		} `json:"info"`
	}

	if err := json.Unmarshal(decompressed, &release); err != nil {
		return nil, fmt.Errorf("JSON unmarshal failed: %w", err)
	}

	return &helmChartMeta{
		ChartName:    release.Chart.Metadata.Name,
		ChartVersion: release.Chart.Metadata.Version,
		AppVersion:   release.Chart.Metadata.AppVersion,
		KubeVersion:  release.Chart.Metadata.KubeVersion,
		Status:       release.Info.Status,
	}, nil
}

type compatResult struct {
	Compatible string
	Reason     string
}

// checkKubeVersionConstraint checks if the target K8s version satisfies a Helm chart's kubeVersion constraint.
// Uses simple parsing instead of the semver library to avoid adding a dependency.
func checkKubeVersionConstraint(kubeVersionConstraint, targetVersion string) compatResult {
	if kubeVersionConstraint == "" {
		return compatResult{Compatible: "unknown", Reason: "no kubeVersion constraint specified in chart"}
	}

	// Normalize targetVersion to semver format
	target := normalizeVersion(targetVersion)

	// Parse the constraint — handle common patterns:
	// ">= 1.19.0", ">= 1.19.0-0", ">=1.16.0-0 <1.28.0-0", etc.
	constraint := strings.TrimSpace(kubeVersionConstraint)

	// Split on space to handle compound constraints like ">=1.16.0-0 <1.28.0-0"
	parts := splitConstraints(constraint)

	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}

		ok, err := evaluateConstraint(part, target)
		if err != nil {
			return compatResult{Compatible: "unknown", Reason: fmt.Sprintf("could not parse constraint '%s': %v", part, err)}
		}
		if !ok {
			return compatResult{
				Compatible: "no",
				Reason:     fmt.Sprintf("target version %s does not satisfy constraint '%s'", targetVersion, kubeVersionConstraint),
			}
		}
	}

	return compatResult{Compatible: "yes", Reason: fmt.Sprintf("target version %s satisfies constraint '%s'", targetVersion, kubeVersionConstraint)}
}

// normalizeVersion converts "1.30" to [1, 30, 0]
func normalizeVersion(v string) [3]int {
	v = strings.TrimPrefix(v, "v")
	// Strip prerelease suffix
	if idx := strings.Index(v, "-"); idx >= 0 {
		v = v[:idx]
	}
	var parts [3]int
	_, _ = fmt.Sscanf(v, "%d.%d.%d", &parts[0], &parts[1], &parts[2])
	return parts
}

// splitConstraints splits compound constraints on spaces, respecting operators
func splitConstraints(constraint string) []string {
	// Remove surrounding whitespace
	constraint = strings.TrimSpace(constraint)

	// Use regex to split on whitespace between constraints
	re := regexp.MustCompile(`\s+`)
	tokens := re.Split(constraint, -1)

	// Recombine: an operator token followed by a version token should be one part
	var parts []string
	for i := 0; i < len(tokens); i++ {
		t := tokens[i]
		if t == "" {
			continue
		}
		// If this token is just an operator, combine with next
		if (t == ">=" || t == "<=" || t == ">" || t == "<" || t == "=" || t == "!=") && i+1 < len(tokens) {
			parts = append(parts, t+tokens[i+1])
			i++
		} else {
			parts = append(parts, t)
		}
	}
	return parts
}

// evaluateConstraint evaluates a single constraint like ">=1.19.0-0" against a target version
func evaluateConstraint(constraint string, target [3]int) (bool, error) {
	constraint = strings.TrimSpace(constraint)

	var op string
	var versionStr string

	switch {
	case strings.HasPrefix(constraint, ">="):
		op = ">="
		versionStr = constraint[2:]
	case strings.HasPrefix(constraint, "<="):
		op = "<="
		versionStr = constraint[2:]
	case strings.HasPrefix(constraint, "!="):
		op = "!="
		versionStr = constraint[2:]
	case strings.HasPrefix(constraint, ">"):
		op = ">"
		versionStr = constraint[1:]
	case strings.HasPrefix(constraint, "<"):
		op = "<"
		versionStr = constraint[1:]
	case strings.HasPrefix(constraint, "="):
		op = "="
		versionStr = constraint[1:]
	case strings.HasPrefix(constraint, "~"):
		// Tilde range: ~1.19.0 means >=1.19.0 <1.20.0
		versionStr = constraint[1:]
		v := normalizeVersion(versionStr)
		if compareVersions(target, v) < 0 {
			return false, nil
		}
		upper := [3]int{v[0], v[1] + 1, 0}
		return compareVersions(target, upper) < 0, nil
	case strings.HasPrefix(constraint, "^"):
		// Caret range: ^1.19.0 means >=1.19.0 <2.0.0
		versionStr = constraint[1:]
		v := normalizeVersion(versionStr)
		if compareVersions(target, v) < 0 {
			return false, nil
		}
		upper := [3]int{v[0] + 1, 0, 0}
		return compareVersions(target, upper) < 0, nil
	default:
		// Treat bare version as exact match
		op = "="
		versionStr = constraint
	}

	v := normalizeVersion(strings.TrimSpace(versionStr))
	cmp := compareVersions(target, v)

	switch op {
	case ">=":
		return cmp >= 0, nil
	case "<=":
		return cmp <= 0, nil
	case ">":
		return cmp > 0, nil
	case "<":
		return cmp < 0, nil
	case "=":
		return cmp == 0, nil
	case "!=":
		return cmp != 0, nil
	default:
		return false, fmt.Errorf("unknown operator: %s", op)
	}
}

// compareVersions compares two 3-part versions, returns -1, 0, or 1
func compareVersions(a, b [3]int) int {
	for i := 0; i < 3; i++ {
		if a[i] < b[i] {
			return -1
		}
		if a[i] > b[i] {
			return 1
		}
	}
	return 0
}

// addOnNamespaces lists the namespaces to scan for add-ons
var addOnNamespaces = []string{"kube-system", "cert-manager", "ingress-nginx", "monitoring"}

// performAddOnVersionCheck scans targeted namespaces for add-on deployments and daemonsets
// via relay kubectl, replacing the agent-side approach that did cluster-wide pod listing.
func performAddOnVersionCheck(accountID string) (*AddOnVersionResult, error) {
	result := &AddOnVersionResult{}

	// Scan deployments and daemonsets in targeted namespaces
	for _, ns := range addOnNamespaces {
		// Fetch deployments
		deployments, err := getKubernetesResources(accountID, "deployments", []string{ns}, false)
		if err != nil {
			// Namespace might not exist, skip
			continue
		}
		for _, raw := range deployments {
			addon := extractAddOnFromResource(raw, ns, "Deployment")
			if addon != nil {
				result.AddOns = append(result.AddOns, *addon)
			}
		}

		// Fetch daemonsets
		daemonsets, err := getKubernetesResources(accountID, "daemonsets", []string{ns}, false)
		if err != nil {
			continue
		}
		for _, raw := range daemonsets {
			addon := extractAddOnFromResource(raw, ns, "DaemonSet")
			if addon != nil {
				result.AddOns = append(result.AddOns, *addon)
			}
		}
	}

	result.TotalAddOns = len(result.AddOns)
	return result, nil
}

// extractAddOnFromResource extracts add-on info from a deployment or daemonset
func extractAddOnFromResource(raw any, namespace, resourceType string) *AddOnInfo {
	obj, ok := raw.(map[string]any)
	if !ok {
		return nil
	}

	meta, ok := obj["metadata"].(map[string]any)
	if !ok {
		return nil
	}
	spec, ok := obj["spec"].(map[string]any)
	if !ok {
		return nil
	}

	name := getSafeString(meta, "name")

	// Get first container image from the template
	image, version := extractImageAndVersion(spec)

	// Check readiness
	status, _ := obj["status"].(map[string]any)
	ready := isResourceReady(status, resourceType)

	return &AddOnInfo{
		Name:      name,
		Namespace: namespace,
		Type:      resourceType,
		Image:     image,
		Version:   version,
		Ready:     ready,
	}
}

// extractImageAndVersion gets the primary container image and extracts its version tag
func extractImageAndVersion(spec map[string]any) (string, string) {
	template, ok := spec["template"].(map[string]any)
	if !ok {
		return "", ""
	}
	templateSpec, ok := template["spec"].(map[string]any)
	if !ok {
		return "", ""
	}
	containers, ok := templateSpec["containers"].([]any)
	if !ok || len(containers) == 0 {
		return "", ""
	}
	container, ok := containers[0].(map[string]any)
	if !ok {
		return "", ""
	}

	image := getSafeString(container, "image")
	version := ""
	if colonIdx := strings.LastIndex(image, ":"); colonIdx >= 0 {
		version = image[colonIdx+1:]
	}

	return image, version
}

// isResourceReady checks if a workload resource is fully ready
func isResourceReady(status map[string]any, resourceType string) bool {
	if status == nil {
		return false
	}
	switch resourceType {
	case "Deployment":
		desired := getSafeInt(status, "replicas")
		available := getSafeInt(status, "availableReplicas")
		return desired > 0 && desired == available
	case "DaemonSet":
		desired := getSafeInt(status, "desiredNumberScheduled")
		ready := getSafeInt(status, "numberReady")
		return desired > 0 && desired == ready
	default:
		return false
	}
}

// performDaemonSetHealthCheck fetches DaemonSet health information (Phase 2: H2)
func performDaemonSetHealthCheck(accountID string, namespaces []string, allNamespaces bool) ([]DaemonSetHealth, error) {
	items, err := getKubernetesResources(accountID, "daemonsets", namespaces, allNamespaces)
	if err != nil {
		return nil, fmt.Errorf("failed to get daemonsets: %w", err)
	}

	results := make([]DaemonSetHealth, 0, len(items))
	for _, raw := range items {
		obj, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		meta, ok := obj["metadata"].(map[string]any)
		if !ok {
			continue
		}
		status, ok := obj["status"].(map[string]any)
		if !ok {
			continue
		}

		results = append(results, DaemonSetHealth{
			Name:             getSafeString(meta, "name"),
			Namespace:        getSafeString(meta, "namespace"),
			DesiredScheduled: getSafeInt(status, "desiredNumberScheduled"),
			CurrentScheduled: getSafeInt(status, "currentNumberScheduled"),
			Ready:            getSafeInt(status, "numberReady"),
			Available:        getSafeInt(status, "numberAvailable"),
		})
	}
	return results, nil
}

// performJobHealthCheck fetches Job health information (Phase 2: H2)
func performJobHealthCheck(accountID string, namespaces []string, allNamespaces bool) ([]JobHealth, error) {
	items, err := getKubernetesResources(accountID, "jobs", namespaces, allNamespaces)
	if err != nil {
		return nil, fmt.Errorf("failed to get jobs: %w", err)
	}

	results := make([]JobHealth, 0, len(items))
	for _, raw := range items {
		obj, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		meta, ok := obj["metadata"].(map[string]any)
		if !ok {
			continue
		}
		spec, ok := obj["spec"].(map[string]any)
		if !ok {
			continue
		}
		status, ok := obj["status"].(map[string]any)
		if !ok {
			continue
		}

		results = append(results, JobHealth{
			Name:        getSafeString(meta, "name"),
			Namespace:   getSafeString(meta, "namespace"),
			Active:      getSafeInt(status, "active"),
			Succeeded:   getSafeInt(status, "succeeded"),
			Failed:      getSafeInt(status, "failed"),
			Completions: getSafeInt(spec, "completions"),
		})
	}
	return results, nil
}

// performCRDCompatibilityCheck scans CRDs in the cluster to identify potential compatibility issues (M2).
func performCRDCompatibilityCheck(accountID string) ([]CRDInfo, error) {
	items, err := getKubernetesResources(accountID, "customresourcedefinitions.apiextensions.k8s.io", nil, false)
	if err != nil {
		return nil, fmt.Errorf("failed to get CRDs: %w", err)
	}

	results := make([]CRDInfo, 0, len(items))
	for _, raw := range items {
		obj, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		meta, _ := obj["metadata"].(map[string]any)
		spec, _ := obj["spec"].(map[string]any)
		if meta == nil || spec == nil {
			continue
		}

		crd := CRDInfo{
			Name:  getSafeString(meta, "name"),
			Group: getSafeString(spec, "group"),
			Scope: getSafeString(spec, "scope"),
		}

		// Parse versions: prefer the storage version, fall back to first served version
		if versions, ok := spec["versions"].([]any); ok {
			var firstServed string
			for _, v := range versions {
				ver, ok := v.(map[string]any)
				if !ok {
					continue
				}
				vName := getSafeString(ver, "name")
				if served, ok := ver["served"].(bool); ok && served {
					crd.Served = true
					if firstServed == "" {
						firstServed = vName
					}
				}
				if stored, ok := ver["storage"].(bool); ok && stored {
					crd.Stored = true
					crd.Version = vName // Storage version takes priority
				}
			}
			if crd.Version == "" {
				crd.Version = firstServed
			}
		}

		results = append(results, crd)
	}
	return results, nil
}

// performIngressCheck scans Ingress resources for health issues (M5).
func performIngressCheck(accountID string, namespaces []string, allNamespaces bool) ([]IngressInfo, error) {
	items, err := getKubernetesResources(accountID, "ingresses.networking.k8s.io", namespaces, allNamespaces)
	if err != nil {
		return nil, fmt.Errorf("failed to get ingresses: %w", err)
	}

	results := make([]IngressInfo, 0, len(items))
	for _, raw := range items {
		obj, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		meta, _ := obj["metadata"].(map[string]any)
		spec, _ := obj["spec"].(map[string]any)
		status, _ := obj["status"].(map[string]any)
		if meta == nil || spec == nil {
			continue
		}

		ing := IngressInfo{
			Name:      getSafeString(meta, "name"),
			Namespace: getSafeString(meta, "namespace"),
			Class:     getSafeString(spec, "ingressClassName"),
		}

		// Extract hosts from rules
		if rules, ok := spec["rules"].([]any); ok {
			for _, r := range rules {
				rule, ok := r.(map[string]any)
				if !ok {
					continue
				}
				if host := getSafeString(rule, "host"); host != "" {
					ing.Hosts = append(ing.Hosts, host)
				}
			}
		}

		// Check TLS
		if tls, ok := spec["tls"].([]any); ok && len(tls) > 0 {
			ing.TLS = true
		}

		// Check status for assigned address
		ing.Status = "Unhealthy"
		if status != nil {
			if lb, ok := status["loadBalancer"].(map[string]any); ok {
				if ingress, ok := lb["ingress"].([]any); ok && len(ingress) > 0 {
					if first, ok := ingress[0].(map[string]any); ok {
						addr := pickFirstNonEmpty(getSafeString(first, "hostname"), getSafeString(first, "ip"))
						if addr != "" {
							ing.Address = addr
							ing.Status = "Healthy"
						}
					}
				}
			}
		}

		results = append(results, ing)
	}
	return results, nil
}

// performNetworkPolicyCheck scans NetworkPolicy resources (M5).
func performNetworkPolicyCheck(accountID string, namespaces []string, allNamespaces bool) ([]NetworkPolicyInfo, error) {
	items, err := getKubernetesResources(accountID, "networkpolicies.networking.k8s.io", namespaces, allNamespaces)
	if err != nil {
		return nil, fmt.Errorf("failed to get network policies: %w", err)
	}

	results := make([]NetworkPolicyInfo, 0, len(items))
	for _, raw := range items {
		obj, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		meta, _ := obj["metadata"].(map[string]any)
		spec, _ := obj["spec"].(map[string]any)
		if meta == nil || spec == nil {
			continue
		}

		np := NetworkPolicyInfo{
			Name:      getSafeString(meta, "name"),
			Namespace: getSafeString(meta, "namespace"),
		}

		// Extract pod selector
		if podSelector, ok := spec["podSelector"].(map[string]any); ok {
			if labels, ok := podSelector["matchLabels"].(map[string]any); ok {
				parts := make([]string, 0, len(labels))
				for k, v := range labels {
					parts = append(parts, fmt.Sprintf("%s=%v", k, v))
				}
				np.PodSelector = strings.Join(parts, ",")
			}
			if np.PodSelector == "" {
				np.PodSelector = "(all pods)"
			}
		}

		// Extract policy types
		if policyTypes, ok := spec["policyTypes"].([]any); ok {
			types := make([]string, 0, len(policyTypes))
			for _, pt := range policyTypes {
				if s, ok := pt.(string); ok {
					types = append(types, s)
				}
			}
			np.PolicyTypes = strings.Join(types, ",")
		}

		results = append(results, np)
	}
	return results, nil
}

// CompareHealthCheckResults compares pre-flight and post-flight health check results
func CompareHealthCheckResults(preCheck, postCheck *HealthCheck) map[string]interface{} {

	nodesComp := compareNodes(preCheck.Nodes, postCheck.Nodes)
	workloadsComp := compareWorkloads(preCheck.Workloads, postCheck.Workloads)
	servicesComp := compareServices(preCheck.Services, postCheck.Services)
	pvComp := comparePersistentVolumes(preCheck.PersistentVolumes, postCheck.PersistentVolumes)

	var degradations []string
	var improvements []string
	totalChanges := 0

	// Helper to append messages and increment change counts
	processAddedRemoved := func(comp map[string]interface{}, addedMsg, removedMsg string) {
		if added, ok := comp["added"].([]string); ok && len(added) > 0 {
			totalChanges += len(added)
			improvements = append(improvements, fmt.Sprintf(addedMsg, len(added)))
		}
		if removed, ok := comp["removed"].([]string); ok && len(removed) > 0 {
			totalChanges += len(removed)
			degradations = append(degradations, fmt.Sprintf(removedMsg, len(removed)))
		}
	}

	if changed, ok := nodesComp["changed"].([]map[string]interface{}); ok {
		totalChanges += len(changed)
		for _, c := range changed {
			oldVer, ok1 := c["old_version"].(string)
			newVer, ok2 := c["new_version"].(string)
			node, ok3 := c["node"].(string)

			if ok1 && ok2 && ok3 {
				improvements = append(improvements,
					fmt.Sprintf("Node upgraded: %s from %s to %s", node, oldVer, newVer))
			}
		}
	}

	processAddedRemoved(nodesComp,
		"%d new node(s) added to cluster",
		"%d node(s) removed from cluster",
	)

	if changed, ok := workloadsComp["changed"].([]map[string]interface{}); ok {
		totalChanges += len(changed)
		for _, c := range changed {
			name, ok1 := c["workload"].(string)
			oldAvail, ok2 := c["old_available"].(int)
			newAvail, ok3 := c["new_available"].(int)
			oldRep, ok4 := c["old_replicas"].(int)
			newRep, ok5 := c["new_replicas"].(int)
			if !ok1 || !ok2 || !ok3 || !ok4 || !ok5 {
				continue
			}

			// Availability
			switch {
			case newAvail < oldAvail:
				if newAvail == 0 {
					degradations = append(degradations,
						fmt.Sprintf("Critical: %s - All replicas unavailable (was %d, now %d)", name, oldAvail, newAvail))
				} else {
					degradations = append(degradations,
						fmt.Sprintf("Workload degradation: %s - Available replicas decreased from %d to %d", name, oldAvail, newAvail))
				}
			case newAvail > oldAvail:
				improvements = append(improvements,
					fmt.Sprintf("Workload improved: %s - Available replicas increased from %d to %d", name, oldAvail, newAvail))
			}

			// Replicas scaling
			switch {
			case newRep < oldRep:
				degradations = append(degradations,
					fmt.Sprintf("Workload scaled down: %s - Replicas decreased from %d to %d", name, oldRep, newRep))
			case newRep > oldRep:
				improvements = append(improvements,
					fmt.Sprintf("Workload scaled up: %s - Replicas increased from %d to %d", name, oldRep, newRep))
			}
		}
	}

	// Workload add/remove
	if added, ok := workloadsComp["added"].([]string); ok {
		totalChanges += len(added)
		for _, w := range added {
			improvements = append(improvements, fmt.Sprintf("New workload deployed: %s", w))
		}
	}
	if removed, ok := workloadsComp["removed"].([]string); ok {
		totalChanges += len(removed)
		for _, w := range removed {
			degradations = append(degradations, fmt.Sprintf("Workload removed: %s", w))
		}
	}

	if changed, ok := servicesComp["changed"].([]map[string]interface{}); ok {
		totalChanges += len(changed)
		for _, c := range changed {
			name, ok1 := c["service"].(string)
			old, ok2 := c["old_status"].(string)
			newState, ok3 := c["new_status"].(string)
			if !ok1 || !ok2 || !ok3 {
				continue
			}

			switch {
			case newState == "unavailable":
				degradations = append(degradations,
					fmt.Sprintf("Service unavailable: %s changed from %s to %s", name, old, newState))

			case newState == "degraded" && old == "healthy":
				degradations = append(degradations,
					fmt.Sprintf("Service degradation: %s changed from healthy to degraded", name))

			case newState == "healthy" && (old == "unavailable" || old == "degraded"):
				improvements = append(improvements,
					fmt.Sprintf("Service recovered: %s changed from %s to healthy", name, old))
			}
		}
	}

	// Service add/remove
	if added, ok := servicesComp["added"].([]string); ok {
		totalChanges += len(added)
		for _, s := range added {
			improvements = append(improvements, fmt.Sprintf("New service created: %s", s))
		}
	}
	if removed, ok := servicesComp["removed"].([]string); ok {
		totalChanges += len(removed)
		for _, s := range removed {
			degradations = append(degradations, fmt.Sprintf("Service removed: %s", s))
		}
	}

	if changed, ok := pvComp["changed"].([]map[string]interface{}); ok {
		totalChanges += len(changed)
		for _, c := range changed {
			name, ok1 := c["pv"].(string)
			old, ok2 := c["old_status"].(string)
			newState, ok3 := c["new_status"].(string)
			if !ok1 || !ok2 || !ok3 {
				continue
			}

			switch {
			case newState == "Released" || newState == "Failed":
				degradations = append(degradations,
					fmt.Sprintf("PV issue: %s changed from %s to %s", name, old, newState))

			case newState == "Bound" && (old == "Released" || old == "Failed"):
				improvements = append(improvements,
					fmt.Sprintf("PV recovered: %s changed from %s to Bound", name, old))
			}
		}
	}

	processAddedRemoved(pvComp,
		"%d new persistent volume(s) created",
		"%d persistent volume(s) removed",
	)

	return map[string]interface{}{
		"nodes_comparison":     nodesComp,
		"workloads_comparison": workloadsComp,
		"services_comparison":  servicesComp,
		"pv_comparison":        pvComp,
		"summary": map[string]interface{}{
			"total_changes": totalChanges,
			"degradations":  degradations,
			"improvements":  improvements,
		},
	}
}

func diffKeys[T any](pre, post map[string]T) (added []string, removed []string) {
	// Added
	for k := range post {
		if _, exists := pre[k]; !exists {
			added = append(added, k)
		}
	}

	// Removed
	for k := range pre {
		if _, exists := post[k]; !exists {
			removed = append(removed, k)
		}
	}

	return
}

func compareNodes(preNodes, postNodes []NodeHealth) map[string]interface{} {
	result := map[string]interface{}{
		"added":   []string{},
		"removed": []string{},
		"changed": []map[string]interface{}{},
	}

	preNodeMap := make(map[string]NodeHealth)
	for _, node := range preNodes {
		preNodeMap[node.Name] = node
	}

	postNodeMap := make(map[string]NodeHealth)
	for _, node := range postNodes {
		postNodeMap[node.Name] = node
	}

	added, removed := diffKeys(preNodeMap, postNodeMap)
	result["added"] = added
	result["removed"] = removed

	var changed []map[string]interface{}
	for name, postNode := range postNodeMap {
		if preNode, exists := preNodeMap[name]; exists {
			if preNode.Version != postNode.Version {
				changed = append(changed, map[string]interface{}{
					"node":        name,
					"old_version": preNode.Version,
					"new_version": postNode.Version,
					"change_type": "version_upgrade",
				})
			}
		}
	}
	result["changed"] = changed
	return result
}

func compareWorkloads(preWorkloads, postWorkloads []WorkloadHealth) map[string]interface{} {
	result := map[string]interface{}{
		"added":   []string{},
		"removed": []string{},
		"changed": []map[string]interface{}{},
	}

	preMap := make(map[string]WorkloadHealth)
	for _, w := range preWorkloads {
		key := fmt.Sprintf("%s/%s/%s", w.Namespace, w.Type, w.Name)
		preMap[key] = w
	}

	postMap := make(map[string]WorkloadHealth)
	for _, w := range postWorkloads {
		key := fmt.Sprintf("%s/%s/%s", w.Namespace, w.Type, w.Name)
		postMap[key] = w
	}

	added, removed := diffKeys(preMap, postMap)
	result["added"] = added
	result["removed"] = removed

	var changed []map[string]interface{}
	for key, postW := range postMap {
		if preW, exists := preMap[key]; exists {
			if preW.Replicas != postW.Replicas || preW.Available != postW.Available {
				changed = append(changed, map[string]interface{}{
					"workload":      key,
					"old_replicas":  preW.Replicas,
					"new_replicas":  postW.Replicas,
					"old_available": preW.Available,
					"new_available": postW.Available,
					"change_type":   "replica_count",
				})
			}
		}
	}

	result["changed"] = changed
	return result
}

func compareServices(preServices, postServices []ServiceHealth) map[string]interface{} {
	result := map[string]interface{}{
		"added":   []string{},
		"removed": []string{},
		"changed": []map[string]interface{}{},
	}

	preMap := make(map[string]ServiceHealth)
	for _, s := range preServices {
		key := fmt.Sprintf("%s/%s", s.Namespace, s.Name)
		preMap[key] = s
	}

	postMap := make(map[string]ServiceHealth)
	for _, s := range postServices {
		key := fmt.Sprintf("%s/%s", s.Namespace, s.Name)
		postMap[key] = s
	}

	added, removed := diffKeys(preMap, postMap)
	result["added"] = added
	result["removed"] = removed

	var changed []map[string]interface{}
	for key, postS := range postMap {
		if preS, exists := preMap[key]; exists {
			if preS.Status != postS.Status || preS.Type != postS.Type {
				changed = append(changed, map[string]interface{}{
					"service":    key,
					"old_status": preS.Status,
					"new_status": postS.Status,
					"old_type":   preS.Type,
					"new_type":   postS.Type,
				})
			}
		}
	}

	result["changed"] = changed
	return result
}

func comparePersistentVolumes(prePVs, postPVs []PersistentVolumeInfo) map[string]interface{} {
	result := map[string]interface{}{
		"added":   []string{},
		"removed": []string{},
		"changed": []map[string]interface{}{},
	}

	preMap := make(map[string]PersistentVolumeInfo)
	for _, pv := range prePVs {
		preMap[pv.Name] = pv
	}

	postMap := make(map[string]PersistentVolumeInfo)
	for _, pv := range postPVs {
		postMap[pv.Name] = pv
	}

	added, removed := diffKeys(preMap, postMap)
	result["added"] = added
	result["removed"] = removed

	var changed []map[string]interface{}
	for name, postPV := range postMap {
		if prePV, exists := preMap[name]; exists {
			if prePV.Status != postPV.Status {
				changed = append(changed, map[string]interface{}{
					"pv":         name,
					"old_status": prePV.Status,
					"new_status": postPV.Status,
				})
			}
		}
	}

	result["changed"] = changed
	return result
}
