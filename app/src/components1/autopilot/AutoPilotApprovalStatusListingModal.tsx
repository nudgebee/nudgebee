import apiAutoPilot from '@api1/autoPilot';
import { Box } from '@mui/material';
import Datetime from '@components1/common/format/Datetime';
import { Modal } from '@components1/ds/Modal';
import CustomTable from '@common-new/tables/CustomTable2';
import CustomLabels from '@components1/common/widgets/CustomLabels';
import { useState, useEffect } from 'react';
import apiUser from '@api1/user';
import { ds } from '@utils/colors';

interface AutoPilotApprovalStatusListingModalProps {
  id: string;
  name: string;
  open: boolean;
  handleClose: () => void;
}

const getAttributesLabels = (label: any) => {
  if (Object.keys(label).includes('minimum_approval')) {
    return <CustomLabels text={`Minimum Required Approvals : ${label['minimum_approval']}`} />;
  }
};

const AutoPilotApprovalStatusListingModal: React.FC<AutoPilotApprovalStatusListingModalProps> = ({ id, name, open, handleClose }) => {
  const [tableData, setTableData] = useState<any[]>([]);
  const [loading, setLoading] = useState<boolean>(false);
  const [attributes, setAttributes] = useState<any>();

  const [perPage, setPerPage] = useState<number>(apiUser.getUserPreferencesTablePageSize());
  const [currentPage, setCurrentPage] = useState<number>(1);

  const [totalRows, setTotalRows] = useState<number>(0);

  useEffect(() => {
    if (open && id) {
      getStatusListing();
    }
  }, [id, open, perPage, currentPage]);

  const getStatusListing = () => {
    setLoading(true);
    apiAutoPilot.getAutoPilotApprovalStatusById(id, perPage, (currentPage - 1) * perPage).then((res: any) => {
      const rows: any[] = res?.data?.auto_pilot_approvals.map((item: any) => [
        { text: item?.user_reviwer_id?.display_name },
        {
          component: <CustomLabels text={item?.status} />,
        },
        { text: item?.reviewer_comments ? '"' + item?.reviewer_comments + '"' : '' },
        { text: <Datetime baseDate={new Date()} value={item?.updated_at} /> },
      ]);
      setLoading(false);
      setTableData(rows);
      setAttributes(res?.data?.attr[0]?.auto_pilot_approval_policy?.policy_attributes);
      setTotalRows(res?.data?.auto_pilot_approvals_aggregate?.aggregate?.count || 0);
    });
  };
  const clearAllAndClose = () => {
    setTableData([]);
    handleClose();
  };
  return (
    <Modal open={open} handleClose={clearAllAndClose} title={'review status for - ' + `"${name}"`} width={'md'}>
      <Box sx={{ p: ds.space[4], mb: ds.space[5] }}>
        {attributes && <Box>{getAttributesLabels(attributes)}</Box>}
        <CustomTable
          id={'autopilot-approval-status-listing'}
          headers={['Reviewer', 'status', 'comment', 'reviewed at']}
          tableData={tableData}
          loading={loading}
          totalRows={totalRows}
          rowsPerPage={perPage}
          pageNumber={currentPage}
          onPageChange={(page: number, limit: number) => {
            setCurrentPage(page);
            setPerPage(limit);
          }}
        />
      </Box>
    </Modal>
  );
};

export default AutoPilotApprovalStatusListingModal;
