/**
 * CloudCostSummary — DS-migrated, service-agnostic Cost Summary panel for the
 * cloud-account Summary tabs (EC2, RDS, S3, ECS, CloudFoundry).
 *
 * Mirrors the typography and Stat-based composition used in
 * `CloudAccountSummary.tsx` so the cloud-account Summary surface looks
 * consistent across the parent page and the per-service pages.
 *
 * Renders Monthly forecast / Current Month (MTD) / Credits-and-Net-spend in
 * one WidgetCard, and a conditional Savings card (tinted green) when there
 * are estimated savings.
 */
import React from 'react';
import { Box, Stack, Typography, useMediaQuery } from '@mui/material';
import InfoOutlinedIcon from '@mui/icons-material/InfoOutlined';
import WidgetCard from '@components1/ds/WidgetCard';
import DsTooltip from '@components1/ds/Tooltip';
import { Stat } from '@components1/ds/Stat';
import { Trend } from '@components1/ds/Trend';
import Currency from '@common-new/format/Currency';
import DoughnutChartK8s from '@components1/common/charts/DoughnutChartK8s';
import { formatNumber } from '@lib/formatter';
import { getBudgetExpectedMonthlyExpense, getExpectedYearlyExpense } from '@lib/budget';
import { ds } from '@utils/colors';

interface CloudCostSummaryProps {
  clusterSummary?: any;
  currencySymbol?: string;
}

const MONTHLY_FORECAST_TOOLTIP =
  'Projected end-of-month cost based on current month-to-date gross spend (excludes credits). Percentage compares to last month gross usage.';
const SAVINGS_TOOLTIP =
  "Savings are estimated by the cloud provider based on the account's full usage. On newly connected accounts they may appear higher than visible spend until cost reports accumulate enough history.";

// Section heading — `bodyLg + medium + gray[700]` exactly matches
// `CloudAccountSummary.SectionHeading`. Sub-section labels do NOT use this;
// they sit inside `<Stat>` which renders its own small/gray label per the DS
// spec — that's the styling fix the visual review surfaced.
const SectionTitle = ({ children }: { children: React.ReactNode }) => (
  <Typography sx={{ fontSize: ds.text.bodyLg, fontWeight: ds.weight.medium, color: ds.gray[700], mb: ds.space[2] }}>{children}</Typography>
);

const NoDataInline = () => <Typography sx={{ color: ds.gray[500], fontSize: ds.text.caption }}>No data available</Typography>;

const SubCaption = ({ label, currency, value }: { label: string; currency: string; value: number | null }) =>
  value !== null && value > 0 ? (
    <Box sx={{ display: 'inline-flex', alignItems: 'baseline', gap: ds.space[1] }}>
      <Typography component='span' sx={{ fontSize: ds.text.caption, color: ds.gray[500] }}>
        {label}:
      </Typography>
      <Currency prefix={currency} value={value} />
    </Box>
  ) : (
    <Typography component='span' sx={{ fontSize: ds.text.caption, color: ds.gray[500] }}>
      {label}: No data
    </Typography>
  );

