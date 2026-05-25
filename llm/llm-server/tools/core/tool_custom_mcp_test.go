package core

import (
	"nudgebee/llm/security"
	"os"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
)

func TestAgentMCP(t *testing.T) {

	testAccountId := os.Getenv("TEST_ACCOUNT")
	testTenantId := os.Getenv("TEST_TENANT")
	testUserId := os.Getenv("TEST_USER")
	sc := security.NewRequestContextForTenantAccountAdmin(testTenantId, testUserId, []string{testAccountId})
	newUUID := uuid.NewString()

	nbCustomMCPTool := nbCustomMCPTool{
		tool: ToolDto{
			Id:           newUUID,
			Name:         "github",
			Type:         ToolTypeCustom,
			ExecutorType: ToolExecutorTypeMCP,
			NBToolType:   NBToolTypeTool,
			Config: map[string]any{
				ToolCustomMcpServerType:       ToolCustomMcpServerTypeCli,
				ToolCustomMcpServerCliCommand: "npx",
				ToolCustomMcpServerCliArgs:    []string{"-y", "@modelcontextprotocol/server-filesystem", "./"},
			},
		},
	}

	toolContext := NewNbToolContext(sc, nbCustomMCPTool, testAccountId, testUserId, uuid.NewString(), uuid.NewString(), "", "", nil, "", NBQueryConfig{}, "1")

	commands, err := nbCustomMCPTool.GetSubCommands()
	assert.Nil(t, err)
	assert.Equal(t, 11, len(commands))

	reponse, err := nbCustomMCPTool.Call(toolContext, NBToolCallRequest{
		Command: "list_directory",
		Arguments: map[string]any{
			"path": "./",
		},
	})
	assert.Nil(t, err)
	assert.NotEmpty(t, reponse.Data)

	reponse, err = nbCustomMCPTool.Call(toolContext, NBToolCallRequest{
		Command: "list_directory1",
		Arguments: map[string]any{
			"path": "./",
		},
	})
	assert.NotNil(t, err)
	assert.Nil(t, err)
	assert.Equal(t, reponse.Status, NBToolResponseStatusError)
	assert.NotEmpty(t, reponse.Data)
}

func TestAgentMCP_HttpCrawl(t *testing.T) {

	testAccountId := os.Getenv("TEST_ACCOUNT")
	testTenantId := os.Getenv("TEST_TENANT")
	testUserId := os.Getenv("TEST_USER")
	sc := security.NewRequestContextForTenantAccountAdmin(testTenantId, testUserId, []string{testAccountId})
	newUUID := uuid.NewString()

	nbCustomMCPTool := nbCustomMCPTool{
		tool: ToolDto{
			Id:           newUUID,
			Name:         "http_crawl",
			Type:         ToolTypeCustom,
			ExecutorType: ToolExecutorTypeMCP,
			NBToolType:   NBToolTypeTool,
			Config: map[string]any{
				ToolCustomMcpServerType:    ToolCustomMcpServerTypeHttp,
				ToolCustomMcpServerHttpUrl: "https://remote.mcpservers.org/fetch/mcp",
			},
		},
	}

	toolContext := NewNbToolContext(sc, nbCustomMCPTool, testAccountId, testUserId, uuid.NewString(), uuid.NewString(), "", "", nil, "", NBQueryConfig{}, "1")

	commands, err := nbCustomMCPTool.GetSubCommands()
	assert.Nil(t, err)
	assert.Equal(t, 1, len(commands))

	reponse, err := nbCustomMCPTool.Call(toolContext, NBToolCallRequest{
		Command: "fetch",
		Arguments: map[string]any{
			"url": "https://en.m.wikipedia.org/wiki/Scion_of_Ikshvaku",
		},
	})
	assert.Nil(t, err)
	assert.NotEmpty(t, reponse.Data)
	println(reponse.Data)
}
