package account

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// The rows below are shaped like real inventory rows that share one resource
// id; the expected pick is the row that is the event's resource.
func TestChooseEventResource(t *testing.T) {
	ec2Key := eventResourceKey{
		externalId:   "arn:aws:ec2:us-east-1:111122223333:compute-instance:i-0abc",
		ids:          []string{"i-0abc"},
		serviceName:  "AmazonEC2",
		resourceType: "compute-instance",
		region:       "us-east-1",
	}
	ec2Rows := []resourceCandidate{
		{ID: "ssm", ResourceId: "i-0abc", ServiceName: "AWSSystemsManager", Type: "managedinstance", Region: "us-east-1",
			ExternalId: "arn:aws:systemsmanager:us-east-1:111122223333:managedinstance:i-0abc"},
		{ID: "optimizer", ResourceId: "i-0abc", ServiceName: "AWSComputeOptimizer", Type: "instance", Region: "us-east-1", Source: "billing"},
		{ID: "inspector", ResourceId: "i-0abc", ServiceName: "AmazonInspectorV2", Type: "instance", Region: "us-east-1", Source: "billing"},
		{ID: "transfer", ResourceId: "i-0abc", ServiceName: "AmazonEC2", Type: "data-transfer", Region: "us-east-1", Source: "billing",
			ExternalId: "arn:aws:ec2:us-east-1:111122223333:data-transfer:i-0abc"},
		{ID: "ec2", ResourceId: "i-0abc", ServiceName: "AmazonEC2", Type: "compute-instance", Region: "us-east-1", Source: "api",
			ExternalId: "arn:aws:ec2:us-east-1:111122223333:compute-instance:i-0abc"},
	}

	tests := []struct {
		name     string
		key      eventResourceKey
		rows     []resourceCandidate
		wantID   string
		wantTie  bool
		wantNone bool
	}{
		{
			name:   "EC2 alarm picks the EC2 row over Systems Manager, Compute Optimizer, Inspector and cost rows",
			key:    ec2Key,
			rows:   ec2Rows,
			wantID: "ec2",
		},
		{
			name:   "EC2 alarm with no exact key still picks the EC2 row of its own type",
			key:    eventResourceKey{ids: []string{"i-0abc"}, serviceName: "AmazonEC2", resourceType: "compute-instance", region: "us-east-1"},
			rows:   ec2Rows,
			wantID: "ec2",
		},
		{
			name: "EventBridge ec2-instance event picks the API EC2 row over the billed one",
			key: eventResourceKey{
				externalId: "arn:aws:ec2:us-east-1:111122223333:ec2-instance:i-0abc",
				ids:        []string{"i-0abc"}, serviceName: "AmazonEC2", resourceType: "ec2-instance", region: "us-east-1",
			},
			rows:   ec2Rows,
			wantID: "ec2",
		},
		{
			name: "Lambda alarm picks its own region's row over rows of other regions",
			key: eventResourceKey{
				externalId: "arn:aws:lambda:us-east-1:111122223333:function:poller",
				ids:        []string{"poller"}, serviceName: "AWSLambda", resourceType: "function", region: "us-east-1",
			},
			rows: []resourceCandidate{
				{ID: "logs", ResourceId: "/aws/lambda/poller", Name: "poller", ServiceName: "AmazonCloudWatch", Type: "log-group", Region: "us-east-1"},
				{ID: "eu", ResourceId: "poller", ServiceName: "AWSLambda", Type: "function", Region: "eu-west-1",
					ExternalId: "arn:aws:lambda:eu-west-1:111122223333:function:poller"},
				{ID: "use1", ResourceId: "poller", ServiceName: "AWSLambda", Type: "function", Region: "us-east-1",
					ExternalId: "arn:aws:lambda:us-east-1:111122223333:function:poller"},
			},
			wantID: "use1",
		},
		{
			name: "Lambda alarm whose region's row is billed still picks it over another region's API row",
			key:  eventResourceKey{ids: []string{"poller"}, serviceName: "AWSLambda", resourceType: "function", region: "us-east-1"},
			rows: []resourceCandidate{
				{ID: "eu", ResourceId: "poller", ServiceName: "AWSLambda", Type: "function", Region: "eu-west-1", Source: "api"},
				{ID: "use1", ResourceId: "poller", ServiceName: "AWSLambda", Type: "function", Region: "us-east-1", Source: "billing"},
			},
			wantID: "use1",
		},
		{
			name: "Load balancer alarm finds the ARN row through its lookup id, not an unrelated row named alike",
			key: eventResourceKey{
				externalId: "arn:aws:elb:us-east-1:111122223333:loadbalancer:app/web/abc123",
				ids: []string{"app/web/abc123",
					"arn:aws:elasticloadbalancing:us-east-1:111122223333:loadbalancer/app/web/abc123"},
				serviceName: "AWSELB", resourceType: "loadbalancer", region: "us-east-1",
			},
			rows: []resourceCandidate{
				{ID: "inspector", ResourceId: "account", Name: "app/web/abc123", ServiceName: "AmazonInspector", Type: "account", Region: "us-east-1"},
				{ID: "alb", ResourceId: "arn:aws:elasticloadbalancing:us-east-1:111122223333:loadbalancer/app/web/abc123", Name: "web",
					ServiceName: "AWSELB", Type: "application_loadbalancer", Region: "us-east-1", Source: "api"},
			},
			wantID: "alb",
		},
		{
			name: "ECS service alarm picks the service, not a cluster named like it",
			key: eventResourceKey{
				ids: []string{"web/web", "web"}, serviceName: "AmazonECS", resourceType: "service", region: "us-east-1",
			},
			rows: []resourceCandidate{
				{ID: "cluster", ResourceId: "web", Name: "web", ServiceName: "AmazonECS", Type: "cluster", Region: "us-east-1"},
				{ID: "service", ResourceId: "web", Name: "web", ServiceName: "AmazonECS", Type: "service", Region: "us-east-1"},
				{ID: "fargate", ResourceId: "web", Name: "web", ServiceName: "AWSFargate", Type: "service", Region: "us-east-1"},
			},
			wantID: "service",
		},
		{
			name: "GCP alert links within its service although type and region are spelled differently",
			key: eventResourceKey{
				externalId: "arn:gcp:cloud sql:us-central:proj:cloudsql_database:proj:db1",
				ids:        []string{"proj:db1"}, serviceName: "Cloud SQL", resourceType: "cloudsql_database", region: "us-central",
			},
			rows: []resourceCandidate{
				{ID: "gemini", ResourceId: "proj:db1", ServiceName: "Gemini API", Type: "gemini-api", Region: "global"},
				{ID: "sql", ResourceId: "proj:db1", ServiceName: "Cloud SQL", Type: "sqladmin.googleapis.com/Instance", Region: "us-central1"},
			},
			wantID: "sql",
		},
		{
			name: "custom-metric alarm with no row of its own service keeps today's match by name",
			key:  eventResourceKey{ids: []string{"checkout"}, serviceName: "CloudWatch", resourceType: "alarm", region: "us-east-1"},
			rows: []resourceCandidate{
				{ID: "svc", ResourceId: "arn:aws:ecs:us-east-1:111122223333:service/prod/checkout", Name: "checkout", ServiceName: "AmazonECS", Type: "service", Region: "us-east-1"},
			},
			wantID: "svc",
		},
		{
			name: "event with a service name the inventory spells differently keeps today's match by id",
			key:  eventResourceKey{ids: []string{"orders"}, serviceName: "AmazonEcs", resourceType: "service", region: "us-east-1"},
			rows: []resourceCandidate{
				{ID: "svc", ResourceId: "orders", ServiceName: "AmazonECS", Type: "service", Region: "us-east-1"},
			},
			wantID: "svc",
		},
		{
			name: "two different rows that fit equally well link nothing",
			key:  eventResourceKey{ids: []string{"orders"}, serviceName: "AmazonECS", resourceType: "service", region: "us-east-1"},
			rows: []resourceCandidate{
				{ID: "a", ResourceId: "orders", ServiceName: "AmazonECS", Type: "service", Region: "us-east-1"},
				{ID: "b", ResourceId: "orders", ServiceName: "AmazonECS", Type: "service", Region: "us-east-1"},
			},
			wantTie: true,
		},
		{
			name:     "no row with the id links nothing",
			key:      eventResourceKey{ids: []string{"i-0zzz"}, serviceName: "AmazonEC2", resourceType: "compute-instance", region: "us-east-1"},
			rows:     ec2Rows,
			wantNone: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			id, ok, tie := chooseEventResource(tc.key, tc.rows)
			assert.Equal(t, tc.wantTie, tie, "tie")
			if tc.wantTie || tc.wantNone {
				assert.False(t, ok)
				assert.Empty(t, id)
				return
			}
			assert.True(t, ok)
			assert.Equal(t, tc.wantID, id)
		})
	}
}

func TestEventResourceKeyFromMapReadsLookupIds(t *testing.T) {
	e := map[string]any{
		"subject_name":       "app/web/abc123",
		"subject_namespace":  "AWSELB",
		"subject_type":       "loadbalancer",
		"subject_node":       "us-east-1",
		"service_key":        "arn:aws:elb:us-east-1:111122223333:loadbalancer:app/web/abc123",
		resourceLookupIdsKey: []string{"arn:aws:elasticloadbalancing:us-east-1:111122223333:loadbalancer/app/web/abc123", "app/web/abc123", ""},
	}
	key := eventResourceKeyFromMap(e)
	assert.Equal(t, []string{"app/web/abc123", "arn:aws:elasticloadbalancing:us-east-1:111122223333:loadbalancer/app/web/abc123"}, key.ids)
	assert.Equal(t, "AWSELB", key.serviceName)
}
