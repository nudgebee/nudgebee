package network

import (
	"fmt"
	"io"
	"net"
	"nudgebee/runbook/internal/tasks/types"
	"time"
)

// TcpTask implements the Task interface for checking TCP connectivity and basic interaction.
type TcpTask struct{}

func (t *TcpTask) GetName() string {
	return "network.tcp"
}

func (t *TcpTask) GetDescription() string {
	return "Test if a host and port are reachable over TCP."
}

func (t *TcpTask) GetDisplayName() string {
	return "TCP Check"
}

func (t *TcpTask) Execute(taskCtx types.TaskContext, params map[string]any) (any, error) {
	host, ok := params["host"].(string)
	if !ok || host == "" {
		return nil, fmt.Errorf("host parameter is required and must be a non-empty string")
	}

	var port string
	switch p := params["port"].(type) {
	case string:
		port = p
	case float64:
		port = fmt.Sprintf("%.0f", p)
	case int:
		port = fmt.Sprintf("%d", p)
	default:
		return nil, fmt.Errorf("port parameter is required and must be a string or number")
	}

	timeout := 5 * time.Second
	if tVal, ok := params["timeout"].(string); ok && tVal != "" {
		if d, err := time.ParseDuration(tVal); err == nil {
			timeout = d
		}
	}
	// Cap timeout at 60s
	if timeout > 60*time.Second {
		timeout = 60 * time.Second
	}

	message, _ := params["message"].(string)

	address := net.JoinHostPort(host, port)

	dialer := net.Dialer{Timeout: timeout}
	conn, err := dialer.DialContext(taskCtx.GetContext(), "tcp", address)

	reachable := false
	var errMsg string
	var response string

	if err == nil {
		reachable = true
		defer func() {
			if err := conn.Close(); err != nil {
				taskCtx.GetLogger().Warn("Failed to close TCP connection", "error", err)
			}
		}()

		if message != "" {
			// Write message
			if err := conn.SetWriteDeadline(time.Now().Add(timeout)); err != nil {
				taskCtx.GetLogger().Warn("Failed to set TCP write deadline", "error", err)
			}
			_, err = conn.Write([]byte(message))
			if err != nil {
				errMsg = fmt.Sprintf("failed to write message: %v", err)
				reachable = false // Technically reached, but failed operation
			}
		}

		readTimeoutStr, _ := params["read_timeout"].(string)

		shouldRead := message != "" || readTimeoutStr != ""

		if reachable && shouldRead && errMsg == "" {
			readTimeout := timeout // default to connect timeout
			if readTimeoutStr != "" {
				if d, err := time.ParseDuration(readTimeoutStr); err == nil {
					readTimeout = d
				}
			}
			// Cap read_timeout at 60s
			if readTimeout > 60*time.Second {
				readTimeout = 60 * time.Second
			}

			if err := conn.SetReadDeadline(time.Now().Add(readTimeout)); err != nil {
				taskCtx.GetLogger().Warn("Failed to set TCP read deadline", "error", err)
			}

			// Read up to 4KB (arbitrary limit for basic checks)
			buf := make([]byte, 4096)
			n, err := conn.Read(buf)
			if err != nil && err != io.EOF {
				if netErr, ok := err.(net.Error); ok && netErr.Timeout() {
					// Timeout reading is fine if we just wanted to send, or if server is silent.
				} else {
					errMsg = fmt.Sprintf("failed to read response: %v", err)
				}
			}
			if n > 0 {
				response = string(buf[:n])
			}
		}

	} else {
		errMsg = err.Error()
	}

	return map[string]any{
		"host":      host,
		"port":      port,
		"reachable": reachable,
		"error":     errMsg,
		"response":  response,
	}, nil
}

func (t *TcpTask) InputSchema() *types.Schema {
	return &types.Schema{
		Properties: map[string]types.Property{
			"host": {
				Type:        "string",
				Description: "The hostname or IP address.",
				Required:    true,
			},
			"port": {
				Type:        "string", // or number
				Description: "The TCP port number.",
				Required:    true,
			},
			"timeout": {
				Type:        "string",
				Description: "Timeout for connection and write operations (default '5s').",
				Required:    false,
				Default:     "5s",
			},
			"message": {
				Type:        "string",
				Description: "Optional message/payload to send upon connection.",
				Required:    false,
			},
			"read_timeout": {
				Type:        "string",
				Description: "Timeout for reading a response. If not set, defaults to connect timeout if message is sent.",
				Required:    false,
			},
		},
	}
}

func (t *TcpTask) OutputSchema() *types.Schema {
	return &types.Schema{
		Properties: map[string]types.Property{
			"host": {
				Type:        "string",
				Description: "The checked host.",
				Required:    true,
			},
			"port": {
				Type:        "string",
				Description: "The checked port.",
				Required:    true,
			},
			"reachable": {
				Type:        "boolean",
				Description: "True if connection was successful.",
				Required:    true,
			},
			"response": {
				Type:        "string",
				Description: "The response received from the server (if any).",
				Required:    false,
			},
			"error": {
				Type:        "string",
				Description: "Error message.",
				Required:    false,
			},
		},
	}
}
