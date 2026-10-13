package selfhosted

import (
	"database/sql"
	"strings"
	"testing"
	"time"

	"nudgebee/services/knowledge_graph/core"
)

const (
	testTenant    = "tenant-1"
	testVMAccount = "vm-account-1"
)

var testScannedAt = time.Date(2026, 10, 13, 10, 47, 4, 0, time.UTC)

func cloudRow() HostRow {
	return HostRow{
		NBResourceID:  "cr-1",
		ResourceID:    "i-053da8d1122d144bc",
		Name:          "i-053da8d1122d144bc",
		CloudProvider: "AWS",
		Region:        "us-east-1",
		PrivateIP:     "172.31.0.10",
		PublicIP:      "3.235.247.102",
		ScanStatus:    sql.NullString{String: "completed", Valid: true},
		PackageCount:  sql.NullInt64{Int64: 495, Valid: true},
		FindingCount:  sql.NullInt64{Int64: 93, Valid: true},
		ScannedAt:     testScannedAt,
		OSFamily:      "ubuntu",
		OSVersion:     "22.04",
	}
}

func placeholderRow() HostRow {
	return HostRow{
		NBResourceID:  "cr-2",
		ResourceID:    "vm-172.31.7.0",
		Name:          "172.31.7.0",
		CloudProvider: "SelfHosted",
		Region:        "global",
		PrivateIP:     "172.31.7.0",
		ScanStatus:    sql.NullString{String: "failed", Valid: true},
		PackageCount:  sql.NullInt64{Int64: 0, Valid: true},
		FindingCount:  sql.NullInt64{Int64: 0, Valid: true},
		ScannedAt:     testScannedAt,
	}
}

func TestSchemaAndOntologyRegistered(t *testing.T) {
	if _, ok := core.LookupSpecificTypeSchema(SpecificTypeSelfHostedVM); !ok {
		t.Fatalf("expected specific_type schema %q to be registered", SpecificTypeSelfHostedVM)
	}
}

func TestConvertHostRows_CloudIdentifiedHost(t *testing.T) {
	nodes := ConvertHostRows([]HostRow{cloudRow()}, testTenant, testVMAccount)
	if len(nodes) != 1 {
		t.Fatalf("got %d nodes, want 1", len(nodes))
	}
	n := nodes[0]

	if n.NodeType != core.NodeTypeComputeInstance || n.SpecificType != SpecificTypeSelfHostedVM {
		t.Errorf("type = %s/%s, want ComputeInstance/SelfHostedVM", n.NodeType, n.SpecificType)
	}
	if n.Source != sourceName || n.CloudAccountID != testVMAccount || n.TenantID != testTenant {
		t.Errorf("source/account/tenant = %s/%s/%s", n.Source, n.CloudAccountID, n.TenantID)
	}
	wantKey := "selfhosted:" + testVMAccount + ":us-east-1:ComputeInstance::i-053da8d1122d144bc"
	if n.UniqueKey != wantKey {
		t.Errorf("unique key = %q, want %q", n.UniqueKey, wantKey)
	}
	if n.ID != core.NodeIDFor(wantKey, testTenant, testVMAccount) {
		t.Errorf("node id is not NodeIDFor-derived")
	}

	want := map[string]interface{}{
		"host_kind":         HostKindCloudIdentified,
		"instance_provider": "aws",
		"instance_id":       "i-053da8d1122d144bc",
		"nb_resource_id":    "cr-1",
		"private_ip":        "172.31.0.10",
		"public_ip":         "3.235.247.102",
		"os_family":         "ubuntu",
		"os_version":        "22.04",
		"package_count":     int64(495),
		"finding_count":     int64(93),
		"scan_status":       "completed",
		"scanned_at":        "2026-10-13T10:47:04Z",
	}
	for k, v := range want {
		if n.Properties[k] != v {
			t.Errorf("properties[%q] = %#v, want %#v", k, n.Properties[k], v)
		}
		// Every scan field is Indexed, so it must be filterable.
		if n.QueryAttributes[k] != v {
			t.Errorf("query_attributes[%q] = %#v, want %#v", k, n.QueryAttributes[k], v)
		}
	}
	// The instance id must never sit in resource_id: every ComputeInstance-by-
	// resource_id index (k8s_ec2_enricher, the provider-identifier resolver)
	// would then mistake this node for the EC2 node it is linked to.
	if _, ok := n.Properties["resource_id"]; ok {
		t.Errorf("cloud-identified host must not carry resource_id, got %v", n.Properties["resource_id"])
	}
	if n.OntologyAttributes["private_ip_address"] != "172.31.0.10" {
		t.Errorf("ontology private_ip_address = %v", n.OntologyAttributes["private_ip_address"])
	}
}

func TestConvertHostRows_OnPremHost(t *testing.T) {
	nodes := ConvertHostRows([]HostRow{placeholderRow()}, testTenant, testVMAccount)
	if len(nodes) != 1 {
		t.Fatalf("got %d nodes, want 1", len(nodes))
	}
	n := nodes[0]
	if n.Properties["host_kind"] != HostKindOnPrem {
		t.Errorf("host_kind = %v, want on_prem", n.Properties["host_kind"])
	}
	if n.Properties["resource_id"] != "vm-172.31.7.0" {
		t.Errorf("resource_id = %v, want the placeholder id", n.Properties["resource_id"])
	}
	for _, k := range []string{"instance_id", "instance_provider"} {
		if _, ok := n.Properties[k]; ok {
			t.Errorf("on-prem host must not carry %s", k)
		}
	}
	if n.Properties["name"] != "172.31.7.0" {
		t.Errorf("name = %v", n.Properties["name"])
	}
}

