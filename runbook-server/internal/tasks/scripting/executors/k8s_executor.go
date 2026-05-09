package executors

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log/slog"
	nudgebeeConfig "nudgebee/runbook/config"
	"os"
	"strings"
	"time"

	taskTypes "nudgebee/runbook/internal/tasks/types"

	"github.com/google/uuid"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors" // Import for error checking
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
)

// KubernetesExecutor implements ScriptExecutor using Kubernetes Jobs.
type KubernetesExecutor struct {
	Client    kubernetes.Interface
	Namespace string
}

func NewKubernetesExecutor() (*KubernetesExecutor, error) {
	// Use client-go's standard way to load config, mimicking kubectl's behavior.
	// This will try KUBECONFIG env var, ~/.kube/config, and then in-cluster config.
	loadingRules := clientcmd.NewDefaultClientConfigLoadingRules()
	configOverrides := &clientcmd.ConfigOverrides{}

	// Allow overriding the context (profile) via environment variable
	if ctxName := os.Getenv("KUBE_CONTEXT"); ctxName != "" {
		configOverrides.CurrentContext = ctxName
	}

	clientConfig := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(loadingRules, configOverrides)

	config, err := clientConfig.ClientConfig()
	if err != nil {
		return nil, fmt.Errorf("failed to load kubernetes config: %w", err)
	}

	// dummy variable to satisfy linter for unused import 'k8s.io/client-go/rest'
	// as its type rest.Config is used indirectly, but not direct functions/variables.
	_ = rest.Config{}

	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		return nil, fmt.Errorf("failed to create kubernetes client: %w", err)
	}

	namespace := nudgebeeConfig.Config.NudgebeeNamespace
	if namespace == "" {
		namespace = "default"
	}

	return &KubernetesExecutor{
		Client:    clientset,
		Namespace: namespace,
	}, nil
}

