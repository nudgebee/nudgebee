/**
 * GovernanceView — the AI Gateway's Governance sub-tab.
 *
 * Makes P1 reliability and P2 intelligence legible from the same metering rows the
 * cost views use:
 *
 *   Reliability  — error-rate headline + health, tail latency (p50/p95/p99), an
 *                  outcome bar (success/4xx/5xx/other), and edge rejections by reason.
 *   Intelligence — rule-routed vs passthrough, the routing decisions (substituted /
 *                  failed-over / deprecated / …), and DLP hits.
 *   Error rate over time — a per-bucket trend line.
 *   Errors by provider — which upstream is failing.
 *   Substitution routes — where cross-provider substitution actually reroutes (from → to).
 *
 * Clickable counts drill into the Requests tab scoped to the rows behind them (via
 * `onDrill`, a GovScope the shell routes to Requests). Reads the shell's already-
 * loaded `metrics`; does no fetching of its own.
 */
import * as React from 'react';
import { Box, CircularProgress } from '@mui/material';
import dayjs from 'dayjs';
import utc from 'dayjs/plugin/utc';
import ChevronRightIcon from '@mui/icons-material/ChevronRight';
import ShieldOutlinedIcon from '@mui/icons-material/ShieldOutlined';
import AutoAwesomeOutlinedIcon from '@mui/icons-material/AutoAwesomeOutlined';
import ShowChartIcon from '@mui/icons-material/ShowChart';
import HubOutlinedIcon from '@mui/icons-material/HubOutlined';
import CallSplitOutlinedIcon from '@mui/icons-material/CallSplitOutlined';
import { Card } from '@ui/Card';
import { Stat } from '@ui/Stat';
import { Chip } from '@ui/Chip';
import { Banner } from '@ui/Banner';
import { EmptyState } from '@ui/EmptyState';
import Chart from '@ui/Chart';
import { fmtDuration } from '@components/llm/cost-analyser/format';
import type { GatewayUsageMetrics, GatewayOutcomes } from '@api1/gateway-usage';
import type { GatewayFilters, GovScope } from '../useGatewayData';

dayjs.extend(utc);

interface GovernanceViewProps {
  metrics: GatewayUsageMetrics | null;
  filters: GatewayFilters;
  loading: boolean;
  error: string | null;
  /** Drill-in: scope the Requests tab to the rows behind a governance count. */
  onDrill: (scope: GovScope) => void;
}

const fmtCount = (n: number | null | undefined): string => (n === null || n === undefined ? '—' : n.toLocaleString('en-US'));
const fmtPct = (n: number): string => `${n.toFixed(1)}%`;
const secs = (s: number): string => fmtDuration(s * 1000);

// Humanize a raw reason key ("secret_blocked" → "Secret blocked") as a fallback
// for any reason not in the explicit maps below.
const humanize = (k: string): string => (k ? k.charAt(0).toUpperCase() + k.slice(1).replace(/_/g, ' ') : k);

// Friendly labels for the routing decisions the gateway records. `passthrough` is
// the baseline (no rule fired) and is shown separately, not as an intelligence row.
const ROUTING_LABEL: Record<string, string> = {
  substitute: 'Substituted (cross-provider)',
  fallback: 'Failed over',
  deprecated: 'Deprecated → replacement',
  alias: 'Aliased',
  load_balance: 'Load-balanced',
  blocked: 'Blocked (deny rule)',
};

// Friendly labels for edge-rejection reasons (attributes.derived.reject_reason).
const REJECT_LABEL: Record<string, string> = {
  rate_limited: 'Rate limited',
  no_credentials: 'No credentials',
  blocked: 'Blocked (deny rule)',
  secret_blocked: 'Secret blocked (DLP)',
};

// ─── Outcome bar ────────────────────────────────────────────────────────────────

type Segment = { key: string; label: string; count: number; color: string };

function outcomeSegments(o: GatewayOutcomes): Segment[] {
  return [
    { key: 'success', label: 'Success (2xx)', count: o.success, color: 'var(--ds-green-500)' },
    { key: 'client_error', label: 'Client error (4xx)', count: o.client_error, color: 'var(--ds-amber-500)' },
    { key: 'server_error', label: 'Server error (5xx)', count: o.server_error, color: 'var(--ds-red-500)' },
    { key: 'other', label: 'Other / unrecorded', count: o.other, color: 'var(--ds-gray-400)' },
  ].filter((s) => s.count > 0);
}

