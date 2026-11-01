import { parseDateValue } from '@shared/format/Datetime';

// Annotations that only mark a rollout. A change touching nothing but these restarted the
// workload; it did not alter its configuration. Reverting one removes the marker, undoes nothing,
// and writing to the pod template can trigger a further rollout — so the option is worse than
// useless, it is misleading. On dev these are the two most-changed paths of all (rollme 1146,
// restartedAt 239 over 7 days), so offering revert on them is the common case, not the edge.
export const ROLLOUT_ONLY_ANNOTATIONS = [
  'kubectl.kubernetes.io/restartedAt',
  'rollme',
  'deployment.kubernetes.io/revision',
  'kubectl.kubernetes.io/last-applied-configuration',
];

export const isRolloutOnlyKey = (key) => ROLLOUT_ONLY_ANNOTATIONS.some((a) => key === a || key.endsWith(`/${a}`) || key.endsWith(`.${a}`));

// Some producers prefix the path with the object's kind ("Deployment.spec.replicas").
export const normalizePath = (path) => (typeof path === 'string' ? path.replace(/^(StatefulSet|DaemonSet|Deployment|ReplicaSet|Rollout)\./, '') : '');

// describeChangeTiming says how long before the alert a change landed. Proximity is the operator's
// main clue to whether a change is related at all — a deploy three weeks before an OOM rarely is —
// and the row used to state only a clock time, leaving the subtraction to them.
export const describeChangeTiming = (changeAt, alertAt) => {
  if (!changeAt || !alertAt) return '';
  const gap = parseDateValue(alertAt).getTime() - parseDateValue(changeAt).getTime();
  if (!Number.isFinite(gap)) return '';
  const abs = Math.abs(gap);
  const minutes = Math.round(abs / 60000);
  const hours = Math.round(abs / 3600000);
  let span;
  if (minutes < 1) span = 'less than a minute';
  else if (minutes < 60) span = `${minutes} min`;
  else if (hours < 48) span = `${hours}h`;
  else span = `${Math.round(abs / 86400000)} days`;
  return gap >= 0 ? `${span} before the alert` : `${span} after the alert started`;
};

// The longest value a row states in full; an image reference or a command is left to the dialog.
const ROW_VALUE_MAX = 32;

// One side of a change as a row can state it: '(not set)' when the field is absent on that side,
// undefined when the value is an object or too long to read in a row.
const rowValue = (value) => {
  if (value === null || value === undefined) return '(not set)';
  if (typeof value === 'object') return undefined;
  const text = String(value).trim();
  if (!text || text.length > ROW_VALUE_MAX || text.startsWith('{') || text.startsWith('[')) return undefined;
  return text;
};

const CONTAINER_PREFIX = /^(init)?[cC]ontainers\[[^\]]+\]\./;

const withoutSpecPrefix = (path) =>
  path
    .replace(/^spec\.template\.spec\./, '')
    .replace(/^spec\.template\.metadata\./, '')
    .replace(/^spec\.template\./, '')
    .replace(/^spec\./, '');

/**
 * What rolling a change back writes, field by field, as "running now → after rollback". The row
 * used to say only which fields a change touched ("changes resources"), so a rollback that lowers
 * a memory limit on an out-of-memory alert read the same as one that raises it. Only short plain
 * values are stated; the rest are counted and left to the dialog, which shows every field.
 *
 * @param {{path?: string, old?: *, new?: *}[]} fields the change's recorded fields
 * @param {number} limit how many fields to state
 */
export const describeRollbackWrites = (fields, limit = 2) => {
  const changed = (Array.isArray(fields) ? fields : [])
    .map((f) => ({ path: withoutSpecPrefix(normalizePath(f?.path)), now: rowValue(f?.new), after: rowValue(f?.old) }))
    .filter((f) => f.path && !isRolloutOnlyKey(f.path));
  // The container is dropped from the label only when every field is in the same one; with two
  // containers "resources.limits.memory" twice would not say which value belongs to which.
  const containers = new Set(changed.map((f) => CONTAINER_PREFIX.exec(f.path)?.[0]).filter(Boolean));
  const stated = [];
  let more = 0;
  changed.forEach(({ path, now, after }) => {
    if (now === undefined || after === undefined || stated.length >= limit) {
      more += 1;
      return;
    }
    stated.push(`${containers.size > 1 ? path : path.replace(CONTAINER_PREFIX, '')} ${now} → ${after}`);
  });
  if (stated.length === 0) return '';
  return more > 0 ? `${stated.join(', ')} and ${more} more` : stated.join(', ');
};

