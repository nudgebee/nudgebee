import { useEffect, useState } from 'react';
import PropTypes from 'prop-types';
import { Box } from '@mui/material';
import LockOutlinedIcon from '@mui/icons-material/LockOutlined';
import DomainOutlinedIcon from '@mui/icons-material/DomainOutlined';
import CloudOutlinedIcon from '@mui/icons-material/CloudOutlined';
import { Chip } from '@ui/Chip';
import Tooltip from '@ui/Tooltip';
import apiUser from '@api1/user';
import { ds } from '@utils/colors';

// Lightweight per-mount cache of cloud account ids → display label. Keeps
// the scope chip from re-fetching the accounts list every time b-Cortex or
// Settings open. Lives in module scope on purpose — the list is global and
// invalidates only on user sign-in / sign-out, which already remounts the
// app.
let accountIndexPromise = null;

const loadAccountIndex = () => {
  if (accountIndexPromise) return accountIndexPromise;
  accountIndexPromise = apiUser
    .listAccounts()
    .then((data) => {
      const map = {};
      if (Array.isArray(data)) {
        // Skip null / id-less entries defensively — `listAccounts` is a
        // tenant-shared cache and an upstream blip can shape a row as
        // null even when the array itself comes back well-formed.
        for (const a of data) {
          if (!a || !a.id) continue;
          map[a.id] = a.cloud_provider ? `${a.account_name} (${a.cloud_provider})` : a.account_name;
        }
      }
      return map;
    })
    .catch(() => {
      // Reset the cached promise on failure so the next open retries
      // instead of locking us into a half-loaded state.
      accountIndexPromise = null;
      return {};
    });
  return accountIndexPromise;
};

/**
 * Inline scope indicator for b-Cortex / Settings tab headers.
 * - Empty accountId → tenant-wide badge. When `readOnly` is true (default)
 *   it carries a "Read-only" suffix; pass `readOnly={false}` for tabs that
 *   still allow writes at the tenant level (e.g. Usage & Limits — tenant
 *   admins manage tenant-scope budgets even with no account selected).
 * - Non-empty accountId → account-scoped badge with the account's display
 *   name.
 *
 * The component renders something immediately (the chip), then upgrades
 * the label once the cached accounts index resolves — keeps the heading
 * from flickering on open.
 */
const ScopeChip = ({ accountId, readOnly = true }) => {
  const isTenantWide = !accountId;
  const [accountLabel, setAccountLabel] = useState('');

  useEffect(() => {
    if (isTenantWide) return undefined;
    let active = true;
    loadAccountIndex().then((index) => {
      if (active) setAccountLabel(index[accountId] || '');
    });
    return () => {
      active = false;
    };
  }, [accountId, isTenantWide]);

  // Use Chip's native `icon` prop and `size='xs'` to match the existing
  // LockChip pattern in memory2/shared.jsx — keeps every "scope" chip in
  // the app at the same stroke weight and spacing.
  if (isTenantWide) {
    const tooltipText = readOnly
      ? 'Tenant-wide view — viewing every account in this tenant. Writes require an account-scoped page.'
      : 'Tenant-scope view — applies to every account in this tenant.';
    const TenantIcon = readOnly ? LockOutlinedIcon : DomainOutlinedIcon;
    return (
      <Tooltip title={tooltipText} placement='top'>
        <Box component='span' sx={{ display: 'inline-flex', cursor: 'help' }}>
          <Chip tone='neutral' size='xs' variant='tag' icon={<TenantIcon sx={{ fontSize: 12 }} />}>
            {readOnly ? 'Tenant · Read-only' : 'Tenant'}
          </Chip>
        </Box>
      </Tooltip>
    );
  }

  const displayLabel = accountLabel || `Account ${accountId.slice(0, 8)}…`;
  return (
    <Tooltip title='Account-scoped view — writes apply to this account only.' placement='top'>
      <Box component='span' sx={{ display: 'inline-flex', cursor: 'help' }}>
        <Chip tone='success' size='xs' variant='tag' icon={<CloudOutlinedIcon sx={{ fontSize: 12 }} />}>
          {displayLabel}
        </Chip>
      </Box>
    </Tooltip>
  );
};

ScopeChip.propTypes = {
  // Empty / unset → tenant-wide indicator. Non-empty → account chip.
  accountId: PropTypes.string,
  // Defaults true. Set false on tabs whose tenant-wide view still allows
  // writes (Usage & Limits is the canonical example — tenant-admins can
  // add / edit / delete tenant-scope budgets without picking an account).
  readOnly: PropTypes.bool,
};

/**
 * Pair of chips for tabs whose data is genuinely a hybrid of tenant +
 * account scope (system catalog + per-account customizations) — Agents,
 * Tools, Functions. Renders the Tenant chip on its own when no account
 * is in scope, and Tenant + Account chips when one is.
 *
 * The Tenant chip's `Read-only` suffix is gated on the parent's mode:
 * - Tenant-wide entry (no account): every write is blocked, suffix shows.
 * - Account entry (hybrid): writes ARE allowed against the account
 *   layer, so the Tenant chip drops the suffix — it's informational
 *   ("this view also includes the system catalog"), not a write gate.
 */
export const HybridScopeChips = ({ accountId }) => {
  const inAccountMode = Boolean(accountId);
  return (
    <Box component='span' sx={{ display: 'inline-flex', alignItems: 'center', gap: ds.space[1] }}>
      <ScopeChip accountId='' readOnly={!inAccountMode} />
      {inAccountMode ? <ScopeChip accountId={accountId} /> : null}
    </Box>
  );
};

HybridScopeChips.propTypes = {
  accountId: PropTypes.string,
};

export default ScopeChip;
