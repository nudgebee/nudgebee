import React, { useRef, useState } from 'react';
import PropTypes from 'prop-types';
import Datetime, { formatClockCompact } from '@shared/format/Datetime';
import {
  describeChangeTiming,
  describeRollbackWrites,
  isPathAncestor,
  isRolloutOnlyKey,
  normalizePath,
  shiftsListPosition,
} from '@components/k8s/investigate/changeRollback';
import { Modal } from '@ui/Modal';
import { Box, Typography } from '@mui/material';
import { Divider } from '@ui/Divider';
import InvestigateResolution from '@components/k8s/investigate/InvestigateResolution';
import LastDeploymentIcon from '@assets/investigation/last-deployment.svg';
import { toast as snackbar } from '@ui/Toast';
import { safeJSONParse } from 'src/utils/common';
import apiKubernetes from '@api1/kubernetes';
import { DiffViewer } from '@ui/DiffViewer';
import { Button } from '@ui/Button';
import { hasWriteAccess } from '@lib/auth';
import { ds } from '@utils/colors';

// The dialog used to title itself with event.subject_name — the POD, which the revert restarts as a
// side effect rather than the object it changes. resource_name carries the real target as
// "deployment/<namespace>/<name>.yaml"; show the workload name, falling back to the raw value when
// the shape is not that triple.
const revertTargetName = (resourceName) => {
  if (typeof resourceName !== 'string' || !resourceName) return '';
  const withoutExt = resourceName.replace(/\.yaml$/i, '');
  const parts = withoutExt.split('/');
  return parts[parts.length - 1] || withoutExt;
};

const isSingleDiff = (e) => e?.type === 'diff' && e?.additional_info?.action_name !== 'deployment_history';

// foldRepeatedChanges groups an event's diff evidences by the object they changed. The collector now
// sends one block per object, but events recorded before that carry one block per edit — five
// rewrites of an operator-managed ConfigMap rendered five identical cards and five Revert rows.
// Blocks arrive newest first, so the first block of each object is the one kept; the rest are
// skipped and carried along as that card's earlier edits, diff included, so each one can still be
// opened. A block with no resource_name is never folded.
export const foldRepeatedChanges = (evidences) => {
  const skip = new Set();
  const changes = new Map();
  const firstIndexByObject = new Map();
  (Array.isArray(evidences) ? evidences : []).forEach((e, index) => {
    if (!isSingleDiff(e)) return;
    const key = typeof e?.data?.resource_name === 'string' ? e.data.resource_name.trim().toLowerCase() : '';
    if (!key) return;
    if (!firstIndexByObject.has(key)) {
      firstIndexByObject.set(key, index);
      changes.set(index, [{ start_at: e.start_at, diff: e.data }]);
      return;
    }
    skip.add(index);
    changes.get(firstIndexByObject.get(key)).push({ start_at: e.start_at, diff: e.data });
  });
  return { skip, changes };
};

const WORKLOAD_KINDS = ['deployment', 'statefulset', 'daemonset', 'rollout', 'replicaset'];

// canRevertDiff decides whether the "What you can do" list may offer to revert one diff block.
//
// The server reverts by writing the recorded old values onto the ALERT's workload. A ConfigMap or
// Ingress edit is not a change to that workload, so reverting it there is wrong, and an operator-
// managed ConfigMap would be rewritten again anyway. And an alert carrying several diff blocks must
// name the change it means (change_event_id): without one the server would undo all of them at once.
export const canRevertDiff = (evidence, event) => {
  const parts = String(evidence?.data?.resource_name || '')
    .replace(/\.yaml$/i, '')
    .split('/');
  if (parts.length !== 3 || !WORKLOAD_KINDS.includes(parts[0].toLowerCase())) return false;
  if (event?.service_key && `${parts[1]}/${parts[2]}` !== event.service_key) return false;
  if (evidence?.additional_info?.change_event_id) return true;
  return (event?.evidences || []).filter(isSingleDiff).length === 1;
};

// isRevertableChange decides whether to offer a revert for one recorded change.
//
// The field summary is NOT required. It is a display convenience the server started sending
// recently; evidence recorded before that has none, and gating the action on it meant the card
// listed changes while the page above said "No fixes are available". The revert itself is built
// server-side from the change's own event, and the dialog fetches and shows that change's real
// diff before anything is dispatched — so the summary's absence costs a preview, not safety.
//
// What the summary DOES buy is knowing a change was only a restart or a scale, neither of which is
// a deploy to put back. With no summary we cannot tell, so the option is offered and the operator
// sees the real diff in the dialog; the server still refuses a revert that would clobber a later
// change.
const isRevertableChange = (d) =>
  !!d?.eventId &&
  !changedObjectKind(d) &&
  !(d.changedFields?.length > 0 && (isRolloutOnlyChange(d.changedFields) || isScaleOnlyChange(d.changedFields)));