// "deployment/<namespace>/<name>.yaml" → "Deployment <namespace>/<name>", naming the kind the revert
// acts on — a DaemonSet revert restarts a pod on every node, which "Deployment" does not suggest.
export const revertTargetLabel = (resourceName) => {
  const parts = String(resourceName || '')
    .replace(/\.yaml$/i, '')
    .split('/');
  if (parts.length !== 3) return '';
  const kind = { deployment: 'Deployment', statefulset: 'StatefulSet', daemonset: 'DaemonSet', rollout: 'Rollout', replicaset: 'ReplicaSet' }[
    parts[0].toLowerCase()
  ];
  return kind ? `${kind} ${parts[1]}/${parts[2]}` : '';
};

// Recorded paths address list items by POSITION ("containers[1].env[2]"), and a revert is written at
// that position. These mirror the server's conflict rules (event/service.go laterChangeConflict), so
// the page never offers a rollback the server would refuse.

/** Whether `ancestor` names a field or list item that contains `path`. */
export const isPathAncestor = (ancestor, path) => path.startsWith(`${ancestor}.`) || path.startsWith(`${ancestor}[`);

/**
 * Whether adding or removing the list item at `itemPath` ("…env[1]") moves an item `path` addresses
 * by position ("…env[2].value"): same list, at or before it.
 */
export const shiftsListPosition = (itemPath, path) => {
  const item = /^(.*)\[(\d+)\]$/.exec(itemPath);
  if (!item) return false;
  const [, list, changedAt] = item;
  const positions = /\[(\d+)\]/g;
  let match;
  while ((match = positions.exec(path))) {
    if (path.slice(0, match.index) === list) return Number(changedAt) <= Number(match[1]);
  }
  return false;
};

const snakeCase = (key) => key.replace(/[A-Z]/g, (c) => `_${c.toLowerCase()}`);

// Reads one key from a manifest the collector serialised snake_case ("init_containers"), while the
// recorded paths are camelCase ("initContainers").
const readKey = (obj, key) => (obj && typeof obj === 'object' ? (key in obj ? obj[key] : obj[snakeCase(key)]) : undefined);

/**
 * A recorded path with list positions replaced by the items' names where the manifests know them:
 * "spec.template.spec.containers[1].env[2]" → "spec.template.spec.containers[sidecar].env[LLM_URL]".
 * With several containers, "containers[1]" says nothing about which one a rollback touches. Looks in
 * the manifest after the change first, then before it (a removed item exists only there), then in the
 * recorded value itself when the path names a whole item.
 *
 * @param {string} path
 * @param {object[]} manifests parsed manifests, most relevant first
 * @param {*} itemValue the recorded value when `path` ends at a list item
 */
export const nameListPositions = (path, manifests, itemValue) => {
  const tokens = String(path || '').match(/[^.[\]]+|\[\d+\]/g) || [];
  const docs = (manifests || []).filter(Boolean);
  let cursors = docs;
  let out = '';
  tokens.forEach((token, index) => {
    const position = /^\[(\d+)\]$/.exec(token);
    if (!position) {
      out += out ? `.${token}` : token;
      cursors = cursors.map((c) => readKey(c, token));
      return;
    }
    const items = cursors.map((c) => (Array.isArray(c) ? c[Number(position[1])] : undefined));
    const isLast = index === tokens.length - 1;
    const named = items.find((item) => item && typeof item === 'object' && typeof item.name === 'string');
    const name = named?.name || (isLast && itemValue && typeof itemValue === 'object' ? itemValue.name : undefined);
    out += name ? `[${name}]` : token;
    cursors = items;
  });
  return out;
};
