import { summarizeMCPHealth } from '../mcpHealthSummary';

const vmAgent = (health, accounts) => ({
  health,
  integration_config_values: [{ name: 'connection_mode', value: 'vm_agent' }],
  integrations_cloud_accounts: accounts.map((id) => ({ cloud_account_id: id })),
});

describe('summarizeMCPHealth', () => {
  it('reports no verdict for a NULL or reset health', () => {
    expect(summarizeMCPHealth({ health: null })).toMatchObject({ status: 'none', label: 'Not checked yet', tone: 'neutral' });
    // An entry that only rate-limits probes has no status yet.
    expect(summarizeMCPHealth({ health: '{"*":{"last_probe_at":"2026-10-13T10:00:00Z"}}' }).status).toBe('none');
  });

  it('reads the * scope of a direct integration, as a string or an object', () => {
    const health = { '*': { status: 'connected', tool_count: 4, last_checked_at: '2026-10-13T10:00:00Z' } };
    for (const h of [health, JSON.stringify(health)]) {
      expect(summarizeMCPHealth({ health: h })).toMatchObject({
        status: 'connected',
        label: 'Connected',
        tone: 'success',
        toolCount: 4,
        checkedAt: '2026-10-13T10:00:00Z',
        reason: '',
        scopeNote: '',
      });
    }
  });

  it('shows the reason of a failure', () => {
    const s = summarizeMCPHealth({ health: { '*': { status: 'auth_failed', reason: 'MCP server returned status 401' } } });
    expect(s).toMatchObject({ label: 'Login rejected', tone: 'critical', reason: 'MCP server returned status 401' });
  });

  it('takes the worst linked account of a vm_agent integration and counts it', () => {
    const health = {
      a1: { status: 'connected', tool_count: 2 },
      a2: { status: 'unreachable', reason: 'agent not connected' },
      gone: { status: 'auth_failed' },
    };
    expect(summarizeMCPHealth(vmAgent(health, ['a1', 'a2', 'a3']))).toMatchObject({
      status: 'unreachable',
      label: "Can't reach server",
      reason: 'agent not connected',
      scopeNote: '1 of 3 accounts',
    });
    // An account not checked yet outranks connected ones; an unlinked account is ignored.
    expect(summarizeMCPHealth(vmAgent(health, ['a1', 'a3']))).toMatchObject({ status: 'none', scopeNote: '1 of 2 accounts' });
    expect(summarizeMCPHealth(vmAgent({ a1: { status: 'connected' } }, ['a1'])).scopeNote).toBe('');
  });
});
