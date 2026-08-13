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

// Mirrors backend priorityToMinScore (api-server/services/triage/scoring.go): pinning a
// priority snaps computed_score to the band floor. Keep the two in sync.
const PRIORITY_MIN_SCORE: Record<string, number> = { P0: 80, P1: 60, P2: 40, P3: 20 };

export interface PriorityPinControlProps {
  /** Representative event id the correction is recorded against. The backend derives the
   * signal class from this event's fingerprint, so no fingerprint prop is needed. */
  eventId?: string | null;
  /** Cloud account id the event belongs to. */
  accountId?: string | null;
  /** Current computed priority (P0..P3) shown on the pill. */
  currentPriority?: string | null;
  /** Current computed score — captured so a failed API call can revert the optimistic score. */
  currentScore?: number | null;
  /** Whether the user can write to this account. Renders nothing when false. */
  canWrite?: boolean;
  /**
   * Called optimistically on selection with the new priority and its band-floor score
   * (matching what the backend will persist), and again with the previous values if the
   * background API call fails (so the caller can revert).
   */
  onChanged?: (newPriority: string | null, newScore?: number | null) => void;
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
  currentScore,
  canWrite = false,
  onChanged,
  scope = 'this_fingerprint',
}) => {
  if (!canWrite || !eventId || !accountId) {
    return null;
  }

  const pin = async (priority: string): Promise<void> => {
    if (priority === currentPriority) return;

    const previousPriority = currentPriority ?? null;
    const previousScore = currentScore ?? null;
    onChanged?.(priority, PRIORITY_MIN_SCORE[priority] ?? null); // optimistic update

    try {
      const res = await apiTriage.classifyEvent({
        event_id: eventId,
        classification: 'true_positive',
        reason_code: 'correct_severity',
        corrected_priority: priority,
        apply_scope: scope,
        apply_to_existing: scope === 'this_fingerprint',
        confirmed: true,
        // A priority override is not a triage-state assertion: keep nb_status as-is
        // instead of letting the classify pipeline force it to ACTION_REQUIRED.
        preserve_status: true,
      });
      if (!res?.success) {
        snackbar.error('Failed to set priority');
        onChanged?.(previousPriority, previousScore); // revert
      }
    } catch {
      snackbar.error('Failed to set priority');
      onChanged?.(previousPriority, previousScore); // revert
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
        active: p.value === currentPriority,
        onSelect: () => pin(p.value),
      }))}
    />
  );
};

export default PriorityPinControl;
