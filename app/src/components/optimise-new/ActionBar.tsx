import { Box } from '@mui/material';
import { useState } from 'react';
import { ds } from 'src/utils/colors';
import ContentCopyOutlinedIcon from '@mui/icons-material/ContentCopyOutlined';
import ConfirmationNumberOutlinedIcon from '@mui/icons-material/ConfirmationNumberOutlined';
import AutoFixHighOutlinedIcon from '@mui/icons-material/AutoFixHighOutlined';
import DoNotDisturbOnOutlinedIcon from '@mui/icons-material/DoNotDisturbOnOutlined';
import RestartAltOutlinedIcon from '@mui/icons-material/RestartAltOutlined';
import NotificationsActiveOutlinedIcon from '@mui/icons-material/NotificationsActiveOutlined';
import SafeIcon from '@shared/icons/SafeIcon';
import { Button } from '@ui/Button';
import { toast } from '@ui/Toast';
import AlarmCreationModal from '@components/cloudaccount/AlarmCreationModal';
import { getNubiIconUrl, useTenantBranding } from '@hooks/useTenantBranding';
import { hasCliCommand } from './cliCommands';
import { hasWriteAccess, hasPermission, missingPermissionMessage } from '@lib/auth';
import { safeParseJSON, panelActionBarSx } from './utils';
import { k8sActionFor } from './k8sActions';
import { canStartCloudAction, cloudActionFor } from './cloudActions';

interface ActionBarProps {
  fullRecommendation: any;
  provider?: string;
  /** False when the cluster's agent is not connected; undefined when unknown. */
  agentConnected?: boolean;
  onCreateTicket?: (rec: any) => void;
  onResolve?: (rec: any) => void;
  onCopyCli?: (rec: any) => void;
  onAskNubi?: (rec: any) => void;
  onDismiss?: (rec: any) => void;
}

