package flow_sources

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"nudgebee/services/cloud"
	"nudgebee/services/internal/database"
	"nudgebee/services/knowledge_graph/core"
	"nudgebee/services/security"
)

// VPC Flow Logs → service dependency edges.
//
// This flow source turns AWS VPC Flow Logs into persisted service-to-service
// edges in the knowledge graph. ACCEPT traffic becomes CALLS (a live
// dependency); REJECT traffic becomes CONNECTION_REJECTED (the security view of
// attempted-but-blocked reachability). It is the AWS-network sibling of the
// eBPF flow source: network-observed, IP-derived, coarser than app-level APM,
// but it uniquely sees cloud-managed edges (RDS/ElastiCache/ALB/cross-VPC) that
// in-cluster observers miss.
//
// Design (Phase 1):
//   - Queries flow logs via CloudWatch Logs Insights (through cloud.QueryLogs),
//     aggregating in-flight so raw flows never touch the graph.
//   - Parses the customer's *actual* configured LogFormat via
//     cloud.BuildVPCFlowLogsParsePattern, so custom v3+ formats (pkt-srcaddr /
//     pkt-dstaddr / flow-direction) are handled without a bespoke parser. When
//     pkt-* fields are present they are used as the effective src/dst so NAT /
//     LB rewriting is defeated (a NAT-gateway ENI's srcaddr is the gateway,
//     but pkt-srcaddr is the originating instance).
//   - Canonicalizes direction: the endpoint with the well-known / low port is
//     the server; ephemeral high ports (32768-65535) are the client, so return
//     flows don't create duplicate edges.
//   - Resolves each private IP to an existing graph node, in order: an in-memory
//     index over the nodes the AWS static source already built (ENIs and the
//     resources behind them, no AWS calls); then, for the K8s accounts mapped
//     to the AWS account, the K8s Node or Workload that held the IP during the
//     flow window, from kube_node_info / kube_pod_info (see
//     vpc_flowlogs_k8s_resolution.go). Under the VPC CNI most IPs are pods, and
//     EKS rotates nodes and ENIs faster than the ENI index can keep up.
//   - IPs still unresolved are not given a node each: they collapse into one
//     "Unidentified addresses (<vpc>)" ExternalService per VPC, with the IPs on
//     the edge, and each IP is recorded in knowledge_graph_classification_review
//     (node_match) with the reason it stayed unresolved.
//   - Because the edge table is unique on (src, dst, relationship_type, ...), a
//     distinct edge per port is impossible; instead ACCEPT/REJECT are kept as
//     separate relationship types and the per-port breakdown is carried in edge
//     properties.
//
// Known Phase-1 limitations (see plan): public/external traffic is filtered out
// (external resolution via ip-ranges/rDNS/ASN is Phase 2); resolution is against
// the current inventory, not the inventory as of when the flow happened
// (time-aware / AWS Config historical join is Phase 2) — every edge is stamped
// with the queried flow_window_start/end so this staleness risk is visible
// rather than hidden; and only CloudWatch-Logs-delivered flow logs are
// supported (no S3/Athena).
//
// Disabled by default. Enable with env KG_ENABLE_AWS_VPC_FLOW=true.

const (
	awsVPCFlowSourceName = "aws-vpc-flow"

	// Ephemeral (client) port range. Linux defaults to 32768-60999, but other
	// OSes (Windows, BSD) use ranges extending to 65535 (RFC 6335's dynamic/
	// private range is 49152-65535), so the ceiling is set to 65535 to avoid
	// misclassifying high-numbered return traffic from non-Linux hosts as a
	// service port. The endpoint using one of these is the caller; the peer's
	// low/well-known port is the server being called.
	ephemeralPortLow  = 32768
	ephemeralPortHigh = 65535

	// defaultFlowTopN caps rows per log group per query. CloudWatch Logs Insights
	// bills by data scanned, not rows returned, so raising this costs nothing
	// extra in AWS fees — it only reduces how much of the long tail (low-volume
	// connections) gets dropped when a busy VPC has more than this many distinct
	// (src, dst, port, protocol, action) tuples in one window.
	defaultFlowTopN = 2000

	// defaultFlowTimeRange matches the hourly KG build cadence so consecutive
	// runs' windows are back-to-back with no gap. A shorter window (e.g. the
	// original 10m) samples the same slice of every hour, so any traffic whose
	// schedule never overlaps that slice would never be observed no matter how
	// long the source runs; a full-hour window has no such blind spot.
	defaultFlowTimeRange = time.Hour
)

// awsVPCFlowRegionRegex validates an AWS region token before it is interpolated
// into an ExecuteCli command string (defense-in-depth against shell injection,
// mirroring the cloud_vpc_flowlogs action's validation).
var awsVPCFlowRegionRegex = regexp.MustCompile(`^[a-z]{2}-[a-z]+-\d{1,2}$`)

