/**
 * Who serves a Kubernetes cluster's logs, and whether that works, read from the agent's
 * connection_status. The agent page, the cluster status dot and the admin Connections
 * view all ask this one function, so they cannot disagree.
 *
 * - `integration`: a non-agent integration (hosted Loki, Datadog, …) serves the logs.
 *   The provider-status check stamps its probe result with an `integration_id`.
 * - `clickhouse`: the account's logs default is the agent's ClickHouse. The stamp names
 *   it; health is the agent's live ClickHouse logs check, which means "ClickHouse answers
 *   and the logs table is present", not that logs are arriving. An error on the entry
 *   (the server found it unusable) reads as down.
 * - `agent`: the agent's own logs provider, judged by its logsConnection flag.
 *
 * See api-server observability/provider_status.go for the stamp.
 *
 * @param {object} connectionStatus the agent row's connection_status
 * @param {object} [providerStatus] the stamp, when the caller holds it separately
 * @returns {{ by: 'integration' | 'clickhouse' | 'agent', connected: boolean, error: string, entry: object | null }}
 */
export const logsServedBy = (connectionStatus, providerStatus = connectionStatus?.providerStatus) => {
  const entry = providerStatus?.logs;
  if (entry?.integration_id) {
    return { by: 'integration', connected: !!entry.connected, error: entry.error || '', entry };
  }
  if (entry?.provider === 'otel_clickhouse' && entry?.source === 'agent') {
    return {
      by: 'clickhouse',
      connected: !entry.error && !!connectionStatus?.clickhouseLogsConnection,
      error: entry.error || connectionStatus?.clickhouseLogsConnectionError || '',
      entry,
    };
  }
  return { by: 'agent', connected: !!connectionStatus?.logsConnection, error: connectionStatus?.logsConnectionError || '', entry: null };
};

/** How the ClickHouse logs check reads wherever its result is shown. */
export const CLICKHOUSE_LOGS_CHECK = 'Logs table present';
