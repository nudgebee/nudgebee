import { useRef, useState, type ElementType } from 'react';
import { Box, Typography } from '@mui/material';
import AccountBalanceWalletOutlinedIcon from '@mui/icons-material/AccountBalanceWalletOutlined';
import StorageOutlinedIcon from '@mui/icons-material/StorageOutlined';
import { ds } from 'src/utils/colors';
import { Card } from '@ui/Card';
import CustomTooltip from '@ui/Tooltip';
import { Divider } from '@ui/Divider';
import { Trend } from '@ui/Trend';
import { Chip } from '@ui/Chip';
import { Skeleton } from '@ui/Skeleton';
import { Stat } from '@ui/Stat';
import { type AccountSummary, type MainCategory } from './insights';

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

// ─── Count chip: DS Chip with dot composition ─────────────────────────────
// Only rendered for non-zero counts — a row of "0 perf / 0 sec" chips competed
// with the account name for width and carried no information.

const CountChip = ({ count, tone, label }: { count: number; tone: 'savings' | 'warning' | 'critical'; label: string }) => (
  <Chip size='2xs' tone={tone} dot>
    {count} {label}
  </Chip>
);

// ─── Format with currency symbol ──────────────────────────────────────────

const formatCurrency = (value: number, symbol: string): string => {
  if (value >= 1000) return symbol + (value / 1000).toFixed(1).replace(/\.0$/, '') + 'k';
  return symbol + Math.round(value).toLocaleString('en-US');
};

// ─── Section icon chip ─────────────────────────────────────────────────────
// Light rounded-square icon chip for section headings — matches the "Quick
// Links" pattern used on the home page (28px, radius-md), kept neutral gray
// rather than per-section color.

const SectionIcon = ({ icon: Icon }: { icon: ElementType }) => (
  <Box
    sx={{
      width: 28,
      height: 28,
      borderRadius: ds.radius.md,
      backgroundColor: ds.gray[100],
      color: ds.gray[600],
      display: 'flex',
      alignItems: 'center',
      justifyContent: 'center',
      flexShrink: 0,
    }}
  >
    <Icon sx={{ fontSize: 16 }} />
  </Box>
);

// ─── Cost overview header ──────────────────────────────────────────────────

const OverviewHeader = (
  <Box sx={{ display: 'flex', alignItems: 'center', gap: ds.space[2] }}>
    <SectionIcon icon={AccountBalanceWalletOutlinedIcon} />
    <Typography sx={{ fontSize: ds.text.bodyLg, fontWeight: ds.weight.semibold, color: ds.gray[700] }}>Cost & Health Overview</Typography>
  </Box>
);

