package gcloud

import (
	"fmt"
	"nudgebee/collector/cloud/providers"
	"strings"
	"time"

	compute "cloud.google.com/go/compute/apiv1"
	"cloud.google.com/go/compute/apiv1/computepb"
	"google.golang.org/api/iterator"
)

// isGCPComputeRegion reports whether name looks like a real GCP Compute Engine
// region (e.g. "us-central1", "europe-west1"). Multi-regional storage aliases
// such as "us", "eu", "asia", "nam4", "eur4" are not accepted by the Compute
// regional APIs and would 400 with "Unknown region" — those return false here.
func isGCPComputeRegion(name string) bool {
	return strings.Contains(name, "-")
}

const (
	ServiceNameCloudLoadBalancing = "Cloud Load Balancing"
)

type cloudLoadBalancingService struct{}

func (s *cloudLoadBalancingService) GetResources(ctx providers.CloudProviderContext, account providers.Account, region string) ([]providers.Resource, error) {
	session, err := getGcloudSessionFromAccount(ctx, account)
	if err != nil {
		return nil, fmt.Errorf("failed to get gcloud session: %w", err)
	}

	resources := []providers.Resource{}

	logLBError := func(msg string, err error, keyvals ...interface{}) {
		RecordGCPPermissionError(ctx, err)
		args := append([]interface{}{"error", err}, keyvals...)
		if isGCPPermissionOrNotFoundError(err) {
			ctx.GetLogger().Warn("skipping "+msg+" — API disabled or permission denied", args...)
		} else {
			ctx.GetLogger().Error("failed to list "+msg, args...)
		}
	}

	// Global resources (when region is "global" or empty)
	if region == "global" || region == "" {
		globalFRs, err := s.listGlobalForwardingRules(ctx, session)
		if err != nil {
			logLBError("global forwarding rules", err)
		} else {
			resources = append(resources, globalFRs...)
		}

		globalBSs, err := s.listGlobalBackendServices(ctx, session)
		if err != nil {
			logLBError("global backend services", err)
		} else {
			resources = append(resources, globalBSs...)
		}

		globalHCs, err := s.listGlobalHealthChecks(ctx, session)
		if err != nil {
			logLBError("global health checks", err)
		} else {
			resources = append(resources, globalHCs...)
		}

		globalURLMaps, err := s.listGlobalUrlMaps(ctx, session)
		if err != nil {
			logLBError("global URL maps", err)
		} else {
			resources = append(resources, globalURLMaps...)
		}

		globalHTTPProxies, err := s.listGlobalTargetHttpProxies(ctx, session)
		if err != nil {
			logLBError("global target HTTP proxies", err)
		} else {
			resources = append(resources, globalHTTPProxies...)
		}

		globalHTTPSProxies, err := s.listGlobalTargetHttpsProxies(ctx, session)
		if err != nil {
			logLBError("global target HTTPS proxies", err)
		} else {
			resources = append(resources, globalHTTPSProxies...)
		}
	}

	// Regional resources (when region is specified).
	if region != "global" && region != "" {
		// Skip GCS-style multi-regional aliases (e.g. "eu", "us", "asia") — these
		// reach this loop because resource regions get cached in the account's
		// regions list, but Compute Engine regional APIs reject them with a 400
		// "Unknown region".
		if !isGCPComputeRegion(region) {
			ctx.GetLogger().Debug("skipping non-compute region for load balancing", "region", region)
			return resources, nil
		}

		regionalFRs, err := s.listRegionalForwardingRules(ctx, session, region)
		if err != nil {
			logLBError("regional forwarding rules", err, "region", region)
		} else {
			resources = append(resources, regionalFRs...)
		}

		regionalBSs, err := s.listRegionalBackendServices(ctx, session, region)
		if err != nil {
			logLBError("regional backend services", err, "region", region)
		} else {
			resources = append(resources, regionalBSs...)
		}

		regionalHCs, err := s.listRegionalHealthChecks(ctx, session, region)
		if err != nil {
			logLBError("regional health checks", err, "region", region)
		} else {
			resources = append(resources, regionalHCs...)
		}

		regionalURLMaps, err := s.listRegionalUrlMaps(ctx, session, region)
		if err != nil {
			logLBError("regional URL maps", err, "region", region)
		} else {
			resources = append(resources, regionalURLMaps...)
		}

		regionalHTTPProxies, err := s.listRegionalTargetHttpProxies(ctx, session, region)
		if err != nil {
			logLBError("regional target HTTP proxies", err, "region", region)
		} else {
			resources = append(resources, regionalHTTPProxies...)
		}

		regionalHTTPSProxies, err := s.listRegionalTargetHttpsProxies(ctx, session, region)
		if err != nil {
			logLBError("regional target HTTPS proxies", err, "region", region)
		} else {
			resources = append(resources, regionalHTTPSProxies...)
		}

		// Target Pools (legacy Network Load Balancers)
		targetPools, err := s.listTargetPools(ctx, session, region)
		if err != nil {
			logLBError("target pools", err, "region", region)
		} else {
			resources = append(resources, targetPools...)
		}
	}

	return resources, nil
}

