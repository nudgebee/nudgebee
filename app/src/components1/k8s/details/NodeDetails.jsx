import { memo, useState } from 'react';
import PropTypes from 'prop-types';
import { getTableData4 } from '@components1/k8s/investigate/cards/util';
import CustomTable from '@components1/common/tables/CustomTable2';
import BoxLayout2 from '@components1/common/BoxLayout2';
import { Box, Typography, Chip, IconButton } from '@mui/material';
import ContentCopyIcon from '@mui/icons-material/ContentCopy';
import CheckIcon from '@mui/icons-material/Check';
import CustomTooltip from '@components1/common/CustomTooltip';
import { snakeToTitleCase } from 'src/utils/common';

// Tooltip content for path nodes
const PathNodeTooltipContent = memo(({ pathNode }) => {
  if (!pathNode) return null;

  return (
    <Box
      sx={{
        display: 'flex',
        flexDirection: 'column',
        gap: '4px',
        padding: '4px',
        minWidth: '200px',
        maxWidth: '400px',
      }}
    >
      {pathNode.accountName && (
        <Box sx={{ display: 'flex', gap: '8px' }}>
          <Typography sx={{ fontSize: '11px', color: '#aaa', minWidth: '70px' }}>Account:</Typography>
          <Typography sx={{ fontSize: '11px', color: '#fff', wordBreak: 'break-word' }}>{pathNode.accountName}</Typography>
        </Box>
      )}
      <Box sx={{ display: 'flex', gap: '8px' }}>
        <Typography sx={{ fontSize: '11px', color: '#aaa', minWidth: '70px' }}>Name:</Typography>
        <Typography sx={{ fontSize: '11px', color: '#fff', wordBreak: 'break-word' }}>{pathNode.name || '-'}</Typography>
      </Box>
      <Box sx={{ display: 'flex', gap: '8px' }}>
        <Typography sx={{ fontSize: '11px', color: '#aaa', minWidth: '70px' }}>Type:</Typography>
        <Typography sx={{ fontSize: '11px', color: '#fff' }}>{snakeToTitleCase(pathNode.nodeType || '')}</Typography>
      </Box>
      <Box sx={{ display: 'flex', gap: '8px', alignItems: 'flex-start' }}>
        <Typography sx={{ fontSize: '11px', color: '#aaa', minWidth: '70px' }}>Unique Key:</Typography>
        <Typography
          sx={{
            fontSize: '10px',
            color: '#ccc',
            wordBreak: 'break-all',
            fontFamily: 'monospace',
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
  const [copied, setCopied] = useState(false);

  const handleCopyUniqueKey = () => {
    if (!uniqueKey) {
      return;
    }
    navigator.clipboard.writeText(uniqueKey);
    setCopied(true);
    setTimeout(() => setCopied(false), 1500);
  };

  return (
    <Box sx={{ display: 'flex', flexDirection: 'column', height: '100%' }}>
      {uniqueKey && (
        <Box
          sx={{
            p: 2,
            display: 'flex',
            alignItems: 'center',
            gap: 1,
            borderBottom: '1px solid #eee',
          }}
          data-testid='kg-node-unique-key'
        >
          <Typography sx={{ fontSize: '12px', color: '#666', minWidth: '90px', flexShrink: 0 }}>Unique Key:</Typography>
          <Typography
            sx={{
              fontSize: '12px',
              fontFamily: 'monospace',
              color: '#222',
              wordBreak: 'break-all',
              flex: 1,
            }}
          >
            {uniqueKey}
          </Typography>
          <CustomTooltip title={copied ? 'Copied!' : 'Copy'} placement='top'>
            <IconButton size='small' onClick={handleCopyUniqueKey} data-testid='kg-node-unique-key-copy-btn' sx={{ flexShrink: 0 }}>
              {copied ? <CheckIcon fontSize='small' /> : <ContentCopyIcon fontSize='small' />}
            </IconButton>
          </CustomTooltip>
        </Box>
      )}
      {/* Path section - show whenever there's path data */}
      {pathToNode.length > 0 && (
        <Box sx={{ p: 2, bgcolor: '#f5f5f5', borderBottom: '1px solid #eee' }}>
          <Typography variant='subtitle2' sx={{ mb: 1, color: '#666', fontSize: '12px' }}>
            Path to this node:
          </Typography>
          <Box sx={{ display: 'flex', flexWrap: 'wrap', alignItems: 'center', gap: 0.5 }}>
            {pathToNode.map((pathNode, index) => (
              <Box key={pathNode.id} sx={{ display: 'flex', alignItems: 'center', gap: 0.5 }}>
                {index > 0 && pathNode.edgeFromPrev && (
                  <Typography sx={{ fontSize: '10px', color: '#1976d2', px: 0.5 }}>—[{pathNode.edgeFromPrev}]→</Typography>
                )}
                {index > 0 && !pathNode.edgeFromPrev && <Typography sx={{ fontSize: '12px', color: '#999', px: 0.5 }}>→</Typography>}
                <CustomTooltip
                  title={<PathNodeTooltipContent pathNode={pathNode} />}
                  placement='bottom'
                  tooltipStyle={{
                    backgroundColor: 'rgba(50, 50, 50, 0.95)',
                    padding: '8px 12px',
                    maxWidth: '450px',
                  }}
                >
                  <Chip
                    label={pathNode.name ? `${pathNode.name} (${snakeToTitleCase(pathNode.nodeType || '')})` : pathNode.label}
                    size='small'
                    variant={index === pathToNode.length - 1 ? 'filled' : 'outlined'}
                    color={index === pathToNode.length - 1 ? 'primary' : 'default'}
                    sx={{ fontSize: '11px', height: '24px', cursor: 'pointer' }}
                  />
                </CustomTooltip>
              </Box>
            ))}
          </Box>
        </Box>
      )}

      {/* Existing labels table */}
      <Box sx={{ flex: 1, overflow: 'auto' }}>
        <BoxLayout2
          heading={'Labels'}
          sharingOptions={{
            download: {
              enabled: false,
              onClick: () => {
                return {
                  tableId: '',
                };
              },
            },
            sharing: { enabled: false },
          }}
        >
          <CustomTable
            tableData={convertedJson2}
            headers={headers}
            rowsPerPage={convertedJson2.length || 0}
            totalRows={convertedJson2.length || 0}
            onPageChange={undefined}
          />
        </BoxLayout2>
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
