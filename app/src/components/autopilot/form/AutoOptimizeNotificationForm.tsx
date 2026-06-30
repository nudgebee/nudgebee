import { GChatIcon, ouMsTeams as MsTeamsIcon, slackIcon as SlackIcon } from '@assets';
import { useEffect, useState } from 'react';
import { Box, Typography } from '@mui/material';
import { Button as DsButton } from '@ui/Button';
import { Select } from '@ui/Select';
import SafeIcon from '@shared/icons/SafeIcon';
import apiAccount from '@api1/account';
import { ds } from '@utils/colors';

interface NotificationFormProps {
  notificationData: any;
  handleSlackButtonClick: () => void;
  setSlackChannelName: (value: string) => void;
  setNotificationData: (data: any) => void;
  slackChannelName: string;
  slackChannelList: any[];
  isLoadingSlackChannels: boolean;
  displayErrorsDesc: any;
  handleTeamsButtonClick: () => void;
  setMsTeamName: (value: string) => void;
  msTeamName: string;
  msChannelListOption: any[];
  msTeamsData: any[];
  setMSChannelName: (value: string) => void;
  msChannelName: string;
  isMsTeamsLoading: boolean;
  handleGoogleChatButtonClick: () => void;
  setGoogleChatChannelName: (value: string) => void;
  googleChatChannelName: string;
  googleChannelList: any[];
  isGoogleChannelsLoading: boolean;
  reviewAutoOptimize?: boolean;
}

const CHANNEL_FIELD_WIDTH = 240;
const MESSAGING_BUTTON_WIDTH = 140;

