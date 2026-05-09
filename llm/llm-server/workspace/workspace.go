package workspace

import (
	"bytes"
	"context"
	"encoding/json"
	stderrors "errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"nudgebee/llm/common"
	"nudgebee/llm/config"
	"nudgebee/llm/security"
	"os"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	kube_labels "k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/util/intstr"
	kubernetes "k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
)

const imageAnnotationKey = "nudgebee.com/code-agent-image"

const CacheNamespaceWorkspaceTokens = "workspace_tokens"

const ENV_NB_LLM_SERVER_URL = "NB_LLM_SERVER_URL"
const ENV_NB_ACCOUNT_ID = "NB_ACCOUNT_ID"
const ENV_NB_CONVERSATION_ID = "NB_CONVERSATION_ID"
const ENV_NB_TOOL_CONFIG_NAME = "NB_TOOL_CONFIG_NAME"
const ENV_NB_WORKSPACE_TOKEN = "NB_WORKSPACE_TOKEN"
const ENV_NB_RELAY_SERVER_ENDPOINT = "RELAY_SERVER_ENDPOINT"

const workspaceTokenLifetime = 365 * 24 * time.Hour

func init() {
	common.CacheCreateNamespace(CacheNamespaceWorkspaceTokens, common.CacheNamespaceWithExpiration(workspaceTokenLifetime))
}

type WorkspaceTokenClaims struct {
	AccountId string `json:"account_id"`
	TenantId  string `json:"tenant_id"`
	jwt.RegisteredClaims
}

type WorkspaceManager interface {
	CreateWorkspace(ctx *security.RequestContext, accountId string) error
	IsWorkspaceExists(ctx *security.RequestContext, accountId string) (bool, error)
	TerminateWorkspace(ctx *security.RequestContext, accountId string) error
	WaitForReady(ctx *security.RequestContext, accountId string) error
	ExecuteCommand(ctx *security.RequestContext, accountId string, conversationId string, command string, env map[string]string) (string, error)
	ExecuteOrLazyCreate(ctx *security.RequestContext, accountId string, conversationId string, command string, env map[string]string) (string, error)
	CallAPI(ctx *security.RequestContext, accountId string, method string, endpoint string, queryParams map[string]string, body any) ([]byte, error)
	CallAPIOrLazyCreate(ctx *security.RequestContext, accountId string, method string, endpoint string, queryParams map[string]string, body any) ([]byte, error)
	ListFiles(ctx *security.RequestContext, accountId string, conversationId string, path string) (any, error)
	ReadFile(ctx *security.RequestContext, accountId string, conversationId string, path string) ([]byte, error)
	ReadFileStream(ctx *security.RequestContext, accountId string, conversationId string, path string) (io.ReadCloser, error)
	BatchReadFile(ctx *security.RequestContext, accountId string, conversationId string, paths []string) (any, error)
	SaveFile(ctx *security.RequestContext, accountId string, conversationId string, path string, content string) error
	DeleteFile(ctx *security.RequestContext, accountId string, conversationId string, path string) error
}

type workspaceManager struct {
	httpClient *http.Client
}

func NewWorkspaceManager() WorkspaceManager {
	return NewWorkspaceManagerWithTimeout(60 * time.Second)
}

func NewWorkspaceManagerWithTimeout(timeout time.Duration) WorkspaceManager {
	return &workspaceManager{
		httpClient: &http.Client{
			Timeout: timeout,
			Transport: otelhttp.NewTransport(&http.Transport{
				Proxy: http.ProxyFromEnvironment,
				DialContext: (&net.Dialer{
					Timeout:   5 * time.Second,
					KeepAlive: 30 * time.Second,
				}).DialContext,
				ForceAttemptHTTP2:     true,
				MaxIdleConns:          100,
				IdleConnTimeout:       90 * time.Second,
				TLSHandshakeTimeout:   10 * time.Second,
				ExpectContinueTimeout: 1 * time.Second,
			}),
		},
	}
}

func (w *workspaceManager) ExecuteOrLazyCreate(ctx *security.RequestContext, accountId string, conversationId string, command string, env map[string]string) (string, error) {
	if accountId == "" {
		ctx.GetLogger().Error("workspace: accountId is required for execution")
		return "", fmt.Errorf("workspace: accountId is required")
	}
	// 1. Optimistic Execution
	resp, err := w.ExecuteCommand(ctx, accountId, conversationId, command, env)
	if err == nil {
		return resp, nil
	}

	// 2. Filter errors: Only attempt recovery for infrastructure issues
	if !w.isRecoverableError(err) {
		return "", err
	}

	// 3. Fallback: Create and Wait
	logger := ctx.GetLogger()
	logger.Info("workspace: execution failed with recoverable error, attempting lazy creation/recovery", "account_id", accountId, "error", err)

	if err := w.CreateWorkspace(ctx, accountId); err != nil {
		return "", fmt.Errorf("failed to create workspace: %w", err)
	}

	if err := w.WaitForReady(ctx, accountId); err != nil {
		return "", fmt.Errorf("failed to wait for workspace readiness: %w", err)
	}

	// 4. Retry Execution
	return w.ExecuteCommand(ctx, accountId, conversationId, command, env)
}

func (w *workspaceManager) isRecoverableError(err error) bool {
	if err == nil {
		return false
	}

	// Check for specific Kubernetes API errors
	if errors.IsNotFound(err) || errors.IsServiceUnavailable(err) || errors.IsTimeout(err) {
		return true
	}

	// Check error string for connectivity issues or readiness failures
	errMsg := strings.ToLower(err.Error())
	recoverableKeywords := []string{
		"connection refused",
		"i/o timeout",
		"not ready",
		"eof",
		"no such host",
		"rejected our request",
		"unknown",
		"post pods",
	}

	for _, keyword := range recoverableKeywords {
		if strings.Contains(errMsg, keyword) {
			return true
		}
	}

	return false
}

func (w *workspaceManager) CallAPI(ctx *security.RequestContext, accountId string, method string, endpoint string, queryParams map[string]string, body any) ([]byte, error) {
	return w.callWorkspaceAPIWithClient(ctx, accountId, method, endpoint, queryParams, body, w.httpClient)
}

func (w *workspaceManager) CallAPIOrLazyCreate(ctx *security.RequestContext, accountId string, method string, endpoint string, queryParams map[string]string, body any) ([]byte, error) {
	if accountId == "" {
		ctx.GetLogger().Error("workspace: accountId is required for API call")
		return nil, fmt.Errorf("workspace: accountId is required")
	}

	// 1. Optimistic execution
	resp, err := w.CallAPI(ctx, accountId, method, endpoint, queryParams, body)
	if err == nil {
		return resp, nil
	}

	// 2. Filter errors: Only attempt recovery for infrastructure issues
	if !w.isRecoverableError(err) {
		return nil, err
	}

	// 3. Fallback: Create and Wait
	logger := ctx.GetLogger()
	logger.Info("workspace: API call failed with recoverable error, attempting lazy creation/recovery", "account_id", accountId, "error", err)

	if err := w.CreateWorkspace(ctx, accountId); err != nil {
		return nil, fmt.Errorf("failed to create workspace: %w", err)
	}

	if err := w.WaitForReady(ctx, accountId); err != nil {
		return nil, fmt.Errorf("failed to wait for workspace readiness: %w", err)
	}

	// 4. Retry
	return w.CallAPI(ctx, accountId, method, endpoint, queryParams, body)
}

