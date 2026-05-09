import { Box, Typography, Divider, Tooltip } from '@mui/material';
import AccountBalanceWalletOutlinedIcon from '@mui/icons-material/AccountBalanceWalletOutlined';
import StorageOutlinedIcon from '@mui/icons-material/StorageOutlined';
import { colors } from 'src/utils/colors';
import WidgetCard from '@common/WidgetCard';
import TrendArrowPercentage from '@components1/common/widgets/TrendArrowPercentage';
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

// ─── Category dot config (matches top filter chip dots) ───────────────────

const CAT_META: Record<MainCategory, { label: string; accent: string }> = {
  performance: { label: 'perf', accent: '#EA580C' },
  cost: { label: 'cost', accent: '#16A34A' },
  security_config: { label: 'sec', accent: '#DC2626' },
};

const CAT_ORDER: MainCategory[] = ['performance', 'cost', 'security_config'];

// ─── Count pill: colored dot + count + label, dimmed when zero ────────────

const CountItem = ({ count, color, label }: { count: number; color: string; label: string }) => {
  const active = count > 0;
  return (
    <Box sx={{ display: 'inline-flex', alignItems: 'center', gap: '5px' }}>
      <Box
        sx={{
          width: '6px',
          height: '6px',
          borderRadius: '50%',
          backgroundColor: active ? color : colors.text.quaternary,
          opacity: active ? 1 : 0.5,
          flexShrink: 0,
        }}
      />
      <Typography component='span' sx={{ fontSize: '12px', fontWeight: 700, color: active ? color : colors.text.quaternary, lineHeight: 1 }}>
        {count}
      </Typography>
      <Typography component='span' sx={{ fontSize: '11px', color: active ? colors.text.tertiary : colors.text.quaternary, lineHeight: 1 }}>
        {label}
      </Typography>
    </Box>
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
      <Box sx={{ mb: '16px' }}>
        <Box sx={{ display: 'flex', alignItems: 'center', gap: '6px', mb: '12px' }}>
          <AccountBalanceWalletOutlinedIcon sx={{ fontSize: '18px', color: colors.text.secondary }} />
          <Typography sx={{ fontSize: '14px', fontWeight: 700, color: colors.text.secondary }}>Cost & Health Overview</Typography>
        </Box>
        <WidgetCard sx={{ mt: 0, p: '12px', borderRadius: '8px' }}>
          <Box sx={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: '10px' }}>
            {[0, 1, 2, 3].map((i) => (
              <Box key={i}>
                <Box sx={{ height: '10px', backgroundColor: '#E5E7EB', borderRadius: '4px', width: '60%', mb: '6px' }} />
                <Box
                  sx={{
                    height: '16px',
                    backgroundColor: '#E5E7EB',
                    borderRadius: '4px',
                    width: '80%',
                    animation: 'pulse 1.5s infinite',
                  }}
                />
              </Box>
            ))}
          </Box>
          <style>{`@keyframes pulse { 0%, 100% { opacity: 1; } 50% { opacity: 0.5; } }`}</style>
        </WidgetCard>
      </Box>
    );
  }

  if (!costByCurrency || costByCurrency.length === 0) return null;

  const SYMBOL_TO_NAME: Record<string, string> = { '₹': 'INR', $: 'USD' };

  return (
    <Box sx={{ mb: '16px' }}>
      <Box sx={{ display: 'flex', alignItems: 'center', gap: '6px', mb: '12px' }}>
        <AccountBalanceWalletOutlinedIcon sx={{ fontSize: '18px', color: colors.text.secondary }} />
        <Typography sx={{ fontSize: '14px', fontWeight: 600, color: colors.text.secondary }}>Cost & Health Overview</Typography>
      </Box>

      {costByCurrency.map((summary) => (
        <WidgetCard key={summary.currencySymbol} sx={{ mt: 0, p: '16px 20px', borderRadius: '8px', mb: '8px' }}>
          {costByCurrency.length > 1 && (
            <Tooltip title={summary.accountNames.join(', ')} placement='top' arrow>
              <Typography
                sx={{ fontSize: '10px', fontWeight: 600, color: colors.text.tertiary, mb: '6px', textTransform: 'uppercase', cursor: 'default' }}
              >
                {SYMBOL_TO_NAME[summary.currencySymbol] || summary.currencySymbol} &middot; {summary.accountNames.length} account
                {summary.accountNames.length !== 1 ? 's' : ''}
              </Typography>
            </Tooltip>
          )}
          <Box sx={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: '10px' }}>
            <Box>
              <Typography sx={{ fontSize: '10px', color: colors.text.quaternary, mb: '2px' }}>Month to Date</Typography>
              <Box sx={{ display: 'flex', alignItems: 'center', gap: '4px' }}>
                <Typography sx={{ fontSize: '16px', fontWeight: 700, color: colors.text.secondary }}>
                  {formatCurrency(summary.mtd, summary.currencySymbol)}
                </Typography>
                {summary.mtdChange !== 0 && (
                  <TrendArrowPercentage value={Math.abs(summary.mtdChange)} sign={summary.mtdChange > 0 ? -1 : 1} width='auto' />
                )}
              </Box>
            </Box>
            <Box>
              <Typography sx={{ fontSize: '10px', color: colors.text.quaternary, mb: '2px' }}>Prev. Month</Typography>
              <Typography sx={{ fontSize: '16px', fontWeight: 700, color: colors.text.secondary }}>
                {formatCurrency(summary.prevMonth, summary.currencySymbol)}
              </Typography>
            </Box>
            <Box>
              <Typography sx={{ fontSize: '10px', color: colors.text.quaternary, mb: '2px' }}>Projected</Typography>
              <Box sx={{ display: 'flex', alignItems: 'center', gap: '4px' }}>
                <Typography sx={{ fontSize: '16px', fontWeight: 700, color: colors.text.secondary }}>
                  {formatCurrency(summary.projected, summary.currencySymbol)}
                </Typography>
                {summary.projectedChange !== 0 && (
                  <TrendArrowPercentage value={Math.abs(summary.projectedChange)} sign={summary.projectedChange > 0 ? -1 : 1} width='auto' />
                )}
              </Box>
            </Box>
            <Box>
              <Typography sx={{ fontSize: '10px', color: colors.text.quaternary, mb: '2px' }}>Year to Date</Typography>
              <Typography sx={{ fontSize: '16px', fontWeight: 700, color: colors.text.secondary }}>
                {formatCurrency(summary.ytd, summary.currencySymbol)}
              </Typography>
            </Box>
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
        mb: '8px',
        overflow: 'hidden',
        borderRadius: '8px',
        cursor: 'pointer',
        border: `1px solid ${selected ? colors.border.primary : colors.border.secondaryLightest}`,
        backgroundColor: selected ? colors.background.primaryLightest : colors.background.white,
        '&:hover': { backgroundColor: selected ? colors.background.primaryLightest : colors.background.tertiaryLightestestest },
        '&:focus-visible': { outline: `2px solid ${colors.border.primary}`, outlineOffset: '1px' },
        px: '14px',
        py: '12px',
      }}
    >
      {/* 2x2 grid: [name | savings] / [MTD+trend | counts] — both rows share baseline alignment */}
      <Box
        sx={{
          display: 'grid',
          gridTemplateColumns: '1fr auto',
          columnGap: '16px',
          rowGap: '8px',
          alignItems: 'center',
        }}
      >
        {/* Row 1, col 1 — Account name + (optional) critical pill */}
        <Box sx={{ display: 'flex', alignItems: 'center', gap: '8px', minWidth: 0 }}>
          <Typography
            sx={{
              fontSize: '13px',
              fontWeight: 600,
              color: colors.text.secondary,
              whiteSpace: 'nowrap',
              overflow: 'hidden',
              textOverflow: 'ellipsis',
            }}
          >
            {account.accountName}
          </Typography>
          {account.criticalCount > 0 && (
            <Box
              sx={{
                display: 'inline-flex',
                alignItems: 'center',
                gap: '4px',
                px: '6px',
                py: '2px',
                borderRadius: '4px',
                backgroundColor: colors.background.errorLight,
                flexShrink: 0,
              }}
            >
              <Typography sx={{ fontSize: '10px', fontWeight: 700, color: colors.error, lineHeight: 1 }}>{account.criticalCount}</Typography>
              <Typography sx={{ fontSize: '10px', fontWeight: 500, color: colors.error, lineHeight: 1 }}>critical</Typography>
            </Box>
          )}
        </Box>

        {/* Row 1, col 2 — Savings */}
        <Typography
          sx={{
            fontSize: '11px',
            fontWeight: account.totalDollarImpact > 0 ? 600 : 400,
            color: account.totalDollarImpact > 0 ? colors.success : colors.text.quaternary,
            whiteSpace: 'nowrap',
            justifySelf: 'end',
          }}
        >
          {account.totalDollarImpact > 0 ? `${formatDollars(account.totalDollarImpact)}/mo savings` : 'no savings'}
        </Typography>

        {/* Row 2, col 1 — MTD value (large) + trend, or empty-state */}
        {acctCost && acctCost.mtd > 0 ? (
          <Box sx={{ display: 'flex', alignItems: 'flex-end', gap: '6px', whiteSpace: 'nowrap', minWidth: 0, lineHeight: 1 }}>
            <Typography sx={{ fontSize: '12px', fontWeight: 500, color: colors.text.secondary, lineHeight: 1 }}>
              {formatCurrency(acctCost.mtd, acctCost.currencySymbol)}
            </Typography>
            <Typography sx={{ fontSize: '10px', color: colors.text.quaternary, textTransform: 'uppercase', letterSpacing: '0.4px', lineHeight: 1 }}>
              MTD
            </Typography>
            {acctCost.change !== 0 && (
              <Box sx={{ display: 'inline-flex', alignItems: 'center', lineHeight: 1, '& *': { lineHeight: 1 } }}>
                <TrendArrowPercentage value={Math.abs(acctCost.change)} sign={acctCost.change > 0 ? -1 : 1} size='sm' />
              </Box>
            )}
          </Box>
        ) : (
          <Typography sx={{ fontSize: '11px', color: colors.text.quaternary, fontStyle: 'italic' }}>No billing data</Typography>
        )}

        {/* Row 2, col 2 — Counts (perf · cost · sec), right-aligned */}
        <Box sx={{ display: 'flex', alignItems: 'center', gap: '10px', justifySelf: 'end' }}>
          {CAT_ORDER.map((cat) => {
            const meta = CAT_META[cat];
            return <CountItem key={cat} count={account.categoryCounts[cat] ?? 0} color={meta.accent} label={meta.label} />;
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
      {((costByCurrency && costByCurrency.length > 0) || costLoading) && <Divider sx={{ mb: '16px' }} />}
      <Box sx={{ display: 'flex', alignItems: 'center', gap: '6px', mb: '12px' }}>
        <StorageOutlinedIcon sx={{ fontSize: '18px', color: colors.text.secondary }} />
        <Typography sx={{ fontSize: '14px', fontWeight: 700, color: colors.text.secondary }}>Accounts & Clusters</Typography>
      </Box>
      <WidgetCard sx={{ mt: 0, p: '12px' }}>
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
          <Typography sx={{ fontSize: '12px', color: colors.text.quaternary, textAlign: 'center', py: '16px' }}>No account data</Typography>
        )}
      </WidgetCard>
    </Box>
  );
};

export default AccountClusterPane;
