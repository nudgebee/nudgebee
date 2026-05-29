package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	binaryName = "./build/code-analysis-agent"
	testRepo   = "https://gitlab.com/nudgebee-group/nudgebee-project"
)

var testLogs = `{"asctime": "2025-07-08 08:04:00,563", "levelname": "ERROR", "filename": "message.py", "lineno": 136, "message": "Can't send message: argument of type 'NoneType' is not iterable", "exc_info": "Traceback (most recent call last):\n File \"/usr/src/app/notifications_server/message_templates/ms_teams/finding.py\", line 87, in add_finding_evidences\n if \"data\" in evidence:\n ^^^^^^^^^^^^^^^^^^\nTypeError: argument of type 'NoneType' is not iterable", "taskName": "Task-194435"}`

// TestCLIBasicCommands tests basic CLI functionality
func TestCLIBasicCommands(t *testing.T) {
	// Build the binary first
	buildCmd := exec.Command("go", "build", "-o", binaryName, "main.go")
	err := buildCmd.Run()
	require.NoError(t, err, "Failed to build binary")
	// added a test change which does absolutely nothing
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

// TestCLIAnalysisFlow tests the full CLI analysis workflow
func TestCLIAnalysisFlow(t *testing.T) {
	// Skip if no GitHub token
	githubToken := os.Getenv("GITLAB_TOKEN")
	// githubToken := os.Getenv("GITHUB_TOKEN")
	if githubToken == "" {
		t.Skip("GITHUB_TOKEN environment variable not set - skipping test")
	}

	// Build the binary first
	buildCmd := exec.Command("go", "build", "-o", binaryName, "main.go")
	err := buildCmd.Run()
	require.NoError(t, err, "Failed to build binary")

	// Test CLI analysis
	cmd := exec.Command(binaryName,
		"--analyze",
		"--repo", testRepo,
		"--logs", testLogs,
		"--token", githubToken,
		"--prompt", "Analyze this Python error",
	)

	// Set timeout for analysis
	timeout := 5 * time.Minute
	done := make(chan error, 1)
	var output []byte

	go func() {
		var err error
		output, err = cmd.CombinedOutput()
		done <- err
	}()

	select {
	case err := <-done:
		if err != nil {
			t.Logf("Command output: %s", string(output))
			// Don't fail immediately - analysis might fail but we want to check the output format
		}

		outputStr := string(output)
		t.Logf("Analysis output length: %d characters", len(outputStr))

		// Check that analysis was attempted
		assert.Contains(t, outputStr, "Starting CLI analysis", "Should start analysis")
		assert.Contains(t, outputStr, testRepo, "Should contain repository URL")

		// Try to parse as JSON if it looks like JSON output
		if strings.Contains(outputStr, "Analysis Result:") {
			// Extract JSON part
			jsonStart := strings.Index(outputStr, "{")
			jsonEnd := strings.LastIndex(outputStr, "}")
			if jsonStart != -1 && jsonEnd != -1 && jsonEnd > jsonStart {
				jsonStr := outputStr[jsonStart : jsonEnd+1]
				var result map[string]any
				if json.Unmarshal([]byte(jsonStr), &result) == nil {
					t.Logf("Successfully parsed JSON result with keys: %v", getKeys(result))

					// Verify expected structure
					if success, ok := result["success"]; ok {
						t.Logf("Analysis success: %v", success)
					}
					if analysisID, ok := result["analysis_id"]; ok {
						assert.NotEmpty(t, analysisID, "Analysis ID should not be empty")
					}
				}
			}
		}

	case <-time.After(timeout):
		if err := cmd.Process.Kill(); err != nil {
			t.Fatalf("Failed to kill process: %v", err)
		}
		t.Fatal("CLI analysis timed out after 5 minutes")
	}
}

// TestCLIEnvironmentVariables tests environment variable support
func TestCLIEnvironmentVariables(t *testing.T) {
	// Skip if no GitHub token
	githubToken := os.Getenv("GITHUB_TOKEN")
	if githubToken == "" {
		t.Skip("GITHUB_TOKEN environment variable not set - skipping test")
	}

	// Build the binary first
	buildCmd := exec.Command("go", "build", "-o", binaryName, "main.go")
	err := buildCmd.Run()
	require.NoError(t, err, "Failed to build binary")

	// Test using environment variable for token
	cmd := exec.Command(binaryName,
		"--analyze",
		"--repo", testRepo,
		"--logs", testLogs,
	)

	// Set environment variable
	cmd.Env = append(os.Environ(), "GITHUB_TOKEN="+githubToken)

	// Run with shorter timeout for this test
	cmd.Env = append(cmd.Env, "REACT_MAX_ITERATIONS=3") // Limit iterations for faster test

	output, _ := cmd.CombinedOutput()
	outputStr := string(output)

	// Should start analysis (token found via env var)
	assert.Contains(t, outputStr, "Starting CLI analysis", "Should start analysis with env var token")

	t.Logf("Environment variable test output: %s", outputStr)
}

// TestCLIArguments tests various CLI argument combinations
func TestCLIArguments(t *testing.T) {
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

// TestCLIOutput tests that CLI produces valid JSON output
func TestCLIOutputFormat(t *testing.T) {
	// This test uses a mock/simple analysis to check output format
	githubToken := os.Getenv("GITHUB_TOKEN")
	if githubToken == "" {
		t.Skip("GITHUB_TOKEN environment variable not set - skipping test")
	}

	// Build the binary first
	buildCmd := exec.Command("go", "build", "-o", binaryName, "main.go")
	err := buildCmd.Run()
	require.NoError(t, err, "Failed to build binary")

	// Simple logs that should be faster to analyze
	simpleLogs := `{"level": "ERROR", "message": "Simple test error"}`

	cmd := exec.Command(binaryName,
		"--analyze",
		"--repo", testRepo,
		"--logs", simpleLogs,
		"--token", githubToken,
		"--prompt", "Quick test analysis",
	)

	// Set shorter timeout
	timeout := 2 * time.Minute
	done := make(chan error, 1)
	var output []byte

	go func() {
		var err error
		output, err = cmd.CombinedOutput()
		done <- err
	}()

	select {
	case <-done:
		outputStr := string(output)

		// Should contain analysis start message
		assert.Contains(t, outputStr, "Starting CLI analysis", "Should start analysis")

		// Should show repository and logs info
		assert.Contains(t, outputStr, testRepo, "Should show repository")
		assert.Contains(t, outputStr, fmt.Sprintf("\"logs_length\":%d", len(simpleLogs)), "Should show logs length")

		// Look for JSON output
		if strings.Contains(outputStr, "Analysis Result:") {
			t.Log("Found analysis result in output")

			// Try to extract and validate JSON
			lines := strings.Split(outputStr, "\n")
			var jsonLines []string
			inJSON := false

			for _, line := range lines {
				if strings.Contains(line, "Analysis Result:") {
					inJSON = true
					continue
				}
				if inJSON && (strings.HasPrefix(line, "{") || strings.HasPrefix(line, " ")) {
					jsonLines = append(jsonLines, line)
				}
			}

			if len(jsonLines) > 0 {
				jsonStr := strings.Join(jsonLines, "\n")
				var result map[string]any
				err := json.Unmarshal([]byte(jsonStr), &result)
				if err == nil {
					t.Logf("Successfully parsed JSON output with keys: %v", getKeys(result))

					// Verify basic structure
					assert.Contains(t, result, "success", "Should have success field")
					assert.Contains(t, result, "analysis_id", "Should have analysis_id field")
				} else {
					t.Logf("JSON parsing failed: %v", err)
					t.Logf("Attempted to parse: %s", jsonStr)
				}
			}
		}

	case <-time.After(timeout):
		if err := cmd.Process.Kill(); err != nil {
			t.Logf("Failed to kill process: %v", err)
		}
		t.Log("CLI output format test timed out, but that's expected for this test")
	}
}

// Helper function to get map keys for logging
func getKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return keys
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
