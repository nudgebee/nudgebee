package core

import (
	"testing"

	toolcore "nudgebee/llm/tools/core"

	"github.com/stretchr/testify/assert"
)

// A wrong-target answer is well-formed and grounded in real numbers from the
// wrong system, so nothing downstream notices. Recording which config answered,
// and how it was chosen, is what makes it visible.
func TestWithResolvedToolTarget(t *testing.T) {
	t.Run("the strategy is added only to a call that recorded a target", func(t *testing.T) {
		qc := toolcore.NBQueryConfig{
			ToolConfigs: map[string]string{"postgres_query_execute": "dev-pg"},
			ToolConfigMetadata: map[string]any{
				"postgres_query_execute": map[string]any{"strategy": "tool_identify"},
			},
		}
		ran := map[string]any{"existing": 1, toolcore.MetadataResolvedTarget: "dev-pg"}
		out := withResolvedToolTarget(ran, qc, "postgres_query_execute")
		assert.Equal(t, "dev-pg", out[toolcore.MetadataResolvedTarget])
		assert.Equal(t, "tool_identify", out[toolcore.MetadataResolvedBy])
		assert.Equal(t, 1, out["existing"], "must not drop details the tool already reported")
	})

	// Some tools put their own "tool_config_name" in additionalDetails — watch's
	// poll contract, and the "NoConfigToolsFound" sentinel. That key is a tool's
	// own value, not a record of what ran, so it must not be read as one.
	t.Run("a tool's own tool_config_name is not a resolved target", func(t *testing.T) {
		qc := toolcore.NBQueryConfig{
			ToolConfigs:        map[string]string{"watch": "dev-pg"},
			ToolConfigMetadata: map[string]any{"watch": map[string]any{"strategy": "tool_identify"}},
		}
		own := map[string]any{"tool_config_name": "NoConfigToolsFound"}
		out := withResolvedToolTarget(own, qc, "watch")
		assert.NotContains(t, out, toolcore.MetadataResolvedBy)
		assert.Equal(t, "NoConfigToolsFound", out["tool_config_name"], "the tool's own key is left alone")
	})

	// A parent orchestrator's map can name a config its call never used. Adding
	// that name here would label the wrong target as the one that ran.
	t.Run("a stale request map never invents a target", func(t *testing.T) {
		qc := toolcore.NBQueryConfig{ToolConfigs: map[string]string{"postgres_query_execute": "dev-pg"}}
		assert.Nil(t, withResolvedToolTarget(nil, qc, "postgres_query_execute"))
	})
}
