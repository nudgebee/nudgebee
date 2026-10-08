import { useState } from 'react';
import PropTypes from 'prop-types';
import { Box } from '@mui/material';
import { Label } from '@ui/Label';
import Tooltip from '@ui/Tooltip';
import { DropdownMenu } from '@ui/DropdownMenu';
import { Modal } from '@ui/Modal';
import { Input } from '@ui/Input';
import { Select } from '@ui/Select';
import { ds } from '@utils/colors';

// Keep in sync with closeReasonCodes/reopenReasonCodes in event/service.go.
const CLOSE_REASON_OPTIONS = [
  { value: 'already_fixed_externally', label: 'Already fixed externally' },
  { value: 'false_alarm', label: 'False alarm' },
  { value: 'duplicate_of_another_alert', label: 'Duplicate of another alert' },
  { value: 'known_issue_wont_fix', label: "Known issue, won't fix" },
  { value: 'other', label: 'Other' },
];

const REOPEN_REASON_OPTIONS = [
  { value: 'issue_recurred', label: 'Issue recurred' },
  { value: 'closed_by_mistake', label: 'Closed by mistake' },
  { value: 'needs_further_investigation', label: 'Needs further investigation' },
  { value: 'other', label: 'Other' },
];

const isValidHttpUrl = (value) => {
  try {
    const { protocol } = new URL(value);
    return protocol === 'http:' || protocol === 'https:';
  } catch {
    return false;
  }
};

// ds/Input has no maxLength prop (it doesn't spread arbitrary native attrs),
// so the limit is enforced by truncating in the onChange handler below.
const REASON_TEXT_MAX_LENGTH = 500;

/**
 * Same interaction shape as NBStatusBadge: a dropdown-arrow Label opening the
 * one valid transition. Closed -> Open is enabled only when closedByName is
 * set, i.e. only for events this same dropdown closed (see
 * GetEventManualStatusChange) — most closes are automatic, and reopening
 * those would fight whatever closed them. Disabled item stays visible rather
 * than hiding the arrow, for row consistency.
 */
