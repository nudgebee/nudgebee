import React, { useEffect, useState, useCallback } from 'react';
import PropTypes from 'prop-types';
import { Box, Typography, Alert, Divider, Chip, IconButton, Tooltip, Switch, FormControlLabel, TextField, Select, MenuItem } from '@mui/material';
import EditIcon from '@mui/icons-material/Edit';
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

const formatCurrency = (amount) => {
  if (amount == null || amount === 0) return '$0.00';
  if (amount >= 1000) return `$${(amount / 1000).toFixed(2)}K`;
  if (amount >= 1) return `$${amount.toFixed(2)}`;
  return `$${amount.toFixed(4)}`;
};

const monthNames = ['January', 'February', 'March', 'April', 'May', 'June', 'July', 'August', 'September', 'October', 'November', 'December'];

const formatPeriod = (period) => {
  if (!period) return '';
  const [year, month] = period.split('-');
  return `${monthNames[parseInt(month) - 1]} ${year}`;
};

const getModuleLabels = (assistantName) => ({ investigation: 'Event Analysis', user_investigation: `${assistantName} Chat` });

// --- Metric Card ---

const MetricCard = ({ label, value, isHighUsage }) => (
  <Box
    sx={{
      padding: '12px',
      borderRadius: '8px',
      backgroundColor: colors.background.tertiaryLightest,
      border: `1px solid ${colors.border.secondaryLightest}`,
    }}
  >
    <Typography sx={{ fontSize: '11px', color: colors.text.tertiary, textTransform: 'uppercase', fontWeight: 500, mb: 0.5 }}>{label}</Typography>
    <Typography sx={{ fontSize: '18px', fontWeight: 600, color: isHighUsage ? colors.error : colors.text.primary }}>{value}</Typography>
  </Box>
);

MetricCard.propTypes = {
  label: PropTypes.string.isRequired,
  value: PropTypes.string.isRequired,
  isHighUsage: PropTypes.bool,
};

// --- Limit Row ---

const LimitRow = ({ label, limitInfo, type }) => {
  if (!limitInfo || !limitInfo.enabled) return null;

  const { limit, usage, remaining } = limitInfo;
  const usagePercentage = limit > 0 ? (usage / limit) * 100 : 0;
  const isHighUsage = usagePercentage >= 90;
  const fmt = type === 'cost' ? formatCurrency : (v) => (v != null ? v.toLocaleString() : '0');

  return (
    <Box sx={{ display: 'grid', gridTemplateColumns: '110px repeat(3, 1fr)', gap: 1.5, alignItems: 'center' }}>
      <Typography sx={{ fontSize: '12px', fontWeight: 500, color: colors.text.tertiary }}>{label}</Typography>
      <MetricCard label='Usage' value={fmt(usage)} />
      <MetricCard label='Limit' value={fmt(limit)} />
      <MetricCard label='Remaining' value={fmt(remaining)} isHighUsage={isHighUsage} />
    </Box>
  );
};

LimitRow.propTypes = {
  label: PropTypes.string.isRequired,
  limitInfo: PropTypes.object,
  type: PropTypes.string.isRequired,
};

// --- Usage Module Card ---

const BudgetModuleCard = ({ title, budgetInfo, showDivider }) => {
  if (!budgetInfo) return null;

  const { budget_disabled, monthly_cost, daily_cost, monthly_count, daily_count } = budgetInfo;
  const hasAnyEnabled = monthly_cost?.enabled || daily_cost?.enabled || monthly_count?.enabled || daily_count?.enabled;

  return (
    <>
      <Box sx={{ mb: 3 }}>
        <Box sx={{ display: 'flex', alignItems: 'center', gap: 1, mb: 2 }}>
          <Typography sx={{ fontSize: '16px', fontWeight: 600, color: colors.text.primary }}>{title}</Typography>
          {budget_disabled && <Chip label='DISABLED' size='small' color='error' sx={{ fontSize: '10px', height: '18px', fontWeight: 600 }} />}
        </Box>

        {budget_disabled ? (
          <Alert severity='warning' sx={{ fontSize: '12px' }}>
            All budget checks are disabled for this entity.
          </Alert>
        ) : !hasAnyEnabled ? (
          <Alert severity='info' sx={{ fontSize: '12px' }}>
            No limits are currently enabled.
          </Alert>
        ) : (
          <Box sx={{ display: 'flex', flexDirection: 'column', gap: 1.5 }}>
            <LimitRow label='Monthly Cost' limitInfo={monthly_cost} type='cost' />
            <LimitRow label='Daily Cost' limitInfo={daily_cost} type='cost' />
            <LimitRow label='Monthly Count' limitInfo={monthly_count} type='count' />
            <LimitRow label='Daily Count' limitInfo={daily_count} type='count' />
          </Box>
        )}
      </Box>
      {showDivider && <Divider sx={{ my: 3 }} />}
    </>
  );
};

