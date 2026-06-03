// Manual Dependencies tab — Phase 2 + 2.5 (NB-30989).
//
// Container that owns:
//   - data fetch (kg_list_manual_dependencies)
//   - status filter
//   - view state: 'table' vs 'resolve' (push-replace pattern)
//   - sub-dialog open/close: CSV import, create/edit form, confirm prompts
//   - all per-row mutations: edit, reresolve, delete
//   - panic actions: re-resolve all, delete all
// Children render the table, the resolve panel, the CSV dialog, the
// create/edit form, and the confirm prompts. Mutations all funnel through
// here so the data-flow stays uni-directional.

import { useCallback, useEffect, useMemo, useState } from 'react';
import { Box, Typography } from '@mui/material';
import PropTypes from 'prop-types';
import ListingLayout from '@components1/ds/ListingLayout';
import FilterDropdown from '@components1/ds/FilterDropdown';
import { Button } from '@components1/ds/Button';
import { toast as snackbar } from '@components1/ds/Toast';
import Loader from '@components1/common/Loader';
import { UploadIcon, PlusIcon, RefreshIcon, DeleteIconRed } from '@assets';
import SafeIcon from '@components1/common/SafeIcon';
import apiKnowledgeGraph from '@api1/knowledge-graph';
import ManualDependenciesTable from './ManualDependenciesTable';
import ResolveAmbiguityPanel from './ResolveAmbiguityPanel';
import CsvImportDialog from './CsvImportDialog';
import EditManualDependencyDialog from './EditManualDependencyDialog';
import ConfirmDialog from './ConfirmDialog';
import { ds } from 'src/utils/colors';

// Status-filter option list — value 'all' bypasses the filter; other values
// map to kg_list_manual_dependencies' status_filter request parameter.
const STATUS_FILTER_OPTIONS = [
  { value: 'all', label: 'All' },
  { value: 'resolved', label: 'Resolved' },
  { value: 'pending', label: 'Pending' },
  { value: 'source_ambiguous,dest_ambiguous', label: 'Ambiguous' },
  { value: 'source_unmatched,dest_unmatched', label: 'Unmatched' },
  { value: 'source_too_many_matches,dest_too_many_matches', label: 'Too many matches' },
  { value: 'node_inactive', label: 'Node inactive' },
];

const VIEW_TABLE = 'table';
const VIEW_RESOLVE = 'resolve';

