package flow_sources

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"nudgebee/services/cloud"
	"nudgebee/services/security"
	"strings"
)

// The load-balancer target-group and tag lookups below were duplicated verbatim
// across four call sites (two flow sources, two cross-source enrichers), each
// starting its own `aws elbv2` process for a load balancer the collector already
// discovered. They live here once so the store-first behaviour and the CLI
// fallback stay in step across all of them.

// FetchLoadBalancerTargetGroups returns a v2 load balancer's target groups as the
// raw describe-target-groups objects the callers already index by key.
//
// topology may be nil. When it answers, the data comes from
// `application_loadbalancer.meta.TargetGroups` and no CLI process is started.
//
// Caution for callers: stored target groups carry a nested
// TargetHealthDescriptions captured at discovery time. Target *membership* is
// topology and safe to read from here; target *health* flips minute to minute and
// must still come from a live describe-target-health call.
func FetchLoadBalancerTargetGroups(
	reqCtx *security.RequestContext,
	awsAccountID, region, loadBalancerARN string,
	topology *CloudTopologyStore,
) ([]map[string]interface{}, error) {
	if raw, hit := topology.TargetGroupsForLB(loadBalancerARN); hit {
		targetGroups := make([]map[string]interface{}, 0, len(raw))
		for _, entry := range raw {
			var tg map[string]interface{}
			if err := json.Unmarshal(entry, &tg); err != nil {
				continue
			}
			targetGroups = append(targetGroups, tg)
		}
		slog.Debug("load balancer target groups served from cloud_resourses",
			"lb_arn", loadBalancerARN, "target_group_count", len(targetGroups))
		return targetGroups, nil
	}

	command := fmt.Sprintf(
		"aws elbv2 describe-target-groups --region %s --load-balancer-arn %s --output json",
		region, loadBalancerARN,
	)
	resp, err := cloud.ExecuteCli(reqCtx, cloud.CloudExecuteCliCommandRequest{
		AccountID: awsAccountID,
		Command:   command,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to query target groups: %w", err)
	}

	data, ok := resp["data"].(string)
	if !ok || data == "" || !strings.HasPrefix(strings.TrimSpace(data), "{") {
		return nil, fmt.Errorf("unexpected or non-JSON response format from cloud collector")
	}

	var parsed struct {
		TargetGroups []map[string]interface{} `json:"TargetGroups"`
	}
	if err := json.Unmarshal([]byte(data), &parsed); err != nil {
		return nil, fmt.Errorf("failed to parse target groups: %w", err)
	}
	return parsed.TargetGroups, nil
}

// FetchLoadBalancerTags returns a load balancer's tags as a flat key -> value map.
// topology may be nil; on a miss this falls back to `aws elbv2 describe-tags`.
func FetchLoadBalancerTags(
	reqCtx *security.RequestContext,
	awsAccountID, loadBalancerARN string,
	topology *CloudTopologyStore,
) (map[string]string, error) {
	if tags, hit := topology.TagsForLB(loadBalancerARN); hit {
		slog.Debug("load balancer tags served from cloud_resourses",
			"lb_arn", loadBalancerARN, "tag_count", len(tags))
		return tags, nil
	}

	command := fmt.Sprintf("aws elbv2 describe-tags --resource-arns %s --output json", loadBalancerARN)
	resp, err := cloud.ExecuteCli(reqCtx, cloud.CloudExecuteCliCommandRequest{
		AccountID: awsAccountID,
		Command:   command,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to query load balancer tags: %w", err)
	}

	data, ok := resp["data"].(string)
	if !ok {
		return nil, fmt.Errorf("invalid response format")
	}

	var parsed struct {
		TagDescriptions []struct {
			Tags []struct {
				Key   string `json:"Key"`
				Value string `json:"Value"`
			} `json:"Tags"`
		} `json:"TagDescriptions"`
	}
	if err := json.Unmarshal([]byte(data), &parsed); err != nil {
		return nil, fmt.Errorf("failed to parse load balancer tags: %w", err)
	}
	if len(parsed.TagDescriptions) == 0 {
		return map[string]string{}, nil
	}

	tags := make(map[string]string, len(parsed.TagDescriptions[0].Tags))
	for _, tag := range parsed.TagDescriptions[0].Tags {
		tags[tag.Key] = tag.Value
	}
	return tags, nil
}

// K8sServiceFromLBTags reads the kubernetes.io/service-name tag, which the AWS
// Load Balancer Controller writes as "<namespace>/<name>". Returns empty strings
// when the tag is absent or malformed.
func K8sServiceFromLBTags(tags map[string]string) (namespace, name string) {
	value, ok := tags["kubernetes.io/service-name"]
	if !ok {
		return "", ""
	}
	parts := strings.Split(value, "/")
	if len(parts) != 2 {
		return "", ""
	}
	return parts[0], parts[1]
}