/** A stacked horizontal bar of the outcome mix, with a legend of counts below. */
function OutcomeBar({ outcomes }: { outcomes: GatewayOutcomes }) {
  const segs = outcomeSegments(outcomes);
  const total = segs.reduce((a, s) => a + s.count, 0);
  if (total === 0) {
    return <Box sx={{ fontSize: 'var(--ds-text-caption)', color: 'var(--ds-gray-500)' }}>No requests in this window.</Box>;
  }
  return (
    <Box sx={{ display: 'flex', flexDirection: 'column', gap: 'var(--ds-space-2)' }}>
      <Box sx={{ display: 'flex', width: '100%', height: 10, borderRadius: 'var(--ds-radius-sm)', overflow: 'hidden' }}>
        {segs.map((s) => (
          <Box key={s.key} title={`${s.label}: ${fmtCount(s.count)}`} sx={{ width: `${(100 * s.count) / total}%`, backgroundColor: s.color }} />
        ))}
      </Box>
      <Box sx={{ display: 'flex', flexWrap: 'wrap', gap: 'var(--ds-space-3)' }}>
        {segs.map((s) => (
          <Box key={s.key} sx={{ display: 'flex', alignItems: 'center', gap: 'var(--ds-space-1)' }}>
            <Box sx={{ width: 8, height: 8, borderRadius: '2px', backgroundColor: s.color }} />
            <Box sx={{ fontSize: 'var(--ds-text-caption)', color: 'var(--ds-gray-600)' }}>
              {s.label}{' '}
              <Box component='span' sx={{ color: 'var(--ds-gray-800)', fontVariantNumeric: 'tabular-nums' }}>
                {fmtCount(s.count)}
              </Box>
            </Box>
          </Box>
        ))}
      </Box>
    </Box>
  );
}

// ─── Small building blocks ───────────────────────────────────────────────────────

/** A clickable label→count row that drills into Requests. */
function SignalRow({ id, label, count, onClick }: { id: string; label: string; count: number; onClick: () => void }) {
  return (
    <Box
      id={id}
      data-testid={id}
      role='button'
      tabIndex={0}
      onClick={onClick}
      onKeyDown={(e) => {
        if (e.key === 'Enter' || e.key === ' ') {
          e.preventDefault();
          onClick();
        }
      }}
      sx={{
        display: 'flex',
        alignItems: 'center',
        justifyContent: 'space-between',
        gap: 'var(--ds-space-2)',
        padding: 'var(--ds-space-2)',
        borderRadius: 'var(--ds-radius-sm)',
        cursor: 'pointer',
        '&:hover': { backgroundColor: 'var(--ds-gray-100)' },
        '&:focus-visible': { outline: '2px solid var(--ds-primary-400)', outlineOffset: '-2px' },
      }}
    >
      <Box sx={{ fontSize: 'var(--ds-text-body)', color: 'var(--ds-gray-700)' }}>{label}</Box>
      <Box sx={{ display: 'flex', alignItems: 'center', gap: 'var(--ds-space-1)' }}>
        <Box
          sx={{
            fontSize: 'var(--ds-text-body)',
            fontWeight: 'var(--ds-font-weight-medium)',
            color: 'var(--ds-gray-800)',
            fontVariantNumeric: 'tabular-nums',
          }}
        >
          {fmtCount(count)}
        </Box>
        <ChevronRightIcon sx={{ fontSize: 16, color: 'var(--ds-gray-400)' }} />
      </Box>
    </Box>
  );
}

function SectionLabel({ children }: { children: React.ReactNode }) {
  return (
    <Box
      sx={{
        fontSize: 'var(--ds-text-caption)',
        fontWeight: 'var(--ds-font-weight-medium)',
        color: 'var(--ds-gray-500)',
        textTransform: 'uppercase',
        letterSpacing: '0.04em',
      }}
    >
      {children}
    </Box>
  );
}

