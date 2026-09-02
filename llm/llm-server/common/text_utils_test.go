package common

import (
	"fmt"
	"strings"
	"testing"
	"unicode/utf8"

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
		{name: "Comma after mention is not part of the name", input: "@k8s_debug, investigate", expected: "investigate"},
		{name: "Repeated mentions are all removed", input: "@a @b @c investigate", expected: "investigate"},
		{name: "Repeated mentions without spaces are all removed", input: "@a@b@c investigate", expected: "investigate"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := StripLeadingAgentMention(tt.input)
			assert.Equal(t, tt.expected, got)
		})
	}
}

func TestStripFirstAgentMention(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{name: "Empty string", input: "", expected: ""},
		{name: "No mention", input: "check my pods", expected: "check my pods"},
		{name: "Single mention dropped", input: "@aws_debug check my EC2 instances", expected: "check my EC2 instances"},
		{name: "Single mention only", input: "@aws_debug", expected: ""},
		{name: "Comma after mention is not part of the name", input: "@finops, which pvcs", expected: "which pvcs"},
		{name: "Extra mentions are kept", input: "@a @b @c check my pods", expected: "@b @c check my pods"},
		{name: "Extra mention kept without space", input: "@a@b query", expected: "@b query"},
		{name: "At-symbol mid-query is preserved", input: "email user@example.com", expected: "email user@example.com"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := StripFirstAgentMention(tt.input)
			assert.Equal(t, tt.expected, got)
		})
	}
}

func TestParseAgentMention(t *testing.T) {
	tests := []struct {
		name      string
		input     string
		wantAgent string
		wantRest  string
	}{
		// Punctuation after the name must not become part of the name, otherwise
		// the agent lookup misses and the router falls through to HelpAgent.
		{name: "Comma after name", input: "@k8s_orchestrator, check my pods", wantAgent: "k8s_orchestrator", wantRest: "check my pods"},
		{name: "Colon after name", input: "@a: check", wantAgent: "a", wantRest: "check"},
		{name: "Period after name", input: "@a. check", wantAgent: "a", wantRest: "check"},
		{name: "Semicolon after name", input: "@a; check", wantAgent: "a", wantRest: "check"},
		{name: "Exclamation after name", input: "@a! check", wantAgent: "a", wantRest: "check"},
		{name: "Question mark after name", input: "@a? check", wantAgent: "a", wantRest: "check"},

		// Repeated mentions: the first wins, the rest are dropped from the query
		// so they don't leak into the agent prompt or the conversation title.
		{name: "Repeated mentions pick the first", input: "@a @b @c check my pods", wantAgent: "a", wantRest: "check my pods"},
		{name: "Repeated mentions without spaces", input: "@a@b@c check my pods", wantAgent: "a", wantRest: "check my pods"},
		{name: "Repeated mentions with separators", input: "@a , @b , check", wantAgent: "a", wantRest: "check"},
		{name: "Repeated mentions only", input: "@a @b", wantAgent: "a", wantRest: ""},

		// Text that must survive: the separator class excludes "-" and "/".
		{name: "Double-dash flag preserved", input: "@a --verbose check", wantAgent: "a", wantRest: "--verbose check"},
		{name: "Single-dash flag preserved", input: "@a -n kube-system", wantAgent: "a", wantRest: "-n kube-system"},
		{name: "Slash command preserved", input: "@a /call foo", wantAgent: "a", wantRest: "/call foo"},
		{name: "Email in body preserved", input: "@a check user@example.com", wantAgent: "a", wantRest: "check user@example.com"},

		// No leading mention.
		{name: "Empty string", input: "", wantAgent: "", wantRest: ""},
		{name: "Whitespace only", input: "   ", wantAgent: "", wantRest: ""},
		{name: "No mention", input: "check my pods", wantAgent: "", wantRest: "check my pods"},
		{name: "Email is not a mention", input: "email user@example.com", wantAgent: "", wantRest: "email user@example.com"},

		// Mention-only queries yield an empty rest, which callers use to return a
		// clarification prompt instead of invoking the agent with no instruction.
		{name: "Mention only", input: "@aws_debug", wantAgent: "aws_debug", wantRest: ""},
		{name: "Mention only with trailing comma", input: "@aws_debug,", wantAgent: "aws_debug", wantRest: ""},
		{name: "Mention with leading whitespace", input: "  @k8s_debug why is my pod failing", wantAgent: "k8s_debug", wantRest: "why is my pod failing"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotAgent, gotRest := ParseAgentMention(tt.input)
			assert.Equal(t, tt.wantAgent, gotAgent, "agent")
			assert.Equal(t, tt.wantRest, gotRest, "rest")
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

func TestTruncateHead(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		maxBytes int
		expected string
	}{
		{
			name:     "string shorter than maxBytes",
			input:    "hello world",
			maxBytes: 20,
			expected: "hello world",
		},
		{
			name:     "string exactly maxBytes",
			input:    "hello world",
			maxBytes: 11,
			expected: "hello world",
		},
		{
			name:     "pure ascii truncated cleanly",
			input:    "hello world",
			maxBytes: 5,
			expected: "hello",
		},
		{
			name:     "multi-byte utf8 character boundary preserved",
			input:    "hello " + "世界", // "世界" is 6 bytes (3 bytes each)
			maxBytes: 7,               // "hello " (6 bytes) + 1 byte of 世 -> should truncate to "hello "
			expected: "hello ",
		},
		{
			name:     "multi-byte utf8 exact character boundary",
			input:    "hello " + "世界",
			maxBytes: 9, // "hello " (6 bytes) + "世" (3 bytes) = 9 bytes
			expected: "hello 世",
		},
		{
			name:     "200 bytes boundary with 2-byte rune",
			input:    strings.Repeat("a", 199) + "é" + " tail",
			maxBytes: 200,
			expected: strings.Repeat("a", 199),
		},
		{
			name:     "empty string",
			input:    "",
			maxBytes: 10,
			expected: "",
		},
		{
			name:     "zero maxBytes",
			input:    "hello",
			maxBytes: 0,
			expected: "",
		},
		{
			name:     "negative maxBytes",
			input:    "hello",
			maxBytes: -5,
			expected: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := TruncateHead(tt.input, tt.maxBytes)
			assert.Equal(t, tt.expected, got)
			assert.True(t, utf8.ValidString(got), "result must be valid UTF-8")
			if tt.maxBytes > 0 {
				assert.LessOrEqual(t, len(got), tt.maxBytes)
			} else {
				assert.Equal(t, 0, len(got))
			}
		})
	}
}
