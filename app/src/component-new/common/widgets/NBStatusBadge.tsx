import React, { useState } from 'react';
import Box from '@mui/material/Box';
import CircularProgress from '@mui/material/CircularProgress';
import Typography from '@mui/material/Typography';
import { Button } from '@components1/ds/Button';
import { Modal } from '@components1/ds/Modal';
import Tooltip from '@components1/ds/Tooltip';
import AccessTimeIcon from '@mui/icons-material/AccessTime';
import apiTriage from 'src/api1/triage';
import { Label, type LabelTone } from '@components1/ds/Label';
import { DropdownMenu, type DropdownMenuItem } from '@components1/ds/DropdownMenu';
import { ds } from '@utils/colors';

interface NBStatusBadgeProps {
  eventId: string;
  currentStatus: string;
  snoozedUntil?: string;
  onStatusChange?: (newStatus: string) => void;
  onCreateTicket?: () => void;
  disabled?: boolean;
  disableTooltip?: boolean;
}

// Status display configuration
const STATUS_CONFIG: Record<string, { label: string; variant: string }> = {
  OPEN: { label: 'Open', variant: 'blue' },
  ACTION_REQUIRED: { label: 'Action Required', variant: 'red' },
  ACKNOWLEDGED: { label: 'Acknowledged', variant: 'purple' },
  INVESTIGATING: { label: 'Investigating', variant: 'yellow' },
  SNOOZED: { label: 'Snoozed', variant: 'grey' },
  SUPPRESSED: { label: 'Suppressed', variant: 'grey' },
  DROPPED: { label: 'Dropped', variant: 'grey' },
  DUPLICATE: { label: 'Duplicate', variant: 'grey' },
  RESOLVED: { label: 'Resolved', variant: 'green' },
};

// Allowed transitions from each status (simplified workflow)
const ALLOWED_TRANSITIONS: Record<string, string[]> = {
  OPEN: ['ACTION_REQUIRED', 'SNOOZED', 'RESOLVED'],
  ACTION_REQUIRED: ['RESOLVED', 'SNOOZED', 'OPEN'],
  SNOOZED: ['OPEN', 'ACTION_REQUIRED', 'RESOLVED'],
  SUPPRESSED: ['OPEN', 'ACTION_REQUIRED', 'RESOLVED'],
  DROPPED: ['OPEN'],
  DUPLICATE: ['OPEN'],
  RESOLVED: ['OPEN', 'ACTION_REQUIRED'],
  // Deprecated statuses - allow transition to new workflow
  ACKNOWLEDGED: ['OPEN', 'ACTION_REQUIRED', 'RESOLVED'],
  INVESTIGATING: ['OPEN', 'ACTION_REQUIRED', 'RESOLVED'],
};

// Color configuration for each variant — used for the menu-item dots and the
// loading-state placeholder.
const VARIANT_COLORS: Record<string, { bg: string; text: string }> = {
  blue: { bg: 'var(--ds-blue-100)', text: 'var(--ds-blue-700)' },
  green: { bg: 'var(--ds-green-100)', text: 'var(--ds-green-700)' },
  grey: { bg: 'var(--ds-gray-100)', text: 'var(--ds-gray-600)' },
  yellow: { bg: 'var(--ds-amber-100)', text: 'var(--ds-amber-600)' },
  purple: { bg: 'var(--ds-purple-100)', text: 'var(--ds-purple-600)' },
  red: { bg: 'var(--ds-red-100)', text: 'var(--ds-red-700)' },
};

// Map the NBStatus variant to a DS V2 tone consumed by the Label badge.
// Purple has no DS V2 status tone; it falls back to neutral.
const VARIANT_TO_TONE: Record<string, LabelTone> = {
  blue: 'info',
  green: 'success',
  grey: 'neutral',
  yellow: 'warning',
  red: 'critical',
  purple: 'neutral',
};

// Snooze duration options
const SNOOZE_OPTIONS = [
  { label: '1 hour', hours: 1 },
  { label: '4 hours', hours: 4 },
  { label: '1 day', hours: 24 },
  { label: '1 week', hours: 168 },
];

