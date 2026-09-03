package workspace

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"nudgebee/llm/config"
	"nudgebee/llm/security"

	"github.com/golang-jwt/jwt/v5"
	"github.com/stretchr/testify/require"
)

func withDockerConfig(t *testing.T, serverURL string) {
	t.Helper()
	originalRuntime := config.Config.LlmServerWorkspaceRuntime
	originalHost := config.Config.LlmServerWorkspaceDockerHost
	originalNetwork := config.Config.LlmServerWorkspaceDockerNetwork
	originalImage := config.Config.LlmServerCodeAgentImage
	originalPort := config.Config.LlmServerWorkspacePort
	originalJWTSecret := config.Config.LlmServerJwtSecret
	config.Config.LlmServerWorkspaceRuntime = "docker"
	config.Config.LlmServerWorkspaceDockerHost = serverURL
	config.Config.LlmServerWorkspaceDockerNetwork = "test-workspace"
	config.Config.LlmServerCodeAgentImage = "example.test/code-agent:v1"
	config.Config.LlmServerWorkspacePort = 8080
	config.Config.LlmServerJwtSecret = "docker-runtime-test-secret"
	t.Cleanup(func() {
		config.Config.LlmServerWorkspaceRuntime = originalRuntime
		config.Config.LlmServerWorkspaceDockerHost = originalHost
		config.Config.LlmServerWorkspaceDockerNetwork = originalNetwork
		config.Config.LlmServerCodeAgentImage = originalImage
		config.Config.LlmServerWorkspacePort = originalPort
		config.Config.LlmServerJwtSecret = originalJWTSecret
	})
}

// TestDockerRuntimeLiveLifecycle exercises the real Docker Engine and the
// Compose network. It is opt-in because it creates and removes a container and
// requires the code-analysis image plus the Compose llm-server to be running.
func TestDockerRuntimeLiveLifecycle(t *testing.T) {
	if os.Getenv("NUDGEBEE_DOCKER_WORKSPACE_LIVE_TEST") != "1" {
		t.Skip("set NUDGEBEE_DOCKER_WORKSPACE_LIVE_TEST=1 to run")
	}

	withDockerConfig(t, "unix:///var/run/docker.sock")
	config.Config.LlmServerWorkspaceDockerNetwork = "nudgebee-workspace"
	config.Config.LlmServerCodeAgentImage = "ghcr.io/nudgebee/code-analysis-agent:latest"
	config.Config.LlmServerJwtSecret = "docker-live-smoke-secret"
	accountID := "docker-live-smoke"
	runtime, err := newDockerRuntime()
	require.NoError(t, err)
	requestContext := security.NewRequestContextForSuperAdmin()
	_ = runtime.delete(context.Background(), accountID)
	require.NoError(t, runtime.create(requestContext, accountID))
	t.Cleanup(func() { _ = runtime.delete(context.Background(), accountID) })

	container, err := runtime.inspect(context.Background(), accountID)
	require.NoError(t, err)
	require.True(t, container.State.Running)
	name := dockerWorkspaceName(accountID)
	require.Equal(t, "1000:3000", dockerInspectField(t, dockerWorkspaceName(accountID), "{{.Config.User}}"))
	require.Equal(t, "nudgebee-workspace", dockerInspectField(t, dockerWorkspaceName(accountID), "{{.HostConfig.NetworkMode}}"))
	require.Equal(t, "/usr/bin/curl", dockerExecOutput(t, name, "/bin/sh", "-c", "command -v curl"))

	token := dockerEnvValue(container.Config.Env, ENV_NB_WORKSPACE_TOKEN)
	require.NotEmpty(t, token)
	require.Eventually(t, func() bool {
		return exec.Command("docker", "exec", name, "curl", "-fsS", "http://127.0.0.1:8080/health").Run() == nil
	}, 30*time.Second, 500*time.Millisecond)

	payload := `{"command":"printf docker-workspace-live-ok","conversation_id":"live-smoke","env":{}}`
	output, err := exec.Command("docker", "exec", name, "curl", "-fsS", "-X", "POST",
		"http://127.0.0.1:8080/execute", "-H", "Content-Type: application/json",
		"-H", "X-Workspace-Token: "+token, "-d", payload).CombinedOutput()
	require.NoError(t, err, string(output))
	require.Contains(t, string(output), "docker-workspace-live-ok")

	// Prove the real Compose llm-server can resolve and reach the dynamic
	// container over the dedicated workspace network.
	output, err = exec.Command("docker", "exec", "nudgebee-llm-server-1", "wget", "-q", "-O", "-", "http://"+name+":8080/health").CombinedOutput()
	require.NoError(t, err, string(output))
}

