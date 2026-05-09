import { Grid, Typography, FormControlLabel, Checkbox, Alert } from '@mui/material';
import React, { useRef, useState } from 'react';
import PropTypes from 'prop-types';
import { Modal } from '@components1/common/modal';
import CustomButton from '@components1/common/NewCustomButton';
import { snackbar } from '@components1/common/snackbarService';
import MarkDowns from '@components1/common/MarkDowns';
import apiKubernetes1 from '@api1/kubernetes1';
import { getBrandTitle } from '@hooks/useTenantBranding';

const CF_UPDATE_INSTRUCTIONS = `### Update CloudFormation Permissions
  ### Step 1. Review the options below
   - Choose which permissions to enable for your Nudgebee integration.
  ### Step 2. Click "Open AWS Console"
   - It will open the CloudFormation stack update page.
   - The new template will be pre-selected.
   - In **Step 2 (Parameters)**, update the following:
  ### Step 3. Update parameters in AWS Console
   - Set **NudgebeeSsmAccess** and **NudgebeeAccessMode** to match your selections below.
   - Click **Next** through the remaining steps and then **Submit**.`;

const CfUpdateModal = ({ open, onClose, accountId }) => {
  const [loading, setLoading] = useState(false);
  const [data, setData] = useState(null);
  const [ssmAccess, setSsmAccess] = useState(false);
  const [readOnly, setReadOnly] = useState(false);
  const fetchIdRef = useRef(0);

  const handleOpen = () => {
    setLoading(true);
    setData(null);
    setSsmAccess(false);
    setReadOnly(false);
    const id = ++fetchIdRef.current;
    apiKubernetes1
      .getCloudUpdateCloudformationPermissions(accountId)
      .then((res) => {
        if (id !== fetchIdRef.current) {
          return;
        }
        const result = res?.data?.data?.cloud_update_cloudformation_permissions;
        if (result?.url) {
          setData(result);
        } else {
          snackbar.error(`Could not find the ${getBrandTitle()} CloudFormation stack for this account.`);
          onClose();
        }
      })
      .catch(() => {
        if (id !== fetchIdRef.current) {
          return;
        }
        snackbar.error('Failed to look up CloudFormation stack. Please try again.');
        onClose();
      })
      .finally(() => {
        if (id !== fetchIdRef.current) {
          return;
        }
        setLoading(false);
      });
  };

  const handleClose = () => {
    fetchIdRef.current++;
    setData(null);
    setSsmAccess(false);
    setReadOnly(false);
    onClose();
  };

  React.useEffect(() => {
    if (open && accountId) {
      handleOpen();
    }
  }, [open, accountId]);

  if (!open) {
    return null;
  }

  return (
    <Modal width='md' open={open} handleClose={loading ? () => {} : handleClose} title='Update CloudFormation Permissions' loader={loading}>
      {data && (
        <>
          {data.needs_update ? (
            <Alert severity='info' sx={{ mx: 2, mt: 1 }}>
              Stack <strong>{data.stack_name}</strong> is on v{data.template_version}. Latest version is v{data.latest_version}.
            </Alert>
          ) : (
            <Alert severity='success' sx={{ mx: 2, mt: 1 }}>
              Stack <strong>{data.stack_name}</strong> is already on the latest version (v{data.latest_version}). You can still update parameter
              settings below.
            </Alert>
          )}

          <MarkDowns data={CF_UPDATE_INSTRUCTIONS} sx={{ width: 'auto' }} />

          <Grid container spacing={1} px={3} mt={1}>
            <Grid item xs={12}>
              <FormControlLabel
                control={<Checkbox checked={ssmAccess} onChange={(e) => setSsmAccess(e.target.checked)} size='small' />}
                label={
                  <Typography variant='body2'>
                    <strong>Enable SSM Parameter Store access</strong> — allows Nudgebee to read parameter values. Only enable if your parameters do
                    not contain secrets.
                  </Typography>
                }
              />
            </Grid>
            <Grid item xs={12}>
              <FormControlLabel
                control={<Checkbox checked={readOnly} onChange={(e) => setReadOnly(e.target.checked)} size='small' />}
                label={
                  <Typography variant='body2'>
                    <strong>Read-Only mode</strong> — disables alerting and automated actions. Use this for monitoring and cost reporting only.
                  </Typography>
                }
              />
            </Grid>
          </Grid>

          <Alert severity='warning' sx={{ mx: 2, mt: 2 }}>
            When you reach <strong>Step 2 (Parameters)</strong> in AWS Console, set:
            <br />
            &bull; <strong>NudgebeeSsmAccess</strong> = <code>{ssmAccess ? 'enabled' : 'disabled'}</code>
            <br />
            &bull; <strong>NudgebeeAccessMode</strong> = <code>{readOnly ? 'readonly' : 'readwrite'}</code>
          </Alert>

          <Grid container spacing={2} mt={1} mb={4} justifyContent='flex-end' sx={{ button: { minWidth: '140px' } }}>
            <Grid item>
              <CustomButton id='close-cf-update-btn' size='Medium' text='Close' variant='secondary' onClick={handleClose} />
            </Grid>
            <Grid item>
              <CustomButton
                id='open-cf-console-btn'
                size='Medium'
                text='Open AWS Console'
                onClick={() => {
                  const cfUrl = new URL(data.url);
                  const hash = cfUrl.hash;
                  const paramSuffix =
                    `&param_NudgebeeSsmAccess=${ssmAccess ? 'enabled' : 'disabled'}` +
                    `&param_NudgebeeAccessMode=${readOnly ? 'readonly' : 'readwrite'}`;
                  cfUrl.hash = hash + paramSuffix;
                  window.open(cfUrl.toString(), '_blank', 'noopener,noreferrer');
                }}
              />
            </Grid>
          </Grid>
        </>
      )}
    </Modal>
  );
};

CfUpdateModal.propTypes = {
  open: PropTypes.bool.isRequired,
  onClose: PropTypes.func.isRequired,
  accountId: PropTypes.string,
};

export default CfUpdateModal;