// parseCreationTimestamp parses GCP's creation timestamp string to time.Time
func parseCreationTimestamp(timestamp string) time.Time {
	if timestamp == "" {
		return time.Now()
	}
	parsed, err := time.Parse(time.RFC3339, timestamp)
	if err != nil {
		return time.Now()
	}
	return parsed
}

// Forwarding Rules

func (s *cloudLoadBalancingService) listGlobalForwardingRules(ctx providers.CloudProviderContext, session gcloudAuthSession) ([]providers.Resource, error) {
	client, err := compute.NewGlobalForwardingRulesRESTClient(ctx.GetContext(), session.Opts...)
	if err != nil {
		return nil, fmt.Errorf("failed to create global forwarding rules client: %w", err)
	}
	defer func() {
		if cerr := client.Close(); cerr != nil {
			ctx.GetLogger().Error("failed to close global forwarding rules client", "error", cerr)
		}
	}()

	resources := []providers.Resource{}
	req := &computepb.ListGlobalForwardingRulesRequest{
		Project: session.ProjectId,
	}

	it := client.List(ctx.GetContext(), req)
	for {
		rule, err := it.Next()
		if err == iterator.Done {
			break
		}
		if err != nil {
			return resources, fmt.Errorf("failed to list global forwarding rules: %w", err)
		}

		resources = append(resources, s.forwardingRuleToResource(session.ProjectId, "global", rule))
	}

	return resources, nil
}

func (s *cloudLoadBalancingService) listRegionalForwardingRules(ctx providers.CloudProviderContext, session gcloudAuthSession, region string) ([]providers.Resource, error) {
	client, err := compute.NewForwardingRulesRESTClient(ctx.GetContext(), session.Opts...)
	if err != nil {
		return nil, fmt.Errorf("failed to create regional forwarding rules client: %w", err)
	}
	defer func() {
		if cerr := client.Close(); cerr != nil {
			ctx.GetLogger().Error("failed to close regional forwarding rules client", "error", cerr)
		}
	}()

	resources := []providers.Resource{}
	req := &computepb.ListForwardingRulesRequest{
		Project: session.ProjectId,
		Region:  region,
	}

	it := client.List(ctx.GetContext(), req)
	for {
		rule, err := it.Next()
		if err == iterator.Done {
			break
		}
		if err != nil {
			return resources, fmt.Errorf("failed to list regional forwarding rules: %w", err)
		}

		resources = append(resources, s.forwardingRuleToResource(session.ProjectId, region, rule))
	}

	return resources, nil
}

func (s *cloudLoadBalancingService) forwardingRuleToResource(projectID, region string, rule *computepb.ForwardingRule) providers.Resource {
	meta := map[string]any{}

	if rule.GetDescription() != "" {
		meta["description"] = rule.GetDescription()
	}
	if rule.GetIPAddress() != "" {
		meta["ip_address"] = rule.GetIPAddress()
	}
	if rule.GetIPProtocol() != "" {
		meta["ip_protocol"] = rule.GetIPProtocol()
	}
	if rule.GetPortRange() != "" {
		meta["port_range"] = rule.GetPortRange()
	}
	if len(rule.GetPorts()) > 0 {
		meta["ports"] = rule.GetPorts()
	}
	if rule.GetTarget() != "" {
		meta["target"] = rule.GetTarget()
	}
	if rule.GetBackendService() != "" {
		meta["backend_service"] = rule.GetBackendService()
	}
	if rule.GetLoadBalancingScheme() != "" {
		meta["load_balancing_scheme"] = rule.GetLoadBalancingScheme()
	}
	if rule.GetNetwork() != "" {
		meta["network"] = rule.GetNetwork()
	}
	if rule.GetSubnetwork() != "" {
		meta["subnetwork"] = rule.GetSubnetwork()
	}
	if rule.GetNetworkTier() != "" {
		meta["network_tier"] = rule.GetNetworkTier()
	}

	tags := convertLabelsToTags(rule.GetLabels())

	scope := "global"
	if region != "global" {
		scope = fmt.Sprintf("regions/%s", region)
	}

	return providers.Resource{
		Id:          fmt.Sprintf("%s/%s/forwardingRules/%s", projectID, scope, rule.GetName()),
		Name:        rule.GetName(),
		Type:        "forwarding-rule",
		Arn:         rule.GetSelfLink(),
		Region:      region,
		ServiceName: ServiceNameCloudLoadBalancing,
		Status:      providers.ResourceStatusActive,
		Tags:        tags,
		Meta:        meta,
		CreatedAt:   parseCreationTimestamp(rule.GetCreationTimestamp()),
	}
}

