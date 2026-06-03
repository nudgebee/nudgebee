import React, { useEffect, useState } from 'react';
import { Box, Typography } from '@mui/material';
import apiAutoPlaybook from '@api1/autoPlaybook';
import apiAccount from '@api1/account';
import k8sApi from '@api1/kubernetes';
import PropTypes from 'prop-types';
import { useData } from '@context/DataContext';
import ActionButtons from './AutoOptimizeActionButtons';
import NotificationForm from './AutoOptimizeNotificationForm';
import { Textarea } from '@components1/k8s/common/TextArea';
import apiAutoPilot from '@api1/autoPilot';
import { hasWriteAccess } from '@lib/auth';
import { ds } from '@utils/colors';
import RunbookTargetResource from '@components1/runbooks/RunbookTargetResource';
import { snackbar } from '@components1/common/snackbarService';

const VerticalAutoOptimizeSingleConfiguration = ({
  autoOptimizeData,
  closeAutoPilotSingleConfigModal,
  msTeamsData,
  isMsTeamsLoading,
  googleChannelList,
  isGoogleChannelsLoading,
  listAutoPilot,
  setIsLoading,
  reviewAutoOptimize = false,
  approvalData = {},
}) => {
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
  const [slackChannelName, setSlackChannelName] = useState(autoOptimizeData?.notification?.slack?.channel_id || '');
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
    reviewComment: '',
  });
  const [selectedApplications, setSelectedApplications] = useState([]);
  const [selectedNamespaces, setSelectedNamespaces] = useState([]);

  useEffect(() => {
    const allAppsFromProps =
      autoOptimizeData?.auto_optimize_resource_maps?.map((m) => m.resource_identifier) || autoOptimizeData?.resource_filter || [];
    setSelectedApplications(allAppsFromProps);

    const distinctNamespaces = allAppsFromProps.reduce((acc, app) => {
      if (!acc.includes(app.namespace)) {
        acc.push(app.namespace);
      }
      return acc;
    }, []);
    setSelectedNamespaces(distinctNamespaces);
    // Depend on id so user edits to selectedApplications / selectedNamespaces aren't
    // clobbered by a re-render in which the parent recreates the prop object.
  }, [autoOptimizeData?.id]);

  const [reviewComment, setReviewComment] = useState('');
  const { selectedCluster } = useData();

  const [isLoadingSlackChannels, setIsLoadingSlackChannels] = useState(false);

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

  const handleReviewAction = (status) => {
    if (status == 'REJECTED') {
      if (!reviewComment) {
        setDisplayErrorsDesc({ ...displayErrorsDesc, reviewComment: 'Please add a review comment if you wish to reject the runbook' });
        return;
      }
      apiAutoPilot
        .updateAutoPilotApprovalStatus(
          approvalData?.id,
          autoOptimizeData?.accountId || autoOptimizeData?.account_id || selectedCluster?.value,
          reviewComment,
          'REJECTED'
        )
        .then((res) => {
          if (res?.data?.update_status_auto_pilot_approval?.id) {
            closeAutoPilotSingleConfigModal(true, 'REJECTED');
          } else {
            snackbar.error(`Failed to reject Auto Optimize !`);
          }
        });
    } else if (status == 'APPROVED') {
      apiAutoPilot
        .updateAutoPilotApprovalStatus(
          approvalData?.id,
          autoOptimizeData?.accountId || autoOptimizeData?.account_id || selectedCluster?.value,
          reviewComment,
          'APPROVED'
        )
        .then((res) => {
          if (res?.data?.update_status_auto_pilot_approval?.id) {
            closeAutoPilotSingleConfigModal(true, 'APPROVED');
          } else {
            snackbar.error(`Failed to approve Auto Optimize !`);
          }
        });
    }
  };

  const handleCreateAutoPilotRule = () => {
    if (!validateAutoPilotRequest()) {
      return;
    }

    if (isResourceFilterEmpty()) {
      showSnackBar('Please select Namespace & Application');
      return;
    }

    setLoadingState(true);

    const data = buildRequestData();

    const apiCall = !autoOptimizeData.id ? createAutoPilotRule : updateAutoPilotRule;
    apiCall(data);
  };

  const isResourceFilterEmpty = () => selectedApplications?.length === 0;

  const showSnackBar = (message) => {
    snackbar.error(message);
  };

  const setLoadingState = (state) => {
    if (setIsLoading) {
      setIsLoading(state);
    }
  };

  const buildRequestData = () => ({
    account_id: autoOptimizeData?.accountId || autoOptimizeData?.account_id || selectedCluster?.value,
    category: 'continuous_rightsize',
    resource_filter: selectedApplications,
    auto_optimize_config: autoOptimizeData?.rule || {},
    schedule: {
      frequency: autoOptimizeData?.schedule_time || '',
      start_date: autoOptimizeData?.start_at || null,
      end_date: autoOptimizeData?.end_at || null,
    },
    notification: buildNotificationData(),
    dryrun: false,
    gitops: autoOptimizeData?.attributes?.git_ops_config || {},
    ticket_config: autoOptimizeData?.attributes?.ticket_config || {},
    ...(!Object.keys(autoOptimizeData).length ? {} : { id: autoOptimizeData?.id }),
  });

  const buildNotificationData = () => ({
    slack: notificationData?.slack
      ? {
          enabled: notificationData?.slack,
          channel_id: slackChannelName,
        }
      : {
          enabled: notificationData?.slack,
        },
    ms_teams: notificationData?.teams
      ? {
          enabled: notificationData?.teams,
          team_id: msTeamName,
          channel_id: msChannelName,
        }
      : {
          enabled: notificationData?.teams,
        },
    email: {
      enabled: notificationData?.email,
    },
    google_chat: notificationData?.google_chat
      ? {
          enabled: notificationData?.google_chat,
          channel_id: googleChatChannelName,
        }
      : {
          enabled: notificationData?.google_chat,
        },
  });

  const createAutoPilotRule = (data) => {
    k8sApi
      .singleConfigAuotPilot(data)
      .then((res) => {
        handleSuccess(res, 'create');
      })
      .finally(() => setLoadingState(false));
  };

  const updateAutoPilotRule = (data) => {
    apiAutoPlaybook
      .singleConfigUpdateAuotPilot(data)
      .then((res) => {
        handleSuccess(res, 'update');
      })
      .finally(() => setLoadingState(false));
  };

  const handleSuccess = (res, type) => {
    if (res?.errors) {
      snackbar.error('Error - ' + res?.errors[0]?.message);
    } else {
      snackbar.success(type == 'create' ? 'Auto Optimize Rule Created Successfully' : 'Auto Optimize Rule Updated Successfully');
      closeAutoPilotSingleConfigModal(true);
      resetForm();
      if (listAutoPilot) {
        listAutoPilot();
      }
    }
  };

  const resetForm = () => {
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
    handleCancel();
  };

  const autoPilotSingleConfigButton = [
    {
      label: 'Cancel',
      backgroundColor: ds.blue[500],
      onClick: handleCancel,
    },
  ];

  if (reviewAutoOptimize && hasWriteAccess(autoOptimizeData?.accountId || autoOptimizeData?.account_id || selectedCluster?.value)) {
    autoPilotSingleConfigButton.push({
      label: 'Reject',
      isDisabled: !Object.keys(approvalData).length || ['REJECTED', 'APPROVED'].includes(approvalData.status),
      backgroundColor: ds.blue[500],
      onClick: () => handleReviewAction('REJECTED'),
    });
    autoPilotSingleConfigButton.push({
      isDisabled: !Object.keys(approvalData).length || ['REJECTED', 'APPROVED'].includes(approvalData.status),
      label: 'Approve',
      backgroundColor: ds.blue[500],
      onClick: () => handleReviewAction('APPROVED'),
    });
  } else {
    autoPilotSingleConfigButton.push({
      label: !autoOptimizeData.id ? 'Create Auto Optimize Rule' : 'Update Auto Optimize Rule',
      backgroundColor: ds.blue[500],
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

  const handleChildComponentChange = (value, type) => {
    switch (type) {
      case 'applications':
        setSelectedApplications(JSON.parse(value));
        break;
      case 'all-applications-check':
        setSelectedApplications(JSON.parse(value));
        break;
      case 'all-applications-uncheck':
        setSelectedApplications([]);
        break;
      case 'namespace':
        setSelectedApplications(selectedApplications.filter((app) => value.includes(app.namespace)));
        setSelectedNamespaces(value);
        break;
    }
  };

  return (
    <>
      <Box>
        <RunbookTargetResource
          selectedCluster={selectedCluster}
          selectedApplications={selectedApplications}
          selectedNamespace={selectedNamespaces}
          multipleNamespace
          reviewRunbook={reviewAutoOptimize}
          handleChildComponentChange={handleChildComponentChange}
        />
        <Box sx={{ display: 'flex', gap: ds.space[4], marginTop: ds.space[4] }}>
          <Box sx={{ display: 'flex', flexDirection: 'column', gap: ds.space[5] }}>
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
          <Box sx={{ display: 'flex', flexDirection: 'column', marginTop: ds.space[4] }}>
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
                <Typography sx={{ color: ds.red[500], fontSize: ds.text.body, marginTop: ds.space[1] }}>{displayErrorsDesc.reviewComment}</Typography>
              ) : null}
            </Box>
          </Box>
        )}

        <ActionButtons buttons={autoPilotSingleConfigButton} activeButton={activeButton} setActiveButton={setActiveButton} />
      </Box>
    </>
  );
};

VerticalAutoOptimizeSingleConfiguration.propTypes = {
  autoOptimizeData: PropTypes.object.isRequired,
  closeAutoPilotSingleConfigModal: PropTypes.func.isRequired,
  msTeamsData: PropTypes.array.isRequired,
  googleChannelList: PropTypes.array.isRequired,
  listAutoPilot: PropTypes.func,
  setIsLoading: PropTypes.func,
  isMsTeamsLoading: PropTypes.bool,
  isGoogleChannelsLoading: PropTypes.bool,
};

export default VerticalAutoOptimizeSingleConfiguration;
