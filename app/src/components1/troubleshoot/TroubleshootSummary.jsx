import { Box, Typography } from '@mui/material';
import ArrowUpwardIcon from '@mui/icons-material/ArrowUpward';
import ArrowDownwardIcon from '@mui/icons-material/ArrowDownward';
import WidgetCard from '@components1/ds/WidgetCard';
import { Stat } from '@components1/ds/Stat';
import { Chip } from '@components1/ds/Chip';
import PropTypes from 'prop-types';
import { ds } from 'src/utils/colors';
import { useEffect, useState } from 'react';
import apiKubernetes1 from '@api1/kubernetes1';
import { getLast24Hrs, getSpecificTime } from '@lib/datetime';
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
    <Chip size='xs' tone={tone} icon={<Arrow sx={{ fontSize: 12 }} />} aria-label={`${goingUp ? 'up' : 'down'} ${Math.abs(diff)} percent`}>
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
  const unitSx = { fontSize: '0.6em', fontWeight: 500, ml: '2px' };

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
        <Box component='span' sx={{ ...unitSx, mr: hours ? '6px' : 0 }}>
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
      <Box component='span' sx={{ ...unitSx, mr: mins ? '6px' : 0 }}>
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

const TroubleshootSummary = ({ type = 'events', tab = 'auto' }) => {
  const { baseTitle } = useTenantBranding();
  const [eventInfographics, setEventInfographics] = useState({
    loading: false,
    current: 0,
    previous: 0,
    diff: 0,
  });
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
    // Only fetch event stats when type='events' (default)
    if (type === 'events') {
      setEventInfographics((prev) => ({
        ...prev,
        loading: true,
      }));

      apiKubernetes1
        .eventComparsion({
          startDate: getLast24Hrs().toISOString(),
          endDate: new Date().toISOString(),
          previousStartDate: new Date(getSpecificTime(2880)).toISOString(),
          previousEndDate: getLast24Hrs().toISOString(),
        })
        .then((res) => {
          const previous = res?.data?.data?.previous?.rows?.[0]?.event_count || 0;
          const current = res?.data?.data?.current?.rows?.[0]?.event_count || 0;
          setEventInfographics({
            loading: false,
            current,
            previous,
            diff: previous === 0 ? (current > 0 ? 100 : 0) : Math.round(((current - previous) / previous) * 100),
          });
        })
        .catch((err) => {
          console.error('Failed to fetch event infographics:', err);
          setEventInfographics((prev) => ({ ...prev, loading: false }));
        });
    }

    // Only fetch investigation stats when type='investigations'
    if (type === 'investigations') {
      setInvestigateInfographics((prev) => ({
        ...prev,
        loading: true,
      }));

      const source = tab === 'auto' ? 'Investigation' : 'UserInvestigation';
      const startDate = getLast24Hrs().toISOString();
      const endDate = new Date().toISOString();
      const eventScoped = tab === 'auto';

      // Volume trend stays a Hasura roll-up — counts only — while the
      // time-saved math has moved to the llm-server time-aggregates endpoint
      // so the manual baseline and hourly rate live in one place. Run both
      // in parallel since neither depends on the other.
      Promise.all([
        apiAskNudgebee.llmConversationComparsion({
          source,
          startDate,
          endDate,
          previousStartDate: new Date(getSpecificTime(2880)).toISOString(),
          previousEndDate: getLast24Hrs().toISOString(),
          extractEventIdsFromTitle: eventScoped,
        }),
        apiAskNudgebee.getConversationTimeAggregates({
          // No accountId — backend rolls up across every account this
          // session can read (matches the legacy widget's Hasura RLS).
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
          const wallTimeSeconds = aggregates?.total_wall_time_seconds ?? 0;
          const manualBaselineMins = aggregates?.manual_baseline_minutes ?? FALLBACK_MANUAL_MINS;
          const hourlyRate = aggregates?.engineer_hourly_rate_usd ?? FALLBACK_HOURLY_USD;

          // Average AI runtime per completed investigation in minutes.
          // Capped at the manual baseline so a slow AI run never produces a
          // negative "time saved". Total saved multiplies by completed rows
          // since in-progress/waiting investigations haven't saved time yet.
          const avgAiMins = completedCount > 0 ? wallTimeSeconds / 60 / completedCount : 0;
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
    }
  }, [type, tab]);

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

  if (type === 'investigations') {
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
  }

  const ev = eventInfographics;
  const evHasBaseline = ev.previous > 0;

  return (
    <Box sx={{ display: 'flex', flexDirection: 'row', width: '100%', gap: ds.space[3], padding: `${ds.space[5]} 0` }}>
      <WidgetCard sx={{ ...widgetCardSx, maxWidth: '320px' }}>
        <Stat
          size='md'
          label='Total Events'
          info={{
            tooltip:
              'Total Events tracks the total volume of raw signals ingested from your monitored clusters in the last 24 hours. The percentage indicates the change in event volume compared to the previous 24-hour period.',
            position: 'right',
          }}
          value={
            ev.loading ? (
              '…'
            ) : (
              <Box sx={{ display: 'inline-flex', alignItems: 'center', gap: ds.space[2] }}>
                <Box component='span'>{ev.current.toLocaleString()}</Box>
                <TrendChip diff={ev.diff} hasBaseline={evHasBaseline} kind='up-is-bad' />
              </Box>
            )
          }
          sub={!ev.loading && evHasBaseline ? `vs ${ev.previous.toLocaleString()} prev 24h` : undefined}
        />
      </WidgetCard>
    </Box>
  );
};

TroubleshootSummary.propTypes = {
  type: PropTypes.oneOf(['events', 'investigations']),
  tab: PropTypes.oneOf(['auto', 'manual']),
};

export default TroubleshootSummary;
