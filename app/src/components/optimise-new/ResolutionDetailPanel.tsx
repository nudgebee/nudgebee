import { useEffect, useState, type ReactNode } from 'react';
import { Box, Table, TableBody, TableCell, TableContainer, TableHead, TableRow, Typography } from '@mui/material';
import CloseIcon from '@mui/icons-material/Close';
import InfoOutlinedIcon from '@mui/icons-material/InfoOutlined';
import HistoryOutlinedIcon from '@mui/icons-material/HistoryOutlined';
import OpenInNewIcon from '@mui/icons-material/OpenInNew';
import RestartAltOutlinedIcon from '@mui/icons-material/RestartAltOutlined';
import TuneOutlinedIcon from '@mui/icons-material/TuneOutlined';
import Link from 'next/link';
import CustomDrawer from '@shared/CustomDrawer';
import Tabs from '@shared/navigation/Tabs';
import Currency from '@shared/format/Currency';
import Datetime from '@shared/format/Datetime';
import Text from '@shared/format/Text';
import CommandExecutionHistory from '@components/cloudaccount/CommandExecutionHistory';
import { Banner } from '@ui/Banner';
import { Card } from '@ui/Card';
import { Button } from '@ui/Button';
import { Label, type LabelTone } from '@ui/Label';
import { SeverityIcon } from '@ui/SeverityIcon';
import { toSeverityLevel, snakeToTitleCase, safeJSONParse } from '@utils/common';
import { ds } from 'src/utils/colors';
import recommendationApi from '@api1/recommendation';
import { buildAppliedChanges, describeResolutionReference, formatDuration, formatResolutionType } from './resolutionDetail';
import { ResourceChangeCell } from './ResourceChangeCell';
import { getResourceDisplayName, panelActionBarSx } from './utils';

const STATUS_TONE: Record<string, LabelTone> = { Success: 'success', Failed: 'critical', InProgress: 'warning' };

const statusText = (status: string) => (status === 'InProgress' ? 'In Progress' : status || 'Unknown');

/**
 * What to call the applied spec, by outcome. A failed resolution changed
 * nothing — heading its target values "What changed" states the opposite of
 * what the banner directly above it says.
 */
const CHANGE_HEADING: Record<string, string> = {
  Success: 'What changed',
  Failed: 'What it would have changed',
  InProgress: 'What it is changing',
};

/** A recommendation status that means the fix actually landed and stuck. */
const CLOSED_STATUSES = new Set(['Closed', 'Archive']);

/**
 * Where a resolution's outcome and its recommendation's status disagree, in
 * words. Returns null when they are consistent, so the caller renders nothing
 * rather than an "all fine" note nobody needs.
 */
const describeStatusMismatch = (resolutionStatus: string, recommendationStatus?: string): string | null => {
  if (!recommendationStatus) return null;
  if (resolutionStatus === 'Success' && !CLOSED_STATUSES.has(recommendationStatus)) return 'applied, but not yet closed';
  if (resolutionStatus === 'Failed' && recommendationStatus === 'InProgress') return 'still marked in progress despite the failure';
  return null;
};

const Section = ({ title, children }: { title: string; children: ReactNode }) => (
  <Box sx={{ mb: ds.space[5] }}>
    {/* Quieter than the content it introduces — the numbers are the point, the
        headings are wayfinding. */}
    <Typography
      sx={{
        fontSize: ds.text.caption,
        fontWeight: ds.weight.semibold,
        color: ds.gray[500],
        mb: ds.space[2],
        textTransform: 'uppercase',
        letterSpacing: '0.06em',
      }}
    >
      {title}
    </Typography>
    {children}
  </Box>
);

