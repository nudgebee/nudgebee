import React, { useEffect, useState, useCallback, useMemo } from 'react';
import PropTypes from 'prop-types';
import {
  Box,
  Typography,
  Alert,
  Divider,
  Chip,
  IconButton,
  Tooltip,
  Switch,
  FormControlLabel,
  TextField,
  Select,
  MenuItem,
  Collapse,
  Stack,
} from '@mui/material';
import EditIcon from '@mui/icons-material/Edit';
import KeyboardArrowDownIcon from '@mui/icons-material/KeyboardArrowDown';
import KeyboardArrowUpIcon from '@mui/icons-material/KeyboardArrowUp';
import { DeleteIconRed as DeleteIcon } from '@assets';
import SafeIcon from '@components1/common/SafeIcon';
import NDialog from '@components1/common/modal/NDialog';
import { colors } from 'src/utils/colors';
import apiBudget from '@api1/budget';
import Loader from '@components1/common/Loader';
import CustomButton from '@components1/common/NewCustomButton';
import { snackbar } from '@components1/common/snackbarService';
import { Modal } from '@components1/common/modal';
import { getUserSession, isTenantAdmin } from '@lib/auth';
import { useTenantBranding } from '@hooks/useTenantBranding';
import apiUser from '@api1/user';
import WidgetCard from '@components1/common/WidgetCard';
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
    <Typography sx={{ fontSize: '20px', fontWeight: 700, lineHeight: 1.25, whiteSpace: 'nowrap' }}>
      <span style={{ color: usageRed ? 'var(--ds-red-500)' : colors.text.secondary }}>{usagePart}</span>
      {limitPart && <span style={{ color: colors.text.tertiary, fontWeight: 500 }}> / {limitPart}</span>}
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
      p: 1.5,
      borderRadius: '8px',
      backgroundColor: colors.background.white,
      border: `1px solid ${colors.border.secondaryLightest}`,
    }}
  >
    <Box sx={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', mb: 0.5 }}>
      <Typography
        sx={{
          fontSize: '10.5px',
          color: colors.text.tertiary,
          textTransform: 'uppercase',
          fontWeight: 600,
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
      <Typography sx={{ fontSize: '13px', fontStyle: 'italic', color: colors.text.tertiary, py: 0.25 }}>{value}</Typography>
    ) : (
      <Box sx={{ display: 'inline-flex', flexDirection: 'column', alignItems: 'stretch', maxWidth: '100%' }}>
        <KpiValue value={value} exhausted={exhausted} progress={progress} />
        {progress != null && (
          <Box sx={{ mt: 0.75 }}>
            <ProgressBar value={progress} thresholds={UTIL_THRESHOLDS} showValue={false} size='sm' />
          </Box>
        )}
      </Box>
    )}
    {sublabel && <Typography sx={{ fontSize: '11px', color: colors.text.tertiary, mt: 0.25 }}>{sublabel}</Typography>}
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
  return colors.border.secondaryLightest;
};

