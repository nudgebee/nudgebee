package core

import (
	"testing"
)

func TestGetPrimaryLanguage(t *testing.T) {
	tests := []struct {
		name     string
		input    []string
		expected string
	}{
		{
			name:     "Empty array",
			input:    []string{},
			expected: "",
		},
		{
			name:     "Single canonical name - Golang",
			input:    []string{"Golang"},
			expected: "Golang",
		},
		{
			name:     "Single alias - go",
			input:    []string{"go"},
			expected: "Golang",
		},
		{
			name:     "Single alias - GO",
			input:    []string{"GO"},
			expected: "Golang",
		},
		{
			name:     "Single canonical name - Python",
			input:    []string{"Python"},
			expected: "Python",
		},
		{
			name:     "Single alias - python",
			input:    []string{"python"},
			expected: "Python",
		},
		{
			name:     "Single alias - py",
			input:    []string{"py"},
			expected: "Python",
		},
		{
			name:     "Single canonical name - Java",
			input:    []string{"Java"},
			expected: "Java",
		},
		{
			name:     "Single alias - java",
			input:    []string{"java"},
			expected: "Java",
		},
		{
			name:     "JavaScript aliases - js",
			input:    []string{"js"},
			expected: "JavaScript",
		},
		{
			name:     "JavaScript aliases - node",
			input:    []string{"node"},
			expected: "JavaScript",
		},
		{
			name:     "JavaScript aliases - Node.js",
			input:    []string{"Node.js"},
			expected: "JavaScript",
		},
		{
			name:     "Multiple languages - first wins (Python, Golang)",
			input:    []string{"Python", "Golang"},
			expected: "Python",
		},
		{
			name:     "Multiple languages - first wins (Golang, Python)",
			input:    []string{"Golang", "Python"},
			expected: "Golang",
		},
		{
			name:     "Multiple languages with aliases - first wins",
			input:    []string{"go", "python"},
			expected: "Golang",
		},
		{
			name:     "TypeScript",
			input:    []string{"TypeScript"},
			expected: "TypeScript",
		},
		{
			name:     "TypeScript alias - ts",
			input:    []string{"ts"},
			expected: "TypeScript",
		},
		{
			name:     "Rust",
			input:    []string{"Rust"},
			expected: "Rust",
		},
		{
			name:     "Rust alias - rust",
			input:    []string{"rust"},
			expected: "Rust",
		},
		{
			name:     "C++",
			input:    []string{"C++"},
			expected: "C++",
		},
		{
			name:     "C++ alias - cpp",
			input:    []string{"cpp"},
			expected: "C++",
		},
		{
			name:     "C# alias - csharp",
			input:    []string{"csharp"},
			expected: "C#",
		},
		{
			name:     "Unknown language - returned as-is",
			input:    []string{"UnknownLang"},
			expected: "UnknownLang",
		},
		{
			name:     "Empty string in array",
			input:    []string{""},
			expected: "",
		},
		{
			name:     "Empty strings with valid language after",
			input:    []string{"", "", "Python"},
			expected: "Python",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := GetPrimaryLanguage(tt.input)
			if result != tt.expected {
				t.Errorf("GetPrimaryLanguage(%v) = %q, want %q", tt.input, result, tt.expected)
			}
		})
	}
}
