package llm

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// Bedrock inference profiles are configured as full ARNs, which already state
// their region. llm-server forwards no separate region on deployments whose
// secret sets no LLM_PROVIDER_REGION, so without this the AWS client would be
// built with no region at all.
func TestRegionFromModelARN(t *testing.T) {
	tests := []struct {
		name  string
		model string
		want  string
	}{
		{
			name:  "inference profile ARN",
			model: "arn:aws:bedrock:us-west-2:864186153326:inference-profile/us.meta.llama4-maverick-17b-instruct-v1:0",
			want:  "us-west-2",
		},
		{
			name:  "imported model ARN",
			model: "arn:aws:bedrock:eu-central-1:1234:imported-model/abc123",
			want:  "eu-central-1",
		},
		{
			name:  "bare model id has no region",
			model: "anthropic.claude-3-5-sonnet-20241022-v2:0",
			want:  "",
		},
		{
			name:  "empty model",
			model: "",
			want:  "",
		},
		{
			name:  "truncated ARN is not guessed at",
			model: "arn:aws:bedrock:us-west-2",
			want:  "",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, regionFromModelARN(tt.model))
		})
	}
}
