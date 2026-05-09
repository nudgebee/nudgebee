import { GChatIcon, ouMsTeams as MsTeamsIcon, slackIcon as SlackIcon } from '@assets';
import React, { useEffect, useState } from 'react';
import KeyboardArrowDownIcon from '@mui/icons-material/KeyboardArrowDown';
import { Autocomplete, Box, Button, Typography, TextField, CircularProgress } from '@mui/material';

import apiAccount from '@api1/account';

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

import { colors } from 'src/utils/colors';
import SafeIcon from '@components1/common/SafeIcon';

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

  return (
    <Box sx={{ display: 'flex', gap: '16px', marginTop: '16px' }}>
      <Box sx={{ display: 'flex', flexDirection: 'column', gap: '24px' }}>
        <Box
          sx={{
            borderRadius: '4px 4px 0px 0px',
            borderTop: `1px solid ${colors.border.primaryLight})`,
            background: colors.background.primaryLightest,
            padding: '8px 16px',
          }}
        >
          <Typography sx={{ color: colors.text.secondary, fontSize: '16px', fontWeight: 600 }}>{'Notify me on'}</Typography>
        </Box>
        <Box
          sx={{
            display: 'flex',
            padding: '10px 12px',
            alignItems: 'end',
            gap: '67px',
          }}
        >
          <Box sx={{ display: 'flex', flexDirection: 'column', gap: '18px' }}>
            <Box sx={{ display: 'flex', flexDirection: 'column', gap: '2px' }}>
              <Box sx={{ display: 'flex', alignItems: 'flex-start', gap: '24px' }}>
                <Button
                  variant={notificationData?.slack ? 'contained' : 'outlined'}
                  startIcon={<SafeIcon src={SlackIcon} width={18} height={18} />}
                  size='small'
                  sx={{
                    textTransform: 'none',
                    borderRadius: '6px',
                    color: notificationData?.slack ? 'antiquewhite' : colors.text.tertiary,
                    border: `0.5px solid ${colors.border.secondary}`,
                    padding: '8px',
                    minWidth: '140px',
                  }}
                  onClick={handleSlackButtonClick}
                  disabled={(messagingPlatforms && !messagingPlatforms.includes('slack')) || reviewAutoOptimize}
                >
                  Slack
                </Button>
                <Autocomplete
                  size='medium'
                  sx={{
                    maxWidth: 240,
                    minWidth: 240,
                    '& .MuiOutlinedInput-root': {
                      padding: '2px 14px !important',
                      backgroundColor: !notificationData?.slack ? colors.background.input : colors.background.white,
                    },
                    '& .MuiAutocomplete-input': {
                      padding: '7.5px 45px 7.5px 5px !important',
                    },
                    '& .MuiInputLabel-root': {
                      lineHeight: '0.7em !important',
                      overflow: 'visible !important',
                    },
                    height: '35px',
                  }}
                  key={'select-channel-slack'}
                  options={slackChannelList}
                  popupIcon={<KeyboardArrowDownIcon sx={{ height: '1.1em', width: '1em', color: colors.text.tertiary }} />}
                  getOptionLabel={(option) => option.label || option}
                  loading={isLoadingSlackChannels}
                  value={slackChannelList.find((option) => option.value === slackChannelName) || slackChannelName}
                  onChange={(event, newValue) => {
                    setSlackChannelName(newValue ? newValue.value : '');
                    setNotificationData({
                      ...notificationData,
                      channelId: newValue ? newValue.value : '',
                    });
                  }}
                  disabled={!notificationData?.slack || reviewAutoOptimize}
                  renderInput={(params) => (
                    <TextField
                      {...params}
                      label='Select Channel'
                      InputProps={{
                        ...params.InputProps,
                        endAdornment: (
                          <>
                            {isLoadingSlackChannels ? <CircularProgress size={20} /> : null}
                            {params.InputProps.endAdornment}
                          </>
                        ),
                      }}
                    />
                  )}
                />
              </Box>
              {displayErrorsDesc.notification.slack.length > 0 ? (
                <Typography sx={{ color: 'red', fontSize: '14px', marginTop: '6px' }}>{displayErrorsDesc.notification.slack}</Typography>
              ) : null}
            </Box>
            <Box sx={{ display: 'flex', flexDirection: 'column', gap: '10px' }}>
              <Box sx={{ display: 'flex', alignItems: 'flex-start', gap: '24px' }}>
                <Button
                  variant={notificationData?.teams ? 'contained' : 'outlined'}
                  startIcon={<SafeIcon src={MsTeamsIcon} width={18} height={18} />}
                  size='small'
                  sx={{
                    textTransform: 'none',
                    borderRadius: '6px',
                    color: notificationData?.teams ? 'antiquewhite' : colors.text.tertiary,
                    border: `0.5px solid ${colors.border.secondary}`,
                    padding: '8px',
                    minWidth: '140px',
                  }}
                  onClick={handleTeamsButtonClick}
                  disabled={(messagingPlatforms && !messagingPlatforms.includes('ms_teams')) || reviewAutoOptimize}
                >
                  MS Teams
                </Button>
                <Box sx={{ display: 'flex', alignItems: 'center', gap: '12px' }}>
                  <Autocomplete
                    size='medium'
                    sx={{
                      maxWidth: 240,
                      minWidth: 240,
                      '& .MuiOutlinedInput-root': {
                        padding: '2px 14px !important',
                        backgroundColor: !notificationData?.teams ? colors.background.input : colors.background.white,
                      },
                      '& .MuiAutocomplete-input': {
                        padding: '7.5px 45px 7.5px 5px !important',
                      },
                      '& .MuiInputLabel-root': {
                        lineHeight: '0.7em !important',
                        overflow: 'visible !important',
                      },
                      height: '35px',
                    }}
                    key={'select-ms-team'}
                    options={msTeamsData || []}
                    popupIcon={<KeyboardArrowDownIcon sx={{ height: '1.1em', width: '1em', color: colors.text.tertiary }} />}
                    getOptionLabel={(option) => option.label || option}
                    loading={isMsTeamsLoading}
                    value={msTeamsData?.find((option) => option.value === msTeamName) || null}
                    onChange={(event, newValue) => {
                      setMsTeamName(newValue ? newValue.value : '');
                      setNotificationData({
                        ...notificationData,
                        teamsId: newValue ? newValue.value : '',
                      });
                    }}
                    disabled={!notificationData?.teams || reviewAutoOptimize}
                    renderInput={(params) => (
                      <TextField
                        {...params}
                        label='Teams'
                        InputProps={{
                          ...params.InputProps,
                          endAdornment: (
                            <>
                              {isMsTeamsLoading ? <CircularProgress size={20} /> : null}
                              {params.InputProps.endAdornment}
                            </>
                          ),
                        }}
                      />
                    )}
                  />
                  <Autocomplete
                    size='medium'
                    sx={{
                      maxWidth: 240,
                      minWidth: 240,
                      '& .MuiOutlinedInput-root': {
                        padding: '2px 14px !important',
                        backgroundColor: !notificationData?.teams ? colors.background.input : colors.background.white,
                      },
                      '& .MuiAutocomplete-input': {
                        padding: '7.5px 45px 7.5px 5px !important',
                      },
                      '& .MuiInputLabel-root': {
                        lineHeight: '0.7em !important',
                        overflow: 'visible !important',
                      },
                      height: '35px',
                    }}
                    key={'select-ms-channel'}
                    options={msChannelListOption}
                    getOptionLabel={(option) => option.label || option}
                    popupIcon={<KeyboardArrowDownIcon sx={{ height: '1.1em', width: '1em', color: colors.text.tertiary }} />}
                    loading={false}
                    value={msChannelListOption.find((option) => option.value === msChannelName) || null}
                    onChange={(_event, newValue) => {
                      setMSChannelName(newValue ? newValue.value : '');
                      setNotificationData({
                        ...notificationData,
                        msChannelId: newValue ? newValue.value : '',
                      });
                    }}
                    disabled={!notificationData?.teams || reviewAutoOptimize}
                    renderInput={(params) => <TextField {...params} label='Channels' />}
                  />
                </Box>
              </Box>
              {displayErrorsDesc.notification.teams.length > 0 ? (
                <Typography sx={{ color: 'red', fontSize: '14px', marginTop: '-6px' }}>{displayErrorsDesc.notification.teams}</Typography>
              ) : null}
            </Box>
            <Box sx={{ display: 'flex', flexDirection: 'column', gap: '10px' }}>
              <Box sx={{ display: 'flex', alignItems: 'center', gap: '24px' }}>
                <Button
                  variant={notificationData?.google_chat ? 'contained' : 'outlined'}
                  startIcon={<SafeIcon src={GChatIcon} width={18} height={18} />}
                  size='small'
                  sx={{
                    textTransform: 'none',
                    borderRadius: '6px',
                    color: notificationData?.google_chat ? 'antiquewhite' : colors.text.tertiary,
                    border: `0.5px solid ${colors.border.secondary}`,
                    padding: '8px',
                    minWidth: '140px',
                    mt: '6px',
                  }}
                  onClick={handleGoogleChatButtonClick}
                  disabled={(messagingPlatforms && !messagingPlatforms.includes('google_chat')) || reviewAutoOptimize}
                >
                  Google Chat
                </Button>
                <Autocomplete
                  size='medium'
                  sx={{
                    maxWidth: 240,
                    minWidth: 240,
                    '& .MuiOutlinedInput-root': {
                      padding: '2px 14px !important',
                      backgroundColor: !notificationData?.google_chat ? colors.background.input : colors.background.white,
                    },
                    '& .MuiAutocomplete-input': {
                      padding: '7.5px 45px 7.5px 5px !important',
                      mt: '-2px',
                    },
                    '& .MuiInputLabel-root': {
                      lineHeight: '0.7em !important',
                      overflow: 'visible !important',
                    },
                    height: '35px',
                  }}
                  key={'select-gchat-channel'}
                  options={googleChannelList}
                  popupIcon={<KeyboardArrowDownIcon sx={{ height: '1.1em', width: '1em', color: colors.text.tertiary }} />}
                  getOptionLabel={(option) => option.label || option}
                  loading={isGoogleChannelsLoading}
                  value={googleChannelList.find((option) => option.value === googleChatChannelName) || null}
                  onChange={(event, newValue) => {
                    setGoogleChatChannelName(newValue ? newValue.value : '');
                    setNotificationData({
                      ...notificationData,
                      gChatChannelId: newValue ? newValue.value : '',
                    });
                  }}
                  disabled={!notificationData?.google_chat || reviewAutoOptimize}
                  renderInput={(params) => (
                    <TextField
                      {...params}
                      label='Channels'
                      InputProps={{
                        ...params.InputProps,
                        endAdornment: (
                          <>
                            {isGoogleChannelsLoading ? <CircularProgress size={20} /> : null}
                            {params.InputProps.endAdornment}
                          </>
                        ),
                      }}
                    />
                  )}
                />
              </Box>
              {displayErrorsDesc.notification.google_chat.length > 0 ? (
                <Typography sx={{ color: 'red', fontSize: '14px', marginTop: '-6px' }}>{displayErrorsDesc.notification.google_chat}</Typography>
              ) : null}{' '}
            </Box>
          </Box>
        </Box>
      </Box>
    </Box>
  );
};

export default NotificationForm;