// One vertical stripe inside a module cell: usage/limit on top, progress
// bar below. When the matrix only has one limit type visible globally, the
// label row is dropped (saves vertical space).
const LimitLine = ({ label, info, type }) => {
  if (!info?.enabled) {
    return <Typography sx={{ fontSize: '12px', color: colors.text.tertiary }}>—</Typography>;
  }
  const { limit, usage, remaining } = info;
  const pct = limit > 0 ? (usage / limit) * 100 : 0;
  const exhausted = limit > 0 && remaining != null && remaining <= 0;
  const fmt = type === 'cost' ? formatCurrency : formatCount;
  return (
    <Box>
      {label && (
        <Typography sx={{ fontSize: '10px', color: colors.text.tertiary, textTransform: 'uppercase', fontWeight: 600, letterSpacing: 0.3, mb: 0.25 }}>
          {label}
        </Typography>
      )}
      <Box sx={{ display: 'inline-flex', flexDirection: 'column', alignItems: 'stretch', maxWidth: '100%' }}>
        <Typography
          sx={{
            fontSize: '12px',
            fontWeight: 500,
            color: exhausted || pct >= UTIL_THRESHOLDS.warning ? 'var(--ds-red-500)' : colors.text.secondary,
            whiteSpace: 'nowrap',
          }}
        >
          {fmt(usage)} <span style={{ color: colors.text.tertiary, fontWeight: 400 }}>/ {fmt(limit)}</span>
        </Typography>
        <Box sx={{ mt: 0.5 }}>
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
    return <Typography sx={{ fontSize: '11.5px', color: colors.text.tertiary, textAlign: 'center' }}>—</Typography>;
  }
  const anyEnabled = visibleLimits.some((l) => info[l.key]?.enabled);
  if (!anyEnabled) {
    return <Typography sx={{ fontSize: '11.5px', color: colors.text.tertiary, fontStyle: 'italic' }}>No limits</Typography>;
  }
  return (
    <Box sx={{ display: 'flex', flexDirection: 'column', gap: 0.75 }}>
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
  const gridTemplate = `170px ${MODULE_KEYS.map(() => '1fr').join(' ')}`.trim();

  return (
    <Box
      sx={{
        borderRadius: '8px',
        border: `1px solid ${colors.border.secondaryLightest}`,
        backgroundColor: colors.background.white,
        p: 1.5,
      }}
    >
      {/* Header */}
      <Box
        sx={{
          display: 'grid',
          gridTemplateColumns: gridTemplate,
          alignItems: 'center',
          gap: 1.5,
          px: 1.5,
          pb: 1,
          mb: 0.5,
          borderBottom: `1px solid ${colors.border.secondaryLightest}`,
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
              gap: 1.5,
              pl: 1.25,
              pr: 1.5,
              py: 0.75,
              borderLeft: `3px solid ${edgeColor}`,
              borderRadius: '4px',
              '&:not(:last-of-type)': { mb: 0.5 },
            }}
          >
            <Box>
              <Typography sx={{ fontSize: '11px', color: colors.text.tertiary, textTransform: 'uppercase', fontWeight: 600, letterSpacing: 0.3 }}>
                {scope.label}
              </Typography>
              <Typography
                sx={{
                  fontSize: '12px',
                  fontWeight: 500,
                  color: colors.text.secondary,
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
  fontSize: '10.5px',
  fontWeight: 600,
  color: colors.text.tertiary,
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
  // Session payload nests tenant twice — see useTenantBranding for the canonical
  // path. Stored here as `tenantId` because the form uses the tenant's name as
  // the entity_id stand-in (see conflict-detection comment below).
  const tenantId = session?.tenant?.tenant?.name || '';
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
    // the user must edit it from Active Configurations instead.
    if (hasConflict) {
      const conflictLabels = conflictingModules.map((m) => moduleLabels[m] || m).join(' and ');
      setError(
        `A configuration already exists for ${conflictLabels} on this ${entityType}. ` +
          'Edit the existing configuration from the Active Configurations section instead of adding a new one.'
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
      <Box sx={{ mb: 1.5 }}>
        <Box sx={{ display: 'flex', alignItems: 'center', gap: 2 }}>
          <FormControlLabel
            control={<Switch checked={enabled} onChange={(e) => setEnabled(e.target.checked)} size='small' />}
            label={<Typography sx={{ fontSize: '13px', fontWeight: 500, minWidth: 120, color: colors.text.secondary }}>{label}</Typography>}
            sx={{ mr: 0, minWidth: 200 }}
          />
          <TextField
            size='small'
            type='number'
            value={value}
            onChange={(e) => setValue(e.target.value)}
            disabled={!enabled}
            placeholder={type === 'cost' ? 'USD' : 'Count'}
            sx={{ width: 130 }}
            inputProps={{ min: 0, max: cap, step: type === 'cost' ? 0.01 : 1 }}
          />
          {cap !== undefined && (
            <Typography sx={{ fontSize: '11px', color: colors.text.tertiary }}>
              max: {type === 'cost' ? `$${cap.toLocaleString()}` : cap.toLocaleString()}
            </Typography>
          )}
        </Box>
        {!enabled && sysDefault != null && sysDefault > 0 && (
          <Typography sx={{ fontSize: '11px', color: colors.text.tertiary, ml: '52px', mt: 0.5 }}>
            System default will apply: {type === 'cost' ? formatCurrency(sysDefault) : sysDefault}
          </Typography>
        )}
      </Box>
    );
  };

  const modalTitle = isEdit ? `Edit Budget - ${moduleLabels[config?.module] || ''}` : 'Create Budget Configuration';

  return (
    <Modal width='md' title={modalTitle} open={open} handleClose={onClose} onClose={onClose}>
      <Box sx={{ p: 1 }}>
        {error && (
          <Alert severity='error' sx={{ mb: 2 }}>
            {error}
          </Alert>
        )}

        {hasConflict && (
          <Alert severity='warning' sx={{ mb: 2, fontSize: '12px' }}>
            A configuration already exists for {conflictingModules.map((m) => moduleLabels[m] || m).join(' and ')} on this {entityType}. Edit the
            existing configuration from the Active Configurations section instead of adding a new one.
          </Alert>
        )}

        <Box sx={{ display: 'flex', gap: 2, mb: 2 }}>
          <Box sx={{ flex: 1 }}>
            <Typography sx={{ fontSize: '12px', fontWeight: 500, color: colors.text.tertiary, mb: 0.5 }}>Scope</Typography>
            <Select
              size='small'
              fullWidth
              value={entityType}
              onChange={(e) => setEntityType(e.target.value)}
              disabled={isEdit}
              sx={{ fontSize: '13px' }}
            >
              <MenuItem value='tenant'>Tenant</MenuItem>
              <MenuItem value='account'>Account</MenuItem>
            </Select>
          </Box>
          <Box sx={{ flex: 2 }}>
            <Typography sx={{ fontSize: '12px', fontWeight: 500, color: colors.text.tertiary, mb: 0.5 }}>
              {entityType === 'tenant' ? 'Tenant' : 'Account'}
            </Typography>
            {entityType === 'tenant' ? (
              <TextField size='small' fullWidth value='Current Tenant' disabled sx={{ '& .MuiInputBase-input': { fontSize: '13px' } }} />
            ) : (
              <Select
                size='small'
                fullWidth
                value={entityId}
                onChange={(e) => setEntityId(e.target.value)}
                disabled={isEdit}
                displayEmpty
                sx={{ fontSize: '13px' }}
              >
                <MenuItem value='' disabled>
                  Select an account
                </MenuItem>
                {accounts.map((acc) => (
                  <MenuItem key={acc.id} value={acc.id}>
                    {acc.account_name} ({acc.cloud_provider})
                  </MenuItem>
                ))}
              </Select>
            )}
          </Box>
        </Box>

        <Box sx={{ mb: 2 }}>
          <Typography sx={{ fontSize: '12px', fontWeight: 500, color: colors.text.tertiary, mb: 0.5 }}>Apply to</Typography>
          <Select size='small' fullWidth value={module} onChange={(e) => setModule(e.target.value)} disabled={isEdit} sx={{ fontSize: '13px' }}>
            {!isEdit && <MenuItem value='both'>Both Modules</MenuItem>}
            <MenuItem value='investigation'>Event Analysis (Automated)</MenuItem>
            <MenuItem value='user_investigation'>Nubi Chat (User)</MenuItem>
          </Select>
        </Box>

        {isSuperAdmin && (
          <Box
            sx={{
              mb: 2,
              p: 1.5,
              borderRadius: '6px',
              border: `1px solid ${budgetDisabled ? colors.border.error : colors.border.primary}`,
              backgroundColor: budgetDisabled ? colors.background.errorLight : 'transparent',
            }}
          >
            <FormControlLabel
              control={<Switch checked={budgetDisabled} onChange={(e) => setBudgetDisabled(e.target.checked)} color='error' />}
              label={
                <Box>
                  <Typography sx={{ fontSize: '13px', fontWeight: 600, color: budgetDisabled ? colors.text.red : colors.text.secondary }}>
                    Disable All Budget Checks
                  </Typography>
                  <Typography sx={{ fontSize: '11px', color: colors.text.tertiary }}>
                    Bypasses all cost and count limits. Use with caution.
                  </Typography>
                </Box>
              }
            />
          </Box>
        )}

        <Divider sx={{ my: 2 }} />
        <Typography sx={{ fontSize: '14px', fontWeight: 600, color: colors.text.secondary, mb: 1.5 }}>Cost Limits (USD)</Typography>
        {renderLimitRow('Monthly Cost', monthlyCostEnabled, setMonthlyCostEnabled, monthlyCostLimit, setMonthlyCostLimit, 'monthly_cost', 'cost')}
        {renderLimitRow('Daily Cost', dailyCostEnabled, setDailyCostEnabled, dailyCostLimit, setDailyCostLimit, 'daily_cost', 'cost')}

        <Divider sx={{ my: 2 }} />
        <Typography sx={{ fontSize: '14px', fontWeight: 600, color: colors.text.secondary, mb: 1.5 }}>Count Limits (Conversations)</Typography>
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

        <Divider sx={{ my: 2 }} />
        <Box sx={{ display: 'flex', justifyContent: 'flex-end', gap: 1.5 }}>
          <CustomButton variant='lightButton' onClick={onClose} disabled={loading} text='Cancel' />
          <CustomButton
            variant='blueButton'
            onClick={handleSave}
            loading={loading}
            disabled={hasConflict}
            text={isEdit ? 'Save Changes' : 'Create'}
          />
        </Box>
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
          py: 0.75,
          px: 1,
          borderRadius: '6px',
          border: `1px dashed ${colors.border.secondaryLightest}`,
          backgroundColor: 'transparent',
          minHeight: 32,
          display: 'flex',
          alignItems: 'center',
          gap: 0.75,
        }}
      >
        <Typography sx={{ fontSize: '12px', fontWeight: 500, color: colors.text.tertiary, whiteSpace: 'nowrap' }}>{moduleLabel}</Typography>
        <Typography sx={{ fontSize: '11px', color: colors.text.tertiary, fontStyle: 'italic' }}>system defaults</Typography>
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
        py: 0.5,
        pl: 1,
        pr: 0.5,
        borderRadius: '6px',
        border: `1px solid ${colors.border.secondaryLightest}`,
        backgroundColor: colors.background.white,
        display: 'flex',
        alignItems: 'center',
        gap: 0.75,
        minHeight: 32,
      }}
    >
      <Typography sx={{ fontSize: '12px', fontWeight: 500, color: colors.text.secondary, whiteSpace: 'nowrap' }}>{moduleLabel}</Typography>
      <Box sx={{ flex: 1, minWidth: 0, display: 'flex', alignItems: 'center', gap: 0.5, flexWrap: 'wrap' }}>
        {cfg.budget_disabled ? (
          <Chip label='ALL DISABLED' size='small' color='error' sx={{ fontSize: '10px', height: '20px', fontWeight: 600 }} />
        ) : limits.length > 0 ? (
          limits.map((l) => (
            <Chip
              key={l}
              label={l}
              size='small'
              color='primary'
              variant='outlined'
              sx={{ fontSize: '10px', height: '20px', borderRadius: '999px' }}
            />
          ))
        ) : (
          <Typography sx={{ fontSize: '11px', color: colors.text.tertiary, fontStyle: 'italic' }}>no limits enabled</Typography>
        )}
      </Box>
      <Tooltip title='Edit'>
        <IconButton size='small' onClick={() => onEdit(cfg)} aria-label='Edit budget config' sx={{ p: 0.25 }}>
          <EditIcon sx={{ fontSize: '15px', color: colors.text.secondary }} />
        </IconButton>
      </Tooltip>
      <Tooltip title='Delete (revert to defaults)'>
        <IconButton size='small' onClick={() => onDelete(cfg)} aria-label='Delete budget config' sx={{ p: 0.25 }}>
          <SafeIcon alt='delete icon' src={DeleteIcon} height='14' width='14' />
        </IconButton>
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
      <Box key={key} sx={{ '&:not(:last-of-type)': { mb: 2 } }}>
        <Stack direction='row' alignItems='baseline' spacing={1} sx={{ mb: 0.75 }}>
          <Typography sx={{ fontSize: '11px', color: colors.text.tertiary, textTransform: 'uppercase', fontWeight: 600, letterSpacing: 0.3 }}>
            {label}
          </Typography>
          <Typography sx={{ fontSize: '12px', fontWeight: 500, color: colors.text.secondary }}>{name}</Typography>
        </Stack>
        <Box sx={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: 1 }}>
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
      <Alert severity='info' sx={{ fontSize: '12.5px' }}>
        {emptyMessage}
      </Alert>
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

  // Lifted budget-config state — top-level "Manage Budgets" button and the
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

  const session = getUserSession();
  const isSuperAdmin = !!session?.isSuperAdmin;
  const isAdmin = isTenantAdmin() || isSuperAdmin;
  // Session payload nests tenant twice — `session.tenant` is the wrapper,
  // `session.tenant.tenant` is the actual tenant record. Match the path used
  // by the shared `useTenantBranding` hook so display stays consistent.
  const tenantName = session?.tenant?.tenant?.name || 'Tenant';

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

  const openManage = () => {
    setEditConfig(null);
    setModalOpen(true);
    setConfigsOpen(true);
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
      <Box sx={{ display: 'flex', justifyContent: 'center', alignItems: 'center', minHeight: '300px' }}>
        <Loader />
      </Box>
    );
  }

  if (error) {
    return (
      <Box sx={{ p: 3 }}>
        <Alert severity='error'>{error}</Alert>
      </Box>
    );
  }

  if (!budgetData) {
    return (
      <Box sx={{ p: 3 }}>
        <Alert severity='info'>No budget data available</Alert>
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
    <Box sx={{ p: 0, pb: 3 }}>
      {/* Header row: title + period + Manage Budgets action */}
      <WidgetCard sx={{ p: '12px 16px', mt: 0, mb: 1.5 }}>
        <Box sx={{ display: 'flex', alignItems: 'flex-start', justifyContent: 'space-between', gap: 2 }}>
          <Box sx={{ flex: 1, minWidth: 0 }}>
            <Stack direction='row' spacing={1} alignItems='center' sx={{ mb: 0.25 }}>
              <Typography sx={{ fontSize: '14px', color: colors.text.secondary, fontWeight: 600, fontFamily: 'Poppins' }}>
                Usage for {formatPeriod(period)}
              </Typography>
              <Label tone={STATUS_TO_LABEL[kpi.status].tone} dot size='md'>
                {STATUS_TO_LABEL[kpi.status].text}
              </Label>
              {kpi.hasDailyEnabled && today && (
                <Chip
                  label={`Today: ${today}`}
                  size='small'
                  sx={{ fontSize: '10.5px', height: '20px', backgroundColor: colors.background.tertiaryLightest, color: colors.text.tertiary }}
                />
              )}
            </Stack>
            <Typography sx={{ fontSize: '11.5px', color: colors.text.tertiary }}>
              Budget and consumption metrics for LLM-powered services. Monthly limits reset at the beginning of each month, daily limits reset at
              midnight.
            </Typography>
          </Box>
          {isAdmin && <CustomButton variant='blueButton' onClick={openManage} text='Manage Budgets' />}
        </Box>
      </WidgetCard>

      {/* "Contact support" banner fires only on TENANT exhaustion — the
          tenant cap is the only limit operators can't raise themselves.
          Account-scope exhaustion is self-serve (edit the account budget)
          so it surfaces via the Exhausted status pill instead, without
          this banner. */}
      {kpi.tenantExhausted && (
        <Alert severity='warning' sx={{ mb: 1.5, fontSize: '12.5px' }}>
          Tenant budget exhausted. Please contact Nudgebee support to enable {assistantName} and LLM-based Event Analysis.
        </Alert>
      )}

      {/* KPI strip */}
      <Box sx={{ display: 'flex', gap: 1.5, mb: 1.5 }}>
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

      {/* Admin: Active Configurations (always visible) + Other Accounts (collapsible) */}
      {isAdmin && (
        <Box sx={{ mt: 4 }}>
          {configsLoading ? (
            <Loader />
          ) : (
            <>
              <Typography
                sx={{ fontSize: '12px', fontWeight: 600, color: colors.text.tertiary, textTransform: 'uppercase', letterSpacing: 0.4, mb: 2 }}
              >
                Active Configurations
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
                <Box sx={{ mt: 2 }}>
                  <Box
                    onClick={() => setConfigsOpen((v) => !v)}
                    sx={{
                      display: 'flex',
                      alignItems: 'center',
                      cursor: 'pointer',
                      p: 0.75,
                      borderRadius: '6px',
                      '&:hover': { backgroundColor: colors.background.tertiaryLightest },
                    }}
                  >
                    <Stack direction='row' alignItems='center' spacing={1}>
                      {configsOpen ? <KeyboardArrowUpIcon fontSize='small' /> : <KeyboardArrowDownIcon fontSize='small' />}
                      <Typography
                        sx={{ fontSize: '12px', fontWeight: 600, color: colors.text.tertiary, textTransform: 'uppercase', letterSpacing: 0.4 }}
                      >
                        Other Account Configurations
                      </Typography>
                      <Chip
                        label={otherConfigs.length}
                        size='small'
                        sx={{ fontSize: '10.5px', height: '18px', backgroundColor: colors.background.tertiaryLightest, color: colors.text.tertiary }}
                      />
                    </Stack>
                  </Box>
                  <Collapse in={configsOpen}>
                    <Box sx={{ pt: 1.5 }}>
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

      <NDialog
        open={!!deleteConfirm}
        handleClose={() => setDeleteConfirm(null)}
        dialogTitle='Delete Budget Configuration'
        dialogContent={
          <>
            Delete budget config for <strong>{getModuleLabels(assistantName)[deleteConfirm?.module] || deleteConfirm?.module}</strong>? This will
            revert to system defaults.
          </>
        }
        handleSubmit={handleDeleteConfirmed}
        buttonText='Delete'
        additionalComponent={null}
        width='sm'
      />

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
    </Box>
  );
};

LLMConsumptionTab.propTypes = {
  accountId: PropTypes.string.isRequired,
};

export default LLMConsumptionTab;
