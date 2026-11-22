import React, { useCallback, useEffect, useState } from 'react';
import Box from '@mui/material/Box';
import { ds } from 'src/utils/colors';
import Text from '@shared/format/Text';
import Datetime from '@shared/format/Datetime';
import { Button } from '@ui/Button';
import { Link } from '@ui/Link';
import { Modal } from '@ui/Modal';
import { Select } from '@ui/Select';
import { Banner } from '@ui/Banner';
import { DropdownMenu } from '@ui/DropdownMenu';
import { MoreVert } from '@mui/icons-material';
import { toast as snackbar } from '@ui/Toast';
import { useBrandingConfig } from '@hooks/useTenantBranding';
import { ALERT_RULE_CHANGED_EVENT } from './cards/AlertRuleChangeAction';
import k8sApi, { firstGraphQLError, type EventAlertControls, type EventMuteScope, type EventMuteScopeName } from '@api1/kubernetes1';
import {
  DEFAULT_MUTE_DURATION,
  MUTE_DURATION_OPTIONS,
  alertDisplayName,
  canDisableAtSource,
  canEditRule,
  hasEventAlertControls,
  mutableScopes,
  mutedScopeText,
  mutedScopes,
  otherSuppressions,
  ruleLocationText,
  scopeImpactText,
  scopeOptionLabel,
} from './eventAlertControls';

type Props = {
  accountId: string;
  eventId: string;
  canWrite: boolean;
  // Alert Manager filtered to this alert, and the account's Triage Rules.
  alertManagerHref: string;
  triageRulesHref: string;
  headingSx: Record<string, unknown>;
  // Whether the section rendered, so the tab can drop "No fixes are available".
  onVisibilityChange?: (visible: boolean) => void;
  // Render only the mute dialog, opened each time muteRequest changes (the remediation loop's "More actions" menu
  // opens it); onCanMuteChange says whether there is anything to mute.
  dialogOnly?: boolean;
  muteRequest?: number;
  onCanMuteChange?: (canMute: boolean) => void;
};

const captionSx = { fontSize: ds.text.caption, color: ds.gray[600] };

