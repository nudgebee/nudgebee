package runtime

import (
	"errors"
	"fmt"
	"net/http"
	"sync"
	"testing"

	"nudgebee/llm/common"
	"nudgebee/llm/llms/huggingface/huggingfaceclient"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func provErr(status int, body string, headers map[string]string) error {
	h := http.Header{}
	for k, v := range headers {
		h.Set(k, v)
	}
	return common.NewProviderHTTPError(status, h, body)
}

// End-to-end over the real provider 429 bodies we know of: the error the client
// builds must classify to the right user-facing message, with the right reason.
func TestRateLimit429sFromRealProviders(t *testing.T) {
	cases := []struct {
		name    string
		body    string
		headers map[string]string
		scope   common.RateLimitScope
		window  common.RateLimitWindow
		message string
	}{
		{
			name:    "custom gateway RPM (the 2026-09-21 incident)",
			body:    "Rate limit is exceeded. This deployment has reached its configured requests-per-minute (RPM) limit. Please retry shortly.",
			scope:   common.RateLimitScopeRequests,
			window:  common.RateLimitWindowMinute,
			message: "The AI service has reached its per-minute request limit. Please wait a moment and try again.",
		},
		{
			name:    "azure openai S0 token rate limit",
			body:    "Requests to the ChatCompletions_Create Operation under Azure OpenAI API version 2024-02-01 have exceeded token rate limit of your current OpenAI S0 pricing tier.",
			scope:   common.RateLimitScopeTokens,
			window:  common.RateLimitWindowUnknown,
			message: "The AI service has reached its token limit. Please wait a moment and try again.",
		},
		{
			name:    "openai requests per day",
			body:    "Rate limit reached for gpt-4 in organization org-abc123 on requests per day (RPD): Limit 200, Used 200, Requested 1.",
			scope:   common.RateLimitScopeRequests,
			window:  common.RateLimitWindowDay,
			message: "The AI service has reached its daily request limit, which will not clear on its own for some time. Contact your administrator if you need more capacity.",
		},
		{
			name:    "openai tokens per minute",
			body:    "Rate limit reached for gpt-4 on tokens per min (TPM): Limit 10000, Used 9999, Requested 500.",
			scope:   common.RateLimitScopeTokens,
			window:  common.RateLimitWindowMinute,
			message: "The AI service has reached its per-minute token limit. Please wait a moment and try again.",
		},
		{
			name:    "bedrock throttling",
			body:    "ThrottlingException: Too many requests, please wait before trying again.",
			scope:   common.RateLimitScopeUnknown,
			window:  common.RateLimitWindowUnknown,
			message: ErrLLMRateLimited.Message,
		},
		{
			name:    "vertex resource exhausted with per-minute quota",
			body:    "RESOURCE_EXHAUSTED: Quota exceeded for aiplatform.googleapis.com/generate_requests per minute per project.",
			scope:   common.RateLimitScopeRequests,
			window:  common.RateLimitWindowMinute,
			message: "The AI service has reached its per-minute request limit. Please wait a moment and try again.",
		},
		{
			name:    "anthropic overloaded",
			body:    "Number of requests has exceeded your rate limit. Please slow down.",
			scope:   common.RateLimitScopeRequests,
			window:  common.RateLimitWindowUnknown,
			message: "The AI service has reached its request limit. Please wait a moment and try again.",
		},
		{
			name:    "headers decide when the body is silent (requests)",
			body:    "Rate limit reached.",
			headers: map[string]string{"x-ratelimit-remaining-requests": "0", "x-ratelimit-remaining-tokens": "5000", "retry-after": "12"},
			scope:   common.RateLimitScopeRequests,
			window:  common.RateLimitWindowUnknown,
			message: "The AI service has reached its request limit. Please wait a moment and try again.",
		},
		{
			name:    "headers decide when the body is silent (tokens)",
			body:    "Rate limit reached.",
			headers: map[string]string{"x-ratelimit-remaining-requests": "7", "x-ratelimit-remaining-tokens": "0"},
			scope:   common.RateLimitScopeTokens,
			window:  common.RateLimitWindowUnknown,
			message: "The AI service has reached its token limit. Please wait a moment and try again.",
		},
		{
			name:    "nothing usable falls back to the generic sentinel",
			body:    "429 Too Many Requests",
			scope:   common.RateLimitScopeUnknown,
			window:  common.RateLimitWindowUnknown,
			message: ErrLLMRateLimited.Message,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := provErr(429, tc.body, tc.headers)

			// The quota detector must still fire — it gates the whole path.
			require.True(t, isQuotaError(err), "isQuotaError must recognise this 429")

			detail := common.RateLimitDetailFromError(err)
			assert.Equal(t, tc.scope, detail.Scope, "scope")
			assert.Equal(t, tc.window, detail.Window, "window")

			classified := ClassifyUserFacingLLMError(err)
			require.NotNil(t, classified)
			assert.Equal(t, tc.message, classified.Error())
			assert.True(t, errors.Is(classified, ErrLLMRateLimited), "must keep the rate-limited identity")
		})
	}
}