const AlertStatusBadge = ({ eventId, status, canToggle, closedByName, closedReasonCode, closedReasonText, closedEvidenceUrl, onToggle }) => {
  // Closed explicitly on mousedown (fires before the menu-opening click), else
  // the tooltip stays open and covers the DropdownMenu — same fix as NBStatusBadge.
  const [tooltipOpen, setTooltipOpen] = useState(false);
  const [pendingStatus, setPendingStatus] = useState(null);
  const [reasonCode, setReasonCode] = useState(null);
  const [reasonText, setReasonText] = useState('');
  const [evidenceUrl, setEvidenceUrl] = useState('');
  const [submitting, setSubmitting] = useState(false);

  const isOpen = status === 'FIRING';
  const isClosed = status === 'CLOSED';
  const isResolved = status === 'RESOLVED';
  const canReopen = isClosed && !!closedByName;
  const showArrow = canToggle && (isOpen || isClosed);

  const isClosingAction = pendingStatus === 'CLOSED';
  // Shown, not required, only when closing a still-FIRING event (the one
  // transition that overrides a live signal) — a free-text URL only proves
  // the caller typed something, not that it's relevant.
  const showEvidenceField = isClosingAction && isOpen;
  const reasonOptions = isClosingAction ? CLOSE_REASON_OPTIONS : REOPEN_REASON_OPTIONS;
  const reasonTextRequired = reasonCode === 'other';
  const trimmedEvidenceUrl = evidenceUrl.trim();
  const evidenceUrlError = trimmedEvidenceUrl && !isValidHttpUrl(trimmedEvidenceUrl) ? 'Enter a valid http:// or https:// URL' : undefined;
  const canConfirm = !!reasonCode && (!reasonTextRequired || reasonText.trim().length > 0) && !evidenceUrlError;

  const closeReasonLabel = closedReasonCode
    ? CLOSE_REASON_OPTIONS.find((option) => option.value === closedReasonCode)?.label || closedReasonCode
    : null;
  const tooltipDesc = closeReasonLabel ? (
    <>
      {closeReasonLabel}
      {closedReasonText ? `: ${closedReasonText}` : ''}
      {closedEvidenceUrl && (
        <>
          <br />
          {closedEvidenceUrl}
        </>
      )}
    </>
  ) : undefined;

  const resetDialog = () => {
    setPendingStatus(null);
    setReasonCode(null);
    setReasonText('');
    setEvidenceUrl('');
  };

  const handleConfirm = async () => {
    if (!canConfirm || !pendingStatus) return;
    setSubmitting(true);
    try {
      await onToggle(eventId, pendingStatus, {
        reasonCode,
        reasonText: reasonText.trim() || undefined,
        evidenceUrl: trimmedEvidenceUrl || undefined,
      });
      resetDialog();
    } finally {
      setSubmitting(false);
    }
  };

  const badgeLabel = (
    <Label
      margin='0'
      text={isOpen ? 'Open' : isClosed ? 'Closed' : isResolved ? 'Resolved' : status}
      variant={isOpen ? 'red' : isClosed ? 'grey' : isResolved ? 'green' : ''}
      showDropdownArrow={showArrow}
    />
  );

  // Box must be the outermost trigger element — DropdownMenu clones it to
  // attach its open-menu onClick; nesting Tooltip outermost broke reopen
  // (same fix as NBStatusBadge's triggerBox).
  const trigger = (
    <Box
      onClick={(e) => e.stopPropagation()}
      onMouseEnter={() => setTooltipOpen(true)}
      onMouseLeave={() => setTooltipOpen(false)}
      onMouseDown={() => setTooltipOpen(false)}
      sx={{ display: 'inline-flex', cursor: showArrow ? 'pointer' : 'default' }}
    >
      {isClosed ? (
        <Tooltip
          variant={tooltipDesc ? 'explainer' : 'default'}
          title={closedByName ? `Closed by ${closedByName}` : 'Closed'}
          desc={tooltipDesc}
          open={tooltipOpen}
          disableHoverListener
          disableFocusListener
          disableTouchListener
        >
          <Box component='span' sx={{ display: 'inline-flex' }}>
            {badgeLabel}
          </Box>
        </Tooltip>
      ) : (
        badgeLabel
      )}
    </Box>
  );

  const dialog = (
    <Modal
      open={pendingStatus !== null}
      handleClose={resetDialog}
      title={isClosingAction ? 'Close this event?' : 'Reopen this event?'}
      width='xs'
      confirmText={isClosingAction ? 'Close Event' : 'Reopen Event'}
      onConfirm={handleConfirm}
      confirmDisabled={!canConfirm}
      loader={submitting}
    >
      <Box sx={{ display: 'flex', flexDirection: 'column', gap: 'var(--ds-space-3)' }}>
        <Select
          label='Reason'
          required
          value={reasonCode}
          onChange={(value) => setReasonCode(value)}
          options={reasonOptions}
          placeholder='Select a reason'
        />
        <Input
          label='Additional details'
          required={reasonTextRequired}
          value={reasonText}
          onChange={(value) => setReasonText((typeof value === 'string' ? value : '').slice(0, REASON_TEXT_MAX_LENGTH))}
          type='textarea'
          rows={3}
          size='sm'
          placeholder={reasonTextRequired ? 'Describe why (required for "Other")' : 'Optional details'}
          help={`${reasonText.length}/${REASON_TEXT_MAX_LENGTH}`}
        />
        {showEvidenceField && (
          <Input
            label='Evidence link (optional)'
            value={evidenceUrl}
            onChange={setEvidenceUrl}
            size='sm'
            placeholder='Link to a dashboard, ticket, or discussion proving this is safe to close'
            help='This event is still firing — closing it overrides a live signal, so a link helps future review.'
            error={evidenceUrlError}
          />
        )}
      </Box>
    </Modal>
  );

  if (!showArrow)
    return (
      <>
        {trigger}
        {dialog}
      </>
    );

  // Dot color matches the status being transitioned TO, not the current one.
  const menuItems = [
    {
      id: 'toggle',
      label: (
        <Box sx={{ display: 'flex', alignItems: 'center', gap: 'var(--ds-space-2)' }}>
          <Box
            sx={{
              width: ds.space[2],
              height: ds.space[2],
              borderRadius: 'var(--ds-radius-pill)',
              backgroundColor: isOpen ? 'var(--ds-green-700)' : 'var(--ds-blue-700)',
              flexShrink: 0,
            }}
          />
          {isOpen ? 'Close' : 'Open'}
        </Box>
      ),
      disabled: isClosed && !canReopen,
      onSelect: () => setPendingStatus(isOpen ? 'CLOSED' : 'FIRING'),
    },
  ];

  return (
    <>
      <DropdownMenu trigger={trigger} items={menuItems} side='bottom' align='start' minWidth={120} />
      {dialog}
    </>
  );
};

AlertStatusBadge.propTypes = {
  eventId: PropTypes.string.isRequired,
  status: PropTypes.string,
  canToggle: PropTypes.bool,
  closedByName: PropTypes.string,
  closedReasonCode: PropTypes.string,
  closedReasonText: PropTypes.string,
  closedEvidenceUrl: PropTypes.string,
  onToggle: PropTypes.func.isRequired,
};

export default AlertStatusBadge;