func (w *workspaceManager) WaitForReady(ctx *security.RequestContext, accountId string) error {
	if accountId == "" {
		ctx.GetLogger().Error("workspace: accountId is required for wait")
		return fmt.Errorf("workspace: accountId is required")
	}
	if !config.Config.LlmServerWorkspaceEnabled {
		return fmt.Errorf("workspace feature is disabled")
	}

	clientset, err := getKubeClient(100, 200)
	if err != nil {
		return err
	}

	namespace := config.Config.LlmServerCodeAgentNamespace
	podName := fmt.Sprintf("workspace-%s", strings.ToLower(accountId))

	// Poll for readiness
	// Use context deadline if available, otherwise default to 5 minutes
	timeout := 300 * time.Second
	pollInterval := 2 * time.Second

	ctxDeadline, ok := ctx.GetContext().Deadline()
	if ok {
		timeout = time.Until(ctxDeadline)
	}

	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()

	timeoutTimer := time.NewTimer(timeout)
	defer timeoutTimer.Stop()

	logger := ctx.GetLogger()
	logger.Info("workspace: waiting for pod to be ready", "pod_name", podName, "timeout", timeout.String())

	lastProgressLog := time.Now()
	notFoundCount := 0
	const maxNotFoundRetries = 5

	for {
		select {
		case <-ctx.GetContext().Done():
			return ctx.GetContext().Err()
		case <-timeoutTimer.C:
			ctx.GetLogger().Error("workspace: timed out waiting for readiness", "pod_name", podName)
			return fmt.Errorf("timed out waiting for workspace %s to be ready", podName)
		case <-ticker.C:
			// Periodic progress log every 30 seconds
			if time.Since(lastProgressLog) > 30*time.Second {
				logger.Info("workspace: still waiting for pod readiness...", "pod_name", podName)
				lastProgressLog = time.Now()
			}

			pod, err := clientset.CoreV1().Pods(namespace).Get(ctx.GetContext(), podName, metav1.GetOptions{})
			if err != nil {
				if errors.IsNotFound(err) {
					notFoundCount++
					if notFoundCount >= maxNotFoundRetries {
						return fmt.Errorf("workspace pod %s not found after %d consecutive checks — pod was likely evicted", podName, notFoundCount)
					}
					continue
				}
				continue
			}
			notFoundCount = 0 // reset on successful get

			// Fail fast when the pod enters a terminal state (Failed, Succeeded,
			// CrashLoopBackOff) instead of polling until the full timeout.
			if terminal, reason := isPodInTerminalState(pod); terminal {
				return fmt.Errorf("workspace pod %s entered terminal state while waiting for readiness: %s", podName, reason)
			}

			for _, cond := range pod.Status.Conditions {
				if cond.Type == corev1.PodReady && cond.Status == corev1.ConditionTrue {
					logger.Info("workspace: pod is now ready and serving", "pod_name", podName)
					return nil
				}
			}
		}
	}
}

