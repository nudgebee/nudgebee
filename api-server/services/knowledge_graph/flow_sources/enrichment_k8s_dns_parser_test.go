package flow_sources

import "testing"

func TestLooksLikeK8sServiceName(t *testing.T) {
	parser := NewK8sDNSParser()

	tests := []struct {
		name     string
		input    string
		expected bool
	}{
		// Should look like K8s service names
		{
			name:     "service.namespace pattern",
			input:    "my-service.default",
			expected: true,
		},
		{
			name:     "service with hyphenated namespace",
			input:    "api-server.kube-system",
			expected: true,
		},

		// Should NOT look like K8s service names (external TLDs)
		{
			name:     "external .com domain",
			input:    "api.example.com",
			expected: false,
		},
		{
			name:     "external .io domain",
			input:    "service.example.io",
			expected: false,
		},
		{
			name:     "external .com domain with port",
			input:    "api.example.com:8080",
			expected: false,
		},
		{
			name:     "external .com domain with trailing dot",
			input:    "api.example.com.",
			expected: false,
		},
		{
			name:     "external domain with uppercase TLD",
			input:    "API.EXAMPLE.COM",
			expected: false,
		},

		// Regression: Contains matched TLD substrings mid-name
		{
			name:     "coordinator contains .co but is not a .co domain",
			input:    "payments.coordinator",
			expected: true,
		},
		{
			name:     "networking contains .net but is not a .net domain",
			input:    "svc.networking",
			expected: true,
		},

		// Should NOT look like K8s service names (other reasons)
		{
			name:     "AWS hostname",
			input:    "ec2.amazonaws.com",
			expected: false,
		},
		{
			name:     "uppercase name normalised to K8s pattern",
			input:    "MyService.Default",
			expected: true,
		},
		{
			name:     "no dot (ambiguous single word)",
			input:    "redis",
			expected: false,
		},
		{
			name:     "contains underscore",
			input:    "my_service.default",
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := parser.LooksLikeK8sServiceName(tt.input)
			if result != tt.expected {
				t.Errorf("LooksLikeK8sServiceName(%q) = %v, expected %v",
					tt.input, result, tt.expected)
			}
		})
	}
}
