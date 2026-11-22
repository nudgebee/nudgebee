package account

import (
	"errors"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
)

func TestEventsRetryDelayDoublesUpToAnHour(t *testing.T) {
	want := []time.Duration{10 * time.Minute, 20 * time.Minute, 40 * time.Minute, time.Hour, time.Hour, time.Hour}
	for i, w := range want {
		if got := eventsRetryDelay(i + 1); got != w {
			t.Errorf("failure %d: got %s, want %s", i+1, got, w)
		}
	}
	if got := eventsRetryDelay(1000); got != time.Hour {
		t.Errorf("failure 1000: got %s, want 1h", got)
	}
}

func TestEventsStatusOnFailure(t *testing.T) {
	now := time.Date(2026, 10, 13, 9, 0, 0, 0, time.UTC)
	expiredSecret := fmt.Errorf("failed to get alerts: failed to get next page: %w",
		&azidentity.AuthenticationFailedError{RawResponse: &http.Response{StatusCode: http.StatusUnauthorized}})
	throttled := fmt.Errorf("failed to get alerts: %w", &azcore.ResponseError{StatusCode: http.StatusTooManyRequests})

	t.Run("first rejection waits ten minutes", func(t *testing.T) {
		status, rejected := eventsStatusOnFailure(expiredSecret, 0, now)
		if rejected == nil {
			t.Fatal("expected a credentialsRejectedError")
		}
		if status["auth_failures"] != 1 || status["retry_after"] != "2026-10-13T09:10:00Z" {
			t.Errorf("got %v", status)
		}
		if !errors.Is(rejected, expiredSecret) {
			t.Error("rejected error does not unwrap to the cloud's error")
		}
	})

	t.Run("fourth rejection in a row waits the one-hour cap", func(t *testing.T) {
		status, rejected := eventsStatusOnFailure(expiredSecret, 3, now)
		if rejected == nil || rejected.failures != 4 {
			t.Fatalf("got %+v", rejected)
		}
		if status["retry_after"] != "2026-10-13T10:00:00Z" {
			t.Errorf("got %v", status)
		}
	})

	t.Run("throttling neither counts nor resets", func(t *testing.T) {
		status, rejected := eventsStatusOnFailure(throttled, 3, now)
		if rejected != nil {
			t.Fatal("throttling must not be treated as rejected credentials")
		}
		if status["auth_failures"] != 3 {
			t.Errorf("count should stay 3, got %v", status)
		}
		if _, ok := status["retry_after"]; ok {
			t.Errorf("throttling must not set a wait, got %v", status)
		}
	})

	t.Run("a healthy account's glitch writes only the error", func(t *testing.T) {
		status, _ := eventsStatusOnFailure(throttled, 0, now)
		if len(status) != 1 || status["err"] == nil {
			t.Errorf("got %v", status)
		}
	})
}
