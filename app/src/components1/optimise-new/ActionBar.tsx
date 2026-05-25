import { Box, IconButton } from '@mui/material';
import { useState } from 'react';
import { colors } from 'src/utils/colors';
import ContentCopyOutlinedIcon from '@mui/icons-material/ContentCopyOutlined';
import ConfirmationNumberOutlinedIcon from '@mui/icons-material/ConfirmationNumberOutlined';
import RocketLaunchOutlinedIcon from '@mui/icons-material/RocketLaunchOutlined';
import AutoFixHighOutlinedIcon from '@mui/icons-material/AutoFixHighOutlined';
import NotificationsActiveOutlinedIcon from '@mui/icons-material/NotificationsActiveOutlined';
import CustomTooltip from '@components1/common/CustomTooltip';
import CustomButton from '@components1/common/NewCustomButton';
import AlarmCreationModal from '@components1/cloudaccount/AlarmCreationModal';
import { getNubiIconUrl, useTenantBranding } from '@hooks/useTenantBranding';
import { hasWriteAccess } from '@lib/auth';
import { snackbar } from '@components1/common/snackbarService';
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
          borderTop: `1px solid ${colors.border.secondaryLightest}`,
          backgroundColor: colors.background.tertiaryLightestestest,
          flexShrink: 0,
          px: '16px',
          py: '10px',
          display: 'flex',
          alignItems: 'center',
          justifyContent: 'space-between',
          gap: '8px',
        }}
        data-testid='action-bar'
      >
        {/* Primary actions */}
        <Box sx={{ display: 'flex', alignItems: 'center', gap: '8px', flexWrap: 'wrap' }}>
          {isK8sRightSizing && canWrite && (
            <CustomButton variant='primary' size='xSmall' text='Resolve' onClick={() => onResolve?.(rec)} id='action-bar-resolve' />
          )}
          <CustomButton
            variant='secondary'
            size='xSmall'
            text='Create Ticket'
            startIcon={<ConfirmationNumberOutlinedIcon sx={{ fontSize: '14px !important' }} />}
            onClick={() => onCreateTicket?.(rec)}
            id='action-bar-create-ticket'
            disabled={!!rec.ticket?.ticket_id}
            showTooltip={!!rec.ticket?.ticket_id}
            toolTipTitle={rec.ticket?.ticket_id ? `Ticket already created: ${rec.ticket.ticket_id}` : ''}
          />
          {isK8sRightSizing && (
            <CustomButton
              variant='secondary'
              size='xSmall'
              text='Copy Command'
              startIcon={<ContentCopyOutlinedIcon sx={{ fontSize: '14px !important' }} />}
              onClick={() => onCopyCli?.(rec)}
              id='action-bar-copy-cli'
            />
          )}
          <CustomButton
            variant='secondary'
            size='xSmall'
            text={`Ask ${assistantName}`}
            startIcon={getNubiIconUrl()}
            onClick={() => onAskNubi?.(rec)}
            id='action-bar-ask-nubi'
            sx={{ '& img, & svg': { filter: 'none !important' } }}
          />
        </Box>

        {/* Secondary actions */}
        <Box sx={{ display: 'flex', alignItems: 'center', gap: '4px' }}>
          {isReplicaRightSizing && canWrite && (
            <CustomTooltip title='Setup HPA AutoPilot'>
              <IconButton
                size='small'
                onClick={() => window.open(`/auto-pilot?accountId=${accountId}`, '_blank')}
                data-testid='action-bar-autopilot'
                sx={{ color: colors.text.tertiary, '&:hover': { color: colors.primary } }}
              >
                <AutoFixHighOutlinedIcon sx={{ fontSize: '18px' }} />
              </IconButton>
            </CustomTooltip>
          )}

          {(isPVRightSizing || isAbandonedResource) && canWrite && (
            <CustomTooltip title={isPVRightSizing ? 'Resize Volume' : 'Scale Down Workload'}>
              <IconButton
                size='small'
                onClick={handleNavigateToDetail}
                data-testid='action-bar-detail-nav'
                sx={{ color: colors.text.tertiary, '&:hover': { color: colors.primary } }}
              >
                <RocketLaunchOutlinedIcon sx={{ fontSize: '18px' }} />
              </IconButton>
            </CustomTooltip>
          )}

          {hasAlarmConfig && canWrite && (
            <CustomTooltip title='Create CloudWatch Alarm'>
              <IconButton
                size='small'
                onClick={() => setIsAlarmModalOpen(true)}
                data-testid='action-bar-alarm'
                sx={{ color: colors.text.tertiary, '&:hover': { color: colors.primary } }}
              >
                <NotificationsActiveOutlinedIcon sx={{ fontSize: '18px' }} />
              </IconButton>
            </CustomTooltip>
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
            snackbar.success('CloudWatch alarm created successfully');
          }}
        />
      )}
    </>
  );
};

export default ActionBar;
