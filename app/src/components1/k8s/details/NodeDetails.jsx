import { memo } from 'react';
import PropTypes from 'prop-types';
import { getTableData4 } from '@components1/k8s/investigate/cards/util';
import CustomTable from '@common-new/tables/CustomTable2';
import { ListingLayout } from '@components1/ds/ListingLayout';
import { Box, Typography, Chip } from '@mui/material';
import Tooltip from '@components1/ds/Tooltip';
import CopyButton from '@common-new/CopyButton';
import { snakeToTitleCase } from 'src/utils/common';

// Tooltip content for path nodes
const PathNodeTooltipContent = memo(({ pathNode }) => {
  if (!pathNode) return null;

  return (
    <Box
      sx={{
        display: 'flex',
        flexDirection: 'column',
        gap: 'var(--ds-space-1)',
        padding: 'var(--ds-space-1)',
        minWidth: '200px',
        maxWidth: '400px',
      }}
    >
      {pathNode.accountName && (
        <Box sx={{ display: 'flex', gap: 'var(--ds-space-2)' }}>
          <Typography sx={{ fontSize: 'var(--ds-text-caption)', color: 'var(--ds-gray-400)', minWidth: '70px' }}>Account:</Typography>
          <Typography sx={{ fontSize: 'var(--ds-text-caption)', color: 'var(--ds-background-100)', wordBreak: 'break-word' }}>
            {pathNode.accountName}
          </Typography>
        </Box>
      )}
      <Box sx={{ display: 'flex', gap: 'var(--ds-space-2)' }}>
        <Typography sx={{ fontSize: 'var(--ds-text-caption)', color: 'var(--ds-gray-400)', minWidth: '70px' }}>Name:</Typography>
        <Typography sx={{ fontSize: 'var(--ds-text-caption)', color: 'var(--ds-background-100)', wordBreak: 'break-word' }}>
          {pathNode.name || '-'}
        </Typography>
      </Box>
      <Box sx={{ display: 'flex', gap: 'var(--ds-space-2)' }}>
        <Typography sx={{ fontSize: 'var(--ds-text-caption)', color: 'var(--ds-gray-400)', minWidth: '70px' }}>Type:</Typography>
        <Typography sx={{ fontSize: 'var(--ds-text-caption)', color: 'var(--ds-background-100)' }}>
          {snakeToTitleCase(pathNode.nodeType || '')}
        </Typography>
      </Box>
      <Box sx={{ display: 'flex', gap: 'var(--ds-space-2)', alignItems: 'flex-start' }}>
        <Typography sx={{ fontSize: 'var(--ds-text-caption)', color: 'var(--ds-gray-400)', minWidth: '70px' }}>Unique Key:</Typography>
        <Typography
          sx={{
            fontSize: 'var(--ds-text-caption)',
            color: 'var(--ds-gray-300)',
            wordBreak: 'break-all',
            fontFamily: 'var(--ds-font-mono)',
          }}
        >
          {pathNode.uniqueKey || pathNode.label}
        </Typography>
      </Box>
    </Box>
  );
});
PathNodeTooltipContent.displayName = 'PathNodeTooltipContent';
PathNodeTooltipContent.propTypes = {
  pathNode: PropTypes.shape({
    accountName: PropTypes.string,
    name: PropTypes.string,
    nodeType: PropTypes.string,
    uniqueKey: PropTypes.string,
    label: PropTypes.string,
  }),
};

