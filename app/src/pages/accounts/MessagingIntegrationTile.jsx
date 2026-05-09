import apiAccount from '@api1/account';
import { Text, ThreeDotsMenu } from '@components1/common';
import BoxLayout2 from '@components1/common/BoxLayout2';
import CloudProviderIcon from '@components1/common/CloudIcon';
import CustomDropdown from '@components1/common/CustomDropdown';
import CustomButton from '@components1/common/NewCustomButton';
import Datetime from '@components1/common/format/Datetime';
import { Modal } from '@components1/common/modal';
import NDialog from '@components1/common/modal/NDialog';
import { snackbar } from '@components1/common/snackbarService';
import CustomTable from '@components1/common/tables/CustomTable2';
import { action } from 'src/utils/actionStyles';
import { hasWriteAccess, isTenantAdmin } from '@lib/auth';
import { Typography, Stack, Grid } from '@mui/material';
import { useEffect, useRef, useState } from 'react';
import { colors } from 'src/utils/colors';
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
  const [_, setDisableSaveButton] = useState(true);
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
                <CustomButton id={`map-channel-${table.length + 1}`} size='Small' text='Map Channel' onClick={() => openUpdateModal(acc)} />
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
              <CustomButton
                id={`map-channel-${table.length + 1}`}
                size='Small'
                text='Map Channel'
                onClick={() => openUpdateModal(acc, installationData[0])}
              />
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
    setDisableSaveButton(true);
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
        if (res?.data?.data?.messaging_platform_delete?.id) {
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
      <NDialog
        buttonText='Confirm'
        open={deleteConfig}
        handleClose={() => setDeleteConfig(false)}
        dialogTitle={`Are you sure you want to delete the configured ${displayName}?`}
        dialogContent={`Deleting this installation will remove all channel routings specified in the notification rules.`}
        handleSubmit={handleDelete}
        loading={isLoading}
      />

      <Modal width='md' open={openModal} handleClose={closeModal} title={mode === 'map' ? 'Map Channel' : 'Update Channel'} loader={isLoading}>
        <Grid container xs={12} gap={3}>
          {provider === 'ms_teams' ? (
            <>
              <CustomDropdown
                label='Team'
                value={teamVal}
                options={teamOptions}
                minWidth='150px'
                isLoading={isLoadingChannels}
                onChange={(e, v) => {
                  setTeamVal(e.target.value);
                  setTeamName(v?.label);
                  setChannelsValues(null);
                  setChannelOptions(
                    v?.channels?.map((item) => ({
                      label: item.name,
                      value: item.id,
                      name: item.name,
                      id: item.id,
                    })) || []
                  );
                }}
              />
              <CustomDropdown
                key={`channels-${teamVal}`}
                label='Channel'
                value={channelsValues?.value || ''}
                options={channelOptions}
                minWidth='250px'
                isLoading={isLoadingChannels}
                isDisabled={!teamVal}
                onChange={(e, v) => {
                  setChannelsValues(v ? { label: v.label, value: v.value, name: v.name, id: v.id } : null);
                }}
              />
            </>
          ) : (
            <CustomDropdown
              label='Channels'
              value={channelVal}
              options={channelOptions}
              minWidth='375px'
              isLoading={isLoadingChannels}
              onChange={(e, v) => {
                setChannelVal(e.target.value);
                setChannelsValues({ name: v?.label, id: v?.value });
                const channels = safeJSONParse(installationData[0].channels);
                const prevChannel = channels?.name || '';
                setDisableSaveButton(prevChannel === v?.label);
              }}
            />
          )}
        </Grid>
        <Grid container justifyContent='end' my={2} gap={1}>
          <CustomButton id='cancel-modal-btn' size='Medium' text='Cancel' variant='secondary' onClick={closeModal} disabled={isLoading} />
          <CustomButton id='save-modal-btn' size='Medium' text='Save' disabled={isLoading || !isTenantAdmin()} onClick={updateChannel} />
        </Grid>
      </Modal>
      <Grid container mt={2}>
        <Grid item xs={12}>
          <Stack direction='row' alignItems='center' justifyContent='space-between'>
            <Stack direction='row' alignItems='center' spacing={1}>
              <Typography fontSize='16px' fontWeight={600} color={colors.text.secondary}>
                {displayName}
              </Typography>
              <CloudProviderIcon cloud_provider={provider.toUpperCase()} />
            </Stack>
            {hasWriteAccess() && (
              <Stack direction='row' spacing={1}>
                <CustomButton
                  id={`test-${toKebabCase(displayName)}-btn`}
                  disabled={!(provider === 'ms_teams' ? installationData[0]?.channels?.channels?.[0]?.id : channelsValues?.id) || isSendingTest}
                  onClick={handleSendTest}
                  text='Test Notification'
                  variant='secondary'
                  loading={isSendingTest}
                />
                <CustomButton
                  id={`add-to-${toKebabCase(displayName)}-btn`}
                  disabled={tableData?.length > 0}
                  onClick={handleInstall}
                  text={`Add to ${displayName}`}
                />
              </Stack>
            )}
          </Stack>
        </Grid>
      </Grid>
      <Stack mt={1}>
        <Typography fontSize='14px' color={colors.text.mid}>
          You can connect your {displayName} user with your Nudgebee account to get functionality directly.
        </Typography>
        <Typography fontSize='14px' color={colors.text.mid}>
          Please use the &quot;Add to {displayName}&quot; button if you need to install the app or contact your administrator.
        </Typography>
      </Stack>

      <BoxLayout2 id={`${provider}-integrations`} sharingOptions={false}>
        <CustomTable id={provider} headers={headers} tableData={tableData} loading={isLoading} />
      </BoxLayout2>

      {(provider === 'slack' || provider === 'ms_teams') && (
        <ChannelAccountMapping provider={provider} displayName={displayName} isConfigured={installationData?.length > 0} />
      )}
    </>
  );
};

export default MessagingIntegrationTile;
