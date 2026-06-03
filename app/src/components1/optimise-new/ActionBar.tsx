import { Box } from '@mui/material';
import { useState } from 'react';
import { ds } from 'src/utils/colors';
import ContentCopyOutlinedIcon from '@mui/icons-material/ContentCopyOutlined';
import ConfirmationNumberOutlinedIcon from '@mui/icons-material/ConfirmationNumberOutlined';
import RocketLaunchOutlinedIcon from '@mui/icons-material/RocketLaunchOutlined';
import AutoFixHighOutlinedIcon from '@mui/icons-material/AutoFixHighOutlined';
import NotificationsActiveOutlinedIcon from '@mui/icons-material/NotificationsActiveOutlined';
import SafeIcon from '@common/SafeIcon';
import { Button } from '@components1/ds/Button';
import { toast } from '@components1/ds/Toast';
import AlarmCreationModal from '@components1/cloudaccount/AlarmCreationModal';
import { getNubiIconUrl, useTenantBranding } from '@hooks/useTenantBranding';
import { hasWriteAccess } from '@lib/auth';
import { safeParseJSON } from './utils';

interface ActionBarProps {
  fullRecommendation: any;
  onCreateTicket?: (rec: any) => void;
  onResolve?: (rec: any) => void;
  onCopyCli?: (rec: any) => void;
  onAskNubi?: (rec: any) => void;
}

const ActionBar = ({ fullRecommendation: rec, onCreateTicket, onResolve, onCopyCli, onAskNubi }: ActionBarProps) => {
  const { assistantName } = useTenantBranding();
  const [isAlarmModalOpen, setIsAlarmModalOpen] = useState(false);

  if (!rec) return null;

  const category = rec.category || '';
  const ruleName = rec.rule_name || '';
  const accountId = rec.account_id || '';
  const isK8sRightSizing = category === 'RightSizing' && ruleName === 'pod_right_sizing';
  const isReplicaRightSizing = category === 'RightSizing' && ruleName === 'replica_right_sizing';
  const isPVRightSizing = category === 'RightSizing' && (ruleName === 'pv_rightsize' || ruleName === 'unused_pvc');
  const isAbandonedResource = category === 'RightSizing' && ruleName === 'abandoned_resource';
  const recData = safeParseJSON(rec.recommendation);
  const hasAlarmConfig = recData?.alarm_config != null;
  const canWrite = hasWriteAccess(accountId);

  const handleNavigateToDetail = () => {
    const detailUrl = `/kubernetes/details/${accountId}#optimize/right-sizing`;
    window.open(detailUrl, '_blank');
  };

  return (
    <>
      <Box
        sx={{
          borderTop: `1px solid ${ds.gray[200]}`,
          backgroundColor: ds.gray[100],
          flexShrink: 0,
          px: ds.space[4],
          py: '10px',
          display: 'flex',
          alignItems: 'center',
          justifyContent: 'space-between',
          gap: ds.space[2],
        }}
        data-testid='action-bar'
      >
        {/* Primary actions */}
        <Box sx={{ display: 'flex', alignItems: 'center', gap: ds.space[2], flexWrap: 'wrap' }}>
          {isK8sRightSizing && canWrite && (
            <Button tone='primary' size='xs' onClick={() => onResolve?.(rec)} id='action-bar-resolve'>
              Resolve
            </Button>
          )}
          <Button
            tone='secondary'
            size='xs'
            icon={<ConfirmationNumberOutlinedIcon />}
            iconPlacement='start'
            onClick={() => onCreateTicket?.(rec)}
            id='action-bar-create-ticket'
            disabled={!!rec.ticket?.ticket_id}
            tooltip={rec.ticket?.ticket_id ? `Ticket already created: ${rec.ticket.ticket_id}` : undefined}
          >
            Create Ticket
          </Button>
          {isK8sRightSizing && (
            <Button
              tone='secondary'
              size='xs'
              icon={<ContentCopyOutlinedIcon />}
              iconPlacement='start'
              onClick={() => onCopyCli?.(rec)}
              id='action-bar-copy-cli'
            >
              Copy Command
            </Button>
          )}
          <Button
            tone='secondary'
            size='xs'
            icon={<SafeIcon src={getNubiIconUrl()} alt={`Ask ${assistantName}`} width={14} height={14} />}
            iconPlacement='start'
            onClick={() => onAskNubi?.(rec)}
            id='action-bar-ask-nubi'
          >
            Ask {assistantName}
          </Button>
        </Box>

        {/* Secondary actions */}
        <Box sx={{ display: 'flex', alignItems: 'center', gap: ds.space[1] }}>
          {isReplicaRightSizing && canWrite && (
            <Button
              tone='ghost'
              composition='icon-only'
              size='sm'
              icon={<AutoFixHighOutlinedIcon />}
              tooltip='Setup HPA AutoPilot'
              aria-label='Setup HPA AutoPilot'
              onClick={() => window.open(`/auto-pilot?accountId=${accountId}`, '_blank')}
              id='action-bar-autopilot'
            />
          )}

          {(isPVRightSizing || isAbandonedResource) && canWrite && (
            <Button
              tone='ghost'
              composition='icon-only'
              size='sm'
              icon={<RocketLaunchOutlinedIcon />}
              tooltip={isPVRightSizing ? 'Resize Volume' : 'Scale Down Workload'}
              aria-label={isPVRightSizing ? 'Resize Volume' : 'Scale Down Workload'}
              onClick={handleNavigateToDetail}
              id='action-bar-detail-nav'
            />
          )}

          {hasAlarmConfig && canWrite && (
            <Button
              tone='ghost'
              composition='icon-only'
              size='sm'
              icon={<NotificationsActiveOutlinedIcon />}
              tooltip='Create CloudWatch Alarm'
              aria-label='Create CloudWatch Alarm'
              onClick={() => setIsAlarmModalOpen(true)}
              id='action-bar-alarm'
            />
          )}
        </Box>
      </Box>

      {hasAlarmConfig && (
        <AlarmCreationModal
          open={isAlarmModalOpen}
          onClose={() => setIsAlarmModalOpen(false)}
          recommendation={rec}
          accountId={accountId}
          onSuccess={() => {
            setIsAlarmModalOpen(false);
            toast.success('CloudWatch alarm created successfully');
          }}
        />
      )}
    </>
  );
};

export default ActionBar;
