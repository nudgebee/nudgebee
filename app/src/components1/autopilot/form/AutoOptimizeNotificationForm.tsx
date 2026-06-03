import { GChatIcon, ouMsTeams as MsTeamsIcon, slackIcon as SlackIcon } from '@assets';
import React, { useEffect, useState } from 'react';
import { Box, Typography } from '@mui/material';
import { Button as DsButton } from '@components1/ds/Button';
import { Select } from '@components1/ds/Select';
import SafeIcon from '@components1/common/SafeIcon';
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
  const [messagingPlatforms, setMessagingPlatforms] = useState<string[]>([]);

  useEffect(() => {
    apiAccount
      .listMessagingPlatform()
      .then((res: any) => {
        if (res?.data && res?.data.length > 0) {
          setMessagingPlatforms(res?.data?.map((m: { platform: string }) => m.platform));
        }
      })
      .catch((error) => {
        console.error(error);
      });
  }, []);

  const slackDisabled = (messagingPlatforms && !messagingPlatforms.includes('slack')) || reviewAutoOptimize;
  const teamsDisabled = (messagingPlatforms && !messagingPlatforms.includes('ms_teams')) || reviewAutoOptimize;
  const googleChatDisabled = (messagingPlatforms && !messagingPlatforms.includes('google_chat')) || reviewAutoOptimize;

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
