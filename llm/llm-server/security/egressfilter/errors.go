package egressfilter

import (
	"errors"
	"fmt"
	"sort"
	"strings"
)

// ErrSecretsBlocked is the sentinel that callers can match with errors.Is when
// they need to special-case a egressfilter block.
var ErrSecretsBlocked = errors.New("egressfilter: outbound LLM call blocked: potential secret detected")

// BlockedMessageMarker is a stable substring of the user-facing Error().
// Exposed so callers that only see the persisted response text (no error
// value available — e.g. agent step classifiers that scan response strings)
// can still identify an egressfilter block without false positives. Keep
// this string in sync with Error.Error() below.
const BlockedMessageMarker = "Request blocked: potential credentials detected"

// Error is the typed error returned in ModeEnforce when one or more rules fire.
// Its Error() string is intentionally written to be safe to surface to the end
// user verbatim: it never echoes the matched value or any other request data.
//
// The audit ID is request-scoped and lets operators correlate the user-visible
// message with the structured log entry that contains the rule IDs.
type Error struct {
	AuditID string
	// RuleIDs is the deduplicated list of rule IDs that matched. Safe to log;
	// not included in Error() to avoid hinting at detector internals.
	RuleIDs []string
}

// Error implements the error interface and produces a user-safe message.
func (e *Error) Error() string {
	return fmt.Sprintf(
		"Request blocked: potential credentials detected. Your input or a tool result appeared to contain a secret (API key, token, or password) and was not sent to the LLM provider. Please remove the credential from the source (runbook, ticket, or query) and retry. Audit ID: %s",
		e.AuditID,
	)
}

// Unwrap lets errors.Is(err, ErrSecretsBlocked) succeed for wrapped egressfilter
// errors.
func (e *Error) Unwrap() error { return ErrSecretsBlocked }

// LogString returns a structured-log-safe representation including rule IDs.
// Use this in slog calls; never use Error() for logs — Error() is the
// user-facing surface.
func (e *Error) LogString() string {
	ids := append([]string(nil), e.RuleIDs...)
	sort.Strings(ids)
	return fmt.Sprintf("egressfilter blocked outbound call: audit_id=%s rules=[%s]", e.AuditID, strings.Join(ids, ","))
}

// AsError reports whether err is or wraps a *Error and returns it.
func AsError(err error) (*Error, bool) {
	var se *Error
	if errors.As(err, &se) {
		return se, true
	}
	return nil, false
}
