package aws

import (
	"testing"

	"nudgebee/collector/cloud/common"
	"nudgebee/collector/cloud/providers"
)

// syncIdentityOf mirrors how account.StoreResources derives the identity columns for
// a resource it pulled from a provider (see the resourceMapKey / resourceDbData block
// in account/etl_resources.go). It is spelled out here rather than imported because
// that logic is inline in the ETL; if the ETL's mapping changes, this must change too
// and the tests below are what will catch the drift.
func syncIdentityOf(accountNumber string, item providers.Resource) (resourceId, externalResourceId string) {
	return item.Id, common.BuildExternalResourceId(
		"AWS", accountNumber, item.Region, item.ServiceName, item.Type, item.Id, "")
}

// The realtime EventBridge path and the bulk sync both write cloud_resourses rows for
// the same AWS resource. Nothing in the schema forces them to agree, and for a long
// time they did not: the realtime path stored its action's lookup key as resourse_id
// and the raw ARN as external_resource_id, so ECS tasks ended up with two rows and
// other services ended up with one row whose external_resource_id flip-flopped. These
// tests pin the agreement.
func TestRealtimeIdentityMatchesSync(t *testing.T) {
	const accountNumber = "864186153326"

	tests := []struct {
		name string
		// resource is what the provider returns from GetResourcesByIds — the same
		// construction GetResources hands to the sync.
		resource providers.Resource
		// lookupKey is what the aws_runbook.yaml rule passes as params.resource_id.
		// For ECS tasks this is a full ARN, because GetResourcesByIds parses the
		// cluster name out of it; for most services it is already the bare id.
		lookupKey string
	}{
		{
			name:      "ecs standalone task (lookup key is an ARN)",
			lookupKey: "arn:aws:ecs:us-east-1:864186153326:task/ecs-task-testing/d3e151b10c8a402884d0726183843012",
			resource: providers.Resource{
				Id:          "d3e151b10c8a402884d0726183843012",
				Name:        "d3e151b10c8a402884d0726183843012",
				ServiceName: ServiceNameECS,
				Type:        getAwsServiceResourceType(ServiceNameECS, "task"),
				Region:      "us-east-1",
				Arn:         "arn:aws:ecs:us-east-1:864186153326:task/ecs-task-testing/d3e151b10c8a402884d0726183843012",
			},
		},
		{
			name:      "ec2 instance (lookup key already bare)",
			lookupKey: "i-0abc123def4567890",
			resource: providers.Resource{
				Id:          "i-0abc123def4567890",
				Name:        "web-1",
				ServiceName: ServiceNameEc2,
				Type:        getAwsServiceResourceType(ServiceNameEc2, "compute-instance"),
				Region:      "us-east-1",
				Arn:         "arn:aws:ec2:us-east-1:864186153326:instance/i-0abc123def4567890",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			wantId, wantErid := syncIdentityOf(accountNumber, tt.resource)

			gotId, gotErid := resolveResourceIdentity(
				tt.resource.Id, tt.lookupKey, tt.resource.Arn,
				accountNumber, tt.resource.Region, tt.resource.ServiceName, tt.resource.Type)

			if gotId != wantId {
				t.Errorf("resourse_id: realtime wrote %q, sync writes %q — the two would not collide on the natural key", gotId, wantId)
			}
			if gotErid != wantErid {
				t.Errorf("external_resource_id: realtime wrote %q, sync writes %q — the two would not collide on (account, external_resource_id)", gotErid, wantErid)
			}
			// The regression that produced 83k duplicate ECS task rows was storing the
			// ARN in these columns. The ARN belongs in `arn`, not in the identity.
			if gotId == tt.resource.Arn || gotErid == tt.resource.Arn {
				t.Errorf("identity must not be the raw ARN: got id=%q erid=%q", gotId, gotErid)
			}
		})
	}
}

// When the live lookup fails there is no provider id to derive from. Deriving anyway
// would run BuildExternalResourceId over a lookup key that may itself be an ARN and
// produce a doubly-prefixed pseudo-ARN, so the pre-existing behaviour is kept.
func TestRealtimeIdentityFallsBackWhenLookupFailed(t *testing.T) {
	const arn = "arn:aws:ecs:us-east-1:864186153326:task/ecs-task-testing/d3e151b10c8a402884d0726183843012"

	gotId, gotErid := resolveResourceIdentity(
		"", arn, arn,
		"864186153326", "us-east-1", ServiceNameECS, "task")

	if gotId != arn {
		t.Errorf("resourse_id = %q, want the lookup key %q", gotId, arn)
	}
	if gotErid != arn {
		t.Errorf("external_resource_id = %q, want the ARN %q", gotErid, arn)
	}
}
