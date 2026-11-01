package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"nudgebee/llm/audit"
	"nudgebee/llm/common"
	"nudgebee/llm/config"
	"nudgebee/llm/security"
	"nudgebee/llm/tools"
	core "nudgebee/llm/tools/core"
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

func TestWorkspaceRelayTargetPreservesWorkspaceIdentity(t *testing.T) {
	ctx := core.NbToolContext{Ctx: security.NewRequestContextForSuperAdmin(), AccountId: "original-aws", ConversationId: "original-conversation", ToolConfig: core.ToolConfig{Name: "selected-cluster", Values: []core.ToolConfigValue{{Name: "id", Value: "target-k8s"}}}}
	target, err := workspaceRelayTarget(ctx, tools.RelayJobKubectl, "selected-cluster", "kubectl get pods")
	require.NoError(t, err)
	require.Equal(t, "target-k8s", target)
	require.Equal(t, "original-aws", ctx.AccountId)
	require.Equal(t, "original-conversation", ctx.ConversationId)
	_, err = workspaceRelayTarget(ctx, tools.RelayJobKubectl, "stale-selection", "kubectl get pods")
	require.Error(t, err)
	ctx.Ctx = security.NewRequestContextForTenantAccountAdmin("tenant", "user", []string{"original-aws"})
	_, err = workspaceRelayTarget(ctx, tools.RelayJobKubectl, "selected-cluster", "kubectl get pods")
	require.ErrorContains(t, err, "access denied")

	// Read-only user attempting a write command is denied
	var readOnlySc security.SecurityContext
	_ = json.Unmarshal([]byte(`{
		"RoleNames": ["account_admin_readonly"],
		"ScopedEntityIds": {
			"account_admin_readonly": ["target-k8s"]
		}
	}`), &readOnlySc)
	ctx.Ctx = security.NewRequestContext(context.Background(), &readOnlySc, slog.Default(), nil, nil)
	_, err = workspaceRelayTarget(ctx, tools.RelayJobKubectl, "selected-cluster", "kubectl delete pod foo")
	require.ErrorContains(t, err, "access denied")
}

func TestWorkspaceRelayTargetWriteAuthorization(t *testing.T) {
	var readOnlySc security.SecurityContext
	_ = json.Unmarshal([]byte(`{
		"TenantId": "t1",
		"UserId": "u1",
		"AccountIds": ["target-k8s"],
		"Roles": ["account_admin_readonly"],
		"ScopedEntityIds": {
			"account_admin_readonly": ["target-k8s"]
		}
	}`), &readOnlySc)
	readOnlyCtx := core.NbToolContext{
		Ctx:            security.NewRequestContext(context.Background(), &readOnlySc, slog.Default(), nil, nil),
		AccountId:      "workspace-acct",
		ConversationId: "conv-1",
		ToolConfig: core.ToolConfig{
			Name:   "selected-cluster",
			Values: []core.ToolConfigValue{{Name: "id", Value: "target-k8s"}},
		},
	}

	// Read commands allowed for read-only user
	for _, readCmd := range []string{
		"kubectl get pods -A",
		"kubectl describe pod api-1",
		"kubectl logs api-1 --tail 100",
		"kubectl rollout status deployment/api",
		"kubectl auth can-i get pods",
	} {
		target, err := workspaceRelayTarget(readOnlyCtx, tools.RelayJobKubectl, "selected-cluster", readCmd)
		require.NoError(t, err, readCmd)
		require.Equal(t, "target-k8s", target, readCmd)
	}

	// Mutating commands denied for read-only user
	for _, writeCmd := range []string{
		"kubectl delete pod api-1",
		"kubectl apply -f deployment.yaml",
		"kubectl scale deployment api --replicas=3",
		"kubectl patch deployment api -p '{}'",
		"kubectl rollout restart deployment/api",
	} {
		_, err := workspaceRelayTarget(readOnlyCtx, tools.RelayJobKubectl, "selected-cluster", writeCmd)
		require.ErrorContains(t, err, "access denied", writeCmd)
	}

	for _, readCmd := range []string{"helm 'list' '-A'", "helm 'status' 'api'"} {
		target, err := workspaceRelayTarget(readOnlyCtx, tools.RelayJobHelm, "selected-cluster", readCmd)
		require.NoError(t, err, readCmd)
		require.Equal(t, "target-k8s", target)
	}
	for _, writeCmd := range []string{"helm 'install' 'api' './chart'", "helm 'upgrade' 'api' './chart'", "helm 'uninstall' 'api'"} {
		_, err := workspaceRelayTarget(readOnlyCtx, tools.RelayJobHelm, "selected-cluster", writeCmd)
		require.ErrorContains(t, err, "access denied", writeCmd)
	}

	// Mutating commands allowed for write user
	var writeSc security.SecurityContext
	_ = json.Unmarshal([]byte(`{
		"TenantId": "t1",
		"UserId": "u1",
		"AccountIds": ["target-k8s"],
		"Roles": ["account_admin"],
		"ScopedEntityIds": {
			"account_admin": ["target-k8s"]
		}
	}`), &writeSc)
	writeCtx := core.NbToolContext{
		Ctx:            security.NewRequestContext(context.Background(), &writeSc, slog.Default(), nil, nil),
		AccountId:      "workspace-acct",
		ConversationId: "conv-1",
		ToolConfig: core.ToolConfig{
			Name:   "selected-cluster",
			Values: []core.ToolConfigValue{{Name: "id", Value: "target-k8s"}},
		},
	}
	for _, writeCmd := range []string{
		"kubectl delete pod api-1",
		"kubectl apply -f deployment.yaml",
		"kubectl scale deployment api --replicas=3",
	} {
		target, err := workspaceRelayTarget(writeCtx, tools.RelayJobKubectl, "selected-cluster", writeCmd)
		require.NoError(t, err, writeCmd)
		require.Equal(t, "target-k8s", target, writeCmd)
	}

	// Nil context or nil security context fails safely without panicking
	nilCtx := core.NbToolContext{
		Ctx:        nil,
		AccountId:  "workspace-acct",
		ToolConfig: core.ToolConfig{Name: "selected-cluster", Values: []core.ToolConfigValue{{Name: "id", Value: "target-k8s"}}},
	}
	_, err := workspaceRelayTarget(nilCtx, tools.RelayJobKubectl, "selected-cluster", "kubectl get pods")
	require.ErrorContains(t, err, "access denied")

	nilScCtx := core.NbToolContext{
		Ctx:        security.NewRequestContext(context.Background(), nil, slog.Default(), nil, nil),
		AccountId:  "workspace-acct",
		ToolConfig: core.ToolConfig{Name: "selected-cluster", Values: []core.ToolConfigValue{{Name: "id", Value: "target-k8s"}}},
	}
	_, err = workspaceRelayTarget(nilScCtx, tools.RelayJobKubectl, "selected-cluster", "kubectl get pods")
	require.ErrorContains(t, err, "access denied")
}

