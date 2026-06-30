import { useState } from 'react';
import { Box } from '@mui/material';
import SyncIcon from '@mui/icons-material/Sync';
import recommendationApi from '@api1/recommendation';
import { hasWriteAccess } from '@lib/auth';
import { ds } from 'src/utils/colors';
import { Button as DsButton } from '@ui/Button';
import { toast as snackbar } from '@ui/Toast';
import Datetime from '@shared/format/Datetime';

interface ScanRefreshButtonProps {
  /** Account id passed to createRecommendationJob and used to gate by write access. */
  accountId: string | undefined | null;
  /** Server-side job name (e.g. 'krr_scan', 'popeye_scan'). */
  jobName: string;
  /** Used for the button's `id`, `data-testid`, and the keyframe animation name. */
  idPrefix: string;
  /**
   * The real "last refreshed" time — the most recent `updated_at` across the
   * loaded recommendations. Sourced from the recommendation data itself (see
   * latestUpdatedAt) so it stays accurate for every scanner, including the
   * ml-k8s-server / recommendation-service ones that never wrote the legacy
   * schedule_jobs.last_exec_time_sec field this button used to read.
   */
  lastRefreshed?: string | number | Date | null;
}

export function ScanRefreshButton({ accountId, jobName, idPrefix, lastRefreshed }: ScanRefreshButtonProps) {
  const [isRefreshLoading, setIsRefreshLoading] = useState(false);

  if (!hasWriteAccess(accountId ?? '')) return null;

  const triggerRecommendationJob = () => {
    if (!accountId) return;
    setIsRefreshLoading(true);
    recommendationApi
      .createRecommendationJob(accountId, jobName)
      .then(() => {
        snackbar.success('Scan triggered. New data will appear shortly.');
      })
      .catch(() => {
        snackbar.error('Failed to trigger scan. Please try again.');
      })
      .finally(() => {
        setIsRefreshLoading(false);
      });
  };

  const spinName = `${idPrefix}-scan-spin`;
  const buttonId = `${idPrefix}-trigger-scan`;

  return (
    <Box sx={{ display: 'inline-flex', alignItems: 'center', gap: ds.space[2] }}>
      <DsButton
        tone='secondary'
        size='sm'
        icon={
          <SyncIcon
            sx={{
              animation: isRefreshLoading ? `${spinName} 2s linear infinite` : 'none',
              [`@keyframes ${spinName}`]: {
                '0%': { transform: 'rotate(0deg)' },
                '100%': { transform: 'rotate(360deg)' },
              },
            }}
          />
        }
        iconPlacement='start'
        onClick={triggerRecommendationJob}
        disabled={isRefreshLoading}
        id={buttonId}
        data-testid={buttonId}
      >
        Refresh
      </DsButton>
      {lastRefreshed != null && lastRefreshed !== '' && (
        <Datetime
          value={lastRefreshed}
          prefix='Refreshed '
          sx={{ fontSize: ds.text.caption, color: ds.gray[500] }}
          sxPrefix={{ fontSize: ds.text.caption, color: ds.gray[500] }}
          sxSuffix={{ fontSize: ds.text.caption, color: ds.gray[500] }}
        />
      )}
    </Box>
  );
}

export default ScanRefreshButton;
