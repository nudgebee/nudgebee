package core

import (
	"nudgebee/llm/security"
	"os"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
)

func TestValidateMCPURL(t *testing.T) {
	tests := []struct {
		name    string
		url     string
		wantErr bool
	}{
		{"valid external HTTPS", "https://google.com/mcp", false},
		{"valid external HTTP", "http://google.com/mcp", false},
		{"loopback IPv4", "http://127.0.0.1:8080/mcp", true},
		{"loopback localhost", "http://localhost:8080/mcp", true},
		{"private 10.x", "http://10.0.0.1:8080/mcp", true},
		{"private 172.16.x", "http://172.16.0.1:8080/mcp", true},
		{"private 192.168.x", "http://192.168.1.1:8080/mcp", true},
		{"link-local metadata", "http://169.254.169.254/latest/meta-data/", true},
		{"IPv6 loopback", "http://[::1]:8080/mcp", true},
		{"unsupported scheme ftp", "ftp://example.com/file", true},
		{"unsupported scheme file", "file:///etc/passwd", true},
		{"unspecified 0.0.0.0", "http://0.0.0.0:8080/mcp", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateMCPURL(tt.url)
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

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