func stubTenantAccounts(t *testing.T, accounts ...string) {
	t.Helper()
	orig := security.WorkspaceTenantAccounts
	security.WorkspaceTenantAccounts = func(string) ([]string, error) { return accounts, nil }
	t.Cleanup(func() { security.WorkspaceTenantAccounts = orig })
}

func workspaceTokenRequest(token string) *gin.Context {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/api/v1/workspace/execute", nil)
	c.Request.Header.Set("X-Workspace-Token", token)
	return c
}

func TestWorkspaceTokenBindsCrossClusterTarget(t *testing.T) {
	config.Config.LlmServerJwtSecret = "test-secret"
	security.SetTenantIdCacheForTest("bound-workspace", "bound-tenant")
	stubTenantAccounts(t, "bound-workspace", "selected-target")
	token, err := workspace.ExecutionToken(security.NewRequestContextForTenantAccountAdmin("bound-tenant", "user", []string{"bound-workspace", "selected-target"}), "bound-workspace", "selected-target", time.Minute)
	require.NoError(t, err)
	c := workspaceTokenRequest(token)
	_, err = authorizeWorkspaceRequest(c, "bound-workspace", nil, nil)
	require.NoError(t, err)
	allowed, ok := c.Get("workspace_allowed_target")
	require.True(t, ok)
	require.Equal(t, "selected-target", allowed)
	_, err = authorizeWorkspaceRequest(c, "selected-target", nil, nil)
	require.Error(t, err)
	_, err = workspace.ExecutionToken(security.NewRequestContextForTenantAccountAdmin("bound-tenant", "user", []string{"bound-workspace"}), "bound-workspace", "selected-target", time.Minute)
	require.Error(t, err)
}

func TestWorkspaceExecutionTokenWithoutTargetUsesSignedUserGrants(t *testing.T) {
	config.Config.LlmServerJwtSecret = "test-secret"
	security.SetTenantIdCacheForTest("grant-workspace", "grant-tenant")
	stubTenantAccounts(t, "grant-workspace", "grant-target")
	token, err := workspace.ExecutionToken(security.NewRequestContextForTenantAccountAdmin("grant-tenant", "user", []string{"grant-workspace", "grant-target"}), "grant-workspace", "", time.Minute)
	require.NoError(t, err)
	c := workspaceTokenRequest(token)
	ctx, err := authorizeWorkspaceRequest(c, "grant-workspace", nil, nil)
	require.NoError(t, err)
	_, bound := c.Get("workspace_allowed_target")
	require.False(t, bound)
	require.True(t, ctx.GetSecurityContext().HasAccountAccess("grant-target", security.SecurityAccessTypeRead))
}

