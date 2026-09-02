/**
 * The provider row of the panel editor's Source card: whether the panel's
 * accounts can actually answer the query it is being written for.
 *
 * It has two modes, because a panel has two.
 *
 * A panel that NAMES its provider speaks one query language, and the row's job
 * is to name the accounts that cannot read it — the author can drop them, or add
 * a second panel for the other provider. It stays silent when they all match:
 * the Select above already says which provider this is.
 *
 * A panel that names none is the pre-existing shape — every account falls back to
 * its own default — so the row reports what those defaults are and warns when
 * they disagree, which is the state that produces a panel that works on one
 * account and errors on the next.
 *
 * The badge deliberately mirrors the log-provider badge on the Kubernetes Logs
 * tab (KubernetesLogs.tsx) — same shape, same icon, same label — so the two
 * places that name a provider read as one idea.
 */
import React from 'react';
import { Box, Stack, Typography } from '@mui/material';
import Tooltip from '@ui/Tooltip';
import { Banner } from '@ui/Banner';
import { Skeleton } from '@ui/Skeleton';
import CloudProviderIcon from '@shared/icons/CloudIcon';
import {
  disabledAccounts,
  groupByProvider,
  isMixed,
  mismatchedAccounts,
  providerIconKey,
  providerLabel,
  type AccountProvider,
  type ProviderType,
} from './panelProviders';

const badgeSx = {
  display: 'inline-flex',
  alignItems: 'center',
  gap: 'var(--ds-space-2)',
  padding: 'var(--ds-space-1) var(--ds-space-3)',
  backgroundColor: 'var(--ds-gray-alpha-100)',
  borderRadius: 'var(--ds-radius-md)',
  border: '1px solid var(--ds-gray-alpha-200)',
  minWidth: 'fit-content',
} as const;

const captionSx = {
  fontSize: 'var(--ds-text-caption)',
  color: 'var(--ds-gray-600)',
} as const;

function ProviderBadge({ provider, count }: { provider: string; count?: number }) {
  return (
    <Box sx={badgeSx}>
      <CloudProviderIcon cloud_provider={providerIconKey(provider)} width='16px' height='16px' />
      <Typography sx={{ fontSize: 'var(--ds-text-body)', fontWeight: 'var(--ds-font-weight-semibold)', color: 'var(--ds-gray-700)' }}>
        {providerLabel(provider)}
      </Typography>
      {count !== undefined && <Typography sx={captionSx}>{count}</Typography>}
    </Box>
  );
}

interface Props {
  providerType: ProviderType;
  loading: boolean;
  entries: AccountProvider[];
  /** Accounts in scope. Larger than `entries` when the fan-out was capped. */
  total: number;
  /** The provider the panel names, or '' for "each account's own default". */
  declared: string;
}

