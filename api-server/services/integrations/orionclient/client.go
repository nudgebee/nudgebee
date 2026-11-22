// Package orionclient talks to the SolarWinds Information Service (SWIS), the
// query API of a self-hosted SolarWinds Platform (Orion) server.
//
// SWIS speaks SWQL, a SQL dialect, over a single JSON endpoint:
//
//	POST {base}/SolarWinds/InformationService/v3/Json/Query
//	{"query": "SELECT TOP 1 NodeID FROM Orion.Nodes", "parameters": {...}}
//	-> {"results": [ {"NodeID": 1}, ... ]}
//
// Errors come back as a JSON body carrying a Message, with a 4xx/5xx status.
package orionclient

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// QueryPath is the SWQL endpoint, relative to the server's base URL. It is also
// what the forager receives as the relative path, so it must not start the URL.
const QueryPath = "/SolarWinds/InformationService/v3/Json/Query"

const defaultTimeout = 60 * time.Second

// One transport per TLS stance, shared across every query. A transport holds the
// connection pool, so building one per request would open a new connection each
// time and leave the old ones to idle out. The pool sizes are set explicitly
// because the default MaxIdleConnsPerHost of 2 would close and reopen connections
// on a page that asks for several keys at once.
//
// A transport is safe to share; an http.Client is not, since callers set its
// Timeout, so each query gets a fresh Client over one of these.
var (
	secureTransport = &http.Transport{
		MaxIdleConns:        100,
		MaxIdleConnsPerHost: 100,
		IdleConnTimeout:     90 * time.Second,
	}
	// #nosec G402 -- opt-in per integration: SWIS ships a self-signed certificate
	// and most on-prem installs never replace it.
	insecureTransport = &http.Transport{
		TLSClientConfig:     &tls.Config{InsecureSkipVerify: true},
		MaxIdleConns:        100,
		MaxIdleConnsPerHost: 100,
		IdleConnTimeout:     90 * time.Second,
	}
)

// ProxyTransport sends one HTTP request through a forager in the customer's
// network. It mirrors promclient.ProxyTransport so the same forager plumbing
// carries both, and takes a path relative to the forager's own base URL.
type ProxyTransport func(ctx context.Context, method, relPath string, header http.Header, body []byte) (int, http.Header, []byte, error)

// Config is one Orion server.
type Config struct {
	BaseURL       string
	Username      string
	Password      string
	TLSSkipVerify bool

	// ViaForager is set when the server is only reachable from inside the
	// customer's network. Query then refuses to dial directly.
	ViaForager     bool
	ProxyTransport ProxyTransport

	Timeout time.Duration
}

// Row is one SWQL result row: column name to value, as SWIS typed it.
type Row map[string]any

type queryRequest struct {
	Query      string         `json:"query"`
	Parameters map[string]any `json:"parameters,omitempty"`
}

type queryResponse struct {
	Results []Row `json:"results"`
}

// swisError is the shape SWIS returns for a rejected query.
type swisError struct {
	Message string `json:"Message"`
}

// Query runs one SWQL statement. Parameters are passed to SWIS rather than
// interpolated: SWQL takes @name placeholders, and a caption or hostname coming
// from a request must never be pasted into the statement.
func (c Config) Query(ctx context.Context, swql string, params map[string]any) ([]Row, error) {
	body, err := json.Marshal(queryRequest{Query: swql, Parameters: params})
	if err != nil {
		return nil, fmt.Errorf("orion: encoding query: %w", err)
	}

	header := http.Header{"Content-Type": []string{"application/json"}}

	var (
		status   int
		respBody []byte
	)
	if c.ViaForager {
		if c.ProxyTransport == nil {
			return nil, fmt.Errorf("orion: connection_mode=%s needs a forager transport; refusing to dial %s directly", "vm_agent", c.BaseURL)
		}
		status, _, respBody, err = c.ProxyTransport(ctx, http.MethodPost, QueryPath, header, body)
	} else {
		status, respBody, err = c.doDirect(ctx, header, body)
	}
	if err != nil {
		return nil, err
	}

	if status != http.StatusOK {
		return nil, fmt.Errorf("orion: %s", describeSWISFailure(status, respBody))
	}

	var parsed queryResponse
	if err := json.Unmarshal(respBody, &parsed); err != nil {
		return nil, fmt.Errorf("orion: the server did not return SWIS JSON (status %d); check that the URL points at the Information Service port, not the web console", status)
	}
	return parsed.Results, nil
}

