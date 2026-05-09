import { Box, Typography } from '@mui/material';
import SummaryWidget from '@components1/optimise/SummaryWidget';
import SummaryLabels from '@components1/common/widgets/SummaryLabels';
import PropTypes from 'prop-types';
import { colors } from 'src/utils/colors';
import { useEffect, useState } from 'react';
import apiKubernetes1 from '@api1/kubernetes1';
import { getLast24Hrs, getSpecificTime } from '@lib/datetime';
import apiAskNudgebee from '@api1/ask-nudgebee';
import ShimmerLoading from '@components1/common/ShimmerLoading';
import { useTenantBranding } from '@hooks/useTenantBranding';

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
        fontSize: '11px',
        fontWeight: 400,
        color: colors.text.tertiary,
        whiteSpace: 'nowrap',
      }}
    >
      last 24h
    </Typography>
  );

  if (type === 'investigations') {
    return (
      <Box sx={{ padding: '20px 0px', display: 'flex', flexDirection: 'row', width: '100%', gap: '10px' }}>
        <ShimmerLoading isLoading={investigateInfographics.loading} width={'100%'}>
          <SummaryWidget
            title='Total Investigations'
            maxWidth='220px'
            variant='default'
            value={
              <Box sx={{ display: 'flex', flexDirection: 'row', alignItems: 'flex-end', gap: '8px' }}>
                <Typography
                  sx={{
                    color: colors.text.secondary,
                    fontSize: '28px',
                    fontWeight: 600,
                    lineHeight: '28px',
                  }}
                >
                  {investigateInfographics.current}
                </Typography>
                <Box sx={{ display: 'flex', flexDirection: 'column', gap: '4px', marginBottom: '2px' }}>
                  <SummaryLabels
                    variant={'info'}
                    label={`${investigateInfographics.diff > 0 ? '+' : ''}${investigateInfographics.diff}%`}
                    grayText='last 24h'
                  />
                </Box>
              </Box>
            }
            sx={{ width: '100%' }}
            showInfoIcon={true}
            tooltipContent={
              'Total Events tracks the number of automatically investigated events in the last 24 hours. The percentage indicates the change in volume processed compared to the previous 24-hour period.'
            }
            tooltipPosition='right'
          />
        </ShimmerLoading>

        <ShimmerLoading isLoading={investigateInfographics.loading} width={'100%'}>
          <SummaryWidget title='Total Triage' value={investigateInfographics.current} maxWidth='200px' variant='default' sx={{ width: '100%' }} />
        </ShimmerLoading>

        {/* 5. Updated Widget: Uses diffTime (Efficiency) instead of diff (Trend) */}
        <ShimmerLoading isLoading={investigateInfographics.loading} width={'100%'}>
          <SummaryWidget
            title='Total Time Saved'
            maxWidth='220px'
            value={
              <Box sx={{ display: 'flex', flexDirection: 'row', alignItems: 'flex-end', gap: '8px' }}>
                <Typography
                  component='div'
                  sx={{
                    color: colors.text.secondary,
                    fontSize: '28px',
                    fontWeight: 600,
                    lineHeight: '28px',
                    whiteSpace: 'nowrap',
                    display: 'flex',
                    alignItems: 'baseline',
                  }}
                >
                  {(() => {
                    const { days, hours, minutes } = splitTimeSaved(investigateInfographics.currentTime);
                    if (days === 0 && hours === 0) {
                      return (
                        <>
                          {minutes}
                          <Box component='span' sx={{ fontSize: '16px', fontWeight: 500, ml: '2px' }}>
                            m
                          </Box>
                        </>
                      );
                    }
                    if (days > 0) {
                      return (
                        <>
                          {days}
                          <Box component='span' sx={{ fontSize: '16px', fontWeight: 500, ml: '2px', mr: hours ? '6px' : 0 }}>
                            d
                          </Box>
                          {hours > 0 && (
                            <>
                              {hours}
                              <Box component='span' sx={{ fontSize: '16px', fontWeight: 500, ml: '2px' }}>
                                h
                              </Box>
                            </>
                          )}
                        </>
                      );
                    }
                    return (
                      <>
                        {hours}
                        <Box component='span' sx={{ fontSize: '16px', fontWeight: 500, ml: '2px', mr: minutes ? '6px' : 0 }}>
                          h
                        </Box>
                        {minutes > 0 && (
                          <>
                            {minutes}
                            <Box component='span' sx={{ fontSize: '16px', fontWeight: 500, ml: '2px' }}>
                              m
                            </Box>
                          </>
                        )}
                      </>
                    );
                  })()}
                </Typography>
                <Box sx={{ display: 'flex', flexDirection: 'column', gap: '4px', marginBottom: '2px' }}>
                  <SummaryLabels
                    variant={investigateInfographics.diffTime < 0 ? 'critical' : 'savings'}
                    label={`${investigateInfographics.diffTime > 0 ? '+' : ''}${investigateInfographics.diffTime}%`}
                    grayText='productivity'
                  />
                </Box>
              </Box>
            }
            variant='savings'
            sx={{ width: '100%' }}
            showInfoIcon={true}
            headerRight={last24hPill}
            tooltipContent={`Engineer time saved in the last 24h (ignores the date filter below). For each completed investigation we compare ${baseTitle}'s actual runtime to a configurable manual baseline. The badge shows the average % of manual effort automated.`}
          />
        </ShimmerLoading>

        <ShimmerLoading isLoading={investigateInfographics.loading} width={'100%'}>
          <SummaryWidget
            title={`${baseTitle} Savings`}
            maxWidth='220px'
            value={
              <Box sx={{ display: 'flex', flexDirection: 'row', alignItems: 'flex-end', gap: '8px' }}>
                <Typography
                  sx={{
                    color: colors.text.secondary,
                    fontSize: '28px',
                    fontWeight: 600,
                    lineHeight: '28px',
                  }}
                >
                  {investigateInfographics.currentCost != null ? `$${investigateInfographics.currentCost.toLocaleString()}` : '$0'}
                </Typography>
                {investigateInfographics.diffCost !== 0 && (
                  <Box sx={{ display: 'flex', flexDirection: 'column', gap: '4px', marginBottom: '2px' }}>
                    <SummaryLabels
                      variant={investigateInfographics.diffCost < 0 ? 'critical' : 'savings'}
                      label={`${investigateInfographics.diffCost > 0 ? '+' : ''}${investigateInfographics.diffCost}%`}
                      grayText=''
                    />
                  </Box>
                )}
              </Box>
            }
            variant='savings'
            showInfoIcon={true}
            headerRight={last24hPill}
            tooltipContent={`Engineer-time cost avoided in the last 24h (ignores the date filter below). Hours saved × engineer hourly rate, using the same manual baseline as Time Saved. The badge shows change in investigation volume vs. the prior 24h.`}
            sx={{ width: '100%' }}
          />
        </ShimmerLoading>
      </Box>
    );
  }

  // ... (Return for 'events' type remains unchanged) ...
  return (
    <Box sx={{ padding: '20px 0px', display: 'flex', flexDirection: 'row', width: '100%', gap: '10px' }}>
      <SummaryWidget
        title='Total Events'
        value={
          <Box sx={{ display: 'flex', flexDirection: 'row', alignItems: 'flex-end', gap: '8px' }}>
            <Typography
              sx={{
                color: colors.text.secondary,
                fontSize: '28px',
                fontWeight: 600,
                lineHeight: '28px',
              }}
            >
              {eventInfographics.current}
            </Typography>
            <Box sx={{ display: 'flex', flexDirection: 'column', gap: '4px', marginBottom: '2px' }}>
              <SummaryLabels
                variant={eventInfographics.diff < 0 ? 'savings' : 'critical'}
                label={`${eventInfographics.diff > 0 ? '+' : ''}${eventInfographics.diff}%`}
                grayText='last 24h'
              />
            </Box>
          </Box>
        }
        maxWidth='220px'
        showInfoIcon={true}
        tooltipContent='Total Events tracks the total volume of raw signals ingested from your monitored clusters in the last 24 hours. The percentage indicates the change in event volume compared to the previous 24-hour period.'
        variant='default'
        sx={{
          width: '100%',
        }}
        tooltipPosition='right'
      />
    </Box>
  );
};

TroubleshootSummary.propTypes = {
  type: PropTypes.oneOf(['events', 'investigations']),
  tab: PropTypes.oneOf(['auto', 'manual']),
};

export default TroubleshootSummary;
