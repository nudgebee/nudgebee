package aws

import (
	"encoding/json"
	"testing"

	"nudgebee/services/config"
	"nudgebee/services/knowledge_graph/sources"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const testLBARN = "arn:aws:elasticloadbalancing:us-east-1:111122223333:loadbalancer/app/web/abc"

// enableLBTargetsFromRowsForTest pins the kill switch for one test and restores
// it. config's init() applies the viper defaults on import, so the flag is already
// on in a test binary -- setting it explicitly keeps each test independent of that
// ambient state and lets the flag-off case be expressed directly.
func enableLBTargetsFromRowsForTest(t *testing.T, on bool) {
	t.Helper()
	prev := config.Config.FeatureKGLBTargetsFromRows
	config.Config.FeatureKGLBTargetsFromRows = on
	t.Cleanup(func() { config.Config.FeatureKGLBTargetsFromRows = prev })
}

// lbSourceWithMeta builds an AWSSource whose row cache holds one load balancer,
// the same way fillRowCache does during a real build. The row's resourse_id is the
// load balancer ARN, which is what it is on every active row.
func lbSourceWithMeta(t *testing.T, lbType, metaJSON string) *AWSSource {
	t.Helper()
	src, err := NewAWSSource(AWSSourceConfig{}, nil)
	require.NoError(t, err)
	src.metaByTypeAndID = map[string]map[string]sources.CloudResourceRow{
		lbType: {testLBARN: {
			ResourceID: testLBARN, Type: lbType, Region: "us-east-1",
			Meta: json.RawMessage(metaJSON),
		}},
	}
	return src
}

const lbMetaWithTargetsJSON = `{
  "LoadBalancerArn": "arn:aws:elasticloadbalancing:us-east-1:111122223333:loadbalancer/app/web/abc",
  "TargetGroups": [
    {"TargetGroupArn": "...targetgroup/web/aaa", "TargetType": "instance",
     "TargetHealthDescriptions": [
       {"Target": {"Id": "i-0aaa"}, "TargetHealth": {"State": "healthy"}},
       {"Target": {"Id": "i-0bbb"}, "TargetHealth": {"State": "unhealthy"}}
     ]},
    {"TargetGroupArn": "...targetgroup/pods/bbb", "TargetType": "ip",
     "TargetHealthDescriptions": [{"Target": {"Id": "10.0.1.23"}}]}
  ]
}`

// TestLBTargetInstanceIDsFromRow_Hit: the row answers, unhealthy targets are still
// members, and ip-type target groups are excluded here (this edge builder only
// wires instance targets).
func TestLBTargetInstanceIDsFromRow_Hit(t *testing.T) {
	defer disableCloudCLIForTest()()
	enableLBTargetsFromRowsForTest(t, true)
	src := lbSourceWithMeta(t, "application_loadbalancer", lbMetaWithTargetsJSON)

	ids, ok := src.lbTargetInstanceIDsFromRow(testLBARN)

	require.True(t, ok)
	assert.ElementsMatch(t, []string{"i-0aaa", "i-0bbb"}, ids)
	assert.NotContains(t, ids, "10.0.1.23", "ip-type targets are not instance targets")
}

func TestLBTargetInstanceIDsFromRow_NetworkLoadBalancer(t *testing.T) {
	defer disableCloudCLIForTest()()
	enableLBTargetsFromRowsForTest(t, true)
	src := lbSourceWithMeta(t, "network_loadbalancer", lbMetaWithTargetsJSON)

	ids, ok := src.lbTargetInstanceIDsFromRow(testLBARN)

	require.True(t, ok)
	assert.ElementsMatch(t, []string{"i-0aaa", "i-0bbb"}, ids)
}

// TestLBTargetInstanceIDsFromRow_EmptyTargetGroupsIsAHit: a load balancer with no
// target groups is a real answer. Missing it would put an idle load balancer on
// the CLI on every build.
func TestLBTargetInstanceIDsFromRow_EmptyTargetGroupsIsAHit(t *testing.T) {
	defer disableCloudCLIForTest()()
	enableLBTargetsFromRowsForTest(t, true)
	src := lbSourceWithMeta(t, "application_loadbalancer", `{"TargetGroups": []}`)

	ids, ok := src.lbTargetInstanceIDsFromRow(testLBARN)

	require.True(t, ok, "an empty target group list is a real answer")
	assert.Empty(t, ids)
}

// TestLBTargetInstanceIDsFromRow_PartialMembershipFallsBack: if any instance-type
// target group has no captured membership, the whole load balancer goes to the
// CLI. A partial list would look like a load balancer that had lost targets and
// would tombstone their edges.
func TestLBTargetInstanceIDsFromRow_PartialMembershipFallsBack(t *testing.T) {
	defer disableCloudCLIForTest()()
	enableLBTargetsFromRowsForTest(t, true)
	src := lbSourceWithMeta(t, "application_loadbalancer", `{
	  "TargetGroups": [
	    {"TargetType": "instance", "TargetHealthDescriptions": [{"Target": {"Id": "i-0aaa"}}]},
	    {"TargetType": "instance"}
	  ]
	}`)

	ids, ok := src.lbTargetInstanceIDsFromRow(testLBARN)

	assert.False(t, ok, "a partially captured load balancer must fall back, not report a short list")
	assert.Nil(t, ids)
}

func TestLBTargetInstanceIDsFromRow_Misses(t *testing.T) {
	tests := []struct {
		name string
		meta string
	}{
		{"TargetGroups key absent -- never captured", `{"LoadBalancerArn":"x"}`},
		{"malformed meta", `{"TargetGroups": "nope"}`},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			defer disableCloudCLIForTest()()
			enableLBTargetsFromRowsForTest(t, true)
			src := lbSourceWithMeta(t, "application_loadbalancer", tc.meta)

			ids, ok := src.lbTargetInstanceIDsFromRow(testLBARN)

			assert.False(t, ok)
			assert.Nil(t, ids)
		})
	}
}

func TestLBTargetInstanceIDsFromRow_NoRowForLB(t *testing.T) {
	defer disableCloudCLIForTest()()
	enableLBTargetsFromRowsForTest(t, true)
	src := lbSourceWithMeta(t, "application_loadbalancer", lbMetaWithTargetsJSON)

	_, ok := src.lbTargetInstanceIDsFromRow("arn:aws:elasticloadbalancing:us-east-1:1:loadbalancer/app/other/zzz")

	assert.False(t, ok, "an unknown load balancer must fall through to the CLI")
}

func TestLBTargetInstanceIDsFromRow_FlagOff(t *testing.T) {
	defer disableCloudCLIForTest()()
	enableLBTargetsFromRowsForTest(t, false)
	src := lbSourceWithMeta(t, "application_loadbalancer", lbMetaWithTargetsJSON)

	_, ok := src.lbTargetInstanceIDsFromRow(testLBARN)

	assert.False(t, ok, "the kill switch must make every lookup a permanent miss")
}

// TestFetchLBTargetInstanceIDs_PrefersRowOverCLI is the end of the chain: with the
// cloud collector unreachable, the exported path still returns targets, which it
// could only do from the row.
func TestFetchLBTargetInstanceIDs_PrefersRowOverCLI(t *testing.T) {
	defer disableCloudCLIForTest()()
	enableLBTargetsFromRowsForTest(t, true)
	src := lbSourceWithMeta(t, "application_loadbalancer", lbMetaWithTargetsJSON)

	ids := src.fetchLBTargetInstanceIDs(nil, "acct-1", "us-east-1", testLBARN)

	assert.ElementsMatch(t, []string{"i-0aaa", "i-0bbb"}, ids)
}
