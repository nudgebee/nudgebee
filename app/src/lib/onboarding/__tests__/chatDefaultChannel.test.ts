import { chatDefaultChannelByType } from '@lib/onboarding/itemEvaluators';

const row = (type: string, cfg: Record<string, string>, status = 'enabled') => ({
  type,
  status,
  integration_config_values: Object.entries(cfg).map(([name, value]) => ({ name, value })),
});

// Mirrors notifications-server's `_default_channel_shape` and
// `_resolve_default_gchat_space` — a platform this calls ready must be one
// the sender can actually post to.
describe('chatDefaultChannelByType', () => {
  it('reads Slack and Discord from default_channel_id', () => {
    expect(chatDefaultChannelByType([row('slack', { default_channel_id: 'C1' }), row('discord', { team_name: 'x' })], [])).toEqual({
      slack: true,
      discord: false,
    });
  });

  it('needs both team and channel for Teams', () => {
    expect(chatDefaultChannelByType([row('ms_teams', { default_channel_id: 'C1' })], [])).toEqual({ ms_teams: false });
    expect(chatDefaultChannelByType([row('ms_teams', { default_channel_id: 'C1', default_team_id: 'T1' })], [])).toEqual({ ms_teams: true });
  });

  it('treats a single Google Chat space as the default, otherwise the flagged one', () => {
    expect(chatDefaultChannelByType([row('google_chat_space', {})], [])).toEqual({ google_chat_space: true });
    expect(chatDefaultChannelByType([row('google_chat_space', {}), row('google_chat_space', {})], [])).toEqual({ google_chat_space: false });
    expect(chatDefaultChannelByType([row('google_chat_space', {}), row('google_chat_space', { is_default: 'true' })], [])).toEqual({
      google_chat_space: true,
    });
  });

  it('flags a second workspace with no default even when the first has one', () => {
    expect(chatDefaultChannelByType([row('slack', { default_channel_id: 'C1' }), row('slack', {})], [])).toEqual({ slack: false });
  });

  it('ignores disabled rows', () => {
    expect(chatDefaultChannelByType([row('google_chat_space', {}), row('google_chat_space', {}, 'disabled')], [])).toEqual({
      google_chat_space: true,
    });
  });

  it('reads legacy messaging_platforms channels when no integration row exists', () => {
    expect(
      chatDefaultChannelByType(
        [],
        [
          { platform: 'slack', channels: JSON.stringify({ id: 'C1', name: 'alerts' }) },
          { platform: 'discord', channels: null },
          { platform: 'ms_teams', channels: { team_id: 'T1', channels: [{ id: 'C1', name: 'General' }] } },
        ]
      )
    ).toEqual({ slack: true, discord: false, ms_teams: true });
  });

  it('lets an integration row win over a legacy row for the same platform', () => {
    expect(
      chatDefaultChannelByType([row('slack', { team_name: 'x' })], [{ platform: 'slack', channels: JSON.stringify({ id: 'C1', name: 'alerts' }) }])
    ).toEqual({ slack: false });
  });

  it('accepts config values serialized as a JSON string', () => {
    expect(
      chatDefaultChannelByType(
        [{ type: 'slack', status: 'enabled', integration_config_values: JSON.stringify([{ name: 'default_channel_id', value: 'C1' }]) }],
        []
      )
    ).toEqual({ slack: true });
  });
});