func init() {
	RegisterFlowSourceFactory(
		awsVPCFlowSourceName,
		func(logger *slog.Logger) (core.FlowSourceInterface, error) {
			return NewVPCFlowLogsFlowSource(logger), nil
		},
		"AWS VPC Flow Logs → service dependency edges (CALLS for ACCEPT, CONNECTION_REJECTED for REJECT)",
		string(core.FlowSourceCategoryNetworking),
	)
}

// VPCFlowLogsFlowSource builds CALLS / CONNECTION_REJECTED edges from AWS VPC Flow Logs.
type VPCFlowLogsFlowSource struct {
	*BaseFlowSource
	timeWindow time.Duration
	topN       int
}

// NewVPCFlowLogsFlowSource creates the flow source. It is enabled only when
// KG_ENABLE_AWS_VPC_FLOW=true (gradual rollout, matching the collector's
// ENABLE_MULTI_SOURCE_SERVICEMAP gate).
func NewVPCFlowLogsFlowSource(logger *slog.Logger) *VPCFlowLogsFlowSource {
	enabled := strings.EqualFold(strings.TrimSpace(os.Getenv("KG_ENABLE_AWS_VPC_FLOW")), "true")
	base := NewBaseFlowSource(awsVPCFlowSourceName, core.FlowSourceCategoryNetworking, enabled, logger)
	return &VPCFlowLogsFlowSource{
		BaseFlowSource: base,
		timeWindow:     defaultFlowTimeRange,
		topN:           defaultFlowTopN,
	}
}

// BuildFlowRelationships is the FlowSourceInterface entry point.
func (s *VPCFlowLogsFlowSource) BuildFlowRelationships(
	reqCtx *security.RequestContext,
	req *core.FlowSourceBuildRequest,
) ([]*core.DbEdge, []*core.DbNode, error) {
	startTime := TimeNow()
	defer s.TrackBuildTime(startTime)

	s.logger.Info("building flow relationships from AWS VPC Flow Logs",
		"source", s.GetName(),
		"tenant_id", req.TenantID,
		"existing_nodes", len(req.ExistingNodes))

	// The node matcher is initialized for parity with other flow sources and
	// possible future use; Phase 1 resolves via the private-IP index below,
	// which is O(1) per IP and needs no AWS calls.
	s.InitializeNodeMatcher(req.ExistingNodes)

	accounts, err := core.GetAWSAccountsForTenant(req.TenantID)
	if err != nil {
		s.IncrementErrorCount()
		return nil, nil, fmt.Errorf("failed to get AWS accounts: %w", err)
	}
	accounts = filterAccounts(accounts, req.CloudAccountIDs)
	if len(accounts) == 0 {
		s.logger.Info("no AWS accounts for tenant, nothing to do", "tenant_id", req.TenantID)
		return []*core.DbEdge{}, []*core.DbNode{}, nil
	}

	// AWS-account → mapped-K8s-accounts. Under the AWS VPC CNI, pods get real VPC
	// IPs and appear in flow logs, so an AWS account's IP space includes the K8s
	// accounts running in it. Resolving within this set (rather than globally)
	// stops a different account's colliding RFC1918 IP from winning.
	awsToK8s := invertK8sCloudAccountMapping(reqCtx, req.TenantID)
	nodeIPResolver := NewK8sNodeIPResolver(req.ExistingNodes)

	edges := make([]*core.DbEdge, 0)
	nodes := make([]*core.DbNode, 0)

	for _, accountID := range accounts {
		regions := s.discoverEnabledRegions(reqCtx, accountID)
		if len(regions) == 0 {
			// Fall back to regions inferred from existing KG nodes — keeps the
			// source working even if the describe-regions call itself fails
			// (permissions, transient AWS/network error).
			regions = regionsForAccount(req.ExistingNodes, accountID)
		}
		if len(regions) == 0 {
			s.logger.Info("no regions discovered for account, skipping", "cloud_account_id", accountID)
			continue
		}

		// Account-scoped IP index: this AWS account plus the K8s accounts that
		// share its VPC address space.
		memberAccounts := map[string]bool{accountID: true}
		for _, k8sAcct := range awsToK8s[accountID] {
			memberAccounts[k8sAcct] = true
		}
		ipIndex := buildPrivateIPIndex(req.ExistingNodes, memberAccounts)

		k8sRes := k8sFlowResolution{
			accounts:     awsToK8s[accountID],
			nodeResolver: nodeIPResolver,
			fetch:        fetchK8sIPInfo(reqCtx),
		}
		accEdges, accNodes, err := s.processAccount(reqCtx, req, accountID, regions, ipIndex, k8sRes)
		if err != nil {
			s.logger.Error("failed to process account for VPC flow logs",
				"cloud_account_id", accountID, "error", err)
			s.IncrementErrorCount()
			continue
		}
		edges = append(edges, accEdges...)
		nodes = append(nodes, accNodes...)
	}

	s.LogMetrics()
	s.logger.Info("completed building flow relationships from AWS VPC Flow Logs",
		"total_edges_created", len(edges),
		"total_nodes_created", len(nodes),
		"accounts_processed", len(accounts))

	return edges, nodes, nil
}

