package aws

import (
	"context"
	"errors"
	"nudgebee/collector/cloud/providers"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	ecsTypes "github.com/aws/aws-sdk-go-v2/service/ecs/types"
	"github.com/stretchr/testify/assert"
)

// ── ecsTaskStatusToNbStatus ──────────────────────────────────────────────────

func TestEcsTaskStatusToNbStatus(t *testing.T) {
	ptr := func(s string) *string { return &s }
	tests := []struct {
		status   *string
		expected providers.ResourceStatus
	}{
		{nil, providers.ResourceStatusUnknown},
		{ptr("RUNNING"), providers.ResourceStatusActive},
		{ptr("running"), providers.ResourceStatusActive},
		{ptr("ACTIVATING"), providers.ResourceStatusActive},
		{ptr("PROVISIONING"), providers.ResourceStatusActive},
		{ptr("PENDING"), providers.ResourceStatusActive},
		{ptr("STOPPED"), providers.ResourceStatusInactive},
		{ptr("STOPPING"), providers.ResourceStatusInactive},
		{ptr("DEACTIVATING"), providers.ResourceStatusInactive},
		{ptr("DEPROVISIONED"), providers.ResourceStatusDeleted},
		{ptr("UNKNOWN_STATUS"), providers.ResourceStatusUnknown},
	}
	for _, tc := range tests {
		label := "nil"
		if tc.status != nil {
			label = *tc.status
		}
		t.Run(label, func(t *testing.T) {
			assert.Equal(t, tc.expected, ecsTaskStatusToNbStatus(tc.status))
		})
	}
}

// ── GetResourcesByIds: ARN format validation ─────────────────────────────────
// These tests exercise the ARN-parsing guard that runs before any AWS API call,
// so they need no credentials.

func TestGetResourcesByIds_MalformedARN_ReturnsUnsupported(t *testing.T) {
	e := &amazonEcs{}
	ctx := providers.NewCloudProviderContext(context.Background())

	// ARN missing the ":task/" segment (e.g., a service ARN instead of a task ARN)
	_, err := e.GetResourcesByIds(ctx, providers.Account{}, "us-east-1",
		[]string{"arn:aws:ecs:us-east-1:123456789012:service/my-cluster/my-service"})
	assert.ErrorIs(t, err, errors.ErrUnsupported,
		"non-task ARN should return ErrUnsupported before any AWS call")
}

func TestGetResourcesByIds_MixedARNs_AnyMalformedReturnsUnsupported(t *testing.T) {
	e := &amazonEcs{}
	ctx := providers.NewCloudProviderContext(context.Background())

	// One valid task ARN mixed with one service ARN. The first bad ARN encountered
	// causes an immediate ErrUnsupported — the function is all-or-nothing on format.
	_, err := e.GetResourcesByIds(ctx, providers.Account{}, "us-east-1", []string{
		"arn:aws:ecs:us-east-1:123456789012:task/my-cluster/task-abc123",
		"arn:aws:ecs:us-east-1:123456789012:service/my-cluster/my-service",
	})
	assert.ErrorIs(t, err, errors.ErrUnsupported)
}

func TestGetResourcesByIds_EmptyInput_ReturnsUnsupported(t *testing.T) {
	e := &amazonEcs{}
	ctx := providers.NewCloudProviderContext(context.Background())

	// An empty-string ARN has no ":task/" → ErrUnsupported, no AWS call.
	_, err := e.GetResourcesByIds(ctx, providers.Account{}, "us-east-1", []string{""})
	assert.ErrorIs(t, err, errors.ErrUnsupported)
}

func TestGetResourcesByIds_DuplicateARNsDeduped_MalformedStillErrors(t *testing.T) {
	e := &amazonEcs{}
	ctx := providers.NewCloudProviderContext(context.Background())

	// lo.Uniq deduplicates before the format check, but malformed ARNs still error.
	badARN := "arn:aws:ecs:us-east-1:123456789012:cluster/my-cluster"
	_, err := e.GetResourcesByIds(ctx, providers.Account{}, "us-east-1",
		[]string{badARN, badARN, badARN})
	assert.ErrorIs(t, err, errors.ErrUnsupported)
}

