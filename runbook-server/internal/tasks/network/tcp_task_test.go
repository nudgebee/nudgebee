package network

import (
	"bufio"
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
}