func (w *workspaceManager) CreateWorkspace(ctx *security.RequestContext, accountId string) error {
	if accountId == "" {
		ctx.GetLogger().Error("workspace: accountId is required for creation")
		return fmt.Errorf("workspace: accountId is required")
	}
	if !config.Config.LlmServerWorkspaceEnabled {
		return fmt.Errorf("workspace feature is disabled")
	}

	logger := ctx.GetLogger()

	clientset, err := getKubeClient(100, 200)
	if err != nil {
		return err
	}

	namespace := config.Config.LlmServerCodeAgentNamespace
	image := config.Config.LlmServerCodeAgentImage
	podName := fmt.Sprintf("workspace-%s", strings.ToLower(accountId))

	// Check if pod already exists and is healthy
	existingPod, err := clientset.CoreV1().Pods(namespace).Get(ctx.GetContext(), podName, metav1.GetOptions{})
	if err == nil {
		// Pod exists — determine whether it needs to be replaced.
		needsReplace := false
		replaceReason := ""

		if terminal, reason := isPodInTerminalState(existingPod); terminal {
			needsReplace = true
			replaceReason = reason
		} else if podImage := existingPod.Annotations[imageAnnotationKey]; podImage != image {
			// Only replace on image mismatch when running in-cluster.
			// Locally, llm-server may be pointing at a shared cluster via
			// kubeconfig and must not stomp on pods owned by the real
			// in-cluster llm-server (e.g. if the developer's local
			// LLM_SERVER_CODE_AGENT_IMAGE differs from what's deployed).
			if _, inClusterErr := rest.InClusterConfig(); inClusterErr == nil {
				needsReplace = true
				replaceReason = fmt.Sprintf("image mismatch (pod=%s, expected=%s)", podImage, image)
			} else {
				logger.Warn("workspace: skipping image-mismatch replacement (not running in-cluster), reusing existing pod",
					"pod_name", podName, "pod_image", podImage, "expected_image", image)
			}
		}

		if needsReplace {
			logger.Info("workspace: existing pod needs replacement, deleting for recreation",
				"pod_name", podName, "phase", existingPod.Status.Phase,
				"reason", replaceReason,
				"terminating", existingPod.DeletionTimestamp != nil)
			gracePeriod := int64(0)
			deleteErr := clientset.CoreV1().Pods(namespace).Delete(ctx.GetContext(), podName, metav1.DeleteOptions{
				GracePeriodSeconds: &gracePeriod,
			})
			if deleteErr != nil && !errors.IsNotFound(deleteErr) {
				return fmt.Errorf("workspace: failed to delete pod for replacement: %w", deleteErr)
			}
			// Wait for the old pod to be fully removed before creating a new one,
			// otherwise the Create call may hit an AlreadyExists error on the
			// still-terminating pod. Skip if already gone (NotFound).
			if deleteErr == nil {
				if err := w.waitForPodDeletion(ctx.GetContext(), clientset, namespace, podName); err != nil {
					return fmt.Errorf("workspace: timed out waiting for old pod deletion: %w", err)
				}
			}
			// Fall through to create a new pod
		} else {
			logger.Info("workspace already exists", "account_id", accountId)
			return nil
		}
	} else if !errors.IsNotFound(err) {
		return fmt.Errorf("failed to check existing workspace pod: %w", err)
	}

	tenantId := ""
	if ctx.GetSecurityContext() != nil {
		tenantId = ctx.GetSecurityContext().GetTenantId()
	}

	// Generate JWT workspace token
	claims := WorkspaceTokenClaims{
		AccountId: accountId,
		TenantId:  tenantId,
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(workspaceTokenLifetime)),
			IssuedAt:  jwt.NewNumericDate(time.Now()),
			Issuer:    "llm-server",
		},
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	workspaceToken, err := token.SignedString([]byte(config.Config.LlmServerJwtSecret))
	if err != nil {
		logger.Error("workspace: failed to sign workspace token", "error", err)
		return fmt.Errorf("failed to sign workspace token: %w", err)
	}

	// Command to start server (no arguments usually starts server, based on main.go)
	args := []string{"/app/code-analysis-agent", "--server"}

	runAsUser := int64(1000)
	runAsGroup := int64(3000)
	runAsNonRoot := true

	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      podName,
			Namespace: namespace,
			Labels: kube_labels.Set{
				"app":        "workspace",
				"account_id": strings.ToLower(accountId),
			},
			Annotations: map[string]string{
				imageAnnotationKey: image,
			},
		},
		Spec: corev1.PodSpec{
			SecurityContext: &corev1.PodSecurityContext{
				RunAsUser:    &runAsUser,
				RunAsGroup:   &runAsGroup,
				RunAsNonRoot: &runAsNonRoot,
			},
			Containers: []corev1.Container{
				{
					Name:            "workspace-server",
					Image:           image,
					ImagePullPolicy: corev1.PullIfNotPresent,
					Command:         args,
					Ports: []corev1.ContainerPort{
						{
							ContainerPort: int32(config.Config.LlmServerWorkspacePort),
							Name:          "http",
						},
					},
					Resources: buildWorkspaceResources(),
					ReadinessProbe: &corev1.Probe{
						ProbeHandler: corev1.ProbeHandler{
							HTTPGet: &corev1.HTTPGetAction{
								Path: "/health",
								Port: intstr.FromInt(config.Config.LlmServerWorkspacePort),
							},
						},
						InitialDelaySeconds: 5,
						PeriodSeconds:       10,
					},
					Env: []corev1.EnvVar{
						{
							Name:  ENV_NB_LLM_SERVER_URL,
							Value: config.Config.LlmServerUrl,
						},
						{
							Name:  ENV_NB_ACCOUNT_ID,
							Value: accountId,
						},
						{
							Name:  ENV_NB_WORKSPACE_TOKEN,
							Value: workspaceToken,
						},
						{
							Name:  ENV_NB_RELAY_SERVER_ENDPOINT,
							Value: config.Config.RelayServerEndpoint,
						},
					},
				},
			},
			RestartPolicy: corev1.RestartPolicyAlways, // Server should restart
		},
	}

	// Pass only required secret keys as env vars instead of mounting the entire secret
	if config.Config.LlmServerCodeAgentSecret != "" {
		secretName := config.Config.LlmServerCodeAgentSecret
		optional := true
		secretEnvVars := []corev1.EnvVar{
			{Name: "LLM_PROVIDER", ValueFrom: &corev1.EnvVarSource{SecretKeyRef: &corev1.SecretKeySelector{LocalObjectReference: corev1.LocalObjectReference{Name: secretName}, Key: "LLM_PROVIDER", Optional: &optional}}},
			{Name: "LLM_MODEL_NAME", ValueFrom: &corev1.EnvVarSource{SecretKeyRef: &corev1.SecretKeySelector{LocalObjectReference: corev1.LocalObjectReference{Name: secretName}, Key: "LLM_MODEL_NAME", Optional: &optional}}},
			{Name: "LLM_PROVIDER_API_KEY", ValueFrom: &corev1.EnvVarSource{SecretKeyRef: &corev1.SecretKeySelector{LocalObjectReference: corev1.LocalObjectReference{Name: secretName}, Key: "LLM_PROVIDER_API_KEY", Optional: &optional}}},
			{Name: "LLM_PROVIDER_API_ENDPOINT", ValueFrom: &corev1.EnvVarSource{SecretKeyRef: &corev1.SecretKeySelector{LocalObjectReference: corev1.LocalObjectReference{Name: secretName}, Key: "LLM_PROVIDER_API_ENDPOINT", Optional: &optional}}},
			{Name: "LLM_PROVIDER_REGION", ValueFrom: &corev1.EnvVarSource{SecretKeyRef: &corev1.SecretKeySelector{LocalObjectReference: corev1.LocalObjectReference{Name: secretName}, Key: "LLM_PROVIDER_REGION", Optional: &optional}}},
			{Name: "LLM_PROVIDER_API_VERSION", ValueFrom: &corev1.EnvVarSource{SecretKeyRef: &corev1.SecretKeySelector{LocalObjectReference: corev1.LocalObjectReference{Name: secretName}, Key: "LLM_PROVIDER_API_VERSION", Optional: &optional}}},
			{Name: "LLM_PROVIDER_API_TYPE", ValueFrom: &corev1.EnvVarSource{SecretKeyRef: &corev1.SecretKeySelector{LocalObjectReference: corev1.LocalObjectReference{Name: secretName}, Key: "LLM_PROVIDER_API_TYPE", Optional: &optional}}},
			{Name: "LLM_PROVIDER_MAX_RETRIES", ValueFrom: &corev1.EnvVarSource{SecretKeyRef: &corev1.SecretKeySelector{LocalObjectReference: corev1.LocalObjectReference{Name: secretName}, Key: "LLM_PROVIDER_MAX_RETRIES", Optional: &optional}}},
			{Name: "NUDGEBEE_ENCRYPTION_KEY", ValueFrom: &corev1.EnvVarSource{SecretKeyRef: &corev1.SecretKeySelector{LocalObjectReference: corev1.LocalObjectReference{Name: secretName}, Key: "NUDGEBEE_ENCRYPTION_KEY", Optional: &optional}}},
		}
		pod.Spec.Containers[0].Env = append(pod.Spec.Containers[0].Env, secretEnvVars...)
	}

	if config.Config.LlmServerCodeAgentImagePullSecret != "" {
		pod.Spec.ImagePullSecrets = []corev1.LocalObjectReference{
			{Name: config.Config.LlmServerCodeAgentImagePullSecret},
		}
	}

	_, err = clientset.CoreV1().Pods(namespace).Create(ctx.GetContext(), pod, metav1.CreateOptions{})
	if err != nil {
		if errors.IsAlreadyExists(err) {
			logger.Info("workspace pod already exists", "pod_name", podName)
			return nil
		}
		logger.Error("workspace: failed to create workspace pod", "pod_name", podName, "error", err)
		return fmt.Errorf("failed to create workspace pod: %w", err)
	}
	logger.Info("workspace pod created", "pod_name", podName)
	return nil
}

