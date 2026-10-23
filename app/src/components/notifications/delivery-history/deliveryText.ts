// Words for delivery rows: what was sent, where, why it failed or wasn't sent and how to fix it, and a
// link to the message. Shared by Admin → Notification Rules → Delivery history and the alert page. The
// brand is passed in by the caller (useBrandingConfig), so the words repaint once branding resolves.
import { safeJSONParse } from 'src/utils/common';
import type { Delivery, DestinationSummary } from '@api1/notification/deliveries';

const PLATFORM_NAMES: Record<string, string> = {
  slack: 'Slack',
  ms_teams: 'Microsoft Teams',
  google_chat: 'Google Chat',
  discord: 'Discord',
  slack_webhook: 'Slack webhook',
  ms_teams_webhook: 'Teams webhook',
  google_chat_webhook: 'Google Chat webhook',
  email: 'Email',
};

export const platformName = (platform?: string | null): string => (platform ? PLATFORM_NAMES[platform] || platform : '');

export const PLATFORM_FILTERS = [
  { label: 'Slack', value: 'slack', platforms: ['slack'] },
  { label: 'Microsoft Teams', value: 'ms_teams', platforms: ['ms_teams'] },
  { label: 'Google Chat', value: 'google_chat', platforms: ['google_chat'] },
  { label: 'Discord', value: 'discord', platforms: ['discord'] },
  { label: 'Webhook', value: 'webhook', platforms: ['slack_webhook', 'ms_teams_webhook', 'google_chat_webhook'] },
  { label: 'Email', value: 'email', platforms: ['email'] },
];

export const TYPE_FILTERS = [
  { label: 'Alert', value: 'alert', kinds: ['finding'] },
  { label: 'Alert resolved', value: 'resolved', kinds: ['finding_resolved'] },
  { label: 'Hourly summary', value: 'summary', kinds: ['grouped_anomaly', 'grouped_slo_alert'] },
  { label: 'Weekly digest', value: 'digest', kinds: ['weekly_digest_events', 'recommendation_nudge_digest'] },
  { label: 'Approval', value: 'approval', kinds: ['approval', 'email:approval_request'] },
];

export const STATUS_FILTERS = [
  { label: 'Sent', value: 'sent' },
  { label: 'Failed', value: 'failed' },
  { label: 'Not sent', value: 'not_sent' },
];

const KIND_LABELS: Record<string, string> = {
  finding: 'Alert',
  finding_resolved: 'Alert resolved',
  grouped_anomaly: 'Hourly summary',
  grouped_slo_alert: 'Hourly summary',
  weekly_digest_events: 'Weekly digest',
  recommendation_nudge_digest: 'Recommendation digest',
  approval: 'Approval',
  automation: 'Automation',
  remediation: 'Remediation',
  generic: 'Message',
  'email:approval_request': 'Approval',
};

export const typeLabel = (kind?: string | null): string => {
  if (!kind) return 'Message';
  if (KIND_LABELS[kind]) return KIND_LABELS[kind];
  if (kind.startsWith('email:')) return 'Email';
  const words = kind.replace(/_/g, ' ');
  return words.charAt(0).toUpperCase() + words.slice(1);
};

const CHANNEL_PLATFORMS = new Set(['slack', 'discord']);

// "#alerts" for a Slack or Discord channel name; the id when no name was recorded.
export const destinationLabel = (row: Pick<Delivery, 'platform' | 'destination' | 'destination_name'>, name?: string | null): string => {
  const label = name || row.destination_name;
  if (!label) return row.destination || '';
  return CHANNEL_PLATFORMS.has(row.platform || '') && !label.startsWith('#') ? `#${label}` : label;
};

export const summaryLabel = (summary: DestinationSummary): string =>
  destinationLabel({ platform: summary.platform, destination: summary.destination }, summary.last_destination_name);

export interface RoutedBy {
  kind: 'rules' | 'default' | 'requested' | 'none';
  rules: { id: string; name: string }[];
}

// routed_by arrives as JSON text: a list of rules, or the JSON string "default" or "requested".
const parseRoutedBy = (raw: unknown): unknown => {
  if (typeof raw !== 'string') return raw;
  const text = raw.trim().replace(/^"(.*)"$/, '$1');
  return text === 'default' || text === 'requested' ? text : safeJSONParse(text);
};

