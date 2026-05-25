import React, { useEffect, useState } from 'react';
import { ListingLayout } from '@components1/ds/ListingLayout';
import DownloadButton from '@common-new/DownloadButton';
import apiAutoPilot from '@api1/autoPilot';
import CustomTable from '@common-new/tables/CustomTable2';
import CustomLabels from '@common-new/widgets/CustomLabels';
import { Button as DsButton } from '@components1/ds/Button';
import Datetime from '@common-new/format/Datetime';
import apiAutoPlaybook from '@api1/autoPlaybook';
import { Modal } from '@common-new/modal';
import AutoOptimizeContinuousVerticalRightSizingSingleConfiguration from '@components1/autopilot/form/AutoOptimizeContinuousVerticalRightSizingSingleConfiguration';
import AutoOptimizeVerticalRightSizingSingleConfiguration from '@components1/autopilot/form/AutoOptimizeVerticalRightSizingSingleConfiguration';
import AutoOptimizeHorizontalRightSizingSingleConfiguration from '@components1/autopilot/form/AutoOptimizeHorizontalRightSizingSingleConfiguration';
import AutoOptimizePVRightSizingSingleConfiguration from '@components1/autopilot/form/AutoOptimizePVRightSizingSingleConfiguration';
import { getUserSession } from '@lib/auth';
import apiAccount from '@api1/account';
import { toast as snackbar } from '@components1/ds/Toast';

interface AutoPilotApprovalsListingProps {
  accountId: string;
  type: string;
}

interface NotificationChannel {
  name: string;
  id: string;
  channels?: { name: string; id: string }[];
}

interface ApiResponse {
  data: {
    data: NotificationChannel[];
  };
}

const tableId = 'approvals';

