package core

import (
	"errors"
	"fmt"
	"net/http"

	"nudgebee/llm/common"
	"nudgebee/llm/security/egressfilter"
)

// User-facing LLM failure errors. Their messages are safe to surface directly
// to the end user (no provider internals, model names, or request IDs — see the
// "Fail Securely" product guideline) and they carry standard HTTP status codes
// so upstream layers can branch on the cause. When one of these is returned
// from a planner it propagates through the normal error path: the executor
// records it as a failed turn and surfaces err.Error() (the Message) as the
// assistant's reply (see executor_planner.go and api/chains.go).
var (
	// ErrLLMRateLimited maps to provider 429 / quota-exhaustion responses.
	ErrLLMRateLimited = common.Error{
		Code:    http.StatusTooManyRequests,
		Message: "The AI service is currently at capacity (rate limit or quota exceeded). Please wait a moment and try again.",
	}
	// ErrLLMRequestTooLarge maps to context-window / token-limit overflow on the
	// initial request (before any tool ran).
	ErrLLMRequestTooLarge = common.Error{
		Code:    http.StatusRequestEntityTooLarge,
		Message: "Your request is too large for the AI model to process. Please shorten your question or reduce the amount of selected context, then try again.",
	}
	// ErrLLMServiceUnavailable maps to transient network / timeout / 5xx failures
	// that survived the retry and fallback budget.
	ErrLLMServiceUnavailable = common.Error{
		Code:    http.StatusServiceUnavailable,
		Message: "The AI service is temporarily unavailable due to a network or timeout issue. Please try again in a few moments.",
	}
)

var errLlmUnableToGenerate = errors.New("error: agent unable to process request")

var errAgentNotFound = errors.New("error: agent not found")

var errPlannerUnableToGeneratePlan = errors.New("error: planner unable to create plan")

var errToolNotFound = errors.New("error: tool not found")

var ErrConversationInProgress = errors.New("conversation: conversation is already in progress")

// ErrConversationPendingFollowup is returned when a new (non-followup) generation
// arrives on a conversation whose latest turn is still WAITING / WAITING_FOR_CLIENT_TOOL
// on a followup answer. Without this gate, the new turn would orphan the prior
// turn at WAITING permanently.
var ErrConversationPendingFollowup = errors.New("conversation: previous turn is waiting on a followup answer; answer or cancel it before starting a new turn")

// ErrCleanupRefusedActiveFollowup is returned by CleanupConversationMessage when
// a non-terminal followup message still references an agent row in the deletion
// set. The right caller behavior is to flip the human message to WAITING and
// stop re-execution — proceeding would create duplicate agent/tool rows
// alongside the preserved-but-in-flight ones.
var ErrCleanupRefusedActiveFollowup = errors.New("conversation: cleanup refused; active followup references agents from this message")

func ErrLlmUnableToGenerate(err error) error {
	if err == nil {
		return errLlmUnableToGenerate
	}
	if errors.Is(err, errLlmUnableToGenerate) {
		return err
	}
	// egressfilter blocks are already user-safe (audit_id + remediation hint,
	// no value echo, no rule names). Passing through unchanged avoids the
	// "error: agent unable to process request\n<egressfilter message>"
	// double-prefix the end user otherwise sees in the response. Callers
	// still detect via errors.Is(err, egressfilter.ErrSecretsBlocked).
	if errors.Is(err, egressfilter.ErrSecretsBlocked) {
		return err
	}
	return errors.Join(errLlmUnableToGenerate, err)
}

func ErrPlannerUnableToGeneratePlan(err error) error {
	if err == nil {
		return errPlannerUnableToGeneratePlan
	}
	if errors.Is(err, errPlannerUnableToGeneratePlan) {
		return err
	}
	return errors.Join(errPlannerUnableToGeneratePlan, err)
}

func ErrToolNotFound(toolName string) error {
	if toolName == "" {
		return errToolNotFound
	}
	return fmt.Errorf("%w: %s", errToolNotFound, toolName)
}