// classifyUserFacingLLMError runs again on the error it produced (planner_react_3
// re-classifies lastErr). Re-deriving the class from our own message text sent a
// token rate limit back as "your request is too large" and dropped the request
// variants entirely, so idempotence is a hard requirement, not a nicety.
func TestClassifyUserFacingLLMErrorIsIdempotent(t *testing.T) {
	inputs := []error{
		ErrLLMRateLimited,
		ErrLLMServiceUnavailable,
		ErrLLMRequestTooLarge,
		RateLimitedError(common.RateLimitDetail{Scope: common.RateLimitScopeTokens, Window: common.RateLimitWindowMinute}),
		RateLimitedError(common.RateLimitDetail{Scope: common.RateLimitScopeRequests, Window: common.RateLimitWindowDay}),
		RateLimitedError(common.RateLimitDetail{Scope: common.RateLimitScopeRequests, Window: common.RateLimitWindowHour}),
		RateLimitedError(common.RateLimitDetail{Scope: common.RateLimitScopeTokens, Window: common.RateLimitWindowUnknown}),
	}

	for _, in := range inputs {
		t.Run(in.Error()[:40], func(t *testing.T) {
			once := ClassifyUserFacingLLMError(in)
			require.NotNil(t, once, "a typed error must never classify to nil")
			assert.Equal(t, in.Error(), once.Error())

			twice := ClassifyUserFacingLLMError(once)
			require.NotNil(t, twice)
			assert.Equal(t, once.Error(), twice.Error(), "second pass must not change the message")
		})
	}
}

// A token RATE limit must never be mistaken for a context-window overflow: the
// remedies are opposites (wait vs shorten the prompt).
func TestTokenRateLimitIsNotAContextWindowOverflow(t *testing.T) {
	rateLimited := RateLimitedError(common.RateLimitDetail{
		Scope: common.RateLimitScopeTokens, Window: common.RateLimitWindowMinute,
	})

	classified := ClassifyUserFacingLLMError(rateLimited)
	assert.NotEqual(t, ErrLLMRequestTooLarge.Message, classified.Error())
	assert.NotContains(t, classified.Error(), "too large")
	assert.NotContains(t, classified.Error(), "shorten")
	assert.True(t, errors.Is(classified, ErrLLMRateLimited))

	// A genuine context-window overflow still classifies as too-large.
	overflow := provErr(400, "This model's maximum context length is 32768 tokens, however you requested 41000 tokens.", nil)
	assert.Equal(t, ErrLLMRequestTooLarge.Message, ClassifyUserFacingLLMError(overflow).Error())
}