func TestConvertHostRows_PlaceholderIsOnPremWhateverItsProvider(t *testing.T) {
	row := placeholderRow()
	row.CloudProvider = "AWS"
	n := ConvertHostRows([]HostRow{row}, testTenant, testVMAccount)[0]
	if n.Properties["host_kind"] != HostKindOnPrem {
		t.Errorf("a vm-<ip> row is on-prem even with an AWS provider, got %v", n.Properties["host_kind"])
	}
}

func TestConvertHostRows_ProvidersAreLowercased(t *testing.T) {
	for _, tc := range []struct{ provider, want string }{
		{"AWS", "aws"}, {"GCP", "gcp"}, {"Azure", "azure"},
	} {
		row := cloudRow()
		row.CloudProvider = tc.provider
		n := ConvertHostRows([]HostRow{row}, testTenant, testVMAccount)[0]
		if n.Properties["instance_provider"] != tc.want {
			t.Errorf("%s: instance_provider = %v, want %s", tc.provider, n.Properties["instance_provider"], tc.want)
		}
	}
}

func TestConvertHostRows_DropsValuesThatAreNotIPs(t *testing.T) {
	row := cloudRow()
	// forager has stored an IMDS error page here.
	row.PublicIP = `<?xml version="1.0" encoding="iso-8859-1"?>`
	row.PrivateIP = " 10.20.2.25 "
	n := ConvertHostRows([]HostRow{row}, testTenant, testVMAccount)[0]
	if _, ok := n.Properties["public_ip"]; ok {
		t.Errorf("public_ip should be dropped, got %v", n.Properties["public_ip"])
	}
	if n.Properties["private_ip"] != "10.20.2.25" {
		t.Errorf("private_ip = %v, want trimmed 10.20.2.25", n.Properties["private_ip"])
	}
}

func TestConvertHostRows_SameKeyAndIDWhereverTheRowIsFiled(t *testing.T) {
	// Default setup: the row lives in the VM account. Re-pointed setup: the same
	// host is read from the target AWS account's row (different nb_resource_id).
	// The node is the VM account's either way, so re-pointing keeps its id.
	defaultRow := cloudRow()
	repointedRow := cloudRow()
	repointedRow.NBResourceID = "cr-in-aws-account"

	a := ConvertHostRows([]HostRow{defaultRow}, testTenant, testVMAccount)[0]
	b := ConvertHostRows([]HostRow{repointedRow}, testTenant, testVMAccount)[0]
	if a.UniqueKey != b.UniqueKey || a.ID != b.ID {
		t.Errorf("key/id differ between setups: %s/%s vs %s/%s", a.UniqueKey, a.ID, b.UniqueKey, b.ID)
	}
}

func TestConvertHostRows_NewestScanWinsOnDuplicateKey(t *testing.T) {
	newest := cloudRow()
	newest.NBResourceID = "cr-new"
	older := cloudRow()
	older.NBResourceID = "cr-old"
	older.ScannedAt = testScannedAt.Add(-48 * time.Hour)

	nodes := ConvertHostRows([]HostRow{newest, older}, testTenant, testVMAccount)
	if len(nodes) != 1 {
		t.Fatalf("got %d nodes, want 1", len(nodes))
	}
	if nodes[0].Properties["nb_resource_id"] != "cr-new" {
		t.Errorf("kept %v, want the first (newest) row", nodes[0].Properties["nb_resource_id"])
	}
}

func TestConvertHostRows_IPv6PlaceholderKeepsSixPartKey(t *testing.T) {
	row := placeholderRow()
	row.ResourceID = "vm-fe80::1"
	n := ConvertHostRows([]HostRow{row}, testTenant, testVMAccount)[0]
	if parts := strings.Split(n.UniqueKey, ":"); len(parts) != core.UniqueKeyPartCount {
		t.Errorf("unique key %q has %d parts, want %d", n.UniqueKey, len(parts), core.UniqueKeyPartCount)
	}
	if n.Properties["resource_id"] != "vm-fe80::1" {
		t.Errorf("resource_id keeps the raw id, got %v", n.Properties["resource_id"])
	}
}

func TestConvertHostRows_SkipsRowsWithoutAnID(t *testing.T) {
	row := cloudRow()
	row.ResourceID = ""
	if nodes := ConvertHostRows([]HostRow{row}, testTenant, testVMAccount); len(nodes) != 0 {
		t.Errorf("got %d nodes, want 0", len(nodes))
	}
}

func TestConvertHostRows_MissingScanCountsAreOmitted(t *testing.T) {
	row := cloudRow()
	row.ScanStatus = sql.NullString{}
	row.PackageCount = sql.NullInt64{}
	row.FindingCount = sql.NullInt64{}
	n := ConvertHostRows([]HostRow{row}, testTenant, testVMAccount)[0]
	for _, k := range []string{"scan_status", "package_count", "finding_count"} {
		if _, ok := n.Properties[k]; ok {
			t.Errorf("%s should be omitted when NULL, got %v", k, n.Properties[k])
		}
	}
}

func TestScanFreshnessDaysDefaults(t *testing.T) {
	if got := scanFreshnessDays(); got <= 0 {
		t.Errorf("scanFreshnessDays() = %d, want > 0", got)
	}
}
