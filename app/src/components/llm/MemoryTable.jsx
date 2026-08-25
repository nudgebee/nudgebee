/**
 * MemoryTable — thin wrapper around the DS CustomTable used by:
 *
 *   - KnowledgeBaseTab (OSS-kept, ships in every build)
 *   - The memory-v2 typed-layer tabs under llm/memory2/ (EE-only,
 *     stripped from the OSS snapshot via .oss-exclude)
 *
 * Originally defined in llm/memory2/shared.jsx. Moved here so the
 * OSS-kept KnowledgeBaseTab doesn't reach into the stripped memory2/
 * tree. Semantics unchanged.
 *
 * `columns` is an array of { key, label, type?, width?, align?, render? }.
 * `type` drives the column width (see TYPE_WIDTH); `render(row)` supplies
 * the cell JSX (falls back to `row[key]`). All table chrome (header,
 * dividers, pagination, empty state) is delegated to the DS table.
 */
import PropTypes from 'prop-types';
import { Typography } from '@mui/material';
import CustomTable from '@shared/tables/CustomTable';

// Column-width defaults. Values verbatim from the original memory2/shared.jsx —
// `text-long` intentionally has no width so it flexes to fill the remaining
// space. A column may still pass an explicit `width` to override its type.
const TYPE_WIDTH = {
  chip: '120px',
  status: '120px',
  label: '120px',
  date: '120px',
  count: '80px',
  number: '80px',
  icon: '64px',
  actions: '140px',
  'text-short': '180px',
  'text-long': undefined,
};

const resolveWidth = (c) => c.width ?? TYPE_WIDTH[c.type];
const resolveAlign = (c) => c.align ?? (c.type === 'count' || c.type === 'number' || c.type === 'actions' ? 'right' : undefined);

export const MemoryTable = ({ columns, rows = [], emptyText = 'Nothing here yet.' }) => (
  <CustomTable
    headers={columns.map((c) => ({ name: c.label, width: resolveWidth(c), align: resolveAlign(c) }))}
    tableData={rows.map((row) =>
      columns.map((c) => ({
        component: c.render ? (
          c.render(row)
        ) : (
          <Typography sx={{ fontSize: 'var(--ds-text-body)', color: 'var(--ds-gray-700)' }}>{row[c.key]}</Typography>
        ),
      }))
    )}
    rowsPerPage={Math.max(rows.length, 1)}
    totalRows={rows.length}
    emptyStateText={emptyText}
  />
);

MemoryTable.propTypes = {
  columns: PropTypes.arrayOf(PropTypes.object).isRequired,
  // Not `.isRequired` — the destructure defaults to [], so callers that
  // pass no rows still render the empty state without a PropTypes warning.
  rows: PropTypes.array,
  emptyText: PropTypes.string,
};

export default MemoryTable;