// The error travels up wrapped (ErrLlmUnableToGenerate joins, planners re-wrap).
// Every signal has to survive the unwrap chain.
func TestRateLimitSignalsSurviveWrapping(t *testing.T) {
	base := provErr(429, "requests-per-minute (RPM) limit reached", map[string]string{"retry-after": "9"})

	wrapped := []error{
		fmt.Errorf("react4: llm generate: %w", base),
		ErrLlmUnableToGenerate(base),
		fmt.Errorf("outer: %w", fmt.Errorf("inner: %w", base)),
		errors.Join(errors.New("unrelated"), base),
	}

	for i, err := range wrapped {
		t.Run(fmt.Sprintf("layer_%d", i), func(t *testing.T) {
			detail := common.RateLimitDetailFromError(err)
			assert.Equal(t, common.RateLimitScopeRequests, detail.Scope)
			assert.Equal(t, common.RateLimitWindowMinute, detail.Window)

			headers, ok := common.RateLimitHeadersFromError(err)
			require.True(t, ok)
			assert.Equal(t, "9", headers["retry-after"])

			assert.True(t, isQuotaError(err))
			assert.Equal(t,
				"The AI service has reached its per-minute request limit. Please wait a moment and try again.",
				ClassifyUserFacingLLMError(err).Error())
		})
	}
}

// Non-429 responses must not acquire a rate-limit reason.
func TestNonRateLimitErrorsAreUntouched(t *testing.T) {
	cases := []struct {
		name string
		err  error
	}{
		{"500 from provider", provErr(500, "internal server error", nil)},
		{"503 from provider", provErr(503, "service unavailable", nil)},
		{"401 bad key", provErr(401, "Incorrect API key provided: sk-nb-1234", nil)},
		{"plain go error", errors.New("connection refused")},
		{"nil", nil},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			detail := common.RateLimitDetailFromError(tc.err)
			assert.False(t, detail.Known(), "must not invent a rate-limit reason")
			assert.Nil(t, common.RateLimitLogAttrs(tc.err))

			if tc.err != nil {
				classified := ClassifyUserFacingLLMError(tc.err)
				if classified != nil {
					assert.False(t, errors.Is(classified, ErrLLMRateLimited),
						"a non-429 must never classify as rate limited")
				}
			}
		})
	}
}

// The whole point of the change: the circuit-open fail-fast path, which is where
// most user-visible failures in a throttling incident land.
func TestCircuitOpenFailFastReportsTheRealCause(t *testing.T) {
	provider, model := "custom", "gpt-5.6-sol"

	t.Run("quota trip keeps the limit detail for the whole cooldown", func(t *testing.T) {
		ResetCircuitBreakers()
		RecordModelRateLimitHit(provider, model,
			provErr(429, "This deployment has reached its configured requests-per-minute (RPM) limit.", nil))

		// Every caller during the cooldown, not just the one that got the 429.
		for i := 0; i < 5; i++ {
			status := modelCircuitStatus(provider, model)
			require.True(t, status.Open, "call %d", i)
			err := circuitOpenError(status.Cause, status.Detail)
			assert.Equal(t,
				"The AI service has reached its per-minute request limit. Please wait a moment and try again.",
				err.Error(), "call %d", i)
			assert.True(t, errors.Is(err, ErrLLMRateLimited))
		}
	})

	t.Run("unavailable trip stays unavailable", func(t *testing.T) {
		ResetCircuitBreakers()
		RecordModelFailure(provider, model)

		status := modelCircuitStatus(provider, model)
		err := circuitOpenError(status.Cause, status.Detail)
		assert.Equal(t, ErrLLMServiceUnavailable, err)
		assert.NotContains(t, err.Error(), "rate limit")
	})

	t.Run("quota trip with an opaque 429 degrades to the generic sentinel", func(t *testing.T) {
		ResetCircuitBreakers()
		RecordModelRateLimitHit(provider, model, provErr(429, "429 Too Many Requests", nil))

		status := modelCircuitStatus(provider, model)
		assert.False(t, status.Detail.Known())
		assert.Equal(t, ErrLLMRateLimited, circuitOpenError(status.Cause, status.Detail))
	})

	t.Run("nil error on trip is safe", func(t *testing.T) {
		ResetCircuitBreakers()
		RecordModelRateLimitHit(provider, model, nil)

		status := modelCircuitStatus(provider, model)
		assert.True(t, status.Open)
		assert.Equal(t, ErrLLMRateLimited, circuitOpenError(status.Cause, status.Detail))
	})
}

