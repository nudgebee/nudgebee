// Package signozclient authenticates against a user-configured SigNoz and sends
// authenticated requests to it.
//
// SigNoz changed its login API underneath a stable query API. Releases up to
// ~v0.95 only have POST /api/v1/login (returns accessJwt); ~v0.100 has both;
// from ~v0.105 v1 login is gone and the route falls through to the SPA, which
// answers 200 text/html. Current releases and SigNoz Cloud log in with
// GET /api/v2/sessions/context + POST /api/v2/sessions/email_password. The
// /api/v3 and /api/v4 query endpoints are unchanged across all of them, so only
// login needs to know which generation it is talking to.
//
// It is a leaf package on purpose, like promclient: integrations (validation)
// and observability (queries) both use it, and a future alertrule caller cannot
// import integrations. The caller injects the *http.Client so its tracing
// transport is kept.
package signozclient

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	neturl "net/url"
	"strings"
	"sync"
	"time"
)

// Login flows. FlavorV2 is the sessions API (current self-hosted and Cloud),
// FlavorV1 the legacy /api/v1/login.
const (
	FlavorV2 = "v2"
	FlavorV1 = "v1"

	// AuthPassword logs in with an email/password and sends the resulting
	// token as a Bearer; AuthAPIKey sends a SigNoz API key (a PAT on legacy
	// releases, a service-account key on current ones and Cloud) on every
	// request and never logs in. An empty AuthType means AuthPassword.
	AuthPassword = "password"
	AuthAPIKey   = "api_key"

	apiKeyHeader = "SIGNOZ-API-KEY"

	// ModeCloud skips detection: SigNoz Cloud is always the v2 sessions API.
	// Any other mode (self_hosted, empty) detects the flow.
	ModeCloud = "cloud"

	loginTimeout = 15 * time.Second
	// queryTimeout is the 30s default common.HttpGet/HttpPost applied to
	// these requests before they moved here.
	queryTimeout = 30 * time.Second
)

var (
	// ErrInvalidCredentials means SigNoz rejected the email/password.
	ErrInvalidCredentials = errors.New("invalid SigNoz credentials")
	// ErrUnknownUser means a v2 SigNoz reports no user for the email.
	ErrUnknownUser = errors.New("SigNoz has no user with this email")
	// ErrPasswordAuthDisabled means the user's org only allows SSO login.
	ErrPasswordAuthDisabled = errors.New("SigNoz organization does not allow email/password login")
	// ErrNoSupportedAuth means the URL answers neither login API, so it is
	// not a SigNoz we can talk to (or not SigNoz at all).
	ErrNoSupportedAuth = errors.New("SigNoz exposes neither /api/v2/sessions nor /api/v1/login")
	// ErrInvalidAPIKey means SigNoz did not accept the API key (401).
	ErrInvalidAPIKey = errors.New("SigNoz rejected the API key")
	// ErrAPIKeyForbidden means the key is valid but its role may not read
	// logs (403) — a role problem, not a wrong key.
	ErrAPIKeyForbidden = errors.New("SigNoz API key is not allowed to read logs")
	// ErrNotSigNozAPI means the URL did not answer like SigNoz's query API —
	// a wrong URL, or a proxy in front of SigNoz answering with its own page.
	ErrNotSigNozAPI = errors.New("URL did not answer like the SigNoz query API (wrong URL, or a proxy/login page in front of SigNoz)")
	// errAPIKeyLogin is a programming error: API keys never log in.
	errAPIKeyLogin = errors.New("signozclient: API-key credentials do not log in")
)

// RequestRejectedError is a 400 from SigNoz: the request contract drifted, not
// the credentials. It carries SigNoz's own message so the user sees it.
type RequestRejectedError struct {
	Path    string
	Message string
}

func (e *RequestRejectedError) Error() string {
	return fmt.Sprintf("SigNoz rejected %s: %s", e.Path, e.Message)
}

// Credentials identify how to authenticate to one SigNoz. Only the fields of
// AuthType are read: a row switched from one auth type to the other keeps the
// old type's stored values, and they must never be used.
type Credentials struct {
	BaseURL  string
	AuthType string
	Email    string
	Password string
	Mode     string
	APIKey   string

	// CacheScope separates the token cache of SigNozs that share a URL, such as a
	// private address behind two customers' foragers. Empty when dialled directly.
	CacheScope string
}

