package utils

import (
	"errors"
	"testing"
)

func TestTicketIDValidationError_Error(t *testing.T) {
	err := &TicketIDValidationError{TicketID: "BAD/ID", Reason: "contains path separator"}
	want := "invalid ticket ID 'BAD/ID': contains path separator"
	if got := err.Error(); got != want {
		t.Errorf("Error() = %q, want %q", got, want)
	}
}

func TestValidateTicketID(t *testing.T) {
	tests := []struct {
		name     string
		ticketID string
		wantErr  bool
	}{
		{name: "valid simple", ticketID: "INC0001", wantErr: false},
		{name: "valid numeric", ticketID: "12345", wantErr: false},
		{name: "empty", ticketID: "", wantErr: true},
		{name: "path traversal", ticketID: "..\\etc", wantErr: true},
		{name: "forward slash", ticketID: "a/b", wantErr: true},
		{name: "back slash", ticketID: "a\\b", wantErr: true},
		{name: "question mark", ticketID: "a?b", wantErr: true},
		{name: "ampersand", ticketID: "a&b", wantErr: true},
		{name: "caret operator", ticketID: "a^b", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateTicketID(tt.ticketID)
			if (err != nil) != tt.wantErr {
				t.Errorf("ValidateTicketID(%q) error = %v, wantErr %v", tt.ticketID, err, tt.wantErr)
			}
		})
	}
}

func TestValidateJiraTicketID(t *testing.T) {
	tests := []struct {
		name     string
		ticketID string
		wantErr  bool
	}{
		{name: "valid key", ticketID: "PROJ-123", wantErr: false},
		{name: "valid numeric", ticketID: "456", wantErr: false},
		{name: "lowercase key", ticketID: "proj-1", wantErr: false},
		{name: "missing number", ticketID: "PROJ-", wantErr: true},
		{name: "no hyphen", ticketID: "PROJ123", wantErr: true},
		{name: "empty", ticketID: "", wantErr: true},
		{name: "injection char", ticketID: "PROJ-1^a", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateJiraTicketID(tt.ticketID)
			if (err != nil) != tt.wantErr {
				t.Errorf("ValidateJiraTicketID(%q) error = %v, wantErr %v", tt.ticketID, err, tt.wantErr)
			}
		})
	}
}

func TestValidateServiceNowTicketID(t *testing.T) {
	tests := []struct {
		name     string
		ticketID string
		wantErr  bool
	}{
		{name: "INC number", ticketID: "INC0010023", wantErr: false},
		{name: "CHG number", ticketID: "CHG0001", wantErr: false},
		{name: "plain numeric", ticketID: "12345", wantErr: false},
		{name: "32-char sys_id", ticketID: "0123456789abcdef0123456789abcdef", wantErr: false},
		{name: "invalid prefix", ticketID: "FOO0001", wantErr: true},
		{name: "short hex", ticketID: "abcdef", wantErr: true},
		{name: "empty", ticketID: "", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateServiceNowTicketID(tt.ticketID)
			if (err != nil) != tt.wantErr {
				t.Errorf("ValidateServiceNowTicketID(%q) error = %v, wantErr %v", tt.ticketID, err, tt.wantErr)
			}
		})
	}
}

func TestValidateNumericIssueIDs(t *testing.T) {
	tests := []struct {
		name     string
		ticketID string
		wantErr  bool
	}{
		{name: "numeric", ticketID: "42", wantErr: false},
		{name: "non-numeric", ticketID: "abc", wantErr: true},
		{name: "empty", ticketID: "", wantErr: true},
		{name: "mixed", ticketID: "12a", wantErr: true},
	}

	for _, tt := range tests {
		t.Run("github/"+tt.name, func(t *testing.T) {
			if err := ValidateGitHubIssueID(tt.ticketID); (err != nil) != tt.wantErr {
				t.Errorf("ValidateGitHubIssueID(%q) error = %v, wantErr %v", tt.ticketID, err, tt.wantErr)
			}
		})
		t.Run("gitlab/"+tt.name, func(t *testing.T) {
			if err := ValidateGitLabIssueID(tt.ticketID); (err != nil) != tt.wantErr {
				t.Errorf("ValidateGitLabIssueID(%q) error = %v, wantErr %v", tt.ticketID, err, tt.wantErr)
			}
		})
	}
}

func TestValidatePagerDutyIncidentID(t *testing.T) {
	tests := []struct {
		name     string
		ticketID string
		wantErr  bool
	}{
		{name: "alphanumeric", ticketID: "P1234ABC", wantErr: false},
		{name: "digits only", ticketID: "9876", wantErr: false},
		{name: "with hyphen", ticketID: "P-1234", wantErr: true},
		{name: "empty", ticketID: "", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidatePagerDutyIncidentID(tt.ticketID)
			if (err != nil) != tt.wantErr {
				t.Errorf("ValidatePagerDutyIncidentID(%q) error = %v, wantErr %v", tt.ticketID, err, tt.wantErr)
			}
		})
	}
}

func TestValidateZenDutyIncidentID(t *testing.T) {
	tests := []struct {
		name     string
		ticketID string
		wantErr  bool
	}{
		{name: "numeric", ticketID: "12345", wantErr: false},
		{name: "uuid-like", ticketID: "a1b2-c3d4-e5f6", wantErr: false},
		{name: "underscore not allowed", ticketID: "a_b", wantErr: true},
		{name: "empty", ticketID: "", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateZenDutyIncidentID(tt.ticketID)
			if (err != nil) != tt.wantErr {
				t.Errorf("ValidateZenDutyIncidentID(%q) error = %v, wantErr %v", tt.ticketID, err, tt.wantErr)
			}
		})
	}
}

func TestValidateProjectKey(t *testing.T) {
	tests := []struct {
		name       string
		projectKey string
		wantErr    bool
	}{
		{name: "valid owner/repo", projectKey: "owner/repo", wantErr: false},
		{name: "valid nested group", projectKey: "group/sub/project", wantErr: false},
		{name: "valid with dots and dashes", projectKey: "my-org/my.repo_1", wantErr: false},
		{name: "empty", projectKey: "", wantErr: true},
		{name: "path traversal", projectKey: "../etc", wantErr: true},
		{name: "backslash", projectKey: "a\\b", wantErr: true},
		{name: "question mark", projectKey: "a?b/c", wantErr: true},
		{name: "hash", projectKey: "a#b/c", wantErr: true},
		{name: "caret", projectKey: "a^b/c", wantErr: true},
		{name: "space", projectKey: "a b/c", wantErr: true},
		{name: "empty segment", projectKey: "owner//repo", wantErr: true},
		{name: "trailing slash empty segment", projectKey: "owner/", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateProjectKey(tt.projectKey)
			if (err != nil) != tt.wantErr {
				t.Errorf("ValidateProjectKey(%q) error = %v, wantErr %v", tt.projectKey, err, tt.wantErr)
			}
		})
	}
}

func TestValidateTicketID_ReturnsTypedError(t *testing.T) {
	err := ValidateTicketID("")
	var validationErr *TicketIDValidationError
	if !errors.As(err, &validationErr) {
		t.Fatalf("expected *TicketIDValidationError, got %T", err)
	}
	if validationErr.Reason == "" {
		t.Error("expected a non-empty Reason on the validation error")
	}
}
