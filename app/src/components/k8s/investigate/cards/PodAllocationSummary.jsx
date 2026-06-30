import { formatMemory } from '@lib/formatter';
import { Box, Stack, Typography } from '@mui/material';
import PropTypes from 'prop-types';
import { ds } from '@utils/colors';

const labelSx = {
  color: 'var(--grey-80, var(--ds-gray-400))',
  fontSize: 'var(--ds-text-body-lg)',
  fontWeight: 'var(--ds-font-weight-medium)',
};

const valueSx = {
  color: 'var(--Data-Points-main, var(--ds-brand-500))',
  fontSize: 'var(--ds-text-body-lg)',
  fontWeight: 'var(--ds-font-weight-medium)',
  overflow: 'hidden',
  textOverflow: 'ellipsis',
  whiteSpace: 'nowrap',
};

const PodAllocationSummary = ({ podMemoryAllocationItem }) => {
  return (
    <Box mt={ds.space.mul(0, 10)}>
      <Stack direction={'row'} justifyContent={'space-between'}>
        <Stack direction={'row'} sx={{ minWidth: 0, flex: 1, marginRight: 'var(--ds-space-4)' }}>
          <Box sx={{ minWidth: 0, maxWidth: ds.space.mul(0, 200), minHeight: ds.space.mul(0, 25) }}>
            {podMemoryAllocationItem?.pod ? (
              <Typography
                component='div'
                sx={{
                  display: 'flex',
                  alignItems: 'baseline',
                  gap: 'var(--ds-space-1)',
                  marginBottom: 'var(--ds-space-1)',
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
                  gap: 'var(--ds-space-1)',
                  marginBottom: 'var(--ds-space-1)',
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
        <Stack gap={ds.space.mul(0, 10)} direction={'row'} sx={{ flexShrink: 0 }}>
          <Box sx={{ display: 'flex' }}>
            {podMemoryAllocationItem?.request ? (
              <Box>
                <Typography
                  sx={{
                    color: 'var(--grey-80, var(--ds-gray-400))',
                    display: 'block',
                    fontSize: 'var(--ds-text-body-lg)',
                    fontWeight: 'var(--ds-font-weight-medium)',
                    marginBottom: 'var(--ds-space-1)',
                  }}
                >
                  Memory (Req)
                </Typography>
                <Typography
                  sx={{
                    color: 'var(--Data-Points-main, var(--ds-brand-500))',
                    display: 'block',
                    fontSize: 'var(--ds-text-title)',
                    fontWeight: 'var(--ds-font-weight-medium)',
                    marginBottom: 'var(--ds-space-1)',
                    textAlign: 'right',
                  }}
                >
                  {formatMemory(podMemoryAllocationItem?.request)}
                </Typography>
              </Box>
            ) : null}
            {podMemoryAllocationItem?.limits ? (
              <Box marginLeft={ds.space.mul(0, 15)}>
                <Typography
                  sx={{
                    color: 'var(--grey-80, var(--ds-gray-400))',
                    display: 'block',
                    fontSize: 'var(--ds-text-body-lg)',
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
                    fontSize: 'var(--ds-text-title)',
                    fontWeight: 'var(--ds-font-weight-medium)',
                    marginBottom: 'var(--ds-space-1)',
                    textAlign: 'right',
                  }}
                >
                  {formatMemory(podMemoryAllocationItem?.limits)}
                </Typography>
              </Box>
            ) : null}
            {podMemoryAllocationItem?.cpu_request ? (
              <Box marginLeft={ds.space.mul(0, 15)}>
                <Typography
                  sx={{
                    color: 'var(--grey-80, var(--ds-gray-400))',
                    display: 'block',
                    fontSize: 'var(--ds-text-body-lg)',
                    fontWeight: 'var(--ds-font-weight-medium)',
                    marginBottom: 'var(--ds-space-1)',
                  }}
                >
                  CPU (Req)
                </Typography>
                <Typography
                  sx={{
                    color: 'var(--Data-Points-main, var(--ds-brand-500))',
                    display: 'block',
                    fontSize: 'var(--ds-text-title)',
                    fontWeight: 'var(--ds-font-weight-medium)',
                    marginBottom: 'var(--ds-space-1)',
                    textAlign: 'right',
                  }}
                >
                  {podMemoryAllocationItem?.cpu_request}
                </Typography>
              </Box>
            ) : null}
            {podMemoryAllocationItem?.cpu_limit ? (
              <Box marginLeft={ds.space.mul(0, 15)}>
                <Typography
                  sx={{
                    color: 'var(--grey-80, var(--ds-gray-400))',
                    display: 'block',
                    fontSize: 'var(--ds-text-body-lg)',
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
                    fontSize: 'var(--ds-text-title)',
                    fontWeight: 'var(--ds-font-weight-medium)',
                    marginBottom: 'var(--ds-space-1)',
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