func (c Credentials) usesAPIKey() bool {
	return c.AuthType == AuthAPIKey
}

// Token is a SigNoz access token and the login flow that produced it.
type Token struct {
	Value  string
	Flavor string
	Expiry time.Time
}

// Login returns a cached token for creds, logging in when there is none or it
// is close to expiry.
func Login(ctx context.Context, hc *http.Client, creds Credentials) (Token, error) {
	if creds.usesAPIKey() {
		return Token{}, errAPIKeyLogin
	}
	key := cacheKey(creds)
	if tok, ok := cachedToken(key); ok {
		return tok, nil
	}

	// The login round trips run WITHOUT the lock: tokenMu is process-wide, so
	// holding it would serialise every tenant's login behind one slow SigNoz.
	// Concurrent misses may each log in; the double-check converges them.
	tok, err := LoginFresh(ctx, hc, creds)
	if err != nil {
		return Token{}, err
	}

	tokenMu.Lock()
	defer tokenMu.Unlock()
	if cached, ok := tokenCache[key]; ok && tokenUsable(cached) {
		return cached, nil
	}
	// Entries for superseded credentials are never asked for again; drop
	// whatever has expired so the cache stays bounded by live credentials.
	for k, t := range tokenCache {
		if !tokenUsable(t) {
			delete(tokenCache, k)
		}
	}
	tokenCache[key] = tok
	return tok, nil
}

// LoginFresh logs in without reading or writing the cache. Validation uses it
// so a Test click always proves the credentials against the server.
func LoginFresh(ctx context.Context, hc *http.Client, creds Credentials) (Token, error) {
	if creds.usesAPIKey() {
		return Token{}, errAPIKeyLogin
	}
	base := strings.TrimRight(creds.BaseURL, "/")

	isV2, ctxData, err := sessionContext(ctx, hc, base, creds.Email)
	if err != nil {
		return Token{}, err
	}

	var tok Token
	switch {
	case isV2:
		tok, err = loginV2(ctx, hc, base, creds, ctxData)
	case creds.Mode == ModeCloud:
		return Token{}, fmt.Errorf("%w at %s (SigNoz Cloud must answer /api/v2/sessions/context — check signoz_url)", ErrNoSupportedAuth, base)
	default:
		tok, err = loginV1(ctx, hc, base, creds)
	}
	if err != nil {
		return Token{}, err
	}

	slog.Info("signoz: logged in", "url", base, "auth", tok.Flavor)
	return tok, nil
}

// Do sends an authenticated request. With an API key the key goes on the
// request as-is: there is no login and nothing to refresh, so a 401 is
// returned to the caller. With a password, on a 401 it drops the cached token,
// logs in again and retries once: a server restart or session revocation must
// not leave a stale token failing until it expires.
func Do(ctx context.Context, hc *http.Client, creds Credentials, method, path string, body []byte) (*http.Response, error) {
	if creds.usesAPIKey() {
		return send(ctx, hc, creds.BaseURL, method, path, body, apiKeyHeader, creds.APIKey)
	}

	tok, err := Login(ctx, hc, creds)
	if err != nil {
		return nil, fmt.Errorf("signoz login: %w", err)
	}

	resp, err := send(ctx, hc, creds.BaseURL, method, path, body, "Authorization", "Bearer "+tok.Value)
	if err != nil || resp.StatusCode != http.StatusUnauthorized {
		return resp, err
	}

	_, _ = io.Copy(io.Discard, resp.Body)
	_ = resp.Body.Close()
	invalidate(creds, tok.Value)

	tok, err = Login(ctx, hc, creds)
	if err != nil {
		return nil, fmt.Errorf("signoz login: %w", err)
	}
	return send(ctx, hc, creds.BaseURL, method, path, body, "Authorization", "Bearer "+tok.Value)
}

// apiKeyProbePath is the label autocomplete the logs page loads first: the
// cheapest call that proves a key may read logs.
const apiKeyProbePath = "/api/v3/autocomplete/attribute_keys?searchText=&dataSource=logs&aggregateOperator=noop"