const ActionBar = ({
  fullRecommendation: rec,
  provider,
  agentConnected,
  onCreateTicket,
  onResolve,
  onCopyCli,
  onAskNubi,
  onDismiss,
}: ActionBarProps) => {
  const { assistantName } = useTenantBranding();
  const [isAlarmModalOpen, setIsAlarmModalOpen] = useState(false);

  if (!rec) return null;

  const category = rec.category || '';
  const ruleName = rec.rule_name || '';
  const accountId = rec.account_id || '';
  const isK8sRightSizing = ruleName === 'pod_right_sizing';
  const isReplicaRightSizing = category === 'RightSizing' && ruleName === 'replica_right_sizing';
  // The other three agent fixes (reclaim / resize a volume, scale down a
  // workload) open their own confirm modal. Each queues an agent task, so the
  // button is disabled, not hidden, while the cluster's agent is down: the
  // reader still sees the action exists and why it is not available.
  const k8sAction = k8sActionFor(rec);
  const cloudAction = cloudActionFor(rec);
  const agentDown = agentConnected === false;
  const recData = safeParseJSON(rec.recommendation);
  const hasAlarmConfig = recData?.alarm_config != null;
  const canWrite = hasWriteAccess(accountId);
  // Creating a ticket needs write access to the recommendation's account OR the
  // tickets:Write custom-role grant (tickets_create → tickets:Write). Without
  // this the button was the one unconditional action in the bar: a read-only
  // viewer could open the modal and only learn they couldn't when the create
  // 403'd. Same gate as the events tables (KubernetesGroupedEventsTable).
  const canCreateTicket = canWrite || hasPermission('tickets', 'Write');
  const isDismissed = rec.status === 'Dismissed';
  // Only offer what the backend legality matrix accepts: dismiss from Open,
  // reactivate from Dismissed. Other statuses (InProgress, Closed, Archive)
  // would just earn an error toast.
  const showDismissAction = canWrite && (isDismissed || !rec.status || rec.status === 'Open');

  return (
    <>
      <Box sx={panelActionBarSx} data-testid='action-bar'>
        {/* Primary actions */}
        <Box sx={{ display: 'flex', alignItems: 'center', gap: ds.space[2], flexWrap: 'wrap' }}>
          {isK8sRightSizing && canWrite && (
            <Button
              tone='primary'
              size='sm'
              icon={<AutoFixHighOutlinedIcon sx={{ color: ds.yellow[400] }} />}
              iconPlacement='start'
              onClick={() => onResolve?.(rec)}
              id='action-bar-resolve'
            >
              Resolve
            </Button>
          )}
          {k8sAction && canWrite && (
            <Button
              tone='primary'
              size='sm'
              icon={<AutoFixHighOutlinedIcon sx={{ color: ds.yellow[400] }} />}
              iconPlacement='start'
              onClick={() => onResolve?.(rec)}
              id='action-bar-k8s-action'
              disabled={agentDown}
              tooltip={agentDown ? 'Agent not connected — reconnect the cluster agent to apply from here' : undefined}
            >
              {k8sAction.label}
            </Button>
          )}
          {cloudAction && canStartCloudAction(rec) && canWrite && (
            <Button
              tone='primary'
              size='sm'
              icon={<AutoFixHighOutlinedIcon sx={{ color: ds.yellow[400] }} />}
              iconPlacement='start'
              onClick={() => onResolve?.(rec)}
              id='action-bar-cloud-action'
            >
              {cloudAction.label}
            </Button>
          )}
          <Button
            tone='secondary'
            size='sm'
            icon={<ConfirmationNumberOutlinedIcon />}
            iconPlacement='start'
            onClick={() => onCreateTicket?.(rec)}
            id='action-bar-create-ticket'
            disabled={!!rec.ticket?.ticket_id || !canCreateTicket}
            // Disabled rather than hidden, matching the events tables: a
            // read-only viewer still needs to see that a ticket exists (and
            // which one), and why the action isn't available to them.
            tooltip={
              rec.ticket?.ticket_id
                ? `Ticket already created: ${rec.ticket.ticket_id}`
                : !canCreateTicket
                ? missingPermissionMessage('tickets:Write')
                : undefined
            }
          >
            Create Ticket
          </Button>
          {hasCliCommand(rec) && (
            <Button
              tone='secondary'
              size='sm'
              icon={<ContentCopyOutlinedIcon />}
              iconPlacement='start'
              onClick={() => onCopyCli?.(rec)}
              id='action-bar-copy-cli'
            >
              Copy Command
            </Button>
          )}
          {hasAlarmConfig && canWrite && (
            <Button
              tone='secondary'
              size='xs'
              icon={<NotificationsActiveOutlinedIcon />}
              iconPlacement='start'
              onClick={() => setIsAlarmModalOpen(true)}
              id='action-bar-alarm'
            >
              Create Alarm
            </Button>
          )}
          <Button
            tone='secondary'
            size='sm'
            icon={<SafeIcon src={getNubiIconUrl()} alt={`Ask ${assistantName}`} width={14} height={14} />}
            iconPlacement='start'
            onClick={() => onAskNubi?.(rec)}
            id='action-bar-ask-nubi'
          >
            Ask {assistantName}
          </Button>
          {/* Ghost tone keeps it subordinate to the actions above; it stays in this
              row rather than the icon cluster so the label isn't clipped by the
              floating chat button anchored over the drawer's bottom-right corner. */}
          {showDismissAction && (
            <Button
              tone='ghost'
              size='sm'
              icon={isDismissed ? <RestartAltOutlinedIcon /> : <DoNotDisturbOnOutlinedIcon />}
              iconPlacement='start'
              tooltip={isDismissed ? 'Return this recommendation to the open list' : 'Suppress it permanently or until a chosen date'}
              onClick={() => onDismiss?.(rec)}
              id='action-bar-dismiss'
            >
              {isDismissed ? 'Reactivate' : 'Dismiss / Snooze'}
            </Button>
          )}
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
              onClick={() => window.open(`/automation?accountId=${accountId}`, '_blank')}
              id='action-bar-autopilot'
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
          provider={provider}
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