const Row = ({ label, children }: { label: string; children: ReactNode }) => (
  <Box
    sx={{
      display: 'grid',
      gridTemplateColumns: 'minmax(120px, max-content) 1fr',
      columnGap: ds.space[4],
      alignItems: 'baseline',
      py: ds.space[2],
      '&:not(:last-of-type)': { borderBottom: `1px solid ${ds.gray[200]}` },
    }}
  >
    <Typography sx={{ fontSize: ds.text.small, color: ds.gray[500], fontWeight: ds.weight.medium }}>{label}</Typography>
    <Box sx={{ fontSize: ds.text.body, color: ds.gray[700], minWidth: 0 }}>{children}</Box>
  </Box>
);

interface ResolutionDetailPanelProps {
  open: boolean;
  onClose: () => void;
  /** The listing row, already in hand — the panel opens without waiting on a fetch. */
  resolution: any | null;
  accounts?: Record<string, { name: string; cloud_provider: string }>;
  onRetry: (resolutionId: string, accountId: string) => void;
  retrying: boolean;
}

/**
 * One resolution — a single attempt to apply a single recommendation.
 *
 * A panel rather than an expanding row, per the presentation rule in
 * docs/architecture-decisions.md: a group expands in place, an individual
 * record opens a drawer. It leads with the outcome, because the question a
 * reader brings here is almost always "did this work, and if not why".
 *
 * The recommendation behind the resolution is fetched lazily and only for its
 * before-state and its current status. Everything the panel needs to open is
 * already on the row, so a slow or failed fetch degrades the "what changed"
 * section rather than the panel.
 */