export default function PanelProviderRow({ providerType, loading, entries, total, declared }: Props) {
  if (loading && entries.length === 0) return <Skeleton shape='rect' width={140} height={28} ariaLabel='Resolving provider' />;
  if (entries.length === 0) return null;

  // A disabled account outranks every other message here: it answers nothing at
  // all, so which provider it would have used, or whether that matches the
  // panel's, are both moot until someone re-enables it.
  const disabled = disabledAccounts(entries);
  const live = entries.filter((e) => !e.disabled);

  // Never let a capped fan-out read as the whole answer.
  const unchecked = Math.max(0, total - entries.length);
  const uncheckedNote = unchecked > 0 && (
    <Typography sx={captionSx}>
      (first {entries.length} of {total} accounts checked)
    </Typography>
  );

  const disabledBanner = disabled.length > 0 && (
    <Banner
      tone={disabled.length === entries.length ? 'critical' : 'warning'}
      surface='section'
      message={`${disabled.join(', ')} ${disabled.length > 1 ? 'are' : 'is'} disabled and will return nothing. ${
        disabled.length === entries.length
          ? 'This panel has no live account to query.'
          : 'Re-enable ' +
            (disabled.length > 1 ? 'them' : 'it') +
            ' under Accounts, or drop ' +
            (disabled.length > 1 ? 'them' : 'it') +
            ' from this panel.'
      }`}
    />
  );

  // Nothing live left to say anything about.
  if (live.length === 0) {
    return (
      <Stack gap={1}>
        {uncheckedNote}
        {disabledBanner}
      </Stack>
    );
  }

  if (declared) {
    const mismatched = mismatchedAccounts(entries, declared);
    if (mismatched.length === 0 || disabledBanner) {
      // One Banner per surface: a disabled account is the more actionable of the
      // two, and re-enabling it may resolve the provider question anyway.
      if (!disabledBanner) return uncheckedNote || null;
      return (
        <Stack gap={1}>
          {uncheckedNote}
          {disabledBanner}
        </Stack>
      );
    }
    const label = providerLabel(declared);
    // All of them missing the provider is a panel that cannot render at all;
    // some of them is one that renders until the viewer switches accounts.
    const all = mismatched.length === live.length;
    return (
      <Stack gap={1}>
        {uncheckedNote}
        <Banner
          tone={all ? 'critical' : 'warning'}
          surface='section'
          message={`${mismatched.join(', ')} ${mismatched.length > 1 ? 'do' : 'does'} not have ${label} configured for ${providerType}, so ${
            mismatched.length > 1 ? 'they' : 'it'
          } will return nothing here. Remove ${mismatched.length > 1 ? 'them' : 'it'}, or add a second panel for ${
            mismatched.length > 1 ? 'their' : 'its'
          } provider.`}
        />
      </Stack>
    );
  }

  const groups = groupByProvider(entries);
  const mixed = isMixed(groups);
  const unconfigured = groups.find((g) => !g.provider);

  // One provider across the board, nothing else configured — the plain case, and
  // the one that must stay quiet.
  if (!mixed && groups[0].provider) {
    const others = live.length === 1 ? live[0].available.filter((p) => p !== groups[0].provider) : [];
    return (
      <Stack gap={1}>
        <Stack direction='row' alignItems='center' gap={1} flexWrap='wrap'>
          <ProviderBadge provider={groups[0].provider} />
          {live.length > 1 && !unchecked && <Typography sx={captionSx}>all {live.length} accounts</Typography>}
          {uncheckedNote}
          {others.length > 0 && (
            <Tooltip title={`Also configured: ${others.map(providerLabel).join(', ')}. Name one above to pin this panel to it.`}>
              <Typography sx={{ ...captionSx, cursor: 'help', textDecoration: 'underline dotted' }}>
                {others.length} other{others.length > 1 ? 's' : ''} configured
              </Typography>
            </Tooltip>
          )}
        </Stack>
        {/* The live accounts agreeing does not make a disabled one stop mattering. */}
        {disabledBanner}
      </Stack>
    );
  }

  return (
    <Stack gap={1}>
      <Stack direction='row' alignItems='center' gap={1} flexWrap='wrap'>
        {groups
          .filter((g) => g.provider)
          .map((g) => (
            <Tooltip key={g.provider} title={g.accounts.join(', ')}>
              <Box component='span' sx={{ display: 'inline-flex' }}>
                <ProviderBadge provider={g.provider} count={mixed ? g.accounts.length : undefined} />
              </Box>
            </Tooltip>
          ))}
        {uncheckedNote}
      </Stack>
      {/* One Banner per surface, per the Banner spec. A disabled account outranks
          both of the others: it is the concrete, fixable thing. */}
      {disabledBanner ? (
        disabledBanner
      ) : unconfigured ? (
        <Banner
          tone='critical'
          surface='section'
          message={`No ${providerType} provider is configured for ${unconfigured.accounts.join(', ')}. ${
            unconfigured.accounts.length > 1 ? 'These accounts' : 'This account'
          } will return nothing. Configure one under Integrations.`}
        />
      ) : (
        <Banner
          tone='warning'
          surface='section'
          message={`These accounts use different ${providerType} providers. Name the one this panel's query is written for above, and add a second panel for the other.`}
        />
      )}
    </Stack>
  );
}
