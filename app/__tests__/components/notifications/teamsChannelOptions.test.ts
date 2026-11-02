import { mappingFromValue, splitTeamsValue, teamsChannelOptions } from '@components/notifications/rules/destinations';

jest.mock('@api1/notification', () => ({}));
jest.mock('@api1/autoPlaybook', () => ({}));
jest.mock('@api1/account', () => ({}));
jest.mock('@api1/integrations', () => ({}));

const eng = { value: 'team-1', label: 'Eng', channels: [{ value: 'c-1', label: 'alerts' }] };
const ops = { value: 'team-2', label: 'Ops', channels: [{ value: 'c-2', label: 'alerts' }] };

describe('teamsChannelOptions', () => {
  it('lists each channel under its team, the value naming team and channel', () => {
    expect(teamsChannelOptions([eng, ops])).toEqual([
      { value: 'team-1/c-1', label: 'alerts', group: 'Eng', searchText: 'Eng', selectedLabel: 'Eng › alerts' },
      { value: 'team-2/c-2', label: 'alerts', group: 'Ops', searchText: 'Ops', selectedLabel: 'Ops › alerts' },
    ]);
  });

  it('shows just the channel once picked when there is one team', () => {
    expect(teamsChannelOptions([eng])[0].selectedLabel).toBe('alerts');
  });

  it('lists nothing for a team whose channels are missing', () => {
    expect(teamsChannelOptions([{ value: 'team-1', label: 'Eng' } as any])).toEqual([]);
  });

  it('falls back to the team id for a team with no name', () => {
    const [first] = teamsChannelOptions([{ ...eng, label: '' }, ops]);
    expect(first.group).toBe('team-1');
    expect(first.selectedLabel).toBe('team-1 › alerts');
  });

  it('a picked option saves both the team and the channel', () => {
    const [, picked] = teamsChannelOptions([eng, ops]);
    expect(splitTeamsValue(picked.value)).toEqual({ teamId: 'team-2', channelId: 'c-2' });
    const destination = { platform: 'ms_teams', label: 'Teams', teams: [eng, ops], options: [], loading: false };
    expect(mappingFromValue(destination, picked.value)?.channels).toEqual({
      team_name: 'Ops',
      team_id: 'team-2',
      channels: [{ name: 'alerts', id: 'c-2' }],
    });
  });
});