// Backend Services

func (s *cloudLoadBalancingService) listGlobalBackendServices(ctx providers.CloudProviderContext, session gcloudAuthSession) ([]providers.Resource, error) {
	client, err := compute.NewBackendServicesRESTClient(ctx.GetContext(), session.Opts...)
	if err != nil {
		return nil, fmt.Errorf("failed to create global backend services client: %w", err)
	}
	defer func() {
		if cerr := client.Close(); cerr != nil {
			ctx.GetLogger().Error("failed to close global backend services client", "error", cerr)
		}
	}()

	resources := []providers.Resource{}
	req := &computepb.ListBackendServicesRequest{
		Project: session.ProjectId,
	}

	it := client.List(ctx.GetContext(), req)
	for {
		bs, err := it.Next()
		if err == iterator.Done {
			break
		}
		if err != nil {
			return resources, fmt.Errorf("failed to list global backend services: %w", err)
		}

		resources = append(resources, s.backendServiceToResource(session.ProjectId, "global", bs))
	}

	return resources, nil
}

func (s *cloudLoadBalancingService) listRegionalBackendServices(ctx providers.CloudProviderContext, session gcloudAuthSession, region string) ([]providers.Resource, error) {
	client, err := compute.NewRegionBackendServicesRESTClient(ctx.GetContext(), session.Opts...)
	if err != nil {
		return nil, fmt.Errorf("failed to create regional backend services client: %w", err)
	}
	defer func() {
		if cerr := client.Close(); cerr != nil {
			ctx.GetLogger().Error("failed to close regional backend services client", "error", cerr)
		}
	}()

	resources := []providers.Resource{}
	req := &computepb.ListRegionBackendServicesRequest{
		Project: session.ProjectId,
		Region:  region,
	}

	it := client.List(ctx.GetContext(), req)
	for {
		bs, err := it.Next()
		if err == iterator.Done {
			break
		}
		if err != nil {
			return resources, fmt.Errorf("failed to list regional backend services: %w", err)
		}

		resources = append(resources, s.backendServiceToResource(session.ProjectId, region, bs))
	}

	return resources, nil
}

func (s *cloudLoadBalancingService) backendServiceToResource(projectID, region string, bs *computepb.BackendService) providers.Resource {
	meta := map[string]any{}

	if bs.GetDescription() != "" {
		meta["description"] = bs.GetDescription()
	}
	if bs.GetProtocol() != "" {
		meta["protocol"] = bs.GetProtocol()
	}
	if bs.GetPort() != 0 {
		meta["port"] = bs.GetPort()
	}
	if bs.GetPortName() != "" {
		meta["port_name"] = bs.GetPortName()
	}
	if bs.GetTimeoutSec() != 0 {
		meta["timeout_sec"] = bs.GetTimeoutSec()
	}
	if bs.GetLoadBalancingScheme() != "" {
		meta["load_balancing_scheme"] = bs.GetLoadBalancingScheme()
	}
	if len(bs.GetHealthChecks()) > 0 {
		meta["health_checks"] = bs.GetHealthChecks()
	}
	if bs.GetSessionAffinity() != "" {
		meta["session_affinity"] = bs.GetSessionAffinity()
	}
	if bs.GetConnectionDraining() != nil {
		meta["connection_draining_timeout_sec"] = bs.GetConnectionDraining().GetDrainingTimeoutSec()
	}

	// Convert backends to a simpler structure
	if len(bs.GetBackends()) > 0 {
		backends := make([]map[string]any, 0, len(bs.GetBackends()))
		for _, backend := range bs.GetBackends() {
			b := map[string]any{
				"group": backend.GetGroup(),
			}
			if backend.GetBalancingMode() != "" {
				b["balancing_mode"] = backend.GetBalancingMode()
			}
			if backend.GetMaxUtilization() != 0 {
				b["max_utilization"] = backend.GetMaxUtilization()
			}
			if backend.GetCapacityScaler() != 0 {
				b["capacity_scaler"] = backend.GetCapacityScaler()
			}
			backends = append(backends, b)
		}
		meta["backends"] = backends
	}

	scope := "global"
	if region != "global" {
		scope = fmt.Sprintf("regions/%s", region)
	}

	return providers.Resource{
		Id:          fmt.Sprintf("%s/%s/backendServices/%s", projectID, scope, bs.GetName()),
		Name:        bs.GetName(),
		Type:        "backend-service",
		Arn:         bs.GetSelfLink(),
		Region:      region,
		ServiceName: ServiceNameCloudLoadBalancing,
		Status:      providers.ResourceStatusActive,
		Tags:        map[string][]string{},
		Meta:        meta,
		CreatedAt:   parseCreationTimestamp(bs.GetCreationTimestamp()),
	}
}

// Health Checks

