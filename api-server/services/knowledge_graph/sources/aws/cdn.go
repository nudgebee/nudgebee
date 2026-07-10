package aws

import (
	"encoding/json"
	"strings"

	"nudgebee/services/knowledge_graph/core"
)

// cloudFrontDistributionSchema — concrete schema for CloudFront distributions.
// Fields written by extractCDNMetadata.
var cloudFrontDistributionSchema = core.SpecificTypeSchema{
	SpecificType: "CloudFrontDistribution",
	NodeType:     core.NodeTypeCDN,
	Properties: []core.PropertyDef{
		{Name: "domain_name", Indexed: true},
		{Name: "dns_name", Indexed: true},
		{Name: "origin_domains"},
		{Name: "is_public_entry"},
		// Additional provider fields (not yet emitted by our extractor):
		{Name: "etag"},
		{Name: "aliases"},
		{Name: "comment"},
		{Name: "enabled"},
		{Name: "price_class"},
		{Name: "http_version"},
		{Name: "is_ipv6_enabled"},
		{Name: "staging"},
		{Name: "last_modified_time"},
		{Name: "viewer_protocol_policy"},
		{Name: "acm_certificate_arn"},
		{Name: "cloudfront_default_certificate"},
		{Name: "minimum_protocol_version"},
		{Name: "ssl_support_method"},
		{Name: "iam_certificate_id"},
		{Name: "geo_restriction_type"},
		{Name: "geo_restriction_locations"},
		{Name: "web_acl_id"},
	},
}

// cloudFrontFunctionSchema — CloudFront Functions share the NodeTypeCDN path;
// their metadata carries no distribution domain, so domain_name is declared for
// ontology parity but is typically empty.
var cloudFrontFunctionSchema = core.SpecificTypeSchema{
	SpecificType: "CloudFrontFunction",
	NodeType:     core.NodeTypeCDN,
	Properties: []core.PropertyDef{
		{Name: "domain_name", Indexed: true},
		{Name: "dns_name", Indexed: true},
		{Name: "is_public_entry"},
	},
}

func init() {
	core.RegisterSpecificTypeSchema(cloudFrontDistributionSchema)
	core.RegisterSpecificTypeSchema(cloudFrontFunctionSchema)
}

// extractCDNMetadata extracts essential fields for CloudFront distributions
func (s *AWSSource) extractCDNMetadata(properties map[string]interface{}, metaMap map[string]interface{}) {
	// CloudFront distributions are always internet-facing.
	properties["is_public_entry"] = true

	// Domain name (critical for CDN endpoint)
	if domainName, ok := metaMap["DomainName"].(string); ok && domainName != "" {
		properties["domain_name"] = domainName
		// Mirror onto dns_name so the ExternalService enricher's index hits
		// when eBPF observes the `<distribution-id>.cloudfront.net` host.
		if existing, _ := properties["dns_name"].(string); existing == "" {
			properties["dns_name"] = strings.ToLower(domainName)
		}
	}

	// Status (important for operational state)
	if status, ok := metaMap["Status"].(string); ok && status != "" {
		properties["status"] = status
	}

	// Extract origin domains (needed for CloudFront->LoadBalancer relationship).
	// AWS API nests Origins under DistributionConfig, not at the top level.
	var originItems []interface{}
	if distConfig, ok := metaMap["DistributionConfig"].(map[string]interface{}); ok {
		if origins, ok := distConfig["Origins"].(map[string]interface{}); ok {
			if items, ok := origins["Items"].([]interface{}); ok {
				originItems = items
			}
		}
	}
	// Fallback: some responses may have Origins at top level (e.g. list-distributions summary)
	if len(originItems) == 0 {
		if origins, ok := metaMap["Origins"].(map[string]interface{}); ok {
			if items, ok := origins["Items"].([]interface{}); ok {
				originItems = items
			}
		}
	}
	if len(originItems) > 0 {
		var originDomains []string
		for _, item := range originItems {
			if originItem, ok := item.(map[string]interface{}); ok {
				if domain, ok := originItem["DomainName"].(string); ok && domain != "" {
					originDomains = append(originDomains, domain)
				}
			}
		}
		if len(originDomains) > 0 {
			if domainsJSON, err := json.Marshal(originDomains); err == nil {
				properties["origin_domains"] = string(domainsJSON)
			}
		}
	}
}
