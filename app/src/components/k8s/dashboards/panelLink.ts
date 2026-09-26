/**
 * Query parameter naming the panel a dashboard link points at. A parameter rather than a hash segment
 * for the same reason the dashboard's own is: the page is hash-routed, and `#dashboards` selects the tab.
 */
export const PANEL_PARAM = 'panel';

/** `href` with its panel parameter set to `panelId` — or removed, for null — keeping everything else. */
export function withPanelParam(href: string, panelId: string | null): string {
  const url = new URL(href);
  if (panelId) url.searchParams.set(PANEL_PARAM, panelId);
  else url.searchParams.delete(PANEL_PARAM);
  return url.toString();
}
