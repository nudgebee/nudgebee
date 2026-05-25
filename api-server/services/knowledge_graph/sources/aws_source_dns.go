package sources

import (
	"net/url"
	"nudgebee/services/knowledge_graph/flow_sources"
	"strings"
)

// synthesizeAWSEndpointDNS sets `properties["dns_name"]` and
// `properties["dns_aliases"]` for AWS resource types where AWS does not expose
// a DNS endpoint in its API metadata (S3, SQS, SNS, DynamoDB, ECR, Lambda,
// Kinesis, API Gateway). It is a no-op when `dns_name` is already set — so
// EFS / RDS / ElastiCache / LB extractors that already populate dns_name from
// real API data are not overridden.
//
// The hostname construction itself lives in flow_sources.AwsServiceDNS so the
// cloud_resourses-derived path in flow_sources.extractDNSName mints the same
// strings — preventing the in-graph and DB-derived index entries from drifting.
//
// Inputs are read from `properties` rather than passed explicitly so the
// function can be called from the generic `extractEssentialMetadataByNodeType`
// dispatch site without each caller having to assemble a struct.
func synthesizeAWSEndpointDNS(properties map[string]interface{}) {
	if existing, _ := properties["dns_name"].(string); existing != "" {
		// Some extractor already wrote a dns_name — trust it (RDS/EFS/EC/LB).
		return
	}

	service, _ := properties["service_name"].(string)
	region, _ := properties["region"].(string)
	accountNumber, _ := properties["aws_account_number"].(string)

	// Pull the bare resource identifier from whichever per-service field the
	// extractors populate. Fall back to `name` so this still works for nodes
	// that took the generic CloudResource path (which doesn't run a specific
	// extractor and so has no per-service id field).
	var resourceID string
	switch service {
	case "AmazonS3":
		resourceID, _ = properties["bucket_name"].(string)
		if resourceID == "" {
			resourceID, _ = properties["name"].(string)
		}
		// For S3 the regional placement lives in `bucket_region` (set by
		// extractStorageMetadata); fall back to the row-level `region` so we
		// still synthesize a hostname for buckets where the meta blob lacked
		// a Region key.
		if br, _ := properties["bucket_region"].(string); br != "" {
			region = br
		}
	// ECR / ECR Public: deliberately not synthesized. The `<account>.dkr.ecr
	// .<region>.amazonaws.com` host is shared by every repo in the account+
	// region; per-repo identity lives in the path. Stamping a shared host on
	// individual repo nodes triggered first-write-wins collisions in
	// buildCloudEndpointIndex (production sync 57: 111 + 99 repos sharing one
	// host) and false-positive matches.
	case "AmazonAPIGateway":
		// API Gateway needs the API id; aws_source stores it in resource_id.
		resourceID, _ = properties["resource_id"].(string)
		if resourceID == "" {
			resourceID, _ = properties["name"].(string)
		}
	default:
		resourceID, _ = properties["name"].(string)
	}

	canonical, aliases := flow_sources.AwsServiceDNS(service, region, accountNumber, resourceID)
	if canonical != "" {
		properties["dns_name"] = strings.ToLower(canonical)
	}
	if len(aliases) > 0 {
		// Stamp aliases as []string so buildCloudEndpointIndex's
		// stringSliceProp can read them in either the in-process or
		// post-JSON-roundtrip shape without a coercion helper.
		lowered := make([]string, 0, len(aliases))
		for _, a := range aliases {
			if a == "" {
				continue
			}
			lowered = append(lowered, strings.ToLower(a))
		}
		if len(lowered) > 0 {
			properties["dns_aliases"] = lowered
		}
	}
}

// repoURIHost returns the host portion of an ECR repository URI of the form
// `<account>.dkr.ecr.<region>.amazonaws.com/<repo>` (or `public.ecr.aws/...`).
// Returns "" for unparseable inputs. AWS sometimes emits the URI without a
// scheme; url.Parse accepts that and treats the whole thing as a path, so we
// patch in `https://` when missing.
func repoURIHost(uri string) string {
	uri = strings.TrimSpace(uri)
	if uri == "" {
		return ""
	}
	if !strings.Contains(uri, "://") {
		uri = "https://" + uri
	}
	parsed, err := url.Parse(uri)
	if err != nil || parsed.Host == "" {
		return ""
	}
	return parsed.Host
}
