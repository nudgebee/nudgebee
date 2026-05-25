package tools

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestCleanMermaidCode(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "plain code",
			input:    "graph TD\nA-->B",
			expected: "graph TD\nA-->B",
		},
		{
			name:     "with mermaid block",
			input:    "```mermaid\ngraph TD\nA-->B\n```",
			expected: "graph TD\nA-->B",
		},
		{
			name:     "with generic block",
			input:    "```\ngraph TD\nA-->B\n```",
			expected: "graph TD\nA-->B",
		},
		{
			name:     "with whitespace",
			input:    "  graph TD\nA-->B  ",
			expected: "graph TD\nA-->B",
		},
		{
			name:     "wrapped with extra newlines",
			input:    "\n\n```mermaid\ngraph TD\nA-->B\n```\n\n",
			expected: "graph TD\nA-->B",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := cleanMermaidCode(tt.input)
			assert.Equal(t, tt.expected, got)
		})
	}
}

func TestValidateMermaidCode(t *testing.T) {
	tests := []struct {
		name          string
		code          string
		expectedError bool
		errorCount    int
	}{
		{
			name:          "valid simple graph",
			code:          "graph TD\nA[\"Start\"] --> B[\"Stop\"]",
			expectedError: false,
			errorCount:    0,
		},
		{
			name:          "invalid single percent comment",
			code:          "graph TD\n% This is a comment\nA-->B",
			expectedError: true,
			errorCount:    1,
		},
		{
			name:          "valid double percent comment",
			code:          "graph TD\n%% This is a comment\nA-->B",
			expectedError: false,
			errorCount:    0,
		},
		{
			name:          "invalid unquoted node label",
			code:          "graph TD\nA[Start] --> B(Stop)",
			expectedError: true,
			errorCount:    2, // One for each node
		},
		{
			name:          "invalid unquoted subgraph title",
			code:          "subgraph My Subgraph\nA-->B\nend",
			expectedError: true,
			errorCount:    1,
		},
		{
			name:          "valid quoted subgraph title",
			code:          "subgraph \"My Subgraph\"\nA-->B\nend",
			expectedError: false,
			errorCount:    0,
		},
		{
			name:          "valid subgraph id title",
			code:          "subgraph ID [\"My Subgraph\"]\nA-->B\nend",
			expectedError: false,
			errorCount:    0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			errors := validateMermaidCode(tt.code)
			if tt.expectedError {
				assert.NotEmpty(t, errors, "Expected errors but got none")
				assert.Equal(t, tt.errorCount, len(errors), "Unexpected number of errors")
			} else {
				assert.Empty(t, errors, "Expected no errors but got some")
			}
		})
	}
}

func TestAgentVisualizationScenarios(t *testing.T) {
	tests := []struct {
		name          string
		code          string
		expectedError bool
	}{
		{
			name:          "Correct: Standard Quoted Node",
			code:          "graph TD\n S1[\"Service (v1)\"]",
			expectedError: false,
		},
		{
			name:          "Correct: Round Brackets Quoted",
			code:          "graph TD\n S1([\"Service (v1)\"]) ",
			expectedError: false,
		},
		{
			name:          "Correct: Double Brace Quoted",
			code:          "graph TD\n S1{{\"Service (v1)\"}} ",
			expectedError: false,
		},
		{
			name:          "Incorrect: Unquoted Round Brackets with parens/spaces",
			code:          "graph TD\nS1(Service (v1))",
			expectedError: true,
		},

		// 2. Subgraphs
		{
			name:          "Correct: Quoted Subgraph",
			code:          "subgraph \"Cluster (Prod)\"\nA-->B\nend",
			expectedError: false,
		},
		{
			name:          "Correct: ID with Quoted Label Subgraph",
			code:          "subgraph C1 [\"Cluster (Prod)\"]\nA-->B\nend",
			expectedError: false,
		},
		{
			name:          "Incorrect: Unquoted Subgraph Title",
			code:          "subgraph Cluster (Prod)\nA-->B\nend",
			expectedError: true,
		},

		// 3. Comments
		{
			name:          "Correct: Double Percent Comment",
			code:          "graph TD\n%% This is a comment\nA-->B",
			expectedError: false,
		},
		{
			name:          "Incorrect: Single Percent Comment",
			code:          "graph TD\n% This is a comment\nA-->B",
			expectedError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			errors := validateMermaidCode(tt.code)
			if tt.expectedError {
				assert.NotEmpty(t, errors, "Expected validation errors for case: %s", tt.name)
			} else {
				assert.Empty(t, errors, "Expected no validation errors for case: %s", tt.name)
			}
		})
	}
}