export const routedBy = (row: Pick<Delivery, 'routed_by'>): RoutedBy => {
  const value = parseRoutedBy(row.routed_by);
  if (value === 'default') return { kind: 'default', rules: [] };
  if (value === 'requested') return { kind: 'requested', rules: [] };
  if (Array.isArray(value) && value.length) {
    return { kind: 'rules', rules: value.map((r: any) => ({ id: String(r?.id ?? ''), name: r?.name || 'Unnamed rule' })) };
  }
  return { kind: 'none', rules: [] };
};

interface Words {
  says: string;
  fix?: string;
}

// Placeholders: {brand} the tenant's brand, {destination} the channel, space or address, {platform} the
// platform's name, {rule} the rule's name, {attempts} how many attempts were made.
const FAILED: Record<string, Words> = {
  not_in_channel: { says: '{brand} was removed from {destination}.', fix: 'Re-invite it.' },
  channel_not_found: {
    says: "{destination} was archived or deleted, or it's private and {brand} isn't in it.",
    fix: 'Pick another channel in the rule, or invite {brand} if the channel is private.',
  },
  permission_denied: { says: "{brand} isn't allowed to post in {destination}.", fix: 'Ask an admin of {destination} to let {brand} post.' },
  auth_failed: {
    says: 'The {platform} connection stopped working: its sign-in was revoked or expired.',
    fix: 'Reconnect {platform} in Admin › Integrations.',
  },
  webhook_gone: { says: 'The webhook was deleted or its URL changed.', fix: 'Paste its new URL in Admin › Integrations.' },
  not_configured: { says: "The {platform} connection isn't finished.", fix: 'Finish setting it up in Admin › Integrations.' },
  recipient_refused: { says: 'The mail server refused {destination}.', fix: 'Check the address is spelled right and can receive mail.' },
  message_rejected: { says: '{platform} refused this message.', fix: 'Nothing to change on your side. Contact support if it keeps happening.' },
  invalid_request: {
    says: "{platform} couldn't accept the request {brand} sent.",
    fix: 'Nothing to change on your side. Contact support if it keeps happening.',
  },
  rate_limited: { says: '{platform} asked {brand} to slow down.', fix: 'If this channel gets many alerts, send fewer to it.' },
  platform_unavailable: { says: "{platform} didn't respond.", fix: 'Nothing to change on your side.' },
  internal_error: { says: '{brand} hit an error while sending.', fix: 'Contact support if it keeps happening.' },
};

// A temporary failure on a send's last attempt, after retries.
const AFTER_RETRIES: Record<string, string> = {
  rate_limited: '{platform} kept asking {brand} to slow down.',
  platform_unavailable: "{platform} didn't respond to {attempts} attempts.",
  internal_error: '{brand} hit an error on all {attempts} attempts.',
};

const PLATFORM_WORDS: Record<string, Words> = {
  'ms_teams:not_in_channel': { says: '{brand} was removed from this team.', fix: 'Add the app to the team again.' },
  'ms_teams:channel_not_found': { says: 'This channel was deleted or moved.', fix: 'Pick another channel in the rule.' },
  'google_chat:permission_denied': { says: "{brand} isn't a member of {destination}.", fix: 'Add it to the space.' },
  'google_chat:channel_not_found': { says: '{destination} no longer exists.', fix: 'Pick another space in the rule.' },
  'email:auth_failed': { says: "The mail server rejected {brand}'s sign-in.", fix: 'Check the mail server settings.' },
  'email:platform_unavailable': { says: "The mail server didn't respond.", fix: 'Nothing to change on your side.' },
};

const NOT_SENT: Record<string, Words> = {
  suppressed: { says: "{rule} says Don't send.", fix: 'Edit {rule} if this should go out.' },
  snoozed: { says: '{rule} is snoozed.', fix: 'It sends again when the snooze ends.' },
  held_for_summary: { says: 'Held for the hourly summary.' },
  digest_only: { says: 'Low-priority alerts go to the digest only.', fix: 'To post these as they happen, add a rule that includes this priority.' },
  dropped_by_default: { says: "No rule sends this, and the default is Don't send.", fix: 'Add a rule, or change the default in Rules.' },
  no_destination: { says: 'No channel to send to.', fix: 'Name a channel in the rule, or set a default channel on the connection.' },
};

