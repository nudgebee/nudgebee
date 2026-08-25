import { useState } from 'react';
import { Box, Typography } from '@mui/material';
import { Modal } from '@ui/Modal';
import { Button } from '@ui/Button';
import FilterDropdown from '@ui/FilterDropdown';
import CustomTextField from '@shared/forms/CustomTextField';
import { toast as snackbar } from '@ui/Toast';
import { ds } from 'src/utils/colors';
import recommendationApi from '@api1/recommendation';
import { getResourceDisplayName } from './utils';

const DAY_MS = 24 * 60 * 60 * 1000;

// Keep labels short — the dropdown trigger ellipsises a selected label past ~90px.
const SNOOZE_OPTIONS = [
  { label: 'Permanent', value: 0 },
  { label: '7 days', value: 7 },
  { label: '30 days', value: 30 },
  { label: '90 days', value: 90 },
];

const snoozeUntil = (days: number) => new Date(Date.now() + days * DAY_MS);

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
      const snoozedUntil = snoozeDays > 0 ? snoozeUntil(snoozeDays).toISOString() : undefined;
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
        <Box>
          <FilterDropdown
            id='dismiss-snooze-select'
            label='Duration'
            options={SNOOZE_OPTIONS}
            value={SNOOZE_OPTIONS.find((o) => o.value === snoozeDays) || null}
            clearable={false}
            // Panel must portal to body — the Modal body clips an inline one.
            disablePortal={false}
            onSelect={(_e: any, item: any) => {
              // Read through a primitive too: a bare value would make `item?.value`
              // undefined and silently turn a requested snooze into a permanent dismiss.
              const next = item && typeof item === 'object' ? item.value : item;
              setSnoozeDays(Number(next ?? 0));
            }}
            sx={{ width: '100%' }}
          />
          <Typography sx={{ fontSize: ds.text.caption, color: ds.gray[500], mt: ds.space[1] }}>
            {snoozeDays > 0
              ? `Returns to the open list on ${snoozeUntil(snoozeDays).toLocaleDateString('en-US', {
                  month: 'short',
                  day: 'numeric',
                  year: 'numeric',
                })}.`
              : 'Stays dismissed until someone reactivates it.'}
          </Typography>
        </Box>
        <CustomTextField
          id='dismiss-reason'
          label='Reason'
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