func (e *KubernetesExecutor) Execute(taskCtx taskTypes.TaskContext, executionConfig ExecutionConfig) (string, error) {
	slog.Debug("K8sExecutor: Starting execution", "language", executionConfig.Language, "script", executionConfig.Script)
	// Generate a unique ID for this execution (used for Job and ConfigMap names)
	executionID := uuid.New().String()
	jobName := fmt.Sprintf("runbook-script-%s", executionID)
	configMapName := jobName
	secretName := jobName
	namespaceName := e.Namespace
	if executionConfig.K8sNamespace != nil && *executionConfig.K8sNamespace != "" {
		namespaceName = *executionConfig.K8sNamespace
	}

	// Determine Image and Command
	var image string
	var command []string
	var scriptFileName string

	switch executionConfig.Language {
	case "bash":
		scriptFileName = "script.sh"
		image = nudgebeeConfig.Config.LlmServerShellImage
		command = []string{"bash", "/workspace/" + scriptFileName}
	case "sh":
		scriptFileName = "script.sh"
		image = nudgebeeConfig.Config.LlmServerShellImage
		command = []string{"sh", "/workspace/" + scriptFileName}
	case "javascript":
		scriptFileName = "script.js"
		image = nudgebeeConfig.Config.ScriptExecutorNodeImage
		command = []string{"node", "/workspace/" + scriptFileName}
	case "python":
		scriptFileName = "script.py"
		image = nudgebeeConfig.Config.LlmServerShellImage
		command = []string{"python3", "/workspace/" + scriptFileName}
	case "powershell":
		scriptFileName = "script.ps1"
		image = nudgebeeConfig.Config.ScriptExecutorPowerShellImage
		command = []string{"pwsh", "-NoProfile", "-File", "/workspace/" + scriptFileName}
	default:
		return "", fmt.Errorf("unsupported language: %s", executionConfig.Language)
	}

	scriptPath := "/workspace/" + scriptFileName

	// Override image if provided
	if executionConfig.K8sImage != "" {
		image = executionConfig.K8sImage
		// If image is overridden, we default to 'sh' entrypoint for safety if not known,
		// or rely on the user ensuring the image has the right interpreter.
		// To be robust, we try to invoke the interpreter directly assuming it's in PATH.
		switch executionConfig.Language {
		case "bash":
			command = []string{"bash", scriptPath}
		case "sh":
			command = []string{"sh", scriptPath}
		case "javascript":
			command = []string{"node", scriptPath}
		case "python":
			command = []string{"python3", scriptPath}
		case "powershell":
			command = []string{"pwsh", "-NoProfile", "-File", scriptPath}
		}
	}

	// Append arguments
	if len(executionConfig.Args) > 0 {
		command = append(command, executionConfig.Args...)
	}

	// Parse Resources
	resources := corev1.ResourceRequirements{}
	if executionConfig.K8sResources != nil {
		if executionConfig.K8sResources.CPURequest != "" {
			q, err := resource.ParseQuantity(executionConfig.K8sResources.CPURequest)
			if err == nil {
				if resources.Requests == nil {
					resources.Requests = make(corev1.ResourceList)
				}
				resources.Requests[corev1.ResourceCPU] = q
			} else {
				slog.Warn("K8sExecutor: Failed to parse CPURequest quantity", "value", executionConfig.K8sResources.CPURequest, "error", err)
			}
		}
		if executionConfig.K8sResources.MemoryRequest != "" {
			q, err := resource.ParseQuantity(executionConfig.K8sResources.MemoryRequest)
			if err == nil {
				if resources.Requests == nil {
					resources.Requests = make(corev1.ResourceList)
				}
				resources.Requests[corev1.ResourceMemory] = q
			} else {
				slog.Warn("K8sExecutor: Failed to parse MemoryRequest quantity", "value", executionConfig.K8sResources.MemoryRequest, "error", err)
			}
		}
		if executionConfig.K8sResources.CPULimit != "" {
			q, err := resource.ParseQuantity(executionConfig.K8sResources.CPULimit)
			if err == nil {
				if resources.Limits == nil {
					resources.Limits = make(corev1.ResourceList)
				}
				resources.Limits[corev1.ResourceCPU] = q
			} else {
				slog.Warn("K8sExecutor: Failed to parse CPULimit quantity", "value", executionConfig.K8sResources.CPULimit, "error", err)
			}
		}
		if executionConfig.K8sResources.MemoryLimit != "" {
			q, err := resource.ParseQuantity(executionConfig.K8sResources.MemoryLimit)
			if err == nil {
				if resources.Limits == nil {
					resources.Limits = make(corev1.ResourceList)
				}
				resources.Limits[corev1.ResourceMemory] = q
			} else {
				slog.Warn("K8sExecutor: Failed to parse MemoryLimit quantity", "value", executionConfig.K8sResources.MemoryLimit, "error", err)
			}
		}
	}

	// Create ConfigMap for Script
	scriptContent := executionConfig.Script
	if executionConfig.Language == "powershell" {
		scriptContent = BuildPowerShellConfigPrefix() + "\n" + executionConfig.Script
	}
	scriptCM := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name: configMapName,
		},
		Data: map[string]string{
			scriptFileName: scriptContent,
		},
	}
	_, err := e.Client.CoreV1().ConfigMaps(namespaceName).Create(taskCtx.GetContext(), scriptCM, metav1.CreateOptions{})
	if err != nil {
		return "", fmt.Errorf("failed to create configmap for script: %w", err)
	}

	// Create Secret for Environment Variables
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      secretName,
			Namespace: namespaceName,
		},
		StringData: executionConfig.Env,
		Type:       corev1.SecretTypeOpaque,
	}
	_, err = e.Client.CoreV1().Secrets(namespaceName).Create(taskCtx.GetContext(), secret, metav1.CreateOptions{})
	if err != nil {
		// Cleanup ConfigMap if Secret creation fails
		_ = e.Client.CoreV1().ConfigMaps(namespaceName).Delete(context.Background(), configMapName, metav1.DeleteOptions{})
		return "", fmt.Errorf("failed to create secret for environment variables: %w", err)
	}

	// Cleanup Resources on Exit
	defer func() {
		bgPolicy := metav1.DeletePropagationBackground
		if err := e.Client.BatchV1().Jobs(namespaceName).Delete(context.Background(), jobName, metav1.DeleteOptions{
			PropagationPolicy: &bgPolicy,
		}); err != nil {
			// It's common for resources to be already deleted or not found, so we could filter those,
			// but for now logging as Warn is safe.
			slog.Warn("K8sExecutor: Failed to delete Job", "jobName", jobName, "error", err)
		}

		// We do not manually delete ConfigMap and Secret here because we rely on OwnerReferences
		// which we add immediately after Job creation.
		// However, if Job creation FAILS, we must manually cleanup.
		// So we check if the job was created successfully? No, defer runs anyway.
		// If job was created, it has owner refs.
		// Actually, defer is fine to call Delete on Secret/CM. If they are already deleted by GC, it just errors 404 which is ignored.
		// So we can keep these for safety.
		if err := e.Client.CoreV1().ConfigMaps(namespaceName).Delete(context.Background(), configMapName, metav1.DeleteOptions{}); err != nil {
			if !errors.IsNotFound(err) {
				slog.Warn("K8sExecutor: Failed to delete ConfigMap", "name", configMapName, "namespace", namespaceName, "error", err)
			}
		}
		if err := e.Client.CoreV1().Secrets(namespaceName).Delete(context.Background(), secretName, metav1.DeleteOptions{}); err != nil {
			if !errors.IsNotFound(err) {
				slog.Warn("K8sExecutor: Failed to delete Secret", "name", secretName, "namespace", namespaceName, "error", err)
			}
		}
	}()

	// Security Context
	nonRoot := int64(1000)
	allowPrivEscalation := false
	readOnlyRoot := true

	// Calculate ActiveDeadlineSeconds from Context Deadline
	var activeDeadlineSeconds *int64
	if deadline, ok := taskCtx.GetContext().Deadline(); ok {
		timeoutDuration := time.Until(deadline)
		if timeoutDuration > 0 {
			seconds := int64(timeoutDuration.Seconds())
			activeDeadlineSeconds = &seconds
		}
	}

	// TTL for cleanup (fallback if server crashes)
	ttlSeconds := int32(300) // 5 minutes after finish

	job := &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{
			Name:      jobName,
			Namespace: namespaceName,
		},
		Spec: batchv1.JobSpec{
			BackoffLimit:            func(i int32) *int32 { return &i }(0), // No retries
			ActiveDeadlineSeconds:   activeDeadlineSeconds,
			TTLSecondsAfterFinished: &ttlSeconds,
			Template: corev1.PodTemplateSpec{
				Spec: corev1.PodSpec{
					RestartPolicy: corev1.RestartPolicyNever,
					Containers: []corev1.Container{
						{
							Name:       "script",
							Image:      image,
							Command:    command,
							WorkingDir: "/workspace",
							EnvFrom: []corev1.EnvFromSource{
								{
									SecretRef: &corev1.SecretEnvSource{
										LocalObjectReference: corev1.LocalObjectReference{
											Name: secretName,
										},
									},
								},
							},
							Env: []corev1.EnvVar{
								{
									Name:  "HOME",
									Value: "/tmp",
								},
							},
							VolumeMounts: []corev1.VolumeMount{
								{
									Name:      "script-vol",
									MountPath: "/workspace",
									ReadOnly:  true,
								},
								{
									Name:      "tmp-vol",
									MountPath: "/tmp",
									ReadOnly:  false,
								},
							},
							SecurityContext: &corev1.SecurityContext{
								RunAsUser:                &nonRoot,
								AllowPrivilegeEscalation: &allowPrivEscalation,
								ReadOnlyRootFilesystem:   &readOnlyRoot,
								Capabilities: &corev1.Capabilities{
									Drop: []corev1.Capability{"ALL"},
								},
							},
							Resources: resources,
						},
					},
					Volumes: []corev1.Volume{
						{
							Name: "script-vol",
							VolumeSource: corev1.VolumeSource{
								ConfigMap: &corev1.ConfigMapVolumeSource{
									LocalObjectReference: corev1.LocalObjectReference{
										Name: configMapName,
									},
								},
							},
						},
						{
							Name: "tmp-vol",
							VolumeSource: corev1.VolumeSource{
								EmptyDir: &corev1.EmptyDirVolumeSource{},
							},
						},
					},
				},
			},
		},
	}

	// Create Job
	createdJob, err := e.Client.BatchV1().Jobs(namespaceName).Create(taskCtx.GetContext(), job, metav1.CreateOptions{})
	if err != nil {
		return "", fmt.Errorf("failed to create job: %w", err)
	}
	slog.Info("K8sExecutor: Job created", "jobName", jobName, "executionID", executionID)

	// Patch Secret and ConfigMap with OwnerReference to ensure cascading deletion
	// This handles the case where the server crashes and defer is not executed.
	// The Job's TTL will clean up the Job, and the OwnerRef will clean up the Secret/CM.
	// We run these patches in background context to ensure they succeed even if main context is tight
	patchCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Construct the patch manually to avoid struct marshalling complexity for a simple merge patch
	patchData := []byte(fmt.Sprintf(`{"metadata":{"ownerReferences":[{"apiVersion":"batch/v1","kind":"Job","name":"%s","uid":"%s","blockOwnerDeletion":true,"controller":true}]}}`, createdJob.Name, createdJob.UID))

	if _, err := e.Client.CoreV1().Secrets(namespaceName).Patch(patchCtx, secretName, types.StrategicMergePatchType, patchData, metav1.PatchOptions{}); err != nil {
		slog.Warn("K8sExecutor: Failed to patch Secret with OwnerReference", "secretName", secretName, "error", err)
	}
	if _, err := e.Client.CoreV1().ConfigMaps(namespaceName).Patch(patchCtx, configMapName, types.StrategicMergePatchType, patchData, metav1.PatchOptions{}); err != nil {
		slog.Warn("K8sExecutor: Failed to patch ConfigMap with OwnerReference", "configMapName", configMapName, "error", err)
	}

	// Wait for Job completion
	var podName string
	slog.Info("K8sExecutor: Waiting for pod creation...")

	const podCreationMaxRetries = 30
	var podFound bool

