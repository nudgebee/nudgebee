package integration_test

import (
	"log/slog"
	"nudgebee/runbook/internal/tasks/k8s" // Keep this import for validation tests
	"nudgebee/runbook/internal/tasks/testutils"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"k8s.io/apimachinery/pkg/api/resource" // Added for resource.ParseQuantity
)

// TestK8sWorkloadRestartTask_Validation is the original unit-like validation test
func TestK8sWorkloadRestartTask_Validation(t *testing.T) {
	task := &k8s.WorkloadRestartTask{}
	taskCtx := testutils.NewTestTaskContext(os.Getenv("TEST_TENANT_ID"), os.Getenv("TEST_K8S_ACCOUNT_ID"), os.Getenv("TEST_USER_ID"), slog.Default())

	testCases := []struct {
		name          string
		params        map[string]any
		expectErr     bool
		expectedError string
	}{
		{
			name:          "Missing Namespace",
			params:        map[string]any{"name": "test", "kind": "Deployment"},
			expectErr:     true,
			expectedError: "namespace, name, and kind are required",
		},
		{
			name:          "Missing Name",
			params:        map[string]any{"namespace": "default", "kind": "Deployment"},
			expectErr:     true,
			expectedError: "namespace, name, and kind are required",
		},
		{
			name:          "Missing Kind",
			params:        map[string]any{"namespace": "default", "name": "test"},
			expectErr:     true,
			expectedError: "namespace, name, and kind are required",
		},
		{
			name:          "Unsupported Kind",
			params:        map[string]any{"namespace": "default", "name": "test", "kind": "Service"},
			expectErr:     true,
			expectedError: "workload type 'Service' is not supported for restart (must be Deployment, StatefulSet, DaemonSet, or Rollout)",
		},
		{
			name:          "Invalid Name Format",
			params:        map[string]any{"namespace": "default", "name": "test_name", "kind": "Deployment"},
			expectErr:     true,
			expectedError: "invalid name format",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := task.Execute(taskCtx, tc.params)
			if tc.expectErr {
				assert.Error(t, err)
				if tc.expectedError != "" {
					assert.Contains(t, err.Error(), tc.expectedError)
				}
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

// All K8s task integration tests will be methods on IntegrationK8sTestSuite
func (s *IntegrationK8sTestSuite) TestK8sWorkloadRestartTask() {
	s.T().Log("Running TestK8sWorkloadRestartTask integration test...")
	namespace := "default"
	deploymentName := "test-restart-deploy"
	// containerName := "nginx" // Name of the container in the test deployment

	// Cleanup any previous deployments
	s.k8sDeleteDeployment(namespace, deploymentName)

	// 1. Create a deployment
	s.k8sCreateDeployment(namespace, deploymentName, 1, "50m", "64Mi", "100m", "128Mi")

	// 2. Execute the WorkloadRestart task
	params := map[string]any{
		"namespace":  namespace,
		"name":       deploymentName,
		"kind":       "Deployment",
		"account_id": testK8sAccountID,
	}
	res, err := s.workflowService.ExecuteTask(s.newRequestContext(), testK8sAccountID, "k8s.workload_restart", params)
	s.Require().NoError(err, "WorkloadRestart task failed")
	s.T().Logf("WorkloadRestartTask result: %+v", res)

	resultMap, ok := res.(map[string]any)
	s.Require().True(ok)
	s.Assert().Equal("success", resultMap["status"])
	s.Assert().Equal("Deployment", resultMap["restarted_kind"])
	s.Assert().Equal(deploymentName, resultMap["restarted_name"])

	// 3. Verify the deployment rolled out (i.e., new Pods were created)
	// This is hard to verify directly with kubectl, as `rollout restart` just updates pod template hash.
	// We can check `deployment.kubernetes.io/revision` annotation.
	s.Require().Eventuallyf(func() bool {
		output, err := s.k8sExecuteCli(s.T().Context(), "", "get", "deployment", deploymentName, "-n", namespace, "-o", `jsonpath={.metadata.annotations.deployment\.kubernetes\.io/revision}`)
		s.T().Logf("Current deployment revision: %s, Error: %v", output, err)
		return err == nil && output != "1" && output != "" // Initial revision is usually 1, after restart it changes
	}, 300*time.Second, 5*time.Second, "Deployment %s/%s did not roll out (revision did not change)", deploymentName, namespace)

	// Cleanup
	s.k8sDeleteDeployment(namespace, deploymentName)
}

func (s *IntegrationK8sTestSuite) TestK8sPodDeleteTask() {
	s.T().Log("Running TestK8sPodDeleteTask integration test...")
	namespace := "default"
	deploymentName := "test-pod-delete-deploy"

	// Cleanup any previous deployments
	s.k8sDeleteDeployment(namespace, deploymentName)

	// 1. Create a deployment with 2 replicas
	s.k8sCreateDeployment(namespace, deploymentName, 2, "50m", "64Mi", "100m", "128Mi")
	s.k8sWaitForDeploymentReplicas(namespace, deploymentName, 2, 60*time.Second)

	// 2. Get the list of pods for the deployment
	podNames, err := s.k8sGetPodNames(namespace, "Deployment", deploymentName)
	s.Require().NoError(err)
	s.Require().Len(podNames, 2, "Expected 2 pods for the deployment")
	s.T().Logf("Initial pods: %v", podNames)

	// 3. Execute the PodDelete task (deletes one pod deterministically)
	params := map[string]any{
		"namespace":  namespace,
		"name":       deploymentName,
		"kind":       "Deployment",
		"account_id": testK8sAccountID,
	}
	res, err := s.workflowService.ExecuteTask(s.newRequestContext(), testK8sAccountID, "k8s.pod_delete", params)
	s.Require().NoError(err, "PodDelete task failed")
	s.T().Logf("PodDeleteTask result: %+v", res)

	resultMap, ok := res.(map[string]any)
	s.Require().True(ok)
	s.Assert().Equal("success", resultMap["status"])
	deletedPodName, ok := resultMap["deleted_pod"].(string)
	s.Require().True(ok)
	s.Assert().NotEmpty(deletedPodName, "Deleted pod name should not be empty")
	s.T().Logf("Pod '%s' was targeted for deletion.", deletedPodName)

	// 4. Verify the targeted pod is deleted and a new one is created
	s.k8sWaitForPodDeletion(namespace, deletedPodName, 60*time.Second)
	s.k8sWaitForDeploymentReplicas(namespace, deploymentName, 2, 60*time.Second) // K8s should bring up a new pod

	// 5. Check if the deployment still has 2 pods, but the deleted one is gone
	currentPodNames, err := s.k8sGetPodNames(namespace, "Deployment", deploymentName)
	s.Require().NoError(err)
	s.Assert().Len(currentPodNames, 2, "Expected 2 pods after recreation")
	s.Assert().NotContains(currentPodNames, deletedPodName, "Deleted pod should not be among current pods")

	// Cleanup
	s.k8sDeleteDeployment(namespace, deploymentName)
}

func (s *IntegrationK8sTestSuite) TestK8sPodDeleteTask_Force() {
	s.T().Log("Running TestK8sPodDeleteTask_Force integration test...")
	namespace := "default"
	deploymentName := "test-pod-delete-force-deploy"

	// Cleanup
	s.k8sDeleteDeployment(namespace, deploymentName)

	// 1. Create deployment
	s.k8sCreateDeployment(namespace, deploymentName, 1, "10m", "10Mi", "20m", "20Mi")
	s.k8sWaitForDeploymentReplicas(namespace, deploymentName, 1, 60*time.Second)

	// 2. Execute PodDelete with force=true
	params := map[string]any{
		"namespace":  namespace,
		"name":       deploymentName,
		"kind":       "Deployment",
		"force":      true,
		"account_id": testK8sAccountID,
	}
	res, err := s.workflowService.ExecuteTask(s.newRequestContext(), testK8sAccountID, "k8s.pod_delete", params)
	s.Require().NoError(err, "PodDelete task with force failed")
	s.T().Logf("PodDeleteTask force result: %+v", res)

	resultMap, ok := res.(map[string]any)
	s.Require().True(ok)
	s.Assert().Equal("success", resultMap["status"])
	deletedPodName, ok := resultMap["deleted_pod"].(string)
	s.Assert().True(ok)
	s.Assert().NotEmpty(deletedPodName)

	// 3. Verify deletion
	s.k8sWaitForPodDeletion(namespace, deletedPodName, 30*time.Second)
	s.k8sWaitForDeploymentReplicas(namespace, deploymentName, 1, 60*time.Second)

	// Cleanup
	s.k8sDeleteDeployment(namespace, deploymentName)
}

func (s *IntegrationK8sTestSuite) TestK8sPVRightsizeTask_Constraints() {
	s.T().Log("Running TestK8sPVRightsizeTask_Constraints integration test...")
	namespace := "default"
	scName := "test-sc-constraints"
	pvName := "test-pv-constraints"
	pvcName := "test-pvc-constraints"
	initialSize := "2Gi"

	// Cleanup
	s.k8sDeletePVC(namespace, pvcName)
	s.k8sDeletePV(pvName)
	s.k8sDeleteStorageClass(scName)

	// Setup
	s.k8sCreateStorageClass(scName)
	s.k8sCreatePV(pvName, "5Gi", scName)
	s.k8sCreatePVC(namespace, pvcName, initialSize, scName)
	s.k8sWaitForPVCBound(namespace, pvcName, 60*time.Second)

	// Test 1: Prevent Shrinking
	s.T().Log("Testing shrinking prevention...")
	paramsShrink := map[string]any{
		"namespace":  namespace,
		"name":       pvcName,
		"kind":       "PersistentVolumeClaim",
		"change_to":  "1Gi", // Smaller than 2Gi
		"account_id": testK8sAccountID,
	}
	_, err := s.workflowService.ExecuteTask(s.newRequestContext(), testK8sAccountID, "k8s.pv_rightsize", paramsShrink)
	s.Require().Error(err)
	s.Assert().Contains(err.Error(), "smaller than current size")

	// Test 2: Max Threshold
	s.T().Log("Testing max threshold...")
	paramsMax := map[string]any{
		"namespace":  namespace,
		"name":       pvcName,
		"kind":       "PersistentVolumeClaim",
		"change_to":  "4Gi",
		"max":        "3Gi", // Threshold lower than target
		"account_id": testK8sAccountID,
	}
	_, err = s.workflowService.ExecuteTask(s.newRequestContext(), testK8sAccountID, "k8s.pv_rightsize", paramsMax)
	s.Require().Error(err)
	s.Assert().Contains(err.Error(), "exceeds maximum allowed size")

	// Cleanup
	s.k8sDeletePVC(namespace, pvcName)
	s.k8sDeletePV(pvName)
	s.k8sDeleteStorageClass(scName)
}

func (s *IntegrationK8sTestSuite) TestK8sHorizontalRightsizeTask() {
	s.T().Log("Running TestK8sHorizontalRightsizeTask integration test...")
	namespace := "default"
	deploymentName := "test-hr-deploy"

	// Cleanup any previous deployments
	s.k8sDeleteDeployment(namespace, deploymentName)

	// 1. Create a deployment with 1 replica
	s.k8sCreateDeployment(namespace, deploymentName, 1, "50m", "64Mi", "100m", "128Mi")
	s.k8sWaitForDeploymentReplicas(namespace, deploymentName, 1, 60*time.Second)

	// 2. Scale up to 3 replicas
	s.T().Log("Scaling up to 3 replicas...")
	paramsScaleUp := map[string]any{
		"namespace":  namespace,
		"name":       deploymentName,
		"kind":       "Deployment",
		"change_to":  3.0, // float64 for JSON unmarshalling
		"direction":  "up",
		"account_id": testK8sAccountID,
	}
	resScaleUp, err := s.workflowService.ExecuteTask(s.newRequestContext(), testK8sAccountID, "k8s.horizontal_rightsize", paramsScaleUp)
	s.Require().NoError(err, "HorizontalRightsize task (scale up) failed")
	s.T().Logf("HorizontalRightsizeTask scale up result: %+v", resScaleUp)

	resultMapScaleUp, ok := resScaleUp.(map[string]any)
	s.Require().True(ok)
	s.Assert().Equal("success", resultMapScaleUp["status"])
	s.Assert().Equal(int64(1), resultMapScaleUp["old_replicas"])
	s.Assert().Equal(int64(3), resultMapScaleUp["new_replicas"])

	s.k8sWaitForDeploymentReplicas(namespace, deploymentName, 3, 60*time.Second)

	// 3. Scale down to 1 replica by change_by
	s.T().Log("Scaling down by 2 replicas...")
	paramsScaleDown := map[string]any{
		"namespace":  namespace,
		"name":       deploymentName,
		"kind":       "Deployment",
		"change_by":  2.0,
		"direction":  "down",
		"account_id": testK8sAccountID,
	}
	resScaleDown, err := s.workflowService.ExecuteTask(s.newRequestContext(), testK8sAccountID, "k8s.horizontal_rightsize", paramsScaleDown)
	s.Require().NoError(err, "HorizontalRightsize task (scale down) failed")
	s.T().Logf("HorizontalRightsizeTask scale down result: %+v", resScaleDown)

	resultMapScaleDown, ok := resScaleDown.(map[string]any)
	s.Require().True(ok)
	s.Assert().Equal("success", resultMapScaleDown["status"])
	s.Assert().Equal(int64(3), resultMapScaleDown["old_replicas"])
	s.Assert().Equal(int64(1), resultMapScaleDown["new_replicas"])

	s.k8sWaitForDeploymentReplicas(namespace, deploymentName, 1, 60*time.Second)

	// Cleanup
	s.k8sDeleteDeployment(namespace, deploymentName)
}

func (s *IntegrationK8sTestSuite) TestK8sVerticalRightsizeTask() {
	s.T().Log("Running TestK8sVerticalRightsizeTask integration test...")
	namespace := "default"
	deploymentName := "test-vr-deploy"
	containerName := "nginx"

	// Cleanup any previous deployments
	s.k8sDeleteDeployment(namespace, deploymentName)

	// 1. Create a deployment with initial resources
	s.k8sCreateDeployment(namespace, deploymentName, 1, "50m", "64Mi", "100m", "128Mi")
	s.k8sWaitForDeploymentReady(namespace, deploymentName, 60*time.Second)

	// 2. Get initial resources
	initialCpuReq, initialMemReq, initialCpuLimit, initialMemLimit, err := s.k8sGetDeploymentResources(namespace, deploymentName, containerName)
	s.Require().NoError(err)
	s.T().Logf("Initial resources: CPU Req=%s, Mem Req=%s, CPU Limit=%s, Mem Limit=%s", initialCpuReq, initialMemReq, initialCpuLimit, initialMemLimit)

	// 3. Execute VerticalRightsize task to scale up CPU and Memory
	s.T().Log("Scaling up CPU and Memory...")
	paramsScaleUp := map[string]any{
		"namespace":  namespace,
		"name":       deploymentName,
		"kind":       "Deployment",
		"direction":  "up",
		"cpu":        map[string]any{"change_pct": 20.0},
		"memory":     map[string]any{"change_pct": 20.0},
		"account_id": testK8sAccountID,
	}
	resScaleUp, err := s.workflowService.ExecuteTask(s.newRequestContext(), testK8sAccountID, "k8s.vertical_rightsize", paramsScaleUp)
	s.Require().NoError(err, "VerticalRightsize task (scale up) failed")
	s.T().Logf("VerticalRightsizeTask scale up result: %+v", resScaleUp)

	resultMapScaleUp, ok := resScaleUp.(map[string]any)
	s.Require().True(ok)
	s.Assert().Equal("success", resultMapScaleUp["status"])

	// 4. Verify resources are updated
	s.k8sWaitForDeploymentReady(namespace, deploymentName, 60*time.Second)
	updatedCpuReq, updatedMemReq, updatedCpuLimit, updatedMemLimit, err := s.k8sGetDeploymentResources(namespace, deploymentName, containerName)
	s.Require().NoError(err)
	s.T().Logf("Updated resources: CPU Req=%s, Mem Req=%s, CPU Limit=%s, Mem Limit=%s", updatedCpuReq, updatedMemReq, updatedCpuLimit, updatedMemLimit)

	// Assertions for updated values (parsing quantities required for precise checks)
	assert.Greater(s.T(), parseCpu(updatedCpuReq), parseCpu(initialCpuReq), "CPU request should increase")
	assert.Greater(s.T(), parseMem(updatedMemReq), parseMem(initialMemReq), "Memory request should increase")
	assert.Greater(s.T(), parseCpu(updatedCpuLimit), parseCpu(initialCpuLimit), "CPU limit should increase")
	assert.Greater(s.T(), parseMem(updatedMemLimit), parseMem(initialMemLimit), "Memory limit should increase")

	// Cleanup
	s.k8sDeleteDeployment(namespace, deploymentName)
}

func (s *IntegrationK8sTestSuite) TestK8sVerticalRightsizeTask_Gitops() {
	s.T().Log("Running TestK8sVerticalRightsizeTask_Gitops integration test...")
	namespace := "nudgebee-test"
	deploymentName := "ticket-server"
	containerName := "ticket-server"

	initialCpuReq, initialMemReq, _, _, err := s.k8sGetDeploymentResources(namespace, deploymentName, containerName)
	s.Require().NoError(err)

	// 2. Execute VerticalRightsize with gitops_config
	params := map[string]any{
		"namespace": namespace,
		"name":      deploymentName,
		"kind":      "Deployment",
		"direction": "up",
		"cpu":       map[string]any{"change_pct": 20.0},
		"gitops_config": map[string]any{
			"enabled": true,
			"name":    os.Getenv("TEST_GITOPS_INTEGRATION_NAME"),
		},
		"account_id": testK8sAccountID,
	}
	res, err := s.workflowService.ExecuteTask(s.newRequestContext(), testK8sAccountID, "k8s.vertical_rightsize", params)
	s.Require().NoError(err, "VerticalRightsize task (gitops) failed")
	s.T().Logf("VerticalRightsizeTask gitops result: %+v", res)

	resultMap, ok := res.(map[string]any)
	s.Require().True(ok)
	s.Assert().Equal("pr_created", resultMap["status"])

	// Check response has resolution_id
	s.Assert().NotEmpty(resultMap["resolution_id"], "Expected resolution_id in response")

	// 3. Verify resources on cluster are UNCHANGED
	currentCpuReq, currentMemReq, _, _, err := s.k8sGetDeploymentResources(namespace, deploymentName, containerName)
	s.Require().NoError(err)
	s.Assert().Equal(initialCpuReq, currentCpuReq, "CPU request should NOT have changed")
	s.Assert().Equal(initialMemReq, currentMemReq, "Memory request should NOT have changed")

	// Cleanup
	//s.k8sDeleteDeployment(namespace, deploymentName)
}

func (s *IntegrationK8sTestSuite) TestK8sPVRightsizeTask() {
	s.T().Log("Running TestK8sPVRightsizeTask integration test...")
	namespace := "default"
	scName := "test-sc-resize"
	pvName := "test-pv"
	pvcName := "test-pvc"
	initialSize := "1Gi"

	// Cleanup
	s.k8sDeletePVC(namespace, pvcName)
	s.k8sDeletePV(pvName)
	s.k8sDeleteStorageClass(scName)

	// 1. Create StorageClass with allowVolumeExpansion: true
	s.k8sCreateStorageClass(scName)

	// 2. Create PV
	s.k8sCreatePV(pvName, "5Gi", scName) // Create a larger PV to back the PVC expansion

	// 3. Create PVC
	s.k8sCreatePVC(namespace, pvcName, initialSize, scName)

	// 4. Wait for PVC to be Bound
	s.k8sWaitForPVCBound(namespace, pvcName, 60*time.Second)

	// 5. Execute PVRightsize task to increase size
	s.T().Log("Increasing PVC size...")
	params := map[string]any{
		"namespace":  namespace,
		"name":       pvcName,
		"kind":       "PersistentVolumeClaim",
		"change_to":  "2Gi",
		"account_id": testK8sAccountID,
	}
	res, err := s.workflowService.ExecuteTask(s.newRequestContext(), testK8sAccountID, "k8s.pv_rightsize", params)
	s.Require().NoError(err, "PVRightsize task failed")
	s.T().Logf("PVRightsizeTask result: %+v", res)

	resultMap, ok := res.(map[string]any)
	s.Require().True(ok)
	s.Assert().Equal("success", resultMap["status"])
	s.Assert().Equal(initialSize, resultMap["old_storage"])
	s.Assert().Equal("2Gi", resultMap["new_storage"])

	// 6. Verify PVC size is updated
	s.Require().Eventuallyf(func() bool {
		size, err := s.k8sGetPVCSize(namespace, pvcName)
		return err == nil && size == "2Gi"
	}, 60*time.Second, 5*time.Second, "PVC %s/%s did not update size to 2Gi within %v", namespace, pvcName, 60*time.Second)

	// Cleanup
	s.k8sDeletePVC(namespace, pvcName)
	s.k8sDeletePV(pvName)
	s.k8sDeleteStorageClass(scName)
}

// Helper functions for parsing resource quantities from kubectl output
func parseCpu(cpu string) float64 {
	q, _ := resource.ParseQuantity(cpu)
	return q.AsApproximateFloat64()
}

func parseMem(mem string) float64 {
	q, _ := resource.ParseQuantity(mem)
	return q.AsApproximateFloat64()
}
