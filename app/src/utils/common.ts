import type { NextRouter } from 'next/router';
import { v5 } from 'uuid';

/**
 * Maps raw API priority / severity strings (HIGH, MEDIUM, FIRING, DEBUG, OK,
 * HIGHEST, …) to ds/SeverityIcon's fixed 5-level union. Unknown / lower-signal
 * values collapse to 'info'.
 */
export type DsSeverityLevel = 'critical' | 'high' | 'medium' | 'low' | 'info';
export const toSeverityLevel = (value: unknown): DsSeverityLevel => {
  const v = (value ?? '').toString().toLowerCase();
  if (v === 'critical' || v === 'highest' || v === 'firing') return 'critical';
  if (v === 'high') return 'high';
  if (v === 'medium') return 'medium';
  if (v === 'low' || v === 'lowest') return 'low';
  return 'info';
};

// Applicable only for multi-select filter
export function syncFilterFromQuery<T>(
  filter: T[],
  queryValue: string | string[] | undefined,
  getKey: (item: T) => string = (item) => item as unknown as string
): T[] {
  if (!filter?.length || !queryValue) return [];
  const keys = new Set(
    Array.isArray(queryValue) ? queryValue : (queryValue as string).includes(',') ? (queryValue as string).split(',') : [queryValue as string]
  );
  return filter.filter((item) => keys.has(getKey(item)));
}

export const getAccountDetailRoute = (accountType: string, id: string) => {
  let route = '';
  switch (accountType?.toUpperCase()) {
    case 'K8S':
      route = `/kubernetes/details/${id}`;
      break;
    case 'SNOWFLAKE':
      route = `/snowflake/details/${id}`;
      break;
    case 'AWS':
      route = `/accounts/account-details?id=${id}`;
      break;
    default:
      route = `/accounts/account-details?id=${id}`;
  }
  return route;
};

export const getCloudProviderLabel = (cloudProvider: string) => {
  let label = '';
  switch (cloudProvider.toUpperCase()) {
    case 'AWS':
      label = 'AWS';
      break;
    case 'GCP':
      label = 'GCP';
      break;
    case 'AZURE':
      label = 'Azure';
      break;
    case 'K8S':
      label = 'Kubernetes';
      break;
    case 'CLOUDFOUNDRY':
      label = 'Cloud Foundry';
      break;
    case 'SNOWFLAKE':
      label = 'Snowflake';
      break;
    case 'OPENAI':
      label = 'Open AI';
      break;
    case 'NEWRELIC':
      label = 'Newrelic';
      break;
    case 'NEWRELIC_WEBHOOK':
      label = 'Newrelic Webhook';
      break;
    case 'JIRA':
      label = 'Jira';
      break;
    case 'SLACK':
      label = 'Slack';
      break;
    case 'MSTEAMS':
      label = 'Ms Teams';
      break;
    case 'GOOGLE_CHAT':
      label = 'Google Chat';
      break;
    case 'GITHUB':
      label = 'GitHub';
      break;
    case 'SERVICENOW':
      label = 'ServiceNow';
      break;
    case 'PAGERDUTY':
      label = 'PagerDuty';
      break;
    case 'PAGERDUTY_WEBHOOK':
      label = 'PagerDuty Webhook';
      break;
    case 'ZENDUTY':
      label = 'ZenDuty';
      break;
    case 'ZENDUTY_WEBHOOK':
      label = 'ZenDuty Webhook';
      break;
    case 'POSTGRES':
      label = 'PostgreSQL';
      break;
    case 'RABBITMQ':
      label = 'RabbitMQ';
      break;
    case 'MYSQL':
      label = 'MySql';
      break;
    case 'REDIS':
      label = 'Redis';
      break;
    case 'CONFLUENCE':
      label = 'Confluence';
      break;
    case 'CLICKHOUSE':
      label = 'Clickhouse';
      break;
    case 'DATADOG':
      label = 'Datadog';
      break;
    case 'DYNATRACE':
      label = 'Dynatrace';
      break;
    case 'DYNATRACE_WEBHOOK':
      label = 'Dynatrace Webhook';
      break;
    case 'GCP_MONITORING_WEBHOOK':
      label = 'GCP Monitoring Webhook';
      break;
    case 'DATADOG_WEBHOOK':
      label = 'Datadog Webhook';
      break;
    case 'PROMETHEUS_ALERTMANAGER_WEBHOOK':
      label = 'Prometheus AlertManager Webhook';
      break;
    case 'GRAFANA_WEBHOOK':
      label = 'Grafana Webhook';
      break;
    case 'ARGOCD':
      label = 'ArgoCD';
      break;
    case 'LLM':
      label = 'LLM';
      break;
    case 'MCP':
      label = 'MCP';
      break;
    case 'LOGGLY':
      label = 'Loggly';
      break;
    case 'LOKI':
      label = 'Loki';
      break;
    case 'SIGNOZ':
      label = 'Signoz';
      break;
    case 'OBSERVE':
      label = 'Observe';
      break;
    case 'AZURE_APP_INSIGHTS':
      label = 'Azure App Insights';
      break;
    case 'PROMETHEUS':
      label = 'Prometheus';
      break;
    case 'CHRONOSPHERE':
      label = 'Chronosphere';
      break;
    case 'OTEL':
      label = 'Otel';
      break;
    case 'AZURE_MONITOR_WEBHOOK':
      label = 'Azure Monitor Webhook';
      break;
    case 'SSH':
      label = 'SSH';
      break;
    case 'SERVICENOW_WEBHOOK':
      label = 'ServiceNow Webhook';
      break;
    case 'JAEGER':
      label = 'Jaeger';
      break;
    case 'SPLUNK_OBSERVABILITY_PLATFORM':
      label = 'Splunk Observability Cloud';
      break;
    case 'SPLUNK_WEBHOOK':
      label = 'Splunk Webhook';
      break;
    case 'SOLARWINDS':
      label = 'SolarWinds';
      break;
    case 'SOLARWINDS_WEBHOOK':
      label = 'SolarWinds Webhook';
      break;
    case 'WORKFLOW_WEBHOOK':
      label = 'Workflow Webhook';
      break;
    case 'BITBUCKET':
      label = 'Bitbucket';
      break;
    case 'GITLAB':
      label = 'Gitlab';
      break;
    case 'GRAFANA-TEMPO':
      label = 'Grafana Tempo';
      break;
    case 'ES':
      label = 'Elasticsearch';
      break;
    case 'PINOT':
      label = 'Apache Pinot';
      break;
    case 'LAST9':
      label = 'Last9';
      break;
    case 'VM_AGENT':
      label = 'Proxy Agent';
      break;
    case 'MSSQL':
      label = 'SQL Server';
      break;
    case 'ORACLE':
      label = 'Oracle';
      break;
    default:
      label = '';
  }

  return label;
};

export const getMsInTimestamp = (millis: string) => {
  // response -> 2023-12-12 21:03:42
  const date = new Date(millis);
  return `${date.getFullYear()}-${(date.getMonth() + 1).toString().padStart(2, '0')}-${date.getDate().toString().padStart(2, '0')} ${date
    .getHours()
    .toString()
    .padStart(2, '0')}:${date.getMinutes().toString().padStart(2, '0')}:${date.getSeconds().toString().padStart(2, '0')}`;
};

export const redK8sErrorCodes = [
  'backoff',
  'failed',
  'unhealthy',
  'warning',
  'invaliddiskcapacity',
  'evicted',
  'killing',
  'critical',
  'crashloopbackoff',
];

export const redLogsErrorCodes = ['failed', 'error', 'permission denied', 'traceback', 'exception'];

export const libraryErrors = ['psycopg2.errors', 'Exception:', 'Caused by:', 'panic:', 'fatal error:', 'Traceback (most recent call last):'];

export const truncateText = (text: string, maxLength: number) => {
  if (text && text.length > maxLength) {
    return text?.slice(0, maxLength) + '...';
  }
  return text;
};

export const formatBytes = (bytes: number, fixed = true, suffix = '') => {
  const units = ['Bytes', 'KB', 'MB', 'GB', 'TB', 'PB', 'EB', 'ZB', 'YB'];
  let i = 0;

  while (bytes >= 1024) {
    bytes /= 1024;
    i++;
  }

  return `${bytes.toFixed(fixed ? 2 : 0)} ${units[i]}${suffix}`;
};

export const getAccountCreationSuccessMsg = (cloudProvider: string) => {
  let msg = '';
  switch (cloudProvider.toUpperCase()) {
    case 'AWS':
      msg = 'AWS account added successfully.';
      break;
    case 'GCP':
      msg = 'GCP account added successfully.';
      break;
    case 'AZURE':
      msg = 'Azure account added successfully.';
      break;
    case 'K8S':
      msg = 'Kubernetes account added successfully. Please wait for curl commands for installing the kubernetes agent.';
      break;
    case 'SNOWFLAKE':
      msg = 'Snowflake account added successfully.';
      break;
    case 'OPENAI':
      msg = 'Open AI account added successfully.';
      break;
    case 'NEWRELIC':
      msg = 'New Relic account added successfully.';
      break;
    case 'SOLARWINDS':
      msg = 'SolarWinds account added successfully.';
      break;
    case 'JIRA':
      msg = 'Jira account added successfully.';
      break;
    case 'SLACK':
      msg = 'Slack account added successfully.';
      break;
    case 'GITHUB':
      msg = 'GitHub account added successfully.';
      break;
    default:
      msg = 'Account added successfully. ';
  }

  return msg;
};

