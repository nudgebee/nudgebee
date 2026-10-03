package agents

import "testing"

func TestGateVerdictPassed(t *testing.T) {
	cases := []struct {
		name     string
		response string
		want     bool
	}{
		{
			name:     "bare pass prefix",
			response: "PASS: Target file is correct",
			want:     true,
		},
		{
			// The incident: the reviewer reasons step by step and only then
			// states the verdict, so PASS is the last line, not the prefix.
			name: "pass after step-by-step reasoning",
			response: "Step-by-step analysis:\n" +
				"1. Entity described in the issue: optimise Prometheus memory usage.\n" +
				"2. Modified file(s): deploy/kubernetes/prometheus/values-prod.yaml.\n" +
				"3. Comparison with requirements: the diff implements all four requirements.\n\n" +
				"PASS: Target file is correct",
			want: true,
		},
		{
			name:     "markdown-wrapped pass verdict with colon inside",
			response: "Here is my review.\n\n**PASS:** the change is coherent",
			want:     true,
		},
		{
			name:     "markdown-wrapped pass verdict with colon outside",
			response: "Here is my review.\n\n**PASS**: the change is coherent",
			want:     true,
		},
		{
			name:     "markdown-wrapped pass verdict without colon",
			response: "Here is my review.\n\n**PASS**",
			want:     true,
		},
		{
			name:     "fail verdict at end",
			response: "1. Issue mentions service X.\n2. Modified file belongs to service Y.\n\nFAIL: wrong target file",
			want:     false,
		},
		{
			name:     "later fail overrides earlier tentative pass",
			response: "PASS: looks plausible at first glance\nOn closer look the indentation is broken\nFAIL: indentation mismatch on line 12",
			want:     false,
		},
		{
			name:     "reasoning that merely mentions failing does not flip a pass",
			response: "PASS: Variable scope verified\nNote: this line does not fail any check",
			want:     true,
		},
		{
			// Prose that starts with the word "pass" but is not a verdict line
			// must not be read as a verdict.
			name:     "prose beginning with pass is not a verdict",
			response: "FAIL: the target file is wrong\nPassing this check would require touching client.go",
			want:     false,
		},
		{
			name:     "no verdict line is treated as not passed",
			response: "The modified file looks reasonable but I am not certain.",
			want:     false,
		},
		{
			name:     "empty response is not passed",
			response: "",
			want:     false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := gateVerdictPassed(tc.response); got != tc.want {
				t.Fatalf("gateVerdictPassed() = %v, want %v", got, tc.want)
			}
		})
	}
}
