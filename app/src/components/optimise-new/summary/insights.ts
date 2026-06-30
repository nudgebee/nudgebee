// ─── Types ─────────────────────────────────────────────────────────────────

export type MainCategory = 'cost' | 'performance' | 'security_config';

export type CostSubCategory = 'anomalies' | 'rightsizing' | 'abandoned' | 'savings';
export type PerfSubCategory = 'throttling' | 'saturation' | 'latency' | 'utilization';
export type SecConfigSubCategory = 'security_vulnerability' | 'critical_config' | 'compliance' | 'drift';

export type SubCategory = CostSubCategory | PerfSubCategory | SecConfigSubCategory;

export type Provider = 'aws' | 'azure' | 'gcp' | 'k8s';
export type Environment = 'prod' | 'staging' | 'dev' | 'sandbox';

export type SortKey = 'savings' | 'age' | 'confidence' | 'resource';

export interface InsightItem {
  id: string;
  category: MainCategory;
  subCategory: SubCategory;
  severity: string;
  resourceId: string;
  resourceName: string;
  provider: Provider;
  env: Environment;
  region: string;
  /** The headline number/value shown prominently — e.g. "$1,420/mo", "94%", "3" */
  impactValue: string;
  /** Subtext explaining what the value means — e.g. "savings potential", "memory usage", "critical CVEs" */
  impactLabel: string;
  /** Parsed dollar value for sorting/totals — 0 if non-monetary */
  dollarImpact: number;
  summary: string;
  nextStep: { label: string; destructive?: boolean };
  /** Days since detected or time-in-state */
  ageDays: number;
  /** 0–100 confidence score */
  confidence: number;
  /** Owning team — null means unassigned */
  owner: string | null;
  /** Account / cluster this resource belongs to */
  accountId: string;
  accountName: string;
  /** Full API record for the detail panel */
  _raw?: any;
}

// ─── Account summary helpers ───────────────────────────────────────────────

export interface AccountSummary {
  accountId: string;
  accountName: string;
  provider: Provider;
  criticalCount: number;
  highCount: number;
  totalDollarImpact: number;
  /** Top items: up to 2 cost, 2 perf, 2 sec/config — max 5 total */
  topItems: InsightItem[];
  totalItems: number;
  /** Per-category total counts, used by the right-rail summary card. */
  categoryCounts: Record<MainCategory, number>;
}

export const getAccountSummaries = (items: InsightItem[]): AccountSummary[] => {
  const map: Record<string, { items: InsightItem[]; provider: Provider; name: string }> = {};
  for (const item of items) {
    if (!map[item.accountId]) map[item.accountId] = { items: [], provider: item.provider, name: item.accountName };
    map[item.accountId].items.push(item);
  }

  return Object.entries(map)
    .map(([accountId, { items: acctItems, provider, name }]) => {
      const sorted = sortInsights(acctItems);
      const costPicks = sorted.filter((i) => i.category === 'cost').slice(0, 2);
      const perfPicks = sorted.filter((i) => i.category === 'performance').slice(0, 2);
      const secPicks = sorted.filter((i) => i.category === 'security_config').slice(0, 2);
      const topItems = [...costPicks, ...perfPicks, ...secPicks].slice(0, 5);

      const categoryCounts: Record<MainCategory, number> = { cost: 0, performance: 0, security_config: 0 };
      for (const it of acctItems) categoryCounts[it.category]++;

      return {
        accountId,
        accountName: name,
        provider,
        criticalCount: acctItems.filter((i) => i.severity === 'critical').length,
        highCount: acctItems.filter((i) => i.severity === 'high').length,
        totalDollarImpact: subtotal(acctItems),
        topItems,
        totalItems: acctItems.length,
        categoryCounts,
      };
    })
    .sort((a, b) => b.criticalCount - a.criticalCount || b.totalDollarImpact - a.totalDollarImpact);
};

// ─── Sub-category display config ───────────────────────────────────────────

export interface SubCategoryMeta {
  key: SubCategory;
  label: string;
  maxShown: number;
  /** Severity-strip color for cards in this group */
  stripColor: string;
}

export const COST_SUBCATEGORIES: SubCategoryMeta[] = [
  { key: 'anomalies', label: 'Cost Summary & Anomalies', maxShown: 3, stripColor: 'var(--ds-amber-400)' },
  { key: 'rightsizing', label: 'Right Sizing Opportunities', maxShown: 3, stripColor: 'var(--ds-green-500)' },
  { key: 'abandoned', label: 'Unutilized & Abandoned', maxShown: 3, stripColor: 'var(--ds-amber-500)' },
  { key: 'savings', label: 'Savings Plans & Modernization', maxShown: 3, stripColor: 'var(--ds-purple-400)' },
];

export const PERF_SUBCATEGORIES: SubCategoryMeta[] = [
  { key: 'throttling', label: 'CPU & Memory Throttling', maxShown: 5, stripColor: 'var(--ds-red-500)' },
  { key: 'saturation', label: 'Resource Saturation', maxShown: 5, stripColor: 'var(--ds-amber-500)' },
  { key: 'latency', label: 'Latency & Throughput', maxShown: 5, stripColor: 'var(--ds-amber-400)' },
  { key: 'utilization', label: 'Over/Under Provisioned', maxShown: 5, stripColor: 'var(--ds-green-500)' },
];

