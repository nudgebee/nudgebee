import { useEffect, useState } from 'react';
import apiNotifications from '@api1/notification';
import apiAutoPlaybook from '@api1/autoPlaybook';
import apiAccount from '@api1/account';
import apiIntegrations from '@api1/integrations';
import { RuleMapping, mappingLabel } from './model';

export interface DestinationOption {
  value: string;
  label: string;
  disabled?: boolean;
}

// One platform a rule can send to: a channel list, or teams with their channels.
export interface Destination {
  platform: string;
  label: string;
  teams?: { value: string; label: string; channels: DestinationOption[] }[];
  options: DestinationOption[];
  loading: boolean;
  installationId?: string;
  hint?: { hint: string; inviteUrl?: string };
  // Saved values whose channel no longer exists.
  stale?: string[];
}

const WEBHOOKS = [
  { platform: 'slack_webhook', label: 'Slack webhook', type: 'slack_webhook', keepDisabled: false },
  { platform: 'ms_teams_webhook', label: 'Teams webhook', type: 'ms_teams_webhook', keepDisabled: false },
  // A rule already pointed at a disabled Google Chat target keeps it (shown, not pickable).
  { platform: 'google_chat_webhook', label: 'Google Chat webhook', type: 'google_chat_webhook', keepDisabled: true },
];

const TEAMS_SEPARATOR = '/';

const PLATFORM_LABELS: Record<string, string> = {
  slack: 'Slack',
  ms_teams: 'Teams',
  google_chat: 'Google Chat',
  discord: 'Discord',
  ...Object.fromEntries(WEBHOOKS.map((w) => [w.platform, w.label])),
};

// The badge preset for a platform: webhooks show their app's icon.
export const platformPreset = (platform: string) => platform.replace(/_webhook$/, '');

export const valueFromMapping = (m: RuleMapping): string => {
  const c = m.channels || {};
  if (m.platform === 'ms_teams') return c.team_id && c.channels?.[0]?.id ? `${c.team_id}${TEAMS_SEPARATOR}${c.channels[0].id}` : '';
  if (m.platform.endsWith('_webhook')) return c.webhook_target_id ? String(c.webhook_target_id) : '';
  return c.id ? String(c.id) : '';
};

// The mapping to save for a picked value, or null when the channel is gone.
export const mappingFromValue = (d: Destination, value: string): RuleMapping | null => {
  if (!value) return null;
  if (d.platform === 'ms_teams') {
    const [teamId, channelId] = value.split(TEAMS_SEPARATOR);
    const team = d.teams?.find((t) => t.value === teamId);
    const channel = team?.channels.find((c) => c.value === channelId);
    if (!team || !channel) return null;
    return {
      platform: 'ms_teams',
      channels: { team_name: team.label, team_id: team.value, channels: [{ name: channel.label, id: channel.value }] },
      installation_id: d.installationId,
    };
  }
  const option = d.options.find((o) => o.value === value);
  if (!option) return null;
  if (d.platform.endsWith('_webhook')) return { platform: d.platform, channels: { webhook_target_id: option.value, name: option.label } };
  const mapping: RuleMapping = { platform: d.platform, channels: { name: option.label, id: option.value } };
  if (d.installationId) mapping.installation_id = d.installationId;
  return mapping;
};

export const teamsValue = (teamId: string, channelId: string) => (teamId ? `${teamId}${TEAMS_SEPARATOR}${channelId}` : '');
export const splitTeamsValue = (value: string) => {
  const [teamId = '', channelId = ''] = (value || '').split(TEAMS_SEPARATOR);
  return { teamId, channelId };
};

// One Teams option per channel of every team, grouped under its team. A picked
// option reads "Team › Channel", or just the channel when there is one team. The
// value names both.
export const teamsChannelOptions = (teams: { value: string; label: string; channels: DestinationOption[] }[]): DestinationOption[] =>
  teams.flatMap((team) => {
    const teamName = team.label || team.value;
    return (team.channels || []).map((channel) => ({
      value: teamsValue(team.value, channel.value),
      label: channel.label,
      group: teamName,
      searchText: teamName,
      selectedLabel: teams.length > 1 ? `${teamName} › ${channel.label}` : channel.label,
    }));
  });

const integrationRows = (res: any) => res?.data?.data?.integrations_list?.rows || [];