export function CloudCostSummary({ clusterSummary = {}, currencySymbol = '$' }: CloudCostSummaryProps) {
  const smallScreen = useMediaQuery('(max-width:1440px)');

  // Prefer gross-spend aggregates; fall back to net-spend aggregates for accounts
  // without the gross-spend column.
  const currentGrossSpend =
    clusterSummary?.gross_spends_aggregate?.aggregate?.sum?.amount || clusterSummary?.spends_aggregate?.aggregate?.sum?.amount || 0;
  const lastMonthGrossSpend =
    clusterSummary?.lm_gross_spends_aggregate?.aggregate?.sum?.amount || clusterSummary?.lm_spends_aggregate?.aggregate?.sum?.amount || 0;
  const lastMonthCredits = Math.abs(clusterSummary?.lm_credits_aggregate?.aggregate?.sum?.amount || 0);
  const currentCredits = Math.abs(clusterSummary?.credits_aggregate?.aggregate?.sum?.amount || 0);
  const currentNetSpend = clusterSummary?.spends_aggregate?.aggregate?.sum?.amount || 0;

  const monthlyForecast = getBudgetExpectedMonthlyExpense(currentGrossSpend);
  const dailyAvgCost = currentGrossSpend / (new Date().getDate() || 1);

  const hasValidPercentage = lastMonthGrossSpend > 0;
  const percentageChange = hasValidPercentage ? ((monthlyForecast - lastMonthGrossSpend) * 100) / lastMonthGrossSpend : 0;

  const savingsYearly = (clusterSummary?.recommendation_aggregate?.aggregate?.sum?.estimated_savings || 0) * 12;
  const yearlyGrossSpend =
    clusterSummary?.yearly_gross_spends_aggregate?.aggregate?.sum?.amount || clusterSummary?.yearly_spends_aggregate?.aggregate?.sum?.amount || 0;
  const yearlyExpense = getExpectedYearlyExpense(currentGrossSpend, yearlyGrossSpend);
  const savingsPercentage = savingsYearly > 0 && yearlyExpense > 1 ? Math.min(Math.round((savingsYearly * 100) / yearlyExpense), 100) : 0;

  const renderForecastValue = () => {
    if (currentGrossSpend <= 0) return <NoDataInline />;
    return (
      <Box display='flex' alignItems='center' gap={ds.space[1]}>
        <Currency prefix={currencySymbol} value={monthlyForecast} />
        {hasValidPercentage && Math.abs(percentageChange) < 1000 && (
          <Trend sign={percentageChange > 0 ? -1 : 1} value={Math.abs(percentageChange)} width='auto' />
        )}
      </Box>
    );
  };

  return (
    <Stack>
      <SectionTitle>Cost Summary</SectionTitle>

      <WidgetCard
        sx={{
          mt: 0,
          display: 'flex',
          alignItems: 'center',
          justifyContent: smallScreen ? 'space-between' : 'flex-start',
          flexWrap: 'wrap',
          gap: ds.space[6],
        }}
      >
        <Stack direction='column' gap={ds.space[4]}>
          <Stat
            size='md'
            label='Monthly forecast'
            info={{ tooltip: MONTHLY_FORECAST_TOOLTIP }}
            value={renderForecastValue()}
            sub={<SubCaption label='Prev mo (gross)' currency={currencySymbol} value={lastMonthGrossSpend > 0 ? lastMonthGrossSpend : null} />}
          />
          <Stat
            size='md'
            label='Current Month (MTD)'
            value={currentGrossSpend > 0 ? <Currency prefix={currencySymbol} value={currentGrossSpend} /> : <NoDataInline />}
            sub={<SubCaption label='Avg daily cost' currency={currencySymbol} value={currentGrossSpend > 0 ? dailyAvgCost : null} />}
          />
          {(currentCredits > 0 || lastMonthCredits > 0) && (
            <Stat
              size='md'
              label='Credits / Discounts'
              value={
                <Box sx={{ display: 'inline-flex', flexDirection: 'column', gap: '2px' }}>
                  {currentCredits > 0 && (
                    <Typography component='span' sx={{ fontSize: ds.text.small, color: ds.green[600] }}>
                      This mo: -{currencySymbol}
                      {formatNumber(currentCredits)}
                    </Typography>
                  )}
                  {lastMonthCredits > 0 && (
                    <Typography component='span' sx={{ fontSize: ds.text.small, color: ds.green[600] }}>
                      Prev mo: -{currencySymbol}
                      {formatNumber(lastMonthCredits)}
                    </Typography>
                  )}
                </Box>
              }
              sub={
                currentCredits > 0 ? (
                  <SubCaption label='Net spend (MTD)' currency={currencySymbol} value={currentNetSpend > 0 ? currentNetSpend : null} />
                ) : undefined
              }
            />
          )}
        </Stack>
      </WidgetCard>

      {/* Savings card — tinted green; only when there's something to show */}
      {savingsYearly > 0 && (
        <WidgetCard
          sx={{
            mt: ds.space[2],
            display: 'flex',
            alignItems: 'center',
            justifyContent: smallScreen ? 'space-between' : 'flex-start',
            flexWrap: 'wrap',
            gap: ds.space[6],
            border: `1px solid ${ds.green[200]}`,
            backgroundColor: ds.green[100],
          }}
        >
          <Box
            sx={{
              display: 'inherit',
              alignItems: 'inherit',
              justifyContent: 'inherit',
              gap: ds.space[5],
              flexGrow: smallScreen ? 1 : 0,
            }}
          >
            <Box display='flex' flexDirection='column'>
              <Box display='flex' alignItems='center' gap={ds.space[1]}>
                <Typography sx={{ color: ds.gray[600], fontSize: ds.text.small }}>Savings</Typography>
                <DsTooltip title={SAVINGS_TOOLTIP} arrow>
                  <InfoOutlinedIcon sx={{ fontSize: 14, color: ds.gray[400], cursor: 'help' }} />
                </DsTooltip>
              </Box>
              <Currency prefix={currencySymbol} value={savingsYearly} suffix='/yr' />
              <Typography sx={{ fontSize: ds.text.caption, color: ds.gray[500] }}>estimated 12 mos</Typography>
            </Box>
            {savingsPercentage > 0 && <DoughnutChartK8s size={'61px'} value={savingsPercentage} isDecimal={true} />}
          </Box>
        </WidgetCard>
      )}
    </Stack>
  );
}

export default CloudCostSummary;
