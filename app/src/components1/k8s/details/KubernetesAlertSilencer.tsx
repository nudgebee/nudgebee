import CustomDropdown from '@components1/common/CustomDropdown';
import { Box, TextField } from '@mui/material';
import dayjs, { type Dayjs } from 'dayjs';
import React, { useState } from 'react';
import DateTimeRangePicker from '@components1/k8s/common/DateTimeRangePicker';
import { inputCustomSx } from '@data/themes/inputField';
import CustomButton from '@components1/common/NewCustomButton';
import { snackbar } from '@components1/common/snackbarService';
import k8sApi from '@api1/kubernetes';
import { getUserSession } from '@lib/auth';
import { formatDate } from '@lib/formatter';

interface KubernetesAlertSilencerProps {
  accountId: string;
  alertData: any;
  keyNotEditable?: boolean;
  handleCloseSilencePopUp: () => void;
  filterTypes: any;
  isEdit?: boolean;
  onSuccess?: () => void;
  silenceId?: string;
  comment?: string;
}

const KubernetesAlertSilencer: React.FC<KubernetesAlertSilencerProps> = ({
  accountId,
  alertData,
  keyNotEditable = true,
  handleCloseSilencePopUp,
  filterTypes,
  isEdit = false,
  onSuccess,
  silenceId,
  comment,
}) => {
  const [startDate, setStartDate] = useState(dayjs(new Date()).valueOf());
  const [endDate, setEndDate] = useState(dayjs().add(1, 'month').valueOf());
  const [loading, setLoading] = useState(false);
  const [selectedAlertData, setSelectedAlertData] = useState(alertData);
  const [selectedFilterTypes, setSelectedFilterTypes] = useState(filterTypes);

  const handleChange = (field: string, value: string) => {
    setSelectedAlertData((prevData: any) => ({
      ...prevData,
      [field]: value,
    }));
  };

  const handleFilterTypeChange = (field: string, filterType: string) => {
    setSelectedFilterTypes((prevFilterTypes: any) => ({
      ...prevFilterTypes,
      [field]: filterType,
    }));
  };

  const handleStartDateEndDate = (type: string, date: Dayjs | null) => {
    if (date != null) {
      if (type == 'start') {
        setStartDate(date.valueOf());
      } else if (type == 'end') {
        setEndDate(date.valueOf());
      }
    } else {
      snackbar.error(`Please select correct ${type} date`);
    }
  };

  const handleSubmit = () => {
    if (isEdit) {
      handleSubmitEdit();
    } else {
      handleCreateSubmit();
    }
  };

  const handleCreateSubmit = () => {
    setLoading(true);
    let matchers = [];
    for (const [key, value] of Object.entries(selectedAlertData)) {
      let matcherObject: any = {};
      matcherObject['name'] = key;
      matcherObject['value'] = value;
      matcherObject['isEqual'] = key == 'alertname' ? true : selectedFilterTypes[key] === 'EQUAL';
      matcherObject['isRegex'] = key == 'alertname' ? false : selectedFilterTypes[key] === 'REGEX';
      matchers.push(matcherObject);
    }
    const data = {
      no_sinks: true,
      body: {
        account_id: accountId,
        action_name: 'add_silence',
        action_params: {
          createdBy: getUserSession()?.user?.email,
          startsAt: formatDate(startDate),
          endsAt: formatDate(endDate),
          matchers: matchers,
          comment: 'From Nudgebee UI',
        },
      },
      cache: false,
    };
    k8sApi
      .relayForwardRequest(data)
      .then((res: any) => {
        if (res?.data.success) {
          const evidence = res?.data?.findings[0].evidence[0];
          if (evidence?.data) {
            const parsedData = JSON.parse(evidence.data);
            if (parsedData) {
              if (parsedData?.[0]?.data?.rows) {
                snackbar.success('Silence Alert created successfully');
                handleCloseSilencePopUp();
                if (onSuccess) {
                  onSuccess();
                }
              }
            }
          }
        } else {
          snackbar.error('Failed to create Alert Silence');
        }
      })
      .catch(() => {
        console.error('failed to create alert');
      })
      .finally(() => {
        setLoading(false);
      });
  };

  const handleSubmitEdit = async () => {
    if (!silenceId) {
      return;
    }

    const matchers = Object.entries(selectedAlertData).map(([key, value]) => ({
      name: key,
      value: value,
      isEqual: key === 'alertname' ? true : selectedFilterTypes[key] === 'EQUAL',
      isRegex: key === 'alertname' ? false : selectedFilterTypes[key] === 'REGEX',
    }));

    setLoading(true);

    const commentText = comment || 'Updated from Nudgebee UI';

    const baseActionParams = {
      createdBy: getUserSession()?.user?.email,
      startsAt: formatDate(startDate),
      endsAt: formatDate(endDate),
      matchers: matchers,
      comment: commentText,
    };

    const updateData = {
      no_sinks: true,
      body: {
        account_id: accountId,
        action_name: 'add_silence',
        action_params: { ...baseActionParams, id: silenceId },
      },
      cache: false,
    };

    try {
      let res = await k8sApi.relayForwardRequest(updateData);

      if (res?.data?.success) {
        snackbar.success(`Successfully updated silence: ${commentText}`);
        if (onSuccess) {
          onSuccess();
        }
        return;
      }

      // If update failed, try creating a new one
      snackbar.warning('Could not update existing silence with same ID. Creating as new...');
      const createData = {
        no_sinks: true,
        body: {
          account_id: accountId,
          action_name: 'add_silence',
          action_params: baseActionParams,
        },
        cache: false,
      };
      res = await k8sApi.relayForwardRequest(createData);

      if (res?.data?.success) {
        snackbar.success(`Successfully created new silence alert`);
        if (onSuccess) {
          onSuccess();
        }
      } else if (res) {
        snackbar.error(`Failed to update silence: ${res?.data?.message || 'Unknown error'}`);
      }
    } catch (error: any) {
      console.error('Error updating silence alert:', error);
      snackbar.error(`Failed to update silence: ${error.message || 'Unknown error'}`);
    } finally {
      handleCloseSilencePopUp();
      setLoading(false);
    }
  };

  const handleValidateAndSubmit = () => {
    if (endDate.valueOf() < Date.now()) {
      snackbar.error('Past time not allowed');
      return;
    }
    if (startDate.valueOf() > endDate.valueOf()) {
      snackbar.error('Start time cannot be after end time');
      return;
    }
    handleSubmit();
  };

  return (
    <Box p={5}>
      {Object.keys(selectedAlertData)
        .filter((m) => m === 'container_id' || m == 'severity' || m == 'alert_name' || m == 'path')
        .map((field: string) => (
          <Box key={field} display='flex' alignItems='center' width='100%' mb={3} gap={'20px'}>
            <TextField value={field} variant='outlined' size='small' disabled={keyNotEditable} sx={{ ...inputCustomSx, marginTop: '10px' }} />
            <CustomDropdown
              value={selectedFilterTypes[field]}
              onChange={(e: React.ChangeEvent<HTMLInputElement>) => handleFilterTypeChange(field, e.target.value)}
              options={['EQUAL', 'REGEX']}
              label={'Matcher'}
              minWidth={'180px'}
              showNormalField={true}
            />
            <TextField
              value={selectedAlertData[field]}
              size='small'
              sx={{ ...inputCustomSx, marginTop: '10px' }}
              variant='outlined'
              onChange={(e) => handleChange(field, e.target.value)}
            />
          </Box>
        ))}
      <Box sx={{ display: 'flex', gap: '20px' }}>
        <DateTimeRangePicker
          handleStartDateEndDate={handleStartDateEndDate}
          startDate={dayjs(startDate)}
          endDate={dayjs(endDate)}
          views={['day', 'hours', 'minutes']}
          minDate={dayjs()}
          maxDateTime={null}
          disableStartDate={false}
        />
      </Box>
      <Box display='flex' alignItems='center' justifyContent='flex-end' gap='12px' pt='24px' sx={{ '& button': { minWidth: '140px' } }}>
        <CustomButton text={'Cancel'} variant='secondary' size='Medium' onClick={handleCloseSilencePopUp} />
        <CustomButton text={isEdit ? 'Update Silence' : 'Silence Alert'} size='Medium' onClick={handleValidateAndSubmit} loading={loading} />
      </Box>
    </Box>
  );
};

export default KubernetesAlertSilencer;