// Every place a rule can send to in this tenant: installed apps' channels and
// webhook targets. Loaded once per open of the page.
export const useDestinations = (enabled: boolean) => {
  const [destinations, setDestinations] = useState<Destination[]>([]);

  useEffect(() => {
    if (!enabled) return;
    let cancelled = false;
    const update = (platform: string, patch: Partial<Destination>) => {
      if (cancelled) return;
      setDestinations((prev) => prev.map((d) => (d.platform === platform ? { ...d, ...patch } : d)));
    };
    const add = (d: Destination) => {
      if (cancelled) return;
      setDestinations((prev) => [...prev.filter((p) => p.platform !== d.platform), d]);
    };

    apiNotifications.getInstalledTools().then((res: any) => {
      const installs: any[] = res?.messaging_platforms || [];
      const installId = (platform: string) => installs.find((i) => i.platform === platform)?.id;
      if (installId('slack')) {
        add({ platform: 'slack', label: 'Slack', options: [], loading: true, installationId: installId('slack') });
        apiAutoPlaybook
          .listSlackChannels('slack')
          .then((r: any) => update('slack', { options: (r?.data || []).map((c: any) => ({ value: c.id, label: c.name })) }))
          .finally(() => update('slack', { loading: false }));
      }
      if (installId('ms_teams')) {
        add({ platform: 'ms_teams', label: 'Teams', options: [], teams: [], loading: true, installationId: installId('ms_teams') });
        apiAutoPlaybook
          .listMSTeamsChannels('ms_teams')
          .then((r: any) =>
            update('ms_teams', {
              teams: (r?.data || []).map((t: any) => ({
                value: t.id,
                label: t.name,
                channels: (t.channels || []).map((c: any) => ({ value: c.id, label: c.name })),
              })),
            })
          )
          .finally(() => update('ms_teams', { loading: false }));
      }
      if (installId('discord')) {
        add({ platform: 'discord', label: 'Discord', options: [], loading: true, installationId: installId('discord') });
        apiAccount
          .getNotificationChannelList('discord')
          .then((r: any) => {
            const channels = r?.data?.channels || r?.data?.data || [];
            update('discord', {
              options: channels.map((c: any) => ({ value: c.id, label: c.name })),
              hint: r?.data?.hint ? { hint: r.data.hint, inviteUrl: r.data.invite_url } : undefined,
            });
          })
          .finally(() => update('discord', { loading: false }));
      }
    });
    // Google Chat spaces are bound to the tenant, not an installed app.
    apiAccount.getNotificationChannelList('google_chat').then((r: any) => {
      const spaces = (r?.data?.data || []).map((c: any) => ({ value: c.id, label: c.name }));
      if (spaces.length) add({ platform: 'google_chat', label: 'Google Chat', options: spaces, loading: false });
    });
    WEBHOOKS.forEach((w) => {
      apiIntegrations
        .listIntegrations({ type: w.type, limit: 200 })
        .then((r: any) => {
          const rows = integrationRows(r).filter((row: any) => w.keepDisabled || row.status !== 'disabled');
          if (rows.length) {
            add({
              platform: w.platform,
              label: w.label,
              options: rows.map((row: any) => ({ value: row.id, label: row.name, disabled: row.status === 'disabled' })),
              loading: false,
            });
          }
        })
        .catch(() => undefined);
    });
    return () => {
      cancelled = true;
      setDestinations([]);
    };
  }, [enabled]);

  const order = ['slack', 'ms_teams', 'google_chat', 'discord', 'slack_webhook', 'ms_teams_webhook', 'google_chat_webhook'];
  return [...destinations].sort((a, b) => order.indexOf(a.platform) - order.indexOf(b.platform));
};

export const valuesFromMappings = (mappings: RuleMapping[]): Record<string, string> =>
  Object.fromEntries(mappings.filter((m) => m.platform !== 'email').map((m) => [m.platform, valueFromMapping(m)]));

// The mappings for the picked values. `problem` names what stops a save: a list
// still loading, a Teams team without a channel, or a channel that no longer exists.
export const buildMappings = (destinations: Destination[], values: Record<string, string>): { mappings: RuleMapping[]; problem?: string } => {
  const mappings: RuleMapping[] = [];
  for (const [platform, value] of Object.entries(values)) {
    if (!value) continue;
    const d = destinations.find((x) => x.platform === platform);
    if (d?.loading) return { mappings, problem: 'Channels are still loading. Try again in a moment.' };
    if (platform === 'ms_teams' && !splitTeamsValue(value).channelId) return { mappings, problem: 'Pick a Teams channel, or clear the team.' };
    const mapping = d && !d.stale?.includes(value) ? mappingFromValue(d, value) : null;
    if (!mapping) {
      const label = d?.label || platform.replace(/_/g, ' ');
      return { mappings, problem: `The ${label} channel this rule used no longer exists. Pick another, or clear it.` };
    }
    mappings.push(mapping);
  }
  return { mappings };
};

// The destinations with a rule's saved channels added where they no longer
// exist, so the picker still shows them and they can be cleared.
export const withSavedChannels = (destinations: Destination[], mappings: RuleMapping[]): Destination[] => {
  const out: Destination[] = destinations.map((d) => ({
    ...d,
    options: [...d.options],
    teams: d.teams?.map((t) => ({ ...t, channels: [...t.channels] })),
  }));
  mappings
    .filter((m) => m.platform !== 'email')
    .forEach((m) => {
      const value = valueFromMapping(m);
      if (!value) return;
      const gone = `${mappingLabel(m)} (no longer available)`;
      let dest = out.find((x) => x.platform === m.platform);
      if (!dest) {
        dest = { platform: m.platform, label: PLATFORM_LABELS[m.platform] || m.platform, options: [], loading: false };
        if (m.platform === 'ms_teams') dest.teams = [];
        out.push(dest);
      }
      const d = dest;
      if (d.loading) return;
      if (m.platform === 'ms_teams') {
        const { teamId, channelId } = splitTeamsValue(value);
        const teams = d.teams || (d.teams = []);
        let team = teams.find((t) => t.value === teamId);
        if (!team) {
          team = { value: teamId, label: m.channels?.team_name || teamId, channels: [] };
          teams.push(team);
        }
        if (!team.channels.some((c) => c.value === channelId)) {
          team.channels.push({ value: channelId, label: gone });
          d.stale = [...(d.stale || []), value];
        }
        return;
      }
      if (!d.options.some((o) => o.value === value)) {
        d.options.push({ value, label: gone });
        d.stale = [...(d.stale || []), value];
      }
    });
  return out;
};