func (s *cloudLoadBalancingService) listGlobalHealthChecks(ctx providers.CloudProviderContext, session gcloudAuthSession) ([]providers.Resource, error) {
	client, err := compute.NewHealthChecksRESTClient(ctx.GetContext(), session.Opts...)
	if err != nil {
		return nil, fmt.Errorf("failed to create global health checks client: %w", err)
	}
	defer func() {
		if cerr := client.Close(); cerr != nil {
			ctx.GetLogger().Error("failed to close global health checks client", "error", cerr)
		}
	}()

	resources := []providers.Resource{}
	req := &computepb.ListHealthChecksRequest{
		Project: session.ProjectId,
	}

	it := client.List(ctx.GetContext(), req)
	for {
		hc, err := it.Next()
		if err == iterator.Done {
			break
		}
		if err != nil {
			return resources, fmt.Errorf("failed to list global health checks: %w", err)
		}

		resources = append(resources, s.healthCheckToResource(session.ProjectId, "global", hc))
	}

	return resources, nil
}

func (s *cloudLoadBalancingService) listRegionalHealthChecks(ctx providers.CloudProviderContext, session gcloudAuthSession, region string) ([]providers.Resource, error) {
	client, err := compute.NewRegionHealthChecksRESTClient(ctx.GetContext(), session.Opts...)
	if err != nil {
		return nil, fmt.Errorf("failed to create regional health checks client: %w", err)
	}
	defer func() {
		if cerr := client.Close(); cerr != nil {
			ctx.GetLogger().Error("failed to close regional health checks client", "error", cerr)
		}
	}()

	resources := []providers.Resource{}
	req := &computepb.ListRegionHealthChecksRequest{
		Project: session.ProjectId,
		Region:  region,
	}

	it := client.List(ctx.GetContext(), req)
	for {
		hc, err := it.Next()
		if err == iterator.Done {
			break
		}
		if err != nil {
			return resources, fmt.Errorf("failed to list regional health checks: %w", err)
		}

		resources = append(resources, s.healthCheckToResource(session.ProjectId, region, hc))
	}

	return resources, nil
}

func (s *cloudLoadBalancingService) healthCheckToResource(projectID, region string, hc *computepb.HealthCheck) providers.Resource {
	meta := map[string]any{}

	if hc.GetDescription() != "" {
		meta["description"] = hc.GetDescription()
	}
	if hc.GetType() != "" {
		meta["type"] = hc.GetType()
	}
	if hc.GetCheckIntervalSec() != 0 {
		meta["check_interval_sec"] = hc.GetCheckIntervalSec()
	}
	if hc.GetTimeoutSec() != 0 {
		meta["timeout_sec"] = hc.GetTimeoutSec()
	}
	if hc.GetHealthyThreshold() != 0 {
		meta["healthy_threshold"] = hc.GetHealthyThreshold()
	}
	if hc.GetUnhealthyThreshold() != 0 {
		meta["unhealthy_threshold"] = hc.GetUnhealthyThreshold()
	}

	// HTTP health check config
	if httpHC := hc.GetHttpHealthCheck(); httpHC != nil {
		meta["http_health_check"] = map[string]any{
			"port":         httpHC.GetPort(),
			"request_path": httpHC.GetRequestPath(),
		}
	}

	// HTTPS health check config
	if httpsHC := hc.GetHttpsHealthCheck(); httpsHC != nil {
		meta["https_health_check"] = map[string]any{
			"port":         httpsHC.GetPort(),
			"request_path": httpsHC.GetRequestPath(),
		}
	}

	// TCP health check config
	if tcpHC := hc.GetTcpHealthCheck(); tcpHC != nil {
		meta["tcp_health_check"] = map[string]any{
			"port": tcpHC.GetPort(),
		}
	}

	// SSL health check config
	if sslHC := hc.GetSslHealthCheck(); sslHC != nil {
		meta["ssl_health_check"] = map[string]any{
			"port": sslHC.GetPort(),
		}
	}

	// GRPC health check config
	if grpcHC := hc.GetGrpcHealthCheck(); grpcHC != nil {
		meta["grpc_health_check"] = map[string]any{
			"port":              grpcHC.GetPort(),
			"grpc_service_name": grpcHC.GetGrpcServiceName(),
		}
	}

	// HTTP2 health check config
	if http2HC := hc.GetHttp2HealthCheck(); http2HC != nil {
		meta["http2_health_check"] = map[string]any{
			"port":         http2HC.GetPort(),
			"request_path": http2HC.GetRequestPath(),
		}
	}

	scope := "global"
	if region != "global" {
		scope = fmt.Sprintf("regions/%s", region)
	}

	return providers.Resource{
		Id:          fmt.Sprintf("%s/%s/healthChecks/%s", projectID, scope, hc.GetName()),
		Name:        hc.GetName(),
		Type:        "health-check",
		Arn:         hc.GetSelfLink(),
		Region:      region,
		ServiceName: ServiceNameCloudLoadBalancing,
		Status:      providers.ResourceStatusActive,
		Tags:        map[string][]string{},
		Meta:        meta,
		CreatedAt:   parseCreationTimestamp(hc.GetCreationTimestamp()),
	}
}

