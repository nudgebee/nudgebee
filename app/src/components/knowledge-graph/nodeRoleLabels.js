// How a Knowledge Graph node's role (database, cache, ...) is shown. Pure
// helpers, no data loading: the vocabulary hook lives in nodeRoles.js.
//
// The server owns the vocabulary (kg_list_node_roles); these labels render a
// role before it loads and if it cannot be loaded, so a card never shows a raw
// value like "messagequeue".

export const NODE_ROLE_LABELS = {
  database: 'Database',
  cache: 'Cache',
  messagequeue: 'Queue',
  search: 'Search',
  objectstorage: 'Object storage',
  loadbalancer: 'Load balancer',
  proxy: 'Proxy',
  apigateway: 'API gateway',
  scheduler: 'Scheduler',
  monitoring: 'Monitoring',
  auth: 'Auth',
};

// The label for a role, or '' for none or a value outside the vocabulary (a
// custom kind may carry a free "role" such as "core-switch").
export const nodeRoleLabel = (role) => NODE_ROLE_LABELS[role] || '';

// A detected role as text: its label, or the value itself when it is outside
// the vocabulary (a custom kind's free "core-switch" a user overrode).
export const detectedRoleText = (role) => nodeRoleLabel(role) || role || '';

// What the role part of a node's properties says, for display.
export const nodeRoleState = (properties) => {
  const p = properties || {};
  const userSet = p.role_source === 'user';
  const role = nodeRoleLabel(p.role) ? p.role : '';
  // What the classifier detected: kept aside when a user overrode it, or the
  // role itself when no user set one.
  const detectedRole = userSet ? p.detected_role || '' : role;
  return {
    role,
    engine: typeof p.engine === 'string' ? p.engine : '',
    userSet,
    detectedRole,
  };
};

// Keys the role overlay keeps on a node to say where its role came from.
// They are shown through the Role row, never as raw properties.
export const NODE_ROLE_BOOKKEEPING_KEYS = ['role_source', 'detected_role', 'detected_engine'];

// Whether a node of this type can be given a role. While the list is unknown
// the answer is yes and the server refuses what it does not accept.
export const canCarryRole = (targetNodeTypes, nodeType) => !targetNodeTypes || targetNodeTypes.includes(nodeType);