export const SEC_CONFIG_SUBCATEGORIES: SubCategoryMeta[] = [
  { key: 'security_vulnerability', label: 'Security Vulnerabilities', maxShown: 5, stripColor: 'var(--ds-red-500)' },
  { key: 'critical_config', label: 'Critical Configuration', maxShown: 5, stripColor: 'var(--ds-amber-500)' },
  { key: 'compliance', label: 'Compliance & Encryption', maxShown: 5, stripColor: 'var(--ds-purple-400)' },
  { key: 'drift', label: 'Config Drift & Hygiene', maxShown: 5, stripColor: 'var(--ds-gray-600)' },
];

// ─── Sort / rank helpers ───────────────────────────────────────────────────

const SEV_RANK: Record<string, number> = { critical: 0, high: 1, medium: 2, low: 3, info: 4 };
const ENV_RANK: Record<Environment, number> = { prod: 0, staging: 1, dev: 2, sandbox: 3 };

export const sortInsights = (items: InsightItem[], sortBy: SortKey = 'savings'): InsightItem[] => {
  return [...items].sort((a, b) => {
    switch (sortBy) {
      case 'savings':
        return (
          b.dollarImpact - a.dollarImpact ||
          (SEV_RANK[a.severity] ?? 4) - (SEV_RANK[b.severity] ?? 4) ||
          (ENV_RANK[a.env] ?? 3) - (ENV_RANK[b.env] ?? 3)
        );
      case 'age':
        return b.ageDays - a.ageDays || b.dollarImpact - a.dollarImpact;
      case 'confidence':
        return b.confidence - a.confidence || b.dollarImpact - a.dollarImpact;
      case 'resource':
        return a.resourceId.localeCompare(b.resourceId);
      default:
        return (SEV_RANK[a.severity] ?? 4) - (SEV_RANK[b.severity] ?? 4) || (ENV_RANK[a.env] ?? 3) - (ENV_RANK[b.env] ?? 3);
    }
  });
};

// ─── Subtotal helpers ──────────────────────────────────────────────────────

export const subtotal = (items: InsightItem[]): number => items.reduce((s, i) => s + i.dollarImpact, 0);

export const formatDollars = (n: number): string => {
  if (n >= 1000) return '$' + (n / 1000).toFixed(1).replace(/\.0$/, '') + 'K';
  return '$' + n.toLocaleString('en-US');
};

export const formatAge = (days: number): string => {
  if (days < 1) {
    const hours = days * 24;
    if (hours < 1) {
      const minutes = Math.max(1, Math.floor(hours * 60));
      return `${minutes}m`;
    }
    return `${Math.floor(hours)}h`;
  }
  if (days < 30) return `${Math.floor(days)}d`;
  if (days < 365) return `${Math.floor(days / 30)}mo`;
  return `${Math.floor(days / 365)}y`;
};

// ─── Top-3 weighted score: savings × confidence × recency ──────────────────

export const getTop3 = (items: InsightItem[]): InsightItem[] => {
  const scored = items.map((item) => {
    const recencyWeight = Math.max(0.1, 1 - item.ageDays / 180);
    const confWeight = item.confidence / 100;
    const dollarWeight = item.dollarImpact > 0 ? item.dollarImpact : 500; // risk items get baseline weight
    const score = dollarWeight * confWeight * recencyWeight;
    return { item, score };
  });
  scored.sort((a, b) => b.score - a.score);
  return scored.slice(0, 3).map((s) => s.item);
};

// ─── Sub-category one-liner generator ──────────────────────────────────────

export const subCategorySummaryLine = (items: InsightItem[]): string => {
  if (items.length === 0) return '';
  const criticals = items.filter((i) => i.severity === 'critical');
  const dollars = subtotal(items);
  const topItem = items[0]; // already sorted by priority

  if (criticals.length > 0 && dollars > 0) {
    return `${criticals.length} critical, ${formatDollars(dollars)}/mo at stake — top hit: ${topItem.resourceId}`;
  }
  if (criticals.length > 0) {
    return `${criticals.length} critical — ${topItem.summary.toLowerCase().slice(0, 60)}`;
  }
  // Pure-savings case is now communicated inline next to the title (e.g. "$X/mo · N resources"),
  // so we omit the redundant italic caption here.
  if (dollars > 0) {
    return '';
  }
  return `${items.length} finding${items.length > 1 ? 's' : ''} — ${topItem.summary.toLowerCase().slice(0, 60)}`;
};

// ─── Nubi briefing generator ───────────────────────────────────────────────

export const generateNubiBriefing = (items: InsightItem[]): string => {
  const criticals = items.filter((i) => i.severity === 'critical').length;
  const dollars = subtotal(items);

  if (criticals > 0 && dollars > 0) {
    return `${criticals} critical issues and ${formatDollars(
      dollars
    )}/mo in savings potential need your attention this week. The biggest risks are in security and cost.`;
  }
  if (dollars > 0) {
    return `${formatDollars(dollars)}/mo in optimization opportunities across ${items.length} findings. ${criticals} are critical severity.`;
  }
  return `${items.length} findings flagged this week, ${criticals} critical. Start with the top 3 below.`;
};

// ─── Overflow count for collapsed groups ───────────────────────────────────

export const overflowSummary = (allItems: InsightItem[], shownCount: number): { count: number; dollars: number } => {
  const overflow = allItems.slice(shownCount);
  return { count: overflow.length, dollars: subtotal(overflow) };
};
