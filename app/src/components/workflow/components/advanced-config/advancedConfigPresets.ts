/**
 * Preset configurations for advanced workflow task settings
 */

export interface Preset {
  label: string;
  value: string | Record<string, unknown>;
  description?: string;
}

// Duration presets for timeout fields
export const DURATION_PRESETS: Preset[] = [
  { label: 'Quick (30s)', value: '30s', description: 'For fast operations' },
  { label: 'Standard (5m)', value: '5m', description: 'Default timeout' },
  { label: 'Long (30m)', value: '30m', description: 'For longer operations' },
  { label: 'Very Long (1h)', value: '1h', description: 'For extended operations' },
  { label: 'Extended (2h)', value: '2h', description: 'For very long operations' },
];

// Failure policy presets
export const FAILURE_POLICY_PRESETS: Preset[] = [
  {
    label: 'Retry 3x (Quick)',
    value: {
      action: 'fail',
      retry: {
        maximum_attempts: 3,
        initial_interval: '1s',
        maximum_interval: '10s',
        backoff_coefficient: 2,
      },
    },
    description: 'Retry 3 times with quick intervals',
  },
  {
    label: 'Retry 5x (Backoff)',
    value: {
      action: 'fail',
      retry: {
        maximum_attempts: 5,
        initial_interval: '5s',
        maximum_interval: '60s',
        backoff_coefficient: 2,
      },
    },
    description: 'Retry 5 times with exponential backoff',
  },
  {
    label: 'Continue on Failure',
    value: {
      action: 'continue',
    },
    description: 'Continue automation even if this task fails',
  },
  {
    label: 'Fail Immediately',
    value: {
      action: 'fail',
    },
    description: 'Stop automation on first failure',
  },
];

// Matrix execution presets
export const MATRIX_PRESETS: Preset[] = [
  {
    label: 'Two Parameters',
    value: {
      region: ['us-east-1', 'us-west-2'],
      environment: ['staging', 'production'],
    },
    description: 'Run with 2 parameter combinations',
  },
  {
    label: 'Three Parameters',
    value: {
      region: ['us-east-1', 'eu-west-1'],
      size: ['small', 'medium'],
      tier: ['basic', 'premium'],
    },
    description: 'Run with 3 parameter combinations',
  },
];

// Task hooks presets
export const HOOKS_PRESETS: Preset[] = [
  {
    label: 'Success Notification',
    value: {
      on_success: {
        task: 'notification.send',
        params: {
          channel: 'slack',
          message: 'Task completed successfully',
        },
      },
    },
    description: 'Send notification on success',
  },
  {
    label: 'Failure Alert',
    value: {
      on_failure: {
        task: 'notification.send',
        params: {
          channel: 'pagerduty',
          message: 'Task failed: {{ .Error }}',
        },
      },
    },
    description: 'Send alert on failure',
  },
  {
    label: 'Complete Lifecycle',
    value: {
      on_success: {
        task: 'notification.send',
        params: { message: 'Success' },
      },
      on_failure: {
        task: 'notification.send',
        params: { message: 'Failed: {{ .Error }}' },
      },
      on_complete: {
        task: 'cleanup.run',
        params: {},
      },
    },
    description: 'Handle success, failure, and cleanup',
  },
];

// Set state presets
export const SET_STATE_PRESETS: Preset[] = [
  {
    label: 'Simple State',
    value: {
      last_result: {
        value: '{{ .Result }}',
        ttl: '24h',
      },
    },
    description: 'Store result for 24 hours',
  },
  {
    label: 'Multiple Keys',
    value: {
      status: {
        value: '{{ .Result.status }}',
        ttl: '1h',
      },
      timestamp: {
        value: '{{ now }}',
        ttl: '1h',
      },
    },
    description: 'Store multiple values',
  },
];

// Set variables presets
export const SET_VARS_PRESETS: Preset[] = [
  {
    label: 'Simple Variable',
    value: {
      result_data: {
        value: '{{ .Result.data }}',
      },
    },
    description: 'Store task result in variable',
  },
  {
    label: 'Multiple Variables',
    value: {
      status: {
        value: '{{ .Result.status }}',
      },
      count: {
        value: '{{ .Result.count }}',
      },
    },
    description: 'Store multiple values as variables',
  },
];

// Conditional execution presets
export const CONDITIONAL_PRESETS: Preset[] = [
  {
    label: 'Check Previous Task',
    value: '{{ eq .Tasks.previous_task.output.status "success" }}',
    description: 'Execute if previous task succeeded',
  },
  {
    label: 'Check Input Variable',
    value: '{{ eq .Inputs.environment "production" }}',
    description: 'Execute only in production',
  },
  {
    label: 'Check State',
    value: '{{ .State.should_run }}',
    description: 'Execute based on state value',
  },
];

// Placeholder examples for fields
export const FIELD_PLACEHOLDERS: Record<string, string> = {
  if: 'e.g., {{ eq .Tasks.task_id.output.status "success" }}',
  timeout: 'e.g., 5m, 300s, 1h',
  set_state: JSON.stringify(
    {
      key: {
        value: '{{ .Result }}',
        ttl: '24h',
      },
    },
    null,
    2
  ),
  set_vars: JSON.stringify(
    {
      var_name: {
        value: '{{ .Result.data }}',
      },
    },
    null,
    2
  ),
  matrix: JSON.stringify(
    {
      param1: ['value1', 'value2'],
      param2: ['a', 'b'],
    },
    null,
    2
  ),
  hooks: JSON.stringify(
    {
      on_success: {},
      on_failure: {},
      on_complete: {},
    },
    null,
    2
  ),
  failure_policy: JSON.stringify(
    {
      action: 'continue',
      retry: {
        maximum_attempts: 3,
        initial_interval: '1s',
        maximum_interval: '60s',
        backoff_coefficient: 2,
      },
    },
    null,
    2
  ),
};

// Helper text for each field
export const FIELD_HELPER_TEXT: Record<string, string> = {
  if: 'Go template expression. Task executes only if condition evaluates to true.',
  timeout: 'Override automation timeout for this task. Format: Go duration (e.g., 30s, 5m, 1h)',
  set_state: 'Store persistent state values with optional TTL. Available across automation runs.',
  set_vars: 'Set automation variables accessible in subsequent tasks within this run.',
  matrix: 'Run task multiple times with parameter combinations (Cartesian product).',
  hooks: 'Define lifecycle hooks: on_success, on_failure, on_complete.',
  failure_policy: 'Define failure handling: action (continue/fail) and optional retry policy.',
};

// Get presets for a specific field
export const getPresetsForField = (field: string): Preset[] => {
  switch (field) {
    case 'timeout':
      return DURATION_PRESETS;
    case 'failure_policy':
      return FAILURE_POLICY_PRESETS;
    case 'matrix':
      return MATRIX_PRESETS;
    case 'hooks':
      return HOOKS_PRESETS;
    case 'set_state':
      return SET_STATE_PRESETS;
    case 'set_vars':
      return SET_VARS_PRESETS;
    case 'if':
      return CONDITIONAL_PRESETS;
    default:
      return [];
  }
};