const NBStatusBadge: React.FC<NBStatusBadgeProps> = ({
  eventId,
  currentStatus,
  snoozedUntil,
  onStatusChange,
  onCreateTicket,
  disabled = false,
  disableTooltip = false,
}) => {
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [snoozeDialogOpen, setSnoozeDialogOpen] = useState(false);
  const [ticketPromptOpen, setTicketPromptOpen] = useState(false);
  const [pendingStatusChange, setPendingStatusChange] = useState<string | null>(null);

  const handleStatusChange = async (newStatus: string, snoozedUntil?: string) => {
    setLoading(true);
    setError(null);

    try {
      await apiTriage.updateNBStatus({
        event_id: eventId,
        nb_status: newStatus as
          | 'OPEN'
          | 'ACKNOWLEDGED'
          | 'INVESTIGATING'
          | 'ACTION_REQUIRED'
          | 'SNOOZED'
          | 'SUPPRESSED'
          | 'DROPPED'
          | 'DUPLICATE'
          | 'RESOLVED',
        snoozed_until: snoozedUntil,
      });

      // For ACTION_REQUIRED, show ticket prompt BEFORE refreshing data
      // This prevents the component from being unmounted during the prompt
      if (newStatus === 'ACTION_REQUIRED' && onCreateTicket) {
        setPendingStatusChange(newStatus);
        setTicketPromptOpen(true);
        // Don't call onStatusChange yet - wait for dialog interaction
      } else {
        onStatusChange?.(newStatus);
      }
    } catch (err) {
      console.error('Failed to update status:', err);
      setError('Failed to update status');
    } finally {
      setLoading(false);
    }
  };

  const handleMenuItemClick = (status: string) => {
    if (status === 'SNOOZED') {
      setSnoozeDialogOpen(true);
    } else {
      handleStatusChange(status);
    }
  };

  const handleSnoozeSelect = (hours: number) => {
    const snoozedUntil = new Date(Date.now() + hours * 60 * 60 * 1000).toISOString();
    setSnoozeDialogOpen(false);
    handleStatusChange('SNOOZED', snoozedUntil);
  };

  const statusConfig = STATUS_CONFIG[currentStatus] || { label: currentStatus || '-', variant: 'grey' };
  const variantColors = VARIANT_COLORS[statusConfig.variant] || VARIANT_COLORS.grey;
  const badgeTone = VARIANT_TO_TONE[statusConfig.variant] || 'neutral';
  const allowedTransitions = ALLOWED_TRANSITIONS[currentStatus] || [];
  const showArrow = !disabled && allowedTransitions.length > 0;

  const snoozeTooltip =
    currentStatus === 'SNOOZED' && snoozedUntil
      ? `Until ${new Date(snoozedUntil).toLocaleString(undefined, { month: 'short', day: 'numeric', hour: 'numeric', minute: '2-digit' })}`
      : '';

  // Build DropdownMenu items from allowed status transitions
  const menuItems: DropdownMenuItem[] =
    allowedTransitions.length > 0
      ? allowedTransitions.map((status) => {
          const config = STATUS_CONFIG[status] || { label: status, variant: 'grey' };
          const colors = VARIANT_COLORS[config.variant] || VARIANT_COLORS.grey;
          return {
            id: status,
            label: (
              <Box sx={{ display: 'flex', alignItems: 'center', gap: 'var(--ds-space-2)' }}>
                <Box
                  sx={{
                    width: ds.space[2],
                    height: ds.space[2],
                    borderRadius: 'var(--ds-radius-pill)',
                    backgroundColor: colors.text,
                    flexShrink: 0,
                  }}
                />
                {config.label}
              </Box>
            ),
            onSelect: () => handleMenuItemClick(status),
          };
        })
      : [{ id: 'no-transitions', label: 'No transitions available', onSelect: () => {}, disabled: true }];

  const badgeContent = loading ? (
    <Box
      sx={{
        display: 'inline-flex',
        alignItems: 'center',
        justifyContent: 'center',
        width: ds.space.mul(0, 45),
        height: ds.space.mul(0, 11),
        backgroundColor: variantColors.bg,
        borderRadius: 'var(--ds-radius-sm)',
      }}
    >
      <CircularProgress size={12} sx={{ color: variantColors.text }} />
    </Box>
  ) : (
    <Label text={statusConfig.label} tone={badgeTone} size='sm' textTransform='none' showDropdownArrow={showArrow} />
  );

  // The trigger Box is shared between the clickable (DropdownMenu) and
  // non-clickable variants. stopPropagation prevents table-row click handlers
  // from firing when the badge is clicked.
  const triggerBox = (
    <Box
      onClick={(e: React.MouseEvent) => e.stopPropagation()}
      sx={{
        display: 'inline-flex',
        cursor: disabled || loading ? 'default' : 'pointer',
        opacity: disabled ? 0.6 : 1,
        transition: 'all 0.2s ease',
        '&:hover': disabled || loading ? {} : { opacity: 0.8 },
      }}
    >
      <Tooltip title={snoozeTooltip} arrow disableHoverListener={disableTooltip || !snoozeTooltip}>
        <Box component='span' sx={{ display: 'inline-flex' }}>
          {badgeContent}
        </Box>
      </Tooltip>
    </Box>
  );

  return (
    <>
      {showArrow && !loading ? (
        <DropdownMenu trigger={triggerBox} items={menuItems} side='bottom' align='start' minWidth={ds.space.mul(0, 70)} />
      ) : (
        triggerBox
      )}

      {/* Snooze Duration Dialog */}
      <Modal
        open={snoozeDialogOpen}
        handleClose={() => setSnoozeDialogOpen(false)}
        title='Snooze for how long?'
        width='xs'
        actionButtons={
          <Button tone='ghost' onClick={() => setSnoozeDialogOpen(false)}>
            Cancel
          </Button>
        }
      >
        <Box sx={{ display: 'flex', flexDirection: 'column', gap: 'var(--ds-space-2)' }}>
          {SNOOZE_OPTIONS.map((option) => (
            <Button key={option.hours} tone='secondary' fullWidth icon={<AccessTimeIcon />} onClick={() => handleSnoozeSelect(option.hours)}>
              {option.label}
            </Button>
          ))}
        </Box>
      </Modal>

      {/* Ticket Creation Prompt */}
      <Modal
        open={ticketPromptOpen}
        handleClose={() => {
          setTicketPromptOpen(false);
          if (pendingStatusChange) {
            onStatusChange?.(pendingStatusChange);
            setPendingStatusChange(null);
          }
        }}
        title='Create a Ticket?'
        width='xs'
        confirmText='Create Ticket'
        onConfirm={() => {
          setTicketPromptOpen(false);
          onCreateTicket?.();
          if (pendingStatusChange) {
            onStatusChange?.(pendingStatusChange);
            setPendingStatusChange(null);
          }
        }}
      >
        <Typography variant='body2' color='text.secondary'>
          This issue requires action. Would you like to create a ticket to track it?
        </Typography>
      </Modal>

      {error && (
        <Typography sx={{ color: 'var(--ds-red-500)', fontSize: 'var(--ds-text-caption)', marginTop: 'var(--ds-space-1)' }}>{error}</Typography>
      )}
    </>
  );
};

export default NBStatusBadge;
