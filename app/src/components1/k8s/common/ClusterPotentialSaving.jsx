import React from 'react';
import { Box } from '@mui/material';
import Currency from '@common-new/format/Currency';
import PropTypes from 'prop-types';
import Text from '@common-new/format/Text';
import ThreeDotLoader from '@components1/common/ThreeDotLoader';
import { ds } from '@utils/colors';

const ClusterPotentialSaving = ({ savingPotentialSummary = {}, loading = false }) => {
  return (
    <Box
      sx={{
        borderRadius: 'var(--ds-radius-lg)',
        background: 'var(--ds-teal-50)',
        minHeight: ds.space.mul(0, 55),
        position: 'relative',
        height: '100%',
        p: 'var(--ds-space-4)',
        boxSizing: 'border-box',
        border: '1px solid var(--ds-green-200)',
      }}
    >
      <Box>
        <Text value={'Savings Potential'} sx={{ fontWeight: 'var(--ds-font-weight-medium)' }} />
        {loading ? (
          <div style={{ marginLeft: 'var(--ds-space-6)', marginTop: 'var(--ds-space-2)' }}>
            <ThreeDotLoader />
          </div>
        ) : (
          <>
            <Currency
              sx={{ fontSize: 'var(--ds-text-heading)', fontWeight: 'var(--ds-font-weight-medium)' }}
              sxPrefix={{ fontSize: 'var(--ds-text-title)' }}
              sxSuffix={{ fontSize: 'var(--ds-text-body-lg)' }}
              value={savingPotentialSummary?.yearly_recommendation_saving ?? '-'}
              suffix='/yr'
              isSavingPotential={true}
              recommendationLabel='Some of cluster recommendations'
            />
            <Currency
              sx={{ fontSize: 'var(--ds-text-heading)', fontWeight: 'var(--ds-font-weight-medium)' }}
              sxPrefix={{ fontSize: 'var(--ds-text-title)' }}
              sxSuffix={{ fontSize: 'var(--ds-text-body-lg)' }}
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
