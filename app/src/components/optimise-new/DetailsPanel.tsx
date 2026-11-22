import { Box, Typography, CircularProgress } from '@mui/material';
import { Divider } from '@ui/Divider';
import { useState, useEffect, useMemo, type ReactNode } from 'react';
import { useEffectiveRecommendation } from '@hooks/useEffectiveRecommendation';
import { Select as DsSelect } from '@ui/Select';
import { ds } from 'src/utils/colors';
import ArrowForwardIcon from '@mui/icons-material/ArrowForward';
import ConfirmationNumberOutlinedIcon from '@mui/icons-material/ConfirmationNumberOutlined';
import TipsAndUpdatesOutlinedIcon from '@mui/icons-material/TipsAndUpdatesOutlined';
import InfoOutlinedIcon from '@mui/icons-material/InfoOutlined';
import SettingsOutlinedIcon from '@mui/icons-material/SettingsOutlined';
import ShieldOutlinedIcon from '@mui/icons-material/ShieldOutlined';
import SystemUpdateAltOutlinedIcon from '@mui/icons-material/SystemUpdateAlt';
import BuildOutlinedIcon from '@mui/icons-material/BuildOutlined';
import ExpandMoreIcon from '@mui/icons-material/ExpandMore';
import TicketLink from '@shared/links/TicketLink';
import PRLink, { resolutionsDeepLink, hasRenderablePRState } from '@shared/links/PRLink';
import MarkDowns from '@shared/viewers/MarkDowns';
import { Label } from '@ui/Label';
import { Button as DsButton } from '@ui/Button';
import { Card } from '@ui/Card';
import { CollapsableCard } from '@ui/CollapsableCard';
import CustomTable from '@shared/tables/CustomTable';
import recommendationApi from '@api1/recommendation';
import { interpolateMitigations } from '@api1/recommendation/data';
import { formatRuleName } from './utils';
import {
  presentRecommendation,
  coverageLabel,
  getImpactSummary,
  dependentRoleLabel,
  provenanceLabel,
  isProdEnvironment,
  prodChipState,
  dependentCountNoun,
  DEPENDENT_COUNT_HELP,
  nodeTypeLabel,
  impactSignalSources,
  getChangeClass,
  deriveVerdict,
  criticalityTone,
  criticalityLabel,
  businessCriticalCount,
  observedAgeLabel,
  OBSERVED_AGE_HELP,
  destructiveKindOf,
  CRITICALITY_HELP,
  coverageSubtitle,
  coveragePhrase,
  coverageExplainer,
  coverageIsCaveat,
  COVERAGE_HELP,
  PROD_CHIP_HELP,
  ENV_UNKNOWN_HELP,
  hasBlastRadiusData,
  SAFETY_BAND_COPY,
  commonTraits,
  sharedTraitText,
  quietSafetyReason,
  sortByRisk,
  neighbourhoodSummary,
  neighbourhoodStartsOpen,
  safetyChipFor,
  type DependentRef,
  type ImpactSummary,
  type CommonTraits,
} from './safetyBand';
import { Banner } from '@ui/Banner';
import DsTooltip from '@ui/Tooltip';
import ApplyMitigationModal, { stripOptionalMarkers } from '@components/cloudaccount/ApplyMitigationModal';
import OwnershipSection from './OwnershipSection';
import { hasWriteAccess } from '@lib/auth';
import InterpretationPanel from './interpretation/InterpretationPanel';
import { buildInterpretation } from './interpretation/buildInterpretation';
import { splitSavings, extractContainerData, summarizeCloudRightSizing, formatCloudTargetSpec } from './rightSizingData';
import { ResourceChangeCell } from './ResourceChangeCell';
import ConfigIssuesList, { summarizeConfigIssues, LEVEL_TONE, LEVEL_NAME } from './evidence/configIssues';
import UsageTrendMini from './UsageTrendMini';
import { buildTrendSeries } from './evidence/trendSeries';
import { Link as DsLink } from '@ui/Link';
import type { RightSizingTrend } from '@hooks/useRightSizingTrend';

interface DetailsPanelProps {
  fullRecommendation: any;
  /** Pod right-sizing usage trend, fetched once by the drawer; drawn compact under the change. */
  trend?: RightSizingTrend;
  accounts?: Record<string, { name: string; cloud_provider: string; account_access?: string }>;
  /** Switches the parent panel to the Evidence tab (usage-trend chart/table). */
  onViewEvidence?: () => void;
  /** Fired after a mitigation command run completes, so the History tab's
   * CommandExecutionHistory (kept mounted alongside this tab) can refresh. */
  onMitigationExecuted?: () => void;
}

const RESOLVED_STATUSES = new Set(['Closed', 'Dismissed', 'Archive']);

