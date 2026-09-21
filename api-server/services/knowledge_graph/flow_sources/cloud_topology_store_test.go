package flow_sources

import (
	"encoding/json"
	"testing"
)

// All identifiers below are synthetic. 123456789012 is AWS's documentation
// account number; nothing here comes from a real tenant.
const (
	testAccountA = "acct-aaaa"
	testAccountB = "acct-bbbb"
)

func row(accountID, resourceType, resourceID, arn, meta string) CloudResourceRow {
	return CloudResourceRow{
		ResourceID: resourceID,
		Type:       resourceType,
		Account:    accountID,
		Tenant:     "tenant-1",
		ARN:        arn,
		Meta:       json.RawMessage(meta),
		IsActive:   true,
	}
}

func newTestStore(rows ...CloudResourceRow) *CloudTopologyStore {
	return NewCloudTopologyStoreFromRows("tenant-1", rows, nil)
}

// --- ENI lookups -------------------------------------------------------------

const eniMeta = `{
  "NetworkInterfaceId": "eni-0123456789abcdef0",
  "SubnetId": "subnet-aaaa",
  "VpcId": "vpc-aaaa",
  "AvailabilityZone": "us-east-1a",
  "InterfaceType": "interface",
  "Status": "in-use",
  "PrivateIpAddress": "10.0.1.10",
  "PrivateIpAddresses": [
    {"Primary": true,  "PrivateIpAddress": "10.0.1.10"},
    {"Primary": false, "PrivateIpAddress": "10.0.1.11"}
  ],
  "Groups": [{"GroupId": "sg-aaaa", "GroupName": "web"}],
  "Attachment": {"AttachmentId": "eni-attach-1", "InstanceId": "i-0123456789abcdef0", "DeviceIndex": 0, "Status": "attached"},
  "TagSet": [{"Key": "Name", "Value": "web-eni"}]
}`

func TestENIMetaByPrivateIP(t *testing.T) {
	store := newTestStore(row(testAccountA, topologyTypeNetworkInterface, "eni-0123456789abcdef0", "", eniMeta))

	tests := []struct {
		name    string
		ip      string
		wantHit bool
	}{
		{"primary address hits", "10.0.1.10", true},
		{"secondary address hits", "10.0.1.11", true},
		{"unknown address misses", "10.0.99.99", false},
		{"empty address misses", "", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			metas, hit := store.ENIMetaByPrivateIP(tt.ip)
			if hit != tt.wantHit {
				t.Fatalf("ENIMetaByPrivateIP(%q) hit = %v, want %v", tt.ip, hit, tt.wantHit)
			}
			if !tt.wantHit {
				return
			}
			if len(metas) != 1 {
				t.Fatalf("got %d metas, want 1", len(metas))
			}
			// The stored meta must decode with the same struct the CLI response
			// uses — that equivalence is what makes this a drop-in replacement.
			var ni awsNetworkInterface
			if err := json.Unmarshal(metas[0], &ni); err != nil {
				t.Fatalf("stored meta does not decode as a describe-network-interfaces element: %v", err)
			}
			if ni.NetworkInterfaceId != "eni-0123456789abcdef0" {
				t.Errorf("NetworkInterfaceId = %q", ni.NetworkInterfaceId)
			}
			if ni.Attachment == nil || ni.Attachment.InstanceId != "i-0123456789abcdef0" {
				t.Errorf("attachment not decoded: %+v", ni.Attachment)
			}
			if len(ni.Groups) != 1 || ni.Groups[0].GroupId != "sg-aaaa" {
				t.Errorf("groups not decoded: %+v", ni.Groups)
			}
		})
	}
}

func TestENIMetaByPrivateIPSkipsUnparseableMeta(t *testing.T) {
	store := newTestStore(row(testAccountA, topologyTypeNetworkInterface, "eni-bad", "", `not json`))
	if _, hit := store.ENIMetaByPrivateIP("10.0.1.10"); hit {
		t.Error("a row with unparseable meta must not produce a hit")
	}
}

// --- Route 53 ----------------------------------------------------------------

const zoneMeta = `{
  "Id": "/hostedzone/Z0123456789ABCDEFGHIJ",
  "Name": "example.internal.",
  "Config": {"PrivateZone": true},
  "RecordSets": [
    {"Name": "db.example.internal.", "Type": "CNAME", "ResourceRecords": [{"Value": "db.abcdef.us-east-1.rds.amazonaws.com"}]}
  ]
}`

func TestHostedZones(t *testing.T) {
	store := newTestStore(row(testAccountA, topologyTypeHostedZone, "Z0123456789ABCDEFGHIJ", "", zoneMeta))

	zones, hit := store.HostedZones(testAccountA)
	if !hit {
		t.Fatal("expected a hit")
	}
	if len(zones) != 1 {
		t.Fatalf("got %d zones, want 1", len(zones))
	}
	// The path form is what the CLI returns and what record lookups pass back,
	// so it must survive unchanged.
	if zones[0].ID != "/hostedzone/Z0123456789ABCDEFGHIJ" {
		t.Errorf("ID = %q, want the /hostedzone/ path form", zones[0].ID)
	}
	if zones[0].Name != "example.internal." {
		t.Errorf("Name = %q", zones[0].Name)
	}
	if !zones[0].PrivateZone {
		t.Error("PrivateZone must be read from meta.Config.PrivateZone")
	}

	if _, hit := store.HostedZones(testAccountB); hit {
		t.Error("zones must be scoped to their own account")
	}
}

