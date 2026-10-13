import { safeJSONParse } from 'src/utils/common';

// MCP integration health (#40994) as the MCP Servers list shows it.
//
// integrations.health is written by llm-server: scope → verdict, where the scope
// is '*' for a direct integration (the relay calls the server itself) and an
// account id for a vm_agent one (reached through that account's agent). NULL, or
// a scope with no status, means no verdict yet.

const STATUS = {
  connected: { label: 'Connected', tone: 'success', rank: 0 },
  none: { label: 'Not checked yet', tone: 'neutral', rank: 1 },
  error: { label: 'Error', tone: 'critical', rank: 2 },
  unreachable: { label: "Can't reach server", tone: 'critical', rank: 2 },
  auth_failed: { label: 'Login rejected', tone: 'critical', rank: 2 },
};

const statusOf = (entry) => (entry?.status && STATUS[entry.status] ? entry.status : 'none');

export function parseMCPHealth(health) {
  if (!health) return {};
  const parsed = typeof health === 'string' ? safeJSONParse(health) : health;
  return parsed && typeof parsed === 'object' ? parsed : {};
}

const connectionMode = (item) => {
  const values = Array.isArray(item?.integration_config_values) ? item.integration_config_values : [];
  return values.find((v) => v?.name === 'connection_mode')?.value || 'direct';
};

/**
 * Summarise an integration's health for one table cell. For a vm_agent
 * integration the worst linked account wins, with "n of m accounts" when the
 * accounts disagree. Accounts no longer linked are ignored.
 *
 * Returns { status, label, tone, reason, toolCount, checkedAt, scopeNote }.
 */
export function summarizeMCPHealth(item) {
  const health = parseMCPHealth(item?.health);
  let entries;
  if (connectionMode(item) === 'vm_agent') {
    const accounts = Array.isArray(item?.integrations_cloud_accounts) ? item.integrations_cloud_accounts : [];
    entries = accounts.map((a) => health[a?.cloud_account_id]);
  } else {
    entries = [health['*']];
  }
  if (entries.length === 0) entries = [undefined];

  let worst = entries[0];
  for (const e of entries) {
    if (STATUS[statusOf(e)].rank > STATUS[statusOf(worst)].rank) worst = e;
  }
  const status = statusOf(worst);
  const same = entries.filter((e) => statusOf(e) === status).length;
  return {
    status,
    label: STATUS[status].label,
    tone: STATUS[status].tone,
    reason: status === 'connected' || status === 'none' ? '' : worst?.reason || '',
    toolCount: typeof worst?.tool_count === 'number' ? worst.tool_count : null,
    checkedAt: worst?.last_checked_at || null,
    scopeNote: same < entries.length ? `${same} of ${entries.length} accounts` : '',
  };
}
