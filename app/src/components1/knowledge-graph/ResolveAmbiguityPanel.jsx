// Resolve ambiguity panel — Phase 2 (NB-30989).
//
// Push-replace view shown when an `*_ambiguous` row is clicked. Renders one
// or both candidate lists (the backend stores both arrays even when the row
// status is a single value — source-side wins precedence). Operator picks
// one candidate per rendered side; single POST resolves the row.
//
// `*_too_many_matches` rows can also land here through future Phase 2.5
// affordances. They have no candidates stored; the panel renders a banner
// telling the operator to re-upload with more qualifiers.

import { useMemo, useState } from 'react';
import { Box, Typography, Radio } from '@mui/material';
import PropTypes from 'prop-types';
import { Button } from '@components1/ds/Button';
import { toast as snackbar } from '@components1/ds/Toast';
import { ArrowBackGrayIcon } from '@assets';
import SafeIcon from '@components1/common/SafeIcon';
import apiKnowledgeGraph from '@api1/knowledge-graph';
import { ds } from 'src/utils/colors';

// Composes the endpoint header line shown at the top of the resolve view.
const formatEndpointHeader = (nodeType, name, namespace, cluster, arn) => {
  if (arn) {
    return `${nodeType}: ${arn}`;
  }
  const ns = namespace ? `${namespace}/` : '';
  const clusterSuffix = cluster ? ` @ ${cluster}` : '';
  return `${nodeType}: ${ns}${name}${clusterSuffix}`;
};

// True when this side renders a "too many" banner instead of a candidate list.
const isTooManyForSide = (status, side) =>
  (side === 'source' && status === 'source_too_many_matches') || (side === 'dest' && status === 'dest_too_many_matches');

const ResolveAmbiguityPanel = ({ row, onBack, onResolved }) => {
  const sourceCandidates = row.source_match_candidates ?? [];
  const destCandidates = row.dest_match_candidates ?? [];

  // Per-side render decisions: render the candidate list when any are
  // present, the "too many" banner when the row is in that state, else
  // nothing for that side.
  const showSourceList = sourceCandidates.length > 0;
  const showDestList = destCandidates.length > 0;
  const showSourceTooMany = !showSourceList && isTooManyForSide(row.resolution_status, 'source');
  const showDestTooMany = !showDestList && isTooManyForSide(row.resolution_status, 'dest');

  const [pickedSourceId, setPickedSourceId] = useState('');
  const [pickedDestId, setPickedDestId] = useState('');
  const [submitting, setSubmitting] = useState(false);

  // Enabled when every list rendered has a corresponding selection. Banner-
  // only sides don't gate the button (operator can't pick from them anyway).
  const canResolve = useMemo(() => {
    if (showSourceList && !pickedSourceId) {
      return false;
    }
    if (showDestList && !pickedDestId) {
      return false;
    }
    return showSourceList || showDestList; // at least one side must be a pickable list
  }, [showSourceList, showDestList, pickedSourceId, pickedDestId]);

  const handleResolve = async () => {
    if (!canResolve) {
      return;
    }
    setSubmitting(true);
    try {
      const res = await apiKnowledgeGraph.resolveManualDependency({
        id: row.id,
        sourceNodeId: pickedSourceId || undefined,
        destinationNodeId: pickedDestId || undefined,
      });
      const errors = res?.data?.errors;
      if (errors?.length) {
        snackbar.error(`Resolve failed: ${errors[0]?.message ?? 'Unknown error'}`);
        return;
      }
      snackbar.success('Dependency resolved.');
      onResolved();
    } catch (err) {
      console.error('Resolve failed:', err);
      snackbar.error('Resolve failed.');
    } finally {
      setSubmitting(false);
    }
  };

  const sourceHeader = formatEndpointHeader(row.source_node_type, row.source_name, row.source_namespace, row.source_cluster, row.source_arn);
  const destHeader = formatEndpointHeader(row.dest_node_type, row.dest_name, row.dest_namespace, row.dest_cluster, row.dest_arn);

  return (
    // Constrain the resolve view to a comfortable form width in the wide
    // parent modal — the candidate list reads better in a column than
    // stretched across the full lg modal.
    <Box sx={{ display: 'flex', flexDirection: 'column', gap: 2, py: 2, maxWidth: '720px', mx: 'auto' }}>
      <Box sx={{ display: 'flex', alignItems: 'center', gap: 1 }}>
        <Button tone='link' size='sm' onClick={onBack} icon={<SafeIcon src={ArrowBackGrayIcon} alt='back' width={14} height={14} />}>
          Back to declarations
        </Button>
      </Box>

      <Box>
        <Typography sx={{ fontSize: '14px', fontWeight: 600, color: ds?.text?.secondary ?? '#374151' }}>
          Disambiguate: {sourceHeader} → {destHeader}
        </Typography>
        <Typography sx={{ fontSize: '12px', color: ds?.text?.secondaryDark ?? '#6b7280', mt: 0.5 }}>
          Pick a node for {showSourceList && showDestList ? 'each side' : showSourceList ? 'the source' : 'the destination'}; the row resolves in one
          step.
        </Typography>
      </Box>

      {showSourceList && (
        <CandidateSection
          title={`Source candidates (${sourceCandidates.length}) — pick one`}
          candidates={sourceCandidates}
          picked={pickedSourceId}
          onPick={setPickedSourceId}
          name='source-pick'
        />
      )}
      {showSourceTooMany && <TooManyBanner side='source' matchCount={row.source_match_count ?? 0} />}

      {showDestList && (
        <CandidateSection
          title={`Destination candidates (${destCandidates.length}) — pick one`}
          candidates={destCandidates}
          picked={pickedDestId}
          onPick={setPickedDestId}
          name='dest-pick'
        />
      )}
      {showDestTooMany && <TooManyBanner side='destination' matchCount={row.dest_match_count ?? 0} />}

      <Box sx={{ display: 'flex', justifyContent: 'flex-end', gap: 1, pt: 2 }}>
        <Button tone='secondary' size='md' onClick={onBack} disabled={submitting}>
          Cancel
        </Button>
        <Button tone='primary' size='md' onClick={handleResolve} disabled={!canResolve || submitting} loading={submitting}>
          Resolve
        </Button>
      </Box>
    </Box>
  );
};

