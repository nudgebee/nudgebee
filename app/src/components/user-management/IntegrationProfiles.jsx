import React, { useState, useEffect, useCallback } from 'react';
import PropTypes from 'prop-types';
import { Box } from '@mui/material';
import { Button } from '@ui/Button';
import { Select } from '@ui/Select';
import { Chip } from '@ui/Chip';
import SafeIcon from '@shared/icons/SafeIcon';
import { ds } from 'src/utils/colors';
import apiUserManagement from '@api1/user';
import { hasWriteAccess } from '@lib/auth';
import slackLogo from '@assets/slack_icon.icon.svg';
import githubLogo from '@assets/github-icon.icon.svg';
import pagerdutyLogo from '@assets/auto-pilot/pager-duty.svg';
import zendutyLogo from '@assets/zenduty.jpeg';
import servicenowLogo from '@assets/servicenow.icon.svg';
import gitlabLogo from '@assets/gitlab.svg';
import jiraLogo from '@assets/jira_icon.icon.svg';
import msTeamsLogo from '@assets/ms_teams_s.svg';

// Provider display metadata for the Integration Profiles section. Keys match the
// integration_type values produced by the Identity Sync job.
const PROVIDER_META = {
  slack: { label: 'Slack', logo: slackLogo },
  github: { label: 'GitHub', logo: githubLogo },
  pagerduty: { label: 'PagerDuty', logo: pagerdutyLogo },
  zenduty: { label: 'ZenDuty', logo: zendutyLogo },
  servicenow: { label: 'ServiceNow', logo: servicenowLogo },
  gitlab: { label: 'GitLab', logo: gitlabLogo },
  jira: { label: 'Jira', logo: jiraLogo },
  ms_teams: { label: 'MS Teams', logo: msTeamsLogo },
};
const providerLabel = (t) => PROVIDER_META[t]?.label || t;
const providerLogo = (t) => PROVIDER_META[t]?.logo;