export const calculateTimeRange = (interval: string) => {
  const currentTime = new Date().getTime();

  let duration;
  const intervalSplit = interval.split(':');
  switch (intervalSplit[1]) {
    case 'm':
      duration = Number(intervalSplit[0]) * 60 * 1000;
      break;
    case 'h':
      duration = Number(intervalSplit[0]) * 60 * 60 * 1000;
      break;
    case 'd':
      duration = Number(intervalSplit[0]) * 24 * 60 * 60 * 1000;
      break;
    default:
      throw new Error('Invalid interval');
  }

  const startTime = currentTime - duration;
  const endTime = currentTime;

  return {
    startTime: startTime,
    endTime: endTime,
  };
};

export const lokiPlusMinus5TimeRange = (fromInterval: number) => {
  if (fromInterval && fromInterval > 0) {
    const date = new Date(fromInterval * 1000);
    const plusTimestamp = new Date(date.getTime());
    plusTimestamp.setMinutes(plusTimestamp.getMinutes() + 5);
    const minusTimestamp = new Date(date.getTime());
    minusTimestamp.setMinutes(minusTimestamp.getMinutes() - 5);
    return { endTime: plusTimestamp.getTime(), startTime: minusTimestamp.getTime() };
  }
};

export const plusMinus5TimeRangePrometheusOfDate = (dateString: string) => {
  if (dateString) {
    const initialDate = new Date(dateString);
    const plus5Mins = new Date(initialDate.getTime() + 5 * 60000);
    const minus5Mins = new Date(initialDate.getTime() - 5 * 60000);
    return {
      startTime: minus5Mins,
      endTime: plus5Mins,
    };
  }
};

export const convertNumberToTimestamp = (interval: number) => {
  const date = new Date(interval);
  const formattedDate = `${(date.getDate() + '').padStart(2, '0')}-${(date.getMonth() + 1 + '').padStart(2, '0')}-${(date.getFullYear() + '').slice(
    -2
  )} ${date.getHours()}:${(date.getMinutes() + '').padStart(2, '0')}:${(date.getSeconds() + '').padStart(2, '0')}`;
  return formattedDate;
};

export const convertDateToPromDateFormat = (givenDate: Date) => {
  return convertDateToSpecificDateFormat(givenDate);
};

const convertDateToSpecificDateFormat = (date: Date) => {
  const year = date.getUTCFullYear();
  const month = (date.getUTCMonth() + 1).toString().padStart(2, '0');
  const day = date.getUTCDate().toString().padStart(2, '0');
  const hours = date.getUTCHours().toString().padStart(2, '0');
  const minutes = date.getUTCMinutes().toString().padStart(2, '0');
  const seconds = date.getUTCSeconds().toString().padStart(2, '0');

  return `${year}-${month}-${day} ${hours}:${minutes}:${seconds} UTC`;
};

export const convertNumberToTimestampPromFormat = (interval: number) => {
  const date = new Date(interval);
  return convertDateToSpecificDateFormat(date);
};

export const convertDateStringForSLOReportChart = (date: string) => {
  const dateObj = new Date(date);

  const year = dateObj.getFullYear();
  const month = String(dateObj.getMonth() + 1).padStart(2, '0');
  const day = String(dateObj.getDate()).padStart(2, '0');
  const hours = String(dateObj.getHours()).padStart(2, '0');
  const minutes = String(dateObj.getMinutes()).padStart(2, '0');

  return `${year}-${month}-${day} ${hours}:${minutes}`;
};

export const checkForZero = (value: number) => {
  if (value === 0) {
    return '0';
  }
  return value;
};

export const isCronValid = (freq: string) => {
  const cronregex = new RegExp(
    /^(\*|(\d|1\d|2\d|3\d|4\d|5\d)|\*\/(\d|1\d|2\d|3\d|4\d|5\d)) (\*|(\d|1\d|2[0-3])|\*\/(\d|1\d|2[0-3])) (\*|([1-9]|1\d|2\d|3[0-1])|\*\/([1-9]|1\d|2\d|3[0-1])) (\*|([1-9]|1[0-2])|\*\/([1-9]|1[0-2])) (\*|([0-6])|\*\/([0-6]))$/
  );
  return cronregex.test(freq);
};

export const isAlertNameValid = (name: string) => {
  const alertNameRegex = new RegExp(/^[a-zA-Z0-9](?:[a-zA-Z0-9\s]*[a-zA-Z0-9])?$/);
  return alertNameRegex.test(name);
};

export const isEmailValid = (value: string) => {
  const nameRegex = /^[a-zA-Z0-9._%+-]+@[a-zA-Z0-9.-]+.[a-zA-Z]{2,4}$/;
  return nameRegex.test(value);
};

export const isLLMFunctionNameValid = (value: string) => {
  const functionNameRegex = /^[a-z][a-z0-9_-]*[a-z0-9]$/;
  return functionNameRegex.test(value) && value.length >= 2 && value.length <= 30;
};

export const isVariableNameValid = (value: string) => {
  // Variable names can only contain letters, numbers, and underscores
  // No hyphens or spaces allowed
  const variableNameRegex = /^[a-zA-Z]\w*$/;
  return variableNameRegex.test(value) && value.length >= 1 && value.length <= 50;
};

export const isK8sAccountNameValid = (value: string) => {
  const regex = /^(?!.*(?:--|__|\s\s))[a-zA-Z0-9](?:[a-zA-Z0-9\s_-]{2,47}[a-zA-Z0-9])?$/;
  return regex.test(value);
};

export const isAWSAccountNameValid = (value: string) => {
  const regex = /^(?!.*(?:--|__|\s\s))[a-zA-Z0-9](?:[a-zA-Z0-9\s_-]{2,47}[a-zA-Z0-9])?$/;
  return regex.test(value);
};

export const titleCaseForAggregationKey = (value: string) => {
  if (value) {
    if (value === 'pod_oom_killer_enricher') {
      return 'PodOOMKill';
    }
    const words = value.split('_');
    const capitalizedWords = words.map((word) => {
      return word.charAt(0).toUpperCase() + word.slice(1);
    });
    return capitalizedWords.join('');
  }
  return '';
};

export const formatDurationInTrace = (input: number, isInSeconds = false) => {
  let nanoseconds;
  if (isInSeconds) {
    nanoseconds = input * 1e9;
  } else {
    nanoseconds = input;
  }
  const microseconds = nanoseconds / 1000;
  const millis = microseconds / 1000;
  const secs = millis / 1000;
  if (secs >= 3600) {
    const hours = Math.floor(secs / 3600);
    const remainingMinutes = Math.floor((secs % 3600) / 60);
    const remainingSeconds = Math.round(secs % 60);
    return hours + 'h ' + remainingMinutes + 'm ' + remainingSeconds + 's';
  } else if (secs >= 60) {
    const minutes = Math.floor(secs / 60);
    const remainingSeconds = Math.round(secs % 60);
    return minutes + 'm ' + remainingSeconds + 's';
  } else if (secs >= 1) {
    return Math.round(secs) + 's';
  } else if (millis >= 1) {
    return Math.round(millis) + 'ms';
  } else if (microseconds >= 1) {
    return Math.round(microseconds) + 'µs';
  }
  return nanoseconds + 'ns';
};

export const formatLatencyInServiceMap = (latencyInSeconds: number) => {
  if (latencyInSeconds === undefined || latencyInSeconds === null || latencyInSeconds === 0 || isNaN(latencyInSeconds)) {
    return '-';
  }
  if (latencyInSeconds >= 1) {
    return latencyInSeconds.toFixed(2) + ' s';
  }
  if (latencyInSeconds >= 0.001) {
    return (latencyInSeconds * 1000).toFixed(2) + ' ms';
  }
  if (latencyInSeconds >= 0.000001) {
    return (latencyInSeconds * 1000000).toFixed(2) + ' μs';
  }
  return (latencyInSeconds * 1000000000).toFixed(2) + ' ns';
};

export const formatDateForTrace = (value: number) => {
  if (isNaN(value)) {
    return '';
  }
  const dateObject = new Date(value);
  return dateObject.toISOString();
};

