import { BoxLayout2 } from '@components1/common';
import React, { useEffect, useState } from 'react';
import { hasWriteAccess } from '@lib/auth';
import apiAutoPilot from '@api1/autoPilot';
import CustomTable from '@components1/common/tables/CustomTable2';
import Datetime from '@components1/common/format/Datetime';
import CustomIconButton from '@components1/CustomIconButton';
import { writeIcon } from '@assets';
import apiUser from '@api1/user';
import { Modal } from '@components1/common/modal';
import AutoPilotPolicyForm from './form/AutoPilotPolicyForm';
import CustomLabels from '@components1/common/widgets/CustomLabels';
import DeleteButton from '@components1/k8s/common/DeleteButton';
import { Box, Typography } from '@mui/material';
import { snackbar } from '@components1/common/snackbarService';
import SafeIcon from '@components1/common/SafeIcon';

interface ActivePolicyDataProps {
  name?: string;
  accountId?: string;
}

export const getAttributesLabels = (label: any) => {
  if (Object.keys(label).includes('minimum_approval')) {
    return <CustomLabels text={`Minimum Required Approvals : ${label['minimum_approval']}`} />;
  }
};

const ApprovalPolicy: React.FC = () => {
  const [tableData, setTableData] = useState<any[]>();

  const [policyModalOpen, setPolicyModalOpen] = useState<boolean>(false);
  const [rowsPerPage, setRowsPerPage] = useState<number>(apiUser.getUserPreferencesTablePageSize());
  const [currentPage, setCurrentPage] = useState<number>(0);
  const [loading, setLoading] = useState<boolean>(false);

  const [activePolicyId, setActivePolicyId] = useState<string>();
  const [usedClusters, setUsedClusters] = useState<any[]>([]);

  const [deleteModalOpen, setDeleteModalOpen] = useState<boolean>(false);
  const [activePolicyData, setActivePolicyData] = useState<ActivePolicyDataProps>({ name: '', accountId: '' });
  const approvalTableId = 'approval-policy';

  const getPolicyListing = () => {
    setLoading(true);
    apiAutoPilot.getAutoPilotPolicies(rowsPerPage, currentPage).then((res: any) => {
      const rows = res?.data?.auto_pilot_approval_policy?.map((item: any) => [
        { text: item?.cloud_account?.account_name },
        { component: getAttributesLabels(item?.policy_attributes) },
        { component: <Datetime value={item?.created_at} /> },
        { text: item?.create_by_user?.display_name },
        { component: <Datetime value={item?.updated_at} /> },
        { component: item?.update_by_user?.display_name },
        {
          component: hasWriteAccess() ? (
            <Box sx={{ display: 'flex' }}>
              <CustomIconButton
                sx={{ mr: '8px' }}
                variant={'iconButton'}
                onClick={() => {
                  setActivePolicyId(item?.id);
                  setPolicyModalOpen(true);
                }}
              >
                <SafeIcon src={writeIcon} alt='edit' />
              </CustomIconButton>
              <DeleteButton onClick={() => handleOpenDeleteModal(item)} />
            </Box>
          ) : null,
        },
      ]);
      setUsedClusters(res?.data?.auto_pilot_approval_policy?.map((item: any) => item.account_id));
      setTableData(rows);
      setLoading(false);
    });
  };

  const handleOpenPolicyModal = () => {
    setPolicyModalOpen(true);
    setActivePolicyId('');
    return;
  };

  const onPageChange = (page: number, limit: number) => {
    setCurrentPage(page - 1);
    setRowsPerPage(limit);
  };

  useEffect(() => {
    getPolicyListing();
  }, []);

  const handleOpenDeleteModal = (item: any) => {
    setActivePolicyId(item?.id);
    setActivePolicyData({ name: item?.cloud_account?.account_name, accountId: item?.account_id });
    setDeleteModalOpen(true);
  };

  const handleCloseDeleteModal = () => {
    setActivePolicyId('');
    setActivePolicyData({ name: '', accountId: '' });
    setDeleteModalOpen(false);
  };

  const handleDeletePolicy = () => {
    apiAutoPilot.deleteAutoPilotPolicy(activePolicyData.name as string, activePolicyData.accountId as string).then((res: any) => {
      if (res?.data?.auto_pilot_approval_policy_delete?.id) {
        snackbar.success(`Policy at ${activePolicyData.name} deleted successfully`);
        getPolicyListing();
      } else {
        snackbar.error(`Failed to delete policy at ${activePolicyData.name}`);
      }
      handleCloseDeleteModal();
    });
  };

  return (
    <>
      <Modal
        width={'sm'}
        open={deleteModalOpen}
        handleClose={() => {
          handleCloseDeleteModal();
        }}
        title={'Delete Approval Policy'}
      >
        <Box p={' 0px 16px 16px 16px '}>
          <Typography sx={{ fontSize: '16px', fontWeight: 400, mb: '8px' }}>
            Are you sure you want to delete the policy set at cluster &apos;{activePolicyData.name}&apos;
          </Typography>
          <Typography sx={{ fontSize: '14px', fontWeight: 400 }}>
            <b>Note:</b> All policies pending approval on &apos;{activePolicyData.name}&apos; will be disabled.
          </Typography>
          <Box sx={{ display: 'flex', justifyContent: 'space-between', mt: '24px' }}>
            <CustomIconButton variant={'secondary'} size={'small'} onClick={() => handleCloseDeleteModal()}>
              Cancel
            </CustomIconButton>
            <CustomIconButton variant={'primary'} size={'small'} onClick={() => handleDeletePolicy()}>
              Delete
            </CustomIconButton>
          </Box>
        </Box>
      </Modal>
      <Modal
        width={'md'}
        open={policyModalOpen}
        handleClose={() => {
          setPolicyModalOpen(false);
        }}
        title={activePolicyId ? 'Edit Policy' : 'Create Policy'}
        loader={loading}
      >
        <AutoPilotPolicyForm
          handlePopupClose={() => {
            setPolicyModalOpen(false);
            getPolicyListing();
          }}
          policyId={activePolicyId}
          usedClusters={usedClusters}
        />
      </Modal>
      <BoxLayout2
        id=''
        modalButton={{
          enabled: hasWriteAccess(),
          text: 'Create Policy',
          onClick: () => {
            handleOpenPolicyModal();
          },
          id: 'create-policy',
        }}
        sharingOptions={{
          download: {
            enabled: true,
            onClick: () => {
              return {
                tableId: approvalTableId,
              };
            },
          },
          sharing: { enabled: false, onClick: null },
        }}
      >
        <CustomTable
          id={approvalTableId}
          tableData={tableData}
          headers={['Cluster', 'Attributes', 'Created At', 'Created By', 'Updated At', 'Updated By', '']}
          rowsPerPage={rowsPerPage}
          onPageChange={onPageChange}
          loading={loading}
          pageNumber={currentPage + 1}
        />
      </BoxLayout2>
    </>
  );
};

export default ApprovalPolicy;
