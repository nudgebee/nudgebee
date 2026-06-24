package core

import (
	"context"
	"net"
	"nudgebee/llm/security"
	"os"
	"testing"
	"time"

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

func TestPinnedSafeDialContext_LiteralRestrictedIP(t *testing.T) {
	tests := []struct {
		name string
		addr string
	}{
		{"loopback v4", "127.0.0.1:80"},
		{"loopback v6", "[::1]:80"},
		{"private 10.x", "10.0.0.1:80"},
		{"link-local metadata", "169.254.169.254:80"},
		{"unspecified", "0.0.0.0:80"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			conn, err := pinnedSafeDialContext(ctx, "tcp", tt.addr)
			if conn != nil {
				_ = conn.Close()
			}
			assert.Error(t, err)
			assert.Contains(t, err.Error(), "restricted IP")
		})
	}
}

func TestPinnedSafeDialContext_UnresolvableHostname(t *testing.T) {
	// Hostnames that fail to resolve should surface a wrapped resolve error,
	// not a panic or a nil-deref.
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_, err := pinnedSafeDialContext(ctx, "tcp", "this-host-does-not-exist-nb-test.invalid:80")
	assert.Error(t, err)
}

func TestPinnedSafeDialContext_ResolvedRestrictedIP(t *testing.T) {
	// Use a hostname (`localhost`) so the DNS-resolution + post-resolve
	// validation branch runs instead of the literal-IP fast path. `localhost`
	// resolves to a loopback address, which isRestrictedIP rejects.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Skip("could not allocate listener:", err)
	}
	defer func() { _ = ln.Close() }()
	_, port, err := net.SplitHostPort(ln.Addr().String())
	assert.NoError(t, err)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	conn, err := pinnedSafeDialContext(ctx, "tcp", net.JoinHostPort("localhost", port))
	if conn != nil {
		_ = conn.Close()
	}
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "restricted IP")
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
