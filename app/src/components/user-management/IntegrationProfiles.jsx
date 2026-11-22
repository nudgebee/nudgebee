import React, { useState, useEffect, useCallback, forwardRef, useImperativeHandle } from 'react';
import PropTypes from 'prop-types';
import { Box } from '@mui/material';
import KeyboardArrowDownIcon from '@mui/icons-material/KeyboardArrowDown';
import { Button } from '@ui/Button';
import { Select } from '@ui/Select';
import { Chip } from '@ui/Chip';
import SafeIcon from '@shared/icons/SafeIcon';
import Datetime from '@shared/format/Datetime';
import { ds } from 'src/utils/colors';
import apiUserManagement from '@api1/user';
import { canManage } from '@lib/auth';
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

// integrations.name is the connection name an admin types when configuring an
// integration ("PagerDuty-test"), so it's worth showing. These two are installed via
// OAuth instead, where nothing prompts for a name and the installer stores the
// workspace identifier it needs to resolve the install by — a Slack team id
// (T05JW…) or an MS Teams account GUID. Meaningless to a reader, so don't show it.
const OPAQUE_INSTANCE_NAME_TYPES = new Set(['slack', 'ms_teams']);
const hasReadableInstanceName = (a) => Boolean(a.integration_name) && !OPAQUE_INSTANCE_NAME_TYPES.has(a.integration_type);

// Small caption above each picker control so every step names itself — keeps the
// three-step map flow (type → account → profile) self-explanatory.
const FIELD_LABEL_SX = { display: 'block', font: "500 11px/1.2 'Roboto'", color: ds.gray[600], mb: 'var(--ds-space-1)' };

// One label/value line in a mapped card's expanded detail panel.
const detailRow = (label, value) => (
  <Box sx={{ display: 'flex', gap: 'var(--ds-space-2)' }}>
    <Box sx={{ flexShrink: 0, width: 96, font: "500 11px/1.4 'Roboto'", color: ds.gray[400] }}>{label}</Box>
    <Box sx={{ flex: 1, minWidth: 0, font: "400 11px/1.4 'Roboto'", color: ds.gray[700], wordBreak: 'break-word' }}>{value}</Box>
  </Box>
);

