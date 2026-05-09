package workspace

import (
	"context"
	"fmt"
	"log/slog"
	"nudgebee/llm/config"
	"nudgebee/llm/security"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestWorkspaceIntegration is a manual integration test.
// Run it with: go test -v -tags=integration ./workspace/... -run TestWorkspaceIntegration
func TestWorkspaceIntegration(t *testing.T) {
	// 1. Setup Configuration for Local Test
	config.Config.LlmServerWorkspaceEnabled = true
	config.Config.LlmServerCodeAgentNamespace = "default"
	config.Config.LlmServerWorkspacePort = 8080
	config.Config.LlmServerCodeAgentSecret = "nudgebee-secret-volume"
	// Use a dummy image or the real one if available
	// config.Config.LlmServerCodeAgentImage = "nudgebee/code-analysis-agent:local"

	// 2. Setup Context & Manager
	// Initialize with a valid context and logger to prevent nil pointer panics in k8s client
	baseCtx := context.Background()
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	ctx := security.NewRequestContext(baseCtx, nil, logger, nil, nil)

	mgr := NewWorkspaceManager()
	accountID := "test-acc-local-1"
	conversationID := "conv-123"

	// 3. Cleanup function (ensure we don't leave pods)
	defer func() {
		fmt.Println("Cleaning up workspace...")
		_ = mgr.TerminateWorkspace(ctx, accountID)
	}()

	// 4. Create Workspace
	fmt.Println("Creating workspace...")
	err := mgr.CreateWorkspace(ctx, accountID)
	require.NoError(t, err)

	// 5. Wait for Pod Ready
	// The CreateWorkspace returns when Pod is created, not ready.
	// We need to poll until the pod is actually ready and serving requests.
	fmt.Println("Waiting for pod to be ready (up to 300s)...")
	ready := false
	for i := 0; i < 60; i++ { // Increased wait time to 5 minutes for slow image pulls
		// We use ExecuteCommand as the readiness probe because it strictly checks PodReady condition
		_, err := mgr.ExecuteCommand(ctx, accountID, conversationID, "echo ready", nil)
		if err == nil {
			fmt.Println("Pod is ready and serving!")
			ready = true
			break
		}
		// Log the error to see progress (e.g. "no running workspace found" or "connection refused")
		fmt.Printf("Attempt %d: Waiting for readiness... (%v)\n", i+1, err)
		time.Sleep(5 * time.Second)
	}
	require.True(t, ready, "Workspace pod never became ready")

	// 6. Execute Multiple Commands to verify Optimistic Execution
	fmt.Println("Executing first command (Warm-up)...")
	start1 := time.Now()
	cmd1 := "echo 'Hello First Execution'"
	output1, err := mgr.ExecuteCommand(ctx, accountID, conversationID, cmd1, nil)
	require.NoError(t, err)
	fmt.Printf("First Command Output: %s (Took: %v)\n", output1, time.Since(start1))
	assert.Contains(t, output1, "Hello First Execution")

	fmt.Println("Executing second command (Optimistic)...")
	start2 := time.Now()
	cmd2 := "pwd"
	output2, err := mgr.ExecuteCommand(ctx, accountID, conversationID, cmd2, nil)
	require.NoError(t, err)
	fmt.Printf("Second Command Output: %s (Took: %v)\n", output2, time.Since(start2))
	assert.Contains(t, output2, "/tmp/code-analysis/exec_workspaces/conv-123")
}
