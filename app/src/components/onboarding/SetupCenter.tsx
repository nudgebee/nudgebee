/**
 * SetupCenter — the guided first-run surface on the home page.
 *
 * Six outcome-named panels, one card each, stacked. Accounts rides in the top
 * card alongside the heading and the progress bar because it is the only thing
 * that gates finishing: everything else can be added later from the
 * integrations page, and the card says so rather than implying a wall.
 *
 * Three rules the layout exists to enforce, each a fix for something the
 * earlier single-card version got wrong:
 *
 *   1. **Nobody leaves setup.** Long provider lists resolve to a few tiles plus
 *      one dropdown, never a link to the 13-tab Integrations catalog. The
 *      earlier design sent people there with a return banner; the trip was the
 *      problem, not the banner.
 *   2. **Four states, not five.** An agent-detected signal is `done` carrying a
 *      chip. A fifth glyph would make the legend unlearnable.
 *   3. **Only required items are scored.** An optional extra must never drag a
 *      panel backwards, or the bar can never fill.
 *
 * Everything shown is derived from `apiAccount.getAllAccount()` plus the
 * agent's own telemetry detection, via `evaluateOnboarding`
 * (@lib/onboardingReadiness), so the checklist reflects real state and every
 * action deep-links to that provider's own form.
 */
import * as React from 'react';
import { Box, Typography } from '@mui/material';
import { useRouter } from 'next/router';
import ExpandMoreIcon from '@mui/icons-material/ExpandMore';
import ArrowForwardIcon from '@mui/icons-material/ArrowForward';
import InfoOutlinedIcon from '@mui/icons-material/InfoOutlined';
import LockOutlinedIcon from '@mui/icons-material/LockOutlined';
import CelebrationOutlinedIcon from '@mui/icons-material/CelebrationOutlined';
import apiIntegrations from '@api1/integrations';
import { parseIntegrationItem } from '@api1/integrations/helpers';
import { ALERT_PROVIDERS } from '@lib/onboarding/setupRegistry';
import apiKnowledgeBase from '@api1/knowledge-base';
import CloudProviderIcon from '@shared/icons/CloudIcon';
import IntegrationDynamicFormModal from '@components/integrations/modal/IntegrationDynamicFormModal';
import K8sAccountModal from '@components/integrations/modal/K8sAccountModal';
import K8sAgentInstallDialog from '@components/integrations/modal/K8sAgentInstallDialog';
import JiraAccountModal from '@components/integrations/modal/JiraAccountModal';
import ServiceNowAccountModal from '@components/integrations/modal/ServiceNowAccountModal';
import FreshdeskAccountModal from '@components/integrations/modal/FreshdeskAccountModal';
import PagerDutyAccountModal from '@components/integrations/modal/PagerDutyAccountModal';
import ZenDutyAccountModal from '@components/integrations/modal/ZenDutyAccountModal';
import GithubAccountModal from '@components/integrations/modal/GithubAccountModal';
import GitlabAccountModal from '@components/integrations/modal/GitlabAccountModal';
import DiscordInstallModal from '@components/integrations/modal/DiscordInstallModal';
import { openChatOAuthInstall } from '@components/integrations/chatOAuthInstall';
import GoogleChatPermissionStatus from '@components/integrations/GoogleChatPermissionStatus';
import AddLLMConfigModal from '@components/llm/AddLLMConfigModal';
import AddAwsAccountModal from '@pages/accounts/AddAwsAccountModal';
import AddGcpAccountModal from '@pages/accounts/AddGcpAccountModal';
import AddAzureAccountModal from '@pages/accounts/AddAzureAccountModal';
import AddSelfHostedAccountModal from '@pages/accounts/AddSelfHostedAccountModal';
import KnowledgeBaseFormModal from '@components/llm/KnowledgeBaseFormModal';
import useKnowledgeBaseForm from '@components/llm/useKnowledgeBaseForm';
import { getIntegrationDescription } from '@lib/integrationDescriptions';
import { Card } from '@ui/Card';
import { Button } from '@ui/Button';
import { Chip } from '@ui/Chip';
import { Label } from '@ui/Label';
import { Select } from '@ui/Select';
import { Modal } from '@ui/Modal';
import { ProgressBar } from '@ui/ProgressBar';
import { Skeleton } from '@ui/Skeleton';
import { EmptyState } from '@ui/EmptyState';
import { fillBrandTokens, useBrandingConfig } from '@hooks/useTenantBranding';
import { useSetupGate } from '@context/SetupGateContext';
import { useOnboardingReadiness } from '@hooks/useOnboardingReadiness';
import {
  PROVIDER_FOLLOW_UPS,
  MODULES,
  ONBOARDING_CLOUD_PROVIDERS,
  type OnboardingAccount,
  type OnboardingItemStatus,
  type OnboardingPanelStatus,
  type OnboardingState,
  type ItemAccountRow,
} from '@lib/onboardingReadiness';

/**
 * The four product modules an item can be required by — display-only chips
 * behind the disclosure. Hues follow the design system's own semantics:
 * purple for agents/automations, green for savings, blue for info, teal for
 * secondary positive. Ported from the reference prototype (docs/prototypes/
 * setup-center-prototype.html), not invented here.
 */
const MODULE_META: Record<string, { name: string; bg: string; ink: string; border: string }> = {
  ts: { name: 'Troubleshooting', bg: 'var(--ds-blue-100)', ink: 'var(--ds-blue-700)', border: 'var(--ds-blue-200)' },
  cost: { name: 'Cost optimisation', bg: 'var(--ds-green-100)', ink: 'var(--ds-green-700)', border: 'var(--ds-green-200)' },
  ops: { name: 'Ops automation', bg: 'var(--ds-purple-100)', ink: 'var(--ds-purple-700)', border: 'var(--ds-purple-200)' },
  k8s: { name: 'Kubernetes ops', bg: 'var(--ds-teal-100)', ink: 'var(--ds-teal-700)', border: 'var(--ds-teal-200)' },
};

/**
 * Names the modules an `na` item is being hidden for, e.g. "Optimize" or
 * "Optimize, Automation" — using `MODULES`' own display order, not the
 * selection's toggle history, so the wording never depends on the order the
 * chips happened to be clicked in.
 *
 * Returns `null` at the filter's default "every module" state (nothing
 * selected, or all four are) — naming every module a truly optional item
 * skips would just repeat "not needed" in longer words, so the caller falls
 * back to the plain label there.
 *
 * Safe to name every currently-selected module by name: `need` is the
 * *strongest* level across them (`needOf` in onboardingReadiness.ts), so
 * `need === 'na'` only happens when every one of them agrees — unlike the
 * `req` case elsewhere on this card, where naming one selected module could
 * name the wrong one.
 */
const notNeededFor = (selectedModules: string[]): string | null => {
  if (selectedModules.length === 0 || selectedModules.length === MODULES.length) return null;
  return MODULES.filter((m) => selectedModules.includes(m.id))
    .map((m) => m.label)
    .join(', ');
};

const CARD_ID = 'home-setup-center';

/** Tiles are capped at this many; above it, the rest move to the dropdown. */
const SHORTLIST = 6;

/**
 * A connected `llm` / `llm_gateway` / `mcp` integration row, as returned by
 * `apiIntegrations.listIntegrations()` and parsed by `parseIntegrationItem`.
 * Fetched separately from the readiness model, which only needs to know a
 * row of one of these types *exists* (via `stateByType`) — this is the one
 * place the AI panel needs to name what was actually configured, not just
 * that something was, and to hand that row back to its own edit modal.
 */
interface ConnectedAiIntegration {
  id: string;
  type: string;
  name?: string;
  integration_config_values: Array<{ name?: string; value?: unknown }>;
  integrations_cloud_accounts: unknown[];
  // The real row carries more (source, status, timestamps, …) — this modal's
  // `editData` contract is `Record<string, unknown>`, not this narrower shape.
  [key: string]: unknown;
}

/**
 * `llm_model_name` is the one extra field worth surfacing today. Same field
 * name, same array-of-`{name,value}` shape `AddLLMConfigModal` itself reads
 * to hydrate its edit form — that modal is this chip's own "click to reopen"
 * target, so the two have to agree on how the config is shaped.
 */
const describeConnectedIntegration = (row: ConnectedAiIntegration): string => {
  const name = row.name || 'Untitled';
  if (row.type !== 'llm') return name;
  const model = row.integration_config_values.find((v) => v?.name === 'llm_model_name')?.value;
  return typeof model === 'string' && model ? `${name} · ${model}` : name;
};

/**
 * `IntegrationDynamicFormModal` — unlike `AddLLMConfigModal`, which reads
 * `integration_config_values` as the array it actually is — expects that
 * field as an object map keyed by field name (`editData?.integration_config_values?.[key]`,
 * `IntegrationDynamicFormModal.jsx:464` and repeated throughout). Passing the
 * raw array-shaped row straight through silently pre-fills every field blank.
 * `ListIntegrations.jsx`'s own "Edit" menu item already does this exact
 * flatten for this exact modal; mirrored here rather than invented fresh.
 */
/**
 * Fetches every connected integration of the given types and groups the rows
 * by their own `type` — shared by the AI panel's fetch and the alerts panel's
 * (below), since both just want "what's really connected, by type" rather
 * than the readiness model's plain `connectedBy` labels.
 */
const fetchIntegrationsByType = async (types: string[]): Promise<Record<string, ConnectedAiIntegration[]>> => {
  const res = await apiIntegrations.listIntegrations({ type: types, limit: 50 }).catch(() => undefined);
  const rows = (res as { data?: { data?: { integrations_list?: { rows?: unknown[] } } } })?.data?.data?.integrations_list?.rows;
  if (!Array.isArray(rows)) return {};
  const byType: Record<string, ConnectedAiIntegration[]> = {};
  rows.forEach((row) => {
    const parsed = parseIntegrationItem(row as Record<string, unknown>) as unknown as ConnectedAiIntegration;
    if (typeof parsed?.type !== 'string') return;
    (byType[parsed.type] ||= []).push(parsed);
  });
  return byType;
};

const toDynamicFormEditData = (row: ConnectedAiIntegration): Record<string, unknown> => ({
  ...row,
  integration_config_values: {
    ...row.integration_config_values.reduce<Record<string, unknown>>((acc, entry) => {
      if (entry?.name) acc[entry.name] = entry.value;
      return acc;
    }, {}),
    account_id: (row.integrations_cloud_accounts as Array<{ cloud_account_id?: string }> | undefined)?.map((a) => a?.cloud_account_id),
    integration_config_name: row.name,
  },
});

/**
 * Types that need a real, dedicated "Add" modal instead of the generic
 * schema-driven form every other route falls through to
 * (`IntegrationDynamicFormModal`) — the 7 ticketing types because their
 * backend schema has no user-facing name field at all (every save failed
 * with "name is required"), Discord because connecting one needs a bot-token
 * input the generic form has no field for. `account-form.jsx`'s own switch
 * never routes any of these through that generic form either — it opens one
 * of these same dedicated modals, which is what their real "Add" button on
 * the Integrations page actually mounts (#41112). Keyed by the registry's own
 * `types[0]` value, the same string `openIntegration` resolves a route to.
 * `DiscordInstallModal` ignores the `tool`/`editConfig` props the ticketing
 * ones use — same call site, ignored rather than branched around.
 */
const DEDICATED_ACCOUNT_MODALS: Record<string, React.ComponentType<any>> = {
  jira: JiraAccountModal,
  servicenow: ServiceNowAccountModal,
  freshdesk: FreshdeskAccountModal,
  pagerduty: PagerDutyAccountModal,
  zenduty: ZenDutyAccountModal,
  github: GithubAccountModal,
  gitlab: GitlabAccountModal,
  discord: DiscordInstallModal,
};

/**
 * Slack and Microsoft Teams connect via a single "open this OAuth URL"
 * action (`account-form.jsx`'s own `installUrl` for each) — no form, so no
 * modal to show at all. `openIntegration` special-cases these two routes
 * before ever touching `integrationModal` state; see its own comment.
 */
const CHAT_OAUTH_INSTALL_URLS: Record<string, string> = {
  SLACK: '/api/integrations/slack/install',
  MSTEAMS: '/api/integrations/ms-teams/install',
};

