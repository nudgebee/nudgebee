package bootstrap

import (
	"io"
	"log/slog"
	"testing"

	"nudgebee/services/config"
	"nudgebee/services/user"
)

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func setAdminEmail(t *testing.T, email string) func() {
	t.Helper()
	prev := config.Config.AdminEmail
	config.Config.AdminEmail = email
	return func() { config.Config.AdminEmail = prev }
}

func TestAdminEmailPrefersExplicitConfig(t *testing.T) {
	restore := setAdminEmail(t, "  configured@example.com  ")
	defer restore()

	// The licence's address is the fallback, so an explicitly configured one
	// must win — and surrounding whitespace must not defeat the comparison.
	if got, want := adminEmail(), "configured@example.com"; got != want {
		t.Errorf("adminEmail() = %q, want %q", got, want)
	}
}

// An unconfigured install must not provision. This is what keeps the change
// additive: every deployment that does not opt in behaves exactly as before.
func TestProvisionIsInertWithoutAnAdminAddress(t *testing.T) {
	restore := setAdminEmail(t, "")
	defer restore()

	// The default (OSS) licence carries no address either, so this resolves to
	// "no admin configured" and must return before touching the database.
	if adminEmail() != "" {
		t.Skip("a licence address is present in this environment; the inert path cannot be exercised")
	}
	if err := Provision(t.Context(), discardLogger()); err != nil {
		t.Errorf("Provision() with no admin address = %v, want nil", err)
	}
}

// The tenant name install-time provisioning passes must match what the
// first-login path generates, or the same admin address would produce
// differently-named tenants depending on which path ran.
func TestGeneratedTenantNameMatchesTheLoginPath(t *testing.T) {
	const email = "admin@acme.com"

	got := user.GeneratedOrgName(displayName, email)

	// displayName is the same literal the first-login path passes to
	// OnboardUser, so both routes land on the same name.
	if want := "admin's Org"; got != want {
		t.Errorf("GeneratedOrgName(%q, %q) = %q, want %q", displayName, email, got, want)
	}
}

func TestGeneratedTenantNameFallsBackToTheAddress(t *testing.T) {
	// With no display name the rule uses the local part of the address, which
	// is the case that matters if the display-name default ever changes.
	if got, want := user.GeneratedOrgName("", "priya@acme.com"), "priya's Org"; got != want {
		t.Errorf("GeneratedOrgName = %q, want %q", got, want)
	}
}

func TestFirstNonEmpty(t *testing.T) {
	if got := firstNonEmpty("", "second", "third"); got != "second" {
		t.Errorf("firstNonEmpty = %q, want %q", got, "second")
	}
	if got := firstNonEmpty("", ""); got != "" {
		t.Errorf("firstNonEmpty of all-empty = %q, want empty", got)
	}
}