// URL Maps

func (s *cloudLoadBalancingService) listGlobalUrlMaps(ctx providers.CloudProviderContext, session gcloudAuthSession) ([]providers.Resource, error) {
	client, err := compute.NewUrlMapsRESTClient(ctx.GetContext(), session.Opts...)
	if err != nil {
		return nil, fmt.Errorf("failed to create global URL maps client: %w", err)
	}
	defer func() {
		if cerr := client.Close(); cerr != nil {
			ctx.GetLogger().Error("failed to close global URL maps client", "error", cerr)
		}
	}()

	resources := []providers.Resource{}
	req := &computepb.ListUrlMapsRequest{
		Project: session.ProjectId,
	}

	it := client.List(ctx.GetContext(), req)
	for {
		urlMap, err := it.Next()
		if err == iterator.Done {
			break
		}
		if err != nil {
			return resources, fmt.Errorf("failed to list global URL maps: %w", err)
		}

		resources = append(resources, s.urlMapToResource(session.ProjectId, "global", urlMap))
	}

	return resources, nil
}

func (s *cloudLoadBalancingService) listRegionalUrlMaps(ctx providers.CloudProviderContext, session gcloudAuthSession, region string) ([]providers.Resource, error) {
	client, err := compute.NewRegionUrlMapsRESTClient(ctx.GetContext(), session.Opts...)
	if err != nil {
		return nil, fmt.Errorf("failed to create regional URL maps client: %w", err)
	}
	defer func() {
		if cerr := client.Close(); cerr != nil {
			ctx.GetLogger().Error("failed to close regional URL maps client", "error", cerr)
		}
	}()

	resources := []providers.Resource{}
	req := &computepb.ListRegionUrlMapsRequest{
		Project: session.ProjectId,
		Region:  region,
	}

	it := client.List(ctx.GetContext(), req)
	for {
		urlMap, err := it.Next()
		if err == iterator.Done {
			break
		}
		if err != nil {
			return resources, fmt.Errorf("failed to list regional URL maps: %w", err)
		}

		resources = append(resources, s.urlMapToResource(session.ProjectId, region, urlMap))
	}

	return resources, nil
}

func (s *cloudLoadBalancingService) urlMapToResource(projectID, region string, urlMap *computepb.UrlMap) providers.Resource {
	meta := map[string]any{}

	if urlMap.GetDescription() != "" {
		meta["description"] = urlMap.GetDescription()
	}
	if urlMap.GetDefaultService() != "" {
		meta["default_service"] = urlMap.GetDefaultService()
	}
	if len(urlMap.GetHostRules()) > 0 {
		meta["host_rules_count"] = len(urlMap.GetHostRules())
	}
	if len(urlMap.GetPathMatchers()) > 0 {
		meta["path_matchers_count"] = len(urlMap.GetPathMatchers())
	}

	scope := "global"
	if region != "global" {
		scope = fmt.Sprintf("regions/%s", region)
	}

	return providers.Resource{
		Id:          fmt.Sprintf("%s/%s/urlMaps/%s", projectID, scope, urlMap.GetName()),
		Name:        urlMap.GetName(),
		Type:        "url-map",
		Arn:         urlMap.GetSelfLink(),
		Region:      region,
		ServiceName: ServiceNameCloudLoadBalancing,
		Status:      providers.ResourceStatusActive,
		Tags:        map[string][]string{},
		Meta:        meta,
		CreatedAt:   parseCreationTimestamp(urlMap.GetCreationTimestamp()),
	}
}

// Target HTTP Proxies

func (s *cloudLoadBalancingService) listGlobalTargetHttpProxies(ctx providers.CloudProviderContext, session gcloudAuthSession) ([]providers.Resource, error) {
	client, err := compute.NewTargetHttpProxiesRESTClient(ctx.GetContext(), session.Opts...)
	if err != nil {
		return nil, fmt.Errorf("failed to create global target HTTP proxies client: %w", err)
	}
	defer func() {
		if cerr := client.Close(); cerr != nil {
			ctx.GetLogger().Error("failed to close global target HTTP proxies client", "error", cerr)
		}
	}()

	resources := []providers.Resource{}
	req := &computepb.ListTargetHttpProxiesRequest{
		Project: session.ProjectId,
	}

	it := client.List(ctx.GetContext(), req)
	for {
		proxy, err := it.Next()
		if err == iterator.Done {
			break
		}
		if err != nil {
			return resources, fmt.Errorf("failed to list global target HTTP proxies: %w", err)
		}

		resources = append(resources, s.targetHttpProxyToResource(session.ProjectId, "global", proxy))
	}

	return resources, nil
}