const NodeDetails = ({ node }) => {
  const labels = node?.properties ?? node?.labels ?? node?.Labels ?? node;
  const { headers = [], convertedJson2 = [] } = getTableData4([labels] || [{}]);
  const pathToNode = node?.pathToNode || [];
  const uniqueKey = node?.unique_key || node?.uniqueKey;

  return (
    <Box sx={{ display: 'flex', flexDirection: 'column', height: '100%' }}>
      {uniqueKey && (
        <Box
          sx={{
            padding: 'var(--ds-space-4)',
            display: 'flex',
            alignItems: 'center',
            gap: 'var(--ds-space-2)',
            borderBottom: '1px solid var(--ds-gray-200)',
          }}
          data-testid='kg-node-unique-key'
        >
          <Typography sx={{ fontSize: 'var(--ds-text-small)', color: 'var(--ds-gray-600)', minWidth: '90px', flexShrink: 0 }}>Unique Key:</Typography>
          <Typography
            sx={{
              fontSize: 'var(--ds-text-small)',
              fontFamily: 'var(--ds-font-mono)',
              color: 'var(--ds-gray-700)',
              wordBreak: 'break-all',
              flex: 1,
            }}
          >
            {uniqueKey}
          </Typography>
          <CopyButton text={uniqueKey} size='sm' />
        </Box>
      )}
      {/* Path section - show whenever there's path data */}
      {pathToNode.length > 0 && (
        <Box sx={{ padding: 'var(--ds-space-4)', bgcolor: 'var(--ds-background-300)', borderBottom: '1px solid var(--ds-gray-200)' }}>
          <Typography variant='subtitle2' sx={{ marginBottom: 'var(--ds-space-2)', color: 'var(--ds-gray-600)', fontSize: 'var(--ds-text-small)' }}>
            Path to this node:
          </Typography>
          <Box sx={{ display: 'flex', flexWrap: 'wrap', alignItems: 'center', gap: 'var(--ds-space-1)' }}>
            {pathToNode.map((pathNode, index) => (
              <Box key={pathNode.id} sx={{ display: 'flex', alignItems: 'center', gap: 'var(--ds-space-1)' }}>
                {index > 0 && pathNode.edgeFromPrev && (
                  <Typography sx={{ fontSize: 'var(--ds-text-caption)', color: 'var(--ds-blue-600)', padding: '0 var(--ds-space-1)' }}>
                    —[{pathNode.edgeFromPrev}]→
                  </Typography>
                )}
                {index > 0 && !pathNode.edgeFromPrev && (
                  <Typography sx={{ fontSize: 'var(--ds-text-small)', color: 'var(--ds-gray-500)', padding: '0 var(--ds-space-1)' }}>→</Typography>
                )}
                <Tooltip
                  title={<PathNodeTooltipContent pathNode={pathNode} />}
                  placement='bottom'
                  tooltipStyle={{
                    backgroundColor: 'var(--ds-gray-alpha-700)',
                    padding: 'var(--ds-space-2) var(--ds-space-3)',
                    maxWidth: '450px',
                  }}
                >
                  <Chip
                    label={pathNode.name ? `${pathNode.name} (${snakeToTitleCase(pathNode.nodeType || '')})` : pathNode.label}
                    size='small'
                    variant={index === pathToNode.length - 1 ? 'filled' : 'outlined'}
                    color={index === pathToNode.length - 1 ? 'primary' : 'default'}
                    sx={{ fontSize: 'var(--ds-text-caption)', height: '24px', cursor: 'pointer' }}
                  />
                </Tooltip>
              </Box>
            ))}
          </Box>
        </Box>
      )}

      {/* Existing labels table */}
      <Box sx={{ flex: 1, overflow: 'auto', margin: 'var(--ds-space-4)' }}>
        <ListingLayout id='node-details-labels'>
          <ListingLayout.Toolbar title='Labels' />
          <ListingLayout.Body>
            <CustomTable
              tableData={convertedJson2}
              headers={headers}
              rowsPerPage={convertedJson2.length || 0}
              totalRows={convertedJson2.length || 0}
              onPageChange={undefined}
            />
          </ListingLayout.Body>
        </ListingLayout>
      </Box>
    </Box>
  );
};

NodeDetails.propTypes = {
  node: PropTypes.shape({
    properties: PropTypes.object,
    labels: PropTypes.object,
    Labels: PropTypes.object,
    pathToNode: PropTypes.array,
    name: PropTypes.string,
    unique_key: PropTypes.string,
    uniqueKey: PropTypes.string,
  }),
};

export default NodeDetails;
