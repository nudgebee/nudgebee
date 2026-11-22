import { clustersCollectingClickHouseLogs, showsAgentProviderToggle } from '../clickHouseLogsToggle';

describe('showsAgentProviderToggle', () => {
  const logs = { integrationType: 'otel_clickhouse', providerKey: 'default_log_provider' };

  it('hides ClickHouse logs where the cluster does not collect its logs there', () => {
    expect(showsAgentProviderToggle({ ...logs, isOn: false, logsCollected: false })).toBe(false);
  });

  it('shows ClickHouse logs where the cluster collects them', () => {
    expect(showsAgentProviderToggle({ ...logs, isOn: false, logsCollected: true })).toBe(true);
  });

  it('keeps a toggle that is already on, so it can be turned off', () => {
    expect(showsAgentProviderToggle({ ...logs, isOn: true, logsCollected: false })).toBe(true);
  });

  it('leaves every other toggle alone', () => {
    expect(showsAgentProviderToggle({ integrationType: 'otel_clickhouse', providerKey: 'default_traces_provider' })).toBe(true);
    expect(showsAgentProviderToggle({ integrationType: 'loki', providerKey: 'default_log_provider' })).toBe(true);
  });
});

describe('clustersCollectingClickHouseLogs', () => {
  it('keeps only clusters whose agent reports clickhouseLogsCollected=true', () => {
    const agents = [
      { cloud_account_id: 'a', connection_status: { clickhouseLogsCollected: true } },
      { cloud_account_id: 'b', connection_status: { clickhouseLogsCollected: false } },
      { cloud_account_id: 'c', connection_status: { clickhouseLogsConnection: true } },
      { cloud_account_id: 'd' },
    ];
    expect([...clustersCollectingClickHouseLogs(agents)]).toEqual(['a']);
    expect(clustersCollectingClickHouseLogs(undefined).size).toBe(0);
  });
});