func (s *cloudLoadBalancingService) listRegionalTargetHttpProxies(ctx providers.CloudProviderContext, session gcloudAuthSession, region string) ([]providers.Resource, error) {
	client, err := compute.NewRegionTargetHttpProxiesRESTClient(ctx.GetContext(), session.Opts...)
	if err != nil {
		return nil, fmt.Errorf("failed to create regional target HTTP proxies client: %w", err)
	}
	defer func() {
		if cerr := client.Close(); cerr != nil {
			ctx.GetLogger().Error("failed to close regional target HTTP proxies client", "error", cerr)
		}
	}()

	resources := []providers.Resource{}
	req := &computepb.ListRegionTargetHttpProxiesRequest{
		Project: session.ProjectId,
		Region:  region,
	}

	it := client.List(ctx.GetContext(), req)
	for {
		proxy, err := it.Next()
		if err == iterator.Done {
			break
		}
		if err != nil {
			return resources, fmt.Errorf("failed to list regional target HTTP proxies: %w", err)
		}

		resources = append(resources, s.targetHttpProxyToResource(session.ProjectId, region, proxy))
	}

	return resources, nil
}

func (s *cloudLoadBalancingService) targetHttpProxyToResource(projectID, region string, proxy *computepb.TargetHttpProxy) providers.Resource {
	meta := map[string]any{}

	if proxy.GetDescription() != "" {
		meta["description"] = proxy.GetDescription()
	}
	if proxy.GetUrlMap() != "" {
		meta["url_map"] = proxy.GetUrlMap()
	}

	scope := "global"
	if region != "global" {
		scope = fmt.Sprintf("regions/%s", region)
	}

	return providers.Resource{
		Id:          fmt.Sprintf("%s/%s/targetHttpProxies/%s", projectID, scope, proxy.GetName()),
		Name:        proxy.GetName(),
		Type:        "target-http-proxy",
		Arn:         proxy.GetSelfLink(),
		Region:      region,
		ServiceName: ServiceNameCloudLoadBalancing,
		Status:      providers.ResourceStatusActive,
		Tags:        map[string][]string{},
		Meta:        meta,
		CreatedAt:   parseCreationTimestamp(proxy.GetCreationTimestamp()),
	}
}

// Target HTTPS Proxies

func (s *cloudLoadBalancingService) listGlobalTargetHttpsProxies(ctx providers.CloudProviderContext, session gcloudAuthSession) ([]providers.Resource, error) {
	client, err := compute.NewTargetHttpsProxiesRESTClient(ctx.GetContext(), session.Opts...)
	if err != nil {
		return nil, fmt.Errorf("failed to create global target HTTPS proxies client: %w", err)
	}
	defer func() {
		if cerr := client.Close(); cerr != nil {
			ctx.GetLogger().Error("failed to close global target HTTPS proxies client", "error", cerr)
		}
	}()

	resources := []providers.Resource{}
	req := &computepb.ListTargetHttpsProxiesRequest{
		Project: session.ProjectId,
	}

	it := client.List(ctx.GetContext(), req)
	for {
		proxy, err := it.Next()
		if err == iterator.Done {
			break
		}
		if err != nil {
			return resources, fmt.Errorf("failed to list global target HTTPS proxies: %w", err)
		}

		resources = append(resources, s.targetHttpsProxyToResource(session.ProjectId, "global", proxy))
	}

	return resources, nil
}

func (s *cloudLoadBalancingService) listRegionalTargetHttpsProxies(ctx providers.CloudProviderContext, session gcloudAuthSession, region string) ([]providers.Resource, error) {
	client, err := compute.NewRegionTargetHttpsProxiesRESTClient(ctx.GetContext(), session.Opts...)
	if err != nil {
		return nil, fmt.Errorf("failed to create regional target HTTPS proxies client: %w", err)
	}
	defer func() {
		if cerr := client.Close(); cerr != nil {
			ctx.GetLogger().Error("failed to close regional target HTTPS proxies client", "error", cerr)
		}
	}()

	resources := []providers.Resource{}
	req := &computepb.ListRegionTargetHttpsProxiesRequest{
		Project: session.ProjectId,
		Region:  region,
	}

	it := client.List(ctx.GetContext(), req)
	for {
		proxy, err := it.Next()
		if err == iterator.Done {
			break
		}
		if err != nil {
			return resources, fmt.Errorf("failed to list regional target HTTPS proxies: %w", err)
		}

		resources = append(resources, s.targetHttpsProxyToResource(session.ProjectId, region, proxy))
	}

	return resources, nil
}

