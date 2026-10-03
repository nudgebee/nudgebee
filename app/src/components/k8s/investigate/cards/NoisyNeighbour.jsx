import CustomTable2 from '@shared/tables/CustomTable2';
import { formatCores, formatMemory } from '@lib/formatter';
import { Box, Typography } from '@mui/material';
import PropTypes from 'prop-types';
import { ds } from '@utils/colors';
import { safeJSONParse } from 'src/utils/common';

const SectionLabel = ({ text }) => (
  <Typography
    sx={{
      color: ds.gray[700],
      fontSize: 'var(--ds-text-small)',
      fontWeight: 'var(--ds-font-weight-semibold)',
      mb: ds.space.mul(0, 2),
    }}
  >
    {text}
  </Typography>
);
SectionLabel.propTypes = { text: PropTypes.string };

const ValueCell = ({ text }) => (
  <Typography
    sx={{
      color: ds.gray[700],
      fontSize: 'var(--ds-text-small)',
      fontWeight: 'var(--ds-font-weight-medium)',
    }}
  >
    {text}
  </Typography>
);
ValueCell.propTypes = { text: PropTypes.string };

const WorkloadCell = ({ name, podName, namespace }) => (
  <>
    <Typography
      sx={{
        color: ds.gray[700],
        fontSize: 'var(--ds-text-small)',
        fontWeight: 'var(--ds-font-weight-medium)',
      }}
    >
      {podName}
    </Typography>
    <Typography
      sx={{
        fontSize: 'var(--ds-text-small)',
        fontStyle: 'normal',
        fontWeight: 'var(--ds-font-weight-medium)',
        lineHeight: '16px',
        color: ds.gray[600],
      }}
      variant='subtitle'
    >
      {namespace}
      {name ? ` / ${name}` : ''}
    </Typography>
  </>
);
WorkloadCell.propTypes = {
  name: PropTypes.string,
  podName: PropTypes.string,
  namespace: PropTypes.string,
};

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
                  color: ds.gray[700],
                  fontSize: 'var(--ds-text-small)',
                  fontWeight: 'var(--ds-font-weight-medium)',
                }}
              >
                {row.pod_name}
              </Typography>
              <Typography
                sx={{
                  fontSize: 'var(--ds-text-small)',
                  fontStyle: 'normal',
                  fontWeight: 'var(--ds-font-weight-medium)',
                  lineHeight: '16px',
                  color: ds.gray[600],
                }}
                variant='subtitle'
              >
                {row.namespace}
              </Typography>
              {row.insight.map((item, _id) => (
                <li key={item}>
                  <Typography
                    sx={{
                      fontSize: 'var(--ds-text-small)',
                      fontStyle: 'normal',
                      fontWeight: 'var(--ds-font-weight-medium)',
                      lineHeight: '16px',
                      color: ds.gray[600],
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
                color: ds.gray[700],
                fontSize: 'var(--ds-text-small)',
                fontWeight: 'var(--ds-font-weight-medium)',
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
                color: ds.gray[700],
                fontSize: 'var(--ds-text-small)',
                fontWeight: 'var(--ds-font-weight-medium)',
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
                color: ds.gray[700],
                fontSize: 'var(--ds-text-small)',
                fontWeight: 'var(--ds-font-weight-medium)',
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
                color: ds.gray[700],
                fontSize: 'var(--ds-text-small)',
                fontWeight: 'var(--ds-font-weight-medium)',
              }}
            >
              {row?.memory_used > 0 ? `${formatMemory(row.memory_used, 'bytes', 'gb', false)} GiB` : '-'}
            </Typography>
          ),
        },
      ];
    });

    // CPU is only present when the metrics provider could measure it. When it
    // is absent the section is skipped entirely rather than drawn with zeros —
    // "we did not measure this" and "nothing is using CPU" are different claims.
    const cpuHeader = ['Workload', 'CPU Node Usage', 'CPU Limit', 'CPU Request', 'CPU Used'];
    const cpuNeighbours = Array.isArray(parsedItem?.cpu_neighbours) ? parsedItem.cpu_neighbours : [];
    const cpuTableData = cpuNeighbours.map((cpuRow) => {
      const nodeUsage = parsedItem?.cpu_allocatable ? (cpuRow.cpu_used / parsedItem.cpu_allocatable) * 100 : null;
      const nodeUsageText = nodeUsage === null || !isFinite(nodeUsage) ? '-' : `${nodeUsage.toFixed(0)} %`;
      return [
        { component: <WorkloadCell name={cpuRow.name} podName={cpuRow.pod_name} namespace={cpuRow.namespace} /> },
        { component: <ValueCell text={nodeUsageText} /> },
        { component: <ValueCell text={formatCores(cpuRow.cpu_limit)} /> },
        { component: <ValueCell text={formatCores(cpuRow.cpu_requested)} /> },
        { component: <ValueCell text={formatCores(cpuRow.cpu_used)} /> },
      ];
    });

    const hasMemory = tableData && tableData.length > 0;
    const hasCpu = cpuTableData.length > 0;

    return (
      <>
        {hasMemory ? (
          <Box mt={ds.space.mul(0, 10)}>
            {hasCpu ? <SectionLabel text='Top memory consumers on this node' /> : null}
            <CustomTable2 tableData={tableData} headers={header} rowsPerPage={tableData.length} totalRows={tableData.length} />
          </Box>
        ) : null}
        {hasCpu ? (
          <Box mt={ds.space.mul(0, 10)}>
            <SectionLabel
              text={
                parsedItem?.cpu_allocatable && parsedItem?.cpu_used
                  ? `Top CPU consumers on this node (${formatCores(parsedItem.cpu_used)} of ${formatCores(parsedItem.cpu_allocatable)} in use)`
                  : 'Top CPU consumers on this node'
              }
            />
            <CustomTable2 tableData={cpuTableData} headers={cpuHeader} rowsPerPage={cpuTableData.length} totalRows={cpuTableData.length} />
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
