// Not for OSS
import { Page, Locator, expect } from "@playwright/test";
import { LoginPage } from "../../../../pages/LoginPage";
import { TraceGroupLocators } from "./traceGroupLocators";

// The sub-tab id [KubernetesDetails].jsx gives the Trace Group tab, and the URL fragment
// it routes to. The tab is labelled "Trace Group" in the UI even though global search
// calls it "Trace Grouping".
export const TRACE_GROUP_TAB_ID = "trace-grouping";
export const TRACE_GROUP_FRAGMENT = /#monitoring\/grouping/;

// The cluster this suite runs against. ClusterDetailsLocators.openClusterFromConfig()
// reads the same pair and aborts on a mismatch, but it is read here too so a missing key
// fails with the key's name instead of surfacing as a tab that never appeared.
export function requiredCluster(): string {
  const cluster = process.env.CLUSTER_NAME || process.env.CLUSTER || "";
  if (!cluster) {
    throw new Error("CLUSTER_NAME or CLUSTER is not set — add it to .env / .env.dev");
  }
  return cluster;
}

// A resource filter value no real span can carry, so the listing is guaranteed to come
// back empty. Suffixed per call because this runs against a shared dev tenant.
export function unmatchableResource(): string {
  return `zz-no-such-resource-${Date.now()}-${Math.random().toString(36).slice(2, 8)}`;
}

// Opens Infra > K8s the way a person does, then Monitoring > Trace Group.
//
// Deliberately not a bare goto: /kubernetes is only a redirector and which cluster it
// opens depends on the global dropdown, so openClusterFromConfig() drives the same
// redirect and then verifies the dropdown really landed on CLUSTER.
export async function openTraceGroup(page: Page): Promise<TraceGroupLocators> {
  requiredCluster();

  const locators = new TraceGroupLocators(page);
  await new LoginPage(page).doFullLogin();
  await locators.navigateToMonitoringTab();
  await locators.clickTab(TRACE_GROUP_TAB_ID);

  await settleTraceGroup(page, locators);
  return locators;
}

// Waits out the tab's first fetch and converts the two ways this module can be absent
// into named failures instead of a bare "listing never appeared".
//
// The tab is hidden outright when the cluster's cloud_provider is jaeger, and the
// listing is replaced by an "unsupported" empty state when the trace provider reports
// supports_trace_grouping false. Both are environment facts a reader needs by name.
export async function settleTraceGroup(page: Page, locators: TraceGroupLocators): Promise<void> {
  await expect(page).toHaveURL(TRACE_GROUP_FRAGMENT, { timeout: 30000 });

  // Settle on whichever of the two the tab actually rendered BEFORE probing. isVisible()
  // is non-blocking, so asking it straight after the URL transition answers "false" for
  // both — the fetch has not landed — and the unsupported case would then fall through to
  // a generic 60s timeout on the listing instead of the named error below.
  await expect(locators.TraceGroupBox.or(locators.UnsupportedState).first()).toBeVisible({ timeout: 60000 });

  if (await locators.UnsupportedState.isVisible()) {
    throw new Error(
      "Trace Group is unsupported for this account's trace provider — the tab rendered " +
        "#trace-grouping-unsupported instead of the listing (supports_trace_grouping is false)."
    );
  }

  await expect(locators.TraceGroupBox).toBeVisible({ timeout: 60000 });
  // The caption is the app's own end-of-fetch signal: CustomTablePagination renders it
  // from totalRows, so it settles once for rows and once for an empty result alike.
  await expect(locators.ResultSummary).toBeVisible({ timeout: 60000 });
}

// Opens a FilterDropdown panel without tripping its clear control.
//
// ds/FilterDropdown defaults clearable=true and renders a clickable clear X at the
// trigger's right edge whenever a value is selected — always true for Span Type, which
// ships on "http". Playwright clicks an element's centre, and on a compact toolbar
// trigger that can land on the X and fire handleClear instead of opening the panel.
export async function openFilter(page: Page, trigger: Locator): Promise<void> {
  await trigger.waitFor({ state: "visible", timeout: 30000 });
  await trigger.click({ position: { x: 8, y: 8 } });
  // Wait on the PANEL, so a later failure stays distinguishable from a name mismatch.
  await expect(page.locator('[role="option"]').first()).toBeVisible({ timeout: 30000 });
}

// Closes an open FilterDropdown panel and waits for it to actually go.
export async function dismissFilter(page: Page): Promise<void> {
  await page.keyboard.press("Escape");
  await expect(page.locator('[role="option"]')).toHaveCount(0, { timeout: 15000 });
}

// Applies a resource filter the way a user does: type, then Enter.
//
// KubernetesTracesGroupListing only commits `inputResource` to the `resource` state that
// drives the fetch in onEnterPress, so typing alone changes nothing.
export async function searchResource(locators: TraceGroupLocators, value: string): Promise<void> {
  await locators.ResourceSearch.click();
  await locators.ResourceSearch.fill(value);
  await expect(locators.ResourceSearch).toHaveValue(value, { timeout: 15000 });
  await locators.ResourceSearch.press("Enter");
}

// True once the app has issued a TraceGroupingV3 whose order_by names `column`.
//
// The sort caret is the only visual signal and it differs by stroke colour alone, so the
// request is what actually proves the sort took effect.
//
// The clause is matched inside that operation's OWN query string rather than anywhere in
// the raw body: `duration_ns` is also one of the fields TraceGroupingV3 selects, so a
// whole-body substring test would pass on the default error_count sort too. Note the
// order_by is NOT in `variables` — traceGroupV2 calls queryGraphQL with `{}` and
// gqlStringify inlines the request object into the query as
// `order_by:[{column:"duration_ns",order:"desc"}]`.
export function orderedByRequest(page: Page, column: string) {
  return page.waitForRequest(
    (request) => {
      if (!request.url().includes("api/graphql") || request.method() !== "POST") return false;
      const raw = request.postData() ?? "";
      let operations: any[];
      try {
        const parsed = JSON.parse(raw);
        operations = Array.isArray(parsed) ? parsed : [parsed];
      } catch {
        // A body this endpoint never sends. Falling back to the raw text keeps the wait
        // honest rather than silently never matching if the transport shape ever changes.
        return raw.includes("TraceGroupingV3") && raw.includes(`order_by:[{column:\\"${column}\\"`);
      }
      return operations.some(
        (op) => op?.operationName === "TraceGroupingV3" && String(op?.query ?? "").includes(`order_by:[{column:"${column}"`)
      );
    },
    { timeout: 60000 }
  );
}
