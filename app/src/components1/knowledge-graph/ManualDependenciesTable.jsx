// Manual Dependencies table — Phase 2 + 2.5 (NB-30989).
//
// Renders a row per kg_manual_dependencies entry with status chips and a
// per-row ThreeDotsMenu (Resolve for ambiguous rows + Edit / Re-resolve /
// Delete for every row). Display-only — every mutation (resolve / edit /
// reresolve / delete / refetch) bubbles up to ManualDependenciesTab via the
// matching `on*Click` callbacks so the data flow stays uni-directional.

import { Box, Typography } from '@mui/material';
import PropTypes from 'prop-types';
import dayjs from 'dayjs';
import relativeTime from 'dayjs/plugin/relativeTime';
import CustomTable from '@common-new/tables/CustomTable2';
import { Label } from '@components1/ds/Label';
import Text from '@common-new/format/Text';
import ThreeDotsMenu from '@common-new/ThreeDotsMenu';
import CustomTooltip from '@components1/common/CustomTooltip';
import { DeleteIconRed, EditIcon, RefreshIcon, pmCheckCircle as ResolveIcon } from '@assets';
import { ds } from 'src/utils/colors';

dayjs.extend(relativeTime);

// Maps resolution_status to human-readable label + DS Label tone (Status
// axis: neutral / info / success / warning / critical). Keeps the chip
// rendering decision out of the JSX so the table cell stays readable.
const STATUS_DISPLAY = {
  resolved: { label: 'Resolved', tone: 'success' },
  pending: { label: 'Pending', tone: 'warning' },
  source_unmatched: { label: 'Source unmatched', tone: 'warning' },
  dest_unmatched: { label: 'Dest unmatched', tone: 'warning' },
  source_ambiguous: { label: 'Source ambiguous', tone: 'warning' },
  dest_ambiguous: { label: 'Dest ambiguous', tone: 'warning' },
  source_too_many_matches: { label: 'Source: too many', tone: 'critical' },
  dest_too_many_matches: { label: 'Dest: too many', tone: 'critical' },
  node_inactive: { label: 'Node inactive', tone: 'neutral' },
  invalid_payload: { label: 'Invalid payload', tone: 'critical' },
};

const AMBIGUOUS_STATUSES = new Set(['source_ambiguous', 'dest_ambiguous']);

// Composes the "Source → Destination" cell. ARN takes precedence; falls back
// to namespace/name; node_type rendered as the leading prefix.
const formatEndpoint = (nodeType, name, namespace, cluster, arn) => {
  if (arn) {
    return `${nodeType}: ${arn}`;
  }
  const ns = namespace ? `${namespace}/` : '';
  const clusterSuffix = cluster ? ` @ ${cluster}` : '';
  return `${nodeType}: ${ns}${name}${clusterSuffix}`;
};

const formatLastResolved = (iso) => {
  if (!iso) {
    return '—';
  }
  try {
    return dayjs(iso).fromNow();
  } catch {
    return iso;
  }
};

const formatMatchCount = (row) => {
  const src = row.source_match_count;
  const dst = row.dest_match_count;
  if (!src && !dst) {
    return '—';
  }
  if (src && dst) {
    return `${src} src · ${dst} dst`;
  }
  if (src) {
    return `${src} src`;
  }
  return `${dst} dst`;
};

// Map ThreeDotsMenu action ids → outbound callback. Keeps the menu-item
// list declarative and the callback dispatch in one place.
const MENU_ACTION_RESOLVE = 'resolve';
const MENU_ACTION_EDIT = 'edit';
const MENU_ACTION_RERESOLVE = 'reresolve';
const MENU_ACTION_DELETE = 'delete';

