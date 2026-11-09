// Nodes section of the Manual Declarations tab — nodes a user declares by
// hand so dependencies can point at resources no collector has discovered.
//
// Owns the list fetch (kg_list_manual_nodes), the edit dialog and every
// per-row mutation; the tab owns the schema, declaring and the JSON import,
// and bumps refreshKey when they change the list. ManualNodesTable only
// renders.

import { useCallback, useEffect, useMemo, useState } from 'react';
import { Box } from '@mui/material';
import PropTypes from 'prop-types';
import ListingLayout from '@ui/ListingLayout';
import { Button } from '@ui/Button';
import { toast as snackbar } from '@ui/Toast';
import Loader from '@shared/Loader';
import apiKnowledgeGraph from '@api1/knowledge-graph';
import ManualNodesTable from './ManualNodesTable';
import EditManualNodeDialog from './EditManualNodeDialog';
import ConfirmDialog from './ConfirmDialog';
import { ds } from 'src/utils/colors';

const ManualNodesSection = ({ open, schema, refreshKey }) => {
  const [rows, setRows] = useState([]);
  const [loading, setLoading] = useState(false);
  const [editDialogOpen, setEditDialogOpen] = useState(false);
  const [editRow, setEditRow] = useState(null);
  const [confirmState, setConfirmState] = useState(null);
  const [confirmSubmitting, setConfirmSubmitting] = useState(false);

  const accountNames = useMemo(
    () =>
      Object.fromEntries(
        schema.filter((p) => p.cloud_account_id).map((p) => [p.cloud_account_id, p.account_name || p.account_number || p.cloud_account_id])
      ),
    [schema]
  );

  const fetchRows = useCallback(async ({ showLoader = true } = {}) => {
    if (showLoader) {
      setLoading(true);
    }
    try {
      const res = await apiKnowledgeGraph.listManualNodes();
      const errors = res?.data?.errors;
      if (errors?.length) {
        snackbar.error(`Failed to load manual nodes: ${errors[0]?.message ?? 'Unknown error'}`);
        return;
      }
      const payload = res?.data?.data?.kg_list_manual_nodes?.data ?? [];
      setRows(Array.isArray(payload) ? payload : []);
    } catch (err) {
      console.error('Failed to fetch manual nodes:', err);
      snackbar.error('Failed to load manual nodes.');
    } finally {
      if (showLoader) {
        setLoading(false);
      }
    }
  }, []);

  useEffect(() => {
    if (open) {
      fetchRows();
    }
  }, [open, fetchRows]);

  // Something outside the section changed the list: refresh without a loader.
  useEffect(() => {
    if (open && refreshKey) {
      fetchRows({ showLoader: false });
    }
  }, [open, refreshKey, fetchRows]);

  const refresh = () => fetchRows({ showLoader: false });

  const closeEdit = () => {
    setEditDialogOpen(false);
    setEditRow(null);
  };

  const handleDeleteClick = (row) => {
    setConfirmState({
      variant: 'delete-row',
      row,
      title: 'Delete manual node?',
      message:
        row.status === 'adopted' ? (
          <>
            A collector now provides <strong>{row.name}</strong>, so the node stays in the Knowledge Graph. Only the declaration is removed.
          </>
        ) : (
          <>
            This removes <strong>{row.name}</strong> from the Knowledge Graph together with every edge touching it. Dependencies that pointed at it
            become unmatched.
          </>
        ),
    });
  };

  const handleMergeClick = (row) => {
    setConfirmState({
      variant: 'merge',
      row,
      title: 'Merge into the collected node?',
      message: (
        <>
          Dependencies pointing at <strong>{row.name}</strong> move to the collected node with the same name, and this declaration is removed.
        </>
      ),
    });
  };

  const handleDeleteAllClick = () => {
    setConfirmState({
      variant: 'delete-all',
      title: 'Delete ALL manual nodes?',
      message: (
        <>
          This withdraws every declaration ({rows.length}) and removes each node that is still only declared, with its edges. Nodes a collector has
          adopted stay. Continue?
        </>
      ),
    });
  };

  const runConfirmed = async () => {
    const { variant, row } = confirmState;
    if (variant === 'delete-row') {
      const res = await apiKnowledgeGraph.deleteManualNode({ id: row.id });
      return { errors: res?.data?.errors, done: 'Manual node deleted.' };
    }
    if (variant === 'merge') {
      const res = await apiKnowledgeGraph.mergeManualNode({ id: row.id });
      const data = res?.data?.data?.kg_apply_manual_node_merge?.data ?? {};
      const moved = data.dependencies_repointed ?? 0;
      return { errors: res?.data?.errors, done: `Merged. ${moved} dependenc${moved === 1 ? 'y' : 'ies'} moved to the collected node.` };
    }
    const res = await apiKnowledgeGraph.deleteAllManualNodes();
    const data = res?.data?.data?.kg_delete_all_manual_nodes?.data ?? {};
    return { errors: res?.data?.errors, done: `Withdrew ${data.rows_deactivated ?? 0} declarations; removed ${data.nodes_tombstoned ?? 0} nodes.` };
  };

  const handleConfirm = async () => {
    if (!confirmState) {
      return;
    }
    setConfirmSubmitting(true);
    try {
      const { errors, done } = await runConfirmed();
      if (errors?.length) {
        snackbar.error(`Action failed: ${errors[0]?.message ?? 'Unknown error'}`);
        return;
      }
      snackbar.success(done);
      setConfirmState(null);
      await refresh();
    } catch (err) {
      console.error('Manual node action failed:', err);
      snackbar.error('Action failed.');
    } finally {
      setConfirmSubmitting(false);
    }
  };

  return (
    <>
      <ListingLayout id='manual-nodes-section'>
        <ListingLayout.Toolbar title='Nodes'>
          <Box sx={{ flex: 1 }} />
          {rows.length > 0 && (
            <Button tone='danger' size='md' onClick={handleDeleteAllClick} id='manual-nodes-delete-all'>
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
            <ManualNodesTable
              rows={rows}
              accountNames={accountNames}
              onEditClick={(row) => {
                setEditRow(row);
                setEditDialogOpen(true);
              }}
              onMergeClick={handleMergeClick}
              onDeleteClick={handleDeleteClick}
            />
          )}
        </ListingLayout.Body>
      </ListingLayout>

      <EditManualNodeDialog
        open={editDialogOpen}
        row={editRow}
        schema={schema}
        onClose={closeEdit}
        onSaved={async () => {
          closeEdit();
          await refresh();
        }}
      />
      <ConfirmDialog
        open={Boolean(confirmState)}
        title={confirmState?.title ?? ''}
        message={confirmState?.message ?? ''}
        confirmLabel={confirmState?.variant === 'merge' ? 'Merge' : confirmState?.variant === 'delete-all' ? 'Delete all' : 'Delete'}
        danger={confirmState?.variant !== 'merge'}
        submitting={confirmSubmitting}
        onConfirm={handleConfirm}
        onClose={() => setConfirmState(null)}
      />
    </>
  );
};

ManualNodesSection.propTypes = {
  open: PropTypes.bool.isRequired,
  // kg_get_manual_node_schema: account names and the edit form's fields.
  schema: PropTypes.array.isRequired,
  // Bumped by the tab when a declaration or an import changed the list.
  refreshKey: PropTypes.number,
};

export default ManualNodesSection;