// "Stop this alert" on the Remediation tab: mute the event's alert in the product
// (for this workload or everywhere, for a set time), or change the rule where it
// is defined when the backend says that change really reaches it. A mute is not a
// fix, so nothing here writes to the event's remediation history.
export default function EventAlertControlsSection({
  accountId,
  eventId,
  canWrite,
  alertManagerHref,
  triageRulesHref,
  headingSx,
  onVisibilityChange,
  dialogOnly = false,
  muteRequest = 0,
  onCanMuteChange,
}: Props) {
  const { title: brand } = useBrandingConfig();
  const [controls, setControls] = useState<EventAlertControls | null>(null);
  const [muteOpen, setMuteOpen] = useState(false);
  const [scope, setScope] = useState<EventMuteScopeName | ''>('');
  const [duration, setDuration] = useState(DEFAULT_MUTE_DURATION);
  const [toggleOpen, setToggleOpen] = useState(false);
  const [busy, setBusy] = useState(false);

  const load = useCallback(async () => {
    if (!eventId || !accountId) {
      return null;
    }
    return k8sApi.getEventAlertControls({ event_id: eventId, account_id: accountId });
  }, [eventId, accountId]);

  useEffect(() => {
    let cancelled = false;
    const refresh = () =>
      load().then((c) => {
        if (!cancelled && c) {
          setControls(c);
        }
      });
    refresh();
    // A fix applied from Nubi's remediation plan changes the rule this row shows.
    window.addEventListener(ALERT_RULE_CHANGED_EVENT, refresh);
    return () => {
      cancelled = true;
      window.removeEventListener(ALERT_RULE_CHANGED_EVENT, refresh);
    };
  }, [load]);

  const visible = hasEventAlertControls(controls, canWrite);
  useEffect(() => {
    onVisibilityChange?.(visible);
  }, [visible, onVisibilityChange]);

  const canMute = Boolean(controls && visible && canWrite && mutableScopes(controls).length > 0);
  useEffect(() => {
    onCanMuteChange?.(canMute);
  }, [canMute, onCanMuteChange]);
  useEffect(() => {
    if (!dialogOnly || !muteRequest || !controls) return;
    setScope(mutableScopes(controls)[0]?.scope || '');
    setDuration(DEFAULT_MUTE_DURATION);
    setMuteOpen(true);
    // Opens on a new request only, not when the controls reload.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [muteRequest]);

  if (!controls || !visible) {
    return null;
  }

  const alertName = alertDisplayName(controls.alert);
  const scopesToMute = mutableScopes(controls);
  const rule = controls.rule;
  const toggleWarning = rule?.operations?.disable_at_source?.warning;

  const applyMute = async (s: EventMuteScopeName, muted: boolean, hours: number) => {
    setBusy(true);
    const res: any = await k8sApi.updateEventMute({ event_id: eventId, account_id: accountId, scope: s, duration_hours: hours, muted });
    setBusy(false);
    const next = res?.data?.data?.controls;
    if (res instanceof Error || res?.data?.errors?.length > 0 || !next) {
      const reason = firstGraphQLError(res);
      snackbar.error(`Failed to ${muted ? 'mute' : 'unmute'} ${alertName}${reason ? `: ${reason}` : ''}`);
      return false;
    }
    setControls(next);
    snackbar.success(`${alertName} ${muted ? 'muted' : 'unmuted'}`);
    return true;
  };

  const openMute = () => {
    setScope(scopesToMute[0]?.scope || '');
    setDuration(DEFAULT_MUTE_DURATION);
    setMuteOpen(true);
  };

  const confirmMute = async () => {
    if (scope && (await applyMute(scope, true, Number(duration)))) {
      setMuteOpen(false);
    }
  };

  const confirmToggle = async () => {
    if (!rule) {
      return;
    }
    const enable = !rule.enabled;
    setBusy(true);
    const res: any = await k8sApi.disableAlertManager({
      accountId,
      alert: rule.alert,
      enable,
      id: rule.rule_id,
      namespace: '',
      group: '',
    });
    setBusy(false);
    if (res instanceof Error || res?.data?.errors?.length > 0 || res?.data?.data?.errors) {
      const reason = firstGraphQLError(res);
      snackbar.error(`Failed to ${enable ? 'enable' : 'disable'} ${alertName}${reason ? `: ${reason}` : ''}`);
      return;
    }
    snackbar.success(`${alertName} ${enable ? 'enabled' : 'disabled'} where it is defined`);
    setToggleOpen(false);
    // Show the new state now; the full reload (counts, capabilities) takes a few seconds.
    setControls((c) => (c?.rule ? { ...c, rule: { ...c.rule, enabled: enable } } : c));
    // A failed reload keeps what is on screen rather than removing the section.
    const reloaded = await load();
    if (reloaded) {
      setControls(reloaded);
    }
  };

  const scopeOptions = scopesToMute.map((s: EventMuteScope) => ({
    value: s.scope,
    label: `${scopeOptionLabel(s, controls.alert)} — ${scopeImpactText(s)}`,
  }));

  const location = ruleLocationText(controls, brand);
  const mutes = mutedScopes(controls);
  const others = otherSuppressions(controls);
  const moreItems = [
    ...(canWrite && canDisableAtSource(controls)
      ? [
          {
            label: rule?.enabled ? 'Turn off where it is defined…' : 'Turn on where it is defined…',
            description: rule?.enabled ? 'Stops the alert for everyone, not just here' : 'The alert starts firing again',
            onSelect: () => setToggleOpen(true),
          },
        ]
      : []),
    ...(canWrite && canEditRule(controls)
      ? [
          {
            label: 'Edit in Alert Manager',
            description: 'Expression, threshold or duration',
            onSelect: () => window.open(alertManagerHref, '_blank', 'noopener,noreferrer'),
          },
        ]
      : []),
  ];

  const muteModal = (
    <Modal
      open={muteOpen}
      handleClose={() => setMuteOpen(false)}
      title={`Mute ${alertName}${brand ? ` in ${brand}` : ''}`}
      confirmText='Mute'
      onConfirm={confirmMute}
      confirmDisabled={!scope}
      loader={busy}
      backdropClickClose={!busy}
    >
      <Box sx={{ display: 'flex', flexDirection: 'column', gap: ds.space[4] }}>
        <Select
          label='Which alerts'
          options={scopeOptions}
          value={scope || null}
          onChange={(v: string) => setScope(v as EventMuteScopeName)}
          clearable={false}
          required
        />
        <Select label='For how long' options={MUTE_DURATION_OPTIONS} value={duration} onChange={setDuration} clearable={false} required />
        <Text
          value='New alerts are still recorded, marked Suppressed: no notifications, automations or AI analysis. The rule itself is not changed.'
          sx={captionSx}
        />
      </Box>
    </Modal>
  );
  if (dialogOnly) {
    return muteModal;
  }

  return (
    <Box sx={{ display: 'flex', flexDirection: 'column', gap: ds.space[2] }} data-testid='event-alert-controls'>
      <Text value='Stop this alert' sx={headingSx} />
      {/* One quiet row: this is the alert, not the problem, so it must not
          outweigh the fixes above it. Changes at the source sit in the menu. */}
      <Box
        sx={{
          display: 'flex',
          alignItems: 'center',
          justifyContent: 'space-between',
          gap: ds.space[4],
          flexWrap: 'wrap',
          px: ds.space[4],
          py: ds.space[3],
          border: `1px solid ${ds.gray[200]}`,
          borderRadius: ds.radius.sm,
        }}
      >
        <Box sx={{ display: 'flex', flexDirection: 'column', gap: ds.space[1], minWidth: 0, flex: 1 }}>
          <Box sx={{ display: 'flex', alignItems: 'baseline', gap: ds.space[2], flexWrap: 'wrap' }}>
            <Text value={alertName} sx={{ fontSize: ds.text.body, fontWeight: ds.weight.medium, wordBreak: 'break-word' }} />
            <Text value={`${location}${rule && !rule.enabled ? ' · turned off where it is defined' : ''}`} sx={captionSx} />
          </Box>
          {mutes.map((s) => (
            <Box key={`muted-${s.scope}`} sx={{ display: 'flex', alignItems: 'center', gap: ds.space[2], flexWrap: 'wrap' }}>
              <Text
                value={
                  <>
                    {`${mutedScopeText(s)} · `}
                    {s.expires_at ? (
                      <>
                        ends <Datetime value={s.expires_at} sx={captionSx} sxPrefix={captionSx} sxSuffix={captionSx} />
                      </>
                    ) : (
                      'until unmuted'
                    )}
                  </>
                }
                sx={captionSx}
              />
              {canWrite && (
                <Button tone='link' size='xs' onClick={() => applyMute(s.scope, false, 0)} disabled={busy}>
                  Unmute
                </Button>
              )}
            </Box>
          ))}
          {others.map((s) => (
            <Box key={s.rule_id} sx={{ display: 'flex', alignItems: 'center', gap: ds.space[2], flexWrap: 'wrap' }}>
              <Text value={`Already suppressed by the triage rule "${s.name || s.rule_id}"`} sx={captionSx} />
              <Link href={triageRulesHref} openInNew style={{ fontSize: ds.text.caption }}>
                Manage
              </Link>
            </Box>
          ))}
        </Box>
        {canWrite && (scopesToMute.length > 0 || moreItems.length > 0) && (
          <Box sx={{ display: 'flex', alignItems: 'center', gap: ds.space[2], flexShrink: 0 }}>
            {scopesToMute.length > 0 && (
              <Button tone='secondary' size='sm' onClick={openMute} disabled={busy} data-testid='event-alert-mute'>
                {mutes.length > 0 ? 'Mute more…' : 'Mute…'}
              </Button>
            )}
            {moreItems.length > 0 && (
              <DropdownMenu
                align='end'
                size='sm'
                minWidth={240}
                items={moreItems}
                trigger={
                  <Button
                    tone='secondary'
                    size='sm'
                    composition='icon-only'
                    icon={<MoreVert />}
                    aria-label='More alert rule actions'
                    disabled={busy}
                  />
                }
              />
            )}
          </Box>
        )}
      </Box>

      {muteModal}

      <Modal
        open={toggleOpen}
        handleClose={() => setToggleOpen(false)}
        title={`Turn ${rule?.enabled ? 'off' : 'on'} ${alertName} where it is defined?`}
        confirmText={rule?.enabled ? 'Turn off' : 'Turn on'}
        onConfirm={confirmToggle}
        loader={busy}
        backdropClickClose={!busy}
      >
        <Box sx={{ display: 'flex', flexDirection: 'column', gap: ds.space[3] }}>
          <Text
            value={rule?.enabled ? "This alert will stop triggering until it's re-enabled." : 'This alert will start triggering again.'}
            sx={{ fontSize: ds.text.body }}
          />
          {toggleWarning && <Banner tone='warning' surface='section' message={toggleWarning} />}
        </Box>
      </Modal>
    </Box>
  );
}