// flowLogConfig is a discovered CloudWatch-Logs-delivered flow log.
type flowLogConfig struct {
	LogGroup string
	Format   string
	Region   string
	// ResourceID is what the flow log is attached to: a VPC, subnet or ENI id.
	ResourceID string
}

// k8sFlowResolution carries what processAccount needs to resolve IPs through
// the K8s accounts mapped to an AWS account.
type k8sFlowResolution struct {
	accounts     []string
	nodeResolver *K8sNodeIPResolver
	fetch        k8sIPInfoFetcher
}

// processAccount discovers this account's flow logs, queries each, and turns the
// aggregated connections into edges.
func (s *VPCFlowLogsFlowSource) processAccount(
	reqCtx *security.RequestContext,
	req *core.FlowSourceBuildRequest,
	accountID string,
	regions []string,
	ipIndex map[string]*core.DbNode,
	k8sRes k8sFlowResolution,
) ([]*core.DbEdge, []*core.DbNode, error) {
	configs := s.discoverFlowLogConfigs(reqCtx, accountID, regions)
	if len(configs) == 0 {
		s.logger.Info("no CloudWatch-delivered VPC flow logs found for account",
			"cloud_account_id", accountID, "regions", regions)
		return nil, nil, nil
	}

	end := time.Now()
	start := end.Add(-s.timeWindow)

	conns := make([]flowConn, 0)
	for _, cfg := range configs {
		srcField, dstField := canonicalAddrFields(cfg.Format)
		parsePattern := cloud.BuildVPCFlowLogsParsePattern(cfg.Format)
		query := buildFlowConnQuery(parsePattern, srcField, dstField, s.topN)

		limit := int64(s.topN)
		resp, err := cloud.QueryLogs(reqCtx, cloud.QueryLogsRequest{
			AccountId: accountID,
			Query: cloud.LogQuery{
				Region:       cfg.Region,
				LogGroupName: cfg.LogGroup,
				QueryString:  query,
				StartTime:    &start,
				EndTime:      &end,
				Limit:        &limit,
			},
		})
		if err != nil {
			s.logger.Warn("VPC flow log query failed, skipping log group",
				"cloud_account_id", accountID, "log_group", cfg.LogGroup, "region", cfg.Region, "error", err)
			s.IncrementErrorCount()
			continue
		}
		scope := vpcForFlowLog(cfg.ResourceID, req.ExistingNodes, accountID)
		if scope == "" {
			scope = cfg.Region
		}
		for _, c := range parseFlowConns(resp.Results, srcField, dstField) {
			c.Scope = scope
			conns = append(conns, c)
		}
	}

	resolved, errs := resolveFlowIPsViaK8s(
		ipsMissingFromIndex(conns, ipIndex), k8sRes.accounts, req.ExistingNodes, k8sRes.nodeResolver, k8sRes.fetch, start, end)
	for _, err := range errs {
		if isAgentNotConnected(err) {
			s.logger.Debug("K8s IP resolution skipped for VPC flow logs", "cloud_account_id", accountID, "error", err)
		} else {
			s.logger.Warn("K8s IP resolution failed for VPC flow logs", "cloud_account_id", accountID, "error", err)
		}
	}

	edges, nodes, unresolved, err := s.buildEdgesFromConns(conns, ipIndex, resolved, req.TenantID, accountID, start, end)
	if err != nil {
		return nil, nil, err
	}
	s.recordUnresolvedIPs(req.TenantID, accountID, unresolved, resolved, len(k8sRes.accounts) > 0, start, end)
	return edges, nodes, nil
}

// ipsMissingFromIndex returns the distinct flow-log IPs the ENI index can't
// resolve — the only ones worth a metrics lookup.
func ipsMissingFromIndex(conns []flowConn, ipIndex map[string]*core.DbNode) []string {
	seen := make(map[string]bool)
	out := make([]string, 0)
	for _, c := range conns {
		for _, ip := range []string{c.SrcIP, c.DstIP} {
			if _, ok := ipIndex[ip]; ok || seen[ip] {
				continue
			}
			seen[ip] = true
			out = append(out, ip)
		}
	}
	sort.Strings(out)
	return out
}

// vpcForFlowLog returns the VPC a flow log covers. Flow logs attach to a VPC,
// a subnet or an ENI; for the latter two the VPC is read off the matching
// Subnet / NetworkInterface node. Empty when it can't be determined.
func vpcForFlowLog(resourceID string, nodes []*core.DbNode, accountID string) string {
	if strings.HasPrefix(resourceID, "vpc-") {
		return resourceID
	}
	if resourceID == "" {
		return ""
	}
	for _, n := range nodes {
		if n == nil || n.CloudAccountID != accountID {
			continue
		}
		if stringProp(n, "subnet_id") != resourceID && stringProp(n, "resource_id") != resourceID {
			continue
		}
		if vpc := stringProp(n, "vpc_id"); vpc != "" {
			return vpc
		}
	}
	return ""
}

