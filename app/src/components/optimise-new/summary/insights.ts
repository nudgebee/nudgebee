// ─── Types ─────────────────────────────────────────────────────────────────

export type MainCategory = 'cost' | 'performance' | 'security_config';

export type CostSubCategory = 'anomalies' | 'rightsizing' | 'abandoned' | 'savings';
export type PerfSubCategory = 'throttling' | 'saturation' | 'latency' | 'utilization';
export type SecConfigSubCategory = 'security_vulnerability' | 'critical_config' | 'compliance' | 'drift';

export type SubCategory = CostSubCategory | PerfSubCategory | SecConfigSubCategory;

export type Provider = 'aws' | 'azure' | 'gcp' | 'k8s';
// Mirrors the cloud_accounts.account_env column, which is binary — an account is
// either production or it isn't. There is no staging/dev/sandbox tier in the data.
export type Environment = 'prod' | 'non_prod';

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
  /** Concise rule/catalog title used as the card & list headline (e.g. "Pod Right Sizing"). */
  title: string;
  /** Terse action/context line under the title (e.g. "Reduce CPU 96%"); '' when none. */
  brief: string;
  /** Full prose description — hover tooltip + ticket subject only, no longer the headline. */
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
const ENV_RANK: Record<Environment, number> = { prod: 0, non_prod: 1 };

export const sortInsights = (items: InsightItem[], sortBy: SortKey = 'savings'): InsightItem[] => {
  return [...items].sort((a, b) => {
    switch (sortBy) {
      case 'savings':
        return (
          b.dollarImpact - a.dollarImpact ||
          (SEV_RANK[a.severity] ?? 4) - (SEV_RANK[b.severity] ?? 4) ||
          (ENV_RANK[a.env] ?? 1) - (ENV_RANK[b.env] ?? 1)
        );
      case 'age':
        return b.ageDays - a.ageDays || b.dollarImpact - a.dollarImpact;
      case 'confidence':
        return b.confidence - a.confidence || b.dollarImpact - a.dollarImpact;
      case 'resource':
        return a.resourceId.localeCompare(b.resourceId);
      default:
        return (SEV_RANK[a.severity] ?? 4) - (SEV_RANK[b.severity] ?? 4) || (ENV_RANK[a.env] ?? 1) - (ENV_RANK[b.env] ?? 1);
    }
  });
};

// ─── Subtotal helpers ──────────────────────────────────────────────────────

export const subtotal = (items: InsightItem[]): number => items.reduce((s, i) => s + i.dollarImpact, 0);

// `symbol` lets callers render the tenant/account billing currency (estimated_savings
// is denominated in the account currency, not always USD). Defaults to '$'.
export const formatDollars = (n: number, symbol = '$'): string => {
  if (n >= 1000) return symbol + (n / 1000).toFixed(1).replace(/\.0$/, '') + 'K';
  // Cap at cents: bare toLocaleString defaults to 3 fraction digits, which
  // rendered a filtered total of 203.5432 as "$203.543".
  return symbol + n.toLocaleString('en-US', { maximumFractionDigits: 2 });
};

// Whole-currency formatting for headline-weight figures ("$18,420") — unabbreviated,
// unlike `formatDollars` above. Mirrors what CostCallout does internally.
export const formatWholeCurrency = (value: number, currency: string): string => {
  try {
    return new Intl.NumberFormat('en-US', { style: 'currency', currency, maximumFractionDigits: 0 }).format(value);
  } catch {
    return `$${Math.round(value).toLocaleString('en-US')}`;
  }
};

