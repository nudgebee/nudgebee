package aws

import (
	"encoding/json"
	"testing"

	"nudgebee/services/knowledge_graph/sources"
)

// row builds a cloud_resourses row for the filter under test. meta is encoded
// as-is so a nil map exercises the "no meta at all" path.
func billingTestRow(service, typ, arn string, meta map[string]interface{}) *sources.CloudResourceRow {
	r := &sources.CloudResourceRow{
		ResourceID:  "snap-0123456789abcdef0",
		Name:        "snap-0123456789abcdef0",
		Type:        typ,
		Status:      "Active",
		ServiceName: service,
		ARN:         arn,
	}
	if meta != nil {
		raw, err := json.Marshal(meta)
		if err != nil {
			panic(err)
		}
		r.Meta = raw
	}
	return r
}

// A billing-export line item carries a snapshot id and nothing else. It must not
// become a graph node: one account produced 505,281 of these in a single run and
// OOMKilled the shared services-server pod (#40937).
func TestShouldIncludeResource_DropsBillingOnlySnapshotStubs(t *testing.T) {
	s := &AWSSource{}

	cases := []struct {
		name    string
		row     *sources.CloudResourceRow
		include bool
	}{
		{
			name:    "billing stub with no ARN is dropped",
			row:     billingTestRow("AmazonEC2", "snapshot", "", map[string]interface{}{"nb_source": "billing"}),
			include: false,
		},
		{
			name:    "type match is case-insensitive",
			row:     billingTestRow("AmazonEC2", "Snapshot", "", map[string]interface{}{"nb_source": "billing"}),
			include: false,
		},
		{
			name: "API-discovered snapshot is kept",
			row: billingTestRow("AmazonEC2", "snapshot", "arn:aws:ec2:us-east-1:1234:snapshot/snap-1",
				map[string]interface{}{"nb_source": "api", "VolumeSize": 8}),
			include: true,
		},
		{
			// An ARN does not redeem a billing row: the derive step fills arn from
			// the report's own resource_arn column, so a report that populates it
			// would otherwise reintroduce the whole unbounded set.
			name: "billing row is dropped even with an ARN",
			row: billingTestRow("AmazonEC2", "snapshot", "arn:aws:ec2:us-east-1:1234:snapshot/snap-1",
				map[string]interface{}{"nb_source": "billing"}),
			include: false,
		},
		{
			name:    "snapshot with no meta at all is kept",
			row:     billingTestRow("AmazonEC2", "snapshot", "", nil),
			include: true,
		},
		{
			name:    "unparseable meta is kept",
			row:     &sources.CloudResourceRow{ServiceName: "AmazonEC2", Type: "snapshot", Meta: json.RawMessage("not json")},
			include: true,
		},
		{
			// Scoped to snapshots: EBS volumes and every other type are untouched,
			// so an AWS type only ever seen via billing does not vanish.
			name:    "billing EBS volume is kept",
			row:     billingTestRow("AmazonEC2", "storage", "", map[string]interface{}{"nb_source": "billing"}),
			include: true,
		},
		{
			name:    "billing snapshot of another service is kept",
			row:     billingTestRow("AmazonRDS", "snapshot", "", map[string]interface{}{"nb_source": "billing"}),
			include: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := s.shouldIncludeResource(tc.row); got != tc.include {
				t.Fatalf("shouldIncludeResource = %v, want %v", got, tc.include)
			}
		})
	}
}

func TestAWSResourceMetaSource(t *testing.T) {
	cases := []struct {
		name string
		row  *sources.CloudResourceRow
		want string
	}{
		{"nil row", nil, ""},
		{"no meta", &sources.CloudResourceRow{}, ""},
		{"empty meta", &sources.CloudResourceRow{Meta: json.RawMessage("")}, ""},
		{"unparseable meta", &sources.CloudResourceRow{Meta: json.RawMessage("{")}, ""},
		{"marker absent", &sources.CloudResourceRow{Meta: json.RawMessage(`{"VolumeSize":8}`)}, ""},
		{"marker not a string", &sources.CloudResourceRow{Meta: json.RawMessage(`{"nb_source":7}`)}, ""},
		{"billing", &sources.CloudResourceRow{Meta: json.RawMessage(`{"nb_source":"billing"}`)}, "billing"},
		{"api", &sources.CloudResourceRow{Meta: json.RawMessage(`{"nb_source":"api"}`)}, "api"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := awsResourceMetaSource(tc.row); got != tc.want {
				t.Fatalf("awsResourceMetaSource = %q, want %q", got, tc.want)
			}
		})
	}
}