// discoverEnabledRegions asks AWS directly which regions this account has
// enabled, via `aws ec2 describe-regions` — which, without --all-regions,
// returns only opted-in (and opt-in-not-required) regions. This replaces
// inferring regions solely from existing KG nodes, which misses flow logs in
// any region the account hasn't had other resources ingested from yet.
func (s *VPCFlowLogsFlowSource) discoverEnabledRegions(
	reqCtx *security.RequestContext,
	accountID string,
) []string {
	resp, err := cloud.ExecuteCli(reqCtx, cloud.CloudExecuteCliCommandRequest{
		AccountID: accountID,
		Command:   "aws ec2 describe-regions --region us-east-1 --output json",
	})
	if err != nil {
		s.logger.Warn("failed to describe regions", "cloud_account_id", accountID, "error", err)
		return nil
	}

	dataStr, ok := resp["data"].(string)
	if !ok {
		return nil
	}
	var parsed struct {
		Regions []struct {
			RegionName string `json:"RegionName"`
		} `json:"Regions"`
	}
	if err := json.Unmarshal([]byte(dataStr), &parsed); err != nil {
		s.logger.Warn("failed to parse describe-regions response", "cloud_account_id", accountID, "error", err)
		return nil
	}

	out := make([]string, 0, len(parsed.Regions))
	for _, r := range parsed.Regions {
		if awsVPCFlowRegionRegex.MatchString(r.RegionName) {
			out = append(out, r.RegionName)
		}
	}
	sort.Strings(out)
	return out
}

// discoverFlowLogConfigs finds CloudWatch-Logs-delivered flow logs for the
// account across the given regions via `aws ec2 describe-flow-logs`.
func (s *VPCFlowLogsFlowSource) discoverFlowLogConfigs(
	reqCtx *security.RequestContext,
	accountID string,
	regions []string,
) []flowLogConfig {
	seen := make(map[string]bool)
	out := make([]flowLogConfig, 0)

	for _, region := range regions {
		if !awsVPCFlowRegionRegex.MatchString(region) {
			continue
		}
		command := fmt.Sprintf("aws ec2 describe-flow-logs --region %s --output json", region)
		resp, err := cloud.ExecuteCli(reqCtx, cloud.CloudExecuteCliCommandRequest{
			AccountID: accountID,
			Command:   command,
		})
		if err != nil {
			s.logger.Warn("failed to describe flow logs", "cloud_account_id", accountID, "region", region, "error", err)
			continue
		}

		dataStr, ok := resp["data"].(string)
		if !ok {
			continue
		}
		var parsed struct {
			FlowLogs []map[string]any `json:"FlowLogs"`
		}
		if err := json.Unmarshal([]byte(dataStr), &parsed); err != nil {
			s.logger.Warn("failed to parse describe-flow-logs response", "cloud_account_id", accountID, "region", region, "error", err)
			continue
		}

		for _, fl := range parsed.FlowLogs {
			if dt, _ := fl["LogDestinationType"].(string); dt != "cloud-watch-logs" {
				continue
			}
			logGroup, _ := fl["LogGroupName"].(string)
			if logGroup == "" || seen[logGroup] {
				continue
			}
			seen[logGroup] = true
			format, _ := fl["LogFormat"].(string) // empty → default format handled by the parse-pattern builder
			resourceID, _ := fl["ResourceId"].(string)
			out = append(out, flowLogConfig{LogGroup: logGroup, Format: format, Region: region, ResourceID: resourceID})
		}
	}

	return out
}

// flowConn is one aggregated flow-log row (post GROUP BY), with the ACCEPT/REJECT
// action preserved — unlike the collector's on-demand engine, we never collapse
// action into a failure count.
type flowConn struct {
	SrcIP       string
	DstIP       string
	DstPort     int
	Protocol    int
	Action      string // ACCEPT | REJECT
	Bytes       int64
	Packets     int64
	Connections int64
	// Scope groups addresses nothing resolves: the VPC the flow log covers,
	// or its region when the VPC can't be determined.
	Scope string
}

// canonicalAddrFields picks the effective src/dst capture-group names. When the
// configured format carries pkt-srcaddr/pkt-dstaddr we key on those (NAT/LB
// de-masking); otherwise the standard srcaddr/dstaddr.
func canonicalAddrFields(logFormat string) (srcField, dstField string) {
	srcField, dstField = "srcaddr", "dstaddr"
	if strings.Contains(logFormat, "${pkt-srcaddr}") {
		srcField = "pkt_srcaddr"
	}
	if strings.Contains(logFormat, "${pkt-dstaddr}") {
		dstField = "pkt_dstaddr"
	}
	return srcField, dstField
}

// buildFlowConnQuery builds the CloudWatch Logs Insights query that aggregates
// IP-pair edges in-flight (never loading raw flows), keeping dstport AND action
// distinct. Only valid, private↔private flows are aggregated in Phase 1.
func buildFlowConnQuery(parsePattern, srcField, dstField string, topN int) string {
	var b strings.Builder
	b.WriteString("fields @timestamp, @message\n")
	fmt.Fprintf(&b, "| parse @message %s\n", parsePattern)
	b.WriteString(`| filter log_status = "OK"`)
	// Private (RFC1918) ↔ private only. Public traffic is out of Phase-1 scope
	// (external resolution is Phase 2), so filter it at query time to keep the
	// scan cheap.
	b.WriteString("\n")
	b.WriteString(privateAddrFilter(srcField))
	b.WriteString("\n")
	b.WriteString(privateAddrFilter(dstField))
	fmt.Fprintf(&b, "\n| stats sum(bytes) as total_bytes, sum(packets) as total_packets, count(*) as connection_count by %s, %s, dstport, protocol, action\n", srcField, dstField)
	fmt.Fprintf(&b, "| sort total_bytes desc\n| limit %d", topN)
	return b.String()
}

