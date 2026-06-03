package flow_sources

import (
	"fmt"
	"strings"
)

// AwsServiceDNS returns the canonical public-DNS hostname for an AWS resource
// plus any well-known aliases the SDK / clients also use. Returns ("", nil) when
// a hostname cannot be deterministically constructed (missing region, unknown
// service, etc.). Pure function — no side effects, no I/O.
//
// Shared by `sources.synthesizeAWSEndpointDNS` (which writes the result onto
// the AWS-source node's properties) and `flow_sources.extractDNSName` (which
// emits the result for cloud_resourses-derived index entries). Keeping the
// hostname construction in one place means the two index-build paths can never
// diverge on what string they consider canonical.
//
// **Per-resource only**: this function deliberately skips services where the
// AWS public hostname is the *region- or account-scoped service endpoint*
// (SQS, SNS, DynamoDB, Lambda, Kinesis, ECR private + public). For those, the
// hostname is shared by every resource of that service in the region/account
// (per-queue/topic/table identity is in the URL path). Stamping a shared host
// as `dns_name` on individual resource nodes would trigger first-write-wins
// collisions in buildCloudEndpointIndex and false-positive matches like
// `sqs.us-east-1.amazonaws.com → some-arbitrary-queue`. Production sync 57
// observed 420 Lambdas / 30 SQS queues / 111 ECR repos sharing one host —
// they're left to the inferred-CloudResource fallback in the strategy chain
// instead of being misattributed to a specific resource.
//
// `service` is the AWS billing service name. `resourceID` is the bare resource
// identifier (bucket name for S3, API id for API Gateway). `region` is the AWS
// region; `accountNumber` is reserved for future per-resource services that
// embed it in the host.
func AwsServiceDNS(service, region, accountNumber, resourceID string) (string, []string) {
	_ = accountNumber
	region = strings.ToLower(strings.TrimSpace(region))

	switch service {
	case "AmazonS3":
		bucket := strings.TrimSpace(resourceID)
		if bucket == "" {
			return "", nil
		}
		if region == "" {
			return fmt.Sprintf("%s.s3.amazonaws.com", bucket), nil
		}
		canonical := fmt.Sprintf("%s.s3.%s.amazonaws.com", bucket, region)
		aliases := []string{
			fmt.Sprintf("%s.s3.dualstack.%s.amazonaws.com", bucket, region),
			fmt.Sprintf("%s.s3-website-%s.amazonaws.com", bucket, region),
			fmt.Sprintf("%s.s3.amazonaws.com", bucket),
		}
		return canonical, aliases

	case "AmazonAPIGateway":
		apiID := strings.TrimSpace(resourceID)
		if region == "" || apiID == "" {
			return "", nil
		}
		return fmt.Sprintf("%s.execute-api.%s.amazonaws.com", apiID, region), nil
	}

	return "", nil
}

// awsServiceFromResourceType maps the `type` column of cloud_resourses (as
// written by the cloud-collector) to the `service_name` AwsServiceDNS expects.
// Used by extractDNSName to drive the synthesizer when the row's type doesn't
// directly correspond to an AWS API field carrying an endpoint.
//
// Both the bare collector spellings (`storage`, `queue`, `topic`, `table`) and
// the legacy explicit ones (`s3_bucket`, `sqs_queue`, …) are accepted —
// production data uses the former, but the SQL allowlist in
// fetchCloudResourcesMap still includes the latter for safety, and any DB
// audit revealing a row in the legacy shape should still be synthesizable.
func awsServiceFromResourceType(resourceType string) string {
	switch resourceType {
	case "s3_bucket", "storage":
		return "AmazonS3"
	case "sqs_queue", "queue":
		return "AWSQueueService"
	case "sns_topic", "topic":
		return "AmazonSNS"
	case "dynamodb_table", "table":
		return "AmazonDynamoDB"
	}
	return ""
}

// GcpServiceDNS returns the canonical public-DNS hostname for a GCP resource,
// for resources where AWS doesn't expose one in API metadata. Mirrors
// AwsServiceDNS — only per-resource hostnames are synthesized; per-region or
// per-project shared service endpoints are skipped (see comment block below).
//
// **Per-resource only** (synthesized):
//   - "Cloud Storage" → `<bucket>.storage.googleapis.com` — the bucket name
//     is the leftmost label, so multiple buckets in the same project don't
//     collide on this host.
//
// **Skipped** (region/project-scoped service endpoint, shared by every
// resource of that service in the project — same first-write-wins collision
// risk that broke the AWS sync 57 build for Lambda/SQS/SNS/DDB):
//   - Cloud Run / Cloud Functions: per-service URL lives in `meta.url`; that's
//     stamped directly by extractGCPCloudRunMetadata. The bare service host
//     `*.run.app` / `*.cloudfunctions.net` is not synthesized.
//   - Pub/Sub `pubsub.googleapis.com`, BigQuery `bigquery.googleapis.com`,
//     Artifact Registry `<region>-docker.pkg.dev`, Spanner / Firestore /
//     KMS / Secret Manager / Cloud DNS / Logging / Monitoring service hosts.
//
// `service` is the value of the GCP source's `service_name` property
// (e.g. "Cloud Storage"). `resourceID` is the bare resource id (bucket name).
// `region` and `project` are reserved for future per-resource cases that
// embed them in the host.
func GcpServiceDNS(service, region, project, resourceID string) (string, []string) {
	_, _ = region, project

	switch service {
	case "Cloud Storage":
		bucket := strings.TrimSpace(resourceID)
		if bucket == "" {
			return "", nil
		}
		return fmt.Sprintf("%s.storage.googleapis.com", bucket), nil
	}

	return "", nil
}

// gcpServiceFromResourceType maps cloud_resourses.type values that the
// GCP cloud-collector writes onto a service_name GcpServiceDNS understands.
// Used by extractDNSName to mint a DNS entry for cloud_resourses rows that
// didn't make it into the in-graph node set this build.
func gcpServiceFromResourceType(resourceType string) string {
	switch resourceType {
	case "storage.googleapis.com/Bucket", "cloud-storage":
		return "Cloud Storage"
	}
	return ""
}