export const formatDateForPlusMinusDuration = (valueInUTC: string | number, durationInMinutes = 5) => {
  let timestamp = Number(valueInUTC);

  // If the timestamp looks like seconds (10 digits), convert to ms
  if (timestamp < 1e12) {
    timestamp *= 1000;
  }

  const dateObject = new Date(timestamp);
  const currentTime = Date.now();

  let extraAddedMinutesInMillis = dateObject.getTime() + durationInMinutes * 60 * 1000;

  if (extraAddedMinutesInMillis > currentTime) {
    extraAddedMinutesInMillis = currentTime;
  }

  const dateMinusMinutes = new Date(dateObject.getTime() - durationInMinutes * 60 * 1000);

  return {
    datePlusMinutes: extraAddedMinutesInMillis,
    dateMinusMinutes: dateMinusMinutes.getTime(),
  };
};

export const formatSeconds = (seconds: number, rounding = true) => {
  if (!seconds) {
    return '--';
  }

  if (seconds < 1e-6) {
    return (rounding ? Math.round(seconds * 1e9) : seconds * 1e9) + 'ns';
  } else if (seconds < 1e-3) {
    return (rounding ? Math.round(seconds * 1e6) : seconds * 1e6) + 'μs';
  } else if (seconds < 1) {
    return (rounding ? Math.round(seconds * 1e3) : seconds * 1e3) + 'ms';
  } else if (seconds < 60) {
    return (rounding ? Math.round(seconds) : seconds) + 's';
  } else if (seconds < 3600) {
    const minutes = seconds / 60;
    return (rounding ? Math.round(minutes) : minutes) + 'm';
  }
  const hours = seconds / 3600;
  return (rounding ? Math.round(hours) : hours) + 'h';
};

export const formatUserRoleName = (role: string) => {
  switch (role) {
    case 'tenant_admin_readonly':
      return 'ReadOnly Admin';
    case 'tenant_admin':
      return 'Admin';
  }
};

export const formatSLOAuditMessage = (data: any) => {
  if (data) {
    return `${data.workload_name ?? ''}|${data.namespace ?? ''}`;
  }
  return '';
};

export const convertToReadableFormat = (input: string) => {
  return input
    .toLowerCase()
    .split(' ')
    .map((word) => word.charAt(0).toUpperCase() + word.slice(1))
    .join(' ');
};

export const formatActionNameForAuditMessage = (actionName: string) => {
  let formattedName = actionName;
  if (actionName) {
    formattedName = actionName
      .replace(/_?enricher_?/gi, '')
      .replace(/_/g, ' ')
      .trim();
    formattedName = formattedName
      .split(' ')
      .map((word) => word.charAt(0).toUpperCase() + word.slice(1).toLowerCase())
      .join(' ');
  }
  return formattedName;
};

export const convertStringCase = (str: string) => {
  // Convert FreeStorageSpace to Free Storage Space
  const words = str.split(/(?=[A-Z])/);
  const capitalizedWords = words.map((word) => {
    if (word.length === 1) {
      return word.toUpperCase();
    }
    return word[0].toUpperCase() + word.slice(1).toLowerCase();
  });
  return capitalizedWords.join(' ');
};

/**
 * Format cloud metric names into readable titles.
 * Handles snake_case, camelCase, and lowercase compound words.
 * e.g. "Average_numberofobjects" → "Average Number Of Objects"
 *      "Average_BucketSizeBytes" → "Average Bucket Size Bytes"
 *      "CPUUtilization" → "CPU Utilization"
 */
export const formatMetricName = (str: string): string => {
  if (!str) return '';

  // Known compound words mapping (lowercase → spaced title case)
  const knownCompounds: Record<string, string> = {
    numberofobjects: 'Number Of Objects',
    bucketsizebytes: 'Bucket Size Bytes',
    bucketsize: 'Bucket Size',
    freestoragespace: 'Free Storage Space',
    cpuutilization: 'CPU Utilization',
    memoryutilization: 'Memory Utilization',
    diskutilization: 'Disk Utilization',
    networkin: 'Network In',
    networkout: 'Network Out',
    networkpacketsin: 'Network Packets In',
    networkpacketsout: 'Network Packets Out',
    statuscheckfailed: 'Status Check Failed',
    databaseconnections: 'Database Connections',
    readiops: 'Read IOPS',
    writeiops: 'Write IOPS',
    readlatency: 'Read Latency',
    writelatency: 'Write Latency',
    readthroughput: 'Read Throughput',
    writethroughput: 'Write Throughput',
    freeablememory: 'Freeable Memory',
    swapusage: 'Swap Usage',
    binlogdiskusage: 'Binlog Disk Usage',
    replicalag: 'Replica Lag',
    volumereadops: 'Volume Read Ops',
    volumewriteops: 'Volume Write Ops',
    volumeread: 'Volume Read',
    volumewrite: 'Volume Write',
  };

  // Split by underscores first
  const parts = str.split('_');

  const formattedParts = parts.map((part) => {
    const lowerPart = part.toLowerCase();
    // Check known compound words
    if (knownCompounds[lowerPart]) {
      return knownCompounds[lowerPart];
    }
    // Apply camelCase splitting (handles BucketSizeBytes → Bucket Size Bytes)
    return convertStringCase(part);
  });

  return formattedParts.join(' ').replace(/\s+/g, ' ').trim();
};

const UPPERCASE_ACRONYMS = new Set([
  'iam',
  'mfa',
  'aws',
  'api',
  'cpu',
  'gpu',
  'ssl',
  'tls',
  'vpc',
  'ebs',
  'rds',
  'ec2',
  'elb',
  'alb',
  'nlb',
  's3',
  'sqs',
  'sns',
  'ecs',
  'eks',
  'acm',
  'waf',
  'kms',
  'ram',
  'io',
  'ip',
  'dns',
  'http',
  'https',
  'ssh',
  'sql',
  'db',
  'url',
  'uri',
  'id',
  'os',
  'vm',
  'ci',
  'cd',
  'llm',
  'mcp',
]);

export const snakeToTitleCase = (str: string) => {
  if (str === null || str === undefined) return '';
  return str
    .split('_')
    .map((word) => {
      const lower = word.toLowerCase();
      if (UPPERCASE_ACRONYMS.has(lower)) return word.toUpperCase();
      return word.charAt(0).toUpperCase() + word.slice(1);
    })
    .join(' ');
};

export const toKebabCase = (str: string) => {
  return str
    .trim()
    .toLowerCase()
    .split(/[\s_]+/)
    .join('-');
};

export const isDateString = (str: string) => {
  const iso8601Regex = /^(\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(?:\.\d{1,3})?(Z)?)$/;
  if (!iso8601Regex.test(str)) {
    return false;
  }
  const date = new Date(str);
  return date instanceof Date && !isNaN(date.getTime());
};

export const groupBy = (array: any, key: string) => {
  return array.reduce((result: any, currentValue: any) => {
    const groupKey = currentValue[key];
    if (!result[groupKey]) {
      result[groupKey] = [];
    }
    result[groupKey].push(currentValue);
    return result;
  }, {});
};

export const extractIp = (value: string) => {
  const regex = /ip-(\d+)-(\d+)-(\d+)-(\d+)/;
  const match = value.match(regex);
  if (match) {
    return `${match[1]}.${match[2]}.${match[3]}.${match[4]}`;
  }
};

export const CPU_QUERIES = [
  {
    key: 'cpu_real',
    query:
      'sum(rate(node_cpu_seconds_total{ __CLUSTER__ mode!="idle"}[24h])) or sum(rate(node_resources_cpu_usage_seconds_total{ __CLUSTER__ mode!="idle"}[24h]))',
  },
  {
    key: 'cpu_request',
    query: 'sum(kube_pod_container_resource_requests{ __CLUSTER__ resource="cpu"})',
  },
  {
    key: 'cpu_limits',
    query: 'sum(kube_pod_container_resource_limits{ __CLUSTER__ resource="cpu"})',
  },
  {
    key: 'cpu_total',
    query: 'sum(machine_cpu_cores{__CLUSTER__}) or sum(node_resources_cpu_logical_cores{__CLUSTER__})',
  },
];

export const MEMORY_QUERIES = [
  {
    key: 'mem_real',
    query:
      'sum(node_memory_MemTotal_bytes{__CLUSTER__} - node_memory_MemAvailable_bytes{__CLUSTER__}) or sum(node_resources_memory_total_bytes{__CLUSTER__} - node_resources_memory_available_bytes{__CLUSTER__})',
  },
  {
    key: 'mem_request',
    query: 'sum(kube_pod_container_resource_requests{__CLUSTER__ resource="memory"})',
  },
  {
    key: 'mem_limits',
    query: 'sum(kube_pod_container_resource_limits{__CLUSTER__ resource="memory"})',
  },
  {
    key: 'mem_total',
    query: 'sum(node_memory_MemTotal_bytes{__CLUSTER__}) or sum(node_resources_memory_total_bytes{__CLUSTER__})',
  },
];

