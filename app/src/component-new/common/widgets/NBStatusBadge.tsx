import React, { useState } from 'react';
import Box from '@mui/material/Box';
import Menu from '@mui/material/Menu';
import MenuItem from '@mui/material/MenuItem';
import CircularProgress from '@mui/material/CircularProgress';
import Typography from '@mui/material/Typography';
import Dialog from '@mui/material/Dialog';
import DialogTitle from '@mui/material/DialogTitle';
import DialogContent from '@mui/material/DialogContent';
import DialogActions from '@mui/material/DialogActions';
import { Button } from '@components1/ds/Button';
import Tooltip from '@components1/ds/Tooltip';
import AccessTimeIcon from '@mui/icons-material/AccessTime';
import apiTriage from 'src/api1/triage';
import { colors as themeColors } from 'src/utils/colors';
import { Label, type LabelTone } from '@components1/ds/Label';

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

// Color configuration for each variant — retained for the menu-item dots and the
// loading-state placeholder, which still need raw bg/text values.
const VARIANT_COLORS: Record<string, { bg: string; text: string }> = {
  blue: { bg: 'var(--nb-status-blue-bg)', text: 'var(--nb-status-blue-text)' },
  green: { bg: 'var(--nb-status-green-bg)', text: 'var(--nb-status-green-text)' },
  grey: { bg: themeColors.background.tertiaryLight, text: 'var(--ds-gray-600)' },
  yellow: { bg: themeColors.background.warningLight, text: 'var(--ds-amber-600)' },
  purple: { bg: themeColors.background.purpleLabel, text: themeColors.text.purpleLabel },
  red: { bg: 'var(--nb-status-red-bg)', text: 'var(--nb-status-red-text)' },
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

// Dropdown menu border colors — match the DS Label tone border exactly.
// outline (not border) is used on the Paper so it isn't clipped by ancestor overflow:hidden.
const VARIANT_TO_BORDER: Record<string, string> = {
  blue: 'var(--ds-blue-200)',
  green: 'var(--ds-green-200)',
  grey: 'var(--ds-gray-200)',
  yellow: 'var(--ds-amber-200)',
  red: 'var(--ds-red-200)',
  purple: 'var(--ds-gray-200)',
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
  const [anchorEl, setAnchorEl] = useState<null | HTMLElement>(null);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [snoozeDialogOpen, setSnoozeDialogOpen] = useState(false);
  const [ticketPromptOpen, setTicketPromptOpen] = useState(false);
  const [pendingStatusChange, setPendingStatusChange] = useState<string | null>(null);

  const open = Boolean(anchorEl);

  const handleClick = (event: React.MouseEvent<HTMLElement>) => {
    event.stopPropagation();
    if (open) {
      setAnchorEl(null);
      return;
    }
    if (!disabled && !loading) {
      setAnchorEl(event.currentTarget);
      setError(null);
    }
  };

  const handleClose = (event?: object) => {
    if (event && 'stopPropagation' in event) {
      (event as React.MouseEvent).stopPropagation();
    }
    setAnchorEl(null);
  };

  const handleStatusChange = async (newStatus: string, snoozedUntil?: string) => {
    handleClose();
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
      handleClose();
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

  return (
    <>
      <Tooltip title={snoozeTooltip} arrow disableHoverListener={disableTooltip || !snoozeTooltip}>
        <Box
          onClick={handleClick}
          sx={{
            display: 'inline-flex',
            cursor: disabled || loading ? 'default' : 'pointer',
            opacity: disabled ? 0.6 : 1,
            transition: 'all 0.2s ease',
            '&:hover': disabled || loading ? {} : { opacity: 0.8 },
          }}
        >
          {loading ? (
            <Box
              sx={{
                display: 'inline-flex',
                alignItems: 'center',
                justifyContent: 'center',
                width: '90px',
                height: '22px',
                backgroundColor: variantColors.bg,
                borderRadius: 'var(--ds-radius-sm)',
              }}
            >
              <CircularProgress size={12} sx={{ color: variantColors.text }} />
            </Box>
          ) : (
            <Label text={statusConfig.label} tone={badgeTone} size='sm' textTransform='none' showDropdownArrow={showArrow} />
          )}
        </Box>
      </Tooltip>

      {/* Status Menu */}
      <Menu
        anchorEl={anchorEl}
        open={open}
        onClose={handleClose}
        anchorOrigin={{ vertical: 'bottom', horizontal: 'left' }}
        transformOrigin={{ vertical: 'top', horizontal: 'left' }}
        MenuListProps={{ disablePadding: true }}
        slotProps={{
          paper: {
            sx: {
              minWidth: '140px',
              overflow: 'hidden',
              borderRadius: 'var(--ds-radius-md)',
              boxShadow: `0px 4px 8px rgba(0,0,0,0.15), 0 0 0 1px ${VARIANT_TO_BORDER[statusConfig.variant] ?? 'var(--ds-gray-200)'}`,
            },
          },
        }}
      >
        {allowedTransitions.length > 0 ? (
          allowedTransitions.map((status) => {
            const config = STATUS_CONFIG[status] || { label: status, variant: 'grey' };
            const colors = VARIANT_COLORS[config.variant] || VARIANT_COLORS.grey;
            return (
              <MenuItem
                key={status}
                onClick={() => handleMenuItemClick(status)}
                sx={{
                  fontSize: 'var(--ds-text-small)',
                  padding: 'var(--ds-space-2) var(--ds-space-4)',
                  '&:hover': { backgroundColor: colors.bg },
                  '&:first-of-type': { borderRadius: 'var(--ds-radius-md) var(--ds-radius-md) 0 0' },
                  '&:last-of-type': { borderRadius: '0 0 var(--ds-radius-md) var(--ds-radius-md)' },
                }}
              >
                <Box
                  sx={{
                    width: '8px',
                    height: '8px',
                    borderRadius: '50%',
                    backgroundColor: colors.text,
                    marginRight: '10px',
                  }}
                />
                {config.label}
              </MenuItem>
            );
          })
        ) : (
          <MenuItem disabled sx={{ fontSize: 'var(--ds-text-body)', color: 'var(--ds-gray-500)' }}>
            No transitions available
          </MenuItem>
        )}
      </Menu>

      {/* Snooze Duration Dialog */}
      <Dialog open={snoozeDialogOpen} onClose={() => setSnoozeDialogOpen(false)} maxWidth='xs' fullWidth>
        <DialogTitle sx={{ pb: 1 }}>Snooze for how long?</DialogTitle>
        <DialogContent>
          <Box sx={{ display: 'flex', flexDirection: 'column', gap: 1 }}>
            {SNOOZE_OPTIONS.map((option) => (
              <Button key={option.hours} tone='secondary' fullWidth icon={<AccessTimeIcon />} onClick={() => handleSnoozeSelect(option.hours)}>
                {option.label}
              </Button>
            ))}
          </Box>
        </DialogContent>
        <DialogActions>
          <Button tone='ghost' onClick={() => setSnoozeDialogOpen(false)}>
            Cancel
          </Button>
        </DialogActions>
      </Dialog>

      {/* Ticket Creation Prompt */}
      <Dialog
        open={ticketPromptOpen}
        onClose={() => {
          setTicketPromptOpen(false);
          // Refresh data after dialog is closed
          if (pendingStatusChange) {
            onStatusChange?.(pendingStatusChange);
            setPendingStatusChange(null);
          }
        }}
        maxWidth='xs'
        fullWidth
      >
        <DialogTitle>Create a Ticket?</DialogTitle>
        <DialogContent>
          <Typography variant='body2' color='text.secondary'>
            This issue requires action. Would you like to create a ticket to track it?
          </Typography>
        </DialogContent>
        <DialogActions>
          <Button
            tone='ghost'
            onClick={() => {
              setTicketPromptOpen(false);
              if (pendingStatusChange) {
                onStatusChange?.(pendingStatusChange);
                setPendingStatusChange(null);
              }
            }}
          >
            Not now
          </Button>
          <Button
            tone='primary'
            onClick={() => {
              setTicketPromptOpen(false);
              onCreateTicket?.();
              if (pendingStatusChange) {
                onStatusChange?.(pendingStatusChange);
                setPendingStatusChange(null);
              }
            }}
          >
            Create Ticket
          </Button>
        </DialogActions>
      </Dialog>

      {error && <Typography sx={{ color: 'var(--ds-red-500)', fontSize: '10px', marginTop: '2px' }}>{error}</Typography>}
    </>
  );
};

export default NBStatusBadge;