// Before branding resolves the brand is empty: the sentence then names "the app" instead.
const fill = (text: string | undefined, values: Record<string, string>): string | undefined => {
  if (text === undefined) return undefined;
  return text.replace(/\{(brand|destination|platform|rule|attempts)\}/g, (_m, key: string, offset: number) => {
    const value = values[key] ?? '';
    return key === 'brand' && offset === 0 ? value.charAt(0).toUpperCase() + value.slice(1) : value;
  });
};

// A temporary failure is retried by the queue: its final row is attempt 2 or later.
const wasRetried = (row: Pick<Delivery, 'attempt'>) => (row.attempt || 1) > 1;

export interface ReasonWords {
  says?: string;
  fix?: string;
}

export const reasonWords = (row: Delivery, brand: string, nameOverride?: string | null): ReasonWords => {
  const reason = row.reason || '';
  const rule = routedBy(row).rules[0]?.name;
  const values = {
    brand: brand || 'the app',
    destination: destinationLabel(row, nameOverride) || 'this channel',
    platform: platformName(row.platform) || 'The platform',
    rule: rule ? `Rule ${rule}` : 'A rule',
    attempts: String(row.attempt || 1),
  };
  if (row.status === 'not_sent') {
    const words = NOT_SENT[reason];
    if (!words) return {};
    // The fix names the rule plainly: "Edit Quiet hours if ...".
    return { says: fill(words.says, values), fix: fill(words.fix, { ...values, rule: rule || 'the rule' }) };
  }
  if (row.status !== 'failed') return {};
  const words = PLATFORM_WORDS[`${row.platform}:${reason}`] || FAILED[reason];
  if (!words) return { says: fill('{platform} did not take the message.', values) };
  const says = wasRetried(row) && AFTER_RETRIES[reason] ? AFTER_RETRIES[reason] : words.says;
  return { says: fill(says, values), fix: fill(words.fix, values) };
};

export type StatusTone = 'success' | 'critical' | 'neutral';

export interface StatusLine {
  label: string;
  tone: StatusTone;
  detail?: string;
}

export const statusLine = (row: Delivery, brand: string): StatusLine => {
  if (row.status === 'sent') {
    return { label: 'Sent', tone: 'success', detail: wasRetried(row) ? `Sent on attempt ${row.attempt}.` : undefined };
  }
  if (row.status === 'failed') {
    return { label: 'Failed', tone: 'critical', detail: reasonWords(row, brand).says };
  }
  return { label: 'Not sent', tone: 'neutral', detail: reasonWords(row, brand).says };
};

export interface MessageLink {
  href: string;
  label: string;
}

// Built from stored ids. Slack needs the workspace URL its integration saved; without it the link opens
// the channel. Discord (no server id stored), Google Chat, webhooks and email have no link.
export const messageLink = (row: Delivery): MessageLink | null => {
  if (row.status !== 'sent' || !row.message_id || !row.destination) return null;
  if (row.platform === 'slack') {
    if (row.team_url) {
      const base = row.team_url.replace(/\/+$/, '');
      const thread = row.thread_id ? `?thread_ts=${row.thread_id}&cid=${row.destination}` : '';
      return { href: `${base}/archives/${row.destination}/p${row.message_id.replace('.', '')}${thread}`, label: 'Open in Slack' };
    }
    if (!row.team_id) return null;
    return { href: `https://slack.com/app_redirect?team=${row.team_id}&channel=${row.destination}`, label: 'Open the channel in Slack' };
  }
  if (row.platform === 'ms_teams') {
    const params = new URLSearchParams();
    if (row.team_id) params.set('groupId', row.team_id);
    if (row.thread_id) params.set('parentMessageId', row.thread_id);
    const query = params.toString();
    return {
      href: `https://teams.microsoft.com/l/message/${encodeURIComponent(row.destination)}/${encodeURIComponent(row.message_id)}${
        query ? `?${query}` : ''
      }`,
      label: 'Open in Teams',
    };
  }
  return null;
};