// changedObjectKind names the object a history entry changed when it is NOT the workload — a
// ConfigMap the workload reads, say — and returns '' for the workload itself. The history lists those
// changes too, linked to the workload's resource, so the server's same-resource check lets a revert
// through and it writes the ConfigMap's `data.*` values onto the workload. Known from the title the
// collector writes ("ConfigMap ns/name was changed") or, for older entries, from data-only paths.
export const changedObjectKind = (d) => {
  const titled = typeof d?.title === 'string' ? d.title.match(/^([A-Za-z]+) \S+ was changed$/) : null;
  if (titled && !WORKLOAD_KINDS.includes(titled[1].toLowerCase())) return titled[1];
  const paths = (Array.isArray(d?.changedFields) ? d.changedFields : []).map((f) => normalizePath(f?.path));
  if (paths.length > 0 && paths.every((p) => /^(data|binaryData|stringData)\./.test(p))) return 'ConfigMap';
  return '';
};

// hasRenderableHistory reports whether this event carries a deployment-history evidence with at
// least one change on it — i.e. whether the list card will render.
const hasRenderableHistory = (event) => {
  const evidence = (event?.evidences || []).find((e) => e?.additional_info?.action_name === 'deployment_history');
  if (!evidence) return false;
  const parsed = safeJSONParse(evidence.data);
  return Array.isArray(parsed?.deployments) && parsed.deployments.length > 0;
};

// A value may arrive as an object (raw evidence) or as a JSON string (the trimmed summary), and an
// annotations-map change carries the annotation names inside the value rather than in the path.
const annotationKeysOf = (value) => {
  let parsed = value;
  if (typeof value === 'string') {
    try {
      parsed = JSON.parse(value);
    } catch {
      return null;
    }
  }
  if (!parsed || typeof parsed !== 'object' || Array.isArray(parsed)) return null;
  return Object.keys(parsed);
};

// isRolloutOnlyChange reports whether EVERY path a change touched is rollout bookkeeping. Every —
// a change that bumped an image and also stamped restartedAt is a real change and stays revertible.
const isRolloutOnlyChange = (fields) => {
  if (!Array.isArray(fields) || fields.length === 0) return false;
  return fields.every((f) => {
    const path = typeof f?.path === 'string' ? f.path : '';
    if (!path) return false;
    if (isRolloutOnlyKey(path)) return true;
    // A bare `...metadata.annotations` path says only that some annotation moved; which one is in
    // the value.
    if (path.endsWith('metadata.annotations')) {
      const keys = [...(annotationKeysOf(f?.new) || []), ...(annotationKeysOf(f?.old) || [])];
      return keys.length > 0 && keys.every(isRolloutOnlyKey);
    }
    return false;
  });
};

// isScaleOnlyChange reports whether a change moved nothing but the replica count. That is the work of
// whatever scales the workload — an HPA, KEDA, a cron that parks an environment overnight, someone
// running `kubectl scale` — not a deploy. Writing the old count back does not undo a deploy, and when
// the old count was 0 it takes the workload down: on dev a quarter of all revert rows were exactly
// that, offered as the fix.
export const isScaleOnlyChange = (fields) => {
  if (!Array.isArray(fields) || fields.length === 0) return false;
  const paths = fields.map((f) => normalizePath(f?.path));
  return paths.includes('spec.replicas') && paths.every((p) => p === 'spec.replicas' || (p && isRolloutOnlyKey(p)));
};

// markSupersededChanges annotates each change (newest first) with the nearest newer change that
// conflicts with rolling it back: one that moved the same field (or a parent or child of it) to a
// different value, or added or removed a list item at or before one of its positions — after which
// that position names a different item. Rolling such a change back would undo the later change or
// write into the wrong item, so the server refuses it — offering it anyway meant a click that could
// only fail. This mirrors the server's rule (laterChangeConflict), rollout annotations included:
// they are part of what a revert writes. A change whose field list was cut short (fields_omitted) cannot be judged
// from here and is left to the server.
// Mirrors the server's sameChangedValue: absent and null are the same, anything else by its JSON.
const sameChangedValue = (a, b) => (a ?? null) === (b ?? null) || JSON.stringify(a ?? null) === JSON.stringify(b ?? null);

