package common

import "testing"

func TestIsValidKubernetesName(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected bool
	}{
		// Valid cases
		{"Valid simple", "frontend", true},
		{"Valid with hyphen", "my-service", true},
		{"Valid alphanumeric", "app123", true},
		{"Valid with hyphen and numbers", "k8s-app-1", true},
		{"Valid single char", "a", true},
		{"Valid single number", "1", true},

		// Invalid cases (security)
		{"SQL Injection 1", "frontend' OR 1=1 --", false},
		{"SQL Injection 2", "frontend; DROP TABLE users", false},
		{"SQL Injection 3", "frontend --", false},
		{"SQL Injection 4", "frontend' UNION SELECT", false},

		// Invalid cases (format)
		{"Empty string", "", false},
		{"Starts with hyphen", "-frontend", false},
		{"Ends with hyphen", "frontend-", false},
		{"Contains underscore", "frontend_backend", false},
		{"Contains uppercase", "Frontend", false},
		{"Contains dot", "frontend.service", false}, // DNS-1123 label doesn't allow dots (subdomain does, but label doesn't)
		{"Contains space", "frontend service", false},
		{"Contains special chars", "frontend@service", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := IsValidKubernetesName(tt.input); got != tt.expected {
				t.Errorf("IsValidKubernetesName(%q) = %v, want %v", tt.input, got, tt.expected)
			}
		})
	}
}