// Messages reach the end user, so provider internals must never appear in them.
func TestUserFacingMessagesNeverEchoProviderInternals(t *testing.T) {
	secrets := []string{
		"gpt-5.6-sol-prod-eastus2",
		"org-abc123",
		"sk-nb-16abcdef",
		"sub-9f3c-4b21",
	}
	body := fmt.Sprintf(
		"deployment %s in organization %s (key %s, subscription %s) exceeded its requests-per-minute (RPM) limit",
		secrets[0], secrets[1], secrets[2], secrets[3])

	classified := ClassifyUserFacingLLMError(provErr(429, body, nil))
	require.NotNil(t, classified)
	for _, secret := range secrets {
		assert.NotContains(t, classified.Error(), secret)
	}
	assert.Equal(t,
		"The AI service has reached its per-minute request limit. Please wait a moment and try again.",
		classified.Error())
}

// Every scope/window combination, including the ones no provider has produced
// yet. The unknown-scope rows are the point: a known window with an unnamed
// dimension must not silently become "request".
func TestRateLimitedErrorMessageMatrix(t *testing.T) {
	cases := []struct {
		scope   common.RateLimitScope
		window  common.RateLimitWindow
		message string
	}{
		{common.RateLimitScopeRequests, common.RateLimitWindowMinute, "The AI service has reached its per-minute request limit. Please wait a moment and try again."},
		{common.RateLimitScopeRequests, common.RateLimitWindowHour, "The AI service has reached its hourly request limit. Please try again later, or contact your administrator if you need more capacity."},
		{common.RateLimitScopeRequests, common.RateLimitWindowDay, "The AI service has reached its daily request limit, which will not clear on its own for some time. Contact your administrator if you need more capacity."},
		{common.RateLimitScopeRequests, common.RateLimitWindowUnknown, "The AI service has reached its request limit. Please wait a moment and try again."},
		{common.RateLimitScopeTokens, common.RateLimitWindowMinute, "The AI service has reached its per-minute token limit. Please wait a moment and try again."},
		{common.RateLimitScopeTokens, common.RateLimitWindowHour, "The AI service has reached its hourly token limit. Please try again later, or contact your administrator if you need more capacity."},
		{common.RateLimitScopeTokens, common.RateLimitWindowDay, "The AI service has reached its daily token limit, which will not clear on its own for some time. Contact your administrator if you need more capacity."},
		{common.RateLimitScopeTokens, common.RateLimitWindowUnknown, "The AI service has reached its token limit. Please wait a moment and try again."},
		{common.RateLimitScopeUnknown, common.RateLimitWindowMinute, "The AI service has reached its per-minute limit. Please wait a moment and try again."},
		{common.RateLimitScopeUnknown, common.RateLimitWindowHour, "The AI service has reached its hourly limit. Please try again later, or contact your administrator if you need more capacity."},
		{common.RateLimitScopeUnknown, common.RateLimitWindowDay, "The AI service has reached its daily limit, which will not clear on its own for some time. Contact your administrator if you need more capacity."},
		{common.RateLimitScopeUnknown, common.RateLimitWindowUnknown, ErrLLMRateLimited.Message},
	}

	for _, tc := range cases {
		t.Run(fmt.Sprintf("%s_%s", tc.scope, tc.window), func(t *testing.T) {
			err := RateLimitedError(common.RateLimitDetail{Scope: tc.scope, Window: tc.window})
			assert.Equal(t, tc.message, err.Error())
			assert.True(t, errors.Is(err, ErrLLMRateLimited))

			// No double spaces or stray articles from the optional subject.
			assert.NotContains(t, err.Error(), "  ")
			assert.NotContains(t, err.Error(), " limit limit")

			// Only the day and hour windows may send the user away; a per-minute
			// limit clears on its own and must keep the "wait" advice.
			if tc.window == common.RateLimitWindowMinute || tc.window == common.RateLimitWindowUnknown {
				assert.Contains(t, err.Error(), "wait a moment")
			} else {
				assert.NotContains(t, err.Error(), "wait a moment")
				assert.Contains(t, err.Error(), "administrator")
			}
		})
	}
}

