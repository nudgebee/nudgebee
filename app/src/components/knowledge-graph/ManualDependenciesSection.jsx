// Dependencies section of the Manual Declarations tab (NB-30989).
//
// Container that owns:
//   - data fetch (kg_list_manual_dependencies)
//   - status filter
//   - view state: 'table' vs 'resolve' (push-replace pattern)
//   - sub-dialog open/close: edit form, declare-node form, confirm prompts
//   - all per-row mutations: edit, reresolve, delete
//   - panic actions: re-resolve all, delete all
// The tab owns creating a dependency and the JSON import, and bumps
// refreshKey when they change the list.

import { useCallback, useEffect, useMemo, useState } from 'react';
import { Box } from '@mui/material';
import PropTypes from 'prop-types';
import ListingLayout from '@ui/ListingLayout';
import FilterDropdown from '@ui/FilterDropdown';
import { Button } from '@ui/Button';
import { toast as snackbar } from '@ui/Toast';
import Loader from '@shared/Loader';
import { RefreshIcon } from '@assets';
import SafeIcon from '@shared/icons/SafeIcon';
import DeleteOutlinedIcon from '@mui/icons-material/DeleteOutlined';
import apiKnowledgeGraph from '@api1/knowledge-graph';
import ManualDependenciesTable from './ManualDependenciesTable';
import ResolveAmbiguityPanel from './ResolveAmbiguityPanel';
import EditManualDependencyDialog from './EditManualDependencyDialog';
import EditManualNodeDialog from './EditManualNodeDialog';
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

const ManualDependenciesSection = ({ open, schema, refreshKey, onNodesChanged }) => {
  const [rows, setRows] = useState([]);
  const [loading, setLoading] = useState(false);
  const [statusFilter, setStatusFilter] = useState('all');
  const [view, setView] = useState(VIEW_TABLE);
  const [selectedRow, setSelectedRow] = useState(null);

  // Edit-dialog state, separate from selectedRow because the resolve view
  // also uses selectedRow. Creating a dependency lives in the tab.
  const [editDialogOpen, setEditDialogOpen] = useState(false);
  const [editRow, setEditRow] = useState(null);

  // Confirm-dialog state. One state object handles single-row delete AND
  // "delete all" — the variant field switches the title/message/handler.
  // Kept inline (rather than in a separate ConfirmController) because there
  // are only two destructive actions in this tab.
  const [confirmState, setConfirmState] = useState(null);
  const [confirmSubmitting, setConfirmSubmitting] = useState(false);

  // "Declare node" on an unmatched endpoint: { row, side } while the node
  // dialog is open. The schema says which node types can be declared.
  const [declareFor, setDeclareFor] = useState(null);
  const declarableTypes = useMemo(() => new Set(schema.flatMap((p) => (p.kinds ?? []).map((k) => k.node_type))), [schema]);

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

  // Something outside the section changed the list: refresh without a loader.
  useEffect(() => {
    if (open && refreshKey) {
      fetchRows({ showLoader: false });
    }
  }, [open, refreshKey, fetchRows]);

  // Pin the declaring row's unmatched side to a node — the one just declared,
  // or the collected node the declaration turned out to duplicate.
  const pinDeclaredNode = async (nodeId) => {
    const { row, side } = declareFor;
    setDeclareFor(null);
    // A node was declared (or matched): the Nodes section lists it.
    onNodesChanged?.();
    if (!nodeId) {
      await fetchRows({ showLoader: false });
      return;
    }
    try {
      const res = await apiKnowledgeGraph.resolveManualDependency(
        side === 'source' ? { id: row.id, sourceNodeId: nodeId } : { id: row.id, destinationNodeId: nodeId }
      );
      const errors = res?.data?.errors;
      if (errors?.length) {
        snackbar.error(`Node declared, but pinning the dependency failed: ${errors[0]?.message ?? 'Unknown error'}`);
      } else {
        snackbar.success('Dependency now points at the node.');
      }
    } catch (err) {
      console.error('Pinning the dependency failed:', err);
      snackbar.error('Node declared, but pinning the dependency failed.');
    }
    await fetchRows({ showLoader: false });
  };

  const declareInitial = useMemo(() => {
    if (!declareFor) {
      return null;
    }
    const { row, side } = declareFor;
    return {
      node_type: row[`${side}_node_type`],
      name: row[`${side}_name`],
      namespace: row[`${side}_namespace`],
      region: row[`${side}_region`],
    };
  }, [declareFor]);

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
          preserved but rows must be declared again to come back. Continue?
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
    <>
      <ListingLayout id='manual-dependencies-section'>
        <ListingLayout.Toolbar title='Dependencies'>
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
            <Button tone='danger' size='md' onClick={handleDeleteAllClick} icon={<DeleteOutlinedIcon sx={{ fontSize: ds.text.bodyLg }} />}>
              Delete all
            </Button>
          )}
        </ListingLayout.Toolbar>
        <ListingLayout.Body>
          {loading ? (
            <Box sx={{ display: 'flex', justifyContent: 'center', py: ds.space[6] }}>
              <Loader />
            </Box>
          ) : (
            <ManualDependenciesTable
              rows={rows}
              onResolveClick={handleResolveClick}
              onEditClick={handleEditClick}
              onReresolveClick={handleReresolveClick}
              onDeleteClick={handleDeleteClick}
              onDeclareNodeClick={(row, side) => setDeclareFor({ row, side })}
              declarableTypes={declarableTypes}
            />
          )}
        </ListingLayout.Body>
      </ListingLayout>

      <EditManualDependencyDialog
        open={editDialogOpen}
        row={editRow}
        onClose={() => {
          setEditDialogOpen(false);
          setEditRow(null);
        }}
        onSaved={handleEditSaved}
      />
      <EditManualNodeDialog
        open={Boolean(declareFor)}
        initial={declareInitial}
        schema={schema}
        onClose={() => setDeclareFor(null)}
        onSaved={(created) => pinDeclaredNode(created?.kg_node_id)}
        onUseExisting={pinDeclaredNode}
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
    </>
  );
};

ManualDependenciesSection.propTypes = {
  open: PropTypes.bool.isRequired,
  // kg_get_manual_node_schema: which endpoint types can be declared as nodes.
  schema: PropTypes.array.isRequired,
  // Bumped by the tab when a new dependency or an import changed the list.
  refreshKey: PropTypes.number,
  // Called after a node is declared from an unmatched endpoint.
  onNodesChanged: PropTypes.func,
};

export default ManualDependenciesSection;
