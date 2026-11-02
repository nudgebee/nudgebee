import apiAccount from '@api1/account';
import { getBrandTitle } from '@hooks/useTenantBranding';
import apiIntegrations from '@api1/integrations';
import Text from '@shared/format/Text';
import ThreeDotsMenu from '@shared/ds/ThreeDotsMenu';
import { ListingLayout } from '@ui/ListingLayout';
import { Button as DsButton } from '@ui/Button';
import CloudProviderIcon from '@shared/icons/CloudIcon';
import { Select } from '@ui/Select';
import { Input } from '@ui/Input';
import { Checkbox } from '@ui/Checkbox';
import Datetime from '@shared/format/Datetime';
import { Modal } from '@ui/Modal';
import { toast as snackbar } from '@ui/Toast';
import CustomTable from '@shared/tables/CustomTable2';
import { action } from 'src/utils/actionStyles';
import { hasWriteAccess, isTenantAdmin, canManage } from '@lib/auth';
import { Typography, Stack, Box } from '@mui/material';
import { useEffect, useRef, useState } from 'react';
import { colors, ds } from 'src/utils/colors';
import { safeJSONParse, toKebabCase } from 'src/utils/common';
import ChannelAccountMapping from '@components/notifications/ChannelAccountMapping';
import WatchedChannels from '@components/notifications/WatchedChannels';
import MsTeamsWebhookTargets from '@components/notifications/MsTeamsWebhookTargets';
import SlackWebhookTargets from '@components/notifications/SlackWebhookTargets';
import Tabs from '@shared/navigation/Tabs';
import { useFeatureAccess } from '@hooks/useFeatureAccess';
import { getIntegrationDescription } from '@lib/integrationDescriptions';
import { EmptyState } from '@ui/EmptyState';
import CheckIcon from '@mui/icons-material/Check';
import LinkOutlinedIcon from '@mui/icons-material/LinkOutlined';
import { splitTeamsValue, teamsChannelOptions, teamsValue } from '@components/notifications/rules/destinations';

// How a Teams team is connected: there is no sign-in, only the bot's link.
const teamsConnectHint = () =>
  `Add the ${getBrandTitle()} app to a team in Microsoft Teams, then open the link it posts (or @mention it) to connect that team.`;