func buildWorkspaceResources() corev1.ResourceRequirements {
	resources := corev1.ResourceRequirements{
		Requests: corev1.ResourceList{
			corev1.ResourceCPU:    resource.MustParse(config.Config.LlmServerWorkspaceResourceRequestCpu),
			corev1.ResourceMemory: resource.MustParse(config.Config.LlmServerWorkspaceResourceRequestMemory),
		},
	}
	if config.Config.LlmServerWorkspaceResourceLimitCpu != "" && config.Config.LlmServerWorkspaceResourceLimitMemory != "" {
		resources.Limits = corev1.ResourceList{
			corev1.ResourceCPU:    resource.MustParse(config.Config.LlmServerWorkspaceResourceLimitCpu),
			corev1.ResourceMemory: resource.MustParse(config.Config.LlmServerWorkspaceResourceLimitMemory),
		}
	}
	return resources
}

func (w *workspaceManager) IsWorkspaceExists(ctx *security.RequestContext, accountId string) (bool, error) {
	if accountId == "" {
		ctx.GetLogger().Error("workspace: accountId is required for exists check")
		return false, fmt.Errorf("workspace: accountId is required")
	}
	if !config.Config.LlmServerWorkspaceEnabled {
		return false, fmt.Errorf("workspace feature is disabled")
	}

	clientset, err := getKubeClient(100, 200)
	if err != nil {
		return false, err
	}

	namespace := config.Config.LlmServerCodeAgentNamespace
	podName := fmt.Sprintf("workspace-%s", strings.ToLower(accountId))

	_, err = clientset.CoreV1().Pods(namespace).Get(ctx.GetContext(), podName, metav1.GetOptions{})
	if err != nil {
		if errors.IsNotFound(err) {
			return false, nil
		}
		return false, fmt.Errorf("failed to get pod: %w", err)
	}

	return true, nil
}

func isPodInCrashLoop(pod *corev1.Pod) bool {
	for _, cs := range pod.Status.ContainerStatuses {
		if cs.State.Waiting != nil && cs.State.Waiting.Reason == "CrashLoopBackOff" {
			return true
		}
	}
	return false
}

// isPodInTerminalState returns true when the pod is not capable of serving
// requests — it has completed, failed, is being deleted, or is stuck in a
// crash loop.  Callers should treat the returned error as recoverable so that
// lazy-create can spin up a replacement.
func isPodInTerminalState(pod *corev1.Pod) (bool, string) {
	if pod.DeletionTimestamp != nil {
		return true, "pod is terminating"
	}
	if pod.Status.Phase == corev1.PodSucceeded {
		return true, "pod has completed (Succeeded phase)"
	}
	if pod.Status.Phase == corev1.PodFailed {
		return true, "pod has failed (Failed phase)"
	}
	if isPodInCrashLoop(pod) {
		return true, "pod is in CrashLoopBackOff"
	}
	// Check if the container exited but hasn't restarted yet (brief window
	// between termination and kubelet restart with RestartPolicy=Always).
	for _, cs := range pod.Status.ContainerStatuses {
		if cs.State.Terminated != nil && cs.State.Terminated.Reason == "Completed" {
			return true, "container has terminated (Completed)"
		}
	}
	return false, ""
}

// waitForPodDeletion polls until the named pod no longer exists.
// It gives up after 60 seconds to avoid blocking callers indefinitely.
func (w *workspaceManager) waitForPodDeletion(ctx context.Context, clientset *kubernetes.Clientset, namespace, podName string) error {
	timeout := 60 * time.Second
	pollInterval := 1 * time.Second
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-timer.C:
			return fmt.Errorf("pod %s still exists after %s", podName, timeout)
		case <-ticker.C:
			_, err := clientset.CoreV1().Pods(namespace).Get(ctx, podName, metav1.GetOptions{})
			if errors.IsNotFound(err) {
				return nil
			}
		}
	}
}

func (w *workspaceManager) TerminateWorkspace(ctx *security.RequestContext, accountId string) error {
	if accountId == "" {
		ctx.GetLogger().Error("workspace: accountId is required for termination")
		return fmt.Errorf("workspace: accountId is required")
	}
	if !config.Config.LlmServerWorkspaceEnabled {
		return fmt.Errorf("workspace feature is disabled")
	}

	clientset, err := getKubeClient(100, 200)
	if err != nil {
		return err
	}

	namespace := config.Config.LlmServerCodeAgentNamespace
	podName := fmt.Sprintf("workspace-%s", strings.ToLower(accountId))

	// Try to find the pod to get the token for revocation
	pod, err := clientset.CoreV1().Pods(namespace).Get(ctx.GetContext(), podName, metav1.GetOptions{})
	if err == nil {
		for _, env := range pod.Spec.Containers[0].Env {
			if env.Name == ENV_NB_WORKSPACE_TOKEN {
				// Revoke token by adding to blacklist cache
				if err := common.CacheSet(CacheNamespaceWorkspaceTokens, env.Value, []byte("revoked"), common.CacheSetWithExpiration(workspaceTokenLifetime)); err != nil {
					ctx.GetLogger().Warn("workspace: failed to revoke token", "error", err)
				}
				break
			}
		}
	}

	gracePeriod := int64(0)
	err = clientset.CoreV1().Pods(namespace).Delete(ctx.GetContext(), podName, metav1.DeleteOptions{
		GracePeriodSeconds: &gracePeriod,
	})
	if err != nil {
		if errors.IsNotFound(err) {
			return nil // Already deleted
		}
		return fmt.Errorf("failed to delete workspace pod: %w", err)
	}
	return nil
}

// CleanupStaleWorkspaces deletes workspace pods running an outdated image.
// Called on startup to ensure all workspaces use the current code-agent image.
func CleanupStaleWorkspaces(ctx context.Context) {
	if !config.Config.LlmServerWorkspaceEnabled {
		return
	}
	// Skip cleanup when running outside K8s (local development).
	// Only treat rest.ErrNotInCluster as "not in K8s"; log other errors as warnings.
	if _, err := rest.InClusterConfig(); err != nil {
		if stderrors.Is(err, rest.ErrNotInCluster) {
			slog.Debug("workspace: skipping stale workspace cleanup (not running in-cluster)")
		} else {
			slog.Warn("workspace: skipping stale workspace cleanup due to config error", "error", err)
		}
		return
	}

	clientset, err := getKubeClient(100, 200)
	if err != nil {
		slog.Warn("workspace: failed to get kube client for cleanup", "error", err)
		return
	}

	namespace := config.Config.LlmServerCodeAgentNamespace
	currentImage := config.Config.LlmServerCodeAgentImage

	pods, err := clientset.CoreV1().Pods(namespace).List(ctx, metav1.ListOptions{
		LabelSelector: "app=workspace",
	})
	if err != nil {
		slog.Warn("workspace: failed to list workspace pods for cleanup", "error", err)
		return
	}

	for _, pod := range pods.Items {
		podImage := pod.Annotations[imageAnnotationKey]
		if podImage == "" || podImage != currentImage {
			slog.Info("workspace: deleting stale workspace pod", "pod", pod.Name, "pod_image", podImage, "current_image", currentImage)
			gracePeriod := int64(0)
			_ = clientset.CoreV1().Pods(namespace).Delete(ctx, pod.Name, metav1.DeleteOptions{
				GracePeriodSeconds: &gracePeriod,
			})
		}
	}
}

