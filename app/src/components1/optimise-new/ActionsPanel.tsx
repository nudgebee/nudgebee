import { Box, Typography, Button, Divider, Chip } from '@mui/material';
import { useState } from 'react';
import { colors } from 'src/utils/colors';
import ContentCopyOutlinedIcon from '@mui/icons-material/ContentCopyOutlined';
import VisibilityOffOutlinedIcon from '@mui/icons-material/VisibilityOffOutlined';
import ConfirmationNumberOutlinedIcon from '@mui/icons-material/ConfirmationNumberOutlined';
import RocketLaunchOutlinedIcon from '@mui/icons-material/RocketLaunchOutlined';
import AutoFixHighOutlinedIcon from '@mui/icons-material/AutoFixHighOutlined';
import OpenInNewIcon from '@mui/icons-material/OpenInNew';
import NotificationsActiveOutlinedIcon from '@mui/icons-material/NotificationsActiveOutlined';
import CustomTicketLink from '@components1/common/CustomTicketLink';
import CustomPRLink from '@components1/common/CustomPRLink';
import CustomTooltip from '@components1/common/CustomTooltip';
import AlarmCreationModal from '@components1/cloudaccount/AlarmCreationModal';
import { hasWriteAccess } from '@lib/auth';
import { snackbar } from '@components1/common/snackbarService';
import recommendationApi from '@api1/recommendation';
import { safeParseJSON } from './utils';

interface ActionsPanelProps {
  fullRecommendation: any;
  onCreateTicket?: (rec: any) => void;
  onResolve?: (rec: any) => void;
  onCopyCli?: (rec: any) => void;
}

const ActionsPanel = ({ fullRecommendation: rec, onCreateTicket, onResolve, onCopyCli }: ActionsPanelProps) => {
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
  const details = recommendationApi.getRecommendationDetails(category, ruleName);

  // Open CLI command modal
  const handleCopyCli = () => {
    onCopyCli?.(rec);
  };

  // Navigate to cluster detail page for resolve flow (non-pod-rightsizing cases)
  const handleResolve = () => {
    const detailUrl = `/kubernetes/details/${accountId}#optimize/right-sizing`;
    window.open(detailUrl, '_blank');
  };

  return (
    <Box sx={{ p: '16px 20px', display: 'flex', flexDirection: 'column', gap: '10px' }}>
      {/* ─── Linked Ticket/PR ─── */}
      {(rec.ticket || rec.resolution) && (
        <Box sx={{ mb: '4px' }}>
          <Typography sx={{ fontSize: '13px', fontWeight: 600, color: colors.text.secondary, mb: '8px' }}>Linked Items</Typography>
          <Box sx={{ display: 'flex', flexDirection: 'column', gap: '6px' }}>
            {rec.ticket && (
              <Box
                sx={{
                  p: '8px 12px',
                  borderRadius: '8px',
                  backgroundColor: colors.background.tertiaryLightestestest,
                  border: `1px solid ${colors.border.secondaryLight}`,
                  display: 'flex',
                  alignItems: 'center',
                  gap: '8px',
                }}
              >
                <ConfirmationNumberOutlinedIcon sx={{ fontSize: '16px', color: colors.primary }} />
                <CustomTicketLink ticketURL={rec.ticket?.url} ticketID={rec.ticket?.ticket_id} />
              </Box>
            )}
            {rec.resolution?.type_reference_id && (
              <Box
                sx={{
                  p: '8px 12px',
                  borderRadius: '8px',
                  backgroundColor: colors.background.costBlock,
                  border: '1px solid #BBF7D0',
                  display: 'flex',
                  alignItems: 'center',
                  gap: '8px',
                }}
              >
                <CustomPRLink prURL={rec.resolution.type_reference_id} statusMessage={rec.resolution.status_message} />
              </Box>
            )}
          </Box>
          <Divider sx={{ my: '12px' }} />
        </Box>
      )}

      {/* ─── Create Ticket ─── */}
      <Typography sx={{ fontSize: '13px', fontWeight: 600, color: colors.text.secondary, mb: '2px' }}>Actions</Typography>

      <CustomTooltip title={rec.ticket?.ticket_id ? `Ticket already created: ${rec.ticket.ticket_id}` : ''}>
        <Box component='span' sx={{ display: 'block' }}>
          <ActionButton
            icon={<ConfirmationNumberOutlinedIcon />}
            label='Create Ticket'
            description={rec.ticket?.ticket_id ? `Ticket already created: ${rec.ticket.ticket_id}` : 'Create a Jira, GitHub, or GitLab issue'}
            onClick={() => onCreateTicket?.(rec)}
            testId='action-create-ticket'
            disabled={!!rec.ticket?.ticket_id}
          />
        </Box>
      </CustomTooltip>

      {/* ─── K8s Pod RightSizing specific actions ─── */}
      {isK8sRightSizing && canWrite && (
        <ActionButton
          icon={<RocketLaunchOutlinedIcon />}
          label='Resolve / Deploy Fix'
          description='Deploy fix, create PR, or schedule auto-optimization'
          onClick={() => onResolve?.(rec)}
          testId='action-resolve-deploy'
        />
      )}

      {/* ─── Replica RightSizing specific actions ─── */}
      {isReplicaRightSizing && canWrite && (
        <ActionButton
          icon={<AutoFixHighOutlinedIcon />}
          label='Setup HPA AutoPilot'
          description='Configure horizontal pod autoscaling optimization'
          onClick={() => {
            window.open(`/auto-pilot?accountId=${accountId}`, '_blank');
          }}
          testId='action-replica-autopilot'
          endIcon={<OpenInNewIcon sx={{ fontSize: '14px' }} />}
        />
      )}

      {/* ─── PV/PVC specific actions ─── */}
      {isPVRightSizing && canWrite && (
        <ActionButton
          icon={<RocketLaunchOutlinedIcon />}
          label={ruleName === 'unused_pvc' ? 'View Volume Details' : 'Resize Volume'}
          description={ruleName === 'unused_pvc' ? 'Review and manage this unused volume' : 'View resize options on the detail page'}
          onClick={handleResolve}
          testId='action-pv-resize'
          endIcon={<OpenInNewIcon sx={{ fontSize: '14px' }} />}
        />
      )}

      {/* ─── Abandoned resource specific actions ─── */}
      {isAbandonedResource && canWrite && (
        <ActionButton
          icon={<RocketLaunchOutlinedIcon />}
          label='Scale Down Workload'
          description='View scale-down options on the detail page (requires confirmation)'
          onClick={handleResolve}
          testId='action-scale-down'
          endIcon={<OpenInNewIcon sx={{ fontSize: '14px' }} />}
        />
      )}

      {/* ─── Create Alarm (for recommendations with alarm_config) ─── */}
      {hasAlarmConfig && canWrite && (
        <ActionButton
          icon={<NotificationsActiveOutlinedIcon />}
          label='Create CloudWatch Alarm'
          description='Create a CloudWatch alarm based on this recommendation'
          onClick={() => setIsAlarmModalOpen(true)}
          testId='action-create-alarm'
        />
      )}

      <Divider sx={{ my: '4px' }} />

      {/* ─── Copy kubectl command (only for pod right sizing) ─── */}
      {isK8sRightSizing && (
        <ActionButton
          icon={<ContentCopyOutlinedIcon />}
          label='Copy kubectl Command'
          description='Copy kubectl set resources command'
          onClick={handleCopyCli}
          testId='action-copy-cli'
          variant='secondary'
        />
      )}

      <ActionButton
        icon={<VisibilityOffOutlinedIcon />}
        label='Dismiss'
        description='Dismiss this recommendation'
        onClick={() => {
          snackbar.info('Dismiss is not yet implemented');
        }}
        testId='action-dismiss'
        variant='danger'
      />

      {/* ─── Category context info ─── */}
      {details?.title && (
        <>
          <Divider sx={{ my: '8px' }} />
          <Box
            sx={{
              p: '10px',
              borderRadius: '8px',
              backgroundColor: colors.background.tertiaryLightestestest,
              border: `1px solid ${colors.border.secondaryLight}`,
            }}
          >
            <Typography sx={{ fontSize: '12px', fontWeight: 600, color: colors.text.secondary, mb: '4px' }}>{details.title}</Typography>
            {details.description && (
              <Typography sx={{ fontSize: '11px', color: colors.text.tertiary, lineHeight: 1.5 }}>
                {details.description.substring(0, 150)}
                {details.description.length > 150 ? '...' : ''}
              </Typography>
            )}
          </Box>
        </>
      )}

      {/* Status chip */}
      <Box sx={{ mt: '8px', display: 'flex', gap: '6px', flexWrap: 'wrap' }}>
        <Chip
          label={`Status: ${rec.status || 'Open'}`}
          size='small'
          sx={{
            fontSize: '11px',
            height: '22px',
            backgroundColor: rec.status === 'Open' ? colors.background.primaryLightest : colors.background.tertiaryLight,
            color: rec.status === 'Open' ? colors.primary : colors.text.tertiary,
          }}
        />
        {rec.hasAutopilotConfigured && (
          <Chip
            label='Auto-Pilot Active'
            size='small'
            sx={{ fontSize: '11px', height: '22px', backgroundColor: colors.background.costBlock, color: '#16A34A' }}
          />
        )}
      </Box>

      {/* ─── Alarm Creation Modal ─── */}
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
    </Box>
  );
};

