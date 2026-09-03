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
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"nudgebee/llm/config"
	"nudgebee/llm/security"

	"k8s.io/apimachinery/pkg/api/resource"
)

const (
	dockerManagedLabel = "com.nudgebee.workspace"
	dockerAccountLabel = "com.nudgebee.workspace.account-id"
	dockerTenantLabel  = "com.nudgebee.workspace.tenant-id"
	dockerImageLabel   = "com.nudgebee.workspace.image"
)

var errDockerWorkspaceNotFound = errors.New("docker workspace container not found")

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
	return newDockerRuntimeForHost(host)
}

func newDockerRuntimeForHost(host string) (*dockerRuntime, error) {
	transport := &http.Transport{}
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
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, resp.StatusCode, fmt.Errorf("read Docker response: %w", err)
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

func (d *dockerRuntime) endpoint(ctx context.Context, accountID string) (string, string, error) {
	container, err := d.inspect(ctx, accountID)
	if err != nil {
		return "", "", err
	}
	if container.Config.Labels[dockerManagedLabel] != "true" || container.Config.Labels[dockerAccountLabel] != accountID {
		return "", "", fmt.Errorf("refusing to use unmanaged Docker container %s", dockerWorkspaceName(accountID))
	}
	if !container.State.Running {
		return "", "", fmt.Errorf("workspace container is not ready: state=%s", container.State.Status)
	}
	if container.State.Health != nil && container.State.Health.Status == "unhealthy" {
		return "", "", fmt.Errorf("workspace container is not ready: health=unhealthy")
	}
	return fmt.Sprintf("http://%s:%d", dockerWorkspaceName(accountID), config.Config.LlmServerWorkspacePort), dockerEnvValue(container.Config.Env, ENV_NB_WORKSPACE_TOKEN), nil
}

func dockerAPIRequest(ctx context.Context, client *http.Client, accountID, method, endpoint string, queryParams map[string]string, body []byte) (*http.Response, error) {
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

func dockerWorkspaceEnv(ctx *security.RequestContext, accountID, token string) []string {
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
	data, pullStatus, err := d.requestWithClient(ctx, d.pullClient, http.MethodPost, pullPath, nil)
	if err != nil {
		return nil, err
	}
	if pullStatus < 200 || pullStatus >= 300 {
		return nil, fmt.Errorf("pull workspace image %q: Docker returned %d: %s", image, pullStatus, strings.TrimSpace(string(data)))
	}
	env, status, err = d.imageEnv(ctx, image)
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
		if existing.Config.Labels[dockerManagedLabel] != "true" || existing.Config.Labels[dockerAccountLabel] != accountID {
			return fmt.Errorf("refusing to reuse unmanaged Docker container %s", name)
		}
		if existing.Config.Image == config.Config.LlmServerCodeAgentImage && existing.State.Running {
			return nil
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
		"Env":   mergeDockerEnv(imageEnv, dockerWorkspaceEnv(ctx, accountID, token)),
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
		// Concurrent lazy creation won the race. The winner owns readiness.
		return nil
	}
	if status < 200 || status >= 300 {
		return fmt.Errorf("create workspace container %s: Docker returned %d: %s", name, status, strings.TrimSpace(string(data)))
	}
	_, status, err = d.request(ctx.GetContext(), http.MethodPost, "/containers/"+url.PathEscape(name)+"/start", nil)
	if err != nil {
		return err
	}
	if status < 200 || status >= 300 {
		return fmt.Errorf("start workspace container %s: Docker returned %d", name, status)
	}
	return nil
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
		return
	}
	filters, _ := json.Marshal(map[string][]string{"label": {dockerManagedLabel + "=true"}})
	data, status, err := runtime.request(ctx, http.MethodGet, "/containers/json?all=true&filters="+url.QueryEscape(string(filters)), nil)
	if err != nil || status < 200 || status >= 300 {
		return
	}
	var containers []struct {
		ID     string            `json:"Id"`
		Labels map[string]string `json:"Labels"`
	}
	if err := json.Unmarshal(data, &containers); err != nil {
		return
	}
	for _, container := range containers {
		if container.Labels[dockerImageLabel] == config.Config.LlmServerCodeAgentImage {
			continue
		}
		_, _, _ = runtime.request(ctx, http.MethodDelete, "/containers/"+url.PathEscape(container.ID)+"?force=true", nil)
	}
}