// CheckAPIKey proves an API key the way queries will use it — through Do, on
// a logs read — and tells a wrong key (401) apart from a key whose role may
// not read logs (403).
func CheckAPIKey(ctx context.Context, hc *http.Client, creds Credentials) error {
	base := strings.TrimRight(creds.BaseURL, "/")
	resp, err := Do(ctx, hc, creds, http.MethodGet, apiKeyProbePath, nil)
	if err != nil {
		return fmt.Errorf("failed to connect to SigNoz at %s: %w", base, err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return fmt.Errorf("failed to read SigNoz response from %s: %w", base, err)
	}

	switch resp.StatusCode {
	case http.StatusOK:
		var env envelope
		if json.Unmarshal(body, &env) != nil || env.Status != "success" {
			return fmt.Errorf("%w at %s", ErrNotSigNozAPI, base)
		}
		return nil
	case http.StatusUnauthorized:
		return ErrInvalidAPIKey
	case http.StatusForbidden:
		return ErrAPIKeyForbidden
	case http.StatusNotFound, http.StatusMethodNotAllowed:
		return fmt.Errorf("%w at %s", ErrNotSigNozAPI, base)
	default:
		return StatusError(apiKeyProbePath, resp.StatusCode, body)
	}
}

func send(ctx context.Context, hc *http.Client, baseURL, method, path string, body []byte, authHeader, authValue string) (*http.Response, error) {
	var reader io.Reader
	if body != nil {
		reader = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, strings.TrimRight(baseURL, "/")+path, reader)
	if err != nil {
		return nil, err
	}
	req.Header.Set(authHeader, authValue)
	req.Header.Set("Content-Type", "application/json")
	return withTimeout(hc).Do(req)
}

// withTimeout gives a client without its own timeout the query default. A
// client Timeout (unlike a context deadline) also bounds reading the body,
// which the caller does after Do returns.
func withTimeout(hc *http.Client) *http.Client {
	if hc.Timeout > 0 {
		return hc
	}
	c := *hc
	c.Timeout = queryTimeout
	return &c
}

// envelope is SigNoz's response wrapper. "error" is an object on current
// releases ({type,code,message}) and a plain string on some legacy routes, so
// it is kept raw and read by errorMessage. Legacy /api/v1/login reports
// failures as "errors":[{code,msg}] instead.
type envelope struct {
	Status string          `json:"status"`
	Data   json.RawMessage `json:"data"`
	Error  json.RawMessage `json:"error"`
	Errors []struct {
		Msg string `json:"msg"`
	} `json:"errors"`
}

type envelopeError struct {
	Type    string `json:"type"`
	Code    string `json:"code"`
	Message string `json:"message"`
}

type contextData struct {
	Exists bool `json:"exists"`
	Orgs   []struct {
		ID           string `json:"id"`
		AuthNSupport *struct {
			Password []json.RawMessage `json:"password"`
		} `json:"authNSupport"`
	} `json:"orgs"`
}

// sessionContext probes GET /api/v2/sessions/context. Only a missing route
// means a legacy SigNoz: v0.51 answers 404, and a SPA behind a proxy answers 200
// with HTML. A v2 server is recognised by the shape of its data (exists/orgs)
// rather than the envelope's status string. Anything else — a 5xx, a 429, a
// proxy's 403 — is returned as an error with its status: falling back to v1
// would misreport a server that is only unavailable as one we can't talk to.
func sessionContext(ctx context.Context, hc *http.Client, base, email string) (bool, contextData, error) {
	const path = "/api/v2/sessions/context"
	status, body, err := call(ctx, hc, http.MethodGet, base+path+"?email="+neturl.QueryEscape(email), nil)
	if err != nil {
		return false, contextData{}, fmt.Errorf("failed to connect to SigNoz at %s: %w", base, err)
	}

	switch {
	case status == http.StatusNotFound || status == http.StatusMethodNotAllowed:
		return false, contextData{}, nil
	case status != http.StatusOK:
		return false, contextData{}, StatusError(path, status, body)
	}

	var env envelope
	var fields map[string]json.RawMessage
	if json.Unmarshal(body, &env) != nil || json.Unmarshal(env.Data, &fields) != nil {
		return false, contextData{}, nil
	}
	if _, ok := fields["exists"]; !ok {
		if _, ok := fields["orgs"]; !ok {
			return false, contextData{}, nil
		}
	}
	var data contextData
	if err := json.Unmarshal(env.Data, &data); err != nil {
		return false, contextData{}, nil
	}
	return true, data, nil
}

func loginV2(ctx context.Context, hc *http.Client, base string, creds Credentials, data contextData) (Token, error) {
	if !data.Exists || len(data.Orgs) == 0 {
		return Token{}, fmt.Errorf("%w %s on %s", ErrUnknownUser, creds.Email, base)
	}
	org := data.Orgs[0]
	if org.AuthNSupport != nil && len(org.AuthNSupport.Password) == 0 {
		return Token{}, fmt.Errorf("%w for %s (SSO only)", ErrPasswordAuthDisabled, creds.Email)
	}

	payload, _ := json.Marshal(map[string]string{
		"email":    creds.Email,
		"password": creds.Password,
		"orgID":    org.ID,
	})
	const path = "/api/v2/sessions/email_password"
	status, body, err := call(ctx, hc, http.MethodPost, base+path, payload)
	if err != nil {
		return Token{}, fmt.Errorf("failed to call SigNoz login at %s: %w", base, err)
	}
	if status != http.StatusOK {
		return Token{}, classify(path, status, body)
	}

	var env envelope
	var parsed struct {
		AccessToken string `json:"accessToken"`
		ExpiresIn   int64  `json:"expiresIn"`
	}
	if json.Unmarshal(body, &env) != nil || json.Unmarshal(env.Data, &parsed) != nil || parsed.AccessToken == "" {
		return Token{}, fmt.Errorf("SigNoz login at %s returned 200 but no accessToken", base)
	}
	return Token{Value: parsed.AccessToken, Flavor: FlavorV2, Expiry: expiryIn(parsed.ExpiresIn)}, nil
}

func loginV1(ctx context.Context, hc *http.Client, base string, creds Credentials) (Token, error) {
	payload, _ := json.Marshal(map[string]string{
		"email":    creds.Email,
		"password": creds.Password,
	})
	const path = "/api/v1/login"
	status, body, err := call(ctx, hc, http.MethodPost, base+path, payload)
	if err != nil {
		return Token{}, fmt.Errorf("failed to call SigNoz login at %s: %w", base, err)
	}

	switch status {
	case http.StatusOK:
		// v0.105+ serves the SPA here with 200 text/html, so a 200 alone
		// proves nothing: only a JSON accessJwt is a v1 login.
		var parsed struct {
			AccessJwt       string `json:"accessJwt"`
			AccessJwtExpiry int64  `json:"accessJwtExpiry"`
		}
		if json.Unmarshal(body, &parsed) != nil || parsed.AccessJwt == "" {
			return Token{}, fmt.Errorf("%w at %s", ErrNoSupportedAuth, base)
		}
		return Token{Value: parsed.AccessJwt, Flavor: FlavorV1, Expiry: expiryAt(parsed.AccessJwtExpiry)}, nil
	case http.StatusNotFound, http.StatusMethodNotAllowed:
		return Token{}, fmt.Errorf("%w at %s", ErrNoSupportedAuth, base)
	default:
		return Token{}, classify(path, status, body)
	}
}

// classify maps a failed login response to a typed error. It reads SigNoz's
// error envelope rather than trusting the status alone: a 400 means the request
// shape drifted and must never be reported as a wrong password.
func classify(path string, status int, body []byte) error {
	msg, code := errorMessage(body)
	switch {
	case status == http.StatusUnauthorized || status == http.StatusForbidden || code == "incorrect_password":
		return ErrInvalidCredentials
	// A 400 is only reported as a rejected request when SigNoz said why; with
	// no message there is nothing to show, so it falls through to StatusError.
	case status == http.StatusBadRequest && msg != "":
		return &RequestRejectedError{Path: path, Message: msg}
	default:
		return StatusError(path, status, body)
	}
}

// errorMessage returns SigNoz's own error message and code from a response
// body. It reports an empty message for a body that is not a SigNoz error
// envelope, so callers never put a foreign page's contents in a user-facing
// error — use StatusError to describe those.
func errorMessage(body []byte) (msg, code string) {
	var env envelope
	if json.Unmarshal(body, &env) == nil && len(env.Error) > 0 {
		var obj envelopeError
		if json.Unmarshal(env.Error, &obj) == nil && (obj.Message != "" || obj.Code != "") {
			return obj.Message, obj.Code
		}
		var s string
		if json.Unmarshal(env.Error, &s) == nil && s != "" {
			return s, ""
		}
	}
	if len(env.Errors) > 0 && env.Errors[0].Msg != "" {
		return env.Errors[0].Msg, ""
	}
	return "", ""
}

// StatusError describes a failed SigNoz response for a user to read. SigNoz's
// own message is used when the body carries one; a body that is not a SigNoz
// error envelope did not come from SigNoz — it is a proxy's or tunnel's page —
// so it is logged rather than shown. Echoing it filled the integration form's
// error popup with raw page source (#41200).
func StatusError(path string, status int, body []byte) error {
	if msg, _ := errorMessage(body); msg != "" {
		return fmt.Errorf("SigNoz %s returned HTTP %d: %s", path, status, msg)
	}
	slog.Warn("signoz: error response carried no SigNoz message",
		"path", path, "status", status, "body", bodyForLog(body))
	switch status {
	case http.StatusBadGateway, http.StatusServiceUnavailable, http.StatusGatewayTimeout:
		return fmt.Errorf("SigNoz is not reachable (HTTP %d) — SigNoz, or a proxy or tunnel in front of it, is unavailable", status)
	default:
		return fmt.Errorf("SigNoz %s returned HTTP %d", path, status)
	}
}

// logBodyLimit bounds how much of an unreadable body reaches the log line.
const logBodyLimit = 512

func bodyForLog(body []byte) string {
	if len(body) > logBodyLimit {
		// Cut the bytes before converting: a query response can be megabytes.
		// ToValidUTF8 drops a rune the cut may have split.
		return strings.ToValidUTF8(strings.TrimSpace(string(body[:logBodyLimit])), "") + "…"
	}
	return strings.TrimSpace(string(body))
}

// call performs one login round trip with its own timeout and returns the
// status and the already-read body.
func call(ctx context.Context, hc *http.Client, method, url string, payload []byte) (int, []byte, error) {
	ctx, cancel := context.WithTimeout(ctx, loginTimeout)
	defer cancel()

	var reader io.Reader
	if payload != nil {
		reader = bytes.NewReader(payload)
	}
	req, err := http.NewRequestWithContext(ctx, method, url, reader)
	if err != nil {
		return 0, nil, err
	}
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := hc.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return 0, nil, err
	}
	return resp.StatusCode, body, nil
}

