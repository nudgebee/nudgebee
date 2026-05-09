package aws

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestEligibility_DerivedFromRuleSet asserts that the dynamic eligibility
// derivation correctly mirrors what the configured rules consume. The
// allowlist is derived from ruleSet.Rules at startup (see newEligibility),
// so this test guards against silent-drop drift if a new rule is added but
// the static template-extraction patterns don't recognize its filter shape.
//
// Catches the failure mode flagged by reviewers on PR #29805: a hardcoded
// table that goes out of sync with aws_runbook.yaml.
func TestEligibility_DerivedFromRuleSet(t *testing.T) {
	rules, err := GetEventRules("")
	require.NoError(t, err, "load embedded aws_runbook.yaml")

	proc := NewTemplatedEventBridgeProcessor(rules, &dummyAwsProvider{})
	require.NotNil(t, proc.eligibility, "eligibility must be derived")
	require.NotEmpty(t, proc.eligibility.rules, "at least one rule must produce a pattern")

	// Each entry asserts that an event of this shape SHOULD be accepted by
	// Eligible() because at least one rule in aws_runbook.yaml consumes it.
	// Failures here mean the dynamic derivation missed a rule's filter — fix
	// the regex/extraction in extractRuleNarrows rather than the test data.
	mustAccept := []struct {
		name       string
		source     string
		detailType string
		// detail-payload fields if narrowing is involved
		eventName  string
		lastStatus string
	}{
		// EC2 lifecycle and tag-sync rules
		{"EC2 state-change", "aws.ec2", "EC2 Instance State-change Notification", "", ""},
		{"EC2 CreateTags via CloudTrail", "aws.ec2", "AWS API Call via CloudTrail", "CreateTags", ""},
		{"EC2 DeleteTags via CloudTrail", "aws.ec2", "AWS API Call via CloudTrail", "DeleteTags", ""},

		// RDS lifecycle and tag-sync rules
		{"RDS DB Instance Event", "aws.rds", "RDS DB Instance Event", "", ""},
		{"RDS AddTagsToResource", "aws.rds", "AWS API Call via CloudTrail", "AddTagsToResource", ""},
		{"RDS RemoveTagsFromResource", "aws.rds", "AWS API Call via CloudTrail", "RemoveTagsFromResource", ""},
		{"RDS CreateDBInstance", "aws.rds", "AWS API Call via CloudTrail", "CreateDBInstance", ""},
		{"RDS DeleteDBCluster", "aws.rds", "AWS API Call via CloudTrail", "DeleteDBCluster", ""},

		// ECS - the All_Events rule has NO lastStatus filter, so events with
		// lastStatus=RUNNING/PROVISIONING/DEPROVISIONING must still be accepted.
		// This is the specific silent-drop case the reviewer flagged.
		{"ECS Task STOPPED", "aws.ecs", "ECS Task State Change", "", "STOPPED"},
		{"ECS Task PENDING", "aws.ecs", "ECS Task State Change", "", "PENDING"},
		{"ECS Task RUNNING (matched by *_All_Events_*)", "aws.ecs", "ECS Task State Change", "", "RUNNING"},
		{"ECS Task PROVISIONING (matched by *_All_Events_*)", "aws.ecs", "ECS Task State Change", "", "PROVISIONING"},
		{"ECS Service Action", "aws.ecs", "ECS Service Action", "", ""},
		{"ECS Deployment State Change", "aws.ecs", "ECS Deployment State Change", "", ""},
		{"ECS CreateCluster", "aws.ecs", "AWS API Call via CloudTrail", "CreateCluster", ""},
		{"ECS DeleteService", "aws.ecs", "AWS API Call via CloudTrail", "DeleteService", ""},

		// Lambda lifecycle (prefix narrowing)
		{"Lambda CreateFunction20150331", "aws.lambda", "AWS API Call via CloudTrail", "CreateFunction20150331", ""},
		{"Lambda UpdateFunctionCode20150331", "aws.lambda", "AWS API Call via CloudTrail", "UpdateFunctionCode20150331", ""},

		// CloudWatch alarms (state-value narrowing isn't statically extractable;
		// the rule must remain wildcard so any alarm event passes Eligible).
		{"CloudWatch Alarm ALARM", "aws.cloudwatch", "CloudWatch Alarm State Change", "", ""},
		{"CloudWatch Alarm OK", "aws.cloudwatch", "CloudWatch Alarm State Change", "", ""},

		// ECR — alert_name=aws.ecr exists in the ruleset but isn't currently
		// in the producer pattern. If it ever lands, Eligible must accept.
		{"ECR push (any detail-type)", "aws.ecr", "AWS API Call via CloudTrail", "PutImage", ""},
		{"ECR scan finding", "aws.ecr", "ECR Image Scan", "", ""},
	}

	for _, tc := range mustAccept {
		t.Run("accept/"+tc.name, func(t *testing.T) {
			ev := buildTestEvent(tc.source, tc.detailType, tc.eventName, tc.lastStatus)
			assert.True(t, proc.Eligible(ev),
				"Eligible should ACCEPT %s/%s eventName=%q lastStatus=%q — at least one rule consumes this combination",
				tc.source, tc.detailType, tc.eventName, tc.lastStatus)
		})
	}

	// Events from sources or detail-types that no rule references — these
	// should be dropped at fast-skip. If a future rule starts consuming
	// any of these, this expectation should be moved into mustAccept.
	mustReject := []struct {
		name       string
		source     string
		detailType string
		eventName  string
		lastStatus string
	}{
		// aws.s3 isn't an alert_name on any rule today.
		{"S3 ObjectCreated", "aws.s3", "Object Created", "", ""},
		// aws.sns isn't either.
		{"SNS notification", "aws.sns", "Custom", "", ""},
	}

	for _, tc := range mustReject {
		t.Run("reject/"+tc.name, func(t *testing.T) {
			ev := buildTestEvent(tc.source, tc.detailType, tc.eventName, tc.lastStatus)
			assert.False(t, proc.Eligible(ev),
				"Eligible should REJECT %s/%s — no rule consumes this source",
				tc.source, tc.detailType)
		})
	}

	// SNS-wrapped / unrecognized payload (Source=="") must always pass — we
	// can't tell what's inside without unwrapping, so we let full processing
	// decide.
	t.Run("accept/empty source falls through", func(t *testing.T) {
		ev := EventBridgeEvent{}
		assert.True(t, proc.Eligible(ev))
	})
}

func buildTestEvent(source, detailType, eventName, lastStatus string) EventBridgeEvent {
	detail := map[string]string{}
	if eventName != "" {
		detail["eventName"] = eventName
	}
	if lastStatus != "" {
		detail["lastStatus"] = lastStatus
	}
	var detailJSON json.RawMessage
	if len(detail) > 0 {
		b, _ := json.Marshal(detail)
		detailJSON = b
	}
	return EventBridgeEvent{
		Source:     source,
		DetailType: detailType,
		Detail:     detailJSON,
	}
}