// privateAddrFilter returns a Logs Insights filter keeping only RFC1918 values
// of the given field (10/8, 172.16/12, 192.168/16). CloudWatch Insights has no
// CIDR predicate, so regex `like` is used (same approach as cloud_vpc_flowlogs).
func privateAddrFilter(field string) string {
	return fmt.Sprintf(`| filter (%s like /^10\./ or %s like /^192\.168\./ or %s like /^172\.(1[6-9]|2[0-9]|3[01])\./)`, field, field, field)
}

// parseFlowConns turns Logs Insights result rows (label/value pairs) into
// flowConn records. srcField/dstField name the effective address columns used
// in the GROUP BY.
func parseFlowConns(results []cloud.LogMessage, srcField, dstField string) []flowConn {
	conns := make([]flowConn, 0, len(results))
	for _, row := range results {
		m := make(map[string]string, len(row.Labels))
		for _, l := range row.Labels {
			m[l.Label] = l.Value
		}
		src := strings.TrimSpace(m[srcField])
		dst := strings.TrimSpace(m[dstField])
		if src == "" || dst == "" || src == "-" || dst == "-" {
			continue
		}
		conns = append(conns, flowConn{
			SrcIP:       src,
			DstIP:       dst,
			DstPort:     atoiSafe(m["dstport"]),
			Protocol:    atoiSafe(m["protocol"]),
			Action:      strings.ToUpper(strings.TrimSpace(m["action"])),
			Bytes:       atoi64Safe(m["total_bytes"]),
			Packets:     atoi64Safe(m["total_packets"]),
			Connections: atoi64Safe(m["connection_count"]),
		})
	}
	return conns
}

// isServicePort reports whether the destination port identifies the server side
// of a connection. Ephemeral high ports (32768-65535) are the client, so a flow
// whose destination is ephemeral is return traffic and is dropped — the forward
// flow (destination = the real service port) carries the dependency.
func isServicePort(port int) bool {
	if port <= 0 {
		return false
	}
	if port >= ephemeralPortLow && port <= ephemeralPortHigh {
		return false
	}
	return true
}

// portStat accumulates per-port traffic within one (src, dst, action) edge.
type portStat struct {
	Port        int
	Protocol    int
	Bytes       int64
	Packets     int64
	Connections int64
}

type edgeAggKey struct {
	srcID  string
	dstID  string
	action string
}

type edgeAggMeta struct {
	srcNode *core.DbNode
	dstNode *core.DbNode
	srcRes  string
	dstRes  string
	// IPs behind an "Unidentified addresses" endpoint, per side.
	srcUnresolved map[string]bool
	dstUnresolved map[string]bool
}

// unresolvedFlowIP is one address no lookup could place, with what the flow
// log says about it — the evidence for its review-table row.
type unresolvedFlowIP struct {
	IP          string
	Scope       string
	Peers       map[string]bool
	Ports       map[int]bool
	Bytes       int64
	Connections int64
}

