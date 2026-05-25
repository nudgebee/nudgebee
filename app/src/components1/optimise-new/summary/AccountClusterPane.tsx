import { useRef, useState } from 'react';
import { Box, Typography } from '@mui/material';
import AccountBalanceWalletOutlinedIcon from '@mui/icons-material/AccountBalanceWalletOutlined';
import StorageOutlinedIcon from '@mui/icons-material/StorageOutlined';
import { ds } from 'src/utils/colors';
import WidgetCard from '../../../component-new/common/WidgetCard';
import CustomTooltip from '@components1/ds/Tooltip';
import { Divider } from '@components1/ds/Divider';
import { Trend } from '@components1/ds/Trend';
import { Chip } from '@components1/ds/Chip';
import { Skeleton } from '@components1/ds/Skeleton';
import { Stat } from '@components1/ds/Stat';
import { formatDollars, type AccountSummary, type MainCategory } from './insights';

// ─── Cost data types ──────────────────────────────────────────────────────

export interface CurrencyCostSummary {
  currencySymbol: string;
  accountNames: string[];
  mtd: number;
  prevMonth: number;
  projected: number;
  ytd: number;
  mtdChange: number;
  projectedChange: number;
}

export interface AccountCost {
  mtd: number;
  change: number;
  projected: number;
  currencySymbol: string;
}

// ─── Category meta — DS tone (closest semantic match per spec) ────────────

const CAT_META: Record<MainCategory, { label: string; tone: 'savings' | 'warning' | 'critical' }> = {
  performance: { label: 'perf', tone: 'warning' },
  cost: { label: 'cost', tone: 'savings' },
  security_config: { label: 'sec', tone: 'critical' },
};

const CAT_ORDER: MainCategory[] = ['performance', 'cost', 'security_config'];

// ─── Count chip: DS Chip with dot composition; neutral when count = 0 ─────

const CountChip = ({ count, tone, label }: { count: number; tone: 'savings' | 'warning' | 'critical'; label: string }) => {
  const active = count > 0;
  return (
    <Chip size='2xs' tone={active ? tone : 'neutral'} dot>
      {count} {label}
    </Chip>
  );
};

// ─── Format with currency symbol ──────────────────────────────────────────

const formatCurrency = (value: number, symbol: string): string => {
  if (value >= 1000) return symbol + (value / 1000).toFixed(1).replace(/\.0$/, '') + 'k';
  return symbol + Math.round(value).toLocaleString('en-US');
};

// ─── Cost overview header ──────────────────────────────────────────────────

