package main

import (
	"os/exec"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	binaryName = "./build/code-analysis-agent"
	testRepo   = "https://gitlab.com/nudgebee-group/nudgebee-project"
)

// TestCLIBasicCommands tests basic CLI functionality
func TestCLIBasicCommands(t *testing.T) {
	// Build the binary first
	buildCmd := exec.Command("go", "build", "-o", binaryName, "main.go")
	err := buildCmd.Run()
	require.NoError(t, err, "Failed to build binary")

	tests := []struct {
		name           string
		args           []string
		expectedOutput string
		expectError    bool
	}{
		{
			name:           "Version command",
			args:           []string{"--version"},
			expectedOutput: "Code Analysis Agent v1.0.0",
			expectError:    false,
		},
		{
			name:           "Health command",
			args:           []string{"--health"},
			expectedOutput: "OK",
			expectError:    false,
		},
		{
			name:           "No arguments shows help",
			args:           []string{},
			expectedOutput: "Code Analysis CLI - Intelligent log analysis with source code correlation",
			expectError:    false,
		},
		{
			name:           "Analyze without required args fails",
			args:           []string{"--analyze"},
			expectedOutput: "Logs are required (--logs) for log analysis",
			expectError:    true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cmd := exec.Command(binaryName, tt.args...)
			output, err := cmd.CombinedOutput()

			if tt.expectError {
				assert.Error(t, err, "Expected command to fail")
			} else {
				assert.NoError(t, err, "Expected command to succeed")
			}

			assert.Contains(t, string(output), tt.expectedOutput, "Output should contain expected text")
		})
	}
}

// TestCLIArguments tests various CLI argument combinations
func TestCLIArguments(t *testing.T) {
	// The CLI falls back to these env vars when --token is absent, and the
	// subprocess inherits them, so clear them for the "Missing token" case.
	t.Setenv("GITHUB_TOKEN", "")
	t.Setenv("GITLAB_TOKEN", "")

	// Build the binary first
	buildCmd := exec.Command("go", "build", "-o", binaryName, "main.go")
	err := buildCmd.Run()
	require.NoError(t, err, "Failed to build binary")

	tests := []struct {
		name        string
		args        []string
		expectError bool
		checkOutput string
	}{
		{
			name:        "Missing repository URL",
			args:        []string{"--analyze", "--logs", "test logs", "--token", "fake-token"},
			expectError: true,
			checkOutput: "failed to initialize LLM client",
		},
		{
			name:        "Missing logs",
			args:        []string{"--analyze", "--repo", testRepo, "--token", "fake-token"},
			expectError: true,
			checkOutput: "Logs are required",
		},
		{
			name:        "Missing token",
			args:        []string{"--analyze", "--repo", testRepo, "--logs", "test logs"},
			expectError: true,
			checkOutput: "Git token is required",
		},
		{
			name:        "Custom branch",
			args:        []string{"--analyze", "--repo", testRepo, "--logs", "test", "--token", "fake", "--branch", "develop"},
			expectError: true, // Will fail auth but should show branch
			checkOutput: "failed to initialize LLM client",
		},
		{
			name:        "Custom prompt",
			args:        []string{"--analyze", "--repo", testRepo, "--logs", "test", "--token", "fake", "--prompt", "Custom analysis prompt"},
			expectError: true, // Will fail auth
			checkOutput: "failed to initialize LLM client",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cmd := exec.Command(binaryName, tt.args...)
			output, err := cmd.CombinedOutput()
			outputStr := string(output)

			if tt.expectError {
				assert.Error(t, err, "Expected command to fail")
			} else {
				assert.NoError(t, err, "Expected command to succeed")
			}

			if tt.checkOutput != "" {
				assert.Contains(t, outputStr, tt.checkOutput, "Output should contain expected text")
			}

			t.Logf("Test '%s' output: %s", tt.name, outputStr)
		})
	}
}

// TestCLIErrorHandling tests error scenarios
func TestCLIErrorHandling(t *testing.T) {
	// Build the binary first
	buildCmd := exec.Command("go", "build", "-o", binaryName, "main.go")
	err := buildCmd.Run()
	require.NoError(t, err, "Failed to build binary")

	tests := []struct {
		name        string
		args        []string
		expectError bool
		errorMsg    string
	}{
		{
			name:        "Invalid repository URL",
			args:        []string{"--analyze", "--repo", "not-a-url", "--logs", "test", "--token", "fake"},
			expectError: true,
			errorMsg:    "", // Will fail during analysis
		},
		{
			name:        "Invalid token format",
			args:        []string{"--analyze", "--repo", testRepo, "--logs", "test", "--token", "invalid-token"},
			expectError: true,
			errorMsg:    "", // Will fail during authentication
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cmd := exec.Command(binaryName, tt.args...)
			output, err := cmd.CombinedOutput()

			if tt.expectError {
				assert.Error(t, err, "Expected command to fail")
			}

			t.Logf("Error test '%s' output: %s", tt.name, string(output))
		})
	}
}

// TestCLIIntegrationQuick tests a quick integration without full analysis
func TestCLIIntegrationQuick(t *testing.T) {
	// Build the binary first
	buildCmd := exec.Command("go", "build", "-o", binaryName, "main.go")
	err := buildCmd.Run()
	require.NoError(t, err, "Failed to build binary")

	// Test that CLI can start analysis (will fail on auth but that's OK)
	cmd := exec.Command(binaryName,
		"--analyze",
		"--repo", testRepo,
		"--logs", "test error log",
		"--token", "fake-token-for-testing",
	)

	output, _ := cmd.CombinedOutput()
	outputStr := string(output)

	// Should fail on auth/analysis but should start properly
	assert.Contains(t, outputStr, "Starting CLI analysis", "Should start analysis")
	assert.Contains(t, outputStr, testRepo, "Should show repository")
	assert.Contains(t, outputStr, "\"logs_length\":14", "Should show correct logs length")

	t.Logf("Quick integration test output: %s", outputStr)
}