type executePayload struct {
	Command        string            `json:"command"`
	ConversationId string            `json:"conversation_id"`
	Env            map[string]string `json:"env"`
}

type executeResponse struct {
	CommandStatus  string `json:"command_status"`
	Response       string `json:"response"`
	ConversationId string `json:"conversation_id"`
	Error          string `json:"error,omitempty"`
}

func (w *workspaceManager) ExecuteCommand(ctx *security.RequestContext, accountId string, conversationId string, command string, env map[string]string) (string, error) {
	if accountId == "" {
		ctx.GetLogger().Error("workspace: accountId is required for direct execution")
		return "", fmt.Errorf("workspace: accountId is required")
	}
	if !config.Config.LlmServerWorkspaceEnabled {
		return "", fmt.Errorf("workspace feature is disabled")
	}

	logger := ctx.GetLogger()
	cmdLog := command
	if len(cmdLog) > 100 {
		cmdLog = cmdLog[:100] + "..."
	}
	logger.Info("workspace: executing command", "command_preview", cmdLog, "account_id", accountId, "conversation_id", conversationId)

	if env == nil {
		env = make(map[string]string)
	}
	env["nb_account_id"] = accountId
	if ctx.GetSecurityContext() != nil {
		env["nb_tenant_id"] = ctx.GetSecurityContext().GetTenantId()
	}

	clientset, err := getKubeClient(100, 200)
	if err != nil {
		return "", err
	}

	namespace := config.Config.LlmServerCodeAgentNamespace
	podName := fmt.Sprintf("workspace-%s", strings.ToLower(accountId))

	// 1. Get Pod Info to find IP
	pod, err := clientset.CoreV1().Pods(namespace).Get(ctx.GetContext(), podName, metav1.GetOptions{})
	if err != nil {
		return "", fmt.Errorf("failed to get pod info: %w", err)
	}

	// Bail early when the pod cannot serve requests so that callers like
	// ExecuteOrLazyCreate can treat the error as recoverable and recreate.
	if terminal, reason := isPodInTerminalState(pod); terminal {
		return "", fmt.Errorf("workspace pod is not ready: %s", reason)
	}

	podIP := pod.Status.PodIP
	if podIP == "" {
		return "", fmt.Errorf("workspace pod is not ready: pod IP is not assigned yet")
	}

	workspaceToken := ""
	for _, envVar := range pod.Spec.Containers[0].Env {
		if envVar.Name == ENV_NB_WORKSPACE_TOKEN {
			workspaceToken = envVar.Value
			break
		}
	}

	// Prepare payload
	payload := executePayload{
		Command:        command,
		ConversationId: conversationId,
		Env:            env,
	}

	jsonData, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("failed to marshal payload: %w", err)
	}

	// 2. Try Direct IP Execution (Fastest, works in-cluster)
	// Skip direct execution if in testing mode to avoid timeout
	isTestMode := config.Config.Env == "testing" || config.Config.Env == "local-test"
	if !isTestMode {
		// Also check if running via go test
		for _, arg := range os.Args {
			if strings.HasPrefix(arg, "-test.") {
				isTestMode = true
				break
			}
		}
	}

	if !isTestMode {
		directUrl := fmt.Sprintf("http://%s:%d/execute", podIP, config.Config.LlmServerWorkspacePort)
		resp, directErr := w.executeDirect(ctx, directUrl, jsonData, workspaceToken)
		if directErr == nil {
			logger.Info("workspace: command execution successful via direct IP", "pod_name", podName)
			return resp, nil
		}
		logger.Info("workspace: direct IP execution failed, falling back to proxy", "pod_name", podName, "error", directErr)
	} else {
		logger.Info("workspace: skipping direct IP execution in test mode", "env", config.Config.Env)
	}

	// 3. Fallback: Execute via K8s Proxy (works locally and in-cluster)
	logger.Info("workspace: executing command via k8s proxy", "pod_name", podName)
	resultRaw, err := clientset.CoreV1().RESTClient().Post().
		Namespace(namespace).
		Resource("pods").
		Name(fmt.Sprintf("%s:%d", podName, config.Config.LlmServerWorkspacePort)).
		SubResource("proxy").
		Suffix("execute").
		Body(jsonData).
		SetHeader("Content-Type", "application/json").
		SetHeader("X-Workspace-Token", workspaceToken).
		Do(ctx.GetContext()).
		Raw()

	if err != nil {
		logger.Error("workspace: failed to execute command via proxy", "pod_name", podName, "error", err)
		return "", fmt.Errorf("failed to execute command via proxy: %w", err)
	}

	var result executeResponse
	if err := json.Unmarshal(resultRaw, &result); err != nil {
		return string(resultRaw), fmt.Errorf("failed to decode response: %w", err)
	}

	logger.Info("workspace: command execution successful via proxy", "pod_name", podName)
	return result.Response, nil
}

