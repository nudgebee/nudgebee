package common

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestIsCommandOutcome(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{"non-zero exit", &CommandExitError{ExitCode: 1}, true},
		{"timed out", &CommandTimeoutError{Timeout: time.Minute}, true},
		// The providers wrap the error with the command's stderr.
		{"wrapped non-zero exit", fmt.Errorf("gcloud CLI command failed: %w, Stderr: API not enabled", &CommandExitError{ExitCode: 1}), true},
		{"wrapped timeout", fmt.Errorf("AWS CLI command failed: %w", &CommandTimeoutError{Timeout: time.Minute}), true},
		{"command could not be started", errors.New(`exec: "gcloud": executable file not found`), false},
		{"no error", nil, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, IsCommandOutcome(tt.err))
		})
	}
}

// The /execute_cli path calls SecureExecute without output limits. It used to return
// plain-text errors there, so a command that ran and failed could not be told from
// a failure to run it, and the route answered 500 either way.
func TestSecureExecuteLegacyPathReturnsTypedOutcomes(t *testing.T) {
	_, _, err := SecureExecute(context.Background(), SecureCommandOptions{Command: "ls /non-existent-directory-12345"})
	var exitErr *CommandExitError
	require.ErrorAs(t, err, &exitErr)
	assert.NotZero(t, exitErr.ExitCode)
	assert.True(t, IsCommandOutcome(err))
	assert.Contains(t, err.Error(), "command exited with status", "the message is unchanged")

	_, _, err = SecureExecute(context.Background(), SecureCommandOptions{Command: "sleep 5", Timeout: 200 * time.Millisecond})
	var timeoutErr *CommandTimeoutError
	require.ErrorAs(t, err, &timeoutErr)
	assert.True(t, IsCommandOutcome(err))
	assert.Contains(t, err.Error(), "command timed out after", "the message is unchanged")
}

// The request's deadline can stop a command before its own timeout does; that is
// still the command running out of time, not a failure to run it.
func TestIsCommandOutcomeCountsARequestDeadline(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	_, _, err := SecureExecute(ctx, SecureCommandOptions{Command: "sleep 5"})
	require.Error(t, err)
	assert.ErrorIs(t, err, context.DeadlineExceeded)
	assert.True(t, IsCommandOutcome(err))
}
