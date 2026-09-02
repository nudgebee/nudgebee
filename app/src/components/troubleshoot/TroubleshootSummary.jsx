import { Box, Typography } from '@mui/material';
import ArrowUpwardIcon from '@mui/icons-material/ArrowUpward';
import ArrowDownwardIcon from '@mui/icons-material/ArrowDownward';
import WidgetCard from '@ui/WidgetCard';
import { Stat } from '@ui/Stat';
import { Chip } from '@ui/Chip';
import PropTypes from 'prop-types';
import { ds } from 'src/utils/colors';
import { useEffect, useMemo, useState } from 'react';
import { getLast24Hrs } from '@lib/datetime';
import apiAskNudgebee from '@api1/ask-nudgebee';
import { useTenantBranding } from '@hooks/useTenantBranding';

// Compact direction-of-change Chip rendered inline next to the Stat value.
// `kind` is the cost-axis intent — 'up-is-bad' (events ingested, alert volume),
// 'up-is-good' (savings, productivity), or 'neutral' (informational, no value
// judgement). When there's no baseline to compare against we render nothing
// rather than show a misleading +100% — anchoring percentages against an empty
// prior period misreads as a real spike to users.
const TrendChip = ({ diff, hasBaseline = true, kind = 'up-is-bad' }) => {
  if (!hasBaseline || diff === 0 || diff == null) return null;
  const goingUp = diff > 0;
  let tone;
  if (kind === 'neutral') tone = 'info';
  else if (kind === 'up-is-bad') tone = goingUp ? 'critical' : 'success';
  else tone = goingUp ? 'success' : 'critical';
  const Arrow = goingUp ? ArrowUpwardIcon : ArrowDownwardIcon;
  return (
    <Chip size='xs' tone={tone} icon={<Arrow sx={{ fontSize: ds.text.small }} />} aria-label={`${goingUp ? 'up' : 'down'} ${Math.abs(diff)} percent`}>
      {goingUp ? '+' : ''}
      {diff}%
    </Chip>
  );
};

TrendChip.propTypes = {
  diff: PropTypes.number,
  hasBaseline: PropTypes.bool,
  kind: PropTypes.oneOf(['up-is-bad', 'up-is-good', 'neutral']),
};

// Manual baseline minutes and engineer hourly rate are no longer constants on
// the frontend — the llm-server returns them in the time-aggregates response
// so a single backend env var can retune both widgets without a frontend
// redeploy. These fallbacks only apply if the response is missing those
// fields (older backends or a failed fetch) and match the historical
// hard-coded values.
const FALLBACK_MANUAL_MINS = 25;
const FALLBACK_HOURLY_USD = 5;

const splitTimeSaved = (totalMinutes) => {
  if (!totalMinutes || totalMinutes <= 0) return { days: 0, hours: 0, minutes: 0 };
  const mins = Math.round(totalMinutes);
  const totalHours = Math.floor(mins / 60);
  return { days: Math.floor(totalHours / 24), hours: totalHours % 24, minutes: mins % 60 };
};

// Renders the d/h/m breakdown used by the Total Time Saved widget so the Stat
// value stays a single node and inherits Stat's value typography tokens. We
// only style the unit suffixes (d/h/m) smaller to match the legacy treatment.
const TimeSavedValue = ({ minutes }) => {
  const { days, hours, minutes: mins } = splitTimeSaved(minutes);
  const unitSx = { fontSize: '0.6em', fontWeight: 'var(--ds-font-weight-medium)', ml: 'var(--ds-space-1)' };

  if (days === 0 && hours === 0) {
    return (
      <Box component='span' sx={{ display: 'inline-flex', alignItems: 'baseline' }}>
        {mins}
        <Box component='span' sx={unitSx}>
          m
        </Box>
      </Box>
    );
  }

  if (days > 0) {
    return (
      <Box component='span' sx={{ display: 'inline-flex', alignItems: 'baseline' }}>
        {days}
        <Box component='span' sx={{ ...unitSx, mr: hours ? ds.space.mul(0, 3) : 0 }}>
          d
        </Box>
        {hours > 0 && (
          <>
            {hours}
            <Box component='span' sx={unitSx}>
              h
            </Box>
          </>
        )}
      </Box>
    );
  }

  return (
    <Box component='span' sx={{ display: 'inline-flex', alignItems: 'baseline' }}>
      {hours}
      <Box component='span' sx={{ ...unitSx, mr: mins ? ds.space.mul(0, 3) : 0 }}>
        h
      </Box>
      {mins > 0 && (
        <>
          {mins}
          <Box component='span' sx={unitSx}>
            m
          </Box>
        </>
      )}
    </Box>
  );
};