func (s *cloudLoadBalancingService) targetHttpsProxyToResource(projectID, region string, proxy *computepb.TargetHttpsProxy) providers.Resource {
	meta := map[string]any{}

	if proxy.GetDescription() != "" {
		meta["description"] = proxy.GetDescription()
	}
	if proxy.GetUrlMap() != "" {
		meta["url_map"] = proxy.GetUrlMap()
	}
	if len(proxy.GetSslCertificates()) > 0 {
		meta["ssl_certificates"] = proxy.GetSslCertificates()
	}
	if proxy.GetSslPolicy() != "" {
		meta["ssl_policy"] = proxy.GetSslPolicy()
	}

	scope := "global"
	if region != "global" {
		scope = fmt.Sprintf("regions/%s", region)
	}

	return providers.Resource{
		Id:          fmt.Sprintf("%s/%s/targetHttpsProxies/%s", projectID, scope, proxy.GetName()),
		Name:        proxy.GetName(),
		Type:        "target-https-proxy",
		Arn:         proxy.GetSelfLink(),
		Region:      region,
		ServiceName: ServiceNameCloudLoadBalancing,
		Status:      providers.ResourceStatusActive,
		Tags:        map[string][]string{},
		Meta:        meta,
		CreatedAt:   parseCreationTimestamp(proxy.GetCreationTimestamp()),
	}
}

// Target Pools (Legacy Network Load Balancers)

func (s *cloudLoadBalancingService) listTargetPools(ctx providers.CloudProviderContext, session gcloudAuthSession, region string) ([]providers.Resource, error) {
	client, err := compute.NewTargetPoolsRESTClient(ctx.GetContext(), session.Opts...)
	if err != nil {
		return nil, fmt.Errorf("failed to create target pools client: %w", err)
	}
	defer func() {
		if cerr := client.Close(); cerr != nil {
			ctx.GetLogger().Error("failed to close target pools client", "error", cerr)
		}
	}()

	resources := []providers.Resource{}
	req := &computepb.ListTargetPoolsRequest{
		Project: session.ProjectId,
		Region:  region,
	}

	it := client.List(ctx.GetContext(), req)
	for {
		pool, err := it.Next()
		if err == iterator.Done {
			break
		}
		if err != nil {
			return resources, fmt.Errorf("failed to list target pools: %w", err)
		}

		resources = append(resources, s.targetPoolToResource(session.ProjectId, region, pool))
	}

	return resources, nil
}

func (s *cloudLoadBalancingService) targetPoolToResource(projectID, region string, pool *computepb.TargetPool) providers.Resource {
	meta := map[string]any{}

	if pool.GetDescription() != "" {
		meta["description"] = pool.GetDescription()
	}
	if pool.GetSessionAffinity() != "" {
		meta["session_affinity"] = pool.GetSessionAffinity()
	}
	if pool.GetFailoverRatio() != 0 {
		meta["failover_ratio"] = pool.GetFailoverRatio()
	}
	if pool.GetBackupPool() != "" {
		meta["backup_pool"] = pool.GetBackupPool()
	}
	if len(pool.GetHealthChecks()) > 0 {
		meta["health_checks"] = pool.GetHealthChecks()
	}
	if len(pool.GetInstances()) > 0 {
		meta["instances"] = pool.GetInstances()
		meta["instance_count"] = len(pool.GetInstances())
	}

	return providers.Resource{
		Id:          fmt.Sprintf("%s/regions/%s/targetPools/%s", projectID, region, pool.GetName()),
		Name:        pool.GetName(),
		Type:        "target-pool",
		Arn:         pool.GetSelfLink(),
		Region:      region,
		ServiceName: ServiceNameCloudLoadBalancing,
		Status:      providers.ResourceStatusActive,
		Tags:        map[string][]string{},
		Meta:        meta,
		CreatedAt:   parseCreationTimestamp(pool.GetCreationTimestamp()),
	}
}

// Recommendations

func (s *cloudLoadBalancingService) GetRecommendations(ctx providers.CloudProviderContext, account providers.Account, filter providers.ListRecommendationsRequest, existingResources []providers.Resource) ([]providers.Recommendation, error) {
	recommendations := []providers.Recommendation{}

	for _, resource := range existingResources {
		switch resource.Type {
		case "forwarding-rule":
			recs := s.getForwardingRuleRecommendations(resource)
			recommendations = append(recommendations, recs...)
		case "backend-service":
			recs := s.getBackendServiceRecommendations(resource)
			recommendations = append(recommendations, recs...)
		}
	}

	return recommendations, nil
}

