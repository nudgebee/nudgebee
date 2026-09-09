import { snakeToTitleCase } from '@utils/common';

/**
 * One cloud security posture check, rolled up across the accounts in view.
 *
 * The listing query returns a row per (rule, severity, account); the cross-account
 * view wants a row per rule, so the fold happens here rather than in the query —
 * it keeps the per-account counts, which the panel shows as the check's spread.
 */
export interface CloudPostureRule {
  ruleName: string;
  /** Worst severity seen for the rule across accounts. */
  severity: string;
  count: number;
  accountIds: string[];
  countByAccount: Record<string, number>;
  /** AWS / Azure / GCP, inferred from the accounts carrying it. */
  provider?: string;
}

const SEVERITY_RANK: Record<string, number> = { Critical: 5, High: 4, Medium: 3, Low: 2, Info: 1, Unknown: 0 };

const rank = (s: string) => SEVERITY_RANK[s] ?? 0;

/**
 * A readable name for a check.
 *
 * The rule name is the last resort: a fifth of the cloud vocabulary is
 * `azure_defender_assessment_<uuid>`, which snake-cases into noise. The catalog
 * title is preferred wherever one exists.
 */
export const cloudRuleTitle = (ruleName: string, catalog?: any): string => {
  if (catalog?.title) return catalog.title;
  // A UUID tail carries no meaning for a reader — name the family instead.
  const uuidTail = /^(.*?)[_-][0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i.exec(ruleName);
  if (uuidTail) return `${snakeToTitleCase(uuidTail[1])} assessment`;
  return snakeToTitleCase(ruleName);
};

/**
 * Fold the per-(rule, severity, account) grouping rows into one row per rule.
 * `providerOf` maps an account id to its cloud provider; a rule spanning
 * providers reports none rather than an arbitrary one.
 */
export const foldCloudPostureRules = (rows: any[], providerOf: (accountId: string) => string | undefined): CloudPostureRule[] => {
  const byRule = new Map<string, CloudPostureRule>();

  for (const row of rows || []) {
    const ruleName = row?.rule_name;
    if (!ruleName) continue;
    const accountId = row?.account_id;
    const count = Number(row?.count) || 0;
    const severity = row?.severity || 'Unknown';

    const existing = byRule.get(ruleName);
    if (!existing) {
      byRule.set(ruleName, {
        ruleName,
        severity,
        count,
        accountIds: accountId ? [accountId] : [],
        countByAccount: accountId ? { [accountId]: count } : {},
      });
      continue;
    }
    existing.count += count;
    if (rank(severity) > rank(existing.severity)) existing.severity = severity;
    if (accountId) {
      existing.countByAccount[accountId] = (existing.countByAccount[accountId] || 0) + count;
      if (!existing.accountIds.includes(accountId)) existing.accountIds.push(accountId);
    }
  }

  const rules = Array.from(byRule.values());
  for (const rule of rules) {
    const providers = new Set(rule.accountIds.map(providerOf).filter(Boolean) as string[]);
    rule.provider = providers.size === 1 ? [...providers][0] : undefined;
  }

  // Worst first, then widest blast radius — the order a reader triages in.
  return rules.sort((a, b) => rank(b.severity) - rank(a.severity) || b.count - a.count);
};
