package workspace

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
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	"nudgebee/llm/common"
	"nudgebee/llm/config"
	"nudgebee/llm/security"

	"github.com/golang-jwt/jwt/v5"
	"k8s.io/apimachinery/pkg/api/resource"
)

const (
	dockerManagedLabel      = "com.nudgebee.workspace"
	dockerAccountLabel      = "com.nudgebee.workspace.account-id"
	dockerTenantLabel       = "com.nudgebee.workspace.tenant-id"
	dockerImageLabel        = "com.nudgebee.workspace.image"
	dockerResponseBodyLimit = 1024 * 1024
	dockerCommandBodyLimit  = 2 * 1024 * 1024
	dockerAPIBodyHardLimit  = 64 * 1024 * 1024
)

var (
	errDockerWorkspaceNotFound     = errors.New("docker workspace container not found")
	errDockerWorkspaceTokenInvalid = errors.New("docker workspace token is invalid or expiring")
)

var dockerRuntimeCache = struct {
	sync.Mutex
	byHost map[string]*dockerRuntime
}{byHost: make(map[string]*dockerRuntime)}

type dockerRuntime struct {
	client     *http.Client
	pullClient *http.Client
	baseURL    string
}

type dockerContainer struct {
	ID     string `json:"Id"`
	Name   string `json:"Name"`
	Config struct {
		Image  string            `json:"Image"`
		Env    []string          `json:"Env"`
		Labels map[string]string `json:"Labels"`
	} `json:"Config"`
	State struct {
		Running bool   `json:"Running"`
		Status  string `json:"Status"`
		Health  *struct {
			Status string `json:"Status"`
		} `json:"Health"`
	} `json:"State"`
}

func dockerMode() bool {
	return strings.EqualFold(strings.TrimSpace(config.Config.LlmServerWorkspaceRuntime), "docker")
}

func newDockerRuntime() (*dockerRuntime, error) {
	host := strings.TrimSpace(config.Config.LlmServerWorkspaceDockerHost)
	if host == "" {
		host = "unix:///var/run/docker.sock"
	}
	if !strings.HasPrefix(host, "unix://") {
		return nil, fmt.Errorf("workspace docker host %q must use a local unix:// socket; remote Engines cannot route workspace traffic", host)
	}
	dockerRuntimeCache.Lock()
	defer dockerRuntimeCache.Unlock()
	if runtime := dockerRuntimeCache.byHost[host]; runtime != nil {
		return runtime, nil
	}
	runtime, err := newDockerRuntimeForHost(host)
	if err != nil {
		return nil, err
	}
	dockerRuntimeCache.byHost[host] = runtime
	return runtime, nil
}

func newDockerRuntimeForHost(host string) (*dockerRuntime, error) {
	var transport *http.Transport
	if defaultTransport, ok := http.DefaultTransport.(*http.Transport); ok {
		transport = defaultTransport.Clone()
	} else {
		transport = &http.Transport{
			Proxy: http.ProxyFromEnvironment,
			DialContext: (&net.Dialer{
				Timeout:   30 * time.Second,
				KeepAlive: 30 * time.Second,
			}).DialContext,
			ForceAttemptHTTP2:     true,
			MaxIdleConns:          100,
			IdleConnTimeout:       90 * time.Second,
			TLSHandshakeTimeout:   10 * time.Second,
			ExpectContinueTimeout: 1 * time.Second,
		}
	}
	baseURL := "http://docker"
	switch {
	case strings.HasPrefix(host, "unix://"):
		socket := strings.TrimPrefix(host, "unix://")
		transport.DialContext = func(ctx context.Context, _, _ string) (net.Conn, error) {
			return (&net.Dialer{Timeout: 5 * time.Second}).DialContext(ctx, "unix", socket)
		}
	case strings.HasPrefix(host, "tcp://"):
		baseURL = "http://" + strings.TrimPrefix(host, "tcp://")
	case strings.HasPrefix(host, "http://") || strings.HasPrefix(host, "https://"):
		baseURL = strings.TrimRight(host, "/")
	default:
		return nil, fmt.Errorf("workspace docker host %q must use unix://, tcp://, http://, or https://", host)
	}

	return &dockerRuntime{
		client:     &http.Client{Transport: transport, Timeout: 60 * time.Second},
		pullClient: &http.Client{Transport: transport, Timeout: 10 * time.Minute},
		baseURL:    baseURL,
	}, nil
}

