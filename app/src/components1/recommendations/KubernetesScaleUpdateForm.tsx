import React, { useEffect, useState } from 'react';

import { Grid, TextField } from '@mui/material';
import { inputSx } from '@data/themes/inputField';
import { Modal } from '@components1/common/modal';
import CustomButton from '@components1/common/NewCustomButton';
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
    <React.Fragment>
      <Modal loader={loading} width='md' open={open} handleClose={handleClose} title={'Scale the ' + selectedRow.kind + ' ' + selectedRow.name}>
        <Grid container spacing={1}>
          <Grid item xs={4}>
            <TextField
              sx={inputSx}
              size='small'
              value={selectedRow?.total_pods || 0}
              margin='normal'
              id='existing-total-pods'
              label='Existing Replica'
              type='text'
              disabled={true}
            />
          </Grid>

          <Grid item xs={4}>
            <TextField
              sx={inputSx}
              size='small'
              value={updateReplicas}
              margin='normal'
              id='new-total-pods'
              label='Update Replica'
              type='number'
              inputProps={{ min: 0 }}
              onKeyDown={(e) => {
                if (e.key === '-' || e.key === 'e') {
                  e.preventDefault();
                }
              }}
              onChange={(e: any) => {
                const value = Number(e.target.value);
                setUpdatedRelicas(value < 0 ? 0 : value);
              }}
            />
          </Grid>
        </Grid>
        <Grid
          container
          spacing={2}
          mt={1}
          mb={4}
          justifyContent='flex-end'
          sx={{
            button: {
              minWidth: '140px',
            },
          }}
        >
          <Grid item>
            <CustomButton
              text='Cancel'
              variant='secondary'
              size='Medium'
              onClick={() => {
                handleClose();
                setUpdatedRelicas(0);
              }}
              disabled={loading}
            />
          </Grid>
          <Grid item>
            <CustomButton size='Medium' id={'save-scale'} text={'Submit'} onClick={() => handleSubmit(updateReplicas)} disabled={loading} />
          </Grid>
        </Grid>
      </Modal>
    </React.Fragment>
  );
};

export default KubernetesScaleUpdateForm;
