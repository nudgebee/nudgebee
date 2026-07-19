package common

import (
	"errors"
	"fmt"
	"testing"

	"google.golang.org/api/googleapi"
)

// TestIsDeterministicGCPError covers the classifier that decides whether a
// BigQuery billing-access failure hard-blocks onboarding. Permission/not-found/
// bad-request errors are definitive and must block; transient errors (rate
// limit, server error) and non-API errors must not.
func TestIsDeterministicGCPError(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{
			name: "403 access denied — the real billing-permission gap",
			err:  &googleapi.Error{Code: 403, Message: "Access Denied: ... User does not have permission to query table"},
			want: true,
		},
		{name: "404 table not found", err: &googleapi.Error{Code: 404, Message: "Not found: Table"}, want: true},
		{name: "401 unauthorized", err: &googleapi.Error{Code: 401, Message: "Unauthorized"}, want: true},
		{name: "400 bad request", err: &googleapi.Error{Code: 400, Message: "Unrecognized name"}, want: true},
		{name: "429 rate limited is transient", err: &googleapi.Error{Code: 429, Message: "Too Many Requests"}, want: false},
		{name: "500 server error is transient", err: &googleapi.Error{Code: 500, Message: "Internal Error"}, want: false},
		{name: "503 unavailable is transient", err: &googleapi.Error{Code: 503, Message: "Backend Error"}, want: false},
		{name: "non-API error (e.g. network/timeout) is not deterministic", err: errors.New("dial tcp: i/o timeout"), want: false},
		{name: "wrapped 403 still classified via errors.As", err: fmt.Errorf("query failed: %w", &googleapi.Error{Code: 403}), want: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isDeterministicGCPError(tt.err); got != tt.want {
				t.Errorf("isDeterministicGCPError() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestValidateBigQueryIdentifier(t *testing.T) {
	tests := []struct {
		name    string
		value   string
		wantErr bool
	}{
		{"valid project ID", "my-billing-project-123", false},
		{"valid domain-scoped project ID", "example.com:my-billing-project-123", false},
		{"valid dataset", "billing_export_dataset", false},
		{"valid table with prefix", "gcp_billing_export_v1_01AB23", false},
		{"backtick injection", "dataset`; DROP TABLE users; --", true},
		{"empty string", "", true},
		{"starts with hyphen", "-invalid", true},
		{"contains spaces", "my dataset", true},
		{"contains backtick", "data`set", true},
		{"contains semicolon", "dataset;drop", true},
		{"valid single char", "a", false},
		{"dots allowed", "project.id", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateBigQueryIdentifier(tt.value, "test field")
			if (err != nil) != tt.wantErr {
				t.Errorf("validateBigQueryIdentifier(%q) error = %v, wantErr %v", tt.value, err, tt.wantErr)
			}
		})
	}
}
