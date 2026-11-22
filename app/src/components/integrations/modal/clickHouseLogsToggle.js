/**
 * The agent's ClickHouse row is linked to nearly every cluster through traces, but it
 * serves a cluster's logs only where the agent reports collecting its container logs
 * there (`clickhouseLogsCollected`). Elsewhere the logs table is empty or close to it,
 * so the row's Logs toggle is not shown. A toggle already on stays, so it can be
 * turned off.
 */
export const showsAgentProviderToggle = ({ integrationType, providerKey, isOn, logsCollected }) =>
  integrationType !== 'otel_clickhouse' || providerKey !== 'default_log_provider' || !!isOn || !!logsCollected;

/** The ids of the clusters whose agent reports collecting its container logs into ClickHouse. */
export const clustersCollectingClickHouseLogs = (agents) =>
  new Set((Array.isArray(agents) ? agents : []).filter((a) => a?.connection_status?.clickhouseLogsCollected === true).map((a) => a.cloud_account_id));
