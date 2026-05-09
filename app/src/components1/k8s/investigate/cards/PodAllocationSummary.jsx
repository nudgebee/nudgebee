import { formatMemory } from '@lib/formatter';
import { Box, Stack, Typography } from '@mui/material';
import PropTypes from 'prop-types';

const labelSx = {
  color: 'var(--grey-80, #9F9F9F)',
  fontSize: '14px',
  fontWeight: 500,
};

const valueSx = {
  color: 'var(--Data-Points-main, #374151)',
  fontSize: '14px',
  fontWeight: 500,
  overflow: 'hidden',
  textOverflow: 'ellipsis',
  whiteSpace: 'nowrap',
};

const PodAllocationSummary = ({ podMemoryAllocationItem }) => {
  return (
    <Box mt={'20px'}>
      <Stack direction={'row'} justifyContent={'space-between'}>
        <Stack direction={'row'} sx={{ minWidth: 0, flex: 1, marginRight: '20px' }}>
          <Box sx={{ minWidth: 0, maxWidth: '400px', minHeight: '50px' }}>
            {podMemoryAllocationItem?.pod ? (
              <Typography
                component='div'
                sx={{
                  display: 'flex',
                  alignItems: 'baseline',
                  gap: '4px',
                  marginBottom: '4px',
                  minWidth: 0,
                }}
              >
                <Box component='span' sx={{ ...labelSx, flexShrink: 0 }}>
                  Pod:
                </Box>
                <Box component='span' sx={{ ...valueSx, minWidth: 0 }} title={podMemoryAllocationItem.pod}>
                  {podMemoryAllocationItem.pod}
                </Box>
              </Typography>
            ) : null}
            {podMemoryAllocationItem?.container ? (
              <Typography
                component='div'
                sx={{
                  display: 'flex',
                  alignItems: 'baseline',
                  gap: '4px',
                  marginBottom: '4px',
                  minWidth: 0,
                }}
              >
                <Box component='span' sx={{ ...labelSx, flexShrink: 0 }}>
                  Container:
                </Box>
                <Box component='span' sx={{ ...valueSx, minWidth: 0 }} title={podMemoryAllocationItem.container}>
                  {podMemoryAllocationItem.container}
                </Box>
              </Typography>
            ) : null}
          </Box>
        </Stack>
        <Stack gap={'20px'} direction={'row'} sx={{ flexShrink: 0 }}>
          <Box sx={{ display: 'flex' }}>
            {podMemoryAllocationItem?.request ? (
              <Box>
                <Typography
                  sx={{
                    color: 'var(--grey-80, #9F9F9F)',
                    display: 'block',
                    fontSize: '14px',
                    fontWeight: 500,
                    marginBottom: '5px',
                  }}
                >
                  Memory (Req)
                </Typography>
                <Typography
                  sx={{
                    color: 'var(--Data-Points-main, #374151)',
                    display: 'block',
                    fontSize: '16px',
                    fontWeight: 500,
                    marginBottom: '5px',
                    textAlign: 'right',
                  }}
                >
                  {formatMemory(podMemoryAllocationItem?.request)}
                </Typography>
              </Box>
            ) : null}
            {podMemoryAllocationItem?.limits ? (
              <Box marginLeft={'30px'}>
                <Typography
                  sx={{
                    color: 'var(--grey-80, #9F9F9F)',
                    display: 'block',
                    fontSize: '14px',
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
                    fontSize: '16px',
                    fontWeight: 500,
                    marginBottom: '5px',
                    textAlign: 'right',
                  }}
                >
                  {formatMemory(podMemoryAllocationItem?.limits)}
                </Typography>
              </Box>
            ) : null}
            {podMemoryAllocationItem?.cpu_request ? (
              <Box marginLeft={'30px'}>
                <Typography
                  sx={{
                    color: 'var(--grey-80, #9F9F9F)',
                    display: 'block',
                    fontSize: '14px',
                    fontWeight: 500,
                    marginBottom: '5px',
                  }}
                >
                  CPU (Req)
                </Typography>
                <Typography
                  sx={{
                    color: 'var(--Data-Points-main, #374151)',
                    display: 'block',
                    fontSize: '16px',
                    fontWeight: 500,
                    marginBottom: '5px',
                    textAlign: 'right',
                  }}
                >
                  {podMemoryAllocationItem?.cpu_request}
                </Typography>
              </Box>
            ) : null}
            {podMemoryAllocationItem?.cpu_limit ? (
              <Box marginLeft={'30px'}>
                <Typography
                  sx={{
                    color: 'var(--grey-80, #9F9F9F)',
                    display: 'block',
                    fontSize: '14px',
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
                    fontSize: '16px',
                    fontWeight: 500,
                    marginBottom: '5px',
                    textAlign: 'right',
                  }}
                >
                  {podMemoryAllocationItem?.cpu_limit}
                </Typography>
              </Box>
            ) : null}
          </Box>
        </Stack>
      </Stack>
    </Box>
  );
};
PodAllocationSummary.propTypes = {
  podMemoryAllocationItem: PropTypes.object,
};

export default PodAllocationSummary;