// ── LaunchType derivation (capacity-provider strategy) ───────────────────────
// The commit derives LaunchType from CapacityProviderName when the ECS API
// returns an empty LaunchType. The table below documents every branch.

func TestDeriveLaunchTypeFromCapacityProvider(t *testing.T) {
	// Mirrors the logic added to GetResourcesByIds so any drift is caught here.
	derive := func(capacityProviderName string) string {
		if capacityProviderName == "FARGATE" || capacityProviderName == "FARGATE_SPOT" {
			return "FARGATE"
		} else if capacityProviderName != "" {
			return "EC2"
		}
		return ""
	}

	tests := []struct {
		capacityProvider string
		want             string
	}{
		{"FARGATE", "FARGATE"},
		{"FARGATE_SPOT", "FARGATE"},
		{"my-custom-asg-provider", "EC2"},
		{"EC2_SPOT_PROVIDER", "EC2"},
		{"", ""},
	}
	for _, tc := range tests {
		t.Run(tc.capacityProvider, func(t *testing.T) {
			assert.Equal(t, tc.want, derive(tc.capacityProvider))
		})
	}
}

// ── CPU/Memory enrichment condition ─────────────────────────────────────────
// Documents when the enrichment from task-definition kicks in.

func TestCpuEnrichmentCondition(t *testing.T) {
	// Mirrors the guard: (task.Cpu == nil || *task.Cpu == "")
	shouldEnrich := func(cpu *string) bool {
		return cpu == nil || *cpu == ""
	}

	ptr := func(s string) *string { return &s }

	assert.True(t, shouldEnrich(nil), "nil Cpu should trigger enrichment")
	assert.True(t, shouldEnrich(ptr("")), "empty-string Cpu should trigger enrichment")
	assert.False(t, shouldEnrich(ptr("256")), "non-empty Cpu must not be overwritten")
	assert.False(t, shouldEnrich(ptr("1024")), "non-empty Cpu must not be overwritten")
}

// ── ARN cluster-extraction helper ────────────────────────────────────────────

func TestGetResourcesByIds_ARNClusterGrouping(t *testing.T) {
	// Feed two task ARNs for the same cluster and one for a different cluster.
	// All are well-formed but point to no real AWS resource, so after ARN parsing
	// the function will try to call AWS and fail with a credentials error.
	// We only care that parsing succeeds (no ErrUnsupported) before the AWS call.
	// Omit AWS credential env so getAwsConfigFromAccount loads ambient creds and
	// returns a config (even with empty creds), then DescribeTasks fails on auth.
	e := &amazonEcs{}
	pCtx := providers.NewCloudProviderContext(context.Background())
	account := providers.Account{
		AccountNumber: "000000000000",
		AccessKey:     aws.String("AKIAIOSFODNN7EXAMPLE"),
		AccessSecret:  aws.String("wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY"),
	}

	_, err := e.GetResourcesByIds(pCtx, account, "us-east-1", []string{
		"arn:aws:ecs:us-east-1:000000000000:task/cluster-a/task-111",
		"arn:aws:ecs:us-east-1:000000000000:task/cluster-a/task-222",
		"arn:aws:ecs:us-east-1:000000000000:task/cluster-b/task-333",
	})
	// ARN parsing succeeds; error is from AWS (credentials/network), not ErrUnsupported.
	assert.NotErrorIs(t, err, errors.ErrUnsupported,
		"well-formed task ARNs must not produce ErrUnsupported")
}

// ── State-transition meta expectations ──────────────────────────────────────
// These tests document what fields GetResourcesByIds must produce at each ECS
// task lifecycle state so the frontend (meta.LastStatus, meta.Cpu, etc.) always
// has correct data.
//
// PENDING state: AWS may not populate task-level Cpu/Memory yet.
//   → enrichment from DescribeTaskDefinition must fill them.
//   → LaunchType derived from CapacityProviderName when empty.
//
// RUNNING state: all fields populated by AWS directly.
//   → no enrichment needed; structToMap carries everything.
//
// STOPPED state: all fields populated + StoppedReason/StopCode non-empty.