func (w *workspaceManager) callWorkspaceAPI(ctx *security.RequestContext, accountId string, method string, endpoint string, queryParams map[string]string, body any) ([]byte, error) {
	if accountId == "" {
		return nil, fmt.Errorf("workspace: accountId is required")
	}
	if !config.Config.LlmServerWorkspaceEnabled {
		return nil, fmt.Errorf("workspace feature is disabled")
	}

	logger := ctx.GetLogger()
	logger.Debug("workspace: calling API", "method", method, "endpoint", endpoint, "account_id", accountId)

	clientset, err := getKubeClient(100, 200)
	if err != nil {
		return nil, err
	}

	namespace := config.Config.LlmServerCodeAgentNamespace
	podName := fmt.Sprintf("workspace-%s", strings.ToLower(accountId))

	// 1. Get Pod Info
	pod, err := clientset.CoreV1().Pods(namespace).Get(ctx.GetContext(), podName, metav1.GetOptions{})
	if err != nil {
		return nil, fmt.Errorf("failed to get pod info: %w", err)
	}

	if terminal, reason := isPodInTerminalState(pod); terminal {
		return nil, fmt.Errorf("workspace pod is not ready: %s", reason)
	}

	podIP := pod.Status.PodIP
	if podIP == "" {
		return nil, fmt.Errorf("workspace pod is not ready: pod IP is not assigned yet")
	}

	workspaceToken := ""
	for _, envVar := range pod.Spec.Containers[0].Env {
		if envVar.Name == ENV_NB_WORKSPACE_TOKEN {
			workspaceToken = envVar.Value
			break
		}
	}

	// Prepare request body
	var bodyBytes []byte
	if body != nil {
		bodyBytes, err = json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("failed to marshal body: %w", err)
		}
	}

	// Helper to build URL with query params
	buildURL := func(baseURL string) string {
		if len(queryParams) == 0 {
			return baseURL
		}
		u, err := url.Parse(baseURL)
		if err != nil {
			return baseURL
		}
		q := u.Query()
		for k, v := range queryParams {
			q.Set(k, v)
		}
		u.RawQuery = q.Encode()
		return u.String()
	}

	// 2. Try Direct IP Execution
	// Note: method is GET, POST, DELETE etc.
	directUrl := buildURL(fmt.Sprintf("http://%s:%d%s", podIP, config.Config.LlmServerWorkspacePort, endpoint))

	httpClient := &http.Client{
		Transport: &http.Transport{
			ResponseHeaderTimeout: 200 * time.Millisecond,
		},
		Timeout: 500 * time.Millisecond,
	}
	req, err := http.NewRequestWithContext(ctx.GetContext(), method, directUrl, bytes.NewBuffer(bodyBytes))
	if err == nil {
		req.Header.Set("Content-Type", "application/json")
		if workspaceToken != "" {
			req.Header.Set("X-Workspace-Token", workspaceToken)
		}
		resp, directErr := httpClient.Do(req)
		if directErr == nil {
			defer func() { _ = resp.Body.Close() }()
			if resp.StatusCode >= 200 && resp.StatusCode < 300 {
				logger.Debug("workspace: API call successful via direct IP", "method", method, "endpoint", endpoint, "status", resp.StatusCode)
				return io.ReadAll(resp.Body)
			}
			errBody, _ := io.ReadAll(resp.Body)
			logger.Warn("workspace: direct API call returned non-2xx status", "status", resp.StatusCode, "body", string(errBody))
			return nil, fmt.Errorf("status %d: %s", resp.StatusCode, string(errBody))
		}
		logger.Info("workspace: direct API call failed, falling back to proxy", "error", directErr, "endpoint", endpoint)
	}

	// 3. Fallback: K8s Proxy
	logger.Debug("workspace: calling API via k8s proxy", "method", method, "endpoint", endpoint)
	reqProxy := clientset.CoreV1().RESTClient().Verb(method).
		RequestURI(fmt.Sprintf("/api/v1/namespaces/%s/pods/%s:%d/proxy%s", namespace, podName, config.Config.LlmServerWorkspacePort, endpoint))

	for k, v := range queryParams {
		reqProxy.Param(k, v)
	}

	if bodyBytes != nil {
		reqProxy.Body(bodyBytes)
		reqProxy.SetHeader("Content-Type", "application/json")
	}
	reqProxy.SetHeader("X-Workspace-Token", workspaceToken)

	resultRaw, err := reqProxy.Do(ctx.GetContext()).Raw()
	if err != nil {
		logger.Error("workspace: proxy API call failed", "method", method, "endpoint", endpoint, "error", err)
		return nil, fmt.Errorf("proxy call failed: %w", err)
	}

	logger.Debug("workspace: API call successful via proxy", "method", method, "endpoint", endpoint)
	return resultRaw, nil
}

// callWorkspaceAPIWithClient is like callWorkspaceAPI but uses the provided HTTP client
// for direct IP calls, allowing callers to control the timeout for long-running operations.
func (w *workspaceManager) callWorkspaceAPIWithClient(ctx *security.RequestContext, accountId string, method string, endpoint string, queryParams map[string]string, body any, httpClient *http.Client) ([]byte, error) {
	if accountId == "" {
		return nil, fmt.Errorf("workspace: accountId is required")
	}
	if !config.Config.LlmServerWorkspaceEnabled {
		return nil, fmt.Errorf("workspace feature is disabled")
	}

	logger := ctx.GetLogger()
	logger.Debug("workspace: calling API with custom client", "method", method, "endpoint", endpoint, "account_id", accountId)

	var bodyBytes []byte
	if body != nil {
		var err error
		bodyBytes, err = json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("failed to marshal body: %w", err)
		}
	}

	buildURL := func(baseURL string) string {
		if len(queryParams) == 0 {
			return baseURL
		}
		u, err := url.Parse(baseURL)
		if err != nil {
			return baseURL
		}
		q := u.Query()
		for k, v := range queryParams {
			q.Set(k, v)
		}
		u.RawQuery = q.Encode()
		return u.String()
	}

	// Local mode: bypass K8s entirely, call a local code-analysis server directly
	if localURL := config.Config.LlmServerWorkspaceLocalUrl; localURL != "" {
		directUrl := buildURL(fmt.Sprintf("%s%s", strings.TrimRight(localURL, "/"), endpoint))
		logger.Debug("workspace: using local URL", "url", directUrl)
		req, reqErr := http.NewRequestWithContext(ctx.GetContext(), method, directUrl, bytes.NewBuffer(bodyBytes))
		if reqErr != nil {
			return nil, fmt.Errorf("failed to build local request: %w", reqErr)
		}
		req.Header.Set("Content-Type", "application/json")
		resp, err := httpClient.Do(req)
		if err != nil {
			return nil, fmt.Errorf("local workspace call failed: %w", err)
		}
		defer func() { _ = resp.Body.Close() }()
		respBody, _ := io.ReadAll(resp.Body)
		if resp.StatusCode >= 200 && resp.StatusCode < 300 {
			logger.Debug("workspace: API call successful via local URL", "method", method, "endpoint", endpoint, "status", resp.StatusCode)
			return respBody, nil
		}
		return nil, fmt.Errorf("local workspace returned status %d: %s", resp.StatusCode, string(respBody))
	}

	clientset, err := getKubeClient(100, 200)
	if err != nil {
		return nil, err
	}

	namespace := config.Config.LlmServerCodeAgentNamespace
	podName := fmt.Sprintf("workspace-%s", strings.ToLower(accountId))

	pod, err := clientset.CoreV1().Pods(namespace).Get(ctx.GetContext(), podName, metav1.GetOptions{})
	if err != nil {
		return nil, fmt.Errorf("failed to get pod info: %w", err)
	}

	if terminal, reason := isPodInTerminalState(pod); terminal {
		return nil, fmt.Errorf("workspace pod is not ready: %s", reason)
	}

	podIP := pod.Status.PodIP
	if podIP == "" {
		return nil, fmt.Errorf("workspace pod is not ready: pod IP is not assigned yet")
	}

	workspaceToken := ""
	for _, envVar := range pod.Spec.Containers[0].Env {
		if envVar.Name == ENV_NB_WORKSPACE_TOKEN {
			workspaceToken = envVar.Value
			break
		}
	}

	// Try Direct IP Execution using the provided httpClient
	isTestMode := config.Config.Env == "testing" || config.Config.Env == "local-test"
	if !isTestMode {
		for _, arg := range os.Args {
			if strings.HasPrefix(arg, "-test.") {
				isTestMode = true
				break
			}
		}
	}

	if !isTestMode {
		directUrl := buildURL(fmt.Sprintf("http://%s:%d%s", podIP, config.Config.LlmServerWorkspacePort, endpoint))
		req, reqErr := http.NewRequestWithContext(ctx.GetContext(), method, directUrl, bytes.NewBuffer(bodyBytes))
		if reqErr == nil {
			req.Header.Set("Content-Type", "application/json")
			if workspaceToken != "" {
				req.Header.Set("X-Workspace-Token", workspaceToken)
			}
			resp, directErr := httpClient.Do(req)
			if directErr == nil {
				defer func() { _ = resp.Body.Close() }()
				if resp.StatusCode >= 200 && resp.StatusCode < 300 {
					logger.Debug("workspace: API call successful via direct IP", "method", method, "endpoint", endpoint, "status", resp.StatusCode)
					return io.ReadAll(resp.Body)
				}
				errBody, _ := io.ReadAll(resp.Body)
				logger.Warn("workspace: direct API call returned non-2xx status", "status", resp.StatusCode, "body", string(errBody))
				return nil, fmt.Errorf("status %d: %s", resp.StatusCode, string(errBody))
			}
			logger.Info("workspace: direct API call failed, falling back to proxy", "error", directErr, "endpoint", endpoint)
		}
	}

	// Fallback: K8s Proxy
	logger.Debug("workspace: calling API via k8s proxy", "method", method, "endpoint", endpoint)
	reqProxy := clientset.CoreV1().RESTClient().Verb(method).
		RequestURI(fmt.Sprintf("/api/v1/namespaces/%s/pods/%s:%d/proxy%s", namespace, podName, config.Config.LlmServerWorkspacePort, endpoint))

	for k, v := range queryParams {
		reqProxy.Param(k, v)
	}

	if bodyBytes != nil {
		reqProxy.Body(bodyBytes)
		reqProxy.SetHeader("Content-Type", "application/json")
	}
	reqProxy.SetHeader("X-Workspace-Token", workspaceToken)

	resultRaw, err := reqProxy.Do(ctx.GetContext()).Raw()
	if err != nil {
		logger.Error("workspace: proxy API call failed", "method", method, "endpoint", endpoint, "error", err)
		return nil, fmt.Errorf("proxy call failed: %w", err)
	}

	logger.Debug("workspace: API call successful via proxy", "method", method, "endpoint", endpoint)
	return resultRaw, nil
}

