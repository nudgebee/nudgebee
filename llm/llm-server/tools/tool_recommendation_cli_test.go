package tools

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"strings"
	"testing"

	"nudgebee/llm/security"
	"nudgebee/llm/tools/core"

	"github.com/stretchr/testify/assert"
)

func TestDestructiveCommands(t *testing.T) {
	cases := []struct {
		command     string
		destructive bool
	}{
		{"aws ec2 delete-volume --volume-id vol-123 --region us-east-1", true},
		{"aws ec2 terminate-instances --instance-ids i-123", true},
		{"az keyvault purge --name kv1", true},
		{"gcloud compute instances delete my-vm --zone us-central1-a", true},
		{"aws ec2 deregister-image --image-id ami-123", true},
		{"az network vnet subnet remove --name s1", true},
		{"aws s3 rm s3://my-bucket/data --recursive", true},
		{"aws s3 rb s3://my-bucket --force", true},
		{"aws ec2 release-address --allocation-id eipalloc-123", true},
		{`aws cloudwatch put-metric-alarm --alarm-name x --alarm-description "delete unused later"`, false},
		{`az monitor metrics alert create --name a --description "remove after Q3"`, false},
		{"aws cloudwatch put-metric-alarm --alarm-name high-cpu", false},
		{"aws ec2 modify-volume --volume-id vol-123 --volume-type gp3", false},
		{"aws ec2 describe-volumes --region us-east-1", false},
		{"aws rds modify-db-instance --db-instance-identifier db1", false},
	}
	for _, c := range cases {
		got := len(destructiveCommands([]string{c.command})) > 0
		assert.Equal(t, c.destructive, got, c.command)
	}
}

// The tool refuses destructive commands without acknowledged risk BEFORE any
// dispatch to api-server — the gate lives in the tool, not in the model's
// reading of the prompt.
func TestRecommendationCliTool_DestructiveGate(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	secCtx := security.NewSecurityContextForSuperAdmin()
	reqCtx := security.NewRequestContext(context.Background(), secCtx, logger, nil, nil)
	nbCtx := core.NbToolContext{Ctx: reqCtx}

	args := func(ack any) map[string]any {
		a := map[string]any{
			"commands":          []any{"aws ec2 delete-volume --volume-id vol-047db3ffd77d73e24 --region us-east-1"},
			"recommendation_id": "fe8bf9b2-b599-440a-8de4-194e563ce83e",
		}
		if ack != nil {
			a["acknowledge_risk"] = ack
		}
		return a
	}

	resp, err := RecommendationCliTool{}.Call(nbCtx, core.NBToolCallRequest{Arguments: args(nil)})
	assert.NoError(t, err)
	assert.Equal(t, core.NBToolResponseStatusError, resp.Status)
	text, _ := json.Marshal(resp.Data)
	assert.Contains(t, string(text), "acknowledge_risk",
		"the refusal must name the flag so the agent knows the re-call shape")
	assert.Contains(t, string(text), "safety band",
		"the refusal must demand the safety presentation, or the gate teaches nothing")

	resp, err = RecommendationCliTool{}.Call(nbCtx, core.NBToolCallRequest{Arguments: args(false)})
	assert.NoError(t, err)
	assert.Equal(t, core.NBToolResponseStatusError, resp.Status,
		"an explicit false is not an acknowledgement")
}

func TestRecommendationCliTool_ConfirmationWarnsDestructive(t *testing.T) {
	input, _ := json.Marshal(map[string]any{
		"commands": []string{"aws ec2 delete-volume --volume-id vol-123 --region us-east-1"},
	})
	q := RecommendationCliTool{}.ConfirmationQuestion(string(input))
	assert.Contains(t, q, "DESTRUCTIVE", "the approver must see the destruction named, not just a command string")
	assert.Contains(t, q, "snapshot", "volume deletion must carry the snapshot-first hint")

	input, _ = json.Marshal(map[string]any{
		"commands": []string{"aws cloudwatch put-metric-alarm --alarm-name high-cpu"},
	})
	q = RecommendationCliTool{}.ConfirmationQuestion(string(input))
	assert.False(t, strings.Contains(q, "DESTRUCTIVE"), "non-destructive commands must not cry wolf")
}

func TestSafetyFactsForRefusal_RejectsNonUUID(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	secCtx := security.NewSecurityContextForSuperAdmin()
	reqCtx := security.NewRequestContext(context.Background(), secCtx, logger, nil, nil)
	nbCtx := core.NbToolContext{Ctx: reqCtx}

	assert.Equal(t, "", safetyFactsForRefusal(nbCtx, "1'; DROP TABLE recommendation; --"))
	assert.Equal(t, "", safetyFactsForRefusal(nbCtx, ""))
}