// The breaker's stored reason across its whole lifecycle.
func TestCircuitBreakerTripDetailLifecycle(t *testing.T) {
	provider, model := "custom", "gpt-5.6-sol"
	rpm := provErr(429, "requests-per-minute (RPM) limit", nil)
	rpd := provErr(429, "requests-per-day (RPD) limit", nil)

	t.Run("latest trip wins, including when it knows less", func(t *testing.T) {
		ResetCircuitBreakers()
		RecordModelRateLimitHit(provider, model, rpm)
		assert.Equal(t, common.RateLimitWindowMinute, modelCircuitStatus(provider, model).Detail.Window)

		// Deliberate: the breaker reports the most recent trip, never a stale
		// reason from an earlier one.
		RecordModelRateLimitHit(provider, model, nil)
		assert.False(t, modelCircuitStatus(provider, model).Detail.Known())
	})

	t.Run("a later outage relabels a quota trip", func(t *testing.T) {
		ResetCircuitBreakers()
		RecordModelRateLimitHit(provider, model, rpd)
		RecordModelFailure(provider, model)

		status := modelCircuitStatus(provider, model)
		assert.Equal(t, fallbackCauseUnavailable, status.Cause)
		assert.False(t, status.Detail.Known())
		assert.Equal(t, ErrLLMServiceUnavailable, circuitOpenError(status.Cause, status.Detail))
	})

	t.Run("success clears the reason with the circuit", func(t *testing.T) {
		ResetCircuitBreakers()
		RecordModelRateLimitHit(provider, model, rpm)
		RecordModelSuccess(provider, model)

		status := modelCircuitStatus(provider, model)
		assert.False(t, status.Open)
		assert.False(t, status.Detail.Known())
	})

	t.Run("reason is per provider+model, not global", func(t *testing.T) {
		ResetCircuitBreakers()
		RecordModelRateLimitHit("custom", "model-a", rpd)
		RecordModelRateLimitHit("custom", "model-b", rpm)

		assert.Equal(t, common.RateLimitWindowDay, modelCircuitStatus("custom", "model-a").Detail.Window)
		assert.Equal(t, common.RateLimitWindowMinute, modelCircuitStatus("custom", "model-b").Detail.Window)
		assert.False(t, modelCircuitStatus("custom", "model-c").Open)
	})
}

// Concurrent trips carrying different reasons must not tear the stored detail:
// a reader has to see one trip's scope paired with that same trip's window.
func TestCircuitBreakerTripDetailNoTornReads(t *testing.T) {
	ResetCircuitBreakers()
	provider, model := "custom", "gpt-5.6-sol"

	errs := []error{
		provErr(429, "requests-per-minute (RPM) limit", nil),
		provErr(429, "tokens-per-day (TPD) limit", nil),
	}
	valid := map[common.RateLimitDetail]bool{
		{Scope: common.RateLimitScopeRequests, Window: common.RateLimitWindowMinute}: true,
		{Scope: common.RateLimitScopeTokens, Window: common.RateLimitWindowDay}:      true,
	}

	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(2)
		go func(i int) { defer wg.Done(); RecordModelRateLimitHit(provider, model, errs[i%2]) }(i)
		go func() {
			defer wg.Done()
			if d := modelCircuitStatus(provider, model).Detail; d.Known() {
				assert.True(t, valid[d], "torn read: %+v", d)
			}
		}()
	}
	wg.Wait()
}