TimeSavedValue.propTypes = {
  minutes: PropTypes.number,
};

// Investigations-only. The All Events tab moved to the Nubi briefing strip
// (@components/troubleshoot/briefing/NubiBriefing), which took the four event
// KPI cards and their drill-downs with it.
const TroubleshootSummary = ({ tab = 'auto', range }) => {
  const { baseTitle } = useTenantBranding();

  // The comparison window for every card. The page passes a single frozen 24h
  // `range` so the cards and the Events drill-down query the identical interval;
  // fall back to computing it here if the component is rendered standalone.
  const resolvedRange = useMemo(() => {
    if (range) return range;
    const endDate = new Date();
    const startDate = getLast24Hrs(endDate);
    return { startDate, endDate, previousStartDate: getLast24Hrs(startDate), previousEndDate: startDate };
  }, [range]);
  const [investigateInfographics, setInvestigateInfographics] = useState({
    loading: false,
    current: 0,
    previous: 0,
    diff: 0,
    currentTime: 0,
    diffTime: 0,
    currentCost: 0,
    diffCost: 0,
  });

  useEffect(() => {
    // Deliberately excludes accountIds — the underlying
    // RPCs roll up across every account this session can read and don't accept
    // an account filter, so this must not re-fetch when the account filter changes.
    setInvestigateInfographics((prev) => ({
      ...prev,
      loading: true,
    }));

    const source = tab === 'auto' ? 'Investigation' : 'UserInvestigation';
    const startDate = resolvedRange.startDate.toISOString();
    const endDate = resolvedRange.endDate.toISOString();
    const eventScoped = tab === 'auto';

    // Volume trend stays a RPC roll-up — counts only — while the
    // time-saved math has moved to the llm-server time-aggregates endpoint
    // so the manual baseline and hourly rate live in one place. Run both
    // in parallel since neither depends on the other.
    Promise.all([
      apiAskNudgebee.llmConversationComparsion({
        source,
        startDate,
        endDate,
        previousStartDate: resolvedRange.previousStartDate.toISOString(),
        previousEndDate: resolvedRange.previousEndDate.toISOString(),
        extractEventIdsFromTitle: eventScoped,
      }),
      apiAskNudgebee.getConversationTimeAggregates({
        // No accountId — backend rolls up across every account this
        // session can read (matches the legacy widget's RPC RLS).
        startDate,
        endDate,
        sources: [source],
        eventScoped,
      }),
    ])
      .then(([comparisonRes, aggregates]) => {
        const previous = comparisonRes?.data?.data?.previous?.aggregate?.count ?? 0;
        const current = comparisonRes?.data?.data?.current?.aggregate?.count ?? 0;

        const completedCount = aggregates?.completed_count ?? 0;
        // total_agent_active_time_seconds (not total_wall_time_seconds) — wall
        // time spans conversation created_at to updated_at and includes time
        // spent waiting on user replies, which dwarfs the manual baseline and
        // clamped every investigation's savings to zero.
        const activeTimeSeconds = aggregates?.total_agent_active_time_seconds ?? 0;
        const manualBaselineMins = aggregates?.manual_baseline_minutes ?? FALLBACK_MANUAL_MINS;
        const hourlyRate = aggregates?.engineer_hourly_rate_usd ?? FALLBACK_HOURLY_USD;

        // Average AI runtime per completed investigation in minutes.
        // Capped at the manual baseline so a slow AI run never produces a
        // negative "time saved". Total saved multiplies by completed rows
        // since in-progress/waiting investigations haven't saved time yet.
        const avgAiMins = completedCount > 0 ? activeTimeSeconds / 60 / completedCount : 0;
        const savedPerInvestigation = Math.max(0, manualBaselineMins - avgAiMins);
        const currentSavedMinutes = completedCount * savedPerInvestigation;

        // Productivity = share of a manual investigation's effort that the AI
        // removes. 0% when we have no completed rows to measure.
        const productivityScore = avgAiMins > 0 && manualBaselineMins > 0 ? Math.round((savedPerInvestigation / manualBaselineMins) * 100) : 0;

        const currentCost = parseFloat(((currentSavedMinutes / 60) * hourlyRate).toFixed(2));
        const volumeDiff = previous === 0 ? (current > 0 ? 100 : 0) : Math.round(((current - previous) / previous) * 100);

        setInvestigateInfographics({
          loading: false,
          current,
          previous,
          // 'diff' remains the volume trend (Last 24h count vs Prev 24h count)
          diff: volumeDiff,

          // Store raw minutes; formatted at render time for readability
          currentTime: currentSavedMinutes,

          // diffTime now represents real per-investigation productivity, not a fixed ratio
          diffTime: productivityScore,

          currentCost,
          // Savings badge must reflect actual savings. Showing the volume
          // trend when currentCost is $0 produced a misleading "+100%" on a
          // zero-savings card; gate the volume-based proxy on real savings.
          diffCost: currentCost > 0 ? volumeDiff : 0,
        });
      })
      .catch((err) => {
        console.error('Failed to fetch investigation infographics:', err);
        // Zero out the cards on failure so the user does not see stale
        // numbers (e.g. a +80% productivity badge from the previous tab)
        // alongside a $0 / 0m total — which previously looked like a bug.
        setInvestigateInfographics({
          loading: false,
          current: 0,
          previous: 0,
          diff: 0,
          currentTime: 0,
          diffTime: 0,
          currentCost: 0,
          diffCost: 0,
        });
      });
  }, [tab, resolvedRange]);

  const last24hPill = (
    <Typography
      sx={{
        fontSize: ds.text.caption,
        fontWeight: ds.weight.regular,
        color: ds.gray[500],
        whiteSpace: 'nowrap',
      }}
    >
      last 24h
    </Typography>
  );

  const widgetCardSx = {
    flex: 1,
    minWidth: 0,
    mt: 0,
    padding: `${ds.space[3]} ${ds.space[4]}`,
  };

  const inv = investigateInfographics;
  const invHasBaseline = inv.previous > 0;

  return (
    <Box sx={{ display: 'flex', flexDirection: 'row', width: '100%', gap: ds.space[3], padding: `${ds.space[5]} 0` }}>
      <WidgetCard sx={widgetCardSx}>
        <Stat
          size='md'
          label='Total Investigations'
          info={{
            tooltip:
              'Total Events tracks the number of automatically investigated events in the last 24 hours. The percentage indicates the change in volume processed compared to the previous 24-hour period.',
            position: 'right',
          }}
          value={
            inv.loading ? (
              '…'
            ) : (
              <Box sx={{ display: 'inline-flex', alignItems: 'center', gap: ds.space[2] }}>
                <Box component='span'>{inv.current.toLocaleString()}</Box>
                <TrendChip diff={inv.diff} hasBaseline={invHasBaseline} kind='neutral' />
              </Box>
            )
          }
          sub={!inv.loading && invHasBaseline ? `vs ${inv.previous.toLocaleString()} prev 24h` : undefined}
        />
      </WidgetCard>

      <WidgetCard sx={widgetCardSx}>
        <Stat
          size='md'
          label='Total Triage'
          info={{
            tooltip: 'Total number of events that were triaged in the last 24 hours.',
          }}
          value={inv.loading ? '…' : inv.current.toLocaleString()}
        />
      </WidgetCard>

      <WidgetCard sx={widgetCardSx}>
        <Stat
          size='md'
          label='Total Time Saved'
          info={{
            tooltip: `Engineer time saved in the last 24h (ignores the date filter below). For each completed investigation we compare ${baseTitle}'s actual runtime to a configurable manual baseline. The badge shows the average % of manual effort automated.`,
          }}
          headerRight={last24hPill}
          value={inv.loading ? '…' : <TimeSavedValue minutes={inv.currentTime} />}
          sub={!inv.loading && inv.diffTime > 0 ? `${inv.diffTime}% of manual effort automated` : undefined}
        />
      </WidgetCard>

      <WidgetCard sx={widgetCardSx}>
        <Stat
          size='md'
          label={`${baseTitle} Savings`}
          info={{
            tooltip:
              'Engineer-time cost avoided in the last 24h (ignores the date filter below). Hours saved × engineer hourly rate, using the same manual baseline as Time Saved. The badge shows change in investigation volume vs. the prior 24h.',
          }}
          headerRight={last24hPill}
          value={
            inv.loading ? (
              '…'
            ) : (
              <Box sx={{ display: 'inline-flex', alignItems: 'center', gap: ds.space[2] }}>
                <Box component='span'>${(inv.currentCost ?? 0).toLocaleString()}</Box>
                <TrendChip diff={inv.diffCost} hasBaseline={invHasBaseline && inv.currentCost > 0} kind='up-is-good' />
              </Box>
            )
          }
        />
      </WidgetCard>
    </Box>
  );
};

TroubleshootSummary.propTypes = {
  tab: PropTypes.oneOf(['auto', 'manual']),
  // Comparison window. Optional — the component computes a fresh last-24h
  // window when omitted.
  range: PropTypes.shape({
    startDate: PropTypes.instanceOf(Date),
    endDate: PropTypes.instanceOf(Date),
    previousStartDate: PropTypes.instanceOf(Date),
    previousEndDate: PropTypes.instanceOf(Date),
  }),
};

export default TroubleshootSummary;