export const markSupersededChanges = (deployments) =>
  (Array.isArray(deployments) ? deployments : []).map((d, index, all) => {
    if (!Array.isArray(d?.changedFields) || d.changedFields.length === 0 || d.fieldsOmitted > 0) return d;
    const mine = new Map(d.changedFields.map((f) => [normalizePath(f?.path), f?.new]));
    for (let j = index - 1; j >= 0; j--) {
      const later = all[j];
      const clash = (later?.changedFields || []).some((f) => {
        const path = normalizePath(f?.path);
        // The same change recorded twice is not a later change (the server skips these too). Compared
        // by content, as the server does: an object value parsed twice is never ===.
        if (mine.has(path) && sameChangedValue(mine.get(path), f?.new)) return false;
        // An add or remove shows as one side of the item absent.
        const addsOrRemoves = f?.old === null || f?.old === undefined || f?.new === null || f?.new === undefined;
        return [...mine.keys()].some(
          (p) => p === path || isPathAncestor(path, p) || isPathAncestor(p, path) || (addsOrRemoves && shiftsListPosition(path, p))
        );
      });
      if (clash) return { ...d, supersededBy: later.timestamp || '' };
    }
    return d;
  });

// summarizeChangedFields names what a change touched in a few words, so a row can say "image" rather
// than leaving the operator to open the dialog to learn what a rollback would undo.
export const summarizeChangedFields = (fields) => {
  const names = [];
  (Array.isArray(fields) ? fields : []).forEach((f) => {
    const path = normalizePath(f?.path);
    if (!path || isRolloutOnlyKey(path)) return;
    const short = path
      .replace(/^spec\.template\.spec\./, '')
      .replace(/^spec\.template\.metadata\./, '')
      .replace(/^spec\.template\./, '')
      .replace(/^spec\./, '')
      .replace(/^(init)?[cC]ontainers\[\d+\]\./, '')
      // The field, not the leaf: four resource values read as "resources", an env entry as "env".
      .split(/[.[]/)[0];
    if (short && !names.includes(short)) names.push(short);
  });
  if (names.length === 0) return '';
  return names.length > 2 ? `${names.slice(0, 2).join(', ')} and ${names.length - 2} more` : names.join(', ');
};

// Hours are how the server sizes the history window; 168 of them is a week, and reads better as one.
const describeWindow = (hours) => {
  const h = Number(hours);
  if (!Number.isFinite(h) || h <= 0) return '';
  return h % 24 === 0 ? `${h / 24} day${h === 24 ? '' : 's'}` : `${h} hours`;
};

// ChangedField renders one changed path as `old -> new`. A null value means the field
// did not exist on that side of the change: null old = the change added it, null new =
// the change removed it. Rendering either as an empty string would misstate what happened.
const changedValueLabel = (value, absentLabel) => {
  if (value === null || value === undefined) return absentLabel;
  return value === '' ? '(empty)' : value;
};

const ChangedField = ({ field }) => (
  <Box sx={{ marginBottom: 'var(--ds-space-2)' }}>
    <Typography sx={{ fontSize: 'var(--ds-text-small)', fontFamily: 'var(--ds-font-mono)', color: 'var(--ds-gray-700)' }}>{field.path}</Typography>
    <Box display={'flex'} flexDirection={'row'} alignItems={'center'} gap={ds.space.mul(0, 3)} flexWrap={'wrap'}>
      <Typography sx={{ fontSize: 'var(--ds-text-small)', fontFamily: 'var(--ds-font-mono)', color: 'var(--ds-red-600)', wordBreak: 'break-all' }}>
        {changedValueLabel(field.old, '(not set)')}
      </Typography>
      <Typography sx={{ fontSize: 'var(--ds-text-small)', color: 'var(--ds-gray-600)' }}>{'\u2192'}</Typography>
      <Typography sx={{ fontSize: 'var(--ds-text-small)', fontFamily: 'var(--ds-font-mono)', color: 'var(--ds-green-600)', wordBreak: 'break-all' }}>
        {changedValueLabel(field.new, '(removed)')}
      </Typography>
    </Box>
  </Box>
);

ChangedField.propTypes = {
  field: PropTypes.shape({
    path: PropTypes.string,
    old: PropTypes.string,
    new: PropTypes.string,
  }).isRequired,
};

// DeploymentHistoryContent lists every change recorded before the alert and lets the operator
// revert the one they choose. It owns the dialog state itself rather than routing through the
// page's single resolve slot, because that slot reverts the alert's own attached change and there
// is exactly one of it — the entire failure being fixed here is a page that could only ever act on
// that one change.
const DeploymentHistoryContent = ({ history, event, cardId, changeChoices }) => {
  const [revertChangeId, setRevertChangeId] = useState('');
  const [expandedId, setExpandedId] = useState('');
  const [diffState, setDiffState] = useState({ loading: false, error: '', old: '', new: '' });

  // Fetched per change rather than shipped with the list: the before/after manifests average
  // ~9.7KB each, and carrying them in this response is what drove services-server to OOM.
  // Each fetch claims a ticket, and only the newest ticket may write state. Toggling A then B then
  // A again can resolve out of order, and a late response would then paint one change's diff under
  // another change's row — showing the operator a diff that belongs to a different change is the
  // exact confusion this card exists to end, so it must not be reachable by clicking quickly.
  const diffRequestRef = useRef(0);

  const toggleDiff = (eventId) => {
    if (expandedId === eventId) {
      setExpandedId('');
      diffRequestRef.current += 1; // abandon any in-flight response for the row being collapsed
      return;
    }
    const ticket = diffRequestRef.current + 1;
    diffRequestRef.current = ticket;
    setExpandedId(eventId);
    setDiffState({ loading: true, error: '', old: '', new: '' });
    apiKubernetes
      .resolveEventRecord(eventId, event?.cloud_account_id)
      .then((res) => {
        if (diffRequestRef.current !== ticket) return;
        const changeEvent = res?.data?.events?.[0];
        const diff = (changeEvent?.evidences || []).find((e) => e?.type === 'diff')?.data;
        if (!diff?.old && !diff?.new) {
          setDiffState({ loading: false, error: 'No before/after manifest was recorded for this change.', old: '', new: '' });
          return;
        }
        setDiffState({ loading: false, error: '', old: diff.old || '', new: diff.new || '' });
      })
      .catch(() => {
        if (diffRequestRef.current !== ticket) return;
        setDiffState({ loading: false, error: 'That change could not be loaded.', old: '', new: '' });
      });
  };
  const { namespace, rolloutName, service, timeRangeHours, deployments } = history;
  const canRevert = hasWriteAccess();

  const handleSnackbar = (type, message) => {
    if (['success', 'error'].includes(type)) {
      snackbar[type](message);
    }
  };

  return (
    <Box>
      <Typography sx={{ fontSize: 'var(--ds-text-small)', color: 'var(--ds-gray-600)', marginBottom: 'var(--ds-space-2)' }}>
        {/* rollout_name is the workload the changes were made to; service_name is the alert's own
            subject, which for a pod-level alert is the pod. Naming the pod here says a pod was
            changed, which is not what happened. */}
        {`${deployments.length} change${deployments.length === 1 ? '' : 's'} to ${rolloutName || service} in ${namespace}${
          describeWindow(timeRangeHours) ? ` over the ${describeWindow(timeRangeHours)} before this alert` : ' before this alert'
        }`}
      </Typography>
      {deployments.map((d, index) => (
        <Box key={d.eventId || index} sx={{ paddingBottom: 'var(--ds-space-3)' }}>
          {/* One line per change: when it happened on the left, what you can do about it on the
              right. The actions used to stack under the row as full-height buttons with a sentence
              between them, which made three changes read as a wall of controls rather than a list
              of changes. */}
          <Box display={'flex'} flexDirection={'row'} justifyContent={'space-between'} alignItems={'center'} gap={ds.space.mul(0, 3)}>
            <Box display={'flex'} flexDirection={'row'} alignItems={'baseline'} gap={ds.space.mul(0, 2)} sx={{ minWidth: 0 }}>
              <Typography
                sx={{
                  color: 'var(--ds-gray-900)',
                  fontSize: 'var(--ds-text-body)',
                  fontWeight: 'var(--ds-font-weight-medium)',
                  whiteSpace: 'nowrap',
                }}
              >
                {/* Computed from the alert's own start, not the server's "1 days" string. */}
                {describeChangeTiming(d.timestamp, event?.starts_at) || (d.timeBeforeEvent ? `${d.timeBeforeEvent} before` : 'Change')}
              </Typography>
              {d.timestamp && (
                <Typography sx={{ fontSize: 'var(--ds-text-small)', color: 'var(--ds-gray-600)', whiteSpace: 'nowrap' }}>
                  {formatClockCompact(d.timestamp)}
                </Typography>
              )}
            </Box>

            <Box display={'flex'} flexDirection={'row'} alignItems={'center'} gap={ds.space.mul(0, 2)} sx={{ flexShrink: 0 }}>
              {d.eventId && (
                <Button size='xs' tone='link' onClick={() => toggleDiff(d.eventId)}>
                  {expandedId === d.eventId ? 'Hide diff' : 'Show diff'}
                </Button>
              )}
              {isRevertableChange(d) && d.supersededBy === undefined && (
                <Button size='xs' tone='secondary' disabled={!canRevert} onClick={() => setRevertChangeId(d.eventId)}>
                  {'Roll back'}
                </Button>
              )}
              {/* Each label is said only where the data shows it. A change with no recorded field
                  list is offered and left to the server, never labelled with a claim we cannot make. */}
              {d.changedFields.length > 0 && isRolloutOnlyChange(d.changedFields) && (
                <Typography sx={{ fontSize: 'var(--ds-text-small)', color: 'var(--ds-gray-500)', whiteSpace: 'nowrap' }}>{'Restart only'}</Typography>
              )}
              {changedObjectKind(d) && (
                <Typography
                  title='This change is to another object the workload uses, not to the workload itself, so it is not rolled back from here.'
                  sx={{ fontSize: 'var(--ds-text-small)', color: 'var(--ds-gray-500)', whiteSpace: 'nowrap' }}
                >
                  {`${changedObjectKind(d)} change`}
                </Typography>
              )}
              {d.changedFields.length > 0 && isScaleOnlyChange(d.changedFields) && (
                <Typography sx={{ fontSize: 'var(--ds-text-small)', color: 'var(--ds-gray-500)', whiteSpace: 'nowrap' }}>{'Scale only'}</Typography>
              )}
              {isRevertableChange(d) && d.supersededBy !== undefined && (
                <Typography
                  title='A later change edited the same fields, so rolling this one back on its own would undo that change too.'
                  sx={{ fontSize: 'var(--ds-text-small)', color: 'var(--ds-gray-500)', whiteSpace: 'nowrap' }}
                >
                  {d.supersededBy ? `Replaced by the ${formatClockCompact(d.supersededBy)} change` : 'Replaced by a later change'}
                </Typography>
              )}
            </Box>
          </Box>

          {/* The changed fields, when the server recorded them. Nothing is said when it did not:
              "Show diff" already answers the question, and repeating an apology on every row was
              louder than the content. */}
          {d.changedFields.length > 0 && (
            <Box sx={{ marginTop: 'var(--ds-space-2)' }}>
              {d.changedFields.map((f, fieldIndex) => (
                <ChangedField key={`${f.path}_${fieldIndex}`} field={f} />
              ))}
              {d.fieldsOmitted > 0 && (
                <Typography sx={{ fontSize: 'var(--ds-text-small)', color: 'var(--ds-gray-600)' }}>
                  {`and ${d.fieldsOmitted} more field${d.fieldsOmitted === 1 ? '' : 's'} not shown`}
                </Typography>
              )}
              {/* A clipped value must never read as the whole value: acting on a half-rendered
                  container command is acting on something you have not seen. */}
              {d.valueTruncated && (
                <Typography sx={{ fontSize: 'var(--ds-text-small)', color: 'var(--ds-amber-600)' }}>
                  {'Long values are shortened — the diff and the revert dialog show them in full.'}
                </Typography>
              )}
            </Box>
          )}

          {expandedId === d.eventId && (
            <Box sx={{ marginTop: 'var(--ds-space-2)' }}>
              {diffState.error ? (
                <Typography sx={{ fontSize: 'var(--ds-text-small)', color: 'var(--ds-red-600)' }}>{diffState.error}</Typography>
              ) : diffState.loading ? (
                <Typography sx={{ fontSize: 'var(--ds-text-small)', color: 'var(--ds-gray-600)' }}>{'Loading the diff…'}</Typography>
              ) : (
                <DiffViewer originalCode={diffState.old} newCode={diffState.new} language='yaml' leftLabel='Before' rightLabel='After' />
              )}
            </Box>
          )}

          {index !== deployments.length - 1 && <Divider sx={{ marginTop: 'var(--ds-space-3)' }} />}
        </Box>
      ))}

      <Modal
        width='md'
        open={!!revertChangeId}
        handleClose={() => setRevertChangeId('')}
        title={`Roll back a change to ${rolloutName || service || event?.subject_name}`}
        loader={false}
      >
        <InvestigateResolution
          accountId={event?.cloud_account_id ?? ''}
          row={event}
          handleClose={() => setRevertChangeId('')}
          updateInvestigateSuccessSnackBar={handleSnackbar}
          isRevertTheDevelopment={!!revertChangeId}
          cardId={cardId}
          changeEventId={revertChangeId}
          changeChoices={changeChoices}
        />
      </Modal>
    </Box>
  );
};

DeploymentHistoryContent.propTypes = {
  history: PropTypes.object.isRequired,
  event: PropTypes.object,
  cardId: PropTypes.string,
  changeChoices: PropTypes.arrayOf(PropTypes.object),
};

// changedPathsLabel names what an edit touched, so rows of one repeatedly-edited object can be told
// apart without opening each diff.
const changedPathsLabel = (diff) => {
  const paths = (Array.isArray(diff?.updated_values) ? diff.updated_values.map((v) => v?.path) : diff?.updated_paths || []).filter(Boolean);
  if (paths.length === 0) return '';
  const shown = paths.slice(0, 3).join(', ');
  return paths.length > 3 ? `${shown} and ${paths.length - 3} more` : shown;
};

// RepeatedChangesContent lists every edit folded into one card, newest first, each with its own diff.
// A card that showed only the newest diff and the other edits as bare times gave no way to see what
// those edits were. Diffs already on the page are shown directly; the collector keeps only the newest
// one per object, so older edits are fetched from their own change event on demand.
const RepeatedChangesContent = ({ changes, event }) => {
  const [expanded, setExpanded] = useState(0);
  // Keyed by change event id, so a slow response can only ever fill in its own row.
  const [fetched, setFetched] = useState({});

  const toggle = (index) => {
    if (expanded === index) {
      setExpanded(-1);
      return;
    }
    setExpanded(index);
    const change = changes[index];
    if (change.diff || !change.eventId || fetched[change.eventId]) return;
    setFetched((prev) => ({ ...prev, [change.eventId]: { loading: true } }));
    apiKubernetes
      .resolveEventRecord(change.eventId, event?.cloud_account_id)
      .then((res) => {
        const diff = (res?.data?.events?.[0]?.evidences || []).find((e) => e?.type === 'diff')?.data;
        setFetched((prev) => ({
          ...prev,
          [change.eventId]: diff?.old || diff?.new ? { diff } : { error: 'No before/after manifest was recorded for this change.' },
        }));
      })
      .catch(() => setFetched((prev) => ({ ...prev, [change.eventId]: { error: 'That change could not be loaded.' } })));
  };

  return (
    <Box sx={{ marginTop: 'var(--ds-space-2)' }}>
      {changes.map((change, index) => {
        const state = change.eventId ? fetched[change.eventId] : null;
        const diff = change.diff || state?.diff;
        const paths = changedPathsLabel(diff);
        return (
          <Box key={change.eventId || `${change.startAt}_${index}`} sx={{ paddingBlock: 'var(--ds-space-2)' }}>
            <Box display={'flex'} flexDirection={'row'} justifyContent={'space-between'} alignItems={'center'} gap={ds.space.mul(0, 3)}>
              <Box display={'flex'} flexDirection={'row'} alignItems={'baseline'} gap={ds.space.mul(0, 2)} sx={{ minWidth: 0, flexWrap: 'wrap' }}>
                <Typography sx={{ color: 'var(--ds-gray-900)', fontSize: 'var(--ds-text-body)', fontWeight: 'var(--ds-font-weight-medium)' }}>
                  {index === 0 ? 'Latest' : `Edit ${changes.length - index}`}
                </Typography>
                {change.startAt && <Datetime value={change.startAt} sx={{ fontSize: 'var(--ds-text-small)', color: 'var(--ds-gray-600)' }} />}
                {paths && (
                  <Typography
                    sx={{ fontSize: 'var(--ds-text-small)', fontFamily: 'var(--ds-font-mono)', color: 'var(--ds-gray-700)', wordBreak: 'break-all' }}
                  >
                    {paths}
                  </Typography>
                )}
              </Box>
              {(change.diff || change.eventId) && (
                <Button size='xs' tone='link' onClick={() => toggle(index)}>
                  {expanded === index ? 'Hide diff' : 'Show diff'}
                </Button>
              )}
            </Box>
            {expanded === index && (
              <Box sx={{ marginTop: 'var(--ds-space-2)' }}>
                {state?.error ? (
                  <Typography sx={{ fontSize: 'var(--ds-text-small)', color: 'var(--ds-red-600)' }}>{state.error}</Typography>
                ) : diff ? (
                  <DiffViewer originalCode={diff.old} newCode={diff.new} language='yaml' leftLabel='Before' rightLabel='After' />
                ) : (
                  <Typography sx={{ fontSize: 'var(--ds-text-small)', color: 'var(--ds-gray-600)' }}>{'Loading the diff…'}</Typography>
                )}
              </Box>
            )}
            {index !== changes.length - 1 && <Divider sx={{ marginTop: 'var(--ds-space-2)' }} />}
          </Box>
        );
      })}
    </Box>
  );
};

RepeatedChangesContent.propTypes = {
  changes: PropTypes.arrayOf(PropTypes.object).isRequired,
  event: PropTypes.object,
};

class LastDeploymentCard {
  // foldedChanges: every edit foldRepeatedChanges folded into this block ({start_at, diff}), newest first.
  constructor(evidenceData, event, index, foldedChanges) {
    this.id = `LastDeploymentCard_${index}`;
    this.foldedChanges = foldedChanges;
    this.icon = LastDeploymentIcon;
    this.text = evidenceData.additional_info?.title || 'Last Deployment Change';
    this.resolveButton = true;
    this.renderContent = false;
    this.highlightsData = [];
    this.diff = [];
    this.event = event;
    this.evidenceData = evidenceData;
    this.deploymentHistory = {};
  }

  canRenderContent = async () => {
    if (this.evidenceData?.additional_info?.action_name == 'deployment_history') {
      const parsedData = safeJSONParse(this.evidenceData.data);
      // Every deployment the server returned is rendered, including one whose changed
      // fields were not recorded. Dropping those used to hide the card entirely: the
      // server stopped sending per-deployment `evidences` in #27136 to stop an OOM,
      // and this branch required them, so the multi-change card rendered for nobody.
      // A deployment we cannot describe still happened, and saying so beats silence.
      const deployments = Array.isArray(parsedData?.deployments) ? parsedData.deployments : [];
      if (deployments.length > 0) {
        // An event can carry BOTH a single `diff` evidence and a `deployment_history` evidence,
        // and investigate.jsx builds one card per evidence — so both render, and both were titled
        // "Last Deployment Change". Two identically-titled cards on one page, one showing a single
        // change and the other several, reads as a duplicate rather than as two different things.
        this.text = this.evidenceData.additional_info?.title || 'Deployment Changes';
        this.deploymentHistory = {
          namespace: parsedData.namespace,
          rolloutName: parsedData.rollout_name,
          service: parsedData.service_name,
          timeRangeHours: parsedData.time_range_hours,
          deployments: deployments.map((d) => ({
            eventId: d.event_id,
            title: d.title,
            timestamp: d.timestamp,
            timeBeforeEvent: d.time_before_event,
            deploymentStrategy: d.deployment_strategy,
            changedFields: Array.isArray(d.updated_values) ? d.updated_values : [],
            fieldsOmitted: d.fields_omitted || 0,
            valueTruncated: !!d.value_truncated,
          })),
        };
        this.deploymentHistory.deployments = markSupersededChanges(this.deploymentHistory.deployments);
        // Every change that can be rolled back on its own is a choice in the rollback dialog, newest
        // first. Only the newest used to be offered, so a workload with an image bump and a separate
        // env change could act on one of them. A change a later one overwrote is not offered — the
        // server refuses it, and a choice that cannot be chosen is noise; the Evidence tab still lists
        // it as replaced. A restart, a scale or another object's change is not a deploy to put back.
        this.setRollbackChoices(
          this.deploymentHistory.deployments
            .filter((d) => isRevertableChange(d) && d.supersededBy === undefined)
            .map((d) => this.buildChoice(d.eventId, d.timestamp, d.changedFields))
        );
        this.renderContent = true;
      }
      this.highlightsData = this.evidenceData.insight;
    } else {
      const diff = this.evidenceData.type === 'diff';
      if (diff) {
        // The history card lists every change in the window, including this one, and now shows its
        // diff in place — so rendering both produced two cards describing the same change, and an
        // action bar with two revert rows that could not be told apart. Stand down when a history
        // card will actually render; if it has nothing to show, this card is still the only view.
        if (hasRenderableHistory(this.event)) {
          this.resolveButton = false;
          return false;
        }
        this.renderContent = true;
        this.diff = this.evidenceData;
        this.highlightsData = this.evidenceData?.insight;
        // The collector folds repeat edits and says so in its title; for events recorded before
        // that, the page folds them and the title has to say it here. Built from the stored title
        // rather than appended to this.text: this runs more than once per card.
        const info = this.evidenceData.additional_info || {};
        this.changes = Array.isArray(info.changes)
          ? info.changes.map((c) => ({ startAt: c?.start_at, eventId: c?.change_event_id }))
          : (this.foldedChanges || []).map((c) => ({ startAt: c?.start_at, diff: c?.diff }));
        if (this.changes.length > 0) this.changes[0] = { ...this.changes[0], diff: this.evidenceData.data };
        const baseTitle = info.title || 'Last Deployment Change';
        this.text = !info.change_count && this.changes.length > 1 ? `${baseTitle} · ${this.changes.length} times` : baseTitle;
        this.revertChangeId = info.change_event_id || '';
        // Decided here rather than when the dialog opens: the action row renders from resolveButton,
        // so a restart- or scale-only change used to get a "Review & run" that opened nothing.
        const fields = this.evidenceData.data?.updated_values;
        this.resolveButton =
          canRevertDiff(this.evidenceData, this.event) &&
          !isRolloutOnlyChange(fields) &&
          !isScaleOnlyChange(fields) &&
          !!(this.evidenceData.data?.old && this.evidenceData.data?.new);
        this.setRollbackChoices(this.resolveButton ? [this.buildChoice(this.revertChangeId, this.evidenceData.start_at, fields)] : []);
      }
    }
    return this.renderContent;
  };

  getHighLightsData = () => {
    return this.highlightsData;
  };

  getContentComponents = () => {
    return [() => this.renderDiffData()];
  };

  renderDiffData = () => {
    if (Object.keys(this.diff).length > 0) {
      return (
        <Box>
          <Box display={'flex'} flexDirection={'row'} justifyContent={'space-between'} alignItems={'center'} marginTop={ds.space.mul(0, 10)}>
            <Typography
              display={'inline'}
              sx={{
                color: 'var(--ds-brand-500)',
                fontSize: 'var(--ds-text-body-lg) !important',
                fontWeight: 'var(--ds-font-weight-medium)',
              }}
            >
              {' Here’s What Changed'}
            </Typography>
            {this.diff.start_at && (
              <Box
                display={'flex'}
                flexDirection={'row'}
                alignItems={'center'}
                justifyContent={'center'}
                gap={ds.space.mul(0, 3)}
                sx={{
                  border: '1px solid var(--grey-40, var(--ds-brand-200))',
                  padding: 'var(--ds-space-1) var(--ds-space-1)',
                  borderRadius: 'var(--ds-radius-sm)',
                }}
              >
                <Typography
                  sx={{
                    color: 'var(--ds-brand-500)',
                    fontSize: 'var(--ds-text-body-lg) !important',
                    fontWeight: 'var(--ds-font-weight-medium)',
                  }}
                  display={'inline'}
                >
                  {'Deployed '}
                </Typography>
                <Datetime
                  value={this.diff.start_at}
                  sx={{
                    fontSize: 'var(--ds-text-body-lg) !important',
                    fontWeight: 'var(--ds-font-weight-medium)',
                    lineHeight: '16px',
                  }}
                />
              </Box>
            )}
          </Box>
          {this.changes?.length > 1 ? (
            <>
              <Typography sx={{ fontSize: 'var(--ds-text-small)', color: 'var(--ds-gray-600)', marginTop: 'var(--ds-space-2)' }}>
                {`Changed ${this.changes.length} times before this alert.`}
              </Typography>
              <RepeatedChangesContent changes={this.changes} event={this.event} />
            </>
          ) : (
            <Box sx={{ marginTop: 'var(--ds-space-3)', marginBottom: 'var(--ds-space-2)' }}>
              <DiffViewer originalCode={this.diff?.data?.old} newCode={this.diff?.data?.new} language='yaml' leftLabel='Before' rightLabel='After' />
            </Box>
          )}
        </Box>
      );
    } else if (this.deploymentHistory?.deployments?.length > 0) {
      return <DeploymentHistoryContent history={this.deploymentHistory} event={this.event} cardId={this.id} changeChoices={this.rollbackChoices} />;
    }
    return (
      <Typography marginTop={ds.space.mul(0, 5)} fontSize={ds.text.bodyLg} fontWeight={ds.weight.medium}>
        No diff available.
      </Typography>
    );
  };

  // One change the rollback dialog can act on.
  buildChoice = (changeEventId, at, fields) => {
    const summary = summarizeChangedFields(fields);
    return {
      changeEventId,
      at,
      when: formatClockCompact(at),
      timing: describeChangeTiming(at, this.event?.starts_at),
      summary,
      writes: describeRollbackWrites(fields),
    };
  };

  // The action row names its change when there is exactly one; with several, it says how many and
  // the dialog is where the operator picks — a picker in the row crowded it.
  setRollbackChoices = (choices) => {
    this.rollbackChoices = choices;
    const newest = choices[0];
    this.resolveButton = choices.length > 0;
    this.choosesTarget = choices.length > 1;
    this.actionSubject = this.choosesTarget ? '' : newest?.when || '';
    this.actionDetail = !newest
      ? ''
      : this.choosesTarget
      ? [`${choices.length} recent changes can be rolled back`, newest.timing ? `newest ${newest.timing}` : ''].filter(Boolean).join(' · ')
      : [newest.timing, newest.writes ? `rolls back ${newest.writes}` : newest.summary ? `changes ${newest.summary}` : '']
          .filter(Boolean)
          .join(' · ');
  };

  // A stable component: the page asks for it on every render, and a fresh one each time would remount
  // the dialog and drop the change it had loaded.
  RevertTheDeployment = (props) => this.renderRevertModal(props, this.rollbackChoices?.[0]?.changeEventId);

  renderRevertModal = (props, changeEventId) => {
    const handleSnackbar = (type, message) => {
      if (['success', 'error'].includes(type)) {
        snackbar[type](message);
      }
    };

    return (
      <Modal
        width='md'
        open={props.open}
        handleClose={props.onCloseComponent}
        title={`Roll back a change to ${
          revertTargetName(this.diff?.data?.resource_name) || this.deploymentHistory?.rolloutName || this.event?.subject_name
        }`}
        loader={false}
      >
        <InvestigateResolution
          accountId={this.event?.cloud_account_id ?? ''}
          row={this.event}
          handleClose={props.onCloseComponent}
          updateInvestigateSuccessSnackBar={handleSnackbar}
          isRevertTheDevelopment={props.open}
          cardId={this.id}
          changeEventId={changeEventId || ''}
          changeChoices={this.rollbackChoices}
        />
      </Modal>
    );
  };

  getResolveComponent = () => (this.rollbackChoices?.length > 0 ? this.RevertTheDeployment : null);
}

export default LastDeploymentCard;