func TestRecordSetsForZone(t *testing.T) {
	store := newTestStore(row(testAccountA, topologyTypeHostedZone, "Z0123456789ABCDEFGHIJ", "", zoneMeta))

	tests := []struct {
		name    string
		zoneID  string
		wantHit bool
	}{
		{"path form", "/hostedzone/Z0123456789ABCDEFGHIJ", true},
		{"bare form", "Z0123456789ABCDEFGHIJ", true},
		{"unknown zone", "Z9999999999999999999", false},
		{"empty zone", "", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			records, hit := store.RecordSetsForZone(testAccountA, tt.zoneID)
			if hit != tt.wantHit {
				t.Fatalf("hit = %v, want %v", hit, tt.wantHit)
			}
			if !tt.wantHit {
				return
			}
			if len(records) != 1 || records[0].Type != "CNAME" {
				t.Fatalf("records = %+v", records)
			}
			if len(records[0].ResourceRecords) != 1 {
				t.Fatalf("resource records not decoded: %+v", records[0])
			}
		})
	}
}

// A zone row collected without its RecordSets must be a miss, not an empty
// result. Returning empty would silently drop every DNS edge in that zone while
// looking like a successful lookup.
func TestRecordSetsForZoneMissesWhenNotCollected(t *testing.T) {
	meta := `{"Id": "/hostedzone/Z0123456789ABCDEFGHIJ", "Name": "example.internal."}`
	store := newTestStore(row(testAccountA, topologyTypeHostedZone, "Z0123456789ABCDEFGHIJ", "", meta))

	if _, hit := store.RecordSetsForZone(testAccountA, "Z0123456789ABCDEFGHIJ"); hit {
		t.Error("a zone with no RecordSets key must miss so the caller falls back to the CLI")
	}
}

// An empty RecordSets array is a real answer — the zone has no records — and
// must NOT send the caller back to the CLI.
func TestRecordSetsForZoneHitsOnEmptyCollectedList(t *testing.T) {
	meta := `{"Id": "/hostedzone/Z0123456789ABCDEFGHIJ", "Name": "example.internal.", "RecordSets": []}`
	store := newTestStore(row(testAccountA, topologyTypeHostedZone, "Z0123456789ABCDEFGHIJ", "", meta))

	records, hit := store.RecordSetsForZone(testAccountA, "Z0123456789ABCDEFGHIJ")
	if !hit {
		t.Error("an explicitly empty RecordSets list is an answer, not a miss")
	}
	if len(records) != 0 {
		t.Errorf("records = %+v, want none", records)
	}
}

// --- Load balancers ----------------------------------------------------------

const lbARN = "arn:aws:elasticloadbalancing:us-east-1:123456789012:loadbalancer/app/web/abc123"

const lbMeta = `{
  "LoadBalancerArn": "arn:aws:elasticloadbalancing:us-east-1:123456789012:loadbalancer/app/web/abc123",
  "DNSName": "web-123456.us-east-1.elb.amazonaws.com",
  "TargetGroups": [
    {"TargetGroupArn": "arn:aws:elasticloadbalancing:us-east-1:123456789012:targetgroup/web/def456", "TargetGroupName": "web", "TargetType": "ip", "Port": 8080}
  ]
}`

func TestTargetGroupsForLB(t *testing.T) {
	store := newTestStore(row(testAccountA, "application_loadbalancer", "web", lbARN, lbMeta))

	groups, hit := store.TargetGroupsForLB(lbARN)
	if !hit {
		t.Fatal("expected a hit")
	}
	if len(groups) != 1 {
		t.Fatalf("got %d target groups, want 1", len(groups))
	}

	var tg struct {
		TargetGroupArn string `json:"TargetGroupArn"`
		TargetType     string `json:"TargetType"`
	}
	if err := json.Unmarshal(groups[0], &tg); err != nil {
		t.Fatalf("stored target group does not decode: %v", err)
	}
	if tg.TargetType != "ip" {
		t.Errorf("TargetType = %q", tg.TargetType)
	}

	if _, hit := store.TargetGroupsForLB("arn:aws:elasticloadbalancing:us-east-1:123456789012:loadbalancer/app/other/zzz"); hit {
		t.Error("an unknown ARN must miss")
	}
}

// A load balancer collected before target groups were captured must miss rather
// than report zero target groups.
func TestTargetGroupsForLBMissesWhenNotCollected(t *testing.T) {
	meta := `{"LoadBalancerArn": "` + lbARN + `", "DNSName": "web-123456.us-east-1.elb.amazonaws.com"}`
	store := newTestStore(row(testAccountA, "application_loadbalancer", "web", lbARN, meta))

	if _, hit := store.TargetGroupsForLB(lbARN); hit {
		t.Error("a load balancer with no TargetGroups key must miss")
	}
}

