package sources

import (
	"encoding/json"
	"log/slog"
	"strings"

	"nudgebee/services/cloud"
	"nudgebee/services/security"
)

// Private DNS zone → VNet link fetching.
//
// Private DNS zones and their virtual-network links are collected via the sparse
// "billing" source, so the link data isn't in cloud_resourses.meta. We fetch it
// live through the cloud collector with a single Azure Resource Graph query, then
// the cross-account enricher (azure_dns_enricher.go) connects the zone nodes to
// the VNets they are linked to. Resolution lives in the enricher rather than the
// per-account source because a zone and its linked VNet frequently sit in
// different subscriptions (hub-spoke), which only the unified graph can match.

// privateDNSVNetLinkQuery returns every private-DNS-zone↔VNet link visible to the
// account in one call (zoneId, vnetId pairs), avoiding a per-zone fan-out.
const privateDNSVNetLinkQuery = `az graph query -q "Resources | where type =~ 'microsoft.network/privatednszones/virtualnetworklinks' | project zoneId=tostring(split(id, '/virtualNetworkLinks/')[0]), vnetId=tostring(properties.virtualNetwork.id)" --output json`

// dnsVNetLink is one private-DNS-zone → VNet association.
type dnsVNetLink struct {
	ZoneID string `json:"zoneId"`
	VnetID string `json:"vnetId"`
}

// argDNSLinkResult is the wrapped Azure Resource Graph response shape.
type argDNSLinkResult struct {
	Data []dnsVNetLink `json:"data"`
}

// parsePrivateDNSVNetLinks parses the ARG query output (the inner CLI JSON string)
// into zone→vnet pairs, tolerating both the wrapped {data:[...]} object and a bare
// array.
func parsePrivateDNSVNetLinks(out string) []dnsVNetLink {
	out = strings.TrimSpace(out)
	if out == "" {
		return nil
	}
	if strings.HasPrefix(out, "{") {
		var wrapped argDNSLinkResult
		if err := json.Unmarshal([]byte(out), &wrapped); err == nil {
			return wrapped.Data
		}
		return nil
	}
	var arr []dnsVNetLink
	if err := json.Unmarshal([]byte(out), &arr); err == nil {
		return arr
	}
	return nil
}

// fetchPrivateDNSVNetLinks runs the live ARG query for one Azure account and
// returns the zone→vnet links. Failures (missing collector, CLI error) yield no
// links rather than an error so callers can keep going across accounts.
func fetchPrivateDNSVNetLinks(reqCtx *security.RequestContext, accountID string, logger *slog.Logger) []dnsVNetLink {
	if reqCtx == nil || accountID == "" {
		return nil
	}
	resp, err := cloud.ExecuteCli(reqCtx, cloud.CloudExecuteCliCommandRequest{
		AccountID: accountID,
		Command:   privateDNSVNetLinkQuery,
	})
	if err != nil {
		logger.Warn("failed to fetch private DNS vnet links via CLI", "account_id", accountID, "error", err)
		return nil
	}
	return parsePrivateDNSVNetLinks(extractCLIOutput(resp))
}
