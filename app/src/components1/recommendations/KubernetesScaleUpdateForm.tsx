import React, { useEffect, useState } from 'react';

import { Box, Grid } from '@mui/material';
import { Input } from '@components1/ds/Input';
import { Modal } from '@components1/ds/Modal';
import { Button as DsButton } from '@components1/ds/Button';
interface KubernetesScaleFormProps {
  selectedRow: any;
  open: boolean;
  handleSubmit: (value: number) => {
    return: any;
  };
  handleClose: () => {
    return: any;
  };
  loading: boolean;
}

const KubernetesScaleUpdateForm: React.FC<KubernetesScaleFormProps> = ({ open, selectedRow, handleSubmit, handleClose, loading }) => {
  const [updateReplicas, setUpdatedRelicas] = useState<number>(0);
  useEffect(() => {
    if (open) {
      setUpdatedRelicas(selectedRow?.total_pods || 1);
    }
  }, [selectedRow]);

  return (
    <Modal
      loader={loading}
      width='md'
      open={open}
      handleClose={handleClose}
      title={'Scale the ' + selectedRow.kind + ' ' + selectedRow.name}
      actionButtons={
        <Box sx={{ display: 'flex', justifyContent: 'flex-end', gap: 'var(--ds-space-3)', p: 'var(--ds-space-3) var(--ds-space-5)' }}>
          <DsButton
            id='cancel-scale-btn'
            tone='secondary'
            size='md'
            onClick={() => {
              handleClose();
              setUpdatedRelicas(0);
            }}
            disabled={loading}
          >
            Cancel
          </DsButton>
          <DsButton id='save-scale' tone='primary' size='md' onClick={() => handleSubmit(updateReplicas)} disabled={loading}>
            Submit
          </DsButton>
        </Box>
      }
    >
      <Box sx={{ padding: 'var(--ds-space-4) var(--ds-space-5)' }}>
        <Grid container spacing={2}>
          <Grid item xs={6}>
            <Input
              size='sm'
              value={String(selectedRow?.total_pods ?? 0)}
              id='existing-total-pods'
              label='Existing Replica'
              disabled
              onChange={() => {}}
            />
          </Grid>
          <Grid item xs={6}>
            <Input
              size='sm'
              value={String(updateReplicas)}
              id='new-total-pods'
              label='Update Replica'
              type='number'
              onKeyDown={(e) => {
                if (e.key === '-' || e.key === 'e') {
                  e.preventDefault();
                }
              }}
              onChange={(next) => {
                const value = Number(next);
                setUpdatedRelicas(value < 0 ? 0 : value);
              }}
            />
          </Grid>
        </Grid>
      </Box>
    </Modal>
  );
};

export default KubernetesScaleUpdateForm;
