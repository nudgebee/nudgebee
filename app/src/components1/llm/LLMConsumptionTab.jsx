import React, { useEffect, useState, useCallback, useMemo } from 'react';
import Tooltip from '@components1/ds/Tooltip';
import PropTypes from 'prop-types';
import { Box, Typography, Alert, Collapse, Stack } from '@mui/material';
import { Divider } from '@components1/ds/Divider';
import { Chip } from '@components1/ds/Chip';
import { Input } from '@components1/ds/Input';
import { Switch } from '@components1/ds/Switch';
import { Select } from '@components1/ds/Select';
import EditIcon from '@mui/icons-material/Edit';
import KeyboardArrowDownIcon from '@mui/icons-material/KeyboardArrowDown';
import KeyboardArrowUpIcon from '@mui/icons-material/KeyboardArrowUp';
import { DeleteIconRed as DeleteIcon } from '@assets';
import SafeIcon from '@components1/common/SafeIcon';
import apiBudget from '@api1/budget';
import Loader from '@components1/common/Loader';
import { Button } from '@components1/ds/Button';
import { toast as snackbar } from '@components1/ds/Toast';
import { Modal } from '@components1/ds/Modal';
import { getUserSession, isTenantAdmin } from '@lib/auth';
import { ds } from '@utils/colors';
import { useTenantBranding } from '@hooks/useTenantBranding';
import apiUser from '@api1/user';
import WidgetCard from '@components1/ds/WidgetCard';
import { ProgressBar } from '@components1/ds/ProgressBar';
import { Label } from '@components1/ds/Label';

// ─── helpers ─────────────────────────────────────────────────────────────────

const formatCurrency = (amount) => {
  if (amount == null || amount === 0) return '$0.00';
  if (amount >= 1000) return `$${(amount / 1000).toFixed(2)}K`;
  if (amount >= 1) return `$${amount.toFixed(2)}`;
  return `$${amount.toFixed(4)}`;
};

const formatCount = (n) => (n != null ? n.toLocaleString() : '0');

const monthNames = ['January', 'February', 'March', 'April', 'May', 'June', 'July', 'August', 'September', 'October', 'November', 'December'];

const formatPeriod = (period) => {
  if (!period) return '';
  const [year, month] = period.split('-');
  return `${monthNames[parseInt(month) - 1]} ${year}`;
};

const getModuleLabels = (assistantName) => ({
  investigation: 'Event Analysis',
  user_investigation: `${assistantName} Chat`,
});

// Sum enabled limits across two source-objects; returns null when neither is
// enabled so the caller can render a dash instead of a misleading "0 / 0".
const sumEnabled = (limitA, limitB) => {
  const aOn = limitA?.enabled;
  const bOn = limitB?.enabled;
  if (!aOn && !bOn) return null;
  return {
    enabled: true,
    usage: (aOn ? limitA.usage || 0 : 0) + (bOn ? limitB.usage || 0 : 0),
    limit: (aOn ? limitA.limit || 0 : 0) + (bOn ? limitB.limit || 0 : 0),
    remaining: (aOn ? limitA.remaining || 0 : 0) + (bOn ? limitB.remaining || 0 : 0),
  };
};

// Walk all known limit slots, return the max usage % among enabled ones — used
// to decide the overall status pill (healthy / warn / exhausted) without an
// 8-way OR.
const maxUsagePct = (budgetData) => {
  let max = 0;
  for (const scope of ['tenant', 'account']) {
    for (const mod of ['investigation', 'user_investigation']) {
      const info = budgetData?.[mod]?.[scope];
      if (!info) continue;
      for (const key of ['monthly_cost', 'daily_cost', 'monthly_count', 'daily_count']) {
        const l = info[key];
        if (l?.enabled && l.limit > 0) {
          const pct = (l.usage / l.limit) * 100;
          if (pct > max) max = pct;
        }
      }
    }
  }
  return max;
};

const hasAnyDailyEnabled = (budgetData) => {
  for (const scope of ['tenant', 'account']) {
    for (const mod of ['investigation', 'user_investigation']) {
      const info = budgetData?.[mod]?.[scope];
      if (info?.daily_cost?.enabled || info?.daily_count?.enabled) return true;
    }
  }
  return false;
};

const computeKpi = (budgetData) => {
  const tenantInv = budgetData?.investigation?.tenant;
  const tenantChat = budgetData?.user_investigation?.tenant;
  const accountInv = budgetData?.investigation?.account;
  const accountChat = budgetData?.user_investigation?.account;

  const tenantSpend = sumEnabled(tenantInv?.monthly_cost, tenantChat?.monthly_cost);
  const accountSpend = sumEnabled(accountInv?.monthly_cost, accountChat?.monthly_cost);
  const conversations = sumEnabled(tenantInv?.monthly_count, tenantChat?.monthly_count);
  const todaySpend = sumEnabled(tenantInv?.daily_cost, tenantChat?.daily_cost);

  const pct = maxUsagePct(budgetData);
  // Status-pill exhaustion: ANY enabled monthly_cost (tenant or account) at
  // zero remaining must surface as Exhausted, because the backend rejects
  // further LLM calls for that scope. The pill speaks to account operators,
  // not just tenant admins.
  const exhausted =
    (tenantInv?.monthly_cost?.enabled && tenantInv.monthly_cost.remaining <= 0) ||
    (tenantChat?.monthly_cost?.enabled && tenantChat.monthly_cost.remaining <= 0) ||
    (accountInv?.monthly_cost?.enabled && accountInv.monthly_cost.remaining <= 0) ||
    (accountChat?.monthly_cost?.enabled && accountChat.monthly_cost.remaining <= 0);

  // Tenant-scope exhaustion is the separate signal for the "contact support
  // to enable" banner — only the tenant admin can raise the tenant cap.
  // Account exhaustion is self-serve (edit the account budget), so we do
  // NOT want the support banner firing in that case.
  const tenantExhausted =
    (tenantInv?.monthly_cost?.enabled && tenantInv.monthly_cost.remaining <= 0) ||
    (tenantChat?.monthly_cost?.enabled && tenantChat.monthly_cost.remaining <= 0);

  let status = 'healthy';
  if (exhausted) status = 'exhausted';
  else if (pct >= UTIL_THRESHOLDS.warning) status = 'critical';
  else if (pct >= UTIL_THRESHOLDS.success) status = 'warn';

  return {
    status,
    tenantExhausted,
    maxPct: pct,
    tenantSpend,
    accountSpend,
    conversations,
    todaySpend,
    hasDailyEnabled: hasAnyDailyEnabled(budgetData),
  };
};

// Single source of truth for utilization thresholds. Shared by:
//   1. `computeKpi` (status pill ladder above) — pct ≥ warning → critical,
//      pct ≥ success → warn. Threshold tweaks here change the pill.
//   2. `ProgressBar` (via its `thresholds` prop, applied below) — drives
//      the bar tone automatically without manual tone picking.
//   3. `edgeColorForPct` (row left-edge indicator) — reuses the same
//      constant so the three visual signals (pill / bar / edge) stay
//      in lockstep.
// Defined after `computeKpi` for read flow but referenced inside its body;
// safe because module-scope const initializers run before React calls
// computeKpi at render time.
const UTIL_THRESHOLDS = { success: 75, warning: 90 };

// Maps the kpi.status enum onto the DS Label tone axis. Keeps the
// existing "healthy / warn / exhausted" vocabulary in computeKpi while
// delegating the visual rendering to the DS primitive.
const STATUS_TO_LABEL = {
  healthy: { tone: 'success', text: 'Healthy' },
  warn: { tone: 'warning', text: 'Warning' },
  // `critical` sits between warn (>=75%) and exhausted (=100%): the limit
  // is in danger but hasn't tripped yet. Tone matches exhausted (red) but
  // text differs so operators see the difference between "about to fail"
  // and "already failed".
  critical: { tone: 'critical', text: 'Critical' },
  exhausted: { tone: 'critical', text: 'Exhausted' },
};

// ─── small components ────────────────────────────────────────────────────────