const PaneHeader = ({ costByCurrency, costLoading }: { costByCurrency?: CurrencyCostSummary[]; costLoading?: boolean }) => {
  if (costLoading) {
    return (
      <Box sx={{ mb: ds.space[4] }}>
        <Card size='md' header={OverviewHeader}>
          <Box sx={{ display: 'grid', gridTemplateColumns: '1fr 1fr', columnGap: ds.space[5], rowGap: ds.space[3] }}>
            {[0, 1, 2, 3].map((i) => (
              <Box key={i} sx={{ display: 'flex', flexDirection: 'column', gap: ds.space[1] }}>
                <Skeleton shape='text' size='caption' width='60%' />
                <Skeleton shape='text' size='title' width='80%' />
              </Box>
            ))}
          </Box>
        </Card>
      </Box>
    );
  }

  if (!costByCurrency || costByCurrency.length === 0) return null;

  const SYMBOL_TO_NAME: Record<string, string> = { '₹': 'INR', $: 'USD' };

  return (
    <Box sx={{ mb: ds.space[4] }}>
      <Card size='md' elevation='flat' header={OverviewHeader}>
        {costByCurrency.map((summary, idx) => (
          <Box key={summary.currencySymbol}>
            {idx > 0 && <Divider sx={{ my: ds.space[4] }} />}
            {costByCurrency.length > 1 && (
              <CustomTooltip title={summary.accountNames.join(', ')} placement='top'>
                <Typography
                  sx={{
                    fontSize: ds.text.caption,
                    fontWeight: ds.weight.semibold,
                    color: ds.gray[600],
                    textTransform: 'uppercase',
                    cursor: 'default',
                    mb: ds.space[3],
                    display: 'inline-block',
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
          </Box>
        ))}
      </Card>
    </Box>
  );
};

// ─── Account card ──────────────────────────────────────────────────────────

const AccountCard = ({
  account,
  acctCost,
  defaultCurrencySymbol,
  selected,
  onSelect,
}: {
  account: AccountSummary;
  acctCost?: AccountCost;
  defaultCurrencySymbol: string;
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
      <Box sx={{ display: 'flex', flexDirection: 'column', gap: ds.space[2], minWidth: 0 }}>
        {/* Row 1 — name takes the remaining width, savings is pinned right and
            never shrinks, so the figures line up card-to-card. */}
        <Box sx={{ display: 'flex', alignItems: 'baseline', gap: ds.space[2], minWidth: 0 }}>
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
                flex: 1,
                minWidth: 0,
              }}
            >
              {account.accountName}
            </Typography>
          </CustomTooltip>
          <Typography
            sx={{
              fontSize: ds.text.caption,
              fontWeight: account.totalDollarImpact > 0 ? ds.weight.semibold : ds.weight.regular,
              color: account.totalDollarImpact > 0 ? ds.green[600] : ds.gray[500],
              whiteSpace: 'nowrap',
              flexShrink: 0,
            }}
          >
            {account.totalDollarImpact > 0
              ? `${formatCurrency(account.totalDollarImpact, acctCost?.currencySymbol || defaultCurrencySymbol)}/mo savings`
              : 'no savings'}
          </Typography>
        </Box>

        {/* Row 2 — badges. Zero-count categories are omitted rather than rendered
            as neutral "0 perf" / "0 sec" chips. */}
        {(account.criticalCount > 0 || CAT_ORDER.some((cat) => (account.categoryCounts[cat] ?? 0) > 0)) && (
          <Box sx={{ display: 'flex', alignItems: 'center', gap: ds.space[1], flexWrap: 'wrap', minWidth: 0 }}>
            {account.criticalCount > 0 && (
              <Chip size='xs' tone='critical' aria-label={`${account.criticalCount} critical`}>
                {account.criticalCount} critical
              </Chip>
            )}
            {CAT_ORDER.filter((cat) => (account.categoryCounts[cat] ?? 0) > 0).map((cat) => (
              <CountChip key={cat} count={account.categoryCounts[cat]} tone={CAT_META[cat].tone} label={CAT_META[cat].label} />
            ))}
          </Box>
        )}
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
  /** Currency symbol for an account's savings when it has no billing data of its own. */
  defaultCurrencySymbol?: string;
  selectedAccountId?: string | null;
  onSelectAccount?: (accountId: string | null) => void;
}

const AccountClusterPane = ({
  accounts,
  costByCurrency,
  accountCosts,
  costLoading,
  defaultCurrencySymbol = '$',
  selectedAccountId,
  onSelectAccount,
}: AccountClusterPaneProps) => {
  const handleSelect = (accountId: string) => {
    if (!onSelectAccount) return;
    onSelectAccount(selectedAccountId === accountId ? null : accountId);
  };
  return (
    <Box sx={{ width: '300px', flexShrink: 0 }}>
      <PaneHeader costByCurrency={costByCurrency} costLoading={costLoading} />
      <Card
        size='md'
        elevation='flat'
        header={
          <Box sx={{ display: 'flex', alignItems: 'center', gap: ds.space[2] }}>
            <SectionIcon icon={StorageOutlinedIcon} />
            <Typography sx={{ fontSize: ds.text.bodyLg, fontWeight: ds.weight.semibold, color: ds.gray[700] }}>Accounts</Typography>
          </Box>
        }
      >
        {accounts.map((acct) => (
          <AccountCard
            key={acct.accountId}
            account={acct}
            acctCost={accountCosts?.[acct.accountId]}
            defaultCurrencySymbol={defaultCurrencySymbol}
            selected={selectedAccountId === acct.accountId}
            onSelect={handleSelect}
          />
        ))}
        {accounts.length === 0 && (
          <Typography sx={{ fontSize: ds.text.small, color: ds.gray[500], textAlign: 'center', py: ds.space[4] }}>No account data</Typography>
        )}
      </Card>
    </Box>
  );
};

export default AccountClusterPane;