// Shared section heading — one mid-tier type treatment for every Details section
// ("Recommendations", "Remediation Steps", "Metadata", …). Sits clearly below the
// 20px panel title and above the 11px slot eyebrows. Optional right-aligned action.
const SectionHeading = ({ children, action }: { children: ReactNode; action?: ReactNode }) => (
  <Box sx={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', gap: ds.space[2], mb: ds.space[3] }}>
    <Typography sx={{ fontSize: ds.text.bodyLg, fontWeight: ds.weight.semibold, color: ds.gray[700], letterSpacing: '-0.01em' }}>
      {children}
    </Typography>
    {action}
  </Box>
);

// How many dependents to show per list before collapsing behind a "Show more"
// toggle — the backend caps each list at 50, too many to render inline.
const DEP_COLLAPSE_LIMIT = 3;

const BLAST_RADIUS_HELP =
  'What else could be affected if you apply this change — the workloads that depend on this resource, and how confident we are in that picture.';

const IMPACTED_HELP = 'Workloads that depend on this resource directly. Production and business-critical ones are listed first.';

// Wraps a chip so the tooltip gets a ref-holding element (Label doesn't forward refs).
const ChipTip = ({ title, children }: { title: string; children: ReactNode }) => (
  <DsTooltip title={title} placement='bottom'>
    <Box component='span' sx={{ display: 'inline-flex' }}>
      {children}
    </Box>
  </DsTooltip>
);

// Note — a quiet supporting line inside the safety card: the coverage reading,
// the apply caveat. Weighted below the summary so the card has one voice.
const Note = ({ children, 'data-testid': testId }: { children: ReactNode; 'data-testid'?: string }) => (
  <Typography data-testid={testId} sx={{ fontSize: ds.text.small, color: ds.gray[500], lineHeight: 1.6 }}>
    {children}
  </Typography>
);

// Term — one phrase of the safety summary line, carrying its explainer on hover.
// Typography forwards refs, so it takes the Tooltip directly.
const Term = ({ title, desc, color, children }: { title: string; desc: string; color?: string; children: ReactNode }) => (
  <DsTooltip variant='explainer' title={title} desc={desc}>
    <Typography
      component='span'
      sx={{
        fontSize: ds.text.small,
        color: color || ds.gray[700],
        fontWeight: ds.weight.medium,
        cursor: 'help',
        textDecoration: 'underline dotted',
        textDecorationColor: ds.gray[400],
        textUnderlineOffset: '3px',
        whiteSpace: 'nowrap',
      }}
    >
      {children}
    </Typography>
  </DsTooltip>
);

const ShowMoreToggle = ({ hidden, open, onToggle }: { hidden: number; open: boolean; onToggle: () => void }) => (
  <Typography
    onClick={onToggle}
    sx={{
      fontSize: ds.text.small,
      color: ds.blue[600],
      fontWeight: ds.weight.medium,
      cursor: 'pointer',
      mt: ds.space[1],
      '&:hover': { textDecoration: 'underline' },
    }}
  >
    {open ? 'Show less' : `Show more (${hidden})`}
  </Typography>
);

// DependentRow — one blast-radius entry on a single line: identity, then only
// the chips that set this row apart. A trait matching the list's common value
// (see commonTraits) is stated once in the group heading instead, and the
// environment shows only when it is production: the summary line above already
// counts the rest.
const DependentRow = ({
  dep,
  direction,
  computedAt,
  common,
}: {
  dep: DependentRef;
  direction: 'upstream' | 'downstream';
  computedAt?: string;
  common: CommonTraits;
}) => {
  const observedAge = direction === 'upstream' ? observedAgeLabel(dep, computedAt) : null;
  const id = dep.namespace ? `${dep.namespace}/${dep.name}` : dep.name;
  // `differs` keeps a trait only where this row departs from the list's norm.
  const differs = (label: string | null, shared: string | null) => (label && label !== shared ? label : null);
  const kind = differs(nodeTypeLabel(dep.node_type), common.nodeType);
  const role = differs(dependentRoleLabel(dep.relationship, direction), common.role);
  const provenance = differs(provenanceLabel(dep.sources), common.provenance);
  const criticality = criticalityLabel(dep.criticality);
  return (
    <Box
      sx={{
        border: `1px solid ${ds.gray[200]}`,
        borderRadius: ds.radius.md,
        px: ds.space[2],
        py: ds.space[1],
        display: 'flex',
        alignItems: 'center',
        gap: ds.space[2],
        minWidth: 0,
      }}
    >
      <Typography
        title={id}
        sx={{
          flex: 1,
          minWidth: 0,
          fontFamily: 'var(--ds-font-mono)',
          fontSize: ds.text.small,
          color: ds.gray[700],
          overflow: 'hidden',
          textOverflow: 'ellipsis',
          whiteSpace: 'nowrap',
        }}
      >
        {dep.namespace && <Box component='span' sx={{ color: ds.gray[500] }}>{`${dep.namespace}/`}</Box>}
        <Box component='span' sx={{ fontWeight: ds.weight.semibold }}>
          {dep.name}
        </Box>
      </Typography>
      <Box
        sx={{ display: 'flex', alignItems: 'center', justifyContent: 'flex-end', flexWrap: 'wrap', gap: ds.space[1], flexShrink: 0, maxWidth: '60%' }}
      >
        {kind && (
          <Label size='sm' tone='neutral'>
            {kind}
          </Label>
        )}
        {role && (
          <Label size='sm' tone={direction === 'upstream' ? 'info' : 'neutral'}>
            {role}
          </Label>
        )}
        {provenance && (
          <ChipTip title={`Source: ${(dep.sources || []).join(', ')}`}>
            <Label size='sm' tone='neutral'>
              {provenance}
            </Label>
          </ChipTip>
        )}
        {isProdEnvironment(dep.environment) && (
          <Label size='sm' tone='critical'>
            Production
          </Label>
        )}
        {observedAge && (
          <ChipTip title={OBSERVED_AGE_HELP}>
            <Label size='sm' tone='warning'>
              {observedAge}
            </Label>
          </ChipTip>
        )}
        {criticality && (
          <ChipTip title={CRITICALITY_HELP}>
            <Label size='sm' tone={criticalityTone(dep.criticality)}>
              {criticality}
            </Label>
          </ChipTip>
        )}
        {(dep.pod_count ?? 0) > 0 && (
          <Label size='sm' tone='neutral'>
            {`${dep.pod_count} pod${dep.pod_count === 1 ? '' : 's'} here`}
          </Label>
        )}
      </Box>
    </Box>
  );
};

// DependentGroup — a titled, collapsible list of DependentRows, most-at-risk
// first. Every neighbourhood list (impacted workloads, depends-on, hosted
// workloads, attached infrastructure) renders through it.
const DependentGroup = ({
  title,
  tooltip,
  deps,
  direction,
  computedAt,
}: {
  title: string;
  tooltip: string;
  deps: DependentRef[];
  direction: 'upstream' | 'downstream';
  computedAt?: string;
}) => {
  const [showAll, setShowAll] = useState(false);
  if (deps.length === 0) return null;
  const sorted = sortByRisk(deps);
  const common = commonTraits(deps, direction);
  const shared = sharedTraitText(deps, direction);
  return (
    <Box sx={{ display: 'flex', flexDirection: 'column', gap: ds.space[1] }}>
      <Box sx={{ display: 'flex', alignItems: 'center', flexWrap: 'wrap', gap: ds.space[2] }}>
        <DsTooltip title={tooltip} placement='bottom'>
          <Typography sx={{ fontSize: ds.text.small, color: ds.gray[500], fontWeight: ds.weight.medium }}>{title}</Typography>
        </DsTooltip>
        {shared && <Typography sx={{ fontSize: ds.text.caption, color: ds.gray[400] }}>{shared}</Typography>}
      </Box>
      {(showAll ? sorted : sorted.slice(0, DEP_COLLAPSE_LIMIT)).map((dep, i) => (
        <DependentRow key={`${dep.namespace || ''}/${dep.name}-${i}`} dep={dep} direction={direction} computedAt={computedAt} common={common} />
      ))}
      {deps.length > DEP_COLLAPSE_LIMIT && (
        <ShowMoreToggle hidden={deps.length - DEP_COLLAPSE_LIMIT} open={showAll} onToggle={() => setShowAll((v) => !v)} />
      )}
    </Box>
  );
};

// SafetySummary — the one line under the banner that carries every count the
// card used to spread over three label rows: change class, dependents,
// production and business-critical dependents, graph coverage. Each phrase
// keeps its explainer on hover.
const SafetySummary = ({ impact }: { impact: ImpactSummary | null }) => {
  const count = impact?.dependent_count;
  const empty = count === 0;
  // With nothing depending on the resource there is no population to be in
  // production or business-critical, so those phrases would restate the zero.
  const prodState = empty ? null : prodChipState(impact);
  const critical = empty ? 0 : businessCriticalCount(impact?.dependents);
  const signalSources = impactSignalSources(impact);
  const parts: ReactNode[] = [];
  if (count != null) {
    // "dependents", not "dependent services" — the list heading right below
    // names what they are, and the shorter phrase keeps this to one line. The
    // pods-vs-services distinction lives on in the tooltip title.
    parts.push(
      <DsTooltip key='count' variant='explainer' title={dependentCountNoun(impact?.dependents)} desc={DEPENDENT_COUNT_HELP}>
        <Typography component='span' sx={{ fontSize: ds.text.small, color: ds.gray[700], fontWeight: ds.weight.semibold, cursor: 'help' }}>
          {empty ? 'No dependents found' : `${count}${impact?.truncated ? '+' : ''} dependent${count === 1 ? '' : 's'}`}
        </Typography>
      </DsTooltip>
    );
  }
  if (prodState === 'prod') {
    parts.push(
      <Term key='prod' title='Production dependents' desc={PROD_CHIP_HELP} color={ds.red[600]}>
        {`${impact!.production_dependents} production`}
      </Term>
    );
  } else if (prodState === 'verified-zero') {
    parts.push(
      <Term key='prod' title='Production dependents' desc={PROD_CHIP_HELP} color={ds.green[600]}>
        none in production
      </Term>
    );
  } else if (prodState === 'unknown') {
    parts.push(
      <Term key='prod' title='Environment unknown' desc={ENV_UNKNOWN_HELP} color={ds.gray[500]}>
        environment unknown
      </Term>
    );
  }
  if (critical > 0) {
    parts.push(
      <Term key='critical' title='Business-critical dependents' desc={CRITICALITY_HELP} color={ds.red[600]}>
        {`${critical} business-critical`}
      </Term>
    );
  }
  if (impact?.coverage_confidence) {
    // The reading goes on the line; what it is based on goes in the tooltip.
    // Spelled out inline, it wrapped the summary onto a second line for the
    // sake of a detail nobody reads twice.
    const subtitle = coverageSubtitle(impact.coverage_confidence);
    const signals = signalSources.length > 0 ? ` Signals seen here: ${signalSources.join(', ')}.` : '';
    parts.push(
      <Term
        key='coverage'
        title={`Graph coverage: ${coverageLabel(impact.coverage_confidence)}${subtitle ? ` — ${subtitle}` : ''}`}
        desc={`${COVERAGE_HELP}${signals}`}
      >
        {coveragePhrase(impact.coverage_confidence)}
      </Term>
    );
  }
  if (parts.length === 0) return null;
  // One wrapping line of running text. The separator trails its phrase rather
  // than leading the next one, so a wrapped line never opens with a stray dot.
  return (
    <Typography data-testid='blast-radius-summary' component='p' sx={{ fontSize: ds.text.small, lineHeight: 1.7 }}>
      {parts.map((part, i) => (
        <Box key={i} component='span'>
          {part}
          {i < parts.length - 1 && (
            <Box component='span' sx={{ color: ds.gray[400], mx: ds.space[2] }}>
              ·
            </Box>
          )}
        </Box>
      ))}
    </Typography>
  );
};

// BlastRadiusSection surfaces the knowledge-graph safety band + impact rollup.
// Renders nothing until a recommendation carries the data (k8s recs once impact
// scoring is enabled), so it's a no-op for everything else. `footnote` is the
// rule's apply caveat (rolling restart, irreversibility), shown here rather
// than in the interpretation panel so risk has one home when this card exists.
const BlastRadiusSection = ({ rec, footnote }: { rec: any; footnote?: string }) => {
  const band = rec?.safety_band as string | undefined;
  const impact = getImpactSummary(rec);
  const changeClass = getChangeClass(rec);
  const safety = presentRecommendation(rec);
  const downstream = impact?.downstream_dependencies || [];
  const hostedWorkloads = impact?.hosted_workloads || [];
  const attachedInfrastructure = impact?.infrastructure_dependents || [];
  const neighbourhood = neighbourhoodSummary(impact);
  const [neighbourhoodOpen, setNeighbourhoodOpen] = useState(neighbourhoodStartsOpen(changeClass, impact));
  const explainer = coverageExplainer(impact?.coverage_confidence, impact?.dependent_count);
  if (!hasBlastRadiusData(rec)) return null;
  const verdict = deriveVerdict(band, impact?.production_dependents, impact?.dependent_count, impact?.truncated, changeClass, destructiveKindOf(rec));
  const quietReason = quietSafetyReason(impact, verdict.tone, changeClass);
  return (
    <Card
      elevation='flat'
      size='sm'
      data-testid='blast-radius-safety'
      header={
        <Box sx={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', gap: ds.space[2] }}>
          <Box sx={{ display: 'flex', alignItems: 'center', gap: ds.space[2] }}>
            <ShieldOutlinedIcon sx={{ fontSize: '18px', color: ds.gray[500] }} />
            <DsTooltip variant='explainer' title='Blast Radius & Safety' desc={BLAST_RADIUS_HELP}>
              <Typography
                component='span'
                sx={{
                  fontFamily: 'var(--ds-font-display)',
                  fontSize: ds.text.body,
                  fontWeight: ds.weight.semibold,
                  color: ds.gray[700],
                  cursor: 'help',
                }}
              >
                Blast Radius &amp; Safety
              </Typography>
            </DsTooltip>
          </Box>
        </Box>
      }
    >
      <Box sx={{ display: 'flex', flexDirection: 'column', gap: ds.space[3] }}>
        {verdict.tone !== 'success' && (
          <Banner surface='section' tone={verdict.tone} title={verdict.title} message={SAFETY_BAND_COPY[safety.key].action} />
        )}
        <SafetySummary impact={impact} />
        {quietReason && <Note>{quietReason}</Note>}
        <DependentGroup
          title='Impacted workloads'
          tooltip={IMPACTED_HELP}
          deps={impact?.dependents || []}
          direction='upstream'
          computedAt={impact?.computed_at}
        />
        {explainer &&
          // Limited coverage is a caveat and keeps its banner. A well-observed
          // graph reporting nothing is just the reading behind the summary
          // line, so it reads as a note rather than an alert.
          (coverageIsCaveat(impact?.coverage_confidence) ? (
            <Banner surface='section' tone='warning' title={`Why coverage is ${coverageLabel(impact?.coverage_confidence)}`} message={explainer} />
          ) : (
            <Note>{explainer}</Note>
          ))}
        {footnote && <Note data-testid='blast-radius-apply-note'>{footnote}</Note>}
        {neighbourhood && (
          <Box sx={{ borderTop: `1px solid ${ds.gray[200]}`, pt: ds.space[2], display: 'flex', flexDirection: 'column', gap: ds.space[2] }}>
            <Box sx={{ alignSelf: 'flex-start' }}>
              <DsButton
                tone='ghost'
                size='sm'
                icon={<ExpandMoreIcon sx={{ transform: neighbourhoodOpen ? 'rotate(180deg)' : 'none', transition: 'transform 120ms' }} />}
                onClick={() => setNeighbourhoodOpen((v) => !v)}
                aria-expanded={neighbourhoodOpen}
                id='blast-radius-neighbourhood-toggle'
              >
                {neighbourhood}
              </DsButton>
            </Box>
            {neighbourhoodOpen && (
              <Box data-testid='blast-radius-neighbourhood' sx={{ display: 'flex', flexDirection: 'column', gap: ds.space[3] }}>
                <DependentGroup
                  title='Depends on'
                  tooltip='What this resource itself calls, publishes to, or subscribes to. Context only — these are not at risk from the change and do not affect the safety band.'
                  deps={downstream}
                  direction='downstream'
                />
                <DependentGroup
                  title='Runs on this machine'
                  tooltip='Workloads currently scheduled on this instance, rolled up from live pod placement. They reschedule if the machine changes — reported separately because they are not callers that break, but an irreversible change stays Risky while anything is still running here.'
                  deps={hostedWorkloads}
                  direction='upstream'
                />
                <DependentGroup
                  title='Attached infrastructure'
                  tooltip='Infrastructure directly attached to this resource (e.g. the instance a volume backs). Not counted as dependent services, but an irreversible change stays Risky while anything is still attached.'
                  deps={attachedInfrastructure}
                  direction='upstream'
                />
              </Box>
            )}
          </Box>
        )}
      </Box>
    </Card>
  );
};

const DetailsPanel = ({ fullRecommendation: rec, trend, accounts = {}, onViewEvidence, onMitigationExecuted }: DetailsPanelProps) => {
  const [details, setDetails] = useState<any>(null);
  const [loading, setLoading] = useState(true);

  const category = rec?.category || '';
  const ruleName = rec?.rule_name || '';
  const accountEntry = accounts[rec?.account_id];
  const accountName = accountEntry?.name || '';
  const cloudProvider = accountEntry?.cloud_provider || '';

  const isActionableStatus = !rec?.status || !RESOLVED_STATUSES.has(rec.status);
  const canExecuteCommand =
    accountEntry?.account_access !== 'readonly' &&
    hasWriteAccess(rec?.account_id) &&
    isActionableStatus &&
    ['aws', 'azure', 'gcp'].includes(cloudProvider.toLowerCase());

  const { alternateOptions, selectedAlternateType, setSelectedAlternateType, effectiveRecommendation } = useEffectiveRecommendation(rec);

  useEffect(() => {
    if (!category || !ruleName) {
      setLoading(false);
      return;
    }
    setLoading(true);
    const result = recommendationApi.getRecommendationDetails(category, ruleName);
    setDetails(result);
    setLoading(false);
  }, [category, ruleName]);

  if (!rec) return null;

  if (loading) {
    return (
      <Box sx={{ display: 'flex', justifyContent: 'center', py: ds.space.mul(0, 20) }}>
        <CircularProgress size={24} />
      </Box>
    );
  }

  const mitigations = interpolateMitigations(details?.mitigations, effectiveRecommendation);
  const recData = effectiveRecommendation.recommendation;
  const fallback = extractFallbackContent(recData, effectiveRecommendation);

  // Resolved values — catalog wins, fallback fills the gaps
  const title = details?.title || fallback.title || formatRuleName(ruleName, category);
  const serviceName = details?.serviceName || fallback.serviceName || '';
  const description = details?.description || fallback.description || '';
  const remediation = fallback.remediation || '';
  const remediationUrl = fallback.remediationUrl || '';
  const insight = getRecommendationInsight(category, ruleName, recData, rec);
  const interpretation = buildInterpretation({
    category,
    ruleName,
    title,
    description,
    serviceName,
    insight,
    savings: rec.estimated_savings || 0,
    recData,
    recommendations: details?.recommendations,
    workload: { kind: rec.cloud_resourse?.meta?.controllerKind, pods: rec.cloud_resourse?.meta?.total_pods ?? null },
  });
  // The rule's apply caveat has one home: the Blast Radius card when the
  // recommendation carries one, the interpretation panel otherwise.
  const showBlastRadius = hasBlastRadiusData(rec);
  // The change card carries the one link to the Evidence tab when it renders;
  // otherwise the interpretation panel's usage strip keeps its own.
  const hasRightSizingChange = ruleName === 'pod_right_sizing' && extractContainerData(recData).length > 0;

  return (
    <Box sx={{ p: ds.space[5], display: 'flex', flexDirection: 'column', gap: ds.space[5] }}>
      {/* Interpretation — verdict / why / impact / risk, re-framed from the
          resolved title, description and insight (replaces the generic blurb). */}
      <InterpretationPanel
        onViewEvidence={hasRightSizingChange ? undefined : onViewEvidence}
        {...interpretation}
        safety={safetyChipFor(rec)}
        risk={showBlastRadius ? undefined : interpretation.risk}
      />

      {/* What applying does — surfaced prominently so the impact is clear before the
          user clicks Apply (e.g. the async, non-disruptive EBS gp2→gp3 modification). */}
      {recData?.apply_impact && (
        <Banner surface='section' tone='info' title='What applying does' message={String(recData.apply_impact)} id='recommendation-apply-impact' />
      )}

      {/* Recommendation Summary — key "what changes" data from JSONB */}
      <RecommendationSummary recData={recData} category={category} ruleName={ruleName} trend={trend} onViewEvidence={onViewEvidence} />

      {/* Blast Radius & Safety — knowledge-graph impact + safety band. Keyed by
          rec id so the "Show all" toggle state resets when the persistent detail
          drawer swaps to a different recommendation (it isn't remounted). */}
      <BlastRadiusSection key={rec?.id} rec={rec} footnote={interpretation.risk} />

      {/* Ownership — who is accountable for this resource, and how that was
          derived. Sits after the change and its blast radius: the reader came
          for what changes, not for who owns it. Keyed like Blast Radius above
          (the drawer is reused, not remounted), but the key MUST stay distinct
          from its sibling's: two siblings sharing one key makes React strand the
          old subtree instead of replacing it, so a card accumulates on every
          recommendation switch. */}
      <OwnershipSection key={`ownership-${rec?.id}`} rec={rec} />

      {/* Remediation Steps — from catalog mitigations (interpolated) */}
      {mitigations && mitigations.length > 0 && (
        <SummaryCard
          title='Remediation Steps'
          icon={<BuildOutlinedIcon sx={{ fontSize: '18px', color: ds.gray[500] }} />}
          data-testid='remediation-steps'
          action={
            <ApplyMitigationModal
              markdowns={mitigations.join('\n\n')}
              accountId={rec?.account_id}
              recommendationId={rec?.id}
              canExecute={canExecuteCommand}
              onExecuted={onMitigationExecuted}
            />
          }
        >
          {alternateOptions.length > 0 && (
            <Box sx={{ mb: ds.space[3], maxWidth: 360 }}>
              <DsSelect
                id='details-alternate-instance-type-selector'
                label='Target Instance Type'
                options={alternateOptions}
                value={selectedAlternateType}
                onChange={(v) => setSelectedAlternateType(v)}
                clearable={false}
              />
            </Box>
          )}
          <Box
            sx={{
              '& pre': { fontSize: ds.text.caption, whiteSpace: 'pre-wrap', wordBreak: 'break-all' },
              '& code': { fontSize: ds.text.caption },
            }}
          >
            {mitigations.map((step: string) => (
              <MarkDowns
                key={step.substring(0, 60)}
                data={stripOptionalMarkers(step)}
                sx={{ fontSize: ds.text.small, lineHeight: 1.6, color: ds.gray[500] }}
                allowExecutable={undefined}
                onLinkClick={undefined}
              />
            ))}
          </Box>
        </SummaryCard>
      )}

      {/* Resource-level remediation — JSONB fallback (only when no catalog mitigations) */}
      {(!mitigations || mitigations.length === 0) && remediation && (
        <SummaryCard title='Remediation' icon={<BuildOutlinedIcon sx={{ fontSize: '18px', color: ds.gray[500] }} />} data-testid='remediation'>
          <Typography sx={{ fontSize: ds.text.small, color: ds.gray[700], lineHeight: 1.6 }}>{remediation}</Typography>
          {remediationUrl && (
            <Box
              component='a'
              href={remediationUrl}
              target='_blank'
              rel='noopener noreferrer'
              sx={{
                fontSize: ds.text.small,
                color: ds.blue[600],
                display: 'block',
                mt: ds.space[2],
                textDecoration: 'none',
                '&:hover': { textDecoration: 'underline' },
              }}
            >
              View remediation guide →
            </Box>
          )}
        </SummaryCard>
      )}

      {/* Linked Items */}
      {(rec.ticket || hasRenderablePRState(rec.resolution)) && (
        <>
          <Divider sx={{ my: 0 }} />
          <Box>
            <SectionHeading>Linked Items</SectionHeading>
            <Box sx={{ display: 'flex', flexDirection: 'column', gap: ds.space[2] }}>
              {rec.ticket && (
                <Box
                  sx={{
                    p: `${ds.space[2]} ${ds.space[3]}`,
                    borderRadius: ds.radius.lg,
                    backgroundColor: ds.gray[100],
                    border: `1px solid ${ds.gray[200]}`,
                    display: 'flex',
                    alignItems: 'center',
                    gap: ds.space[2],
                  }}
                >
                  <ConfirmationNumberOutlinedIcon sx={{ fontSize: ds.text.title, color: ds.blue[600] }} />
                  <TicketLink ticketURL={rec.ticket?.url} ticketID={rec.ticket?.ticket_id} />
                </Box>
              )}
              {hasRenderablePRState(rec.resolution) && (
                <Box
                  sx={{
                    p: `${ds.space[2]} ${ds.space[3]}`,
                    borderRadius: ds.radius.lg,
                    // A resolution without a url is a pending or failed PR attempt; the
                    // chip carries its own tone, so keep the container neutral there.
                    backgroundColor: rec.resolution.type_reference_id ? ds.green[100] : ds.gray[100],
                    border: `1px solid ${rec.resolution.type_reference_id ? ds.green[200] : ds.gray[200]}`,
                    display: 'flex',
                    alignItems: 'center',
                    gap: ds.space[2],
                  }}
                >
                  <PRLink
                    prURL={rec.resolution.type_reference_id}
                    statusMessage={rec.resolution.status_message}
                    status={rec.resolution.status}
                    resolutionHref={resolutionsDeepLink(rec.id)}
                  />
                </Box>
              )}
            </Box>
          </Box>
        </>
      )}

      {/* Why this matters — the rationale, the catalog's further steps,
          compliance and references, folded behind one disclosure. They were the
          tallest part of the tab and the least often read; the verdict above
          already says what to do, and the chart says why. */}
      {(interpretation.whyNow || details?.recommendations?.length > 1 || details?.compliances?.length > 0 || details?.references?.length > 0) && (
        <Box data-testid='recommendation-rationale'>
          <CollapsableCard
            defaultOpen={false}
            elevation='flat'
            header={
              <Box sx={{ display: 'flex', alignItems: 'baseline', gap: ds.space[2], flexWrap: 'wrap' }}>
                <Typography
                  sx={{ fontFamily: 'var(--ds-font-display)', fontSize: ds.text.body, fontWeight: ds.weight.semibold, color: ds.gray[700] }}
                >
                  Why this matters
                </Typography>
                <Typography sx={{ fontSize: ds.text.caption, color: ds.gray[500] }}>
                  {[
                    interpretation.whyNow && 'rationale',
                    details?.recommendations?.length > 1 && 'further steps',
                    details?.compliances?.length > 0 && 'compliance',
                    details?.references?.length > 0 && 'references',
                  ]
                    .filter(Boolean)
                    .join(', ')}
                </Typography>
              </Box>
            }
          >
            <Box sx={{ display: 'flex', flexDirection: 'column', gap: ds.space[4] }}>
              {interpretation.whyNow && (
                <Typography sx={{ fontSize: ds.text.small, color: ds.gray[700], lineHeight: 1.6 }}>{interpretation.whyNow}</Typography>
              )}
              {details?.recommendations?.length > 1 && (
                <>
                  <Box>
                    <SectionHeading>Further steps</SectionHeading>
                    <Box sx={{ display: 'flex', flexDirection: 'column', gap: ds.space[2] }}>
                      {details.recommendations.slice(1).map((step: string, idx: number) => (
                        <Box key={step.substring(0, 60)} sx={{ display: 'flex', gap: ds.space[2], alignItems: 'flex-start' }}>
                          <Typography
                            sx={{
                              fontSize: ds.text.small,
                              fontWeight: ds.weight.semibold,
                              color: ds.blue[600],
                              minWidth: ds.space.mul(0, 10),
                              mt: ds.space[0],
                            }}
                          >
                            {idx + 1}.
                          </Typography>
                          <Typography sx={{ fontSize: ds.text.small, color: ds.gray[500], lineHeight: 1.6 }}>{step}</Typography>
                        </Box>
                      ))}
                    </Box>
                  </Box>
                </>
              )}

              {details?.compliances?.length > 0 && (
                <>
                  <Box>
                    <SectionHeading>Compliance</SectionHeading>
                    <Box sx={{ display: 'flex', gap: ds.space[2], flexWrap: 'wrap' }}>
                      {details.compliances.map((c: string) => (
                        <Label key={c} size='sm' tone='neutral'>
                          {c}
                        </Label>
                      ))}
                    </Box>
                  </Box>
                </>
              )}

              {details?.references?.length > 0 && (
                <>
                  <Box>
                    <SectionHeading>References</SectionHeading>
                    <Box sx={{ display: 'flex', flexDirection: 'column', gap: ds.space[1] }}>
                      {details.references.map((url: string) => (
                        <Box
                          key={url}
                          component='a'
                          href={url}
                          target='_blank'
                          rel='noopener noreferrer'
                          sx={{
                            fontSize: ds.text.small,
                            color: ds.blue[600],
                            textDecoration: 'none',
                            wordBreak: 'break-all',
                            '&:hover': { textDecoration: 'underline' },
                          }}
                        >
                          {url.replace(/https?:\/\//, '').replace(/\/$/, '')}
                        </Box>
                      ))}
                    </Box>
                  </Box>
                </>
              )}
            </Box>
          </CollapsableCard>
        </Box>
      )}

      {/* Metadata */}
      <Box data-testid='recommendation-metadata'>
        <CollapsableCard
          defaultOpen={false}
          elevation='flat'
          header={
            <Box sx={{ display: 'flex', alignItems: 'center', gap: ds.space[2] }}>
              <InfoOutlinedIcon sx={{ fontSize: '18px', color: ds.gray[500] }} />
              <Typography sx={{ fontFamily: 'var(--ds-font-display)', fontSize: ds.text.body, fontWeight: ds.weight.semibold, color: ds.gray[700] }}>
                Metadata
              </Typography>
            </Box>
          }
        >
          <MetaRow label='ID' value={rec.id} mono />
          <MetaRow label='Rule' value={ruleName} />
          <MetaRow label='Account' value={accountName || rec.account_id} />
        </CollapsableCard>
      </Box>
    </Box>
  );
};

// ─── MetaRow helper ───

const MetaRow = ({ label, value, mono }: { label: string; value?: string; mono?: boolean }) => (
  <Box sx={{ display: 'flex', justifyContent: 'space-between', py: ds.space[2], gap: ds.space[3] }}>
    <Typography sx={{ fontSize: ds.text.small, color: ds.gray[500], whiteSpace: 'nowrap' }}>{label}</Typography>
    <Typography
      sx={{
        fontSize: mono ? ds.text.caption : ds.text.small,
        color: ds.gray[700],
        fontFamily: mono ? ds.font.mono : undefined,
        textAlign: 'right',
        overflow: 'hidden',
        textOverflow: 'ellipsis',
        whiteSpace: 'nowrap',
      }}
    >
      {value || '—'}
    </Typography>
  </Box>
);

const SummaryCard = ({
  title,
  icon,
  action,
  children,
  'data-testid': testId,
}: {
  title: string;
  icon?: ReactNode;
  action?: ReactNode;
  children: ReactNode;
  'data-testid'?: string;
}) => (
  <Card
    elevation='flat'
    variant='accent'
    tone='warning'
    size='sm'
    data-testid={testId}
    header={
      <Box sx={{ display: 'flex', alignItems: 'center', gap: ds.space[2] }}>
        {icon}
        <Typography sx={{ fontFamily: 'var(--ds-font-display)', fontSize: ds.text.body, fontWeight: ds.weight.semibold, color: ds.gray[700] }}>
          {title}
        </Typography>
        {action && (
          <>
            <Box sx={{ flex: 1 }} />
            {action}
          </>
        )}
      </Box>
    }
  >
    {children}
  </Card>
);

// ─── Recommendation Summary — renders key "what to change" data from JSONB ───

const K8sRightSizingSummary = ({ recData, trend, onViewEvidence }: { recData: any; trend?: RightSizingTrend; onViewEvidence?: () => void }) => {
  const containers = extractContainerData(recData);
  const first = containers[0];
  const cpuRecommended = first?.cpu?.recommended?.request;
  const memoryRecommended = first?.memory?.recommended?.request;
  // The chart carries the recommendation: the recommended request drawn on
  // seven days of usage, right under the table that states it. Null until the
  // drawer's fetch returns rows, and never for a workload with no trend data.
  // Memoised on the rows and the two targets, since building the series
  // formats a date per row and the panel re-renders more often than they change.
  const perPod = trend?.perPod;
  const hasTrend = !!trend?.hasData;
  const trendSeries = useMemo(
    () => (hasTrend && perPod ? buildTrendSeries(perPod, cpuRecommended, memoryRecommended) : null),
    [hasTrend, perPod, cpuRecommended, memoryRecommended]
  );
  if (containers.length === 0) return null;
  // Per-container dollars, when the producers stamped the per-entry figures.
  // A single container has nothing to split: its figure is the workload's,
  // already in the panel header, and the two round differently ("+$1" against
  // "<$0.73/mo"), which reads as a discrepancy rather than a breakdown.
  const split = containers.length > 1 ? splitSavings(recData) : null;
  const dollarsByContainer = new Map(split?.containers.map((c) => [c.name, c.total]) ?? []);
  return (
    <Card
      variant='accent'
      tone='warning'
      size='sm'
      data-testid='recommended-resource-changes'
      header={
        <Box sx={{ display: 'flex', alignItems: 'center', gap: ds.space[2] }}>
          <TipsAndUpdatesOutlinedIcon sx={{ fontSize: '18px', color: ds.yellow[600] }} />
          <Typography sx={{ fontFamily: 'var(--ds-font-display)', fontSize: ds.text.body, fontWeight: ds.weight.semibold, color: ds.gray[700] }}>
            Recommended Resource Changes
          </Typography>
          <Box sx={{ flex: 1 }} />
          {containers.length > 1 && (
            <Label size='sm' tone='neutral'>
              {containers.length} containers
            </Label>
          )}
          {onViewEvidence && (
            // The Evidence tab is React-state controlled (no route), so the hash
            // href is a no-op we preventDefault on, then switch tabs.
            <DsLink
              href='#evidence'
              secondaryText
              onClick={(e) => {
                e.preventDefault();
                onViewEvidence();
              }}
              style={{ whiteSpace: 'nowrap' }}
            >
              <Box component='span' data-testid='view-full-evidence'>
                Full evidence
              </Box>
            </DsLink>
          )}
        </Box>
      }
    >
      <CustomTable
        headers={split ? ['Container', 'CPU Request', 'Memory Request', 'Monthly ($)'] : ['Container', 'CPU Request', 'Memory Request']}
        tableData={containers.map(({ containerName, cpu, memory }) => [
          {
            component: (
              <Typography sx={{ fontSize: ds.text.small, color: ds.gray[700], fontWeight: ds.weight.medium, fontFamily: ds.font.mono }}>
                {containerName}
              </Typography>
            ),
            data: containerName,
          },
          {
            component: cpu ? (
              <ResourceChangeCell current={cpu.allocated?.request} recommended={cpu.recommended?.request} isMem={false} />
            ) : (
              <Typography sx={{ fontSize: ds.text.small, color: ds.gray[500] }}>{'—'}</Typography>
            ),
          },
          {
            component: memory ? (
              <ResourceChangeCell current={memory.allocated?.request} recommended={memory.recommended?.request} isMem />
            ) : (
              <Typography sx={{ fontSize: ds.text.small, color: ds.gray[500] }}>{'—'}</Typography>
            ),
          },
          ...(split
            ? [
                {
                  component: <ContainerDollarsCell dollars={dollarsByContainer.get(containerName)} />,
                  data: dollarsByContainer.get(containerName) ?? 0,
                },
              ]
            : []),
        ])}
      />
      {trend && (
        <Box sx={{ mt: ds.space[3] }}>
          <UsageTrendMini
            series={trendSeries}
            loading={trend.loading}
            basis={trend.perPod.source ? 'per pod' : 'all replicas'}
            rangeDays={trend.rangeDays}
          />
        </Box>
      )}
    </Card>
  );
};

// Signed monthly figure for one container: green saves, red costs more, a
// dash when the row predates the per-entry stamp.
const ContainerDollarsCell = ({ dollars }: { dollars: number | undefined }) => {
  if (dollars == null) return <Typography sx={{ fontSize: ds.text.small, color: ds.gray[500] }}>{'—'}</Typography>;
  const rounded = Math.round(Math.abs(dollars));
  // Under a dollar either way is flat: no sign, neutral colour.
  const color = rounded === 0 ? ds.gray[500] : dollars > 0 ? ds.green[600] : ds.red[600];
  const sign = rounded === 0 ? '' : dollars > 0 ? '+' : '−';
  return (
    <Typography sx={{ fontSize: ds.text.small, fontWeight: ds.weight.medium, color, whiteSpace: 'nowrap' }}>
      {sign}${rounded}
    </Typography>
  );
};

const CloudRightSizingSummary = ({ recData }: { recData: any }) => {
  const s = summarizeCloudRightSizing(recData);
  if (!s) return null;
  const currentPrice = recData.current_price;
  const recommendedPrice = recData.recommended_price ?? s.targetPrice;
  const targetSpec = formatCloudTargetSpec(s);
  return (
    <SummaryCard
      title='Recommended Changes'
      icon={<TipsAndUpdatesOutlinedIcon sx={{ fontSize: '18px', color: ds.yellow[600] }} />}
      data-testid='cloud-recommended-changes'
    >
      <Box>
        {/* Instance-type comparison (alternate/generation) or single recommended target (underutilized). */}
        {(s.currentInstance || s.targetInstance) && (
          <Box
            sx={{
              display: 'flex',
              alignItems: 'center',
              gap: ds.space[4],
              p: ds.space[3],
              backgroundColor: ds.blue[100],
              borderRadius: ds.radius.lg,
              border: `1px solid ${ds.blue[200]}`,
              mb: ds.space[2],
              justifyContent: 'center',
            }}
          >
            {s.currentInstance && <InstanceBadge label='Current' value={s.currentInstance} variant='error' />}
            {s.currentInstance && s.targetInstance && <ArrowForwardIcon sx={{ fontSize: '18px', color: ds.blue[700] }} />}
            {s.targetInstance && <InstanceBadge label='Recommended' value={s.targetInstance} variant='success' />}
          </Box>
        )}
        {targetSpec && <SummaryRow label='Target Spec' value={targetSpec} highlight />}
        {currentPrice != null && recommendedPrice != null ? (
          <Box
            sx={{
              display: 'flex',
              alignItems: 'center',
              gap: ds.space[4],
              p: '10px',
              mt: ds.space[2],
              backgroundColor: ds.green[100],
              borderRadius: ds.radius.lg,
              border: `1px solid ${ds.green[200]}`,
              justifyContent: 'center',
            }}
          >
            <Box sx={{ textAlign: 'center' }}>
              <Typography sx={{ fontSize: ds.text.caption, color: ds.gray[500] }}>Current</Typography>
              <Typography sx={{ fontSize: ds.text.bodyLg, fontWeight: ds.weight.semibold, color: ds.red[600] }}>
                ${Number(currentPrice).toFixed(2)}/hr
              </Typography>
            </Box>
            <ArrowForwardIcon sx={{ fontSize: ds.text.title, color: ds.green[600] }} />
            <Box sx={{ textAlign: 'center' }}>
              <Typography sx={{ fontSize: ds.text.caption, color: ds.gray[500] }}>Recommended</Typography>
              <Typography sx={{ fontSize: ds.text.bodyLg, fontWeight: ds.weight.semibold, color: ds.green[600] }}>
                ${Number(recommendedPrice).toFixed(2)}/hr
              </Typography>
            </Box>
          </Box>
        ) : (
          s.targetPrice != null && <SummaryRow label='On-Demand Rate (recommended)' value={`$${Number(s.targetPrice).toFixed(4)}/hr`} />
        )}
      </Box>
    </SummaryCard>
  );
};

const InfraUpgradeSummary = ({ recData }: { recData: any }) => {
  const currentVer = recData.current_version || recData.current_api_version || recData.version || recData.chartVersion || '';
  const recommendedVer = recData.recommended_version || recData.recommended_api_version || recData.replacement_api || recData.latestVersion || '';
  if (!currentVer && !recommendedVer) return null;
  return (
    <SummaryCard
      title='Version Change'
      icon={<SystemUpdateAltOutlinedIcon sx={{ fontSize: '18px', color: ds.gray[500] }} />}
      data-testid='version-change'
    >
      <Box
        sx={{
          display: 'flex',
          alignItems: 'center',
          gap: ds.space[3],
          p: ds.space[3],
          backgroundColor: ds.blue[100],
          borderRadius: ds.radius.lg,
          border: `1px solid ${ds.blue[200]}`,
          justifyContent: 'center',
        }}
      >
        {currentVer && <InstanceBadge label='Current' value={currentVer} variant='error' />}
        {currentVer && recommendedVer && <ArrowForwardIcon sx={{ fontSize: ds.text.title, color: ds.blue[700] }} />}
        {recommendedVer && <InstanceBadge label='Recommended' value={recommendedVer} variant='success' />}
      </Box>
      {recData.chartName && <SummaryRow label='Chart' value={recData.chartName} />}
      {recData.kind && <SummaryRow label='Kind' value={recData.kind} />}
      {recData.name && <SummaryRow label='Resource' value={recData.name} />}
    </SummaryCard>
  );
};

const SecuritySummary = ({ recData }: { recData: any }) => {
  const vulnId = recData.vulnerability_id || '';
  const severity = recData.severity || recData.Severity?.Label || '';
  const fixVersion = recData.fix_version || '';
  const image = recData.image || '';
  const ruleId = recData.rule_id || '';
  const ruleDescription = recData.rule_description || '';
  if (!vulnId && !severity && !ruleId && !recData.Title) return null;
  return (
    <SummaryCard title='Finding Summary' icon={<ShieldOutlinedIcon sx={{ fontSize: '18px', color: ds.gray[500] }} />} data-testid='finding-summary'>
      {vulnId && <SummaryRow label='Vulnerability' value={vulnId} />}
      {ruleId && <SummaryRow label='Rule ID' value={ruleId} />}
      {ruleDescription && <SummaryRow label='Rule' value={ruleDescription} />}
      {severity && <SummaryRow label='Severity' value={severity.toUpperCase?.()} />}
      {image && <SummaryRow label='Image' value={image} />}
      {fixVersion && <SummaryRow label='Fix Version' value={fixVersion} highlight />}
      {recData.ServiceName && <SummaryRow label='Service' value={recData.ServiceName} />}
      {recData.Compliance?.Status && <SummaryRow label='Compliance' value={recData.Compliance.Status} />}
    </SummaryCard>
  );
};

const ConfigurationSummary = ({ recData }: { recData: any }) => {
  if (Array.isArray(recData)) {
    const summary = summarizeConfigIssues(recData);
    return (
      <SummaryCard
        title={`Findings (${recData.length})`}
        icon={<InfoOutlinedIcon sx={{ fontSize: '18px', color: ds.gray[500] }} />}
        data-testid='configuration-findings'
        action={
          summary.hasLevels ? (
            <Box sx={{ display: 'flex', gap: '6px', flexWrap: 'wrap' }}>
              {[3, 2, 1].map((lv) =>
                summary.levelCounts[lv] > 0 ? (
                  <Label key={lv} size='sm' tone={LEVEL_TONE[lv]}>
                    {summary.levelCounts[lv]} {LEVEL_NAME[lv]}
                  </Label>
                ) : null
              )}
            </Box>
          ) : null
        }
      >
        <ConfigIssuesList items={recData} />
      </SummaryCard>
    );
  }
  const recommendedTags: string[] = Array.isArray(recData.recommended_tags) ? recData.recommended_tags : [];
  // service_name is intentionally excluded — it's already shown as the verdict chip.
  const hasFields = recData.alarm_type || recData.instance_type || recData.load_balancer_name || recData.region || recommendedTags.length > 0;
  if (!hasFields) return null;
  return (
    <SummaryCard
      title='Configuration Summary'
      icon={<SettingsOutlinedIcon sx={{ fontSize: '18px', color: ds.gray[500] }} />}
      data-testid='configuration-summary'
    >
      {recData.alarm_type && <SummaryRow label='Alarm Type' value={recData.alarm_type} />}
      {recData.instance_type && <SummaryRow label='Instance Type' value={recData.instance_type} />}
      {recData.load_balancer_name && <SummaryRow label='Load Balancer' value={recData.load_balancer_name} />}
      {recData.region && <SummaryRow label='Region' value={recData.region} />}
      {recommendedTags.length > 0 && (
        <Box sx={{ mt: ds.space.mul(0, 3) }}>
          <Typography sx={{ fontSize: ds.text.small, color: ds.gray[500], mb: ds.space[1] }}>Recommended Tags</Typography>
          <Box sx={{ display: 'flex', flexWrap: 'wrap', gap: ds.space[1] }}>
            {recommendedTags.map((tag: string) => (
              <Label key={tag} size='sm' tone='warning'>
                {tag}
              </Label>
            ))}
          </Box>
        </Box>
      )}
    </SummaryCard>
  );
};

// Fields hidden from the generic Summary fallback: prose already shown in the
// interpretation, plus internal routing identifiers (raw UUIDs) that are noise.
const SUMMARY_HIDDEN_FIELDS = new Set([
  'reason',
  'apply_impact', // shown prominently in its own Banner, not as a generic summary row
  'message',
  'description',
  'Description',
  'Title',
  'cloud_account_id',
  'account_id',
  'resource_id',
  'tenant_id',
  'cloud_provider',
  'source',
  'id',
]);

const RecommendationSummary = ({
  recData,
  category,
  ruleName,
  trend,
  onViewEvidence,
}: {
  recData: any;
  category: string;
  ruleName: string;
  trend?: RightSizingTrend;
  onViewEvidence?: () => void;
}) => {
  if (!recData || typeof recData !== 'object') return null;

  if (ruleName === 'pod_right_sizing') return <K8sRightSizingSummary recData={recData} trend={trend} onViewEvidence={onViewEvidence} />;
  if (category === 'RightSizing' && !Array.isArray(recData)) return <CloudRightSizingSummary recData={recData} />;
  if (category === 'InfraUpgrade' || category === 'K8sVersionUpgrade') return <InfraUpgradeSummary recData={recData} />;
  if (category === 'Security') return <SecuritySummary recData={recData} />;
  if (category === 'Configuration') return <ConfigurationSummary recData={recData} />;

  // Generic fallback: show key scalar fields from the JSONB
  const scalarEntries = Object.entries(recData)
    .filter(([, v]) => v != null && typeof v !== 'object')
    .filter(([k]) => !SUMMARY_HIDDEN_FIELDS.has(k))
    .slice(0, 6);
  if (scalarEntries.length === 0) return null;
  return (
    <SummaryCard title='Summary' icon={<InfoOutlinedIcon sx={{ fontSize: '18px', color: ds.gray[500] }} />} data-testid='recommendation-summary'>
      {scalarEntries.map(([key, value]) => (
        <SummaryRow key={key} label={key.replace(/_/g, ' ').replace(/\b\w/g, (c) => c.toUpperCase())} value={String(value)} />
      ))}
    </SummaryCard>
  );
};

// ─── Summary helpers ───

const SummaryRow = ({ label, value, highlight }: { label: string; value: string; highlight?: boolean }) => (
  <Box sx={{ display: 'flex', justifyContent: 'space-between', py: ds.space[1] }}>
    <Typography sx={{ fontSize: ds.text.small, color: ds.gray[500] }}>{label}</Typography>
    <Typography
      sx={{
        fontSize: ds.text.small,
        color: highlight ? ds.green[600] : ds.gray[700],
        fontWeight: highlight ? ds.weight.semibold : ds.weight.regular,
      }}
    >
      {value}
    </Typography>
  </Box>
);

const InstanceBadge = ({ label, value, variant }: { label: string; value: string; variant: 'success' | 'error' }) => (
  <Box sx={{ textAlign: 'center' }}>
    <Typography sx={{ fontSize: ds.text.caption, color: ds.gray[500], mb: ds.space[0] }}>{label}</Typography>
    <Box
      component='span'
      sx={{
        display: 'inline-block',
        fontFamily: ds.font.mono,
        fontSize: ds.text.small,
        fontWeight: ds.weight.semibold,
        color: variant === 'success' ? ds.green[600] : ds.red[600],
        backgroundColor: variant === 'success' ? ds.green[100] : ds.red[100],
        border: `1px solid ${variant === 'success' ? ds.green[200] : ds.red[200]}`,
        borderRadius: ds.radius.sm,
        px: ds.space[2],
        py: ds.space[0],
      }}
    >
      {value}
    </Box>
  </Box>
);

// Right-sizing container parsing is shared with the interpretation adapter.

// Memory values from the K8s collector are always in bytes
// ─── Fallback content extraction from recommendation JSONB ───

interface FallbackContent {
  title: string;
  description: string;
  serviceName: string;
  remediation: string;
  remediationUrl: string;
}

function extractFallbackContent(recData: any, rec: any): FallbackContent {
  const result: FallbackContent = { title: '', description: '', serviceName: '', remediation: '', remediationUrl: '' };
  if (!recData || typeof recData !== 'object' || Array.isArray(recData)) return result;

  result.title = recData.Title || '';
  result.description = recData.Description || recData.description?.replace(/\[b\]|\[\/b\]/g, '') || recData.reason || recData.message || '';
  if (result.description.length > 800) {
    result.description = result.description.substring(0, 800) + '...';
  }
  result.serviceName = recData.service_name || recData.ServiceName || rec.cloud_resourse?.meta?.config?.serviceName || rec.service_name || '';
  result.remediation = recData.Remediation?.Recommendation?.Text || recData.remediation || '';
  result.remediationUrl = recData.Remediation?.Recommendation?.Url || '';
  return result;
}

// ─── Pod right-sizing insight — analyzes actual resource change direction ───

interface ResourceChangeCounts {
  cpuUp: number;
  cpuDown: number;
  memUp: number;
  memDown: number;
}

const DEFAULT_POD_INSIGHT =
  "Resource requests for this workload don't match observed usage. Adjusting them to reflect actual consumption improves cluster utilization and can reduce costs.";

function countResourceChanges(containers: { containerName: string; cpu: any; memory: any }[]): ResourceChangeCounts {
  let cpuUp = 0,
    cpuDown = 0,
    memUp = 0,
    memDown = 0;
  for (const c of containers) {
    const cpuCur = c.cpu?.allocated?.request;
    const cpuRec = c.cpu?.recommended?.request;
    if (cpuCur != null && cpuRec != null && cpuCur !== cpuRec) {
      if (cpuRec > cpuCur) cpuUp++;
      else cpuDown++;
    }
    const memCur = c.memory?.allocated?.request;
    const memRec = c.memory?.recommended?.request;
    if (memCur != null && memRec != null && memCur !== memRec) {
      if (memRec > memCur) memUp++;
      else memDown++;
    }
  }
  return { cpuUp, cpuDown, memUp, memDown };
}

function describeResourceChanges({ cpuUp, cpuDown, memUp, memDown }: ResourceChangeCounts): string[] {
  const changes: string[] = [];
  if (cpuDown > 0 && cpuUp === 0) changes.push('CPU requests are higher than needed');
  else if (cpuUp > 0 && cpuDown === 0) changes.push('CPU requests are too low for observed usage');
  else if (cpuUp > 0 && cpuDown > 0) changes.push('some containers need more CPU while others need less');

  if (memDown > 0 && memUp === 0) changes.push('memory requests are higher than needed');
  else if (memUp > 0 && memDown === 0) changes.push('memory requests are too low for observed usage');
  else if (memUp > 0 && memDown > 0) changes.push('some containers need more memory while others need less');
  return changes;
}

function buildProvisioningExplanation(changes: string[], counts: ResourceChangeCounts): string {
  const { cpuUp, cpuDown, memUp, memDown } = counts;
  if (cpuUp === 0 && memUp === 0) {
    return `This workload is over-provisioned — ${changes.join(
      ' and '
    )}. Over-provisioned containers block cluster resources that other workloads could use, increasing infrastructure costs without benefit.`;
  }
  if (cpuDown === 0 && memDown === 0) {
    return `This workload is under-provisioned — ${changes.join(
      ' and '
    )}. Under-provisioned containers risk throttling, OOM kills, and degraded performance. Increasing resource requests ensures reliability and stability.`;
  }
  return `This workload has a resource mismatch — ${changes.join(
    '; '
  )}. Aligning resource requests with actual usage ensures both reliability and cost-efficiency.`;
}

function getPodRightSizingInsight(recData: any, savingsText: string): string {
  const containers = extractContainerData(recData);
  if (containers.length === 0) return `${DEFAULT_POD_INSIGHT}${savingsText}`;

  const counts = countResourceChanges(containers);
  const changes = describeResourceChanges(counts);
  if (changes.length === 0) return `${DEFAULT_POD_INSIGHT}${savingsText}`;

  const explanation = buildProvisioningExplanation(changes, counts);
  return `${explanation} Adjusting requests to match observed consumption improves cluster utilization.${savingsText}`;
}

// ─── Contextual insight — explains WHY this recommendation matters ───

const RIGHTSIZING_RULE_INSIGHTS: Record<string, string> = {
  replica_right_sizing:
    'The replica count for this workload can be optimized based on actual traffic and usage patterns. Running more replicas than needed during low-usage periods wastes compute resources.',
  pv_rightsize:
    'This persistent volume has significantly more storage allocated than what is being used. Over-provisioned storage incurs unnecessary costs and the excess capacity cannot be used by other workloads.',
  unused_pvc:
    'This persistent volume claim is not attached to any running workload. Unattached volumes still incur storage charges and should be cleaned up if they are no longer needed.',
  abandoned_resource:
    'This workload appears to be inactive — it has very low or no utilization over an extended period. Abandoned workloads consume cluster resources and incur costs without delivering value.',
};

function getRightSizingInsight(ruleName: string, recData: any, savingsText: string): string {
  if (ruleName === 'pod_right_sizing') return getPodRightSizingInsight(recData, savingsText);

  const staticInsight = RIGHTSIZING_RULE_INSIGHTS[ruleName];
  if (staticInsight) return `${staticInsight}${savingsText}`;

  if (/^aws_native_(ce_ri|ce_savings_plan|purchase_reserved|purchase_savings)/.test(ruleName)) {
    return `You have consistent on-demand usage that could benefit from a commitment-based pricing model. Purchasing Reserved Instances or Savings Plans for predictable workloads typically reduces costs by 30-60% compared to on-demand pricing.${savingsText}`;
  }

  const currentType = recData?.current_instance_type || recData?.instance_type || '';
  const recommendedType = recData?.recommended_instance_type || '';
  if (currentType && recommendedType) {
    return `Based on observed CPU, memory, and network utilization, this instance is larger than what your workload requires. Downsizing from ${currentType} to ${recommendedType} will maintain performance while reducing compute costs.${savingsText}`;
  }
  return `Based on usage analysis, this resource is not fully utilized. Right-sizing to match actual demand reduces costs while maintaining performance.${savingsText}`;
}

function getInfraUpgradeInsight(recData: any): string {
  const currentVer = recData?.current_version || recData?.current_api_version || recData?.version || '';
  const recommendedVer = recData?.recommended_version || recData?.recommended_api_version || recData?.replacement_api || recData?.latestVersion || '';
  if (currentVer && recommendedVer) {
    return `You are running version ${currentVer}, but ${recommendedVer} is available with security patches, bug fixes, and improvements. Running outdated versions may expose your infrastructure to known vulnerabilities and result in loss of vendor support.`;
  }
  return 'A newer version is available with security patches, bug fixes, and feature improvements. Running outdated versions may expose your infrastructure to known vulnerabilities and result in loss of vendor support.';
}

function getSecurityInsight(ruleName: string, recData: any): string {
  if (ruleName === 'image_scan') {
    const severity = recData?.severity || '';
    const sevText = severity ? ` This is a ${severity.toLowerCase()}-severity issue.` : '';
    return `A known vulnerability has been found in a container image running in your environment. Unpatched vulnerabilities can be exploited to gain unauthorized access, escalate privileges, or disrupt services.${sevText} Updating to a patched image version is recommended.`;
  }
  if (recData?.rule_id || recData?.rule_description || ruleName?.includes('cis')) {
    return 'A CIS Benchmark check has identified a security hardening gap. CIS Benchmarks are consensus-based security configurations developed by the Center for Internet Security and are widely adopted as industry-standard guidelines for securing IT systems.';
  }
  if (recData?.Title || recData?.Compliance) {
    return 'AWS Security Hub has identified a security finding based on automated checks against security standards like AWS Foundational Security Best Practices, CIS, or PCI DSS. Remediating this finding improves your compliance posture and reduces attack surface.';
  }
  return 'A security issue has been identified that could expose your infrastructure to risk. Addressing security findings proactively reduces your attack surface and improves your overall security posture.';
}

function getRecommendationInsight(category: string, ruleName: string, recData: any, rec: any): string {
  const savings = rec.estimated_savings || 0;
  const savingsText = Math.round(savings) >= 1 ? ` Estimated savings: ~$${savings.toFixed(0)}/month.` : '';

  // pod_right_sizing rows live under Configuration when requests are unset,
  // but the KRR-derived insight still applies
  if (ruleName === 'pod_right_sizing') return getRightSizingInsight(ruleName, recData, savingsText);
  if (category === 'RightSizing') return getRightSizingInsight(ruleName, recData, savingsText);
  if (category === 'Configuration')
    return 'A configuration best practice violation has been detected. Misconfigurations can lead to reliability issues, security gaps, or unexpected costs. Addressing this aligns your infrastructure with industry best practices and reduces operational risk.';
  if (category === 'InfraUpgrade' || category === 'K8sVersionUpgrade') return getInfraUpgradeInsight(recData);
  if (category === 'Security') return getSecurityInsight(ruleName, recData);
  if (category === 'K8sSpotRecommendation')
    return 'This workload is a good candidate for spot or preemptible instances based on its fault-tolerance characteristics. Spot instances offer the same compute capacity at significantly lower prices (typically 60-90% savings) and are suitable for stateless, restart-tolerant workloads.';
  return '';
}

export default DetailsPanel;