// Secondary line for cards/list rows: show the terse `brief` under the title only
// when it adds information beyond the title. Drops it when it duplicates the title,
// and hard-clamps the rare long one so a row stays a single scannable line rather
// than wrapping a full sentence (the original "titles too big" complaint).
const SECONDARY_LINE_MAX = 80;
export const secondaryLine = (item: InsightItem): string => {
  const b = (item.brief || '').trim();
  if (!b || b === item.title) return '';
  return b.length > SECONDARY_LINE_MAX ? b.slice(0, SECONDARY_LINE_MAX - 1).trimEnd() + '…' : b;
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

// ─── Ranked queue: severity × savings × confidence × recency ───────────────

// Severity multiplier. Without it the score is savings-only for monetary findings
// and a flat baseline for risk findings, which ranked a critical public bucket
// below a medium right-sizing — visibly wrong in a list headed "Do this first".
const SEV_WEIGHT: Record<string, number> = { critical: 3, high: 2, medium: 1.2, low: 0.8, info: 0.5 };

export const getTopRanked = (items: InsightItem[], count = 3): InsightItem[] => {
  const scored = items.map((item) => {
    const recencyWeight = Math.max(0.1, 1 - item.ageDays / 180);
    const confWeight = item.confidence / 100;
    const dollarWeight = item.dollarImpact > 0 ? item.dollarImpact : 500; // risk items get baseline weight
    const sevWeight = SEV_WEIGHT[(item.severity || '').toLowerCase()] ?? 1;
    const score = dollarWeight * confWeight * recencyWeight * sevWeight;
    return { item, score };
  });
  scored.sort((a, b) => b.score - a.score);

  // A pure severity-weighted score lets a wave of $0-impact critical findings
  // (e.g. CPU/Mem right-sizing with no cost estimate) fill every slot — visible
  // on live data as "Top N impact" reading $0/mo. Reserve
  // up to half the queue for genuine dollar savings so both signals surface,
  // instead of the highest-scoring severity picks silently squeezing them all out.
  const impactQuota = Math.ceil(count / 2);
  const topBySavings = [...items]
    .filter((i) => i.dollarImpact > 0)
    .sort((a, b) => b.dollarImpact - a.dollarImpact)
    .slice(0, impactQuota);

  const picked = new Map(topBySavings.map((item) => [item.id, item]));
  for (const { item } of scored) {
    if (picked.size >= count) break;
    if (!picked.has(item.id)) picked.set(item.id, item);
  }

  // Re-order the final set by the same composite score so it reads as one
  // ranked list — "savings picks, then severity picks" stitched together would
  // read as two lists instead of one priority order.
  const scoreById = new Map(scored.map((s) => [s.item.id, s.score]));
  return [...picked.values()].sort((a, b) => (scoreById.get(b.id) ?? 0) - (scoreById.get(a.id) ?? 0));
};

// ─── Sub-category one-liner generator ──────────────────────────────────────

export const subCategorySummaryLine = (items: InsightItem[], symbol = '$'): string => {
  if (items.length === 0) return '';
  const criticals = items.filter((i) => i.severity === 'critical');
  const dollars = subtotal(items);
  const topItem = items[0]; // already sorted by priority

  if (criticals.length > 0 && dollars > 0) {
    return `${criticals.length} critical, ${formatDollars(dollars, symbol)}/mo at stake — top hit: ${topItem.resourceId}`;
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

// Which words in the (still fully static) briefing sentence get bold + colour.
// The copy itself stays a fixed template — only `criticals`/`dollars` interpolate
// — this just lets the two numbers that matter stand out in the sentence.
export type BriefingEmphasis = 'money' | 'alert';

export interface BriefingSegment {
  text: string;
  emphasis?: BriefingEmphasis;
}

// `totalDollars` is the canonical full-set savings total (the headline number); pass
// it so the briefing agrees with the headline instead of summing only the shown rows.
// `totalCount` is the true count of in-scope recommendations across the whole
// tenant — `items` itself is already a curated subset (top-by-urgency ∪
// top-by-impact), so once a tenant has more findings than that curation pulls,
// `items.length` alone understates what's actually out there. Appended as a
// trailing clause — critical/savings info leads, the "out of N total" context
// follows — only when it's actually informative, i.e. curation is discarding
// something.
export const generateNubiBriefing = (items: InsightItem[], totalDollars?: number, symbol = '$', totalCount?: number): BriefingSegment[] => {
  const criticals = items.filter((i) => i.severity === 'critical').length;
  const dollars = totalDollars ?? subtotal(items);
  const suffix: BriefingSegment[] =
    totalCount != null && totalCount > items.length ? [{ text: ` Selected from ${totalCount.toLocaleString()} total recommendations.` }] : [];

  if (criticals > 0 && dollars > 0) {
    return [
      { text: `${criticals} critical issue${criticals > 1 ? 's' : ''}`, emphasis: 'alert' },
      { text: ' and ' },
      { text: `${formatDollars(dollars, symbol)}/mo`, emphasis: 'money' },
      { text: ' in savings potential need your attention this week.' },
      ...suffix,
    ];
  }
  if (dollars > 0) {
    return [
      { text: `${formatDollars(dollars, symbol)}/mo`, emphasis: 'money' },
      { text: ` in optimization opportunities across ${items.length} findings. ` },
      { text: `${criticals}`, emphasis: 'alert' },
      { text: ' are critical severity.' },
      ...suffix,
    ];
  }
  return [
    { text: `${items.length} findings` },
    { text: ' flagged this week, ' },
    { text: `${criticals} critical`, emphasis: 'alert' },
    { text: '. Start with the top 3 below.' },
    ...suffix,
  ];
};

// ─── Overflow count for collapsed groups ───────────────────────────────────

export const overflowSummary = (allItems: InsightItem[], shownCount: number): { count: number; dollars: number } => {
  const overflow = allItems.slice(shownCount);
  return { count: overflow.length, dollars: subtotal(overflow) };
};