// KPI value: split "$3.79K / $4.00K" so usage takes red at high utilization
// (>= warning threshold) or on full exhaustion. The limit always reads as
// muted reference text so the eye anchors on the moving number.
const KpiValue = ({ value, exhausted, progress }) => {
  const [usagePart, limitPart] = value.split(' / ');
  const highUtil = progress != null && progress >= UTIL_THRESHOLDS.warning;
  const usageRed = exhausted || highUtil;
  return (
    <Typography sx={{ fontSize: 'var(--ds-text-heading)', fontWeight: 'var(--ds-font-weight-semibold)', lineHeight: 1.25, whiteSpace: 'nowrap' }}>
      <span style={{ color: usageRed ? 'var(--ds-red-500)' : 'var(--ds-gray-700)' }}>{usagePart}</span>
      {limitPart && <span style={{ color: 'var(--ds-gray-500)', fontWeight: 'var(--ds-font-weight-medium)' }}> / {limitPart}</span>}
    </Typography>
  );
};

KpiValue.propTypes = {
  value: PropTypes.string.isRequired,
  exhausted: PropTypes.bool,
  progress: PropTypes.number,
};

// KPI tile: small uppercase label, big value with usage/limit split colors,
// optional sublabel, then a DS ProgressBar (tone derived from thresholds,
// not picked manually). Width of bar tracks the value text via inline-flex.
const KpiTile = ({ label, value, sublabel, progress, badge, exhausted, placeholder }) => (
  <Box
    sx={{
      flex: 1,
      minWidth: 0,
      p: ds.space[3],
      borderRadius: ds.radius.lg,
      backgroundColor: 'var(--ds-background-100)',
      border: `1px solid ${'var(--ds-gray-200)'}`,
    }}
  >
    <Box sx={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', mb: ds.space[1] }}>
      <Typography
        sx={{
          fontSize: 'var(--ds-text-caption)',
          color: 'var(--ds-gray-500)',
          textTransform: 'uppercase',
          fontWeight: 'var(--ds-font-weight-semibold)',
          letterSpacing: 0.4,
          whiteSpace: 'nowrap',
          overflow: 'hidden',
          textOverflow: 'ellipsis',
        }}
      >
        {label}
      </Typography>
      {badge}
    </Box>
    {placeholder ? (
      <Typography sx={{ fontSize: 'var(--ds-text-body)', fontStyle: 'italic', color: 'var(--ds-gray-500)', py: ds.space[0] }}>{value}</Typography>
    ) : (
      <Box sx={{ display: 'inline-flex', flexDirection: 'column', alignItems: 'stretch', maxWidth: '100%' }}>
        <KpiValue value={value} exhausted={exhausted} progress={progress} />
        {progress != null && (
          <Box sx={{ mt: ds.space.mul(0, 3) }}>
            <ProgressBar value={progress} thresholds={UTIL_THRESHOLDS} showValue={false} size='sm' />
          </Box>
        )}
      </Box>
    )}
    {sublabel && <Typography sx={{ fontSize: 'var(--ds-text-caption)', color: 'var(--ds-gray-500)', mt: ds.space[0] }}>{sublabel}</Typography>}
  </Box>
);

KpiTile.propTypes = {
  label: PropTypes.string.isRequired,
  value: PropTypes.string.isRequired,
  sublabel: PropTypes.string,
  progress: PropTypes.number,
  badge: PropTypes.node,
  exhausted: PropTypes.bool,
  placeholder: PropTypes.bool,
};

// ─── usage matrix (dense pivoted table) ──────────────────────────────────────

const COLS = [
  { key: 'monthly_cost', label: 'Monthly $', type: 'cost' },
  { key: 'daily_cost', label: 'Daily $', type: 'cost' },
  { key: 'monthly_count', label: 'Monthly #', type: 'count' },
  { key: 'daily_count', label: 'Daily #', type: 'count' },
];

const MODULE_KEYS = ['investigation', 'user_investigation'];

// Max usage % across all enabled limits within a (scope) — used for the
// row's left-edge tone (healthy / warn / error).
const scopeMaxPct = (scopeKey, budgetData, visibleLimits) => {
  let m = 0;
  for (const mod of MODULE_KEYS) {
    const info = budgetData?.[mod]?.[scopeKey];
    if (!info) continue;
    for (const { key } of visibleLimits) {
      const l = info[key];
      if (l?.enabled && l.limit > 0) {
        const p = (l.usage / l.limit) * 100;
        if (p > m) m = p;
      }
    }
  }
  return m;
};

// Left-edge tone indicator — neutral border for healthy rows; warning/error
// colors are surfaced via a 3px coloured edge so the page doesn't look like
// an emergency just because one cell is hot. The per-cell ProgressBar carries
// the precise per-limit signal — this is just the row-level summary.
//
// Mirrors the same thresholds as ProgressBar's deriveTone, but the edge is
// not a DS primitive yet, so we map manually.
const edgeColorForPct = (pct) => {
  if (pct >= UTIL_THRESHOLDS.warning) return 'var(--ds-red-500)';
  if (pct >= UTIL_THRESHOLDS.success) return 'var(--ds-amber-500)';
  return 'var(--ds-gray-200)';
};

// One vertical stripe inside a module cell: usage/limit on top, progress
// bar below. When the matrix only has one limit type visible globally, the
// label row is dropped (saves vertical space).
const LimitLine = ({ label, info, type }) => {
  if (!info?.enabled) {
    return <Typography sx={{ fontSize: 'var(--ds-text-small)', color: 'var(--ds-gray-500)' }}>—</Typography>;
  }
  const { limit, usage, remaining } = info;
  const pct = limit > 0 ? (usage / limit) * 100 : 0;
  const exhausted = limit > 0 && remaining != null && remaining <= 0;
  const fmt = type === 'cost' ? formatCurrency : formatCount;
  return (
    <Box>
      {label && (
        <Typography
          sx={{
            fontSize: 'var(--ds-text-caption)',
            color: 'var(--ds-gray-500)',
            textTransform: 'uppercase',
            fontWeight: 'var(--ds-font-weight-semibold)',
            letterSpacing: 0.3,
            mb: ds.space[0],
          }}
        >
          {label}
        </Typography>
      )}
      <Box sx={{ display: 'inline-flex', flexDirection: 'column', alignItems: 'stretch', maxWidth: '100%' }}>
        <Typography
          sx={{
            fontSize: 'var(--ds-text-small)',
            fontWeight: 'var(--ds-font-weight-medium)',
            color: exhausted || pct >= UTIL_THRESHOLDS.warning ? 'var(--ds-red-500)' : 'var(--ds-gray-700)',
            whiteSpace: 'nowrap',
          }}
        >
          {fmt(usage)} <span style={{ color: 'var(--ds-gray-500)', fontWeight: 'var(--ds-font-weight-regular)' }}>/ {fmt(limit)}</span>
        </Typography>
        <Box sx={{ mt: ds.space[1] }}>
          <ProgressBar value={pct} thresholds={UTIL_THRESHOLDS} showValue={false} size='sm' />
        </Box>
      </Box>
    </Box>
  );
};

LimitLine.propTypes = {
  label: PropTypes.string,
  info: PropTypes.object,
  type: PropTypes.oneOf(['cost', 'count']).isRequired,
};

// One cell of the pivoted matrix: stacks each visible limit type's LimitLine
// vertically. "No limits" when this (scope × module) has nothing enabled.
const ModuleCell = ({ info, visibleLimits, showLabels }) => {
  if (!info) {
    return <Typography sx={{ fontSize: 'var(--ds-text-caption)', color: 'var(--ds-gray-500)', textAlign: 'center' }}>—</Typography>;
  }
  const anyEnabled = visibleLimits.some((l) => info[l.key]?.enabled);
  if (!anyEnabled) {
    return <Typography sx={{ fontSize: 'var(--ds-text-caption)', color: 'var(--ds-gray-500)', fontStyle: 'italic' }}>No limits</Typography>;
  }
  return (
    <Box sx={{ display: 'flex', flexDirection: 'column', gap: ds.space.mul(0, 3) }}>
      {visibleLimits.map((l) => (
        <LimitLine key={l.key} label={showLabels ? l.label : undefined} info={info[l.key]} type={l.type} />
      ))}
    </Box>
  );
};

ModuleCell.propTypes = {
  info: PropTypes.object,
  visibleLimits: PropTypes.array.isRequired,
  showLabels: PropTypes.bool.isRequired,
};

// Pivoted matrix: 2 rows (Tenant / Account), 2 columns (Event Analysis /
// {assistantName} Chat). Each cell stacks its enabled limit lines. Halves
// the row count vs. the previous scope×module layout while keeping the
// per-module breakdown visible — totals already live in the KPI strip.
const UsageMatrix = ({ budgetData, tenantName, accountName, assistantName }) => {
  const moduleLabels = getModuleLabels(assistantName);
  const scopes = [
    { key: 'tenant', label: 'Tenant', name: tenantName },
    { key: 'account', label: 'Account', name: accountName },
  ];

  // The cross-product (cols × modules × scopes) is cheap, but only `budgetData`
  // varies between renders — memoize so we don't re-traverse on parent renders
  // that don't touch the budget shape (KPI tile hover, banner expand, etc.).
  // `scopes` is closed over from props but its only used field here is the
  // boolean `.enabled` deep inside `budgetData`; not adding it to deps avoids
  // a stale closure since scope keys ('tenant'/'account') are constants.
  const visibleLimits = useMemo(
    () => COLS.filter(({ key }) => MODULE_KEYS.some((mod) => scopes.some((s) => budgetData?.[mod]?.[s.key]?.[key]?.enabled))),
    // eslint-disable-next-line react-hooks/exhaustive-deps
    [budgetData]
  );
  const showLabels = visibleLimits.length > 1;
  const gridTemplate = `${ds.space.mul(0, 85)} ${MODULE_KEYS.map(() => '1fr').join(' ')}`.trim();

  return (
    <Box
      sx={{
        borderRadius: ds.radius.lg,
        border: `1px solid ${'var(--ds-gray-200)'}`,
        backgroundColor: 'var(--ds-background-100)',
        p: ds.space[3],
      }}
    >
      {/* Header */}
      <Box
        sx={{
          display: 'grid',
          gridTemplateColumns: gridTemplate,
          alignItems: 'center',
          gap: ds.space[3],
          px: ds.space[3],
          pb: ds.space[2],
          mb: ds.space[1],
          borderBottom: `1px solid ${'var(--ds-gray-200)'}`,
        }}
      >
        <Typography sx={HEADER_CELL_SX}>Scope</Typography>
        {MODULE_KEYS.map((mod) => (
          <Typography key={mod} sx={HEADER_CELL_SX}>
            {moduleLabels[mod]}
          </Typography>
        ))}
      </Box>
      {/* One row per scope */}
      {scopes.map((scope) => {
        const edgeColor = edgeColorForPct(scopeMaxPct(scope.key, budgetData, visibleLimits));
        return (
          <Box
            key={scope.key}
            sx={{
              display: 'grid',
              gridTemplateColumns: gridTemplate,
              alignItems: 'center',
              gap: ds.space[3],
              pl: ds.space.mul(0, 5),
              pr: ds.space[3],
              py: ds.space.mul(0, 3),
              borderLeft: `3px solid ${edgeColor}`,
              borderRadius: ds.radius.sm,
              '&:not(:last-of-type)': { mb: ds.space[1] },
            }}
          >
            <Box>
              <Typography
                sx={{
                  fontSize: 'var(--ds-text-caption)',
                  color: 'var(--ds-gray-500)',
                  textTransform: 'uppercase',
                  fontWeight: 'var(--ds-font-weight-semibold)',
                  letterSpacing: 0.3,
                }}
              >
                {scope.label}
              </Typography>
              <Typography
                sx={{
                  fontSize: 'var(--ds-text-small)',
                  fontWeight: 'var(--ds-font-weight-medium)',
                  color: 'var(--ds-gray-700)',
                  whiteSpace: 'nowrap',
                  overflow: 'hidden',
                  textOverflow: 'ellipsis',
                }}
              >
                {scope.name}
              </Typography>
            </Box>
            {MODULE_KEYS.map((mod) => (
              <ModuleCell key={mod} info={budgetData?.[mod]?.[scope.key]} visibleLimits={visibleLimits} showLabels={showLabels} />
            ))}
          </Box>
        );
      })}
    </Box>
  );
};

UsageMatrix.propTypes = {
  budgetData: PropTypes.object.isRequired,
  tenantName: PropTypes.string.isRequired,
  accountName: PropTypes.string.isRequired,
  assistantName: PropTypes.string.isRequired,
};

const HEADER_CELL_SX = {
  fontSize: 'var(--ds-text-caption)',
  fontWeight: 'var(--ds-font-weight-semibold)',
  color: 'var(--ds-gray-500)',
  textTransform: 'uppercase',
  letterSpacing: 0.4,
};

// ─── Budget Config Edit Modal (unchanged from prior implementation) ──────────

const BudgetEditModal = ({ open, onClose, onSaved, config, maxCaps, systemDefaults, existingConfigs = [] }) => {
  const { assistantName } = useTenantBranding();
  const moduleLabels = getModuleLabels(assistantName);
  const isEdit = !!config;
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState('');
  const [accounts, setAccounts] = useState([]);

  const session = getUserSession();
  // Stored as `tenantId` because the form uses the tenant's name as the
  // entity_id stand-in (see conflict-detection comment below).
  const tenantId = session?.tenant?.name || '';
  const isSuperAdmin = !!session?.isSuperAdmin;

  const [entityType, setEntityType] = useState('tenant');
  const [entityId, setEntityId] = useState(tenantId);
  const [module, setModule] = useState('both');
  const [budgetDisabled, setBudgetDisabled] = useState(false);

  const [monthlyCostEnabled, setMonthlyCostEnabled] = useState(true);
  const [monthlyCostLimit, setMonthlyCostLimit] = useState('');
  const [dailyCostEnabled, setDailyCostEnabled] = useState(false);
  const [dailyCostLimit, setDailyCostLimit] = useState('');
  const [monthlyCountEnabled, setMonthlyCountEnabled] = useState(false);
  const [monthlyCountLimit, setMonthlyCountLimit] = useState('');
  const [dailyCountEnabled, setDailyCountEnabled] = useState(false);
  const [dailyCountLimit, setDailyCountLimit] = useState('');

  // Modules that already have a config for the current scope. When creating
  // (not editing), we refuse to overwrite — the user must edit from Active
  // Configurations instead.
  //
  // Tenant scope: match on (entity_type === 'tenant') only. The form's
  // `entityId` is session.tenant.tenant.name, but the backend may store
  // entity_id differently for tenant configs (e.g. a tenant UUID). There's
  // only ONE tenant in this user's context, so any tenant-scoped config for
  // the same module IS a conflict.
  //
  // Account scope: must match entity_id exactly (account UUID).
  // Modal re-renders on every limit-field keystroke; memoize so the nested
  // filter over existingConfigs doesn't re-traverse needlessly.
  const conflictingModules = useMemo(() => {
    if (isEdit) return [];
    if (entityType === 'account' && !entityId) return [];
    const targetModules = module === 'both' ? ['investigation', 'user_investigation'] : [module];
    return targetModules.filter((mod) =>
      existingConfigs.some((c) => {
        if (c.entity_type !== entityType) return false;
        if (c.module !== mod) return false;
        if (entityType === 'account') return c.entity_id === entityId;
        return true;
      })
    );
  }, [isEdit, entityType, entityId, module, existingConfigs]);
  const hasConflict = conflictingModules.length > 0;

  useEffect(() => {
    if (open) {
      apiUser.listAccounts().then((data) => {
        if (Array.isArray(data)) setAccounts(data);
      });
    }
  }, [open]);

  useEffect(() => {
    if (!isEdit) {
      if (entityType === 'tenant') setEntityId(tenantId);
      else setEntityId('');
    }
  }, [entityType, isEdit, tenantId]);

  // Reset form to defaults whenever the (entityType, entityId, module)
  // selection changes in create mode. We no longer pre-fill from an existing
  // config because creating-while-existing is blocked outright.
  useEffect(() => {
    if (!isEdit && open) {
      setMonthlyCostEnabled(true);
      setMonthlyCostLimit('');
      setDailyCostEnabled(false);
      setDailyCostLimit('');
      setMonthlyCountEnabled(false);
      setMonthlyCountLimit('');
      setDailyCountEnabled(false);
      setDailyCountLimit('');
      setBudgetDisabled(false);
    }
  }, [entityType, entityId, module, isEdit, open]);

  useEffect(() => {
    if (open && config) {
      setEntityType(config.entity_type);
      setEntityId(config.entity_id);
      setModule(config.module);
      setBudgetDisabled(config.budget_disabled);
      setMonthlyCostEnabled(config.monthly_cost_enabled);
      setMonthlyCostLimit(config.monthly_cost_limit != null ? String(config.monthly_cost_limit) : '');
      setDailyCostEnabled(config.daily_cost_enabled);
      setDailyCostLimit(config.daily_cost_limit != null ? String(config.daily_cost_limit) : '');
      setMonthlyCountEnabled(config.monthly_count_enabled);
      setMonthlyCountLimit(config.monthly_count_limit != null ? String(config.monthly_count_limit) : '');
      setDailyCountEnabled(config.daily_count_enabled);
      setDailyCountLimit(config.daily_count_limit != null ? String(config.daily_count_limit) : '');
    } else if (open) {
      setEntityType('tenant');
      setEntityId(tenantId);
      setModule('both');
      setBudgetDisabled(false);
      setMonthlyCostEnabled(true);
      setMonthlyCostLimit('');
      setDailyCostEnabled(false);
      setDailyCostLimit('');
      setMonthlyCountEnabled(false);
      setMonthlyCountLimit('');
      setDailyCountEnabled(false);
      setDailyCountLimit('');
    }
    setError('');
  }, [open, config, tenantId]);

  const getMaxCap = (field) => {
    if (!maxCaps) return undefined;
    if (field === 'monthly_cost') return entityType === 'tenant' ? maxCaps.monthly_cost_tenant : maxCaps.monthly_cost_account;
    if (field === 'daily_cost') return entityType === 'tenant' ? maxCaps.daily_cost_tenant : maxCaps.daily_cost_account;
    if (field === 'monthly_count') return maxCaps.monthly_count;
    if (field === 'daily_count') return maxCaps.daily_count;
    return undefined;
  };

  const validateLimits = () => {
    const checks = [
      { enabled: monthlyCostEnabled, value: monthlyCostLimit, label: 'Monthly Cost', cap: getMaxCap('monthly_cost'), isCount: false },
      { enabled: dailyCostEnabled, value: dailyCostLimit, label: 'Daily Cost', cap: getMaxCap('daily_cost'), isCount: false },
      { enabled: monthlyCountEnabled, value: monthlyCountLimit, label: 'Monthly Count', cap: getMaxCap('monthly_count'), isCount: true },
      { enabled: dailyCountEnabled, value: dailyCountLimit, label: 'Daily Count', cap: getMaxCap('daily_count'), isCount: true },
    ];
    for (const c of checks) {
      if (!c.enabled || c.value === '') continue;
      const num = c.isCount ? parseInt(c.value, 10) : parseFloat(c.value);
      if (isNaN(num)) return `${c.label}: invalid number`;
      if (num < 0) return `${c.label}: cannot be negative`;
      if (c.cap !== undefined && num > c.cap) return `${c.label}: exceeds max (${c.cap})`;
    }
    return null;
  };

  const handleSave = async () => {
    if (!entityId.trim()) {
      setError('Please select an entity');
      return;
    }

    // Hard-block when the selected (entity, module) already has a config —
    // the user must edit it from Active Budgets instead.
    if (hasConflict) {
      const conflictLabels = conflictingModules.map((m) => moduleLabels[m] || m).join(' and ');
      setError(
        `A budget already exists for ${conflictLabels} on this ${entityType}. ` +
          'Edit the existing budget from the Active Budgets section instead of adding a new one.'
      );
      return;
    }

    const validationError = validateLimits();
    if (validationError) {
      setError(validationError);
      return;
    }

    const buildRequest = (mod) => {
      const req = {
        entity_type: entityType,
        entity_id: entityId.trim(),
        module: mod,
        monthly_cost_enabled: monthlyCostEnabled,
        daily_cost_enabled: dailyCostEnabled,
        monthly_count_enabled: monthlyCountEnabled,
        daily_count_enabled: dailyCountEnabled,
      };
      if (isSuperAdmin) req.budget_disabled = budgetDisabled;
      if (monthlyCostLimit !== '') req.monthly_cost_limit = parseFloat(monthlyCostLimit);
      if (dailyCostLimit !== '') req.daily_cost_limit = parseFloat(dailyCostLimit);
      if (monthlyCountLimit !== '') req.monthly_count_limit = parseInt(monthlyCountLimit, 10);
      if (dailyCountLimit !== '') req.daily_count_limit = parseInt(dailyCountLimit, 10);
      return req;
    };

    const modules = module === 'both' ? ['investigation', 'user_investigation'] : [module];

    setLoading(true);
    setError('');
    try {
      const saved = [];
      for (const mod of modules) {
        const res = await apiBudget.upsertBudgetConfig(buildRequest(mod));
        if (res.errors?.length > 0) {
          const failedLabel = moduleLabels[mod] || mod;
          if (saved.length > 0) {
            setError(`Saved ${saved.join(', ')} but failed on ${failedLabel}: ${res.errors[0]?.message}`);
          } else {
            setError(res.errors[0]?.message || `Failed to save ${failedLabel}`);
          }
          onSaved();
          return;
        }
        saved.push(moduleLabels[mod] || mod);
      }
      snackbar.success(modules.length > 1 ? 'Budget configurations saved for both modules' : 'Budget configuration saved');
      onSaved();
      onClose();
    } catch (e) {
      setError(e?.message || 'Failed to save');
    } finally {
      setLoading(false);
    }
  };

  const getSystemDefault = (field) => {
    if (!systemDefaults) return null;
    const level = systemDefaults[entityType];
    if (!level) return null;
    const mod = module === 'both' ? 'investigation' : module;
    const modDefaults = level[mod];
    if (!modDefaults) return null;
    const keyMap = {
      monthly_cost: 'monthly_cost_limit',
      daily_cost: 'daily_cost_limit',
      monthly_count: 'monthly_count_limit',
      daily_count: 'daily_count_limit',
    };
    return modDefaults[keyMap[field]];
  };

  const renderLimitRow = (label, enabled, setEnabled, value, setValue, maxCapField, type) => {
    const cap = getMaxCap(maxCapField);
    const sysDefault = getSystemDefault(maxCapField);
    return (
      <Box sx={{ mb: ds.space[3] }}>
        <Box sx={{ display: 'flex', alignItems: 'center', gap: ds.space[4] }}>
          <Box sx={{ minWidth: ds.space.mul(0, 100) }}>
            <Switch
              size='sm'
              checked={enabled}
              onChange={(_e, next) => setEnabled(next)}
              label={
                <Typography
                  sx={{
                    fontSize: 'var(--ds-text-body)',
                    fontWeight: 'var(--ds-font-weight-medium)',
                    minWidth: ds.space.mul(0, 60),
                    color: 'var(--ds-gray-700)',
                  }}
                >
                  {label}
                </Typography>
              }
            />
          </Box>
          <Box sx={{ width: ds.space.mul(0, 65) }}>
            <Input
              size='sm'
              type='number'
              value={value}
              onChange={(next) => setValue(next)}
              disabled={!enabled}
              placeholder={type === 'cost' ? 'USD' : 'Count'}
              inputMode='numeric'
            />
          </Box>
          {cap !== undefined && (
            <Typography sx={{ fontSize: 'var(--ds-text-caption)', color: 'var(--ds-gray-500)' }}>
              max: {type === 'cost' ? `$${cap.toLocaleString()}` : cap.toLocaleString()}
            </Typography>
          )}
          {!enabled && sysDefault != null && sysDefault > 0 && (
            <Typography sx={{ fontSize: 'var(--ds-text-caption)', color: 'var(--ds-gray-500)' }}>
              · System default will apply: {type === 'cost' ? formatCurrency(sysDefault) : sysDefault}
            </Typography>
          )}
        </Box>
      </Box>
    );
  };

  const modalTitle = isEdit ? `Edit Budget - ${moduleLabels[config?.module] || ''}` : 'Create Budget Configuration';

  const accountOptions = accounts.map((acc) => ({ value: acc.id, label: `${acc.account_name} (${acc.cloud_provider})` }));

  return (
    <Modal
      width='md'
      title={modalTitle}
      open={open}
      handleClose={onClose}
      onClose={onClose}
      actionButtons={
        <Box sx={{ display: 'flex', justifyContent: 'flex-end', gap: ds.space[3], p: `${ds.space[3]} ${ds.space[5]}` }}>
          <Button tone='secondary' size='md' onClick={onClose} disabled={loading}>
            Cancel
          </Button>
          <Button tone='primary' size='md' onClick={handleSave} loading={loading} disabled={hasConflict}>
            {isEdit ? 'Save Changes' : 'Create'}
          </Button>
        </Box>
      }
    >
      <Box sx={{ p: ds.space[2] }}>
        {error && (
          <Alert severity='error' sx={{ mb: ds.space[4] }}>
            {error}
          </Alert>
        )}

        {hasConflict && (
          <Alert severity='warning' sx={{ mb: ds.space[4], fontSize: 'var(--ds-text-caption)' }}>
            A budget already exists for {conflictingModules.map((m) => moduleLabels[m] || m).join(' and ')} on this {entityType}. Edit the existing
            budget from the Active Budgets section instead of adding a new one.
          </Alert>
        )}

        <Box sx={{ display: 'flex', gap: 2, mb: ds.space[4] }}>
          <Box sx={{ flex: 1 }}>
            <Box sx={{ mb: ds.space[4], width: ds.space.mul(0, 130) }}>
              <Select
                label='Scope'
                value={entityType}
                onChange={(next) => setEntityType(next)}
                options={[
                  { value: 'tenant', label: 'Tenant' },
                  { value: 'account', label: 'Account' },
                ]}
                disabled={isEdit}
                size='sm'
              />
            </Box>
          </Box>
          <Box sx={{ flex: 2 }}>
            {entityType === 'tenant' ? (
              <Input size='sm' label='Tenant' value='Current Tenant' disabled onChange={() => {}} />
            ) : (
              <Select
                label='Account'
                value={entityId}
                onChange={(next) => setEntityId(next)}
                options={accountOptions}
                placeholder='Select an account'
                disabled={isEdit}
                size='sm'
              />
            )}
          </Box>
        </Box>

        <Box sx={{ mb: ds.space[4], width: ds.space.mul(0, 130) }}>
          <Select
            label='Apply to'
            value={module}
            onChange={(next) => setModule(next)}
            options={[
              ...(isEdit ? [] : [{ value: 'both', label: 'Both Modules' }]),
              { value: 'investigation', label: 'Event Analysis (Automated)' },
              { value: 'user_investigation', label: 'Nubi Chat (User)' },
            ]}
            disabled={isEdit}
            size='sm'
          />
        </Box>

        {isSuperAdmin && (
          <Box
            sx={{
              mb: ds.space[4],
              p: ds.space[3],
              borderRadius: ds.radius.md,
              border: `1px solid ${budgetDisabled ? 'var(--ds-red-500)' : 'var(--ds-blue-500)'}`,
              backgroundColor: budgetDisabled ? 'var(--ds-red-100)' : 'transparent',
            }}
          >
            <Switch
              checked={budgetDisabled}
              onChange={(_e, next) => setBudgetDisabled(next)}
              label={
                <Typography
                  sx={{
                    fontSize: 'var(--ds-text-body)',
                    fontWeight: 'var(--ds-font-weight-semibold)',
                    color: budgetDisabled ? 'var(--ds-red-600)' : 'var(--ds-gray-700)',
                  }}
                >
                  Disable All Budget Checks
                </Typography>
              }
              description='Bypasses all cost and count limits. Use with caution.'
            />
          </Box>
        )}

        <Divider sx={{ my: 'var(--ds-space-4)' }} />
        <Typography
          sx={{
            fontSize: 'var(--ds-text-body-lg)',
            fontWeight: 'var(--ds-font-weight-semibold)',
            color: 'var(--ds-gray-700)',
            mb: ds.space[3],
            fontFamily: 'var(--ds-font-display)',
          }}
        >
          Cost Limits (USD)
        </Typography>
        {renderLimitRow('Monthly Cost', monthlyCostEnabled, setMonthlyCostEnabled, monthlyCostLimit, setMonthlyCostLimit, 'monthly_cost', 'cost')}
        {renderLimitRow('Daily Cost', dailyCostEnabled, setDailyCostEnabled, dailyCostLimit, setDailyCostLimit, 'daily_cost', 'cost')}

        <Divider sx={{ my: 'var(--ds-space-4)' }} />
        <Typography
          sx={{
            fontSize: 'var(--ds-text-body-lg)',
            fontWeight: 'var(--ds-font-weight-semibold)',
            color: 'var(--ds-gray-700)',
            mb: ds.space[3],
            fontFamily: 'var(--ds-font-display)',
          }}
        >
          Count Limits (Conversations)
        </Typography>
        {renderLimitRow(
          'Monthly Count',
          monthlyCountEnabled,
          setMonthlyCountEnabled,
          monthlyCountLimit,
          setMonthlyCountLimit,
          'monthly_count',
          'count'
        )}
        {renderLimitRow('Daily Count', dailyCountEnabled, setDailyCountEnabled, dailyCountLimit, setDailyCountLimit, 'daily_count', 'count')}
      </Box>
    </Modal>
  );
};

BudgetEditModal.propTypes = {
  open: PropTypes.bool.isRequired,
  onClose: PropTypes.func.isRequired,
  onSaved: PropTypes.func.isRequired,
  config: PropTypes.object,
  maxCaps: PropTypes.object,
  systemDefaults: PropTypes.object,
  existingConfigs: PropTypes.array,
};

// ─── Active configs compact view (one row per scope, modules side-by-side) ───

// One dense chip showing the limits set on a single (scope × module) config,
// with inline edit/delete. Used by ActiveConfigsCompact so both modules of a
// scope share a single horizontal row.
const ActiveConfigChip = ({ moduleLabel, cfg, onEdit, onDelete }) => {
  if (!cfg) {
    return (
      <Box
        sx={{
          py: ds.space.mul(0, 3),
          px: ds.space[2],
          borderRadius: ds.radius.md,
          border: `1px dashed ${'var(--ds-gray-200)'}`,
          backgroundColor: 'transparent',
          minHeight: ds.space[6],
          display: 'flex',
          alignItems: 'center',
          gap: ds.space.mul(0, 3),
        }}
      >
        <Typography
          sx={{ fontSize: 'var(--ds-text-small)', fontWeight: 'var(--ds-font-weight-medium)', color: 'var(--ds-gray-500)', whiteSpace: 'nowrap' }}
        >
          {moduleLabel}
        </Typography>
        <Typography sx={{ fontSize: 'var(--ds-text-caption)', color: 'var(--ds-gray-500)', fontStyle: 'italic' }}>system defaults</Typography>
      </Box>
    );
  }
  const limits = [];
  if (!cfg.budget_disabled) {
    if (cfg.monthly_cost_enabled) limits.push(`Monthly Cost: ${formatCurrency(cfg.monthly_cost_limit)}`);
    if (cfg.daily_cost_enabled) limits.push(`Daily Cost: ${formatCurrency(cfg.daily_cost_limit)}`);
    if (cfg.monthly_count_enabled) limits.push(`Monthly Count: ${cfg.monthly_count_limit ?? 'default'}`);
    if (cfg.daily_count_enabled) limits.push(`Daily Count: ${cfg.daily_count_limit ?? 'default'}`);
  }
  return (
    <Box
      sx={{
        py: ds.space[1],
        pl: ds.space[2],
        pr: ds.space[1],
        borderRadius: ds.radius.md,
        border: `1px solid ${'var(--ds-gray-200)'}`,
        backgroundColor: 'var(--ds-background-100)',
        display: 'flex',
        alignItems: 'center',
        gap: ds.space.mul(0, 3),
        minHeight: ds.space[6],
      }}
    >
      <Typography
        sx={{ fontSize: 'var(--ds-text-small)', fontWeight: 'var(--ds-font-weight-medium)', color: 'var(--ds-gray-700)', whiteSpace: 'nowrap' }}
      >
        {moduleLabel}
      </Typography>
      <Box sx={{ flex: 1, minWidth: 0, display: 'flex', alignItems: 'center', gap: ds.space[1], flexWrap: 'wrap' }}>
        {cfg.budget_disabled ? (
          <Chip tone='critical' size='2xs' shape='rect' solid>
            ALL DISABLED
          </Chip>
        ) : limits.length > 0 ? (
          limits.map((l) => (
            <Chip key={l} tone='info' size='2xs' variant='tag'>
              {l}
            </Chip>
          ))
        ) : (
          <Typography sx={{ fontSize: 'var(--ds-text-caption)', color: 'var(--ds-gray-500)', fontStyle: 'italic' }}>no limits enabled</Typography>
        )}
      </Box>
      <Tooltip title='Edit'>
        <Button
          tone='ghost'
          size='sm'
          icon={<EditIcon sx={{ fontSize: 'var(--ds-text-body-lg)', color: 'var(--ds-gray-700)' }} />}
          onClick={() => onEdit(cfg)}
          aria-label='Edit budget config'
        />
      </Tooltip>
      <Tooltip title='Delete (revert to defaults)'>
        <Button
          tone='ghost'
          size='sm'
          icon={<SafeIcon alt='delete icon' src={DeleteIcon} height='14' width='14' />}
          onClick={() => onDelete(cfg)}
          aria-label='Delete budget config'
        />
      </Tooltip>
    </Box>
  );
};

ActiveConfigChip.propTypes = {
  moduleLabel: PropTypes.string.isRequired,
  cfg: PropTypes.object,
  onEdit: PropTypes.func.isRequired,
  onDelete: PropTypes.func.isRequired,
};

// Generic compact configs renderer. Takes a list of scopes (each with a
// stable key, a label like "Tenant"/"Account", a display name, and its
// configs). Renders one row per non-empty scope, each row showing both
// modules' configs in a 2-column grid. Used by both the Active Configurations
// section (Tenant + current account) and the Other Account Configurations
// section (one row per other account).
const ActiveConfigsCompact = ({ scopes, assistantName, onEdit, onDelete, emptyMessage }) => {
  const moduleLabels = getModuleLabels(assistantName);

  const renderScopeRow = ({ key, label, name, configs }) => {
    if (!configs || configs.length === 0) return null;
    const byModule = {
      investigation: configs.find((c) => c.module === 'investigation'),
      user_investigation: configs.find((c) => c.module === 'user_investigation'),
    };
    return (
      <Box key={key} sx={{ '&:not(:last-of-type)': { mb: ds.space[4] } }}>
        <Stack direction='row' alignItems='baseline' spacing={1} sx={{ mb: ds.space.mul(0, 3) }}>
          <Typography
            sx={{
              fontSize: 'var(--ds-text-caption)',
              color: 'var(--ds-gray-500)',
              textTransform: 'uppercase',
              fontWeight: 'var(--ds-font-weight-semibold)',
              letterSpacing: 0.3,
            }}
          >
            {label}
          </Typography>
          <Typography sx={{ fontSize: 'var(--ds-text-small)', fontWeight: 'var(--ds-font-weight-medium)', color: 'var(--ds-gray-700)' }}>
            {name}
          </Typography>
        </Stack>
        <Box sx={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: ds.space[2] }}>
          {MODULE_KEYS.map((mod) => (
            <ActiveConfigChip key={mod} moduleLabel={moduleLabels[mod]} cfg={byModule[mod]} onEdit={onEdit} onDelete={onDelete} />
          ))}
        </Box>
      </Box>
    );
  };

  const hasAny = scopes.some((s) => s.configs && s.configs.length > 0);
  if (!hasAny) {
    return emptyMessage ? (
      <Typography sx={{ fontSize: 'var(--ds-text-small)', color: 'var(--ds-gray-500)', py: ds.space[3] }}>{emptyMessage}</Typography>
    ) : null;
  }

  return <Box>{scopes.map(renderScopeRow)}</Box>;
};