func (s *cloudLoadBalancingService) getForwardingRuleRecommendations(resource providers.Resource) []providers.Recommendation {
	recommendations := []providers.Recommendation{}
	meta := resource.Meta

	// Check for unused forwarding rule (no target or backend service)
	target, _ := meta["target"].(string)
	backendService, _ := meta["backend_service"].(string)

	if target == "" && backendService == "" {
		recommendations = append(recommendations, providers.Recommendation{
			CategoryName: providers.RecommendationCategoryRightSizing,
			RuleName:     "gcp_lb_unused_forwarding_rule",
			Severity:     providers.RecommendationSeverityMedium,
			Savings:      0,
			Data: map[string]any{
				"forwarding_rule_name": resource.Name,
				"ip_address":           meta["ip_address"],
				"reason":               "Forwarding rule has no target or backend service configured",
			},
			Action:              providers.RecommendationActionDelete,
			ResourceServiceName: resource.ServiceName,
			ResourceId:          resource.Id,
			ResourceType:        resource.Type,
			ResourceRegion:      resource.Region,
		})
	}

	// Check for missing labels
	if len(resource.Tags) == 0 {
		recommendations = append(recommendations, providers.Recommendation{
			CategoryName: providers.RecommendationCategoryConfiguration,
			RuleName:     "gcp_lb_no_labels",
			Severity:     providers.RecommendationSeverityLow,
			Savings:      0,
			Data: map[string]any{
				"forwarding_rule_name": resource.Name,
				"reason":               "Forwarding rule has no labels for organization and cost allocation",
			},
			Action:              providers.RecommendationActionModify,
			ResourceServiceName: resource.ServiceName,
			ResourceId:          resource.Id,
			ResourceType:        resource.Type,
			ResourceRegion:      resource.Region,
		})
	}

	return recommendations
}

func (s *cloudLoadBalancingService) getBackendServiceRecommendations(resource providers.Resource) []providers.Recommendation {
	recommendations := []providers.Recommendation{}
	meta := resource.Meta

	// Check for backend service without health checks - fixed type assertion
	var hasHealthChecks bool
	if hc := meta["health_checks"]; hc != nil {
		if slice, ok := hc.([]interface{}); ok && len(slice) > 0 {
			hasHealthChecks = true
		} else if strSlice, ok := hc.([]string); ok && len(strSlice) > 0 {
			hasHealthChecks = true
		}
	}
	if !hasHealthChecks {
		recommendations = append(recommendations, providers.Recommendation{
			CategoryName: providers.RecommendationCategoryConfiguration,
			RuleName:     "gcp_lb_backend_no_health_check",
			Severity:     providers.RecommendationSeverityHigh,
			Savings:      0,
			Data: map[string]any{
				"backend_service_name": resource.Name,
				"reason":               "Backend service has no health checks configured",
			},
			Action:              providers.RecommendationActionModify,
			ResourceServiceName: resource.ServiceName,
			ResourceId:          resource.Id,
			ResourceType:        resource.Type,
			ResourceRegion:      resource.Region,
		})
	}

	// Check for backend service with no backends - fixed type assertion
	var hasBackends bool
	if b := meta["backends"]; b != nil {
		if slice, ok := b.([]interface{}); ok && len(slice) > 0 {
			hasBackends = true
		} else if mapSlice, ok := b.([]map[string]any); ok && len(mapSlice) > 0 {
			hasBackends = true
		}
	}
	if !hasBackends {
		recommendations = append(recommendations, providers.Recommendation{
			CategoryName: providers.RecommendationCategoryRightSizing,
			RuleName:     "gcp_lb_backend_no_backends",
			Severity:     providers.RecommendationSeverityMedium,
			Savings:      0,
			Data: map[string]any{
				"backend_service_name": resource.Name,
				"reason":               "Backend service has no backend groups configured",
			},
			Action:              providers.RecommendationActionDelete,
			ResourceServiceName: resource.ServiceName,
			ResourceId:          resource.Id,
			ResourceType:        resource.Type,
			ResourceRegion:      resource.Region,
		})
	}

	return recommendations
}

func (s *cloudLoadBalancingService) GetMetrices(ctx providers.CloudProviderContext, account providers.Account, filter providers.QueryMetricsRequest) (providers.QueryMetricsResponse, error) {
	// Load balancing metrics are handled via Cloud Monitoring service
	return providers.QueryMetricsResponse{
		Items: []providers.MetricItem{},
	}, nil
}

func (s *cloudLoadBalancingService) ApplyRecommendation(ctx providers.CloudProviderContext, account providers.Account, recommendation providers.Recommendation) error {
	return fmt.Errorf("cloud load balancing service does not support applying recommendations")
}

func (s *cloudLoadBalancingService) ApplyCommand(ctx providers.CloudProviderContext, account providers.Account, command providers.ApplyCommandRequest) (providers.ApplyCommandResponse, error) {
	return providers.ApplyCommandResponse{}, fmt.Errorf("cloud load balancing service does not support commands")
}

func (s *cloudLoadBalancingService) GetLogFilter(_ providers.CloudProviderContext, _ providers.Account, resourceId string) string {
	// Could return a Cloud Logging filter for load balancer logs
	return ""
}

// convertLabelsToTags converts GCP labels (map[string]string) to the tags format (map[string][]string)
func convertLabelsToTags(labels map[string]string) map[string][]string {
	tags := make(map[string][]string)
	for k, v := range labels {
		tags[k] = []string{v}
	}
	return tags
}