func dockerExecOutput(t *testing.T, containerName string, args ...string) string {
	t.Helper()
	output, err := exec.Command("docker", append([]string{"exec", containerName}, args...)...).CombinedOutput()
	require.NoError(t, err, string(output))
	return strings.TrimSpace(string(output))
}

func dockerInspectField(t *testing.T, containerName, format string) string {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for {
		output, err := exec.Command("docker", "inspect", "--format", format, containerName).CombinedOutput()
		if err == nil {
			return strings.TrimSpace(string(output))
		}
		if time.Now().After(deadline) {
			t.Fatalf("docker inspect %s failed: %v: %s", containerName, err, output)
		}
		time.Sleep(500 * time.Millisecond)
	}
}

func TestDockerWorkspaceNameIsDeterministicAndSafe(t *testing.T) {
	name := dockerWorkspaceName("ACCOUNT/With Unsafe Characters")
	require.Regexp(t, `^nb-workspace-[a-f0-9]{16}$`, name)
	require.Equal(t, name, dockerWorkspaceName("ACCOUNT/With Unsafe Characters"))
	require.NotEqual(t, name, dockerWorkspaceName("another-account"))
}

func TestDockerRuntimeEndpoint(t *testing.T) {
	accountID := "account-1"
	withDockerConfig(t, "unused")
	token, err := signWorkspaceToken(accountID, "")
	require.NoError(t, err)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/containers/"+dockerWorkspaceName(accountID)+"/json", r.URL.Path)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"Id": "container-id",
			"Config": map[string]any{
				"Image":  "example.test/code-agent:v1",
				"Env":    []string{ENV_NB_WORKSPACE_TOKEN + "=" + token},
				"Labels": map[string]string{dockerManagedLabel: "true", dockerAccountLabel: accountID},
			},
			"State": map[string]any{"Running": true, "Status": "running"},
		})
	}))
	defer server.Close()

	runtime, err := newDockerRuntimeForHost(server.URL)
	require.NoError(t, err)
	endpoint, token, err := runtime.endpoint(context.Background(), accountID)
	require.NoError(t, err)
	require.Equal(t, "http://"+dockerWorkspaceName(accountID)+":8080", endpoint)
	require.NotEmpty(t, token)
}

func TestDockerMissingContainerIsRecoverableForLazyCreation(t *testing.T) {
	server := httptest.NewServer(http.NotFoundHandler())
	defer server.Close()
	withDockerConfig(t, server.URL)

	runtime, err := newDockerRuntimeForHost(server.URL)
	require.NoError(t, err)
	_, err = runtime.inspect(context.Background(), "missing-account")
	require.ErrorIs(t, err, errDockerWorkspaceNotFound)

	manager := NewWorkspaceManager().(*workspaceManager)
	require.True(t, manager.isRecoverableError(err))
}