/**
 * A knowledge base row, as `apiKnowledgeBase.getKnowledgeBases('')` (the same
 * tenant-wide mode `KnowledgeBaseTab.jsx` itself uses when it has no single
 * account in scope) returns it. Skills has no `integrations_list` row and no
 * `types` to match against (`itemEvaluators.ts`'s `skillsEvaluator` reads
 * `hasKnowledgeBase` alone), so this is a parallel, Skills-only enrichment —
 * not the `ConnectedAiIntegration` path above.
 */
interface ConnectedKnowledgeBase {
  id: string;
  name?: string;
  account_name?: string;
  account_id?: string;
  document_count?: number;
}

/**
 * Same "— account" suffix `KnowledgeBaseTab.jsx` itself falls back to in
 * tenant-wide mode (`TestRetrievalPanel`'s own option labels), so a
 * multi-account tenant can tell entries apart the same way that surface
 * already does.
 */
const describeKnowledgeBase = (kb: ConnectedKnowledgeBase): string => {
  const name = kb.name || 'Untitled';
  const account = kb.account_name || kb.account_id;
  return account ? `${name} — ${account}` : name;
};

/** A row rendered as a chip in `ItemRow` — the shape every enrichment source
 * (AI integrations, knowledge bases, accounts, matched providers) reduces
 * down to, so there is one chip-rendering path instead of one per source. */
interface ConnectedChip {
  key: string;
  label: string;
  onClick?: () => void;
  /** Leading provider/account glyph. Omitted where there is nothing to key
   *  an icon off (the raw auto-detected-provider fallback, see `ItemRow`). */
  icon?: React.ReactNode;
  /** Per-chip tone override — one account or provider can be mid-flight
   *  while its siblings are healthy. Falls back to the item's own state
   *  (existing AI/KB chip behaviour) when omitted. */
  tone?: 'success' | 'warning' | 'critical' | 'neutral';
  /** A knowledge base chip's own edit fetch (name/content/agent mappings)
   *  is async, unlike every other chip's onClick — see known issue #22. */
  loading?: boolean;
}

/** `OnboardingState` → the tone its chip renders in. `blank` is unreachable in
 * practice (a connected chip only ever renders for a non-blank row) but kept
 * for a total, honest mapping rather than a cast. */
const STATE_TONE: Record<OnboardingState, 'success' | 'warning' | 'critical' | 'neutral'> = {
  done: 'success',
  partial: 'warning',
  error: 'critical',
  blank: 'neutral',
};

/**
 * A small trailing status glyph for a connected chip — an explicit signal on
 * top of the chip's own background tint, which alone reads too subtly to
 * scan at a glance across a row of several similarly-coloured chips.
 * Deliberately not `StatusGlyph` at a smaller size: that one is a 20px badge
 * meant to stand alone as an item's whole status; this rides inside an
 * already-labelled 18px chip, and `blank` never reaches a connected chip.
 */
const ChipStatusGlyph = ({ tone }: { tone: 'success' | 'warning' | 'critical' | 'neutral' }) => {
  if (tone === 'success') {
    return (
      <svg width={10} height={10} viewBox='0 0 24 24' aria-hidden='true'>
        <circle cx='12' cy='12' r='10' fill='none' stroke='var(--ds-green-500)' strokeWidth='2.5' />
        <path d='M7 12.5l3.2 3.2L17.5 8' fill='none' stroke='var(--ds-green-500)' strokeWidth='2.5' strokeLinecap='round' strokeLinejoin='round' />
      </svg>
    );
  }
  if (tone === 'warning') {
    return (
      <svg width={10} height={10} viewBox='0 0 24 24' aria-hidden='true'>
        <circle cx='12' cy='12' r='7' fill='var(--ds-amber-500)' />
      </svg>
    );
  }
  if (tone === 'critical') {
    return (
      <svg width={10} height={10} viewBox='0 0 24 24' aria-hidden='true'>
        <circle cx='12' cy='12' r='7' fill='var(--ds-red-500)' />
      </svg>
    );
  }
  return null;
};

/** Chips beyond this many collapse into a single "+N more" chip — a
 * potentially long, live-fetched list (many MCP servers, many knowledge
 * bases on an established tenant) must not overrun the row indefinitely. */
const CONNECTED_CHIP_CAP = 6;

/* ------------------------------------------------------------------ *
 * Status glyphs. Four, and only four.                                *
 * ------------------------------------------------------------------ */

const GLYPH_LABEL: Record<OnboardingState, string> = {
  done: 'Connected',
  partial: 'In progress',
  error: 'Needs attention',
  blank: 'Not connected',
};

/**
 * True exactly when `alertsEvaluator` downgraded an otherwise-healthy Alerts
 * row to `partial` for the one reason it ever does: a webhook exists and
 * every provider on it is itself connected, but nothing has posted to it
 * yet. Shared by the caption below and the per-chip tone, so a provider
 * chip's own checkmark never contradicts the sentence sitting right under
 * it — a real customer finding, not a hypothetical (the chips show
 * "connected" while the row says "waiting", which reads as the chips lying).
 */
const isWaitingForFirstAlert = (item: OnboardingItemStatus): boolean => {
  if (item.id !== 'alerts' || item.state !== 'partial' || !item.connectedByState) return false;
  const states = Object.values(item.connectedByState);
  return states.length > 0 && states.every((s) => s === 'done');
};

/**
 * Known issue #34. `accountEvaluator`'s aggregate reads done the moment any
 * one account works (issue #30); `providerMatchEvaluator`'s reads the worst
 * across every match (issue #20). Both rules are individually correct for
 * what each one answers — a milestone vs. a signal's health — and changing
 * either would reopen the issue the other one fixed. But a healthy chip
 * sitting right next to a broken or pending one, under a glyph that
 * disagrees with one of them, reads as a mistake rather than two different
 * questions. This names the rule in plain terms for exactly the case that's
 * ambiguous — not a redesign of the chips, which already show, in their own
 * color, which specific connection is which.
 */
const mixedStateNote = (item: OnboardingItemStatus): string | undefined => {
  // `chatEvaluator`'s own downgrade: installed, but no default channel to post to.
  if (item.missingDefaultChannel?.length) {
    const names = item.missingDefaultChannel.join(' and ');
    return `${names} ${
      item.missingDefaultChannel.length > 1 ? 'have' : 'has'
    } no default channel yet, so notifications that don't match a rule have nowhere to go.`;
  }
  // `alertsEvaluator`'s own downgrade: every matched provider is itself
  // `done`, yet the item reads `partial` — that combination only happens
  // when a webhook URL exists but nothing has posted to it yet.
  if (isWaitingForFirstAlert(item)) {
    return 'Connected — waiting for the first alert to arrive.';
  }
  if (item.accounts) {
    return item.state === 'done' && item.accounts.some((a) => a.state !== 'done')
      ? 'Marked connected once any one account works — chips above show which are still pending.'
      : undefined;
  }
  if (item.connectedByState && item.state !== 'done') {
    const states = Object.values(item.connectedByState);
    if (states.some((s) => s === 'done') && states.some((s) => s !== 'done')) {
      return 'Flags if any connection needs attention, even while another works fine.';
    }
  }
  return undefined;
};

/**
 * Drawn inline rather than composed from icon-font glyphs: the four need to
 * read as one family at 18px, and the amber ring has to be able to spin while
 * something is genuinely mid-flight.
 */
const StatusGlyph = ({ state, size = 20, spin = false, fraction }: { state: OnboardingState; size?: number; spin?: boolean; fraction?: number }) => {
  const common = { width: size, height: size, viewBox: '0 0 24 24', 'aria-hidden': true as const, style: { flexShrink: 0 } };
  if (state === 'done') {
    return (
      <svg {...common}>
        <circle cx='12' cy='12' r='9.4' fill='var(--ds-green-500)' />
        <path
          d='M7.7 12.4l2.9 2.9 5.7-6.2'
          stroke='var(--ds-background-100)'
          strokeWidth='2.1'
          fill='none'
          strokeLinecap='round'
          strokeLinejoin='round'
        />
      </svg>
    );
  }
  if (state === 'partial') {
    if (!spin && fraction !== undefined) {
      const r = 9.4;
      const circumference = 2 * Math.PI * r;
      const filled = Math.max(0, Math.min(1, fraction)) * circumference;
      return (
        <svg {...common}>
          <circle cx='12' cy='12' r={r} fill='none' stroke='var(--ds-amber-200)' strokeWidth='2.3' />
          <circle
            cx='12'
            cy='12'
            r={r}
            fill='none'
            stroke='var(--ds-amber-500)'
            strokeWidth='2.3'
            strokeLinecap='round'
            strokeDasharray={`${filled} ${circumference}`}
            transform='rotate(-90 12 12)'
          />
        </svg>
      );
    }
    return (
      <svg {...common}>
        <circle cx='12' cy='12' r='9.4' fill='none' stroke='var(--ds-amber-200)' strokeWidth='2.3' />
        <path d='M12 2.6a9.4 9.4 0 019.4 9.4' fill='none' stroke='var(--ds-amber-500)' strokeWidth='2.3' strokeLinecap='round'>
          {spin && <animateTransform attributeName='transform' type='rotate' from='0 12 12' to='360 12 12' dur='1.1s' repeatCount='indefinite' />}
        </path>
      </svg>
    );
  }
  if (state === 'error') {
    return (
      <svg {...common}>
        <path d='M12 3.2l9.6 16.6H2.4z' fill='var(--ds-red-500)' />
        <path d='M12 9.2v4.4' stroke='var(--ds-background-100)' strokeWidth='1.9' strokeLinecap='round' />
        <circle cx='12' cy='16.6' r='1.05' fill='var(--ds-background-100)' />
      </svg>
    );
  }
  return (
    <svg {...common}>
      <circle cx='12' cy='12' r='9.2' fill='none' stroke='var(--ds-gray-400)' strokeWidth='1.7' strokeDasharray='2.6 3.3' />
    </svg>
  );
};

/* ------------------------------------------------------------------ *
 * Provider picker — tiles, then one dropdown for the long tail.       *
 * ------------------------------------------------------------------ */

/** One clickable provider tile — compact: a monogram-sized icon, a name, and
 *  (once connected) a small status glyph. Deliberately not the same full
 *  "Connected" text pill the chips elsewhere use: at this tile's width, the
 *  pill's own padding and word left barely any room for the provider's own
 *  name, which is the one thing the tile actually exists to show — squeezing
 *  "Azure App Insights" down to "A…" defeats the tile's whole purpose. The
 *  glyph alone (reusing `ChipStatusGlyph`, the same "done" mark the chips use)
 *  reads at a glance and costs a fraction of the width; the accessible name
 *  ("<label> — connected. Add another", set below) still says "connected" in
 *  full for anyone not reading it visually. */
const ProviderTile = ({
  route,
  label,
  connected,
  onSelect,
}: {
  route: string;
  label: string;
  connected: boolean;
  onSelect: (route: string) => void;
}) => (
  <Card
    id={`setup-provider-${route.toLowerCase()}`}
    data-testid={`setup-provider-${route.toLowerCase()}`}
    variant='outlined'
    size='sm'
    elevation='flat'
    interactive
    aria-label={connected ? `${label} — connected. Add another` : `Connect ${label}`}
    onClick={() => onSelect(route)}
  >
    <Box sx={{ display: 'flex', alignItems: 'center', gap: 'var(--ds-space-2)', width: '100%', minWidth: 0 }}>
      <Box sx={{ width: 20, height: 20, display: 'flex', alignItems: 'center', justifyContent: 'center', flexShrink: 0 }}>
        <CloudProviderIcon cloud_provider={route} width='16px' height='16px' />
      </Box>
      <Typography
        noWrap
        sx={{
          fontFamily: 'var(--ds-font-display)',
          fontSize: 'var(--ds-text-caption)',
          fontWeight: 'var(--ds-font-weight-medium)',
          color: 'var(--ds-gray-700)',
          flexGrow: 1,
          minWidth: 0,
        }}
      >
        {label}
      </Typography>
      {connected && (
        <Box sx={{ display: 'flex', alignItems: 'center', flexShrink: 0 }} aria-hidden='true'>
          <ChipStatusGlyph tone='success' />
        </Box>
      )}
    </Box>
  </Card>
);