const ManualDependenciesTab = ({ open }) => {
  const [rows, setRows] = useState([]);
  const [loading, setLoading] = useState(false);
  const [statusFilter, setStatusFilter] = useState('all');
  const [view, setView] = useState(VIEW_TABLE);
  const [selectedRow, setSelectedRow] = useState(null);

  // Sub-dialog state. editRow=null means "create new"; editRow=<row> means
  // "edit this row". We keep them as separate state vars from selectedRow
  // because the resolve view also uses selectedRow.
  const [csvDialogOpen, setCsvDialogOpen] = useState(false);
  const [editDialogOpen, setEditDialogOpen] = useState(false);
  const [editRow, setEditRow] = useState(null);

  // Confirm-dialog state. One state object handles single-row delete AND
  // "delete all" — the variant field switches the title/message/handler.
  // Kept inline (rather than in a separate ConfirmController) because there
  // are only two destructive actions in this tab.
  const [confirmState, setConfirmState] = useState(null);
  const [confirmSubmitting, setConfirmSubmitting] = useState(false);

  const statusFilterArray = useMemo(() => {
    if (!statusFilter || statusFilter === 'all') {
      return undefined;
    }
    return statusFilter.split(',').filter(Boolean);
  }, [statusFilter]);

  const fetchRows = useCallback(
    async ({ showLoader = true } = {}) => {
      if (showLoader) {
        setLoading(true);
      }
      try {
        const res = await apiKnowledgeGraph.listManualDependencies({ statusFilter: statusFilterArray });
        const errors = res?.data?.errors;
        if (errors?.length) {
          snackbar.error(`Failed to load manual dependencies: ${errors[0]?.message ?? 'Unknown error'}`);
          return;
        }
        const payload = res?.data?.data?.kg_list_manual_dependencies?.data ?? [];
        setRows(Array.isArray(payload) ? payload : []);
      } catch (err) {
        console.error('Failed to fetch manual dependencies:', err);
        snackbar.error('Failed to load manual dependencies.');
      } finally {
        if (showLoader) {
          setLoading(false);
        }
      }
    },
    [statusFilterArray]
  );

  // Refetch whenever the tab is opened OR the status filter changes. When the
  // dialog closes we leave state in place so reopening is instant.
  useEffect(() => {
    if (!open) {
      return;
    }
    fetchRows();
  }, [open, fetchRows]);

  // ---- Per-row handlers ------------------------------------------------

  const handleResolveClick = (row) => {
    setSelectedRow(row);
    setView(VIEW_RESOLVE);
  };

  const handleBackToTable = () => {
    setSelectedRow(null);
    setView(VIEW_TABLE);
  };

  const handleResolvedSuccess = async () => {
    handleBackToTable();
    await fetchRows({ showLoader: false });
  };

  const handleCsvImported = async () => {
    setCsvDialogOpen(false);
    await fetchRows({ showLoader: false });
  };

  const handleNewClick = () => {
    setEditRow(null);
    setEditDialogOpen(true);
  };

  const handleEditClick = (row) => {
    setEditRow(row);
    setEditDialogOpen(true);
  };

  const handleEditSaved = async () => {
    setEditDialogOpen(false);
    setEditRow(null);
    await fetchRows({ showLoader: false });
  };

  // Re-resolve a single row. Kept inline (no confirm) because re-resolving is
  // idempotent + non-destructive — at worst the row's status stays the same.
  const handleReresolveClick = async (row) => {
    try {
      const res = await apiKnowledgeGraph.reresolveManualDependency({ id: row.id });
      const errors = res?.data?.errors;
      if (errors?.length) {
        snackbar.error(`Re-resolve failed: ${errors[0]?.message ?? 'Unknown error'}`);
        return;
      }
      snackbar.success('Re-resolved.');
      await fetchRows({ showLoader: false });
    } catch (err) {
      console.error('Re-resolve failed:', err);
      snackbar.error('Re-resolve failed.');
    }
  };

  const handleDeleteClick = (row) => {
    setConfirmState({
      variant: 'delete-row',
      row,
      title: 'Delete declaration?',
      message: (
        <>
          This removes the declaration <strong>{row.source_name}</strong> → <strong>{row.dest_name}</strong> ({row.relationship_type}) AND the
          matching <code>source=&apos;manual&apos;</code> edge from the Knowledge Graph. The audit row is soft-deleted (preserved for history).
        </>
      ),
    });
  };

  // ---- Toolbar / panic handlers ---------------------------------------

  const handleReresolveAll = async () => {
    setConfirmSubmitting(true);
    try {
      const res = await apiKnowledgeGraph.reresolveManualDependencies();
      const errors = res?.data?.errors;
      if (errors?.length) {
        snackbar.error(`Bulk re-resolve failed: ${errors[0]?.message ?? 'Unknown error'}`);
        return;
      }
      const count = res?.data?.data?.kg_reresolve_manual_dependencies?.count ?? 0;
      snackbar.success(`Re-resolved ${count} row${count === 1 ? '' : 's'}.`);
      await fetchRows({ showLoader: false });
    } catch (err) {
      console.error('Bulk re-resolve failed:', err);
      snackbar.error('Bulk re-resolve failed.');
    } finally {
      setConfirmSubmitting(false);
    }
  };

  const handleDeleteAllClick = () => {
    setConfirmState({
      variant: 'delete-all',
      title: 'Delete ALL manual dependencies?',
      message: (
        <>
          This will <strong>soft-delete every manual declaration</strong> for the tenant ({rows.length} active row
          {rows.length === 1 ? '' : 's'}) and remove every <code>source=&apos;manual&apos;</code> edge from the Knowledge Graph. The audit history is
          preserved but rows must be re-imported to come back. Continue?
        </>
      ),
    });
  };

  // Single confirm-action dispatcher. Looks at confirmState.variant and
  // calls the right RPC; success path always refreshes the list. Errors
  // surface through snackbar, dialog stays open so the operator can retry.
  const handleConfirm = async () => {
    if (!confirmState) {
      return;
    }
    setConfirmSubmitting(true);
    try {
      if (confirmState.variant === 'delete-row') {
        const res = await apiKnowledgeGraph.deleteManualDependency({ id: confirmState.row.id });
        const errors = res?.data?.errors;
        if (errors?.length) {
          snackbar.error(`Delete failed: ${errors[0]?.message ?? 'Unknown error'}`);
          return;
        }
        snackbar.success('Declaration deleted.');
      } else if (confirmState.variant === 'delete-all') {
        const res = await apiKnowledgeGraph.deleteAllManualDependencies();
        const errors = res?.data?.errors;
        if (errors?.length) {
          snackbar.error(`Delete all failed: ${errors[0]?.message ?? 'Unknown error'}`);
          return;
        }
        const payload = res?.data?.data?.kg_delete_all_manual_dependencies?.data ?? {};
        snackbar.success(`Wiped ${payload.rows_deactivated ?? 0} declarations and ${payload.edges_deleted ?? 0} edges.`);
      }
      setConfirmState(null);
      await fetchRows({ showLoader: false });
    } catch (err) {
      console.error('Confirm action failed:', err);
      snackbar.error('Action failed.');
    } finally {
      setConfirmSubmitting(false);
    }
  };

  // ---- Render ----------------------------------------------------------

  if (view === VIEW_RESOLVE && selectedRow) {
    return <ResolveAmbiguityPanel row={selectedRow} onBack={handleBackToTable} onResolved={handleResolvedSuccess} />;
  }

  return (
    <Box sx={{ display: 'flex', flexDirection: 'column', gap: 2, py: 2 }}>
      <Typography sx={{ fontSize: '12px', color: ds?.text?.secondaryDark ?? '#6b7280' }}>
        Declare service-to-service dependencies (CALLS / PUBLISHES_TO / SUBSCRIBES_TO) so the Knowledge Graph can correlate alerts even without
        distributed traces. Upload a CSV to seed many rows at once. Each row resolves to KG nodes at upload time; ambiguous rows can be disambiguated
        from the list.
      </Typography>

      <ListingLayout>
        <ListingLayout.Toolbar>
          <FilterDropdown
            id='manual-deps-status-filter'
            label='Status'
            options={STATUS_FILTER_OPTIONS}
            value={statusFilter}
            onSelect={(e) => setStatusFilter(e?.target?.value || 'all')}
          />
          <Box sx={{ flex: 1 }} />
          {/* Bulk re-resolve only makes sense when there's at least one
              non-resolved row to retry. Hide otherwise to keep the toolbar
              quiet. */}
          {rows.length > 0 && (
            <Button
              tone='secondary'
              size='md'
              onClick={handleReresolveAll}
              icon={<SafeIcon src={RefreshIcon} alt='refresh' width={14} height={14} />}
              disabled={confirmSubmitting}
            >
              Re-resolve all
            </Button>
          )}
          {rows.length > 0 && (
            <Button
              tone='danger'
              size='md'
              onClick={handleDeleteAllClick}
              icon={<SafeIcon src={DeleteIconRed} alt='delete' width={14} height={14} />}
            >
              Delete all
            </Button>
          )}
          <Button
            tone='secondary'
            size='md'
            onClick={() => setCsvDialogOpen(true)}
            icon={<SafeIcon src={UploadIcon} alt='upload' width={14} height={14} />}
          >
            Import CSV
          </Button>
          <Button tone='primary' size='md' onClick={handleNewClick} icon={<SafeIcon src={PlusIcon} alt='add' width={14} height={14} />}>
            New declaration
          </Button>
        </ListingLayout.Toolbar>
        <ListingLayout.Body>
          {loading ? (
            <Box sx={{ display: 'flex', justifyContent: 'center', py: 4 }}>
              <Loader />
            </Box>
          ) : (
            <ManualDependenciesTable
              rows={rows}
              onResolveClick={handleResolveClick}
              onEditClick={handleEditClick}
              onReresolveClick={handleReresolveClick}
              onDeleteClick={handleDeleteClick}
            />
          )}
        </ListingLayout.Body>
      </ListingLayout>

      <CsvImportDialog open={csvDialogOpen} onClose={() => setCsvDialogOpen(false)} onImported={handleCsvImported} />
      <EditManualDependencyDialog
        open={editDialogOpen}
        row={editRow}
        onClose={() => {
          setEditDialogOpen(false);
          setEditRow(null);
        }}
        onSaved={handleEditSaved}
      />
      <ConfirmDialog
        open={Boolean(confirmState)}
        title={confirmState?.title ?? ''}
        message={confirmState?.message ?? ''}
        confirmLabel={confirmState?.variant === 'delete-all' ? 'Delete all' : 'Delete'}
        danger
        submitting={confirmSubmitting}
        onConfirm={handleConfirm}
        onClose={() => setConfirmState(null)}
      />
    </Box>
  );
};

ManualDependenciesTab.propTypes = {
  open: PropTypes.bool.isRequired,
};

export default ManualDependenciesTab;
