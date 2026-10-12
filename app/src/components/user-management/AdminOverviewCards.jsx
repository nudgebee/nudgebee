import { useEffect, useState } from 'react';
import { useRouter } from 'next/router';
import { Box, Typography } from '@mui/material';
import ChevronRightRoundedIcon from '@mui/icons-material/ChevronRightRounded';
import { Card } from '@ui/Card';
import { Chip } from '@ui/Chip';
import SafeIcon from '@shared/icons/SafeIcon';
import ErrorBoundary from '@shared/ErrorBoundary';
import { User1, NotificationIcon1, IntegrationsIcon, AgentIcon, SettingsIcon } from '@assets';
import apiNotifications from '@api1/notification';
import { getAdminOverviewCounts } from '@api1/admin-overview';
import { isOSSDeploymentMode } from '@hooks/useBCortexEnabled';

// The "Overview" sub-view of the admin landing tab — one card per existing admin
// tab. Each card carries a short description, an at-a-glance count line, and
// quick-action links that deep-link into that tab (or a specific sub-tab).
//
// Each count deep-links (via its stat's `target`) into the tab it summarises and
// counts only active/enabled rows (see getAdminOverviewCounts). Every one is
// fetched independently and fail-soft, so a slow or failing source degrades to a
// (non-clickable) dash rather than blocking the others.
//
// Each quick-action link either navigates (hash, optionally + an Integrations
// `?integration=` slug) or opens a modal / selects an inner tab at the
// destination via an `action` slug (see useAdminAction + the destination bodies
// that consume it). Counts with no cheap tenant-wide source are omitted on
// purpose (agents/tools are account-scoped; models/label-maps have no count).

// Each fetcher declares the count keys it owns (so an error can null exactly
// those) and returns a { key: value } patch merged into the counts map. A value
// is one of three states, per key:
//   undefined → still loading  (render a skeleton)
//   null      → load failed / unavailable
//   number    → the real count (0 is a valid count, preserved)
//
// Nine of the ten counts come from ONE batched GraphQL document
// (getAdminOverviewCounts) — the gateway fans its root fields out in parallel
// and merges by alias, so it is a single HTTP call, not nine. `channels` stays
// separate because its accurate value needs getInstalledTools' messaging-
// platform + integration merge, which a raw aggregate can't reproduce.
const FETCHERS = [
  {
    keys: ['users', 'groups', 'rules', 'accounts', 'providers', 'mcps', 'connected', 'flags', 'roles'],
    run: async () => await getAdminOverviewCounts(),
  },
  { keys: ['channels'], run: async () => ({ channels: (await apiNotifications.getInstalledTools())?.messaging_platforms?.length ?? null }) },
];

