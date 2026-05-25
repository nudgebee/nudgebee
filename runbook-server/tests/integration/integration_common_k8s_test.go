package integration_test

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"os/exec" // Added
	"strings" // Ensure strings is present if needed for k8sExecuteCli
	"testing"
	"time"

	"github.com/stretchr/testify/suite"

	"nudgebee/runbook/config"
	"nudgebee/runbook/services/security"
)

// k8sExecuteCli executes a kubectl command directly using os/exec.
func (s *IntegrationTestSuite) k8sExecuteCli(ctx context.Context, stdinContent string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "kubectl", args...)

	if stdinContent != "" {
		cmd.Stdin = strings.NewReader(stdinContent)
	}

	// Pass through KUBECONFIG if set in environment
	if kubeconfig := os.Getenv("KUBECONFIG"); kubeconfig != "" {
		cmd.Env = append(os.Environ(), "KUBECONFIG="+kubeconfig)
	}

	output, err := cmd.CombinedOutput()
	outputStr := strings.TrimSpace(string(output))
	if err != nil {
		return outputStr, fmt.Errorf("kubectl command failed: %w\nOutput: %s", err, outputStr)
	}
	return outputStr, nil
}

// newRequestContext creates a security.RequestContext for integration tests.
func (s *IntegrationTestSuite) newRequestContext() *security.RequestContext {
	return security.NewRequestContext(context.Background(), security.NewSecurityContextForTenantAccountAdmin(testTenantID, testUserID, []string{testK8sAccountID}), slog.Default(), nil, nil)
}
func (s *IntegrationTestSuite) k8sCreateDeployment(namespace, name string, replicas int, cpuRequest, memRequest, cpuLimit, memLimit string) {
	deploymentYAML := fmt.Sprintf(`
apiVersion: apps/v1
kind: Deployment
metadata:
  name: %s
  namespace: %s
spec:
  replicas: %d
  selector:
    matchLabels:
      app: %s
  template:
    metadata:
      labels:
        app: %s
    spec:
      containers:
      - name: nginx
        image: nginx:1.14.2
        ports:
        - containerPort: 80
        resources:
          limits:
            cpu: %s
            memory: %s
          requests:
            cpu: %s
            memory: %s
`,
		name, namespace, replicas, name, name, cpuLimit, memLimit, cpuRequest, memRequest)

	_, err := s.k8sExecuteCli(s.T().Context(), deploymentYAML, "apply", "-f", "-")
	s.Require().NoError(err, "Failed to create deployment %s/%s", namespace, name)

	// Wait for deployment to be ready
	s.k8sWaitForDeploymentReady(namespace, name, 120*time.Second)
	s.T().Logf("Deployment %s/%s created and ready.", namespace, name)
}

// k8sDeleteDeployment deletes a test NGINX deployment.
func (s *IntegrationTestSuite) k8sDeleteDeployment(namespace, name string) {
	_, err := s.k8sExecuteCli(s.T().Context(), "", "delete", "deployment", name, "-n", namespace, "--ignore-not-found=true") // Add ignore-not-found
	s.Require().NoError(err, "Failed to delete deployment %s/%s", namespace, name)
	s.T().Logf("Deployment %s/%s deleted.", namespace, name)
}

// k8sGetDeploymentResources retrieves CPU/Memory requests and limits for a container in a deployment.
func (s *IntegrationTestSuite) k8sGetDeploymentResources(namespace, name, containerName string) (cpuReq, memReq, cpuLimit, memLimit string, err error) {
	output, err := s.k8sExecuteCli(s.T().Context(), "", "get", "deployment", name, "-n", namespace, "-o", fmt.Sprintf("jsonpath={.spec.template.spec.containers[?(@.name==\"%s\")].resources}", containerName))
	if err != nil {
		fmt.Println("k8s cli error", output, err)
		return "", "", "", "", err
	}

	var resources struct {
		Limits   map[string]string `json:"limits"`
		Requests map[string]string `json:"requests"`
	}
	// kubectl jsonpath might return a single item as plain object, or an array if multiple matches.
	// Assume single container match for now.
	if err := json.Unmarshal([]byte(output), &resources); err != nil {
		// If unmarshal as direct object fails, try array of objects
		var resourcesArray []struct {
			Limits   map[string]string `json:"limits"`
			Requests map[string]string `json:"requests"`
		}
		if err := json.Unmarshal([]byte(output), &resourcesArray); err == nil && len(resourcesArray) > 0 {
			resources = resourcesArray[0]
		} else {
			return "", "", "", "", fmt.Errorf("failed to parse resources output: %w, output: %s", err, output)
		}
	}

	cpuReq = resources.Requests["cpu"]
	memReq = resources.Requests["memory"]
	cpuLimit = resources.Limits["cpu"]
	memLimit = resources.Limits["memory"]
	return cpuReq, memReq, cpuLimit, memLimit, nil
}

