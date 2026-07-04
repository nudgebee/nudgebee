// KG node picker — Phase 2.6 (NB-30989).
//
// Three cascading FilterDropdowns let an operator pick an existing KG node
// directly instead of typing identifiers. Used inside the Manual Dependency
// dialog when Kind === 'kg-pick'. The picked node's identifiers are projected
// into the parent form via onPick, and the parent pins the row to the node's
// UUID via kg_resolve_manual_dependency.
//
// Cascade contract:
//   Account → kg_get_filter_options({accountIds:[X]}) → node_types
//   Node type → kg_get_filter_options({accountIds:[X], nodeTypes:[Y]}) → node_id_map
//   Node → kg_get_node(id) to fetch full identifiers → onPick(node)
//
// Loading is lazy: the type / node lists are fetched only when their dropdown
// is opened (FilterDropdown.onOpen), not eagerly. When the dialog opens an
// already-pinned side in edit mode it passes the selected account/type/node so
// the dropdowns DISPLAY the pin (via single seed options) without fetching the
// full lists — the operator only pays for a list when they open it to re-pick.
//
// Backend stays unchanged — Phase 2.6 is frontend-only.

import { useEffect, useMemo, useState } from 'react';
import { Box, Typography } from '@mui/material';
import PropTypes from 'prop-types';
import FilterDropdown from '@ui/FilterDropdown';
import { toast as snackbar } from '@ui/Toast';
import apiKnowledgeGraph from '@api1/knowledge-graph';
import { ds } from 'src/utils/colors';

