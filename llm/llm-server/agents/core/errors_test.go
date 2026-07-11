package core

import (
	"errors"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"

	"nudgebee/llm/security/egressfilter"
)

func TestWrapperErrors(t *testing.T) {
	e := ErrLlmUnableToGenerate(errors.New("hello"))
	assert.NotNil(t, e)
	assert.ErrorIs(t, e, errLlmUnableToGenerate)
}

// TestErrLlmUnableToGenerate_PassesThroughEgressFilterBlocks locks the
// contract that egressfilter block errors are NOT wrapped with the
// "error: agent unable to process request" prefix. The egressfilter
// already produces a user-safe message with audit_id; wrapping would
// surface a confusing double-prefix to end users / UI consumers.
func TestErrLlmUnableToGenerate_PassesThroughEgressFilterBlocks(t *testing.T) {
	ef := &egressfilter.Error{
		AuditID: "egress-deadbeef1234",
		RuleIDs: []string{"aws-access-key-id"},
	}
	// Same shape the wrapper produces.
	wrapped := errors.Join(egressfilter.ErrSecretsBlocked, ef)

	got := ErrLlmUnableToGenerate(wrapped)

	assert.Same(t, wrapped, got,
		"egressfilter blocks must pass through unchanged — no double prefix")
	assert.False(t, errors.Is(got, errLlmUnableToGenerate),
		"the returned error must NOT carry the agent-unable-to-process sentinel")
	assert.True(t, errors.Is(got, egressfilter.ErrSecretsBlocked),
		"the returned error must still be detectable as a egressfilter block")
	assert.False(t,
		strings.Contains(got.Error(), "error: agent unable to process request"),
		"the double-prefix string must not appear in the user-facing error")
}

// TestIsRetryableError_EgressFilterIsTerminal locks the explicit early-out
// in isRetryableError: an egressfilter block is by definition non-retryable.
// Same payload → same block; retrying would just burn provider quota on a
// call that never reaches the provider anyway.
func TestIsRetryableError_EgressFilterIsTerminal(t *testing.T) {
	ef := &egressfilter.Error{AuditID: "egress-deadbeef1234"}
	wrapped := errors.Join(egressfilter.ErrSecretsBlocked, ef)
	assert.False(t, isRetryableError(wrapped),
		"egressfilter blocks must be classified non-retryable")

	// Even when buried under the agent-unable-to-process join (paranoia
	// check: future caller stacks may wrap differently).
	doubled := errors.Join(errLlmUnableToGenerate, wrapped)
	assert.False(t, isRetryableError(doubled),
		"deeply wrapped egressfilter blocks must still classify as non-retryable")
}
