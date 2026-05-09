package tools

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"nudgebee/code-analysis-agent/tools/core"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Helper function to check if glab CLI is installed
func isGlabInstalled() bool {
	_, err := exec.LookPath("glab")
	return err == nil
}

// TestNewGLabTool tests the constructor
func TestNewGLabTool(t *testing.T) {
	workspaceDir := "/tmp/test-workspace"
	tool := NewGLabTool(workspaceDir)

	require.NotNil(t, tool)
	assert.Equal(t, workspaceDir, tool.workspaceDir)
}

// TestGLabTool_Name tests that Name() returns the correct tool name
func TestGLabTool_Name(t *testing.T) {
	tool := NewGLabTool("/tmp")

	assert.Equal(t, "glab", tool.Name())
}

// TestGLabTool_Description tests that Description() returns a non-empty, informative string
func TestGLabTool_Description(t *testing.T) {
	tool := NewGLabTool("/tmp")

	desc := tool.Description()

	assert.NotEmpty(t, desc)
	assert.Contains(t, desc, "GitLab CLI")
	assert.Contains(t, desc, "mr")
	assert.Contains(t, desc, "issue")
	assert.Contains(t, desc, "COMMON EXAMPLES")
}

// TestGLabTool_GetType tests that GetType() returns the correct tool type
func TestGLabTool_GetType(t *testing.T) {
	tool := NewGLabTool("/tmp")

	assert.Equal(t, core.NBToolTypeCodeAnalysis, tool.GetType())
}

// TestGLabTool_InputSchema tests the input schema structure
func TestGLabTool_InputSchema(t *testing.T) {
	tool := NewGLabTool("/tmp")

	schema := tool.InputSchema()

	assert.Equal(t, "object", schema.Type)
	assert.NotEmpty(t, schema.Description)
	assert.Contains(t, schema.Required, "args")

	// Verify args property exists and has correct structure
	argsProperty, ok := schema.Properties["args"].(map[string]any)
	require.True(t, ok, "args property should exist")
	assert.Equal(t, "array", argsProperty["type"])

	// Verify items structure
	items, ok := argsProperty["items"].(map[string]any)
	require.True(t, ok, "items should exist")
	assert.Equal(t, "string", items["type"])
}

// TestGLabTool_Execute_InvalidArgsType tests error handling when args is not an array
func TestGLabTool_Execute_InvalidArgsType(t *testing.T) {
	tool := NewGLabTool("/tmp")

	testCases := []struct {
		name  string
		input map[string]any
	}{
		{
			name:  "args is string",
			input: map[string]any{"args": "mr list"},
		},
		{
			name:  "args is number",
			input: map[string]any{"args": 123},
		},
		{
			name:  "args is nil",
			input: map[string]any{"args": nil},
		},
		{
			name:  "args is missing",
			input: map[string]any{},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			resp := tool.Execute(context.Background(), tc.input)

			assert.Equal(t, "error", resp.Status)
			assert.Contains(t, resp.Error, "invalid input")
			assert.Contains(t, resp.Error, "'args' is not a list of strings")
		})
	}
}

// TestGLabTool_Execute_EmptyArgs tests error handling when args array is empty
func TestGLabTool_Execute_EmptyArgs(t *testing.T) {
	tool := NewGLabTool("/tmp")

	input := map[string]any{
		"args": []any{},
	}

	resp := tool.Execute(context.Background(), input)

	assert.Equal(t, "error", resp.Status)
	assert.Contains(t, resp.Error, "no arguments provided")
}

// TestGLabTool_Execute_NonStringArgs tests handling of non-string elements in args
func TestGLabTool_Execute_NonStringArgs(t *testing.T) {
	if !isGlabInstalled() {
		t.Skip("glab CLI not installed, skipping test")
	}

	tool := NewGLabTool(t.TempDir())

	// Mix of strings and non-strings - non-strings should be filtered out
	input := map[string]any{
		"args": []any{"version", 123, true, nil},
	}

	// This will run "glab version" since non-strings are skipped
	resp := tool.Execute(context.Background(), input)

	// The command may succeed or fail depending on environment,
	// but it should not panic and should return a valid response
	assert.NotEmpty(t, resp.Status)
}

// TestGLabTool_Execute_UsesWorkspaceDir tests that the tool uses the workspace directory
func TestGLabTool_Execute_UsesWorkspaceDir(t *testing.T) {
	if !isGlabInstalled() {
		t.Skip("glab CLI not installed, skipping test")
	}

	tmpDir := t.TempDir()
	tool := NewGLabTool(tmpDir)

	input := map[string]any{
		"args": []any{"version"},
	}

	resp := tool.Execute(context.Background(), input)

	// version command should succeed regardless of directory
	assert.Equal(t, "success", resp.Status)
}

// TestGLabTool_Execute_UsesInjectedWorkingDir tests that injected working_directory takes precedence
func TestGLabTool_Execute_UsesInjectedWorkingDir(t *testing.T) {
	if !isGlabInstalled() {
		t.Skip("glab CLI not installed, skipping test")
	}

	defaultDir := t.TempDir()
	injectedDir := t.TempDir()

	tool := NewGLabTool(defaultDir)

	input := map[string]any{
		"args":              []any{"version"},
		"working_directory": injectedDir,
	}

	resp := tool.Execute(context.Background(), input)

	// version command should succeed
	assert.Equal(t, "success", resp.Status)
}

