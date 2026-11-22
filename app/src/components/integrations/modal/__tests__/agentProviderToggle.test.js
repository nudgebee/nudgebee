/**
 * Turning an agent integration's "default provider" on or off for one cluster must
 * send only that cluster's value. The backend records a default the user turned on,
 * so the agent's heartbeat stops taking it back; re-sending every cluster's value as
 * it was when the modal opened would record choices the user never made.
 * The account list still names every linked cluster, because the save unlinks any
 * cluster left out of it.
 */
import { buildAgentProviderTogglePayload } from '../agentProviderToggle';

const base = {
  editData: { id: 'int-ch', name: 'otel_clickhouse', source: 'agent' },
  integrationName: 'otel_clickhouse',
  accountIds: ['acc-1', 'acc-2', 'acc-3'],
  providerKey: 'default_log_provider',
};

describe('buildAgentProviderTogglePayload', () => {
  it('sends only the toggled cluster in the per-cluster value', () => {
    const payload = buildAgentProviderTogglePayload({ ...base, cloudAccountId: 'acc-2', newValue: true });

    expect(payload.integration_config_values).toEqual([
      { name: 'default_log_provider', value: JSON.stringify({ 'acc-2': 'true' }), is_encrypted: false },
    ]);
  });

  it('keeps every linked cluster in account_ids so none is unlinked', () => {
    const payload = buildAgentProviderTogglePayload({ ...base, cloudAccountId: 'acc-2', newValue: false });

    expect(payload.account_ids).toEqual(['acc-1', 'acc-2', 'acc-3']);
    expect(JSON.parse(payload.integration_config_values[0].value)).toEqual({ 'acc-2': 'false' });
  });

  it('updates the existing integration and keeps its source', () => {
    const payload = buildAgentProviderTogglePayload({ ...base, cloudAccountId: 'acc-1', newValue: true });

    expect(payload).toMatchObject({
      integration_id: 'int-ch',
      integration_name: 'otel_clickhouse',
      integration_config_name: 'otel_clickhouse',
      skip_validation: true,
      source: 'agent',
    });
  });
});
