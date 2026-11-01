import React, { useEffect, useMemo, useState } from 'react';
import { Box, Typography } from '@mui/material';
import Tooltip from '@ui/Tooltip';
import KeyboardDoubleArrowLeftIcon from '@mui/icons-material/KeyboardDoubleArrowLeft';
import KeyboardDoubleArrowRightIcon from '@mui/icons-material/KeyboardDoubleArrowRight';
import { useRouter } from 'next/router';
import OwnerBadge from '@components/ownership/OwnerBadge';
import useResourceOwner, { sourceText } from '@hooks/useResourceOwner';
import PropTypes from 'prop-types';
import { ds } from 'src/utils/colors';
import Datetime, { formatClock, formatClockCompact } from '@shared/format/Datetime';
import { splitInternalLabels } from './sidebarLabels';
import Text from '@shared/format/Text';
import SafeIcon from '@shared/icons/SafeIcon';
import { Label } from '@ui/Label';
import { Button } from '@ui/Button';
import { Link } from '@ui/Link';
import NBStatusBadge from '@shared/widgets/NBStatusBadge';
import TriageScore from '@shared/widgets/TriageScore';
import InvestigateDropdown from '@components/k8s/investigate/InvestigateDropdown';
import { EventOccurrenceRows, EventStateRows, useEventOccurrence } from '@components/k8s/investigate/EventStateRows';
import k8sApi from '@api1/kubernetes';
import { SOURCE_SEVERITY_INFO } from '@api1/triage';
import { describeContainerMemory } from '@components/k8s/investigate/podFacts';
import { hasWriteAccess, getUserSession } from '@lib/auth';
import { snackbar } from '@shared/snackbarService';
import { exitCodeMapping, snakeToTitleCase } from 'src/utils/common';
import { hasEventSubjectLink } from 'src/utils/eventSubjectLink';
import TroubleShootIcon from '@assets/home/node-errors-icon.svg';
import { infoIcon } from '@assets';

// buildOwnershipLevels returns the ownership chain to resolve for the investigated
// resource, most specific rung first, skipping any rung whose key is unavailable.
//
// The k8s/cloud split is taken from the caller's own isK8s flag and NOT inferred from
// whether a namespace is present: cloud events carry a `subject_namespace` too, but it
// holds the cloud service name (`EC2`, `RDS`) rather than a namespace, so inferring
// would build a nonsense `<account>/EC2` namespace rung.
//
// `cloud_resource_id` is best-effort on both variants — the collectors only set it when
// the event matched a known resource — so the resource rung is frequently absent and the
// chain falls back to namespace/account. `cloud_account_id` is always present, so there
// is always at least one rung to resolve.
export function buildOwnershipLevels({ isK8s, cloudResourceId, cloudAccountId, namespace }) {
  const levels = [];
  if (isK8s) {
    if (cloudResourceId) {
      levels.push({ level: 'Workload', resourceType: 'workload', resourceKey: cloudResourceId, own: null });
    }
    if (cloudAccountId && namespace) {
      levels.push({ level: 'Namespace', resourceType: 'namespace', resourceKey: `${cloudAccountId}/${namespace}`, own: null });
    }
  } else if (cloudResourceId) {
    levels.push({ level: 'Resource', resourceType: 'cloud_resource', resourceKey: cloudResourceId, own: null });
  }
  if (cloudAccountId) {
    levels.push({ level: 'Cloud account', resourceType: 'cloud_account', resourceKey: cloudAccountId, own: null });
  }
  return levels;
}

// One label/value row. minmax(0, 1fr) so a long value wraps or ellipsizes
// instead of widening the panel.
const rowSx = {
  display: 'grid',
  alignItems: 'flex-start',
  gridTemplateColumns: `${ds.space.mul(1, 25)} minmax(0, 1fr)`,
  columnGap: ds.space[2],
};
const valueTextSx = { color: ds.gray[700], lineHeight: '1.4' };

// `info` puts an icon after the value whose hover says what the row means.
const Row = ({ label, children, testId, info }) => (
  <Box sx={rowSx} data-testid={testId}>
    <Text value={label} secondaryText />
    <Box sx={{ minWidth: 0, display: 'flex', alignItems: 'center', flexWrap: 'wrap', gap: ds.space[1] }}>
      {children}
      {info && (
        <Tooltip title={info} arrow placement='bottom'>
          {/* SafeIcon can't hold a ref, and without one the tooltip never opens. */}
          <Box component='span' sx={{ display: 'inline-flex' }}>
            <SafeIcon src={infoIcon} alt='info' style={{ width: '13px', height: '13px', cursor: 'pointer', filter: 'opacity(0.5)' }} />
          </Box>
        </Tooltip>
      )}
    </Box>
  </Box>
);
Row.propTypes = { label: PropTypes.string, children: PropTypes.node, testId: PropTypes.string, info: PropTypes.string };

