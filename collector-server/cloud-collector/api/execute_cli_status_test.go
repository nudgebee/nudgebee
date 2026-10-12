package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"testing"
	"time"

	"nudgebee/collector/cloud/common"

	"github.com/stretchr/testify/assert"
)

func TestExecuteCliErrorStatus(t *testing.T) {
	live := context.Background()
	hungUp, cancel := context.WithCancel(context.Background())
	cancel()

	exitFailure := fmt.Errorf("gcloud CLI command failed: %w, Stderr: API [compute.googleapis.com] not enabled on project", &common.CommandExitError{ExitCode: 1})
	timeout := fmt.Errorf("AWS CLI command failed: %w", &common.CommandTimeoutError{Timeout: time.Minute})
	collectorFault := errors.New("provider not found")
	// What SecureExecute returns when the request's 130s deadline stops a command
	// before its own 180s timeout.
	requestDeadline := fmt.Errorf("AWS CLI command failed: command cancelled: %w", context.DeadlineExceeded)
	pipedFailure := fmt.Errorf("gcloud CLI command failed: pipeline stage %q failed: %w", "grep bar", &common.CommandExitError{ExitCode: 1})

	tests := []struct {
		name string
		ctx  context.Context
		err  error
		want int
	}{
		{"a command that ran and failed is not a server error", live, exitFailure, http.StatusUnprocessableEntity},
		{"a command that ran out of time", live, timeout, http.StatusGatewayTimeout},
		{"a fault in the collector is still a server error", live, collectorFault, http.StatusInternalServerError},
		{"a caller that hung up is not a collector error", hungUp, collectorFault, 499},
		{"a command outcome wins over a hang-up", hungUp, exitFailure, http.StatusUnprocessableEntity},
		{"a command stopped by the request deadline ran out of time", live, requestDeadline, http.StatusGatewayTimeout},
		{"a piped command that exited non-zero", live, pipedFailure, http.StatusUnprocessableEntity},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, executeCliErrorStatus(tt.ctx, tt.err))
		})
	}
}

// The reviewer's reproductions, end to end through the real executor.
func TestExecuteCliErrorStatusWithRealCommands(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	_, _, err := common.SecureExecute(ctx, common.SecureCommandOptions{Command: "sleep 5"})
	assert.Equal(t, http.StatusGatewayTimeout, executeCliErrorStatus(context.Background(), err))
}
