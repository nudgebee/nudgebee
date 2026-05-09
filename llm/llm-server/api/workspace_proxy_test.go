package api

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"nudgebee/llm/config"
	"nudgebee/llm/security"
	"nudgebee/llm/workspace"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAuthorizeWorkspaceRequest(t *testing.T) {
	// Setup config
	config.Config.LlmServerJwtSecret = "test-secret"

	// Mock DB cache
	security.SetTenantIdCacheForTest("acc1", "tenant1")
	security.SetTenantIdCacheForTest("acc2", "tenant1")

	tests := []struct {
		name               string
		token              string
		requestedAccountId string
		setupToken         func() string
		expectedError      string
	}{
		{
			name:               "Missing Token",
			token:              "",
			requestedAccountId: "acc1",
			expectedError:      "missing authentication token",
		},
		{
			name:               "Invalid Token",
			token:              "invalid.token.string",
			requestedAccountId: "acc1",
			expectedError:      "invalid or expired token",
		},
		{
			name: "Valid Token",
			setupToken: func() string {
				claims := workspace.WorkspaceTokenClaims{
					AccountId: "acc1",
					TenantId:  "tenant1",
					RegisteredClaims: jwt.RegisteredClaims{
						ExpiresAt: jwt.NewNumericDate(time.Now().Add(1 * time.Hour)),
					},
				}
				token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
				s, _ := token.SignedString([]byte(config.Config.LlmServerJwtSecret))
				return s
			},
			requestedAccountId: "acc1",
			expectedError:      "",
		},
		{
			name: "Wrong Account ID",
			setupToken: func() string {
				claims := workspace.WorkspaceTokenClaims{
					AccountId: "acc2", // Different account
					TenantId:  "tenant1",
					RegisteredClaims: jwt.RegisteredClaims{
						ExpiresAt: jwt.NewNumericDate(time.Now().Add(1 * time.Hour)),
					},
				}
				token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
				s, _ := token.SignedString([]byte(config.Config.LlmServerJwtSecret))
				return s
			},
			requestedAccountId: "acc1",
			expectedError:      "token does not match account",
		},
		{
			name: "Wrong Tenant ID",
			setupToken: func() string {
				claims := workspace.WorkspaceTokenClaims{
					AccountId: "acc1",
					TenantId:  "wrong-tenant",
					RegisteredClaims: jwt.RegisteredClaims{
						ExpiresAt: jwt.NewNumericDate(time.Now().Add(1 * time.Hour)),
					},
				}
				token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
				s, _ := token.SignedString([]byte(config.Config.LlmServerJwtSecret))
				return s
			},
			requestedAccountId: "acc1",
			expectedError:      "token does not match tenant",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)

			token := tt.token
			if tt.setupToken != nil {
				token = tt.setupToken()
			}

			if token != "" {
				c.Request = httptest.NewRequest("GET", "/", nil)
				c.Request.Header.Set("X-Workspace-Token", token)
			} else {
				c.Request = httptest.NewRequest("GET", "/", nil)
			}

			_, err := authorizeWorkspaceRequest(c, tt.requestedAccountId, nil, nil)

			if tt.expectedError != "" {
				assert.Error(t, err)
				assert.Contains(t, err.Error(), tt.expectedError)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

// errReader satisfies io.Reader and always returns a configured error. Used
// to exercise the Read-failure branch in readWorkspaceFileForDownload.
type errReader struct{ err error }

func (e errReader) Read(_ []byte) (int, error) { return 0, e.err }

func TestReadWorkspaceFileForDownload(t *testing.T) {
	t.Run("happy path — valid UTF-8 text passes through", func(t *testing.T) {
		src := strings.NewReader("hello, world\nline two\n")
		content, status, errMsg := readWorkspaceFileForDownload(src, 1024)
		assert.Equal(t, http.StatusOK, status)
		assert.Empty(t, errMsg)
		assert.Equal(t, "hello, world\nline two\n", content)
	})

	t.Run("exactly at the limit — accepted", func(t *testing.T) {
		// 16 bytes of content, 16-byte cap.
		src := strings.NewReader("0123456789abcdef")
		content, status, errMsg := readWorkspaceFileForDownload(src, 16)
		assert.Equal(t, http.StatusOK, status)
		assert.Empty(t, errMsg)
		assert.Equal(t, "0123456789abcdef", content)
	})

	t.Run("one byte over the limit — 413", func(t *testing.T) {
		src := strings.NewReader("0123456789abcdefX")
		content, status, errMsg := readWorkspaceFileForDownload(src, 16)
		assert.Equal(t, http.StatusRequestEntityTooLarge, status)
		assert.Contains(t, errMsg, "16-byte download limit")
		assert.Empty(t, content)
	})

	t.Run("binary / invalid UTF-8 — 415", func(t *testing.T) {
		// 0xff 0xfe is not a valid UTF-8 start byte sequence.
		src := bytes.NewReader([]byte{0x48, 0x69, 0xff, 0xfe, 0x0a})
		content, status, errMsg := readWorkspaceFileForDownload(src, 1024)
		assert.Equal(t, http.StatusUnsupportedMediaType, status)
		assert.Contains(t, errMsg, "UTF-8")
		assert.Empty(t, content)
	})

	t.Run("reader error surfaces as 500", func(t *testing.T) {
		content, status, errMsg := readWorkspaceFileForDownload(errReader{err: errors.New("disk exploded")}, 1024)
		assert.Equal(t, http.StatusInternalServerError, status)
		assert.Contains(t, errMsg, "disk exploded")
		assert.Empty(t, content)
	})

	t.Run("empty file is valid text", func(t *testing.T) {
		src := strings.NewReader("")
		content, status, errMsg := readWorkspaceFileForDownload(src, 1024)
		assert.Equal(t, http.StatusOK, status)
		assert.Empty(t, errMsg)
		assert.Equal(t, "", content)
	})
}

func TestWorkspaceFileMaxDownloadBytes(t *testing.T) {
	t.Run("falls back to 5 MiB when config is zero", func(t *testing.T) {
		orig := config.Config.LlmServerWorkspaceFileMaxDownloadBytes
		defer func() { config.Config.LlmServerWorkspaceFileMaxDownloadBytes = orig }()

		config.Config.LlmServerWorkspaceFileMaxDownloadBytes = 0
		assert.Equal(t, int64(5*1024*1024), workspaceFileMaxDownloadBytes())

		config.Config.LlmServerWorkspaceFileMaxDownloadBytes = -1
		assert.Equal(t, int64(5*1024*1024), workspaceFileMaxDownloadBytes())
	})

	t.Run("honors positive config value", func(t *testing.T) {
		orig := config.Config.LlmServerWorkspaceFileMaxDownloadBytes
		defer func() { config.Config.LlmServerWorkspaceFileMaxDownloadBytes = orig }()

		config.Config.LlmServerWorkspaceFileMaxDownloadBytes = 1024
		assert.Equal(t, int64(1024), workspaceFileMaxDownloadBytes())
	})
}

// fakeWorkspaceManager lets tests inject controllable ReadFileStream behavior
// without standing up the full workspace package. All other methods are
// unused; leaving them unimplemented would require a trivially long struct,
// so we embed an interface with nil methods via assertion only at the one
// call site we actually exercise.
type fakeWorkspaceManager struct {
	workspace.WorkspaceManager // embed so unused methods are nil / panic if touched
	readFileStream             func(accountId, conversationId, path string) (io.ReadCloser, error)
}

func (f *fakeWorkspaceManager) ReadFileStream(_ *security.RequestContext, accountId, conversationId, path string) (io.ReadCloser, error) {
	return f.readFileStream(accountId, conversationId, path)
}

type nopCloser struct{ io.Reader }

func (nopCloser) Close() error { return nil }

func TestHandleWorkspaceGetFileWithPayload(t *testing.T) {
	// Restore the factory var after the test run.
	origFactory := newWorkspaceManager
	defer func() { newWorkspaceManager = origFactory }()

	origLimit := config.Config.LlmServerWorkspaceFileMaxDownloadBytes
	defer func() { config.Config.LlmServerWorkspaceFileMaxDownloadBytes = origLimit }()
	config.Config.LlmServerWorkspaceFileMaxDownloadBytes = 32 // tiny, for overrun test

	ctx := security.NewRequestContext(context.Background(), security.NewSecurityContextForSuperAdmin(), slog.Default(), nil, nil)
	payload := map[string]any{
		"account_id":      "acc1",
		"path":            "logs/report.txt",
		"conversation_id": "conv1",
	}

	t.Run("200 returns file body as JSON string", func(t *testing.T) {
		newWorkspaceManager = func() workspace.WorkspaceManager {
			return &fakeWorkspaceManager{
				readFileStream: func(accountId, conversationId, path string) (io.ReadCloser, error) {
					assert.Equal(t, "acc1", accountId)
					assert.Equal(t, "conv1", conversationId)
					assert.Equal(t, "logs/report.txt", path)
					return nopCloser{strings.NewReader("hello world")}, nil
				},
			}
		}

		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest("POST", "/", nil)

		handleWorkspaceGetFileWithPayload(c, payload, ctx)

		require.Equal(t, http.StatusOK, w.Code)
		assert.Equal(t, `"hello world"`, strings.TrimSpace(w.Body.String()))
	})

	t.Run("413 when file exceeds the configured cap", func(t *testing.T) {
		newWorkspaceManager = func() workspace.WorkspaceManager {
			return &fakeWorkspaceManager{
				readFileStream: func(_, _, _ string) (io.ReadCloser, error) {
					// 33 bytes vs a 32-byte cap
					return nopCloser{strings.NewReader(strings.Repeat("x", 33))}, nil
				},
			}
		}

		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest("POST", "/", nil)

		handleWorkspaceGetFileWithPayload(c, payload, ctx)

		assert.Equal(t, http.StatusRequestEntityTooLarge, w.Code)
		assert.Contains(t, w.Body.String(), "download limit")
	})

	t.Run("415 when file is not valid UTF-8", func(t *testing.T) {
		newWorkspaceManager = func() workspace.WorkspaceManager {
			return &fakeWorkspaceManager{
				readFileStream: func(_, _, _ string) (io.ReadCloser, error) {
					return nopCloser{bytes.NewReader([]byte{0xff, 0xfe, 0x00})}, nil
				},
			}
		}

		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest("POST", "/", nil)

		handleWorkspaceGetFileWithPayload(c, payload, ctx)

		assert.Equal(t, http.StatusUnsupportedMediaType, w.Code)
		assert.Contains(t, w.Body.String(), "UTF-8")
	})

	t.Run("404 when workspace manager reports not found", func(t *testing.T) {
		newWorkspaceManager = func() workspace.WorkspaceManager {
			return &fakeWorkspaceManager{
				readFileStream: func(_, _, _ string) (io.ReadCloser, error) {
					return nil, errors.New("file not found: logs/report.txt")
				},
			}
		}

		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest("POST", "/", nil)

		handleWorkspaceGetFileWithPayload(c, payload, ctx)

		assert.Equal(t, http.StatusNotFound, w.Code)
		assert.Contains(t, w.Body.String(), "File not found")
	})

	t.Run("500 when workspace manager reports a generic error", func(t *testing.T) {
		newWorkspaceManager = func() workspace.WorkspaceManager {
			return &fakeWorkspaceManager{
				readFileStream: func(_, _, _ string) (io.ReadCloser, error) {
					return nil, errors.New("relay unavailable")
				},
			}
		}

		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest("POST", "/", nil)

		handleWorkspaceGetFileWithPayload(c, payload, ctx)

		assert.Equal(t, http.StatusInternalServerError, w.Code)
		assert.Contains(t, w.Body.String(), "relay unavailable")
	})
}