// "pod" → "Pod", "ec2_instance" → "Ec2 Instance"; "Where" when the event has no type.
const subjectLabel = (type) => {
  const words = String(type || '')
    .split(/[_\s]+/)
    .filter(Boolean);
  if (words.length === 0) return 'Where';
  return words.map((w) => w.charAt(0).toUpperCase() + w.slice(1)).join(' ');
};

// Short, labelled groups instead of one long list: the reader scans the
// headings to find the fact they need.
const Section = ({ title, children, testId }) => (
  <Box
    data-testid={testId}
    sx={{ mt: ds.space[5], pt: ds.space[3], borderTop: `0.5px solid ${ds.gray[200]}`, display: 'flex', flexDirection: 'column', gap: ds.space[2] }}
  >
    <Typography
      sx={{ fontSize: ds.text.caption, fontWeight: ds.weight.semibold, letterSpacing: '0.07em', textTransform: 'uppercase', color: ds.gray[500] }}
    >
      {title}
    </Typography>
    {children}
  </Box>
);
Section.propTypes = { title: PropTypes.string, children: PropTypes.node, testId: PropTypes.string };

const Disclosure = ({ open, onToggle, label, testId }) => (
  <Button tone='link' size='xs' onClick={onToggle} data-testid={testId} aria-expanded={open}>
    {`${label} ${open ? '▾' : '▸'}`}
  </Button>
);
Disclosure.propTypes = { open: PropTypes.bool, onToggle: PropTypes.func, label: PropTypes.string, testId: PropTypes.string };

