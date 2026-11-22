package cloud

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync/atomic"
	"testing"
	"time"

	"nudgebee/services/config"
	"nudgebee/services/security"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestIsClientTimeout(t *testing.T) {
	clientTimeout := &url.Error{Op: "Post", URL: "http://collector/v1/cloud/execute_cli",
		Err: fmt.Errorf("context deadline exceeded (Client.Timeout exceeded while awaiting headers): %w", context.DeadlineExceeded)}
	refused := &url.Error{Op: "Post", URL: "http://collector/v1/cloud/execute_cli",
		Err: &net.OpError{Op: "dial", Net: "tcp", Err: errors.New("connect: connection refused")}}

	assert.True(t, isClientTimeout(clientTimeout))
	assert.True(t, isClientTimeout(context.DeadlineExceeded))
	assert.False(t, isClientTimeout(refused), "a quick connection failure is still retried")
	assert.False(t, isClientTimeout(errors.New("boom")))
}

func useCollector(t *testing.T, serverURL string, timeout time.Duration) *security.RequestContext {
	t.Helper()
	previousURL, previousTimeout := config.Config.CloudCollectorServerUrl, executeCliTimeout
	config.Config.CloudCollectorServerUrl = serverURL
	executeCliTimeout = timeout
	t.Cleanup(func() {
		config.Config.CloudCollectorServerUrl = previousURL
		executeCliTimeout = previousTimeout
	})
	return security.NewRequestContextForSuperAdmin(slog.Default(), nil, nil)
}

// A command that used its whole time limit is not run again: before, the client
// gave up after 60s and retried three more times, four minutes for the same result.
func TestExecuteCliDoesNotRetryAClientTimeout(t *testing.T) {
	var calls atomic.Int32
	release := make(chan struct{})
	collector := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		select {
		case <-release:
		case <-r.Context().Done():
		}
	}))
	defer collector.Close()
	defer close(release)
	ctx := useCollector(t, collector.URL, 100*time.Millisecond)

	start := time.Now()
	_, err := ExecuteCli(ctx, CloudExecuteCliCommandRequest{AccountID: "acc-1", Command: "aws sns list-subscriptions --region us-east-1"})

	require.Error(t, err)
	assert.True(t, isClientTimeout(err))
	assert.Equal(t, int32(1), calls.Load(), "the timed-out command must run once, not four times")
	assert.Less(t, time.Since(start), 2*time.Second, "no backoff waits after a timeout")
}

// A quick connection failure is still retried, as before.
func TestExecuteCliStillRetriesAConnectionFailure(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	address := listener.Addr().String()
	require.NoError(t, listener.Close()) // nothing listens there now
	ctx := useCollector(t, "http://"+address, time.Second)

	_, err = ExecuteCliWithRetry(ctx, CloudExecuteCliCommandRequest{AccountID: "acc-1", Command: "aws sts get-caller-identity"}, 1)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "after 2 attempts")
}