// k8sCreatePV creates a hostPath PV for testing.
func (s *IntegrationTestSuite) k8sCreatePV(name, size, className string) {
	pvYAML := fmt.Sprintf(`
apiVersion: v1
kind: PersistentVolume
metadata:
  name: %s
spec:
  storageClassName: %s
  capacity:
    storage: %s
  accessModes:
    - ReadWriteOnce
  hostPath:
    path: "/tmp/%s"
`, name, className, size, name)

	_, err := s.k8sExecuteCli(s.T().Context(), pvYAML, "apply", "-f", "-")
	s.Require().NoError(err, "Failed to create PV %s", name)
	s.T().Logf("PV %s created with size %s and class %s.", name, size, className)
}

// k8sDeletePV deletes a test PV.
func (s *IntegrationTestSuite) k8sDeletePV(name string) {
	_, err := s.k8sExecuteCli(s.T().Context(), "", "delete", "pv", name, "--ignore-not-found=true")
	s.Require().NoError(err, "Failed to delete PV %s", name)
	s.T().Logf("PV %s deleted.", name)
}

// k8sCreatePVC creates a simple PVC for testing.
func (s *IntegrationTestSuite) k8sCreatePVC(namespace, name, size, className string) {
	pvcYAML := fmt.Sprintf(`
apiVersion: v1
kind: PersistentVolumeClaim
metadata:
  name: %s
  namespace: %s
spec:
  storageClassName: %s
  accessModes:
    - ReadWriteOnce
  resources:
    requests:
      storage: %s
`,
		name, namespace, className, size)

	_, err := s.k8sExecuteCli(s.T().Context(), pvcYAML, "apply", "-f", "-")
	s.Require().NoError(err, "Failed to create PVC %s/%s", namespace, name)
	s.T().Logf("PVC %s/%s created with size %s and class %s.", namespace, name, size, className)
}

// k8sDeletePVC deletes a test PVC.
func (s *IntegrationTestSuite) k8sDeletePVC(namespace, name string) {
	_, err := s.k8sExecuteCli(s.T().Context(), "", "delete", "pvc", name, "-n", namespace, "--ignore-not-found=true") // Add ignore-not-found
	s.Require().NoError(err, "Failed to delete PVC %s/%s", namespace, name)
	s.T().Logf("PVC %s/%s deleted.", namespace, name)
}

// k8sGetPVCSize retrieves the storage request size of a PVC.
func (s *IntegrationTestSuite) k8sGetPVCSize(namespace, name string) (string, error) {
	output, err := s.k8sExecuteCli(s.T().Context(), "", "get", "pvc", name, "-n", namespace, "-o", "jsonpath={.spec.resources.requests.storage}")
	if err != nil {
		return "", err
	}
	return output, nil
}

// k8sGetPodNames retrieves a list of pod names for a given workload.

func (s *IntegrationTestSuite) k8sGetPodNames(namespace, kind, name string) ([]string, error) {

	var output string

	var err error

	if strings.ToLower(kind) == "pod" {

		// If input is already a pod name, return it directly in a slice

		return []string{name}, nil

	} else {

		// Get pods managed by a deployment/statefulset/etc.

		output, err = s.k8sExecuteCli(s.T().Context(), "", "get", "pods", "-l", fmt.Sprintf("app=%s", name), "-n", namespace, "-o", "jsonpath={.items[*].metadata.name}")

	}

	if err != nil {

		return nil, err

	}

	if output == "" {

		return []string{}, nil

	}

	return strings.Fields(output), nil // Splits string by whitespace

}

// k8sWaitForDeploymentReplicas waits for a deployment to reach a desired replica count.
func (s *IntegrationTestSuite) k8sWaitForDeploymentReplicas(namespace, name string, expected int, timeout time.Duration) {
	s.Require().Eventuallyf(func() bool {
		output, err := s.k8sExecuteCli(s.T().Context(), "", "get", "deployment", name, "-n", namespace, "-o", "jsonpath={.status.readyReplicas}")
		if err != nil {
			return false
		}

		// If output is empty, it means 0 replicas are ready (field is missing in status)
		if output == "" {
			return expected == 0
		}

		var ready int64
		_, err = fmt.Sscanf(output, "%d", &ready)
		if err != nil {
			s.T().Logf("Failed to parse ready replicas from output '%s': %v", output, err)
			return false
		}
		return ready == int64(expected)
	}, timeout, 5*time.Second, "Deployment %s/%s did not reach %d ready replicas within %v", namespace, name, expected, timeout)
}