const PaneHeader = ({ costByCurrency, costLoading }: { costByCurrency?: CurrencyCostSummary[]; costLoading?: boolean }) => {
  if (costLoading) {
    return (
      <Box sx={{ mb: ds.space[4] }}>
        <Box sx={{ display: 'flex', alignItems: 'center', gap: ds.space[1], mb: ds.space[3] }}>
          <AccountBalanceWalletOutlinedIcon sx={{ fontSize: 18, color: ds.gray[700] }} />
          <Typography sx={{ fontSize: ds.text.bodyLg, fontWeight: ds.weight.semibold, color: ds.gray[700] }}>Cost & Health Overview</Typography>
        </Box>
        <WidgetCard sx={{ mt: 0, p: ds.space[4], borderRadius: ds.radius.md }}>
          <Box sx={{ display: 'grid', gridTemplateColumns: '1fr 1fr', columnGap: ds.space[5], rowGap: ds.space[3] }}>
            {[0, 1, 2, 3].map((i) => (
              <Box key={i} sx={{ display: 'flex', flexDirection: 'column', gap: ds.space[1] }}>
                <Skeleton shape='text' size='caption' width='60%' />
                <Skeleton shape='text' size='title' width='80%' />
              </Box>
            ))}
          </Box>
        </WidgetCard>
      </Box>
    );
  }

  if (!costByCurrency || costByCurrency.length === 0) return null;

  const SYMBOL_TO_NAME: Record<string, string> = { '₹': 'INR', $: 'USD' };

  return (
    <Box sx={{ mb: ds.space[4] }}>
      <Box sx={{ display: 'flex', alignItems: 'center', gap: ds.space[1], mb: ds.space[3] }}>
        <AccountBalanceWalletOutlinedIcon sx={{ fontSize: 18, color: ds.gray[700] }} />
        <Typography sx={{ fontSize: ds.text.bodyLg, fontWeight: ds.weight.semibold, color: ds.gray[700] }}>Cost & Health Overview</Typography>
      </Box>

      {costByCurrency.map((summary) => (
        <WidgetCard key={summary.currencySymbol} sx={{ mt: 0, p: `${ds.space[4]} ${ds.space[5]}`, borderRadius: ds.radius.md, mb: ds.space[3] }}>
          {costByCurrency.length > 1 && (
            <CustomTooltip title={summary.accountNames.join(', ')} placement='top'>
              <Typography
                sx={{
                  fontSize: ds.text.caption,
                  fontWeight: ds.weight.semibold,
                  color: ds.gray[600],
                  mb: ds.space[1],
                  textTransform: 'uppercase',
                  cursor: 'default',
                }}
              >
                {SYMBOL_TO_NAME[summary.currencySymbol] || summary.currencySymbol} &middot; {summary.accountNames.length} account
                {summary.accountNames.length !== 1 ? 's' : ''}
              </Typography>
            </CustomTooltip>
          )}
          <Box sx={{ display: 'grid', gridTemplateColumns: '1fr 1fr', columnGap: ds.space[5], rowGap: ds.space[3] }}>
            <Stat
              label='Month to Date'
              size='sm'
              value={
                <Box sx={{ display: 'flex', alignItems: 'center', gap: ds.space[1] }}>
                  <Box component='span' sx={{ fontSize: ds.text.title, fontWeight: ds.weight.semibold, color: ds.gray[700] }}>
                    {formatCurrency(summary.mtd, summary.currencySymbol)}
                  </Box>
                  {summary.mtdChange !== 0 && <Trend value={Math.abs(summary.mtdChange)} sign={summary.mtdChange > 0 ? -1 : 1} size='sm' />}
                </Box>
              }
            />
            <Stat
              label='Prev. Month'
              size='sm'
              value={
                <Box component='span' sx={{ fontSize: ds.text.title, fontWeight: ds.weight.semibold, color: ds.gray[700] }}>
                  {formatCurrency(summary.prevMonth, summary.currencySymbol)}
                </Box>
              }
            />
            <Stat
              label='Projected'
              size='sm'
              value={
                <Box sx={{ display: 'flex', alignItems: 'center', gap: ds.space[1] }}>
                  <Box component='span' sx={{ fontSize: ds.text.title, fontWeight: ds.weight.semibold, color: ds.gray[700] }}>
                    {formatCurrency(summary.projected, summary.currencySymbol)}
                  </Box>
                  {summary.projectedChange !== 0 && (
                    <Trend value={Math.abs(summary.projectedChange)} sign={summary.projectedChange > 0 ? -1 : 1} size='sm' />
                  )}
                </Box>
              }
            />
            <Stat
              label='Year to Date'
              size='sm'
              value={
                <Box component='span' sx={{ fontSize: ds.text.title, fontWeight: ds.weight.semibold, color: ds.gray[700] }}>
                  {formatCurrency(summary.ytd, summary.currencySymbol)}
                </Box>
              }
            />
          </Box>
        </WidgetCard>
      ))}
    </Box>
  );
};

// ─── Account card ──────────────────────────────────────────────────────────

