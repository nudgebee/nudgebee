package cloud

import "testing"

// A proxy or ingress failure returns HTML, not JSON. The caller needs the
// status, not a syntax error from trying to parse the error page.
func TestCredentialsErrorFallsBackToStatus(t *testing.T) {
	got := credentialsError(map[string]any{}, 502)
	if got != "cloud collector returned status 502" {
		t.Fatalf("got %q, want the status", got)
	}
}

// When the collector does send a reason, that reason is what the step shows —
// "assume role ... AccessDenied" is actionable, a status code is not.
func TestCredentialsErrorPrefersTheProviderMessage(t *testing.T) {
	body := map[string]any{"errors": []any{map[string]any{"message": "aws: assume role arn:...:role/x: AccessDenied"}}}

	got := credentialsError(body, 500)
	if got != "aws: assume role arn:...:role/x: AccessDenied" {
		t.Fatalf("got %q, want the provider message", got)
	}
}

func TestCredentialsExpiringReportsWhetherTheyRevokeThemselves(t *testing.T) {
	if (AccountCredentials{}).Expiring() {
		t.Fatal("credentials without an expiry reported as expiring")
	}
}