const NotificationForm = ({
  notificationData,
  handleSlackButtonClick,
  setSlackChannelName,
  setNotificationData,
  slackChannelName,
  slackChannelList,
  isLoadingSlackChannels,
  displayErrorsDesc,
  handleTeamsButtonClick,
  setMsTeamName,
  msTeamName,
  msChannelListOption,
  msTeamsData,
  setMSChannelName,
  msChannelName,
  isMsTeamsLoading,
  handleGoogleChatButtonClick,
  setGoogleChatChannelName,
  googleChatChannelName,
  googleChannelList,
  isGoogleChannelsLoading,
  reviewAutoOptimize = false,
}: NotificationFormProps) => {
  const [connectedPlatforms, setConnectedPlatforms] = useState<string[] | null>(null);

  useEffect(() => {
    apiAccount
      .listConnectedMessagingPlatforms()
      .then((res) => setConnectedPlatforms(res?.data ?? []))
      .catch(() => setConnectedPlatforms([]));
  }, []);

  // A platform is selectable when it's installed — via the legacy messaging_platforms
  // table or the new integrations storage, the same source the Integrations page reads.
  // Until that resolves (null) we treat platforms as available to avoid a disabled flash.
  const slackConnected = connectedPlatforms === null || connectedPlatforms.includes('slack');
  const teamsConnected = connectedPlatforms === null || connectedPlatforms.includes('ms_teams');
  const googleChatConnected = connectedPlatforms === null || connectedPlatforms.includes('google_chat');

  // Keep a button enabled while its platform is selected so the user can always toggle
  // it back off, even if the integration was disconnected after it was configured.
  const slackDisabled = reviewAutoOptimize || (!slackConnected && !notificationData?.slack);
  const teamsDisabled = reviewAutoOptimize || (!teamsConnected && !notificationData?.teams);
  const googleChatDisabled = reviewAutoOptimize || (!googleChatConnected && !notificationData?.google_chat);

  return (
    <Box sx={{ display: 'flex', flexDirection: 'column', gap: 0, mt: ds.space[4] }}>
      <Box
        sx={{
          borderRadius: `${ds.radius.sm} ${ds.radius.sm} 0 0`,
          background: ds.blue[100],
          padding: `${ds.space[2]} ${ds.space[4]}`,
        }}
      >
        <Typography sx={{ color: ds.gray[700], fontSize: ds.text.title, fontWeight: ds.weight.semibold }}>Notify me on</Typography>
      </Box>

      <Typography sx={{ color: ds.gray[500], fontSize: ds.text.caption, padding: `${ds.space[2]} ${ds.space[4]} 0` }}>
        You're notified only when this optimization makes a change — an in-place resize applied, a PR raised, or a ticket created (and if a change
        fails). Scheduled runs that make no changes stay silent. For PRs, the link arrives in a short follow-up message once the PR is created.
      </Typography>

      <Box sx={{ display: 'flex', flexDirection: 'column', gap: ds.space[4], padding: `${ds.space[4]} ${ds.space[3]}` }}>
        {/* Slack */}
        <Box sx={{ display: 'flex', flexDirection: 'column', gap: ds.space[1] }}>
          <Box sx={{ display: 'flex', alignItems: 'flex-end', gap: ds.space[5] }}>
            <Box sx={{ width: MESSAGING_BUTTON_WIDTH }}>
              <DsButton
                fullWidth
                tone={notificationData?.slack ? 'primary' : 'secondary'}
                size='md'
                icon={<SafeIcon src={SlackIcon} width={18} height={18} />}
                onClick={handleSlackButtonClick}
                disabled={slackDisabled}
                tooltip={slackDisabled && !reviewAutoOptimize ? 'Connect Slack in Integrations to enable notifications' : undefined}
              >
                Slack
              </DsButton>
            </Box>
            <Box sx={{ width: CHANNEL_FIELD_WIDTH }}>
              <Select
                id='select-slack-channel'
                label='Select Channel'
                value={slackChannelName || ''}
                options={slackChannelList}
                onChange={(next) => {
                  setSlackChannelName(next || '');
                  setNotificationData({ ...notificationData, channelId: next || '' });
                }}
                disabled={!notificationData?.slack || reviewAutoOptimize || isLoadingSlackChannels}
                placeholder={isLoadingSlackChannels ? 'Loading…' : 'Select channel'}
              />
            </Box>
          </Box>
          {displayErrorsDesc.notification.slack.length > 0 ? (
            <Typography sx={{ color: ds.red[500], fontSize: ds.text.body, marginTop: ds.space[1] }}>
              {displayErrorsDesc.notification.slack}
            </Typography>
          ) : null}
        </Box>

        {/* MS Teams */}
        <Box sx={{ display: 'flex', flexDirection: 'column', gap: ds.space[1] }}>
          <Box sx={{ display: 'flex', alignItems: 'flex-end', gap: ds.space[5] }}>
            <Box sx={{ width: MESSAGING_BUTTON_WIDTH }}>
              <DsButton
                fullWidth
                tone={notificationData?.teams ? 'primary' : 'secondary'}
                size='md'
                icon={<SafeIcon src={MsTeamsIcon} width={18} height={18} />}
                onClick={handleTeamsButtonClick}
                disabled={teamsDisabled}
                tooltip={teamsDisabled && !reviewAutoOptimize ? 'Connect MS Teams in Integrations to enable notifications' : undefined}
              >
                MS Teams
              </DsButton>
            </Box>
            <Box sx={{ display: 'flex', alignItems: 'flex-end', gap: ds.space[3] }}>
              <Box sx={{ width: CHANNEL_FIELD_WIDTH }}>
                <Select
                  id='select-ms-team'
                  label='Teams'
                  value={msTeamName || ''}
                  options={msTeamsData || []}
                  onChange={(next) => {
                    setMsTeamName(next || '');
                    setNotificationData({ ...notificationData, teamsId: next || '' });
                  }}
                  disabled={!notificationData?.teams || reviewAutoOptimize || isMsTeamsLoading}
                  placeholder={isMsTeamsLoading ? 'Loading…' : 'Select team'}
                />
              </Box>
              <Box sx={{ width: CHANNEL_FIELD_WIDTH }}>
                <Select
                  id='select-ms-channel'
                  label='Channels'
                  value={msChannelName || ''}
                  options={msChannelListOption}
                  onChange={(next) => {
                    setMSChannelName(next || '');
                    setNotificationData({ ...notificationData, msChannelId: next || '' });
                  }}
                  disabled={!notificationData?.teams || reviewAutoOptimize || !msTeamName}
                  placeholder={!msTeamName ? 'Select a team first' : 'Select channel'}
                />
              </Box>
            </Box>
          </Box>
          {displayErrorsDesc.notification.teams.length > 0 ? (
            <Typography sx={{ color: ds.red[500], fontSize: ds.text.body, marginTop: ds.space[1] }}>
              {displayErrorsDesc.notification.teams}
            </Typography>
          ) : null}
        </Box>

        {/* Google Chat */}
        <Box sx={{ display: 'flex', flexDirection: 'column', gap: ds.space[1] }}>
          <Box sx={{ display: 'flex', alignItems: 'flex-end', gap: ds.space[5] }}>
            <Box sx={{ width: MESSAGING_BUTTON_WIDTH }}>
              <DsButton
                fullWidth
                tone={notificationData?.google_chat ? 'primary' : 'secondary'}
                size='md'
                icon={<SafeIcon src={GChatIcon} width={18} height={18} />}
                onClick={handleGoogleChatButtonClick}
                disabled={googleChatDisabled}
                tooltip={googleChatDisabled && !reviewAutoOptimize ? 'Connect Google Chat in Integrations to enable notifications' : undefined}
              >
                Google Chat
              </DsButton>
            </Box>
            <Box sx={{ width: CHANNEL_FIELD_WIDTH }}>
              <Select
                id='select-gchat-channel'
                label='Channels'
                value={googleChatChannelName || ''}
                options={googleChannelList}
                onChange={(next) => {
                  setGoogleChatChannelName(next || '');
                  setNotificationData({ ...notificationData, gChatChannelId: next || '' });
                }}
                disabled={!notificationData?.google_chat || reviewAutoOptimize || isGoogleChannelsLoading}
                placeholder={isGoogleChannelsLoading ? 'Loading…' : 'Select channel'}
              />
            </Box>
          </Box>
          {displayErrorsDesc.notification.google_chat.length > 0 ? (
            <Typography sx={{ color: ds.red[500], fontSize: ds.text.body, marginTop: ds.space[1] }}>
              {displayErrorsDesc.notification.google_chat}
            </Typography>
          ) : null}
        </Box>
      </Box>
    </Box>
  );
};

export default NotificationForm;
