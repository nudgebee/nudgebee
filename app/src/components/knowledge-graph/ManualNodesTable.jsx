// Manual Nodes table — one row per declared Knowledge Graph node.
//
// Display-only: every mutation (edit / merge / delete) bubbles up to
// ManualNodesSection through the on*Click callbacks.

import { Box, Typography } from '@mui/material';
import PropTypes from 'prop-types';
import CustomTable from '@shared/tables/CustomTable';
import { Label } from '@ui/Label';
import Datetime from '@shared/format/Datetime';
import Text from '@shared/format/Text';
import ThreeDotsMenu from '@ui/ThreeDotsMenu';
import Tooltip from '@ui/Tooltip';
import { DeleteIconRed, EditIcon, pmCheckCircle as MergeIcon } from '@assets';
import { ds } from 'src/utils/colors';

// What each status tells the operator. An adopted node is the collector's
// now — nothing left to do; a possible duplicate wants a Merge.
const STATUS_DISPLAY = {
  active: { label: 'Active', tone: 'info', tip: 'Declared by hand. It stays in the graph until you delete it or a collector discovers it.' },
  adopted: {
    label: 'Adopted',
    tone: 'success',
    tip: 'A collector now provides this node. It is the same node, so dependencies on it are unchanged; the details you typed were replaced by the collected data.',
  },
  possible_duplicate: {
    label: 'Possible duplicate',
    tone: 'warning',
    tip: 'A collected node with the same type and name exists. Merge to move dependencies onto it.',
  },
};

const MENU_ACTION_EDIT = 'edit';
const MENU_ACTION_MERGE = 'merge';
const MENU_ACTION_DELETE = 'delete';

const buildMenuItems = (row) => {
  const items = [];
  if (row.status === 'possible_duplicate') {
    items.push({ id: MENU_ACTION_MERGE, label: 'Merge into collected node', icon: MergeIcon });
  }
  if (row.status !== 'adopted') {
    items.push({ id: MENU_ACTION_EDIT, label: 'Edit', icon: EditIcon });
  }
  items.push({ id: MENU_ACTION_DELETE, label: 'Delete', icon: DeleteIconRed });
  return items;
};

// PascalCase specific type → spaced label ("SQSQueue" → "SQS Queue",
// "EC2Instance" → "EC2 Instance"). The type picker searches these labels.
export const humanizeType = (type) =>
  (type || '')
    .replace(/([a-z])([A-Z])/g, '$1 $2')
    .replace(/([0-9])([A-Z][a-z])/g, '$1 $2')
    .replace(/([A-Z]+)([A-Z][a-z])/g, '$1 $2');

// "Where" cell: the account (or "Outside connected accounts") plus the
// identity qualifiers that place the node.
const formatWhere = (row, accountNames) => {
  const where = row.cloud_account_id ? accountNames[row.cloud_account_id] || row.cloud_account_id : 'Outside connected accounts';
  const qualifiers = [row.region, row.namespace].filter(Boolean).join(' · ');
  return qualifiers ? `${where} · ${qualifiers}` : where;
};

const ManualNodesTable = ({ rows, accountNames, onEditClick, onMergeClick, onDeleteClick }) => {
  if (!rows.length) {
    return (
      <Box sx={{ textAlign: 'center', py: ds.space[7] }}>
        <Typography sx={{ fontSize: ds.text.bodyLg, color: ds.brand[500], fontWeight: ds.weight.semibold, mb: ds.space[1] }}>
          No manual nodes yet
        </Typography>
        <Typography sx={{ fontSize: ds.text.small, color: ds.gray[500] }}>
          Click <strong>Declare node</strong> to declare a resource the Knowledge Graph has not discovered, or <strong>Import JSON</strong> for many.
        </Typography>
      </Box>
    );
  }

  const handleMenuAction = (action, row) => {
    switch (action?.id) {
      case MENU_ACTION_EDIT:
        onEditClick(row);
        break;
      case MENU_ACTION_MERGE:
        onMergeClick(row);
        break;
      case MENU_ACTION_DELETE:
        onDeleteClick(row);
        break;
      default:
        break;
    }
  };

  const tableData = rows.map((row) => {
    const status = STATUS_DISPLAY[row.status] || { label: row.status, tone: 'neutral' };
    const where = formatWhere(row, accountNames);
    return [
      {
        component: (
          <Box sx={{ display: 'flex', flexDirection: 'column', gap: ds.space[0], minWidth: 0 }}>
            <Tooltip title={row.unique_key}>
              <Text value={row.name} sx={{ fontSize: ds.text.small, fontWeight: ds.weight.medium }} />
            </Tooltip>
            <Text value={humanizeType(row.specific_type)} sx={{ fontSize: ds.text.caption, color: ds.gray[500] }} />
          </Box>
        ),
      },
      {
        component: (
          <Tooltip title={where}>
            <Text value={where} sx={{ fontSize: ds.text.small }} />
          </Tooltip>
        ),
      },
      {
        component: (
          <Tooltip title={status.tip}>
            <Box component='span'>
              <Label tone={status.tone}>{status.label}</Label>
            </Box>
          </Tooltip>
        ),
      },
      { component: <Datetime value={row.created_at} /> },
      { component: <ThreeDotsMenu menuItems={buildMenuItems(row)} onMenuClick={handleMenuAction} data={row} /> },
    ];
  });

  return (
    <CustomTable
      headers={[
        { name: 'Node', width: '30%' },
        { name: 'Where', width: '32%' },
        { name: 'Status', width: '18%' },
        { name: 'Declared', width: '12%' },
        { name: '', width: '8%' },
      ]}
      tableData={tableData}
      rowsPerPage={tableData.length}
      totalRows={tableData.length}
      id='manual-nodes-table'
    />
  );
};

ManualNodesTable.propTypes = {
  rows: PropTypes.array.isRequired,
  accountNames: PropTypes.object.isRequired,
  onEditClick: PropTypes.func.isRequired,
  onMergeClick: PropTypes.func.isRequired,
  onDeleteClick: PropTypes.func.isRequired,
};

export default ManualNodesTable;
