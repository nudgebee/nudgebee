import React, { useEffect, useState, useCallback, useMemo } from 'react';
import { Box, Typography } from '@mui/material';
import { Banner } from '@ui/Banner';
import { Switch } from '@ui/Switch';
import { Checkbox } from '@ui/Checkbox';
import { Modal } from '@ui/Modal';
import SearchInput from '@ui/SearchInput';
import { Button as DsButton } from '@ui/Button';
import Text from '@shared/format/Text';
import Datetime from '@shared/format/Datetime';
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
  watched_since: string | null;
  watched_by: string | null;
}

const WatchedChannels: React.FC<WatchedChannelsProps> = ({ provider, isConfigured }) => {
  const [flagEnabled, setFlagEnabled] = useState(false);
  const [allChannels, setAllChannels] = useState<WatchableChannel[]>([]);
  const [teamId, setTeamId] = useState('');
  const [partial, setPartial] = useState(false);
  const [isLoading, setIsLoading] = useState(false);
  const [togglingIds, setTogglingIds] = useState<string[]>([]);

  const [pickerOpen, setPickerOpen] = useState(false);
  const [pickerSearch, setPickerSearch] = useState('');
  const [selectedIds, setSelectedIds] = useState<string[]>([]);
  const [saving, setSaving] = useState(false);

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
      setAllChannels(result?.data ?? []);
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

  const watched = useMemo(() => allChannels.filter((channel) => channel.watched), [allChannels]);

  const candidates = useMemo(
    () => allChannels.filter((channel) => !channel.watched).filter((channel) => channel.name?.toLowerCase().includes(pickerSearch.toLowerCase())),
    [allChannels, pickerSearch]
  );

  const handleUnwatch = useCallback(
    async (channel: WatchableChannel) => {
      setTogglingIds((prev) => [...prev, channel.id]);
      try {
        const result: any = await apiNotifications.disableChannelWatch({
          platform: provider,
          channelId: channel.id,
          teamId,
        });
        if (result?.error) {
          snackbar.error(result.error.message || 'Failed to stop watching');
          return;
        }
        setAllChannels((prev) => prev.map((c) => (c.id === channel.id ? { ...c, watched: false } : c)));
        snackbar.success(`Stopped watching #${channel.name}`);
      } finally {
        setTogglingIds((prev) => prev.filter((id) => id !== channel.id));
      }
    },
    [provider, teamId]
  );

  const openPicker = () => {
    setPickerSearch('');
    setSelectedIds([]);
    setPickerOpen(true);
  };

  const toggleSelected = (channelId: string, next: boolean) => {
    setSelectedIds((prev) => (next ? [...prev, channelId] : prev.filter((id) => id !== channelId)));
  };

  const handleWatchSelected = async () => {
    setSaving(true);
    const failedIds: string[] = [];
    try {
      for (const channelId of selectedIds) {
        const channel = allChannels.find((c) => c.id === channelId);
        const result: any = await apiNotifications.enableChannelWatch({
          platform: provider,
          channelId,
          teamId,
          channelName: channel?.name,
        });
        if (result?.error) {
          snackbar.error(`#${channel?.name || channelId}: ${result.error.message || 'failed'}`);
          failedIds.push(channelId);
        }
      }
      const succeeded = selectedIds.length - failedIds.length;
      if (succeeded > 0) {
        snackbar.success(
          succeeded === 1 ? 'Watching 1 channel — a notice was posted there' : `Watching ${succeeded} channels — a notice was posted in each`
        );
        await loadChannels();
      }
      // Failed channels stay selected so a retry is one click away.
      if (failedIds.length === 0) {
        setPickerOpen(false);
      } else {
        setSelectedIds(failedIds);
      }
    } finally {
      setSaving(false);
    }
  };

  const headers = ['Channel', 'Retention', { name: 'Watching', width: '110px' }];

  const tableData = useMemo(
    () =>
      watched.map((channel) => [
        {
          component: (
            <Box display='flex' flexDirection='column'>
              <Text value={`#${channel.name}${channel.is_private ? ' (private)' : ''}`} />
              {channel.watched_since || channel.watched_by ? (
                <Typography fontSize={ds.text.caption} color={ds.gray[600]}>
                  {channel.watched_since ? (
                    <>
                      Watching since <Datetime value={channel.watched_since} />
                    </>
                  ) : (
                    'Watching'
                  )}
                  {channel.watched_by ? ` · by ${channel.watched_by}` : ''}
                </Typography>
              ) : null}
            </Box>
          ),
        },
        { component: <Text value={`${channel.retention_days ?? 30}d`} /> },
        {
          component: (
            <Switch
              size='sm'
              checked
              disabled={!hasWriteAccess()}
              loading={togglingIds.includes(channel.id)}
              onChange={() => handleUnwatch(channel)}
            />
          ),
        },
      ]),
    [watched, togglingIds, handleUnwatch]
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
              <DsButton id='refresh-watched-channels-btn' tone='secondary' size='md' onClick={loadChannels} disabled={isLoading}>
                Refresh
              </DsButton>
              {hasWriteAccess() ? (
                <DsButton id='watch-channels-btn' tone='primary' size='md' onClick={openPicker} disabled={isLoading}>
                  Watch channels
                </DsButton>
              ) : undefined}
            </Box>
          }
        >
          <Box display='flex' flexDirection='column'>
            <Typography sx={{ fontFamily: 'var(--ds-font-display)' }} fontSize={ds.text.title} fontWeight={ds.weight.semibold} color={ds.gray[700]}>
              Watched Channels
            </Typography>
            <Typography fontSize={ds.text.caption} color={ds.gray[600]}>
              {watched.length > 0
                ? `${watched.length} ${
                    watched.length === 1 ? 'channel' : 'channels'
                  } watched — Nubi uses them for context when someone @mentions it there.`
                : 'The only places Nubi listens. Everything else is untouched.'}
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
                message='Watching a channel posts a public notice there, and Nubi only ever acts when explicitly @mentioned. Anyone with write access can turn watching off.'
              />
            )}
            <CustomTable
              id={`${provider}-watched-channels-table`}
              headers={headers}
              tableData={tableData}
              loading={isLoading}
              emptyHeading='No channels watched yet'
              emptySubHeading='Use "Watch channels" to pick where Nubi should listen.'
            />
          </Box>
        </ListingLayout.Body>
      </ListingLayout>

      <Modal
        width='sm'
        open={pickerOpen}
        handleClose={() => setPickerOpen(false)}
        title='Watch channels'
        loader={saving}
        actionButtons={
          <Box
            sx={{
              display: 'flex',
              alignItems: 'center',
              justifyContent: 'space-between',
              width: '100%',
              p: 'var(--ds-space-3) var(--ds-space-5)',
            }}
          >
            <Typography fontSize={ds.text.caption} color={ds.gray[600]}>
              {selectedIds.length > 0 ? `${selectedIds.length} selected` : ''}
            </Typography>
            <Box sx={{ display: 'flex', gap: 'var(--ds-space-3)' }}>
              <DsButton id='picker-cancel-btn' tone='secondary' size='md' onClick={() => setPickerOpen(false)} disabled={saving}>
                Cancel
              </DsButton>
              <DsButton
                id='picker-watch-btn'
                tone='primary'
                size='md'
                onClick={handleWatchSelected}
                disabled={selectedIds.length === 0 || saving}
                loading={saving}
              >
                {selectedIds.length === 1 ? 'Watch 1 channel' : selectedIds.length > 1 ? `Watch ${selectedIds.length} channels` : 'Watch channels'}
              </DsButton>
            </Box>
          </Box>
        }
      >
        <Box sx={{ display: 'flex', flexDirection: 'column', gap: 'var(--ds-space-3)' }}>
          <SearchInput
            value={pickerSearch}
            onChange={(next: string) => setPickerSearch(next)}
            onClear={() => setPickerSearch('')}
            label='Search channels'
            minWidth='100%'
          />
          <Box
            sx={{
              border: '1px solid',
              borderColor: ds.gray[300],
              borderRadius: 'var(--ds-radius-lg)',
              maxHeight: 320,
              overflowY: 'auto',
            }}
          >
            {candidates.map((channel, index) => {
              const needsInvite = channel.is_private && !channel.is_member;
              return (
                <Box
                  key={channel.id}
                  sx={{
                    padding: 'var(--ds-space-3) var(--ds-space-4)',
                    borderTop: index === 0 ? 'none' : '1px solid',
                    borderTopColor: ds.gray[200],
                    '&:hover': { backgroundColor: ds.gray[100] },
                  }}
                >
                  <Checkbox
                    checked={selectedIds.includes(channel.id)}
                    onChange={(next: boolean) => toggleSelected(channel.id, next)}
                    disabled={needsInvite || saving}
                    label={`#${channel.name}`}
                    description={needsInvite ? 'Private — invite @Nubi in Slack first' : channel.is_private ? 'Private' : undefined}
                  />
                </Box>
              );
            })}
            {candidates.length === 0 && (
              <Typography sx={{ padding: 'var(--ds-space-4)' }} fontSize={ds.text.caption} color={ds.gray[600]}>
                {pickerSearch ? 'No channels match your search.' : 'Every channel is already watched.'}
              </Typography>
            )}
          </Box>
          <Banner tone='info' surface='section' message='Nubi joins each selected channel and posts a notice there.' />
        </Box>
      </Modal>
    </Box>
  );
};

export default WatchedChannels;
