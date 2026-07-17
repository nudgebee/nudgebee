import { Fragment } from 'react';
import { Box, Typography } from '@mui/material';
import PropTypes from 'prop-types';
import { Divider } from '@ui/Divider';
import { ds } from 'src/utils/colors';

const InfographicList = ({ sequence }) => {
  return (
    <Box
      sx={{
        background: 'var(--ds-blue-200)',
        border: '0.5px solid var(--ds-blue-400)',
        borderRadius: 'var(--ds-radius-sm)',
        display: 'flex',
        height: ds.space.mul(0, 18),
        alignItems: 'center',
        p: '0 var(--ds-space-4)',
        boxShadow: `0px ${ds.space[1]} ${ds.space.mul(0, 3)} -1px var(--ds-blue-200)`,
      }}
    >
      {sequence &&
        sequence.length > 0 &&
        sequence.map((item) => {
          return (
            <Fragment key={item.text}>
              <Box sx={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', minWidth: ds.space.mul(0, 50) }}>
                <Typography
                  sx={{
                    fontWeight: 'var(--ds-font-weight-regular)',
                    fontSize: 'var(--ds-text-small)',
                    color: 'var(--ds-gray-700)',
                    mr: 'var(--ds-space-5)',
                  }}
                >
                  {item.text}
                </Typography>
                <Typography sx={{ fontWeight: 'var(--ds-font-weight-semibold)', fontSize: 'var(--ds-text-title)', color: 'var(--ds-gray-700)' }}>
                  {item.value}
                </Typography>
              </Box>
              {item !== sequence[sequence.length - 1] && (
                <Divider orientation='vertical' color='var(--ds-gray-400)' sx={{ mx: 'var(--ds-space-5)', my: 'var(--ds-space-2)' }} />
              )}
            </Fragment>
          );
        })}
    </Box>
  );
};

export default InfographicList;

InfographicList.propTypes = {
  sequence: PropTypes.array,
};
