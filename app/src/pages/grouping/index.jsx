import { Box } from '@mui/material';
import { useRouter } from 'next/router';
import React, { useEffect, useState } from 'react';
import KubernetesInsertApplicationGroupingModal from '@components1/k8s/landing/k8sGrouping/KubernetesInsertApplicationGroupingModal';
import KubernetesApplicationGroupingSummary from '@components1/k8s/landing/k8sGrouping/KubernetesApplicationGroupingSummary';
import apiAppGrouping from '@api1/application-groupings';
import KubernetesWorkloadsTable from '@components1/k8s/details/KubernetesWorkloads';
import KubernetesEvents from '@components1/events/KubernetesEvents';
import CustomTabs from '@common-new/CustomTabsForDrilldown';
import { snackbar } from '@components1/common/snackbarService';
import Loader from '@components1/common/Loader';

const tabOptions = [
  { text: 'Summary', value: 0, disabled: false },
  { text: 'Events', value: 1, disabled: false, betaIcon: true },
  { text: 'Applications', value: 2, disabled: false, betaIcon: true },
  { text: 'Monitoring', value: 3, disabled: true, betaIcon: true },
];

const KubernetesAppGroupingDashboard = () => {
  const router = useRouter();

  const [tab, setTab] = useState(tabOptions[0].value);
  const [groupId, setGroupId] = useState(router.query.groupId ?? '');
  const [groupingModalOpen, setGroupingModalOpen] = useState(false);
  const [isEdit, setIsEdit] = useState(false);
  const [applications, setApplications] = useState([]);
  const [resourceIds, setResourceIds] = useState([]);
  const [accountId, setAccountId] = useState('');
  const [accountName, setAccountName] = useState('');

  const [isDataReady, setIsDataReady] = useState(false);
  const [renderForApplicationIssue, setRenderForApplicationIssue] = useState(false);

  const getApplicationsByGroup = () => {
    setIsDataReady(false);
    apiAppGrouping
      .getApplicationsByGroup(groupId)
      .then((res) => {
        setAccountName(res?.data?.application_group_mapping[0]?.cloud_account.account_name);
        setAccountId(res?.data?.application_group_mapping[0]?.account_id);
        const appsMapping = res?.data?.application_group_mapping || [];
        const resourceIds = res?.data?.application_group_mapping.map((item) => item?.cloud_resource_id) || [];
        setResourceIds(resourceIds);
        setApplications(appsMapping);
      })
      .finally(() => {
        setIsDataReady(true);
      });
  };

  useEffect(() => {
    if (groupId != router.query.groupId) {
      setGroupId(router.query.groupId);
    }
  }, [router.query.groupId]);

  useEffect(() => {
    getApplicationsByGroup();
  }, [groupId]);

  const handleCloseModal = () => {
    setGroupingModalOpen(false);
    setIsEdit(false);
    getApplicationsByGroup();
  };

  const handleChangeTab = (_e, value) => {
    setRenderForApplicationIssue(false);
    setTab(value);
  };

  return (
    <>
      <KubernetesInsertApplicationGroupingModal
        open={groupingModalOpen}
        isUpdateGroup={isEdit}
        handleClose={handleCloseModal}
        groupId={groupId}
        handleSnackBarData={(data) => {
          snackbar[data.severity](data.message);
        }}
      />
      <Box sx={{ mt: '24px' }}>
        <CustomTabs
          options={tabOptions}
          value={tab}
          onChange={handleChangeTab}
          rightButton={{
            text: 'Edit Application Group',
            onClick: () => {
              setIsEdit(true);
              setGroupingModalOpen(true);
            },
            visible: true,
          }}
        />
      </Box>
      {tab === 0 &&
        (isDataReady ? (
          <KubernetesApplicationGroupingSummary
            applications={applications}
            accountId={accountId}
            accountName={accountName}
            groupId={groupId}
            setTab={setTab}
            setRenderForApplicationIssue={setRenderForApplicationIssue}
          />
        ) : (
          <Loader />
        ))}
      {tab === 1 && (
        <KubernetesEvents
          accountId={accountId}
          resource_ids={resourceIds}
          defaultQuery={
            renderForApplicationIssue ? { aggregation_key: ['HighErrorCriticalLogs', 'ApplicationAPIFailures'], finding_type: 'issue' } : {}
          }
        />
      )}
      {tab === 2 && <KubernetesWorkloadsTable resource_ids={resourceIds} accountId={accountId} />}
    </>
  );
};

export default KubernetesAppGroupingDashboard;