// The specialised error travels up inside ErrLlmUnableToGenerate (errors.Join),
// whose Error() prefixes "error: agent unable to process request". The user must
// get the clean message, not the double-prefixed join.
func TestSpecialisedErrorSurvivesUnableToGenerateWrapper(t *testing.T) {
	inner := RateLimitedError(common.RateLimitDetail{
		Scope: common.RateLimitScopeTokens, Window: common.RateLimitWindowDay,
	})
	wrapped := ErrLlmUnableToGenerate(inner)

	assert.Contains(t, wrapped.Error(), "error: agent unable to process request")

	classified := ClassifyUserFacingLLMError(wrapped)
	require.NotNil(t, classified)
	assert.Equal(t, inner.Error(), classified.Error())
	assert.NotContains(t, classified.Error(), "error: agent unable to process request")
	assert.True(t, errors.Is(classified, ErrLLMRateLimited))
}

// A non-429 provider failure must keep its own classification end to end.
func TestNon429ProviderFailuresKeepTheirClass(t *testing.T) {
	overflow := provErr(400, "This model's maximum context length is 32768 tokens, however you requested 41000 tokens.", nil)
	assert.Equal(t, ErrLLMRequestTooLarge.Message, ClassifyUserFacingLLMError(overflow).Error())
	assert.False(t, common.RateLimitDetailFromError(overflow).Known())

	unavailable := provErr(503, "upstream connect error or disconnect/reset before headers", nil)
	classified := ClassifyUserFacingLLMError(unavailable)
	if classified != nil {
		assert.False(t, errors.Is(classified, ErrLLMRateLimited))
	}
}

// Swapping the clients from fmt.Errorf to a typed error must not move a single
// classification: the retry, fallback and circuit-trip decisions are all driven
// by string matching on the message. This pins new-vs-legacy parity per status.
func TestProviderHTTPErrorClassifiesIdenticallyToLegacyString(t *testing.T) {
	cases := []struct {
		status int
		body   string
	}{
		{429, "Rate limit reached for gpt-4 on requests per day (RPD): Limit 200, Used 200."},
		{429, "Rate limit is exceeded. This deployment has reached its configured requests-per-minute (RPM) limit."},
		{429, "Requests to the ChatCompletions_Create Operation have exceeded token rate limit."},
		{400, "This model's maximum context length is 32768 tokens, however you requested 41000 tokens."},
		{401, "Incorrect API key provided: sk-nb-1234."},
		{408, "request timeout"},
		{500, "internal server error"},
		{502, "bad gateway"},
		{503, "service unavailable"},
		{504, "gateway timeout"},
	}

	for _, tc := range cases {
		t.Run(fmt.Sprintf("%d_%.24s", tc.status, tc.body), func(t *testing.T) {
			typed := provErr(tc.status, tc.body, nil)
			legacy := fmt.Errorf("API returned unexpected status code: %d: %s", tc.status, tc.body)

			assert.Equal(t, legacy.Error(), typed.Error(), "message must be byte-identical")
			assert.Equal(t, isQuotaError(legacy), isQuotaError(typed), "isQuotaError")
			assert.Equal(t, isTransientError(legacy), isTransientError(typed), "isTransientError")
			assert.Equal(t, isTokenLimitError(legacy), isTokenLimitError(typed), "isTokenLimitError")
			assert.Equal(t, isRetryableError(legacy), isRetryableError(typed), "isRetryableError")
			assert.Equal(t, isCircuitTrippingError(legacy), isCircuitTrippingError(typed), "isCircuitTrippingError")
		})
	}
}

