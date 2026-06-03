package integrations

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"nudgebee/runbook/internal/tasks/testutils"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

type CustomString string

func TestHttpRequestTask_Execute(t *testing.T) {
	// Create a mock server
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/success":
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			if _, err := w.Write([]byte(`{"status": "ok"}`)); err != nil {
				t.Fatalf("failed to write response: %v", err)
			}
		case "/error":
			w.WriteHeader(http.StatusInternalServerError)
			if _, err := w.Write([]byte(`{"error": "internal server error"}`)); err != nil {
				t.Fatalf("failed to write response: %v", err)
			}
		case "/post":
			if r.Method != http.MethodPost {
				w.WriteHeader(http.StatusMethodNotAllowed)
				return
			}
			body, _ := io.ReadAll(r.Body)
			if r.URL.Path == "/post" && r.Header.Get("X-Test-Header") != "true" && string(body) == "" {
				w.WriteHeader(http.StatusBadRequest)
				if _, err := w.Write([]byte(`{"error": "missing header"}`)); err != nil {
					t.Fatalf("failed to write response: %v", err)
				}
				return
			}
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			if string(body) != "" {
				if _, err := w.Write(body); err != nil {
					t.Fatalf("failed to write response: %v", err)
				}
				return
			}
			if _, err := w.Write([]byte(`{"received": true}`)); err != nil {
				t.Fatalf("failed to write response: %v", err)
			}
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	task := &HttpTask{}
	taskCtx := testutils.NewTestTaskContext(os.Getenv("TEST_TENANT_ID"), os.Getenv("TEST_ACCOUNT_ID"), "", slog.Default())

	strBody := `{"ref": "main"}`
	testCases := []struct {
		name          string
		params        map[string]any
		expected      any
		expectErr     bool
		expectedError string
	}{
		{
			name: "Successful GET Request",
			params: map[string]any{
				"url": server.URL + "/success",
			},
			expected: map[string]any{
				"body": map[string]any{
					"status": "ok",
				},
				"headers": map[string]any{
					"Content-Length": "16",               // Updated
					"Content-Type":   "application/json", // Updated
					"Date":           "{{ANY}}",
				},
				"status_code": float64(200),
			},
		},
		{
			name: "Successful POST Request with Headers",
			params: map[string]any{
				"url":    server.URL + "/post",
				"method": "POST",
				"headers": map[string]string{
					"X-Test-Header": "true",
				},
			},
			expected: map[string]any{
				"body": map[string]any{
					"received": true,
				},
				"headers": map[string]any{
					"Content-Length": "18",               // Updated
					"Content-Type":   "application/json", // Updated
					"Date":           "{{ANY}}",
				},
				"status_code": float64(200),
			},
		},
		{
			name: "Server Error",
			params: map[string]any{
				"url": server.URL + "/error",
			},
			expectErr:     true,
			expectedError: "http request failed with status 500",
		},
		{
			name: "Invalid URL",
			params: map[string]any{
				"url": "invalid-url",
			},
			expectErr:     true,
			expectedError: "invalid URL format", // Updated
		},
		{
			name: "Missing URL",
			params: map[string]any{
				"method": "GET",
			},
			expectErr:     true,
			expectedError: "url is a required parameter",
		},
		{
			name: "Custom String Body (Prevent Double Encoding)",
			params: map[string]any{
				"url":    server.URL + "/post",
				"method": "POST",
				"body":   CustomString(`{"ref": "main"}`),
			},
			expected: map[string]any{
				"body": map[string]any{
					"ref": "main",
				},
				"headers": map[string]any{
					"Content-Length": "15",
					"Content-Type":   "application/json",
					"Date":           "{{ANY}}",
				},
				"status_code": float64(200),
			},
		},
		{
			name: "String Body",
			params: map[string]any{
				"url":    server.URL + "/post",
				"method": "POST",
				"body":   `{"ref": "main"}`,
			},
			expected: map[string]any{
				"body": map[string]any{
					"ref": "main",
				},
				"headers": map[string]any{
					"Content-Length": "15",
					"Content-Type":   "application/json",
					"Date":           "{{ANY}}",
				},
				"status_code": float64(200),
			},
		},
		{
			name: "Pointer to String Body",
			params: map[string]any{
				"url":    server.URL + "/post",
				"method": "POST",
				"body":   &strBody,
			},
			expected: map[string]any{
				"body": map[string]any{
					"ref": "main",
				},
				"headers": map[string]any{
					"Content-Length": "15",
					"Content-Type":   "application/json",
					"Date":           "{{ANY}}",
				},
				"status_code": float64(200),
			},
		},
		{
			name: "Map Body",
			params: map[string]any{
				"url":    server.URL + "/post",
				"method": "POST",
				"body":   map[string]any{"ref": "main"},
			},
			expected: map[string]any{
				"body": map[string]any{
					"ref": "main",
				},
				"headers": map[string]any{
					"Content-Length": "14", // map marshals to {"ref":"main"} (no spaces)
					"Content-Type":   "application/json",
					"Date":           "{{ANY}}",
				},
				"status_code": float64(200),
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			result, err := task.Execute(taskCtx, tc.params)

			if tc.expectErr {
				assert.Error(t, err)
				if tc.expectedError != "" {
					assert.Contains(t, err.Error(), tc.expectedError)
				}
			} else {
				assert.NoError(t, err)
				// Handle dynamic headers like Date
				resultMap := result.(map[string]any)
				if headers, ok := resultMap["headers"].(map[string]any); ok {
					headers["Date"] = "{{ANY}}"
				}
				assert.Equal(t, tc.expected, result)
			}
		})
	}
}