const AutoPilotApprovalsListing: React.FC<AutoPilotApprovalsListingProps> = ({ accountId, type }) => {
  const [tableRows, setTableRows] = useState([]);
  const [approvalData, setApprovalData] = useState<any>({});
  const [loading, setLoading] = useState<boolean>(false);
  const [autoOptimizeFormType, setAutoOptimizeFormType] = useState<string>('');
  const [autoOptimizeData, setAutoOptimizeData] = useState<any>({});
  const [isAutoOptimizeSingleFormOpen, setIsAutoOptimizeSingleFormOpen] = useState(false);
  const [msTeamsData, setMsTeamsData] = useState<{ label: string; value: string; channels?: { name: string; id: string }[] }[]>([]);
  const [googleChannelList, setGoogleChannelList] = useState<{ label: string; value: string }[]>([]);
  const [isMsTeamsLoading, setIsMsTeamsLoading] = useState(false);
  const [isGoogleChannelsLoading, setIsGoogleChannelsLoading] = useState(false);

  const [allAutoPilotNames, setAllAutoPilotNames] = useState([]);

  const currentUser = getUserSession().user.email;

  const getAutoOptimize = (id: string) => {
    apiAutoPilot.getAutoPilotByPk(id).then((res) => {
      setAutoOptimizeData(res?.data?.auto_pilot_by_pk);
      setAutoOptimizeFormType(res?.data?.auto_pilot_by_pk?.category);
      setIsAutoOptimizeSingleFormOpen(true);
    });
  };

  const handleReviewClick = (item: any) => {
    setApprovalData(item);
    if (item.auto_pilot_type == 'auto_optimize') {
      getAutoOptimize(item?.autopilot_id);
    }
  };

  const getAutoPilotNames = () => {
    const query: any = {
      accountId: accountId,
    };
    if (type == 'runbook') {
      apiAutoPlaybook.listAutoPlaybook({ account_id: { _eq: accountId } }, 1000, 0, { sort_by: 'name', sort_order: 'asc' }).then((res: any) => {
        setAllAutoPilotNames(res?.data?.auto_playbook_listing ?? []);
      });
    } else if (type == 'auto_optimize') {
      apiAutoPilot.getAutoOptimizeNames(query).then((res: any) => {
        setAllAutoPilotNames(res?.data ?? []);
      });
    }
  };

  useEffect(() => {
    getAutoPilotNames();
  }, [accountId]);

  const getAutoPilotNameById = (id: string) => {
    const item: any = allAutoPilotNames.find((i: any) => i.id == id);
    if (item) {
      return item.name;
    }
    return '-';
  };

  const getApprovalsListing = () => {
    setLoading(true);
    apiAutoPilot.getAutoPilotApprovals(accountId, type, currentUser).then((res: any) => {
      if (res.errors) {
        setLoading(false);
      } else {
        const tableData = res?.data?.auto_pilot_approvals.map((item: any) => [
          { text: getAutoPilotNameById(item?.autopilot_id) },
          { text: item?.auto_pilot_approval_status?.description },
          { component: <CustomLabels text={item?.status} /> },
          { text: item?.reviewer_comments ?? '-' },
          { component: <Datetime value={item?.created_at} /> },
          {
            component: !['APPROVED', 'REJECTED'].includes(item?.status) ? (
              <DsButton tone='primary' size='sm' onClick={() => handleReviewClick(item)}>
                Review
              </DsButton>
            ) : (
              <></>
            ),
          },
        ]);
        setTableRows(tableData);
        setLoading(false);
      }
    });
  };

  useEffect(() => {
    if (allAutoPilotNames.length) {
      getApprovalsListing();
    }
  }, [allAutoPilotNames]);

  useEffect(() => {
    if (type == 'auto_optimize' && isAutoOptimizeSingleFormOpen) {
      const fetchMsTeamsChannels = async () => {
        if (msTeamsData.length === 0) {
          setIsMsTeamsLoading(true);
          try {
            const res = (await apiAccount.getNotificationChannelList('ms_teams')) as ApiResponse;
            const teamOptions =
              res?.data?.data?.map((item: NotificationChannel) => ({
                label: item.name,
                value: item.id,
                channels: item.channels,
              })) || [];
            setMsTeamsData(teamOptions);
          } finally {
            setIsMsTeamsLoading(false);
          }
        }
      };

      const fetchGoogleChatChannels = async () => {
        if (googleChannelList.length === 0) {
          setIsGoogleChannelsLoading(true);
          try {
            const res = (await apiAccount.getNotificationChannelList('google_chat')) as ApiResponse;
            const chatOptions =
              res?.data?.data?.map((item: NotificationChannel) => ({
                label: item.name,
                value: item.id,
              })) || [];
            setGoogleChannelList(chatOptions);
          } finally {
            setIsGoogleChannelsLoading(false);
          }
        }
      };

      fetchMsTeamsChannels();
      fetchGoogleChatChannels();
    }
  }, [isAutoOptimizeSingleFormOpen, msTeamsData.length, googleChannelList.length]);

  const closeAutoPilotSingleConfigModal = (success: boolean, status = '') => {
    if (success) {
      snackbar.success(`Auto Optimize ${status} Successfully`);
    }
    setIsAutoOptimizeSingleFormOpen(false);
    setAutoOptimizeFormType('');
    getApprovalsListing();
  };

  return (
    <>
      {isAutoOptimizeSingleFormOpen && autoOptimizeFormType === 'continuous_rightsize' && (
        <Modal
          width='md'
          open={isAutoOptimizeSingleFormOpen}
          handleClose={() => closeAutoPilotSingleConfigModal(false)}
          title={'Review Auto Optimize Configuration - Vertical RightSizing'}
          loader={loading}
        >
          <AutoOptimizeContinuousVerticalRightSizingSingleConfiguration
            autoOptimizeData={autoOptimizeData}
            closeAutoPilotSingleConfigModal={closeAutoPilotSingleConfigModal}
            msTeamsData={msTeamsData}
            googleChannelList={googleChannelList}
            isMsTeamsLoading={isMsTeamsLoading}
            isGoogleChannelsLoading={isGoogleChannelsLoading}
            listAutoPilot={getApprovalsListing}
            setIsLoading={setLoading}
            reviewAutoOptimize={true}
            approvalData={approvalData}
          />
        </Modal>
      )}
      {isAutoOptimizeSingleFormOpen && autoOptimizeFormType === 'vertical_rightsize' && (
        <Modal
          width='md'
          open={isAutoOptimizeSingleFormOpen}
          handleClose={() => closeAutoPilotSingleConfigModal(false)}
          title={'Review Auto Optimize Configuration - Scheduled Vertical RightSizing'}
          loader={loading}
        >
          <AutoOptimizeVerticalRightSizingSingleConfiguration
            autoOptimizeData={autoOptimizeData}
            closeAutoPilotSingleConfigModal={closeAutoPilotSingleConfigModal}
            msTeamsData={msTeamsData}
            googleChannelList={googleChannelList}
            isMsTeamsLoading={isMsTeamsLoading}
            isGoogleChannelsLoading={isGoogleChannelsLoading}
            listAutoPilot={getApprovalsListing}
            setIsLoading={setLoading}
            currentData={{}}
            data={{}}
            reviewAutoOptimize={true}
            approvalData={approvalData}
          />
        </Modal>
      )}
      {isAutoOptimizeSingleFormOpen && autoOptimizeFormType === 'horizontal_rightsize' && (
        <Modal
          width='md'
          open={isAutoOptimizeSingleFormOpen}
          handleClose={() => closeAutoPilotSingleConfigModal(false)}
          title={'Review Auto Optimize Configuration  - Replica Rightsizing'}
          loader={loading}
        >
          <AutoOptimizeHorizontalRightSizingSingleConfiguration
            autoOptimizeData={autoOptimizeData}
            closeAutoPilotSingleConfigModal={closeAutoPilotSingleConfigModal}
            msTeamsData={msTeamsData}
            googleChannelList={googleChannelList}
            isMsTeamsLoading={isMsTeamsLoading}
            isGoogleChannelsLoading={isGoogleChannelsLoading}
            listAutoPilot={getApprovalsListing}
            setIsLoading={setLoading}
            reviewAutoOptimize={true}
            approvalData={approvalData}
          />
        </Modal>
      )}
      {isAutoOptimizeSingleFormOpen && autoOptimizeFormType === 'pvc_rightsize' && (
        <Modal
          width='md'
          open={isAutoOptimizeSingleFormOpen}
          handleClose={() => closeAutoPilotSingleConfigModal(false)}
          title={'Review Auto Optimize Configuration - Persistent Volume Claim Rightsizing'}
          loader={loading}
        >
          <AutoOptimizePVRightSizingSingleConfiguration
            autoOptimizeData={autoOptimizeData}
            closeAutoPilotSingleConfigModal={closeAutoPilotSingleConfigModal}
            msTeamsData={msTeamsData}
            googleChannelList={googleChannelList}
            isMsTeamsLoading={isMsTeamsLoading}
            isGoogleChannelsLoading={isGoogleChannelsLoading}
            listAutoPilot={getApprovalsListing}
            setIsLoading={setLoading}
            _isLoading={loading}
            reviewAutoOptimize={true}
            approvalData={approvalData}
          />
        </Modal>
      )}
      <ListingLayout id='autopilot-approvals-listing-box'>
        <ListingLayout.Toolbar actions={<DownloadButton onClick={() => ({ tableId: tableId })} />} />
        <ListingLayout.Body>
          <CustomTable
            id={tableId}
            headers={[
              { name: 'Name', width: '25%' },
              { name: 'Description', width: '20%' },
              { name: 'Status', width: '10%' },
              { name: 'Comment', width: '25%' },
              { name: 'Created at', width: '10%' },
              { name: '', width: '5%' },
            ]}
            tableData={tableRows}
            loading={loading}
            rowsPerPage={tableRows?.length}
          />
        </ListingLayout.Body>
      </ListingLayout>
    </>
  );
};

export default AutoPilotApprovalsListing;