func (w *workspaceManager) callWorkspaceAPIStream(ctx *security.RequestContext, accountId string, method string, endpoint string, queryParams map[string]string, body any) (io.ReadCloser, error) {
	if accountId == "" {
		return nil, fmt.Errorf("workspace: accountId is required")
	}
	if !config.Config.LlmServerWorkspaceEnabled {
		return nil, fmt.Errorf("workspace feature is disabled")
	}

	// Prepare request body
	var bodyBytes []byte
	if body != nil {
		var err error
		bodyBytes, err = json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("failed to marshal body: %w", err)
		}
	}

	// Helper to build URL with query params
	buildURL := func(baseURL string) string {
		if len(queryParams) == 0 {
			return baseURL
		}
		u, err := url.Parse(baseURL)
		if err != nil {
			return baseURL
		}
		q := u.Query()
		for k, v := range queryParams {
			q.Set(k, v)
		}
		u.RawQuery = q.Encode()
		return u.String()
	}

	// Local mode: bypass K8s entirely
	if localURL := config.Config.LlmServerWorkspaceLocalUrl; localURL != "" {
		directUrl := buildURL(fmt.Sprintf("%s%s", strings.TrimRight(localURL, "/"), endpoint))
		req, err := http.NewRequestWithContext(ctx.GetContext(), method, directUrl, bytes.NewBuffer(bodyBytes))
		if err != nil {
			return nil, fmt.Errorf("failed to build local stream request: %w", err)
		}
		req.Header.Set("Content-Type", "application/json")
		localClient := &http.Client{}
		resp, err := localClient.Do(req)
		if err != nil {
			return nil, fmt.Errorf("local workspace stream call failed: %w", err)
		}
		if resp.StatusCode != http.StatusOK {
			defer func() { _ = resp.Body.Close() }()
			errBody, _ := io.ReadAll(resp.Body)
			return nil, fmt.Errorf("local workspace stream returned status %d: %s", resp.StatusCode, string(errBody))
		}
		return resp.Body, nil
	}

	clientset, err := getKubeClient(100, 200)
	if err != nil {
		return nil, err
	}

	namespace := config.Config.LlmServerCodeAgentNamespace
	podName := fmt.Sprintf("workspace-%s", strings.ToLower(accountId))

	// 1. Get Pod Info
	pod, err := clientset.CoreV1().Pods(namespace).Get(ctx.GetContext(), podName, metav1.GetOptions{})
	if err != nil {
		return nil, fmt.Errorf("failed to get pod info: %w", err)
	}

	if terminal, reason := isPodInTerminalState(pod); terminal {
		return nil, fmt.Errorf("workspace pod is not ready: %s", reason)
	}

	podIP := pod.Status.PodIP
	if podIP == "" {
		return nil, fmt.Errorf("workspace pod is not ready: pod IP is not assigned yet")
	}

	workspaceToken := ""
	for _, envVar := range pod.Spec.Containers[0].Env {
		if envVar.Name == ENV_NB_WORKSPACE_TOKEN {
			workspaceToken = envVar.Value
			break
		}
	}

	// 2. Try Direct IP Execution
	directUrl := buildURL(fmt.Sprintf("http://%s:%d%s", podIP, config.Config.LlmServerWorkspacePort, endpoint))

	httpClient := &http.Client{
		Transport: &http.Transport{
			ResponseHeaderTimeout: 200 * time.Millisecond,
		},
		Timeout: 500 * time.Millisecond,
	}
	req, err := http.NewRequestWithContext(ctx.GetContext(), method, directUrl, bytes.NewBuffer(bodyBytes))
	if err == nil {
		req.Header.Set("Content-Type", "application/json")
		if workspaceToken != "" {
			req.Header.Set("X-Workspace-Token", workspaceToken)
		}
		resp, directErr := httpClient.Do(req)
		if directErr == nil {
			if resp.StatusCode == http.StatusOK {
				return resp.Body, nil
			}
			defer func() { _ = resp.Body.Close() }()
			errBody, _ := io.ReadAll(resp.Body)
			return nil, fmt.Errorf("status %d: %s", resp.StatusCode, string(errBody))
		}
		ctx.GetLogger().Info("workspace: direct API stream call failed, falling back to proxy", "error", directErr)
	}

	// 3. Fallback: K8s Proxy
	reqProxy := clientset.CoreV1().RESTClient().Verb(method).
		RequestURI(fmt.Sprintf("/api/v1/namespaces/%s/pods/%s:%d/proxy%s", namespace, podName, config.Config.LlmServerWorkspacePort, endpoint))

	for k, v := range queryParams {
		reqProxy.Param(k, v)
	}

	if bodyBytes != nil {
		reqProxy.Body(bodyBytes)
		reqProxy.SetHeader("Content-Type", "application/json")
	}
	reqProxy.SetHeader("X-Workspace-Token", workspaceToken)

	stream, err := reqProxy.Stream(ctx.GetContext())

	if err != nil {
		return nil, fmt.Errorf("proxy stream failed: %w", err)
	}

	return stream, nil
}