func TestDockerRuntimeCreateUsesConstrainedSpecification(t *testing.T) {
	accountID := "account-2"
	var createBody map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/images/"):
			_ = json.NewEncoder(w).Encode(map[string]any{"Config": map[string]any{"Env": []string{"PATH=/usr/bin:/bin", "IMAGE_DEFAULT=preserved"}}})
		case r.Method == http.MethodGet:
			http.NotFound(w, r)
		case r.Method == http.MethodPost && r.URL.Path == "/containers/create":
			require.NoError(t, json.NewDecoder(r.Body).Decode(&createBody))
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{"Id":"created"}`))
		case r.Method == http.MethodPost && r.URL.Path == "/containers/"+dockerWorkspaceName(accountID)+"/start":
			w.WriteHeader(http.StatusNoContent)
		default:
			http.Error(w, "unexpected request", http.StatusBadRequest)
		}
	}))
	defer server.Close()
	withDockerConfig(t, server.URL)

	runtime, err := newDockerRuntimeForHost(server.URL)
	require.NoError(t, err)
	// The nil request context is sufficient here because token creation only
	// consults it for an optional tenant security context.
	requestContext := security.NewRequestContextForSuperAdmin()
	require.NoError(t, runtime.create(requestContext, accountID))

	require.Equal(t, "example.test/code-agent:v1", createBody["Image"])
	require.Nil(t, createBody["Binds"])
	hostConfig := createBody["HostConfig"].(map[string]any)
	require.Equal(t, "test-workspace", hostConfig["NetworkMode"])
	require.ElementsMatch(t, []any{"ALL"}, hostConfig["CapDrop"])
	require.ElementsMatch(t, []any{"no-new-privileges:true"}, hostConfig["SecurityOpt"])
	require.Contains(t, createBody["Env"], "PATH=/usr/bin:/bin")
	require.Contains(t, createBody["Env"], "IMAGE_DEFAULT=preserved")
}

func TestDockerRuntimeCreateDoesNotMaskInspectionFailure(t *testing.T) {
	var createCalled bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && r.URL.Path == "/containers/create" {
			createCalled = true
		}
		http.Error(w, "engine unavailable", http.StatusInternalServerError)
	}))
	defer server.Close()
	withDockerConfig(t, server.URL)

	runtime, err := newDockerRuntimeForHost(server.URL)
	require.NoError(t, err)
	err = runtime.create(security.NewRequestContextForSuperAdmin(), "account-3")
	require.ErrorContains(t, err, "Docker returned 500")
	require.False(t, createCalled)
}

func TestMergeDockerEnvPreservesImageDefaultsAndOverridesByName(t *testing.T) {
	merged := mergeDockerEnv(
		[]string{"PATH=/image/bin", "IMAGE_DEFAULT=preserved"},
		[]string{"PATH=/workspace/bin", "NB_ACCOUNT_ID=account-1"},
	)
	require.Equal(t, []string{"PATH=/workspace/bin", "IMAGE_DEFAULT=preserved", "NB_ACCOUNT_ID=account-1"}, merged)
}

func TestDockerRuntimeRejectsRemoteEngine(t *testing.T) {
	withDockerConfig(t, "tcp://docker.example.test:2375")
	_, err := newDockerRuntime()
	require.ErrorContains(t, err, "must use a local unix:// socket")
}

func TestDockerRuntimeEndpointRejectsUnmanagedContainer(t *testing.T) {
	accountID := "account-unmanaged"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"Id": "container-id",
			"Config": map[string]any{
				"Env":    []string{ENV_NB_WORKSPACE_TOKEN + "=signed-token"},
				"Labels": map[string]string{},
			},
			"State": map[string]any{"Running": true, "Status": "running"},
		})
	}))
	defer server.Close()
	runtime, err := newDockerRuntimeForHost(server.URL)
	require.NoError(t, err)

	_, _, err = runtime.endpoint(context.Background(), accountID)
	require.ErrorContains(t, err, "refusing to use unmanaged Docker container")
}

func TestDockerWorkspaceTokenReusable(t *testing.T) {
	withDockerConfig(t, "unused")
	now := time.Now()
	valid, err := jwt.NewWithClaims(jwt.SigningMethodHS256, WorkspaceTokenClaims{
		RegisteredClaims: jwt.RegisteredClaims{ExpiresAt: jwt.NewNumericDate(now.Add(time.Hour))},
	}).SignedString([]byte(config.Config.LlmServerJwtSecret))
	require.NoError(t, err)
	expiring, err := jwt.NewWithClaims(jwt.SigningMethodHS256, WorkspaceTokenClaims{
		RegisteredClaims: jwt.RegisteredClaims{ExpiresAt: jwt.NewNumericDate(now.Add(5 * time.Minute))},
	}).SignedString([]byte(config.Config.LlmServerJwtSecret))
	require.NoError(t, err)

	require.True(t, dockerWorkspaceTokenReusable(valid, now))
	require.False(t, dockerWorkspaceTokenReusable(expiring, now))
	require.False(t, dockerWorkspaceTokenReusable("not-a-jwt", now))
}

func TestDockerRuntimeExpiredTokenTriggersLazyRecreation(t *testing.T) {
	accountID := "account-expired"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"Config": map[string]any{
				"Env": []string{ENV_NB_WORKSPACE_TOKEN + "=expired"},
				"Labels": map[string]string{
					dockerManagedLabel: "true", dockerAccountLabel: accountID,
				},
			},
			"State": map[string]any{"Running": true, "Status": "running"},
		})
	}))
	defer server.Close()
	runtime, err := newDockerRuntimeForHost(server.URL)
	require.NoError(t, err)

	_, _, err = runtime.endpoint(context.Background(), accountID)
	require.ErrorIs(t, err, errDockerWorkspaceTokenInvalid)
	require.True(t, NewWorkspaceManager().(*workspaceManager).isRecoverableError(err))
}
