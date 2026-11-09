import { useMemo, useState } from 'react';
import { Box, Typography } from '@mui/material';
import { Modal } from '@ui/Modal';
import { Banner } from '@ui/Banner';
import { toast as snackbar } from '@ui/Toast';
import CopyButton from '@shared/buttons/CopyButton';
import recommendationApi from '@api1/recommendation';
import { ds } from 'src/utils/colors';
import { canStartCloudAction, cloudActionFor } from './cloudActions';

interface CloudActionModalProps {
  open: boolean;
  onClose: () => void;
  recommendation: any;
  onSuccess?: () => void;
}

// The confirm surface for a fix applied in the cloud account directly. The
// backend applies the stored recommendation and tracks the change until the
// provider reports it finished, so this only confirms and starts it.
const CloudActionModal = ({ open, onClose, recommendation: rec, onSuccess }: CloudActionModalProps) => {
  const action = useMemo(() => cloudActionFor(rec), [rec]);
  const [submitting, setSubmitting] = useState(false);

  if (!action) return null;

  const canStart = canStartCloudAction(rec);

  const submit = async () => {
    setSubmitting(true);
    try {
      const res: any = await recommendationApi.applyRecommendation(rec.account_id || '', rec.id, {});
      // A request that never reached the backend comes back with neither data
      // nor errors, so only returned data counts as started.
      if (res?.errors?.length || res?.data == null) {
        snackbar.error(res?.errors?.[0]?.message || `Failed to ${action.label.toLowerCase()}`);
        return;
      }
      snackbar.success(action.successMessage);
      onSuccess?.();
      onClose();
    } catch (err: any) {
      snackbar.error(err?.message || `Failed to ${action.label.toLowerCase()}`);
    } finally {
      setSubmitting(false);
    }
  };

  return (
    <Modal
      width='sm'
      open={open}
      handleClose={() => !submitting && onClose()}
      title={action.label}
      confirmText={action.confirmText}
      onConfirm={submit}
      confirmDisabled={submitting || !canStart}
      loader={submitting}
      backdropClickClose={!submitting}
    >
      <Box sx={{ display: 'flex', flexDirection: 'column', gap: ds.space[3] }} id='cloud-action-modal'>
        {!canStart && (
          <Banner
            id='cloud-action-started'
            surface='section'
            tone='info'
            title={rec.status === 'InProgress' ? 'Already started' : 'Not open'}
            message={
              rec.status === 'InProgress'
                ? 'This change is already running in the cloud account — follow it under Resolutions.'
                : 'Only an open recommendation can be applied.'
            }
          />
        )}
        {action.impact ? (
          <Banner id='cloud-action-impact' surface='section' tone='info' title='What applying does' message={action.impact} />
        ) : (
          <Typography variant='body2' sx={{ color: ds.gray[700] }}>
            {action.summary}
          </Typography>
        )}

        <Box component='dl' sx={{ m: 0, display: 'grid', gridTemplateColumns: 'max-content 1fr', columnGap: ds.space[4], rowGap: ds.space[1] }}>
          {action.facts.map((fact) => (
            <Box key={fact.label} sx={{ display: 'contents' }}>
              <Typography component='dt' variant='body2' sx={{ color: ds.gray[600] }}>
                {fact.label}
              </Typography>
              <Box component='dd' sx={{ m: 0, display: 'flex', alignItems: 'center', gap: ds.space[1], minWidth: 0 }}>
                <Typography variant='body2' sx={{ color: ds.gray[700], fontWeight: ds.weight.medium, wordBreak: 'break-all' }}>
                  {fact.value}
                </Typography>
                {fact.copyable && <CopyButton text={fact.value} size='sm' />}
              </Box>
            </Box>
          ))}
        </Box>
      </Box>
    </Modal>
  );
};

export default CloudActionModal;
