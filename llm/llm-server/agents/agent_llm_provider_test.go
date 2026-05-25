package agents

import (
	"encoding/json"
	"fmt"
	"nudgebee/llm/agents/core"
	"nudgebee/llm/security"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
)

// Test suite for validating LLM provider compatibility with k8s_debug agent
// These tests cover simple, medium, and hard scenarios to ensure consistent behavior
// across different LLM providers (AWS Bedrock, Azure OpenAI, Google Vertex AI, etc.)

func TestLLMProvider_AllScenarios(t *testing.T) {
	testCases := []struct {
		SessionId        string
		Query            string
		AccountId        string
		UserId           string
		Description      string
		Difficulty       string
		MinExpectedSteps int
		ShouldUseTool    bool
		ExpectedBehavior string
	}{
		// ==================== SIMPLE TESTS ====================
		{
			SessionId:        "llm-provider-test-simple-1",
			AccountId:        os.Getenv("TEST_ACCOUNT"),
			UserId:           os.Getenv("TEST_USER"),
			Query:            "Hello, can you help me?",
			Description:      "Basic conversational response without tool usage",
			Difficulty:       "SIMPLE",
			MinExpectedSteps: 0,
			ShouldUseTool:    false,
			ExpectedBehavior: "Should respond conversationally without using tools",
		},
		{
			SessionId:        "llm-provider-test-simple-2",
			AccountId:        os.Getenv("TEST_ACCOUNT"),
			UserId:           os.Getenv("TEST_USER"),
			Query:            "List all pods in the nudgebee namespace",
			Description:      "Single kubectl command execution",
			Difficulty:       "SIMPLE",
			MinExpectedSteps: 1,
			ShouldUseTool:    true,
			ExpectedBehavior: "Should execute kubectl get pods command",
		},
		{
			SessionId:        "llm-provider-test-simple-3",
			AccountId:        os.Getenv("TEST_ACCOUNT"),
			UserId:           os.Getenv("TEST_USER"),
			Query:            "What is the status of llm-server pod?",
			Description:      "Get specific pod status with conditional logic",
			Difficulty:       "SIMPLE",
			MinExpectedSteps: 1,
			ShouldUseTool:    true,
			ExpectedBehavior: "Should get pod status and describe it",
		},

		// ==================== MEDIUM TESTS ====================
		{
			SessionId:        "llm-provider-test-medium-1",
			AccountId:        os.Getenv("TEST_ACCOUNT"),
			UserId:           os.Getenv("TEST_USER"),
			Query:            "Why is the rag-server pod restarting? Check the last 50 logs.",
			Description:      "Multi-step investigation with specific parameters",
			Difficulty:       "MEDIUM",
			MinExpectedSteps: 2,
			ShouldUseTool:    true,
			ExpectedBehavior: "Should get pod status + logs with proper parameters",
		},
		{
			SessionId:        "llm-provider-test-medium-2",
			AccountId:        os.Getenv("TEST_ACCOUNT"),
			UserId:           os.Getenv("TEST_USER"),
			Query:            "Compare CPU and memory usage between llm-server and rag-server pods",
			Description:      "Parallel data gathering and comparison",
			Difficulty:       "MEDIUM",
			MinExpectedSteps: 2,
			ShouldUseTool:    true,
			ExpectedBehavior: "Should gather metrics from both pods and compare",
		},
		{
			SessionId:        "llm-provider-test-medium-3",
			AccountId:        os.Getenv("TEST_ACCOUNT"),
			UserId:           os.Getenv("TEST_USER"),
			Query:            "Check if any pods are in CrashLoopBackOff state. If yes, get their logs and events.",
			Description:      "Conditional logic with dependent steps",
			Difficulty:       "MEDIUM",
			MinExpectedSteps: 1,
			ShouldUseTool:    true,
			ExpectedBehavior: "Should check pod status, conditionally get logs/events",
		},

		// ==================== HARD TESTS ====================
		{
			SessionId:        "llm-provider-test-hard-1",
			AccountId:        os.Getenv("TEST_ACCOUNT"),
			UserId:           os.Getenv("TEST_USER"),
			Query:            "Investigate high memory usage in the nudgebee namespace. Check pod metrics, identify top consumers, analyze their logs for memory leaks, and check if there are any OOMKilled events.",
			Description:      "Multi-phase RCA with metrics, logs, and events",
			Difficulty:       "HARD",
			MinExpectedSteps: 3,
			ShouldUseTool:    true,
			ExpectedBehavior: "Should perform comprehensive investigation: metrics → logs → events",
		},
		{
			SessionId:        "llm-provider-test-hard-2",
			AccountId:        os.Getenv("TEST_ACCOUNT"),
			UserId:           os.Getenv("TEST_USER"),
			Query:            "The llm-server is failing to connect to relay-server. Investigate the issue by checking: 1) Network connectivity 2) Service endpoints 3) Both pods' logs for connection errors 4) Any network policies that might be blocking traffic",
			Description:      "Cross-service debugging with network analysis",
			Difficulty:       "HARD",
			MinExpectedSteps: 4,
			ShouldUseTool:    true,
			ExpectedBehavior: "Should check connectivity, services, logs, and network policies",
		},
		{
			SessionId:        "llm-provider-test-hard-3",
			AccountId:        os.Getenv("TEST_ACCOUNT"),
			UserId:           os.Getenv("TEST_USER"),
			Query:            "Analyze performance bottlenecks in the nudgebee namespace. For each service: check resource utilization, identify pods approaching limits, examine slow query logs from database pods, and correlate with any recent scaling events or configuration changes.",
			Description:      "Comprehensive performance analysis with correlation",
			Difficulty:       "HARD",
			MinExpectedSteps: 3,
			ShouldUseTool:    true,
			ExpectedBehavior: "Should analyze resources, logs, and correlate events",
		},
		{
			SessionId:        "llm-provider-test-hard-4",
			AccountId:        os.Getenv("TEST_ACCOUNT"),
			UserId:           os.Getenv("TEST_USER"),
			Query:            "Database queries are slow. First check PostgreSQL pod status and resource usage. If resources are fine, analyze slow query logs. If no slow queries found, check for lock contention. Finally, verify index usage and suggest optimizations.",
			Description:      "Multi-step investigation with fallback strategies",
			Difficulty:       "HARD",
			MinExpectedSteps: 2,
			ShouldUseTool:    true,
			ExpectedBehavior: "Should demonstrate conditional branching and fallback logic",
		},
	}

	// Iterate through all test cases
	for i, tc := range testCases {
		t.Run(fmt.Sprintf("%s_%d_%s", tc.Difficulty, i+1, tc.Description), func(t *testing.T) {
			// Setup
			sc := security.NewRequestContextForTenantAccountAdmin(os.Getenv("TEST_TENANT"), tc.UserId, []string{tc.AccountId})
			k8sAgent := newK8sDebugAgent(tc.AccountId)

			// Clean up previous conversation
			err := core.DeleteConversationBySession(tc.SessionId, tc.AccountId, tc.UserId)
			assert.Nil(t, err)

			// Execute the query
			resp, err := core.HandleConversationSessionRequest(sc, k8sAgent, tc.UserId, tc.AccountId, tc.SessionId, tc.Query)

			// Print detailed output
			fmt.Printf("\n")
			fmt.Printf("================================================================================\n")
			fmt.Printf("🎯 Test #%d: [%s] %s\n", i+1, tc.Difficulty, tc.Description)
			fmt.Printf("================================================================================\n")
			fmt.Printf("📝 Query: %s\n", tc.Query)
			fmt.Printf("--------------------------------------------------------------------------------\n")
			fmt.Printf("💬 Response: %v\n", resp.Response)
			fmt.Printf("--------------------------------------------------------------------------------\n")

			// Print agent execution steps
			if len(resp.AgentStepResponse) > 0 {
				fmt.Printf("🔧 Agent Execution Steps (%d steps):\n", len(resp.AgentStepResponse))
				invocationLog, _ := json.MarshalIndent(resp.AgentStepResponse, "", "  ")
				fmt.Printf("%s\n", string(invocationLog))
			} else {
				fmt.Printf("🔧 Agent Execution Steps: No tools used (knowledge-based response)\n")
			}

			fmt.Printf("--------------------------------------------------------------------------------\n")
			fmt.Printf("✅ Expected Behavior: %s\n", tc.ExpectedBehavior)
			fmt.Printf("================================================================================\n\n")

			// Assertions
			assert.Nil(t, err, "Should not return error")
			assert.NotNil(t, resp, "Response should not be nil")
			assert.Greater(t, len(resp.Response), 0, "Response should have content")

			// Validate tool usage
			if tc.ShouldUseTool {
				assert.NotNil(t, resp.AgentStepResponse, "Should have agent step response")
				assert.GreaterOrEqual(t, len(resp.AgentStepResponse), tc.MinExpectedSteps,
					"Should have at least %d execution steps", tc.MinExpectedSteps)
			}

			// Log results to test output
			t.Logf("Test completed: %s - %s", tc.Difficulty, tc.Description)
			t.Logf("Response length: %d characters", len(fmt.Sprintf("%v", resp.Response)))
			t.Logf("Tool invocations: %d", len(resp.AgentStepResponse))
		})
	}
}

