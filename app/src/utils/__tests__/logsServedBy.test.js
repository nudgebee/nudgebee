import { logsServedBy } from '../logsServedBy';

describe('logsServedBy', () => {
  it('reads the agent flag when nothing else serves the logs', () => {
    expect(logsServedBy({ logsConnection: true })).toMatchObject({ by: 'agent', connected: true });
    expect(logsServedBy({ logsConnection: false, logsConnectionError: 'loki: 502' })).toMatchObject({
      by: 'agent',
      connected: false,
      error: 'loki: 502',
    });
    expect(logsServedBy(null)).toMatchObject({ by: 'agent', connected: false });
  });

  it('reads the probe result of an integration that serves the logs', () => {
    const providerStatus = { logs: { provider: 'loki', source: 'user', integration_id: 'l-1', connected: false, error: '401' } };
    expect(logsServedBy({ logsConnection: true, providerStatus })).toMatchObject({ by: 'integration', connected: false, error: '401' });
  });

  // The agent names its Loki as its logs provider whatever the default is, so with
  // ClickHouse serving the logs, logsConnection describes a backend nothing reads from.
  it("reads the live ClickHouse logs check when the agent's ClickHouse serves the logs", () => {
    const providerStatus = { logs: { provider: 'otel_clickhouse', source: 'agent' } };
    expect(logsServedBy({ logsConnection: false, clickhouseLogsConnection: true, providerStatus })).toMatchObject({
      by: 'clickhouse',
      connected: true,
      error: '',
    });
    expect(
      logsServedBy({ logsConnection: true, clickhouseLogsConnection: false, clickhouseLogsConnectionError: 'table missing', providerStatus })
    ).toMatchObject({ by: 'clickhouse', connected: false, error: 'table missing' });
  });

  it('treats a ClickHouse entry carrying an error as down', () => {
    const providerStatus = { logs: { provider: 'otel_clickhouse', source: 'agent', error: 'not reachable right now' } };
    expect(logsServedBy({ clickhouseLogsConnection: true, providerStatus })).toMatchObject({
      by: 'clickhouse',
      connected: false,
      error: 'not reachable right now',
    });
  });

  it('takes the stamp from its own argument when one is passed', () => {
    const providerStatus = { logs: { provider: 'otel_clickhouse', source: 'agent' } };
    expect(logsServedBy({ clickhouseLogsConnection: true }, providerStatus)).toMatchObject({ by: 'clickhouse', connected: true });
  });
});