export const PERCENTILE_QUERIES = [
  {
    key: 'p90_mem',
    query:
      'quantile(0.9, node_memory_MemTotal_bytes{__CLUSTER__} - node_memory_MemAvailable_bytes{__CLUSTER__}) or quantile(0.9, node_resources_memory_total_bytes{__CLUSTER__} - node_resources_memory_available_bytes{__CLUSTER__})',
  },
  {
    key: 'p90_cpu',
    query:
      'sum(quantile_over_time(0.90, rate(node_cpu_seconds_total{__CLUSTER__ mode!="idle"}[24h])[24h:])) or sum(quantile_over_time(0.90, rate(node_resources_cpu_usage_seconds_total{__CLUSTER__ mode!="idle"}[24h])[24h:]))',
  },
  {
    key: 'p50_mem',
    query:
      'quantile(0.5, node_memory_MemTotal_bytes{__CLUSTER__} - node_memory_MemAvailable_bytes{__CLUSTER__}) or quantile(0.5, node_resources_memory_total_bytes{__CLUSTER__} - node_resources_memory_available_bytes{__CLUSTER__})',
  },
  {
    key: 'p50_cpu',
    query:
      'sum(quantile_over_time(0.50, rate(node_cpu_seconds_total{__CLUSTER__ mode!="idle"}[24h])[24h:])) or sum(quantile_over_time(0.50, rate(node_resources_cpu_usage_seconds_total{__CLUSTER__ mode!="idle"}[24h])[24h:]))',
  },
  {
    key: 'max_usage_mem',
    query:
      'max_over_time(sum(node_memory_MemTotal_bytes{__CLUSTER__} - node_memory_MemFree_bytes{__CLUSTER__} - node_memory_Buffers_bytes{__CLUSTER__} - node_memory_Cached_bytes{__CLUSTER__})[24h:])',
  },
  {
    key: 'max_usage_cpu',
    query:
      'sum(max_over_time(rate(node_cpu_seconds_total{__CLUSTER__ mode!="idle"}[24h])[24h:])) or sum(max_over_time(rate(node_resources_cpu_usage_seconds_total{__CLUSTER__ mode!="idle"}[24h])[24h:]))',
  },
];

export const CLUSTER_USAGE_PROMQL1 = [
  {
    key: 'cpu_real',
    query:
      'sum (rate(node_cpu_seconds_total{__CLUSTER__ mode!="idle"}[5m])) or sum(rate(node_resources_cpu_usage_seconds_total{__CLUSTER__ mode!="idle"}[5m]))',
  },
  {
    key: 'cpu_request',
    query: 'sum(kube_pod_container_resource_requests{__CLUSTER__ resource="cpu"})',
  },
  {
    key: 'cpu_limits',
    query: 'sum(kube_pod_container_resource_limits{__CLUSTER__ resource="cpu"})',
  },
  {
    key: 'cpu_total',
    query: 'sum(machine_cpu_cores{__CLUSTER__})  or sum(node_resources_cpu_logical_cores{__CLUSTER__})',
  },
  {
    key: 'mem_real',
    query:
      'sum(node_memory_MemTotal_bytes{__CLUSTER__} - node_memory_MemAvailable_bytes{__CLUSTER__}) or sum(node_resources_memory_total_bytes{__CLUSTER__} - node_resources_memory_available_bytes{__CLUSTER__})',
  },
  {
    key: 'mem_request',
    query: 'sum(kube_pod_container_resource_requests{__CLUSTER__ resource="memory"})',
  },
  {
    key: 'mem_limits',
    query: 'sum(kube_pod_container_resource_limits{__CLUSTER__ resource="memory"})',
  },
  {
    key: 'mem_total',
    query: 'sum(node_memory_MemTotal_bytes{__CLUSTER__}) or sum(node_resources_memory_total_bytes{__CLUSTER__})',
  },
];

export const containsLink = (value: string) => {
  const urlPattern = new RegExp(
    '(https?:\\/\\/)?' + // protocol
      '((([a-zA-Z0-9\\-]+\\.)+[a-zA-Z]{2,})|' + // domain name
      '((\\d{1,3}\\.){3}\\d{1,3}))' + // OR ip (v4) address
      '(\\:\\d+)?(\\/[-a-zA-Z0-9@:%._\\+~#=]*)*' + // port and path
      '(\\?[;&a-zA-Z0-9%_\\.,~+=-]*)?' + // query string
      '(\\#[-a-zA-Z0-9_]*)?',
    'i' // fragment locator
  );

  return urlPattern.test(value);
};

