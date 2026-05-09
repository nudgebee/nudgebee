package tools

import (
	"nudgebee/llm/security"
	"nudgebee/llm/tools/core"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestServerIdentifyConfig(t *testing.T) {
	tool := ServerExecuteTool{}
	ctx := core.NbToolContext{}

	configs := []core.ToolConfig{
		{
			Name: "prod-ssh",
			Values: []core.ToolConfigValue{
				{Name: "host", Value: "prod-ssh.example.com"},
			},
		},
		{
			Name: "dev-ssh",
			Values: []core.ToolConfigValue{
				{Name: "host", Value: "dev-ssh, dev-ssh-.*"},
			},
		},
	}

	t.Run("Identify by Name (Exact)", func(t *testing.T) {
		req := core.NBToolCallRequest{
			Arguments: map[string]any{
				"instance": "prod-ssh",
			},
		}
		cfg, err := tool.IdentifyConfig(ctx, req, configs)
		assert.NoError(t, err)
		assert.Equal(t, "prod-ssh", cfg.Name)
	})

	t.Run("Identify by Host Pattern (Simple)", func(t *testing.T) {
		req := core.NBToolCallRequest{
			Arguments: map[string]any{
				"instance": "dev-ssh",
			},
		}
		cfg, err := tool.IdentifyConfig(ctx, req, configs)
		assert.NoError(t, err)
		assert.Equal(t, "dev-ssh", cfg.Name)
	})

	t.Run("Identify by Host Pattern (Regex)", func(t *testing.T) {
		req := core.NBToolCallRequest{
			Arguments: map[string]any{
				"instance": "dev-ssh-01",
			},
		}
		cfg, err := tool.IdentifyConfig(ctx, req, configs)
		assert.NoError(t, err)
		assert.Equal(t, "dev-ssh", cfg.Name)
	})

	t.Run("Identify by Command JSON (Fallback)", func(t *testing.T) {
		req := core.NBToolCallRequest{
			Command: `{"instance": "dev-ssh-02", "args": "ls"}`,
		}
		cfg, err := tool.IdentifyConfig(ctx, req, configs)
		assert.NoError(t, err)
		assert.Equal(t, "dev-ssh", cfg.Name)
	})

	t.Run("Case Insensitive Match (Name)", func(t *testing.T) {
		req := core.NBToolCallRequest{
			Arguments: map[string]any{
				"instance": "PROD-SSH",
			},
		}
		cfg, err := tool.IdentifyConfig(ctx, req, configs)
		assert.NoError(t, err)
		assert.Equal(t, "prod-ssh", cfg.Name)
	})

	t.Run("Empty instance returns error", func(t *testing.T) {
		req := core.NBToolCallRequest{
			Command: `{"args": "ls"}`,
		}
		_, err := tool.IdentifyConfig(ctx, req, configs)
		assert.Error(t, err)
		assert.Equal(t, "missing instance json field", err.Error())
	})
}

func TestServerCallParsing(t *testing.T) {
	tool := ServerExecuteTool{}
	sc := security.NewRequestContextForSuperAdmin()
	ctx := core.NbToolContext{
		Ctx: sc,
	}

	t.Run("Parse from Arguments", func(t *testing.T) {
		req := core.NBToolCallRequest{
			Arguments: map[string]any{
				"instance": "dev-ssh",
				"args":     "ls",
				"command":  "shell",
			},
		}
		// It will still fail in executeShellCommand because ctx.ToolConfig is empty and no DB is available
		// but we check if it gets past the "missing args or instance" check.
		_, err := tool.Call(ctx, req)
		assert.Error(t, err)
		assert.NotEqual(t, "missing args or instance field", err.Error())
	})

	t.Run("Parse from JSON Command", func(t *testing.T) {
		req := core.NBToolCallRequest{
			Command: `{"instance": "dev-ssh", "args": "ls", "command": "shell"}`,
		}
		_, err := tool.Call(ctx, req)
		assert.Error(t, err)
		assert.NotEqual(t, "missing args or instance field", err.Error())
		assert.NotContains(t, err.Error(), "invalid request format")
	})
}
