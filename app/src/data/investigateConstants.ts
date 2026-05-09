// Domain constants for the investigate page and related components.
// Other files using the same magic strings can import from here incrementally.

export const SUBJECT_STATUS = {
  UNRESOLVED: 'Unresolved',
} as const;

export const SUBJECT_TYPE = {
  POD: 'pod',
} as const;

export const AGGREGATION_KEY = {
  ANOMALY: 'Anomaly',
  KUBE_PERSISTENT_VOLUME_FILLING_UP: 'KubePersistentVolumeFillingUp',
  CPU_THROTTLING_HIGH: 'CPUThrottlingHigh',
  POD_MEMORY_REACHING_LIMIT: 'PodMemoryReachingLimit',
  IMAGE_PULL_BACKOFF_REPORTER: 'image_pull_backoff_reporter',
  KUBE_NODE_NOT_READY: 'KubeNodeNotReady',
} as const;

// Alert keys that support the "Resolve Event" action
export const RESOLVABLE_ALERT_KEYS = [
  AGGREGATION_KEY.KUBE_PERSISTENT_VOLUME_FILLING_UP,
  AGGREGATION_KEY.CPU_THROTTLING_HIGH,
  AGGREGATION_KEY.POD_MEMORY_REACHING_LIMIT,
  AGGREGATION_KEY.IMAGE_PULL_BACKOFF_REPORTER,
  AGGREGATION_KEY.KUBE_NODE_NOT_READY,
] as const;

export const RCA_STATUS = {
  COMPLETED: 'COMPLETED',
  FAILED: 'FAILED',
  IN_PROGRESS: 'IN_PROGRESS',
} as const;