// A quick-action link deep-links into a tab (`hash`) or, for the Integrations
// category tabs, a hash plus a `?integration=<slug>` query param. Slugs are the
// stable ones from integration.jsx's INTEGRATION_SLUG_BY_ID.
const CARDS = [
  {
    fragment: 'access-users',
    title: 'Access & Users',
    icon: User1,
    description: 'Invite people, group them, and control what each can see and change.',
    // `omitOnNull` on Roles: it is legitimately absent on a tenant without the
    // custom-roles feature (listCustomRoles 403/400s), so a dash would read as
    // an error — drop the stat instead.
    stats: [
      { key: 'users', one: 'user', many: 'users', target: { hash: 'access-users' } },
      { key: 'groups', one: 'group', many: 'groups', target: { hash: 'access-users/groups' } },
      { key: 'roles', one: 'role', many: 'roles', omitOnNull: true, target: { hash: 'access-users/roles' } },
    ],
    links: [
      { label: 'Invite new users', hash: 'access-users', action: 'invite-user' },
      { label: 'Create a group', hash: 'access-users/groups', action: 'create-group' },
      // eeOnly links go to Enterprise screens (custom roles, b-Cortex) that OSS does not have.
      { label: 'Configure roles and permissions', hash: 'access-users/roles', eeOnly: true },
      { label: 'Assign resource ownership', hash: 'access-users/ownership' },
      { label: 'Review the audit log', hash: 'access-users/audit-log' },
    ],
  },
  {
    fragment: 'integrations',
    title: 'Integrations',
    icon: IntegrationsIcon,
    description: 'Connect clouds, clusters, observability tools, ticketing and messaging.',
    stats: [
      { key: 'connected', one: 'connected', many: 'connected', target: { hash: 'integrations' } },
      { key: 'accounts', one: 'account', many: 'accounts', target: { hash: 'integrations', query: { integration: 'account' } } },
    ],
    links: [
      { label: 'Connect a cloud account', hash: 'integrations', query: { integration: 'account' } },
      { label: 'Add Kubernetes clusters', hash: 'integrations', query: { integration: 'cloud' } },
      { label: 'Add logs, metrics and traces', hash: 'integrations', query: { integration: 'observability' } },
      { label: 'Connect messaging and ticketing', hash: 'integrations', query: { integration: 'alert' } },
      { label: 'Browse all providers', hash: 'integrations' },
    ],
  },
  {
    fragment: 'notification-rules',
    title: 'Notification Rules',
    icon: NotificationIcon1,
    description: 'Decide which events reach your team, for which clusters, and on which channel.',
    stats: [
      { key: 'rules', one: 'rule', many: 'rules', target: { hash: 'notification-rules' } },
      { key: 'channels', one: 'channel', many: 'channels', target: { hash: 'integrations', query: { integration: 'alert' } } },
    ],
    // The four alert links open the Create Rule modal preset to that source
    // type; channels have no modal in this tab, so that one navigates to the
    // messaging integrations tile.
    links: [
      { label: 'Route troubleshooting alerts to a channel', hash: 'notification-rules', action: 'alert-troubleshoot' },
      { label: 'Route optimization alerts to a channel', hash: 'notification-rules', action: 'alert-optimize' },
      { label: 'Route SLO alerts to a channel', hash: 'notification-rules', action: 'alert-slo' },
      { label: 'Schedule a weekly digest', hash: 'notification-rules', action: 'alert-weekly_digest' },
      { label: 'Manage notification channels', hash: 'integrations', query: { integration: 'alert' } },
    ],
  },
  {
    fragment: 'ai-tools',
    title: 'AI & Tools',
    icon: AgentIcon,
    description: 'Choose the models, tools and agents that power the assistant.',
    stats: [
      { key: 'providers', one: 'provider', many: 'providers', target: { hash: 'ai-tools/providers' } },
      { key: 'mcps', one: 'MCP', many: 'MCPs', target: { hash: 'ai-tools/tools-mcp', action: 'tools-mcp-servers' } },
    ],
    // Most links navigate to the relevant AI & Tools sub-tab, but two point
    // elsewhere because the admin sub-tab is a read-only mirror, not where the
    // work is done: "Add providers and models" opens the LLM integrations form
    // (Providers tab is read-only — Manage in Integrations), and "Create skills"
    // deep-links the b-Cortex Knowledge Base (`?bcortex=knowledge-base`), where
    // skills actually live (Functions tab is the read-only oversight mirror).
    // "Create a new agent" navigates rather than opening its modal on purpose:
    // agent creation needs an account selected first (the Agents admin tab is
    // tenant-wide by default, where Create is hidden), which the overview has no
    // way to preset.
    links: [
      { label: 'Add providers and models', path: '/accounts/account-form?cloudProvider=llm' },
      { label: 'Add tools and MCPs', hash: 'ai-tools/tools-mcp' },
      { label: 'Create a new agent', hash: 'ai-tools' },
      { label: 'Create skills', query: { bcortex: 'knowledge-base' }, eeOnly: true },
      { label: 'Set budgets and limits', hash: 'ai-tools/budgets-limits' },
    ],
  },
  {
    fragment: 'tenant-settings',
    title: 'Tenant Settings',
    icon: SettingsIcon,
    description: "Your tenant's identity, self-onboarding, label mapping and feature flags.",
    stats: [{ key: 'flags', one: 'feature on', many: 'features on', target: { hash: 'tenant-settings', action: 'settings-features' } }],
    links: [
      { label: 'Edit tenant identity', hash: 'tenant-settings', action: 'settings-general' },
      { label: 'Set up self-onboarding', hash: 'tenant-settings', action: 'settings-general' },
      { label: 'Map labels for logs and alerts', hash: 'tenant-settings', action: 'settings-labels' },
      { label: 'Turn features on or off', hash: 'tenant-settings', action: 'settings-features' },
      { label: 'View tenant details', hash: 'tenant-settings' },
    ],
  },
];

