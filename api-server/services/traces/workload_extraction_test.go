package traces

import (
	"testing"
)

func TestExtractWorkloadFromPodName(t *testing.T) {
	tests := []struct {
		podName          string
		expectedWorkload string
		description      string
	}{
		{
			podName:          "nginx-deployment-7b7d7f9f9d-abcde",
			expectedWorkload: "nginx-deployment",
			description:      "Deployment pod name",
		},
		{
			podName:          "redis-statefulset-0",
			expectedWorkload: "redis-statefulset",
			description:      "StatefulSet pod name with index 0",
		},
		{
			podName:          "postgres-statefulset-2",
			expectedWorkload: "postgres-statefulset",
			description:      "StatefulSet pod name with index 2",
		},
		{
			podName:          "backup-job-12345",
			expectedWorkload: "backup-job",
			description:      "Job pod name",
		},
		{
			podName:          "cleanup-cronjob-1640995200-abcde",
			expectedWorkload: "cleanup-cronjob",
			description:      "CronJob pod name",
		},
		{
			podName:          "monitoring-daemonset-xyz12",
			expectedWorkload: "monitoring-daemonset",
			description:      "DaemonSet pod name",
		},
		{
			podName:          "simple-pod",
			expectedWorkload: "simple-pod",
			description:      "Simple pod name without standard suffixes",
		},
		{
			podName:          "",
			expectedWorkload: "",
			description:      "Empty pod name",
		},
		{
			podName:          "app-service-deployment-abc123def-xyz89",
			expectedWorkload: "app-service-deployment",
			description:      "Deployment with hyphens in workload name",
		},
	}

	for _, tt := range tests {
		t.Run(tt.description, func(t *testing.T) {
			result := extractWorkloadFromPodName(tt.podName)
			if result != tt.expectedWorkload {
				t.Errorf("extractWorkloadFromPodName(%q) = %q, expected %q",
					tt.podName, result, tt.expectedWorkload)
			}
		})
	}
}
