package core

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"nudgebee/llm/common"
	"time"

	"github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/client/transport"
	"github.com/mark3labs/mcp-go/mcp"
)

// noRedirectMCPClient disables HTTP redirects so the MCP server can't redirect
// the client to an internal/private URL after the initial validateMCPURL check.
// Any 3xx response is surfaced to the caller instead of followed.
var noRedirectMCPClient = &http.Client{
	CheckRedirect: func(req *http.Request, via []*http.Request) error {
		return http.ErrUseLastResponse
	},
}

const ToolCustomMcpServerType = "mcp_server_type"
const ToolCustomMcpServerTypeCli = "cli"
const ToolCustomMcpServerTypeHttp = "http"

const ToolCustomMcpServerCliCommand = "mcp_cli_command"
const ToolCustomMcpServerCliArgs = "mcp_cli_args"
const ToolCustomMcpServerCliEnv = "mcp_cli_env"
const ToolCustomMcpServerHttpUrl = "mcp_http_url"
const ToolCustomMcpServerHttpHeaders = "mcp_http_headers"

type nbCustomMCPTool struct {
	tool ToolDto
}

func (m nbCustomMCPTool) Name() string {
	return m.tool.Name
}

func (m nbCustomMCPTool) Description() string {
	return m.tool.Description
}

func (m nbCustomMCPTool) InputSchema() ToolSchema {
	if m.tool.InputSchema.Type == "" {
		return ToolSchema{
			Type: ToolSchemaTypeObject,
			Properties: map[string]ToolSchemaProperty{
				"command": {
					Type:        ToolSchemaTypeString,
					Description: "REQUIRED: The specific MCP operation name to execute (e.g., 'search_code', 'get_repo'). See tool description for available commands.",
				},
				"args": {
					Type:        ToolSchemaTypeObject,
					Description: "OPTIONAL: A JSON object containing arguments for the specified 'command'. Structure depends on the command.",
				},
			},
			Required: []string{"command"},
		}
	}
	return m.tool.InputSchema
}

func (m nbCustomMCPTool) GetExecutorType() ToolExecutorType {
	return ToolExecutorTypeMCP
}

func (m nbCustomMCPTool) GetType() NBToolType {
	if m.tool.NBToolType != "" {
		return m.tool.NBToolType
	}
	return NBToolTypeTool
}

func (m nbCustomMCPTool) GetSubCommands() ([]NBToolCommand, error) {
	client, err := m.getMcpClient()
	if err != nil {
		return []NBToolCommand{}, err
	}
	defer func() {
		if err := client.Close(); err != nil {
			slog.Error("mcp: failed to close client", "error", err)
		}
	}()

	_, err = client.Initialize(context.Background(), mcp.InitializeRequest{})
	if err != nil {
		return []NBToolCommand{}, err
	}

	tools, err := client.ListTools(context.Background(), mcp.ListToolsRequest{})
	if err != nil {
		return []NBToolCommand{}, err
	}
	commands := make([]NBToolCommand, 0, len(tools.Tools))

	for _, t := range tools.Tools {
		props := make(map[string]ToolSchemaProperty, len(t.InputSchema.Properties))
		for k, v := range t.InputSchema.Properties {
			dataMap := v.(map[string]any)
			descripton := ""
			if dataMap["description"] != nil {
				descripton = dataMap["description"].(string)
			}
			type1 := ToolSchemaTypeString
			if dataMap["type"] != nil {
				type1 = ToolSchemaType(dataMap["type"].(string))
				if type1 == "" {
					type1 = ToolSchemaTypeString
				}
			}
			props[k] = ToolSchemaProperty{
				Type:        type1,
				Description: descripton,
			}
		}

		commands = append(commands, NBToolCommand{
			Name:        t.Name,
			Description: t.Description,
			InputSchema: ToolSchema{
				Type:       ToolSchemaType(t.InputSchema.Type),
				Required:   t.InputSchema.Required,
				Properties: props,
			},
		})
	}

	return commands, nil
}