const ResolutionDetailPanel = ({ open, onClose, resolution, accounts, onRetry, retrying }: ResolutionDetailPanelProps) => {
  const [fullRecommendation, setFullRecommendation] = useState<any>(null);
  const [activeTab, setActiveTab] = useState(0);

  const recommendationId = resolution?.recommendation_id;
  const resolutionId = resolution?.id;

  // Reset per resolution, not per open: reopening the drawer on a different row
  // would otherwise land on a History tab the new one may not even have.
  useEffect(() => {
    setActiveTab(0);
  }, [resolutionId]);
  useEffect(() => {
    if (!open || !recommendationId) {
      setFullRecommendation(null);
      return undefined;
    }
    let active = true;
    recommendationApi
      .getK8sRecommendation({ recommendationId, status: [], limit: 1 })
      .then((result: any) => {
        if (active) setFullRecommendation(result?.data?.recommendation?.[0] || null);
      })
      .catch(() => {
        if (active) setFullRecommendation(null);
      });
    return () => {
      active = false;
    };
  }, [open, recommendationId]);

  if (!resolution) return null;

  const rec = resolution.recommendation || {};
  const status = resolution.status || '';
  const accountName = accounts?.[resolution.account_id]?.name || resolution.account_id || '—';
  const resolverName = resolution.resolver_display_name || resolution.data?.provider_config?.name;
  const duration = formatDuration(resolution.created_at, resolution.updated_at);

  const appliedRaw = resolution.data?.data;
  const applied = typeof appliedRaw === 'string' ? safeJSONParse(appliedRaw) : appliedRaw;
  const recommendationData =
    typeof fullRecommendation?.recommendation === 'string' ? safeJSONParse(fullRecommendation.recommendation) : fullRecommendation?.recommendation;
  const changes = buildAppliedChanges(applied, recommendationData);
  // One column per field any container sets, in a fixed order — a resolution that
  // touched only requests should not carry two empty limit columns.
  const changeColumns = ['CPU request', 'CPU limit', 'Memory request', 'Memory limit'].filter((label) =>
    changes.some((change) => change.rows.some((row) => row.label === label))
  );

  // The recommendation's live status, preferred over the copy carried on the row
  // — the row is as old as the last listing fetch.
  const currentRecStatus = fullRecommendation?.status || rec.status;
  const statusMismatch = describeStatusMismatch(status, currentRecStatus);
  const reference = describeResolutionReference(resolution.type_reference_id, status, resolution.data?.ticket_key);
  const showHistoryTab = resolution.type_reference_id === 'cli_execution' && Boolean(recommendationId);

  return (
    <CustomDrawer open={open} onClose={onClose} bare nonModal variant='modern' width='680px' storageKey='nb.resolutionDrawer.width'>
      <Box data-testid='resolution-detail-panel' sx={{ height: '100%', display: 'flex', flexDirection: 'column' }}>
        <Box sx={{ p: `${ds.space[4]} ${ds.space[5]} 0 ${ds.space[5]}`, display: 'flex', alignItems: 'flex-start', gap: ds.space[3] }}>
          <Box sx={{ flex: 1, minWidth: 0 }}>
            <Box sx={{ display: 'flex', alignItems: 'center', gap: ds.space[2], mb: ds.space[2], flexWrap: 'wrap' }}>
              <Label size='sm' tone={STATUS_TONE[status] ?? 'neutral'}>
                {statusText(status)}
              </Label>
              {rec.severity && <SeverityIcon level={toSeverityLevel(rec.severity)} aria-label={rec.severity} />}
              <Typography sx={{ fontSize: ds.text.small, color: ds.gray[500] }}>{formatResolutionType(resolution.type) || 'Resolution'}</Typography>
            </Box>
            <Typography
              sx={{ fontSize: ds.text.title, fontWeight: ds.weight.semibold, color: ds.gray[700], lineHeight: 1.3, letterSpacing: '-0.01em' }}
            >
              {rec.rule_name ? snakeToTitleCase(rec.rule_name) : 'Resolution'}
            </Typography>
            <Typography sx={{ fontSize: ds.text.small, color: ds.gray[500] }}>
              {getResourceDisplayName(rec, '—')}
              {accountName ? ` · ${accountName}` : ''}
            </Typography>
          </Box>
          <Button
            tone='ghost'
            composition='icon-only'
            size='sm'
            icon={<CloseIcon />}
            aria-label='Close'
            onClick={onClose}
            id='resolution-panel-close'
          />
        </Box>

        {/* The same tab strip the recommendation panel carries, so the two panels
            read as one family. History appears only for the resolution types that
            have one — an empty tab is worse than no tab. */}
        <Box sx={{ px: ds.space[5], pt: ds.space[2], borderBottom: `1px solid ${ds.gray[200]}` }}>
          <Tabs
            value={activeTab}
            onChange={(newVal: number) => setActiveTab(newVal)}
            behavior='filter'
            showSurface={false}
            variant='primary'
            ariaLabel='resolution detail tabs'
            options={{
              tabOptions: [
                { value: 0, text: 'Details', id: 'resolution-tab-details', icon: <InfoOutlinedIcon sx={{ fontSize: 16 }} /> },
                ...(showHistoryTab
                  ? [{ value: 1, text: 'History', id: 'resolution-tab-history', icon: <HistoryOutlinedIcon sx={{ fontSize: 16 }} /> }]
                  : []),
              ],
            }}
          />
        </Box>

        <Box sx={{ flex: 1, overflowY: 'auto', p: `${ds.space[5]} ${ds.space[5]} ${ds.space[6]} ${ds.space[5]}` }}>
          {activeTab === 1 && recommendationId && (
            <CommandExecutionHistory accountId={resolution.account_id} recommendationId={recommendationId} resolutionId={resolution.id} />
          )}
          {activeTab === 0 && (
            <>
              {/* Outcome leads: for a failure the reason is the whole reason to open this. */}
              {status === 'Failed' && (
                <Box sx={{ mb: ds.space[5] }}>
                  <Banner
                    tone='critical'
                    title='This resolution failed'
                    message={resolution.status_message || 'No reason was recorded.'}
                    id='resolution-panel-failure'
                  />
                </Box>
              )}
              {status !== 'Failed' && resolution.status_message && (
                <Box sx={{ mb: ds.space[5] }}>
                  <Banner tone={status === 'Success' ? 'success' : 'info'} message={resolution.status_message} id='resolution-panel-message' />
                </Box>
              )}

              {/* The same Card + table the recommendation panel uses for the same kind
                  of fact, down to the shared ResourceChangeCell — a reader should not
                  have to re-learn "before → after" per tab. */}
              {changes.length > 0 && (
                <Card
                  variant='accent'
                  tone={status === 'Success' ? 'info' : 'warning'}
                  size='sm'
                  data-testid='resolution-applied-changes'
                  sx={{ mb: ds.space[5] }}
                  header={
                    <Box sx={{ display: 'flex', alignItems: 'center', gap: ds.space[2] }}>
                      <TuneOutlinedIcon sx={{ fontSize: '18px', color: ds.gray[500] }} />
                      <Typography
                        sx={{ fontFamily: 'var(--ds-font-display)', fontSize: ds.text.body, fontWeight: ds.weight.semibold, color: ds.gray[700] }}
                      >
                        {CHANGE_HEADING[status] ?? 'Target values'}
                      </Typography>
                      <Box sx={{ flex: 1 }} />
                      <Label size='sm' tone='neutral'>
                        {changes.length} {changes.length === 1 ? 'container' : 'containers'}
                      </Label>
                    </Box>
                  }
                >
                  <TableContainer
                    sx={{
                      borderRadius: ds.radius.lg,
                      border: `1px solid ${ds.gray[200]}`,
                      backgroundColor: ds.background[100],
                      '& .MuiTableCell-root': { px: ds.space[3], py: ds.space[2], fontSize: ds.text.small, borderColor: ds.gray[200] },
                    }}
                  >
                    <Table size='small'>
                      <TableHead>
                        <TableRow sx={{ backgroundColor: ds.gray[100] }}>
                          {['Container', ...changeColumns].map((heading) => (
                            <TableCell
                              key={heading}
                              sx={{ fontWeight: ds.weight.semibold, color: ds.gray[700], fontSize: `${ds.text.caption} !important` }}
                            >
                              {heading}
                            </TableCell>
                          ))}
                        </TableRow>
                      </TableHead>
                      <TableBody>
                        {changes.map((change) => (
                          <TableRow key={change.containerName} sx={{ '&:last-child td': { borderBottom: 'none' } }}>
                            <TableCell>
                              <Typography
                                sx={{ fontSize: ds.text.small, color: ds.gray[700], fontWeight: ds.weight.medium, fontFamily: ds.font.mono }}
                              >
                                {change.containerName}
                              </Typography>
                            </TableCell>
                            {changeColumns.map((column) => {
                              const row = change.rows.find((candidate) => candidate.label === column);
                              if (!row) {
                                return (
                                  <TableCell key={column}>
                                    <Typography sx={{ fontSize: ds.text.small, color: ds.gray[500] }}>—</Typography>
                                  </TableCell>
                                );
                              }
                              return (
                                <TableCell key={column}>
                                  {row.after === null ? (
                                    // Unparseable quantity: show what was written rather
                                    // than the cell's dash for "no value".
                                    <Typography sx={{ fontSize: ds.text.small, color: ds.gray[700] }}>{row.afterText}</Typography>
                                  ) : (
                                    <ResourceChangeCell current={row.before} recommended={row.after} isMem={row.isMem} />
                                  )}
                                </TableCell>
                              );
                            })}
                          </TableRow>
                        ))}
                      </TableBody>
                    </Table>
                  </TableContainer>
                </Card>
              )}

              {/* Timing and context were two grids of the same shape stacked on
                  each other, one of them repeating "1d ago" twice. One block. */}
              <Section title='Details'>
                <Row label={status === 'InProgress' ? 'Running for' : 'Ran for'}>
                  {duration || '—'}
                  {resolution.created_at && (
                    <Box component='span' sx={{ color: ds.gray[500] }}>
                      {' · started '}
                      <Datetime value={resolution.created_at} />
                    </Box>
                  )}
                </Row>
                <Row label='Account'>{accountName}</Row>
                <Row label='Resolver'>
                  {resolution.resolver_type || '—'}
                  {resolverName ? ` · ${resolverName}` : ''}
                </Row>
                {(reference.href || reference.detail || reference.missing) && (
                  <Row label='Produced'>
                    {reference.href ? (
                      <Link
                        href={reference.href}
                        target='_blank'
                        style={{ display: 'inline-flex', alignItems: 'center', gap: 4, fontSize: ds.text.body }}
                      >
                        {formatResolutionType(resolution.type) || 'Open'}
                        {reference.detail ? ` ${reference.detail}` : ''}
                        <OpenInNewIcon sx={{ fontSize: 14 }} />
                      </Link>
                    ) : (
                      <Box component='span' sx={{ color: reference.missing ? ds.gray[500] : ds.gray[700] }}>
                        {reference.missing ? `No ${formatResolutionType(resolution.type) || 'artefact'} was created` : reference.detail}
                      </Box>
                    )}
                  </Row>
                )}
                {currentRecStatus && (
                  <Row label='Recommendation'>
                    <Box sx={{ display: 'inline-flex', alignItems: 'center', gap: ds.space[2], flexWrap: 'wrap' }}>
                      <Label size='sm' tone={CLOSED_STATUSES.has(currentRecStatus) ? 'success' : 'neutral'}>
                        {currentRecStatus}
                      </Label>
                      {/* The resolution's status and the recommendation's are written by
                      different processes and routinely disagree (#35490). Naming the
                      disagreement beats leaving two labels side by side for the
                      reader to reconcile — and a failed attempt that left the
                      recommendation locked in progress is the worse of the two,
                      because that state is what stops it being recommended again. */}
                      {statusMismatch && (
                        // A sentence, not a category: Label title-cases by default,
                        // which turned this into "Still Marked In Progress Despite
                        // The Failure".
                        <Label size='sm' tone='warning' textTransform='none'>
                          {statusMismatch}
                        </Label>
                      )}
                      {recommendationId && (
                        <Link
                          href={`/optimise?id=${recommendationId}#recommendations`}
                          style={{ display: 'inline-flex', alignItems: 'center', gap: 4, fontSize: ds.text.small }}
                        >
                          View
                          <OpenInNewIcon sx={{ fontSize: 12 }} />
                        </Link>
                      )}
                    </Box>
                  </Row>
                )}
                {rec.estimated_savings ? (
                  <Row label='Est. savings'>
                    <Box sx={{ display: 'inline-flex', alignItems: 'baseline', gap: ds.space[1] }}>
                      <Currency value={rec.estimated_savings} precison={2} prefix='$' varient='savings' />
                      <Box component='span' sx={{ fontSize: ds.text.small, color: ds.gray[500] }}>
                        / mo
                      </Box>
                    </Box>
                  </Row>
                ) : null}
              </Section>

              {/* The applied payload is only worth showing raw when it could not be
              read as a change — a config fix, a cloud resize, an unknown shape. */}
              {changes.length === 0 && applied && (
                <Section title='Applied'>
                  <Box sx={{ p: ds.space[4], backgroundColor: ds.background[100], borderRadius: ds.radius.sm, border: `1px solid ${ds.gray[300]}` }}>
                    <Text
                      value={typeof applied === 'string' ? applied : JSON.stringify(applied, null, 2)}
                      sx={{ fontSize: ds.text.body, color: ds.gray[700], whiteSpace: 'pre-wrap', wordBreak: 'break-word' }}
                    />
                  </Box>
                </Section>
              )}
            </>
          )}
        </Box>

        {/* Actions sit in a footer bar, where the recommendation panel puts its
            own — a retry is something you do about this resolution, not part of
            reading it, and burying it in the failure banner made it easy to miss. */}
        {status === 'Failed' && (
          <Box sx={panelActionBarSx} data-testid='resolution-action-bar'>
            <Button
              tone='primary'
              size='sm'
              icon={<RestartAltOutlinedIcon />}
              iconPlacement='start'
              disabled={retrying}
              onClick={() => onRetry(resolution.id, resolution.account_id)}
              id='resolution-panel-retry'
            >
              {retrying ? 'Retrying…' : 'Retry'}
            </Button>
          </Box>
        )}
      </Box>
    </CustomDrawer>
  );
};

export default ResolutionDetailPanel;