export const awsInstances = [
  'a1.medium',
  'a1.large',
  'a1.xlarge',
  'a1.2xlarge',
  'a1.4xlarge',
  'a1.metal',
  'c1.medium',
  'c1.xlarge',
  'c3.large',
  'c3.xlarge',
  'c3.2xlarge',
  'c3.4xlarge',
  'c3.8xlarge',
  'c4.large',
  'c4.xlarge',
  'c4.2xlarge',
  'c4.4xlarge',
  'c4.8xlarge',
  'c5.large',
  'c5.xlarge',
  'c5.2xlarge',
  'c5.4xlarge',
  'c5.9xlarge',
  'c5.12xlarge',
  'c5.18xlarge',
  'c5.24xlarge',
  'c5.metal',
  'c5a.large',
  'c5a.xlarge',
  'c5a.2xlarge',
  'c5a.4xlarge',
  'c5a.8xlarge',
  'c5a.12xlarge',
  'c5a.16xlarge',
  'c5a.24xlarge',
  'c5ad.large',
  'c5ad.xlarge',
  'c5ad.2xlarge',
  'c5ad.4xlarge',
  'c5ad.8xlarge',
  'c5ad.12xlarge',
  'c5ad.16xlarge',
  'c5ad.24xlarge',
  'c5d.large',
  'c5d.xlarge',
  'c5d.2xlarge',
  'c5d.4xlarge',
  'c5d.9xlarge',
  'c5d.12xlarge',
  'c5d.18xlarge',
  'c5d.24xlarge',
  'c5d.metal',
  'c5n.large',
  'c5n.xlarge',
  'c5n.2xlarge',
  'c5n.4xlarge',
  'c5n.9xlarge',
  'c5n.18xlarge',
  'c5n.metal',
  'c6a.large',
  'c6a.xlarge',
  'c6a.2xlarge',
  'c6a.4xlarge',
  'c6a.8xlarge',
  'c6a.12xlarge',
  'c6a.16xlarge',
  'c6a.24xlarge',
  'c6a.32xlarge',
  'c6a.48xlarge',
  'c6a.metal',
  'c6g.medium',
  'c6g.large',
  'c6g.xlarge',
  'c6g.2xlarge',
  'c6g.4xlarge',
  'c6g.8xlarge',
  'c6g.12xlarge',
  'c6g.16xlarge',
  'c6g.metal',
  'c6gd.medium',
  'c6gd.large',
  'c6gd.xlarge',
  'c6gd.2xlarge',
  'c6gd.4xlarge',
  'c6gd.8xlarge',
  'c6gd.12xlarge',
  'c6gd.16xlarge',
  'c6gd.metal',
  'c6gn.medium',
  'c6gn.large',
  'c6gn.xlarge',
  'c6gn.2xlarge',
  'c6gn.4xlarge',
  'c6gn.8xlarge',
  'c6gn.12xlarge',
  'c6gn.16xlarge',
  'c6i.large',
  'c6i.xlarge',
  'c6i.2xlarge',
  'c6i.4xlarge',
  'c6i.8xlarge',
  'c6i.12xlarge',
  'c6i.16xlarge',
  'c6i.24xlarge',
  'c6i.32xlarge',
  'c6i.metal',
  'c6id.large',
  'c6id.xlarge',
  'c6id.2xlarge',
  'c6id.4xlarge',
  'c6id.8xlarge',
  'c6id.12xlarge',
  'c6id.16xlarge',
  'c6id.24xlarge',
  'c6id.32xlarge',
  'c6id.metal',
  'c6in.large',
  'c6in.xlarge',
  'c6in.2xlarge',
  'c6in.4xlarge',
  'c6in.8xlarge',
  'c6in.12xlarge',
  'c6in.16xlarge',
  'c6in.24xlarge',
  'c6in.32xlarge',
  'c6in.metal',
  'c7a.medium',
  'c7a.large',
  'c7a.xlarge',
  'c7a.2xlarge',
  'c7a.4xlarge',
  'c7a.8xlarge',
  'c7a.12xlarge',
  'c7a.16xlarge',
  'c7a.24xlarge',
  'c7a.32xlarge',
  'c7a.48xlarge',
  'c7a.metal-48xl',
  'c7g.medium',
  'c7g.large',
  'c7g.xlarge',
  'c7g.2xlarge',
  'c7g.4xlarge',
  'c7g.8xlarge',
  'c7g.12xlarge',
  'c7g.16xlarge',
  'c7g.metal',
  'c7gd.medium',
  'c7gd.large',
  'c7gd.xlarge',
  'c7gd.2xlarge',
  'c7gd.4xlarge',
  'c7gd.8xlarge',
  'c7gd.12xlarge',
  'c7gd.16xlarge',
  'c7gd.metal',
  'c7gn.medium',
  'c7gn.large',
  'c7gn.xlarge',
  'c7gn.2xlarge',
  'c7gn.4xlarge',
  'c7gn.8xlarge',
  'c7gn.12xlarge',
  'c7gn.16xlarge',
  'c7gn.metal',
  'c7i.large',
  'c7i.xlarge',
  'c7i.2xlarge',
  'c7i.4xlarge',
  'c7i.8xlarge',
  'c7i.12xlarge',
  'c7i.16xlarge',
  'c7i.24xlarge',
  'c7i.metal-24xl',
  'c7i.48xlarge',
  'c7i.metal-48xl',
  'd2.xlarge',
  'd2.2xlarge',
  'd2.4xlarge',
  'd2.8xlarge',
  'd3.xlarge',
  'd3.2xlarge',
  'd3.4xlarge',
  'd3.8xlarge',
  'd3en.xlarge',
  'd3en.2xlarge',
  'd3en.4xlarge',
  'd3en.6xlarge',
  'd3en.8xlarge',
  'd3en.12xlarge',
  'dl1.24xlarge',
  'f1.2xlarge',
  'f1.4xlarge',
  'f1.16xlarge',
  'g3.4xlarge',
  'g3.8xlarge',
  'g3.16xlarge',
  'g3s.xlarge',
  'g4ad.xlarge',
  'g4ad.2xlarge',
  'g4ad.4xlarge',
  'g4ad.8xlarge',
  'g4ad.16xlarge',
  'g4dn.xlarge',
  'g4dn.2xlarge',
  'g4dn.4xlarge',
  'g4dn.8xlarge',
  'g4dn.12xlarge',
  'g4dn.16xlarge',
  'g4dn.metal',
  'g5.xlarge',
  'g5.2xlarge',
  'g5.4xlarge',
  'g5.8xlarge',
  'g5.12xlarge',
  'g5.16xlarge',
  'g5.24xlarge',
  'g5.48xlarge',
  'g5g.xlarge',
  'g5g.2xlarge',
  'g5g.4xlarge',
  'g5g.8xlarge',
  'g5g.16xlarge',
  'g5g.metal',
  'g6.xlarge',
  'g6.2xlarge',
  'g6.4xlarge',
  'g6.8xlarge',
  'g6.12xlarge',
  'g6.16xlarge',
  'g6.24xlarge',
  'g6.48xlarge',
  'gr6.4xlarge',
  'gr6.8xlarge',
  'h1.2xlarge',
  'h1.4xlarge',
  'h1.8xlarge',
  'h1.16xlarge',
  'hpc7g.4xlarge',
  'hpc7g.8xlarge',
  'hpc7g.16xlarge',
  'i2.xlarge',
  'i2.2xlarge',
  'i2.4xlarge',
  'i2.8xlarge',
  'i3.large',
  'i3.xlarge',
  'i3.2xlarge',
  'i3.4xlarge',
  'i3.8xlarge',
  'i3.16xlarge',
  'i3.metal',
  'i3en.large',
  'i3en.xlarge',
  'i3en.2xlarge',
  'i3en.3xlarge',
  'i3en.6xlarge',
  'i3en.12xlarge',
  'i3en.24xlarge',
  'i3en.metal',
  'i4g.large',
  'i4g.xlarge',
  'i4g.2xlarge',
  'i4g.4xlarge',
  'i4g.8xlarge',
  'i4g.16xlarge',
  'i4i.large',
  'i4i.xlarge',
  'i4i.2xlarge',
  'i4i.4xlarge',
  'i4i.8xlarge',
  'i4i.12xlarge',
  'i4i.16xlarge',
  'i4i.24xlarge',
  'i4i.32xlarge',
  'i4i.metal',
  'im4gn.large',
  'im4gn.xlarge',
  'im4gn.2xlarge',
  'im4gn.4xlarge',
  'im4gn.8xlarge',
  'im4gn.16xlarge',
  'inf1.xlarge',
  'inf1.2xlarge',
  'inf1.6xlarge',
  'inf1.24xlarge',
  'inf2.xlarge',
  'inf2.8xlarge',
  'inf2.24xlarge',
  'inf2.48xlarge',
  'is4gen.medium',
  'is4gen.large',
  'is4gen.xlarge',
  'is4gen.2xlarge',
  'is4gen.4xlarge',
  'is4gen.8xlarge',
  'm1.small',
  'm1.medium',
  'm1.large',
  'm1.xlarge',
  'm2.xlarge',
  'm2.2xlarge',
  'm2.4xlarge',
  'm3.medium',
  'm3.large',
  'm3.xlarge',
  'm3.2xlarge',
  'm4.large',
  'm4.xlarge',
  'm4.2xlarge',
  'm4.4xlarge',
  'm4.10xlarge',
  'm4.16xlarge',
  'm5.large',
  'm5.xlarge',
  'm5.2xlarge',
  'm5.4xlarge',
  'm5.8xlarge',
  'm5.12xlarge',
  'm5.16xlarge',
  'm5.24xlarge',
  'm5.metal',
  'm5a.large',
  'm5a.xlarge',
  'm5a.2xlarge',
  'm5a.4xlarge',
  'm5a.8xlarge',
  'm5a.12xlarge',
  'm5a.16xlarge',
  'm5a.24xlarge',
  'm5ad.large',
  'm5ad.xlarge',
  'm5ad.2xlarge',
  'm5ad.4xlarge',
  'm5ad.8xlarge',
  'm5ad.12xlarge',
  'm5ad.16xlarge',
  'm5ad.24xlarge',
  'm5d.large',
  'm5d.xlarge',
  'm5d.2xlarge',
  'm5d.4xlarge',
  'm5d.8xlarge',
  'm5d.12xlarge',
  'm5d.16xlarge',
  'm5d.24xlarge',
  'm5d.metal',
  'm5dn.large',
  'm5dn.xlarge',
  'm5dn.2xlarge',
  'm5dn.4xlarge',
  'm5dn.8xlarge',
  'm5dn.12xlarge',
  'm5dn.16xlarge',
  'm5dn.24xlarge',
  'm5dn.metal',
  'm5n.large',
  'm5n.xlarge',
  'm…',
  'm6idn.metal',
  'm6in.large',
  'm6in.xlarge',
  'm6in.2xlarge',
  'm6in.4xlarge',
  'm6in.8xlarge',
  'm6in.12xlarge',
  'm6in.16xlarge',
  'm6in.24xlarge',
  'm6in.32xlarge',
  'm6in.metal',
  'm7a.medium',
  'm7a.large',
  'm7a.xlarge',
  'm7a.2xlarge',
  'm7a.4xlarge',
  'm7a.8xlarge',
  'm7a.12xlarge',
  'm7a.16xlarge',
  'm7a.24xlarge',
  'm7a.32xlarge',
  'm7a.48xlarge',
  'm7a.metal-48xl',
  'm7g.medium',
  'm7g.large',
  'm7g.xlarge',
  'm7g.2xlarge',
  'm7g.4xlarge',
  'm7g.8xlarge',
  'm7g.12xlarge',
  'm7g.16xlarge',
  'm7g.metal',
  'm7gd.medium',
  'm7gd.large',
  'm7gd.xlarge',
  'm7gd.2xlarge',
  'm7gd.4xlarge',
  'm7gd.8xlarge',
  'm7gd.12xlarge',
  'm7gd.16xlarge',
  'm7gd.metal',
  'm7i.large',
  'm7i.xlarge',
  'm7i.2xlarge',
  'm7i.4xlarge',
  'm7i.8xlarge',
  'm7i.12xlarge',
  'm7i.16xlarge',
  'm7i.24xlarge',
  'm7i.metal-24xl',
  'm7i.48xlarge',
  'm7i.metal-48xl',
  'm7i-flex.large',
  'm7i-flex.xlarge',
  'm7i-flex.2xlarge',
  'm7i-flex.4xlarge',
  'm7i-flex.8xlarge',
  'p2.xlarge',
  'p2.8xlarge',
  'p2.16xlarge',
  'p3.2xlarge',
  'p3.8xlarge',
  'p3.16xlarge',
  'p3dn.24xlarge',
  'p4d.24xlarge',
  'p5.48xlarge',
  'r3.large',
  'r3.xlarge',
  'r3.2xlarge',
  'r3.4xlarge',
  'r3.8xlarge',
  'r4.large',
  'r4.xlarge',
  'r4.2xlarge',
  'r4.4xlarge',
  'r4.8xlarge',
  'r4.16xlarge',
  'r5.large',
  'r5.xlarge',
  'r5.2xlarge',
  'r5.4xlarge',
  'r5.8xlarge',
  'r5.12xlarge',
  'r5.16xlarge',
  'r5.24xlarge',
  'r5.metal',
  'r5a.large',
  'r5a.xlarge',
  'r5a.2xlarge',
  'r5a.4xlarge',
  'r5a.8xlarge',
  'r5a.12xlarge',
  'r5a.16xlarge',
  'r5a.24xlarge',
  'r5ad.large',
  'r5ad.xlarge',
  'r5ad.2xlarge',
  'r5ad.4xlarge',
  'r5ad.8xlarge',
  'r5ad.12xlarge',
  'r5ad.16xlarge',
  'r5ad.24xlarge',
  'r5b.large',
  'r5b.xlarge',
  'r5b.2xlarge',
  'r5b.4xlarge',
  'r5b.8xlarge',
  'r5b.12xlarge',
  'r5b.16xlarge',
  'r5b.24xlarge',
  'r5b.metal',
  'r5d.large',
  'r5d.xlarge',
  'r5d.2xlarge',
  'r5d.4xlarge',
  'r5d.8xlarge',
  'r5d.12xlarge',
  'r5d.16xlarge',
  'r5d.24xlarge',
  'r5d.metal',
  'r5dn.large',
  'r5dn.xlarge',
  'r5dn.2xlarge',
  'r5dn.4xlarge',
  'r5dn.8xlarge',
  'r5dn.12xlarge',
  'r5dn.16xlarge',
  'r5dn.24xlarge',
  'r5dn.metal',
  'r5n.large',
  'r5n.xlarge',
  'r5n.2xlarge',
  'r5n.4xlarge',
  'r5n.8xlarge',
  'r5n.12xlarge',
  'r5n.16xlarge',
  'r5n.24xlarge',
  'r5n.metal',
  'r6a.large',
  'r6a.xlarge',
  'r6a.2xlarge',
  'r6a.4xlarge',
  'r6a.8xlarge',
  'r6a.12xlarge',
  'r6a.16xlarge',
  'r6a.24xlarge',
  'r6a.32xlarge',
  'r6a.48xlarge',
  'r6a.metal',
  'r6g.medium',
  'r6g.large',
  'r6g.xlarge',
  'r6g.2xlarge',
  'r6g.4xlarge',
  'r6g.8xlarge',
  'r6g.12xlarge',
  'r6g.16xlarge',
  'r6g.metal',
  'r6gd.medium',
  'r6gd.large',
  'r6gd.xlarge',
  'r6gd.2xlarge',
  'r6gd.4xlarge',
  'r6gd.8xlarge',
  'r6gd.12xlarge',
  'r6gd.16xlarge',
  'r6gd.metal',
  'r6i.large',
  'r6i.xlarge',
  'r6i.2xlarge',
  'r6i.4xlarge',
  'r6i.8xlarge',
  'r6i.12xlarge',
  'r6i.16xlarge',
  'r6i.24xlarge',
  'r6i.32xlarge',
  'r6i.metal',
  'r6id.large',
  'r6id.xlarge',
  'r6id.2xlarge',
  'r6id.4xlarge',
  'r6id.8xlarge',
  'r6id.12xlarge',
  'r6id.16xlarge',
  'r6id.24xlarge',
  'r6id.32xlarge',
  'r6id.metal',
  'r6idn.large',
  'r6idn.xlarge',
  'r6idn.2xlarge',
  'r6idn.4xlarge',
  'r6idn.8xlarge',
  'r6idn.12xlarge',
  'r6idn.16xlarge',
  'r6idn.24xlarge',
  'r6idn.32xlarge',
  'r6idn.metal',
  'r6in.large',
  'r6in.xlarge',
  'r6in.2xlarge',
  'r6in.4xlarge',
  'r6in.8xlarge',
  'r6in.12xlarge',
  'r6in.16xlarge',
  'r6in.24xlarge',
  'r6in.32xlarge',
  'r6in.metal',
  'r7a.medium',
  'r7a.large',
  'r7a.xlarge',
  'r7a.2xlarge',
  'r7a.4xlarge',
  'r7a.8xlarge',
  'r7a.12xlarge',
  'r7a.16xlarge',
  'r7a.24xlarge',
  'r7a.32xlarge',
  'r7a.48xlarge',
  'r7a.metal-48xl',
  'r7g.medium',
  'r7g.large',
  'r7g.xlarge',
  'r7g.2xlarge',
  'r7g.4xlarge',
  'r7g.8xlarge',
  'r7g.12xlarge',
  'r7g.16xlarge',
  'r7g.metal',
  'r7gd.medium',
  'r7gd.large',
  'r7gd.xlarge',
  'r7gd.2xlarge',
  'r7gd.4xlarge',
  'r7gd.8xlarge',
  'r7gd.12xlarge',
  'r7gd.16xlarge',
  'r7gd.metal',
  'r7i.large',
  'r7i.xlarge',
  'r7i.2xlarge',
  'r7i.4xlarge',
  'r7i.8xlarge',
  'r7i.12xlarge',
  'r7i.16xlarge',
  'r7i.24xlarge',
  'r7i.metal-24xl',
  'r7i.48xlarge',
  'r7i.metal-48xl',
  'r7iz.large',
  'r7iz.xlarge',
  'r7iz.2xlarge',
  'r7iz.4xlarge',
  'r7iz.8xlarge',
  'r7iz.12xlarge',
  'r7iz.16xlarge',
  'r7iz.metal-16xl',
  'r7iz.32xlarge',
  'r7iz.metal-32xl',
  't1.micro',
  't2.nano',
  't2.micro',
  't2.small',
  't2.medium',
  't2.large',
  't2.xlarge',
  't2.2xlarge',
  't3.nano',
  't3.micro',
  't3.small',
  't3.medium',
  't3.large',
  't3.xlarge',
  't3.2xlarge',
  't3a.nano',
  't3a.micro',
  't3a.small',
  't3a.medium',
  't3a.large',
  't3a.xlarge',
  't3a.2xlarge',
  't4g.nano',
  't4g.micro',
  't4g.small',
  't4g.medium',
  't4g.large',
  't4g.xlarge',
  't4g.2xlarge',
  'trn1.2xlarge',
  'trn1.32xlarge',
  'trn1n.32xlarge',
  'u-12tb1.112xlarge',
  'u-18tb1.112xlarge',
  'u-24tb1.112xlarge',
  'u-3tb1.56xlarge',
  'u-6tb1.56xlarge',
  'u-6tb1.112xlarge',
  'u-9tb1.112xlarge',
  'u7i-12tb.224xlarge',
  'u7in-16tb.224xlarge',
  'u7in-24tb.224xlarge',
  'u7in-32tb.224xlarge',
  'vt1.3xlarge',
  'vt1.6xlarge',
  'vt1.24xlarge',
  'x1.16xlarge',
  'x1.32xlarge',
  'x1e.xlarge',
  'x1e.2xlarge',
  'x1e.4xlarge',
  'x1e.8xlarge',
  'x1e.16xlarge',
  'x1e.32xlarge',
  'x2gd.medium',
  'x2gd.large',
  'x2gd.xlarge',
  'x2gd.2xlarge',
  'x2gd.4xlarge',
  'x2gd.8xlarge',
  'x2gd.12xlarge',
  'x2gd.16xlarge',
  'x2gd.metal',
  'x2idn.16xlarge',
  'x2idn.24xlarge',
  'x2idn.32xlarge',
  'x2idn.metal',
  'x2iedn.xlarge',
  'x2iedn.2xlarge',
  'x2iedn.4xlarge',
  'x2iedn.8xlarge',
  'x2iedn.16xlarge',
  'x2iedn.24xlarge',
  'x2iedn.32xlarge',
  'x2iedn.metal',
  'x2iezn.2xlarge',
  'x2iezn.4xlarge',
  'x2iezn.6xlarge',
  'x2iezn.8xlarge',
  'x2iezn.12xlarge',
  'x2iezn.metal',
  'z1d.large',
  'z1d.xlarge',
  'z1d.2xlarge',
  'z1d.3xlarge',
  'z1d.6xlarge',
  'z1d.12xlarge',
  'z1d.metal',
];

