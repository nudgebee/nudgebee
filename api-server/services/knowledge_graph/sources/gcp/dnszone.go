package gcp

import (
	"encoding/json"
	"fmt"
	"nudgebee/services/cloud"
	"nudgebee/services/knowledge_graph/core"
	"nudgebee/services/knowledge_graph/sources"
	"nudgebee/services/security"
	"strings"
)

// cloudDNSZoneSchema is the concrete per-specific_type property schema for the
// CloudDNSZone specific_type. Names are the node.Properties keys written by
// ensureGCPDNSZoneNodes (CLI).
// core.QueryablePropertiesMap[NodeTypeDNSZone] uses zone_type/record_count, which
// GCP doesn't populate, so the useful native fields (dns_name/visibility/
// is_public_entry) are Indexed here; the record aggregates are declared only.
var cloudDNSZoneSchema = core.SpecificTypeSchema{
	SpecificType: "CloudDNSZone",
	NodeType:     core.NodeTypeDNSZone,
	Properties: []core.PropertyDef{
		{Name: "dns_name", Indexed: true},
		{Name: "visibility", Indexed: true},
		{Name: "is_public_entry", Indexed: true},
		{Name: "a_record_ips"},
		{Name: "cname_records"},
		{Name: "name_servers"},
		// Additional provider fields (not yet emitted by our extractor):
		{Name: "description"},
		{Name: "dnssec_state"},
		{Name: "dnssec_key_signing_algorithm"},
		{Name: "dnssec_zone_signing_algorithm"},
		{Name: "kind"},
		{Name: "nameservers"},
		{Name: "created_at"},
	},
}

func init() { core.RegisterSpecificTypeSchema(cloudDNSZoneSchema) }

// fetchDNSZonesFromGCP fetches all Cloud DNS managed zones via gcloud CLI, then for each zone
// fetches its record sets and attaches them to the zone struct.
func (s *GCPSource) fetchDNSZonesFromGCP(reqCtx *security.RequestContext, accountID string) ([]GCPDNSManagedZone, error) {
	cmd := "gcloud dns managed-zones list --format=json"
	s.logger.Info("fetching GCP DNS managed zones via CLI", "account_id", accountID)

	resp, err := cloud.ExecuteCliWithRetry(reqCtx, cloud.CloudExecuteCliCommandRequest{
		AccountID: accountID,
		Command:   cmd,
	}, 3)
	if err != nil {
		return nil, fmt.Errorf("failed to execute gcloud CLI: %w", err)
	}

	output, err := parseGCloudCLIResponse(resp)
	if err != nil {
		return nil, err
	}

	var zones []GCPDNSManagedZone
	if err := json.Unmarshal([]byte(output), &zones); err != nil {
		return nil, fmt.Errorf("failed to parse DNS zones response: %w", err)
	}

	// For each zone, fetch its record sets. Per-zone fetch is required because gcloud doesn't
	// expose a "list all record sets across all zones" command.
	for i := range zones {
		records, err := s.fetchDNSRecordSetsFromGCP(reqCtx, accountID, zones[i].Name)
		if err != nil {
			s.logger.Warn("failed to fetch record sets for DNS zone",
				"zone", zones[i].Name, "error", err)
			continue
		}
		zones[i].Records = records
	}

	return zones, nil
}

// fetchDNSRecordSetsFromGCP fetches all record sets for a single managed zone.
func (s *GCPSource) fetchDNSRecordSetsFromGCP(reqCtx *security.RequestContext, accountID, zoneName string) ([]GCPDNSResourceRecordSet, error) {
	cmd := fmt.Sprintf("gcloud dns record-sets list --zone=%s --format=json", zoneName)

	resp, err := cloud.ExecuteCliWithRetry(reqCtx, cloud.CloudExecuteCliCommandRequest{
		AccountID: accountID,
		Command:   cmd,
	}, 3)
	if err != nil {
		return nil, fmt.Errorf("failed to execute gcloud CLI: %w", err)
	}

	output, err := parseGCloudCLIResponse(resp)
	if err != nil {
		return nil, err
	}

	var records []GCPDNSResourceRecordSet
	if err := json.Unmarshal([]byte(output), &records); err != nil {
		return nil, fmt.Errorf("failed to parse DNS records response: %w", err)
	}

	return records, nil
}

// ensureGCPDNSZoneNodes creates DNSZone nodes from Cloud DNS managed zones (CLI data).
// Each zone gets a flat a_record_ips list aggregating all A-record values across all record sets,
// so the cross-account rule "gcp_dns_to_loadbalancer" can match them via `contains` against
// LoadBalancer.ip_address.
func (s *GCPSource) ensureGCPDNSZoneNodes(nodes []*core.DbNode, lookup *sources.NodeLookup, cliData *gcpCLIData, req *core.SourceBuildRequest) []*core.DbNode {
	for name, zone := range cliData.dnsZones {
		// Skip if a DNSZone node already exists with this name.
		found := false
		if dnsNodes, ok := lookup.ByNodeType[core.NodeTypeDNSZone]; ok {
			for _, existing := range dnsNodes {
				if extractGCPShortName(getNodeName(existing)) == name {
					found = true
					break
				}
			}
		}
		if found {
			continue
		}

		isPublic := strings.EqualFold(zone.Visibility, "public") || zone.Visibility == ""

		var aRecordIPs []interface{}
		var cnameRecords []interface{}
		for _, r := range zone.Records {
			switch r.Type {
			case "A", "AAAA":
				for _, v := range r.Rrdatas {
					aRecordIPs = append(aRecordIPs, v)
				}
			case "CNAME":
				for _, v := range r.Rrdatas {
					cnameRecords = append(cnameRecords, map[string]interface{}{"name": r.Name, "target": v})
				}
			}
		}

		properties := map[string]interface{}{
			"name":            name,
			"dns_name":        zone.DnsName,
			"type":            "dns-managed-zone",
			"subtype":         "GCPDNSZone",
			"service_name":    "Cloud DNS",
			"cloud_provider":  "GCP",
			"inferred":        false,
			"visibility":      zone.Visibility,
			"is_public_entry": isPublic,
		}
		if len(aRecordIPs) > 0 {
			properties["a_record_ips"] = aRecordIPs
		}
		if len(cnameRecords) > 0 {
			properties["cname_records"] = cnameRecords
		}
		if len(zone.NameServers) > 0 {
			properties["name_servers"] = zone.NameServers
		}

		uniqueKey := fmt.Sprintf("gcp:%s:global:%s:%s",
			req.CloudAccountID, core.NodeTypeDNSZone, name)
		node := core.NewNode(core.NodeTypeDNSZone, uniqueKey, properties, req.TenantID, req.CloudAccountID, "gcp")
		nodes = append(nodes, node)
		s.logger.Debug("created GCP DNSZone node from CLI",
			"name", name, "dns_name", zone.DnsName, "a_records", len(aRecordIPs))
	}
	return nodes
}