// Reusable action button component
const ActionButton = ({
  icon,
  label,
  description,
  onClick,
  testId,
  variant = 'primary',
  endIcon,
  disabled = false,
}: {
  icon: React.ReactNode;
  label: string;
  description: string;
  onClick: () => void;
  testId: string;
  variant?: 'primary' | 'secondary' | 'danger';
  endIcon?: React.ReactNode;
  disabled?: boolean;
}) => {
  const hoverColors = {
    primary: { borderColor: colors.primary, color: colors.primary, backgroundColor: colors.background.primaryLightest },
    secondary: { borderColor: '#16A34A', color: '#16A34A', backgroundColor: colors.background.costBlock },
    danger: { borderColor: '#DC2626', color: '#DC2626', backgroundColor: colors.background.accordionSummay },
  };

  return (
    <Button
      variant='outlined'
      startIcon={icon}
      endIcon={endIcon}
      data-testid={testId}
      fullWidth
      onClick={onClick}
      disabled={disabled}
      sx={{
        textTransform: 'none',
        justifyContent: 'flex-start',
        borderColor: colors.border.secondaryLight,
        color: colors.text.secondary,
        fontSize: '13px',
        py: '8px',
        px: '12px',
        borderRadius: '8px',
        display: 'flex',
        alignItems: 'center',
        '&:hover': hoverColors[variant],
        '& .MuiButton-endIcon': { marginLeft: 'auto' },
      }}
    >
      <Box sx={{ display: 'flex', flexDirection: 'column', alignItems: 'flex-start', textAlign: 'left' }}>
        <Typography sx={{ fontSize: '13px', fontWeight: 500, lineHeight: 1.3 }}>{label}</Typography>
        <Typography sx={{ fontSize: '11px', color: colors.text.tertiary, lineHeight: 1.3 }}>{description}</Typography>
      </Box>
    </Button>
  );
};

export default ActionsPanel;