export const awsInstanceCategory = [
  { label: 'C - Compute Optimized', value: 'c' },
  { label: 'D - Dense Storage', value: 'd' },
  { label: 'F - FPGA', value: 'f' },
  { label: 'G - Graphics Intensive', value: 'g' },
  { label: 'Hpc - High-performance Computing', value: 'hpc' },
  { label: 'Im - Storage Optimized', value: 'im' },
  { label: 'I - Storage optimized (1 to 4 ratio of vCPU to memory)', value: 'i' },
  { label: 'Is - Storage optimized (1 to 6 ratio of vCPU to memory)', value: 'is' },
  { label: 'inf - AWS Inferentia', value: 'inf' },
  { label: 'M - General Purpose', value: 'm' },
  { label: 'P - GPU accelerated', value: 'p' },
  { label: 'R - Memory Optimized', value: 'r' },
  { label: 'T - Burstable performance', value: 't' },
  { label: 'Trn - AWS Trainium', value: 'trn' },
  { label: 'U - High memory', value: 'u' },
  { label: 'VT - Video transcoding', value: 'vt' },
  { label: 'X - Memory intensive', value: 'x' },
  { label: 'Z - High performance', value: 'z' },
];

export const azureInstanceFamily = ['D', 'F', 'L'];

export const awsInstanceFamily = [
  'a1',
  'c1',
  'c3',
  'c4',
  'c5',
  'c5a',
  'c5ad',
  'c5d',
  'c5n',
  'c6a',
  'c6g',
  'c6gd',
  'c6gn',
  'c6i',
  'c6id',
  'c6in',
  'c7a',
  'c7g',
  'c7gd',
  'c7gn',
  'c7i',
  'd2',
  'd3',
  'd3en',
  'dl1',
  'f1',
  'g3',
  'g3s',
  'g4ad',
  'g4dn',
  'g5',
  'g5g',
  'g6',
  'gr6',
  'h1',
  'hpc7g',
  'i2',
  'i3',
  'i3en',
  'i4g',
  'i4i',
  'im4gn',
  'inf1',
  'inf2',
  'is4gen',
  'm1',
  'm2',
  'm3',
  'm4',
  'm5',
  'm5a',
  'm5ad',
  'm5d',
  'm5dn',
  'm5n',
  'm5zn',
  'm6a',
  'm6g',
  'm6gd',
  'm6i',
  'm6id',
  'm6idn',
  'm6in',
  'm7a',
  'm7g',
  'm7gd',
  'm7i',
  'm7i-flex',
  'p2',
  'p3',
  'p3dn',
  'p4d',
  'p5',
  'r3',
  'r4',
  'r5',
  'r5a',
  'r5ad',
  'r5b',
  'r5d',
  'r5dn',
  'r5n',
  'r6a',
  'r6g',
  'r6gd',
  'r6i',
  'r6id',
  'r6idn',
  'r6in',
  'r7a',
  'r7g',
  'r7gd',
  'r7i',
  'r7iz',
  't1',
  't2',
  't3',
  't3a',
  't4g',
  'trn1',
  'trn1n',
  'u-12tb1',
  'u-18tb1',
  'u-24tb1',
  'u-3tb1',
  'u-6tb1',
  'u-9tb1',
  'u7i-12tb',
  'u7in-16tb',
  'u7in-24tb',
  'u7in-32tb',
  'vt1',
  'x1',
  'x1e',
  'x2gd',
  'x2idn',
  'x2iedn',
  'x2iezn',
  'z1d',
];