func (w *workspaceManager) ListFiles(ctx *security.RequestContext, accountId string, conversationId string, path string) (any, error) {
	ctx.GetLogger().Info("workspace: listing files", "path", path, "account_id", accountId, "conversation_id", conversationId)
	queryParams := map[string]string{
		"path": path,
	}
	if conversationId != "" {
		queryParams["conversation_id"] = conversationId
	}
	// Pod expects GET /api/v1/files
	resp, err := w.callWorkspaceAPI(ctx, accountId, "GET", "/api/v1/files", queryParams, nil)
	if err != nil {
		return nil, err
	}

	var result map[string]any
	if err := json.Unmarshal(resp, &result); err != nil {
		return nil, err
	}
	return result["files"], nil
}

func (w *workspaceManager) ReadFile(ctx *security.RequestContext, accountId string, conversationId string, path string) ([]byte, error) {
	ctx.GetLogger().Info("workspace: reading file", "path", path, "account_id", accountId, "conversation_id", conversationId)
	queryParams := map[string]string{
		"path": path,
	}
	if conversationId != "" {
		queryParams["conversation_id"] = conversationId
	}
	// Pod expects GET /api/v1/files/content
	return w.callWorkspaceAPI(ctx, accountId, "GET", "/api/v1/files/content", queryParams, nil)
}

func (w *workspaceManager) ReadFileStream(ctx *security.RequestContext, accountId string, conversationId string, path string) (io.ReadCloser, error) {
	ctx.GetLogger().Info("workspace: reading file stream", "path", path, "account_id", accountId, "conversation_id", conversationId)
	queryParams := map[string]string{
		"path": path,
	}
	if conversationId != "" {
		queryParams["conversation_id"] = conversationId
	}
	// Pod expects GET /api/v1/files/content
	return w.callWorkspaceAPIStream(ctx, accountId, "GET", "/api/v1/files/content", queryParams, nil)
}

func (w *workspaceManager) BatchReadFile(ctx *security.RequestContext, accountId string, conversationId string, paths []string) (any, error) {
	ctx.GetLogger().Info("workspace: batch reading files", "paths", paths, "account_id", accountId, "conversation_id", conversationId)
	body := map[string]any{
		"paths": paths,
	}
	if conversationId != "" {
		body["conversation_id"] = conversationId
	}

	resp, err := w.callWorkspaceAPI(ctx, accountId, "POST", "/api/v1/files/read-batch", nil, body)
	if err != nil {
		return nil, err
	}

	var result map[string]any
	if err := json.Unmarshal(resp, &result); err != nil {
		return nil, err
	}
	return result["files"], nil
}

func (w *workspaceManager) SaveFile(ctx *security.RequestContext, accountId string, conversationId string, path string, content string) error {
	ctx.GetLogger().Info("workspace: saving file", "path", path, "account_id", accountId, "conversation_id", conversationId, "size", len(content))
	body := map[string]string{
		"path":    path,
		"content": content,
	}
	if conversationId != "" {
		body["conversation_id"] = conversationId
	}
	_, err := w.callWorkspaceAPI(ctx, accountId, "POST", "/api/v1/files/save", nil, body)
	return err
}

func (w *workspaceManager) DeleteFile(ctx *security.RequestContext, accountId string, conversationId string, path string) error {
	ctx.GetLogger().Info("workspace: deleting file", "path", path, "account_id", accountId, "conversation_id", conversationId)
	queryParams := map[string]string{
		"path": path,
	}
	if conversationId != "" {
		queryParams["conversation_id"] = conversationId
	}
	_, err := w.callWorkspaceAPI(ctx, accountId, "DELETE", "/api/v1/files/delete", queryParams, nil)
	return err
}

func (w *workspaceManager) executeDirect(ctx *security.RequestContext, url string, data []byte, token string) (string, error) {
	req, err := http.NewRequestWithContext(ctx.GetContext(), "POST", url, bytes.NewBuffer(data))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("X-Workspace-Token", token)
	}

	resp, err := w.httpClient.Do(req)
	if err != nil {
		return "", err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("direct execution returned status: %d", resp.StatusCode)
	}

	var result executeResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", err
	}

	return result.Response, nil
}

// Helpers

func getKubeClient(qps float32, burst int) (*kubernetes.Clientset, error) {
	var (
		restCfg *rest.Config
		err     error
	)

	// Explicit override: load from a kubeconfig file (and optional context). This
	// takes precedence over in-cluster config so llm-server can target a remote
	// cluster for workspace pod operations even when running inside a pod.
	if override := config.Config.LlmServerWorkspaceKubeconfigPath; override != "" {
		loadingRules := &clientcmd.ClientConfigLoadingRules{ExplicitPath: override}
		overrides := &clientcmd.ConfigOverrides{
			CurrentContext: config.Config.LlmServerWorkspaceKubeContext,
		}
		restCfg, err = clientcmd.NewNonInteractiveDeferredLoadingClientConfig(loadingRules, overrides).ClientConfig()
		if err != nil {
			return nil, fmt.Errorf("failed to load workspace kubeconfig %q: %w", override, err)
		}
	} else {
		// Try in-cluster config first, then fall back to default kubeconfig discovery.
		restCfg, err = rest.InClusterConfig()
		if err != nil {
			kubeconfig := os.Getenv("KUBECONFIG")
			if kubeconfig == "" {
				kubeconfig = clientcmd.RecommendedHomeFile
			}
			if ctxName := config.Config.LlmServerWorkspaceKubeContext; ctxName != "" {
				loadingRules := &clientcmd.ClientConfigLoadingRules{ExplicitPath: kubeconfig}
				overrides := &clientcmd.ConfigOverrides{CurrentContext: ctxName}
				restCfg, err = clientcmd.NewNonInteractiveDeferredLoadingClientConfig(loadingRules, overrides).ClientConfig()
			} else {
				restCfg, err = clientcmd.BuildConfigFromFlags("", kubeconfig)
			}
			if err != nil {
				return nil, fmt.Errorf("failed to create kubernetes config: %w", err)
			}
		}
	}

	restCfg.QPS = qps
	restCfg.Burst = burst

	clientset, err := kubernetes.NewForConfig(restCfg)
	if err != nil {
		return nil, fmt.Errorf("failed to create kubernetes client: %w", err)
	}
	return clientset, nil
}