function CardHeader({ icon, title, trailing }: { icon: React.ReactNode; title: string; trailing?: React.ReactNode }) {
  return (
    <Box sx={{ display: 'flex', alignItems: 'center', gap: 'var(--ds-space-2)' }}>
      <Box sx={{ display: 'flex', color: 'var(--ds-gray-500)' }}>{icon}</Box>
      <Box sx={{ fontSize: 'var(--ds-text-subtitle)', fontWeight: 'var(--ds-font-weight-semibold)', color: 'var(--ds-gray-800)' }}>{title}</Box>
      {trailing && <Box sx={{ marginLeft: 'auto' }}>{trailing}</Box>}
    </Box>
  );
}

// Health verdict for the error rate — a shared dependency's at-a-glance signal.
// <1% healthy, <5% elevated, else high. (semantic Chip tone, not the accent.)
function healthChip(errorRatePct: number): React.ReactNode {
  const [tone, label] =
    errorRatePct < 1
      ? (['success', 'Healthy'] as const)
      : errorRatePct < 5
      ? (['warning', 'Elevated'] as const)
      : (['critical', 'High error rate'] as const);
  return (
    <Chip variant='status' tone={tone} size='sm' dot>
      {label}
    </Chip>
  );
}

// A severity color for an error-rate percentage (green/amber/red dot).
const errColor = (pct: number): string => (pct < 1 ? 'var(--ds-green-500)' : pct < 5 ? 'var(--ds-amber-500)' : 'var(--ds-red-500)');

// ─── View ─────────────────────────────────────────────────────────────────────

