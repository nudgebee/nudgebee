package event

type SeverityThreshold struct {
	AggregationKey         string
	HighThresholdHours     *float64
	CriticalThresholdHours *float64
}

func float64Ptr(f float64) *float64 {
	return &f
}

var severityThresholds = []SeverityThreshold{
	{AggregationKey: "KubePodCrashLooping", CriticalThresholdHours: float64Ptr(1)},
	{AggregationKey: "KubePersistentVolumeErrors", CriticalThresholdHours: float64Ptr(3)},
	{AggregationKey: "KubeVersionMismatch"},
	{AggregationKey: "KubeQuotaFullyUsed", HighThresholdHours: float64Ptr(6), CriticalThresholdHours: float64Ptr(24)},
	{AggregationKey: "KubeAggregatedAPIDown", CriticalThresholdHours: float64Ptr(0.5)},
	{AggregationKey: "KubeAPITerminatedRequests", CriticalThresholdHours: float64Ptr(1)},
	{AggregationKey: "KubeHpaMaxedOut", HighThresholdHours: float64Ptr(4), CriticalThresholdHours: float64Ptr(12)},
	{AggregationKey: "KubeProxyDown", CriticalThresholdHours: float64Ptr(0.5)},
	{AggregationKey: "KubeCPUQuotaOvercommit"},
	{AggregationKey: "KubeStatefulSetReplicasMismatch", HighThresholdHours: float64Ptr(2), CriticalThresholdHours: float64Ptr(6)},
	{AggregationKey: "KubeContainerWaiting", HighThresholdHours: float64Ptr(6), CriticalThresholdHours: float64Ptr(24)},
	{AggregationKey: "KubeletTooManyPods", HighThresholdHours: float64Ptr(1)},
	{AggregationKey: "KubeDeploymentGenerationMismatch", HighThresholdHours: float64Ptr(2)},
	{AggregationKey: "KubeSchedulerDown", CriticalThresholdHours: float64Ptr(1)},
	{AggregationKey: "KubeletPlegDurationHigh", HighThresholdHours: float64Ptr(2)},
	{AggregationKey: "KubeAPIDown", CriticalThresholdHours: float64Ptr(0.5)},
	{AggregationKey: "KubeletClientCertificateExpiration", HighThresholdHours: float64Ptr(12), CriticalThresholdHours: float64Ptr(24)},
	{AggregationKey: "KubeQuotaExceeded", HighThresholdHours: float64Ptr(2)},
	{AggregationKey: "KubeAPIErrorBudgetBurn", CriticalThresholdHours: float64Ptr(1)},
	{AggregationKey: "KubeDeploymentReplicasMismatch", HighThresholdHours: float64Ptr(2)},
	{AggregationKey: "KubeletServerCertificateExpiration", HighThresholdHours: float64Ptr(12), CriticalThresholdHours: float64Ptr(24)},
	{AggregationKey: "KubeHpaReplicasMismatch", HighThresholdHours: float64Ptr(4)},
	{AggregationKey: "KubeDaemonSetRolloutStuck", HighThresholdHours: float64Ptr(2), CriticalThresholdHours: float64Ptr(6)},
	{AggregationKey: "KubeNodeReadinessFlapping", HighThresholdHours: float64Ptr(1)},
	{AggregationKey: "KubeClientCertificateExpiration", HighThresholdHours: float64Ptr(12)},
	{AggregationKey: "KubeletPodStartUpLatencyHigh", HighThresholdHours: float64Ptr(2)},
	{AggregationKey: "KubeStatefulSetUpdateNotRolledOut", HighThresholdHours: float64Ptr(4)},
	{AggregationKey: "KubeNodeUnreachable", CriticalThresholdHours: float64Ptr(0.5)},
	{AggregationKey: "KubeDaemonSetMisScheduled", HighThresholdHours: float64Ptr(2)},
	{AggregationKey: "KubeMemoryQuotaOvercommit"},
	{AggregationKey: "KubeStatefulSetGenerationMismatch", HighThresholdHours: float64Ptr(2)},
	{AggregationKey: "KubeDaemonSetNotScheduled", HighThresholdHours: float64Ptr(2)},
	{AggregationKey: "KubeControllerManagerDown", CriticalThresholdHours: float64Ptr(0.5)},
	{AggregationKey: "KubePersistentVolumeFillingUp", HighThresholdHours: float64Ptr(6)},
	{AggregationKey: "KubePodNotReady", HighThresholdHours: float64Ptr(1), CriticalThresholdHours: float64Ptr(6)},
	{AggregationKey: "KubeNodeNotReady", HighThresholdHours: float64Ptr(1), CriticalThresholdHours: float64Ptr(3)},
	{AggregationKey: "KubeQuotaAlmostFull", HighThresholdHours: float64Ptr(6)},
	{AggregationKey: "KubeJobFailed", CriticalThresholdHours: float64Ptr(0.5)},
	{AggregationKey: "KubeCPUOvercommit"},
	{AggregationKey: "KubeletServerCertificateRenewalErrors", HighThresholdHours: float64Ptr(1)},
	{AggregationKey: "KubeletDown", CriticalThresholdHours: float64Ptr(0.5)},
	{AggregationKey: "KubeClientErrors", HighThresholdHours: float64Ptr(2)},
	{AggregationKey: "KubeAggregatedAPIErrors", HighThresholdHours: float64Ptr(2)},
	{AggregationKey: "NodeClockNotSynchronising", HighThresholdHours: float64Ptr(1)},
	{AggregationKey: "CPUThrottlingHigh", HighThresholdHours: float64Ptr(2)},
	{AggregationKey: "KubeletClientCertificateRenewalErrors", HighThresholdHours: float64Ptr(2)},
	{AggregationKey: "KubeJobCompletion"},
	{AggregationKey: "KubeMemoryOvercommit"},
	{AggregationKey: "NodeFilesystemAlmostOutOfFiles", HighThresholdHours: float64Ptr(6)},
	{AggregationKey: "NodeFilesystemSpaceFillingUp", HighThresholdHours: float64Ptr(6)},
	{AggregationKey: "NodeClockSkewDetected", HighThresholdHours: float64Ptr(1)},
	{AggregationKey: "NodeRAIDDegraded", HighThresholdHours: float64Ptr(1)},
	{AggregationKey: "NodeNetworkReceiveErrs", HighThresholdHours: float64Ptr(1)},
	{AggregationKey: "NodeNetworkTransmitErrs", HighThresholdHours: float64Ptr(1)},
	{AggregationKey: "NodeFileDescriptorLimit", HighThresholdHours: float64Ptr(2)},
	{AggregationKey: "NodeTextFileCollectorScrapeError", HighThresholdHours: float64Ptr(2)},
	{AggregationKey: "NodeRAIDDiskFailure", CriticalThresholdHours: float64Ptr(0.5)},
	{AggregationKey: "NodeHighNumberConntrackEntriesUsed", HighThresholdHours: float64Ptr(1)},
	{AggregationKey: "NodeFilesystemAlmostOutOfSpace", HighThresholdHours: float64Ptr(6)},
	{AggregationKey: "NodeFilesystemFilesFillingUp", HighThresholdHours: float64Ptr(6)},
	{AggregationKey: "AlertmanagerFailedToSendAlerts", CriticalThresholdHours: float64Ptr(0.5)},
	{AggregationKey: "AlertmanagerClusterCrashlooping", CriticalThresholdHours: float64Ptr(1)},
	{AggregationKey: "AlertmanagerConfigInconsistent", HighThresholdHours: float64Ptr(2)},
	{AggregationKey: "AlertmanagerMembersInconsistent", HighThresholdHours: float64Ptr(2)},
	{AggregationKey: "AlertmanagerClusterFailedToSendAlerts", CriticalThresholdHours: float64Ptr(1)},
	{AggregationKey: "AlertmanagerFailedReload", HighThresholdHours: float64Ptr(1)},
	{AggregationKey: "AlertmanagerClusterDown", CriticalThresholdHours: float64Ptr(0.5)},
	{AggregationKey: "image_pull_backoff_reporter", HighThresholdHours: float64Ptr(2), CriticalThresholdHours: float64Ptr(6)},
	{AggregationKey: "pod_oom_killer_enricher", CriticalThresholdHours: float64Ptr(0.5)},
	{AggregationKey: "report_crash_loop", CriticalThresholdHours: float64Ptr(1)},
}

func getThresholdForKey(aggregationKey string) *SeverityThreshold {
	for i := range severityThresholds {
		if severityThresholds[i].AggregationKey == aggregationKey {
			return &severityThresholds[i]
		}
	}
	return nil
}
