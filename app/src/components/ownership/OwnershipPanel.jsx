import React, { useEffect, useState } from 'react';
import PropTypes from 'prop-types';
import { Box, Divider } from '@mui/material';
import { Button as DsButton } from '@ui/Button';
import { Chip } from '@ui/Chip';
import Text from '@shared/format/Text';
import { isTenantAdmin } from '@lib/auth';
import apiOwnership from '@api1/ownership';
import OwnerBadge from './OwnerBadge';
import AssignOwnerModal from './AssignOwnerModal';

// The chain of ownable levels for a resource, most-specific first. The effective
// owner is the first level that has its own owner.
function buildLevels({ resourceType, resourceKey, cloudAccountId, namespace, resourceLabel }) {
  if (resourceType === 'namespace') {
    return [
      { level: 'Namespace', sub: resourceLabel, type: 'namespace', key: resourceKey },
      ...(cloudAccountId ? [{ level: 'Cloud account', sub: null, type: 'cloud_account', key: cloudAccountId }] : []),
    ];
  }
  if (resourceType === 'cloud_account') {
    return [{ level: 'Cloud account', sub: resourceLabel, type: 'cloud_account', key: resourceKey }];
  }
  // workload
  return [
    { level: 'Workload', sub: resourceLabel, type: 'workload', key: resourceKey },
    ...(cloudAccountId && namespace ? [{ level: 'Namespace', sub: namespace, type: 'namespace', key: `${cloudAccountId}/${namespace}` }] : []),
    ...(cloudAccountId ? [{ level: 'Cloud account', sub: null, type: 'cloud_account', key: cloudAccountId }] : []),
  ];
}

function derivedText(levels, effIndex) {
  if (effIndex < 0) return 'No owner assigned yet.';
  if (effIndex === 0) {
    return levels[0].own.source === 'rule' ? 'Matched by an ownership rule.' : 'Assigned directly to this resource.';
  }
  return levels[effIndex].level === 'Namespace' ? 'Inherited from the namespace owner.' : 'Inherited from the cloud account (cluster) owner.';
}

function Row({ label, children }) {
  return (
    <Box sx={{ display: 'flex', alignItems: 'center', gap: 'var(--ds-space-4)' }}>
      <Box sx={{ minWidth: 160, flexShrink: 0 }}>
        <Text value={label} secondaryText />
      </Box>
      <Box sx={{ minWidth: 0, display: 'flex', alignItems: 'center', gap: 'var(--ds-space-2)' }}>{children}</Box>
    </Box>
  );
}
Row.propTypes = { label: PropTypes.node.isRequired, children: PropTypes.node };

// Ownership detail panel — rendered as the "Ownership" tab of a workload or
// namespace drilldown. Shows the effective owner, how it was derived, and the
// full ownership chain (workload → namespace → cloud account) so it's clear who
// owns each level — the effective owner is the lowest level that has one.
export default function OwnershipPanel(props) {
  const { resourceType, resourceKey, cloudAccountId, resourceLabel } = props;
  const [levels, setLevels] = useState([]);
  const [loading, setLoading] = useState(true);
  const [assignOpen, setAssignOpen] = useState(false);
  const canEdit = isTenantAdmin();

  const refetch = () => {
    const descs = buildLevels(props);
    if (!resourceKey) {
      setLevels([]);
      setLoading(false);
      return;
    }
    setLoading(true);
    Promise.all(descs.map((d) => apiOwnership.getOwner({ resourceType: d.type, resourceKey: d.key }).catch(() => null)))
      .then((results) => {
        setLevels(
          descs.map((d, i) => {
            const res = results[i];
            // A level's "own" owner is one resolved directly on it (via self),
            // not inherited from a higher level.
            return { ...d, own: res && res.found && res.via === 'self' ? res : null };
          })
        );
      })
      .finally(() => setLoading(false));
  };
  useEffect(() => {
    refetch();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [resourceType, resourceKey, cloudAccountId, props.namespace]);

  const effIndex = levels.findIndex((l) => l.own);
  // Effective owner for the current resource, with `via` relative to it so the
  // badge shows the right inherited/rule hint.
  const effective =
    effIndex >= 0
      ? { ...levels[effIndex].own, via: effIndex === 0 ? 'self' : levels[effIndex].level === 'Namespace' ? 'namespace' : 'cluster' }
      : null;
  const hasDirectOwner = levels[0]?.own && levels[0].own.via === 'self' && levels[0].own.source === 'manual';

  return (
    <Box sx={{ p: 'var(--ds-space-5)', display: 'flex', flexDirection: 'column', gap: 'var(--ds-space-4)' }}>
      <Row label='Effective owner'>{loading ? <Text value='Resolving…' secondaryText /> : <OwnerBadge owner={effective} />}</Row>
      <Row label='How it was derived'>
        <Text value={loading ? '' : derivedText(levels, effIndex)} secondaryText />
      </Row>

      {levels.length > 1 ? (
        <>
          <Divider sx={{ my: 'var(--ds-space-1)' }} />
          <Text value='Ownership chain' secondaryText />
          {levels.map((l, i) => (
            <Row key={l.level} label={l.sub ? `${l.level} · ${l.sub}` : l.level}>
              {l.own ? <OwnerBadge owner={l.own} /> : <Text value='—' secondaryText />}
              {i === effIndex ? (
                <Chip variant='tag' size='2xs' tone='info'>
                  effective
                </Chip>
              ) : null}
            </Row>
          ))}
        </>
      ) : null}

      {canEdit ? (
        <Box sx={{ display: 'flex', gap: 1, mt: 'var(--ds-space-2)' }}>
          <DsButton tone='secondary' size='sm' onClick={() => setAssignOpen(true)} id='ownership-tab-assign'>
            {hasDirectOwner ? 'Change owner' : 'Assign owner'}
          </DsButton>
        </Box>
      ) : null}
      {assignOpen ? (
        <AssignOwnerModal
          open={assignOpen}
          onClose={() => setAssignOpen(false)}
          onChange={refetch}
          resourceType={resourceType}
          resourceKey={resourceKey}
          cloudAccountId={cloudAccountId}
          resourceLabel={resourceLabel}
        />
      ) : null}
    </Box>
  );
}

OwnershipPanel.propTypes = {
  resourceType: PropTypes.string.isRequired,
  resourceKey: PropTypes.string,
  cloudAccountId: PropTypes.string,
  namespace: PropTypes.string,
  resourceLabel: PropTypes.string,
};