const KgNodePicker = ({ pickedAccountId, pickedNodeType, pickedNodeId, selectedLabel, cloudAccounts, onAccountChange, onNodeTypeChange, onPick }) => {
  const [nodeTypes, setNodeTypes] = useState([]);
  const [loadingTypes, setLoadingTypes] = useState(false);
  // Account the type list was last loaded for — guards against refetching the
  // same list on every dropdown open.
  const [loadedTypesAccount, setLoadedTypesAccount] = useState('');
  // nodeIdMap is the {unique_key: node_id} shape returned by
  // kg_get_filter_options — same shape KnowledgeGraph.jsx uses for its node
  // filter dropdown. We convert to FilterDropdown options below.
  const [nodeIdMap, setNodeIdMap] = useState({});
  const [loadingNodes, setLoadingNodes] = useState(false);
  const [loadedNodesKey, setLoadedNodesKey] = useState('');
  const [fetchingNode, setFetchingNode] = useState(false);

  const accountOptions = useMemo(
    () =>
      cloudAccounts.map((acc) => ({
        value: acc.id,
        label: [acc.account_name || acc.account_number || acc.id, acc.cloud_provider, acc.account_number].filter(Boolean).join(' · '),
      })),
    [cloudAccounts]
  );

  // Account changed → drop the cached type + node lists so the next time a
  // downstream dropdown is opened it re-fetches for the new account.
  useEffect(() => {
    setNodeTypes([]);
    setLoadedTypesAccount('');
    setNodeIdMap({});
    setLoadedNodesKey('');
  }, [pickedAccountId]);

  // Node type changed → drop the cached node list.
  useEffect(() => {
    setNodeIdMap({});
    setLoadedNodesKey('');
  }, [pickedNodeType]);

  // Lazy-load node types for the selected account — fired by the Node type
  // dropdown's onOpen, not eagerly on mount. Keeps an edited row that just
  // displays its pinned node from fetching lists the operator never opens.
  const loadNodeTypes = () => {
    if (!pickedAccountId || loadingTypes || loadedTypesAccount === pickedAccountId) {
      return;
    }
    setLoadingTypes(true);
    apiKnowledgeGraph
      .getFilterOptions({ accountIds: [pickedAccountId] })
      .then((res) => {
        const types = res?.data?.data?.kg_get_filter_options?.data?.node_types ?? [];
        setNodeTypes(types);
        setLoadedTypesAccount(pickedAccountId);
      })
      .catch((err) => {
        console.error('Failed to load KG node types for account:', err);
        snackbar.error('Failed to load node types for the selected account.');
      })
      .finally(() => setLoadingTypes(false));
  };

  // Lazy-load nodes for the selected account + type — fired by the Node
  // dropdown's onOpen.
  const nodesKey = `${pickedAccountId}::${pickedNodeType}`;
  const loadNodes = () => {
    if (!pickedAccountId || !pickedNodeType || loadingNodes || loadedNodesKey === nodesKey) {
      return;
    }
    setLoadingNodes(true);
    apiKnowledgeGraph
      .getFilterOptions({ accountIds: [pickedAccountId], nodeTypes: [pickedNodeType] })
      .then((res) => {
        const map = res?.data?.data?.kg_get_filter_options?.data?.node_id_map ?? {};
        setNodeIdMap(map);
        setLoadedNodesKey(nodesKey);
      })
      .catch((err) => {
        console.error('Failed to load KG nodes:', err);
        snackbar.error('Failed to load nodes.');
      })
      .finally(() => setLoadingNodes(false));
  };

  // Loaded node-type list, or — before it's loaded — a single seed option so a
  // pre-selected type still displays its label.
  const nodeTypeOptions = useMemo(() => {
    if (nodeTypes.length) {
      return nodeTypes.map((t) => ({ value: t, label: t }));
    }
    return pickedNodeType ? [{ value: pickedNodeType, label: pickedNodeType }] : [];
  }, [nodeTypes, pickedNodeType]);

  // Loaded node list ({unique_key: id}), or — before it's loaded — a single
  // seed option (selectedLabel) so a pre-selected node still displays.
  const nodeOptions = useMemo(() => {
    const entries = Object.entries(nodeIdMap);
    if (entries.length) {
      return entries.map(([uniqueKey, id]) => ({ value: id, label: uniqueKey }));
    }
    return pickedNodeId ? [{ value: pickedNodeId, label: selectedLabel || pickedNodeId }] : [];
  }, [nodeIdMap, pickedNodeId, selectedLabel]);

  // On Node pick, fetch the full node body via kg_get_node so the parent can
  // project identifiers (name, namespace, cluster, properties.arn, …) onto the
  // per-side form fields. node_id_map only carries id+unique_key so we need the
  // second call to get the full node object.
  const handleNodePicked = (e, value) => {
    const id = typeof value === 'string' ? value : value?.value;
    if (!id) {
      onPick(null);
      return;
    }
    setFetchingNode(true);
    apiKnowledgeGraph
      .getNode(id)
      .then((res) => {
        const node = res?.data?.data?.kg_get_node?.data;
        if (!node) {
          snackbar.error('Failed to fetch picked node details.');
          onPick(null);
          return;
        }
        // The node body is a jsonb blob; normalize to the shape onPick expects
        // (id + flat properties used by the parent's projection).
        onPick({
          id: node.id || id,
          node_type: node.node_type || '',
          name: node.name || node?.query_attributes?.name || node?.properties?.name || '',
          namespace: node.namespace || node?.query_attributes?.namespace || '',
          cluster: node.cluster || node?.query_attributes?.cluster || '',
          properties: node.properties || {},
        });
      })
      .catch((err) => {
        console.error('Failed to fetch picked node:', err);
        snackbar.error('Failed to fetch picked node details.');
      })
      .finally(() => setFetchingNode(false));
  };

  const totalNodes = Object.keys(nodeIdMap).length;
  const nodesLoaded = loadedNodesKey === nodesKey;
  return (
    <Box sx={{ display: 'flex', flexDirection: 'column', gap: ds.space.mul(1, 2.5) }}>
      {/* Shown only while the pinned node's account is being resolved; once it
          is, the dropdowns below display the pin themselves. */}
      {selectedLabel && pickedNodeId && !pickedAccountId && (
        <Box sx={{ p: ds.space[2], borderRadius: ds.radius.sm, backgroundColor: ds.brand[100], border: `1px solid ${ds.brand[200]}` }}>
          <Typography sx={{ fontSize: ds.text.small, fontWeight: ds.weight.medium, color: ds.gray[700] }}>
            Loading pinned node: {selectedLabel}…
          </Typography>
        </Box>
      )}

      <FilterDropdown
        label='Account *'
        placeholder='Pick an account'
        options={accountOptions}
        value={pickedAccountId || null}
        onSelect={(e, v) => onAccountChange(typeof v === 'string' ? v : v?.value || '')}
        size='sm'
        searchPlaceholder='Search accounts'
      />

      <FilterDropdown
        label='Node type *'
        placeholder={pickedAccountId ? 'Pick a node type' : 'Pick an account first'}
        options={nodeTypeOptions}
        value={pickedNodeType || null}
        onSelect={(e, v) => onNodeTypeChange(typeof v === 'string' ? v : v?.value || '')}
        onOpen={loadNodeTypes}
        disabled={!pickedAccountId}
        isOptionsLoading={loadingTypes}
        size='sm'
        searchPlaceholder='Search node types'
      />

      <FilterDropdown
        label='Node *'
        placeholder={!pickedNodeType ? 'Pick a node type first' : nodesLoaded && totalNodes === 0 ? 'No nodes found' : 'Pick a node'}
        options={nodeOptions}
        value={pickedNodeId || null}
        onSelect={handleNodePicked}
        onOpen={loadNodes}
        disabled={!pickedNodeType}
        isOptionsLoading={loadingNodes || fetchingNode}
        size='sm'
        searchPlaceholder='Search nodes by unique key'
        // unique_key labels are long composite identifiers
        // ({source}:{account}:{location}:{type}:{hierarchy}:{name}). The
        // default 220px popover truncates everything past the type segment
        // — give the menu room to render the meaningful tail in full.
        popoverWidth='520px'
      />

      {pickedNodeType && nodesLoaded && totalNodes > 0 && (
        <Typography sx={{ fontSize: ds.text.caption, color: ds.gray[500], fontStyle: 'italic' }}>
          {totalNodes} node{totalNodes === 1 ? '' : 's'} available — use type-ahead search to narrow.
        </Typography>
      )}
    </Box>
  );
};

KgNodePicker.propTypes = {
  pickedAccountId: PropTypes.string,
  pickedNodeType: PropTypes.string,
  pickedNodeId: PropTypes.string,
  selectedLabel: PropTypes.string,
  cloudAccounts: PropTypes.array.isRequired,
  onAccountChange: PropTypes.func.isRequired,
  onNodeTypeChange: PropTypes.func.isRequired,
  onPick: PropTypes.func.isRequired,
};

export default KgNodePicker;
