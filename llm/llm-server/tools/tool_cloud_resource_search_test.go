package tools

import (
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestMissRegionHint pins the empty-result message that bounds a live fallback to the
// account's real regions — the fix for the observed fan-out where, on a DB miss, the
// model scanned all ~16 regions and enumerated regions via shell (session 9ab533bd).
func TestMissRegionHint(t *testing.T) {
	t.Run("no known regions returns empty so caller keeps the plain message", func(t *testing.T) {
		assert.Equal(t, "", missRegionHint("payments-reconciler-v7", "Lambda", nil))
		assert.Equal(t, "", missRegionHint("payments-reconciler-v7", "Lambda", []string{}))
	})

	t.Run("service-scoped: lists only the account's regions and forbids other-region / shell scanning", func(t *testing.T) {
		msg := missRegionHint("payments-reconciler-v7", "Lambda", []string{"ap-south-1", "us-east-1"})
		assert.Contains(t, msg, "payments-reconciler-v7")
		assert.Contains(t, msg, "Lambda resources in these regions ONLY: ap-south-1, us-east-1")
		// The bound must be authoritative, not a soft hint: it closes the two escape
		// hatches we saw in the trace — trying other regions, and shell enumeration.
		assert.Contains(t, msg, "do NOT try other regions or enumerate regions via shell")
		assert.Contains(t, msg, "it does not exist")
	})

	t.Run("no service: account-wide phrasing (no dangling service word)", func(t *testing.T) {
		msg := missRegionHint("some-thing", "", []string{"us-east-1"})
		assert.Contains(t, msg, "This account has resources in these regions ONLY: us-east-1")
		assert.NotContains(t, msg, "  ") // no double space from an empty service scope
	})

	t.Run("empty resource name uses a generic match phrase (service/type-only search)", func(t *testing.T) {
		msg := missRegionHint("", "Lambda", []string{"us-east-1"})
		assert.Contains(t, msg, "Found 0 resources matching your query")
		assert.NotContains(t, msg, "matching ''") // no dangling empty-quote target
		assert.Contains(t, msg, "Lambda resources in these regions ONLY: us-east-1")
	})

	t.Run("regions are joined verbatim in the order given", func(t *testing.T) {
		msg := missRegionHint("x", "EC2", []string{"eu-north-1", "us-east-1", "us-west-2"})
		assert.True(t, strings.Contains(msg, "eu-north-1, us-east-1, us-west-2"))
	})
}

// TestCloudResourceSearchPage pins #39201: the limit is shared round-robin
// across accounts instead of filling with the alphabetically first account.
func TestCloudResourceSearchPage(t *testing.T) {
	q := cloudResourceSearchPage("SELECT 1")
	assert.Contains(t, q, "ROW_NUMBER() OVER (PARTITION BY m.account_id ORDER BY m.name) AS rn")
	assert.Contains(t, q, "ORDER BY rn,")
	assert.Contains(t, q, "AS account_counts")
	assert.Contains(t, q, fmt.Sprintf("LIMIT %d", cloudResourceSearchLimit))
}

func TestCloudCutoffNote(t *testing.T) {
	assert.Empty(t, cloudCutoffNote(nil))
	assert.Empty(t, cloudCutoffNote([]CloudResourceInfo{{Total: 1, AccountCounts: `{"a":1}`}}))
	// aws-prod has matches but none on the page: it still shows in the note.
	counts := `{"aws-demo": 54, "dev-aws": 21, "aws-prod": 12}`
	rows := []CloudResourceInfo{
		{AccountName: "aws-demo", Total: 87, AccountCounts: counts},
		{AccountName: "dev-aws", Total: 87, AccountCounts: counts},
	}
	note := cloudCutoffNote(rows)
	assert.Contains(t, note, "Showing 2 of 87")
	assert.Contains(t, note, "by account: aws-demo 54, dev-aws 21, aws-prod 12")
}

// TestCloudServicePatterns pins #42036: Azure inventory stores ARM types, so a
// friendly service name must expand to the stored service_name.
func TestCloudServicePatterns(t *testing.T) {
	cases := []struct {
		input string
		want  string // a pattern that must be present
	}{
		{"cosmos", "%microsoft.documentdb/databaseaccounts%"},
		{"Cosmos DB", "%microsoft.documentdb/databaseaccounts%"},
		{"Azure Cosmos DB", "%microsoft.documentdb/databaseaccounts%"},
		{"AKS", "%microsoft.containerservice/managedclusters%"},
		{"App Service", "%microsoft.web/sites%"},
		{"Azure Key Vault", "%keyvault%"},       // prefix stripped, compact
		{"secrets manager", "%secretsmanager%"}, // compact form matches AWSSecretsManager
		{"Amazon SQS", "%AWSQueueService%"},     // prefix stripped, then alias
		{"GKE", "%Kubernetes Engine%"},
		{"function", "%AWSLambda%"}, // singular and plural both mapped
		{"blobs", "%AmazonS3%"},
		{"ALBs", "%AWSELB%"},
		{"Cloud SQL", "%Cloud SQL%"}, // input kept as given (GCP names have spaces)
		{"microsoft.documentdb/databaseaccounts", "%microsoft.documentdb/databaseaccounts%"},
	}
	for _, tc := range cases {
		t.Run(tc.input, func(t *testing.T) {
			assert.Contains(t, cloudServicePatterns(tc.input), tc.want)
		})
	}

	t.Run("empty input yields no filter", func(t *testing.T) {
		assert.Nil(t, cloudServicePatterns("  "))
	})
	t.Run("duplicates dropped case-insensitively", func(t *testing.T) {
		assert.Equal(t, []string{"%EC2%"}, cloudServicePatterns("EC2"))
	})
	t.Run("short prefix-stripped remainder is not a pattern", func(t *testing.T) {
		// "AmazonES" is the stored OpenSearch name; %es% would match most services.
		assert.NotContains(t, cloudServicePatterns("AmazonES"), "%es%")
		assert.NotContains(t, cloudServicePatterns("Azure AD"), "%ad%")
		// Short names still resolve through the alias table.
		assert.Contains(t, cloudServicePatterns("Amazon SQS"), "%AWSQueueService%")
	})
	t.Run("alias lookup is nil for names without an entry", func(t *testing.T) {
		assert.Nil(t, cloudServiceAliasPatterns("dynamodb"))
		assert.Equal(t, []string{"microsoft.documentdb/databaseaccounts"}, cloudServiceAliasPatterns("Azure Cosmos DB"))
	})
}

// storedServiceNames are service_name values the cloud collectors write.
var storedServiceNames = strings.Split(`AWSBackup|AWSCloudFormation|AWSCloudTrail|AWSConfig|AWSELB|AWSElasticBeanstalk|AWSFargate|AWSGlue|AWSIAM|AWSKMS|AWSLambda|AWSQueueService|AWSSecretsManager|AWSSecurityHub|AWSStepFunctions|AWSSystemsManager|AWSWAF|AWSXRay|AmazonBedrock|AmazonCloudFront|AmazonCloudWatch|AmazonDynamoDB|AmazonEC2|AmazonECR|AmazonECS|AmazonEFS|AmazonEKS|AmazonES|AmazonElastiCache|AmazonEventBridge|AmazonFSx|AmazonGuardDuty|AmazonInspector|AmazonMSK|AmazonMemoryDB|AmazonOpenSearchService|AmazonRDS|AmazonRedshift|AmazonRoute53|AmazonS3|AmazonSES|AmazonSNS|AmazonSageMaker|AmazonVPC|Artifact Registry|BigQuery|Cloud Load Balancing|Cloud Monitoring|Cloud Tasks|Compute Engine|compute.googleapis.com/Disk|Firestore|Cloud Functions|Kubernetes Engine|Gemini API|IAM|Memorystore|Networking|Cloud Pub/Sub|Cloud Run|Cloud SQL|Cloud Scheduler|Secret Manager|Cloud Storage|VM Manager|Vertex AI|microsoft.app/containerapps|microsoft.cache/redis|microsoft.cdn/profiles|microsoft.compute/disks|microsoft.compute/virtualmachinescalesets|microsoft.compute/virtualmachines|microsoft.containerservice/managedclusters|microsoft.dbformariadb/servers|microsoft.dbformysql/flexibleservers|microsoft.dbforpostgresql/flexibleservers|microsoft.documentdb/databaseaccounts|microsoft.keyvault/vaults|microsoft.network/applicationgateways|microsoft.network/azurefirewalls|microsoft.network/dnszones|microsoft.network/frontdoors|microsoft.network/virtualnetworks|microsoft.sql/managedinstances|microsoft.sql/servers|microsoft.storage/storageaccounts|microsoft.storage/storageaccounts/fileservices|microsoft.web/sites|microsoft.web/sites/functions|microsoft.containerregistry/registries|microsoft.logic/workflows|microsoft.operationalinsights/workspaces|microsoft.recoveryservices/vaults|microsoft.servicebus/namespaces|microsoft.eventhub/namespaces|microsoft.web/serverfarms|microsoft.network/loadbalancers|microsoft.network/networksecuritygroups|microsoft.network/publicipaddresses`, "|")

// TestCloudServicePatternsCorpus checks friendly names people type against the
// stored names: each must find its service (no miss) without matching most of
// the catalog (no over-match). Services NudgeBee does not collect are left out.
func TestCloudServicePatternsCorpus(t *testing.T) {
	for _, q := range strings.Split(`EC2|AWS EC2|Amazon EC2|S3|AWS S3|Amazon S3|RDS|AWS RDS|Amazon RDS|Aurora|DynamoDB|AWS DynamoDB|Lambda|AWS Lambda|SQS|AWS SQS|Amazon SQS|SNS|AWS SNS|ELB|ALB|Application Load Balancer|load balancer|EKS|AWS EKS|ECS|ECR|EFS|IAM|AWS IAM|KMS|CloudWatch|CloudFront|Route 53|Route53|Secrets Manager|Step Functions|ElastiCache|AWS ElastiCache|Redshift|OpenSearch|Elasticsearch|MSK|Kafka|MemoryDB|SageMaker|Bedrock|WAF|CloudTrail|Systems Manager|SSM|Fargate|Glue|EventBridge|Backup|VPC|GuardDuty|Compute Engine|GCE|GCP VM|GKE|Kubernetes Engine|Cloud SQL|GCP Cloud SQL|Cloud Storage|GCS|BigQuery|Pub/Sub|PubSub|Cloud Run|Cloud Functions|Memorystore|Firestore|Secret Manager|Artifact Registry|Vertex AI|Cloud Tasks|Cloud Scheduler|Cosmos DB|Azure Cosmos DB|AKS|App Service|Function App|Azure Functions|Key Vault|Azure SQL|SQL Database|Azure VM|VM|Virtual Machine|Storage Account|Blob|Azure Files|Redis|Azure Cache for Redis|Service Bus|Event Hubs|Event Hub|Log Analytics|Container Apps|ACR|Container Registry|Front Door|Application Gateway|VNet|NSG|Public IP|PostgreSQL|Postgres|MySQL|MariaDB|Logic Apps|Recovery Services Vault|Azure Firewall|DNS`, "|") {
		var hits []string
		for _, s := range storedServiceNames {
			for _, p := range cloudServicePatterns(q) {
				if strings.Contains(strings.ToLower(s), strings.ToLower(strings.Trim(p, "%"))) {
					hits = append(hits, s)
					break
				}
			}
		}
		assert.NotEmpty(t, hits, "%q matched no stored service", q)
		assert.LessOrEqual(t, len(hits), 5, "%q matched too many services: %v", q, hits)
	}
}

func TestCloudServicePatternsShortRemainder(t *testing.T) {
	assert.Contains(t, cloudServicePatterns("AWS RDS"), "%amazonrds%")
	assert.Contains(t, cloudServicePatterns("Azure SQL"), "%microsoft.sql%")
	assert.NotContains(t, cloudServicePatterns("AWS RDS"), "%rds%")
}

func TestCloudResourceFilters(t *testing.T) {
	req := CloudResourceSearchRequest{ResourceName: "orders", Service: "cosmos", ResourceType: "cosmos", Region: "eastus"}
	sql, args := cloudResourceFilters(req, []string{"%cosmos%"}, nil, 2)
	assert.Equal(t, " AND (cr.name ILIKE $2 OR cr.resourse_id = $3) AND cr.service_name ILIKE ANY($4) AND cr.type ILIKE $5 AND cr.region = $6", sql)
	assert.Len(t, args, 5)

	// The alias retry adds one placeholder for the type's service names.
	sql, args = cloudResourceFilters(req, []string{"%cosmos%"}, []string{"microsoft.documentdb/databaseaccounts"}, 2)
	assert.Equal(t, " AND (cr.name ILIKE $2 OR cr.resourse_id = $3) AND cr.service_name ILIKE ANY($4) AND (cr.type ILIKE $5 OR cr.service_name ILIKE ANY($6)) AND cr.region = $7", sql)
	assert.Len(t, args, 6)

	sql, args = cloudResourceFilters(CloudResourceSearchRequest{Region: "us-east-1"}, nil, nil, 2)
	assert.Equal(t, " AND cr.region = $2", sql)
	assert.Equal(t, []interface{}{"us-east-1"}, args)
}