export function GovernanceView({ metrics, filters, loading, error, onDrill }: GovernanceViewProps) {
  const gov = metrics?.governance ?? null;
  const errorRate = gov?.outcomes.error_rate_pct ?? 0;
  // The denominator for every count on this tab — anchors DLP / substitution /
  // error figures against total volume.
  const totalReq = gov ? gov.outcomes.success + gov.outcomes.client_error + gov.outcomes.server_error + gov.outcomes.other : 0;

  // Split routing decisions: passthrough is the baseline, the rest are "the gateway
  // did something" (the intelligence story). Backend already sorts by count desc.
  const routing = gov?.routing_reasons ?? [];
  const passthrough = routing.find((r) => r.key === 'passthrough')?.requests ?? 0;
  const routed = routing.filter((r) => r.key !== 'passthrough');
  const routedTotal = routed.reduce((a, r) => a + r.requests, 0);

  // Error-rate trend: one % point per bucket. A line needs ≥2 points to read as a trend.
  const bucketFmt = filters.granularity === 'hour' ? 'DD MMM HH:00' : 'DD MMM';
  const series = gov?.outcome_series ?? [];
  const trendLabels = series.map((b) => dayjs(b.bucket).format(bucketFmt));
  const trendData = series.map((b) => {
    const t = b.success + b.client_error + b.server_error + b.other;
    return t ? (100 * (t - b.success)) / t : 0;
  });
  // Enrichment fields are optional (an older gateway build omits them) — default
  // to empty so the panels degrade gracefully rather than crash on version skew.
  const byProvider = gov?.outcomes_by_provider ?? [];
  const routes = gov?.substitution_routes ?? [];

  if (error) {
    return <Banner tone='critical' title='Could not load governance metrics' message={error} />;
  }
  if (loading && !gov) {
    return (
      <Box sx={{ display: 'flex', justifyContent: 'center', alignItems: 'center', minHeight: 240 }}>
        <CircularProgress size={28} />
      </Box>
    );
  }
  if (!gov) {
    return <EmptyState size='section' illustration='no-results' title='No traffic' description='No requests were forwarded in this window.' />;
  }

  return (
    <Box sx={{ display: 'flex', flexDirection: 'column', gap: 'var(--ds-space-4)' }}>
      {/* ─── Row 1: Reliability | Intelligence summary ───────────────── */}
      <Box sx={{ display: 'grid', gap: 'var(--ds-space-4)', gridTemplateColumns: { xs: '1fr', md: '1fr 1fr' }, alignItems: 'start' }}>
        <Card>
          <Box sx={{ display: 'flex', flexDirection: 'column', gap: 'var(--ds-space-4)' }}>
            <CardHeader icon={<ShieldOutlinedIcon sx={{ fontSize: 18 }} />} title='Reliability' trailing={healthChip(errorRate)} />

            <Box sx={{ display: 'flex', gap: 'var(--ds-space-5)', flexWrap: 'wrap' }}>
              <Stat label='Total requests' value={fmtCount(totalReq)} size='hero' />
              <Stat label='Error rate' value={fmtPct(errorRate)} />
              <Stat label='Succeeded' value={fmtCount(gov.outcomes.success)} />
              <Stat
                label='Errored'
                value={fmtCount(gov.outcomes.client_error + gov.outcomes.server_error + gov.outcomes.other)}
                onClick={() => onDrill({ kind: 'status', value: 'error', label: 'Errors' })}
              />
            </Box>

            {gov.latency && (
              <Box sx={{ display: 'flex', gap: 'var(--ds-space-5)', flexWrap: 'wrap' }}>
                <Stat label='Latency p50' value={secs(gov.latency.p50_seconds)} size='sm' />
                <Stat label='p95' value={secs(gov.latency.p95_seconds)} size='sm' />
                <Stat label='p99' value={secs(gov.latency.p99_seconds)} size='sm' />
              </Box>
            )}

            <OutcomeBar outcomes={gov.outcomes} />

            <Box sx={{ display: 'flex', flexDirection: 'column', gap: 'var(--ds-space-2)' }}>
              <SectionLabel>Rejections by reason</SectionLabel>
              {gov.rejects.length === 0 ? (
                <Box sx={{ fontSize: 'var(--ds-text-caption)', color: 'var(--ds-gray-500)' }}>
                  No edge rejections with a recorded reason in this window.
                </Box>
              ) : (
                gov.rejects.map((r) => (
                  <SignalRow
                    key={r.key}
                    id={`gov-reject-${r.key}`}
                    label={REJECT_LABEL[r.key] ?? humanize(r.key)}
                    count={r.requests}
                    onClick={() => onDrill({ kind: 'reject', value: r.key, label: REJECT_LABEL[r.key] ?? humanize(r.key) })}
                  />
                ))
              )}
            </Box>
          </Box>
        </Card>

        <Card>
          <Box sx={{ display: 'flex', flexDirection: 'column', gap: 'var(--ds-space-4)' }}>
            <CardHeader icon={<AutoAwesomeOutlinedIcon sx={{ fontSize: 18 }} />} title='Intelligence' />

            <Box sx={{ display: 'flex', gap: 'var(--ds-space-5)', flexWrap: 'wrap' }}>
              <Stat label='Routed by a rule' value={fmtCount(routedTotal)} size='hero' />
              <Stat label='Passthrough' value={fmtCount(passthrough)} />
              <Stat
                label='DLP hits'
                value={fmtCount(gov.dlp_hits)}
                onClick={gov.dlp_hits > 0 ? () => onDrill({ kind: 'dlp', label: 'DLP hits' }) : undefined}
              />
            </Box>

            <Box sx={{ display: 'flex', flexDirection: 'column', gap: 'var(--ds-space-2)' }}>
              <SectionLabel>Routing decisions</SectionLabel>
              {routed.length === 0 ? (
                <Box sx={{ fontSize: 'var(--ds-text-caption)', color: 'var(--ds-gray-500)' }}>
                  No rule-driven routing in this window — all traffic passed through untouched.
                </Box>
              ) : (
                routed.map((r) => (
                  <SignalRow
                    key={r.key}
                    id={`gov-routing-${r.key}`}
                    label={ROUTING_LABEL[r.key] ?? humanize(r.key)}
                    count={r.requests}
                    onClick={() => onDrill({ kind: 'routing', value: r.key, label: ROUTING_LABEL[r.key] ?? humanize(r.key) })}
                  />
                ))
              )}
            </Box>
          </Box>
        </Card>
      </Box>

      {/* ─── Row 2: error-rate trend ─────────────────────────────────── */}
      {trendData.length >= 2 && (
        <Card>
          <Box sx={{ display: 'flex', flexDirection: 'column', gap: 'var(--ds-space-3)' }}>
            <CardHeader icon={<ShowChartIcon sx={{ fontSize: 18 }} />} title='Error rate over time' />
            <Chart.TimeSeries
              labels={trendLabels}
              series={[{ key: 'Error rate', data: trendData }]}
              shape='line'
              format={(v) => fmtPct(v)}
              compactFormat={(v) => `${Math.round(v)}%`}
              showLegend={false}
              height={200}
              id='gov-error-rate-trend'
            />
          </Box>
        </Card>
      )}

      {/* ─── Row 3: errors by provider | substitution routes ─────────── */}
      <Box sx={{ display: 'grid', gap: 'var(--ds-space-4)', gridTemplateColumns: { xs: '1fr', md: '1fr 1fr' }, alignItems: 'start' }}>
        <Card>
          <Box sx={{ display: 'flex', flexDirection: 'column', gap: 'var(--ds-space-3)' }}>
            <CardHeader icon={<HubOutlinedIcon sx={{ fontSize: 18 }} />} title='Errors by provider' />
            {byProvider.length === 0 ? (
              <Box sx={{ fontSize: 'var(--ds-text-caption)', color: 'var(--ds-gray-500)' }}>No provider traffic in this window.</Box>
            ) : (
              byProvider.map((p) => (
                <Box
                  key={p.provider}
                  sx={{
                    display: 'flex',
                    alignItems: 'center',
                    justifyContent: 'space-between',
                    gap: 'var(--ds-space-2)',
                    padding: 'var(--ds-space-1) var(--ds-space-2)',
                  }}
                >
                  <Box sx={{ fontSize: 'var(--ds-text-body)', color: 'var(--ds-gray-700)' }}>{p.provider || '—'}</Box>
                  <Box sx={{ display: 'flex', alignItems: 'center', gap: 'var(--ds-space-3)' }}>
                    <Box sx={{ fontSize: 'var(--ds-text-caption)', color: 'var(--ds-gray-500)', fontVariantNumeric: 'tabular-nums' }}>
                      {fmtCount(p.requests)} req
                    </Box>
                    <Box sx={{ display: 'flex', alignItems: 'center', gap: 'var(--ds-space-1)', minWidth: 64, justifyContent: 'flex-end' }}>
                      <Box sx={{ width: 8, height: 8, borderRadius: '50%', backgroundColor: errColor(p.error_rate_pct) }} />
                      <Box sx={{ fontSize: 'var(--ds-text-body)', color: 'var(--ds-gray-800)', fontVariantNumeric: 'tabular-nums' }}>
                        {fmtPct(p.error_rate_pct)}
                      </Box>
                    </Box>
                  </Box>
                </Box>
              ))
            )}
          </Box>
        </Card>

        <Card>
          <Box sx={{ display: 'flex', flexDirection: 'column', gap: 'var(--ds-space-3)' }}>
            <CardHeader icon={<CallSplitOutlinedIcon sx={{ fontSize: 18 }} />} title='Substitution routes' />
            {routes.length === 0 ? (
              <Box sx={{ fontSize: 'var(--ds-text-caption)', color: 'var(--ds-gray-500)' }}>No cross-provider substitution in this window.</Box>
            ) : (
              routes.map((r, i) => (
                <Box
                  key={`${r.from_provider}/${r.from_model}->${r.to_provider}/${r.to_model}-${i}`}
                  sx={{
                    display: 'flex',
                    alignItems: 'center',
                    justifyContent: 'space-between',
                    gap: 'var(--ds-space-2)',
                    padding: 'var(--ds-space-1) var(--ds-space-2)',
                  }}
                >
                  <Box sx={{ display: 'flex', flexDirection: 'column', minWidth: 0, fontSize: 'var(--ds-text-caption)' }}>
                    <Box sx={{ color: 'var(--ds-gray-500)', overflowWrap: 'anywhere' }}>
                      {r.from_provider || '—'}/{r.from_model || '—'}
                    </Box>
                    <Box sx={{ color: 'var(--ds-gray-800)', fontWeight: 'var(--ds-font-weight-medium)', overflowWrap: 'anywhere' }}>
                      → {r.to_provider}/{r.to_model}
                    </Box>
                  </Box>
                  <Box
                    sx={{ fontSize: 'var(--ds-text-body)', color: 'var(--ds-gray-800)', fontVariantNumeric: 'tabular-nums', whiteSpace: 'nowrap' }}
                  >
                    ×{fmtCount(r.requests)}
                  </Box>
                </Box>
              ))
            )}
          </Box>
        </Card>
      </Box>
    </Box>
  );
}

export default GovernanceView;