func TestHttpRequestTask_BearerAuth(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		authHeader := r.Header.Get("Authorization")
		if authHeader != "Bearer test-token-123" {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"error": "unauthorized"}`))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"authenticated": true}`))
	}))
	defer server.Close()

	task := &HttpTask{}
	taskCtx := testutils.NewTestTaskContext("", "", "", slog.Default())

	result, err := task.Execute(taskCtx, map[string]any{
		"url":          server.URL,
		"auth_type":    "bearer",
		"bearer_token": "test-token-123",
	})

	assert.NoError(t, err)
	resultMap := result.(map[string]any)
	assert.Equal(t, float64(200), resultMap["status_code"])
}

func TestHttpRequestTask_BasicAuth(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user, pass, ok := r.BasicAuth()
		if !ok || user != "admin" || pass != "secret" {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"error": "unauthorized"}`))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"authenticated": true}`))
	}))
	defer server.Close()

	task := &HttpTask{}
	taskCtx := testutils.NewTestTaskContext("", "", "", slog.Default())

	result, err := task.Execute(taskCtx, map[string]any{
		"url":                 server.URL,
		"auth_type":           "basic",
		"basic_auth_username": "admin",
		"basic_auth_password": "secret",
	})

	assert.NoError(t, err)
	resultMap := result.(map[string]any)
	assert.Equal(t, float64(200), resultMap["status_code"])
}

func TestHttpRequestTask_OAuth2Auth(t *testing.T) {
	// OAuth token server
	tokenServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(oauthTokenResponse{
			AccessToken: "oauth-test-token",
			ExpiresIn:   3600,
		})
	}))
	defer tokenServer.Close()

	// API server that requires the OAuth token
	apiServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer oauth-test-token" {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"error": "unauthorized"}`))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"authenticated": true}`))
	}))
	defer apiServer.Close()

	task := &HttpTask{}
	taskCtx := testutils.NewTestTaskContext("", "", "", slog.Default())

	result, err := task.Execute(taskCtx, map[string]any{
		"url":                 apiServer.URL,
		"auth_type":           "oauth2",
		"oauth_token_url":     tokenServer.URL,
		"oauth_client_id":     "test-client",
		"oauth_client_secret": "test-secret",
		"oauth_scope":         "api.read",
	})

	assert.NoError(t, err)
	resultMap := result.(map[string]any)
	assert.Equal(t, float64(200), resultMap["status_code"])
}

func TestHttpRequestTask_OAuth2MissingFields(t *testing.T) {
	task := &HttpTask{}
	taskCtx := testutils.NewTestTaskContext("", "", "", slog.Default())

	_, err := task.Execute(taskCtx, map[string]any{
		"url":       "https://example.com",
		"auth_type": "oauth2",
		// Missing required OAuth fields
	})

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "OAuth 2.0 requires")
}

func TestHttpRequestTask_APIKeyAuth(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-API-Key") != "my-api-key" {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"error": "unauthorized"}`))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"authenticated": true}`))
	}))
	defer server.Close()

	task := &HttpTask{}
	taskCtx := testutils.NewTestTaskContext("", "", "", slog.Default())

	result, err := task.Execute(taskCtx, map[string]any{
		"url":             server.URL,
		"auth_type":       "api_key",
		"api_key":         "my-api-key",
		"api_header_name": "X-API-Key",
	})

	assert.NoError(t, err)
	resultMap := result.(map[string]any)
	assert.Equal(t, float64(200), resultMap["status_code"])
}

// Integration test: OAuth2 client_credentials against real Auth0 + httpbin.
// Set these env vars to run:
//
//	OAUTH_TOKEN_URL=https://your-tenant.auth0.com/oauth/token
//	OAUTH_CLIENT_ID=...
//	OAUTH_CLIENT_SECRET=...
//	OAUTH_AUDIENCE=https://your-tenant.auth0.com/api/v2/
func TestHttpRequestTask_OAuth2_Integration(t *testing.T) {
	tokenURL := os.Getenv("OAUTH_TOKEN_URL")
	clientID := os.Getenv("OAUTH_CLIENT_ID")
	clientSecret := os.Getenv("OAUTH_CLIENT_SECRET")
	audience := os.Getenv("OAUTH_AUDIENCE")

	if tokenURL == "" || clientID == "" || clientSecret == "" {
		t.Skip("Skipping OAuth2 integration test. Set OAUTH_TOKEN_URL, OAUTH_CLIENT_ID, OAUTH_CLIENT_SECRET to enable.")
	}

	// Local server that echoes back the Authorization header.
	// We use this instead of httpbin.org because Auth0 JWTs are too large
	// for httpbin's header size limit.
	var receivedAuth string
	echoServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok": true}`))
	}))
	defer echoServer.Close()

	task := &HttpTask{}
	taskCtx := testutils.NewTestTaskContext("", "", "", slog.Default())

	result, err := task.Execute(taskCtx, map[string]any{
		"url":                 echoServer.URL,
		"auth_type":           "oauth2",
		"oauth_token_url":     tokenURL,
		"oauth_client_id":     clientID,
		"oauth_client_secret": clientSecret,
		"oauth_audience":      audience,
	})

	assert.NoError(t, err)
	resultMap := result.(map[string]any)
	assert.Equal(t, float64(200), resultMap["status_code"])
	assert.NotEmpty(t, receivedAuth, "Authorization header should be present")
	assert.True(t, strings.HasPrefix(receivedAuth, "Bearer "), "Authorization header should start with 'Bearer '")
	t.Logf("OAuth2 token received (first 50 chars): %.50s...", receivedAuth)
}
