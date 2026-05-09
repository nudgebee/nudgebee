import React from 'react';
import { Box } from '@mui/material';
import Currency from '@components1/common/format/Currency';
import PropTypes from 'prop-types';
import { Text } from '@components1/common';
import ThreeDotLoader from '@components1/common/ThreeDotLoader';

const ClusterPotentialSaving = ({ savingPotentialSummary = {}, loading = false }) => {
  return (
    <Box
      sx={{
        borderRadius: '10px',
        background: '#F0FDF9',
        minHeight: '110px',
        position: 'relative',
        height: '100%',
        p: '18px',
        boxSizing: 'border-box',
        border: '1px solid #A7F3D0',
      }}
    >
      <Box>
        <Text value={'Savings Potential'} sx={{ fontWeight: 500 }} />
        {loading ? (
          <div style={{ marginLeft: '30px', marginTop: '10px' }}>
            <ThreeDotLoader />
          </div>
        ) : (
          <>
            <Currency
              sx={{ fontSize: '22px', fontWeight: 500 }}
              sxPrefix={{ fontSize: '16px' }}
              sxSuffix={{ fontSize: '14px' }}
              value={savingPotentialSummary?.yearly_recommendation_saving ?? '-'}
              suffix='/yr'
              isSavingPotential={true}
              recommendationLabel='Some of cluster recommendations'
            />
            <Currency
              sx={{ fontSize: '22px', fontWeight: 500 }}
              sxPrefix={{ fontSize: '16px' }}
              sxSuffix={{ fontSize: '14px' }}
              value={
                savingPotentialSummary?.yearly_recommendation_saving && !isNaN(savingPotentialSummary.yearly_recommendation_saving)
                  ? (savingPotentialSummary.yearly_recommendation_saving / 12).toFixed(2)
                  : '-'
              }
              suffix='/mo'
              isSavingPotential={true}
              recommendationLabel='Some of cluster recommendations'
            />
          </>
        )}
      </Box>
    </Box>
  );
};

export default ClusterPotentialSaving;

ClusterPotentialSaving.propTypes = {
  savingPotentialSummary: PropTypes.any,
};
