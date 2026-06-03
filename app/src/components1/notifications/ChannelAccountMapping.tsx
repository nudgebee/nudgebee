import React, { useEffect, useState, useCallback, useMemo } from 'react';
import { Box, Typography } from '@mui/material';
import { Modal } from '@components1/ds/Modal';
import { Select } from '@components1/ds/Select';
import { Button as DsButton } from '@components1/ds/Button';
import Text from '@common-new/format/Text';
import ThreeDotsMenu from '@common-new/ThreeDotsMenu';
import Datetime from '@common-new/format/Datetime';
import CustomTable from '@common-new/tables/CustomTable2';
import { ListingLayout } from '@components1/ds/ListingLayout';
import { action } from 'src/utils/actionStyles';
import { toast as snackbar } from '@components1/ds/Toast';
import apiNotifications from '@api1/notification';
import apiDashboard from '@api1/home';
import apiAccount from '@api1/account';
import { hasWriteAccess } from '@lib/auth';
import { ds } from 'src/utils/colors';

interface ChannelAccountMappingProps {
  provider: string;
  displayName: string;
  isConfigured: boolean;
}

const ChannelAccountMapping: React.FC<ChannelAccountMappingProps> = ({ provider, isConfigured }) => {
  const [mappings, setMappings] = useState<any[]>([]);
  const [openModal, setOpenModal] = useState(false);
  const [deleteModalOpen, setDeleteModalOpen] = useState(false);
  const [isLoading, setIsLoading] = useState(false);
  const [isLoadingChannels, setIsLoadingChannels] = useState(false);
  const [isLoadingAccounts, setIsLoadingAccounts] = useState(false);

  const [cloudAccounts, setCloudAccounts] = useState<any[]>([]);
  const [channels, setChannels] = useState<any[]>([]);
  const [teams, setTeams] = useState<any[]>([]);
  const [messagingPlatform, setMessagingPlatform] = useState<any>(null);
  const [channelNameMap, setChannelNameMap] = useState<Record<string, string>>({});
  const [teamNameMap, setTeamNameMap] = useState<Record<string, string>>({});

  const [selectedAccount, setSelectedAccount] = useState('');
  const [selectedChannel, setSelectedChannel] = useState('');
  const [selectedTeam, setSelectedTeam] = useState('');
  const [selectedMappingId, setSelectedMappingId] = useState('');

  const headers = useMemo(
    () =>
      provider === 'ms_teams'
        ? ['Cloud Account', 'Team', 'Channel', 'Created At', 'Created By', '']
        : ['Cloud Account', 'Channel', 'Created At', 'Created By', ''],
    [provider]
  );

  const loadCloudAccounts = useCallback(async () => {
    setIsLoadingAccounts(true);
    try {
      const response = await apiDashboard.getCloudAccounts();
      if (Array.isArray(response)) {
        setCloudAccounts(response.map((item: any) => ({ label: item.account_name, value: item.id, id: item.id, provider: item.cloud_provider })));
      }
    } catch (error) {
      console.error('Error loading cloud accounts:', error);
    } finally {
      setIsLoadingAccounts(false);
    }
  }, []);

  useEffect(() => {
    loadCloudAccounts();
  }, [loadCloudAccounts]);

  const loadChannels = useCallback(async () => {
    setIsLoadingChannels(true);
    try {
      const res: any = await apiAccount.getNotificationChannelList(provider);
      const data = res?.data?.data ?? [];

      // Build channel name map for display
      const nameMap: Record<string, string> = {};
      const teamMap: Record<string, string> = {};

      if (provider === 'ms_teams') {
        setTeams(data.map((item: any) => ({ label: item.name, value: item.id, channels: item.channels, teamId: item.id })));
        // For MS Teams, build map from all team channels and teams
        data.forEach((team: any) => {
          // Build team name map
          if (team.id && team.name) {
            teamMap[team.id] = team.name;
          }
          // Build channel name map
          if (team.channels && Array.isArray(team.channels)) {
            team.channels.forEach((channel: any) => {
              nameMap[channel.id] = channel.name;
            });
          }
        });
        setTeamNameMap(teamMap);
      } else {
        const channelOpts = data.map((item: any) => ({ label: item.name, value: item.id }));
        setChannels(channelOpts);
        // For Slack/Google Chat, build map from channels
        data.forEach((channel: any) => {
          nameMap[channel.id] = channel.name;
        });
      }

      setChannelNameMap(nameMap);
    } catch (error) {
      console.error('Error loading channels:', error);
    } finally {
      setIsLoadingChannels(false);
    }
  }, [provider]);

  useEffect(() => {
    if (isConfigured) {
      loadChannels();
    }
  }, [isConfigured, loadChannels]);

  const loadMessagingPlatform = useCallback(async () => {
    try {
      const response: any = await apiAccount.getMessagingPlatform(provider);
      if (response?.data && response.data.length > 0) {
        setMessagingPlatform(response.data[0]);
      }
    } catch (error) {
      console.error('Error loading messaging platform:', error);
    }
  }, [provider]);

  useEffect(() => {
    if (isConfigured) {
      loadMessagingPlatform();
    }
  }, [isConfigured, loadMessagingPlatform]);

  const loadMappings = useCallback(async () => {
    setIsLoading(true);
    try {
      const response = await apiNotifications.listChannelAccountMappings(provider);
      setMappings(response?.data || []);
    } catch (error) {
      snackbar.error('Failed to load mappings');
      console.error(error);
    } finally {
      setIsLoading(false);
    }
  }, [provider]);

  useEffect(() => {
    if (isConfigured) {
      loadMappings();
    }
  }, [isConfigured, loadMappings]);

  const getMenuItems = () => {
    if (!hasWriteAccess()) return [];
    return [
      { label: 'Delete', id: 'delete' },
      { label: 'Edit', id: 'edit' },
    ];
  };

  const onMenuClick = (menuItem: any, mapping: any) => {
    if (menuItem.id === 'delete') {
      handleDeleteClick(mapping.id);
    } else if (menuItem.id === 'edit') {
      handleEdit(mapping);
    }
  };

  const tableData = useMemo(
    () =>
      mappings.map((mapping) => {
        const accountName = mapping.cloud_account?.account_name || 'Unknown';
        const teamName = teamNameMap[mapping.team_id] || mapping.team_id || '-';
        const channelName = channelNameMap[mapping.channel_id] || mapping.channel_id || '-';
        const createdBy = mapping.user_created_by?.display_name || '-';

        const row = [{ component: <Text value={accountName} /> }];

        // Add Team column for MS Teams
        if (provider === 'ms_teams') {
          row.push({ component: <Text value={teamName} /> });
        }

        row.push(
          { component: <Text value={channelName} /> },
          { component: <Datetime value={mapping.created_at} /> },
          { component: <Text value={createdBy} /> },
          {
            component: <ThreeDotsMenu sx={{ ...action.primary }} menuItems={getMenuItems()} data={mapping} onMenuClick={onMenuClick} />,
          }
        );

        return row;
      }),
    [mappings, channelNameMap, teamNameMap, provider]
  );

  const handleEdit = (mapping: any) => {
    setSelectedMappingId(mapping.id);
    setSelectedAccount(mapping.account_id);
    setSelectedChannel(mapping.channel_id);
    setSelectedTeam(mapping.team_id || '');
    setOpenModal(true);
  };

  const handleDeleteClick = (id: string) => {
    setSelectedMappingId(id);
    setDeleteModalOpen(true);
  };

  const handleDelete = async () => {
    setIsLoading(true);
    try {
      await apiNotifications.deleteChannelAccountMapping(selectedMappingId);
      snackbar.success('Mapping deleted');
      await loadMappings();
    } catch {
      snackbar.error('Failed to delete');
    } finally {
      setDeleteModalOpen(false);
      setSelectedMappingId('');
      setIsLoading(false);
    }
  };

  const resetForm = () => {
    setSelectedMappingId('');
    setSelectedAccount('');
    setSelectedChannel('');
    setSelectedTeam('');
  };

  const handleSave = async () => {
    if (!selectedAccount || !selectedChannel) {
      return snackbar.error('Select account and channel');
    }

    if (!messagingPlatform?.id) {
      snackbar.error('Messaging platform not loaded');
      return;
    }

    // For MS Teams, require team selection
    if (provider === 'ms_teams' && !selectedTeam) {
      snackbar.error('Please select a team');
      return;
    }

    setIsLoading(true);
    try {
      // For MS Teams: use selectedTeam, for Slack/Google Chat: use workspace id (team_id from messaging_platform)
      const teamId = provider === 'ms_teams' ? selectedTeam : messagingPlatform.team_id;

      if (selectedMappingId) {
        const updatePayload = {
          id: selectedMappingId,
          account_id: selectedAccount,
          team_id: teamId,
          channel_id: selectedChannel,
        };
        await apiNotifications.updateChannelAccountMapping(updatePayload);
        snackbar.success('Mapping updated');
      } else {
        const payload = {
          ac_id: selectedAccount,
          platform: provider,
          team_id: teamId,
          channel_id: selectedChannel,
        };
        await apiNotifications.insertChannelAccountMapping(payload);
        snackbar.success('Mapping saved');
      }

      await loadMappings();
      setOpenModal(false);
      resetForm();
    } catch {
      snackbar.error('Failed to save');
    } finally {
      setIsLoading(false);
    }
  };

  const handleTeamChange = (next: string) => {
    setSelectedTeam(next);
    setSelectedChannel('');
    const team = teams.find((t: any) => t.value === next);
    setChannels(team?.channels?.map((item: any) => ({ label: item.name, value: item.id })) || []);
  };

  if (!isConfigured) {
    return null;
  }

  return (
    <>
      <Modal
        open={deleteModalOpen}
        handleClose={() => setDeleteModalOpen(false)}
        title='Delete Mapping'
        width='sm'
        actionButtons={
          <Box sx={{ display: 'flex', justifyContent: 'flex-end', gap: 'var(--ds-space-3)', p: 'var(--ds-space-3) var(--ds-space-5)' }}>
            <DsButton tone='secondary' size='sm' onClick={() => setDeleteModalOpen(false)} disabled={isLoading}>
              Cancel
            </DsButton>
            <DsButton tone='danger' size='sm' onClick={handleDelete} loading={isLoading}>
              Delete
            </DsButton>
          </Box>
        }
      >
        <Box sx={{ padding: 'var(--ds-space-5)' }}>
          <Typography
            sx={{
              fontFamily: 'var(--ds-font-display)',
              fontSize: 'var(--ds-text-body)',
              fontWeight: 'var(--ds-font-weight-regular)',
              color: ds.gray[700],
              lineHeight: 1.5,
            }}
          >
            Are you sure you want to delete this mapping?
          </Typography>
        </Box>
      </Modal>

      <Modal
        width='md'
        open={openModal}
        handleClose={() => {
          setOpenModal(false);
          resetForm();
        }}
        title={selectedMappingId ? 'Edit Mapping' : 'Add Mapping'}
        loader={isLoading}
        actionButtons={
          <Box sx={{ display: 'flex', justifyContent: 'flex-end', gap: 'var(--ds-space-3)', p: 'var(--ds-space-3) var(--ds-space-5)' }}>
            <DsButton
              id='cancel-btn'
              tone='secondary'
              size='md'
              onClick={() => {
                setOpenModal(false);
                resetForm();
              }}
              disabled={isLoading}
            >
              Cancel
            </DsButton>
            <DsButton id='save-btn' tone='primary' size='md' onClick={handleSave} disabled={!selectedAccount || !selectedChannel || isLoading}>
              Save
            </DsButton>
          </Box>
        }
      >
        <Box sx={{ display: 'flex', flexDirection: 'column', gap: 'var(--ds-space-4)', padding: 'var(--ds-space-4) var(--ds-space-5)' }}>
          <Select
            id='cloud-account'
            label='Cloud Account'
            required
            value={selectedAccount}
            options={cloudAccounts}
            onChange={(next) => setSelectedAccount(next)}
            disabled={isLoadingAccounts}
            placeholder={isLoadingAccounts ? 'Loading…' : 'Select cloud account'}
          />

          {provider === 'ms_teams' ? (
            <>
              <Select
                id='team'
                label='Team'
                required
                value={selectedTeam}
                options={teams}
                onChange={handleTeamChange}
                disabled={isLoadingChannels}
                placeholder={isLoadingChannels ? 'Loading…' : 'Select team'}
              />
              <Select
                id='channel'
                key={`channel-${selectedTeam}`}
                label='Channel'
                required
                value={selectedChannel}
                options={channels}
                onChange={(next) => setSelectedChannel(next)}
                disabled={!selectedTeam || isLoadingChannels}
                placeholder={!selectedTeam ? 'Select a team first' : isLoadingChannels ? 'Loading…' : 'Select channel'}
              />
            </>
          ) : (
            <Select
              id='channel'
              label='Channel'
              required
              value={selectedChannel}
              options={channels}
              onChange={(next) => setSelectedChannel(next)}
              disabled={isLoadingChannels}
              placeholder={isLoadingChannels ? 'Loading…' : 'Select channel'}
            />
          )}
        </Box>
      </Modal>

      <Box mt={ds.space[6]}>
        <ListingLayout id={`${provider}-channel-account-mappings`}>
          <ListingLayout.Toolbar
            actions={
              hasWriteAccess() ? (
                <DsButton id='add-mapping-btn' tone='primary' size='md' onClick={() => setOpenModal(true)}>
                  Add Mapping
                </DsButton>
              ) : undefined
            }
          >
            <Box display='flex' flexDirection='column'>
              <Typography sx={{ fontFamily: 'var(--ds-font-display)' }} fontSize={ds.text.title} fontWeight={ds.weight.semibold} color={ds.gray[700]}>
                Channel-Account Mappings
              </Typography>
              <Typography fontSize={ds.text.caption} color={ds.gray[600]}>
                Map channels to cloud accounts, this will be used to associate account(s) when conversations are started from mapped channel.
              </Typography>
            </Box>
          </ListingLayout.Toolbar>
          <ListingLayout.Body>
            <CustomTable id={`${provider}-mappings`} headers={headers} tableData={tableData} loading={isLoading} />
          </ListingLayout.Body>
        </ListingLayout>
      </Box>
    </>
  );
};

export default ChannelAccountMapping;