// Builds the per-row menu items. Order matters — most-likely action first:
//   - Resolve (only when ambiguous; lets the operator pick candidates)
//   - Edit (always; fix qualifiers; primary recovery for *_too_many_matches
//     where the resolve panel can't help — its banner directs here)
//   - Reresolve (always; rerun the resolver, e.g. after the missing AWS
//     resource finally got ingested)
//   - Delete (always; destructive, last)
const buildMenuItems = (row) => {
  const items = [];
  if (AMBIGUOUS_STATUSES.has(row.resolution_status)) {
    items.push({ id: MENU_ACTION_RESOLVE, label: 'Resolve', icon: ResolveIcon });
  }
  items.push({ id: MENU_ACTION_EDIT, label: 'Edit', icon: EditIcon });
  items.push({ id: MENU_ACTION_RERESOLVE, label: 'Re-resolve', icon: RefreshIcon });
  items.push({ id: MENU_ACTION_DELETE, label: 'Delete', icon: DeleteIconRed });
  return items;
};

const ManualDependenciesTable = ({ rows, onResolveClick, onEditClick, onReresolveClick, onDeleteClick }) => {
  if (!rows.length) {
    return (
      <Box sx={{ textAlign: 'center', py: 6 }}>
        <Typography sx={{ fontSize: '14px', color: ds?.text?.secondary ?? '#374151', fontWeight: 600, mb: 0.5 }}>
          No manual dependencies yet
        </Typography>
        <Typography sx={{ fontSize: '12px', color: ds?.text?.secondaryDark ?? '#6b7280' }}>
          Click <strong>Import CSV</strong> to seed declarations from a file.
        </Typography>
      </Box>
    );
  }

  // Single dispatch — ThreeDotsMenu hands back (actionObj, data); we forward
  // to the right outer callback. Keeps the per-row JSX uncluttered.
  const handleMenuAction = (action, row) => {
    switch (action?.id) {
      case MENU_ACTION_RESOLVE:
        onResolveClick(row);
        break;
      case MENU_ACTION_EDIT:
        onEditClick(row);
        break;
      case MENU_ACTION_RERESOLVE:
        onReresolveClick(row);
        break;
      case MENU_ACTION_DELETE:
        onDeleteClick(row);
        break;
      default:
        break;
    }
  };

  const tableData = rows.map((row) => {
    const status = STATUS_DISPLAY[row.resolution_status] || { label: row.resolution_status, variant: 'grey' };

    const srcEndpoint = formatEndpoint(row.source_node_type, row.source_name, row.source_namespace, row.source_cluster, row.source_arn);
    const dstEndpoint = formatEndpoint(row.dest_node_type, row.dest_name, row.dest_namespace, row.dest_cluster, row.dest_arn);

    return [
      {
        component: (
          <Box sx={{ display: 'flex', flexDirection: 'column', gap: 0.25, minWidth: 0 }}>
            <CustomTooltip title={srcEndpoint}>
              <Text value={srcEndpoint} sx={{ fontSize: '12px' }} />
            </CustomTooltip>
            <CustomTooltip title={dstEndpoint}>
              <Text value={`↳ ${dstEndpoint}`} sx={{ fontSize: '12px', color: ds?.text?.secondaryDark ?? '#6b7280' }} />
            </CustomTooltip>
          </Box>
        ),
      },
      { component: <Label tone='info'>{row.relationship_type}</Label> },
      { component: <Label tone={status.tone}>{status.label}</Label> },
      { component: <Text value={formatMatchCount(row)} sx={{ fontSize: '12px' }} /> },
      { component: <Text value={formatLastResolved(row.last_resolved_at)} sx={{ fontSize: '12px' }} /> },
      {
        component: <ThreeDotsMenu menuItems={buildMenuItems(row)} onMenuClick={handleMenuAction} data={row} />,
      },
    ];
  });

  return (
    <CustomTable
      headers={[
        { name: 'Source → Destination', width: '40%' },
        { name: 'Type', width: '12%' },
        { name: 'Status', width: '18%' },
        { name: 'Matches', width: '10%' },
        { name: 'Last resolved', width: '12%' },
        { name: '', width: '8%' },
      ]}
      tableData={tableData}
      rowsPerPage={tableData.length}
      totalRows={tableData.length}
      id='manual-dependencies-table'
    />
  );
};

ManualDependenciesTable.propTypes = {
  rows: PropTypes.array.isRequired,
  onResolveClick: PropTypes.func.isRequired,
  onEditClick: PropTypes.func.isRequired,
  onReresolveClick: PropTypes.func.isRequired,
  onDeleteClick: PropTypes.func.isRequired,
};

export default ManualDependenciesTable;
