package sources

import (
	"nudgebee/services/knowledge_graph/flow_sources"
	"strings"
)

// synthesizeGCPEndpointDNS sets `properties["dns_name"]` for GCP resource
// types where the GCP API metadata doesn't carry one but a deterministic
// public hostname can be constructed from the resource id (Cloud Storage,
// today). No-op when `dns_name` is already set, so Cloud SQL (connectionName)
// and GKE (cluster endpoint) — which their per-type extractors stamp directly
// — are not overridden.
//
// Mirrors sources.synthesizeAWSEndpointDNS in shape and intent. The actual
// hostname construction lives in flow_sources.GcpServiceDNS so the
// cloud_resourses-derived path (extractDNSName) and the in-graph path can
// never diverge on what string they consider canonical.
func synthesizeGCPEndpointDNS(properties map[string]interface{}) {
	if existing, _ := properties["dns_name"].(string); existing != "" {
		return
	}

	service, _ := properties["service_name"].(string)
	region, _ := properties["region"].(string)
	project, _ := properties["gcp_project_id"].(string)

	resourceID, _ := properties["name"].(string)

	canonical, aliases := flow_sources.GcpServiceDNS(service, region, project, resourceID)
	if canonical != "" {
		properties["dns_name"] = strings.ToLower(canonical)
	}
	if len(aliases) > 0 {
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

// extractGCPCloudRunURL stamps the per-service Cloud Run URL from `meta.url`
// onto `properties["dns_name"]` so DirectEndpointMatchStrategy hits when eBPF
// observes traffic to the service hostname. The URL has the per-service
// identifier baked into the leftmost label
// (e.g. `nudgebee-booth-eln5wjp7uq-el.a.run.app`), so multiple services in
// the same project don't collide on the host.
//
// Defensive: no-op when `dns_name` is already set or `meta.url` is missing /
// not a string.
func extractGCPCloudRunURL(properties map[string]interface{}, metaMap map[string]interface{}) {
	if existing, _ := properties["dns_name"].(string); existing != "" {
		return
	}
	url, ok := metaMap["url"].(string)
	if !ok || url == "" {
		return
	}
	if host := repoURIHost(url); host != "" {
		properties["dns_name"] = strings.ToLower(host)
	}
}
