package azure

import (
	"nudgebee/services/knowledge_graph/core"
	"strings"
)

// azureLoadBalancerSchema is the concrete per-specific_type property schema for the
// AzureLoadBalancer specific_type (Standard LB + Application Gateway). Names are the
// node.Properties keys written by extractLoadBalancerMetadata; universal base keys
// are implicit. Frontend/backend lists are declared but not indexed.
var azureLoadBalancerSchema = core.SpecificTypeSchema{
	SpecificType: "AzureLoadBalancer",
	NodeType:     core.NodeTypeLoadBalancer,
	Properties: []core.PropertyDef{
		{Name: "dns_name", Indexed: true}, // frontend IP / hostname
		{Name: "ip_address", Indexed: true},
		{Name: "lb_sku", Indexed: true},
		{Name: "resource_group", Indexed: true},
		{Name: "is_public_entry"},
		{Name: "frontend_ip_addresses"},
		{Name: "frontend_hostnames"},
		{Name: "public_ip_refs"},
		{Name: "backend_pool_ids"},
		{Name: "backend_addresses"},
	},
}

// azureBackendPoolSchema covers the AzureBackendPool specific_type. The Azure
// source does not currently emit a dedicated BackendPool node (backend pools are
// folded into the load balancer as backend_pool_ids), so this schema is minimal —
// registered to satisfy specific_type coverage.
var azureBackendPoolSchema = core.SpecificTypeSchema{
	SpecificType: "AzureBackendPool",
	NodeType:     core.NodeTypeBackendPool,
	Properties: []core.PropertyDef{
		{Name: "resource_group", Indexed: true},
	},
}

func init() {
	core.RegisterSpecificTypeSchema(azureLoadBalancerSchema)
	core.RegisterSpecificTypeSchema(azureBackendPoolSchema)
}

// extractLoadBalancerMetadata handles both Standard Load Balancer (subtype=loadbalancer) and
// Application Gateway (subtype=applicationgateway). Both expose frontend IPs and backend address
// pools; Application Gateway additionally exposes httpListeners with hostnames.
func (s *AzureSource) extractLoadBalancerMetadata(properties map[string]interface{}, metaMap map[string]interface{}) {
	// Flat key (simplified/mock data)
	if frontendIP, ok := metaMap["frontendIpAddress"].(string); ok && frontendIP != "" {
		properties["dns_name"] = frontendIP
		properties["ip_address"] = frontendIP
	}
	if sku, ok := metaMap["sku"].(string); ok && sku != "" {
		properties["lb_sku"] = sku
	}
	s.extractAzureLBFrontendIPs(properties, metaMap)
	s.extractAzureLBBackendPools(properties, metaMap)
	s.extractAzureLBHTTPListenerHostnames(properties, metaMap)

	// Mark public entry: Application Gateway is always internet-facing unless explicitly
	// configured as Internal Load Balancer mode; Standard LB is public iff it has a publicIPAddress
	// reference on any frontend IP configuration.
	subtype, _ := properties["subtype"].(string)
	if strings.EqualFold(subtype, "applicationgateway") || strings.EqualFold(subtype, "applicationgateways") {
		properties["is_public_entry"] = true
	} else if _, hasPublicRefs := properties["public_ip_refs"]; hasPublicRefs {
		properties["is_public_entry"] = true
	}
}

// extractAzureLBFrontendIPs reads frontendIPConfigurations[].properties.privateIPAddress
// and publicIPAddress.id from an Azure LB / App Gateway, surfacing ip_address and public_ip_refs.
func (s *AzureSource) extractAzureLBFrontendIPs(properties map[string]interface{}, metaMap map[string]interface{}) {
	frontends, ok := metaMap["frontendIPConfigurations"].([]interface{})
	if !ok {
		return
	}
	var ips []interface{}
	var publicRefs []interface{}
	for _, f := range frontends {
		fProps := nestedProps(f)
		if fProps == nil {
			continue
		}
		if priv, ok := fProps["privateIPAddress"].(string); ok && priv != "" {
			ips = append(ips, priv)
		}
		if pub, ok := fProps["publicIPAddress"].(map[string]interface{}); ok {
			if id, ok := pub["id"].(string); ok && id != "" {
				publicRefs = append(publicRefs, id)
			}
		}
	}
	if len(ips) > 0 {
		properties["frontend_ip_addresses"] = ips
		if _, set := properties["ip_address"]; !set {
			properties["ip_address"] = ips[0]
		}
	}
	if len(publicRefs) > 0 {
		properties["public_ip_refs"] = publicRefs
	}
}

// extractAzureLBBackendPools reads backendAddressPools[] from an Azure LB / App Gateway,
// surfacing backend_pool_ids (always) and backend_addresses (App Gateway only).
func (s *AzureSource) extractAzureLBBackendPools(properties map[string]interface{}, metaMap map[string]interface{}) {
	pools, ok := metaMap["backendAddressPools"].([]interface{})
	if !ok {
		return
	}
	var poolIDs []interface{}
	var backendAddrs []interface{}
	for _, p := range pools {
		pMap, ok := p.(map[string]interface{})
		if !ok {
			continue
		}
		if id, ok := pMap["id"].(string); ok && id != "" {
			poolIDs = append(poolIDs, id)
		}
		pProps, _ := pMap["properties"].(map[string]interface{})
		if pProps == nil {
			continue
		}
		// Application Gateway shape: properties.backendAddresses[].fqdn|ipAddress
		if addrs, ok := pProps["backendAddresses"].([]interface{}); ok {
			backendAddrs = append(backendAddrs, collectAzureBackendAddresses(addrs)...)
		}
	}
	if len(poolIDs) > 0 {
		properties["backend_pool_ids"] = poolIDs
	}
	if len(backendAddrs) > 0 {
		properties["backend_addresses"] = backendAddrs
	}
}

// extractAzureLBHTTPListenerHostnames reads httpListeners[].properties.hostName (App Gateway only)
// and surfaces them as frontend_hostnames.
func (s *AzureSource) extractAzureLBHTTPListenerHostnames(properties map[string]interface{}, metaMap map[string]interface{}) {
	listeners, ok := metaMap["httpListeners"].([]interface{})
	if !ok {
		return
	}
	var hostnames []interface{}
	for _, l := range listeners {
		lProps := nestedProps(l)
		if lProps == nil {
			continue
		}
		if h, ok := lProps["hostName"].(string); ok && h != "" {
			hostnames = append(hostnames, h)
		}
	}
	if len(hostnames) > 0 {
		properties["frontend_hostnames"] = hostnames
	}
}

// nestedProps unwraps a map element's "properties" sub-map (Azure Resource Graph nested format),
// or returns the element itself if it's already a flat map. Returns nil if the element is not a map.
func nestedProps(v interface{}) map[string]interface{} {
	m, ok := v.(map[string]interface{})
	if !ok {
		return nil
	}
	if inner, ok := m["properties"].(map[string]interface{}); ok {
		return inner
	}
	return m
}

// collectAzureBackendAddresses extracts ipAddress and fqdn values from an Application Gateway
// backendAddresses[] array.
func collectAzureBackendAddresses(addrs []interface{}) []interface{} {
	var out []interface{}
	for _, a := range addrs {
		aMap, ok := a.(map[string]interface{})
		if !ok {
			continue
		}
		if v, ok := aMap["ipAddress"].(string); ok && v != "" {
			out = append(out, v)
		}
		if v, ok := aMap["fqdn"].(string); ok && v != "" {
			out = append(out, v)
		}
	}
	return out
}