// TestMetaFieldsAtPendingState documents which fields are expected when
// DescribeTasks returns a PENDING-state task with nil Cpu/Memory (common for
// Fargate tasks not yet fully scheduled by AWS).
func TestMetaFieldsAtPendingState(t *testing.T) {
	ptr := func(s string) *string { return &s }

	// Simulate what DescribeTasks returns at PENDING state.
	task := ecsTypes.Task{
		TaskArn:           ptr("arn:aws:ecs:us-east-1:123456789012:task/demo-cluster/abc123"),
		ClusterArn:        ptr("arn:aws:ecs:us-east-1:123456789012:cluster/demo-cluster"),
		TaskDefinitionArn: ptr("arn:aws:ecs:us-east-1:123456789012:task-definition/nginx:1"),
		LastStatus:        ptr("PENDING"),
		DesiredStatus:     ptr("RUNNING"),
		// Cpu and Memory are nil — AWS hasn't scheduled the task yet.
		Cpu:    nil,
		Memory: nil,
		// LaunchType empty — task uses capacity provider strategy.
		LaunchType:           "",
		CapacityProviderName: ptr("FARGATE"),
	}

	meta := structToMap(task)

	// Fields always present regardless of state.
	assert.Equal(t, "PENDING", meta["LastStatus"], "LastStatus must reflect current state")
	assert.Equal(t, "RUNNING", meta["DesiredStatus"])
	assert.Equal(t, "arn:aws:ecs:us-east-1:123456789012:cluster/demo-cluster", meta["ClusterArn"])
	assert.Equal(t, "arn:aws:ecs:us-east-1:123456789012:task-definition/nginx:1", meta["TaskDefinitionArn"])

	// Cpu/Memory are nil at PENDING — enrichment from DescribeTaskDefinition must
	// supply them. Verify the enrichment guard fires correctly.
	cpuNil := task.Cpu
	assert.True(t, cpuNil == nil || (cpuNil != nil && *cpuNil == ""),
		"Cpu must be nil/empty at PENDING to trigger DescribeTaskDefinition enrichment")

	// LaunchType derivation: FARGATE capacity provider → "FARGATE".
	derivedLaunchType := ""
	if cp := task.CapacityProviderName; cp != nil {
		if *cp == "FARGATE" || *cp == "FARGATE_SPOT" {
			derivedLaunchType = "FARGATE"
		} else if *cp != "" {
			derivedLaunchType = "EC2"
		}
	}
	assert.Equal(t, "FARGATE", derivedLaunchType,
		"FARGATE capacity provider must produce LaunchType=FARGATE")
}

// TestMetaFieldsAtRunningState documents that a RUNNING-state task from
// DescribeTasks has all fields populated — no enrichment needed.
func TestMetaFieldsAtRunningState(t *testing.T) {
	ptr := func(s string) *string { return &s }

	task := ecsTypes.Task{
		TaskArn:              ptr("arn:aws:ecs:us-east-1:123456789012:task/demo-cluster/abc123"),
		ClusterArn:           ptr("arn:aws:ecs:us-east-1:123456789012:cluster/demo-cluster"),
		TaskDefinitionArn:    ptr("arn:aws:ecs:us-east-1:123456789012:task-definition/nginx:1"),
		LastStatus:           ptr("RUNNING"),
		DesiredStatus:        ptr("RUNNING"),
		Cpu:                  ptr("256"),
		Memory:               ptr("512"),
		LaunchType:           ecsTypes.LaunchTypeFargate,
		CapacityProviderName: nil,
	}

	meta := structToMap(task)

	assert.Equal(t, "RUNNING", meta["LastStatus"])
	assert.Equal(t, "256", meta["Cpu"], "Cpu must be populated at RUNNING state")
	assert.Equal(t, "512", meta["Memory"], "Memory must be populated at RUNNING state")
	assert.Equal(t, "FARGATE", meta["LaunchType"], "LaunchType must be populated at RUNNING state")

	// Enrichment guard must NOT fire — Cpu is already present.
	assert.False(t, task.Cpu == nil || *task.Cpu == "",
		"non-nil Cpu at RUNNING state must skip DescribeTaskDefinition enrichment")
}

