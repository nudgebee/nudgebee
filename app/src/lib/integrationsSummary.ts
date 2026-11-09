// Shared "what is connected" logic for the Integrations view, so the admin Overview card's
// connection count and the Integrations page's "Connected N" badge always agree — both count
// the same thing: distinct provider tiles with at least one active account.
//
// `apiAccount.getAllAccount()` merges THREE backend populations into one per-provider map:
//   - all_accounts        — cloud / cluster / VM accounts (accounts table)
//   - messaging_platforms — legacy Slack / Teams / Google Chat / Discord installs
//   - integrations        — the integrations table (ticketing, observability, db, webhook, …)
// A naive integrations_aggregate count sees only the third and misses the other two, which is
// why the Overview card used to under-report. Count through here instead.

// Normalizes a getAllAccount() `.data` payload into { [PROVIDER_KEY]: Account[] }, with each
// account carrying a canonical `status` ('active' | 'disabled'). Mirrors how the Integrations
// page groups accounts into provider tiles.
export const normalizeIntegrationAccounts = (resData: any): Record<string, any[]> => {
  const map: Record<string, any[]> = {};

  const addToMap = (key: string | undefined, item: any) => {
    const k = key?.toUpperCase();
    if (!k) {
      return;
    }
    if (!map[k]) {
      map[k] = [];
    }
    map[k].push(item);
  };

  // 1. Process Cloud Accounts
  resData?.all_accounts?.forEach((acc: any) => {
    addToMap(acc.cloud_provider, { ...acc, status: acc.status || (acc.is_active ? 'active' : 'disabled') });
  });

  // 2. Process Messaging Platforms (Custom parsing logic preservation)
  resData?.messaging_platforms?.forEach((acc: any) => {
    let channels: string[] = [];
    if (acc.platform === 'slack' && acc.channels) {
      channels = [acc.team_name];
    } else if (acc.platform === 'ms_teams' && acc.channels?.team_name) {
      channels = [acc.channels.team_name];
    } else if (acc.platform === 'google_chat' || acc.platform === 'discord') {
      try {
        const parsed = typeof acc.channels === 'string' ? JSON.parse(acc.channels) : acc.channels;
        if (parsed?.name) {
          channels = [parsed.name];
        }
      } catch {
        // Ignore parsing errors
      }
    }

    const key = acc.platform === 'ms_teams' ? 'MSTEAMS' : acc.platform;
    // Messaging platforms in this API usually don't have 'disabled' status in same way, assuming active if exists
    addToMap(key, { ...acc, status: 'active', channels });
  });

  // 3. Process Generic Integrations
  resData?.integrations?.forEach((acc: any) => {
    // Map 'status: enabled' to 'active' for consistency
    const status = acc.status === 'enabled' ? 'active' : 'disabled';

    // google_chat_space bindings count under the Google Chat card; the space is the
    // destination (no channel picker), so seed channels for the messaging badge check.
    if (acc.type === 'google_chat_space') {
      addToMap('GOOGLE_CHAT', { ...acc, status, channels: [acc.name] });
      return;
    }

    // Slack / MS Teams / Discord integration installs supersede legacy messaging_platforms
    // entries for the same card; multiple installs of the same type accumulate.
    if (acc.type === 'slack' || acc.type === 'ms_teams' || acc.type === 'discord') {
      const cardKey = acc.type === 'ms_teams' ? 'MSTEAMS' : acc.type.toUpperCase();
      // Clear legacy messaging_platforms entries (they carry a `platform` field) once,
      // then append so multiple same-type integration installs are preserved.
      if (!map[cardKey] || map[cardKey].some((item: any) => item.platform)) {
        map[cardKey] = [];
      }
      map[cardKey].push({ ...acc, status, channels: [acc.name] });
      return;
    }

    // Special case mapping for OTel/Clickhouse naming if needed
    let key = acc.type;
    if (acc.type === 'postgresql') {
      key = 'POSTGRES';
    }
    if (acc.type === 'otel_clickhouse') {
      key = 'OTEL';
    }

    addToMap(key, { ...acc, status });
  });

  return map;
};

// Count of distinct provider tiles that have at least one active account — the same number the
// Integrations page shows as "Connected N" on its All tab. `resData` is a getAllAccount() `.data`
// payload; returns null when it is missing/an Error, so the card renders a dash rather than 0.
export const countConnectedProviders = (resData: any): number | null => {
  if (!resData || resData instanceof Error) {
    return null;
  }
  const map = normalizeIntegrationAccounts(resData);
  return Object.values(map).filter((accounts) => accounts.some((a: any) => a.status === 'active' || a.is_active)).length;
};