podCreationLoop:
	for i := 0; i < podCreationMaxRetries; i++ {
		select {
		case <-taskCtx.GetContext().Done():
			return "", taskCtx.GetContext().Err()
		case <-time.After(1 * time.Second):
			pods, err := e.Client.CoreV1().Pods(namespaceName).List(taskCtx.GetContext(), metav1.ListOptions{
				LabelSelector: fmt.Sprintf("job-name=%s", jobName),
			})
			if err == nil && len(pods.Items) > 0 {
				podName = pods.Items[0].Name
				slog.Info("K8sExecutor: Pod found", "podName", podName)
				podFound = true
				break podCreationLoop
			}
		}
	}

	if !podFound {
		statusDetails := e.getPodStatusDetails(context.Background(), namespaceName, fmt.Sprintf("job-name=%s", jobName))
		return "", fmt.Errorf("timeout waiting for pod creation%s", statusDetails)
	}
	slog.Info("K8sExecutor: Waiting for pod to finish...", "podName", podName)
	var podFinished = false
	var executionErr error

podFinishLoop:
	for {
		select {
		case <-taskCtx.GetContext().Done():
			return "", taskCtx.GetContext().Err()

		case <-time.After(1 * time.Second):
			pod, err := e.Client.CoreV1().Pods(namespaceName).Get(taskCtx.GetContext(), podName, metav1.GetOptions{})
			if err != nil {
				slog.Warn("K8sExecutor: Error getting pod status, retrying", "error", err)
				continue
			}

			if pod.Status.Phase == corev1.PodSucceeded || pod.Status.Phase == corev1.PodFailed {
				slog.Info("K8sExecutor: Pod finished", "phase", pod.Status.Phase, "podName", podName)
				if pod.Status.Phase == corev1.PodFailed {
					errMsg := "K8sExecutor: Pod failed:\n"
					for _, cs := range pod.Status.ContainerStatuses {
						if cs.State.Terminated != nil {
							errMsg += fmt.Sprintf("  Container %s: State=%v, ExitCode=%d, Reason=%s, Message=%s\n", cs.Name, cs.State, cs.State.Terminated.ExitCode, cs.State.Terminated.Reason, cs.State.Terminated.Message)
						} else {
							errMsg += fmt.Sprintf("  Container %s: State=%v (Not Terminated)\n", cs.Name, cs.State)
						}
					}
					slog.Error("K8sExecutor: Pod execution failed", "podName", podName, "details", errMsg)
					executionErr = fmt.Errorf("%s", errMsg)
				}
				podFinished = true
				break podFinishLoop
			}
		}
	}

	if !podFinished {
		statusDetails := e.getPodStatusDetails(context.Background(), namespaceName, fmt.Sprintf("job-name=%s", jobName))
		logs, _ := e.getPodLogs(context.Background(), namespaceName, podName, "")
		output := ""
		if logs != "" {
			output = fmt.Sprintf("\nOutput: %s", logs)
		}
		return logs, fmt.Errorf("pod did not finish successfully or timed out%s%s", statusDetails, output)
	}

	// Fetch Logs
	slog.Info("K8sExecutor: Fetching logs", "podName", podName)
	logs, err := e.getPodLogs(taskCtx.GetContext(), namespaceName, podName, "")
	if err != nil {
		// If we failed to stream logs, log the error but prefer returning the pod failure error if it exists
		slog.Error("K8sExecutor: error in opening logs stream", "error", err)
		if executionErr != nil {
			return "", executionErr
		}
		return "", fmt.Errorf("error in fetching logs: %w", err)
	}

	if executionErr != nil {
		slog.Error("K8sExecutor: Execution failed with logs", "podName", podName, "logs", logs)
		// Include the logs in the error message so callers can see the actual error
		executionErr = fmt.Errorf("%w\nOutput: %s", executionErr, logs)
	}

	return logs, executionErr
}