func (c Config) doDirect(ctx context.Context, header http.Header, body []byte) (int, []byte, error) {
	endpoint, err := JoinQueryPath(c.BaseURL)
	if err != nil {
		return 0, nil, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return 0, nil, fmt.Errorf("orion: building request: %w", err)
	}
	req.Header = header.Clone()
	req.SetBasicAuth(c.Username, c.Password)

	timeout := c.Timeout
	if timeout <= 0 {
		timeout = defaultTimeout
	}
	transport := secureTransport
	if c.TLSSkipVerify {
		transport = insecureTransport
	}
	client := &http.Client{Timeout: timeout, Transport: transport}

	resp, err := client.Do(req)
	if err != nil {
		return 0, nil, fmt.Errorf("orion: cannot reach %s: %w", c.BaseURL, explainTLSFailure(err))
	}
	defer func() { _ = resp.Body.Close() }()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return resp.StatusCode, nil, fmt.Errorf("orion: reading response: %w", err)
	}
	return resp.StatusCode, respBody, nil
}

// describeSWISFailure turns a status and body into something a person can act on.
// SWIS answers a bad credential with 401 and a malformed query with 400 plus a
// Message naming the entity or column it did not recognise.
func describeSWISFailure(status int, body []byte) string {
	var e swisError
	if err := json.Unmarshal(body, &e); err == nil && strings.TrimSpace(e.Message) != "" {
		return fmt.Sprintf("SWIS returned %d: %s", status, strings.TrimSpace(e.Message))
	}
	switch status {
	case http.StatusUnauthorized:
		return "SWIS rejected the credentials (401). Check the Orion account and password."
	case http.StatusForbidden:
		return "SWIS accepted the credentials but refused the query (403). The Orion account needs read access to the entities being queried."
	case http.StatusNotFound:
		return "SWIS was not found at that URL (404). Use the Information Service port (17774, or 17778 before SolarWinds Platform 2023.1), not the web console port."
	}
	snippet := strings.TrimSpace(string(body))
	if len(snippet) > 200 {
		snippet = snippet[:200] + "…"
	}
	return fmt.Sprintf("SWIS returned %d: %s", status, snippet)
}

// explainTLSFailure annotates the one TLS failure every default SolarWinds install
// hits. SWIS ships a self-signed certificate whose serial number is negative, and
// Go refuses to PARSE such a certificate — which happens before verification, so
// tls_skip_verify cannot help and the raw error does not say why. Confirmed against
// SolarWinds Platform 2026.4.0, whose certificate is CN=SolarWinds-Orion with
// serial -4EAB6A6A1879596BBFA2A5BACD295E6F.
func explainTLSFailure(err error) error {
	if err == nil || !strings.Contains(err.Error(), "negative serial number") {
		return err
	}
	return fmt.Errorf("%w — this is SolarWinds' own self-signed certificate, which Nudgebee cannot accept at any TLS setting, "+
		"because the certificate is rejected before verification. Replace the SWIS certificate on the Orion server with one "+
		"issued by a certificate authority (SolarWinds Platform: Settings > All Settings > HTTPS Settings)", err)
}

// ExplainTLSFailureForTest exposes explainTLSFailure to the integration tests.
func ExplainTLSFailureForTest(err error) error { return explainTLSFailure(err) }

// JoinQueryPath appends the SWQL endpoint to a server base URL, tolerating a
// trailing slash and a base that already carries the path.
func JoinQueryPath(base string) (string, error) {
	trimmed := strings.TrimSpace(base)
	if trimmed == "" {
		return "", fmt.Errorf("orion: the server URL is empty")
	}
	u, err := url.Parse(trimmed)
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
		return "", fmt.Errorf("must be an http or https URL with a host, including the port (e.g. https://orion.example.com:17774)")
	}
	if strings.HasSuffix(strings.TrimSuffix(u.Path, "/"), QueryPath) {
		return u.String(), nil
	}
	u.Path = strings.TrimSuffix(u.Path, "/") + QueryPath
	return u.String(), nil
}