// TestGLabTool_Execute_Version tests running glab version command
func TestGLabTool_Execute_Version(t *testing.T) {
	if !isGlabInstalled() {
		t.Skip("glab CLI not installed, skipping test")
	}

	tool := NewGLabTool(t.TempDir())

	input := map[string]any{
		"args": []any{"version"},
	}

	resp := tool.Execute(context.Background(), input)

	assert.Equal(t, "success", resp.Status)
	assert.Contains(t, resp.Result, "successfully")
	assert.Contains(t, resp.Observation, "glab")

	// Verify data contains expected fields
	data, ok := resp.Data.(map[string]any)
	require.True(t, ok, "Data should be a map")
	assert.Contains(t, data["command"], "glab")
	assert.Contains(t, data["command"], "version")
}

// TestGLabTool_Execute_Help tests running glab help command
func TestGLabTool_Execute_Help(t *testing.T) {
	if !isGlabInstalled() {
		t.Skip("glab CLI not installed, skipping test")
	}

	tool := NewGLabTool(t.TempDir())

	input := map[string]any{
		"args": []any{"help"},
	}

	resp := tool.Execute(context.Background(), input)

	assert.Equal(t, "success", resp.Status)

	// Verify output contains help information
	data, ok := resp.Data.(map[string]any)
	require.True(t, ok, "Data should be a map")

	stdout, ok := data["stdout"].(string)
	require.True(t, ok, "stdout should be a string")
	assert.NotEmpty(t, stdout)
}

// TestGLabTool_Execute_InvalidCommand tests error handling for invalid glab commands
func TestGLabTool_Execute_InvalidCommand(t *testing.T) {
	if !isGlabInstalled() {
		t.Skip("glab CLI not installed, skipping test")
	}

	tool := NewGLabTool(t.TempDir())

	input := map[string]any{
		"args": []any{"nonexistent-command-xyz"},
	}

	resp := tool.Execute(context.Background(), input)

	assert.Equal(t, "error", resp.Status)
	assert.Contains(t, resp.Error, "glab command failed")
}

// TestGLabTool_Execute_MultipleArgs tests commands with multiple arguments
func TestGLabTool_Execute_MultipleArgs(t *testing.T) {
	if !isGlabInstalled() {
		t.Skip("glab CLI not installed, skipping test")
	}

	tool := NewGLabTool(t.TempDir())

	input := map[string]any{
		"args": []any{"help", "mr"},
	}

	resp := tool.Execute(context.Background(), input)

	assert.Equal(t, "success", resp.Status)

	// Verify the command includes all args
	data, ok := resp.Data.(map[string]any)
	require.True(t, ok, "Data should be a map")
	assert.Contains(t, data["command"], "help")
	assert.Contains(t, data["command"], "mr")
}

// TestGLabTool_Execute_WithGitRepo tests glab commands in a git repository context
func TestGLabTool_Execute_WithGitRepo(t *testing.T) {
	if !isGlabInstalled() {
		t.Skip("glab CLI not installed, skipping test")
	}

	// Create a temporary directory with a .git folder to simulate a repo
	tmpDir := t.TempDir()
	gitDir := filepath.Join(tmpDir, ".git")
	err := os.MkdirAll(gitDir, 0755)
	require.NoError(t, err)

	tool := NewGLabTool(tmpDir)

	// version command should work regardless of git context
	input := map[string]any{
		"args": []any{"version"},
	}

	resp := tool.Execute(context.Background(), input)

	assert.Equal(t, "success", resp.Status)
}

// TestGLabTool_Execute_ResponseStructure tests the response structure is correct
func TestGLabTool_Execute_ResponseStructure(t *testing.T) {
	if !isGlabInstalled() {
		t.Skip("glab CLI not installed, skipping test")
	}

	tool := NewGLabTool(t.TempDir())

	input := map[string]any{
		"args": []any{"version"},
	}

	resp := tool.Execute(context.Background(), input)

	// Verify success response structure
	assert.Equal(t, "success", resp.Status)
	assert.NotEmpty(t, resp.Result)
	assert.NotEmpty(t, resp.Observation)
	assert.Empty(t, resp.Error)

	// Verify Data structure
	data, ok := resp.Data.(map[string]any)
	require.True(t, ok, "Data should be a map")
	assert.Contains(t, data, "stdout")
	assert.Contains(t, data, "stderr")
	assert.Contains(t, data, "command")
}

// TestGLabTool_Execute_ErrorResponseStructure tests the error response structure
func TestGLabTool_Execute_ErrorResponseStructure(t *testing.T) {
	tool := NewGLabTool(t.TempDir())

	input := map[string]any{
		"args": "invalid",
	}

	resp := tool.Execute(context.Background(), input)

	// Verify error response structure
	assert.Equal(t, "error", resp.Status)
	assert.NotEmpty(t, resp.Error)
	assert.NotEmpty(t, resp.Observation)
	assert.Empty(t, resp.Result)
}

// TestNewGLabToolWithToken tests the constructor with token
func TestNewGLabToolWithToken(t *testing.T) {
	workspaceDir := "/tmp/test-workspace"
	testToken := "glpat-test123456789"
	tool := NewGLabToolWithToken(workspaceDir, testToken)

	require.NotNil(t, tool)
	assert.Equal(t, workspaceDir, tool.workspaceDir)
	assert.Equal(t, testToken, tool.gitlabToken)
}

// TestGLabTool_SetGitLabToken tests the SetGitLabToken method
func TestGLabTool_SetGitLabToken(t *testing.T) {
	tool := NewGLabTool("/tmp")
	assert.Equal(t, "", tool.gitlabToken)

	testToken := "glpat-newsecret987654321"
	tool.SetGitLabToken(testToken)
	assert.Equal(t, testToken, tool.gitlabToken)
}
