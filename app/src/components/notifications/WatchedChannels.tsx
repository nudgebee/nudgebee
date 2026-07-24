import React, { useEffect, useState, useCallback, useMemo } from 'react';
import { Box, Typography } from '@mui/material';
import { Banner } from '@ui/Banner';
import { Switch } from '@ui/Switch';
import SearchInput from '@ui/SearchInput';
import { Button as DsButton } from '@ui/Button';
import Text from '@shared/format/Text';
import CustomTable from '@shared/tables/CustomTable';
import { ListingLayout } from '@ui/ListingLayout';
import { toast as snackbar } from '@ui/Toast';
import apiNotifications from '@api1/notification';
import { hasWriteAccess, fetchFeatureFlagsForTenant, hasFeatureAccessCached } from '@lib/auth';
import { ds } from '@utils/colors';

interface WatchedChannelsProps {
  provider: string;
  isConfigured: boolean;
}

interface WatchableChannel {
  id: string;
  name: string;
  is_private: boolean;
  is_member: boolean;
  watched: boolean;
  retention_days: number | null;
}

const WatchedChannels: React.FC<WatchedChannelsProps> = ({ provider, isConfigured }) => {
  const [flagEnabled, setFlagEnabled] = useState(false);
  const [channels, setChannels] = useState<WatchableChannel[]>([]);
  const [teamId, setTeamId] = useState('');
  const [partial, setPartial] = useState(false);
  const [isLoading, setIsLoading] = useState(false);
  const [search, setSearch] = useState('');
  const [togglingIds, setTogglingIds] = useState<string[]>([]);

  useEffect(() => {
    fetchFeatureFlagsForTenant().finally(() => setFlagEnabled(hasFeatureAccessCached('CHANNEL_AWARENESS')));
  }, []);

  const loadChannels = useCallback(async () => {
    setIsLoading(true);
    try {
      const result: any = await apiNotifications.listWatchableChannels(provider);
      if (result?.error) {
        snackbar.error(result.error.message || 'Failed to load channels');
        return;
      }
      setChannels(result?.data ?? []);
      setTeamId(result?.team_id ?? '');
      setPartial(Boolean(result?.partial));
    } finally {
      setIsLoading(false);
    }
  }, [provider]);

  useEffect(() => {
    if (flagEnabled && isConfigured) {
      loadChannels();
    }
  }, [flagEnabled, isConfigured, loadChannels]);

  const handleToggle = useCallback(
    async (channel: WatchableChannel, next: boolean) => {
      setTogglingIds((prev) => [...prev, channel.id]);
      try {
        const result: any = next
          ? await apiNotifications.enableChannelWatch({
              platform: provider,
              channelId: channel.id,
              teamId,
              channelName: channel.name,
            })
          : await apiNotifications.disableChannelWatch({ platform: provider, channelId: channel.id, teamId });
        if (result?.error) {
          snackbar.error(result.error.message || `Failed to ${next ? 'start' : 'stop'} watching`);
          return;
        }
        setChannels((prev) => prev.map((c) => (c.id === channel.id ? { ...c, watched: next } : c)));
        snackbar.success(next ? `Watching #${channel.name} — a notice was posted in the channel` : `Stopped watching #${channel.name}`);
      } finally {
        setTogglingIds((prev) => prev.filter((id) => id !== channel.id));
      }
    },
    [provider, teamId]
  );

  const filtered = useMemo(() => channels.filter((channel) => channel.name?.toLowerCase().includes(search.toLowerCase())), [channels, search]);

  const headers = ['Channel', 'Visibility', { name: 'Watching', width: '120px' }];

  const tableData = useMemo(
    () =>
      filtered.map((channel) => [
        { component: <Text value={`#${channel.name}`} /> },
        { component: <Text value={channel.is_private ? 'Private' : 'Public'} /> },
        {
          component: (
            <Switch
              size='sm'
              checked={Boolean(channel.watched)}
              disabled={!hasWriteAccess()}
              loading={togglingIds.includes(channel.id)}
              onChange={(_event, checked) => handleToggle(channel, checked)}
            />
          ),
        },
      ]),
    [filtered, togglingIds, handleToggle]
  );

  if (!flagEnabled || !isConfigured) {
    return null;
  }

  return (
    <Box mt={ds.space[6]}>
      <ListingLayout id={`${provider}-watched-channels`}>
        <ListingLayout.Toolbar
          actions={
            <Box sx={{ display: 'flex', alignItems: 'center', gap: 'var(--ds-space-3)' }}>
              <SearchInput value={search} onChange={(next: string) => setSearch(next)} onClear={() => setSearch('')} label='Search channels' />
              <DsButton id='refresh-watched-channels-btn' tone='secondary' size='md' onClick={loadChannels} disabled={isLoading}>
                Refresh
              </DsButton>
            </Box>
          }
        >
          <Box display='flex' flexDirection='column'>
            <Typography sx={{ fontFamily: 'var(--ds-font-display)' }} fontSize={ds.text.title} fontWeight={ds.weight.semibold} color={ds.gray[700]}>
              Watched Channels
            </Typography>
            <Typography fontSize={ds.text.caption} color={ds.gray[600]}>
              Nubi follows watched channels to build context it can use when someone @mentions it there.
            </Typography>
          </Box>
        </ListingLayout.Toolbar>
        <ListingLayout.Body>
          <Box sx={{ display: 'flex', flexDirection: 'column', gap: 'var(--ds-space-3)' }}>
            {partial ? (
              <Banner
                tone='warning'
                surface='section'
                message='Slack rate-limited the channel listing — some channels may be missing. Refresh to retry.'
              />
            ) : (
              <Banner
                tone='info'
                surface='section'
                message='Turning a channel on posts a public notice there, and Nubi only ever acts when explicitly @mentioned. Anyone with write access can turn watching off.'
              />
            )}
            <CustomTable id={`${provider}-watched-channels-table`} headers={headers} tableData={tableData} loading={isLoading} />
          </Box>
        </ListingLayout.Body>
      </ListingLayout>
    </Box>
  );
};

export default WatchedChannels;
