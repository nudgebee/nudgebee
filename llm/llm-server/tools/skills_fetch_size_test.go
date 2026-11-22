package tools

import (
	"testing"

	"nudgebee/llm/config"

	"github.com/stretchr/testify/require"
)

func TestRagSkillFetchSizeFollowsJudge(t *testing.T) {
	prev := config.Config.KBRerankEnabled
	t.Cleanup(func() { config.Config.KBRerankEnabled = prev })

	config.Config.KBRerankEnabled = false
	require.Equal(t, ragSkillTopK, ragSkillFetchSize())

	config.Config.KBRerankEnabled = true
	require.Equal(t, 3*ragSkillTopK, ragSkillFetchSize())
}