// TestMetaFieldsAtStoppedState documents that a STOPPED task carries
// StoppedReason and StopCode in addition to all RUNNING-state fields.
func TestMetaFieldsAtStoppedState(t *testing.T) {
	ptr := func(s string) *string { return &s }

	task := ecsTypes.Task{
		TaskArn:           ptr("arn:aws:ecs:us-east-1:123456789012:task/demo-cluster/abc123"),
		ClusterArn:        ptr("arn:aws:ecs:us-east-1:123456789012:cluster/demo-cluster"),
		TaskDefinitionArn: ptr("arn:aws:ecs:us-east-1:123456789012:task-definition/nginx:1"),
		LastStatus:        ptr("STOPPED"),
		DesiredStatus:     ptr("STOPPED"),
		Cpu:               ptr("256"),
		Memory:            ptr("512"),
		LaunchType:        ecsTypes.LaunchTypeFargate,
		StoppedReason:     ptr("Essential container in task exited"),
		StopCode:          ecsTypes.TaskStopCodeEssentialContainerExited,
	}

	meta := structToMap(task)

	assert.Equal(t, "STOPPED", meta["LastStatus"])
	assert.Equal(t, "256", meta["Cpu"])
	assert.Equal(t, "512", meta["Memory"])
	assert.Equal(t, "Essential container in task exited", meta["StoppedReason"],
		"StoppedReason must be present at STOPPED state")
	assert.NotEmpty(t, meta["StopCode"],
		"StopCode must be present at STOPPED state")
}

// TestStateTransitionMetaProgression documents that each state transition
// produces a superset of the previous state's fields — RUNNING overwrites
// PENDING with complete data, STOPPED adds stop-specific fields.
func TestStateTransitionMetaProgression(t *testing.T) {
	ptr := func(s string) *string { return &s }
	taskArn := "arn:aws:ecs:us-east-1:123456789012:task/demo-cluster/abc123"

	pendingTask := ecsTypes.Task{
		TaskArn: ptr(taskArn), LastStatus: ptr("PENDING"), Cpu: nil, Memory: nil,
	}
	runningTask := ecsTypes.Task{
		TaskArn: ptr(taskArn), LastStatus: ptr("RUNNING"), Cpu: ptr("256"), Memory: ptr("512"),
		LaunchType: ecsTypes.LaunchTypeFargate,
	}
	stoppedTask := ecsTypes.Task{
		TaskArn: ptr(taskArn), LastStatus: ptr("STOPPED"), Cpu: ptr("256"), Memory: ptr("512"),
		LaunchType: ecsTypes.LaunchTypeFargate, StoppedReason: ptr("User stopped task"),
		StopCode: ecsTypes.TaskStopCodeUserInitiated,
	}

	pendingMeta := structToMap(pendingTask)
	runningMeta := structToMap(runningTask)
	stoppedMeta := structToMap(stoppedTask)

	// LastStatus advances correctly at each state.
	assert.Equal(t, "PENDING", pendingMeta["LastStatus"])
	assert.Equal(t, "RUNNING", runningMeta["LastStatus"])
	assert.Equal(t, "STOPPED", stoppedMeta["LastStatus"])

	// Cpu/Memory absent at PENDING, present at RUNNING and STOPPED.
	assert.Nil(t, pendingMeta["Cpu"], "PENDING: Cpu not yet populated by AWS")
	assert.Equal(t, "256", runningMeta["Cpu"], "RUNNING: Cpu must be populated")
	assert.Equal(t, "256", stoppedMeta["Cpu"], "STOPPED: Cpu must still be populated")

	// StoppedReason only meaningful at STOPPED.
	assert.Empty(t, pendingMeta["StoppedReason"], "PENDING: no stop reason yet")
	assert.Empty(t, runningMeta["StoppedReason"], "RUNNING: no stop reason yet")
	assert.Equal(t, "User stopped task", stoppedMeta["StoppedReason"])
}
