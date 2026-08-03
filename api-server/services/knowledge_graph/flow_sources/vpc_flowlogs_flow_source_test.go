package flow_sources

import (
	"log/slog"
	"strings"
	"testing"
	"time"

	"nudgebee/services/cloud"
	"nudgebee/services/knowledge_graph/core"
)

func mkNode(id string, nt core.NodeType, name string) *core.DbNode {
	return &core.DbNode{
		ID:         id,
		NodeType:   nt,
		UniqueKey:  string(nt) + ":" + name,
		Properties: map[string]any{"name": name},
	}
}

func TestCanonicalAddrFields(t *testing.T) {
	// Default v2 format → raw addrs.
	src, dst := canonicalAddrFields("${version} ${srcaddr} ${dstaddr}")
	if src != "srcaddr" || dst != "dstaddr" {
		t.Fatalf("default format: got (%s,%s), want (srcaddr,dstaddr)", src, dst)
	}
	// Custom v3+ format with pkt-* → de-masked addrs.
	src, dst = canonicalAddrFields("${srcaddr} ${dstaddr} ${pkt-srcaddr} ${pkt-dstaddr} ${flow-direction}")
	if src != "pkt_srcaddr" || dst != "pkt_dstaddr" {
		t.Fatalf("pkt format: got (%s,%s), want (pkt_srcaddr,pkt_dstaddr)", src, dst)
	}
}

func TestIsServicePort(t *testing.T) {
	cases := map[int]bool{
		443:   true,  // https
		5432:  true,  // postgres
		80:    true,  // http
		8080:  true,  // below ephemeral floor
		40000: false, // ephemeral client
		32768: false, // ephemeral floor
		60999: false, // ephemeral, Linux default ceiling
		65535: false, // ephemeral ceiling
		0:     false, // invalid
	}
	for port, want := range cases {
		if got := isServicePort(port); got != want {
			t.Errorf("isServicePort(%d) = %v, want %v", port, got, want)
		}
	}
}

func TestBuildFlowConnQuery(t *testing.T) {
	q := buildFlowConnQuery("/pattern/", "pkt_srcaddr", "pkt_dstaddr", 250)
	// Must aggregate on the de-masked fields, keep dstport AND action distinct.
	if !strings.Contains(q, "by pkt_srcaddr, pkt_dstaddr, dstport, protocol, action") {
		t.Errorf("query missing correct GROUP BY:\n%s", q)
	}
	// Must filter to valid, private-only traffic and cap results.
	for _, want := range []string{`log_status = "OK"`, `pkt_srcaddr like /^10\./`, "limit 250"} {
		if !strings.Contains(q, want) {
			t.Errorf("query missing %q:\n%s", want, q)
		}
	}
}

func TestParseFlowConns(t *testing.T) {
	results := []cloud.LogMessage{
		{Labels: []cloud.LogLabel{
			{Label: "pkt_srcaddr", Value: "10.0.1.20"},
			{Label: "pkt_dstaddr", Value: "10.0.2.50"},
			{Label: "dstport", Value: "5432"},
			{Label: "protocol", Value: "6"},
			{Label: "action", Value: "accept"},
			{Label: "total_bytes", Value: "1500"},
			{Label: "total_packets", Value: "12"},
			{Label: "connection_count", Value: "3.0"},
		}},
		// Missing dst → skipped.
		{Labels: []cloud.LogLabel{{Label: "pkt_srcaddr", Value: "10.0.1.20"}}},
	}
	conns := parseFlowConns(results, "pkt_srcaddr", "pkt_dstaddr")
	if len(conns) != 1 {
		t.Fatalf("got %d conns, want 1", len(conns))
	}
	c := conns[0]
	if c.SrcIP != "10.0.1.20" || c.DstIP != "10.0.2.50" || c.DstPort != 5432 ||
		c.Protocol != 6 || c.Action != "ACCEPT" || c.Bytes != 1500 || c.Packets != 12 || c.Connections != 3 {
		t.Fatalf("unexpected conn: %+v", c)
	}
}

func TestBuildPrivateIPIndex_PrefersResourceOverENI(t *testing.T) {
	eni := mkNode("eni-1", core.NodeTypeNetworkInterface, "eni-abc")
	eni.Properties["private_ip_address"] = "10.0.0.5"
	ec2 := mkNode("i-1", core.NodeTypeComputeInstance, "checkout")
	ec2.Properties["private_ip"] = "10.0.0.5"
	pod := mkNode("p-1", core.NodeTypePod, "worker-0")
	pod.Properties["pod_ip"] = "10.0.0.9"

	// ENI first so a naive last/first-write would pick it; rank must still win.
	idx := buildPrivateIPIndex([]*core.DbNode{eni, ec2, pod}, nil)
	if got := idx["10.0.0.5"]; got == nil || got.ID != "i-1" {
		t.Errorf("10.0.0.5 resolved to %v, want the ComputeInstance i-1", got)
	}
	if got := idx["10.0.0.9"]; got == nil || got.ID != "p-1" {
		t.Errorf("10.0.0.9 resolved to %v, want pod p-1", got)
	}
}

