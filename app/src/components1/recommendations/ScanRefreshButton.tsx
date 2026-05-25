import { useEffect, useState } from 'react';
import { Box } from '@mui/material';
import SyncIcon from '@mui/icons-material/Sync';
import recommendationApi from '@api1/recommendation';
import { useData } from '@context/DataContext';
import { hasWriteAccess } from '@lib/auth';
import { ds } from 'src/utils/colors';
import { Button as DsButton } from '@components1/ds/Button';
import { toast as snackbar } from '@components1/ds/Toast';
import Datetime from '@common-new/format/Datetime';

interface ScheduleJob {
  runnable_params?: { action_func_name?: string };
  state?: { last_exec_time_sec?: number };
}

interface ScanRefreshButtonProps {
  /** Account id passed to createRecommendationJob and used to gate by write access. */
  accountId: string | undefined | null;
  /** Server-side job name (e.g. 'krr_scan', 'popeye_scan'). */
  jobName: string;
  /** Used for the button's `id`, `data-testid`, and the keyframe animation name. */
  idPrefix: string;
}

export function ScanRefreshButton({ accountId, jobName, idPrefix }: ScanRefreshButtonProps) {
  const { selectedCluster } = useData();
  const [refreshTime, setRefreshTime] = useState<ScheduleJob>({});
  const [isRefreshLoading, setIsRefreshLoading] = useState(false);

  useEffect(() => {
    let job: ScheduleJob = {};
    for (const j of (selectedCluster?.agent?.connection_status?.schedule_jobs ?? []) as ScheduleJob[]) {
      if (j?.runnable_params?.action_func_name === jobName) {
        job = j;
      }
    }
    setRefreshTime(job);
  }, [selectedCluster, jobName]);

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
      {refreshTime?.state?.last_exec_time_sec != null && (
        <Datetime
          value={new Date(refreshTime.state.last_exec_time_sec * 1000)}
          sx={{ fontSize: ds.text.caption, color: ds.gray[500] }}
          sxSuffix={{ fontSize: ds.text.caption, color: ds.gray[500] }}
        />
      )}
    </Box>
  );
}

export default ScanRefreshButton;
