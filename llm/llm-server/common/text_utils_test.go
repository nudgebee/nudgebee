package common

import (
	"fmt"
	"testing"

	"github.com/pkoukk/tiktoken-go"
	"github.com/stretchr/testify/assert"
)

// getTokenCount calculates the number of tokens in a text using the cl100k_base tokenizer.
// This is useful for comparing word count with token count to determine thresholds.
func getTokenCount(text string) (int, error) {
	tkm, err := tiktoken.GetEncoding("cl100k_base")
	if err != nil {
		return 0, err
	}
	tokens := tkm.Encode(text, nil, nil)
	return len(tokens), nil
}

func TestGetWordCount(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected int
	}{
		{name: "Empty string", input: "", expected: 0},
		{name: "Simple sentence", input: "hello world", expected: 2},
		{name: "Sentence with stop words", input: "this is a test", expected: 1}, // "test"
		{name: "Sentence with punctuation", input: "hello, world!", expected: 2},
		{name: "Mixed case", input: "Hello World", expected: 2},
		{name: "Numbers", input: "hello 123", expected: 2},
		{name: "Multiple spaces and newlines", input: "hello   \n world", expected: 2},
		{name: "Only stop words", input: "the is a an", expected: 0},
		{name: "Complex sentence", input: "The quick brown fox jumps over the lazy dog.", expected: 6}, // quick, brown, fox, jumps, lazy, dog
		{name: "Hyphenated word (resource name)", input: "my-app-deployment", expected: 1},
		{name: "Dotted word (version/IP)", input: "v1.2.3", expected: 1},
		{name: "Sentence ending with dot", input: "end of sentence.", expected: 2},      // "end", "sentence"
		{name: "Trailing punctuation with hyphen", input: "some-flag.", expected: 1},    // "some-flag"
		{name: "Leading hyphen (flag)", input: "-n default", expected: 2},               // "n", "default"
		{name: "Mixed separators", input: "pod:my-pod,namespace=default", expected: 4},  // "pod", "my-pod", "namespace", "default"
		{name: "User specific case", input: "get me logs of nudebee-test", expected: 3}, // "logs", "nudebee-test" (get, me, of are stop words)
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := GetWordCount(tt.input)
			assert.Equal(t, tt.expected, got)
		})
	}
}

func TestStripLeadingAgentMention(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{name: "Empty string", input: "", expected: ""},
		{name: "Whitespace only", input: "   ", expected: ""},
		{name: "No mention", input: "check my pods", expected: "check my pods"},
		{name: "No mention with surrounding whitespace is trimmed", input: "  check my pods  ", expected: "check my pods"},
		{name: "Simple mention", input: "@aws_debug check my EC2 instances", expected: "check my EC2 instances"},
		{name: "Mention with leading whitespace", input: "  @k8s_debug why is my pod failing", expected: "why is my pod failing"},
		{name: "Mention only", input: "@aws_debug", expected: ""},
		{name: "Mention only with trailing space", input: "@aws_debug   ", expected: ""},
		{name: "At-symbol mid-query is preserved", input: "email user@example.com", expected: "email user@example.com"},
		{name: "Multiple spaces after mention collapse via trim", input: "@k8s_debug    investigate", expected: "investigate"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := StripLeadingAgentMention(tt.input)
			assert.Equal(t, tt.expected, got)
		})
	}
}

func TestTokenCountComparison(t *testing.T) {
	queries := []string{
		"hello",
		"show me pods in kube-system",
		"get me logs of nudebee-test",
		"kubectl get pods -n default",
		"pod-restart-policy: always",
		"v1.2.3-beta.1",
		"my_variable_name",
		"I want to scale up the deployment backend-service to 5 replicas because we are expecting high traffic.",
	}

	fmt.Println("\n--- Word Count vs Token Count Comparison ---")
	fmt.Printf("%-100s | %-10s | %-10s\n", "Query", "Word Count", "Token Count")
	fmt.Println("----------------------------------------------------------------------------------------------------------------------------------------")

	for _, q := range queries {
		wc := GetWordCount(q)
		tc, err := getTokenCount(q)
		if err != nil {
			t.Logf("Error counting tokens for query '%s': %v", q, err)
			tc = -1
		}
		fmt.Printf("%-100s | %-10d | %-10d\n", q, wc, tc)
	}
	fmt.Println("--------------------------------------------")
}
