import React from 'react';
import { DropdownMenu } from '@ui/DropdownMenu';
import { Button as DsButton } from '@ui/Button';
import KeyboardArrowDownIcon from '@mui/icons-material/KeyboardArrowDown';
import { snackbar } from '@shared/snackbarService';
import { ds } from '@utils/colors';
import apiTriage from '@api1/triage';

const PRIORITY_OPTIONS = [
  { value: 'P0', label: 'P0 — Critical' },
  { value: 'P1', label: 'P1 — High' },
  { value: 'P2', label: 'P2 — Medium' },
  { value: 'P3', label: 'P3 — Low' },
];

export interface PriorityPinControlProps {
  /** Representative event id the correction is recorded against. The backend derives the
   * signal class from this event's fingerprint, so no fingerprint prop is needed. */
  eventId?: string | null;
  /** Cloud account id the event belongs to. */
  accountId?: string | null;
  /** Current computed priority (P0..P3) shown on the pill. */
  currentPriority?: string | null;
  /** Whether the user can write to this account. Renders nothing when false. */
  canWrite?: boolean;
  /** Called after a successful pin/clear so the caller can refetch. */
  onChanged?: () => void;
  /** Pin scope. Defaults to the whole alert class ('this_fingerprint'). */
  scope?: 'this_event' | 'this_fingerprint';
}

/**
 * Inline priority-override control: a small pill showing the current P-level that, when clicked,
 * lets a user pin the priority (a durable human correction the model won't overwrite). Centralizes
 * the classify call + toasts so every triage table renders the same affordance.
 */
const PriorityPinControl: React.FC<PriorityPinControlProps> = ({
  eventId,
  accountId,
  currentPriority,
  canWrite = false,
  onChanged,
  scope = 'this_fingerprint',
}) => {
  if (!canWrite || !eventId || !accountId) {
    return null;
  }

  const pin = async (priority: string): Promise<void> => {
    try {
      const res = await apiTriage.classifyEvent({
        event_id: eventId,
        classification: 'true_positive',
        reason_code: 'correct_severity',
        corrected_priority: priority,
        apply_scope: scope,
        apply_to_existing: scope === 'this_fingerprint',
        confirmed: true,
      });
      if (res?.success) {
        snackbar.success(scope === 'this_fingerprint' ? `Pinned to ${priority} for this alert class` : `Pinned this alert to ${priority}`);
        onChanged?.();
      } else {
        snackbar.error('Failed to set priority');
      }
    } catch {
      snackbar.error('Failed to set priority');
    }
  };

  return (
    <DropdownMenu
      align='start'
      minWidth={180}
      trigger={
        <DsButton
          tone='secondary'
          size='xs'
          trailingAccent={<KeyboardArrowDownIcon sx={{ fontSize: ds.text.body }} />}
          aria-label='Override priority'
          tooltip='Override priority for this alert class'
          onClick={(e) => e.stopPropagation()}
        >
          {currentPriority || 'Set'}
        </DsButton>
      }
      items={PRIORITY_OPTIONS.map((p) => ({
        id: p.value,
        label: p.label,
        onSelect: () => pin(p.value),
      }))}
    />
  );
};

export default PriorityPinControl;
