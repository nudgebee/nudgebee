import { formatMemory } from '@lib/formatter';
import { Box, Stack, Typography } from '@mui/material';
import PropTypes from 'prop-types';

const MemoryAllocationSummary = ({ memoryAllocationItem }) => {
  return (
    <Box mt={'20px'}>
      <Stack direction={'row'} justifyContent={'space-between'}>
        <Stack direction={'row'}>
          <Box>
            {memoryAllocationItem?.container ? (
              <>
                <Typography
                  sx={{
                    color: 'var(--grey-80, #9F9F9F)',
                    display: 'block',
                    fontSize: '13px',
                    fontWeight: 500,
                    marginBottom: '5px',
                  }}
                >
                  Container
                </Typography>
                <Typography
                  sx={{
                    color: 'var(--Data-Points-main, #374151)',
                    display: 'block',
                    fontSize: '14px',
                    fontWeight: 500,
                    marginBottom: '5px',
                  }}
                >
                  {memoryAllocationItem?.container}
                </Typography>
              </>
            ) : null}
          </Box>
        </Stack>
        <Stack gap={'20px'} direction={'row'}>
          <Box
            sx={{
              display: 'flex',
            }}
          >
            {memoryAllocationItem?.request?.cpu ? (
              <Box>
                <Typography
                  sx={{
                    color: 'var(--grey-80, #9F9F9F)',
                    display: 'block',
                    fontSize: '13px',
                    fontWeight: 500,
                    marginBottom: '5px',
                  }}
                >
                  CPU (Requested)
                </Typography>
                <Typography
                  sx={{
                    color: 'var(--Data-Points-main, #374151)',
                    display: 'block',
                    fontSize: '14px',
                    fontWeight: 500,
                    marginBottom: '5px',
                    textAlign: 'right',
                  }}
                >
                  {memoryAllocationItem?.request?.cpu}
                </Typography>
              </Box>
            ) : null}
            {memoryAllocationItem?.limits?.cpu ? (
              <Box margin={'0 30px'}>
                <Typography
                  sx={{
                    color: 'var(--grey-80, #9F9F9F)',
                    display: 'block',
                    fontSize: '13px',
                    fontWeight: 500,
                    marginBottom: '5px',
                  }}
                >
                  CPU (Limit)
                </Typography>
                <Typography
                  sx={{
                    color: 'var(--Data-Points-main, #374151)',
                    display: 'block',
                    fontSize: '14px',
                    fontWeight: 500,
                    marginBottom: '5px',
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
                    color: 'var(--grey-80, #9F9F9F)',
                    display: 'block',
                    fontSize: '13px',
                    fontWeight: 500,
                    marginBottom: '5px',
                  }}
                >
                  Memory (Requested)
                </Typography>
                <Typography
                  sx={{
                    color: 'var(--Data-Points-main, #374151)',
                    display: 'block',
                    fontSize: '14px',
                    fontWeight: 500,
                    marginBottom: '5px',
                    textAlign: 'right',
                  }}
                >
                  {formatMemory(memoryAllocationItem?.request?.memory)}
                </Typography>
              </Box>
            ) : null}
            {memoryAllocationItem?.limits?.memory ? (
              <Box marginLeft={'30px'}>
                <Typography
                  sx={{
                    color: 'var(--grey-80, #9F9F9F)',
                    display: 'block',
                    fontSize: '13px',
                    fontWeight: 500,
                    marginBottom: '5px',
                  }}
                >
                  Memory (Limit)
                </Typography>
                <Typography
                  sx={{
                    color: 'var(--Data-Points-main, #374151)',
                    display: 'block',
                    fontSize: '14px',
                    fontWeight: 500,
                    marginBottom: '5px',
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
