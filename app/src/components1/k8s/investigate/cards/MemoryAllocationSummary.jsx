import { formatMemory } from '@lib/formatter';
import { ds } from '@utils/colors';
import { Box, Stack, Typography } from '@mui/material';
import PropTypes from 'prop-types';

const MemoryAllocationSummary = ({ memoryAllocationItem }) => {
  return (
    <Box mt={ds.space.mul(0, 10)}>
      <Stack direction={'row'} justifyContent={'space-between'}>
        <Stack direction={'row'}>
          <Box>
            {memoryAllocationItem?.container ? (
              <>
                <Typography
                  sx={{
                    color: 'var(--grey-80, var(--ds-gray-400))',
                    display: 'block',
                    fontSize: 'var(--ds-text-body)',
                    fontWeight: 'var(--ds-font-weight-medium)',
                    marginBottom: 'var(--ds-space-1)',
                  }}
                >
                  Container
                </Typography>
                <Typography
                  sx={{
                    color: 'var(--Data-Points-main, var(--ds-brand-500))',
                    display: 'block',
                    fontSize: 'var(--ds-text-body-lg)',
                    fontWeight: 'var(--ds-font-weight-medium)',
                    marginBottom: 'var(--ds-space-1)',
                  }}
                >
                  {memoryAllocationItem?.container}
                </Typography>
              </>
            ) : null}
          </Box>
        </Stack>
        <Stack gap={ds.space.mul(0, 10)} direction={'row'}>
          <Box
            sx={{
              display: 'flex',
            }}
          >
            {memoryAllocationItem?.request?.cpu ? (
              <Box>
                <Typography
                  sx={{
                    color: 'var(--grey-80, var(--ds-gray-400))',
                    display: 'block',
                    fontSize: 'var(--ds-text-body)',
                    fontWeight: 'var(--ds-font-weight-medium)',
                    marginBottom: 'var(--ds-space-1)',
                  }}
                >
                  CPU (Requested)
                </Typography>
                <Typography
                  sx={{
                    color: 'var(--Data-Points-main, var(--ds-brand-500))',
                    display: 'block',
                    fontSize: 'var(--ds-text-body-lg)',
                    fontWeight: 'var(--ds-font-weight-medium)',
                    marginBottom: 'var(--ds-space-1)',
                    textAlign: 'right',
                  }}
                >
                  {memoryAllocationItem?.request?.cpu}
                </Typography>
              </Box>
            ) : null}
            {memoryAllocationItem?.limits?.cpu ? (
              <Box margin={`0 ${ds.space.mul(0, 15)}`}>
                <Typography
                  sx={{
                    color: 'var(--grey-80, var(--ds-gray-400))',
                    display: 'block',
                    fontSize: 'var(--ds-text-body)',
                    fontWeight: 'var(--ds-font-weight-medium)',
                    marginBottom: 'var(--ds-space-1)',
                  }}
                >
                  CPU (Limit)
                </Typography>
                <Typography
                  sx={{
                    color: 'var(--Data-Points-main, var(--ds-brand-500))',
                    display: 'block',
                    fontSize: 'var(--ds-text-body-lg)',
                    fontWeight: 'var(--ds-font-weight-medium)',
                    marginBottom: 'var(--ds-space-1)',
                    textAlign: 'right',
                  }}
                >
                  {memoryAllocationItem?.limits?.cpu}
                </Typography>
              </Box>
            ) : null}
          </Box>
          <Box sx={{ display: 'flex' }}>
            {memoryAllocationItem?.request?.memory ? (
              <Box>
                <Typography
                  sx={{
                    color: 'var(--grey-80, var(--ds-gray-400))',
                    display: 'block',
                    fontSize: 'var(--ds-text-body)',
                    fontWeight: 'var(--ds-font-weight-medium)',
                    marginBottom: 'var(--ds-space-1)',
                  }}
                >
                  Memory (Requested)
                </Typography>
                <Typography
                  sx={{
                    color: 'var(--Data-Points-main, var(--ds-brand-500))',
                    display: 'block',
                    fontSize: 'var(--ds-text-body-lg)',
                    fontWeight: 'var(--ds-font-weight-medium)',
                    marginBottom: 'var(--ds-space-1)',
                    textAlign: 'right',
                  }}
                >
                  {formatMemory(memoryAllocationItem?.request?.memory)}
                </Typography>
              </Box>
            ) : null}
            {memoryAllocationItem?.limits?.memory ? (
              <Box marginLeft={ds.space.mul(0, 15)}>
                <Typography
                  sx={{
                    color: 'var(--grey-80, var(--ds-gray-400))',
                    display: 'block',
                    fontSize: 'var(--ds-text-body)',
                    fontWeight: 'var(--ds-font-weight-medium)',
                    marginBottom: 'var(--ds-space-1)',
                  }}
                >
                  Memory (Limit)
                </Typography>
                <Typography
                  sx={{
                    color: 'var(--Data-Points-main, var(--ds-brand-500))',
                    display: 'block',
                    fontSize: 'var(--ds-text-body-lg)',
                    fontWeight: 'var(--ds-font-weight-medium)',
                    marginBottom: 'var(--ds-space-1)',
                    textAlign: 'right',
                  }}
                >
                  {formatMemory(memoryAllocationItem?.limits?.memory)}
                </Typography>
              </Box>
            ) : null}
          </Box>
        </Stack>
      </Stack>
    </Box>
  );
};
MemoryAllocationSummary.propTypes = {
  memoryAllocationItem: PropTypes.object,
};

export default MemoryAllocationSummary;