// Which providers get which section under the installation table. Google Chat has
// webhook targets too but renders through its own panel (GoogleChatSpacesPanel), and
// Discord has neither section, so neither appears here.
const MAPPING_PROVIDERS = ['slack', 'ms_teams'];
const WEBHOOK_TARGETS_BY_PROVIDER = { slack: SlackWebhookTargets, ms_teams: MsTeamsWebhookTargets };

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
  const [threadRepeats, setThreadRepeats] = useState(true);
  const [deleteConfig, setDeleteConfig] = useState(false);
  const [mode, setMode] = useState('map'); // map | update
  const [isLoading, setIsLoading] = useState(false);
  const [isLoadingChannels, setIsLoadingChannels] = useState(false);
  const [teamVal, setTeamVal] = useState('');
  const [teamOptions, setTeamOptions] = useState([]);
  const [teamName, setTeamName] = useState('');
  const [isSendingTest, setIsSendingTest] = useState(false);

  const [openInstallModal, setOpenInstallModal] = useState(false);
  const [botToken, setBotToken] = useState('');
  const [isInstalling, setIsInstalling] = useState(false);
  // Discord bot invite guidance: set when the bot is in no server yet (install
  // response or channel-list hint), so the user knows to invite it.
  const [inviteUrl, setInviteUrl] = useState(null);
  const [channelListHint, setChannelListHint] = useState(null);
  // Which provider the rows in state belong to. Tagged rather than a plain "have I
  // loaded" boolean because this component is reused across providers without
  // remounting — the account-form switch returns the same element type — so an
  // untagged flag stays true through the swap.
  const [loadedFor, setLoadedFor] = useState(null);
  const hasLoaded = loadedFor === provider;
  // Which of the sections under the installation table is showing. Component-local
  // (not a URL fragment), like Admin Overview's sub-views.
  const [sectionTab, setSectionTab] = useState('mappings');
  // Same flag WatchedChannels gates itself on; still resolving (null) counts as off.
  const channelAwarenessEnabled = useFeatureAccess('CHANNEL_AWARENESS') === true;
  // Only Slack and Discord thread repeat findings, and only integration installs
  // store the setting.
  const showThreadRepeats = ['slack', 'discord'].includes(provider) && installationData?.[0]?._origin === 'integration';

  // Fetch installations
  const listMessagingPlatform = () => {
    setIsLoading(true);
    apiAccount
      .getMessagingInstallations(provider)
      .then((res) => {
        setIsLoading(false);
        if (res?.data?.length === 1) {
          setInstallationData(res?.data);
        } else {
          setInstallationData([]);
        }
      })
      .catch(() => setIsLoading(false))
      .finally(() => setLoadedFor(provider));
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
          if (provider === 'discord') {
            setChannelListHint(res?.data?.hint ? { hint: res.data.hint, inviteUrl: res.data.invite_url } : null);
          }
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
    if (provider === 'discord') {
      setOpenInstallModal(true);
      return;
    }

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

  const handleDiscordInstall = async () => {
    if (!botToken) return;
    setIsInstalling(true);
    const result = await apiAccount.installDiscord(botToken);
    setIsInstalling(false);
    if (result?.success) {
      snackbar.success('Discord integration installed successfully!');
      setBotToken('');
      listMessagingPlatform();
      fetchChannelList();
      if (result?.guild_count === 0 && result?.invite_url) {
        // Token is valid but the bot isn't in any server — keep the modal open
        // and walk the user through the invite before they hunt for channels.
        setInviteUrl(result.invite_url);
      } else {
        setOpenInstallModal(false);
      }
    } else {
      snackbar.error(result?.error || 'Failed to install Discord integration');
    }
  };

  const closeInstallModal = () => {
    setOpenInstallModal(false);
    setBotToken('');
    setInviteUrl(null);
    listMessagingPlatform();
    fetchChannelList();
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
      setThreadRepeats(installationData?.[0]?.thread_repeats !== false);
    }
    setInstallationId(acc?.id);
    setMode('update');
    setOpenModal(true);
  };

  const getMenuItems = (acc) => {
    if (!hasWriteAccess()) return [];
    const items = [{ label: provider === 'ms_teams' ? 'Disconnect' : 'Delete', id: 'delete' }];
    if (provider === 'discord') {
      items.push({ label: 'Update Token', id: 'update_token' });
    }
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
    } else if (menuItem.id === 'update_token') {
      setOpenInstallModal(true);
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

    const inst = installationData?.[0];
    if (inst?._origin === 'integration') {
      // Integration installs store the default destination as scalar config values
      // (sending uses the IDs; names are kept only for display here).
      const values =
        provider === 'ms_teams'
          ? [
              { name: 'default_team_id', value: teamVal || '' },
              { name: 'default_team_name', value: teamName || '' },
              { name: 'default_channel_id', value: channelsValues?.id || '' },
              { name: 'default_channel_name', value: channelsValues?.name || '' },
            ]
          : [
              { name: 'default_channel_id', value: channelsValues?.id || '' },
              { name: 'default_channel_name', value: channelsValues?.name || '' },
            ];
      // Sent only when changed, so a plain channel edit never writes the setting.
      if (showThreadRepeats && threadRepeats !== (inst.thread_repeats !== false)) {
        values.push({ name: 'thread_repeats', value: String(threadRepeats) });
      }
      apiIntegrations
        .addIntegrations({
          integration_name: provider,
          integration_config_name: inst._integration_name,
          integration_config_values: values,
          account_ids: [],
          source: 'user',
          skip_validation: true,
          // integration_id switches the backend from create (which rejects the
          // existing team/workspace name) to update, so re-saving the default
          // channel for an installed integration persists instead of erroring.
          ...(inst.id ? { integration_id: inst.id } : {}),
        })
        .then((res) => {
          setIsLoading(false);
          if (res?.data?.data?.integrations_create_config?.id) {
            listMessagingPlatform();
            snackbar.success(`${displayName} channel updated successfully`);
            closeModal();
          } else {
            snackbar.error(`Failed to update ${displayName} channel`);
          }
        })
        .catch(() => {
          setIsLoading(false);
          snackbar.error(`Failed to update ${displayName} channel`);
        });
      return;
    }

    apiAccount
      .updateMessagingPlatform(installationId, JSON.stringify(channelsValues))
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

    if (provider === 'ms_teams') {
      // Releases every team bound to the workspace.
      apiAccount
        .deleteMsTeamsConnection()
        .then((res) => {
          if (res?.success) {
            snackbar.success(`${displayName} disconnected`);
            listMessagingPlatform();
            fetchChannelList();
          } else {
            snackbar.error(res?.error || `Failed to disconnect ${displayName}`);
          }
        })
        .catch(() => snackbar.error(`Failed to disconnect ${displayName}`))
        .finally(() => {
          setIsLoading(false);
          setDeleteConfig(false);
        });
      return;
    }

    const inst = installationData?.[0];
    if (inst?._origin === 'integration') {
      apiIntegrations
        .deleteIntegrations({
          integration_name: provider,
          integration_config_name: inst._integration_name,
          source: 'user',
        })
        .then((res) => {
          setIsLoading(false);
          if (res?.data?.data?.integrations_delete_config?.status) {
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
      return;
    }

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

  const isConnected = tableData?.length > 0;

  const showConnectPrompt = hasLoaded && !isLoading && tableData?.length === 0;

  // Sections under the installation table, one tab each. Webhook targets need no
  // app install, so they stay available before Connect; the other two only render
  // once the app is installed (and Watched Channels only behind its feature flag) —
  // a hidden tab, not an empty one. Nothing shows until the installation lookup has
  // landed: before it, Webhook targets is the only visible tab, so it would render
  // (and fetch) first and then be swapped out when the default tab becomes Mappings.
  const hasInstallation = installationData?.length > 0;
  const WebhookTargets = WEBHOOK_TARGETS_BY_PROVIDER[provider];
  const mappingsHidden = !MAPPING_PROVIDERS.includes(provider) || !hasInstallation;
  const watchedHidden = provider !== 'slack' || !hasInstallation || !channelAwarenessEnabled;
  const sectionTabs = [
    { value: 'mappings', text: 'Channel-Account Mappings', hidden: mappingsHidden },
    { value: 'watched', text: 'Watched Channels', hidden: watchedHidden },
    { value: 'webhooks', text: 'Webhook targets', hidden: !WebhookTargets },
  ].map((tab) => ({ ...tab, id: `${toKebabCase(provider)}-${tab.value}-tab` }));
  const visibleSectionTabs = hasLoaded ? sectionTabs.filter((tab) => !tab.hidden) : [];
  // Falls back to the first visible tab when the remembered one is not available
  // (the default, Mappings, is hidden while the workspace is not connected).
  const activeSection = visibleSectionTabs.find((tab) => tab.value === sectionTab)?.value ?? visibleSectionTabs[0]?.value;

  return (
    <>
      <Modal
        width='sm'
        open={openInstallModal}
        handleClose={closeInstallModal}
        title={inviteUrl ? 'Invite the Bot to Your Server' : 'Install Discord Integration'}
        subtitle={getIntegrationDescription(provider)}
        loader={isInstalling}
        actionButtons={
          <Box sx={{ display: 'flex', justifyContent: 'flex-end', gap: ds.space[3], p: '12px 24px' }}>
            {inviteUrl ? (
              <DsButton id='done-install-modal-btn' size='md' tone='primary' onClick={closeInstallModal}>
                Done
              </DsButton>
            ) : (
              <>
                <DsButton id='cancel-install-modal-btn' size='md' tone='secondary' onClick={closeInstallModal} disabled={isInstalling}>
                  Cancel
                </DsButton>
                <DsButton id='save-install-modal-btn' size='md' tone='primary' disabled={isInstalling || !botToken} onClick={handleDiscordInstall}>
                  Install
                </DsButton>
              </>
            )}
          </Box>
        }
      >
        <Box sx={{ display: 'flex', flexDirection: 'column', gap: '20px', padding: '20px 24px' }}>
          {inviteUrl ? (
            <>
              <Typography sx={{ fontSize: ds.text.body, color: ds.brand[500], lineHeight: 1.5 }}>
                The bot token is saved, but the bot is not a member of any Discord server yet — no channels will be available until it is invited.
                Open the invite page, pick your server, and authorize the bot (it only requests View Channels and Send Messages permissions).
              </Typography>
              <DsButton id='open-discord-invite-btn' size='md' tone='primary' onClick={() => window.open(inviteUrl, '_blank', 'noopener')}>
                Open Discord Invite Page
              </DsButton>
            </>
          ) : (
            <Input
              id='bot-token'
              label='Discord Bot Token'
              required
              type='password'
              value={botToken}
              onChange={(val) => setBotToken(val)}
              placeholder='Enter your Discord bot token'
            />
          )}
        </Box>
      </Modal>

      <Modal
        open={deleteConfig}
        handleClose={() => setDeleteConfig(false)}
        title={provider === 'ms_teams' ? `Disconnect ${displayName}?` : `Are you sure you want to delete the configured ${displayName}?`}
        width='sm'
        actionButtons={
          <Box sx={{ display: 'flex', justifyContent: 'flex-end', gap: '12px', p: '12px 24px' }}>
            <DsButton tone='secondary' size='sm' onClick={() => setDeleteConfig(false)} disabled={isLoading}>
              Cancel
            </DsButton>
            <DsButton tone='danger' size='sm' onClick={handleDelete} loading={isLoading}>
              {provider === 'ms_teams' ? 'Disconnect' : 'Delete'}
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
            {provider === 'ms_teams'
              ? `Every team connected to this workspace is disconnected and its channel-account mappings are removed. Notification rules that send to those teams stop delivering. To connect a team again, @mention the ${getBrandTitle()} app in it and open the link it posts.`
              : 'Deleting this installation will remove all channel routings specified in the notification rules.'}
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
            <Select
              id='channel'
              label='Channel'
              required
              value={teamVal && channelsValues?.value ? teamsValue(teamVal, channelsValues.value) : ''}
              grouped
              defaultGroupsOpen
              options={teamsChannelOptions(
                teamOptions.map((t) => ({
                  value: t.value,
                  label: t.label,
                  channels: (t.channels || []).map((c) => ({ value: c.id, label: c.name })),
                }))
              )}
              onChange={(next) => {
                const { teamId, channelId } = splitTeamsValue(next);
                const team = teamOptions.find((t) => t.value === teamId);
                const channel = team?.channels?.find((c) => c.id === channelId);
                setTeamVal(teamId);
                setTeamName(team?.label || '');
                setChannelsValues(channel ? { label: channel.name, value: channel.id, name: channel.name, id: channel.id } : null);
              }}
              disabled={isLoadingChannels}
              placeholder={isLoadingChannels ? 'Loading…' : 'Select channel'}
            />
          ) : (
            <>
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
              {showThreadRepeats && (
                <Checkbox
                  id='thread-repeats-checkbox'
                  checked={threadRepeats}
                  onChange={setThreadRepeats}
                  label='Reply to repeat alerts in the original thread'
                  description='Turn off to post every repeat as a new message.'
                />
              )}
              {provider === 'discord' && !isLoadingChannels && channelOptions.length === 0 && channelListHint && (
                <Box sx={{ display: 'flex', flexDirection: 'column', gap: ds.space[3] }}>
                  <Typography sx={{ fontSize: ds.text.body, color: ds.brand[500], lineHeight: 1.5 }}>
                    {channelListHint.hint === 'bot_not_in_any_server'
                      ? 'The bot is not a member of any Discord server yet. Invite it to a server to see its channels here.'
                      : 'The bot is in a server but cannot see any text channels. Grant it View Channels and Send Messages permissions.'}
                  </Typography>
                  {channelListHint.inviteUrl && (
                    <DsButton
                      id='open-discord-invite-map-btn'
                      size='sm'
                      tone='secondary'
                      onClick={() => window.open(channelListHint.inviteUrl, '_blank', 'noopener')}
                    >
                      Open Discord Invite Page
                    </DsButton>
                  )}
                </Box>
              )}
            </>
          )}
        </Box>
      </Modal>
      <ListingLayout id={`${provider}-integrations`}>
        <ListingLayout.Header
          icon={<CloudProviderIcon cloud_provider={provider.toUpperCase()} width={ds.space[5]} height={ds.space[5]} />}
          title={displayName}
          description={getIntegrationDescription(provider)}
          actions={
            canManage('messagingplatforms', 'Write') ? (
              <Stack direction='row' spacing={1}>
                <DsButton
                  id={`test-${toKebabCase(provider)}-btn`}
                  tone='secondary'
                  size='md'
                  disabled={!(provider === 'ms_teams' ? installationData[0]?.channels?.channels?.[0]?.id : channelsValues?.id) || isSendingTest}
                  onClick={handleSendTest}
                  loading={isSendingTest}
                >
                  Test Notification
                </DsButton>
                {hasLoaded && isConnected && (
                  <DsButton id={`add-to-${toKebabCase(provider)}-btn`} tone='primary' size='md' icon={<CheckIcon sx={{ fontSize: 18 }} />} disabled>
                    Connected
                  </DsButton>
                )}
              </Stack>
            ) : undefined
          }
        />
        {showConnectPrompt ? (
          // This page has no filters, so an empty table can only mean "the app is not
          // installed yet" — the install instruction that used to sit permanently under
          // the title now lives here, where it is the one thing left to do.
          <ListingLayout.Body padding={ds.space[5]}>
            <EmptyState
              id={`${provider}-not-installed`}
              icon={<LinkOutlinedIcon sx={{ fontSize: 20 }} />}
              title={`${displayName} is not connected yet`}
              description={provider === 'ms_teams' ? teamsConnectHint() : `Connect your ${displayName} user to ${getBrandTitle()}.`}
              action={
                canManage('messagingplatforms', 'Write') && provider !== 'ms_teams'
                  ? {
                      id: `add-to-${toKebabCase(provider)}-btn`,
                      label: `Connect ${displayName}`,
                      onClick: handleInstall,
                      icon: <CloudProviderIcon cloud_provider={provider.toUpperCase()} width='16px' height='16px' />,
                    }
                  : undefined
              }
            />
          </ListingLayout.Body>
        ) : (
          <ListingLayout.Body>
            <CustomTable id={provider} headers={headers} tableData={tableData} loading={isLoading} />
            {provider === 'ms_teams' && hasLoaded && isConnected && (
              <Box
                id='ms-teams-connected-teams'
                sx={{ display: 'flex', flexDirection: 'column', gap: ds.space[1], p: `${ds.space[3]} ${ds.space[4]}` }}
              >
                <Typography sx={{ fontSize: ds.text.body, color: ds.gray[700] }}>
                  Connected teams: {teamOptions.map((team) => team.label || team.value).join(', ') || '—'}
                </Typography>
                <Typography sx={{ fontSize: ds.text.small, color: ds.gray[600] }}>{teamsConnectHint()}</Typography>
              </Box>
            )}
          </ListingLayout.Body>
        )}
      </ListingLayout>

      {visibleSectionTabs.length > 1 && (
        <Box sx={{ mb: 'var(--ds-space-3)' }}>
          <Tabs
            value={activeSection}
            onChange={setSectionTab}
            behavior='filter'
            ariaLabel={`${displayName} settings sections`}
            options={{ tabOptions: visibleSectionTabs }}
          />
        </Box>
      )}

      {/* Once visible, every section stays mounted across tab switches (hidden via CSS,
          not unmounted) so its already-fetched data survives switching tabs instead of
          re-fetching on every switch back — Mappings/Watched Channels both paginate the
          same Slack channels_list endpoint and were colliding on Slack's rate limit;
          Webhook targets has no such collision but was re-querying needlessly too. */}
      {!mappingsHidden && (
        <Box sx={{ display: activeSection === 'mappings' ? 'block' : 'none' }}>
          <ChannelAccountMapping provider={provider} displayName={displayName} isConfigured={hasInstallation} />
        </Box>
      )}

      {!watchedHidden && (
        <Box sx={{ display: activeSection === 'watched' ? 'block' : 'none' }}>
          <WatchedChannels provider={provider} isConfigured={hasInstallation} />
        </Box>
      )}

      {WebhookTargets && (
        <Box sx={{ display: activeSection === 'webhooks' ? 'block' : 'none' }}>
          <WebhookTargets />
        </Box>
      )}
    </>
  );
};

export default MessagingIntegrationTile;
