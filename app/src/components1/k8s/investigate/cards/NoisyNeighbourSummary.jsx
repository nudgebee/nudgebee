import { formatMemory } from '@lib/formatter';
import { Box, Stack, Typography } from '@mui/material';
import PropTypes from 'prop-types';
import { safeJSONParse } from 'src/utils/common';
import { ds } from '@utils/colors';

const NoisyNeighbourSummary = ({ row }) => {
  const dataString = row?.evidences;
  if (dataString) {
    const data = dataString.filter((item) => {
      if (item.type !== 'json' || !item.data) {
        return false;
      }
      const parsedJson = safeJSONParse(item.data);
      return parsedJson?.name === 'noisy_neighbours';
    });
    let parsedItem = {};
    if (data.length) {
      const parsedData = safeJSONParse(data?.[0]?.data);
      if (parsedData) {
        parsedItem = parsedData?.data;
      }
    }
    return (
      <Box mt={ds.space.mul(0, 10)}>
        <Stack direction={'row'} justifyContent={'space-between'}>
          <Stack direction={'row'}>
            {parsedItem?.node_name && (
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
                  Node Name
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
                  {parsedItem?.node_name}
                </Typography>
              </Box>
            )}
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
                Cluster
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
                {row?.cluster ?? '-'}
              </Typography>
            </Box>
          </Stack>

          <Stack direction={'row'}>
            {parsedItem?.memory_allocatable ? (
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
                  Memory Capacity
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
                  {formatMemory(parsedItem?.memory_allocatable, 'bytes', 'gb', false)} GiB
                </Typography>
              </Box>
            ) : null}
            {parsedItem?.memory_used ? (
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
                  Used Memory
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
                  {formatMemory(parsedItem?.memory_used, 'bytes', 'gb', false)} GiB
                </Typography>
              </Box>
            ) : null}
            {parsedItem?.memory_requested ? (
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
                  Requested Memory
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
                  {formatMemory(parsedItem?.memory_requested, 'bytes', 'gb', false)} GiB
                </Typography>
              </Box>
            ) : null}
          </Stack>
        </Stack>
      </Box>
    );
  }
};
NoisyNeighbourSummary.propTypes = {
  row: PropTypes.object,
};

export default NoisyNeighbourSummary;