const OverviewCards = () => {
  const router = useRouter();
  const [counts, setCounts] = useState({});

  useEffect(() => {
    let cancelled = false;
    // Independent, parallel, fail-soft: each fetcher settles on its own and
    // writes only its own keys, so one slow/failed source never holds up or
    // blanks the others. On error its keys go to null (→ dash), not stuck-loading.
    FETCHERS.forEach(({ keys, run }) => {
      run()
        .then((patch) => {
          if (!cancelled) setCounts((prev) => ({ ...prev, ...patch }));
        })
        .catch(() => {
          if (!cancelled) setCounts((prev) => ({ ...prev, ...Object.fromEntries(keys.map((k) => [k, null])) }));
        });
    });
    return () => {
      cancelled = true;
    };
  }, []);

  // Navigate to a tab (hash), optionally carrying an Integrations `?integration=`
  // slug and/or an `?action=` slug the destination body reads to open a modal or
  // select an inner tab (see useAdminAction). A `path` link instead routes to an
  // absolute route (used where the admin sub-tab is a read-only mirror and the
  // real add flow lives elsewhere, e.g. LLM providers → the integrations form).
  const go = (link) => {
    if (link.path) {
      router.push(link.path);
      return;
    }
    const query = { ...(link.query || {}), ...(link.action ? { action: link.action } : {}) };
    if (Object.keys(query).length > 0) {
      router.push({ pathname: '/user-management', query, hash: link.hash });
    } else {
      router.push(`/user-management#${link.hash}`);
    }
  };

  return (
    <Box
      sx={{
        display: 'grid',
        gridTemplateColumns: { xs: '1fr', md: '1fr 1fr' },
        gap: 'var(--ds-space-4)',
        alignItems: 'start',
      }}
    >
      {CARDS.map((card) => {
        const visibleStats = card.stats.filter((s) => !(s.omitOnNull && counts[s.key] === null));
        const visibleLinks = card.links.filter((l) => !(l.eeOnly && isOSSDeploymentMode()));
        return (
          <ErrorBoundary key={card.fragment}>
            <Card id={`admin-overview-card-${card.fragment}`} data-testid={`admin-overview-card-${card.fragment}`}>
              <Box sx={{ display: 'flex', flexDirection: 'column', gap: 'var(--ds-space-2)' }}>
                {/* Header — clickable region that opens the tab. A button, not the
                    whole Card, so the quick-action links below aren't nested
                    inside another button (invalid + breaks keyboard nav). */}
                <Box
                  role='button'
                  tabIndex={0}
                  onClick={() => go({ hash: card.fragment })}
                  onKeyDown={(e) => {
                    if (e.key === 'Enter' || e.key === ' ') {
                      e.preventDefault();
                      go({ hash: card.fragment });
                    }
                  }}
                  id={`admin-overview-open-${card.fragment}`}
                  aria-label={`Open ${card.title}`}
                  sx={{
                    display: 'flex',
                    alignItems: 'center',
                    gap: 'var(--ds-space-2)',
                    cursor: 'pointer',
                    borderRadius: 'var(--ds-radius-md)',
                    outline: 'none',
                    '&:hover .admin-overview-title, &:focus-visible .admin-overview-title': { textDecoration: 'underline' },
                    '&:focus-visible': { outline: '2px solid var(--ds-blue-500)', outlineOffset: 2 },
                  }}
                >
                  {/* The five card icons come from two asset families: three are
                      inline SVGR line-icons with a blue (#3B82F6) stroke/fill baked
                      in, two are <img>-rendered and already dark. Normalise the
                      inline ones to the card's foreground ink so all five read as
                      one set. Only inline SVG exposes [stroke]/[fill] to CSS (which
                      overrides the baked presentation attribute); the <img> icons
                      are unaffected and already match, and fill="none" backgrounds
                      are preserved by the :not() guard. */}
                  <Box
                    sx={{
                      display: 'inline-flex',
                      flexShrink: 0,
                      '& [stroke]': { stroke: 'var(--ds-foreground)' },
                      '& [fill]:not([fill="none"])': { fill: 'var(--ds-foreground)' },
                    }}
                  >
                    <SafeIcon src={card.icon} height={18} width={18} alt='' />
                  </Box>
                  <Typography
                    className='admin-overview-title'
                    sx={{
                      flex: 1,
                      fontFamily: 'var(--ds-font-display)',
                      fontSize: 'var(--ds-text-body-lg)',
                      fontWeight: 'var(--ds-font-weight-semibold)',
                      color: 'var(--ds-foreground)',
                    }}
                  >
                    {card.title}
                  </Typography>
                  <ChevronRightRoundedIcon sx={{ fontSize: 18, color: 'var(--ds-gray-400)' }} />
                </Box>

                <Typography sx={{ fontSize: 'var(--ds-text-small)', color: 'var(--ds-gray-600)', lineHeight: 1.5 }}>{card.description}</Typography>

                {visibleStats.length > 0 && (
                  <Box sx={{ display: 'flex', alignItems: 'center', flexWrap: 'wrap', gap: 'var(--ds-space-2)' }}>
                    {visibleStats.map((s) => {
                      const value = counts[s.key];
                      const testId = `admin-overview-stat-${card.fragment}-${s.key}`;
                      // Loading → subtle chip with spinner; failed → subtle chip with a dash;
                      // a real count → filled blue (info) chip, clickable to its tab. Blue reads
                      // as "here's a number you can open"; the grey states have nothing to open.
                      // variant='count' is explicit so the chip keeps the info fill: an onClick
                      // alone derives variant 'action', which renders transparent (outline only).
                      if (value === undefined) {
                        return (
                          <Chip key={s.key} size='md' tone='subtle' loading>
                            {s.many}
                          </Chip>
                        );
                      }
                      if (value === null) {
                        return (
                          <Chip key={s.key} size='md' tone='subtle'>
                            —
                          </Chip>
                        );
                      }
                      return (
                        <Chip
                          key={s.key}
                          variant='count'
                          size='md'
                          tone='info'
                          onClick={s.target ? () => go(s.target) : undefined}
                          id={testId}
                          data-testid={testId}
                        >
                          <Box component='span' sx={{ fontWeight: 'var(--ds-font-weight-semibold)' }}>
                            {value}
                          </Box>{' '}
                          {value === 1 ? s.one : s.many}
                        </Chip>
                      );
                    })}
                  </Box>
                )}

                {visibleLinks.length > 0 && (
                  <Box sx={{ display: 'flex', flexDirection: 'column', mt: 'var(--ds-space-1)', mx: 'calc(-1 * var(--ds-space-2))' }}>
                    {visibleLinks.map((link) => (
                      <Box
                        key={link.label}
                        role='button'
                        tabIndex={0}
                        onClick={() => go(link)}
                        onKeyDown={(e) => {
                          if (e.key === 'Enter' || e.key === ' ') {
                            e.preventDefault();
                            go(link);
                          }
                        }}
                        id={`admin-overview-link-${card.fragment}-${link.label
                          .toLowerCase()
                          .replace(/[^a-z0-9]+/g, '-')
                          .replace(/^-|-$/g, '')}`}
                        sx={{
                          display: 'flex',
                          alignItems: 'center',
                          justifyContent: 'space-between',
                          gap: 'var(--ds-space-2)',
                          px: 'var(--ds-space-2)',
                          py: 'var(--ds-space-1)',
                          borderRadius: 'var(--ds-radius-md)',
                          cursor: 'pointer',
                          outline: 'none',
                          transition: 'background-color 120ms ease',
                          '&:hover, &:focus-visible': { backgroundColor: 'var(--ds-background-200)' },
                          '&:focus-visible': { outline: '2px solid var(--ds-blue-500)', outlineOffset: -2 },
                          '&:hover .admin-overview-chevron, &:focus-visible .admin-overview-chevron': { transform: 'translateX(2px)' },
                        }}
                      >
                        <Typography sx={{ fontSize: 'var(--ds-text-body)', color: 'var(--ds-gray-700)' }}>{link.label}</Typography>
                        <ChevronRightRoundedIcon
                          className='admin-overview-chevron'
                          sx={{ fontSize: 16, color: 'var(--ds-gray-400)', transition: 'transform 120ms ease' }}
                        />
                      </Box>
                    ))}
                  </Box>
                )}
              </Box>
            </Card>
          </ErrorBoundary>
        );
      })}
    </Box>
  );
};

export default OverviewCards;