// validateMCPURL prevents SSRF by rejecting URLs that resolve to internal/private networks.
func validateMCPURL(rawURL string) error {
	u, err := url.Parse(rawURL)
	if err != nil {
		return fmt.Errorf("invalid URL: %w", err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Errorf("unsupported scheme %q: only http and https are allowed", u.Scheme)
	}
	host, _, err := net.SplitHostPort(u.Host)
	if err != nil {
		host = u.Host
	}
	if host == "" {
		return fmt.Errorf("empty hostname")
	}
	ips, err := net.LookupIP(host)
	if err != nil {
		return fmt.Errorf("unable to resolve host %q: %w", host, err)
	}
	if len(ips) == 0 {
		return fmt.Errorf("host %q resolved to zero addresses", host)
	}
	for _, ip := range ips {
		if ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsUnspecified() || ip.IsMulticast() {
			return fmt.Errorf("URL resolves to a restricted IP address: %s", ip.String())
		}
	}
	return nil
}

func (m nbCustomMCPTool) getMcpClient() (*client.Client, error) {
	mcperverType := m.tool.Config[ToolCustomMcpServerType]
	if mcperverType == nil {
		mcperverType = ToolCustomMcpServerTypeCli
	}

	var mcpClient *client.Client
	var err error
	switch mcperverType {
	case ToolCustomMcpServerTypeCli:
		config := m.tool.Config
		if config == nil {
			config = make(map[string]any)
		}
		cliCommand := config[ToolCustomMcpServerCliCommand]
		if cliCommand == nil {
			return mcpClient, errors.New("invalid config for mcp")
		}
		cliCommandStr, ok := cliCommand.(string)
		if !ok {
			return mcpClient, errors.New("invalid config for mcp")
		}
		if cliCommandStr == "" {
			return mcpClient, errors.New("invalid config for mcp")
		}

		cliCommandAnyStr := []string{}

		cliCommandArgsAny := config[ToolCustomMcpServerCliArgs]
		if cliCommandArgsAny != nil {
			cliCommandAny, ok := cliCommandArgsAny.([]any)
			if ok {
				cliCommandAnyStr = make([]string, len(cliCommandAny))
				for i, arg := range cliCommandAny {
					cliCommandAnyStr[i] = arg.(string)
				}
			} else {
				cliCommandAnyStr, ok = cliCommandArgsAny.([]string)
				if !ok {
					return mcpClient, errors.New("invalid config for mcp")
				}
			}

		}

		cliEnvStr := []string{}

		cliCommandEnvAny := config[ToolCustomMcpServerCliEnv]
		if cliCommandEnvAny != nil {
			cliCommandEnvMap, ok := cliCommandEnvAny.(map[string]any)
			if ok {
				for k, v := range cliCommandEnvMap {
					cliEnvStr = append(cliEnvStr, k+"="+v.(string))
				}
			}
		}

		mcpClient, err = client.NewStdioMCPClient(
			cliCommandStr,
			cliEnvStr,
			cliCommandAnyStr...,
		)
		if err != nil {
			slog.Error("mcp: unable to create client", "error", err)
			return mcpClient, errors.New("mcp: unable to create client " + err.Error())
		}
		return mcpClient, nil
	case ToolCustomMcpServerTypeHttp:
		config := m.tool.Config
		if config == nil {
			config = make(map[string]any)
		}
		httpUrl := config[ToolCustomMcpServerHttpUrl]
		if httpUrl == nil {
			return mcpClient, errors.New("invalid config for mcp")
		}
		httpUrlStr, ok := httpUrl.(string)
		if !ok {
			return mcpClient, errors.New("invalid config for mcp")
		}

		if err := validateMCPURL(httpUrlStr); err != nil {
			return mcpClient, fmt.Errorf("mcp: blocked HTTP URL: %w", err)
		}

		httpHeaders := map[string]string{}

		toolCustomMcpServerHttpHeadersAny := config[ToolCustomMcpServerHttpHeaders]
		if toolCustomMcpServerHttpHeadersAny != nil {
			httpHeadersAny, ok := toolCustomMcpServerHttpHeadersAny.(map[string]any)
			if !ok {
				return mcpClient, errors.New("invalid config for mcp")
			}

			httpHeaders = make(map[string]string, len(httpHeadersAny))
			for i, arg := range httpHeadersAny {
				httpHeaders[i] = arg.(string)
			}
		}

		if httpHeaders["Content-Type"] == "" && httpHeaders["content-type"] == "" {
			httpHeaders["Content-Type"] = "application/json, text/event-stream"
		}

		if httpHeaders["Accept"] == "" && httpHeaders["accept"] == "" {
			httpHeaders["Accept"] = "application/json, text/event-stream"
		}

		mcpClient, err = client.NewStreamableHttpClient(
			httpUrlStr,
			transport.WithHTTPHeaders(httpHeaders),
			transport.WithHTTPBasicClient(noRedirectMCPClient),
		)
		if err != nil {
			slog.Error("mcp: unable to create client", "error", err)
			return mcpClient, errors.New("mcp: unable to create client " + err.Error())
		}
		return mcpClient, nil
	}
	return nil, errors.New("mcp: unsupported MCP client:" + mcperverType.(string))
}

// Call executes the MCP operation defined by the input against the configured target.
func (m nbCustomMCPTool) Call(ctx NbToolContext, input NBToolCallRequest) (NBToolResponse, error) {
	log := ctx.Ctx.GetLogger().With("toolName", m.Name(), "toolId", m.tool.Id, "accountId", ctx.AccountId, "userId", ctx.UserId, "toolCallId", ctx.ToolCallId)
	log.Info("mcp: Executing MCP tool", "input", input)

	// Extract operation (required by default schema)
	if input.Command == "" {
		log.Warn("Input 'command' field is not a non-empty string")
		return NBToolResponse{Status: NBToolResponseStatusError, Data: "'command' field must be a non-empty string.", Type: NBToolResponseTypeText}, nil
	}

	mcpArgs := input.Arguments

	mcpClient, err := m.getMcpClient()
	if err != nil {
		log.Error("mcp: failed to create client:", "error", err)
		return NBToolResponse{}, err
	}

	defer func() {
		if err := mcpClient.Close(); err != nil {
			slog.Error("mcp: failed to close mcpClient", "error", err)
		}
	}()

	// Create context with timeout
	ctx1, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Initialize the client
	initRequest := mcp.InitializeRequest{}
	initRequest.Params.ProtocolVersion = mcp.LATEST_PROTOCOL_VERSION
	initRequest.Params.ClientInfo = mcp.Implementation{
		Name:    "llm-server-mcp-client",
		Version: "1.0.0",
	}

	_, err = mcpClient.Initialize(ctx1, initRequest)
	if err != nil {
		log.Error("mcp: failed to initialize:", "error", err)
		return NBToolResponse{Status: NBToolResponseStatusError, Data: "mcp: Failed to initialize:", Type: NBToolResponseTypeText}, nil
	}

	listDirRequest := mcp.CallToolRequest{
		Request: mcp.Request{
			Method: input.Command,
		},
	}
	listDirRequest.Params.Arguments = mcpArgs
	listDirRequest.Params.Name = input.Command

	result, err := mcpClient.CallTool(ctx1, listDirRequest)
	if err != nil {
		log.Error("mcp: failed to list allowed directories:", "error", err)
		return NBToolResponse{Status: NBToolResponseStatusError, Data: ""}, err
	}

	response := ""
	for _, content := range result.Content {
		if textContent, ok := content.(mcp.TextContent); ok {
			response = response + "\n" + textContent.Text
		} else {
			jsonBytes, _ := common.MarshalJsonIndent(content, "", "  ")
			response = response + "\n" + string(jsonBytes)
		}
	}

	status := NBToolResponseStatusSuccess
	if result.IsError {
		status = NBToolResponseStatusError
	}

	return NBToolResponse{Status: status, Data: response, Type: NBToolResponseTypeText}, nil
}

type ClientToolWrapper struct {
	Command NBToolCommand
}

func NewClientToolWrapper(command NBToolCommand) NBTool {
	return &ClientToolWrapper{Command: command}
}

func (c *ClientToolWrapper) Name() string {
	return c.Command.Name
}

func (c *ClientToolWrapper) Description() string {
	return c.Command.Description
}

func (c *ClientToolWrapper) InputSchema() ToolSchema {
	return c.Command.InputSchema
}

func (c *ClientToolWrapper) Call(ctx NbToolContext, input NBToolCallRequest) (NBToolResponse, error) {
	// This method shouldn't typically be called directly on the server side
	// as client tools are executed on the client.
	// However, we can return a waiting status to indicate it should be delegated.
	return NBToolResponse{
		Status: NBToolResponseStatusWaitingForClient,
		Data:   "Waiting for client execution",
	}, nil
}

func (c *ClientToolWrapper) GetType() NBToolType {
	return NBToolTypeTool
}