function InvestigateSidebar({
  row,
  podDetails,
  othersData,
  alertLabels,
  alertRules,
  queryParam,
  isK8s,
  isCloud,
  sourceKnown = true,
  onPodClick,
  onRowChange,
  onCreateTicket,
  onResetState,
  collapsed,
  onToggleCollapse,
}) {
  const router = useRouter();
  const [showAll, setShowAll] = useState(false);
  const [showInternal, setShowInternal] = useState(false);
  const [showDetails, setShowDetails] = useState(false);
  const [showLabels, setShowLabels] = useState(false);
  const occurrence = useEventOccurrence(row);
  const [showChain, setShowChain] = useState(false);

  const {
    effective: owner,
    levels: ownerLevels,
    effectiveIndex: ownerEffectiveIndex,
    loading: ownerLoading,
    unresolvable: ownerUnresolvable,
  } = useResourceOwner(
    // Wait for the page to actually know k8s-vs-cloud. It defaults to k8s until the
    // account list loads, and resolving the wrong chain would ask for a namespace rung
    // keyed on a cloud service name (`<account>/AmazonEC2`).
    sourceKnown
      ? buildOwnershipLevels({
          isK8s,
          cloudResourceId: row?.cloud_resource_id,
          cloudAccountId: row?.cloud_account_id,
          namespace: row?.subject_namespace,
        })
      : []
  );
  // Nothing to resolve (no account), the source isn't known yet, or the resolve failed
  // — drop the section rather than claim the resource is unowned.
  const showOwner = sourceKnown && !ownerUnresolvable && !!row?.cloud_account_id;
  const [incidentMembers, setIncidentMembers] = useState([]);

  // Members of this event's incident group (#34655) — fetched only when the
  // event is a group leader.
  useEffect(() => {
    let cancelled = false;
    // Keep the same array when already empty: a fresh [] changes `incidentMemberIds`, which
    // re-runs InvestigateDropdown's fetch (it lists excludeIds in its deps) on every event load.
    setIncidentMembers((prev) => (prev.length ? [] : prev));
    if (row?.id && row?.cloud_account_id && row?.incident_member_count > 0) {
      k8sApi
        .getIncidentGroupMembers(row.id, row.cloud_account_id)
        .then((members) => {
          if (!cancelled) setIncidentMembers(members);
        })
        .catch(() => {});
    }
    return () => {
      cancelled = true;
    };
  }, [row?.id, row?.cloud_account_id, row?.incident_member_count]);

  // Who manually closed this event, if anyone — same batch lookup the Events
  // table uses (event_get_status_change), just called with a single id here.
  // Only returns a name when the LATEST status change was a manual close, so
  // it's re-fetched whenever the event's own status flips (a reopen elsewhere,
  // or this panel's own toggle below, can make a previously-set name stale).
  const [closedByName, setClosedByName] = useState(undefined);
  const [closedReasonCode, setClosedReasonCode] = useState(undefined);
  const [closedReasonText, setClosedReasonText] = useState(undefined);
  const [closedEvidenceUrl, setClosedEvidenceUrl] = useState(undefined);
  useEffect(() => {
    let cancelled = false;
    setClosedByName(undefined);
    setClosedReasonCode(undefined);
    setClosedReasonText(undefined);
    setClosedEvidenceUrl(undefined);
    if (row?.id && row?.status === 'CLOSED') {
      k8sApi
        .getEventStatusChanges([row.id])
        .then((changes) => {
          if (cancelled) return;
          const match = (changes || []).find((c) => c.event_id === row.id);
          if (match?.changed_by_name) {
            setClosedByName(match.changed_by_name);
            setClosedReasonCode(match.reason_code);
            setClosedReasonText(match.reason_text);
            setClosedEvidenceUrl(match.evidence_url);
          }
        })
        .catch(() => {});
    }
    return () => {
      cancelled = true;
    };
  }, [row?.id, row?.status]);

  const handleToggleAlertStatus = async (eventId, newStatus, details) => {
    try {
      await k8sApi.updateEventStatus(eventId, newStatus, details);
      onRowChange((prev) => ({ ...prev, status: newStatus }));
      if (newStatus === 'CLOSED') {
        const closerName = getUserSession()?.user?.name || getUserSession()?.user?.email;
        if (closerName) {
          setClosedByName(closerName);
          setClosedReasonCode(details?.reasonCode);
          setClosedReasonText(details?.reasonText);
          setClosedEvidenceUrl(details?.evidenceUrl);
        }
      } else {
        setClosedByName(undefined);
        setClosedReasonCode(undefined);
        setClosedReasonText(undefined);
        setClosedEvidenceUrl(undefined);
      }
      snackbar.success(newStatus === 'CLOSED' ? 'Event closed' : 'Event opened');
    } catch (_error) {
      snackbar.error(newStatus === 'CLOSED' ? 'Failed to close event' : 'Failed to open event');
    }
  };

  const detailsPathPrefix = isCloud ? '/cloud-account/details' : '/kubernetes/details';

  const fitCustomLabelStyles = useMemo(
    () => ({
      width: 'auto',
      maxWidth: '100%',
      minWidth: 0,
      justifySelf: 'start',
      whiteSpace: 'normal',
      overflowWrap: 'anywhere',
      padding: 'var(--ds-space-1) var(--ds-space-2)',
      boxSizing: 'border-box',
    }),
    []
  );

  const investigateDescription = useMemo(() => {
    let logText = row?.description;
    if (isK8s && alertLabels?.nb_webhook_url) {
      logText = (logText || '') + `\n\n**Alert Url -** [${alertLabels.alertname || alertLabels.nb_webhook_event_id}](${alertLabels.nb_webhook_url})`;
    }
    if (logText) {
      const logSampleMatch = logText.match(/Log Sample:\s*(\{.*?\})\s*Failure Count:/s);
      const containerIdMatch = logText.match(/Container ID:\s*(\/[^\s]+)/);
      const failureCountMatch = logText.match(/Failure Count:\s*(\d+)/);
      return {
        logSample: logSampleMatch ? logSampleMatch[1] : logText,
        containerId: containerIdMatch ? containerIdMatch[1] : '',
        failureCount: failureCountMatch ? failureCountMatch[1] : '',
      };
    }
    return { logSample: '', containerId: '', failureCount: '' };
  }, [row?.description, isK8s, alertLabels]);

  const mapLabels = (label) => {
    if (!label) {
      return [];
    }
    return Object.entries(label).map(([key, value]) => (
      <Label
        textTransform='none'
        height='auto'
        margin='0'
        wordBreak={'break-all'}
        displayTooltip
        key={key}
        text={`${key}=${value}`}
        variant={'grey'}
        maxWidth={ds.space.mul(1, 65)}
        tooltipCharLimit={40}
      />
    ));
  };

  const incidentMemberIds = useMemo(() => incidentMembers.map((m) => String(m.id)), [incidentMembers]);
  // Nudgebee's own bookkeeping keys are not alert labels: a triage rule id of all
  // zeros on the first screen reads as a broken value, and a routing key printed
  // here is an integration secret on screen.
  const [publicLabels, internalLabels] = useMemo(() => splitInternalLabels(alertLabels), [alertLabels]);

  const labels = useMemo(() => mapLabels(publicLabels), [publicLabels]);
  const internalLabelChips = useMemo(() => mapLabels(internalLabels), [internalLabels]);
  const visibleLabels = useMemo(() => (showAll ? labels : labels.slice(0, 5)), [labels, showAll]);
  const toggleShow = () => setShowAll((prev) => !prev);

  if (collapsed) {
    return (
      <Box
        sx={{
          position: 'sticky !important',
          top: `${ds.space.mul(0, 37)} !important`,
          display: 'flex',
          justifyContent: 'center',
          pt: 'var(--ds-space-2)',
        }}
      >
        <Button
          tone='secondary'
          size='md'
          composition='icon-only'
          aria-label='Expand details panel'
          tooltip='Expand details'
          tooltipPlacement='right'
          icon={<KeyboardDoubleArrowRightIcon style={{ width: 20, height: 20 }} />}
          onClick={onToggleCollapse}
          data-testid='sidebar-expand-toggle'
        />
      </Box>
    );
  }

  const hasCrashDetails = podDetails?.data?.containers?.some(
    (pd) => pd?.status?.reason || (pd?.status?.exitCode !== undefined && pd?.status?.exitCode !== null) || pd?.name || pd?.status?.state
  );
  const hasLastState = podDetails?.data?.containers?.some(
    (pd) => pd?.lastStatus && (pd.lastStatus.state || pd.lastStatus.exitCode !== undefined || pd.lastStatus.reason)
  );
  const restarts = podDetails?.data?.restarts;
  const containersReady = podDetails?.data?.containersReady;
  const showHowOften = !!occurrence || (isK8s && (restarts > 0 || othersData?.length > 0));

  return (
    <Box sx={{ position: 'sticky !important', top: `${ds.space.mul(0, 37)} !important` }}>
      <Box
        sx={{
          backgroundColor: ds.background[100],
          border: `0.5px solid ${ds.gray[300]}`,
          borderRadius: 'var(--ds-radius-lg)',
          padding: '0 var(--ds-space-4) var(--ds-space-4) var(--ds-space-4)',
          maxHeight: `calc(100vh - ${ds.space.mul(0, 55)})`,
          overflowY: 'auto',
          overflowX: 'hidden',
          '::-webkit-scrollbar': { width: ds.space[1] },
        }}
      >
        <Box
          sx={{
            position: 'sticky',
            top: 0,
            zIndex: 2,
            background: ds.background[100],
            display: 'flex',
            alignItems: 'center',
            gap: 'var(--ds-space-2)',
            borderBottom: `1px solid ${ds.gray[200]}`,
            paddingTop: 'var(--ds-space-4)',
            paddingBottom: 'var(--ds-space-3)',
          }}
        >
          <SafeIcon alt='kube-icon' src={TroubleShootIcon} />
          <Text
            value={row?.title || ''}
            showAutoEllipsis
            placement='right'
            sx={{ fontWeight: 'var(--ds-font-weight-semibold)', fontSize: 'var(--ds-text-title)' }}
          />
          <Box sx={{ ml: 'auto', flexShrink: 0 }}>
            <Button
              tone='ghost'
              size='sm'
              composition='icon-only'
              aria-label='Collapse panel'
              tooltip='Collapse panel'
              tooltipPlacement='right'
              icon={<KeyboardDoubleArrowLeftIcon />}
              onClick={onToggleCollapse}
              data-testid='sidebar-collapse-toggle'
            />
          </Box>
        </Box>

        {/* The facts that decide what to do next. */}
        <Box sx={{ mt: ds.space[3], display: 'flex', flexDirection: 'column', gap: ds.space[2] }}>
          {/* Where it is and whose it is, first. The subject's type labels the
              row ("Pod", "Deployment", "Ec2 Instance"); rows without a value are
              skipped rather than shown as "-". */}
          <Row label={subjectLabel(row?.subject_type)}>
            <Box
              role='button'
              tabIndex={0}
              onClick={onPodClick}
              onKeyDown={(e) => (e.key === 'Enter' || e.key === ' ') && onPodClick(e)}
              sx={{ cursor: 'pointer', minWidth: 0 }}
              data-testid='pod-name-link'
            >
              <Text
                value={row?.subject_name ? row?.subject_name : '-'}
                secondaryText
                showAutoEllipsis
                copyableTooltip
                sx={{ color: hasEventSubjectLink(row) ? ds.blue[600] : ds.gray[700], lineHeight: '1.4' }}
              />
            </Box>
          </Row>
          {row?.subject_namespace && (
            // Cloud events keep the service name and region in these fields.
            <Row label={isCloud ? 'Service' : 'Namespace'}>
              <Text value={row.subject_namespace} secondaryText showAutoEllipsis sx={valueTextSx} />
            </Row>
          )}
          {row?.subject_node && (
            <Row label={isCloud ? 'Region' : 'Node'}>
              <Text value={row.subject_node} copyableTooltip showAutoEllipsis sx={{ fontSize: ds.text.small, ...valueTextSx }} />
            </Row>
          )}
          {row?.cluster && (
            <Row label='Cluster'>
              <Text value={row.cluster} secondaryText showAutoEllipsis sx={valueTextSx} />
            </Row>
          )}
          {showOwner && (
            <Row label='Owner'>
              {ownerLoading ? (
                <Text value={'Resolving…'} secondaryText />
              ) : owner ? (
                <OwnerBadge owner={owner} hideHint />
              ) : (
                <Text value={'Unassigned'} secondaryText sx={valueTextSx} />
              )}
            </Row>
          )}
          <Box sx={{ borderTop: `0.5px solid ${ds.gray[200]}`, my: ds.space[1] }} />
          <Row label='When'>
            {/* Relative answers "is this fresh", the clock time "which moment"; the
                full form with timezone and year is the hover. */}
            {row?.starts_at && (
              <Datetime value={row?.starts_at} sx={{ fontSize: ds.text.small }} sxSuffix={{ color: ds.gray[700], fontSize: ds.text.small }} />
            )}
            {formatClockCompact(row?.starts_at) && (
              <Tooltip title={formatClock(row.starts_at)} arrow placement='bottom'>
                <Box component='span' sx={{ display: 'inline-flex' }}>
                  <Text
                    value={`· ${formatClockCompact(row.starts_at)}`}
                    secondaryText
                    sx={{ ...valueTextSx, whiteSpace: 'nowrap', cursor: 'default' }}
                  />
                </Box>
              </Tooltip>
            )}
          </Row>
          <EventStateRows
            row={row}
            canToggleStatus={hasWriteAccess(row?.cloud_account_id || router.query.accountId)}
            closedByName={closedByName}
            closedReasonCode={closedReasonCode}
            closedReasonText={closedReasonText}
            closedEvidenceUrl={closedEvidenceUrl}
            onToggleStatus={handleToggleAlertStatus}
          />
          {/* This row and Triage below can disagree, and nothing said why. This one is what the
              alert's source set; the Triage row already explains its own score on hover. */}
          <Row label='Source Severity' info={SOURCE_SEVERITY_INFO}>
            <Label text={row?.priority || '-'} margin='0' />
          </Row>
          {row?.computed_score !== null && row?.computed_score !== undefined && (
            <Row label='Triage'>
              <TriageScore
                layout='inline'
                score={row?.computed_score}
                priority={row?.computed_priority}
                scoreFactors={row?.score_factors}
                confidence={row?.score_confidence}
                eventId={row?.id}
                accountId={row?.cloud_account_id || router.query.accountId}
                canWrite={hasWriteAccess(router.query.accountId)}
                onChanged={(newPriority, newScore, newFactors) => {
                  onRowChange?.((prev) => ({ ...prev, computed_priority: newPriority, computed_score: newScore, score_factors: newFactors }));
                }}
                align='end'
              />
            </Row>
          )}
          {/* The triage-status dropdown is its own field, not part of the score — split
              out of the Triage row per the triage-score design handoff. */}
          <Row label='Status'>
            <NBStatusBadge
              eventId={row?.id}
              currentStatus={row?.nb_status || 'OPEN'}
              snoozedUntil={row?.snoozed_until}
              onStatusChange={(newStatus, snoozedUntil) => {
                onRowChange((prev) => ({ ...prev, nb_status: newStatus, snoozed_until: snoozedUntil ?? null }));
              }}
              onCreateTicket={!row?.ticket_id ? () => onCreateTicket(row) : undefined}
            />
          </Row>
        </Box>

        {showHowOften && (
          <Section title='How often' testId='sidebar-how-often'>
            <EventOccurrenceRows occurrence={occurrence} accountId={row?.cloud_account_id} />
            {isK8s && restarts > 0 && (
              <Row label='Restarts'>
                <Text value={restarts} secondaryText sx={{ ...valueTextSx, color: restarts > 1 ? ds.red[500] : ds.gray[700] }} />
              </Row>
            )}
            {isK8s && othersData?.length > 0 && (
              // Every alert of this rule on this service before the event — a
              // different population from Firings, so a separate row.
              <Row label='Same rule here'>
                <Box sx={{ display: 'flex', flexDirection: 'column' }}>
                  {othersData.map((item) => (
                    <Text key={item} value={item} secondaryText sx={valueTextSx} />
                  ))}
                </Box>
              </Row>
            )}
          </Section>
        )}

        {isK8s && hasCrashDetails && (
          <Section title='Container' testId='sidebar-container'>
            {containersReady?.total > 0 && (
              // The same two-column grid as the container rows below, so the values line up.
              <Box
                sx={{
                  display: 'grid',
                  alignItems: 'flex-start',
                  gridTemplateColumns: `${ds.space.mul(1, 25)} minmax(0, 1fr)`,
                  gap: 'var(--ds-space-1)',
                }}
              >
                <Text value={'Ready'} secondaryText />
                <Text
                  value={`${containersReady.ready} of ${containersReady.total} ${containersReady.total === 1 ? 'container' : 'containers'}`}
                  secondaryText
                  sx={{ color: containersReady.ready < containersReady.total ? ds.red[500] : ds.gray[700] }}
                />
              </Box>
            )}
            {podDetails?.data?.containers?.length > 0 && (
              <Box sx={{ display: 'flex', flexDirection: 'column', gap: 'var(--ds-space-1)' }}>
                {podDetails?.data?.containers?.map((pd, index) => (
                  <React.Fragment key={pd?.name || index}>
                    <Box
                      sx={{
                        display: 'grid',
                        flexDirection: 'column',
                        alignItems: 'flex-start',
                        gridTemplateColumns: `${ds.space.mul(1, 25)} minmax(0, 1fr)`,
                        gap: 'var(--ds-space-1)',
                      }}
                    >
                      {pd?.name && (
                        <>
                          {pd?.status?.reason && (
                            <>
                              <Text value={'Reason'} secondaryText />
                              <Label variant={'red'} customLabelStyle={{ padding: 'var(--ds-space-1) var(--ds-space-1)' }} text={pd.status.reason} />
                            </>
                          )}
                          <>
                            <Text value={'Container'} secondaryText />
                            <Text
                              value={pd.name}
                              copyableTooltip
                              showAutoEllipsis
                              sx={{ fontSize: 'var(--ds-text-small)', color: ds.gray[700], lineHeight: '1.4' }}
                            />
                          </>
                          {pd?.status?.exitCode !== undefined && pd?.status?.exitCode !== null && (
                            <>
                              <Text value={'Exit code'} secondaryText />
                              <Box sx={{ display: 'flex', alignItems: 'center', gap: 'var(--ds-space-1)' }}>
                                <Text value={pd.status.exitCode} secondaryText sx={{ color: ds.gray[700] }} />
                                <Tooltip title={exitCodeMapping[pd.status.exitCode] || 'Unknown'} arrow placement='bottom'>
                                  <SafeIcon
                                    src={infoIcon}
                                    alt='info'
                                    style={{ width: '13px', height: '13px', cursor: 'pointer', filter: 'opacity(0.5)' }}
                                  />
                                </Tooltip>
                              </Box>
                            </>
                          )}
                          {pd?.status?.state && (
                            <>
                              <Text value={'Status'} secondaryText />
                              <Text value={pd.status.state} secondaryText sx={{ color: ds.gray[700] }} />
                            </>
                          )}
                          {describeContainerMemory(pd) && (
                            <>
                              <Text value={'Memory'} secondaryText />
                              <Text value={describeContainerMemory(pd)} secondaryText sx={{ color: ds.gray[700] }} />
                            </>
                          )}
                        </>
                      )}
                    </Box>
                  </React.Fragment>
                ))}
              </Box>
            )}
          </Section>
        )}

        {isK8s && hasLastState && (
          <Section title='Previous run'>
            {podDetails?.data?.containers?.map((pd, index) => (
              <Box key={pd?.name || index}>
                <Box className='container-box' sx={{ mb: ds.space[2], fontSize: 'var(--ds-text-body)' }}>
                  <Box>
                    {pd?.name && pd?.lastStatus && Object.values(pd.lastStatus).some((v) => v !== null && v !== undefined) && (
                      <Box
                        sx={{
                          display: 'grid',
                          flexDirection: 'column',
                          alignItems: 'flex-start',
                          gridTemplateColumns: `${ds.space.mul(1, 25)} minmax(0, 1fr)`,
                          gap: 'var(--ds-space-1)',
                        }}
                      >
                        {pd.lastStatus.state && (
                          <>
                            <Text value={'State'} secondaryText />
                            <Text value={pd.lastStatus.state} secondaryText sx={{ color: ds.gray[700] }} />
                          </>
                        )}
                        {pd.lastStatus.exitCode !== undefined && (
                          <>
                            <Text value={'Exit Code'} secondaryText />
                            <Box sx={{ display: 'flex', alignItems: 'center', gap: 'var(--ds-space-1)' }}>
                              <Text value={pd.lastStatus.exitCode} secondaryText sx={{ color: ds.gray[700] }} />
                              <Tooltip title={exitCodeMapping[pd.lastStatus.exitCode] || 'Unknown'} arrow placement='bottom'>
                                <SafeIcon
                                  src={infoIcon}
                                  alt='info'
                                  style={{ width: '12px', height: '12px', cursor: 'pointer', filter: 'opacity(0.5)' }}
                                />
                              </Tooltip>
                            </Box>
                          </>
                        )}
                        {pd.lastStatus.reason && (
                          <>
                            <Text value={'Reason'} secondaryText />
                            <Text value={pd.lastStatus.reason} secondaryText sx={{ color: ds.gray[700] }} />
                          </>
                        )}
                      </Box>
                    )}
                  </Box>
                </Box>
              </Box>
            ))}
          </Section>
        )}

        <Section title='Related' testId='sidebar-related'>
          <Row label='Rule'>
            {row?.source === 'automation' && row?.labels?.automation_id ? (
              <Link
                style={{ textDecoration: 'none', display: 'inline-flex', margin: '0' }}
                href={`/automation/${row.labels.automation_id}?executionId=${row.labels.automation_execution_id || ''}&accountId=${
                  row?.cloud_account_id || ''
                }#executions`}
                openInNew={true}
                id='rule-automation-link'
              >
                <Label
                  text={row.labels.automation_name || row?.aggregation_key || '-'}
                  margin='0'
                  height='auto'
                  wordBreak='break-word'
                  customLabelStyle={fitCustomLabelStyles}
                  displayTooltip={isCloud}
                  tooltipCharLimit={isCloud ? 25 : undefined}
                />
              </Link>
            ) : row.aggregation_key && alertRules?.includes(row.aggregation_key) ? (
              <Link
                style={{ textDecoration: 'none', display: 'inline-flex', margin: '0' }}
                href={`${detailsPathPrefix}/${row?.cloud_account_id}?name=${row?.aggregation_key}#monitoring/alert-manager`}
                openInNew={true}
              >
                <Label
                  text={(row?.aggregation_key?.includes('_') ? snakeToTitleCase(row?.aggregation_key) : row?.aggregation_key) || '-'}
                  margin='0'
                  height='auto'
                  wordBreak='break-word'
                  customLabelStyle={fitCustomLabelStyles}
                  displayTooltip={isCloud}
                  tooltipCharLimit={isCloud ? 25 : undefined}
                />
              </Link>
            ) : (
              <Label
                text={(row?.aggregation_key?.includes('_') ? snakeToTitleCase(row?.aggregation_key) : row?.aggregation_key) || '-'}
                margin='0'
                height='auto'
                wordBreak='break-word'
                customLabelStyle={fitCustomLabelStyles}
                displayTooltip={isCloud}
                tooltipCharLimit={isCloud ? 25 : undefined}
              />
            )}
          </Row>
          {row?.incident_leader_id && (
            <Row label='Grouped under'>
              <Link
                style={{ textDecoration: 'none', display: 'inline-flex', margin: '0' }}
                href={`/investigate?id=${row.incident_leader_id}&accountId=${row?.cloud_account_id}`}
                openInNew={true}
              >
                <Text value={'leader event'} secondaryText sx={{ color: ds.blue[600] }} />
              </Link>
            </Row>
          )}
          {row?.incident_member_count > 0 && incidentMembers.length > 0 && (
            <InvestigateDropdown
              query={queryParam}
              inputMaxWidth='100%'
              subjectName={row?.subject_name}
              subjectNamespace={row?.subject_namespace}
              resetStateWhenItemSelected={onResetState}
              title={`Grouped Alerts (${row.incident_member_count})`}
              placeholder='Alerts grouped with this event'
              optionsOverride={incidentMembers.map((m) => ({
                value: String(m.id),
                label: m.title || m.aggregation_key,
                // parseDateValue, not new Date: these strings carry no offset, and
                // reading them as local shifts every grouped alert by the offset.
                subtext: formatClock(m.starts_at) || undefined,
              }))}
            />
          )}
          <InvestigateDropdown
            query={queryParam}
            inputMaxWidth='100%'
            subjectName={row?.subject_name}
            subjectNamespace={row?.subject_namespace}
            resetStateWhenItemSelected={onResetState}
            excludeIds={incidentMemberIds}
          />

          {/* References rather than decisions: each opens on demand. */}
          <Box sx={{ display: 'flex', flexWrap: 'wrap', alignItems: 'center', gap: ds.space[3], mt: ds.space[1] }}>
            <Disclosure open={showDetails} onToggle={() => setShowDetails((prev) => !prev)} label='Details' testId='toggle-details-btn' />
            {labels.length > 0 && (
              <Disclosure
                open={showLabels}
                onToggle={() => setShowLabels((prev) => !prev)}
                label={`Alert labels (${labels.length})`}
                testId='toggle-alert-labels-btn'
              />
            )}
            {internalLabelChips.length > 0 && (
              <Disclosure
                open={showInternal}
                onToggle={() => setShowInternal((prev) => !prev)}
                label={`Internal fields (${internalLabelChips.length})`}
                testId='toggle-internal-labels-btn'
              />
            )}
          </Box>

          {showDetails && (
            <Box sx={{ display: 'flex', flexDirection: 'column', gap: ds.space[2] }} data-testid='sidebar-details'>
              {row?.labels?.nb_triage_rule_id && (
                <Row label='Triage rule'>
                  <Link
                    style={{ textDecoration: 'none', display: 'inline-flex', margin: '0' }}
                    href={`${detailsPathPrefix}/${row?.cloud_account_id}#events/triage-rules`}
                    openInNew={true}
                  >
                    <Text value={'open'} secondaryText sx={{ color: ds.blue[600] }} />
                  </Link>
                </Row>
              )}
              {showOwner && !ownerLoading && (
                <Row label='Owner source'>
                  <Text value={sourceText(ownerLevels, ownerEffectiveIndex)} secondaryText sx={valueTextSx} />
                </Row>
              )}
              {showOwner && (
                <Box sx={{ display: 'grid' }}>
                  {!ownerLoading && ownerLevels.length > 1 && (
                    <Box sx={{ gridColumn: '1 / -1', mt: 'var(--ds-space-1)' }}>
                      <Button
                        tone='link'
                        size='xs'
                        onClick={() => setShowChain((prev) => !prev)}
                        data-testid='ownership-chain-toggle'
                        aria-expanded={showChain}
                      >
                        {showChain ? 'Hide chain' : 'Show chain'}
                      </Button>

                      {showChain && (
                        <Box sx={{ display: 'flex', flexDirection: 'column', gap: 'var(--ds-space-1)', mt: 'var(--ds-space-1)' }}>
                          {ownerLevels.map((l, i) => (
                            <Box
                              key={l.level}
                              sx={{
                                display: 'flex',
                                alignItems: 'center',
                                gap: 'var(--ds-space-1)',
                                minWidth: 0,
                                pl: `calc(var(--ds-space-2) * ${i})`,
                              }}
                            >
                              {i > 0 && <Text value={'└'} secondaryText />}
                              <Text value={l.level} secondaryText />
                              {l.own ? <OwnerBadge owner={l.own} hideHint /> : <Text value={'—'} secondaryText />}
                              {i === ownerEffectiveIndex && (
                                <Label size='sm' tone='info'>
                                  effective
                                </Label>
                              )}
                            </Box>
                          ))}
                        </Box>
                      )}
                    </Box>
                  )}
                </Box>
              )}
              {isK8s && podDetails?.data?.containers?.[0]?.imageName && (
                <Row label='Image'>
                  <Text
                    value={podDetails.data.containers[0].imageName}
                    showAutoEllipsis
                    copyableTooltip
                    sx={{ fontSize: ds.text.small, ...valueTextSx }}
                  />
                </Row>
              )}
              {podDetails?.data?.qosClass && (
                <Row label='QOS class'>
                  <Text value={podDetails.data.qosClass} secondaryText sx={valueTextSx} />
                </Row>
              )}
              {investigateDescription.containerId && (
                <Row label='Container id'>
                  <Text
                    value={investigateDescription.containerId}
                    copyableTooltip
                    showAutoEllipsis
                    sx={{ fontSize: ds.text.small, ...valueTextSx }}
                  />
                </Row>
              )}
              {investigateDescription.failureCount && (
                <Row label='Failure count'>
                  <Text value={investigateDescription.failureCount} secondaryText sx={valueTextSx} />
                </Row>
              )}
              {isCloud && row?.description && (
                <Row label='Description'>
                  <Text value={investigateDescription.logSample} showAutoEllipsis sx={{ fontSize: ds.text.small }} />
                </Row>
              )}
            </Box>
          )}

          {showLabels && (
            <Box sx={{ display: 'flex', flexDirection: 'column', gap: ds.space[2] }}>
              <Box sx={{ display: 'flex', flexWrap: 'wrap', gap: ds.space[1] }}>{visibleLabels}</Box>
              {labels.length > 5 && (
                <Box>
                  <Button tone='link' size='xs' onClick={toggleShow} data-testid='toggle-labels-btn'>
                    Show {showAll ? 'Less' : `More (${labels.length - 5})`}
                  </Button>
                </Box>
              )}
            </Box>
          )}

          {showInternal && <Box sx={{ display: 'flex', flexWrap: 'wrap', gap: ds.space[1] }}>{internalLabelChips}</Box>}
        </Section>
      </Box>
    </Box>
  );
}

InvestigateSidebar.propTypes = {
  row: PropTypes.object,
  podDetails: PropTypes.object,
  othersData: PropTypes.array,
  alertLabels: PropTypes.object,
  alertRules: PropTypes.array,
  queryParam: PropTypes.object,
  isK8s: PropTypes.bool,
  sourceKnown: PropTypes.bool,
  isCloud: PropTypes.bool,
  onPodClick: PropTypes.func,
  onRowChange: PropTypes.func,
  onCreateTicket: PropTypes.func,
  onResetState: PropTypes.func,
  collapsed: PropTypes.bool,
  onToggleCollapse: PropTypes.func,
};

export default InvestigateSidebar;
