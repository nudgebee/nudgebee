import React, { useState } from 'react';
import { TextField, Typography, Box, InputAdornment, Radio, RadioGroup, FormControlLabel } from '@mui/material';
import { Checkbox } from '@components1/ds/Checkbox';
import apiRecommendations from '@api1/recommendation';
import yaml from 'js-yaml';
import CustomTable from '@common-new/tables/CustomTable2';
import PropTypes from 'prop-types';
import CustomButton from '@components1/common/NewCustomButton';
import { snackbar } from '@components1/common/snackbarService';
import { parseHttpResponseBodyMessage, safeJSONParse } from 'src/utils/common';
import { ds } from '@utils/colors';

const InvestigateResolution = ({ row, handleClose, updateInvestigateSuccessSnackBar, isRevertTheDevelopment = false, cardId = '' }) => {
  const [requestBody, setRequestBody] = useState({});
  const [selectedOption, setSelectedOption] = useState('');
  const [loading, setLoading] = useState(false);
  const [validationError, setValidationError] = useState({
    imageChangeContainerName: '',
    imageNameWithTag: '',
  });

  const handleRadioChange = (e) => {
    const value = e.target.value;
    setSelectedOption(value);
    if (value === 'size') {
      setRequestBody({ size: requestBody.size, increase_replicas: '', restart: false, revert: false });
    } else if (value === 'increase_replicas') {
      setRequestBody({ size: '', increase_replicas: requestBody.increase_replicas, restart: false, revert: false });
    } else if (value === 'restart') {
      setRequestBody({ size: '', increase_replicas: '', restart: true, revert: false });
    } else if (value === 'revert') {
      setRequestBody({ size: '', increase_replicas: '', restart: false, revert: true });
    } else if (value === 'cordon') {
      setRequestBody({ cordon: true, drain: false });
    } else if (value === 'drain') {
      setRequestBody({ cordon: false, drain: true });
    }
  };

  const aggregationKey = row?.aggregation_key;

  const getNestedValue = (obj, path) => {
    return path.split(/(?<!\[[^\]]*)\./).reduce((acc, part) => {
      if (!acc) {
        return '';
      }
      let arrayMatch = part.match(/^(.+?)\[(\d+)\]$/);
      if (arrayMatch) {
        const [, key, index] = arrayMatch;
        return acc[key] ? acc[key][Number(index)] : '';
      }
      let bracketMatch = part.match(/^(.+?)\['(.+)'\]$/);
      if (bracketMatch) {
        const [, key, innerKey] = bracketMatch;
        return acc[key] ? acc[key][innerKey] : '';
      }
      return acc[part];
    }, obj);
  };

  const renderConditionalFields = () => {
    let existingVolume = '';
    let revertChanges = [];
    let diffExists = false;
    const json = row?.evidences.find((f) => f.type == 'json')?.data ?? '';
    if (json) {
      const jsonParsed = typeof json === 'string' ? safeJSONParse(json) : json;
      existingVolume = jsonParsed?.spec?.resources?.requests?.storage ?? '';
    }
    const diff = row?.evidences.find((f) => f.type == 'diff')?.data ?? '';
    if (diff) {
      diffExists = true;
      const oldData = yaml.load(diff.old);
      const newData = yaml.load(diff.new);
      revertChanges = diff.updated_paths.map((path) => {
        const updatedPath = path.replace(/^(StatefulSet|DaemonSet|Deployment|ReplicaSet)\./, '');
        const oldValue = getNestedValue(oldData, updatedPath);
        const newValue = getNestedValue(newData, updatedPath);
        return [
          {
            text: updatedPath,
          },
          {
            text: JSON.stringify(oldValue) ?? '',
          },
          {
            text: JSON.stringify(newValue) ?? '',
          },
        ];
      });
    }

    if (isRevertTheDevelopment) {
      return (
        <>
          {diff?.resource_name && <Typography>Resource Name: {diff?.resource_name}</Typography>}
          <CustomTable
            tableData={revertChanges}
            headers={['Path', 'Old Value', 'New Value']}
            totalRows={revertChanges?.length}
            rowsPerPage={revertChanges?.length}
          />
        </>
      );
    } else if (aggregationKey === 'KubePersistentVolumeFillingUp' || aggregationKey == 'KubernetesVolumeOutOfDiskSpace') {
      return (
        <div style={{ display: 'flex', flexDirection: 'column', gap: 'var(--ds-space-4)' }}>
          {existingVolume && <Typography>Current storage size: {existingVolume}</Typography>}
          <Box display={'grid'} gridTemplateColumns={'1fr 1fr'} gap={ds.space[3]}>
            <TextField
              value={requestBody.value}
              label='Increase Volume To'
              type='number'
              onChange={(e) => setRequestBody({ size: e.target.value })}
              InputProps={{
                endAdornment: (
                  <InputAdornment
                    position='end'
                    sx={{ '& p': { color: 'var(--ds-brand-300)', fontSize: 'var(--ds-text-small)', fontWeight: 'var(--ds-font-weight-regular)' } }}
                  >
                    Gi
                  </InputAdornment>
                ),
              }}
              size='small'
              required
            />
          </Box>
        </div>
      );
    } else if (aggregationKey === 'CPUThrottlingHigh' || aggregationKey === 'PodMemoryReachingLimit') {
      return (
        <div style={{ display: 'flex', flexDirection: 'column', gap: 'var(--ds-space-4)', my: 'var(--ds-space-4)' }}>
          <RadioGroup value={selectedOption} onChange={handleRadioChange}>
            <Box display={'flex'} flexDirection={'column'} gap={ds.space[3]}>
              <FormControlLabel
                value='size'
                control={<Radio />}
                label={
                  <TextField
                    value={requestBody.size || ''}
                    label={aggregationKey === 'CPUThrottlingHigh' ? 'Increase CPU To' : 'Increase Memory To'}
                    type='number'
                    onChange={(e) => setRequestBody({ size: e.target.value, increase_replicas: '', restart: false, revert: false })}
                    size='small'
                  />
                }
              />
              <FormControlLabel
                value='increase_replicas'
                control={<Radio />}
                label={
                  <TextField
                    value={requestBody.increase_replicas || ''}
                    label='Increase Replicas'
                    type='number'
                    onChange={(e) => setRequestBody({ size: '', increase_replicas: e.target.value, restart: false, revert: false })}
                    size='small'
                  />
                }
              />
              <FormControlLabel value='restart' control={<Radio />} label='Restart' />
              {diffExists && (
                <>
                  <FormControlLabel value='revert' control={<Radio />} label='Revert' />
                  {selectedOption === 'revert' && (
                    <CustomTable
                      tableData={revertChanges}
                      headers={['Path', 'Old Value', 'New Value']}
                      totalRows={revertChanges?.length}
                      rowsPerPage={revertChanges?.length}
                    />
                  )}
                </>
              )}
            </Box>
          </RadioGroup>
        </div>
      );
    } else if (aggregationKey === 'image_pull_backoff_reporter') {
      return (
        <div style={{ display: 'flex', flexDirection: 'column', gap: 'var(--ds-space-4)' }}>
          {diffExists ? (
            <>
              <Checkbox
                checked={!!requestBody.revert}
                onChange={(next) =>
                  setRequestBody((prev) => ({
                    ...prev,
                    revert: next,
                    imageChangeContainerName: '',
                    imageNameWithTag: '',
                  }))
                }
                label='Revert the Deployment'
              />
              <CustomTable
                tableData={revertChanges}
                headers={['Path', 'Old Value', 'New Value']}
                totalRows={revertChanges?.length}
                rowsPerPage={revertChanges?.length}
              />
            </>
          ) : (
            <></>
          )}
          <Typography>Mention Container Name and Image Tag to change</Typography>
          <br />
          <Box display={'grid'} gridTemplateColumns={'1fr 1fr'} gap={ds.space[3]}>
            <TextField
              value={requestBody.imageChangeContainerName || ''}
              label='Container Name'
              type='text'
              onChange={(e) => {
                setRequestBody((prev) => ({
                  ...prev,
                  imageChangeContainerName: e.target.value,
                  revert: false,
                }));
                setValidationError((prev) => ({ ...prev, imageChangeContainerName: '' }));
              }}
              size='small'
              sx={{ width: '100%' }}
              required
              helperText={validationError.imageChangeContainerName}
              error={!!validationError.imageChangeContainerName}
            />
            <TextField
              value={requestBody.imageNameWithTag || ''}
              label='Image (with Tag)'
              type='text'
              onChange={(e) => {
                setRequestBody((prev) => ({
                  ...prev,
                  imageNameWithTag: e.target.value,
                  revert: false,
                }));
                setValidationError((prev) => ({ ...prev, imageNameWithTag: '' }));
              }}
              size='small'
              sx={{ width: '100%' }}
              required
              helperText={validationError.imageNameWithTag}
              error={!!validationError.imageNameWithTag}
            />
          </Box>
        </div>
      );
    } else if (aggregationKey === 'KubeNodeNotReady') {
      return (
        <div style={{ display: 'flex', flexDirection: 'column', gap: 'var(--ds-space-4)' }}>
          <RadioGroup value={requestBody.cordon ? 'cordon' : requestBody.drain ? 'drain' : ''} onChange={handleRadioChange}>
            <FormControlLabel value='cordon' control={<Radio />} label='Cordon (Mark node as unschedulable.)' />
            <FormControlLabel
              value='drain'
              control={<Radio />}
              label='Node will be marked unschedulable to prevent new pods from arriving. drain evicts the pods if the API server supports eviction. Otherwise, it will use normal DELETE to delete the pods.'
            />
          </RadioGroup>
        </div>
      );
    }
    return null;
  };

  const validateValuesBeforeSubmit = () => {
    let valid = true;
    if (aggregationKey === 'KubePersistentVolumeFillingUp' || aggregationKey == 'KubernetesVolumeOutOfDiskSpace') {
      if (!requestBody.size) {
        snackbar.error('Required fields cannot be empty');
        valid = false;
      }
    } else if (aggregationKey === 'CPUThrottlingHigh' || aggregationKey === 'PodMemoryReachingLimit') {
      if (!requestBody.size && !requestBody.increase_replicas && !requestBody.restart && !requestBody.revert) {
        snackbar.error('Select atleast one type of resolution');
        valid = false;
      }
    } else if (aggregationKey === 'KubeNodeNotReady') {
      if (!requestBody.cordon && !requestBody.drain) {
        snackbar.error('Select atleast one type of resolution');
        valid = false;
      }
    } else if (aggregationKey === 'image_pull_backoff_reporter') {
      const errors = { imageChangeContainerName: '', imageNameWithTag: '' };

      if (!requestBody.imageChangeContainerName) {
        errors.imageChangeContainerName = 'Container Name is required';
        valid = false;
      }

      if (!requestBody.imageNameWithTag) {
        errors.imageNameWithTag = 'Image is required';
        valid = false;
      }

      setValidationError(errors);
    }
    return valid;
  };

  const handleSubmit = () => {
    if (!validateValuesBeforeSubmit()) {
      return;
    }
    setLoading(true);
    apiRecommendations
      .applyRecommendation(
        row?.cloud_account_id,
        row?.id,
        isRevertTheDevelopment
          ? {
              revert: true,
              ...(cardId && { card_id: cardId }),
            }
          : { ...requestBody, ...(cardId && { card_id: cardId }) },
        'kubernetes',
        {},
        'event'
      )
      .then((res) => {
        if (!res?.errors) {
          updateInvestigateSuccessSnackBar('success', 'Resolution applied successfully');
        } else {
          updateInvestigateSuccessSnackBar('error', `Failed to apply resolution ${parseHttpResponseBodyMessage(res)}`);
        }
      })
      .catch(() => {
        updateInvestigateSuccessSnackBar('error', 'Failed to apply resolution');
      })
      .finally(() => {
        setLoading(false);
        handleClose();
      });
  };

  return (
    <>
      <Box p={`${ds.space.mul(0, 10)} 0px`}>{renderConditionalFields()}</Box>
      <Box
        display='flex'
        alignItems='center'
        justifyContent='flex-end'
        gap={ds.space[3]}
        p={`${ds.space[4]} 0px`}
        sx={{
          borderTop: '0.5px solid var(--ds-gray-200)',
          button: {
            minWidth: ds.space.mul(0, 70),
          },
        }}
      >
        <CustomButton text={'Cancel'} size='Medium' onClick={handleClose} variant='secondary' />
        <CustomButton text={'Submit'} size='Medium' onClick={handleSubmit} loading={loading} />
      </Box>
    </>
  );
};

InvestigateResolution.propTypes = {
  row: PropTypes.object,
  handleClose: PropTypes.func,
  updateInvestigateSuccessSnackBar: PropTypes.func,
  isRevertTheDevelopment: PropTypes.bool,
  cardId: PropTypes.string,
};

export default InvestigateResolution;
