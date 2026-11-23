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
// Stored target groups carry a nested TargetHealthDescriptions captured at
// discovery time. Read its membership with TargetGroupMemberIDs: membership is
// what every knowledge-graph caller wants, and for instance-type target groups
// it is stable topology the snapshot can answer.
//
// describe-target-health is still needed for an ip-type target group, whose
// members are pod or task IPs that churn faster than discovery refreshes the row,
// and for any caller that needs live health *state* -- none in this package does.
// The previous wording here said health "must still come from a live
// describe-target-health call", which was read as applying to membership too --
// and is why four call sites kept shelling out once per target group per build
// for instance ids this already returns.
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

	if !awsRegionPattern.MatchString(region) {
		return nil, fmt.Errorf("invalid AWS region %q", region)
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
	awsAccountID, region, loadBalancerARN string,
	topology *CloudTopologyStore,
) (map[string]string, error) {
	if tags, hit := topology.TagsForLB(loadBalancerARN); hit {
		slog.Debug("load balancer tags served from cloud_resourses",
			"lb_arn", loadBalancerARN, "tag_count", len(tags))
		return tags, nil
	}

	if !awsRegionPattern.MatchString(region) {
		return nil, fmt.Errorf("invalid AWS region %q", region)
	}
	command := fmt.Sprintf(
		"aws elbv2 describe-tags --region %s --resource-arns %s --output json",
		region, loadBalancerARN,
	)
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

// TargetGroupMemberIDs returns the target ids registered on a stored target
// group, and whether that membership was captured at all.
//
// Membership is topology: it changes when a target group gains or loses a
// target, not continuously. Health state is the volatile part, and nothing in
// the knowledge graph reads it -- every caller of describe-target-health here
// pulled out TargetHealthDescriptions[].Target.Id and discarded the rest, which
// is exactly what the discovery snapshot already holds.
//
// Presence is a different question from emptiness and the two must not be
// collapsed (the same rule as metaField in cloud_topology_store.go). An ABSENT
// TargetHealthDescriptions key means discovery never captured membership for
// this target group, so the caller must fall back to describe-target-health. An
// EMPTY array means it did capture it and the target group genuinely has no
// targets; falling back there would shell out for an empty answer on every
// build, forever.
//
// A target group that arrived over the CLI rather than from a row carries no
// TargetHealthDescriptions at all -- describe-target-groups does not return it --
// so this misses and the caller's live path runs, with no flag check needed.
//
// Only instance-type target groups are served from the row. An ip-type group's
// members are pod or task IPs that change on every rollout, and the row is only
// refreshed by full discovery: Register/DeregisterTargets deliberately do not
// trigger a resync (see ELB_CloudTrail_Resource_Sync in the collector's
// aws_runbook.yaml). Serving them from the row would point the graph at pods
// that no longer exist, or at whichever pod reused the IP, for up to a day.
func TargetGroupMemberIDs(tg map[string]interface{}) ([]string, bool) {
	if !LBTargetsFromRowsEnabled() {
		return nil, false
	}
	if targetType, _ := tg["TargetType"].(string); targetType != "instance" {
		return nil, false
	}
	raw, present := tg["TargetHealthDescriptions"]
	if !present || raw == nil {
		return nil, false
	}
	descriptions, ok := raw.([]interface{})
	if !ok {
		return nil, false
	}

	ids := make([]string, 0, len(descriptions))
	for _, entry := range descriptions {
		description, ok := entry.(map[string]interface{})
		if !ok {
			continue
		}
		target, ok := description["Target"].(map[string]interface{})
		if !ok {
			continue
		}
		if id, ok := target["Id"].(string); ok && id != "" {
			ids = append(ids, id)
		}
	}
	return ids, true
}
