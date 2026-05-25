import CustomTable2 from '@components1/common/tables/CustomTable2';
import { formatMemory } from '@lib/formatter';
import { Box, Typography } from '@mui/material';
import PropTypes from 'prop-types';
import { colors } from 'src/utils/colors';
import { safeJSONParse } from 'src/utils/common';

const NoisyNeighbour = ({ row }) => {
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
        parsedItem = parsedData?.data || {};
      }
    }

    let header = ['Workload', 'Memory Node Usage', 'Memory Limit', 'Memory Request', 'Memory Used'];
    parsedItem?.neighbours?.forEach((item) => {
      item.memory_node_usage = (item.memory_used / parsedItem.memory_allocatable) * 100;
      item.insight = [];
      if (!item.memory_requested) {
        let text = 'Container ' + item.name + ' does not have a memory requests';
        item.insight.push(text);
      }
      if (!item.memory_limit) {
        let text = 'Container ' + item.name + ' does not have a memory limit';
        item.insight.push(text);
      }

      if (item.memory_requested && item.memory_used && item.memory_requested < item.memory_used) {
        let mem = ((item.memory_used - item.memory_requested) / item.memory_requested) * 100;
        let text = 'Container ' + item.name + ' using ' + mem?.toFixed(2) + '% more than requested.';
        item.insight.push(text);
      }
    });
    parsedItem?.neighbours?.sort((a, b) => {
      return (b.memory_node_usage || 0) - (a.memory_node_usage || 0);
    });
    const tableData = parsedItem?.neighbours?.map((row) => {
      return [
        {
          component: (
            <>
              <Typography
                sx={{
                  color: colors.text.secondary,
                  fontSize: '12px',
                  fontWeight: 500,
                }}
              >
                {row.pod_name}
              </Typography>
              <Typography
                sx={{
                  fontFamily: 'Roboto',
                  fontSize: '12px',
                  fontStyle: 'normal',
                  fontWeight: 500,
                  lineHeight: '16px',
                  color: colors.text.tertiary,
                }}
                variant='subtitle'
              >
                {row.namespace}
              </Typography>
              {row.insight.map((item, _id) => (
                <li key={item}>
                  <Typography
                    sx={{
                      fontFamily: 'Roboto',
                      fontSize: '12px',
                      fontStyle: 'normal',
                      fontWeight: 500,
                      lineHeight: '16px',
                      color: colors.text.tertiary,
                    }}
                    variant='subtitle'
                  >
                    {item}
                  </Typography>
                </li>
              ))}
            </>
          ),
        },
        {
          component: (
            <Typography
              sx={{
                color: colors.text.secondary,
                fontSize: '12px',
                fontWeight: 500,
              }}
            >
              {row.memory_node_usage?.toFixed(0)} %
            </Typography>
          ),
        },
        {
          component: (
            <Typography
              sx={{
                color: colors.text.secondary,
                fontSize: '12px',
                fontWeight: 500,
              }}
            >
              {row.memory_limit && row.memory_limit !== 0 ? `${formatMemory(row.memory_limit, 'bytes', 'gb', false)} GiB` : '-'}
            </Typography>
          ),
        },
        {
          component: (
            <Typography
              sx={{
                color: colors.text.secondary,
                fontSize: '12px',
                fontWeight: 500,
              }}
            >
              {row?.memory_requested > 0 ? `${formatMemory(row.memory_requested, 'bytes', 'gb', false)} GiB` : '-'}
            </Typography>
          ),
        },
        {
          component: (
            <Typography
              sx={{
                color: colors.text.secondary,
                fontSize: '12px',
                fontWeight: 500,
              }}
            >
              {row?.memory_used > 0 ? `${formatMemory(row.memory_used, 'bytes', 'gb', false)} GiB` : '-'}
            </Typography>
          ),
        },
      ];
    });

    return (
      <>
        {tableData && tableData.length > 0 ? (
          <Box mt={'20px'}>
            <CustomTable2 tableData={tableData} headers={header} rowsPerPage={tableData.length} totalRows={tableData.length} />
          </Box>
        ) : null}
      </>
    );
  }
};
NoisyNeighbour.propTypes = {
  row: PropTypes.object,
};

export default NoisyNeighbour;
