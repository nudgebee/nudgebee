import React, { useEffect, useState } from 'react';
import { Typography, Box, Radio, RadioGroup, FormControlLabel } from '@mui/material';
import { Input } from '@ui/Input';
import apiRecommendations from '@api1/recommendation';
import apiKubernetes from '@api1/kubernetes';
import * as yaml from 'js-yaml';
import PropTypes from 'prop-types';
import { Button } from '@ui/Button';
import { toast as snackbar } from '@ui/Toast';
import { parseHttpResponseBodyMessage, safeJSONParse } from 'src/utils/common';
import { ds } from '@utils/colors';
import { Callout } from '@ui/Callout';
import { formatClockCompact } from '@shared/format/Datetime';
import { detectGitOpsManager } from '@utils/workloadManager';
import {
  describeChangeTiming,
  isRolloutOnlyKey,
  nameListPositions,
  resolutionRefusalText,
  revertTargetLabel,
} from '@components/k8s/investigate/changeRollback';
import { useRollbackCheck } from '@components/k8s/investigate/useRollbackCheck';

// What a rollback's rolling restart looks like for each kind — a DaemonSet restarts a pod on every
// node, which "rolling restart" alone does not tell anyone approving it.
const RESTART_IMPACT_BY_KIND = {
  deployment: 'Replaces the pods one by one (rolling update).',
  rollout: 'Replaces the pods through the Rollout’s own strategy.',
  statefulset: 'Replaces the pods one at a time, highest ordinal first.',
  daemonset: 'Replaces the pod on every node it runs on.',
};

// Reads who deploys this workload from the manifest recorded with the change. The snapshot is
// serialised snake_case, so owner references may arrive under either spelling.
const workloadManagerFromManifest = (manifest) => {
  if (typeof manifest !== 'string' || !manifest) return null;
  try {
    const metadata = yaml.load(manifest)?.metadata || {};
    return detectGitOpsManager(metadata.annotations || {}, metadata.labels || {}, metadata.ownerReferences || metadata.owner_references || []);
  } catch {
    return null;
  }
};

// Whether a rollback done here will last, in the operator's terms, or null when nothing redeploys
// the workload behind our back. A repo annotation alone (manager 'GitOps') names where the source
// lives, not that anything syncs from it, so it is not warned about.
const describeRollbackDurability = (managed, namespace) => {
  if (!managed?.manager) return null;
  if (managed.operator) {
    return {
      tone: 'critical',
      blocks: true,
      message: `${managed.operator.kind} ${managed.operator.name} owns this workload and rewrites it from its own spec, so a rollback here is undone within seconds. Change the ${managed.operator.kind} resource instead.`,
    };
  }
  if (managed.manager === 'Argo CD' || managed.manager === 'Flux') {
    return {
      tone: 'warning',
      message: `Deployed by ${managed.manager}. If auto-sync or self-heal is on, the next sync puts this change straight back — roll back in Git or in ${managed.manager} for it to stick.`,
    };
  }
  if (managed.manager === 'Helm') {
    const release = managed.helmRelease;
    return {
      tone: 'warning',
      message: release
        ? `Deployed by Helm (release ${release}). The next helm upgrade re-applies this change, and Helm's history will not show this rollback. To roll back through Helm instead — which rolls back everything in release ${release}, not only this workload:`
        : "Deployed by Helm. The next helm upgrade re-applies this change, and Helm's history will not show this rollback.",
      command: release ? `helm rollback ${release} -n ${namespace || '<namespace>'}` : '',
    };
  }
  return null;
};

// A value as it reads in the manifest: strings bare (no JSON quotes), structures as JSON.
const formatFieldValue = (value) => {
  if (value === null || value === undefined || value === '') return '(not set)';
  return typeof value === 'string' ? value : JSON.stringify(value);
};

