import React, { useEffect, useState } from 'react';
import { ListingLayout } from '@ui/ListingLayout';
import FilterDropdown from '@ui/FilterDropdown';
import DownloadButton from '@shared/buttons/DownloadButton';
import apiAutoPilot from '@api1/autoPilot';
import CustomTable from '@shared/tables/CustomTable';
import { Label, type LabelTone } from '@ui/Label';
import { Button as DsButton } from '@ui/Button';
import Datetime from '@shared/format/Datetime';
import { hasWriteAccess } from '@lib/auth';
import AutoOptimizeRunReviewModal, { type RunApproval } from '@components/autopilot/AutoOptimizeRunReviewModal';

interface AutoPilotApprovalsListingProps {
  accountId: string;
  type: string;
  accountOptions?: { label: string; value: string }[];
  selectedAccountId?: string;
  isAccountsLoading?: boolean;
  onAccountChange?: (e: unknown, option: { value: string } | null) => void;
}

const tableId = 'approvals';

const STATUS_DISPLAY: Record<string, { text: string; tone: LabelTone }> = {
  PENDING: { text: 'Waiting', tone: 'warning' },
  APPROVED: { text: 'Approved', tone: 'success' },
  REJECTED: { text: 'Rejected', tone: 'critical' },
  EXPIRED: { text: 'Expired', tone: 'neutral' },
  CANCELLED: { text: 'Cancelled', tone: 'neutral' },
};

// A PENDING row past its window is expired even before the automation's next
// run records it.
const effectiveStatus = (item: any) => {
  const expiresAt = item?.attributes?.expires_at;
  if (item?.status === 'PENDING' && expiresAt && new Date(expiresAt).getTime() <= Date.now()) {
    return 'EXPIRED';
  }
  return item?.status;
};

const AutoPilotApprovalsListing: React.FC<AutoPilotApprovalsListingProps> = ({
  accountId,
  type,
  accountOptions = [],
  selectedAccountId = '',
  isAccountsLoading = false,
  onAccountChange,
}) => {
  const [tableRows, setTableRows] = useState([]);
  const [loading, setLoading] = useState<boolean>(false);
  const [reviewing, setReviewing] = useState<RunApproval | null>(null);
  const [allAutoPilotNames, setAllAutoPilotNames] = useState([]);

  const canDecide = hasWriteAccess(accountId);

  const getAutoPilotNames = () => {
    if (type == 'auto_optimize') {
      apiAutoPilot.getAutoOptimizeNames({ accountId }).then((res: any) => {
        setAllAutoPilotNames(res?.data ?? []);
      });
    }
  };

  useEffect(() => {
    getAutoPilotNames();
  }, [accountId]);

  const getAutoPilotNameById = (id: string) => {
    const item: any = allAutoPilotNames.find((i: any) => i.id == id);
    return item ? item.name : '-';
  };

  const getApprovalsListing = () => {
    setLoading(true);
    apiAutoPilot.getAutoPilotApprovals(accountId, type, '').then((res: any) => {
      if (res.errors) {
        setLoading(false);
        return;
      }
      const tableData = (res?.data?.auto_pilot_approvals ?? []).map((item: any) => {
        const status = effectiveStatus(item);
        const display = STATUS_DISPLAY[status] ?? { text: status, tone: 'neutral' as LabelTone };
        const decided = ['APPROVED', 'REJECTED'].includes(status);
        return [
          { text: getAutoPilotNameById(item?.autopilot_id) },
          { text: item?.attributes?.task_ids?.length ?? '-' },
          { component: <Label tone={display.tone} text={display.text} /> },
          {
            component:
              status === 'PENDING' && item?.attributes?.expires_at ? (
                <Datetime value={item.attributes.expires_at} />
              ) : decided && item?.reviewer_display_name ? (
                <>{item.reviewer_display_name}</>
              ) : (
                <>-</>
              ),
          },
          { text: item?.reviewer_comments ?? '-' },
          { component: <Datetime value={item?.created_at} /> },
          {
            component:
              status === 'PENDING' && canDecide ? (
                <DsButton tone='primary' size='sm' onClick={() => setReviewing(item)}>
                  Review
                </DsButton>
              ) : (
                <></>
              ),
          },
        ];
      });
      setTableRows(tableData);
      setLoading(false);
    });
  };

  useEffect(() => {
    if (allAutoPilotNames.length) {
      getApprovalsListing();
    }
  }, [allAutoPilotNames]);

  return (
    <>
      <AutoOptimizeRunReviewModal
        open={!!reviewing}
        approval={reviewing}
        automationName={reviewing ? getAutoPilotNameById(reviewing.autopilot_id) : ''}
        accountId={accountId}
        onClose={(decided) => {
          setReviewing(null);
          if (decided) getApprovalsListing();
        }}
      />
      <ListingLayout id='autopilot-approvals-listing-box'>
        <ListingLayout.Toolbar actions={<DownloadButton onClick={() => ({ tableId: tableId })} />}>
          <FilterDropdown
            id='auto-pilot-approvals-filter-account'
            label='Account'
            options={accountOptions}
            value={selectedAccountId}
            isOptionsLoading={isAccountsLoading}
            onSelect={onAccountChange}
          />
        </ListingLayout.Toolbar>
        <ListingLayout.Body>
          <CustomTable
            id={tableId}
            headers={[
              { name: 'Automation', width: '24%' },
              { name: 'Changes', width: '8%' },
              { name: 'Status', width: '10%' },
              { name: 'Expires / Decided by', width: '14%' },
              { name: 'Comment', width: '22%' },
              { name: 'Planned at', width: '12%' },
              { name: '', width: '10%' },
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