func TestWorkspaceExecutionTokenRevocation(t *testing.T) {
	config.Config.LlmServerJwtSecret = "test-secret"
	security.SetTenantIdCacheForTest("revoked-workspace", "revoked-tenant")
	stubTenantAccounts(t, "revoked-workspace")
	token, err := workspace.ExecutionToken(security.NewRequestContextForTenantAccountAdmin("revoked-tenant", "user", []string{"revoked-workspace"}), "revoked-workspace", "", time.Minute)
	require.NoError(t, err)
	require.NoError(t, common.CacheSet(workspace.CacheNamespaceWorkspaceTokens, token, []byte("revoked"), common.CacheSetWithExpiration(time.Minute)))
	t.Cleanup(func() { _ = common.CacheDelete(workspace.CacheNamespaceWorkspaceTokens, token) })

	_, err = authorizeWorkspaceRequest(workspaceTokenRequest(token), "revoked-workspace", nil, nil)
	require.ErrorContains(t, err, "token has been revoked")
}

func TestWorkspaceExecutionTokensKeepConcurrentUsersIsolated(t *testing.T) {
	const workspaceAccount = "isolation-workspace"
	config.Config.LlmServerJwtSecret = "test-secret"
	security.SetTenantIdCacheForTest(workspaceAccount, "isolation-tenant")
	stubTenantAccounts(t, workspaceAccount, "cluster-a", "cluster-b")
	tools.StubTenantClusters(t, "isolation-tenant", []core.ToolConfig{
		{Name: "shared", Values: []core.ToolConfigValue{{Name: "id", Value: workspaceAccount}}},
		{Name: "only-a", Values: []core.ToolConfigValue{{Name: "id", Value: "cluster-a"}}},
		{Name: "only-b", Values: []core.ToolConfigValue{{Name: "id", Value: "cluster-b"}}},
	})

	for _, tc := range []struct {
		user    string
		account string
		visible string
		hidden  string
	}{
		{user: "user-a", account: "cluster-a", visible: "only-a", hidden: "only-b"},
		{user: "user-b", account: "cluster-b", visible: "only-b", hidden: "only-a"},
	} {
		t.Run(tc.user, func(t *testing.T) {
			caller := security.NewRequestContextForTenantAccountAdmin("isolation-tenant", tc.user, []string{workspaceAccount, tc.account})
			token, err := workspace.ExecutionToken(caller, workspaceAccount, "", time.Minute)
			require.NoError(t, err)
			ctx, err := authorizeWorkspaceRequest(workspaceTokenRequest(token), workspaceAccount, nil, nil)
			require.NoError(t, err)
			require.Equal(t, tc.user, ctx.GetSecurityContext().GetUserId())

			result, handled, err := virtualKubectlConfig(ctx, WorkspaceExecuteRequest{
				AccountId: workspaceAccount,
				Command:   "kubectl config get-contexts -o name",
			}, "shared")
			require.NoError(t, err)
			require.True(t, handled)
			require.Contains(t, result, tc.visible)
			require.NotContains(t, result, tc.hidden)
		})
	}
}

// An execution token authorizes the shim call as the user who ran the command:
// a read-only user can read the cluster through the shim but not write to it.
func TestExecutionTokenAuthorizesAsTheUser(t *testing.T) {
	config.Config.LlmServerJwtSecret = "test-secret"
	security.SetTenantIdCacheForTest("ws-acct", "t1")
	stubTenantAccounts(t, "ws-acct")
	var readOnly security.SecurityContext
	require.NoError(t, json.Unmarshal([]byte(`{"TenantId":"t1","UserId":"u1","AccountIds":["ws-acct"],"Roles":["account_admin_readonly"],"ScopedEntityIds":{"account_admin_readonly":["ws-acct"]}}`), &readOnly))
	token, err := workspace.ExecutionToken(security.NewRequestContext(context.Background(), &readOnly, slog.Default(), nil, nil), "ws-acct", "", time.Minute)
	require.NoError(t, err)

	c := workspaceTokenRequest(token)
	ctx, err := authorizeWorkspaceRequest(c, "ws-acct", nil, nil)
	require.NoError(t, err)
	assert.Equal(t, "u1", ctx.GetSecurityContext().GetUserId())
	assert.False(t, ctx.GetSecurityContext().IsTenantAdmin())
	audience, _ := c.Get(workspaceTokenAudienceKey)
	assert.Equal(t, workspace.AudienceWorkspaceExecute, audience)

	toolCtx := core.NbToolContext{Ctx: ctx, AccountId: "ws-acct"}
	target, err := workspaceRelayTarget(toolCtx, tools.RelayJobKubectl, "", "kubectl get pods")
	require.NoError(t, err)
	assert.Equal(t, "ws-acct", target)
	_, err = workspaceRelayTarget(toolCtx, tools.RelayJobKubectl, "", "kubectl delete pod api-1")
	require.ErrorContains(t, err, "access denied")
}