BudgetModuleCard.propTypes = {
  title: PropTypes.string.isRequired,
  budgetInfo: PropTypes.object,
  showDivider: PropTypes.bool,
};

// --- Budget Config Edit Modal ---

const BudgetEditModal = ({ open, onClose, onSaved, config, maxCaps, systemDefaults, existingConfigs = [] }) => {
  const { assistantName } = useTenantBranding();
  const moduleLabels = getModuleLabels(assistantName);
  const isEdit = !!config;
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState('');
  const [accounts, setAccounts] = useState([]);

  const session = getUserSession();
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

  // Check if current selection matches an existing config
  const hasExistingConfig =
    !isEdit &&
    entityId &&
    module !== 'both' &&
    existingConfigs.some((c) => c.entity_type === entityType && c.entity_id === entityId && c.module === module);

  useEffect(() => {
    if (open) {
      apiUser.listAccounts().then((data) => {
        if (Array.isArray(data)) setAccounts(data);
      });
    }
  }, [open]);

  useEffect(() => {
    if (!isEdit) {
      if (entityType === 'tenant') {
        setEntityId(tenantId);
      } else {
        setEntityId('');
      }
    }
  }, [entityType, isEdit, tenantId]);

  const loadFromExisting = (cfg) => {
    setBudgetDisabled(cfg.budget_disabled);
    setMonthlyCostEnabled(cfg.monthly_cost_enabled);
    setMonthlyCostLimit(cfg.monthly_cost_limit != null ? String(cfg.monthly_cost_limit) : '');
    setDailyCostEnabled(cfg.daily_cost_enabled);
    setDailyCostLimit(cfg.daily_cost_limit != null ? String(cfg.daily_cost_limit) : '');
    setMonthlyCountEnabled(cfg.monthly_count_enabled);
    setMonthlyCountLimit(cfg.monthly_count_limit != null ? String(cfg.monthly_count_limit) : '');
    setDailyCountEnabled(cfg.daily_count_enabled);
    setDailyCountLimit(cfg.daily_count_limit != null ? String(cfg.daily_count_limit) : '');
  };

  useEffect(() => {
    if (!isEdit && open && entityId && module !== 'both') {
      const existing = existingConfigs.find((c) => c.entity_type === entityType && c.entity_id === entityId && c.module === module);
      if (existing) {
        loadFromExisting(existing);
      } else {
        // Reset to defaults when no existing config matches
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
    }
  }, [entityType, entityId, module, isEdit, open, existingConfigs]);

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
          onSaved(); // refresh to show partial save
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
    // For "both" modules, use investigation defaults as representative
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

  const modalTitle = isEdit
    ? `Edit Budget - ${moduleLabels[config?.module] || ''}`
    : hasExistingConfig
    ? 'Update Budget Configuration'
    : 'Create Budget Configuration';

  return (
    <Modal width='md' title={modalTitle} open={open} handleClose={onClose} onClose={onClose}>
      <Box sx={{ p: 1 }}>
        {error && (
          <Alert severity='error' sx={{ mb: 2 }}>
            {error}
          </Alert>
        )}

        {hasExistingConfig && (
          <Alert severity='info' sx={{ mb: 2, fontSize: '12px' }}>
            A configuration already exists for this selection. Your changes will update the existing config.
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
          <CustomButton variant='blueButton' onClick={handleSave} loading={loading} text={isEdit || hasExistingConfig ? 'Save Changes' : 'Create'} />
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

// --- Budget Config List (admin section) ---

const ConfigCard = ({ cfg, onEdit, onDelete }) => {
  const { assistantName } = useTenantBranding();
  const moduleLabels = getModuleLabels(assistantName);
  const renderLimitChip = (label, enabled, limit, type) => {
    if (!enabled) return null;
    const display = limit != null ? (type === 'cost' ? formatCurrency(limit) : String(limit)) : 'Default';
    return <Chip label={`${label}: ${display}`} size='small' color='primary' variant='outlined' sx={{ fontSize: '10px', height: '20px' }} />;
  };

  return (
    <Box
      sx={{
        p: 1.5,
        borderRadius: '6px',
        border: `1px solid ${colors.border.secondaryLightest}`,
        backgroundColor: colors.background.white,
        display: 'flex',
        justifyContent: 'space-between',
        alignItems: 'center',
      }}
    >
      <Box sx={{ display: 'flex', alignItems: 'center', gap: 1, flexWrap: 'wrap' }}>
        <Typography sx={{ fontSize: '13px', fontWeight: 500, color: colors.text.secondary, minWidth: 100 }}>
          {moduleLabels[cfg.module] || cfg.module}
        </Typography>
        {cfg.budget_disabled ? (
          <Chip label='ALL DISABLED' size='small' color='error' sx={{ fontSize: '10px', height: '20px', fontWeight: 600 }} />
        ) : (
          <>
            {renderLimitChip('Monthly Cost', cfg.monthly_cost_enabled, cfg.monthly_cost_limit, 'cost')}
            {renderLimitChip('Daily Cost', cfg.daily_cost_enabled, cfg.daily_cost_limit, 'cost')}
            {renderLimitChip('Monthly Count', cfg.monthly_count_enabled, cfg.monthly_count_limit, 'count')}
            {renderLimitChip('Daily Count', cfg.daily_count_enabled, cfg.daily_count_limit, 'count')}
            {!cfg.monthly_cost_enabled && !cfg.daily_cost_enabled && !cfg.monthly_count_enabled && !cfg.daily_count_enabled && (
              <Typography sx={{ fontSize: '11px', color: colors.text.tertiary, fontStyle: 'italic' }}>No limits enabled</Typography>
            )}
          </>
        )}
      </Box>
      <Box sx={{ display: 'flex', gap: 0.5, flexShrink: 0 }}>
        <Tooltip title='Edit'>
          <IconButton size='small' onClick={() => onEdit(cfg)} aria-label='Edit budget config'>
            <EditIcon sx={{ fontSize: '16px', color: colors.text.secondary }} />
          </IconButton>
        </Tooltip>
        <Tooltip title='Delete (revert to defaults)'>
          <IconButton size='small' onClick={() => onDelete(cfg)} aria-label='Delete budget config'>
            <SafeIcon alt='delete icon' src={DeleteIcon} height='16' width='16' />
          </IconButton>
        </Tooltip>
      </Box>
    </Box>
  );
};

ConfigCard.propTypes = {
  cfg: PropTypes.object.isRequired,
  onEdit: PropTypes.func.isRequired,
  onDelete: PropTypes.func.isRequired,
};

const BudgetConfigSection = ({ onConfigChanged, tenantName }) => {
  const { assistantName } = useTenantBranding();
  const moduleLabels = getModuleLabels(assistantName);
  const [configs, setConfigs] = useState([]);
  const [accounts, setAccounts] = useState([]);
  const [loading, setLoading] = useState(true);
  const [maxCaps, setMaxCaps] = useState(null);
  const [systemDefaults, setSystemDefaults] = useState(null);
  const [modalOpen, setModalOpen] = useState(false);
  const [editConfig, setEditConfig] = useState(null);
  const [deleteConfirm, setDeleteConfirm] = useState(null);

  const loadConfigs = useCallback(async () => {
    setLoading(true);
    try {
      const [tenantRes, accountRes, defaultsRes, accountList] = await Promise.all([
        apiBudget.listBudgetConfigs('tenant'),
        apiBudget.listBudgetConfigs('account'),
        apiBudget.getSystemDefaults(),
        apiUser.listAccounts(),
      ]);
      // Surface API errors instead of silently showing empty state
      const apiErrors = tenantRes.errors || accountRes.errors || defaultsRes.errors;
      if (apiErrors) {
        const msg = Array.isArray(apiErrors) ? apiErrors[0]?.message : 'Failed to load configurations';
        snackbar.error(msg || 'Failed to load configurations');
      }
      const allConfigs = [...(tenantRes.data || []), ...(accountRes.data || [])];
      setConfigs(allConfigs);
      if (defaultsRes.data?.max_caps) setMaxCaps(defaultsRes.data.max_caps);
      if (defaultsRes.data?.defaults) setSystemDefaults(defaultsRes.data.defaults);
      if (Array.isArray(accountList)) setAccounts(accountList);
    } catch {
      snackbar.error('Failed to load budget configurations');
    } finally {
      setLoading(false);
    }
  }, [onConfigChanged]);

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
        onConfigChanged?.();
      }
    } catch {
      snackbar.error('Failed to delete budget config');
    }
  };

  const getAccountName = (accountId) => {
    const acc = accounts.find((a) => a.id === accountId);
    return acc ? `${acc.account_name} (${acc.cloud_provider})` : accountId;
  };

  // Group configs: tenant first, then by account
  const tenantConfigs = configs.filter((c) => c.entity_type === 'tenant');
  const accountConfigs = configs.filter((c) => c.entity_type === 'account');
  const accountGroups = {};
  accountConfigs.forEach((cfg) => {
    if (!accountGroups[cfg.entity_id]) accountGroups[cfg.entity_id] = [];
    accountGroups[cfg.entity_id].push(cfg);
  });

  if (loading) return <Loader />;

  return (
    <Box>
      <Box sx={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', mb: 2 }}>
        <Typography sx={{ fontSize: '14px', fontWeight: 500, color: colors.text.secondary }}>Budget Configurations</Typography>
        <CustomButton
          variant='blueButton'
          onClick={() => {
            setEditConfig(null);
            setModalOpen(true);
          }}
          text='Manage Budgets'
        />
      </Box>

      {configs.length === 0 ? (
        <Alert severity='info' sx={{ fontSize: '13px' }}>
          No custom configurations. System defaults are being applied.
        </Alert>
      ) : (
        <Box sx={{ display: 'flex', flexDirection: 'column', gap: 2 }}>
          {/* Tenant configs */}
          {tenantConfigs.length > 0 && (
            <Box>
              <Typography sx={{ fontSize: '13px', fontWeight: 600, color: colors.text.secondary, mb: 0.5 }}>Tenant</Typography>
              <Typography sx={{ fontSize: '11px', color: colors.text.tertiary, mb: 1 }}>{tenantName}</Typography>
              <Box sx={{ display: 'flex', flexDirection: 'column', gap: 1 }}>
                {tenantConfigs.map((cfg) => (
                  <ConfigCard
                    key={cfg.id}
                    cfg={cfg}
                    onEdit={(c) => {
                      setEditConfig(c);
                      setModalOpen(true);
                    }}
                    onDelete={(c) => setDeleteConfirm(c)}
                  />
                ))}
              </Box>
            </Box>
          )}

          {/* Account configs grouped by account */}
          {Object.keys(accountGroups).length > 0 && (
            <Typography sx={{ fontSize: '13px', fontWeight: 600, color: colors.text.secondary, mt: 1 }}>Accounts</Typography>
          )}
          {Object.entries(accountGroups).map(([accountId, cfgs]) => (
            <Box key={accountId}>
              <Typography sx={{ fontSize: '11px', color: colors.text.tertiary, mb: 1 }}>{getAccountName(accountId)}</Typography>
              <Box sx={{ display: 'flex', flexDirection: 'column', gap: 1 }}>
                {cfgs.map((cfg) => (
                  <ConfigCard
                    key={cfg.id}
                    cfg={cfg}
                    onEdit={(c) => {
                      setEditConfig(c);
                      setModalOpen(true);
                    }}
                    onDelete={(c) => setDeleteConfirm(c)}
                  />
                ))}
              </Box>
            </Box>
          ))}
        </Box>
      )}

      <NDialog
        open={!!deleteConfirm}
        handleClose={() => setDeleteConfirm(null)}
        dialogTitle='Delete Budget Configuration'
        dialogContent={
          <>
            Delete budget config for <strong>{moduleLabels[deleteConfirm?.module] || deleteConfirm?.module}</strong>? This will revert to system
            defaults.
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
          onConfigChanged?.();
        }}
        config={editConfig}
        maxCaps={maxCaps}
        systemDefaults={systemDefaults}
        existingConfigs={configs}
      />
    </Box>
  );
};

// --- Main Component ---

const LLMConsumptionTab = ({ accountId }) => {
  const { assistantName } = useTenantBranding();
  const [loading, setLoading] = useState(true);
  const [budgetData, setBudgetData] = useState(null);
  const [error, setError] = useState(null);
  const [refreshKey, setRefreshKey] = useState(0);
  const [accountName, setAccountName] = useState('');
  const session = getUserSession();
  const isSuperAdmin = !!session?.isSuperAdmin;
  const isAdmin = isTenantAdmin() || isSuperAdmin;
  const tenantName = session?.tenant?.name || 'Tenant';

  useEffect(() => {
    const fetchBudgetStatus = async () => {
      if (!accountId) {
        setError('Account ID is required');
        setLoading(false);
        return;
      }

      try {
        setLoading(true);
        const [response, accountList] = await Promise.all([apiBudget.getBudgetStatus(accountId), apiUser.listAccounts()]);
        if (Array.isArray(accountList)) {
          const acc = accountList.find((a) => a.id === accountId);
          if (acc) setAccountName(`${acc.account_name} (${acc.cloud_provider})`);
        }
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

  const { period, today, investigation, user_investigation } = budgetData;

  const hasDailyEnabled =
    investigation?.tenant?.daily_cost?.enabled ||
    investigation?.account?.daily_cost?.enabled ||
    user_investigation?.tenant?.daily_cost?.enabled ||
    user_investigation?.account?.daily_cost?.enabled ||
    investigation?.tenant?.daily_count?.enabled ||
    investigation?.account?.daily_count?.enabled ||
    user_investigation?.tenant?.daily_count?.enabled ||
    user_investigation?.account?.daily_count?.enabled;

  const isBudgetExhausted =
    (investigation?.tenant?.monthly_cost?.enabled && investigation?.tenant?.monthly_cost?.remaining <= 0) ||
    (user_investigation?.tenant?.monthly_cost?.enabled && user_investigation?.tenant?.monthly_cost?.remaining <= 0);

  return (
    <Box sx={{ p: 0, pb: 3 }}>
      <WidgetCard sx={{ p: '16px 20px', mt: 0, mb: 2 }}>
        <Typography sx={{ fontSize: '14px', color: colors.text.secondary, fontWeight: 600, fontFamily: 'Poppins' }}>
          Usage for {formatPeriod(period)}
          {hasDailyEnabled && today ? ` — Today: ${today}` : ''}
        </Typography>
        <Typography sx={{ fontSize: '12px', color: colors.text.tertiary }}>
          Budget and consumption metrics for LLM-powered services. Monthly limits reset at the beginning of each month, daily limits reset at
          midnight.
        </Typography>
      </WidgetCard>

      {isBudgetExhausted && (
        <Alert severity='warning' sx={{ mb: 3, fontSize: '13px' }}>
          Budget exhausted. Please contact Nudgebee support team to enable Nubi and LLM-based Event Analysis.
        </Alert>
      )}

      <Typography sx={{ fontSize: '12px', fontWeight: 600, color: colors.text.tertiary, textTransform: 'uppercase', mb: 1 }}>
        Tenant — {tenantName}
      </Typography>
      <BudgetModuleCard title='Event Analysis' budgetInfo={investigation?.tenant} showDivider={true} />
      <BudgetModuleCard title={`${assistantName} Chat`} budgetInfo={user_investigation?.tenant} showDivider={true} />

      <Typography sx={{ fontSize: '12px', fontWeight: 600, color: colors.text.tertiary, textTransform: 'uppercase', mb: 1 }}>
        Account — {accountName || accountId}
      </Typography>
      <BudgetModuleCard title='Event Analysis' budgetInfo={investigation?.account} showDivider={true} />
      <BudgetModuleCard title={`${assistantName} Chat`} budgetInfo={user_investigation?.account} showDivider={isAdmin} />

      {isAdmin && (
        <>
          <Divider sx={{ my: 3 }} />
          <BudgetConfigSection onConfigChanged={() => setRefreshKey((k) => k + 1)} tenantName={tenantName} />
        </>
      )}
    </Box>
  );
};

LLMConsumptionTab.propTypes = {
  accountId: PropTypes.string.isRequired,
};

export default LLMConsumptionTab;
