package tasks

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"time"

	"nudgebee/runbook/internal/logsafe"

	"go.temporal.io/sdk/activity"
	temporalLog "go.temporal.io/sdk/log"
	"go.temporal.io/sdk/temporal"
)

// SecretLoader returns the literal values of an account's automation secrets
// (logsafe.SecretValues), so a step's logs and errors can be masked.
type SecretLoader func(ctx context.Context, tenantID, accountID string) ([]string, error)

// secretCacheTTL lets a loop over many items load the account's secrets once.
// A secret added or changed within this window is masked once it expires.
const secretCacheTTL = 30 * time.Second

type cachedSecrets struct {
	values  []string
	expires time.Time
}

// NewCachedSecretLoader wraps load in a short-lived per-account cache.
func NewCachedSecretLoader(load SecretLoader) SecretLoader {
	var mu sync.Mutex
	items := map[string]cachedSecrets{}
	return func(ctx context.Context, tenantID, accountID string) ([]string, error) {
		key := tenantID + ":" + accountID
		now := time.Now()
		mu.Lock()
		if it, ok := items[key]; ok && now.Before(it.expires) {
			mu.Unlock()
			return it.values, nil
		}
		mu.Unlock()

		values, err := load(ctx, tenantID, accountID)
		if err != nil {
			return nil, err
		}
		mu.Lock()
		for k, it := range items { // keep the map bounded by live entries
			if now.After(it.expires) {
				delete(items, k)
			}
		}
		items[key] = cachedSecrets{values: values, expires: now.Add(secretCacheTTL)}
		mu.Unlock()
		return values, nil
	}
}

// secretLoadTimeout bounds the config read a step's first log line or error
// waits on.
const secretLoadTimeout = 5 * time.Second

// stepSecrets loads a step's secret values on first use, once. A failed load
// masks nothing rather than failing the step: masking is a second line of
// defence, and a config read must not decide whether an automation runs. The
// load outlives the step's context, so a step stopped mid-call still has the
// error it returns masked.
type stepSecrets struct {
	once   sync.Once
	load   func() ([]string, error)
	logger temporalLog.Logger // unmasked, so the warning cannot recurse
	values []string
}

func newStepSecrets(ctx context.Context, loader SecretLoader, tenantID, accountID string, logger temporalLog.Logger) *stepSecrets {
	s := &stepSecrets{logger: logger}
	if loader != nil {
		s.load = func() ([]string, error) {
			loadCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), secretLoadTimeout)
			defer cancel()
			return loader(loadCtx, tenantID, accountID)
		}
	}
	return s
}

func (s *stepSecrets) get() []string {
	s.once.Do(func() {
		if s.load == nil {
			return
		}
		values, err := s.load()
		if err != nil {
			s.logger.Warn("could not load secrets, so this step's logs and errors are not masked", "error", err)
			return
		}
		s.values = values
	})
	return s.values
}

// scrubbingLogger replaces secret values in a step's log lines.
type scrubbingLogger struct {
	inner   temporalLog.Logger
	secrets *stepSecrets
}

func newScrubbingLogger(inner temporalLog.Logger, secrets *stepSecrets) temporalLog.Logger {
	return &scrubbingLogger{inner: temporalLog.Skip(inner, 1), secrets: secrets}
}

func (l *scrubbingLogger) Debug(msg string, keyvals ...any) {
	msg, keyvals = l.scrub(msg, keyvals)
	l.inner.Debug(msg, keyvals...)
}

func (l *scrubbingLogger) Info(msg string, keyvals ...any) {
	msg, keyvals = l.scrub(msg, keyvals)
	l.inner.Info(msg, keyvals...)
}

func (l *scrubbingLogger) Warn(msg string, keyvals ...any) {
	msg, keyvals = l.scrub(msg, keyvals)
	l.inner.Warn(msg, keyvals...)
}

func (l *scrubbingLogger) Error(msg string, keyvals ...any) {
	msg, keyvals = l.scrub(msg, keyvals)
	l.inner.Error(msg, keyvals...)
}

func (l *scrubbingLogger) With(keyvals ...any) temporalLog.Logger {
	_, keyvals = l.scrub("", keyvals)
	return &scrubbingLogger{inner: temporalLog.With(l.inner, keyvals...), secrets: l.secrets}
}