// A user whose only access is a custom-role grant to run AI on the account
// (no built-in account role) could run Nubi before execution tokens existed
// and must still be able to.
func TestExecutionTokenAdmitsCustomRoleGrant(t *testing.T) {
	config.Config.LlmServerJwtSecret = "test-secret"
	security.SetTenantIdCacheForTest("ws-acct", "t1")
	stubTenantAccounts(t, "ws-acct", "other-acct")
	var customOnly security.SecurityContext
	require.NoError(t, json.Unmarshal([]byte(`{"TenantId":"t1","UserId":"u1","AccountIds":["ws-acct","other-acct"],"ScopedCustomPermissions":{"ws-acct":{"ai_misc:Execute":true}}}`), &customOnly))
	token, err := workspace.ExecutionToken(security.NewRequestContext(context.Background(), &customOnly, slog.Default(), nil, nil), "ws-acct", "", time.Minute)
	require.NoError(t, err)
	ctx, err := authorizeWorkspaceRequest(workspaceTokenRequest(token), "ws-acct", nil, nil)
	require.NoError(t, err)

	toolCtx := core.NbToolContext{Ctx: ctx, AccountId: "ws-acct"}
	_, err = workspaceRelayTarget(toolCtx, tools.RelayJobKubectl, "", "kubectl get pods")
	require.NoError(t, err)
	_, err = workspaceRelayTarget(toolCtx, tools.RelayJobKubectl, "", "kubectl rollout restart deployment/api")
	require.NoError(t, err, "the grant that admits running Nubi also admits its writes, as for remediation")

	toolCtx.AccountId = "other-acct"
	_, err = workspaceRelayTarget(toolCtx, tools.RelayJobKubectl, "", "kubectl get pods")
	require.ErrorContains(t, err, "access denied", "the grant is scoped to ws-acct")
}

func TestExecutionTokenClaimsAreValidated(t *testing.T) {
	config.Config.LlmServerJwtSecret = "test-secret"
	security.SetTenantIdCacheForTest("ws-acct", "t1")
	stubTenantAccounts(t, "ws-acct")
	sign := func(claims workspace.WorkspaceTokenClaims) string {
		s, err := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString([]byte(config.Config.LlmServerJwtSecret))
		require.NoError(t, err)
		return s
	}
	live := jwt.RegisteredClaims{Audience: jwt.ClaimStrings{workspace.AudienceWorkspaceExecute}, ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Minute))}
	user := &security.WorkspaceGrants{UserId: "u1", Roles: []string{security.AUTH_TENANT_ADMIN_ROLE}, AccountIds: []string{"ws-acct"}}

	for name, claims := range map[string]workspace.WorkspaceTokenClaims{
		"super admin grants":                  {AccountId: "ws-acct", TenantId: "t1", Grants: &security.WorkspaceGrants{UserId: "u1", Roles: []string{security.AUTH_SUPER_ADMIN_FULL_ROLE}}, RegisteredClaims: live},
		"grants without the execute audience": {AccountId: "ws-acct", TenantId: "t1", Grants: user, RegisteredClaims: jwt.RegisteredClaims{ExpiresAt: live.ExpiresAt}},
		"execute audience without grants":     {AccountId: "ws-acct", TenantId: "t1", RegisteredClaims: live},
		"execute token without tenant":        {AccountId: "ws-acct", Grants: user, RegisteredClaims: live},
		"target outside the grants":           {AccountId: "ws-acct", TenantId: "t1", TargetAccountId: "not-granted", Grants: user, RegisteredClaims: live},
		"expired": {AccountId: "ws-acct", TenantId: "t1", Grants: user, RegisteredClaims: jwt.RegisteredClaims{
			Audience: live.Audience, ExpiresAt: jwt.NewNumericDate(time.Now().Add(-time.Second)),
		}},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := authorizeWorkspaceRequest(workspaceTokenRequest(sign(claims)), "ws-acct", nil, nil)
			require.Error(t, err)
		})
	}
}

func TestVirtualKubectlConfigUsesAuthorizedContexts(t *testing.T) {
	const workspaceAccount = "virtual-config-workspace"
	tools.StubTenantClusters(t, "virtual-config-tenant", []core.ToolConfig{
		{Name: "k8s-prod", Values: []core.ToolConfigValue{{Name: "id", Value: workspaceAccount}}},
		{Name: "k8s-dev", Values: []core.ToolConfigValue{{Name: "id", Value: "virtual-config-dev"}}},
		{Name: "k8s: dev #1", Values: []core.ToolConfigValue{{Name: "id", Value: "virtual-config-special"}}},
		{Name: "k8s-secret", Values: []core.ToolConfigValue{{Name: "id", Value: "virtual-config-secret"}}},
	})
	ctx := security.NewRequestContextForTenantAccountAdmin("virtual-config-tenant", "user", []string{workspaceAccount, "virtual-config-dev", "virtual-config-special"})

	request := WorkspaceExecuteRequest{
		AccountId:  workspaceAccount,
		ConfigName: "k8s-prod",
		Command:    "kubectl config get-contexts -o name",
	}
	result, handled, err := virtualKubectlConfig(ctx, request, "k8s-prod")
	require.NoError(t, err)
	require.True(t, handled)
	require.Equal(t, "k8s-dev\nk8s-prod\nk8s-dev-1\n", result)
	require.NotContains(t, result, "k8s-secret")

	request.Command = "kubectl config current-context"
	result, handled, err = virtualKubectlConfig(ctx, request, "k8s-prod")
	require.NoError(t, err)
	require.True(t, handled)
	require.Equal(t, "k8s-prod\n", result)

	request.Command = "kubectl config get-clusters"
	result, handled, err = virtualKubectlConfig(ctx, request, "k8s-prod")
	require.NoError(t, err)
	require.True(t, handled)
	require.Equal(t, "NAME\nk8s-dev\nk8s-prod\nk8s-dev-1\n", result)
	require.NotContains(t, result, "k8s-secret")

	request.Command = "kubectl config use-context k8s-dev"
	_, handled, err = virtualKubectlConfig(ctx, request, "k8s-prod")
	require.True(t, handled)
	require.ErrorContains(t, err, "read-only")

	request.Command = "kubectl config view"
	result, handled, err = virtualKubectlConfig(ctx, request, "k8s-prod")
	require.NoError(t, err)
	require.True(t, handled)
	parsed, err := common.DecodeYamlToMap(result)
	require.NoError(t, err, "context names must be safely serialized as YAML")
	require.Equal(t, "k8s-prod", parsed["current-context"])
	contexts := parsed["contexts"].([]any)
	require.Equal(t, "k8s-dev-1", contexts[2].(map[string]any)["name"])
}

