package utils

import (
	"fmt"
	"regexp"
	"strings"
)

// Pre-compiled validation patterns — regexp.MustCompile costs ~1-5µs per call;
// these functions sit on the request path for every ticket operation.
var (
	jiraKeyPattern     = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_]*-\d+$|^\d+$`)
	incidentPattern    = regexp.MustCompile(`^(INC|CHG|PRB|REQ|RITM|SCTASK)?\d+$|^[a-f0-9]{32}$`)
	numericIDPattern   = regexp.MustCompile(`^\d+$`)
	alphanumPattern    = regexp.MustCompile(`^[A-Za-z0-9]+$`)
	safeSegmentPattern = regexp.MustCompile(`^[A-Za-z0-9._-]+$`)
	zdIncidentPattern  = regexp.MustCompile(`^[A-Za-z0-9\-]+$`)
)

// TicketIDValidationError represents a ticket ID validation failure
type TicketIDValidationError struct {
	TicketID string
	Reason   string
}

func (e *TicketIDValidationError) Error() string {
	return fmt.Sprintf("invalid ticket ID '%s': %s", e.TicketID, e.Reason)
}

// ValidateTicketID validates that a ticket ID does not contain path traversal
// or injection characters. Returns an error if validation fails.
func ValidateTicketID(ticketID string) error {
	if ticketID == "" {
		return &TicketIDValidationError{TicketID: ticketID, Reason: "ticket ID cannot be empty"}
	}

	// Check for path traversal sequences
	if strings.Contains(ticketID, "..") {
		return &TicketIDValidationError{TicketID: ticketID, Reason: "contains path traversal sequence"}
	}

	// Check for URL path separators
	if strings.Contains(ticketID, "/") || strings.Contains(ticketID, "\\") {
		return &TicketIDValidationError{TicketID: ticketID, Reason: "contains path separator"}
	}

	// Check for query string characters that could be used for injection
	if strings.Contains(ticketID, "?") || strings.Contains(ticketID, "&") {
		return &TicketIDValidationError{TicketID: ticketID, Reason: "contains query string characters"}
	}

	// Check for ServiceNow query operators (^ is used for AND/OR)
	if strings.Contains(ticketID, "^") {
		return &TicketIDValidationError{TicketID: ticketID, Reason: "contains query operator characters"}
	}

	return nil
}

// ValidateJiraTicketID validates a Jira issue key format (PROJECT-123)
func ValidateJiraTicketID(ticketID string) error {
	if err := ValidateTicketID(ticketID); err != nil {
		return err
	}

	// Jira issue key format: PROJECT-123 or just a numeric ID
	if !jiraKeyPattern.MatchString(ticketID) {
		return &TicketIDValidationError{
			TicketID: ticketID,
			Reason:   "invalid Jira issue key format (expected PROJECT-123 or numeric ID)",
		}
	}

	return nil
}

// ValidateServiceNowTicketID validates a ServiceNow incident number format
func ValidateServiceNowTicketID(ticketID string) error {
	if err := ValidateTicketID(ticketID); err != nil {
		return err
	}

	// ServiceNow incident numbers typically follow INC0000001 pattern or sys_id (32-char hex)
	if !incidentPattern.MatchString(ticketID) {
		return &TicketIDValidationError{
			TicketID: ticketID,
			Reason:   "invalid ServiceNow ticket ID format",
		}
	}

	return nil
}

// ValidateGitHubIssueID validates a GitHub issue number (numeric)
func ValidateGitHubIssueID(ticketID string) error {
	if err := ValidateTicketID(ticketID); err != nil {
		return err
	}

	// GitHub issue IDs are numeric
	if !numericIDPattern.MatchString(ticketID) {
		return &TicketIDValidationError{
			TicketID: ticketID,
			Reason:   "invalid GitHub issue ID format (expected numeric)",
		}
	}

	return nil
}

// ValidateGitLabIssueID validates a GitLab issue IID (numeric)
func ValidateGitLabIssueID(ticketID string) error {
	if err := ValidateTicketID(ticketID); err != nil {
		return err
	}

	// GitLab issue IIDs are numeric
	if !numericIDPattern.MatchString(ticketID) {
		return &TicketIDValidationError{
			TicketID: ticketID,
			Reason:   "invalid GitLab issue IID format (expected numeric)",
		}
	}

	return nil
}

// ValidatePagerDutyIncidentID validates a PagerDuty incident ID
func ValidatePagerDutyIncidentID(ticketID string) error {
	if err := ValidateTicketID(ticketID); err != nil {
		return err
	}

	// PagerDuty incident IDs are alphanumeric (e.g., P1234ABC or just alphanumeric)
	if !alphanumPattern.MatchString(ticketID) {
		return &TicketIDValidationError{
			TicketID: ticketID,
			Reason:   "invalid PagerDuty incident ID format (expected alphanumeric)",
		}
	}

	return nil
}

// ValidateProjectKey validates a project key (e.g., "owner/repo" for GitHub,
// "group/project" for GitLab) to prevent path traversal and injection attacks.
func ValidateProjectKey(projectKey string) error {
	if projectKey == "" {
		return fmt.Errorf("project key cannot be empty")
	}

	// Check for path traversal sequences
	if strings.Contains(projectKey, "..") {
		return fmt.Errorf("invalid project key %q: contains path traversal sequence", projectKey)
	}

	// Check for backslash path separators
	if strings.Contains(projectKey, "\\") {
		return fmt.Errorf("invalid project key %q: contains backslash", projectKey)
	}

	// Check for query string / injection characters
	for _, ch := range []string{"?", "&", "#", "^", " ", "\t", "\n"} {
		if strings.Contains(projectKey, ch) {
			return fmt.Errorf("invalid project key %q: contains disallowed character", projectKey)
		}
	}

	// Each segment between slashes must be non-empty and contain only safe characters
	segments := strings.Split(projectKey, "/")
	for _, seg := range segments {
		if seg == "" {
			return fmt.Errorf("invalid project key %q: contains empty path segment", projectKey)
		}
		if !safeSegmentPattern.MatchString(seg) {
			return fmt.Errorf("invalid project key %q: segment %q contains disallowed characters", projectKey, seg)
		}
	}

	return nil
}

// ValidateZenDutyIncidentID validates a ZenDuty incident ID
func ValidateZenDutyIncidentID(ticketID string) error {
	if err := ValidateTicketID(ticketID); err != nil {
		return err
	}

	// ZenDuty incident IDs can be numeric or UUID-like
	if !zdIncidentPattern.MatchString(ticketID) {
		return &TicketIDValidationError{
			TicketID: ticketID,
			Reason:   "invalid ZenDuty incident ID format",
		}
	}

	return nil
}