/**
 * Tiles for the providers most tenants pick, and one dropdown holding the rest.
 *
 * A full grid of every provider stops being a menu and becomes a wall, so tiles
 * are always cut to `SHORTLIST` and everything — shown tiles included — stays
 * reachable from the dropdown. Nothing here navigates out of setup.
 *
 * Tile slots favour providers the tenant can actually use today: cloud-capable
 * ones (`cloudCapable: true` — works on any account type) always qualify;
 * everything else only qualifies once the tenant has a Kubernetes or
 * Self-hosted account, since those are the only account types a non-cloud-
 * capable provider's account picker ever offers. A provider that loses the
 * tile-slot race this way is never hidden — it still sits in the dropdown,
 * this only decides who gets the front row.
 */
const ProviderPicker = ({
  itemId,
  providers,
  connectedLabels,
  connectedProviders,
  onSelect,
}: {
  itemId: string;
  providers: Array<{ route: string; label: string; blurb?: string; cloudCapable?: boolean }>;
  connectedLabels: string[];
  connectedProviders: string[];
  onSelect: (route: string) => void;
}) => {
  const hasK8sOrSelfHosted = connectedProviders.includes('K8s') || connectedProviders.includes('SelfHosted');
  const usableNow = providers.filter((p) => p.cloudCapable || hasK8sOrSelfHosted);
  const usableLater = providers.filter((p) => !(p.cloudCapable || hasK8sOrSelfHosted));
  const long = providers.length > SHORTLIST;
  const tiles = [...usableNow, ...usableLater].slice(0, SHORTLIST);
  const options = providers.map((p) => ({
    value: p.route,
    label: connectedLabels.includes(p.label) ? `${p.label} — already connected` : p.label,
    disabled: connectedLabels.includes(p.label),
  }));

  return (
    <Box data-testid={`setup-picker-${itemId}`}>
      <Box sx={{ display: 'grid', gap: 'var(--ds-space-2)', gridTemplateColumns: 'repeat(auto-fill, minmax(150px, 1fr))' }}>
        {tiles.map((p) => (
          <ProviderTile
            key={`${itemId}-${p.route}`}
            route={p.route}
            label={p.label}
            connected={connectedLabels.includes(p.label)}
            onSelect={onSelect}
          />
        ))}
      </Box>
      {long && (
        <Box sx={{ mt: 'var(--ds-space-2)', maxWidth: 320 }}>
          <Select
            id={`setup-more-${itemId}`}
            size='sm'
            value={null}
            options={options}
            placeholder={`All ${providers.length} providers`}
            onChange={onSelect}
          />
        </Box>
      )}
    </Box>
  );
};

/* ------------------------------------------------------------------ *
 * One item row.                                                      *
 * ------------------------------------------------------------------ */

/**
 * Always visible: glyph, name, the one-line purpose, what is connected, one
 * action. Everything else — the longer why, When and Effort, and the impact
 * table — sits behind a single disclosure, closed by default.
 */