// buildEdgesFromConns resolves endpoints, canonicalizes direction, aggregates
// per (src, dst, action) with a per-port breakdown, and emits CALLS (ACCEPT) /
// CONNECTION_REJECTED (REJECT) edges. Endpoints resolve through the ENI index,
// then through k8sResolved (K8s nodes/workloads, see
// vpc_flowlogs_k8s_resolution.go). The rest collapse into one "Unidentified
// addresses" node per scope and are returned for the review table, so nothing
// is silently dropped.
//
// windowStart/windowEnd are the queried log window, stamped onto every edge as
// a freshness signal: endpoints are resolved against the *current* graph, not
// the graph as of when the flow actually happened, so a resolution can be
// stale if the underlying IP has changed hands since (see the flow source's
// package comment on the time-aware-join limitation). Surfacing the window
// lets a consumer judge that risk instead of trusting every resolution alike.
func (s *VPCFlowLogsFlowSource) buildEdgesFromConns(
	conns []flowConn,
	ipIndex map[string]*core.DbNode,
	k8sResolved map[string]flowIPResolution,
	tenantID string,
	cloudAccountID string,
	windowStart, windowEnd time.Time,
) ([]*core.DbEdge, []*core.DbNode, []unresolvedFlowIP, error) {
	ports := make(map[edgeAggKey]map[int]*portStat)
	meta := make(map[edgeAggKey]*edgeAggMeta)
	groupNodes := make(map[string]*core.DbNode)
	unresolved := make(map[string]*unresolvedFlowIP)

	resolve := func(ip string) (*core.DbNode, string) {
		if n, ok := ipIndex[ip]; ok {
			return n, "ip-index"
		}
		if r, ok := k8sResolved[ip]; ok && r.node != nil {
			return r.node, r.tag
		}
		return nil, ""
	}
	noteUnresolved := func(ip, scope, peer string, c flowConn) {
		u := unresolved[ip]
		if u == nil {
			u = &unresolvedFlowIP{IP: ip, Scope: scope, Peers: map[string]bool{}, Ports: map[int]bool{}}
			unresolved[ip] = u
		}
		if peer != "" {
			u.Peers[peer] = true
		}
		u.Ports[c.DstPort] = true
		u.Bytes += c.Bytes
		u.Connections += c.Connections
	}

	for _, c := range conns {
		if c.Action != "ACCEPT" && c.Action != "REJECT" {
			continue
		}
		// Drop response traffic (destination is an ephemeral/client port).
		if !isServicePort(c.DstPort) {
			continue
		}

		srcNode, srcRes := resolve(c.SrcIP)
		dstNode, dstRes := resolve(c.DstIP)
		// Require at least one real (resolved) endpoint; a flow between two
		// unknown IPs carries no topology signal.
		if srcNode == nil && dstNode == nil {
			continue
		}
		srcUnresolved, dstUnresolved := srcNode == nil, dstNode == nil
		if srcUnresolved {
			srcNode = getOrCreateUnresolvedGroupNode(groupNodes, c.Scope, tenantID, cloudAccountID)
			srcRes = "unresolved"
			noteUnresolved(c.SrcIP, c.Scope, stringProp(dstNode, "name"), c)
		}
		if dstUnresolved {
			dstNode = getOrCreateUnresolvedGroupNode(groupNodes, c.Scope, tenantID, cloudAccountID)
			dstRes = "unresolved"
			noteUnresolved(c.DstIP, c.Scope, stringProp(srcNode, "name"), c)
		}
		if srcNode.ID == dstNode.ID {
			continue // self-loop (e.g. two IPs of the same ENI/host)
		}

		key := edgeAggKey{srcID: srcNode.ID, dstID: dstNode.ID, action: c.Action}
		if ports[key] == nil {
			ports[key] = make(map[int]*portStat)
			meta[key] = &edgeAggMeta{
				srcNode: srcNode, dstNode: dstNode, srcRes: srcRes, dstRes: dstRes,
				srcUnresolved: map[string]bool{}, dstUnresolved: map[string]bool{},
			}
		}
		if srcUnresolved {
			meta[key].srcUnresolved[c.SrcIP] = true
		}
		if dstUnresolved {
			meta[key].dstUnresolved[c.DstIP] = true
		}
		ps := ports[key][c.DstPort]
		if ps == nil {
			ps = &portStat{Port: c.DstPort, Protocol: c.Protocol}
			ports[key][c.DstPort] = ps
		}
		ps.Bytes += c.Bytes
		ps.Packets += c.Packets
		ps.Connections += c.Connections
	}

	edges := make([]*core.DbEdge, 0, len(ports))
	for key, portMap := range ports {
		m := meta[key]
		relType := core.RelationshipCalls
		if key.action == "REJECT" {
			relType = core.RelationshipConnectionRejected
		}

		breakdown := make([]map[string]any, 0, len(portMap))
		dstPorts := make([]int, 0, len(portMap))
		var totalBytes, totalPackets, totalConns int64
		for _, ps := range portMap {
			breakdown = append(breakdown, map[string]any{
				"port":        ps.Port,
				"protocol":    protocolName(ps.Protocol),
				"bytes":       ps.Bytes,
				"packets":     ps.Packets,
				"connections": ps.Connections,
			})
			dstPorts = append(dstPorts, ps.Port)
			totalBytes += ps.Bytes
			totalPackets += ps.Packets
			totalConns += ps.Connections
		}
		sort.Ints(dstPorts)
		sort.Slice(breakdown, func(i, j int) bool {
			return breakdown[i]["port"].(int) < breakdown[j]["port"].(int)
		})

		props := map[string]any{
			"action":                key.action,
			"dst_ports":             dstPorts,
			"port_breakdown":        breakdown,
			"total_bytes":           totalBytes,
			"total_packets":         totalPackets,
			"connection_count":      totalConns,
			"resolution_source_src": m.srcRes,
			"resolution_source_dst": m.dstRes,
			"discovered_from":       "vpc_flow_logs",
			"flow_window_start":     windowStart,
			"flow_window_end":       windowEnd,
		}
		if len(m.srcUnresolved) > 0 {
			props["unresolved_src_ips"] = sortedKeys(m.srcUnresolved)
		}
		if len(m.dstUnresolved) > 0 {
			props["unresolved_dst_ips"] = sortedKeys(m.dstUnresolved)
		}

		edge := s.CreateEdge(m.srcNode, m.dstNode, relType, props, tenantID, cloudAccountID)
		if edge != nil {
			edges = append(edges, edge)
		}
	}

	nodes := make([]*core.DbNode, 0, len(groupNodes))
	for _, n := range groupNodes {
		nodes = append(nodes, n)
	}
	unresolvedList := make([]unresolvedFlowIP, 0, len(unresolved))
	for _, u := range unresolved {
		unresolvedList = append(unresolvedList, *u)
	}
	sort.Slice(unresolvedList, func(i, j int) bool { return unresolvedList[i].IP < unresolvedList[j].IP })
	return edges, nodes, unresolvedList, nil
}