func TestKubectlContextAliasesAreShellSafeAndResolvable(t *testing.T) {
	const workspaceAccount = "context-alias-workspace"
	tools.StubTenantClusters(t, "context-alias-tenant", []core.ToolConfig{
		{Name: "solar winds POC", Values: []core.ToolConfigValue{{Name: "id", Value: "solar-account"}}},
		{Name: "plain", Values: []core.ToolConfigValue{{Name: "id", Value: workspaceAccount}}},
	})
	ctx := security.NewRequestContextForTenantAccountAdmin("context-alias-tenant", "user", []string{workspaceAccount, "solar-account"})

	result, handled, err := virtualKubectlConfig(ctx, WorkspaceExecuteRequest{
		AccountId: workspaceAccount,
		Command:   "kubectl config get-contexts -o name",
	}, "plain")
	require.NoError(t, err)
	require.True(t, handled)
	require.Equal(t, "plain\nsolar-winds-POC\n", result)

	req := WorkspaceExecuteRequest{Command: "kubectl --context solar-winds-POC get pods"}
	require.NoError(t, normalizeKubectlProxyRequest(&req))
	require.Equal(t, "solar-winds-POC", req.TargetName)
	require.Equal(t, "solar winds POC", mustResolveKubectlContextName(t, ctx, workspaceAccount, req.TargetName))

	// Existing callers that quote the original integration name remain valid.
	require.Equal(t, "solar winds POC", mustResolveKubectlContextName(t, ctx, workspaceAccount, "solar winds POC"))
}

func TestKubectlContextAliasesDisambiguateSanitizedCollisions(t *testing.T) {
	contexts := []core.ToolConfig{
		{Name: "solar winds", Values: []core.ToolConfigValue{{Name: "id", Value: "account-a"}}},
		{Name: "solar-winds", Values: []core.ToolConfigValue{{Name: "id", Value: "account-b"}}},
	}
	aliases := tools.KubernetesTargetAliases(contexts)
	require.Len(t, aliases, 2)
	require.NotEqual(t, strings.ToLower(aliases[0].Name), strings.ToLower(aliases[1].Name))
	require.Regexp(t, `^solar-winds-[0-9a-f]{8}$`, aliases[0].Name)
	require.Regexp(t, `^solar-winds-[0-9a-f]{8}$`, aliases[1].Name)

	// Force a second-order collision with the first stable hashed alias. Its
	// fallback must retain that hash rather than reverting to an order-sensitive
	// suffix on the unhashed base.
	hashedAlias := aliases[0].Name
	aliases = tools.KubernetesTargetAliases([]core.ToolConfig{
		{Name: hashedAlias, Values: []core.ToolConfigValue{{Name: "id", Value: "account-c"}}},
		contexts[0],
		contexts[1],
	})
	require.Equal(t, hashedAlias, aliases[0].Name)
	require.Equal(t, hashedAlias+"-2", aliases[1].Name)
}

func mustResolveKubectlContextName(t *testing.T, ctx *security.RequestContext, accountID, requested string) string {
	t.Helper()
	target, err := resolveKubectlContext(ctx, accountID, requested)
	require.NoError(t, err)
	return target.Name
}

func TestKubectlConfigIndexOnlyMatchesSubcommand(t *testing.T) {
	require.Equal(t, 0, kubectlConfigIndex([]string{"config", "get-contexts"}))
	require.Equal(t, 2, kubectlConfigIndex([]string{"--request-timeout", "5s", "config", "view"}))
	require.Equal(t, 2, kubectlConfigIndex([]string{"-v", "5", "config", "view"}))
	require.Equal(t, 1, kubectlConfigIndex([]string{"--v=5", "config", "view"}))
	require.Equal(t, 2, kubectlConfigIndex([]string{"--vmodule", "loader=2", "config", "view"}))
	require.Equal(t, -1, kubectlConfigIndex([]string{"get", "config", "-n", "default"}))
}