func TestBuildPrivateIPIndex_AccountScoping(t *testing.T) {
	a := mkNode("i-a", core.NodeTypeComputeInstance, "svc-a")
	a.CloudAccountID = "acct-a"
	a.Properties["private_ip"] = "10.0.1.5"
	b := mkNode("i-b", core.NodeTypeComputeInstance, "svc-b")
	b.CloudAccountID = "acct-b"
	b.Properties["private_ip"] = "10.0.1.5" // same RFC1918 IP in a different account

	// Scoped to acct-a: the colliding IP from acct-b must not be indexed.
	idx := buildPrivateIPIndex([]*core.DbNode{a, b}, map[string]bool{"acct-a": true})
	if got := idx["10.0.1.5"]; got == nil || got.ID != "i-a" {
		t.Errorf("scoped index resolved 10.0.1.5 to %v, want acct-a's node i-a", got)
	}
	if len(idx) != 1 {
		t.Errorf("scoped index has %d entries, want 1", len(idx))
	}
}

func TestBuildEdgesFromConns(t *testing.T) {
	s := NewVPCFlowLogsFlowSource(slog.Default())

	ec2 := mkNode("i-checkout", core.NodeTypeComputeInstance, "checkout")
	rds := mkNode("db-orders", core.NodeTypeDatabase, "orders-db")
	ipIndex := map[string]*core.DbNode{
		"10.0.1.20": ec2,
		"10.0.2.50": rds,
	}

	conns := []flowConn{
		// Forward call to RDS:5432 (ACCEPT), two rows aggregate.
		{SrcIP: "10.0.1.20", DstIP: "10.0.2.50", DstPort: 5432, Protocol: 6, Action: "ACCEPT", Bytes: 100, Packets: 2, Connections: 1},
		{SrcIP: "10.0.1.20", DstIP: "10.0.2.50", DstPort: 5432, Protocol: 6, Action: "ACCEPT", Bytes: 50, Packets: 1, Connections: 1},
		// Return traffic (ephemeral dst) → must be dropped.
		{SrcIP: "10.0.2.50", DstIP: "10.0.1.20", DstPort: 41000, Protocol: 6, Action: "ACCEPT", Bytes: 9000, Packets: 30, Connections: 1},
		// Blocked attempt on 3306 (REJECT) → separate edge type.
		{SrcIP: "10.0.1.20", DstIP: "10.0.2.50", DstPort: 3306, Protocol: 6, Action: "REJECT", Bytes: 40, Packets: 1, Connections: 4},
		// Unresolved source IP → sync-gap node + edge to RDS.
		{SrcIP: "10.0.9.9", DstIP: "10.0.2.50", DstPort: 5432, Protocol: 6, Action: "ACCEPT", Bytes: 10, Packets: 1, Connections: 1},
		// Both unknown → no signal, dropped.
		{SrcIP: "10.0.9.8", DstIP: "10.0.9.7", DstPort: 5432, Protocol: 6, Action: "ACCEPT", Bytes: 1, Packets: 1, Connections: 1},
	}

	windowEnd := time.Now()
	windowStart := windowEnd.Add(-time.Hour)
	edges, nodes, err := s.buildEdgesFromConns(conns, ipIndex, "tenant-1", "acct-1", windowStart, windowEnd)
	if err != nil {
		t.Fatalf("buildEdgesFromConns error: %v", err)
	}

	var calls, rejects, syncGapCalls *core.DbEdge
	for _, e := range edges {
		switch {
		case e.SourceNodeID == "i-checkout" && e.DestinationNodeID == "db-orders" && e.RelationshipType == core.RelationshipCalls:
			calls = e
		case e.SourceNodeID == "i-checkout" && e.DestinationNodeID == "db-orders" && e.RelationshipType == core.RelationshipConnectionRejected:
			rejects = e
		case e.DestinationNodeID == "db-orders" && e.RelationshipType == core.RelationshipCalls && e.SourceNodeID != "i-checkout":
			syncGapCalls = e
		}
	}

	if calls == nil {
		t.Fatal("missing checkout→orders-db CALLS edge")
	}
	if got := calls.Properties["total_bytes"].(int64); got != 150 {
		t.Errorf("CALLS total_bytes = %d, want 150 (two ACCEPT rows aggregated)", got)
	}
	if ports := calls.Properties["dst_ports"].([]int); len(ports) != 1 || ports[0] != 5432 {
		t.Errorf("CALLS dst_ports = %v, want [5432]", ports)
	}
	if calls.Properties["resolution_source_src"] != "ip-index" || calls.Properties["resolution_source_dst"] != "ip-index" {
		t.Errorf("CALLS resolution provenance not stamped: %v / %v",
			calls.Properties["resolution_source_src"], calls.Properties["resolution_source_dst"])
	}
	if calls.Properties["flow_window_start"] != windowStart || calls.Properties["flow_window_end"] != windowEnd {
		t.Errorf("CALLS freshness window not stamped: got start=%v end=%v, want start=%v end=%v",
			calls.Properties["flow_window_start"], calls.Properties["flow_window_end"], windowStart, windowEnd)
	}

	if rejects == nil {
		t.Fatal("missing checkout→orders-db CONNECTION_REJECTED edge")
	}
	if ports := rejects.Properties["dst_ports"].([]int); len(ports) != 1 || ports[0] != 3306 {
		t.Errorf("REJECT dst_ports = %v, want [3306]", ports)
	}

	if syncGapCalls == nil {
		t.Fatal("missing sync-gap→orders-db CALLS edge for the unresolved source IP")
	}
	if syncGapCalls.Properties["resolution_source_src"] != "unresolved" {
		t.Errorf("sync-gap edge src provenance = %v, want unresolved", syncGapCalls.Properties["resolution_source_src"])
	}

	// Exactly one sync-gap ExternalService node created (for 10.0.9.9); the
	// both-unknown flow created none.
	if len(nodes) != 1 {
		t.Fatalf("got %d created nodes, want 1 sync-gap node", len(nodes))
	}
	if nodes[0].NodeType != core.NodeTypeExternalService || nodes[0].Properties["is_sync_gap"] != true {
		t.Errorf("created node is not a sync-gap ExternalService: %+v", nodes[0])
	}

	// No edge should reference the dropped ephemeral-return flow (port 41000).
	for _, e := range edges {
		if ports, ok := e.Properties["dst_ports"].([]int); ok {
			for _, p := range ports {
				if p == 41000 {
					t.Errorf("response traffic on ephemeral port 41000 was not dropped")
				}
			}
		}
	}
}