ActiveConfigsCompact.propTypes = {
  scopes: PropTypes.arrayOf(
    PropTypes.shape({
      key: PropTypes.string.isRequired,
      label: PropTypes.string.isRequired,
      name: PropTypes.string.isRequired,
      configs: PropTypes.array.isRequired,
    })
  ).isRequired,
  assistantName: PropTypes.string.isRequired,
  onEdit: PropTypes.func.isRequired,
  onDelete: PropTypes.func.isRequired,
  emptyMessage: PropTypes.string,
};

// ─── main component ──────────────────────────────────────────────────────────

const LLMConsumptionTab = ({ accountId }) => {
  const { assistantName } = useTenantBranding();
  const [loading, setLoading] = useState(true);
  const [budgetData, setBudgetData] = useState(null);
  const [error, setError] = useState(null);
  const [refreshKey, setRefreshKey] = useState(0);
  const [accountName, setAccountName] = useState('');

  // Lifted budget-config state — top-level "Add Budget" button and the
  // collapsed list section both feed this single modal.
  const [configs, setConfigs] = useState([]);
  const [configsLoading, setConfigsLoading] = useState(true);
  const [accounts, setAccounts] = useState([]);
  const [maxCaps, setMaxCaps] = useState(null);
  const [systemDefaults, setSystemDefaults] = useState(null);
  const [modalOpen, setModalOpen] = useState(false);
  const [editConfig, setEditConfig] = useState(null);
  const [deleteConfirm, setDeleteConfirm] = useState(null);
  const [configsOpen, setConfigsOpen] = useState(false);
  // Pre-dialog confirmation when the user already has budgets at BOTH tenant
  // and current-account scope. Nudges them toward editing existing rows
  // instead of stacking new conflicting ones.
  const [existingWarningOpen, setExistingWarningOpen] = useState(false);

  const session = getUserSession();
  const isSuperAdmin = !!session?.isSuperAdmin;
  const isAdmin = isTenantAdmin() || isSuperAdmin;
  const tenantName = session?.tenant?.name || 'Tenant';

  // Account list is essentially static for the session — fetch it ONCE on
  // mount and reuse from every consumer (budget-status effect, loadConfigs,
  // BudgetEditModal). Avoids the previous pattern of re-fetching on every
  // accountId / refreshKey change and on every loadConfigs invocation.
  useEffect(() => {
    let cancelled = false;
    apiUser.listAccounts().then((data) => {
      if (!cancelled && Array.isArray(data)) setAccounts(data);
    });
    return () => {
      cancelled = true;
    };
  }, []);

  // Derive the friendly account-name label from the cached accounts list.
  // Falls back to empty string until accounts have loaded; the budget-status
  // effect doesn't need to wait for accounts to finish before fetching.
  useEffect(() => {
    if (!accountId || accounts.length === 0) return;
    const acc = accounts.find((a) => a.id === accountId);
    if (acc) setAccountName(`${acc.account_name} (${acc.cloud_provider})`);
  }, [accountId, accounts]);

  useEffect(() => {
    const fetchBudgetStatus = async () => {
      if (!accountId) {
        setError('Account ID is required');
        setLoading(false);
        return;
      }
      try {
        setLoading(true);
        const response = await apiBudget.getBudgetStatus(accountId);
        if (response.errors && response.errors.length > 0) {
          setError('Failed to fetch budget status');
          snackbar.error('Failed to fetch budget status');
        } else if (response.data) {
          setBudgetData(response.data);
        } else {
          setError('No data available');
        }
      } catch {
        setError('An error occurred while fetching budget status');
        snackbar.error('An error occurred while fetching budget status');
      } finally {
        setLoading(false);
      }
    };

    fetchBudgetStatus();
  }, [accountId, refreshKey]);

  const loadConfigs = useCallback(async () => {
    if (!isAdmin) return;
    setConfigsLoading(true);
    try {
      const [tenantRes, accountRes, defaultsRes] = await Promise.all([
        apiBudget.listBudgetConfigs('tenant'),
        apiBudget.listBudgetConfigs('account'),
        apiBudget.getSystemDefaults(),
      ]);
      const apiErrors = tenantRes.errors || accountRes.errors || defaultsRes.errors;
      if (apiErrors) {
        const msg = Array.isArray(apiErrors) ? apiErrors[0]?.message : 'Failed to load configurations';
        snackbar.error(msg || 'Failed to load configurations');
      }
      const allConfigs = [...(tenantRes.data || []), ...(accountRes.data || [])];
      setConfigs(allConfigs);
      if (defaultsRes.data?.max_caps) setMaxCaps(defaultsRes.data.max_caps);
      if (defaultsRes.data?.defaults) setSystemDefaults(defaultsRes.data.defaults);
    } catch {
      snackbar.error('Failed to load budget configurations');
    } finally {
      setConfigsLoading(false);
    }
  }, [isAdmin]);

  useEffect(() => {
    loadConfigs();
  }, [loadConfigs]);

  const handleDeleteConfirmed = async () => {
    const cfg = deleteConfirm;
    setDeleteConfirm(null);
    if (!cfg) return;
    try {
      const res = await apiBudget.deleteBudgetConfig(cfg.id);
      if (res.errors?.length > 0) {
        snackbar.error(res.errors[0]?.message || 'Failed to delete');
      } else {
        snackbar.success('Budget config deleted — system defaults will apply');
        loadConfigs();
        setRefreshKey((k) => k + 1);
      }
    } catch {
      snackbar.error('Failed to delete budget config');
    }
  };

  // Opens the Add Budget dialog directly — bypasses the existing-budgets
  // warning. Shared by the warning popup's "Continue" action and the
  // unconditional path when no warning is needed.
  const proceedToAddBudget = () => {
    setExistingWarningOpen(false);
    setEditConfig(null);
    setModalOpen(true);
    setConfigsOpen(true);
  };

  const openManage = () => {
    // If the tenant AND the current account already have budgets, surface a
    // pre-dialog nudge: most "Add Budget" clicks here will hit the save-time
    // conflict guard once the user picks a scope+module. Catching it before
    // the form saves a wasted edit.
    if (tenantActiveConfigs.length > 0 && accountActiveConfigs.length > 0) {
      setExistingWarningOpen(true);
      return;
    }
    proceedToAddBudget();
  };

  const kpi = useMemo(() => (budgetData ? computeKpi(budgetData) : null), [budgetData]);

  // Split configs so the ones that affect THIS view (tenant + current
  // account) stay always-visible, and unrelated other-account configs hide
  // behind a small toggle.
  const tenantActiveConfigs = useMemo(() => configs.filter((c) => c.entity_type === 'tenant'), [configs]);
  const accountActiveConfigs = useMemo(() => configs.filter((c) => c.entity_type === 'account' && c.entity_id === accountId), [configs, accountId]);
  const otherConfigs = useMemo(() => configs.filter((c) => c.entity_type === 'account' && c.entity_id !== accountId), [configs, accountId]);

  // Scope arrays for ActiveConfigsCompact. Active = tenant + current account.
  // Other-account section = one scope per non-current account that has configs.
  const activeScopes = useMemo(
    () => [
      { key: 'tenant', label: 'Tenant', name: tenantName, configs: tenantActiveConfigs },
      { key: `account-${accountId}`, label: 'Account', name: accountName || accountId, configs: accountActiveConfigs },
    ],
    [tenantName, accountName, accountId, tenantActiveConfigs, accountActiveConfigs]
  );

  const otherAccountScopes = useMemo(() => {
    const byAccount = {};
    for (const cfg of otherConfigs) {
      if (!byAccount[cfg.entity_id]) byAccount[cfg.entity_id] = [];
      byAccount[cfg.entity_id].push(cfg);
    }
    return Object.entries(byAccount).map(([id, cfgs]) => {
      const acc = accounts.find((a) => a.id === id);
      return {
        key: `account-${id}`,
        label: 'Account',
        name: acc ? `${acc.account_name} (${acc.cloud_provider})` : id,
        configs: cfgs,
      };
    });
  }, [otherConfigs, accounts]);

  if (loading) {
    return (
      <Box sx={{ display: 'flex', justifyContent: 'center', alignItems: 'center', minHeight: ds.space.mul(1, 75) }}>
        <Loader />
      </Box>
    );
  }

  if (error) {
    return (
      <Box sx={{ p: ds.space[5] }}>
        <Typography sx={{ fontSize: 'var(--ds-text-body)', color: 'var(--ds-red-600)' }}>{error}</Typography>
      </Box>
    );
  }

  if (!budgetData) {
    return (
      <Box sx={{ p: ds.space[5] }}>
        <Typography sx={{ fontSize: 'var(--ds-text-body)', color: 'var(--ds-gray-500)' }}>No budget data available</Typography>
      </Box>
    );
  }

  const { period, today } = budgetData;

  // Build per-tile KPI props. When the source aggregate is null (no enabled
  // limit), render a placeholder tile ("No limit configured") instead of a
  // bare em-dash that reads as "broken".
  const buildKpi = (info, fmt) =>
    info
      ? {
          value: `${fmt(info.usage)} / ${fmt(info.limit)}`,
          progress: info.limit > 0 ? (info.usage / info.limit) * 100 : 0,
          exhausted: info.limit > 0 && info.remaining != null && info.remaining <= 0,
          placeholder: false,
        }
      : { value: 'No limit configured', progress: null, exhausted: false, placeholder: true };

  const tenantSpendKpi = buildKpi(kpi.tenantSpend, formatCurrency);
  const accountSpendKpi = buildKpi(kpi.accountSpend, formatCurrency);
  const conversationsKpi = buildKpi(kpi.conversations, formatCount);
  const todaySpendKpi = buildKpi(kpi.todaySpend, formatCurrency);

  return (
    <Box sx={{ p: 0, pb: ds.space[5] }}>
      {/* Header row: title + period + Manage Budgets action */}
      <WidgetCard sx={{ py: ds.space[3], px: ds.space[4], mt: 0, mb: ds.space[3] }}>
        <Box sx={{ display: 'flex', alignItems: 'flex-start', justifyContent: 'space-between', gap: ds.space[4] }}>
          <Box sx={{ flex: 1, minWidth: 0 }}>
            <Stack direction='row' spacing={1} alignItems='center' sx={{ mb: ds.space[0] }}>
              <Typography
                sx={{
                  fontSize: 'var(--ds-text-body-lg)',
                  color: 'var(--ds-gray-700)',
                  fontWeight: 'var(--ds-font-weight-semibold)',
                  fontFamily: 'Poppins',
                }}
              >
                Usage for {formatPeriod(period)}
              </Typography>
              <Label tone={STATUS_TO_LABEL[kpi.status].tone} dot size='md'>
                {STATUS_TO_LABEL[kpi.status].text}
              </Label>
              {kpi.hasDailyEnabled && today && (
                <Chip tone='neutral' size='2xs'>
                  {`Today: ${today}`}
                </Chip>
              )}
            </Stack>
            <Typography sx={{ fontSize: 'var(--ds-text-caption)', color: 'var(--ds-gray-500)' }}>
              Budget and consumption metrics for LLM-powered services. Monthly limits reset at the beginning of each month, daily limits reset at
              midnight.
            </Typography>
          </Box>
          {isAdmin && (
            <Button tone='primary' size='md' onClick={openManage}>
              Add Budget
            </Button>
          )}
        </Box>
      </WidgetCard>

      {/* "Contact support" banner fires only on TENANT exhaustion — the
          tenant cap is the only limit operators can't raise themselves.
          Account-scope exhaustion is self-serve (edit the account budget)
          so it surfaces via the Exhausted status pill instead, without
          this banner. */}
      {kpi.tenantExhausted && (
        <Alert severity='warning' sx={{ mb: ds.space[3], fontSize: 'var(--ds-text-small)' }}>
          Tenant budget exhausted. Please contact Nudgebee support to enable {assistantName} and LLM-based Event Analysis.
        </Alert>
      )}

      {/* KPI strip */}
      <Box sx={{ display: 'flex', gap: ds.space[3], mb: ds.space[3] }}>
        <KpiTile
          label='Monthly spend — tenant'
          value={tenantSpendKpi.value}
          sublabel={tenantName}
          progress={tenantSpendKpi.progress}
          exhausted={tenantSpendKpi.exhausted}
          placeholder={tenantSpendKpi.placeholder}
        />
        <KpiTile
          label='Monthly spend — account'
          value={accountSpendKpi.value}
          sublabel={accountName || accountId}
          progress={accountSpendKpi.progress}
          exhausted={accountSpendKpi.exhausted}
          placeholder={accountSpendKpi.placeholder}
        />
        <KpiTile
          label='Conversations (month)'
          value={conversationsKpi.value}
          sublabel='Tenant • all modules'
          progress={conversationsKpi.progress}
          exhausted={conversationsKpi.exhausted}
          placeholder={conversationsKpi.placeholder}
        />
        {kpi.hasDailyEnabled && (
          <KpiTile
            label="Today's spend"
            value={todaySpendKpi.value}
            sublabel='Tenant • all modules'
            progress={todaySpendKpi.progress}
            exhausted={todaySpendKpi.exhausted}
            placeholder={todaySpendKpi.placeholder}
          />
        )}
      </Box>

      {/* Usage matrix */}
      <UsageMatrix budgetData={budgetData} tenantName={tenantName} accountName={accountName || accountId} assistantName={assistantName} />

      {/* Admin: Active Budgets (always visible) + Other Accounts (collapsible) */}
      {isAdmin && (
        <Box sx={{ mt: ds.space[6] }}>
          {configsLoading ? (
            <Loader />
          ) : (
            <>
              <Typography
                sx={{
                  fontSize: 'var(--ds-text-small)',
                  fontWeight: 'var(--ds-font-weight-semibold)',
                  color: 'var(--ds-gray-500)',
                  textTransform: 'uppercase',
                  letterSpacing: 0.4,
                  mb: ds.space[4],
                }}
              >
                Active Budgets
              </Typography>
              <ActiveConfigsCompact
                scopes={activeScopes}
                assistantName={assistantName}
                emptyMessage='No custom configurations for tenant or this account. System defaults are being applied.'
                onEdit={(cfg) => {
                  setEditConfig(cfg);
                  setModalOpen(true);
                }}
                onDelete={(cfg) => setDeleteConfirm(cfg)}
              />

              {otherConfigs.length > 0 && (
                <Box sx={{ mt: ds.space[4] }}>
                  <Box
                    onClick={() => setConfigsOpen((v) => !v)}
                    sx={{
                      display: 'flex',
                      alignItems: 'center',
                      cursor: 'pointer',
                      p: ds.space.mul(0, 3),
                      borderRadius: ds.radius.md,
                      '&:hover': { backgroundColor: 'var(--ds-background-200)' },
                    }}
                  >
                    <Stack direction='row' alignItems='center' spacing={1}>
                      {configsOpen ? <KeyboardArrowUpIcon fontSize='small' /> : <KeyboardArrowDownIcon fontSize='small' />}
                      <Typography
                        sx={{
                          fontSize: 'var(--ds-text-small)',
                          fontWeight: 'var(--ds-font-weight-semibold)',
                          color: 'var(--ds-gray-500)',
                          textTransform: 'uppercase',
                          letterSpacing: 0.4,
                        }}
                      >
                        Budgets for Other Accounts
                      </Typography>
                      <Chip tone='neutral' size='2xs' variant='count'>
                        {otherConfigs.length}
                      </Chip>
                    </Stack>
                  </Box>
                  <Collapse in={configsOpen}>
                    <Box sx={{ pt: ds.space[3] }}>
                      <ActiveConfigsCompact
                        scopes={otherAccountScopes}
                        assistantName={assistantName}
                        onEdit={(cfg) => {
                          setEditConfig(cfg);
                          setModalOpen(true);
                        }}
                        onDelete={(cfg) => setDeleteConfirm(cfg)}
                      />
                    </Box>
                  </Collapse>
                </Box>
              )}
            </>
          )}
        </Box>
      )}

      <Modal
        open={!!deleteConfirm}
        handleClose={() => setDeleteConfirm(null)}
        onClose={() => setDeleteConfirm(null)}
        title='Delete Budget Configuration'
        width='sm'
        actionButtons={
          <Box sx={{ display: 'flex', justifyContent: 'flex-end', gap: ds.space[3], py: ds.space[3], px: ds.space[5] }}>
            <Button tone='secondary' size='sm' onClick={() => setDeleteConfirm(null)}>
              Cancel
            </Button>
            <Button tone='danger' size='sm' onClick={handleDeleteConfirmed}>
              Delete
            </Button>
          </Box>
        }
      >
        <Box sx={{ padding: ds.space[5] }}>
          <Typography
            sx={{
              fontFamily: 'var(--ds-font-display)',
              fontSize: 'var(--ds-text-body)',
              fontWeight: 'var(--ds-font-weight-regular)',
              color: 'var(--ds-gray-700)',
              lineHeight: 1.5,
            }}
          >
            Delete budget config for <strong>{getModuleLabels(assistantName)[deleteConfirm?.module] || deleteConfirm?.module}</strong>? This will
            revert to system defaults.
          </Typography>
        </Box>
      </Modal>

      <BudgetEditModal
        open={modalOpen}
        onClose={() => {
          setModalOpen(false);
          setEditConfig(null);
        }}
        onSaved={() => {
          loadConfigs();
          setRefreshKey((k) => k + 1);
        }}
        config={editConfig}
        maxCaps={maxCaps}
        systemDefaults={systemDefaults}
        existingConfigs={configs}
      />

      <Modal
        open={existingWarningOpen}
        handleClose={() => setExistingWarningOpen(false)}
        onClose={() => setExistingWarningOpen(false)}
        title='Existing budgets found'
        width='sm'
        actionButtons={
          <Box sx={{ display: 'flex', justifyContent: 'flex-end', gap: ds.space[3], py: ds.space[3], px: ds.space[5] }}>
            <Button tone='secondary' size='sm' onClick={() => setExistingWarningOpen(false)}>
              Cancel
            </Button>
            <Button tone='primary' size='sm' onClick={proceedToAddBudget}>
              Continue
            </Button>
          </Box>
        }
      >
        <Box sx={{ padding: ds.space[5] }}>
          <Typography
            sx={{
              fontFamily: 'var(--ds-font-display)',
              fontSize: 'var(--ds-text-body)',
              fontWeight: 'var(--ds-font-weight-regular)',
              color: 'var(--ds-gray-700)',
              lineHeight: 1.5,
            }}
          >
            You already have budgets at:
          </Typography>
          <Box
            component='ul'
            sx={{ mt: ds.space[2], mb: ds.space[4], pl: ds.space[5], color: 'var(--ds-gray-700)', fontSize: 'var(--ds-text-body)' }}
          >
            <li>
              {tenantName || 'Tenant'} (tenant, {tenantActiveConfigs.length} {tenantActiveConfigs.length === 1 ? 'module' : 'modules'})
            </li>
            <li>
              {accountName || 'This account'} (account, {accountActiveConfigs.length} {accountActiveConfigs.length === 1 ? 'module' : 'modules'})
            </li>
          </Box>
          <Typography
            sx={{
              fontFamily: 'var(--ds-font-display)',
              fontSize: 'var(--ds-text-body)',
              fontWeight: 'var(--ds-font-weight-regular)',
              color: 'var(--ds-gray-700)',
              lineHeight: 1.5,
            }}
          >
            Edit them from the <strong>Active Budgets</strong> section below, or continue to add a budget for a different account or module.
          </Typography>
        </Box>
      </Modal>
    </Box>
  );
};

LLMConsumptionTab.propTypes = {
  accountId: PropTypes.string.isRequired,
};

export default LLMConsumptionTab;
