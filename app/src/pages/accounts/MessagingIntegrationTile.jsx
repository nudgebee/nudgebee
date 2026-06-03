import apiAccount from '@api1/account';
import Text from '@common-new/format/Text';
import ThreeDotsMenu from '@common-new/ThreeDotsMenu';
import { ListingLayout } from '@components1/ds/ListingLayout';
import { Button as DsButton } from '@components1/ds/Button';
import CloudProviderIcon from '@components1/common/CloudIcon';
import { Select } from '@components1/ds/Select';
import Datetime from '@common-new/format/Datetime';
import { Modal } from '@components1/ds/Modal';
import { toast as snackbar } from '@components1/ds/Toast';
import CustomTable from '@common-new/tables/CustomTable2';
import { action } from 'src/utils/actionStyles';
import { hasWriteAccess, isTenantAdmin } from '@lib/auth';
import { Typography, Stack, Box } from '@mui/material';
import { useEffect, useRef, useState } from 'react';
import { colors, ds } from 'src/utils/colors';
import { safeJSONParse, toKebabCase } from 'src/utils/common';
import ChannelAccountMapping from '@components1/notifications/ChannelAccountMapping';

const MessagingIntegrationTile = ({
  provider, // "slack" | "google_chat"
  displayName, // "Slack" | "Google Chat"
  installUrl, // API install URL
  headers, // table headers
  hasTeamName = false, // Slack has team_name, GChat doesn’t
}) => {
  const intervalIdRef = useRef(null);
  const [installationData, setInstallationData] = useState([]);
  const [tableData, setTableData] = useState([]);
  const [installationId, setInstallationId] = useState('');
  const [openModal, setOpenModal] = useState(false);
  const [channelVal, setChannelVal] = useState('');
  const [channelOptions, setChannelOptions] = useState([]);
  const [channelsValues, setChannelsValues] = useState({});
  const [deleteConfig, setDeleteConfig] = useState(false);
  const [mode, setMode] = useState('map'); // map | update
  const [isLoading, setIsLoading] = useState(false);
  const [isLoadingChannels, setIsLoadingChannels] = useState(false);
  const [teamVal, setTeamVal] = useState('');
  const [teamOptions, setTeamOptions] = useState([]);
  const [teamName, setTeamName] = useState('');
  const [isSendingTest, setIsSendingTest] = useState(false);

  // Fetch installations
  const listMessagingPlatform = () => {
    setIsLoading(true);
    apiAccount
      .getMessagingPlatform(provider)
      .then((res) => {
        setIsLoading(false);
        if (res?.data?.length === 1) {
          setInstallationData(res?.data);
        } else {
          setInstallationData([]);
        }
      })
      .catch(() => setIsLoading(false));
  };

  // Fetch available channels
  const fetchChannelList = () => {
    setIsLoadingChannels(true);
    if (provider === 'ms_teams') {
      apiAccount
        .getNotificationChannelList('ms_teams')
        .then((res) => {
          let opts =
            res?.data?.data?.map((item) => ({
              label: item.name,
              value: item.id,
              channels: item.channels,
            })) || [];
          setTeamOptions(opts);
        })
        .finally(() => setIsLoadingChannels(false));
    } else {
      apiAccount
        .getNotificationChannelList(provider)
        .then((res) => {
          let opts = res?.data?.data?.map((item) => ({ label: item.name, value: item.id })) || [];
          setChannelOptions(opts);
        })
        .finally(() => setIsLoadingChannels(false));
    }
  };

  useEffect(() => {
    listMessagingPlatform();
    fetchChannelList();

    return () => {
      if (intervalIdRef.current) {
        clearInterval(intervalIdRef.current);
      }
    };
  }, []);

  const handleInstall = () => {
    // Open as popup instead of new tab
    const width = 600;
    const height = 700;
    const left = window.screenX + (window.outerWidth - width) / 2;
    const top = window.screenY + (window.outerHeight - height) / 2;
    const win = window.open(installUrl, `${provider}_install`, `popup,width=${width},height=${height},left=${left},top=${top}`);

    // Poll for popup close and refresh for all providers
    if (intervalIdRef.current) {
      clearInterval(intervalIdRef.current);
    }
    intervalIdRef.current = setInterval(() => {
      if (win?.closed) {
        clearInterval(intervalIdRef.current);
        intervalIdRef.current = null;
        listMessagingPlatform();
        fetchChannelList();
      }
    }, 500);
  };

  const handleSendTest = async () => {
    setIsSendingTest(true);
    try {
      let channelId, teamId;
      if (provider === 'ms_teams') {
        channelId = installationData[0]?.channels?.channels?.[0]?.id;
        teamId = installationData[0]?.channels?.team_id;
      } else {
        channelId = channelsValues?.id;
      }
      if (!channelId) {
        snackbar.error('No channel mapped. Please map a channel first.');
        setIsSendingTest(false);
        return;
      }
      const result = await apiAccount.sendTestNotification(provider, channelId, teamId);
      if (result?.success) {
        snackbar.success(`Test notification sent to ${displayName} successfully!`);
      } else {
        snackbar.error(result?.error || `Failed to send test notification to ${displayName}`);
      }
    } catch {
      snackbar.error(`Failed to send test notification to ${displayName}`);
    } finally {
      setIsSendingTest(false);
    }
  };

  const openUpdateModal = (acc, channel = {}) => {
    if (provider === 'ms_teams') {
      const channelData = acc?.channels?.channels?.[0] || null;
      const teamName = acc?.channels?.team_name || '';
      const teamId = acc?.channels?.team_id || '';
      setChannelsValues(
        channelData ? { label: channelData.label || channelData.name, value: channelData.id, name: channelData.name, id: channelData.id } : null
      );
      setTeamVal(teamId);
      setTeamName(teamName);
    } else {
      const parsed = safeJSONParse(channel?.channels);
      setChannelVal(parsed?.name || '');
      setChannelsValues(parsed || {});
    }
    setInstallationId(acc?.id);
    setMode('update');
    setOpenModal(true);
  };

  const getMenuItems = (acc) => {
    if (!hasWriteAccess()) return [];
    const items = [{ label: 'Delete', id: 'delete' }];
    if (provider === 'ms_teams') {
      const channelsArray = Array.isArray(acc?.channels?.channels) ? acc.channels.channels : [];
      const channelList = channelsArray.map((c) => c.label || c.name) || [];
      if (channelList.length > 0) {
        items.push({ label: 'Edit', id: 'edit' });
      }
    } else {
      const channels = safeJSONParse(installationData[0]?.channels);
      if (channels?.name?.length > 0) {
        items.push({ label: 'Edit', id: 'edit' });
      }
    }
    return items;
  };

  const onMenuClick = (menuItem, acc) => {
    if (menuItem.id === 'delete') {
      setDeleteConfig(true);
    } else if (menuItem.id === 'edit') {
      if (provider === 'ms_teams') {
        openUpdateModal(acc);
      } else {
        openUpdateModal(acc, installationData[0]);
      }
    }
  };

  useEffect(() => {
    let table = [];

    if (provider === 'ms_teams') {
      for (let acc of installationData || []) {
        const channelsArray = Array.isArray(acc?.channels?.channels) ? acc.channels.channels : [];
        const channelList = channelsArray.map((c) => c.label || c.name) || [];
        const teamName = acc?.channels?.team_name || '';

        table.push([
          { component: <Text value={acc.username} /> },
          { component: <Datetime value={acc.created_at} /> },
          { component: teamName ? <Text value={teamName} /> : <></> },
          {
            component:
              channelList.length === 0 && isTenantAdmin() ? (
                <DsButton id={`map-channel-${table.length + 1}`} size='sm' onClick={() => openUpdateModal(acc)}>
                  Map Channel
                </DsButton>
              ) : (
                <Text value={channelList.join(', ')} />
              ),
          },
          {
            component: <ThreeDotsMenu sx={{ ...action.primary }} menuItems={getMenuItems(acc)} data={acc} onMenuClick={onMenuClick} />,
          },
        ]);
      }
    } else {
      // Slack / Google Chat logic
      let channelName = '';
      if (installationData?.length === 1) {
        const channels = safeJSONParse(installationData[0].channels);
        if (channels) {
          channelName = channels?.name || '';
          setChannelsValues(channels);
        }
      }

      for (let acc of installationData || []) {
        let row = [];
        if (hasTeamName) {
          row.push({ component: <Text value={acc.team_name} /> });
        }

        row.push({ component: <Datetime value={acc.created_at} /> });

        row.push({
          component:
            channelName?.length === 0 && isTenantAdmin() ? (
              <DsButton id={`map-channel-${table.length + 1}`} size='sm' onClick={() => openUpdateModal(acc, installationData[0])}>
                Map Channel
              </DsButton>
            ) : (
              <Text value={channelName} />
            ),
        });

        row.push({
          component: <ThreeDotsMenu sx={{ ...action.primary }} menuItems={getMenuItems(acc)} data={acc} onMenuClick={onMenuClick} />,
        });

        table.push(row);
      }
    }

    setTableData(table);
  }, [installationData]);

  const closeModal = () => {
    setOpenModal(false);
  };

  const updateChannel = () => {
    setIsLoading(true);
    let payload;

    if (provider === 'ms_teams') {
      payload = {
        channels: channelsValues ? [channelsValues] : [],
        team_name: teamName,
        team_id: teamVal,
      };
    } else {
      payload = JSON.stringify(channelsValues);
    }
    apiAccount
      .updateMessagingPlatform(installationId, payload)
      .then((res) => {
        setIsLoading(false);
        if (res.data.affected_rows === 1) {
          listMessagingPlatform();
          snackbar.success(`${displayName} channel updated successfully`);
          closeModal();
        }
      })
      .catch(() => {
        setIsLoading(false);
        snackbar.error(`Failed to update ${displayName} channel`);
      });
  };

  const handleDelete = () => {
    setIsLoading(true);
    apiAccount
      .deleteMessagingPlatform(installationData?.[0]?.id)
      .then((res) => {
        setIsLoading(false);
        if (res?.data?.data?.messagingplatforms_delete?.id) {
          snackbar.success(`${displayName} configuration deleted successfully`);
          listMessagingPlatform();
        } else {
          snackbar.error(`Failed to delete ${displayName} configuration`);
        }
        setDeleteConfig(false);
      })
      .catch(() => {
        setIsLoading(false);
        setDeleteConfig(false);
        snackbar.error(`Failed to delete ${displayName} configuration`);
      });
  };

  return (
    <>
      <Modal
        open={deleteConfig}
        handleClose={() => setDeleteConfig(false)}
        title={`Are you sure you want to delete the configured ${displayName}?`}
        width='sm'
        actionButtons={
          <Box sx={{ display: 'flex', justifyContent: 'flex-end', gap: '12px', p: '12px 24px' }}>
            <DsButton tone='secondary' size='sm' onClick={() => setDeleteConfig(false)} disabled={isLoading}>
              Cancel
            </DsButton>
            <DsButton tone='danger' size='sm' onClick={handleDelete} loading={isLoading}>
              Delete
            </DsButton>
          </Box>
        }
      >
        <Box sx={{ padding: '24px' }}>
          <Typography
            sx={{
              fontFamily: 'var(--ds-font-display)',
              fontSize: 'var(--ds-text-body)',
              fontWeight: 'var(--ds-font-weight-regular)',
              color: colors.text.secondary,
              lineHeight: 1.5,
            }}
          >
            Deleting this installation will remove all channel routings specified in the notification rules.
          </Typography>
        </Box>
      </Modal>

      <Modal
        width='md'
        open={openModal}
        handleClose={closeModal}
        title={mode === 'map' ? 'Map Channel' : 'Update Channel'}
        loader={isLoading}
        actionButtons={
          <Box sx={{ display: 'flex', justifyContent: 'flex-end', gap: '12px', p: '12px 24px' }}>
            <DsButton id='cancel-modal-btn' size='md' tone='secondary' onClick={closeModal} disabled={isLoading}>
              Cancel
            </DsButton>
            <DsButton
              id='save-modal-btn'
              size='md'
              tone='primary'
              disabled={isLoading || !isTenantAdmin() || (provider === 'ms_teams' ? !teamVal || !channelsValues?.value : !channelVal)}
              onClick={updateChannel}
            >
              Save
            </DsButton>
          </Box>
        }
      >
        <Box sx={{ display: 'flex', flexDirection: 'column', gap: '20px', padding: '20px 24px' }}>
          {provider === 'ms_teams' ? (
            <>
              <Select
                id='team'
                label='Team'
                required
                value={teamVal}
                options={teamOptions}
                onChange={(next) => {
                  setTeamVal(next);
                  const team = teamOptions.find((t) => t.value === next);
                  setTeamName(team?.label);
                  setChannelsValues(null);
                  setChannelOptions(
                    team?.channels?.map((item) => ({
                      label: item.name,
                      value: item.id,
                      name: item.name,
                      id: item.id,
                    })) || []
                  );
                }}
                disabled={isLoadingChannels}
                placeholder={isLoadingChannels ? 'Loading…' : 'Select team'}
              />
              <Select
                id='channel'
                key={`channels-${teamVal}`}
                label='Channel'
                required
                value={channelsValues?.value || ''}
                options={channelOptions}
                onChange={(next) => {
                  const opt = channelOptions.find((o) => o.value === next);
                  setChannelsValues(opt ? { label: opt.label, value: opt.value, name: opt.name, id: opt.id } : null);
                }}
                disabled={!teamVal || isLoadingChannels}
                placeholder={!teamVal ? 'Select a team first' : isLoadingChannels ? 'Loading…' : 'Select channel'}
              />
            </>
          ) : (
            <Select
              id='channels'
              label='Channels'
              required
              value={channelVal}
              options={channelOptions}
              onChange={(next) => {
                setChannelVal(next);
                const opt = channelOptions.find((o) => o.value === next);
                setChannelsValues({ name: opt?.label, id: opt?.value });
              }}
              disabled={isLoadingChannels}
              placeholder={isLoadingChannels ? 'Loading…' : 'Select channel'}
            />
          )}
        </Box>
      </Modal>
      <ListingLayout id={`${provider}-integrations`}>
        <ListingLayout.Toolbar
          actions={
            hasWriteAccess() ? (
              <Stack direction='row' spacing={1}>
                <DsButton
                  id={`test-${toKebabCase(displayName)}-btn`}
                  tone='secondary'
                  size='md'
                  disabled={!(provider === 'ms_teams' ? installationData[0]?.channels?.channels?.[0]?.id : channelsValues?.id) || isSendingTest}
                  onClick={handleSendTest}
                  loading={isSendingTest}
                >
                  Test Notification
                </DsButton>
                <DsButton
                  id={`add-to-${toKebabCase(displayName)}-btn`}
                  tone='primary'
                  size='md'
                  disabled={tableData?.length > 0}
                  onClick={handleInstall}
                >
                  {`Add to ${displayName}`}
                </DsButton>
              </Stack>
            ) : undefined
          }
        >
          <Stack direction='row' alignItems='center' spacing={ds.space[2]}>
            <CloudProviderIcon cloud_provider={provider.toUpperCase()} />
            <Box display='flex' flexDirection='column'>
              <Typography sx={{ fontFamily: 'var(--ds-font-display)' }} fontSize={ds.text.title} fontWeight={ds.weight.semibold} color={ds.gray[700]}>
                {displayName}
              </Typography>
              <Typography fontSize={ds.text.caption} color={ds.gray[600]}>
                Connect your {displayName} user to Nudgebee. Use the &quot;Add to {displayName}&quot; button to install the app.
              </Typography>
            </Box>
          </Stack>
        </ListingLayout.Toolbar>
        <ListingLayout.Body>
          <CustomTable id={provider} headers={headers} tableData={tableData} loading={isLoading} />
        </ListingLayout.Body>
      </ListingLayout>

      {(provider === 'slack' || provider === 'ms_teams') && (
        <ChannelAccountMapping provider={provider} displayName={displayName} isConfigured={installationData?.length > 0} />
      )}
    </>
  );
};

export default MessagingIntegrationTile;
