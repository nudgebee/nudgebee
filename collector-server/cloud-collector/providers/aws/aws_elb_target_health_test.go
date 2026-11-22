package aws

import (
	"encoding/json"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/elasticloadbalancingv2"
	"github.com/aws/aws-sdk-go-v2/service/elasticloadbalancingv2/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestTargetHealthDescriptionsEmptyIsAnEmptyArray is the contract that makes the
// knowledge graph's row path work for target groups with no registered targets.
//
// The graph reads an ABSENT TargetHealthDescriptions key as "membership was never
// captured" and answers it with a live describe-target-health call. So an empty
// target group must be stored as `[]` — a real answer — and never as `null` or a
// missing key, or it shells out on every build forever to be told the same
// nothing. A nil slice would marshal to `null`, which the reader also rejects,
// hence the assertion on the serialised form rather than just the length.
func TestTargetHealthDescriptionsEmptyIsAnEmptyArray(t *testing.T) {
	out := &elasticloadbalancingv2.DescribeTargetHealthOutput{}

	got := targetHealthDescriptions(out)

	require.NotNil(t, got, "a nil slice would marshal to null, which the graph reads as a miss")
	assert.Empty(t, got)

	encoded, err := json.Marshal(got)
	require.NoError(t, err)
	assert.Equal(t, "[]", string(encoded), "an empty membership must serialise as [], not null")
}

func TestTargetHealthDescriptionsNilOutput(t *testing.T) {
	got := targetHealthDescriptions(nil)

	require.NotNil(t, got)
	assert.Empty(t, got)
}

// TestTargetHealthDescriptionsCarriesTargetIDs pins the one field the knowledge
// graph actually reads out of this payload. Health state is carried too, but
// nothing in the graph consumes it.
func TestTargetHealthDescriptionsCarriesTargetIDs(t *testing.T) {
	out := &elasticloadbalancingv2.DescribeTargetHealthOutput{
		TargetHealthDescriptions: []types.TargetHealthDescription{
			{
				Target:       &types.TargetDescription{Id: aws.String("i-0aaa"), Port: aws.Int32(80)},
				TargetHealth: &types.TargetHealth{State: types.TargetHealthStateEnumHealthy},
			},
			{
				Target:       &types.TargetDescription{Id: aws.String("i-0bbb"), Port: aws.Int32(80)},
				TargetHealth: &types.TargetHealth{State: types.TargetHealthStateEnumUnhealthy},
			},
		},
	}

	got := targetHealthDescriptions(out)

	require.Len(t, got, 2)

	encoded, err := json.Marshal(got)
	require.NoError(t, err)
	var decoded []map[string]interface{}
	require.NoError(t, json.Unmarshal(encoded, &decoded))

	ids := []string{}
	for _, description := range decoded {
		target, ok := description["Target"].(map[string]interface{})
		require.True(t, ok, "every description must carry a Target object")
		id, ok := target["Id"].(string)
		require.True(t, ok, "every Target must carry a string Id")
		ids = append(ids, id)
	}
	// Unhealthy targets are still members: the graph wires topology, not health.
	assert.Equal(t, []string{"i-0aaa", "i-0bbb"}, ids)
}