func (l *scrubbingLogger) WithCallerSkip(depth int) temporalLog.Logger {
	return &scrubbingLogger{inner: temporalLog.Skip(l.inner, depth), secrets: l.secrets}
}

func (l *scrubbingLogger) scrub(msg string, keyvals []any) (string, []any) {
	values := l.secrets.get()
	if len(values) == 0 {
		return msg, keyvals
	}
	out := make([]any, len(keyvals))
	for i, kv := range keyvals {
		out[i] = scrubLogValue(kv, values)
	}
	return logsafe.ScrubSecretValues(msg, values), out
}

// scrubLogValue masks one log value, inside maps and lists too. Any other value
// is replaced by its masked text only when that text held a secret, so values
// with nothing to hide keep their type in the log line.
func scrubLogValue(v any, values []string) any {
	switch t := v.(type) {
	case nil:
		return nil
	case string:
		return logsafe.ScrubSecretValues(t, values)
	case map[string]any:
		out := make(map[string]any, len(t))
		for k, val := range t {
			out[logsafe.ScrubSecretValues(k, values)] = scrubLogValue(val, values)
		}
		return out
	case []any:
		out := make([]any, len(t))
		for i, val := range t {
			out[i] = scrubLogValue(val, values)
		}
		return out
	case error:
		return scrubbedText(t.Error(), v, values)
	default:
		return scrubbedText(fmt.Sprintf("%v", t), v, values)
	}
}

func scrubbedText(text string, original any, values []string) any {
	if clean := logsafe.ScrubSecretValues(text, values); clean != text {
		return clean
	}
	return original
}

// scrubError masks secret values in the error a step returns, before Temporal
// logs it, stores it in the run's history and hands it to the workflow. An error
// with nothing to hide is returned as is.
//
// A rebuilt error keeps what decides how the step ends: the application error
// type and retryability (a retry policy's non_retryable_error_types names the
// type), and a context.Canceled or DeadlineExceeded cause, which is how a step
// stopped mid-call is reported as canceled rather than failed. No other cause
// is kept: Temporal sends the whole chain, and its text is what held the secret.
func scrubError(err error, secrets *stepSecrets) error {
	// A bare context error holds no secret, and a stopped step should not wait
	// on a secret load to report it. Only an exact match: a wrapped one can.
	if err == nil || err == context.Canceled || err == context.DeadlineExceeded || errors.Is(err, activity.ErrResultPending) {
		return err
	}
	var canceled *temporal.CanceledError
	var timeout *temporal.TimeoutError
	if errors.As(err, &canceled) || errors.As(err, &timeout) {
		return err
	}
	values := secrets.get()
	if len(values) == 0 {
		return err
	}
	text := errorText(err)
	clean := logsafe.ScrubSecretValues(text, values)
	if clean == text {
		return err
	}

	var cause error
	switch {
	case errors.Is(err, context.Canceled):
		cause = context.Canceled
	case errors.Is(err, context.DeadlineExceeded):
		cause = context.DeadlineExceeded
	}
	if cause != nil {
		clean = strings.TrimSuffix(clean, ": "+cause.Error())
	}

	// Only a top-level application error sets the step's type and retryability;
	// Temporal reports one wrapped in another error by the wrapper's type.
	if appErr, ok := err.(*temporal.ApplicationError); ok {
		return temporal.NewApplicationErrorWithOptions(clean, appErr.Type(), temporal.ApplicationErrorOptions{
			NonRetryable:   appErr.NonRetryable(),
			NextRetryDelay: appErr.NextRetryDelay(),
			Category:       appErr.Category(),
			Cause:          cause,
		})
	}
	return temporal.NewApplicationErrorWithOptions(clean, sdkErrorType(err), temporal.ApplicationErrorOptions{Cause: cause})
}

// errorText is the text Temporal reports for err, without the type and
// retryability an application error appends, which the rebuilt error adds back.
func errorText(err error) string {
	if appErr, ok := err.(*temporal.ApplicationError); ok {
		if c := appErr.Unwrap(); c != nil {
			return appErr.Message() + ": " + c.Error()
		}
		return appErr.Message()
	}
	return err.Error()
}

// sdkErrorType is the type name Temporal gives a plain Go error (its struct
// name; empty for errors.New), so a retry policy naming it still matches.
func sdkErrorType(err error) string {
	t := reflect.TypeOf(err)
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	if name := t.Name(); name != "errorString" {
		return name
	}
	return ""
}