const ItemRow = ({
  item,
  first,
  compact,
  open,
  showPicker,
  canWrite,
  onToggleWhy,
  onTogglePicker,
  onSelect,
  connectedProviders,
  connectedIntegrations,
  onOpenConnectedIntegration,
  alertIntegrationsByType,
  connectedKnowledgeBases,
  onOpenKnowledgeBase,
  pendingKnowledgeBaseId,
  selectedModules,
  onReopenAgentInstall,
  onSetDefaultChannel,
}: {
  item: OnboardingItemStatus;
  first: boolean;
  /** True when this is the only item in its panel (accounts): the panel's own
   * header already names it, so repeating the item's name would say the same
   * thing twice — skip straight to purpose, connected accounts and the action. */
  compact?: boolean;
  open: boolean;
  showPicker: boolean;
  canWrite: boolean;
  onToggleWhy: (id: string) => void;
  onTogglePicker: (id: string) => void;
  onSelect: (route: string) => void;
  /** The tenant's own connected cloud/account types, e.g. ['AWS', 'K8s'] —
   * passed straight through to `ProviderPicker`. See its own doc comment. */
  connectedProviders: string[];
  /** The real, named rows behind this item's `connectedBy` count — only ever
   * populated for the AI panel's three items (`SetupCenter`'s own fetch, kept
   * out of the shared readiness model since nothing else needs it). Absent
   * for every other item, which keeps showing its plain provider-label chips. */
  connectedIntegrations?: ConnectedAiIntegration[];
  onOpenConnectedIntegration?: (row: ConnectedAiIntegration) => void;
  /** Full dict (not one item's slice, unlike `connectedIntegrations` above) —
   * the alerts item has 13 providers, not one, so its chip loop below looks
   * up each provider's own row by type itself rather than taking a
   * pre-sliced list the way the AI panel's single-provider items do. */
  alertIntegrationsByType?: Record<string, ConnectedAiIntegration[]>;
  /** Same idea as the pair above, for Skills specifically — see
   * `ConnectedKnowledgeBase`'s own doc comment for why it's a separate pair
   * rather than reusing `connectedIntegrations`. */
  connectedKnowledgeBases?: ConnectedKnowledgeBase[];
  onOpenKnowledgeBase?: (kb: ConnectedKnowledgeBase) => void;
  /** The knowledge base whose edit fetch is currently in flight (known issue
   *  #22) — the fetch is per-row, so this names the one row to show busy,
   *  not the whole item. */
  pendingKnowledgeBaseId?: string | null;
  /** The card's own module filter, for naming which of them an `na` item's
   *  "Not needed" tag refers to. See `notNeededFor`. */
  selectedModules: string[];
  /** Reopens the install command for a K8s/self-hosted account row stuck
   *  `partial` on its agent — see the account-chip `onClick` below for why. */
  onReopenAgentInstall?: (account: ItemAccountRow) => void;
  /** Opens where a chat app's default channel is picked — see
   *  `openDefaultChannelSetup`. Takes the provider's registry `route`. */
  onSetDefaultChannel?: (route: string) => void;
}) => {
  const connected = item.connectedBy.length > 0;
  // A row with no alternatives to choose between opens its form directly — the
  // AI panel's items *are* the catalog entries, so a picker there would list
  // its own siblings.
  const direct = item.direct && item.providers.length === 1;
  const notNeededForModules = notNeededFor(selectedModules);
  // The picker and the disclosure read as a child of the item's own name
  // line, so they sit one step further in. Not meaningful in compact mode:
  // there is no name line to nest under. Connected chips used to share this
  // indent too, but a chip row already reads as "what's under this item"
  // without it — the extra 32px just pushed a short row of pills in from
  // the left for no legibility gain, worse the narrower the card.
  const nestedIndent = compact ? 0 : '32px';

  // The provider *tile's* own "Connected" badge — and the picker dropdown's
  // "already connected" disabling — must mean a real, finished connection,
  // not merely "a row for this provider exists": a K8s account still waiting
  // on its agent must not tell the tile it's done. `item.connectedBy` (used
  // everywhere else on this row — the summary chips above) stays exactly as
  // it is on purpose: a partial or broken connection still has to show up
  // there, in its own warning/critical tone, not disappear. Only the account
  // item has the per-row state (`item.accounts`) needed to tell "exists" from
  // "done" apart; every other item's tile keeps today's behaviour.
  const tileConnectedLabels = item.accounts
    ? ONBOARDING_CLOUD_PROVIDERS.filter((p) => item.accounts!.some((a) => a.cloudProvider === p.provider && a.state === 'done')).map((p) => p.label)
    : item.connectedBy;

  // Every enrichment source reduces to the same chip shape, so there is one
  // render path below rather than one per source — no item is ever fed more
  // than one of these at once.
  const enrichedChips: ConnectedChip[] =
    connectedIntegrations && connectedIntegrations.length > 0
      ? connectedIntegrations.map((row) => ({
          key: row.id,
          label: describeConnectedIntegration(row),
          onClick: () => onOpenConnectedIntegration?.(row),
        }))
      : connectedKnowledgeBases && connectedKnowledgeBases.length > 0
      ? connectedKnowledgeBases.map((kb) => ({
          key: kb.id,
          label: describeKnowledgeBase(kb),
          onClick: () => onOpenKnowledgeBase?.(kb),
          loading: kb.id === pendingKnowledgeBaseId,
        }))
      : item.accounts && item.accounts.length > 0
      ? // Mostly static tags — an account row has no in-card form to reopen,
        // it's managed from the Accounts page. The one exception: a K8s/
        // self-hosted row stuck `partial` on its agent gets a click target
        // that reopens the install command, since that's the one thing a
        // locked-to-/setup first-time user has no other way to reach (the
        // app-wide reminder banner for exactly this is suppressed on this
        // route — see `PendingAgentInstallBanner`'s own comment for why).
        item.accounts.map((a) => ({
          key: a.id,
          label: a.label,
          icon: <CloudProviderIcon cloud_provider={a.cloudProvider} width='12px' height='12px' />,
          tone: STATE_TONE[a.state],
          onClick:
            a.state === 'partial' && ['k8s', 'selfhosted'].includes((a.cloudProvider || '').toLowerCase())
              ? () => onReopenAgentInstall?.(a)
              : undefined,
        }))
      : [];

  // Keyed for the fallback chip path below — a leading icon for a
  // provider-label chip when the label resolves back to this item's own
  // registry entry (it always does, except the one auto-detected-but-not-done
  // corner case in `providerMatchEvaluator`, which appends a raw signal name
  // that never matches a registered `label` — that chip just renders without
  // an icon, same as before this lookup existed).
  const providerByLabel = new Map(item.providers.map((p): [string, (typeof item.providers)[number]] => [p.label, p]));
  const note = mixedStateNote(item);

  return (
    <Box
      data-testid={`setup-item-${item.id}`}
      sx={{
        display: 'flex',
        gap: 'var(--ds-space-3)',
        py: compact ? 0 : 'var(--ds-space-4)',
        // Solid rules between items, none inside one. The first row in a panel
        // sits directly under the header, so it takes no rule at all.
        borderTop: first || compact ? 'none' : '1px solid var(--ds-gray-300)',
        ...(first && !compact ? { pt: 'var(--ds-space-1)' } : {}),
        // `na` fades but stays clickable. Nothing is hidden: a customer who
        // picked one module can still see — and connect — what the others use,
        // which is the whole reason the chip row says "Nothing is hidden".
        ...(item.need === 'na' && !item.planned ? { opacity: 0.55 } : {}),
      }}
    >
      {!compact && <StatusGlyph state={item.state} spin={item.state === 'partial'} />}

      <Box sx={{ flexGrow: 1, minWidth: 0 }}>
        {!compact && (
          <Box sx={{ display: 'flex', alignItems: 'center', gap: 'var(--ds-space-2)', flexWrap: 'wrap', justifyContent: 'space-between' }}>
            <Box sx={{ display: 'flex', alignItems: 'center', gap: 'var(--ds-space-2)', flexWrap: 'wrap', minWidth: 0 }}>
              <Typography sx={{ fontSize: 'var(--ds-text-body)', fontWeight: 'var(--ds-font-weight-medium)', color: 'var(--ds-gray-700)' }}>
                {item.name}
              </Typography>
              {item.planned && (
                <Label tone='neutral' size='sm'>
                  Planned
                </Label>
              )}
              {/* Three tones, driven by what the selected modules need — not by
                  the item's structural `optional` flag, which answers a
                  different question (see OnboardingItemStatus.need). `req`
                  carries no tag at all: tagging the majority case is noise. */}
              {!item.planned && item.need === 'opt' && (
                <Label tone='neutral' size='sm'>
                  Optional
                </Label>
              )}
              {!item.planned && item.need === 'na' && (
                <Label tone='neutral' size='sm'>
                  {notNeededForModules ? `Not needed for ${notNeededForModules}` : 'Not needed'}
                </Label>
              )}
              {item.autoDetected && (
                <Chip size='xs' tone='success' dot>
                  Auto-detected · nothing to configure
                </Chip>
              )}
              <Typography component='span' sx={{ fontSize: 'var(--ds-text-caption)', color: 'var(--ds-gray-500)' }}>
                {GLYPH_LABEL[item.state]}
              </Typography>
            </Box>
            {canWrite && !item.planned && item.providers.length > 0 && (
              <Box sx={{ flexShrink: 0 }}>
                <Button
                  tone={connected ? 'ghost' : 'secondary'}
                  size='xs'
                  data-testid={`setup-add-${item.id}`}
                  onClick={() => (direct ? onSelect(item.providers[0].route) : onTogglePicker(item.id))}
                >
                  {connected ? 'Add more' : 'Add'}
                </Button>
              </Box>
            )}
          </Box>
        )}

        <Typography sx={{ fontSize: 'var(--ds-text-small)', color: 'var(--ds-gray-600)', mt: compact ? 0 : '2px' }}>
          {fillBrandTokens(item.purpose)}
        </Typography>

        {connected && !item.autoDetected && (
          <Box sx={{ display: 'flex', gap: 'var(--ds-space-1)', flexWrap: 'wrap', mt: 'var(--ds-space-2)' }}>
            {enrichedChips.length > 0
              ? enrichedChips.slice(0, CONNECTED_CHIP_CAP).map((chip) => {
                  const tone = chip.tone ?? (item.state === 'error' ? 'critical' : 'success');
                  return (
                    <Chip
                      key={chip.key}
                      size='xs'
                      tone={tone}
                      shape='rect'
                      icon={chip.icon}
                      trailingIcon={<ChipStatusGlyph tone={tone} />}
                      onClick={chip.onClick}
                      loading={chip.loading}
                    >
                      {chip.label}
                    </Chip>
                  );
                })
              : item.connectedBy.map((label) => {
                  const provider = providerByLabel.get(label);
                  // Known issue #23: color this chip by its own provider's
                  // state where the evaluator can tell them apart, not by the
                  // item's aggregate — a healthy provider must not read
                  // critical just because a sibling on the same item is
                  // broken. Falls back to the aggregate where it can't
                  // (`connectedByState` is undefined for evaluators — Skills,
                  // Accounts — that only ever have one state to give).
                  const labelState = item.connectedByState?.[label];
                  // Every provider here is individually `done` — Alertmanager
                  // and PagerDuty are both wired up and healthy — but showing
                  // that as a green check reads as "working" when nothing has
                  // actually arrived yet. Match the item's own amber "In
                  // progress" signal instead of contradicting it.
                  const tone = isWaitingForFirstAlert(item)
                    ? 'warning'
                    : labelState
                    ? STATE_TONE[labelState]
                    : item.state === 'error'
                    ? 'critical'
                    : 'success';
                  // Alerts only, today: its webhook URL is the one thing on
                  // this item a user might reasonably need to see again (a
                  // locked first-time user has no other way back to it — see
                  // the account-chip comment above for the same shape of
                  // problem). Not every provider here has a fetched row
                  // (`alertIntegrationsByType` is empty for anything that
                  // isn't a webhook type), so this is a no-op, inert chip for
                  // everything else — same as before this existed.
                  const alertRow = alertIntegrationsByType?.[provider?.types?.[0] || '']?.[0];
                  return (
                    <Chip
                      key={`${item.id}-${label}`}
                      size='xs'
                      tone={tone}
                      shape='rect'
                      icon={provider && <CloudProviderIcon cloud_provider={provider.route} width='12px' height='12px' />}
                      trailingIcon={<ChipStatusGlyph tone={tone} />}
                      onClick={alertRow ? () => onOpenConnectedIntegration?.(alertRow) : undefined}
                    >
                      {item.missingDefaultChannel?.includes(label)
                        ? `${label} · No default channel`
                        : item.defaultBy?.includes(label)
                        ? `${label} · Default`
                        : label}
                    </Chip>
                  );
                })}
            {enrichedChips.length > CONNECTED_CHIP_CAP && (
              <Chip size='xs' tone={item.state === 'error' ? 'critical' : 'success'} shape='rect'>
                +{enrichedChips.length - CONNECTED_CHIP_CAP} more
              </Chip>
            )}
          </Box>
        )}

        {connected && !item.autoDetected && note && (
          <Typography
            data-testid={`setup-item-${item.id}-mixed-state-note`}
            sx={{ fontSize: 'var(--ds-text-caption)', color: 'var(--ds-gray-500)', mt: 'var(--ds-space-1)' }}
          >
            {note}
          </Typography>
        )}

        {canWrite && onSetDefaultChannel && item.missingDefaultChannel?.length ? (
          <Box sx={{ display: 'flex', gap: 'var(--ds-space-2)', flexWrap: 'wrap', mt: 'var(--ds-space-2)' }}>
            {item.missingDefaultChannel.map((label) => {
              const provider = providerByLabel.get(label);
              if (!provider) return null;
              return (
                <Button
                  key={`${item.id}-default-channel-${provider.route}`}
                  tone='secondary'
                  size='xs'
                  data-testid={`setup-default-channel-${provider.route.toLowerCase()}`}
                  onClick={() => onSetDefaultChannel(provider.route)}
                >
                  Set {label} default channel
                </Button>
              );
            })}
          </Box>
        ) : null}

        {/* Compact (accounts) has no name row to carry the Add action, so it
            keeps its own inline button here instead. */}
        {compact && canWrite && !item.planned && item.providers.length > 0 && (
          <Box sx={{ mt: 'var(--ds-space-2)' }}>
            <Button
              tone={connected ? 'ghost' : 'secondary'}
              size='xs'
              data-testid={`setup-add-${item.id}`}
              onClick={() => (direct ? onSelect(item.providers[0].route) : onTogglePicker(item.id))}
            >
              {connected ? 'Add more' : 'Add'}
            </Button>
          </Box>
        )}

        {showPicker && canWrite && !direct && item.providers.length > 0 && (
          <Box sx={{ mt: 'var(--ds-space-3)', pr: nestedIndent }}>
            <ProviderPicker
              itemId={item.id}
              providers={item.providers}
              connectedLabels={tileConnectedLabels}
              connectedProviders={connectedProviders}
              onSelect={onSelect}
            />
          </Box>
        )}

        <Box sx={{ mt: 'var(--ds-space-2)' }}>
          <Button tone='ghost' size='xs' data-testid={`setup-why-${item.id}`} aria-expanded={open} onClick={() => onToggleWhy(item.id)}>
            Why this matters
            <ExpandMoreIcon sx={{ fontSize: 16, transform: open ? 'rotate(180deg)' : 'none', transition: 'transform 150ms ease' }} />
          </Button>
        </Box>

        {open && (
          <Box
            sx={{
              mt: 'var(--ds-space-3)',
              p: 'var(--ds-space-3)',
              background: 'var(--ds-background-200)',
              border: '1px solid var(--ds-gray-200)',
              borderRadius: 'var(--ds-radius-md)',
            }}
          >
            <Typography sx={{ fontSize: 'var(--ds-text-small)', color: 'var(--ds-gray-600)', mb: 'var(--ds-space-2)' }}>
              {fillBrandTokens(item.why)}
            </Typography>

            {item.meta.length > 0 && (
              <Box sx={{ display: 'grid', gridTemplateColumns: '96px 1fr', columnGap: 'var(--ds-space-3)', rowGap: '3px', mb: 'var(--ds-space-2)' }}>
                {item.meta.map(([label, value]) => (
                  <React.Fragment key={`${item.id}-${label}`}>
                    <Typography
                      sx={{
                        fontSize: '10px',
                        fontWeight: 'var(--ds-font-weight-medium)',
                        color: 'var(--ds-gray-500)',
                        textTransform: 'uppercase',
                        letterSpacing: '0.06em',
                      }}
                    >
                      {label}
                    </Typography>
                    <Typography sx={{ fontSize: 'var(--ds-text-caption)', color: 'var(--ds-gray-600)' }}>{fillBrandTokens(value)}</Typography>
                  </React.Fragment>
                ))}
              </Box>
            )}

            {item.modules && item.modules.length > 0 && (
              <Box sx={{ display: 'flex', alignItems: 'center', gap: '5px', flexWrap: 'wrap', mb: 'var(--ds-space-2)' }}>
                <Typography
                  component='span'
                  sx={{
                    fontSize: '10px',
                    fontWeight: 'var(--ds-font-weight-medium)',
                    color: 'var(--ds-gray-500)',
                    textTransform: 'uppercase',
                    letterSpacing: '0.06em',
                    mr: '1px',
                  }}
                >
                  Required by
                </Typography>
                {item.modules.map((key) => {
                  const m = MODULE_META[key];
                  if (!m) return null;
                  return (
                    <Box
                      key={`${item.id}-${key}`}
                      component='span'
                      sx={{
                        display: 'inline-flex',
                        alignItems: 'center',
                        height: '17px',
                        px: '7px',
                        borderRadius: 'var(--ds-radius-sm)',
                        border: `1px solid ${m.border}`,
                        background: m.bg,
                        color: m.ink,
                        fontFamily: 'var(--ds-font-display)',
                        fontWeight: 'var(--ds-font-weight-medium)',
                        fontSize: '9.5px',
                        whiteSpace: 'nowrap',
                      }}
                    >
                      {m.name}
                    </Box>
                  );
                })}
              </Box>
            )}

            {item.unlocks && item.unlocks.length > 0 && (
              <>
                <Typography
                  sx={{
                    fontSize: 'var(--ds-text-caption)',
                    fontWeight: 'var(--ds-font-weight-medium)',
                    color: 'var(--ds-gray-700)',
                  }}
                >
                  Unlocks
                </Typography>
                <Box component='ul' sx={{ m: 0, mb: 'var(--ds-space-1)', pl: 'var(--ds-space-4)' }}>
                  {item.unlocks.map((u) => (
                    <Typography key={`${item.id}-${u}`} component='li' sx={{ fontSize: 'var(--ds-text-caption)', color: 'var(--ds-gray-600)' }}>
                      {fillBrandTokens(u)}
                    </Typography>
                  ))}
                </Box>
              </>
            )}

            {item.state !== 'done' && (
              <Box
                sx={{
                  mt: 'var(--ds-space-2)',
                  p: 'var(--ds-space-2) var(--ds-space-3)',
                  background: 'var(--ds-amber-100)',
                  border: '1px solid var(--ds-amber-200)',
                  borderRadius: 'var(--ds-radius-md)',
                }}
              >
                <Box sx={{ display: 'flex', alignItems: 'center', gap: '6px' }}>
                  <InfoOutlinedIcon sx={{ fontSize: 14, color: 'var(--ds-amber-500)', flexShrink: 0 }} />
                  <Typography
                    sx={{ fontFamily: 'var(--ds-font-display)', fontWeight: 'var(--ds-font-weight-semibold)', fontSize: 'var(--ds-text-caption)' }}
                  >
                    Skip this and these stay empty
                  </Typography>
                </Box>
                {item.impact.map(([surface, cost]) => (
                  <Box
                    key={`${item.id}-${surface}`}
                    sx={{
                      display: 'grid',
                      gridTemplateColumns: '150px 1fr',
                      gap: 'var(--ds-space-3)',
                      py: 'var(--ds-space-2)',
                      borderTop: '1px solid var(--ds-amber-200)',
                      fontSize: 'var(--ds-text-caption)',
                    }}
                  >
                    <Typography
                      sx={{
                        fontFamily: 'var(--ds-font-display)',
                        fontWeight: 'var(--ds-font-weight-medium)',
                        fontSize: 'var(--ds-text-caption)',
                        color: 'var(--ds-amber-700)',
                      }}
                    >
                      {fillBrandTokens(surface)}
                    </Typography>
                    <Typography sx={{ fontSize: 'var(--ds-text-caption)', color: 'var(--ds-gray-600)' }}>{fillBrandTokens(cost)}</Typography>
                  </Box>
                ))}
              </Box>
            )}
          </Box>
        )}
      </Box>
    </Box>
  );
};

/* ------------------------------------------------------------------ *
 * One panel, rendered as its own card.                                *
 * ------------------------------------------------------------------ */

