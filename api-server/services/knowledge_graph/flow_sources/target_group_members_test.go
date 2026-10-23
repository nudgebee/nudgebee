package flow_sources

import (
	"encoding/json"
	"testing"

	"nudgebee/services/config"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// tgFromJSON decodes a stored target group the way FetchLoadBalancerTargetGroups
// hands it to callers, so these tests exercise the same shape the real path does
// rather than a hand-built map.
func tgFromJSON(t *testing.T, raw string) map[string]interface{} {
	t.Helper()
	var tg map[string]interface{}
	require.NoError(t, json.Unmarshal([]byte(raw), &tg))
	return tg
}

// enableLBTargetsFromRows pins the kill switch for one test and restores it.
// config's init() applies the viper defaults on import, so the flag is already on
// in a test binary -- setting it explicitly keeps each test independent of that
// ambient state and lets the flag-off case be expressed directly.
func enableLBTargetsFromRows(t *testing.T, on bool) {
	t.Helper()
	prev := config.Config.FeatureKGLBTargetsFromRows
	config.Config.FeatureKGLBTargetsFromRows = on
	t.Cleanup(func() { config.Config.FeatureKGLBTargetsFromRows = prev })
}

// The payload below is copied from a real application_loadbalancer row's
// meta.TargetGroups entry. Instance ids are synthetic.
const storedTargetGroupJSON = `{
  "TargetGroupArn": "arn:aws:elasticloadbalancing:us-east-1:111122223333:targetgroup/web/abc",
  "TargetGroupName": "web",
  "TargetType": "instance",
  "TargetHealthDescriptions": [
    {"Target": {"Id": "i-0aaa", "Port": 80}, "TargetHealth": {"State": "healthy"}},
    {"Target": {"Id": "i-0bbb", "Port": 80}, "TargetHealth": {"State": "unhealthy"}}
  ]
}`

func TestTargetGroupMemberIDs_Hit(t *testing.T) {
	enableLBTargetsFromRows(t, true)

	ids, ok := TargetGroupMemberIDs(tgFromJSON(t, storedTargetGroupJSON))

	require.True(t, ok)
	assert.ElementsMatch(t, []string{"i-0aaa", "i-0bbb"}, ids)
}

// TestTargetGroupMemberIDs_UnhealthyTargetsAreStillMembers pins the point of the
// whole change: the caller wants membership, so a target's health must not filter
// it out. An unhealthy instance is still wired to the load balancer.
func TestTargetGroupMemberIDs_UnhealthyTargetsAreStillMembers(t *testing.T) {
	enableLBTargetsFromRows(t, true)

	ids, ok := TargetGroupMemberIDs(tgFromJSON(t, storedTargetGroupJSON))

	require.True(t, ok)
	assert.Contains(t, ids, "i-0bbb", "health state must not decide membership")
}

// TestTargetGroupMemberIDs_EmptyIsAHit is the most important assertion here.
// Returning false for a genuinely empty target group would send it to the CLI on
// every build, forever, to be told the same thing.
func TestTargetGroupMemberIDs_EmptyIsAHit(t *testing.T) {
	enableLBTargetsFromRows(t, true)

	ids, ok := TargetGroupMemberIDs(tgFromJSON(t, `{"TargetType":"instance","TargetHealthDescriptions":[]}`))

	require.True(t, ok, "an empty membership list is a real answer, not a miss")
	assert.Empty(t, ids)
}

func TestTargetGroupMemberIDs_Misses(t *testing.T) {
	tests := []struct {
		name string
		raw  string
	}{
		{"key absent -- never captured, or arrived over the CLI", `{"TargetType":"instance"}`},
		{"literal null", `{"TargetHealthDescriptions":null}`},
		{"not an array", `{"TargetHealthDescriptions":{"nope":1}}`},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			enableLBTargetsFromRows(t, true)

			ids, ok := TargetGroupMemberIDs(tgFromJSON(t, tc.raw))

			assert.False(t, ok)
			assert.Nil(t, ids)
		})
	}
}

// TestTargetGroupMemberIDs_SkipsMalformedEntries: a description missing its
// Target, or carrying a non-string id, is dropped without taking the rest of the
// target group with it.
func TestTargetGroupMemberIDs_SkipsMalformedEntries(t *testing.T) {
	enableLBTargetsFromRows(t, true)

	ids, ok := TargetGroupMemberIDs(tgFromJSON(t, `{
	  "TargetType": "instance",
	  "TargetHealthDescriptions": [
	    {"Target": {"Id": "i-0good"}},
	    {"TargetHealth": {"State": "healthy"}},
	    {"Target": {"Id": 42}},
	    {"Target": {"Id": ""}},
	    "not-an-object"
	  ]
	}`))

	require.True(t, ok)
	assert.Equal(t, []string{"i-0good"}, ids)
}

// TestTargetGroupMemberIDs_NonInstanceTargetsMiss: an ip-type group's members are
// pod or task IPs that change on every rollout, while the row only refreshes on
// full discovery. Serving them from the row would link the load balancer to pods
// that are gone, so only instance-type membership is a hit.
func TestTargetGroupMemberIDs_NonInstanceTargetsMiss(t *testing.T) {
	tests := []struct {
		name string
		raw  string
	}{
		{"ip", `{"TargetType":"ip","TargetHealthDescriptions":[{"Target":{"Id":"10.0.1.23"}}]}`},
		{"ip, empty", `{"TargetType":"ip","TargetHealthDescriptions":[]}`},
		{"lambda", `{"TargetType":"lambda","TargetHealthDescriptions":[{"Target":{"Id":"arn:aws:lambda:us-east-1:111122223333:function:f"}}]}`},
		{"type absent", `{"TargetHealthDescriptions":[{"Target":{"Id":"i-0aaa"}}]}`},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			enableLBTargetsFromRows(t, true)

			ids, ok := TargetGroupMemberIDs(tgFromJSON(t, tc.raw))

			assert.False(t, ok, "only instance-type membership is served from the row")
			assert.Nil(t, ids)
		})
	}
}

func TestTargetGroupMemberIDs_FlagOff(t *testing.T) {
	enableLBTargetsFromRows(t, false)

	ids, ok := TargetGroupMemberIDs(tgFromJSON(t, storedTargetGroupJSON))

	assert.False(t, ok, "the kill switch must make every lookup a permanent miss")
	assert.Nil(t, ids)
}