export const azureInstanceZone = ['uksouth-1', 'uksouth-2', 'uksouth-3'];

export const awsInstanceZone = [
  'us-east-1a',
  'us-east-1b',
  'us-east-1c',
  'us-east-1d',
  'us-east-1e',
  'us-east-1f',
  'us-east-2a',
  'us-east-2b',
  'us-east-2c',
  'us-west-1a',
  'us-west-1c',
  'us-west-2a',
  'us-west-2b',
  'us-west-2c',
  'us-west-2d',
  'ca-central-1a',
  'ca-central-1b',
  'ca-central-1d',
  'sa-east-1a',
  'sa-east-1b',
  'sa-east-1c',
  'eu-west-1a',
  'eu-west-1b',
  'eu-west-1c',
  'eu-west-2a',
  'eu-west-2b',
  'eu-west-2c',
  'eu-west-3a',
  'eu-west-3b',
  'eu-west-3c',
  'eu-central-1a',
  'eu-central-1b',
  'eu-central-1c',
  'eu-north-1a',
  'eu-north-1b',
  'eu-north-1c',
  'ap-northeast-1a',
  'ap-northeast-1c',
  'ap-northeast-1d',
  'ap-northeast-2a',
  'ap-northeast-2b',
  'ap-northeast-2c',
  'ap-southeast-1a',
  'ap-southeast-1b',
  'ap-southeast-1c',
  'ap-southeast-2a',
  'ap-southeast-2b',
  'ap-southeast-2c',
  'ap-south-1a',
  'ap-south-1b',
  'ap-south-1c',
  'me-south-1a',
  'me-south-1b',
  'me-south-1c',
  'af-south-1a',
  'af-south-1b',
  'af-south-1c',
];

export const colorsArray = [
  '#FCA5A5',
  '#4ADE80',
  '#60A5FA',
  '#FBBF24',
  '#A78BFA',
  '#F472B6',
  '#34D399',
  '#FCD34D',
  '#818CF8',
  '#D1D5DB',
  '#10B981',
  '#3B82F6',
  '#EC4899',
  '#F97316',
  '#F59E0B',
  '#6EE7B7',
  '#93C5FD',
  '#E11D48',
  '#A3E635',
  '#6366F1',
];
export interface DateRange {
  startDate: Date;
  endDate: Date | null;
  key: 'selection';
}

export interface MUiDateRange {
  startDate: number;
  endDate: number | null;
}

export interface Option {
  disabled?: boolean;
  value: string;
  text: string;
}

export interface CustomTabsProps {
  text: string;
  value: string;
}

export interface CustomDropDownProps {
  value: string;
  label: string;
}

export interface WorkloadObject {
  name: string;
  cloud_resource_id?: string;
  namespace: string;
  kind: string;
}

export interface SortOrderObject {
  name: string;
  order: string;
}

export interface TicketDataPojo {
  id: string;
  title: string;
  priority: string;
  aggregation_key: string;
  subject_type: string;
  subject_name: string;
  subject_namespace: string;
  account_id: string;
}

export interface SnackBarProps {
  message: string;
  severity?: string;
}

export interface Annotations {
  description: string;
  summary: string;
}

export interface Labels {
  severity: string;
}

export interface ApplicationStats {
  name: string;
  namespace: string;
  accountName: string;
  accountId: string;
  workloadId: string;
  nrequests: number | null;
  nerrors: number | null;
  nerrorscritical: number;
  nevents: number | null;
  neventscpu: number | null;
  neventsmemory: number | null;
  cpu: number | null;
  memoryp99: number | null;
  latency: number | null;
  rtt: number | null;
  optimize: number | null;
  maxCPUReq: number | null;
  maxMemoryReq: number | null;
  maxMemoryUsage: number | null;
  totalPods: number | null;
  readyPods: number | null;
  pod_error_count: number | null;
  application_error_count: number | null;
  max_cpu_limit: number | null;
  max_memory_limit: number | null;
  memory_p50: number | null;
  cpu_p50: number | null;
  total_slo_count: number | null;
  failed_slo_count: number;
}

const levenshteinDistance = (a: string, b: string) => {
  const lenA = a.length;
  const lenB = b.length;

  // Create a 2D array (matrix) to store the distances
  const dp = Array(lenA + 1)
    .fill(null)
    .map(() => Array(lenB + 1).fill(null));

  // Fill the first row and column of the matrix
  for (let i = 0; i <= lenA; i++) {
    dp[i][0] = i;
  }
  for (let j = 0; j <= lenB; j++) {
    dp[0][j] = j;
  }

  // Populate the rest of the matrix
  for (let i = 1; i <= lenA; i++) {
    for (let j = 1; j <= lenB; j++) {
      const cost = a[i - 1] === b[j - 1] ? 0 : 1;
      dp[i][j] = Math.min(
        dp[i - 1][j] + 1, // Deletion
        dp[i][j - 1] + 1, // Insertion
        dp[i - 1][j - 1] + cost // Substitution
      );
    }
  }

  // The last cell of the matrix is the Levenshtein distance
  return dp[lenA][lenB];
};

export const isAtMost70PercentDifferent = (str1: string, str2: string) => {
  const distance = levenshteinDistance(str1, str2);
  const maxLength = Math.max(str1.length, str2.length);
  const differencePercentage = (distance / maxLength) * 100;
  return differencePercentage <= 70;
};

export const buildContainerLabel = (value: string) => {
  const regex = /^\/k8s\/([^/]+)\/([^/]+)\/([^/]+)$/;
  const match = regex.exec(value);
  if (match) {
    const [, namespace, podname, container] = match;
    return {
      namespace: namespace || '',
      podname: podname || '',
      container: container || '',
    };
  }
  return value;
};

export const compareVersions = (version1: string, version2: string) => {
  if (version1 && version2) {
    const v1Parts = version1.split('.').map(Number);
    const v2Parts = version2.split('.').map(Number);

    while (v1Parts.length < v2Parts.length) {
      v1Parts.push(0);
    }
    while (v2Parts.length < v1Parts.length) {
      v2Parts.push(0);
    }

    for (let i = 0; i < v1Parts.length; i++) {
      if (v1Parts[i] > v2Parts[i]) {
        return true;
      }
      if (v1Parts[i] < v2Parts[i]) {
        return false;
      }
    }
  }
  return false;
};

export const extractNamespaceAndApplication = (value: string, type: string): string | undefined => {
  if (!value) {
    return value;
  }
  const valueArray = value.split('/').filter((e) => e !== '');
  if (valueArray.length === 4) {
    if (type === 'namespace') {
      return valueArray[1];
    } else if (type === 'application') {
      const secondLastHyphenIndex = valueArray[2].lastIndexOf('-', valueArray[2].lastIndexOf('-') - 1);
      const result = secondLastHyphenIndex !== -1 ? valueArray[2].substring(0, secondLastHyphenIndex) : valueArray[2];
      return result;
    }
  } else {
    return value;
  }
};

