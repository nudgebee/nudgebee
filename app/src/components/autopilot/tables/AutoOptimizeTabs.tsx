import React, { useState, useEffect, useMemo } from 'react';
import { Modal } from '@ui/Modal';
import apiAccount from '@api1/account';
import apiUser from '@api1/user';
import { getCurrentTenant } from '@lib/auth';
import AutoOptimizeVerticalRightSizingSingleConfiguration from '@components/autopilot/form/AutoOptimizeVerticalRightSizingSingleConfiguration';
import AutoOptimizeHorizontalRightSizingSingleConfiguration from '@components/autopilot/form/AutoOptimizeHorizontalRightSizingSingleConfiguration';
import AutoOptimizePVRightSizingSingleConfiguration from '@components/autopilot/form/AutoOptimizePVRightSizingSingleConfiguration';
import AutoOptimizeContinuousVerticalRightSizingSingleConfiguration from '@components/autopilot/form/AutoOptimizeContinuousVerticalRightSizingSingleConfiguration';
import AutoOptimizeListingTable from './AutoOptimizeListingTable';
import AutoPilotApprovalsListing from './AutoPilotApprovalsTable';
import { useData } from '@context/DataContext';
import { useUpdateAllClusterOption } from '@shared/layout/UpdateDataContext';

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
  const [manualAccountId, setManualAccountId] = useState<string>('');

  const tenantId = getCurrentTenant().id;
  const { selectedCluster, allCluster } = useData();
  const updateAllClusters = useUpdateAllClusterOption();

  // allCluster is only populated by the header cluster dropdown elsewhere in
  // the app; since /optimise doesn't render it, fetch it ourselves the first
  // time this tab is opened.
  useEffect(() => {
    if (allCluster == null) {
      updateAllClusters();
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [allCluster]);

  // Auto Optimize only targets Kubernetes workloads, so the account filter
  // is scoped to K8s-type accounts only.
  const accountOptions = useMemo(
    () =>
      (allCluster || [])
        .filter((cluster: any) => cluster.cloud_provider === 'K8s')
        .map((cluster: any) => ({ label: cluster.label, value: cluster.value })),
    [allCluster]
  );

  // The current account is never read from (or written to) the URL, the
  // per-provider preference cache, or the global selectedCluster — a manual
  // pick lives only in this tab's own state (manualAccountId). Absent a
  // manual pick, it defaults the same way GlobalPageSearch.jsx resolves "an
  // account of this provider": the globally selected cluster if it's K8s,
  // else the user's last-picked K8s account (read-only lookup), else the
  // first available K8s account.
  const resolvedAccountId = useMemo(() => {
    if (manualAccountId && accountOptions.some((option: { value: string }) => option.value === manualAccountId)) {
      return manualAccountId;
    }
    if (selectedCluster?.cloud_provider === 'K8s' && selectedCluster?.value) {
      return selectedCluster.value;
    }
    const cachedId = apiUser.getLastAccountIdForProvider('K8s', tenantId);
    if (cachedId && accountOptions.some((option: { value: string }) => option.value === cachedId)) {
      return cachedId;
    }
    return accountOptions[0]?.value || '';
  }, [manualAccountId, selectedCluster, accountOptions, tenantId]);

  // A manual pick only updates this tab's own state — it doesn't touch the
  // URL, the per-provider preference cache, or the global selectedCluster.
  const handleAccountChange = (e: any, option: any): void => {
    const value = e?.target?.value || option?.value;
    if (!value) {
      return;
    }
    setManualAccountId(value);
  };

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
            accountOptions={accountOptions}
            defaultAccountId={resolvedAccountId}
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
            accountOptions={accountOptions}
            defaultAccountId={resolvedAccountId}
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
            accountOptions={accountOptions}
            defaultAccountId={resolvedAccountId}
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
            accountOptions={accountOptions}
            defaultAccountId={resolvedAccountId}
          />
        </Modal>
      )}
      {subTab == 0 && (
        <AutoOptimizeListingTable
          handleOpenCreateAutoOptimize={handleOpenCreateAutoOptimize}
          autoOptimizeData={autoOptimizeData}
          setAutoOptimizeData={(data: any) => setAutoOptimizeData(data)}
          refresh={refreshListing}
          accountOptions={accountOptions}
          selectedAccountId={resolvedAccountId}
          isAccountsLoading={allCluster == null}
          onAccountChange={handleAccountChange}
        />
      )}
      {subTab == 1 && (
        <AutoPilotApprovalsListing
          type={'auto_optimize'}
          accountId={resolvedAccountId}
          accountOptions={accountOptions}
          selectedAccountId={resolvedAccountId}
          isAccountsLoading={allCluster == null}
          onAccountChange={handleAccountChange}
        />
      )}
    </>
  );
};

export default AutoOptimizeTabs;