func TestNormalizeKubectlProxyRequestExtractsConcreteContext(t *testing.T) {
	tests := []struct {
		name       string
		command    string
		wantCmd    string
		wantTarget string
		wantErr    string
	}{
		{name: "separate", command: "kubectl get pods --context prod -A", wantCmd: "kubectl 'get' 'pods' '-A'", wantTarget: "prod"},
		{name: "equals and quoting", command: "kubectl --context=dev logs 'api pod'", wantCmd: "kubectl 'logs' 'api pod'", wantTarget: "dev"},
		{name: "default", command: "kubectl get nodes", wantCmd: "kubectl 'get' 'nodes'"},
		{name: "jsonpath braces", command: "kubectl get namespace kube-system -o 'jsonpath={.metadata.uid}'", wantCmd: "kubectl 'get' 'namespace' 'kube-system' '-o' 'jsonpath={.metadata.uid}'"},
		{name: "single quote", command: `kubectl get pods -l 'owner=o'\''reilly'`, wantCmd: "kubectl 'get' 'pods' '-l' 'owner=o'\\''reilly'"},
		{name: "missing", command: "kubectl get pods --context", wantErr: "requires a value"},
		{name: "conflict", command: "kubectl --context=a get pods --context b", wantErr: "more than one context"},
		{name: "wrong executable", command: "helm get pods", wantErr: "must start with kubectl"},
		{name: "invalid quoting", command: "kubectl get 'pods", wantErr: "unable to parse"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			req := WorkspaceExecuteRequest{Command: tc.command}
			err := normalizeKubectlProxyRequest(&req)
			if tc.wantErr != "" {
				require.ErrorContains(t, err, tc.wantErr)
				return
			}
			require.NoError(t, err)
			require.Equal(t, tc.wantCmd, req.Command)
			require.Equal(t, tc.wantTarget, req.TargetName)
		})
	}

}

func TestNormalizeHelmProxyRequestExtractsConcreteContext(t *testing.T) {
	for _, tc := range []struct {
		command    string
		wantCmd    string
		wantTarget string
		wantErr    string
	}{
		{command: "helm list -A", wantCmd: "helm 'list' '-A'"},
		{command: "helm --kube-context dev list -A", wantCmd: "helm 'list' '-A'", wantTarget: "dev"},
		{command: "helm list --kube-context=prod", wantCmd: "helm 'list'", wantTarget: "prod"},
		{command: "helm list --kube-context", wantErr: "requires a value"},
		{command: "helm --kube-context -n default list", wantErr: "requires a value"},
		{command: "helm list --kube-context=-n", wantErr: "requires a value"},
		{command: "helm list --kube-context --kube-apiserver=https://other", wantErr: "requires a value"},
		{command: "helm --kube-context=dev list --kube-context prod", wantErr: "more than one cluster"},
		{command: "helm list --kube-apiserver=https://other", wantErr: "not allowed"},
		{command: "helm list --kubeconfig /tmp/config", wantErr: "not allowed"},
		{command: "kubectl get pods", wantErr: "must start with helm"},
	} {
		req := WorkspaceExecuteRequest{Command: tc.command}
		err := normalizeHelmProxyRequest(&req)
		if tc.wantErr != "" {
			require.ErrorContains(t, err, tc.wantErr)
			continue
		}
		require.NoError(t, err)
		require.Equal(t, tc.wantCmd, req.Command)
		require.Equal(t, tc.wantTarget, req.TargetName)
	}
}

func TestRequireExecutionToken(t *testing.T) {
	orig := config.Config.LlmServerWorkspaceExecuteRequireScopedToken
	t.Cleanup(func() { config.Config.LlmServerWorkspaceExecuteRequireScopedToken = orig })
	ctx := security.NewRequestContextForSuperAdmin()
	request := func(audience string) (*gin.Context, *httptest.ResponseRecorder) {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest(http.MethodPost, "/api/v1/workspace/execute", nil)
		if audience != "" {
			c.Set(workspaceTokenAudienceKey, audience)
		}
		return c, w
	}

	config.Config.LlmServerWorkspaceExecuteRequireScopedToken = false
	c, _ := request(workspace.AudienceWorkspacePod)
	assert.True(t, requireExecutionToken(c, ctx, "ws-acct"), "pod token is still accepted while the flag is off")

	config.Config.LlmServerWorkspaceExecuteRequireScopedToken = true
	c, w := request(workspace.AudienceWorkspacePod)
	assert.False(t, requireExecutionToken(c, ctx, "ws-acct"))
	assert.Equal(t, http.StatusUnauthorized, w.Code)
	c, _ = request(workspace.AudienceWorkspaceExecute)
	assert.True(t, requireExecutionToken(c, ctx, "ws-acct"))
	c, _ = request("")
	assert.True(t, requireExecutionToken(c, ctx, "ws-acct"), "gateway-authenticated calls carry no workspace token")
}