// Helper test to run only SIMPLE tests
func TestLLMProvider_Simple(t *testing.T) {
	runTestsByDifficulty(t, "SIMPLE")
}

// Helper test to run only MEDIUM tests
func TestLLMProvider_Medium(t *testing.T) {
	runTestsByDifficulty(t, "MEDIUM")
}

// Helper test to run only HARD tests
func TestLLMProvider_Hard(t *testing.T) {
	runTestsByDifficulty(t, "HARD")
}

// Helper function to filter and run tests by difficulty
func runTestsByDifficulty(t *testing.T, difficulty string) {
	// Get all test cases (copy from TestLLMProvider_AllScenarios)
	testCases := []struct {
		SessionId        string
		Query            string
		AccountId        string
		UserId           string
		Description      string
		Difficulty       string
		MinExpectedSteps int
		ShouldUseTool    bool
		ExpectedBehavior string
	}{
		// SIMPLE
		{
			SessionId:        "llm-provider-test-simple-1",
			AccountId:        os.Getenv("TEST_ACCOUNT"),
			UserId:           os.Getenv("TEST_USER"),
			Query:            "Hello, can you help me?",
			Description:      "Basic conversational response without tool usage",
			Difficulty:       "SIMPLE",
			MinExpectedSteps: 0,
			ShouldUseTool:    false,
			ExpectedBehavior: "Should respond conversationally without using tools",
		},
		{
			SessionId:        "llm-provider-test-simple-2",
			AccountId:        os.Getenv("TEST_ACCOUNT"),
			UserId:           os.Getenv("TEST_USER"),
			Query:            "List all pods in the nudgebee namespace",
			Description:      "Single kubectl command execution",
			Difficulty:       "SIMPLE",
			MinExpectedSteps: 1,
			ShouldUseTool:    true,
			ExpectedBehavior: "Should execute kubectl get pods command",
		},
		{
			SessionId:        "llm-provider-test-simple-3",
			AccountId:        os.Getenv("TEST_ACCOUNT"),
			UserId:           os.Getenv("TEST_USER"),
			Query:            "What is the status of llm-server pod?",
			Description:      "Get specific pod status with conditional logic",
			Difficulty:       "SIMPLE",
			MinExpectedSteps: 1,
			ShouldUseTool:    true,
			ExpectedBehavior: "Should get pod status and describe it",
		},
		// MEDIUM
		{
			SessionId:        "llm-provider-test-medium-1",
			AccountId:        os.Getenv("TEST_ACCOUNT"),
			UserId:           os.Getenv("TEST_USER"),
			Query:            "Why is the rag-server pod restarting? Check the last 50 logs.",
			Description:      "Multi-step investigation with specific parameters",
			Difficulty:       "MEDIUM",
			MinExpectedSteps: 2,
			ShouldUseTool:    true,
			ExpectedBehavior: "Should get pod status + logs with proper parameters",
		},
		{
			SessionId:        "llm-provider-test-medium-2",
			AccountId:        os.Getenv("TEST_ACCOUNT"),
			UserId:           os.Getenv("TEST_USER"),
			Query:            "Compare CPU and memory usage between llm-server and rag-server pods",
			Description:      "Parallel data gathering and comparison",
			Difficulty:       "MEDIUM",
			MinExpectedSteps: 2,
			ShouldUseTool:    true,
			ExpectedBehavior: "Should gather metrics from both pods and compare",
		},
		{
			SessionId:        "llm-provider-test-medium-3",
			AccountId:        os.Getenv("TEST_ACCOUNT"),
			UserId:           os.Getenv("TEST_USER"),
			Query:            "Check if any pods are in CrashLoopBackOff state. If yes, get their logs and events.",
			Description:      "Conditional logic with dependent steps",
			Difficulty:       "MEDIUM",
			MinExpectedSteps: 1,
			ShouldUseTool:    true,
			ExpectedBehavior: "Should check pod status, conditionally get logs/events",
		},
		// HARD
		{
			SessionId:        "llm-provider-test-hard-1",
			AccountId:        os.Getenv("TEST_ACCOUNT"),
			UserId:           os.Getenv("TEST_USER"),
			Query:            "Investigate high memory usage in the nudgebee namespace. Check pod metrics, identify top consumers, analyze their logs for memory leaks, and check if there are any OOMKilled events.",
			Description:      "Multi-phase RCA with metrics, logs, and events",
			Difficulty:       "HARD",
			MinExpectedSteps: 3,
			ShouldUseTool:    true,
			ExpectedBehavior: "Should perform comprehensive investigation: metrics → logs → events",
		},
		{
			SessionId:        "llm-provider-test-hard-2",
			AccountId:        os.Getenv("TEST_ACCOUNT"),
			UserId:           os.Getenv("TEST_USER"),
			Query:            "The llm-server is failing to connect to relay-server. Investigate the issue by checking: 1) Network connectivity 2) Service endpoints 3) Both pods' logs for connection errors 4) Any network policies that might be blocking traffic",
			Description:      "Cross-service debugging with network analysis",
			Difficulty:       "HARD",
			MinExpectedSteps: 4,
			ShouldUseTool:    true,
			ExpectedBehavior: "Should check connectivity, services, logs, and network policies",
		},
		{
			SessionId:        "llm-provider-test-hard-3",
			AccountId:        os.Getenv("TEST_ACCOUNT"),
			UserId:           os.Getenv("TEST_USER"),
			Query:            "Analyze performance bottlenecks in the nudgebee namespace. For each service: check resource utilization, identify pods approaching limits, examine slow query logs from database pods, and correlate with any recent scaling events or configuration changes.",
			Description:      "Comprehensive performance analysis with correlation",
			Difficulty:       "HARD",
			MinExpectedSteps: 3,
			ShouldUseTool:    true,
			ExpectedBehavior: "Should analyze resources, logs, and correlate events",
		},
		{
			SessionId:        "llm-provider-test-hard-4",
			AccountId:        os.Getenv("TEST_ACCOUNT"),
			UserId:           os.Getenv("TEST_USER"),
			Query:            "Database queries are slow. First check PostgreSQL pod status and resource usage. If resources are fine, analyze slow query logs. If no slow queries found, check for lock contention. Finally, verify index usage and suggest optimizations.",
			Description:      "Multi-step investigation with fallback strategies",
			Difficulty:       "HARD",
			MinExpectedSteps: 2,
			ShouldUseTool:    true,
			ExpectedBehavior: "Should demonstrate conditional branching and fallback logic",
		},
	}

	// Filter test cases by difficulty
	var filteredTests []struct {
		SessionId        string
		Query            string
		AccountId        string
		UserId           string
		Description      string
		Difficulty       string
		MinExpectedSteps int
		ShouldUseTool    bool
		ExpectedBehavior string
	}

	for _, tc := range testCases {
		if tc.Difficulty == difficulty {
			filteredTests = append(filteredTests, tc)
		}
	}

	// Run filtered tests
	for i, tc := range filteredTests {
		t.Run(fmt.Sprintf("%s_%d_%s", tc.Difficulty, i+1, tc.Description), func(t *testing.T) {
			sc := security.NewRequestContextForTenantAccountAdmin(os.Getenv("TEST_TENANT"), tc.UserId, []string{tc.AccountId})
			k8sAgent := newK8sDebugAgent(tc.AccountId)

			err := core.DeleteConversationBySession(tc.SessionId, tc.AccountId, tc.UserId)
			assert.Nil(t, err)

			resp, err := core.HandleConversationSessionRequest(sc, k8sAgent, tc.UserId, tc.AccountId, tc.SessionId, tc.Query)

			fmt.Printf("\n")
			fmt.Printf("================================================================================\n")
			fmt.Printf("🎯 Test #%d: [%s] %s\n", i+1, tc.Difficulty, tc.Description)
			fmt.Printf("================================================================================\n")
			fmt.Printf("📝 Query: %s\n", tc.Query)
			fmt.Printf("--------------------------------------------------------------------------------\n")
			fmt.Printf("💬 Response: %v\n", resp.Response)
			fmt.Printf("--------------------------------------------------------------------------------\n")

			if len(resp.AgentStepResponse) > 0 {
				fmt.Printf("🔧 Agent Execution Steps (%d steps):\n", len(resp.AgentStepResponse))
				invocationLog, _ := json.MarshalIndent(resp.AgentStepResponse, "", "  ")
				fmt.Printf("%s\n", string(invocationLog))
			} else {
				fmt.Printf("🔧 Agent Execution Steps: No tools used (knowledge-based response)\n")
			}

			fmt.Printf("--------------------------------------------------------------------------------\n")
			fmt.Printf("✅ Expected Behavior: %s\n", tc.ExpectedBehavior)
			fmt.Printf("================================================================================\n\n")

			assert.Nil(t, err)
			assert.NotNil(t, resp)
			assert.Greater(t, len(resp.Response), 0)

			if tc.ShouldUseTool {
				assert.NotNil(t, resp.AgentStepResponse)
				assert.GreaterOrEqual(t, len(resp.AgentStepResponse), tc.MinExpectedSteps)
			}

			t.Logf("Test completed: %s - %s", tc.Difficulty, tc.Description)
		})
	}
}