// k8sWaitForPodDeletion waits for a pod to be deleted.
func (s *IntegrationTestSuite) k8sWaitForPodDeletion(namespace, podName string, timeout time.Duration) {
	s.Require().Eventuallyf(func() bool {
		_, err := s.k8sExecuteCli(s.T().Context(), "", "get", "pod", podName, "-n", namespace)
		return err != nil && strings.Contains(err.Error(), "NotFound") // Expecting NotFound error
	}, timeout, 2*time.Second, "Pod %s/%s was not deleted within %v", namespace, podName, timeout)
}

// k8sWaitForDeploymentReady waits for a deployment to be ready (all replicas up).
func (s *IntegrationTestSuite) k8sWaitForDeploymentReady(namespace, name string, timeout time.Duration) {
	var lastOutput string
	s.Require().Eventuallyf(func() bool {
		output, err := s.k8sExecuteCli(s.T().Context(), "", "get", "deployment", name, "-n", namespace, "-o", `jsonpath={.status.conditions[?(@.type=="Available")].status}`)
		if err != nil {
			lastOutput = fmt.Sprintf("Error: %v, Output: %s", err, output)
			return false
		}
		// Robustly trim quotes and whitespace
		cleanedOutput := strings.Trim(output, "'\"\n\r\t %")
		lastOutput = cleanedOutput
		return cleanedOutput == "True"
	}, timeout, 5*time.Second, "Deployment %s/%s did not become ready within %v. Last output: %s", namespace, name, timeout, &lastOutput) // Pass pointer to lastOutput
}

func (s *IntegrationTestSuite) k8sWaitForPVCBound(namespace, name string, timeout time.Duration) {
	s.Require().Eventuallyf(func() bool {
		output, err := s.k8sExecuteCli(s.T().Context(), "", "get", "pvc", name, "-n", namespace, "-o", "jsonpath={.status.phase}")
		if err != nil {
			return false
		}
		return output == "Bound"
	}, timeout, 2*time.Second, "PVC %s/%s did not become Bound within %v", namespace, name, timeout)
}

// k8sCreateStorageClass creates a StorageClass for testing.
func (s *IntegrationTestSuite) k8sCreateStorageClass(name string) {
	scYAML := fmt.Sprintf(`
apiVersion: storage.k8s.io/v1
kind: StorageClass
metadata:
  name: %s
provisioner: kubernetes.io/no-provisioner
volumeBindingMode: Immediate
allowVolumeExpansion: true
`, name)

	_, err := s.k8sExecuteCli(s.T().Context(), scYAML, "apply", "-f", "-")
	s.Require().NoError(err, "Failed to create StorageClass %s", name)
	s.T().Logf("StorageClass %s created.", name)
}

// k8sDeleteStorageClass deletes a test StorageClass.
func (s *IntegrationTestSuite) k8sDeleteStorageClass(name string) {
	_, err := s.k8sExecuteCli(s.T().Context(), "", "delete", "sc", name, "--ignore-not-found=true")
	s.Require().NoError(err, "Failed to delete StorageClass %s", name)
	s.T().Logf("StorageClass %s deleted.", name)
}

// IntegrationK8sTestSuite is a suite for K8s-specific integration tests.
type IntegrationK8sTestSuite struct {
	IntegrationTestSuite
}

func (s *IntegrationK8sTestSuite) SetupSuite() {
	s.IntegrationTestSuite.SetupSuite()
	// Any K8s-specific setup for the entire suite can go here
	// Ensure testK8sAccountID is set.
	if testK8sAccountID == "" {
		s.T().Skip("TEST_K8S_ACCOUNT_ID is not set, skipping K8s integration tests.")
	}
}

func (s *IntegrationK8sTestSuite) TearDownSuite() {
	s.IntegrationTestSuite.TearDownSuite()
	// Any K8s-specific teardown for the entire suite can go here
}

func TestIntegrationK8sTestSuite(t *testing.T) {
	// Check if the integration tests are enabled
	if config.Config.RunIntegrationTests != "true" {
		t.Skip("Skipping K8s integration tests. Set RUN_INTEGRATION_TESTS=true to enable.")
	}
	// Check if the K8s specific account ID is set.
	if testK8sAccountID == "" {
		t.Skip("TEST_K8S_ACCOUNT_ID is not set, skipping K8s integration tests.")
	}
	suite.Run(t, new(IntegrationK8sTestSuite))
}

var testK8sAccountID = os.Getenv("TEST_K8S_ACCOUNT_ID")