// The shim prints `result` and exits 1 only when `error` is set; a non-200 makes
// it print the raw body instead of the command output. A non-zero exit from the
// relay must therefore stay 200 with both fields.
func TestWorkspaceExecuteReply(t *testing.T) {
	notFound := "Error from server (NotFound): pods \"gone\" not found\n"

	status, body := workspaceExecuteReply(notFound, &tools.RelayCommandExitError{ExitCode: 1})
	assert.Equal(t, http.StatusOK, status, "non-zero exit must stay 200 so the shim prints the output")
	assert.Equal(t, notFound, body["result"])
	assert.Equal(t, "command exited with status 1", body["error"])

	status, body = workspaceExecuteReply("pod/app-1 Running\n", nil)
	assert.Equal(t, http.StatusOK, status)
	assert.NotContains(t, body, "error")

	status, body = workspaceExecuteReply(nil, errors.New("unable to access relay server"))
	assert.Equal(t, http.StatusInternalServerError, status)
	assert.Equal(t, "unable to access relay server", body["error"])

	// A request the cluster agent refused fails the command like a non-zero
	// exit, with the agent's reason as the printed output.
	refusal := `kubectl: verb "patch" not in read-only allowlist`
	status, body = workspaceExecuteReply(refusal, &tools.RelayCommandRefusedError{StatusCode: 500, Message: refusal})
	assert.Equal(t, http.StatusOK, status, "a refusal must stay 200 so the shim prints the agent's reason")
	assert.Equal(t, refusal, body["result"])
	assert.Contains(t, body["error"], refusal)
}

// The workspace's per-account config list only holds its own cluster, so a
// per-call target on another cluster must be found by name across the tenant.
func TestWorkspaceRelayTargetResolvesOtherTenantCluster(t *testing.T) {
	const workspaceAccount = "relay-target-workspace"
	clusters := []core.ToolConfig{
		{Name: "k8s-prod", Values: []core.ToolConfigValue{{Name: "id", Value: workspaceAccount}}},
		{Name: "k8s-dev", Values: []core.ToolConfigValue{{Name: "id", Value: "relay-target-dev"}}},
	}
	tools.StubTenantClusters(t, "relay-tenant", clusters)

	ctx := core.NbToolContext{
		Ctx:       security.NewRequestContextForTenantAccountAdmin("relay-tenant", "user", []string{workspaceAccount, "relay-target-dev"}),
		AccountId: workspaceAccount,
		// What NewNbToolContext yields when the requested name is not in the
		// workspace account's narrowed list.
		ToolConfig: core.ToolConfig{},
	}
	target, err := workspaceRelayTarget(ctx, tools.RelayJobKubectl, "k8s-dev", "kubectl get pods")
	require.NoError(t, err)
	require.Equal(t, "relay-target-dev", target)

	_, err = workspaceRelayTarget(ctx, tools.RelayJobKubectl, "k8s-unknown", "kubectl get pods")
	require.ErrorContains(t, err, "unavailable")

	ctx.Ctx = security.NewRequestContextForTenantAccountAdmin("relay-tenant", "user", []string{workspaceAccount})
	_, err = workspaceRelayTarget(ctx, tools.RelayJobKubectl, "k8s-dev", "kubectl get pods")
	require.ErrorContains(t, err, "unavailable", "a cluster the caller cannot read is not resolvable")
}

func TestWorkspaceProxyResolvesTwoConcreteContextsIndependently(t *testing.T) {
	const workspaceAccount = "relay-multi-context-workspace"
	tools.StubTenantClusters(t, "relay-multi-context-tenant", []core.ToolConfig{
		{Name: "k8s-dev", Values: []core.ToolConfigValue{{Name: "id", Value: "relay-multi-dev"}}},
		{Name: "k8s-prod", Values: []core.ToolConfigValue{{Name: "id", Value: "relay-multi-prod"}}},
		{Name: "solar winds POC", Values: []core.ToolConfigValue{{Name: "id", Value: "relay-multi-solar"}}},
	})
	toolCtx := core.NbToolContext{
		Ctx:       security.NewRequestContextForTenantAccountAdmin("relay-multi-context-tenant", "user", []string{workspaceAccount, "relay-multi-dev", "relay-multi-prod", "relay-multi-solar"}),
		AccountId: workspaceAccount,
	}

	for _, tc := range []struct {
		context string
		account string
	}{
		{context: "k8s-dev", account: "relay-multi-dev"},
		{context: "k8s-prod", account: "relay-multi-prod"},
		{context: "solar-winds-POC", account: "relay-multi-solar"},
	} {
		req := WorkspaceExecuteRequest{Command: "kubectl --context " + common.ShellEscape(tc.context) + " get pods"}
		require.NoError(t, normalizeKubectlProxyRequest(&req))
		require.Equal(t, "kubectl 'get' 'pods'", req.Command)
		target, err := workspaceRelayTarget(toolCtx, tools.RelayJobKubectl, req.TargetName, req.Command)
		require.NoError(t, err)
		require.Equal(t, tc.account, target)
	}
}

