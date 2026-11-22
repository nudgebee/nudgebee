package network

import (
	"bufio"
	"fmt"
	"net"
	"nudgebee/runbook/internal/tasks/testutils"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestTcpTask_Execute(t *testing.T) {
	task := &TcpTask{}
	logger := &TestLogger{}
	taskCtx := testutils.NewTestTaskContext("tenant", "account", "user", logger)

	// Start a local echo server
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Failed to start test listener: %v", err)
	}
	defer func() {
		if err := listener.Close(); err != nil {
			t.Logf("Error closing listener: %v", err)
		}
	}()

	addr := listener.Addr().(*net.TCPAddr)
	port := addr.Port

	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer func() {
					if err := c.Close(); err != nil {
						t.Logf("Error closing client connection: %v", err)
					}
				}()
				// Simple echo
				reader := bufio.NewReader(c)
				line, _ := reader.ReadString('\n')
				if line != "" {
					if _, err := c.Write([]byte("echo: " + line)); err != nil {
						t.Logf("Error writing to client connection: %v", err)
					}
				}
			}(conn)
		}
	}()

	t.Run("Reachable Port (Connect Only)", func(t *testing.T) {
		params := map[string]any{
			"host": "127.0.0.1",
			"port": port,
		}

		res, err := task.Execute(taskCtx, params)
		assert.NoError(t, err)

		resultMap, ok := res.(map[string]any)
		assert.True(t, ok)
		assert.True(t, resultMap["reachable"].(bool))
	})

	t.Run("Send Message and Receive Response", func(t *testing.T) {
		params := map[string]any{
			"host":    "127.0.0.1",
			"port":    port,
			"message": "hello\n",
		}

		res, err := task.Execute(taskCtx, params)
		assert.NoError(t, err)

		resultMap, ok := res.(map[string]any)
		assert.True(t, ok)
		assert.True(t, resultMap["reachable"].(bool))
		assert.Equal(t, "echo: hello\n", resultMap["response"])
	})

	t.Run("Unreachable Port", func(t *testing.T) {
		closedPort := port + 1
		params := map[string]any{
			"host":    "127.0.0.1",
			"port":    closedPort,
			"timeout": "100ms",
		}

		res, err := task.Execute(taskCtx, params)
		assert.NoError(t, err)

		resultMap, ok := res.(map[string]any)
		assert.True(t, ok)
		assert.False(t, resultMap["reachable"].(bool))
		assert.NotEmpty(t, resultMap["error"])
	})

	t.Run("Numeric string port is trimmed and dialled", func(t *testing.T) {
		params := map[string]any{
			"host": "127.0.0.1",
			"port": fmt.Sprintf(" %d ", port),
		}

		res, err := task.Execute(taskCtx, params)
		assert.NoError(t, err)

		resultMap, ok := res.(map[string]any)
		assert.True(t, ok)
		assert.True(t, resultMap["reachable"].(bool))
		assert.Equal(t, fmt.Sprintf("%d", port), resultMap["port"])
	})

	t.Run("Service name port is still accepted", func(t *testing.T) {
		// The dialer has always resolved names like "https"; saved steps using
		// one must keep running, whatever the local port 443 answers.
		res, err := task.Execute(taskCtx, map[string]any{
			"host":    "127.0.0.1",
			"port":    "https",
			"timeout": "100ms",
		})
		assert.NoError(t, err)
		resultMap, ok := res.(map[string]any)
		assert.True(t, ok)
		assert.Equal(t, "https", resultMap["port"])
	})

	t.Run("Invalid port fails the task before dialling", func(t *testing.T) {
		for _, badPort := range []any{"443x", "", "  ", "0", "65536", "-1", "44.3", 0, 70000} {
			res, err := task.Execute(taskCtx, map[string]any{
				"host": "127.0.0.1",
				"port": badPort,
			})
			assert.Nil(t, res, "port %v", badPort)
			if assert.Error(t, err, "port %v", badPort) {
				assert.Contains(t, err.Error(), "use a port number between 1 and 65535", "port %v", badPort)
			}
		}
	})
}
