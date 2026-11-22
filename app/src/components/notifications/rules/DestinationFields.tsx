import React from 'react';
import { Box, Typography } from '@mui/material';
import { Select } from '@ui/Select';
import { Link } from '@ui/Link';
import SafeIcon from '@shared/icons/SafeIcon';
import { SlackIcon, MSTeamsIcon, GChatIcon, DiscordIcon } from '@assets';
import { ds } from '@utils/colors';
import { Destination, teamsChannelOptions } from './destinations';

const ICONS: Record<string, any> = { slack: SlackIcon, ms_teams: MSTeamsIcon, google_chat: GChatIcon, discord: DiscordIcon };

export const PlatformIcon: React.FC<{ platform: string; size?: number }> = ({ platform, size = 14 }) => {
  const icon = ICONS[platform.replace(/_webhook$/, '')];
  return icon ? <SafeIcon src={icon} alt='' width={size} height={size} /> : null;
};

// Ids the e2e suite knows the editor's pickers by.
export const EDITOR_PICKER_IDS: Record<string, string> = {
  slack: 'slack-channel',
  ms_teams: 'msteam-channel',
  google_chat: 'gchat-channel',
  discord: 'discord-channel',
};

interface Props {
  idPrefix: string;
  ids?: Record<string, string>;
  destinations: Destination[];
  // Picked value per platform ('' = none).
  value: Record<string, string>;
  onChange: (next: Record<string, string>) => void;
}

// One picker per place the tenant can send to; each platform takes one channel.
const DestinationFields: React.FC<Props> = ({ idPrefix, ids = {}, destinations, value, onChange }) => {
  const idFor = (key: string) => ids[key] || `${idPrefix}-${key}`;
  const set = (platform: string, next: string) => onChange({ ...value, [platform]: next });
  if (!destinations.length) {
    return (
      <Typography sx={{ fontSize: ds.text.body, color: ds.gray[600] }}>
        No messaging app is connected yet. Connect Slack, Teams, Google Chat or Discord under Integrations.
      </Typography>
    );
  }
  return (
    <Box sx={{ display: 'grid', gridTemplateColumns: 'repeat(auto-fill, minmax(220px, 1fr))', gap: ds.space[3] }}>
      {destinations.map((d) => {
        const label = (
          <Box component='span' sx={{ display: 'inline-flex', alignItems: 'center', gap: ds.space[1] }}>
            <PlatformIcon platform={d.platform} />
            {d.label}
          </Box>
        );
        return (
          <Box key={d.platform} sx={{ display: 'flex', flexDirection: 'column', gap: ds.space[1] }}>
            <Select
              id={idFor(d.platform)}
              label={label}
              size='sm'
              placeholder='None'
              clearable
              loading={d.loading}
              value={value[d.platform] || null}
              options={d.platform === 'ms_teams' ? teamsChannelOptions(d.teams || []) : d.options}
              grouped={d.platform === 'ms_teams'}
              defaultGroupsOpen
              onChange={(next) => set(d.platform, next)}
            />
            {d.hint && !d.options.length && (
              <Typography sx={{ fontSize: ds.text.small, color: ds.gray[600] }}>
                {d.hint.hint}{' '}
                {d.hint.inviteUrl && (
                  <Link href={d.hint.inviteUrl} openInNew>
                    Invite the bot
                  </Link>
                )}
              </Typography>
            )}
          </Box>
        );
      })}
    </Box>
  );
};

export default DestinationFields;