// WithRateLimitHeaders wraps errors other code already inspects by type. The
// huggingface retry loop does errors.As(err, &*StatusCodeError) and refuses to
// retry a 4xx; if the wrapper hid that, every terminal 4xx would burn the full
// retry budget before failing.
func TestWrapperStaysTransparentToExistingErrorInspection(t *testing.T) {
	t.Run("errors.As still reaches StatusCodeError", func(t *testing.T) {
		raw := &huggingfaceclient.StatusCodeError{StatusCode: 429, Body: "rpm limit"}
		wrapped := common.WithRateLimitHeaders(raw, 429, http.Header{"Retry-After": []string{"5"}})

		var statusErr *huggingfaceclient.StatusCodeError
		require.True(t, errors.As(wrapped, &statusErr))
		assert.Equal(t, 429, statusErr.StatusCode)
		assert.Equal(t, raw.Error(), wrapped.Error())
	})

	t.Run("a terminal 4xx stays non-retryable", func(t *testing.T) {
		raw := &huggingfaceclient.StatusCodeError{StatusCode: 400, Body: "bad request"}
		wrapped := common.WithRateLimitHeaders(raw, 400, http.Header{})

		var statusErr *huggingfaceclient.StatusCodeError
		require.True(t, errors.As(wrapped, &statusErr))
		assert.False(t, statusErr.Retryable(), "4xx must remain terminal through the wrapper")
	})

	t.Run("sentinel identity survives the extra unwrap hop", func(t *testing.T) {
		raw := &huggingfaceclient.StatusCodeError{StatusCode: 503, Body: "unavailable"}
		wrapped := common.WithRateLimitHeaders(raw, 503, http.Header{})
		assert.ErrorIs(t, wrapped, huggingfaceclient.ErrUnexpectedStatusCode)
	})
}

// Providers whose clients carry headers via WithRateLimitHeaders rather than
// ProviderHTTPError (anthropic, vertex, huggingface) must produce the same
// user-facing messages as the ones that do. Their error wording differs, so this
// drives each through its own construction path.
func TestRateLimit429sFromWrapperPathProviders(t *testing.T) {
	anthropic := func(status int, body string, h http.Header) error {
		return common.WithRateLimitHeaders(
			fmt.Errorf("anthropic: API error (%d rate_limit_error): %s", status, body), status, h)
	}
	vertex := func(status int, body string, h http.Header) error {
		return common.WithRateLimitHeaders(
			fmt.Errorf("endpoint returned status %d: %s", status, body), status, h)
	}
	hf := func(status int, body string, h http.Header) error {
		return common.WithRateLimitHeaders(
			&huggingfaceclient.StatusCodeError{StatusCode: status, Body: body}, status, h)
	}

	hdrs := func(kv map[string]string) http.Header {
		h := http.Header{}
		for k, v := range kv {
			h.Set(k, v)
		}
		return h
	}

	cases := []struct {
		name    string
		err     error
		scope   common.RateLimitScope
		window  common.RateLimitWindow
		message string
	}{
		{
			name: "anthropic per-minute request limit",
			err: anthropic(429, "Number of requests has exceeded your per-minute rate limit.",
				hdrs(map[string]string{"Retry-After": "21", "Anthropic-RateLimit-Requests-Remaining": "0"})),
			scope: common.RateLimitScopeRequests, window: common.RateLimitWindowMinute,
			message: "The AI service has reached its per-minute request limit. Please wait a moment and try again.",
		},
		{
			name: "anthropic daily token allowance",
			err: anthropic(429, "Your organization has exceeded its daily token allowance.",
				hdrs(map[string]string{"Anthropic-RateLimit-Tokens-Remaining": "0"})),
			scope: common.RateLimitScopeTokens, window: common.RateLimitWindowDay,
			message: "The AI service has reached its daily token limit, which will not clear on its own for some time. Contact your administrator if you need more capacity.",
		},
		{
			name: "vertex RESOURCE_EXHAUSTED per minute",
			err: vertex(429, "RESOURCE_EXHAUSTED: Quota exceeded for aiplatform.googleapis.com/generate_requests per minute per project.",
				nil),
			scope: common.RateLimitScopeRequests, window: common.RateLimitWindowMinute,
			message: "The AI service has reached its per-minute request limit. Please wait a moment and try again.",
		},
		{
			name:  "huggingface hourly request limit",
			err:   hf(429, "Rate limit reached. You reached your hourly request limit.", nil),
			scope: common.RateLimitScopeRequests, window: common.RateLimitWindowHour,
			message: "The AI service has reached its hourly request limit. Please try again later, or contact your administrator if you need more capacity.",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			require.True(t, isQuotaError(tc.err), "isQuotaError must recognise this 429")

			detail := common.RateLimitDetailFromError(tc.err)
			assert.Equal(t, tc.scope, detail.Scope)
			assert.Equal(t, tc.window, detail.Window)

			classified := ClassifyUserFacingLLMError(tc.err)
			require.NotNil(t, classified)
			assert.Equal(t, tc.message, classified.Error())
			assert.True(t, errors.Is(classified, ErrLLMRateLimited))

			// And the same message for every fail-fast during the cooldown.
			ResetCircuitBreakers()
			RecordModelRateLimitHit("custom", "gpt-5.6-sol", tc.err)
			status := modelCircuitStatus("custom", "gpt-5.6-sol")
			assert.Equal(t, tc.message, circuitOpenError(status.Cause, status.Detail).Error(),
				"cooldown message must match the fresh 429")
		})
	}
}