// IntegrationProfiles shows every external account (Slack/GitHub/PagerDuty/ZenDuty)
// linked to this user — auto-matched by email or mapped manually. In the default
// (editable) mode a tenant admin can map an as-yet-unmatched account or unmap an
// existing one; with `readOnly` it is a display-only list (no map/unmap controls,
// and the unmapped-accounts fetch is skipped) — used in the users-list expandable row.
const IntegrationProfiles = forwardRef(function IntegrationProfiles({ userId, onNotify, readOnly = false, hideHeading = false }, ref) {
  const [accounts, setAccounts] = useState([]);
  const [unmapped, setUnmapped] = useState([]);
  const [loading, setLoading] = useState(false);
  const [selectedType, setSelectedType] = useState('');
  const [selectedInstance, setSelectedInstance] = useState('');
  const [selectedToMap, setSelectedToMap] = useState('');
  // Which mapped card is expanded to show its mapping details (one at a time).
  const [expandedId, setExpandedId] = useState(null);
  // Staged edits — nothing is written to the backend until the parent modal's Save
  // runs applyPendingChanges(). pendingUnmap holds ids of persisted mappings marked
  // for deletion; pendingMap holds unmapped profiles chosen to be created. Both are
  // discarded on unmount (Cancel / closing the dialog), so a mis-click is reversible
  // until Save — which is what the dialog's Cancel/Save buttons promise.
  const [pendingUnmap, setPendingUnmap] = useState(() => new Set());
  const [pendingMap, setPendingMap] = useState([]);
  // readOnly forces display-only regardless of write access; gating every write
  // affordance (and the unmapped fetch) on canEdit keeps the two modes in sync.
  const canEdit = canManage('users', 'Write') && !readOnly;

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

  // Stage a map: remember the chosen profile and clear the picker. Written on Save.
  const handleMap = () => {
    if (!selectedToMap) return;
    const profile = unmapped.find((a) => a.id === selectedToMap);
    if (!profile) return;
    setPendingMap((prev) => (prev.some((p) => p.id === profile.id) ? prev : [...prev, profile]));
    setSelectedToMap('');
    setSelectedInstance('');
    setSelectedType('');
  };

  // Stage / un-stage removal of a persisted mapping — toggles the card's Undo state.
  const toggleUnmap = (mappingId) => {
    setPendingUnmap((prev) => {
      const next = new Set(prev);
      if (next.has(mappingId)) next.delete(mappingId);
      else next.add(mappingId);
      return next;
    });
  };

  // Drop a staged (not-yet-saved) map before it is written.
  const handleRemovePending = (mappingId) => {
    setPendingMap((prev) => prev.filter((p) => p.id !== mappingId));
  };

  // Flush staged edits — called by the parent modal on Save changes. Deletes the
  // unmaps, creates the maps, then reloads the real list. Returns false (and toasts)
  // if any op failed so the modal can hold its success path; true when there was
  // nothing to do or everything succeeded.
  useImperativeHandle(ref, () => ({
    applyPendingChanges: async () => {
      if (pendingUnmap.size === 0 && pendingMap.length === 0) return true;
      const unmapIds = Array.from(pendingUnmap);
      const mapProfiles = pendingMap;
      const results = await Promise.allSettled([
        ...unmapIds.map((mappingId) => apiUserManagement.deleteAccountMapping({ mappingId })),
        ...mapProfiles.map((p) => apiUserManagement.createAccountMapping({ mappingId: p.id, userId })),
      ]);
      const succeeded = (r) => r.status === 'fulfilled' && r.value?.id;
      // Keep only the ops that failed staged, so Save can retry them without the user
      // re-selecting — and without re-issuing the ones that already went through.
      const failedUnmaps = unmapIds.filter((_, i) => !succeeded(results[i]));
      const failedMaps = mapProfiles.filter((_, i) => !succeeded(results[unmapIds.length + i]));
      setPendingUnmap(new Set(failedUnmaps));
      setPendingMap(failedMaps);
      await load();
      if (failedUnmaps.length > 0 || failedMaps.length > 0) {
        onNotify?.({ message: 'Some integration profile changes could not be saved', severity: 'error' });
        return false;
      }
      return true;
    },
  }));

  // Tenant-scoped integrations (messaging/ticketing) have no account_id — show them
  // under an "All accounts" group rather than a blank header.
  const accountLabel = (a) => (a.account_id ? a.account_name || a.account_id : 'All accounts');

  // An "integration instance" is one configured connection (e.g. one of two ServiceNow
  // connections). It's distinguished by integration_id and labelled by integration_name;
  // mapping is per-instance on the backend, so the picker keys on the instance, not the
  // cloud account. Fall back to account_id / accountLabel for legacy rows lacking an
  // integration_id or name.
  const instanceId = (a) => a.integration_id || a.account_id || '';
  const instanceLabel = (a) => a.integration_name || accountLabel(a);

  // Hide instances the user is already mapped to — only surface ones they aren't mapped
  // in yet. Key on integration_type + account_id + integration_id so being mapped in one
  // instance never hides a sibling instance of the same type the user isn't mapped in.
  const instanceKey = (a) => `${a.integration_type}|${a.account_id || ''}|${a.integration_id || ''}`;
  const mappedKeys = new Set(accounts.map(instanceKey));
  // A staged map also hides its instance/profile from the picker, so you can't queue
  // the same mapping twice before saving.
  const stagedKeys = new Set(pendingMap.map(instanceKey));
  const pendingMapIds = new Set(pendingMap.map((p) => p.id));
  const mappableUnmapped = unmapped.filter((a) => !mappedKeys.has(instanceKey(a)) && !stagedKeys.has(instanceKey(a)) && !pendingMapIds.has(a.id));

  // Cascading filters keep the picker uncluttered: choose integration type, then
  // integration instance, then the specific profile. Each step is scoped to the prior choice.
  const typeOptions = Array.from(new Set(mappableUnmapped.map((a) => a.integration_type))).map((t) => ({ value: t, label: providerLabel(t) }));
  // Only mappable when there's at least one not-yet-integrated instance to choose.
  const hasUnmapped = mappableUnmapped.length > 0;

  const instancesForType = mappableUnmapped.filter((a) => a.integration_type === selectedType);
  // One option per distinct instance. Use the integration name only when the type
  // actually spans multiple instances (where it disambiguates); for a single instance
  // the name adds nothing and is often a cryptic id (Slack team id, MS Teams uuid), so
  // fall back to the friendly account label.
  const instanceRows = Array.from(new Map(instancesForType.map((a) => [instanceId(a), a])).values());
  const instanceOptions = instanceRows.map((a) => ({
    value: instanceId(a),
    label: instanceRows.length > 1 ? instanceLabel(a) : accountLabel(a),
  }));
  // The instance step only matters when a type spans multiple instances; with a single
  // instance there's nothing to choose, so the selector is hidden (not shown as a
  // confusing greyed-out box). effectiveInstance always resolves to the chosen instance
  // or the sole/first one, so the profile list is never empty.
  const instanceStepEnabled = selectedType !== '' && instanceOptions.length > 1;
  const effectiveInstance = selectedInstance || instanceOptions[0]?.value || '';

  const profileOptions = instancesForType
    .filter((a) => instanceId(a) === effectiveInstance)
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

  // A person can be mapped into multiple instances of one type (each shows as its own
  // card). Surface the instance name on a card only for types where this user has 2+
  // instances, so it disambiguates the duplicates without cluttering the common
  // single-instance case (or exposing cryptic instance ids like a Slack team id).
  const instancesPerType = new Map();
  for (const a of accounts) {
    if (!instancesPerType.has(a.integration_type)) {
      instancesPerType.set(a.integration_type, new Set());
    }
    instancesPerType.get(a.integration_type).add(a.integration_id || '');
  }
  const showInstanceName = (a) => (instancesPerType.get(a.integration_type)?.size ?? 0) > 1 && Boolean(a.integration_name);

  return (
    // In the narrow Edit-User modal, cap the width so cards don't stretch. In the wide
    // read-only users-list tab, let the grid fill the full width (cards flow into more
    // columns instead of wasting the right half) — per-card maxWidth keeps each tidy.
    <Box data-testid='user-modal-integration-profiles' sx={{ maxWidth: readOnly ? 'none' : 720 }}>
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
      ) : accounts.length === 0 && pendingMap.length === 0 ? (
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
              <Box
                sx={{
                  display: 'grid',
                  // Single column in the narrow Edit-User modal so each card has room to show
                  // the account name + provider · email — in two cramped columns the identity
                  // text truncated to nothing, leaving cards that looked empty. The wide
                  // read-only users-list tab flows cards into as many columns as fit.
                  // minWidth:0 lets tracks shrink so long emails truncate with an ellipsis
                  // instead of overflowing the modal body's clipped (overflowX:hidden) edge.
                  gridTemplateColumns: readOnly ? 'repeat(auto-fill, minmax(280px, 1fr))' : '1fr',
                  gap: 'var(--ds-space-2)',
                  alignItems: 'start',
                  '& > *': { minWidth: 0 },
                }}
              >
                {group.items.map((a) => {
                  const expanded = expandedId === a.id;
                  const staged = pendingUnmap.has(a.id);
                  return (
                    <Box
                      key={a.id}
                      data-testid={`integration-account-${a.integration_type}`}
                      sx={{
                        display: 'flex',
                        flexDirection: 'column',
                        border: `1px solid ${expanded ? ds.gray[400] : ds.background[300]}`,
                        borderRadius: 'var(--ds-radius-md)',
                        opacity: staged ? 0.55 : 1,
                        transition: 'border-color 0.15s, box-shadow 0.15s, opacity 0.15s',
                        '&:hover': { borderColor: ds.gray[400], boxShadow: `0px 1px 3px 0px ${ds.gray.alpha[300]}` },
                      }}
                    >
                      {/* Clickable summary row — toggles the detail panel below */}
                      <Box
                        onClick={() => setExpandedId(expanded ? null : a.id)}
                        sx={{
                          display: 'flex',
                          alignItems: 'center',
                          gap: 'var(--ds-space-3)',
                          padding: 'var(--ds-space-2) var(--ds-space-3)',
                          cursor: 'pointer',
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
                              textDecoration: staged ? 'line-through' : 'none',
                              overflow: 'hidden',
                              textOverflow: 'ellipsis',
                              whiteSpace: 'nowrap',
                            }}
                          >
                            {a.display_name || a.username || a.external_user_id}
                          </Box>
                          {(() => {
                            // Tiles for a type with 2+ instances (e.g. PagerDuty) all share the
                            // same email, so it's repeated noise — show "Provider · instance name"
                            // instead, which is what actually distinguishes the cards. Single-
                            // instance tiles (Slack/ZenDuty) have no separate instance name worth
                            // showing, so keep "Provider · email" as the identifier there.
                            const subtitle = showInstanceName(a)
                              ? [providerLabel(a.integration_type), a.integration_name].filter(Boolean).join(' · ')
                              : [providerLabel(a.integration_type), a.email || a.username].filter(Boolean).join(' · ');
                            return (
                              <Box
                                title={subtitle}
                                sx={{
                                  font: "400 11px/1.3 'Roboto'",
                                  color: ds.gray[400],
                                  overflow: 'hidden',
                                  textOverflow: 'ellipsis',
                                  whiteSpace: 'nowrap',
                                }}
                              >
                                {subtitle}
                              </Box>
                            );
                          })()}
                        </Box>

                        {staged ? (
                          <Chip variant='status' size='xs' dot tone='critical'>
                            Unsaved
                          </Chip>
                        ) : (
                          <Chip variant='status' size='xs' dot tone={a.mapped_via === 'manual' ? 'info' : 'success'}>
                            {a.mapped_via === 'manual' ? 'Manual' : 'Auto'}
                          </Chip>
                        )}
                        {canEdit && (
                          <Button
                            id={`integration-account-unmap-${a.id}`}
                            tone='secondary'
                            size='sm'
                            onClick={(e) => {
                              e.stopPropagation();
                              toggleUnmap(a.id);
                            }}
                          >
                            {staged ? 'Undo' : 'Unmap'}
                          </Button>
                        )}
                        <KeyboardArrowDownIcon
                          sx={{
                            fontSize: 18,
                            flexShrink: 0,
                            color: ds.gray[400],
                            transition: 'transform 0.15s',
                            transform: expanded ? 'rotate(0deg)' : 'rotate(-90deg)',
                          }}
                        />
                      </Box>

                      {/* Expanded detail: how it was mapped, by whom, when last synced */}
                      {expanded && (
                        <Box
                          sx={{
                            display: 'flex',
                            flexDirection: 'column',
                            gap: 'var(--ds-space-1)',
                            padding: 'var(--ds-space-2) var(--ds-space-3) var(--ds-space-3)',
                            borderTop: `1px solid ${ds.background[300]}`,
                          }}
                        >
                          {detailRow('How mapped', a.mapped_via === 'manual' ? 'Manual' : 'Auto · matched by email')}
                          {a.mapped_via === 'manual' && detailRow('Mapped by', a.mapped_by_name || '—')}
                          {a.last_synced_at &&
                            detailRow(
                              'Last synced',
                              <Datetime value={a.last_synced_at} sx={{ fontSize: ds.text.caption }} sxSuffix={{ fontSize: ds.text.caption }} />
                            )}
                          {detailRow('External ID', a.external_user_id)}
                          {hasReadableInstanceName(a) && detailRow('Integration', a.integration_name)}
                          {a.email && detailRow('Email', a.email)}
                        </Box>
                      )}
                    </Box>
                  );
                })}
              </Box>
            </Box>
          ))}

          {/* Staged maps — shown as dashed "Pending" cards until Save writes them. */}
          {pendingMap.length > 0 && (
            <Box sx={{ display: 'flex', flexDirection: 'column', gap: 'var(--ds-space-2)' }}>
              <Box
                sx={{
                  font: "600 11px/1.2 'Roboto'",
                  letterSpacing: '0.04em',
                  textTransform: 'uppercase',
                  color: ds.gray[400],
                }}
              >
                To be added on save
              </Box>
              <Box
                sx={{
                  display: 'grid',
                  gridTemplateColumns: readOnly ? 'repeat(auto-fill, minmax(280px, 1fr))' : '1fr',
                  gap: 'var(--ds-space-2)',
                  alignItems: 'start',
                  '& > *': { minWidth: 0 },
                }}
              >
                {pendingMap.map((a) => (
                  <Box
                    key={`pending-${a.id}`}
                    data-testid={`integration-account-pending-${a.integration_type}`}
                    sx={{
                      display: 'flex',
                      alignItems: 'center',
                      gap: 'var(--ds-space-3)',
                      padding: 'var(--ds-space-2) var(--ds-space-3)',
                      border: `1px dashed ${ds.gray[400]}`,
                      borderRadius: 'var(--ds-radius-md)',
                    }}
                  >
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
                        {[providerLabel(a.integration_type), a.email || a.username].filter(Boolean).join(' · ')}
                      </Box>
                    </Box>
                    <Chip variant='status' size='xs' dot tone='warning'>
                      Unsaved
                    </Chip>
                    {canEdit && (
                      <Button id={`integration-account-remove-pending-${a.id}`} tone='secondary' size='sm' onClick={() => handleRemovePending(a.id)}>
                        Remove
                      </Button>
                    )}
                  </Box>
                ))}
              </Box>
            </Box>
          )}
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
              {/* Step 1: integration type, plus an instance selector only when the type
                  spans 2+ instances (hidden otherwise — nothing to choose). */}
              <Box sx={{ display: 'flex', gap: 'var(--ds-space-2)', '& > *': { flex: 1, minWidth: 0 } }}>
                <Box>
                  <Box component='label' sx={FIELD_LABEL_SX}>
                    Integration type
                  </Box>
                  <Select
                    id='user-modal-map-type'
                    placeholder='Select…'
                    value={selectedType}
                    options={typeOptions}
                    onChange={(next) => {
                      setSelectedType(next);
                      setSelectedInstance('');
                      setSelectedToMap('');
                    }}
                    minWidth='100%'
                  />
                </Box>
                {instanceStepEnabled && (
                  <Box>
                    <Box component='label' sx={FIELD_LABEL_SX}>
                      Integration
                    </Box>
                    <Select
                      id='user-modal-map-account-filter'
                      placeholder='Select…'
                      value={effectiveInstance}
                      options={instanceOptions}
                      onChange={(next) => {
                        setSelectedInstance(next);
                        setSelectedToMap('');
                      }}
                      minWidth='100%'
                    />
                  </Box>
                )}
              </Box>

              {/* Step 2: the specific unmatched profile (a person), scoped to the choices above */}
              <Box sx={{ display: 'flex', alignItems: 'flex-end', gap: 'var(--ds-space-2)' }}>
                <Box sx={{ flex: 1, minWidth: 0 }}>
                  <Box component='label' sx={FIELD_LABEL_SX}>
                    Profile
                  </Box>
                  <Select
                    id='user-modal-map-account'
                    placeholder={selectedType ? 'Select a profile' : 'Pick an integration type first'}
                    value={selectedToMap}
                    options={profileOptions}
                    onChange={(next) => setSelectedToMap(next)}
                    disabled={!selectedType}
                    minWidth='100%'
                  />
                </Box>
                <Button id='user-modal-map-account-button' size='md' disabled={!selectedToMap} onClick={handleMap}>
                  Map
                </Button>
              </Box>
            </Box>
          )}
        </Box>
      )}
    </Box>
  );
});

IntegrationProfiles.propTypes = {
  userId: PropTypes.string,
  onNotify: PropTypes.func,
  readOnly: PropTypes.bool,
  hideHeading: PropTypes.bool,
};

export default IntegrationProfiles;
