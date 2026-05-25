import React, { useEffect, useState } from 'react';
import AutoPilotHeaderCard from '@components1/autopilot/card/AutoOptimizeHeaderCard';
import { Box, Grid, InputAdornment, TextField, Typography } from '@mui/material';
import apiAutoPlaybook from '@api1/autoPlaybook';
import apiAccount from '@api1/account';
import k8sApi from '@api1/kubernetes';
import PropTypes from 'prop-types';
import { useData } from '@context/DataContext';
import ActionButtons from './AutoOptimizeActionButtons';
import NotificationForm from './AutoOptimizeNotificationForm';
import { inputSx } from '@data/themes/inputField';
import { Textarea } from '@components1/k8s/common/TextArea';
import apiAutoPilot from '@api1/autoPilot';
import { snackbar } from '@components1/common/snackbarService';

const PVAutoOptimizeSingleConfiguration = ({
  autoOptimizeData,
  closeAutoPilotSingleConfigModal,
  msTeamsData,
  isMsTeamsLoading,
  googleChannelList,
  isGoogleChannelsLoading,
  listAutoPilot,
  _isLoading,
  setIsLoading,
  reviewAutoOptimize = false,
  approvalData = {},
}) => {
  const { selectedCluster } = useData();

  const [activeButton, setActiveButton] = useState('');
  const [notificationData, setNotificationData] = useState({
    email: autoOptimizeData?.notification?.email?.enabled || false,
    slack: autoOptimizeData?.notification?.slack?.enabled || false,
    teams: autoOptimizeData?.notification?.ms_teams?.enabled || false,
    google_chat: autoOptimizeData?.notification?.google_chat?.enabled || false,
    channelId: autoOptimizeData?.notification?.slack?.channel_id || '',
    teamsId: autoOptimizeData?.notification?.ms_teams?.team_id || '',
    msChannelId: autoOptimizeData?.notification?.ms_teams?.channel_id || '',
    gChatChannelId: autoOptimizeData?.notification?.google_chat?.channel_id || '',
    gChatChannelName: autoOptimizeData?.notification?.google_chat?.channel_name || '',
  });
  const [slackChannelList, setSlackChannelList] = useState([]);
  const [slackChannelName, setSlackChannelName] = useState(autoOptimizeData?.notification?.slack?.channel_name || '');
  const [isLoadingSlackChannels, setIsLoadingSlackChannels] = useState(false);
  const [msTeamName, setMsTeamName] = useState(autoOptimizeData?.notification?.ms_teams?.team_id || '');
  const [msChannelListOption, setMsChannelListOption] = useState([]);
  const [msChannelName, setMSChannelName] = useState(autoOptimizeData?.notification?.ms_teams?.channel_id || '');
  const [googleChatChannelName, setGoogleChatChannelName] = useState(autoOptimizeData?.notification?.google_chat?.channel_id || '');
  const [displayErrorsDesc, setDisplayErrorsDesc] = useState({
    notification: {
      teams: '',
      slack: '',
      google_chat: '',
    },
    gitops: {
      message: '',
    },
  });
  const [resourceFilter, setResourceFilter] = useState(
    autoOptimizeData?.auto_optimize_resource_maps?.map((m) => m.resource_identifier) || autoOptimizeData?.resource_filter || []
  );
  const [thresholdPct, setThresholdPct] = useState(autoOptimizeData?.rule?.rightsize_threshold_pct ?? 80);
  const [increasePct, setIncreasePct] = useState(autoOptimizeData?.rule?.increase_by_pct ?? 10);
  const [reviewComment, setReviewComment] = useState('');

  useEffect(() => {
    if (msTeamName) {
      filterChannelsName(msTeamName);
    }
  }, [msTeamName, msChannelName, msTeamsData]);

  const filterChannelsName = (value) => {
    const channelValue = value;
    const selectedMsTeamsData = msTeamsData?.find((item) => item?.value === channelValue);
    if (selectedMsTeamsData) {
      setMsChannelListOption(selectedMsTeamsData?.channels?.map((channel) => ({ label: channel?.name, value: channel?.id })));
    } else {
      setMsChannelListOption([]);
    }
  };

  useEffect(() => {
    if (approvalData?.reviewer_comments) {
      setReviewComment(approvalData.reviewer_comments);
    }

    const fetchSlackChannels = async () => {
      setIsLoadingSlackChannels(true);
      try {
        const platforms = 'slack';
        const res = await apiAccount.getNotificationChannelList(platforms);
        const channelOptions = res?.data?.data?.map((item) => ({ label: item.name, value: item.id })) || [];
        setSlackChannelList(channelOptions);
      } finally {
        setIsLoadingSlackChannels(false);
      }
    };

    fetchSlackChannels();
  }, []);

  const handleCancel = () => {
    closeAutoPilotSingleConfigModal(false);
    setNotificationData({
      ...notificationData,
      slack: false,
      teams: false,
      google_chat: false,
    });
  };

  const validateAutoPilotRequest = () => {
    let valid = true;
    const validate = {
      notification: {
        teams: '',
        slack: '',
        google_chat: '',
      },
      gitops: {
        message: '',
      },
    };

    if (notificationData?.slack && !slackChannelName) {
      validate.notification.slack = 'Select Slack Channel';
    }

    if (notificationData?.teams) {
      if (!msTeamName && !msChannelName) {
        validate.notification.teams = 'Select Team and Channel';
        valid = false;
      }
      if (!msTeamName) {
        validate.notification.teams = 'Select atleast one team';
        valid = false;
      }
      if (msTeamName && !msChannelName) {
        validate.notification.teams = 'Select one Channel';
        valid = false;
      }
    }

    if (notificationData?.google_chat && !googleChatChannelName) {
      validate.notification.google_chat = 'Select Google Chat Channel';
      valid = false;
    }

    setDisplayErrorsDesc(validate);
    return valid;
  };

  const handleCreateAutoPilotRule = () => {
    if (!validateAutoPilotRequest()) {
      return;
    }

    if (resourceFilter?.length == 0) {
      snackbar.error('Please select Namespace & PVC');
      return;
    }

    if (setIsLoading) {
      setIsLoading(true);
    }

    const data = {
      account_id: autoOptimizeData?.accountId ?? autoOptimizeData?.account_id ?? selectedCluster?.value,
      category: 'pvc_rightsize',
      resource_filter: resourceFilter,
      auto_optimize_config: {
        rightsize_threshold_pct: thresholdPct ? parseInt(thresholdPct) : 10,
        increase_by_pct: increasePct ? parseInt(increasePct) : 10,
      },
      schedule: {
        frequency: '0 * * * *',
        start_date: new Date().toISOString(),
      },
      notification: {
        slack: notificationData?.slack
          ? {
              enabled: notificationData?.slack,
              channel_id: notificationData?.channelId,
            }
          : {
              enabled: notificationData?.slack,
            },
        ms_teams: notificationData?.teams
          ? { enabled: notificationData?.teams, team_id: msTeamName, channel_id: msChannelName }
          : {
              enabled: notificationData?.teams,
            },
        email: {
          enabled: notificationData?.email,
        },
        google_chat: notificationData?.google_chat
          ? {
              enabled: notificationData?.google_chat,
              channel_id: notificationData?.gChatChannelId,
            }
          : {
              enabled: notificationData?.google_chat,
            },
      },
      ticket_config: {},
      dryrun: false,
    };
    if (!autoOptimizeData.id) {
      k8sApi
        .singleConfigAuotPilot(data)
        .then((res) => {
          setIsLoading(false);
          if (res?.errors) {
            snackbar.error('Error - ' + res?.errors[0]?.message);
          } else {
            closeAutoPilotSingleConfigModal(true);
            setNotificationData({
              ...notificationData,
              slack: false,
              teams: false,
            });
            setDisplayErrorsDesc({
              notification: {
                teams: '',
                slack: '',
                google_chat: '',
              },
              gitops: {
                message: '',
              },
            });
            setMSChannelName('');
            setMsTeamName('');
            setSlackChannelName('');
            snackbar.success('Auto Optimize Rule created successfully');
            handleCancel();
          }
        })
        .finally(() => {
          setIsLoading(false);
        });
    } else {
      data.id = autoOptimizeData?.id;
      apiAutoPlaybook
        .singleConfigUpdateAuotPilot(data)
        .then((res) => {
          setIsLoading(false);
          if (res?.errors) {
            snackbar.error('Error - ' + res?.errors[0]?.message);
          } else {
            closeAutoPilotSingleConfigModal(true);
            if (listAutoPilot) {
              listAutoPilot();
            }
          }
        })
        .finally(() => {
          setIsLoading(false);
        });
    }
  };

  const handleReviewAction = (status) => {
    if (status == 'REJECTED') {
      if (!reviewComment) {
        setDisplayErrorsDesc({ ...displayErrorsDesc, reviewComment: 'Please add a review comment if you wish to reject the runbook' });
        return;
      }
      apiAutoPilot
        .updateAutoPilotApprovalStatus(
          approvalData?.id,
          autoOptimizeData?.accountId ?? autoOptimizeData?.account_id ?? selectedCluster?.value,
          reviewComment,
          'REJECTED'
        )
        .then((res) => {
          if (res?.data?.update_status_auto_pilot_approval?.id) {
            closeAutoPilotSingleConfigModal(true, 'REJECTED');
            snackbar.success('Auto Optimize Rule Rejected Successfully');
          } else {
            snackbar.error(`Failed to approve Auto Optimize !`);
          }
        });
    } else if (status == 'APPROVED') {
      apiAutoPilot
        .updateAutoPilotApprovalStatus(
          approvalData?.id,
          autoOptimizeData?.accountId ?? autoOptimizeData?.account_id ?? selectedCluster?.value,
          reviewComment,
          'APPROVED'
        )
        .then((res) => {
          if (res?.data?.update_status_auto_pilot_approval?.id) {
            closeAutoPilotSingleConfigModal(true, 'APPROVED');
            snackbar.success('Auto Optimize Rule Approved Successfully');
          } else {
            snackbar.error(`Failed to approve Auto Optimize !`);
          }
        });
    }
  };

  const autoPilotSingleConfigButton = [
    {
      label: 'Cancel',
      backgroundColor: '#3B82F6',
      onClick: handleCancel,
    },
  ];

  if (reviewAutoOptimize) {
    autoPilotSingleConfigButton.push({
      isDisabled: !Object.keys(approvalData).length || approvalData.status == 'REJECTED',
      label: 'Reject',
      backgroundColor: '#3B82F6',
      onClick: () => handleReviewAction('REJECTED'),
    });
    autoPilotSingleConfigButton.push({
      isDisabled: !Object.keys(approvalData).length || approvalData.status == 'APPROVED',
      label: 'Approve',
      backgroundColor: '#3B82F6',
      onClick: () => handleReviewAction('APPROVED'),
    });
  } else {
    autoPilotSingleConfigButton.push({
      label: !autoOptimizeData.id ? 'Create Auto Optimize Rule' : 'Update Auto Optimize Rule',
      backgroundColor: '#3B82F6',
      onClick: handleCreateAutoPilotRule,
    });
  }

  const handleSlackButtonClick = () => {
    const slackButtonState = !notificationData.slack;
    setNotificationData({
      ...notificationData,
      slack: slackButtonState,
    });
    if (!slackButtonState) {
      setSlackChannelName('');
    }
  };

  const handleTeamsButtonClick = () => {
    const teamsButtonState = !notificationData.teams;
    setNotificationData({
      ...notificationData,
      teams: teamsButtonState,
    });
    if (!teamsButtonState) {
      setMsTeamName('');
      setMSChannelName('');
    }
  };

  const handleGoogleChatButtonClick = () => {
    const googleChatButtonState = !notificationData.google_chat;
    setNotificationData({
      ...notificationData,
      google_chat: googleChatButtonState,
    });
    if (!googleChatButtonState) {
      setGoogleChatChannelName('');
    }
  };

  return (
    <Box>
      <Box sx={{ marginTop: '24px' }}>
        <AutoPilotHeaderCard
          header='Optimization Config'
          data={autoOptimizeData}
          setResourceFilter={setResourceFilter}
          isMultiSelect={false}
          reviewAutoOptimize={reviewAutoOptimize}
          type='pvc'
        />
      </Box>
      <Box sx={{ display: 'flex', gap: '16px', marginTop: '16px' }}>
        <Box sx={{ display: 'flex', flexDirection: 'column', gap: '24px' }}>
          <Box sx={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center' }}>
            <Grid item xs={12}>
              <Typography sx={{ color: '#737373', fontSize: '12px', fontWeight: 400 }}>{'Increase Storage Size By'}</Typography>
              <TextField
                InputProps={{
                  endAdornment: (
                    <InputAdornment position='end' sx={{ '& p': { color: '#B9B9B9', fontSize: '12px', fontWeight: 400 } }}>
                      %
                    </InputAdornment>
                  ),
                }}
                sx={{
                  ...inputSx,
                  '& .MuiOutlinedInput-root': {
                    height: '34px',
                  },
                }}
                size='small'
                value={increasePct || ''}
                fullWidth
                name={'increaseSizeBy'}
                type='number'
                onChange={(e) => {
                  setIncreasePct(e.target.value);
                }}
                disabled={reviewAutoOptimize}
              />
            </Grid>
          </Box>
          <Box
            sx={{
              borderRadius: '4px',
              borderTop: '1px solid #DBEAFE)',
              background: '#EFF6FF',
              padding: '8px 16px',
              marginTop: '20px',
            }}
          >
            <Box>
              <Typography sx={{ color: '#374151', fontSize: '16px', fontWeight: 600 }}>{'Trigger Thresholds'}</Typography>
              <Typography sx={{ color: '#9F9F9F', fontSize: '12px', fontWeight: 400 }}>
                <b>Trigger Threshold - </b>Do changes when Storage is greater than the configured value
                <br />
              </Typography>
            </Box>
          </Box>
          <Box sx={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center' }}>
            <Grid item xs={12}>
              <TextField
                InputProps={{
                  endAdornment: (
                    <InputAdornment position='end' sx={{ '& p': { color: '#B9B9B9', fontSize: '12px', fontWeight: 400 } }}>
                      %
                    </InputAdornment>
                  ),
                }}
                sx={{
                  ...inputSx,
                  '& .MuiOutlinedInput-root': {
                    height: '34px',
                  },
                }}
                size='small'
                value={thresholdPct || ''}
                name={'thresholdPct'}
                type='number'
                onChange={(e) => {
                  setThresholdPct(e?.target?.value);
                }}
                disabled={reviewAutoOptimize}
              />
            </Grid>
          </Box>
          <NotificationForm
            msTeamsData={msTeamsData}
            msChannelListOption={msChannelListOption}
            slackChannelList={slackChannelList}
            notificationData={notificationData}
            slackChannelName={slackChannelName}
            setSlackChannelName={setSlackChannelName}
            displayErrorsDesc={displayErrorsDesc}
            googleChannelList={googleChannelList}
            googleChatChannelName={googleChatChannelName}
            handleGoogleChatButtonClick={handleGoogleChatButtonClick}
            handleSlackButtonClick={handleSlackButtonClick}
            handleTeamsButtonClick={handleTeamsButtonClick}
            msChannelName={msChannelName}
            msTeamName={msTeamName}
            setMSChannelName={setMSChannelName}
            setMsTeamName={setMsTeamName}
            setNotificationData={setNotificationData}
            setGoogleChatChannelName={setGoogleChatChannelName}
            reviewAutoOptimize={reviewAutoOptimize}
            isLoadingSlackChannels={isLoadingSlackChannels}
            isMsTeamsLoading={isMsTeamsLoading}
            isGoogleChannelsLoading={isGoogleChannelsLoading}
          />
        </Box>
      </Box>
      {reviewAutoOptimize && (
        <Box sx={{ display: 'flex', flexDirection: 'column', marginTop: '16px' }}>
          <Textarea
            value={reviewComment}
            placeholder='Please comment if you wish to reject this PR'
            onChange={(e) => setReviewComment(e.target.value)}
            minRows={2}
            maxRows={4}
            maxLength={250}
          />
          <Box>
            {displayErrorsDesc.reviewComment ? (
              <Typography sx={{ color: 'red', fontSize: '14px', marginTop: '4px' }}>{displayErrorsDesc.reviewComment}</Typography>
            ) : null}
          </Box>
        </Box>
      )}
      <ActionButtons buttons={autoPilotSingleConfigButton} activeButton={activeButton} setActiveButton={setActiveButton} />
    </Box>
  );
};

PVAutoOptimizeSingleConfiguration.propTypes = {
  autoOptimizeData: PropTypes.object.isRequired,
  closeAutoPilotSingleConfigModal: PropTypes.func.isRequired,
  msTeamsData: PropTypes.array.isRequired,
  googleChannelList: PropTypes.array.isRequired,
  listAutoPilot: PropTypes.func,
  isLoading: PropTypes.bool,
  setIsLoading: PropTypes.func,
  isMsTeamsLoading: PropTypes.bool,
  isGoogleChannelsLoading: PropTypes.bool,
};

export default PVAutoOptimizeSingleConfiguration;
