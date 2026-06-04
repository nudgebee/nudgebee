package aws

import (
	"strings"
	"testing"
)

// TestSanitizeConfigExpression covers the three behaviors that matter:
//   - The single-quote escape (the actual injection defense).
//   - The 2048-char length cap.
//   - Pass-through of inputs without quotes (any ARN-shaped or other string).
//
// We intentionally do NOT enumerate "valid ARN" variants — the function makes no
// ARN-format claims and only escapes/caps. A representative pass-through case is
// enough; per-ARN-variant coverage belongs in the AWS Config integration layer,
// not here.
func TestSanitizeConfigExpression(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    string
		wantErr bool
	}{
		{
			// Pass-through: arbitrary non-quote input is returned unchanged.
			name:  "pass-through without single quotes",
			input: "arn:aws:ec2:us-east-1:123456789012:instance/i-1234567890abcdef0",
			want:  "arn:aws:ec2:us-east-1:123456789012:instance/i-1234567890abcdef0",
		},
		{
			// The single quote is the injection vector. After escape it becomes a
			// literal quote inside the AWS Config string literal, not a delimiter.
			name:  "single quote is escaped (injection blocked)",
			input: "arn:aws:ec2:us-east-1:123456789012:instance/i-123' OR '1'='1",
			want:  "arn:aws:ec2:us-east-1:123456789012:instance/i-123'' OR ''1''=''1",
		},
		{
			// Empty string is harmless — produces `WHERE x = ''`, matches nothing.
			name:  "empty string passes through",
			input: "",
			want:  "",
		},
		{
			name:    "value exceeding 2048-char cap is rejected",
			input:   strings.Repeat("a", 2049),
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := sanitizeConfigExpression(tt.input)
			if (err != nil) != tt.wantErr {
				t.Errorf("sanitizeConfigExpression(%q) error = %v, wantErr %v", tt.input, err, tt.wantErr)
				return
			}
			if !tt.wantErr && got != tt.want {
				t.Errorf("sanitizeConfigExpression(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}