const PanelBody = ({
  panel,
  expanded,
  onToggle,
  openWhy,
  openPicker,
  canWrite,
  onToggleWhy,
  onTogglePicker,
  onSelect,
  connectedProviders,
  connectedIntegrationsByType,
  onOpenConnectedIntegration,
  alertIntegrationsByType,
  connectedKnowledgeBases,
  onOpenKnowledgeBase,
  pendingKnowledgeBaseId,
  selectedModules,
  onReopenAgentInstall,
  onSetDefaultChannel,
}: {
  panel: OnboardingPanelStatus;
  /** Single-open accordion: only the expanded panel renders its items. */
  expanded: boolean;
  onToggle: () => void;
  openWhy: Record<string, boolean>;
  openPicker: Record<string, boolean>;
  canWrite: boolean;
  onToggleWhy: (id: string) => void;
  onTogglePicker: (id: string) => void;
  onSelect: (route: string) => void;
  /** Passed straight through to `ItemRow` → `ProviderPicker`. */
  connectedProviders: string[];
  /** Both pairs below are passed straight through to `ItemRow` — see its own
   * doc comment. Only the AI panel's and Skills' items actually use either. */
  connectedIntegrationsByType?: Record<string, ConnectedAiIntegration[]>;
  onOpenConnectedIntegration?: (row: ConnectedAiIntegration) => void;
  /** Passed straight through to `ItemRow` — see its own doc comment. Only the
   * alerts item actually uses it. */
  alertIntegrationsByType?: Record<string, ConnectedAiIntegration[]>;
  connectedKnowledgeBases?: ConnectedKnowledgeBase[];
  onOpenKnowledgeBase?: (kb: ConnectedKnowledgeBase) => void;
  pendingKnowledgeBaseId?: string | null;
  /** Passed straight through to `ItemRow` — see its own doc comment. */
  selectedModules: string[];
  /** Passed straight through to `ItemRow` — see its own doc comment. */
  onReopenAgentInstall?: (account: ItemAccountRow) => void;
  /** Passed straight through to `ItemRow` — see its own doc comment. */
  onSetDefaultChannel?: (route: string) => void;
}) => {
  // Accounts is the one panel where the item *is* the panel — its own name
  // would just repeat "Accounts and clusters" a second time. An explicit
  // registry flag, not inferred from `items.length === 1`: `smarter` can
  // reach one visible item too (Skills, once Preferences/Rules are hidden),
  // and its panel title doesn't name Skills the way Accounts' does.
  const compact = panel.compact === true;

  // Telemetry is `anyOf`: metrics or logs alone is enough to read `done`.
  // Traces is `req` for two modules (build-spec §9, "Green Telemetry while
  // the banner still asks for traces"), so a tenant can see this panel done
  // and still be missing something Troubleshoot/Automation actually needs.
  // Both facts are true and contradict on screen without this line. Generic
  // wording on purpose — `need` is the strongest level across every selected
  // module, not which one specifically, so naming one module by name here
  // could name the wrong one.
  const tracesGap =
    panel.id === 'telemetry' && panel.state === 'done'
      ? panel.items.find((i) => i.id === 'traces' && i.need === 'req' && i.state !== 'done')
      : undefined;

  const scorable = panel.items.filter((i) => !i.planned);
  const anyItemPending = panel.items.some((i) => i.state === 'partial');
  // `connectedBy` names every provider with a real, non-disabled account row —
  // a K8s cluster still waiting on its agent is in there too. The badge says
  // "connected", so it only counts once the item's own state agrees: a row
  // that exists but is still `partial` must not read as finished here, even
  // though the glyph beside it already (correctly) shows the spinner.
  const countLabel =
    panel.id === 'accounts'
      ? panel.items[0]?.state === 'done' && panel.items[0].connectedBy.length
        ? `${panel.items[0].connectedBy.length} connected`
        : ''
      : scorable.length > 0
      ? `${scorable.filter((i) => i.state === 'done').length} of ${scorable.length}`
      : '';

  return (
    <>
      <Box
        component='button'
        type='button'
        data-testid={`setup-panel-toggle-${panel.id}`}
        aria-expanded={expanded}
        onClick={onToggle}
        sx={{
          display: 'flex',
          alignItems: 'flex-start',
          gap: 'var(--ds-space-3)',
          width: '100%',
          border: 'none',
          background: 'none',
          padding: 0,
          font: 'inherit',
          textAlign: 'left',
          cursor: 'pointer',
        }}
      >
        <StatusGlyph
          state={panel.state}
          size={22}
          spin={anyItemPending}
          fraction={!anyItemPending && panel.requiredTotal > 0 ? panel.requiredDone / panel.requiredTotal : undefined}
        />
        <Box sx={{ flexGrow: 1, minWidth: 0 }}>
          <Box sx={{ display: 'flex', alignItems: 'center', gap: 'var(--ds-space-2)', flexWrap: 'wrap' }}>
            <Typography
              className='setup-panel-title'
              sx={{
                fontFamily: 'var(--ds-font-display)',
                fontSize: 'var(--ds-text-body)',
                fontWeight: 'var(--ds-font-weight-semibold)',
                color: 'var(--ds-gray-700)',
              }}
            >
              {panel.title}
            </Typography>
          </Box>
          <Typography sx={{ fontSize: 'var(--ds-text-small)', color: 'var(--ds-gray-600)' }}>{fillBrandTokens(panel.sub)}</Typography>
          {tracesGap && (
            <Typography
              data-testid='setup-telemetry-traces-gap'
              sx={{ fontSize: 'var(--ds-text-caption)', color: 'var(--ds-gray-500)', mt: 'var(--ds-space-0)' }}
            >
              Metrics or logs got this to done — traces are still required for what you've selected.
            </Typography>
          )}
        </Box>
        {countLabel && (
          <Chip size='xs' tone='neutral' shape='pill' sx={{ flexShrink: 0, mt: 'var(--ds-space-0)' }}>
            {countLabel}
          </Chip>
        )}
        <ExpandMoreIcon
          sx={{
            fontSize: 20,
            color: 'var(--ds-gray-500)',
            flexShrink: 0,
            mt: '1px',
            transform: expanded ? 'rotate(180deg)' : 'none',
            transition: 'transform 150ms ease',
          }}
        />
      </Box>

      {expanded && (
        <>
          {panel.blocked && (
            <Typography sx={{ fontSize: 'var(--ds-text-caption)', color: 'var(--ds-gray-600)', fontStyle: 'italic', mt: 'var(--ds-space-2)' }}>
              Connect a model above first — there is nothing to teach until your agents can think.
            </Typography>
          )}
          <Box sx={{ mt: 'var(--ds-space-2)', pl: '34px' }}>
            {panel.items.map((item, idx) => (
              <ItemRow
                key={item.id}
                item={item}
                first={idx === 0}
                compact={compact}
                open={!!openWhy[item.id]}
                // Open by default while there's little to lose by showing the
                // catalog (nothing connected yet, or exactly one thing — the
                // tenant may still be choosing). Past that, the catalog is
                // redundant with the connected-chips row above it and just
                // adds noise; "Add more" reveals it on demand instead.
                showPicker={openPicker[item.id] ?? item.connectedBy.length <= 1}
                canWrite={canWrite}
                onToggleWhy={onToggleWhy}
                onTogglePicker={onTogglePicker}
                onSelect={onSelect}
                connectedProviders={connectedProviders}
                connectedIntegrations={connectedIntegrationsByType?.[item.providers[0]?.types?.[0] || '']}
                onOpenConnectedIntegration={onOpenConnectedIntegration}
                alertIntegrationsByType={item.id === 'alerts' ? alertIntegrationsByType : undefined}
                connectedKnowledgeBases={item.id === 'skills' ? connectedKnowledgeBases : undefined}
                onOpenKnowledgeBase={onOpenKnowledgeBase}
                pendingKnowledgeBaseId={item.id === 'skills' ? pendingKnowledgeBaseId : undefined}
                selectedModules={selectedModules}
                onReopenAgentInstall={onReopenAgentInstall}
                onSetDefaultChannel={item.id === 'chat' ? onSetDefaultChannel : undefined}
              />
            ))}
          </Box>
        </>
      )}
    </>
  );
};

/* ------------------------------------------------------------------ *
 * The account gate. No account, no integration — every form needs an  *
 * account_id, so there is nothing any other panel could actually do.  *
 * ------------------------------------------------------------------ */

/**
 * A panel with nothing to open yet: no glyph, no chevron, no items, not
 * clickable. Distinct from `panel.blocked` (panel 4 before AI is done), which
 * stays expandable and only disables its action — this one can't be browsed
 * at all, because every row inside it would need an account it doesn't have.
 */
const LockedPanelRow = ({ panel, first }: { panel: OnboardingPanelStatus; first: boolean }) => (
  <Box
    data-testid={`setup-locked-${panel.id}`}
    sx={{
      display: 'flex',
      alignItems: 'flex-start',
      gap: 'var(--ds-space-3)',
      py: 'var(--ds-space-3)',
      borderTop: first ? 'none' : '1px solid var(--ds-gray-200)',
    }}
  >
    <LockOutlinedIcon sx={{ fontSize: 18, color: 'var(--ds-gray-400)', flexShrink: 0, mt: '1px' }} />
    <Box sx={{ minWidth: 0 }}>
      <Box sx={{ display: 'flex', alignItems: 'center', gap: 'var(--ds-space-2)', flexWrap: 'wrap' }}>
        <Typography sx={{ fontSize: 'var(--ds-text-body)', fontWeight: 'var(--ds-font-weight-medium)', color: 'var(--ds-gray-500)' }}>
          {panel.title}
        </Typography>
      </Box>
      <Typography sx={{ fontSize: 'var(--ds-text-small)', color: 'var(--ds-gray-500)' }}>{fillBrandTokens(panel.sub)}</Typography>
    </Box>
  </Box>
);

/* ------------------------------------------------------------------ *
 * The card stack.                                                     *
 * ------------------------------------------------------------------ */