// buildPrivateIPIndex indexes existing graph nodes by their private IP(s) so a
// flow-log IP resolves to a real node without any AWS call. On collision the
// most specific node type wins (a resource beats the raw ENI it lives on).
//
// accountFilter, when non-nil, restricts indexing to nodes whose CloudAccountID
// is in the set — this is how cross-account RFC1918 collisions are avoided
// (10.0.1.5 exists in many VPCs). Pass nil to index all nodes.
func buildPrivateIPIndex(nodes []*core.DbNode, accountFilter map[string]bool) map[string]*core.DbNode {
	idx := make(map[string]*core.DbNode)

	add := func(ip string, n *core.DbNode) {
		ip = strings.TrimSpace(ip)
		if ip == "" || n == nil {
			return
		}
		if existing, ok := idx[ip]; !ok || ipNodeRank(n.NodeType) > ipNodeRank(existing.NodeType) {
			idx[ip] = n
		}
	}

	for _, n := range nodes {
		if n == nil || n.Properties == nil {
			continue
		}
		if accountFilter != nil && !accountFilter[n.CloudAccountID] {
			continue
		}
		for _, key := range []string{"private_ip", "private_ip_address", "pod_ip"} {
			if v, ok := n.Properties[key].(string); ok {
				add(v, n)
			}
		}
		switch arr := n.Properties["private_ips"].(type) {
		case []any:
			for _, e := range arr {
				if str, ok := e.(string); ok {
					add(str, n)
				}
			}
		case []string:
			for _, str := range arr {
				add(str, n)
			}
		}
	}
	return idx
}

// ipNodeRank ranks node types by resolution specificity so that, when the same
// IP is carried by several nodes, a real workload/resource wins over the raw
// NetworkInterface it is attached to, which in turn wins over an ExternalService.
func ipNodeRank(nt core.NodeType) int {
	switch nt {
	case core.NodeTypeComputeInstance, core.NodeTypeDatabase, core.NodeTypeCache,
		core.NodeTypeWorkload, core.NodeTypePod, core.NodeTypeServerlessFunction,
		core.NodeTypeService, core.NodeTypeK8sService, core.NodeTypeLoadBalancer,
		core.NodeTypeMessageQueue:
		return 3
	case core.NodeTypeNetworkInterface:
		return 2
	case core.NodeTypeExternalService:
		return 0
	default:
		return 1
	}
}

// getOrCreateUnresolvedGroupNode returns the one node standing in for every
// private address in scope (a VPC id, or a region when the VPC is unknown) that
// no lookup could place. One node per IP used to be minted instead; on a
// cluster that rotates nodes daily that was ~20 bare-IP "external services" a
// day, each claiming to be external when it is inside the VPC. The individual
// IPs travel on the edge (unresolved_src_ips / unresolved_dst_ips) and in the
// review table. The subtype is what core/impact.go keys on to keep these out of
// "possible cause" lists.
func getOrCreateUnresolvedGroupNode(cache map[string]*core.DbNode, scope, tenantID, cloudAccountID string) *core.DbNode {
	if scope == "" {
		scope = "unknown"
	}
	if n, ok := cache[scope]; ok {
		return n
	}
	name := fmt.Sprintf("Unidentified addresses (%s)", scope)
	uniqueKey := core.BuildUniqueKey(
		core.DeriveCloudProvider(awsVPCFlowSourceName, core.NodeTypeExternalService),
		cloudAccountID,
		"",
		core.NodeTypeExternalService,
		"",
		name,
	)
	props := map[string]any{
		"name":            name,
		"kind":            "ExternalService",
		"subtype":         core.UnresolvedVPCAddressSubtype,
		"is_sync_gap":     true,
		"discovered_from": "vpc_flow_logs",
		"scope":           scope,
	}
	if strings.HasPrefix(scope, "vpc-") {
		props["vpc_id"] = scope
	} else {
		props["region"] = scope
	}
	n := core.NewNode(core.NodeTypeExternalService, uniqueKey, props, tenantID, cloudAccountID, awsVPCFlowSourceName)
	cache[scope] = n
	return n
}

