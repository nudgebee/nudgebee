package aws

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"nudgebee/collector/cloud/providers"

	"github.com/aws/smithy-go"
)

func s3AccessDenied() error {
	// The shape AWS returns for a refused CUR object read: an explicit
	// s3:GetObject Deny on the report bucket.
	return fmt.Errorf("operation error S3: GetObject, https response error StatusCode: 403: %w",
		&smithy.GenericAPIError{
			Code:    "AccessDenied",
			Message: `User: arn:aws:sts::123456789012:assumed-role/Role/session is not authorized to perform: s3:GetObject on resource: "arn:aws:s3:::cur-bucket/manifest.json" with an explicit deny in an identity-based policy`,
		})
}

func TestClassifyCurReadError(t *testing.T) {
	t.Run("a refused read becomes not-configured", func(t *testing.T) {
		// Onboarding accepts an account with no CUR S3 access, so the runtime must
		// not answer 500 and must not mark the whole agent disconnected.
		got := classifyCurReadError(s3AccessDenied())
		if !errors.Is(got, providers.ErrCostNotConfigured) {
			t.Fatalf("expected ErrCostNotConfigured, got %v", got)
		}
	})

	t.Run("the reason survives for the Spends error column", func(t *testing.T) {
		got := classifyCurReadError(s3AccessDenied())
		if msg := got.Error(); !strings.Contains(msg, "s3:GetObject") || !strings.Contains(msg, "explicit deny") {
			t.Fatalf("original denial was lost: %q", msg)
		}
	})

	t.Run("nil stays nil", func(t *testing.T) {
		if got := classifyCurReadError(nil); got != nil {
			t.Fatalf("got %v, want nil", got)
		}
	})

	t.Run("an unrelated failure stays a real error", func(t *testing.T) {
		// A parse failure or a 5xx is a genuine fault and must keep failing the
		// sync, or we would hide real breakage behind an empty cost page.
		boom := errors.New("failed to parse cost report: unexpected EOF")
		got := classifyCurReadError(boom)
		if errors.Is(got, providers.ErrCostNotConfigured) {
			t.Fatalf("unrelated error was wrongly classified as not-configured: %v", got)
		}
		if !errors.Is(got, boom) {
			t.Fatalf("original error not preserved: %v", got)
		}
	})

	t.Run("already not-configured is left alone", func(t *testing.T) {
		got := classifyCurReadError(providers.ErrCostNotConfigured)
		if !errors.Is(got, providers.ErrCostNotConfigured) {
			t.Fatalf("got %v", got)
		}
	})

	t.Run("broken credentials are not mistaken for a missing CUR", func(t *testing.T) {
		// isCurAuthorizationDenied deliberately narrows to the access-denied codes;
		// an expired token is a credential fault and must still surface.
		expired := fmt.Errorf("sts: %w", &smithy.GenericAPIError{Code: "ExpiredTokenException", Message: "token expired"})
		if errors.Is(classifyCurReadError(expired), providers.ErrCostNotConfigured) {
			t.Fatal("expired credentials were classified as not-configured")
		}
	})
}
