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
	// ErrLLMOutOfCredit maps to the provider account running out of credit; unlike a
	// rate limit it won't clear by waiting.
	ErrLLMOutOfCredit = common.Error{
		Code:    http.StatusPaymentRequired,
		Message: "The AI provider account used for this request has run out of credit. Ask an account admin to add credit with the provider, or choose a different default under Integrations → LLM Providers.",
	}
)

// RateLimitedLLMError is ErrLLMRateLimited carrying a message specialised to the
// limit the provider named. It keeps the sentinel's identity under errors.Is for
// two reasons: callers branching on the class still match, and
// classifyUserFacingLLMError can recognise an error it already produced instead
// of re-deriving the class from the message text.
//
// That second reason is not theoretical. isTokenLimitError matches the substring
// "token limit", so a token *rate* limit worded "per-minute token limit" is read
// back as a context-window overflow and the user is told to shorten a request
// that was exactly the right size.
type RateLimitedLLMError struct {
	// Err holds the user-safe message and HTTP code, the same shape the ErrLLM*
	// sentinels use. It is a named field rather than embedded because a type
	// literally named Error would shadow the Error() method.
	Err common.Error
}

func (e RateLimitedLLMError) Error() string { return e.Err.Error() }

// Unwrap exposes the inner common.Error so errors.As reaches the HTTP code, the
// way it does for the plain sentinels. Without it the specialised variants would
// be invisible to any caller branching on the status the sentinels document.
func (e RateLimitedLLMError) Unwrap() error { return e.Err }

func (e RateLimitedLLMError) Is(target error) bool {
	return target == error(ErrLLMRateLimited)
}

// RateLimitedError is ErrLLMRateLimited specialised by which limit the provider
// said was exhausted. The remedy differs by window and the generic message gives
// the wrong one half the time: "wait a moment and try again" is right for a
// per-minute limit and actively misleading for a per-day limit, which will not
// clear today however long the user waits.
//
// The wording is drawn from a closed vocabulary rather than echoed from the
// provider — a provider's own 429 text carries deployment and account names, and
// these messages go straight to the end user (see the "Fail Securely" guideline
// on the sentinels above). When the provider said nothing usable, this returns
// the unchanged generic sentinel rather than guessing.
func RateLimitedError(detail common.RateLimitDetail) error {
	if !detail.Known() {
		return ErrLLMRateLimited
	}

	// Trailing space, not a bare noun: an unknown dimension drops out of the
	// sentence entirely ("its per-minute limit") rather than defaulting to
	// "request", which would assert a dimension the provider never named. The
	// window alone is still worth saying — it is the half that changes the remedy.
	subject := ""
	switch detail.Scope {
	case common.RateLimitScopeRequests:
		subject = "request "
	case common.RateLimitScopeTokens:
		subject = "token "
	}

	message := func(format string) error {
		return RateLimitedLLMError{Err: common.Error{
			Code:    http.StatusTooManyRequests,
			Message: fmt.Sprintf(format, subject),
		}}
	}

	switch detail.Window {
	case common.RateLimitWindowDay:
		return message("The AI service has reached its daily %slimit, which will not clear on its own for some time. Contact your administrator if you need more capacity.")
	case common.RateLimitWindowHour:
		return message("The AI service has reached its hourly %slimit. Please try again later, or contact your administrator if you need more capacity.")
	case common.RateLimitWindowMinute:
		return message("The AI service has reached its per-minute %slimit. Please wait a moment and try again.")
	default:
		return message("The AI service has reached its %slimit. Please wait a moment and try again.")
	}
}

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

// ErrAmbiguousLLMConfig is returned when an LLM integration-config result set
// spans more than one integration. Collapsing those rows into one map yields a
// credential nobody configured (one integration's api key against another's
// endpoint), so the reader refuses instead. Callers must scope the query to a
// single integration before it gets here.
var ErrAmbiguousLLMConfig = errors.New("llm config: result set spans multiple integrations; refusing to merge")

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