// recordUnresolvedIPs writes one knowledge_graph_classification_review row per
// address that stayed unresolved, so the misses can be reviewed without
// re-running ingestion. Fire-and-forget: a failure here never affects the build.
func (s *VPCFlowLogsFlowSource) recordUnresolvedIPs(
	tenantID, accountID string,
	unresolved []unresolvedFlowIP,
	k8sResolved map[string]flowIPResolution,
	k8sTried bool,
	windowStart, windowEnd time.Time,
) {
	if len(unresolved) == 0 {
		return
	}
	dbManager, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		s.logger.Warn("skipping unresolved VPC IP review records: no db manager", "error", err)
		return
	}
	lookups := []string{"ip-index"}
	if k8sTried {
		lookups = append(lookups, resolutionK8sNodeIP, resolutionK8sPodIP)
	}
	const maxPeers = 20
	for _, u := range unresolved {
		reason := reasonVPCIPUnresolved
		evidence := map[string]any{}
		if r, ok := k8sResolved[u.IP]; ok && r.reason != "" {
			reason = r.reason
			for k, v := range r.detail {
				evidence[k] = v
			}
		}
		peers := sortedKeys(u.Peers)
		if len(peers) > maxPeers {
			peers = peers[:maxPeers]
		}
		ports := make([]int, 0, len(u.Ports))
		for p := range u.Ports {
			ports = append(ports, p)
		}
		sort.Ints(ports)
		evidence["peers"] = peers
		evidence["dst_ports"] = ports
		evidence["bytes"] = u.Bytes
		evidence["connections"] = u.Connections
		evidence["lookups_tried"] = lookups
		evidence["flow_window_start"] = windowStart
		evidence["flow_window_end"] = windowEnd

		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		core.RecordUncertainClassification(ctx, dbManager, core.UncertainClassificationCandidate{
			TenantID:           tenantID,
			Source:             awsVPCFlowSourceName,
			ClassificationKind: "node_match",
			CandidateType:      "private_ip",
			CandidateName:      u.IP,
			CandidateNamespace: u.Scope,
			CandidateCluster:   accountID,
			ReasonCode:         reason,
			ReasonDescription:  "VPC flow-log address matched no ENI, K8s node or K8s workload in the graph",
			Evidence:           evidence,
		})
		cancel()
	}
}

// regionsForAccount collects the distinct AWS regions this account has nodes in,
// so flow-log discovery only queries regions that are actually in use. Region is
// read from node properties, falling back to the region segment of the 6-part
// unique key ({provider}:{account}:{region}:{type}:{hierarchy}:{name}).
func regionsForAccount(nodes []*core.DbNode, cloudAccountID string) []string {
	set := make(map[string]bool)
	for _, n := range nodes {
		if n == nil || n.CloudAccountID != cloudAccountID {
			continue
		}
		if n.Properties != nil {
			if r, ok := n.Properties["region"].(string); ok && awsVPCFlowRegionRegex.MatchString(r) {
				set[r] = true
				continue
			}
		}
		parts := strings.Split(n.UniqueKey, ":")
		if len(parts) >= 3 && awsVPCFlowRegionRegex.MatchString(parts[2]) {
			set[parts[2]] = true
		}
	}
	out := make([]string, 0, len(set))
	for r := range set {
		out = append(out, r)
	}
	sort.Strings(out)
	return out
}

// invertK8sCloudAccountMapping returns AWS-account → K8s-accounts by inverting
// core.GetK8sCloudAccountMapping (which is keyed by K8s account). Best-effort:
// on error it returns an empty map so resolution simply falls back to the AWS
// account's own nodes.
func invertK8sCloudAccountMapping(reqCtx *security.RequestContext, tenantID string) map[string][]string {
	k8sToAws, err := core.GetK8sCloudAccountMapping(reqCtx, tenantID, "AWS")
	if err != nil {
		return map[string][]string{}
	}
	awsToK8s := make(map[string][]string)
	for k8sAccount, awsAccounts := range k8sToAws {
		for _, awsAccount := range awsAccounts {
			awsToK8s[awsAccount] = append(awsToK8s[awsAccount], k8sAccount)
		}
	}
	return awsToK8s
}

// filterAccounts intersects the tenant's AWS accounts with the build's requested
// account filter (if any).
func filterAccounts(accounts []string, filter []string) []string {
	if len(filter) == 0 {
		return accounts
	}
	allow := make(map[string]bool, len(filter))
	for _, a := range filter {
		allow[a] = true
	}
	out := make([]string, 0, len(accounts))
	for _, a := range accounts {
		if allow[a] {
			out = append(out, a)
		}
	}
	return out
}

func atoiSafe(s string) int {
	v, err := strconv.Atoi(strings.TrimSpace(s))
	if err != nil {
		return 0
	}
	return v
}

func atoi64Safe(s string) int64 {
	// Insights numeric aggregates can come back as "1234" or "1234.0".
	s = strings.TrimSpace(s)
	if s == "" {
		return 0
	}
	if v, err := strconv.ParseInt(s, 10, 64); err == nil {
		return v
	}
	if f, err := strconv.ParseFloat(s, 64); err == nil {
		return int64(f)
	}
	return 0
}

// protocolName maps IANA protocol numbers to names for edge readability.
func protocolName(protocol int) string {
	switch protocol {
	case 1:
		return "icmp"
	case 6:
		return "tcp"
	case 17:
		return "udp"
	case 47:
		return "gre"
	case 50:
		return "esp"
	case 58:
		return "icmpv6"
	default:
		return fmt.Sprintf("proto-%d", protocol)
	}
}