// IntegrationProfiles shows every external account (Slack/GitHub/PagerDuty/ZenDuty)
// linked to this user — auto-matched by email or mapped manually. In the default
// (editable) mode a tenant admin can map an as-yet-unmatched account or unmap an
// existing one; with `readOnly` it is a display-only list (no map/unmap controls,
// and the unmapped-accounts fetch is skipped) — used in the users-list expandable row.
export default function IntegrationProfiles({ userId, onNotify, readOnly = false, hideHeading = false }) {
  const [accounts, setAccounts] = useState([]);
  const [unmapped, setUnmapped] = useState([]);
  const [loading, setLoading] = useState(false);
  const [selectedType, setSelectedType] = useState('');
  const [selectedAccount, setSelectedAccount] = useState('');
  const [selectedToMap, setSelectedToMap] = useState('');
  const [busy, setBusy] = useState(false);
  // readOnly forces display-only regardless of write access; gating every write
  // affordance (and the unmapped fetch) on canEdit keeps the two modes in sync.
  const canEdit = hasWriteAccess() && !readOnly;

  const load = useCallback(async () => {
    if (!userId) return;
    setLoading(true);
    const [mapped, free] = await Promise.all([
      apiUserManagement.listIntegrationAccounts(userId),
      canEdit ? apiUserManagement.listUnmappedAccounts(null) : Promise.resolve([]),
    ]);
    setAccounts(Array.isArray(mapped) ? mapped : []);
    setUnmapped(Array.isArray(free) ? free : []);
    setLoading(false);
  }, [userId, canEdit]);

  useEffect(() => {
    load();
  }, [load]);

  const handleMap = async () => {
    if (!selectedToMap) return;
    setBusy(true);
    try {
      const res = await apiUserManagement.createAccountMapping({ mappingId: selectedToMap, userId });
      if (res?.id) {
        onNotify?.({ message: 'Integration account mapped', severity: 'success' });
        setSelectedToMap('');
        setSelectedAccount('');
        setSelectedType('');
        await load();
      } else {
        onNotify?.({ message: 'Failed to map account', severity: 'error' });
      }
    } catch (err) {
      onNotify?.({ message: err?.message || 'Failed to map account', severity: 'error' });
    } finally {
      setBusy(false);
    }
  };

  const handleUnmap = async (mappingId) => {
    setBusy(true);
    try {
      const res = await apiUserManagement.deleteAccountMapping({ mappingId });
      if (res?.id) {
        onNotify?.({ message: 'Mapping removed', severity: 'success' });
        await load();
      } else {
        onNotify?.({ message: 'Failed to remove mapping', severity: 'error' });
      }
    } catch (err) {
      onNotify?.({ message: err?.message || 'Failed to remove mapping', severity: 'error' });
    } finally {
      setBusy(false);
    }
  };

  // Tenant-scoped integrations (messaging/ticketing) have no account_id — show them
  // under an "All accounts" group rather than a blank header.
  const accountLabel = (a) => (a.account_id ? a.account_name || a.account_id : 'All accounts');
  const hasUnmapped = unmapped.length > 0;

  // Cascading filters keep the picker uncluttered: choose integration type, then
  // account, then the specific profile. Each step is scoped to the prior choice.
  const typeOptions = Array.from(new Set(unmapped.map((a) => a.integration_type))).map((t) => ({ value: t, label: providerLabel(t) }));

  const accountsForType = unmapped.filter((a) => a.integration_type === selectedType);
  const accountOptions = Array.from(
    new Map(accountsForType.map((a) => [a.account_id || '', { value: a.account_id || '', label: accountLabel(a) }])).values()
  );
  // With a single account (the common tenant-scoped case) there's nothing to choose.
  const showAccountStep = selectedType !== '' && accountOptions.length > 1;
  const effectiveAccount = showAccountStep ? selectedAccount : accountOptions[0]?.value ?? '';

  const profileOptions = accountsForType
    .filter((a) => (a.account_id || '') === effectiveAccount)
    .map((a) => ({
      value: a.id,
      label: `${a.display_name || a.username || a.external_user_id}${a.email ? ` (${a.email})` : ''}`,
    }));

  // Group mapped profiles by cloud account — an identity scoped to multiple
  // accounts shows once under each account.
  const accountGroups = [];
  const groupIndex = new Map();
  for (const a of accounts) {
    const key = a.account_id || 'unknown';
    if (!groupIndex.has(key)) {
      const group = { key, name: accountLabel(a), items: [] };
      groupIndex.set(key, group);
      accountGroups.push(group);
    }
    groupIndex.get(key).items.push(a);
  }

  return (
    // Cap the width so account rows don't stretch edge-to-edge in the wide users-list
    // expandable row (which would shove the Auto/Manual badge far from the account).
    // Inside the narrower Edit-User modal this is a no-op.
    <Box data-testid='user-modal-integration-profiles' sx={{ maxWidth: 720 }}>
      {/* Heading is redundant in the users-list tab (the tab is already labelled
          "Integration profiles") — hidden there; kept in the Edit User modal,
          where it titles one section among several. */}
      {!hideHeading && (
        <Box component='label' sx={{ display: 'block', font: "500 12px/1.2 'Roboto'", color: ds.gray[700], mb: 'var(--ds-space-2)' }}>
          Integration profiles
        </Box>
      )}

      {loading ? (
        <Box sx={{ font: "400 12px/1.4 'Roboto'", color: ds.gray[400] }}>Loading…</Box>
      ) : accounts.length === 0 ? (
        <Box sx={{ font: "400 12px/1.4 'Roboto'", color: ds.gray[400] }}>
          No linked integration accounts yet. The Identity Sync maps accounts to users by email automatically.
        </Box>
      ) : (
        <Box sx={{ display: 'flex', flexDirection: 'column', gap: 'var(--ds-space-3)' }}>
          {accountGroups.map((group) => (
            <Box key={group.key} sx={{ display: 'flex', flexDirection: 'column', gap: 'var(--ds-space-2)' }}>
              <Box
                sx={{
                  font: "600 11px/1.2 'Roboto'",
                  letterSpacing: '0.04em',
                  textTransform: 'uppercase',
                  color: ds.gray[400],
                }}
              >
                {group.name}
              </Box>
              <Box sx={{ display: 'flex', flexWrap: 'wrap', gap: 'var(--ds-space-2)' }}>
                {group.items.map((a) => (
                  <Box
                    key={a.id}
                    data-testid={`integration-account-${a.integration_type}`}
                    sx={{
                      flex: '1 1 280px',
                      maxWidth: 380,
                      display: 'flex',
                      alignItems: 'center',
                      gap: 'var(--ds-space-3)',
                      padding: 'var(--ds-space-2) var(--ds-space-3)',
                      border: `1px solid ${ds.background[300]}`,
                      borderRadius: 'var(--ds-radius-md)',
                      transition: 'border-color 0.15s, box-shadow 0.15s',
                      '&:hover': { borderColor: ds.gray[400], boxShadow: `0px 1px 3px 0px ${ds.gray.alpha[300]}` },
                    }}
                  >
                    {/* Provider logo in a rounded avatar */}
                    <Box
                      sx={{
                        width: 36,
                        height: 36,
                        flexShrink: 0,
                        borderRadius: 'var(--ds-radius-pill)',
                        border: `1px solid ${ds.background[300]}`,
                        background: ds.background[100],
                        display: 'flex',
                        alignItems: 'center',
                        justifyContent: 'center',
                        '& img, & svg': { width: 20, height: 20, objectFit: 'contain' },
                      }}
                    >
                      {providerLogo(a.integration_type) && (
                        <SafeIcon src={providerLogo(a.integration_type)} alt={providerLabel(a.integration_type)} width={20} height={20} />
                      )}
                    </Box>

                    {/* Account identity: name on top, provider · email beneath */}
                    <Box sx={{ flex: 1, minWidth: 0 }}>
                      <Box
                        sx={{
                          font: "600 13px/1.3 'Roboto'",
                          color: ds.gray[700],
                          overflow: 'hidden',
                          textOverflow: 'ellipsis',
                          whiteSpace: 'nowrap',
                        }}
                      >
                        {a.display_name || a.username || a.external_user_id}
                      </Box>
                      <Box
                        sx={{
                          font: "400 11px/1.3 'Roboto'",
                          color: ds.gray[400],
                          overflow: 'hidden',
                          textOverflow: 'ellipsis',
                          whiteSpace: 'nowrap',
                        }}
                      >
                        {providerLabel(a.integration_type)}
                        {(a.email || a.username) && ` · ${a.email || a.username}`}
                      </Box>
                    </Box>

                    <Chip variant='status' size='xs' dot tone={a.mapped_via === 'manual' ? 'info' : 'success'}>
                      {a.mapped_via === 'manual' ? 'Manual' : 'Auto'}
                    </Chip>
                    {canEdit && (
                      <Button id={`integration-account-unmap-${a.id}`} tone='secondary' size='sm' disabled={busy} onClick={() => handleUnmap(a.id)}>
                        Unmap
                      </Button>
                    )}
                  </Box>
                ))}
              </Box>
            </Box>
          ))}
        </Box>
      )}

      {canEdit && !loading && (
        <Box sx={{ mt: 'var(--ds-space-3)' }}>
          <Box component='label' sx={{ display: 'block', font: "500 12px/1.2 'Roboto'", color: ds.gray[700], mb: 'var(--ds-space-2)' }}>
            Map an integration account
          </Box>

          {!hasUnmapped ? (
            <Box sx={{ font: "400 11px/1.4 'Roboto'", color: ds.gray[400] }}>
              No unmatched accounts to map. Accounts the Identity Sync discovered but couldn&apos;t match by email appear here; the sync runs every 30
              minutes across your Slack, GitHub, PagerDuty and ZenDuty integrations.
            </Box>
          ) : (
            <Box sx={{ display: 'flex', flexDirection: 'column', gap: 'var(--ds-space-2)' }}>
              {/* Step 1: integration type, + account when the type spans several */}
              <Box sx={{ display: 'flex', gap: 'var(--ds-space-2)', '& > *': { flex: 1, minWidth: 0 } }}>
                <Select
                  id='user-modal-map-type'
                  placeholder='Integration type'
                  value={selectedType}
                  options={typeOptions}
                  onChange={(next) => {
                    setSelectedType(next);
                    setSelectedAccount('');
                    setSelectedToMap('');
                  }}
                  minWidth='100%'
                />
                {showAccountStep && (
                  <Select
                    id='user-modal-map-account-filter'
                    placeholder='Account'
                    value={selectedAccount}
                    options={accountOptions}
                    onChange={(next) => {
                      setSelectedAccount(next);
                      setSelectedToMap('');
                    }}
                    minWidth='100%'
                  />
                )}
              </Box>

              {/* Step 2: the specific unmatched profile, scoped to the choices above */}
              <Box sx={{ display: 'flex', alignItems: 'flex-end', gap: 'var(--ds-space-2)' }}>
                <Box sx={{ flex: 1, minWidth: 0 }}>
                  <Select
                    id='user-modal-map-account'
                    placeholder={selectedType ? 'Select an User account' : 'Pick an integration type first'}
                    value={selectedToMap}
                    options={profileOptions}
                    onChange={(next) => setSelectedToMap(next)}
                    disabled={!selectedType || (showAccountStep && !selectedAccount)}
                    minWidth='100%'
                  />
                </Box>
                <Button id='user-modal-map-account-button' size='md' disabled={!selectedToMap || busy} loading={busy} onClick={handleMap}>
                  Map
                </Button>
              </Box>
            </Box>
          )}
        </Box>
      )}
    </Box>
  );
}

IntegrationProfiles.propTypes = {
  userId: PropTypes.string,
  onNotify: PropTypes.func,
  readOnly: PropTypes.bool,
  hideHeading: PropTypes.bool,
};