export function isRenderedInIframe(): boolean {
  return window.self !== window.top;
}

export const updateNavigationUrl = (tabValue: string, subTabValue: string, router: NextRouter, filterOptions: any[]) => {
  // Find fragments
  const selectedTab = filterOptions.find((t) => t.value === tabValue);
  let hash = selectedTab?.fragment || '';
  if (selectedTab?.tabOptions?.length > 0) {
    const selectedSubTab = selectedTab.tabOptions.find((st: any) => st.value === subTabValue);
    if (selectedSubTab?.fragment) {
      hash = hash ? `${hash}/${selectedSubTab.fragment}` : selectedSubTab.fragment;
    }
  }
  // Return OBJECT for router.push/replace
  return {
    pathname: router.pathname, // Keep the dynamic pattern
    query: router.query, // Keep the existing IDs
    hash: hash, // Update the hash
  };
};

export function extractWorkloadName(podName: string): string {
  const match = RegExp(/^(.*?)(-[a-z0-9]{5,}-[a-z0-9]{4,}|-\d+)$/).exec(podName);
  return match ? match[1] : podName;
}

export const convertObjectToArray = (obj: Record<string, unknown>): Array<Record<string, unknown>> => {
  return Object.keys(obj).map((key) => {
    const value = obj[key];
    if (typeof value === 'object' && value !== null && Object.keys(value).length > 0) {
      return { [key]: value };
    }
    return { [key]: {} };
  });
};

export const isMarkdown = (value: string): boolean => {
  if (typeof value !== 'string') {
    return false;
  }

  // Patterns for common Markdown syntax
  const patterns = [
    { regex: /^#{1,6}\s/m, weight: 2 }, // Headings (#, ##, ###)
    { regex: /\*\*[^*]+\*\*/g, weight: 4 }, // Bold (**bold**)
    { regex: /_[^_]+_/g, weight: 1 }, // Italic (_italic_)
    { regex: /`[^`]+`/g, weight: 2 }, // Inline code (`code`)
    { regex: /\[(.*?)\]\((.*?)\)/g, weight: 3 }, // Links [text](url)
    { regex: /!\[(.*?)\]\((.*?)\)/g, weight: 3 }, // Images ![alt](url)
    { regex: /^\s*[-*+]\s+/gm, weight: 1 }, // Unordered lists (- item, * item)
    { regex: /^\d+\.\s+/gm, weight: 1 }, // Ordered lists (1. item)
    { regex: /\n-{3,}\n/g, weight: 2 }, // Horizontal rule (---)
    { regex: />\s.*/g, weight: 1 }, // Blockquotes (> quote)
  ];

  let score = 0;
  patterns.forEach(({ regex, weight }) => {
    if (regex.test(value)) {
      score += (value.match(regex) || []).length * weight;
    }
  });
  const wordCount = value.split(/\s+/).length;
  const markdownRatio = score / wordCount;

  return score > 3 && markdownRatio > 0.1;
};

export const parseJSONSafely = (data: string): unknown => {
  try {
    return typeof data === 'string' && data.startsWith('{') ? JSON.parse(data.replace(/'/g, '"')) : data;
  } catch (error) {
    console.warn('Failed to parse JSON:', data, error);
    return data;
  }
};

export const prettifyName = (name: string): string => {
  // "ErrorRate" -> "Error Rate"
  // CPUThrottlingTime -> "CPU Throttling Time"
  return name.replace(/([a-z])([A-Z])/g, '$1 $2');
};

export const exitCodeMapping = {
  // Original codes
  0: 'Container stopped normally.',
  1: 'Application or config error OR General container error in Kubernetes',
  125: 'Docker run command failed.',
  126: "Command in image couldn't be invoked.",
  127: 'File or directory not found.',
  134: 'Container aborted itself (SIGABRT).',
  139: 'Memory access violation (Segfault).',

  // Additional container exit codes
  2: 'Misuse of shell builtins or command line usage error.',
  130: 'Container terminated by CTRL+C (SIGINT) OR Process interrupted in Kubernetes.',
  131: 'Container terminated due to SIGQUIT.',
  132: 'Container terminated due to illegal instruction (SIGILL).',
  133: 'Container terminated due to SIGTRAP (trace/breakpoint trap).',
  135: 'Container terminated due to SIGBUS (bus error).',
  136: 'Container terminated due to floating point exception (SIGFPE).',
  138: 'Container terminated due to SIGUSR1.',
  140: 'Container terminated due to SIGUSR2.',
  141: 'Container terminated due to SIGPIPE (broken pipe).',
  142: 'Container terminated due to SIGALRM (alarm clock).',
  143: 'Container terminated gracefully (SIGTERM).',

  // OOM and resource limits
  9: 'Container killed (SIGKILL) by host system.',
  137: 'Container out of memory (OOM) or killed by host OR Forcefully killed by system (SIGKILL)',

  // Docker-specific codes
  255: 'Docker daemon or client error.',

  // Kubernetes-specific codes
  128: 'Invalid argument in Kubernetes OR Invalid exit code used.',

  129: 'Operation not permitted in Kubernetes.',
  250: 'Container runtime error in Kubernetes.',
  254: 'Kubernetes pod sandbox creation error.',

  // High-numbered exit codes
  243: 'Container terminated due to external service dependency failure.',
  244: 'Container terminated due to unsatisfied config requirements.',
  245: 'Container terminated due to volume mount issues.',
  246: 'Container terminated due to networking issues.',
  247: 'Container terminated due to resource quota exceeded.',
  248: 'Container terminated due to initialization timeout.',
  249: 'Container terminated due to readiness probe failure.',

  // Exit code ranges
  '1-127': 'Application-specific error codes.',
  '128+n': 'Fatal error signal "n" (Linux signal + 128).',
};

export function isValidSeverity(severity: string): severity is 'success' | 'info' | 'warning' | 'error' {
  return ['success', 'info', 'warning', 'error'].includes(severity);
}

/**
 * Generates a formatted label for a tool, appending a warning icon if configuration is missing.
 * @param tool - The tool object containing name, type, and configuration status.
 * @returns A formatted string label.
 */
export const getToolLabel = (tool: { name: string; type: string; needs_config?: boolean; is_configured?: boolean }) => {
  const warningIcon = tool.needs_config && !tool.is_configured ? ' ⚠️' : '';
  return `${tool.name} - ${tool.type}${warningIcon}`;
};

export const getLlmIdentifierValidationMessage = (value: string): string => {
  if (!value || typeof value !== 'string') {
    return 'Name is required.';
  }
  if (!/^[a-zA-Z]/.test(value)) {
    return 'Name must start with a letter (a-z or A-Z).';
  }

  if (value.length < 3) {
    return 'Name must be at least 3 characters long.';
  }

  if (value.length > 50) {
    return 'Name must be 50 characters or less.';
  }

  if (!/^[a-zA-Z]\w*$/.test(value)) {
    return 'Name can only contain letters, numbers, and underscores.';
  }

  return '';
};

export function parseHttpResponseBodyMessage(httpResponse: any): string {
  const errors = httpResponse?.errors;
  if (Array.isArray(errors) && errors.length > 0) {
    return (
      errors[0]?.extensions?.internal?.response?.body?.[0]?.message ||
      errors[0]?.extensions?.internal?.response?.body?.errors?.[0]?.message ||
      errors[0]?.extensions?.internal?.error?.message ||
      errors[0]?.message ||
      ''
    );
  }
  return '';
}

export function generateRandomUUID(input: string): string {
  const MY_NAMESPACE = '6ba7b810-9dad-11d1-80b4-00c04fd430c8';
  return v5(input, MY_NAMESPACE);
}

export const formatValueWithUnit = (value: number, title: string) => {
  if (title !== 'Memory') {
    return { value };
  }

  if (value < 1024) {
    return { value, unit: 'B' };
  }
  if (value < 1024 ** 2) {
    return { value: value / 1024, unit: 'KB' };
  }
  if (value < 1024 ** 3) {
    return { value: value / 1024 ** 2, unit: 'MB' };
  }
  if (value < 1024 ** 4) {
    return { value: value / 1024 ** 3, unit: 'GB' };
  }
  return { value: value / 1024 ** 4, unit: 'TB' };
};

export function safeJSONParse(data: string) {
  if (typeof data !== 'string') {
    console.warn('Expected a string to parse as JSON, but got:', typeof data);
    return null;
  }

  // Skip JSON parsing if the string doesn't look like JSON
  const trimmed = data.trim();
  if (!trimmed.startsWith('{') && !trimmed.startsWith('[')) {
    return null;
  }

  try {
    return JSON.parse(data);
  } catch (error) {
    console.error('Invalid JSON string:', error, data);
    return null;
  }
}

export function parseRelayHttpResponseBodyMessage(httpResponse: any): string {
  const error = httpResponse?.data?.data?.error;
  if (error) {
    return error || '';
  }
  return '';
}
