import { formatMemory } from '@lib/formatter';
import { Box, Stack, Typography } from '@mui/material';
import PropTypes from 'prop-types';
import { safeJSONParse } from 'src/utils/common';

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
      <Box mt={'20px'}>
        <Stack direction={'row'} justifyContent={'space-between'}>
          <Stack direction={'row'}>
            {parsedItem?.node_name && (
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
                  Node Name
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
                  {parsedItem?.node_name}
                </Typography>
              </Box>
            )}
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
                Cluster
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
                {row?.cluster ?? '-'}
              </Typography>
            </Box>
          </Stack>

          <Stack direction={'row'}>
            {parsedItem?.memory_allocatable ? (
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
                  Memory Capacity
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
                  {formatMemory(parsedItem?.memory_allocatable, 'bytes', 'gb', false)} GiB
                </Typography>
              </Box>
            ) : null}
            {parsedItem?.memory_used ? (
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
                  Used Memory
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
                  {formatMemory(parsedItem?.memory_used, 'bytes', 'gb', false)} GiB
                </Typography>
              </Box>
            ) : null}
            {parsedItem?.memory_requested ? (
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
                  Requested Memory
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