func (e *KubernetesExecutor) getPodStatusDetails(ctx context.Context, namespace, labelSelector string) string {
	pods, err := e.Client.CoreV1().Pods(namespace).List(ctx, metav1.ListOptions{
		LabelSelector: labelSelector,
	})
	if err != nil || len(pods.Items) == 0 {
		return ""
	}

	pod := pods.Items[0]
	var sb strings.Builder
	fmt.Fprintf(&sb, "\nPod %s: Phase=%s", pod.Name, pod.Status.Phase)
	if pod.Status.Reason != "" {
		fmt.Fprintf(&sb, ", Reason=%s", pod.Status.Reason)
	}
	if pod.Status.Message != "" {
		fmt.Fprintf(&sb, ", Message=%s", pod.Status.Message)
	}

	for _, cs := range pod.Status.ContainerStatuses {
		if cs.State.Waiting != nil {
			fmt.Fprintf(&sb, "\n  Container %s waiting: %s - %s", cs.Name, cs.State.Waiting.Reason, cs.State.Waiting.Message)
		}
		if cs.State.Terminated != nil {
			fmt.Fprintf(&sb, "\n  Container %s terminated: ExitCode=%d, Reason=%s, Message=%s", cs.Name, cs.State.Terminated.ExitCode, cs.State.Terminated.Reason, cs.State.Terminated.Message)
		}
	}

	// Add events
	events, err := e.Client.CoreV1().Events(namespace).List(ctx, metav1.ListOptions{
		FieldSelector: "involvedObject.name=" + pod.Name,
	})
	if err == nil && len(events.Items) > 0 {
		sb.WriteString("\nRecent Pod Events:")
		for _, event := range events.Items {
			fmt.Fprintf(&sb, "\n  - %s: %s", event.Reason, event.Message)
		}
	}

	return sb.String()
}

func (e *KubernetesExecutor) getPodLogs(ctx context.Context, namespace, podName, containerName string) (string, error) {
	if podName == "" {
		return "", nil
	}
	logOptions := &corev1.PodLogOptions{}
	if containerName != "" {
		logOptions.Container = containerName
	}
	req := e.Client.CoreV1().Pods(namespace).GetLogs(podName, logOptions)
	podLogs, err := req.Stream(ctx)
	if err != nil {
		return "", err
	}
	defer func() {
		if err := podLogs.Close(); err != nil {
			slog.Warn("K8sExecutor: Failed to close pod logs stream", "error", err)
		}
	}()

	buf := new(bytes.Buffer)
	_, err = io.Copy(buf, podLogs)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(buf.String()), nil
}
