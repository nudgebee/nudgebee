// Manual Declarations tab — what a user declares by hand: dependencies the
// Knowledge Graph cannot see (NB-30989) and the nodes those dependencies
// point at when no collector has discovered them (NB-39830).
//
// Owns the schema fetch both sections need, creating a node or a dependency,
// and the JSON import that declares both at once. Each section owns its own
// list, filters and per-row actions; refreshKey tells a section its list
// changed from out here.

import { useCallback, useEffect, useState } from 'react';
import { Box, Typography } from '@mui/material';
import PropTypes from 'prop-types';
import { Button } from '@ui/Button';
import { UploadIcon, PlusIcon, PlusIconSecondary } from '@assets';
import SafeIcon from '@shared/icons/SafeIcon';
import apiKnowledgeGraph from '@api1/knowledge-graph';
import ManualNodesSection from './ManualNodesSection';
import ManualDependenciesSection from './ManualDependenciesSection';
import JsonImportDialog from './JsonImportDialog';
import EditManualNodeDialog from './EditManualNodeDialog';
import EditManualDependencyDialog from './EditManualDependencyDialog';
import { ds } from 'src/utils/colors';

const ManualDeclarationsTab = ({ open }) => {
  const [schema, setSchema] = useState([]);
  const [importOpen, setImportOpen] = useState(false);
  const [nodeDialogOpen, setNodeDialogOpen] = useState(false);
  const [depDialogOpen, setDepDialogOpen] = useState(false);
  const [nodesRefresh, setNodesRefresh] = useState(0);
  const [depsRefresh, setDepsRefresh] = useState(0);

  const refreshNodes = useCallback(() => setNodesRefresh((n) => n + 1), []);
  const refreshDeps = useCallback(() => setDepsRefresh((n) => n + 1), []);

  useEffect(() => {
    if (!open) {
      return;
    }
    apiKnowledgeGraph
      .getManualNodeSchema()
      .then((res) => {
        const payload = res?.data?.data?.kg_get_manual_node_schema?.data;
        setSchema(Array.isArray(payload) ? payload : []);
      })
      .catch((err) => console.error('Failed to fetch the manual node schema:', err));
  }, [open]);

  return (
    <Box sx={{ display: 'flex', flexDirection: 'column', gap: ds.space[4], py: ds.space[4] }}>
      <Typography sx={{ fontSize: ds.text.small, color: ds.gray[500] }}>
        Declare service-to-service dependencies (CALLS / PUBLISHES_TO / SUBSCRIBES_TO) so the Knowledge Graph can correlate alerts even without
        distributed traces, and declare the resources no collector has discovered so those dependencies have something to point at. Import one JSON
        document to declare nodes and dependencies together.
      </Typography>

      <Box sx={{ display: 'flex', justifyContent: 'flex-end', flexWrap: 'wrap', gap: ds.space[2] }}>
        <Button
          tone='secondary'
          size='md'
          onClick={() => setImportOpen(true)}
          icon={<SafeIcon src={UploadIcon} alt='upload' width={14} height={14} />}
          id='manual-declarations-import-json'
        >
          Import JSON
        </Button>
        <Button
          tone='secondary'
          size='md'
          onClick={() => setNodeDialogOpen(true)}
          // PlusIcon is drawn white for primary buttons; a secondary one needs the dark variant.
          icon={<SafeIcon src={PlusIconSecondary} alt='add' width={14} height={14} />}
          id='manual-nodes-new'
        >
          Declare node
        </Button>
        <Button
          tone='primary'
          size='md'
          onClick={() => setDepDialogOpen(true)}
          icon={<SafeIcon src={PlusIcon} alt='add' width={14} height={14} />}
          id='manual-deps-new'
        >
          Declare dependency
        </Button>
      </Box>

      <ManualNodesSection open={open} schema={schema} refreshKey={nodesRefresh} />
      <ManualDependenciesSection open={open} schema={schema} refreshKey={depsRefresh} onNodesChanged={refreshNodes} />

      <JsonImportDialog
        open={importOpen}
        schema={schema}
        onClose={() => setImportOpen(false)}
        onImported={() => {
          setImportOpen(false);
          refreshNodes();
          refreshDeps();
        }}
      />
      <EditManualNodeDialog
        open={nodeDialogOpen}
        row={null}
        schema={schema}
        onClose={() => setNodeDialogOpen(false)}
        onSaved={() => {
          setNodeDialogOpen(false);
          refreshNodes();
        }}
      />
      <EditManualDependencyDialog
        open={depDialogOpen}
        row={null}
        onClose={() => setDepDialogOpen(false)}
        onSaved={() => {
          setDepDialogOpen(false);
          refreshDeps();
        }}
      />
    </Box>
  );
};

ManualDeclarationsTab.propTypes = {
  open: PropTypes.bool.isRequired,
};

export default ManualDeclarationsTab;