// TestBuildPrivateIPIndex_PrefersDatabaseOverRDSENI covers the RDS shape
// specifically: an RDS instance and the network interface it is attached to
// share one address, and flow logs only ever report that address. The database
// has to win, or every "talks to this database" edge terminates on an interface
// named RDSNetworkInterface and blast radius stops one hop short of the resource
// an RDS alarm is about.
func TestBuildPrivateIPIndex_PrefersDatabaseOverRDSENI(t *testing.T) {
	eni := mkNode("eni-rds", core.NodeTypeNetworkInterface, "RDSNetworkInterface")
	eni.Properties["private_ips"] = []string{"172.31.4.191"}
	db := mkNode("db-main", core.NodeTypeDatabase, "main")
	db.Properties["private_ip_address"] = "172.31.4.191"
	db.Properties["endpoint_address"] = "main.ca5yt51qtp3r.us-east-1.rds.amazonaws.com"

	// ENI first, mirroring collection order, so rank rather than ordering decides.
	idx := buildPrivateIPIndex([]*core.DbNode{eni, db}, nil)
	if got := idx["172.31.4.191"]; got == nil || got.ID != "db-main" {
		t.Errorf("172.31.4.191 resolved to %v, want the Database db-main", got)
	}
}

// Without the resolved address the database cannot be indexed at all, so the
// interface is the only candidate. This is the behaviour the collector-side
// resolution exists to prevent; it is pinned here so the fix cannot regress
// silently to "an ENI is good enough".
func TestBuildPrivateIPIndex_DatabaseWithoutIPFallsBackToENI(t *testing.T) {
	eni := mkNode("eni-rds", core.NodeTypeNetworkInterface, "RDSNetworkInterface")
	eni.Properties["private_ips"] = []string{"172.31.4.191"}
	db := mkNode("db-main", core.NodeTypeDatabase, "main")
	db.Properties["endpoint_address"] = "main.ca5yt51qtp3r.us-east-1.rds.amazonaws.com"

	idx := buildPrivateIPIndex([]*core.DbNode{eni, db}, nil)
	if got := idx["172.31.4.191"]; got == nil || got.ID != "eni-rds" {
		t.Errorf("172.31.4.191 resolved to %v, want the ENI when the database has no address", got)
	}
}
