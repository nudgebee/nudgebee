import React, { useState, useEffect } from 'react';
import { useRouter } from 'next/router';
import { Modal } from '@components1/common/modal';
import apiAccount from '@api1/account';
import AutoOptimizeVerticalRightSizingSingleConfiguration from '@components1/autopilot/form/AutoOptimizeVerticalRightSizingSingleConfiguration';
import AutoOptimizeHorizontalRightSizingSingleConfiguration from '@components1/autopilot/form/AutoOptimizeHorizontalRightSizingSingleConfiguration';
import AutoOptimizePVRightSizingSingleConfiguration from '@components1/autopilot/form/AutoOptimizePVRightSizingSingleConfiguration';
import AutoOptimizeContinuousVerticalRightSizingSingleConfiguration from '@components1/autopilot/form/AutoOptimizeContinuousVerticalRightSizingSingleConfiguration';
import AutoOptimizeListingTable from './AutoOptimizeListingTable';
import AutoPilotApprovalsListing from './AutoPilotApprovalsTable';

interface AutoOptimizeTabsProps {
  subTab?: number;
  handleOpenCreateAutoOptimize: () => void;
  handleCloseCreateAutoOptimize: () => void;
  openCreateAutoOptimize: boolean;
  openCreateAutoOptimizeType: string;
  type?: string;
  _type?: string;
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

const AutoOptimizeTabs: React.FC<AutoOptimizeTabsProps> = ({
  subTab = 0,
  handleOpenCreateAutoOptimize,
  handleCloseCreateAutoOptimize,
  openCreateAutoOptimize,
  openCreateAutoOptimizeType,
  _type = 'K8s',
}) => {
  const [autoOptimizeData, setAutoOptimizeData] = useState({});
  const [msTeamsData, setMsTeamsData] = useState<{ label: string; value: string; channels?: { name: string; id: string }[] }[]>([]);
  const [isMsTeamsLoading, setIsMsTeamsLoading] = useState<boolean>(false);
  const [googleChannelList, setGoogleChannelList] = useState<{ label: string; value: string }[]>([]);
  const [isGoogleChannelsLoading, setIsGoogleChannelsLoading] = useState<boolean>(false);
  const [loading, setLoading] = useState<boolean>(false);
  const [refreshListing, setRefreshListing] = useState<boolean>(false);

  const router = useRouter();

  useEffect(() => {
    const fetchMsTeamsChannels = async () => {
      if (msTeamsData.length === 0) {
        setIsMsTeamsLoading(true);
        try {
          const res = (await apiAccount.getNotificationChannelList('ms_teams')) as ApiResponse;
          const teamOptions =
            res?.data?.data?.map((item: NotificationChannel) => ({
              label: item.name,
              value: item.id,
              channels: item.channels?.map((channel) => ({ name: channel.name, id: channel.id })),
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

    if (openCreateAutoOptimize) {
      fetchMsTeamsChannels();
      fetchGoogleChatChannels();
    }
  }, [openCreateAutoOptimize, msTeamsData.length, googleChannelList.length]);

  const closeAutoPilotSingleConfigModal = (success: boolean) => {
    if (success) {
      setRefreshListing(!refreshListing);
    }
    if (handleCloseCreateAutoOptimize) {
      setAutoOptimizeData({});
      handleCloseCreateAutoOptimize();
    }
  };

  return (
    <>
      {openCreateAutoOptimize && openCreateAutoOptimizeType === 'continuous_rightsize' && (
        <Modal
          width='md'
          open={openCreateAutoOptimize}
          handleClose={() => closeAutoPilotSingleConfigModal(false)}
          title={
            !Object.keys(autoOptimizeData).length
              ? 'Auto Optimize Configuration - Vertical RightSizing'
              : 'Update Auto Optimize Configuration - Vertical RightSizing'
          }
          loader={loading}
        >
          <AutoOptimizeContinuousVerticalRightSizingSingleConfiguration
            autoOptimizeData={autoOptimizeData}
            closeAutoPilotSingleConfigModal={closeAutoPilotSingleConfigModal}
            msTeamsData={msTeamsData}
            isMsTeamsLoading={isMsTeamsLoading}
            googleChannelList={googleChannelList}
            isGoogleChannelsLoading={isGoogleChannelsLoading}
            setIsLoading={setLoading}
          />
        </Modal>
      )}
      {openCreateAutoOptimize && openCreateAutoOptimizeType === 'vertical_rightsize' && (
        <Modal
          width='md'
          open={openCreateAutoOptimize}
          handleClose={() => closeAutoPilotSingleConfigModal(false)}
          title={
            !Object.keys(autoOptimizeData).length
              ? 'Auto Optimize Configuration - Scheduled Vertical RightSizing'
              : 'Update Auto Optimize Configuration - Scheduled Vertical RightSizing'
          }
          loader={loading}
        >
          <AutoOptimizeVerticalRightSizingSingleConfiguration
            autoOptimizeData={autoOptimizeData}
            closeAutoPilotSingleConfigModal={closeAutoPilotSingleConfigModal}
            msTeamsData={msTeamsData}
            isMsTeamsLoading={isMsTeamsLoading}
            googleChannelList={googleChannelList}
            isGoogleChannelsLoading={isGoogleChannelsLoading}
            setIsLoading={setLoading}
            currentData={{}}
          />
        </Modal>
      )}
      {openCreateAutoOptimize && openCreateAutoOptimizeType === 'horizontal_rightsize' && (
        <Modal
          width='lg'
          open={openCreateAutoOptimize}
          handleClose={() => closeAutoPilotSingleConfigModal(false)}
          title={!Object.keys(autoOptimizeData).length ? 'Auto Optimize - Replica Rightsizing' : 'Update Auto Optimize - Replica RightSizing'}
          loader={loading}
        >
          <AutoOptimizeHorizontalRightSizingSingleConfiguration
            autoOptimizeData={autoOptimizeData}
            closeAutoPilotSingleConfigModal={closeAutoPilotSingleConfigModal}
            msTeamsData={msTeamsData}
            isMsTeamsLoading={isMsTeamsLoading}
            googleChannelList={googleChannelList}
            isGoogleChannelsLoading={isGoogleChannelsLoading}
            setIsLoading={setLoading}
          />
        </Modal>
      )}
      {openCreateAutoOptimize && openCreateAutoOptimizeType === 'pvc_rightsize' && (
        <Modal
          width='md'
          open={openCreateAutoOptimize}
          handleClose={() => closeAutoPilotSingleConfigModal(false)}
          title={
            !Object.keys(autoOptimizeData).length
              ? 'Auto Optimize - Persistent Volume Claim Rightsizing'
              : 'Update Auto Optimize - Persistent Volume Claim Rightsizing'
          }
          loader={loading}
        >
          <AutoOptimizePVRightSizingSingleConfiguration
            autoOptimizeData={autoOptimizeData}
            closeAutoPilotSingleConfigModal={closeAutoPilotSingleConfigModal}
            msTeamsData={msTeamsData}
            isMsTeamsLoading={isMsTeamsLoading}
            googleChannelList={googleChannelList}
            isGoogleChannelsLoading={isGoogleChannelsLoading}
            setIsLoading={setLoading}
            _isLoading={loading}
          />
        </Modal>
      )}
      {subTab == 0 && (
        <AutoOptimizeListingTable
          handleOpenCreateAutoOptimize={handleOpenCreateAutoOptimize}
          autoOptimizeData={autoOptimizeData}
          setAutoOptimizeData={(data: any) => setAutoOptimizeData(data)}
          refresh={refreshListing}
        />
      )}
      {subTab == 1 && <AutoPilotApprovalsListing type={'auto_optimize'} accountId={router?.query?.accountId as string} />}
    </>
  );
};

export default AutoOptimizeTabs;