func TestComplexScenario(t *testing.T) {
	code := "graph TD\n    subgraph \"Namespace: nudgebee\"\n        K8S_DEPLOYMENT[\"Deployment: llm-server\"]\n        POD_SET[\"ReplicaSet\"]\n        POD_1[\"Pod (llm-server-xyz1)\"]\n        POD_2[\"Pod (llm-server-abc2)\"]\n    end\n\n    subgraph \"Dependencies\"\n        MODEL_REGISTRY[\"Model Registry (S3/GCS)\"]\n        CONFIG_MAP[\"ConfigMap: llm-config\"]\n        SECRET[\"Secret: api-keys\"]\n        SERVICE[\"Service: llm-service\"]\n    end\n\n    %% Deployment to ReplicaSet\n    K8S_DEPLOYMENT --> POD_SET\n    \n    %% ReplicaSet to Pods\n    POD_SET --> POD_1\n    POD_SET --> POD_2\n    \n    %% Pod Dependencies\n    POD_1 -- Pulls Image From --> MODEL_REGISTRY\n    POD_1 -- Mounts --> CONFIG_MAP\n    POD_1 -- Uses --> SECRET\n    \n    POD_2 -- Pulls Image From --> MODEL_REGISTRY\n    POD_2 -- Mounts --> CONFIG_MAP\n    POD_2 -- Uses --> SECRET\n    \n    %% External Exposure\n    SERVICE -- Routes Traffic To --> POD_1\n    SERVICE -- Routes Traffic To --> POD_2\n    \n    %% Styling (Optional but helpful for clarity)\\n    classDef deployment fill:#f9f,stroke:#333,stroke-width:2px;\n    class K8S_DEPLOYMENT deployment;"

	t.Run("Valid Complex K8s Architecture", func(t *testing.T) {
		errors := validateMermaidCode(code)
		assert.Empty(t, errors, "Expected no validation errors for complex scenario")
	})
}

func TestXyChartArrays(t *testing.T) {
	t.Run("Invalid: Multi-line x-axis array (xychart-beta)", func(t *testing.T) {
		code := "xychart-beta\n    x-axis [\n        \"Jan\", \"Feb\"\n    ]\n    bar [10, 20]"
		errors := validateMermaidCode(code)
		assert.NotEmpty(t, errors, "Should detect multi-line x-axis array")
		if len(errors) > 0 {
			assert.Contains(t, errors[0], "single line", "Error message should mention single line requirement")
		}
	})

	t.Run("Invalid: Multi-line bar array (xychart)", func(t *testing.T) {
		code := "xychart\n    x-axis [\"Jan\", \"Feb\"]\n    bar [\n        10,\n        20\n    ]"
		errors := validateMermaidCode(code)
		assert.NotEmpty(t, errors, "Should detect multi-line bar array")
		if len(errors) > 0 {
			assert.Contains(t, errors[0], "single line", "Error message should mention single line requirement")
		}
	})

	t.Run("Valid: Single-line arrays", func(t *testing.T) {
		code := "xychart\n    x-axis [\"Jan\", \"Feb\"]\n    bar [10, 20]"
		errors := validateMermaidCode(code)
		assert.Empty(t, errors, "Should accept valid single-line arrays")
	})

	t.Run("Valid: Pie Chart", func(t *testing.T) {
		code := "pie title Pets\n    \"Dogs\" : 386\n    \"Cats\" : 85"
		errors := validateMermaidCode(code)
		assert.Empty(t, errors, "Valid pie chart should pass validation")
	})

	t.Run("Invalid: null values in line array", func(t *testing.T) {
		code := "xychart\n    x-axis [\"T1\", \"T2\"]\n    line \"Series 1\" [10.5, null]"
		errors := validateMermaidCode(code)
		assert.NotEmpty(t, errors, "Should detect 'null' values in line array")
		assert.Contains(t, errors[0], "null", "Error message should mention 'null' is not supported")
	})

	t.Run("Invalid: strings in bar array", func(t *testing.T) {
		code := "xychart\n    x-axis [\"T1\", \"T2\"]\n    bar [\"10.5\", \"20.0\"]"
		errors := validateMermaidCode(code)
		assert.NotEmpty(t, errors, "Should detect string values in bar array")
		assert.Contains(t, errors[0], "numeric", "Error message should mention numeric requirement")
	})
}

func TestClassDiagramSupport(t *testing.T) {
	t.Run("Valid: Class Diagram (Brackets should not trigger node check)", func(t *testing.T) {
		code := "classDiagram\n    class BankAccount {\n        +String owner\n        +BigDecimal balance\n        +deposit(amount)\n        +withdrawal(amount)\n    }"
		errors := validateMermaidCode(code)
		assert.Empty(t, errors, "Class diagram should pass validation")
	})

	t.Run("Valid: Sequence Diagram (No node check)", func(t *testing.T) {
		code := "sequenceDiagram\n    Alice->>Bob: Hello Bob, how are you?\n    alt is sick\n        Bob->>Alice: Not so good :(\n    else is well\n        Bob->>Alice: Feeling fresh like a daisy\n    end"
		errors := validateMermaidCode(code)
		assert.Empty(t, errors, "Sequence diagram should pass validation")
	})
}
