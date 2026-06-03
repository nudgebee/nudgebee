// KG node picker — Phase 2.6 (NB-30989).
//
// Three cascading FilterDropdowns let an operator pick an existing KG node
// directly instead of typing identifiers. Used inside the New Manual
// Dependency dialog when Kind === 'kg-pick'. The picked node's identifiers
// are projected into the parent form via onPick, and the parent pins the
// row to the node's UUID via kg_resolve_manual_dependency after Create.
//
// Cascade contract:
//   Account → kg_get_filter_options({accountIds:[X]}) → node_types
//   Node type → kg_get_filter_options({accountIds:[X], nodeTypes:[Y]}) → node_id_map
//   Node → kg_get_node(id) to fetch full identifiers → onPick(node)
//
// This mirrors the KG filter dropdown pattern in KnowledgeGraph.jsx — both
// dropdown levels are driven by node_id_map from kg_get_filter_options so
// the picker stays consistent with how the rest of the product reasons
// about cross-filtered nodes. The final getNode call fetches the node body
// so the parent can project identifiers onto the per-side form fields.
//
// FilterDropdown is the DS primitive per user's explicit ask; its built-in
// substring + glob search handles 500-node lists comfortably.
//
// Backend stays unchanged — Phase 2.6 is frontend-only.

import { useEffect, useMemo, useState } from 'react';
import { Box, Typography } from '@mui/material';
import PropTypes from 'prop-types';
import FilterDropdown from '@components1/ds/FilterDropdown';
import { toast as snackbar } from '@components1/ds/Toast';
import apiKnowledgeGraph from '@api1/knowledge-graph';
import { ds } from 'src/utils/colors';

const KgNodePicker = ({ pickedAccountId, pickedNodeType, pickedNodeId, cloudAccounts, onAccountChange, onNodeTypeChange, onPick }) => {
  const [nodeTypes, setNodeTypes] = useState([]);
  const [loadingTypes, setLoadingTypes] = useState(false);
  // nodeIdMap is the {unique_key: node_id} shape returned by
  // kg_get_filter_options — same shape KnowledgeGraph.jsx uses for its
  // node filter dropdown. We convert to FilterDropdown options below.
  const [nodeIdMap, setNodeIdMap] = useState({});
  const [loadingNodes, setLoadingNodes] = useState(false);
  const [fetchingNode, setFetchingNode] = useState(false);

  const accountOptions = useMemo(
    () =>
      cloudAccounts.map((acc) => ({
        value: acc.id,
        label: [acc.account_name || acc.account_number || acc.id, acc.cloud_provider, acc.account_number].filter(Boolean).join(' · '),
      })),
    [cloudAccounts]
  );

  // Account picked → fetch node types present in that account. The same
  // call also returns a node_id_map spanning all types in the account; we
  // re-fetch on type-pick to narrow.
  useEffect(() => {
    if (!pickedAccountId) {
      setNodeTypes([]);
      return undefined;
    }
    let cancelled = false;
    setLoadingTypes(true);
    apiKnowledgeGraph
      .getFilterOptions({ accountIds: [pickedAccountId] })
      .then((res) => {
        if (cancelled) {
          return;
        }
        const types = res?.data?.data?.kg_get_filter_options?.data?.node_types ?? [];
        setNodeTypes(types);
      })
      .catch((err) => {
        console.error('Failed to load KG node types for account:', err);
        snackbar.error('Failed to load node types for the selected account.');
      })
      .finally(() => {
        if (!cancelled) {
          setLoadingTypes(false);
        }
      });
    return () => {
      cancelled = true;
    };
  }, [pickedAccountId]);

  // Type picked → re-call kg_get_filter_options with both account and
  // type filters; node_id_map now narrows to nodes of that type in that
  // account. Same endpoint as the type-list call above — keeps the API
  // surface tight and mirrors KnowledgeGraph.jsx's filter pattern.
  useEffect(() => {
    if (!pickedAccountId || !pickedNodeType) {
      setNodeIdMap({});
      return undefined;
    }
    let cancelled = false;
    setLoadingNodes(true);
    apiKnowledgeGraph
      .getFilterOptions({ accountIds: [pickedAccountId], nodeTypes: [pickedNodeType] })
      .then((res) => {
        if (cancelled) {
          return;
        }
        const map = res?.data?.data?.kg_get_filter_options?.data?.node_id_map ?? {};
        setNodeIdMap(map);
      })
      .catch((err) => {
        console.error('Failed to load KG nodes:', err);
        snackbar.error('Failed to load nodes.');
      })
      .finally(() => {
        if (!cancelled) {
          setLoadingNodes(false);
        }
      });
    return () => {
      cancelled = true;
    };
  }, [pickedAccountId, pickedNodeType]);

  const nodeTypeOptions = useMemo(() => nodeTypes.map((t) => ({ value: t, label: t })), [nodeTypes]);

  // node_id_map is { unique_key: node_id }. Label = unique_key (composite
  // identifier string — same display as the KG filter dropdown); value =
  // node_id (UUID, what FilterDropdown returns to onSelect).
  const nodeOptions = useMemo(
    () =>
      Object.entries(nodeIdMap).map(([uniqueKey, id]) => ({
        value: id,
        label: uniqueKey,
      })),
    [nodeIdMap]
  );

  // On Node pick, fetch the full node body via kg_get_node so the parent
  // can project identifiers (name, namespace, cluster, properties.arn, …)
  // onto the per-side form fields. node_id_map only carries id+unique_key
  // so we need the second call to get the full node object.
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
        // kg_get_node returns {data: jsonb} per the action schema; the
        // node body lives directly in `.data`. The earlier `node` field
        // name was a stale wrapper bug that never tripped because this
        // RPC had no callers before Phase 2.6.
        const node = res?.data?.data?.kg_get_node?.data;
        if (!node) {
          snackbar.error('Failed to fetch picked node details.');
          onPick(null);
          return;
        }
        // The node body is a jsonb blob; normalize to the shape onPick
        // expects (id + flat properties used by the parent's projection).
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
  return (
    <Box sx={{ display: 'flex', flexDirection: 'column', gap: 1.25 }}>
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
        disabled={!pickedAccountId}
        isOptionsLoading={loadingTypes}
        size='sm'
        searchPlaceholder='Search node types'
      />

      <FilterDropdown
        label='Node *'
        placeholder={!pickedNodeType ? 'Pick a node type first' : totalNodes === 0 && !loadingNodes ? 'No nodes found' : 'Pick a node'}
        options={nodeOptions}
        value={pickedNodeId || null}
        onSelect={handleNodePicked}
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

      {pickedNodeType && totalNodes > 0 && (
        <Typography sx={{ fontSize: '11px', color: ds?.text?.secondaryDark ?? '#6b7280', fontStyle: 'italic' }}>
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
  cloudAccounts: PropTypes.array.isRequired,
  onAccountChange: PropTypes.func.isRequired,
  onNodeTypeChange: PropTypes.func.isRequired,
  onPick: PropTypes.func.isRequired,
};

export default KgNodePicker;