const SetupCenter = () => {
  const router = useRouter();
  // Module chips. All on by default: a customer who has expressed no preference
  // should see the fullest view, not an arbitrary subset. Held locally for now;
  // it moves onto the onboard_step record with the rest of the state in Phase 3.
  const [selectedModules, setSelectedModules] = React.useState<string[]>(() => MODULES.map((m) => m.id));
  // Both disclosures are per-item, so two panels can sit open at different
  // depths without fighting each other.
  const [openWhy, setOpenWhy] = React.useState<Record<string, boolean>>({});
  const [openPicker, setOpenPicker] = React.useState<Record<string, boolean>>({});

  // Read the brand where it renders: /api/public/app_config is a real round trip,
  // so a value snapshotted into state would keep the house name on a partner tenant.
  const { title: brandTitle } = useBrandingConfig();
  // Both come from the session, by way of the gate. `hasWriteAccess()` resolves
  // against a module global that only `withAuth` populates, and `withAuth` wraps
  // neither `/setup` nor `/home` — so on this page it answered "not an admin"
  // for everyone, real admins included, and the card rendered with nothing the
  // user could act on and no way out.
  const { canManageSetup: canWrite, hasConnectedAccount, refresh: refreshGate } = useSetupGate();

  // Shared with `SetupProgressBanner` (see the hook's own doc comment) so the
  // two can never disagree on "X of N connected" for the same tenant.
  const { readiness, loading, refresh: refreshReadinessInputs, accounts } = useOnboardingReadiness(selectedModules);

  // Which integration modal is open, if any — every non-account provider opens
  // its integration modal in place, over the card, never a trip to the
  // integrations catalog. Keyed by the real backend `type` (lowercase), not
  // the model's display `route`. `editData` is null for "Add" (a fresh form)
  // and the real row for a chip click on an already-connected AI integration
  // (a pre-filled form, same modal).
  // `editData`'s shape depends on the destination modal, not just on whether
  // it's editing — see `toDynamicFormEditData`'s own doc comment.
  const [integrationModal, setIntegrationModal] = React.useState<{ type: string; label: string; editData: Record<string, unknown> | null } | null>(
    null
  );
  // Which cloud/K8s account modal is open, if any. Accounts get the same
  // in-place treatment as every other provider — none of the five routes
  // through /accounts/account-form's own listing page, which is what
  // rendered when a tile only deep-linked there.
  const [accountModal, setAccountModal] = React.useState<string | null>(null);
  // The K8s account whose install command is being re-shown — see
  // `reopenAgentInstall` below. Self-hosted's equivalent reuses
  // `integrationModal` (the same `vm_agent` form Setup already opens right
  // after creating the account), so it needs no state of its own here.
  const [k8sAgentInstallAccount, setK8sAgentInstallAccount] = React.useState<ItemAccountRow | null>(null);
  // Single-open accordion across all six panels. Accounts starts open since
  // it is the one panel every fresh tenant has to act on first.
  const [expandedPanel, setExpandedPanel] = React.useState('accounts');

  // route -> {label, types} across every non-account panel, built from live
  // readiness rather than re-importing PANELS, so it always matches what is
  // actually rendered.
  const providerIndex = React.useMemo(() => {
    const map: Record<string, { label: string; types?: string[] }> = {};
    (readiness?.panels || []).forEach((panel) => {
      if (panel.id === 'accounts') return;
      panel.items.forEach((item) => {
        item.providers.forEach((p) => {
          if (!map[p.route]) map[p.route] = { label: p.label, types: p.types };
        });
      });
    });
    return map;
  }, [readiness]);

  // The real, named LLM/AI-gateway/MCP rows behind the AI panel's chips —
  // fetched separately from readiness (which only needs to know a row of one
  // of these types exists), keyed by backend `type` so `ItemRow` can look up
  // its own item's rows via `item.providers[0].types[0]`. Silent failure is
  // fine: the panel still works from `connectedBy`'s plain provider labels,
  // same as before this existed.
  const [aiIntegrations, setAiIntegrations] = React.useState<Record<string, ConnectedAiIntegration[]>>({});
  // Called from both the mount effect below and `closeIntegrationModal` on
  // save, so a slower mount-time fetch could otherwise resolve after — and
  // overwrite — a faster post-save one. Same request-sequence discipline as
  // `useOnboardingReadiness`'s own mount effect: only the most-recently
  // *started* call is ever allowed to write state.
  const aiIntegrationsRequestId = React.useRef(0);
  const fetchAiIntegrations = React.useCallback(async () => {
    const requestId = (aiIntegrationsRequestId.current += 1);
    const byType = await fetchIntegrationsByType(['llm', 'llm_gateway', 'mcp']);
    if (requestId !== aiIntegrationsRequestId.current) return;
    setAiIntegrations(byType);
  }, []);

  // Same idea, for the alerts panel's 13 webhook providers: lets a connected
  // provider's chip reopen its own edit form (see `onReopenAlertIntegration`
  // below) instead of just naming it. Separate from `aiIntegrations` rather
  // than merged into one fetch — different domain, and merging would mean a
  // misleadingly-named `aiIntegrations` dict carrying alert rows too.
  const [alertIntegrations, setAlertIntegrations] = React.useState<Record<string, ConnectedAiIntegration[]>>({});
  const alertIntegrationsRequestId = React.useRef(0);
  const fetchAlertIntegrations = React.useCallback(async () => {
    const requestId = (alertIntegrationsRequestId.current += 1);
    const byType = await fetchIntegrationsByType(ALERT_PROVIDERS.flatMap((p) => p.types || []));
    if (requestId !== alertIntegrationsRequestId.current) return;
    setAlertIntegrations(byType);
  }, []);

  React.useEffect(() => {
    fetchAiIntegrations();
    fetchAlertIntegrations();
  }, [fetchAiIntegrations, fetchAlertIntegrations]);

  // The real, named knowledge bases behind the Skills chip — same reasoning
  // as `aiIntegrations` above, parallel rather than shared because Skills
  // isn't type-keyed the way the AI panel's three items are (see
  // `ConnectedKnowledgeBase`'s own doc comment). An empty `accountId` is
  // `KnowledgeBaseTab.jsx`'s own tenant-wide mode — one call for every
  // account's knowledge bases, not one call per account.
  const [knowledgeBases, setKnowledgeBases] = React.useState<ConnectedKnowledgeBase[]>([]);
  const knowledgeBasesRequestId = React.useRef(0);
  const fetchKnowledgeBases = React.useCallback(async () => {
    const requestId = (knowledgeBasesRequestId.current += 1);
    const res = await apiKnowledgeBase.getKnowledgeBases('').catch(() => undefined);
    if (requestId !== knowledgeBasesRequestId.current) return;
    if (!Array.isArray(res?.data)) return;
    setKnowledgeBases(res.data as ConnectedKnowledgeBase[]);
  }, []);

  React.useEffect(() => {
    fetchKnowledgeBases();
  }, [fetchKnowledgeBases]);

  const openAccountModal = React.useCallback((route: string) => {
    setAccountModal(route);
  }, []);

  // A chip click on an already-connected integration — originally just the AI
  // panel's three items, now also an alerts provider — same modal as "Add",
  // pre-filled. The label still comes from the registry (via `providerIndex`,
  // matched on the row's own `type`) rather than a second hardcoded copy of it.
  const openConnectedIntegration = React.useCallback(
    (row: ConnectedAiIntegration) => {
      const label = Object.values(providerIndex).find((p) => p.types?.includes(row.type))?.label || row.type;
      setIntegrationModal({ type: row.type, label, editData: row.type === 'llm' ? row : toDynamicFormEditData(row) });
    },
    [providerIndex]
  );

  // Refreshes readiness without touching `accountModal`. K8s's wizard calls
  // this the moment step 1 creates the account row, well before it is done —
  // step 2 (the install command, the wait-for-agent poll) still has to render,
  // so the modal must stay mounted. Wiring this to the same function that
  // closes the modal was the bug: it tore the wizard down right after step 1,
  // before the user ever saw the install command.
  const refreshReadiness = React.useCallback(() => {
    refreshReadinessInputs();
    // The gate reads accounts separately, so it has to be told too — otherwise
    // connecting the first account leaves the user gated until a page reload.
    refreshGate();
  }, [refreshReadinessInputs, refreshGate]);

  // Accounts a knowledge base can actually be created under — real (non-demo)
  // rows only. Reuses the account list this page already fetched for
  // readiness rather than triggering a second listAccounts request.
  const eligibleKbAccounts = React.useMemo(
    () => (accounts || []).filter((a): a is OnboardingAccount & { id: string } => typeof a?.id === 'string' && a.id.length > 0 && a.id !== 'demo'),
    [accounts]
  );

  const kbForm = useKnowledgeBaseForm({
    onSaved: () => {
      fetchKnowledgeBases();
      refreshReadiness();
    },
  });

  // Shown only when there is more than one account to choose from — see
  // handleAddKnowledgeBase.
  const [kbAccountPickerOpen, setKbAccountPickerOpen] = React.useState(false);

  // The one chip whose edit fetch (name/content/agent mappings) is in
  // flight — known issue #22: unlike every other item, opening this one
  // isn't instant, and nothing said so. `kbForm.submitting` already tracks
  // "a request is in flight" but not which row, so this is scoped to the
  // clicked chip's id specifically rather than reusing that shared flag.
  const [pendingKnowledgeBaseId, setPendingKnowledgeBaseId] = React.useState<string | null>(null);

  // "Add" on the Skills item used to hand off to the b-Cortex modal's
  // Knowledge Base tab via the `?bcortex=` deep link — but nothing upstream
  // of /setup resolves a selected account into that link, so it always opens
  // tenant-wide, and tenant-wide mode hides every write affordance including
  // "+ Create". The button looked live and did nothing. Opening the create
  // form directly, scoped to a real account, actually lets the user add one.
  const handleAddKnowledgeBase = React.useCallback(() => {
    if (eligibleKbAccounts.length === 1) {
      kbForm.openCreate(eligibleKbAccounts[0].id);
    } else if (eligibleKbAccounts.length > 1) {
      setKbAccountPickerOpen(true);
    }
    // Zero eligible accounts: nothing to scope the create to. Not reachable
    // in practice — the accounts panel gates the rest of the card — so this
    // is a silent no-op rather than a dead-end error state.
  }, [eligibleKbAccounts, kbForm]);

  const handlePickKbAccount = React.useCallback(
    (accountId: string) => {
      setKbAccountPickerOpen(false);
      kbForm.openCreate(accountId);
    },
    [kbForm]
  );

  // A chip click already knows which account it belongs to — the row itself
  // carries it — so unlike Add, it never needs the picker.
  const handleOpenKnowledgeBase = React.useCallback(
    (kb: ConnectedKnowledgeBase) => {
      if (!kb.account_id) return;
      setPendingKnowledgeBaseId(kb.id);
      // `openEdit` always resolves (it catches its own errors, see the hook)
      // — clearing here on either outcome is what stops the chip's busy
      // state outliving the fetch it names.
      kbForm.openEdit(kb.account_id, kb).finally(() => setPendingKnowledgeBaseId(null));
    },
    [kbForm]
  );

  // Turning the last chip off would leave a card with nothing required and no
  // way to read it, so the model treats an empty selection as every module
  // selected. Toggling is therefore always safe — there is no dead end.
  const onToggleModule = React.useCallback((id: string) => {
    setSelectedModules((prev) => (prev.includes(id) ? prev.filter((m) => m !== id) : [...prev, id]));
  }, []);

  const closeAccountModal = React.useCallback(
    (saved?: boolean) => {
      setAccountModal(null);
      if (saved) {
        refreshReadiness();
      }
    },
    [refreshReadiness]
  );

  // #41115: a self-hosted account alone collects nothing — it needs a VM
  // Agent integration before any forager exists, and that step lives on a
  // different route entirely (not a registry-resolvable `openIntegration`
  // target), easy to leave for later and forget. Opens the same generic,
  // schema-driven form the real Integrations page uses for `vm_agent` (its
  // own account picker handles scoping to the one just created), mirroring
  // how Kubernetes's own wizard shows its install step right after step 1 —
  // just as a second modal rather than a second step of the same one, since
  // AddSelfHostedAccountModal is shared with pages outside Setup and isn't
  // built as an inline two-step wizard.
  const closeSelfHostedAccountModal = React.useCallback(
    (saved?: boolean) => {
      closeAccountModal(saved);
      if (saved) setIntegrationModal({ type: 'vm_agent', label: 'Proxy Agent', editData: null });
    },
    [closeAccountModal]
  );

  // The install step for a K8s/self-hosted account that's still `partial` on
  // its agent — reached by clicking that account's own chip. A locked-to-
  // /setup first-time user has no other way back to it: the credentials are
  // shown once at creation and never stored, and the app-wide reminder that
  // would normally point this out (`PendingAgentInstallBanner`) is
  // deliberately suppressed on this exact route (see its own comment) to
  // avoid duplicating this row's state. Self-hosted reuses the same `vm_agent`
  // form the account-create flow already opens — safe to reopen even if a
  // Proxy Agent integration exists from an earlier, abandoned attempt, since
  // the generic form's own account picker just lets the user pick this
  // account again. K8s has no such form to reopen, so it gets its own small
  // dialog that fetches a fresh install command on demand.
  const reopenAgentInstall = React.useCallback((account: ItemAccountRow) => {
    if (account.cloudProvider?.toLowerCase() === 'selfhosted') {
      setIntegrationModal({ type: 'vm_agent', label: 'Proxy Agent', editData: null });
    } else {
      setK8sAgentInstallAccount(account);
    }
  }, []);

  const closeIntegrationModal = React.useCallback(
    (saved?: boolean) => {
      setIntegrationModal(null);
      if (saved) {
        refreshReadinessInputs();
        // The AI/alerts panels' chip labels are a separate fetch from
        // readiness (see `fetchAiIntegrations`/`fetchAlertIntegrations`), so a
        // just-saved name/token change needs its own refresh too —
        // `refreshReadinessInputs` alone would leave the chip showing the
        // pre-edit detail until the next full reload.
        fetchAiIntegrations();
        fetchAlertIntegrations();
      }
    },
    [refreshReadinessInputs, fetchAiIntegrations, fetchAlertIntegrations]
  );

  const openIntegration = React.useCallback(
    (route: string) => {
      // Skills has no integration form of its own — its Add opens the
      // Knowledge Base create form directly (see handleAddKnowledgeBase):
      // the account-scoped popup, not the tenant-wide b-Cortex modal this
      // used to hand off to.
      if (route === 'KNOWLEDGE_BASE') {
        handleAddKnowledgeBase();
        return;
      }
      // Slack and Teams have no form of their own to show — connecting is a
      // single "open this OAuth URL, wait for the popup to close" action
      // (see chatOAuthInstall.ts's own doc comment for why only these two).
      // There's nothing to put in the modal slot; just start it, and refresh
      // once the popup closes the same way the real Integrations page does —
      // it has no success/failure signal either, only the close itself.
      const chatInstallUrl = CHAT_OAUTH_INSTALL_URLS[route];
      if (chatInstallUrl) {
        openChatOAuthInstall(route.toLowerCase(), chatInstallUrl, () => closeIntegrationModal(true));
        return;
      }
      const info = providerIndex[route];
      setIntegrationModal({ type: info?.types?.[0] || route.toLowerCase(), label: info?.label || route, editData: null });
    },
    [providerIndex, handleAddKnowledgeBase, closeIntegrationModal]
  );

  // A chat app's default channel is picked on its own Integrations page
  // ("Map Channel", or Google Chat's "Set default") — the channel lists and
  // team pickers live there, not in a form Setup can open. New tab for the
  // same reason as the Google Chat hand-off below; readiness refreshes when
  // this tab regains focus, so the step clears without a reload. Each
  // registry route is already that page's `?cloudProvider=` value.
  // One pending listener at most, and none left behind after unmount — a
  // second click before refocusing must not queue a second refetch.
  const refocusRefreshRef = React.useRef<(() => void) | null>(null);
  React.useEffect(
    () => () => {
      if (refocusRefreshRef.current) window.removeEventListener('focus', refocusRefreshRef.current);
    },
    []
  );
  const openDefaultChannelSetup = React.useCallback(
    (route: string) => {
      window.open(`/accounts/account-form?cloudProvider=${route.toLowerCase()}`, '_blank', 'noopener');
      if (refocusRefreshRef.current) return;
      const onFocus = () => {
        refocusRefreshRef.current = null;
        refreshReadinessInputs();
      };
      refocusRefreshRef.current = onFocus;
      window.addEventListener('focus', onFocus, { once: true });
    },
    [refreshReadinessInputs]
  );

  const togglePanel = React.useCallback((id: string) => setExpandedPanel((prev) => (prev === id ? '' : id)), []);
  const toggleWhy = React.useCallback((id: string) => setOpenWhy((prev) => ({ ...prev, [id]: !prev[id] })), []);
  const togglePicker = React.useCallback((id: string) => setOpenPicker((prev) => ({ ...prev, [id]: !prev[id] })), []);

  if (loading) {
    return (
      <Card id={`${CARD_ID}-loading`} size='md' elevation='flat' sx={{ mb: 'var(--ds-space-4)' }}>
        <Skeleton width='40%' height={20} />
        <Box sx={{ mt: 'var(--ds-space-3)' }}>
          <Skeleton width='100%' height={8} />
        </Box>
      </Card>
    );
  }

  // The card lives on its own route now, so there is no "stop nagging" case to
  // handle: a complete tenant that opens /setup sees every panel green, which is
  // the honest answer. The old `done === total` early return existed only
  // because the card squatted on the home page, and it was what left a gated
  // user with no Finish button on screen and no way to unlock the product.
  //
  // A failed lookup is different: with no readiness there is nothing truthful to
  // draw, and guessing "nothing is connected" would be worse than saying so.
  if (!readiness) {
    return (
      <Card id={`${CARD_ID}-unavailable`} data-testid={`${CARD_ID}-unavailable`} size='md' elevation='flat'>
        <Typography sx={{ fontSize: 'var(--ds-text-body)', fontWeight: 'var(--ds-font-weight-medium)', color: 'var(--ds-gray-700)' }}>
          We could not load your setup state
        </Typography>
        <Typography sx={{ fontSize: 'var(--ds-text-small)', color: 'var(--ds-gray-600)', mt: 'var(--ds-space-1)' }}>
          Nothing you have already connected is affected. Reload the page to try again.
        </Typography>
      </Card>
    );
  }

  const { panels, percent, done, total, essentialDone, essentialTotal, connectedProviders, isFreshTenant } = readiness;
  const essentialsLeft = essentialTotal - essentialDone;
  const followUps = connectedProviders.flatMap((p) => (PROVIDER_FOLLOW_UPS[p] || []).map((f) => ({ provider: p, ...f })));
  const [accountsPanel, ...rest] = panels;
  // `hasCloudAccount` is existence, which is no longer the gate's own
  // threshold either — SetupGateContext now keys on the same per-row
  // "actually connecting or connected" check this exit row always has.
  // Kept separate from `SetupGateContext` regardless: the exit
  // row asks "is there somewhere real to send them", answered from the
  // panel's own already-fetched rows, not a second read of the gate's own
  // (differently-scoped) account lookup. A bare row that exists but hasn't
  // actually connected (a K8s account still `partial`, agent not checked in
  // yet) must not count here either.
  //
  // That's a per-account question, not the panel's own aggregate: with two
  // accounts, one done and one still installing, `accountsPanel.state` is
  // `partial` (worst-wins — correct, and left alone, for the progress bar and
  // this card's own status glyph, which must keep saying something here is
  // unfinished). But the exit button asks "is there anywhere real to send
  // them", and there already is — so it reads the per-account rows directly
  // instead, and unlocks the moment any one of them is actually done.
  const canGoToDashboard = (accountsPanel.items.find((i) => i.id === 'account')?.accounts ?? []).some((a) => a.state === 'done');
  // Same condition the exit bar itself renders on, below — kept as one flag
  // so the two can't drift apart. Reserves room at the foot of the panel
  // stack so its last card is never hidden behind the fixed bar sitting on
  // top of it; generous on purpose, since the bar's own text can wrap to two
  // lines on a narrow viewport.
  const exitBarVisible = canWrite;

  // A non-admin who reaches here on a tenant with no connected account (see
  // SetupGateContext's own doc comment for why they're sent here at all now)
  // has nothing below they could act on — showing the checklist, even
  // read-only the way the `!canWrite` notes further down do once an account
  // exists, would just tease actions that aren't theirs to take. One line
  // instead. `hasConnectedAccount` is the exact signal that sent them here,
  // not a second, possibly-disagreeing computation of the same question.
  if (!canWrite && hasConnectedAccount === false) {
    return (
      <Box data-testid='setup-ask-admin'>
        {/* `no-permissions` over `first-time`: what's special about this render
            path isn't that the tenant is new (an admin on the same tenant sees
            the full checklist instead) — it's that this viewer specifically
            can't act on it. `surface`, since this replaces the page's entire
            content rather than sitting inside one of the bordered Cards below. */}
        <EmptyState
          size='page'
          surface
          illustration='no-permissions'
          title={`${brandTitle} hasn't been set up yet`}
          description="Ask a tenant administrator to connect an account. There's nothing to do here until then."
        />
      </Box>
    );
  }

  const DedicatedAccountModal = integrationModal ? DEDICATED_ACCOUNT_MODALS[integrationModal.type] : undefined;

  return (
    <Box
      data-testid={CARD_ID}
      sx={{ mb: 'var(--ds-space-4)', display: 'flex', flexDirection: 'column', gap: 'var(--ds-space-3)', pb: exitBarVisible ? '104px' : 0 }}
    >
      {/* Accounts rides with the heading and the bar: it is the only panel that
          gates finishing, so separating it from the progress it drives would
          break the one causal link on the screen. */}
      <Card id={CARD_ID} size='md' elevation='flat'>
        <Typography
          sx={{
            fontFamily: 'var(--ds-font-display)',
            fontSize: 'var(--ds-text-title)',
            fontWeight: 'var(--ds-font-weight-semibold)',
            color: 'var(--ds-brand-600)',
          }}
        >
          {isFreshTenant ? `Set up ${brandTitle}` : `Finish setting up ${brandTitle}`}
        </Typography>
        <Typography sx={{ fontSize: 'var(--ds-text-small)', color: 'var(--ds-gray-600)' }}>
          {isFreshTenant
            ? 'Connect a cluster or cloud account to begin. Everything else builds on it.'
            : essentialsLeft > 0
            ? `${essentialsLeft} essential ${essentialsLeft === 1 ? 'panel' : 'panels'} left before the core workflow works end to end.`
            : `Essentials are done. The panels below widen what ${brandTitle} can do.`}
        </Typography>

        {/* Module chips. Changing them changes what is required, never what is
            shown — an item no selected module needs fades rather than
            disappearing, so nobody is quietly cut off from a capability. */}
        {canWrite && (
          <Box sx={{ display: 'flex', alignItems: 'center', gap: 'var(--ds-space-2)', flexWrap: 'wrap', mt: 'var(--ds-space-3)' }}>
            <Typography
              sx={{
                fontSize: 'var(--ds-text-caption)',
                fontWeight: 'var(--ds-font-weight-medium)',
                color: 'var(--ds-gray-600)',
                textTransform: 'uppercase',
                letterSpacing: '0.04em',
              }}
            >
              Set up for
            </Typography>
            {MODULES.map((m) => {
              const on = selectedModules.includes(m.id);
              return (
                <Chip
                  key={m.id}
                  size='xs'
                  // `pressed` is the DS's own toggle API — it resolves the chip
                  // to the filter variant and carries the aria state, which a
                  // raw aria-pressed on a chip would not.
                  pressed={on}
                  data-testid={`setup-module-${m.id}`}
                  onClick={() => onToggleModule(m.id)}
                >
                  {m.label}
                </Chip>
              );
            })}
            <Typography sx={{ fontSize: 'var(--ds-text-caption)', color: 'var(--ds-gray-500)' }}>
              Changes what is required. Nothing is hidden.
            </Typography>
          </Box>
        )}

        <Box sx={{ mt: 'var(--ds-space-3)', mb: 'var(--ds-space-2)' }}>
          <ProgressBar
            id={`${CARD_ID}-progress`}
            value={percent}
            size='md'
            tone={essentialsLeft === 0 ? 'success' : 'info'}
            // ProgressBar reserves a fixed 80px label column with no overflow
            // handling, so anything longer than about 'N of N' wraps to a second
            // line and knocks the bar out of alignment — the unit can't be
            // spelled out here, which is why it is spelled out just below instead.
            label={`${done} of ${total}`}
            showValue
          />
          {/* Two denominators in two units, one above the other: the subtitle
              counts essential *panels*, this bar counts required *items* across
              every panel, essential or not. Nothing in either number's own text
              says so — "N panels left" reads like it could be the same axis as
              an adjacent, unlabeled "X of Y". One line breaks the assumption
              rather than trying to rename either number (known issues #16). */}
          {!isFreshTenant && essentialsLeft > 0 && (
            <Typography sx={{ fontSize: 'var(--ds-text-caption)', color: 'var(--ds-gray-500)', mt: 'var(--ds-space-1)' }}>
              Counts required items across every panel — not just the {essentialsLeft} essential {essentialsLeft === 1 ? 'panel' : 'panels'} above.
            </Typography>
          )}
        </Box>

        {isFreshTenant ? (
          <>
            {/* No account yet, so nothing else can be opened — every integration
                form needs an account_id. One card: pick a provider, then a flat
                list of what unlocks the moment one exists. Not the six-card
                accordion below, which only makes sense once there's something
                to scope it to. */}
            {canWrite && (
              <Box sx={{ mt: 'var(--ds-space-3)' }}>
                <Typography
                  sx={{
                    fontSize: 'var(--ds-text-caption)',
                    fontWeight: 'var(--ds-font-weight-medium)',
                    color: 'var(--ds-gray-700)',
                    textTransform: 'uppercase',
                    letterSpacing: '0.04em',
                  }}
                >
                  Start here — pick a provider
                </Typography>
                <Box sx={{ mt: 'var(--ds-space-2)' }}>
                  <ProviderPicker
                    itemId={accountsPanel.items[0].id}
                    providers={accountsPanel.items[0].providers}
                    connectedLabels={[]}
                    connectedProviders={connectedProviders}
                    onSelect={openAccountModal}
                  />
                </Box>
              </Box>
            )}

            <Box sx={{ mt: 'var(--ds-space-4)' }}>
              <Typography
                sx={{
                  fontSize: 'var(--ds-text-caption)',
                  fontWeight: 'var(--ds-font-weight-medium)',
                  color: 'var(--ds-gray-700)',
                  textTransform: 'uppercase',
                  letterSpacing: '0.04em',
                  mb: 'var(--ds-space-0)',
                }}
              >
                Unlocks once an account exists
              </Typography>
              <Typography sx={{ fontSize: 'var(--ds-text-caption)', color: 'var(--ds-gray-600)', mb: 'var(--ds-space-2)' }}>
                Every integration form asks which account it belongs to, so none of these can be opened yet. Connect one above and all five unlock at
                once.
              </Typography>
              {rest.map((panel, idx) => (
                <LockedPanelRow key={panel.id} panel={panel} first={idx === 0} />
              ))}
            </Box>

            {!canWrite && (
              <Typography sx={{ fontSize: 'var(--ds-text-caption)', color: 'var(--ds-gray-600)', fontStyle: 'italic', mt: 'var(--ds-space-3)' }}>
                You need write access to connect accounts and integrations. This checklist shows what a tenant administrator still has to finish.
              </Typography>
            )}
          </>
        ) : (
          <>
            <PanelBody
              panel={accountsPanel}
              expanded={expandedPanel === accountsPanel.id}
              onToggle={() => togglePanel(accountsPanel.id)}
              openWhy={openWhy}
              openPicker={openPicker}
              canWrite={canWrite}
              onToggleWhy={toggleWhy}
              onTogglePicker={togglePicker}
              onSelect={openAccountModal}
              connectedProviders={connectedProviders}
              selectedModules={selectedModules}
              onReopenAgentInstall={reopenAgentInstall}
            />

            {followUps.length > 0 && (
              <Box
                sx={{
                  mt: 'var(--ds-space-3)',
                  p: 'var(--ds-space-3)',
                  borderRadius: 'var(--ds-radius-md)',
                  background: 'var(--ds-amber-100)',
                  border: '1px solid var(--ds-amber-200)',
                }}
              >
                <Typography sx={{ fontSize: 'var(--ds-text-caption)', fontWeight: 'var(--ds-font-weight-medium)', color: 'var(--ds-gray-700)' }}>
                  Worth checking on the accounts you have
                </Typography>
                {followUps.map((f) => (
                  <Typography
                    key={`${f.provider}-${f.text}`}
                    sx={{ fontSize: 'var(--ds-text-caption)', color: 'var(--ds-gray-600)', mt: 'var(--ds-space-1)' }}
                  >
                    {f.provider}: {f.text}
                  </Typography>
                ))}
              </Box>
            )}
          </>
        )}
      </Card>

      {!isFreshTenant && (
        <>
          {rest.map((panel) => (
            <Card key={panel.id} id={`${CARD_ID}-${panel.id}`} data-testid={`setup-panel-${panel.id}`} size='sm' elevation='flat'>
              <PanelBody
                panel={panel}
                expanded={expandedPanel === panel.id}
                onToggle={() => togglePanel(panel.id)}
                openWhy={openWhy}
                openPicker={openPicker}
                canWrite={canWrite}
                onToggleWhy={toggleWhy}
                onTogglePicker={togglePicker}
                onSelect={openIntegration}
                connectedProviders={connectedProviders}
                connectedIntegrationsByType={aiIntegrations}
                onOpenConnectedIntegration={openConnectedIntegration}
                alertIntegrationsByType={alertIntegrations}
                connectedKnowledgeBases={knowledgeBases}
                onOpenKnowledgeBase={handleOpenKnowledgeBase}
                pendingKnowledgeBaseId={pendingKnowledgeBaseId}
                selectedModules={selectedModules}
                onSetDefaultChannel={openDefaultChannelSetup}
              />
            </Card>
          ))}

          {!canWrite && (
            <Typography sx={{ fontSize: 'var(--ds-text-caption)', color: 'var(--ds-gray-600)', fontStyle: 'italic' }}>
              You need write access to connect accounts and integrations. This checklist shows what a tenant administrator still has to finish.
            </Typography>
          )}
        </>
      )}

      {/* A fixed footer, not a row in the stack — reachable without scrolling
          back down through however many panels are expanded, the same reason
          `Card`'s own `elevation='floating'` doc comment names for this exact
          shape (a sticky/fixed surface that needs to visually separate itself
          from content scrolling underneath it). There is no Save: nothing
          here is a draft, every connection is already live.

          The outer strip spans the full viewport so it can center the card
          the same way the page itself is centered, without knowing the
          sidebar's width — and stays `pointerEvents: 'none'` outside the
          card itself so it never intercepts a click meant for whatever
          (sidebar, a scrolled panel's own button) is underneath its empty
          margins. Only the card re-enables pointer events.

          It navigates, it does not unlock — the gate itself still lifts on
          existence alone (SetupGateContext), because waiting on agent health
          there would trap a mid-install K8s tenant on this very page. This
          button asks a stricter question on purpose: is there a *finished*
          account to actually look at — any one, not every one. A lone K8s
          row still `partial` (agent not connected yet) leaves this disabled
          even though the gate is already open — pressing it while it's live
          never sends someone to a dashboard with nothing on it yet. A second
          account still installing does not hold this back once a first one
          is done: there is already somewhere real to send them. Stays visible
          on a fresh tenant with zero accounts too — `canGoToDashboard`'s copy
          says what's still needed and the button stays disabled, rather than
          the whole bar vanishing and leaving no on-screen answer to "what do
          I do now". */}
      {exitBarVisible && (
        <Box sx={{ position: 'fixed', bottom: 0, left: 80, right: 0, zIndex: 10, pointerEvents: 'none' }}>
          <Box sx={{ maxWidth: 1240, mx: 'auto', px: 'var(--ds-space-4)', pointerEvents: 'auto' }}>
            <Card elevation='floating' size='sm' sx={{ borderRadius: 'var(--ds-radius-xl) var(--ds-radius-xl) 0 0' }}>
              <Box sx={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', gap: 'var(--ds-space-3)', flexWrap: 'wrap' }}>
                <Box sx={{ display: 'flex', alignItems: 'center', gap: 'var(--ds-space-2)' }}>
                  {canGoToDashboard ? (
                    <CelebrationOutlinedIcon sx={{ fontSize: 'var(--ds-text-heading)', color: 'var(--ds-green-600)' }} />
                  ) : (
                    <InfoOutlinedIcon sx={{ fontSize: 'var(--ds-text-heading)', color: 'var(--ds-blue-400)' }} />
                  )}
                  <Typography sx={{ fontFamily: 'var(--ds-font-display)', fontSize: 'var(--ds-text-body-lg)', color: 'var(--ds-gray-700)' }}>
                    {canGoToDashboard
                      ? `Nice work! To get the best out of ${brandTitle}, please add more integrations.`
                      : `You need at least one connected account to use ${brandTitle}.`}
                  </Typography>
                </Box>
                <Button
                  tone='primary'
                  size='md'
                  disabled={!canGoToDashboard}
                  data-testid='setup-go-to-dashboard'
                  trailingAccent={<ArrowForwardIcon />}
                  onClick={() => router.push('/home')}
                >
                  Go to dashboard
                </Button>
              </Box>
            </Card>
          </Box>
        </Box>
      )}

      {integrationModal &&
        (integrationModal.type === 'llm' ? (
          <AddLLMConfigModal
            open
            onClose={() => closeIntegrationModal(false)}
            editData={integrationModal.editData}
            onSaved={() => closeIntegrationModal(true)}
          />
        ) : integrationModal.type === 'google_chat_space' ? (
          // Google Chat has no in-app "start" action at all, unlike every
          // other chat/ticket provider here — connecting begins on Google's
          // side (an admin adds the bot to a space) and only finishes on
          // /accounts/account-form, the one place that reads the resulting
          // Connect-card deep-link (`space_id`/`display_name` query params —
          // see GoogleChatSpacesPanel.jsx's own doc comment). There is
          // nothing to open here; showing instructions instead of a fake
          // "Add" form is the honest version of this fix, not a stand-in for
          // a real one (#41112). New tab, never the current one: a tenant
          // still locked onto /setup would just get bounced straight back —
          // harmless either way, but only the new tab pays for it.
          <Modal
            open
            handleClose={() => closeIntegrationModal(false)}
            title='Connect Google Chat'
            subtitle={`Connects through the ${brandTitle} bot — nothing to sign into here.`}
            actionButtons={
              <Box sx={{ display: 'flex', justifyContent: 'flex-end', gap: 'var(--ds-space-3)', p: `var(--ds-space-3) var(--ds-space-5)` }}>
                <Button tone='secondary' size='md' onClick={() => closeIntegrationModal(false)}>
                  Close
                </Button>
                <Button
                  tone='primary'
                  size='md'
                  onClick={() => window.open('/accounts/account-form?cloudProvider=google_chat', '_blank', 'noopener')}
                >
                  Open Google Chat setup
                </Button>
              </Box>
            }
          >
            <Typography sx={{ fontSize: 'var(--ds-text-body)', color: 'var(--ds-gray-700)', lineHeight: 1.5, p: 'var(--ds-space-5)', pt: 0 }}>
              Add the {brandTitle} bot to a Google Chat space. It posts a Connect card there — open that page, and the card's link binds the space to
              this tenant.
            </Typography>
            {/* The one part of the real page's own flow that works without
                leaving Setup Center — see GoogleChatPermissionStatus.jsx's
                own doc comment for why it's a duplicate, not a shared import. */}
            <Box sx={{ px: 'var(--ds-space-5)', pb: 'var(--ds-space-5)' }}>
              <GoogleChatPermissionStatus />
            </Box>
          </Modal>
        ) : DedicatedAccountModal ? (
          <DedicatedAccountModal
            openModal
            handleClose={(saved?: boolean) => closeIntegrationModal(saved)}
            tool={integrationModal.type}
            editConfig={integrationModal.editData}
          />
        ) : (
          <IntegrationDynamicFormModal
            openModal
            handleClose={(saved?: boolean) => closeIntegrationModal(saved)}
            integrationName={integrationModal.type}
            title={`${integrationModal.editData ? 'Edit' : 'Add'} ${integrationModal.label}`}
            subtitle={getIntegrationDescription(integrationModal.type)}
            integrationData={[]}
            editData={integrationModal.editData}
          />
        ))}

      {/* Same in-place treatment for accounts: these are the exact modals
          /accounts/account-form?cloudProvider=X mounts, reached here directly
          instead of through that page's own listing view. */}
      {accountModal === 'K8S' && <K8sAccountModal openModal handleClose={closeAccountModal} handleOnAccountCreate={refreshReadiness} />}
      {accountModal === 'AWS' && <AddAwsAccountModal open onClose={closeAccountModal} onAccountCreated={refreshReadiness} />}
      {accountModal === 'GCP' && <AddGcpAccountModal open onClose={closeAccountModal} onAccountCreated={refreshReadiness} />}
      {accountModal === 'AZURE' && <AddAzureAccountModal open onClose={closeAccountModal} onAccountCreated={refreshReadiness} />}
      {accountModal === 'SELFHOSTED' && <AddSelfHostedAccountModal open onClose={closeSelfHostedAccountModal} />}
      <K8sAgentInstallDialog open={!!k8sAgentInstallAccount} onClose={() => setK8sAgentInstallAccount(null)} account={k8sAgentInstallAccount} />

      {/* Only shown when Add on Skills has more than one account to choose
          from — a single eligible account skips straight to the create form. */}
      {kbAccountPickerOpen && (
        <Modal open handleClose={() => setKbAccountPickerOpen(false)} title='Which account is this knowledge base for?' width='sm'>
          <Box sx={{ padding: 'var(--ds-space-5)' }}>
            <Typography sx={{ fontSize: 'var(--ds-text-small)', color: 'var(--ds-gray-600)', mb: 'var(--ds-space-4)' }}>
              A knowledge base belongs to one account. Choose which one, then add it.
            </Typography>
            <Box data-testid='setup-kb-account-picker'>
              <Select
                id='setup-kb-account-picker'
                size='sm'
                placeholder='Select an account'
                value={null}
                options={eligibleKbAccounts.map((a) => ({ value: a.id, label: a.account_name || a.id }))}
                onChange={handlePickKbAccount}
              />
            </Box>
          </Box>
        </Modal>
      )}

      {kbForm.open && (
        <KnowledgeBaseFormModal
          open
          onClose={kbForm.close}
          onSubmit={kbForm.handleSubmit}
          editKnowledgeBase={kbForm.editKnowledgeBase}
          loading={kbForm.submitting}
          agents={kbForm.agents}
          agentsLoading={kbForm.agentsLoading}
          tools={kbForm.tools}
          toolsLoading={kbForm.toolsLoading}
          contextTagOptions={kbForm.contextTagOptions}
          contextTagsLoading={kbForm.contextTagsLoading}
          initialAgentIds={kbForm.formAgentIds}
        />
      )}
    </Box>
  );
};

export default SetupCenter;
