// The role vocabulary a Knowledge Graph node can take, loaded once per page
// from kg_list_node_roles. Display helpers are in nodeRoleLabels.js and are
// re-exported here.

import { useEffect, useState } from 'react';
import apiKnowledgeGraph from '@api1/knowledge-graph';
import { NODE_ROLE_LABELS } from './nodeRoleLabels';

export { NODE_ROLE_LABELS, NODE_ROLE_BOOKKEEPING_KEYS, canCarryRole, detectedRoleText, nodeRoleLabel, nodeRoleState } from './nodeRoleLabels';

let vocabularyPromise = null;

// Resets the cached vocabulary (tests).
export const resetNodeRoleVocabulary = () => {
  vocabularyPromise = null;
};

const fallbackVocabulary = () => ({
  roles: Object.entries(NODE_ROLE_LABELS).map(([value, label]) => ({ value, label })),
  targetNodeTypes: null, // unknown: let the server decide
});

const loadVocabulary = () => {
  if (!vocabularyPromise) {
    // Started inside the chain so any failure, a throw included, falls back.
    vocabularyPromise = Promise.resolve()
      .then(() => apiKnowledgeGraph.listNodeRoles())
      .then((data) => (data?.roles?.length ? { roles: data.roles, targetNodeTypes: data.target_node_types ?? null } : fallbackVocabulary()))
      .catch(() => {
        vocabularyPromise = null; // try again next time
        return fallbackVocabulary();
      });
  }
  return vocabularyPromise;
};

// The roles to offer and the node types that may carry one. Loaded once per
// page; targetNodeTypes is null until known.
export const useNodeRoles = () => {
  const [vocabulary, setVocabulary] = useState(fallbackVocabulary);
  useEffect(() => {
    let active = true;
    loadVocabulary().then((v) => active && setVocabulary(v));
    return () => {
      active = false;
    };
  }, []);
  return vocabulary;
};