const AccountCard = ({
  account,
  acctCost,
  selected,
  onSelect,
}: {
  account: AccountSummary;
  acctCost?: AccountCost;
  selected: boolean;
  onSelect: (accountId: string) => void;
}) => {
  const nameRef = useRef<HTMLSpanElement>(null);
  const [isTruncated, setIsTruncated] = useState(false);

  return (
    <Box
      component='button'
      type='button'
      onClick={() => onSelect(account.accountId)}
      data-testid={`account-card-${account.accountId}`}
      sx={{
        appearance: 'none',
        font: 'inherit',
        color: 'inherit',
        textAlign: 'left',
        width: '100%',
        display: 'block',
        mb: ds.space[2],
        overflow: 'hidden',
        borderRadius: ds.radius.md,
        cursor: 'pointer',
        border: `1px solid ${selected ? ds.blue[500] : ds.gray[200]}`,
        backgroundColor: selected ? ds.blue[100] : ds.background[100],
        '&:hover': { backgroundColor: selected ? ds.blue[100] : ds.gray[100] },
        '&:focus-visible': { outline: `2px solid ${ds.blue[500]}`, outlineOffset: '1px' },
        px: ds.space[3],
        py: ds.space[3],
      }}
    >
      <Box
        sx={{
          display: 'grid',
          gridTemplateColumns: '1fr auto',
          columnGap: ds.space[4],
          rowGap: ds.space[2],
          alignItems: 'center',
        }}
      >
        {/* Row 1, col 1 — Account name + (optional) critical pill */}
        <Box sx={{ display: 'flex', alignItems: 'center', gap: ds.space[2], minWidth: 0 }}>
          <CustomTooltip title={isTruncated ? account.accountName : ''} placement='top'>
            <Typography
              ref={nameRef}
              onMouseEnter={() => {
                if (nameRef.current) setIsTruncated(nameRef.current.scrollWidth > nameRef.current.clientWidth);
              }}
              sx={{
                fontSize: ds.text.body,
                fontWeight: ds.weight.semibold,
                color: ds.gray[700],
                whiteSpace: 'nowrap',
                overflow: 'hidden',
                textOverflow: 'ellipsis',
              }}
            >
              {account.accountName}
            </Typography>
          </CustomTooltip>
          {account.criticalCount > 0 && (
            <Chip size='xs' tone='critical' aria-label={`${account.criticalCount} critical`}>
              {account.criticalCount} critical
            </Chip>
          )}
        </Box>

        {/* Row 1, col 2 — Savings */}
        <Typography
          sx={{
            fontSize: ds.text.caption,
            fontWeight: account.totalDollarImpact > 0 ? ds.weight.semibold : ds.weight.regular,
            color: account.totalDollarImpact > 0 ? ds.green[600] : ds.gray[500],
            whiteSpace: 'nowrap',
            justifySelf: 'end',
          }}
        >
          {account.totalDollarImpact > 0 ? `${formatDollars(account.totalDollarImpact)}/mo savings` : 'no savings'}
        </Typography>

        {/* Row 2, col 1 — MTD value (large) + trend, or empty-state */}
        {acctCost && acctCost.mtd > 0 ? (
          <Box sx={{ display: 'flex', alignItems: 'flex-end', gap: ds.space[1], whiteSpace: 'nowrap', minWidth: 0, lineHeight: 1 }}>
            <Typography sx={{ fontSize: ds.text.small, fontWeight: ds.weight.medium, color: ds.gray[700], lineHeight: 1 }}>
              {formatCurrency(acctCost.mtd, acctCost.currencySymbol)}
            </Typography>
            <Typography sx={{ fontSize: ds.text.caption, color: ds.gray[500], textTransform: 'uppercase', letterSpacing: '0.4px', lineHeight: 1 }}>
              MTD
            </Typography>
            {acctCost.change !== 0 && (
              <Box sx={{ display: 'inline-flex', alignItems: 'center', lineHeight: 1, '& *': { lineHeight: 1 } }}>
                <Trend value={Math.abs(acctCost.change)} sign={acctCost.change > 0 ? -1 : 1} size='sm' />
              </Box>
            )}
          </Box>
        ) : (
          <Typography sx={{ fontSize: ds.text.caption, color: ds.gray[500], fontStyle: 'italic' }}>No billing data</Typography>
        )}

        {/* Row 2, col 2 — Counts (perf · cost · sec), right-aligned */}
        <Box sx={{ display: 'flex', alignItems: 'center', gap: ds.space[1], justifySelf: 'end' }}>
          {CAT_ORDER.map((cat) => {
            const meta = CAT_META[cat];
            return <CountChip key={cat} count={account.categoryCounts[cat] ?? 0} tone={meta.tone} label={meta.label} />;
          })}
        </Box>
      </Box>
    </Box>
  );
};

// ─── Main pane ─────────────────────────────────────────────────────────────

interface AccountClusterPaneProps {
  accounts: AccountSummary[];
  costByCurrency?: CurrencyCostSummary[];
  accountCosts?: Record<string, AccountCost>;
  costLoading?: boolean;
  selectedAccountId?: string | null;
  onSelectAccount?: (accountId: string | null) => void;
}

const AccountClusterPane = ({ accounts, costByCurrency, accountCosts, costLoading, selectedAccountId, onSelectAccount }: AccountClusterPaneProps) => {
  const handleSelect = (accountId: string) => {
    if (!onSelectAccount) return;
    onSelectAccount(selectedAccountId === accountId ? null : accountId);
  };
  return (
    <Box sx={{ width: '370px', flexShrink: 0 }}>
      <PaneHeader costByCurrency={costByCurrency} costLoading={costLoading} />
      {((costByCurrency && costByCurrency.length > 0) || costLoading) && <Divider sx={{ mb: ds.space[4] }} />}
      <Box sx={{ display: 'flex', alignItems: 'center', gap: ds.space[1], mb: ds.space[3] }}>
        <StorageOutlinedIcon sx={{ fontSize: 18, color: ds.gray[700] }} />
        <Typography sx={{ fontSize: ds.text.bodyLg, fontWeight: ds.weight.semibold, color: ds.gray[700] }}>Accounts & Clusters</Typography>
      </Box>
      <WidgetCard sx={{ mt: 0, p: ds.space[3] }}>
        {accounts.map((acct) => (
          <AccountCard
            key={acct.accountId}
            account={acct}
            acctCost={accountCosts?.[acct.accountId]}
            selected={selectedAccountId === acct.accountId}
            onSelect={handleSelect}
          />
        ))}
        {accounts.length === 0 && (
          <Typography sx={{ fontSize: ds.text.small, color: ds.gray[500], textAlign: 'center', py: ds.space[4] }}>No account data</Typography>
        )}
      </WidgetCard>
    </Box>
  );
};

export default AccountClusterPane;