// Older rows spell the type differently; all of them must index.
func TestLoadBalancerTypeSpellings(t *testing.T) {
	for _, lbType := range topologyLoadBalancerTypes {
		t.Run(lbType, func(t *testing.T) {
			store := newTestStore(row(testAccountA, lbType, "web", lbARN, lbMeta))
			if _, hit := store.TargetGroupsForLB(lbARN); !hit {
				t.Errorf("type %q was not indexed as a load balancer", lbType)
			}
		})
	}
}

// A row whose meta omits LoadBalancerArn is still reachable by its arn column.
func TestLoadBalancerIndexedByARNColumn(t *testing.T) {
	meta := `{"DNSName": "web-123456.us-east-1.elb.amazonaws.com", "TargetGroups": []}`
	store := newTestStore(row(testAccountA, "loadbalancer", "web", lbARN, meta))

	if _, hit := store.TargetGroupsForLB(lbARN); !hit {
		t.Error("expected the arn column to index the row when meta has no LoadBalancerArn")
	}
}

func TestTagsForLB(t *testing.T) {
	r := row(testAccountA, "application_loadbalancer", "web", lbARN, lbMeta)
	r.Tags = json.RawMessage(`{"kubernetes.io/service-name": ["default/web"], "env": []}`)
	store := newTestStore(r)

	tags, hit := store.TagsForLB(lbARN)
	if !hit {
		t.Fatal("expected a hit")
	}
	if tags["kubernetes.io/service-name"] != "default/web" {
		t.Errorf("tags = %+v", tags)
	}
	if v, ok := tags["env"]; !ok || v != "" {
		t.Errorf("a key with no values must map to the empty string, got %q ok=%v", v, ok)
	}
}

// --- Scoping, nil handling, stats --------------------------------------------

// A nil store is the disabled/failed-to-build case. Every lookup must miss
// without panicking, so callers need no nil checks of their own.
func TestNilStoreMissesEverything(t *testing.T) {
	var store *CloudTopologyStore

	if _, hit := store.ENIMetaByPrivateIP("10.0.1.10"); hit {
		t.Error("ENIMetaByPrivateIP")
	}
	if _, hit := store.HostedZones(testAccountA); hit {
		t.Error("HostedZones")
	}
	if _, hit := store.RecordSetsForZone(testAccountA, "Z0123456789ABCDEFGHIJ"); hit {
		t.Error("RecordSetsForZone")
	}
	if _, hit := store.TargetGroupsForLB(lbARN); hit {
		t.Error("TargetGroupsForLB")
	}
	if _, hit := store.TagsForLB(lbARN); hit {
		t.Error("TagsForLB")
	}
	store.LogStats("nil-store")
}

func TestStatsCountHitsAndMisses(t *testing.T) {
	store := newTestStore(row(testAccountA, topologyTypeNetworkInterface, "eni-0123456789abcdef0", "", eniMeta))

	store.ENIMetaByPrivateIP("10.0.1.10")
	store.ENIMetaByPrivateIP("10.0.99.99")
	store.ENIMetaByPrivateIP("10.0.99.98")

	if got := store.hits[topologyTypeNetworkInterface]; got != 1 {
		t.Errorf("hits = %d, want 1", got)
	}
	if got := store.misses[topologyTypeNetworkInterface]; got != 2 {
		t.Errorf("misses = %d, want 2", got)
	}
	store.LogStats("test")
}

func TestNormalizeHostedZoneID(t *testing.T) {
	tests := map[string]string{
		"/hostedzone/Z0123456789ABCDEFGHIJ": "Z0123456789ABCDEFGHIJ",
		"Z0123456789ABCDEFGHIJ":             "Z0123456789ABCDEFGHIJ",
		"  Z0123456789ABCDEFGHIJ  ":         "Z0123456789ABCDEFGHIJ",
		"":                                  "",
	}
	for in, want := range tests {
		if got := normalizeHostedZoneID(in); got != want {
			t.Errorf("normalizeHostedZoneID(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestMetaField(t *testing.T) {
	tests := []struct {
		meta    string
		key     string
		wantOK  bool
		wantRaw string
	}{
		{`{"TargetGroups": []}`, "TargetGroups", true, "[]"},
		{`{"TargetGroups": null}`, "TargetGroups", true, "null"},
		{`{"DNSName": "x"}`, "TargetGroups", false, ""},
		{`not json`, "TargetGroups", false, ""},
		{``, "TargetGroups", false, ""},
	}
	for _, tt := range tests {
		raw, ok := metaField(json.RawMessage(tt.meta), tt.key)
		if ok != tt.wantOK {
			t.Errorf("metaField(%q, %q) ok = %v, want %v", tt.meta, tt.key, ok, tt.wantOK)
			continue
		}
		if ok && string(raw) != tt.wantRaw {
			t.Errorf("metaField(%q, %q) raw = %q, want %q", tt.meta, tt.key, raw, tt.wantRaw)
		}
	}
}
