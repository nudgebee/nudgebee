// Manual Nodes tab — nodes a user declares by hand so dependencies can point
// at resources no collector has discovered.
//
// Owns the data fetch (kg_list_manual_nodes + kg_get_manual_node_schema), the
// declare/edit dialog, the CSV import and every mutation; ManualNodesTable
// only renders.

import { useCallback, useEffect, useMemo, useState } from 'react';
import { Box, Typography } from '@mui/material';
import PropTypes from 'prop-types';
import ListingLayout from '@ui/ListingLayout';
import { Button } from '@ui/Button';
import { toast as snackbar } from '@ui/Toast';
import Loader from '@shared/Loader';
import { UploadIcon, PlusIcon } from '@assets';
import SafeIcon from '@shared/icons/SafeIcon';
import apiKnowledgeGraph from '@api1/knowledge-graph';
import ManualNodesTable from './ManualNodesTable';
import EditManualNodeDialog from './EditManualNodeDialog';
import CsvImportDialog from './CsvImportDialog';
import ManualNodeCsvTypeValues from './ManualNodeCsvTypeValues';
import ConfirmDialog from './ConfirmDialog';
import { ds } from 'src/utils/colors';

// Column reference for the node CSV. Mirrors csvRowToManualNode on the backend
// (manual_node_repository.go): any column not listed here is taken as an
// optional property of the row's type.
const MANUAL_NODES_CSV_SCHEMA = [
  { name: 'name', required: true, purpose: 'The resource name as the cloud console shows it.', example: 'orders-db' },
  {
    name: 'cloud_account',
    required: false,
    purpose: 'Connected account id or account number. Blank = not in a connected account.',
    example: '123456789012',
  },
  {
    name: 'specific_type',
    required: false,
    purpose: 'Concrete type in a connected account (required there). Values are listed below.',
    example: 'RDSInstance',
  },
  { name: 'node_type', required: false, purpose: 'Type outside a connected account (required there). Values are listed below.', example: 'Database' },
  { name: 'region', required: false, purpose: 'Region; required in a connected account.', example: 'us-east-1' },
  { name: 'network', required: false, purpose: 'VPC id or name, for types keyed by their VPC.', example: 'vpc-0abc123' },
  { name: 'namespace', required: false, purpose: 'Kubernetes namespace (required in a cluster); optional grouping elsewhere.', example: 'payments' },
  { name: 'description', required: false, purpose: 'What it is and who owns it.', example: 'billing primary' },
  { name: 'labels', required: false, purpose: 'key=value pairs separated by ;', example: 'team=payments;env=prod' },
];

const MANUAL_NODES_CSV_CONFIG = {
  title: 'Import Manual Nodes (CSV)',
  schema: MANUAL_NODES_CSV_SCHEMA,
  requiredColumns: ['name'],
  requiredNote: (
    <>
      Required: <strong>name</strong>, plus <strong>specific_type</strong> in a connected account (with <strong>region</strong> for a cloud resource,{' '}
      <strong>namespace</strong> for a Kubernetes one), or <strong>node_type</strong> outside one. Any other column is an optional property of the
      type: a key/value one such as annotations is written like labels, a list such as subnet_ids as <strong>a;b</strong>.
    </>
  ),
  importCsv: (csv) => apiKnowledgeGraph.importManualNodes({ csv }),
  resultKey: 'kg_import_manual_nodes',
};

const ManualNodesTab = ({ open }) => {
  const [rows, setRows] = useState([]);
  const [schema, setSchema] = useState([]);
  const [loading, setLoading] = useState(false);
  const [csvDialogOpen, setCsvDialogOpen] = useState(false);
  const [editDialogOpen, setEditDialogOpen] = useState(false);
  const [editRow, setEditRow] = useState(null);
  const [confirmState, setConfirmState] = useState(null);
  const [confirmSubmitting, setConfirmSubmitting] = useState(false);

  // The CSV dialog lists the type values this tenant's schema accepts.
  const csvConfig = useMemo(() => ({ ...MANUAL_NODES_CSV_CONFIG, valuesPanel: <ManualNodeCsvTypeValues schema={schema} /> }), [schema]);

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

  const fetchSchema = useCallback(async () => {
    try {
      const res = await apiKnowledgeGraph.getManualNodeSchema();
      const payload = res?.data?.data?.kg_get_manual_node_schema?.data ?? [];
      setSchema(Array.isArray(payload) ? payload : []);
    } catch (err) {
      console.error('Failed to fetch the manual node schema:', err);
    }
  }, []);

  useEffect(() => {
    if (!open) {
      return;
    }
    fetchRows();
    fetchSchema();
  }, [open, fetchRows, fetchSchema]);

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
    <Box sx={{ display: 'flex', flexDirection: 'column', gap: ds.space[4], py: ds.space[4] }}>
      <Typography sx={{ fontSize: ds.text.small, color: ds.gray[500] }}>
        Declare resources the Knowledge Graph has not discovered — an on-prem database, a third-party API, a resource in an account without a
        collector — so manual dependencies can point at them. A node declared in a connected account is adopted by the collector when it discovers the
        same resource.
      </Typography>

      <ListingLayout>
        <ListingLayout.Toolbar>
          <Box sx={{ flex: 1 }} />
          {rows.length > 0 && (
            <Button tone='danger' size='md' onClick={handleDeleteAllClick} id='manual-nodes-delete-all'>
              Delete all
            </Button>
          )}
          <Button
            tone='secondary'
            size='md'
            onClick={() => setCsvDialogOpen(true)}
            icon={<SafeIcon src={UploadIcon} alt='upload' width={14} height={14} />}
            id='manual-nodes-import-csv'
          >
            Import CSV
          </Button>
          <Button
            tone='primary'
            size='md'
            onClick={() => {
              setEditRow(null);
              setEditDialogOpen(true);
            }}
            icon={<SafeIcon src={PlusIcon} alt='add' width={14} height={14} />}
            id='manual-nodes-new'
          >
            New node
          </Button>
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

      <CsvImportDialog
        open={csvDialogOpen}
        onClose={() => setCsvDialogOpen(false)}
        onImported={async () => {
          setCsvDialogOpen(false);
          await refresh();
        }}
        config={csvConfig}
      />
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
    </Box>
  );
};

ManualNodesTab.propTypes = {
  open: PropTypes.bool.isRequired,
};

export default ManualNodesTab;