func TestWorkspaceExecuteAuditUsesResolvedTargetAndOutcome(t *testing.T) {
	ctx := security.NewRequestContextForTenantAccountAdmin("audit-tenant", "audit-user", []string{"workspace", "target"})
	req := WorkspaceExecuteRequest{AccountId: "workspace", Tool: "kubectl", Command: "kubectl get pods", TargetName: "k8s-dev"}

	success := workspaceExecuteAudit(ctx, req, "kubectl", "target", true).Audits[0]
	require.Equal(t, "target", success.AccountId)
	require.Equal(t, "audit-tenant", success.TenantId)
	require.Equal(t, "audit-user", success.UserId)
	require.Equal(t, audit.EventStatusSuccess, success.EventStatus)
	require.Equal(t, "workspace", success.EventAttr["workspace_account_id"])
	require.Equal(t, "k8s-dev", success.EventAttr["target_context"])
	require.Equal(t, "kubectl get pods", success.EventAttr["command"])

	failure := workspaceExecuteAudit(ctx, req, "kubectl", "target", false).Audits[0]
	require.Equal(t, audit.EventStatusFailure, failure.EventStatus)
}

// A database, cache or broker command bound to another account uses that
// account's integration of the same name and is relayed there; anything else
// keeps the workspace's own account.
func TestWorkspaceBoundIntegration(t *testing.T) {
	restore := integrationConfigByName
	t.Cleanup(func() { integrationConfigByName = restore })
	integrationConfigByName = func(_ *security.RequestContext, account string, _ core.NBTool, name string) (core.ToolConfig, error) {
		if account == "prod-id" && name == "prod-pg" {
			return core.ToolConfig{Id: "pg-2", Name: "prod-pg"}, nil
		}
		return core.ToolConfig{}, errors.New("2 integrations named orders-db are connected to the selected account; none was used")
	}
	workspaceCtx := core.NbToolContext{AccountId: "ws-id", TargetAccountId: "ws-id", ToolConfig: core.ToolConfig{Id: "pg-1", Name: "prod-pg"}}

	got, err := workspaceBoundIntegration(workspaceCtx, tools.PostgresExecuteTool{}, tools.RelayJobPostgres, "prod-id", "prod-pg")
	require.NoError(t, err)
	assert.Equal(t, "prod-id", got.AccountId, "relayed to the target account")
	assert.Equal(t, "pg-2", got.ToolConfig.Id, "the target's prod-pg, not the workspace account's integration of the same name")
	target, err := workspaceRelayTarget(got, tools.RelayJobPostgres, "prod-pg", "select 1")
	require.NoError(t, err)
	assert.Equal(t, "prod-id", target)

	_, err = workspaceBoundIntegration(workspaceCtx, tools.PostgresExecuteTool{}, tools.RelayJobPostgres, "prod-id", "orders-db")
	assert.ErrorContains(t, err, "none was used")

	for name, tc := range map[string]struct {
		job    tools.RelayJob
		target string
	}{
		"own account":        {tools.RelayJobPostgres, "ws-id"},
		"not bound":          {tools.RelayJobPostgres, ""},
		"not an integration": {tools.RelayJobKubectl, "prod-id"},
	} {
		got, err := workspaceBoundIntegration(workspaceCtx, tools.PostgresExecuteTool{}, tc.job, tc.target, "prod-pg")
		require.NoError(t, err, name)
		assert.Equal(t, workspaceCtx, got, name)
	}
}

// The SQL and ArgoCD jobs relay to another account's integration like Postgres.
func TestWorkspaceBoundIntegration_PerCallJobs(t *testing.T) {
	restore := integrationConfigByName
	t.Cleanup(func() { integrationConfigByName = restore })
	integrationConfigByName = func(_ *security.RequestContext, account string, _ core.NBTool, name string) (core.ToolConfig, error) {
		return core.ToolConfig{Id: account + "/" + name, Name: name}, nil
	}
	workspaceCtx := core.NbToolContext{AccountId: "ws-id", TargetAccountId: "ws-id", ToolConfig: core.ToolConfig{Id: "own", Name: "db-2"}}
	for job, tool := range map[tools.RelayJob]core.NBTool{
		tools.RelayJobMysql:      tools.MySQLExecuteTool{},
		tools.RelayJobMssql:      tools.MSSQLExecuteTool{},
		tools.RelayJobOracle:     tools.OracleExecuteTool{},
		tools.RelayJobClickhouse: tools.ClickhouseExecuteTool{},
		tools.RelayJobArgoCD:     tools.ArgoCDExecuteTool{},
	} {
		got, err := workspaceBoundIntegration(workspaceCtx, tool, job, "prod-id", "db-2")
		require.NoError(t, err, job)
		assert.Equal(t, "prod-id", got.AccountId, job)
		assert.Equal(t, "prod-id/db-2", got.ToolConfig.Id, "%s: the target's integration of that name", job)
	}
}