ResolveAmbiguityPanel.propTypes = {
  row: PropTypes.object.isRequired,
  onBack: PropTypes.func.isRequired,
  onResolved: PropTypes.func.isRequired,
};

// Pickable list of candidates. Each row is a radio + display block with the
// secondary identifying fields (namespace / cluster / arn) underneath.
const CandidateSection = ({ title, candidates, picked, onPick, name }) => (
  <Box>
    <Typography sx={{ fontSize: '13px', fontWeight: 600, color: ds?.text?.secondary ?? '#374151', mb: 1 }}>{title}</Typography>
    <Box sx={{ display: 'flex', flexDirection: 'column', gap: 0.5 }}>
      {candidates.map((c) => {
        const checked = picked === c.node_id;
        return (
          <Box
            key={c.node_id}
            onClick={() => onPick(c.node_id)}
            sx={{
              display: 'flex',
              alignItems: 'flex-start',
              gap: 1,
              padding: '8px 12px',
              borderRadius: '6px',
              border: `1px solid ${checked ? ds?.border?.primary ?? '#3b82f6' : ds?.border?.secondary ?? '#e5e7eb'}`,
              cursor: 'pointer',
              backgroundColor: checked ? ds?.background?.primaryLightest ?? '#eff6ff' : 'transparent',
              '&:hover': { backgroundColor: ds?.background?.tertiaryLightest ?? '#f3f4f6' },
            }}
          >
            <Radio name={name} size='small' checked={checked} onChange={() => onPick(c.node_id)} sx={{ p: 0, mt: '2px' }} />
            <Box sx={{ display: 'flex', flexDirection: 'column', gap: 0.25, minWidth: 0 }}>
              <Typography sx={{ fontSize: '13px', fontWeight: 500 }}>{c.display_name}</Typography>
              <Typography sx={{ fontSize: '11px', color: ds?.text?.secondaryDark ?? '#6b7280' }}>
                {c.node_type}
                {c.namespace ? ` · ${c.namespace}` : ''}
                {c.cluster ? ` · ${c.cluster}` : ''}
                {c.arn ? ` · ${c.arn}` : ''}
              </Typography>
            </Box>
          </Box>
        );
      })}
    </Box>
  </Box>
);

CandidateSection.propTypes = {
  title: PropTypes.string.isRequired,
  candidates: PropTypes.array.isRequired,
  picked: PropTypes.string.isRequired,
  onPick: PropTypes.func.isRequired,
  name: PropTypes.string.isRequired,
};

// Banner for `*_too_many_matches` sides. No candidate list to show; the
// operator must add qualifiers and re-upload (or use kg_update_manual_dependency
// via curl in Phase 2 — per-row edit form lands in Phase 2.5).
const TooManyBanner = ({ side, matchCount }) => (
  <Box
    sx={{
      padding: '12px 14px',
      borderRadius: '6px',
      border: `1px solid ${ds?.border?.warning ?? '#fcd34d'}`,
      backgroundColor: ds?.background?.warningLightest ?? '#fffbeb',
    }}
  >
    <Typography sx={{ fontSize: '13px', fontWeight: 600, color: ds?.text?.warning ?? '#92400e', mb: 0.5 }}>
      {matchCount} {side} candidates — too many to pick from
    </Typography>
    <Typography sx={{ fontSize: '12px', color: ds?.text?.warning ?? '#92400e' }}>
      Add a <strong>namespace</strong>, <strong>cluster</strong>, or <strong>ARN</strong> qualifier to narrow the match — re-upload via CSV, or call{' '}
      <code>kg_update_manual_dependency</code> directly.
    </Typography>
  </Box>
);

TooManyBanner.propTypes = {
  side: PropTypes.string.isRequired,
  matchCount: PropTypes.number.isRequired,
};

export default ResolveAmbiguityPanel;
