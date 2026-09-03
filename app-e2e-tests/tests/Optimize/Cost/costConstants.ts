// Not for OSS

// Contract constants for the Cost tab of the tenant-level Optimize module
// (/optimise#cost). Every value is read from app/src rather than guessed, and is
// named here so an app-side rename fails one obvious line instead of eight
// locators spread across the spec.

// WIDGET_CATEGORIES in OptimizeNewPage.tsx, paired with the label
// WIDGET_CATEGORY_LABELS gives it and the testid the card renders — the category
// lowercased. Configuration is absent on purpose: it has its own tab, so this
// strip never offers a card for it.
export const CATEGORY_CARDS = [
  { category: "RightSizing", label: "Right Sizing", testId: "optimize-card-rightsizing" },
  { category: "InfraUpgrade", label: "Infra Upgrade", testId: "optimize-card-infraupgrade" },
  { category: "K8sSpotRecommendation", label: "Spot Instance", testId: "optimize-card-k8sspotrecommendation" },
] as const;

export type CategoryCard = (typeof CATEGORY_CARDS)[number];

// Column contract from TABLE_HEADERS in OptimizeNewPage.tsx. The eighth header is
// the row-actions column and is deliberately unnamed, so it is not listed here.
export const COST_HEADERS = ["Severity", "Resource", "Recommendation", "Category", "Safety", "Savings", "Last Seen"] as const;

// SORT_PRESETS / SORT_ORDER in OptimizeNewPage.tsx. `value` is the suffix of the
// menu item's id (`optimize-sort-${value}`); `label` is what the trigger shows
// once the preset is active.
export const SORT_SEVERE = { value: "severe", label: "Most severe" } as const;
export const SORT_HIGHEST_SAVINGS = { value: "savings", label: "Highest savings" } as const;

// One SAVINGS_BUCKETS entry (optimise-new/utils.ts). Chosen as the bucket under
// test because its key reaches the URL verbatim and its label is not a substring
// of any neighbouring option's, so the option match cannot land on the wrong row.
export const SAVINGS_BUCKET = { key: "gte10", label: "≥ $10 /mo" } as const;

// CustomTablePagination.jsx:69-76 — "Showing 1-10 of 1,234 results". Captures the range
// end and the total, so "is there another page" can be read off the settled footer
// instead of off a pagination button whose absence a non-waiting count() cannot tell
// apart from a component that has not rendered yet.
export const PAGINATION_SUMMARY_PATTERN = /Showing\s+[\d,]+-([\d,]+)\s+of\s+([\d,]+)\s+results/;

// Zero-based index of the Resource column in TABLE_HEADERS. The trailing actions
// cell stops propagation, so this is also the cell that carries a row click
// through to the detail panel.
export const RESOURCE_COLUMN = 1;

// A rule name no scan can have produced, so "the panel found nothing" is about the
// option search working rather than about what the shared dev tenant holds.
// Suffixed per run because the same string is typed into a live filter panel.
export function noMatchRule(): string {
  return `zz-no-such-rule-${Date.now().toString(36)}-${Math.random().toString(36).slice(2, 7)}`;
}
