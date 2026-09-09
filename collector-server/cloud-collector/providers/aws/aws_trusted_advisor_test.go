package aws

import (
	"context"
	"errors"
	"os"
	"testing"

	"nudgebee/collector/cloud/providers"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMapTACategory(t *testing.T) {
	tests := []struct {
		name     string
		category string
		expected providers.RecommendationCategory
	}{
		{"cost_optimizing", "cost_optimizing", providers.RecommendationCategoryRightSizing},
		{"security", "security", providers.RecommendationCategorySecurity},
		{"fault_tolerance", "fault_tolerance", providers.RecommendationCategoryConfiguration},
		{"performance", "performance", providers.RecommendationCategoryConfiguration},
		{"service_limits", "service_limits", providers.RecommendationCategoryConfiguration},
		{"unknown category", "unknown", providers.RecommendationCategoryConfiguration},
		{"empty string", "", providers.RecommendationCategoryConfiguration},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := mapTACategory(tt.category)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestMapTAStatusToSeverity(t *testing.T) {
	tests := []struct {
		name     string
		status   *string
		expected providers.RecommendationSeverity
	}{
		{"nil status", nil, providers.RecommendationSeverityMedium},
		{"error status", strPtr("error"), providers.RecommendationSeverityHigh},
		{"warning status", strPtr("warning"), providers.RecommendationSeverityMedium},
		{"ok status", strPtr("ok"), providers.RecommendationSeverityLow},
		{"unknown status", strPtr("info"), providers.RecommendationSeverityLow},
		{"empty status", strPtr(""), providers.RecommendationSeverityLow},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := mapTAStatusToSeverity(tt.status)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestSanitizeMetadataKey(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{"simple lowercase", "region", "region"},
		{"with spaces", "Estimated Monthly Savings", "estimated_monthly_savings"},
		{"with parentheses", "Estimated Monthly Savings ($)", "estimated_monthly_savings_$"},
		{"with slashes", "Read/Write Capacity", "read_write_capacity"},
		{"mixed", "CPU Utilization (14-Day)", "cpu_utilization_14-day"},
		{"already snake_case", "instance_type", "instance_type"},
		{"empty string", "", ""},
		{"UPPERCASE", "STATUS", "status"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := sanitizeMetadataKey(tt.input)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestExtractTASavings(t *testing.T) {
	tests := []struct {
		name     string
		data     map[string]any
		expected float64
	}{
		{
			// Trusted Advisor renders money for display, so this is the shape
			// real metadata arrives in, not an edge case.
			"estimated_monthly_savings present",
			map[string]any{"estimated_monthly_savings": "$150.00"},
			150.00,
		},
		{
			"estimated_monthly_savings numeric string",
			map[string]any{"estimated_monthly_savings": "150.00"},
			150.00,
		},
		{
			"thousands separator",
			map[string]any{"estimated_monthly_savings": "$1,234.56"},
			1234.56,
		},
		{
			"currency-suffixed header key",
			map[string]any{"estimated_monthly_savings_$": "$2,000.00"},
			2000.00,
		},
		{
			"monthly_savings field",
			map[string]any{"monthly_savings": "75.50"},
			75.50,
		},
		{
			"no savings fields",
			map[string]any{"region": "us-east-1", "status": "warning"},
			0,
		},
		{
			"savings is non-string type",
			map[string]any{"estimated_monthly_savings": 100.0},
			100.0,
		},
		{
			"unparseable placeholder",
			map[string]any{"estimated_monthly_savings": "N/A"},
			0,
		},
		{
			"empty data",
			map[string]any{},
			0,
		},
		{
			"priority order: estimated_monthly_savings first",
			map[string]any{
				"estimated_monthly_savings": "100.00",
				"monthly_savings":           "50.00",
			},
			100.00,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := extractTASavings(tt.data)
			assert.InDelta(t, tt.expected, result, 0.01)
		})
	}
}

func TestIsAccessDeniedError(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		expected bool
	}{
		{"AccessDeniedException", errors.New("AccessDeniedException: User is not authorized"), true},
		{"SubscriptionRequiredException", errors.New("SubscriptionRequiredException: AWS Support is not enabled"), true},
		{"not subscribed", errors.New("account is not subscribed to AWS Support"), true},
		{"generic error", errors.New("connection timeout"), false},
		{"throttling", errors.New("ThrottlingException: Rate exceeded"), false},
		{"partial match in context", errors.New("something AccessDeniedException something"), true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := isAccessDeniedError(tt.err)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestStringValue(t *testing.T) {
	tests := []struct {
		name     string
		input    *string
		expected string
	}{
		{"nil", nil, ""},
		{"empty string", strPtr(""), ""},
		{"non-empty", strPtr("hello"), "hello"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := stringValue(tt.input)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func strPtr(s string) *string {
	return &s
}

func TestParseMoneyString(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected float64
		wantErr  bool
	}{
		{name: "currency prefix", input: "$36.14", expected: 36.14},
		{name: "thousands separator", input: "1,234.56", expected: 1234.56},
		{name: "currency and separator", input: "$1,234.56", expected: 1234.56},
		{name: "surrounding whitespace", input: "  $42.00 ", expected: 42.0},
		{name: "plain decimal", input: "36.14", expected: 36.14},
		{name: "integer", input: "150", expected: 150},
		{name: "negative with currency", input: "-$5.00", expected: -5.0},
		{name: "trailing parenthetical ignored", input: "$100.00 (20%)", expected: 100.0},
		{name: "leading percentage does not win over the amount", input: "(20%) of $100.00 estimated", expected: 100.0},
		{name: "sign after symbol", input: "$-5.00", expected: -5.0},
		{name: "rupee symbol", input: "₹2,500.50", expected: 2500.50},
		{name: "bare number when no currency symbol", input: "1234", expected: 1234},
		{name: "percent-only string has no currency, first number wins", input: "20%", expected: 20},
		{name: "empty", input: "", wantErr: true},
		{name: "placeholder dash", input: "-", wantErr: true},
		{name: "non numeric", input: "N/A", wantErr: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := parseMoneyString(tc.input)
			if tc.wantErr {
				assert.Error(t, err)
				return
			}
			assert.NoError(t, err)
			assert.InDelta(t, tc.expected, got, 0.0001)
		})
	}
}

func TestTrustedAdvisorIntegration(t *testing.T) {
	if os.Getenv("AWS_PROFILE") == "" && os.Getenv("AWS_ACCESS_KEY_ID") == "" {
		t.Skip("skipping integration test: set AWS_PROFILE=prod or AWS credentials to run")
	}

	service := &awsTrustedAdvisor{}
	ctx := providers.NewCloudProviderContext(context.Background())
	account := providers.Account{AccountNumber: testAWSAccountNumber}

	recommendations, err := service.GetRecommendations(ctx, account, providers.ListRecommendationsRequest{}, nil)
	require.NoError(t, err)

	t.Logf("fetched %d Trusted Advisor recommendations", len(recommendations))

	// Trusted Advisor may return 0 recommendations if no Business/Enterprise support
	// That's expected — the test validates graceful handling
	for i, rec := range recommendations {
		assert.Equal(t, "aws", rec.Data["source"])
		assert.Equal(t, ServiceNameTrustedAdvisor, rec.ResourceServiceName)
		assert.Equal(t, "trusted-advisor-check", rec.ResourceType)
		assert.Contains(t, rec.RuleName, "aws_native_ta_")

		t.Logf("  [%d] rule=%s category=%s severity=%s savings=%.2f region=%s",
			i, rec.RuleName, rec.CategoryName, rec.Severity, rec.Savings, rec.ResourceRegion)
	}
}