const InvestigateResolution = ({
  row,
  handleClose,
  updateInvestigateSuccessSnackBar,
  isRevertTheDevelopment = false,
  cardId = '',
  changeEventId = '',
  changeChoices = [],
}) => {
  const [requestBody, setRequestBody] = useState({});
  const [selectedOption, setSelectedOption] = useState('');
  const [loading, setLoading] = useState(false);
  // Kept in state (not just a toast) so the reason stays on screen next to the diff the user was
  // reading. A toast disappears; the question "why did that fail?" does not.
  const [submitError, setSubmitError] = useState('');
  const [validationError, setValidationError] = useState({
    imageChangeContainerName: '',
    imageNameWithTag: '',
  });
  // When the operator picked a specific change to undo, the table below must describe THAT
  // change. It is loaded from the change's own event rather than from the summary the history
  // card renders: that summary truncates long values, and approving a revert from a shortened
  // container command means approving something you have not seen.
  const [selectedChange, setSelectedChange] = useState(null);
  const [selectedChangeError, setSelectedChangeError] = useState('');
  // The change being rolled back. Starts at the one the caller named and moves when the operator
  // picks another from the list — the row outside only says how many there are.
  const [activeChangeId, setActiveChangeId] = useState(changeEventId);
  useEffect(() => setActiveChangeId(changeEventId), [changeEventId]);
  const revertsSelectedChange = !!activeChangeId && activeChangeId !== row?.id;

  useEffect(() => {
    if (!revertsSelectedChange) {
      setSelectedChange(null);
      setSelectedChangeError('');
      return undefined;
    }
    let cancelled = false;
    setSelectedChange(null);
    setSelectedChangeError('');
    apiKubernetes
      .resolveEventRecord(activeChangeId, row?.cloud_account_id)
      .then((res) => {
        if (cancelled) return;
        const event = res?.data?.events?.[0];
        if (!event) {
          setSelectedChangeError('That change could not be loaded, so there is nothing to confirm against.');
          return;
        }
        setSelectedChange(event);
      })
      .catch(() => {
        if (!cancelled) setSelectedChangeError('That change could not be loaded, so there is nothing to confirm against.');
      });
    return () => {
      cancelled = true;
    };
  }, [revertsSelectedChange, activeChangeId, row?.cloud_account_id]);

  // The fields below are what the change recorded. Only the server knows whether the workload was
  // changed again since, and it used to say so only as a refusal after the click.
  const rollbackCheck = useRollbackCheck({
    accountId: row?.cloud_account_id,
    eventId: row?.id,
    changeEventId: revertsSelectedChange ? activeChangeId : '',
    enabled: isRevertTheDevelopment,
  });
  const rollbackBlocked = isRevertTheDevelopment && rollbackCheck.status === 'blocked';
  const rollbackChecking = isRevertTheDevelopment && rollbackCheck.status === 'checking';

  // Who deploys the workload decides whether a rollback here lasts. Read from the manifest recorded
  // with the change being rolled back — the state that change left, i.e. the one live now.
  const revertDiff = (() => {
    if (!isRevertTheDevelopment) return null;
    const source = revertsSelectedChange ? selectedChange : row;
    const evidences = Array.isArray(source?.evidences) ? source.evidences : [];
    return evidences.find((f) => f?.type == 'diff')?.data || null;
  })();
  const durability = revertDiff
    ? describeRollbackDurability(
        workloadManagerFromManifest(revertDiff.new),
        String(revertDiff.resource_name || '').split('/')[1] || row?.subject_namespace
      )
    : null;

  // Several recorded changes: the operator picks which one to roll back, here where its fields are
  // shown, rather than in the action row.
  const changePicker =
    isRevertTheDevelopment && changeChoices.length > 1 ? (
      <Box>
        <Typography sx={{ fontSize: 'var(--ds-text-small)', color: 'var(--ds-gray-600)', mb: ds.space[1] }}>{'Which change to roll back'}</Typography>
        <RadioGroup value={activeChangeId} onChange={(e) => setActiveChangeId(e.target.value)}>
          <Box sx={{ border: '1px solid var(--ds-gray-200)', borderRadius: 'var(--ds-radius-sm)' }}>
            {changeChoices.map((choice, index) => (
              <FormControlLabel
                key={choice.changeEventId}
                value={choice.changeEventId}
                disabled={loading}
                control={<Radio size='small' />}
                sx={{
                  m: 0,
                  px: ds.space[2],
                  py: ds.space[1],
                  width: '100%',
                  boxSizing: 'border-box',
                  borderTop: index === 0 ? 'none' : '1px solid var(--ds-gray-200)',
                }}
                label={
                  <Box>
                    <Typography sx={{ fontSize: 'var(--ds-text-body)', fontWeight: 'var(--ds-font-weight-medium)' }}>
                      {[choice.when, choice.timing].filter(Boolean).join(' · ')}
                    </Typography>
                    <Typography sx={{ fontSize: 'var(--ds-text-small)', color: 'var(--ds-gray-600)' }}>
                      {choice.summary ? `changes ${choice.summary}` : ''}
                    </Typography>
                  </Box>
                }
              />
            ))}
          </Box>
        </RadioGroup>
      </Box>
    ) : null;

  const handleRadioChange = (e) => {
    const value = e.target.value;
    setSelectedOption(value);
    if (value === 'size') {
      setRequestBody({ size: requestBody.size, increase_replicas: '', restart: false, revert: false });
    } else if (value === 'increase_replicas') {
      setRequestBody({ size: '', increase_replicas: requestBody.increase_replicas, restart: false, revert: false });
    } else if (value === 'restart') {
      setRequestBody({ size: '', increase_replicas: '', restart: true, revert: false });
    } else if (value === 'cordon') {
      setRequestBody({ cordon: true, drain: false });
    } else if (value === 'drain') {
      setRequestBody({ cordon: false, drain: true });
    }
  };

  const aggregationKey = row?.aggregation_key;

  const getNestedValue = (obj, path) => {
    return path.split(/(?<!\[[^\]]*)\./).reduce((acc, part) => {
      if (!acc) {
        return '';
      }
      let arrayMatch = part.match(/^(.+?)\[(\d+)\]$/);
      if (arrayMatch) {
        const [, key, index] = arrayMatch;
        return acc[key] ? acc[key][Number(index)] : '';
      }
      let bracketMatch = part.match(/^(.+?)\['(.+)'\]$/);
      if (bracketMatch) {
        const [, key, innerKey] = bracketMatch;
        return acc[key] ? acc[key][innerKey] : '';
      }
      return acc[part];
    }, obj);
  };

  const renderConditionalFields = () => {
    let existingVolume = '';
    let revertFields = [];
    const json = row?.evidences.find((f) => f.type == 'json')?.data ?? '';
    if (json) {
      const jsonParsed = typeof json === 'string' ? safeJSONParse(json) : json;
      existingVolume = jsonParsed?.spec?.resources?.requests?.storage ?? '';
    }
    // The selected change's own evidence wins when there is one; otherwise this is the single
    // change the alert carries, which is what the older card sends.
    const diffSource = revertsSelectedChange ? selectedChange : row;
    // `|| []` only guards null/undefined. Evidences arriving as an object — a shape this code does
    // not control, since it is whatever the record parsed to — is truthy, and .find on it throws,
    // taking the whole revert dialog down.
    const evidences = Array.isArray(diffSource?.evidences) ? diffSource.evidences : [];
    const diff = evidences.find((f) => f?.type == 'diff')?.data ?? '';
    if (diff) {
      const stripKind = (path) => path.replace(/^(StatefulSet|DaemonSet|Deployment|ReplicaSet|Rollout)\./, '');
      // Prefer updated_values: it records old/new per path directly. Re-deriving
      // them by walking diff.old/diff.new renders blanks whenever the field's
      // YAML key differs from the path, because the snapshot is serialised
      // snake_case while the paths are camelCase — e.g. the path
      // spec.template.spec.initContainers[0].image has no match in a snapshot
      // that spells it init_containers.
      const updatedValues = Array.isArray(diff.updated_values) ? diff.updated_values : [];
      if (updatedValues.length) {
        revertFields = updatedValues.map((change) => ({ path: stripKind(change?.path ?? ''), old: change?.old, new: change?.new }));
      } else {
        const oldData = yaml.load(diff.old);
        const newData = yaml.load(diff.new);
        revertFields = (diff.updated_paths ?? []).map((path) => {
          const updatedPath = stripKind(path);
          return { path: updatedPath, old: getNestedValue(oldData, updatedPath), new: getNestedValue(newData, updatedPath) };
        });
      }
    }

    if (isRevertTheDevelopment) {
      // Nothing is confirmable until the selected change has loaded, and a revert dispatched
      // from an empty table would undo fields the operator never saw.
      if (revertsSelectedChange && !selectedChange) {
        return (
          <Box sx={{ display: 'flex', flexDirection: 'column', gap: ds.space[3] }}>
            {changePicker}
            <Typography>{selectedChangeError || 'Loading the change you selected…'}</Typography>
          </Box>
        );
      }
      const changedAt = revertsSelectedChange ? selectedChange?.starts_at : evidences.find((f) => f?.type == 'diff')?.start_at;
      const timing = describeChangeTiming(changedAt, row?.starts_at);
      const kind = String(diff?.resource_name || '')
        .split('/')[0]
        .toLowerCase();
      const touchesPods = revertFields.some((f) => f.path.startsWith('spec.template'));
      // Positions become names ("containers[sidecar]") so a workload with several containers says
      // which one the rollback touches. The manifests are best-effort: unparseable ones are skipped.
      const manifests = [diff?.new, diff?.old].map((m) => {
        try {
          return typeof m === 'string' ? yaml.load(m) : null;
        } catch {
          return null;
        }
      });
      const monoSx = { fontSize: 'var(--ds-text-small)', fontFamily: 'var(--ds-font-mono)', wordBreak: 'break-all' };
      return (
        <Box sx={{ display: 'flex', flexDirection: 'column', gap: ds.space[3] }}>
          {changePicker}
          <Box>
            {diff?.resource_name && (
              <Typography sx={{ fontSize: 'var(--ds-text-body)', fontWeight: 'var(--ds-font-weight-medium)' }}>
                {revertTargetLabel(diff.resource_name) || diff.resource_name}
              </Typography>
            )}
            {changedAt && (
              <Typography sx={{ fontSize: 'var(--ds-text-small)', color: 'var(--ds-gray-600)' }}>
                {`Undoes the change deployed ${formatClockCompact(changedAt)}${timing ? ` · ${timing}` : ''}`}
              </Typography>
            )}
          </Box>
          {durability && (
            <Callout
              tone={durability.tone}
              message={
                <>
                  {durability.message}
                  {durability.command && (
                    <Box component='code' sx={{ display: 'block', mt: ds.space[1], fontFamily: 'var(--ds-font-mono)', userSelect: 'all' }}>
                      {durability.command}
                    </Box>
                  )}
                </>
              }
            />
          )}
          {rollbackBlocked && <Callout tone='critical' message={rollbackCheck.reason} data-testid='rollback-blocked-reason' />}
          {/* Current value first, then what the rollback writes: "Old / New" named the change's own
              sides and left the operator to work out which one they were about to apply. The first
              value is what the change set; it is called "Now" only once the server has confirmed
              nothing changed it since — and never for a restart marker, which the server does not
              check: the next deploy or restart writes another. */}
          <Box sx={{ border: '1px solid var(--ds-gray-200)', borderRadius: 'var(--ds-radius-sm)' }}>
            {revertFields.map((f, index) => (
              <Box
                key={`${f.path}_${index}`}
                sx={{
                  p: ds.space[3],
                  borderTop: index === 0 ? 'none' : '1px solid var(--ds-gray-200)',
                  display: 'flex',
                  flexDirection: 'column',
                  gap: ds.space[1],
                }}
              >
                <Typography sx={{ ...monoSx, color: 'var(--ds-gray-700)' }} title={f.path}>
                  {nameListPositions(f.path, manifests, f.new ?? f.old)}
                  {isRolloutOnlyKey(f.path) && (
                    <Typography component='span' sx={{ fontSize: 'var(--ds-text-small)', color: 'var(--ds-gray-500)', fontFamily: 'inherit' }}>
                      {'  (restart marker)'}
                    </Typography>
                  )}
                </Typography>
                <Box sx={{ display: 'grid', gridTemplateColumns: 'max-content 1fr', columnGap: ds.space[3], rowGap: ds.space[1] }}>
                  <Typography sx={{ fontSize: 'var(--ds-text-small)', color: 'var(--ds-gray-600)' }}>
                    {rollbackCheck.status === 'allowed' && !isRolloutOnlyKey(f.path) ? 'Now' : 'At change time'}
                  </Typography>
                  <Typography sx={{ ...monoSx, color: 'var(--ds-red-600)' }}>{formatFieldValue(f.new)}</Typography>
                  <Typography sx={{ fontSize: 'var(--ds-text-small)', color: 'var(--ds-gray-600)' }}>{'After rollback'}</Typography>
                  <Typography sx={{ ...monoSx, color: 'var(--ds-green-600)' }}>{formatFieldValue(f.old)}</Typography>
                </Box>
              </Box>
            ))}
          </Box>
          {touchesPods && RESTART_IMPACT_BY_KIND[kind] && (
            <Typography sx={{ fontSize: 'var(--ds-text-small)', color: 'var(--ds-gray-600)' }}>{RESTART_IMPACT_BY_KIND[kind]}</Typography>
          )}
        </Box>
      );
    } else if (aggregationKey === 'KubePersistentVolumeFillingUp' || aggregationKey == 'KubernetesVolumeOutOfDiskSpace') {
      return (
        <div style={{ display: 'flex', flexDirection: 'column', gap: 'var(--ds-space-4)' }}>
          {existingVolume && <Typography>Current storage size: {existingVolume}</Typography>}
          <Box display={'grid'} gridTemplateColumns={'1fr 1fr'} gap={ds.space[3]}>
            <Input
              value={requestBody.size || ''}
              label='Increase Volume To'
              type='number'
              onChange={(value) => setRequestBody({ size: value })}
              suffix='Gi'
              size='sm'
              required
            />
          </Box>
        </div>
      );
    } else if (aggregationKey === 'CPUThrottlingHigh' || aggregationKey === 'PodMemoryReachingLimit') {
      return (
        <div style={{ display: 'flex', flexDirection: 'column', gap: 'var(--ds-space-4)', my: 'var(--ds-space-4)' }}>
          <RadioGroup value={selectedOption} onChange={handleRadioChange}>
            <Box display={'flex'} flexDirection={'column'} gap={ds.space[3]}>
              <FormControlLabel
                value='size'
                control={<Radio />}
                label={
                  <Input
                    value={requestBody.size || ''}
                    label={aggregationKey === 'CPUThrottlingHigh' ? 'Increase CPU To' : 'Increase Memory To'}
                    type='number'
                    onChange={(value) => setRequestBody({ size: value, increase_replicas: '', restart: false, revert: false })}
                    size='sm'
                  />
                }
              />
              <FormControlLabel
                value='increase_replicas'
                control={<Radio />}
                label={
                  <Input
                    value={requestBody.increase_replicas || ''}
                    label='Increase Replicas'
                    type='number'
                    onChange={(value) => setRequestBody({ size: '', increase_replicas: value, restart: false, revert: false })}
                    size='sm'
                  />
                }
              />
              <FormControlLabel value='restart' control={<Radio />} label='Restart' />
            </Box>
          </RadioGroup>
        </div>
      );
    } else if (aggregationKey === 'image_pull_backoff_reporter') {
      return (
        <div style={{ display: 'flex', flexDirection: 'column', gap: 'var(--ds-space-4)' }}>
          <Typography>Mention Container Name and Image Tag to change</Typography>
          <br />
          <Box display={'grid'} gridTemplateColumns={'1fr 1fr'} gap={ds.space[3]}>
            <Input
              value={requestBody.imageChangeContainerName || ''}
              label='Container Name'
              type='text'
              onChange={(value) => {
                setRequestBody((prev) => ({
                  ...prev,
                  imageChangeContainerName: value,
                }));
                setValidationError((prev) => ({ ...prev, imageChangeContainerName: '' }));
              }}
              size='sm'
              required
              error={validationError.imageChangeContainerName || undefined}
            />
            <Input
              value={requestBody.imageNameWithTag || ''}
              label='Image (with Tag)'
              type='text'
              onChange={(value) => {
                setRequestBody((prev) => ({
                  ...prev,
                  imageNameWithTag: value,
                }));
                setValidationError((prev) => ({ ...prev, imageNameWithTag: '' }));
              }}
              size='sm'
              required
              error={validationError.imageNameWithTag || undefined}
            />
          </Box>
        </div>
      );
    } else if (aggregationKey === 'KubeNodeNotReady') {
      return (
        <div style={{ display: 'flex', flexDirection: 'column', gap: 'var(--ds-space-4)' }}>
          <RadioGroup value={requestBody.cordon ? 'cordon' : requestBody.drain ? 'drain' : ''} onChange={handleRadioChange}>
            <FormControlLabel value='cordon' control={<Radio />} label='Cordon (Mark node as unschedulable.)' />
            <FormControlLabel
              value='drain'
              control={<Radio />}
              label='Node will be marked unschedulable to prevent new pods from arriving. drain evicts the pods if the API server supports eviction. Otherwise, it will use normal DELETE to delete the pods.'
            />
          </RadioGroup>
        </div>
      );
    }
    return null;
  };

  const validateValuesBeforeSubmit = () => {
    let valid = true;
    if (aggregationKey === 'KubePersistentVolumeFillingUp' || aggregationKey == 'KubernetesVolumeOutOfDiskSpace') {
      if (!requestBody.size) {
        snackbar.error('Required fields cannot be empty');
        valid = false;
      }
    } else if (aggregationKey === 'CPUThrottlingHigh' || aggregationKey === 'PodMemoryReachingLimit') {
      if (!requestBody.size && !requestBody.increase_replicas && !requestBody.restart) {
        snackbar.error('Select atleast one type of resolution');
        valid = false;
      }
    } else if (aggregationKey === 'KubeNodeNotReady') {
      if (!requestBody.cordon && !requestBody.drain) {
        snackbar.error('Select atleast one type of resolution');
        valid = false;
      }
    } else if (aggregationKey === 'image_pull_backoff_reporter') {
      const errors = { imageChangeContainerName: '', imageNameWithTag: '' };

      if (!requestBody.imageChangeContainerName) {
        errors.imageChangeContainerName = 'Container Name is required';
        valid = false;
      }

      if (!requestBody.imageNameWithTag) {
        errors.imageNameWithTag = 'Image is required';
        valid = false;
      }

      setValidationError(errors);
    }
    return valid;
  };

  // The dialog stays open on failure. Closing it discarded the diff the user was reading and left
  // only a toast, so retrying meant navigating back to the event and reopening the dialog.
  const showFailure = (reason) => {
    const message = reason || 'The request was rejected and no reason was returned.';
    setSubmitError(resolutionRefusalText(message));
    updateInvestigateSuccessSnackBar('error', `Failed to apply resolution: ${message}`);
  };

  const handleSubmit = () => {
    // Belt and braces with the disabled Submit: a revert must never be dispatched for a change
    // whose contents were never rendered.
    if (revertsSelectedChange && !selectedChange) {
      return;
    }
    if (durability?.blocks || rollbackBlocked) {
      return;
    }
    if (!validateValuesBeforeSubmit()) {
      return;
    }
    setSubmitError('');
    setLoading(true);
    apiRecommendations
      .applyRecommendation(
        row?.cloud_account_id,
        row?.id,
        isRevertTheDevelopment
          ? {
              revert: true,
              ...(cardId && { card_id: cardId }),
              ...(revertsSelectedChange && { change_event_id: activeChangeId }),
            }
          : { ...requestBody, ...(cardId && { card_id: cardId }) },
        'kubernetes',
        {},
        'event'
      )
      .then((res) => {
        if (!res?.errors) {
          updateInvestigateSuccessSnackBar('success', 'Resolution applied successfully');
          handleClose();
          return;
        }
        showFailure(parseHttpResponseBodyMessage(res));
      })
      .catch((err) => {
        // Transport and 5xx failures used to surface as a fixed string, so the actual reason never
        // reached anyone — an apiserver rejection read the same as a network blip.
        showFailure(parseHttpResponseBodyMessage(err?.response?.data) || err?.message);
      })
      .finally(() => {
        setLoading(false);
      });
  };

  return (
    <>
      <Box p={`${ds.space.mul(0, 10)} 0px`}>{renderConditionalFields()}</Box>
      {submitError && (
        <Box
          sx={{
            p: ds.space[3],
            borderRadius: ds.radius.sm,
            border: `1px solid ${ds.red[200]}`,
            background: ds.red[100],
          }}
        >
          <Typography sx={{ fontSize: ds.text.small, color: ds.red[700] }}>{submitError}</Typography>
        </Box>
      )}
      <Box
        display='flex'
        alignItems='center'
        justifyContent='flex-end'
        gap={ds.space[3]}
        p={`${ds.space[4]} 0px`}
        sx={{
          borderTop: '0.5px solid var(--ds-gray-200)',
          button: {
            minWidth: ds.space.mul(0, 70),
          },
        }}
      >
        <Button size='md' onClick={handleClose} tone='secondary'>
          Cancel
        </Button>
        {/* Dispatching before the selected change has loaded approves a diff the operator has not
            seen — the dialog is still showing "Loading…" at that point. The server validates the
            change independently, so this is about consent, not corruption. */}
        <Button
          size='md'
          onClick={handleSubmit}
          loading={loading}
          disabled={(revertsSelectedChange && !selectedChange) || !!durability?.blocks || rollbackBlocked || rollbackChecking}
        >
          {isRevertTheDevelopment ? 'Roll back' : 'Submit'}
        </Button>
      </Box>
    </>
  );
};

InvestigateResolution.propTypes = {
  row: PropTypes.object,
  handleClose: PropTypes.func,
  updateInvestigateSuccessSnackBar: PropTypes.func,
  isRevertTheDevelopment: PropTypes.bool,
  cardId: PropTypes.string,
  changeEventId: PropTypes.string,
  changeChoices: PropTypes.arrayOf(
    PropTypes.shape({
      changeEventId: PropTypes.string,
      when: PropTypes.string,
      timing: PropTypes.string,
      summary: PropTypes.string,
    })
  ),
};

export default InvestigateResolution;
