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
import Button from '@mui/material/Button';
import Tooltip from '@mui/material/Tooltip';
import KeyboardArrowDownIcon from '@mui/icons-material/KeyboardArrowDown';
import AccessTimeIcon from '@mui/icons-material/AccessTime';
import apiTriage from 'src/api1/triage';
import { colors as themeColors } from 'src/utils/colors';

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

// Color configuration for each variant
const VARIANT_COLORS: Record<string, { bg: string; text: string }> = {
  blue: { bg: 'var(--nb-status-blue-bg)', text: 'var(--nb-status-blue-text)' },
  green: { bg: 'var(--nb-status-green-bg)', text: 'var(--nb-status-green-text)' },
  grey: { bg: themeColors.background.tertiaryLight, text: '#616161' },
  yellow: { bg: themeColors.background.warningLight, text: '#F57F17' },
  purple: { bg: themeColors.background.purpleLabel, text: themeColors.text.purpleLabel },
  red: { bg: 'var(--nb-status-red-bg)', text: 'var(--nb-status-red-text)' },
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
  const allowedTransitions = ALLOWED_TRANSITIONS[currentStatus] || [];

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
            alignItems: 'center',
            justifyContent: 'center',
            padding: '2px 10px',
            borderRadius: '4px',
            backgroundColor: variantColors.bg,
            color: variantColors.text,
            cursor: disabled || loading ? 'default' : 'pointer',
            opacity: disabled ? 0.6 : 1,
            width: '90px',
            height: '22px',
            transition: 'all 0.2s ease',
            '&:hover': disabled || loading ? {} : { opacity: 0.8, boxShadow: '0 1px 3px rgba(0,0,0,0.12)' },
          }}
        >
          {loading ? (
            <CircularProgress size={12} sx={{ color: variantColors.text }} />
          ) : (
            <>
              <Typography
                sx={{
                  fontSize: '11px',
                  fontWeight: 500,
                  fontFamily: 'Roboto',
                  textTransform: 'none',
                  whiteSpace: 'nowrap',
                }}
              >
                {statusConfig.label}
              </Typography>
              {!disabled && allowedTransitions.length > 0 && <KeyboardArrowDownIcon sx={{ fontSize: '14px', ml: 0.5, opacity: 0.7 }} />}
            </>
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
        slotProps={{
          paper: {
            sx: {
              minWidth: '140px',
              boxShadow: '0 4px 12px rgba(0,0,0,0.15)',
              borderRadius: '8px',
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
                  fontSize: '13px',
                  padding: '8px 16px',
                  '&:hover': { backgroundColor: colors.bg },
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
                {status === 'SNOOZED' && <AccessTimeIcon sx={{ fontSize: '14px', ml: 'auto', opacity: 0.5 }} />}
              </MenuItem>
            );
          })
        ) : (
          <MenuItem disabled sx={{ fontSize: '13px', color: '#999' }}>
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
              <Button
                key={option.hours}
                variant='outlined'
                onClick={() => handleSnoozeSelect(option.hours)}
                sx={{
                  justifyContent: 'flex-start',
                  textTransform: 'none',
                  py: 1.5,
                }}
              >
                <AccessTimeIcon sx={{ mr: 1, fontSize: '18px' }} />
                {option.label}
              </Button>
            ))}
          </Box>
        </DialogContent>
        <DialogActions>
          <Button onClick={() => setSnoozeDialogOpen(false)} color='inherit'>
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
            onClick={() => {
              setTicketPromptOpen(false);
              // Refresh data when user dismisses
              if (pendingStatusChange) {
                onStatusChange?.(pendingStatusChange);
                setPendingStatusChange(null);
              }
            }}
            color='inherit'
          >
            Not now
          </Button>
          <Button
            onClick={() => {
              setTicketPromptOpen(false);
              onCreateTicket?.();
              // Refresh data after opening ticket form
              if (pendingStatusChange) {
                onStatusChange?.(pendingStatusChange);
                setPendingStatusChange(null);
              }
            }}
            variant='contained'
            color='primary'
          >
            Create Ticket
          </Button>
        </DialogActions>
      </Dialog>

      {error && <Typography sx={{ color: 'red', fontSize: '10px', marginTop: '2px' }}>{error}</Typography>}
    </>
  );
};

export default NBStatusBadge;