// unknownExpiryTTL bounds how long a token with no reported expiry is reused.
const unknownExpiryTTL = 5 * time.Minute

func expiryIn(seconds int64) time.Time {
	if seconds <= 0 {
		return time.Now().Add(unknownExpiryTTL)
	}
	return time.Now().Add(time.Duration(seconds) * time.Second)
}

func expiryAt(unixSeconds int64) time.Time {
	if unixSeconds <= 0 {
		return time.Now().Add(unknownExpiryTTL)
	}
	return time.Unix(unixSeconds, 0)
}

// tokenRefreshSkew is how far before real expiry a cached token stops being
// reused, so it cannot expire mid-flight on the request it was fetched for.
const tokenRefreshSkew = 60 * time.Second

var (
	tokenMu    sync.Mutex
	tokenCache = map[string]Token{}
)

// cacheKey covers every input that determines the token. The password is
// hashed so the raw secret is not held as a map key, and a rotated password
// cannot be served a token minted from the old one. APIKey is deliberately not
// part of it: the API-key path sends the key itself and never reads or writes
// this cache — keep it that way, or keys would share cached tokens.
func cacheKey(c Credentials) string {
	sum := sha256.Sum256([]byte(c.Password))
	return strings.Join([]string{c.CacheScope, strings.TrimRight(c.BaseURL, "/"), c.Email, c.Mode, hex.EncodeToString(sum[:])}, "|")
}

func cachedToken(key string) (Token, bool) {
	tokenMu.Lock()
	defer tokenMu.Unlock()
	tok, ok := tokenCache[key]
	if !ok || !tokenUsable(tok) {
		return Token{}, false
	}
	return tok, true
}

func tokenUsable(t Token) bool {
	return t.Value != "" && time.Now().Add(tokenRefreshSkew).Before(t.Expiry)
}

// invalidate drops the cached token for creds, but only if it is still the one
// that was rejected: another goroutine may already have cached a fresh one.
func invalidate(c Credentials, rejected string) {
	key := cacheKey(c)
	tokenMu.Lock()
	defer tokenMu.Unlock()
	if tok, ok := tokenCache[key]; ok && tok.Value == rejected {
		delete(tokenCache, key)
	}
}