func (d *dockerRuntime) start(ctx context.Context, accountID string) error {
	name := dockerWorkspaceName(accountID)
	_, status, err := d.request(ctx, http.MethodPost, "/containers/"+url.PathEscape(name)+"/start", nil)
	if err != nil {
		return err
	}
	if status == http.StatusNoContent || status == http.StatusNotModified {
		return nil
	}
	return fmt.Errorf("start workspace container %s: Docker returned %d", name, status)
}

func dockerWorkspaceName(accountID string) string {
	sum := sha256.Sum256([]byte(accountID))
	return "nb-workspace-" + hex.EncodeToString(sum[:8])
}

func (d *dockerRuntime) request(ctx context.Context, method, path string, body any) ([]byte, int, error) {
	return d.requestWithClient(ctx, d.client, method, path, body)
}

func (d *dockerRuntime) requestWithClient(ctx context.Context, client *http.Client, method, path string, body any) ([]byte, int, error) {
	var reader io.Reader
	if body != nil {
		payload, err := json.Marshal(body)
		if err != nil {
			return nil, 0, fmt.Errorf("marshal Docker request: %w", err)
		}
		reader = bytes.NewReader(payload)
	}
	req, err := http.NewRequestWithContext(ctx, method, d.baseURL+path, reader)
	if err != nil {
		return nil, 0, fmt.Errorf("build Docker request: %w", err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, 0, fmt.Errorf("call Docker Engine: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	data, err := io.ReadAll(io.LimitReader(resp.Body, dockerResponseBodyLimit+1))
	if err != nil {
		return nil, resp.StatusCode, fmt.Errorf("read Docker response: %w", err)
	}
	if len(data) > dockerResponseBodyLimit {
		return nil, resp.StatusCode, fmt.Errorf("docker response exceeds %d bytes", dockerResponseBodyLimit)
	}
	return data, resp.StatusCode, nil
}

func (d *dockerRuntime) inspect(ctx context.Context, accountID string) (*dockerContainer, error) {
	name := dockerWorkspaceName(accountID)
	data, status, err := d.request(ctx, http.MethodGet, "/containers/"+url.PathEscape(name)+"/json", nil)
	if err != nil {
		return nil, err
	}
	if status == http.StatusNotFound {
		return nil, fmt.Errorf("%w: %s", errDockerWorkspaceNotFound, name)
	}
	if status < 200 || status >= 300 {
		return nil, fmt.Errorf("inspect workspace container %s: Docker returned %d: %s", name, status, strings.TrimSpace(string(data)))
	}
	var container dockerContainer
	if err := json.Unmarshal(data, &container); err != nil {
		return nil, fmt.Errorf("decode workspace container inspection: %w", err)
	}
	return &container, nil
}

func dockerEnvValue(env []string, key string) string {
	prefix := key + "="
	for _, value := range env {
		if strings.HasPrefix(value, prefix) {
			return strings.TrimPrefix(value, prefix)
		}
	}
	return ""
}

func validateDockerWorkspaceOwnership(container *dockerContainer, accountID string) error {
	if container.Config.Labels[dockerManagedLabel] != "true" || container.Config.Labels[dockerAccountLabel] != accountID {
		return fmt.Errorf("refusing to use unmanaged Docker container %s", dockerWorkspaceName(accountID))
	}
	return nil
}

func readDockerWorkspaceBody(body io.Reader, limit int64) ([]byte, error) {
	data, err := io.ReadAll(io.LimitReader(body, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, fmt.Errorf("docker workspace response exceeds %d bytes", limit)
	}
	return data, nil
}

func dockerWorkspaceAPIBodyLimit() int64 {
	limit := int64(config.Config.LlmServerWorkspaceFileMaxDownloadBytes)
	if limit <= 0 {
		limit = 5 * 1024 * 1024
	}
	limit += dockerResponseBodyLimit // JSON envelope and non-file endpoint headroom.
	if limit > dockerAPIBodyHardLimit {
		return dockerAPIBodyHardLimit
	}
	return limit
}

func dockerWorkspaceTokenReusable(token string, now time.Time) bool {
	claims := &WorkspaceTokenClaims{}
	parsed, err := jwt.ParseWithClaims(token, claims, func(token *jwt.Token) (any, error) {
		if token.Method == nil || token.Method.Alg() != jwt.SigningMethodHS256.Alg() {
			return nil, fmt.Errorf("unexpected signing method")
		}
		return []byte(config.Config.LlmServerJwtSecret), nil
	})
	if err != nil || parsed == nil || !parsed.Valid || claims.ExpiresAt == nil {
		return false
	}
	return claims.ExpiresAt.After(now.Add(10 * time.Minute))
}

func (d *dockerRuntime) endpoint(ctx context.Context, accountID string) (string, string, error) {
	container, err := d.inspect(ctx, accountID)
	if err != nil {
		return "", "", err
	}
	if err := validateDockerWorkspaceOwnership(container, accountID); err != nil {
		return "", "", err
	}
	token := dockerEnvValue(container.Config.Env, ENV_NB_WORKSPACE_TOKEN)
	if !dockerWorkspaceTokenReusable(token, time.Now()) {
		return "", "", fmt.Errorf("%w: %s", errDockerWorkspaceTokenInvalid, dockerWorkspaceName(accountID))
	}
	if !container.State.Running {
		return "", "", fmt.Errorf("workspace container is not ready: state=%s", container.State.Status)
	}
	if container.State.Health != nil && container.State.Health.Status == "unhealthy" {
		return "", "", fmt.Errorf("workspace container is not ready: health=unhealthy")
	}
	return fmt.Sprintf("http://%s:%d", dockerWorkspaceName(accountID), config.Config.LlmServerWorkspacePort), token, nil
}

func dockerAPIRequest(ctx context.Context, client *http.Client, accountID, method, endpoint string, queryParams map[string]string, body []byte) (*http.Response, error) {
	if client == nil {
		client = common.HttpClient()
	}
	runtime, err := newDockerRuntime()
	if err != nil {
		return nil, err
	}
	baseURL, token, err := runtime.endpoint(ctx, accountID)
	if err != nil {
		return nil, err
	}
	u, err := url.Parse(strings.TrimRight(baseURL, "/") + endpoint)
	if err != nil {
		return nil, fmt.Errorf("build Docker workspace URL: %w", err)
	}
	query := u.Query()
	for key, value := range queryParams {
		query.Set(key, value)
	}
	u.RawQuery = query.Encode()
	req, err := http.NewRequestWithContext(ctx, method, u.String(), bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("build Docker workspace request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("X-Workspace-Token", token)
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("call Docker workspace: %w", err)
	}
	return resp, nil
}

func workspaceToken(ctx *security.RequestContext, accountID string) (string, error) {
	tenantID := ""
	if ctx.GetSecurityContext() != nil {
		tenantID = ctx.GetSecurityContext().GetTenantId()
	}
	return signWorkspaceToken(accountID, tenantID)
}

func dockerWorkspaceEnv(accountID, token string) []string {
	env := []string{
		ENV_NB_LLM_SERVER_URL + "=" + config.Config.LlmServerUrl,
		ENV_NB_ACCOUNT_ID + "=" + accountID,
		ENV_NB_WORKSPACE_TOKEN + "=" + token,
		ENV_NB_RELAY_SERVER_ENDPOINT + "=" + config.Config.RelayServerEndpoint,
		"SERVER_WRITE_TIMEOUT=" + config.Config.LlmServerWorkspaceCommandTimeout,
	}
	for _, key := range []string{"LLM_PROVIDER", "LLM_MODEL_NAME", "LLM_PROVIDER_API_KEY", "LLM_PROVIDER_API_ENDPOINT", "LLM_PROVIDER_REGION", "LLM_PROVIDER_API_VERSION", "LLM_PROVIDER_API_TYPE", "LLM_PROVIDER_MAX_RETRIES", "BASE_URL"} {
		if value, ok := os.LookupEnv(key); ok {
			env = append(env, key+"="+value)
		}
	}
	for _, item := range ParseExtraEnv(config.Config.LlmServerCodeAgentExtraEnv) {
		env = append(env, item.Name+"="+item.Value)
	}
	return env
}

func mergeDockerEnv(base, overrides []string) []string {
	result := append([]string(nil), base...)
	positions := make(map[string]int, len(result))
	for index, item := range result {
		key, _, _ := strings.Cut(item, "=")
		positions[key] = index
	}
	for _, item := range overrides {
		key, _, _ := strings.Cut(item, "=")
		if index, ok := positions[key]; ok {
			result[index] = item
			continue
		}
		positions[key] = len(result)
		result = append(result, item)
	}
	return result
}

func (d *dockerRuntime) imageEnv(ctx context.Context, image string) ([]string, int, error) {
	data, status, err := d.request(ctx, http.MethodGet, "/images/"+url.PathEscape(image)+"/json", nil)
	if err != nil || status == http.StatusNotFound {
		return nil, status, err
	}
	if status < 200 || status >= 300 {
		return nil, status, fmt.Errorf("inspect workspace image %q: Docker returned %d: %s", image, status, strings.TrimSpace(string(data)))
	}
	var inspection struct {
		Config struct {
			Env []string `json:"Env"`
		} `json:"Config"`
	}
	if err := json.Unmarshal(data, &inspection); err != nil {
		return nil, status, fmt.Errorf("decode workspace image inspection: %w", err)
	}
	return inspection.Config.Env, status, nil
}

func (d *dockerRuntime) ensureImageEnv(ctx context.Context, image string) ([]string, error) {
	env, status, err := d.imageEnv(ctx, image)
	if err != nil {
		return nil, err
	}
	if status != http.StatusNotFound {
		return env, nil
	}
	pullPath := "/images/create?fromImage=" + url.QueryEscape(image)
	pullCtx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()
	req, err := http.NewRequestWithContext(pullCtx, http.MethodPost, d.baseURL+pullPath, nil)
	if err != nil {
		return nil, fmt.Errorf("build Docker image pull request: %w", err)
	}
	resp, err := d.pullClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("pull workspace image %q: %w", image, err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		data, _ := io.ReadAll(io.LimitReader(resp.Body, dockerResponseBodyLimit))
		return nil, fmt.Errorf("pull workspace image %q: Docker returned %d: %s", image, resp.StatusCode, strings.TrimSpace(string(data)))
	}
	decoder := json.NewDecoder(resp.Body)
	for {
		var progress struct {
			Error       string `json:"error"`
			ErrorDetail struct {
				Message string `json:"message"`
			} `json:"errorDetail"`
		}
		if err := decoder.Decode(&progress); err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			return nil, fmt.Errorf("decode Docker image pull response for %q: %w", image, err)
		}
		if progress.Error != "" {
			return nil, fmt.Errorf("pull workspace image %q: %s", image, progress.Error)
		}
		if progress.ErrorDetail.Message != "" {
			return nil, fmt.Errorf("pull workspace image %q: %s", image, progress.ErrorDetail.Message)
		}
	}
	env, status, err = d.imageEnv(pullCtx, image)
	if err != nil {
		return nil, err
	}
	if status < 200 || status >= 300 {
		return nil, fmt.Errorf("workspace image %q is unavailable after pull", image)
	}
	return env, nil
}

func dockerResourceLimits() (int64, int64, error) {
	var memory, nanoCPUs int64
	if value := config.Config.LlmServerWorkspaceResourceLimitMemory; value != "" {
		quantity, err := resource.ParseQuantity(value)
		if err != nil {
			return 0, 0, fmt.Errorf("parse workspace memory limit: %w", err)
		}
		memory = quantity.Value()
	}
	if value := config.Config.LlmServerWorkspaceResourceLimitCpu; value != "" {
		quantity, err := resource.ParseQuantity(value)
		if err != nil {
			return 0, 0, fmt.Errorf("parse workspace CPU limit: %w", err)
		}
		nanoCPUs = quantity.MilliValue() * 1_000_000
	}
	return memory, nanoCPUs, nil
}

func (d *dockerRuntime) create(ctx *security.RequestContext, accountID string) error {
	name := dockerWorkspaceName(accountID)
	existing, inspectErr := d.inspect(ctx.GetContext(), accountID)
	if inspectErr != nil && !errors.Is(inspectErr, errDockerWorkspaceNotFound) {
		return inspectErr
	}
	if inspectErr == nil {
		if err := validateDockerWorkspaceOwnership(existing, accountID); err != nil {
			return err
		}
		token := dockerEnvValue(existing.Config.Env, ENV_NB_WORKSPACE_TOKEN)
		if existing.Config.Image == config.Config.LlmServerCodeAgentImage && dockerWorkspaceTokenReusable(token, time.Now()) {
			if existing.State.Running {
				return nil
			}
			return d.start(ctx.GetContext(), accountID)
		}
		if err := d.delete(ctx.GetContext(), accountID); err != nil {
			return err
		}
	}

	token, err := workspaceToken(ctx, accountID)
	if err != nil {
		return err
	}
	tenantID := ""
	if ctx.GetSecurityContext() != nil {
		tenantID = ctx.GetSecurityContext().GetTenantId()
	}
	memory, nanoCPUs, err := dockerResourceLimits()
	if err != nil {
		return err
	}
	imageEnv, err := d.ensureImageEnv(ctx.GetContext(), config.Config.LlmServerCodeAgentImage)
	if err != nil {
		return err
	}
	network := config.Config.LlmServerWorkspaceDockerNetwork
	createBody := map[string]any{
		"Image": config.Config.LlmServerCodeAgentImage,
		"Cmd":   []string{"/app/code-analysis-agent", "--server"},
		"User":  "1000:3000",
		"Env":   mergeDockerEnv(imageEnv, dockerWorkspaceEnv(accountID, token)),
		"Labels": map[string]string{
			dockerManagedLabel: "true", dockerAccountLabel: accountID,
			dockerTenantLabel: tenantID, dockerImageLabel: config.Config.LlmServerCodeAgentImage,
		},
		"HostConfig": map[string]any{
			"NetworkMode": network, "Memory": memory, "NanoCpus": nanoCPUs,
			"RestartPolicy": map[string]string{"Name": "unless-stopped"},
			"SecurityOpt":   []string{"no-new-privileges:true"}, "CapDrop": []string{"ALL"},
		},
		"NetworkingConfig": map[string]any{"EndpointsConfig": map[string]any{network: map[string]any{}}},
	}
	path := "/containers/create?name=" + url.QueryEscape(name)
	data, status, err := d.request(ctx.GetContext(), http.MethodPost, path, createBody)
	if err != nil {
		return err
	}
	if status == http.StatusConflict {
		// Concurrent lazy creation may have won the race. Verify that the
		// conflicting name belongs to the requested account before trusting it.
		winner, inspectErr := d.inspect(ctx.GetContext(), accountID)
		if inspectErr != nil {
			return fmt.Errorf("verify conflicting workspace container %s: %w", name, inspectErr)
		}
		if ownershipErr := validateDockerWorkspaceOwnership(winner, accountID); ownershipErr != nil {
			return ownershipErr
		}
		winnerToken := dockerEnvValue(winner.Config.Env, ENV_NB_WORKSPACE_TOKEN)
		if winner.Config.Image != config.Config.LlmServerCodeAgentImage || !dockerWorkspaceTokenReusable(winnerToken, time.Now()) {
			return fmt.Errorf("conflicting workspace container %s does not match the requested image and token", name)
		}
		if winner.State.Running {
			return nil
		}
		return d.start(ctx.GetContext(), accountID)
	}
	if status < 200 || status >= 300 {
		return fmt.Errorf("create workspace container %s: Docker returned %d: %s", name, status, strings.TrimSpace(string(data)))
	}
	return d.start(ctx.GetContext(), accountID)
}

func (d *dockerRuntime) delete(ctx context.Context, accountID string) error {
	name := dockerWorkspaceName(accountID)
	data, status, err := d.request(ctx, http.MethodDelete, "/containers/"+url.PathEscape(name)+"?force=true", nil)
	if err != nil {
		return err
	}
	if status == http.StatusNotFound || status == http.StatusNoContent {
		return nil
	}
	return fmt.Errorf("delete workspace container %s: Docker returned %d: %s", name, status, strings.TrimSpace(string(data)))
}

func cleanupDockerWorkspaces(ctx context.Context) {
	runtime, err := newDockerRuntime()
	if err != nil {
		slog.Warn("workspace: initialize Docker runtime for cleanup", "error", err)
		return
	}
	filters, _ := json.Marshal(map[string][]string{"label": {dockerManagedLabel + "=true"}})
	data, status, err := runtime.request(ctx, http.MethodGet, "/containers/json?all=true&filters="+url.QueryEscape(string(filters)), nil)
	if err != nil {
		slog.Warn("workspace: list Docker containers for cleanup", "error", err)
		return
	}
	if status < 200 || status >= 300 {
		slog.Warn("workspace: list Docker containers for cleanup", "status", status)
		return
	}
	var containers []struct {
		ID     string            `json:"Id"`
		Labels map[string]string `json:"Labels"`
	}
	if err := json.Unmarshal(data, &containers); err != nil {
		slog.Warn("workspace: decode Docker containers for cleanup", "error", err)
		return
	}
	for _, container := range containers {
		if ctx.Err() != nil {
			break
		}
		if container.Labels[dockerImageLabel] == config.Config.LlmServerCodeAgentImage {
			continue
		}
		_, status, err := runtime.request(ctx, http.MethodDelete, "/containers/"+url.PathEscape(container.ID)+"?force=true", nil)
		if err != nil || (status != http.StatusNoContent && status != http.StatusNotFound) {
			slog.Warn("workspace: delete stale Docker container", "container_id", container.ID, "status", status, "error", err)
		}
	}
}