// A provider that names no dimension must not have one invented for it. Bedrock's
// throttle message says only "Too many requests" — the HTTP reason phrase, which
// Azure returns for token limits too — so the generic sentinel is the honest answer.
func TestProvidersThatNameNoDimensionGetTheGenericMessage(t *testing.T) {
	cases := []struct{ name, body string }{
		{"bedrock throttling", "ThrottlingException: Too many requests, please wait before trying again."},
		{"bare proxy 429", ""},
		{"status line only", "429 Too Many Requests"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := provErr(429, tc.body, nil)
			assert.False(t, common.RateLimitDetailFromError(err).Known())

			classified := ClassifyUserFacingLLMError(err)
			require.NotNil(t, classified)
			assert.Equal(t, ErrLLMRateLimited.Message, classified.Error())
		})
	}
}

// The cooldown message must equal the fresh-429 message for every provider shape:
// most failures in a throttling incident are fail-fasts, and a reason that only
// appears on the one call that reached the provider is a reason nobody sees.
func TestCooldownMessageMatchesFresh429ForEveryShape(t *testing.T) {
	bodies := []string{
		"Rate limit is exceeded. This deployment has reached its configured requests-per-minute (RPM) limit.",
		"Rate limit reached for gpt-4 on requests per day (RPD): Limit 200, Used 200.",
		"Requests to the ChatCompletions_Create Operation have exceeded token rate limit.",
		"429 Too Many Requests",
	}

	for _, body := range bodies {
		t.Run(fmt.Sprintf("%.36s", body), func(t *testing.T) {
			err := provErr(429, body, nil)
			fresh := ClassifyUserFacingLLMError(err)
			require.NotNil(t, fresh)

			ResetCircuitBreakers()
			RecordModelRateLimitHit("custom", "gpt-5.6-sol", err)
			for i := 0; i < 3; i++ {
				status := modelCircuitStatus("custom", "gpt-5.6-sol")
				require.True(t, status.Open)
				assert.Equal(t, fresh.Error(), circuitOpenError(status.Cause, status.Detail).Error(),
					"fail-fast %d", i)
			}
		})
	}
}
