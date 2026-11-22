import { normalizeIntegrationAccounts } from '@lib/integrationsSummary';

describe('normalizeIntegrationAccounts', () => {
  it('shows Microsoft Teams as connected only from its integration rows', () => {
    const legacyOnly = normalizeIntegrationAccounts({ messaging_platforms: [{ platform: 'ms_teams', channels: { team_name: 'Ops' } }] });
    expect(legacyOnly.MSTEAMS).toBeUndefined();

    const bound = normalizeIntegrationAccounts({
      messaging_platforms: [{ platform: 'ms_teams', channels: { team_name: 'Ops' } }],
      integrations: [{ type: 'ms_teams', status: 'enabled', name: 'tenant-a' }],
    });
    expect(bound.MSTEAMS).toEqual([{ type: 'ms_teams', status: 'active', name: 'tenant-a', channels: ['tenant-a'] }]);
  });
});
