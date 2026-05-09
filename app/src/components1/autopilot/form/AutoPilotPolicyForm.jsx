import apiDashboard from '@api1/home';
import CustomDropdown from '@components1/common/CustomDropdown';
import React, { useEffect, useState } from 'react';
import PropTypes from 'prop-types';
import { Box, Typography, TextField, Grid } from '@mui/material';
import apiUser from '@api1/user';
import CustomMultiDropdown from '@components1/common/CustomMultiDropdown';
import apiAutoPilot from '@api1/autoPilot';
import Loader from '@components1/common/Loader';
import CustomButton from '@components1/common/NewCustomButton';
import { colors } from 'src/utils/colors';
import { snackbar } from '@components1/common/snackbarService';

/**
 * @param {{
 *   handlePopupClose: () => void,
 *   policyId?: string,
 *   usedClusters?: any[]
 * }} props
 */

const AutoPilotPolicyForm = ({ handlePopupClose, policyId = '', usedClusters = [] }) => {
  const [clusterOption, setClusterOption] = useState([]);
  const [countReviewers, setCountReviewers] = useState(0);
  const [selectedCluster, setSelectedCluster] = useState();
  const [userOptions, setUserOptions] = useState([]);
  const [selectedReviewees, setSelectedReviewees] = useState([]);
  const [selectedReviewers, setSelectedReviewers] = useState([]);
  const [loading, setLoading] = useState(false);
  const [displayErrorsDesc, setDisplayErrorsDesc] = useState({
    account: '',
    countReviewers: '',
    reviewers: '',
    reviewees: '',
  });

  const getClustersData = async () => {
    try {
      setClusterOption([]);
      const response = await apiDashboard.getCloudAccounts('K8s');
      if (response && response.length > 0) {
        let clusters = response?.map((item) => ({
          label: item.account_name,
          value: item.id,
        }));

        if (!policyId) {
          clusters = clusters.filter((item) => !usedClusters.includes(item.value));
        }
        setClusterOption(clusters);
      }
    } catch (error) {
      console.error(error);
    }
  };

  const getPolicyData = (id) => {
    if (clusterOption.length) {
      setLoading(true);

      apiAutoPilot.getAutoPilotPolicyByPk(id).then((res) => {
        if (!res?.errors) {
          const data = res?.data?.auto_pilot_approval_policy_by_pk;
          setCountReviewers(data?.policy_attributes?.minimum_approval);
          setSelectedReviewees(data?.auto_pilot_reviewees?.map((item) => item.user.id));
          setSelectedReviewers(data?.auto_pilot_reviewers?.map((item) => item.reviwer_user.id));
          setSelectedCluster(data.account_id);
        }
        setLoading(false);
      });
    }
  };

  useEffect(() => {
    getClustersData();
  }, []);

  useEffect(() => {
    if (policyId != '') {
      getPolicyData(policyId);
    }
  }, [clusterOption]);

  useEffect(() => {
    let params = { status: 'active' };
    apiUser.listUsers(params).then((res) => {
      const userOptions = res?.data
        ?.filter((m) => m.username != '')
        ?.map((u) => ({
          label: u.username,
          value: u.id,
        }));
      setUserOptions(userOptions);
    });
  }, []);

  const handleSelectReviewee = (e) => {
    setSelectedReviewees(e?.target.value);
  };

  const handleSelectReviewer = (e) => {
    setSelectedReviewers(e?.target.value);
  };

  const handleSubmit = () => {
    let error = false;
    if (countReviewers > selectedReviewers.length) {
      setDisplayErrorsDesc((prev) => ({ ...prev, countReviewers: 'Minimum number of reviewers cannot be lesser than selected reviewers' }));
      error = true;
    } else {
      setDisplayErrorsDesc((prev) => ({ ...prev, countReviewers: '' }));
    }

    if (!selectedCluster?.value && !policyId) {
      setDisplayErrorsDesc((prev) => ({ ...prev, account: 'Please select a cluster' }));
      error = true;
    } else {
      setDisplayErrorsDesc((prev) => ({ ...prev, account: '' }));
    }

    if (!selectedReviewees || selectedReviewees.length == 0) {
      setDisplayErrorsDesc((prev) => ({ ...prev, reviewees: 'Please select atleast one reviewee' }));
      error = true;
    } else {
      setDisplayErrorsDesc((prev) => ({ ...prev, reviewees: '' }));
    }

    if (!selectedReviewers || selectedReviewers.length == 0) {
      setDisplayErrorsDesc((prev) => ({ ...prev, reviewers: 'Please select atleast one reviewer' }));
      error = true;
    } else {
      setDisplayErrorsDesc((prev) => ({ ...prev, reviewers: '' }));
    }

    if (error) {
      return;
    }

    if (policyId != '') {
      apiAutoPilot
        .updateAutoPilotPolicy(policyId, selectedCluster?.value ?? selectedCluster, countReviewers, selectedReviewees, selectedReviewers)
        .then((res) => {
          if (res?.data?.update_auto_pilot_policy?.id) {
            snackbar.success('AutoPilot Policy Updated !');
            handlePopupClose();
          } else {
            snackbar.error(res?.errors[0].message);
          }
        });
    } else {
      apiAutoPilot
        .createAutoPilotPolicy(selectedCluster?.value ?? selectedCluster, countReviewers, selectedReviewees, selectedReviewers)
        .then((res) => {
          if (res?.data?.create_auto_pilot_policy?.id) {
            snackbar.success('AutoPilot Policy Created !');
            handlePopupClose();
          } else {
            snackbar.error(res?.errors[0].message);
          }
        });
    }
  };

  return (
    <>
      <Box display='flex' flexDirection={'column'} justifyContent='space-between' gap='12px' p='16px 18px'>
        {loading && <Loader />}

        <Box>
          <CustomDropdown
            options={clusterOption}
            label={'Select Cluster'}
            onChange={(event, v) => {
              setSelectedCluster(v);
            }}
            value={selectedCluster ?? ''}
            minHeight='38px'
            minWidth='200px'
            inputLabelSx={{ fontWeight: 400, fontSize: '16px' }}
            isDisabled={loading || policyId != ''}
            showNormalField
            isRequired
          />
          <Box>
            {displayErrorsDesc.account ? (
              <Typography sx={{ color: colors.errorText, fontSize: '14px' }}>{displayErrorsDesc.account}</Typography>
            ) : null}
          </Box>
        </Box>
        <Grid container spacing={2}>
          <Grid item md={12}>
            <CustomMultiDropdown
              label='Add reviewees'
              value={selectedReviewees}
              options={userOptions}
              onChange={(e) => {
                handleSelectReviewee(e);
              }}
              handleCloseIcon={(data) => {
                setSelectedReviewees(data);
                if (data && data.length == 0) {
                  setSelectedReviewees([]);
                }
              }}
              minHeight='40px'
              minWidth='100%'
              maxWidth='100%'
              inputLabelSx={{ fontWeight: 400, fontSize: '16px' }}
              isDisabled={loading}
              isRequired
              enableSearch
            />
            <Box>
              {displayErrorsDesc.reviewees ? (
                <Typography sx={{ color: colors.errorText, fontSize: '14px' }}>{displayErrorsDesc.reviewees}</Typography>
              ) : null}
            </Box>
          </Grid>
          <Grid item md={12}>
            <CustomMultiDropdown
              label='Add reviewers'
              value={selectedReviewers}
              options={userOptions}
              onChange={(e) => {
                handleSelectReviewer(e);
              }}
              handleCloseIcon={(data) => {
                setSelectedReviewers(data);
                if (data && data.length == 0) {
                  setSelectedReviewers([]);
                }
              }}
              minHeight='40px'
              minWidth='100%'
              maxWidth='100%'
              inputLabelSx={{ fontWeight: 400, fontSize: '16px' }}
              isDisabled={loading}
              isRequired
              enableSearch
            />
            <Box>
              {displayErrorsDesc.reviewers ? (
                <Typography sx={{ color: colors.errorText, fontSize: '14px' }}>{displayErrorsDesc.reviewers}</Typography>
              ) : null}
            </Box>
          </Grid>
        </Grid>
        <Box>
          <Typography sx={{ color: colors.text.tertiary, fontSize: '12px', fontWeight: 400, mb: '6px' }}>Minimum number of reviewers*</Typography>
          <TextField
            InputProps={{
              inputProps: { min: 0 },
            }}
            sx={{
              '&.MuiFormControl-root': {
                maxWidth: '110px',
              },
              '& .MuiInputBase-root': {
                height: '36px',
              },
            }}
            size='small'
            value={countReviewers}
            fullWidth
            type='number'
            onChange={(e) => {
              const value = e.target.value;
              if (value != null && value != undefined) {
                setCountReviewers(parseInt(value));
              }
            }}
            onKeyDown={(e) => {
              if (e.key === '-') {
                e.preventDefault();
              }
            }}
          />
        </Box>
        <Box>
          {displayErrorsDesc.countReviewers ? (
            <Typography sx={{ color: colors.errorText, fontSize: '14px', marginTop: '4px' }}>{displayErrorsDesc.countReviewers}</Typography>
          ) : null}
        </Box>
      </Box>
      <Box
        display='flex'
        alignItems='center'
        justifyContent='flex-end'
        gap='12px'
        m='16px'
        pt='16px'
        sx={{ borderTop: `0.5px solid ${colors.border.vertical}`, '& button': { minWidth: '140px' } }}
      >
        <CustomButton text={'Cancel'} onClick={handlePopupClose} isDisabled={loading} size='Medium' variant='secondary' />
        <CustomButton text={policyId ? 'Update Policy' : 'Create Policy'} onClick={handleSubmit} isDisabled={loading} size='Medium' />
      </Box>
    </>
  );
};
AutoPilotPolicyForm.propTypes = {
  handlePopupClose: PropTypes.func.isRequired,
  policyId: PropTypes.string,
  usedClusters: PropTypes.array,
};

export default AutoPilotPolicyForm;
