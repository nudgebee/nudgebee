import { useState } from 'react';
import { Box, MenuItem, TextField, Typography } from '@mui/material';
import { Modal } from '@ui/Modal';
import { Button } from '@ui/Button';
import { toast as snackbar } from '@ui/Toast';
import { ds } from 'src/utils/colors';
import recommendationApi from '@api1/recommendation';
import { getResourceDisplayName } from './utils';

const SNOOZE_OPTIONS = [
  { label: "Don't snooze — dismiss permanently", days: 0 },
  { label: 'Snooze for 7 days', days: 7 },
  { label: 'Snooze for 30 days', days: 30 },
  { label: 'Snooze for 90 days', days: 90 },
];

interface DismissModalProps {
  rec: any;
  onClose: () => void;
  // Called with the recommendation id after the backend applied the change,
  // so the caller can drop/refresh the row.
  onSuccess: (recommendationId: string) => void;
}

/**
 * Dismiss or snooze a recommendation. A snooze is a dismissal with an expiry:
 * the backend stores it as Dismissed + snoozed_until and returns it to Open
 * automatically when the period lapses. Both forms suppress the recommendation
 * from the default list, nudges, digest, and the finops score.
 */
const DismissModal = ({ rec, onClose, onSuccess }: DismissModalProps) => {
  const [snoozeDays, setSnoozeDays] = useState(0);
  const [reason, setReason] = useState('');
  const [submitting, setSubmitting] = useState(false);

  const resourceName = getResourceDisplayName(rec, '');

  const submit = async () => {
    setSubmitting(true);
    try {
      const snoozedUntil = snoozeDays > 0 ? new Date(Date.now() + snoozeDays * 24 * 60 * 60 * 1000).toISOString() : undefined;
      const res = await recommendationApi.updateRecommendationDismissal(rec.account_id, rec.id, {
        dismissed: true,
        reason: reason.trim(),
        snoozedUntil,
      });
      if (res?.errors?.length) {
        snackbar.error(res.errors[0]?.message || 'Failed to dismiss recommendation');
        return;
      }
      // Fail closed: an empty response means the change cannot be confirmed.
      if (!res?.data || res.data.applied === false) {
        snackbar.error(res?.data?.message || 'Recommendation could not be dismissed');
        return;
      }
      snackbar.success(snoozeDays > 0 ? `Snoozed for ${snoozeDays} days — it will return automatically.` : 'Recommendation dismissed');
      onSuccess(rec.id);
    } catch {
      snackbar.error('Failed to dismiss recommendation');
    } finally {
      setSubmitting(false);
    }
  };

  return (
    <Modal open handleClose={onClose} width='sm' title='Dismiss Recommendation' isConfirmRequired={false} isCancelRequired={false}>
      <Typography sx={{ fontSize: ds.text.small, color: ds.gray[600], mb: ds.space[3] }}>
        {resourceName ? `Dismiss the recommendation for ${resourceName}.` : 'Dismiss this recommendation.'} It leaves the open list, nudges, digest,
        and the FinOps score; a snoozed one returns automatically when the period ends.
      </Typography>
      <Box sx={{ display: 'flex', flexDirection: 'column', gap: ds.space[3] }}>
        <TextField
          id='dismiss-snooze-select'
          select
          size='small'
          label='Duration'
          value={snoozeDays}
          onChange={(e) => setSnoozeDays(Number(e.target.value))}
        >
          {SNOOZE_OPTIONS.map((opt) => (
            <MenuItem key={opt.days} value={opt.days}>
              {opt.label}
            </MenuItem>
          ))}
        </TextField>
        <TextField
          id='dismiss-reason'
          label='Reason (optional)'
          size='small'
          multiline
          minRows={2}
          value={reason}
          onChange={(e) => setReason(e.target.value)}
          placeholder='Why is this being dismissed?'
        />
        <Box sx={{ display: 'flex', justifyContent: 'flex-end', gap: ds.space[2] }}>
          <Button tone='secondary' size='sm' onClick={onClose} id='dismiss-cancel'>
            Cancel
          </Button>
          <Button tone='primary' size='sm' onClick={submit} disabled={submitting} id='dismiss-confirm'>
            {snoozeDays > 0 ? 'Snooze' : 'Dismiss'}
          </Button>
        </Box>
      </Box>
    </Modal>
  );
};

export default DismissModal;
