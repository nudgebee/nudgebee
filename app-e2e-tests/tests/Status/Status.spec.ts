// Not for OSS
import { test, expect } from "@playwright/test";
import { StatusLocators } from "./statusLocators";
import { openStatusPage, fetchStatusPayload, waitForComponentsRendered } from "./statusHelper";
import {
  STATUS_API_PATH,
  STATUS_PAGE_PATH,
  POLL_INTERVAL_MS,
  SUMMARY_LABEL,
  COMPONENT_LABEL,
  DASHBOARD_COMPONENT_ID,
  LEAK_PATTERNS,
  expectedRollup,
} from "./statusConstants";

// The public status page (app/src/pages/status.tsx) and the endpoint behind it
// (app/src/pages/api/public/status.ts).
//
// Read-only by construction: the module has no write surface at all — no form, no
// action, nothing that mutates tenant state — so every test here is safe to run
// concurrently with anything else on the shared dev cluster and safe to run twice.
//
// The dev cluster's real health is not fixed, so nothing below asserts a particular
// status. The assertions are invariants instead: the page agrees with its own API, the
// overall status follows the documented rollup, and no upstream address escapes.
test.describe.configure({ timeout: 180000 });
test.beforeEach(() => {
  test.setTimeout(180000);
});

test(
  "Status sanity - open /status, verify the page renders the Nudgebee Status heading, the overall status bar and the components card",
  { tag: ["@dev", "@sanity", "@functional"] },
  async ({ page }) => {
    const locators = await openStatusPage(page);

    await expect(locators.heading).toBeVisible();
    await expect(locators.summaryBar).toBeVisible();
    await expect(locators.componentsCard).toBeVisible();

    await test.step("The status bar settles on one of the five contract labels", async () => {
      await waitForComponentsRendered(locators);
      const labels = Object.values(SUMMARY_LABEL);
      await expect(locators.summaryBar).toHaveText(new RegExp(labels.join("|")));
    });

    await test.step("The footer states the accuracy ceiling of the checks", async () => {
      await expect(page.getByText(/Checks report whether each service is reachable/)).toBeVisible();
    });
  }
);

test(
  "Status sanity - open /status, verify the page renders bare with no sidebar, header or cluster picker from the app shell",
  { tag: ["@dev", "@sanity", "@functional"] },
  async ({ page }) => {
    const locators = await openStatusPage(page);
    await waitForComponentsRendered(locators);

    // _app.tsx matches '/status' exactly and renders <Component/> outside PageLayout,
    // so none of the chrome may mount. The exact match is the point: a substring check
    // would also strip chrome from a future /kubernetes/status.
    await expect(locators.homeBtn).toHaveCount(0);
    await expect(locators.OptimizeBtn).toHaveCount(0);
    await expect(locators.InfraBtn).toHaveCount(0);
    await expect(locators.clusterPicker).toHaveCount(0);

    // ...and the page itself is still the thing that rendered, so the assertions above
    // cannot pass by the page having failed to load at all.
    await expect(locators.summaryBar).toBeVisible();
  }
);

test(
  "Status - open /status in a signed-out browser context, verify the public status page renders without redirecting to sign-in",
  { tag: ["@dev", "@smoke", "@functional"] },
  async ({ browser, baseURL }) => {
    // baseURL is the only thing carried over. A context built here inherits none of the
    // rest of the config's `use` block, so it still has no storageState and is genuinely
    // anonymous — which is the contract being tested.
    const anonymous = await browser.newContext({ baseURL });
    const page = await anonymous.newPage();

    try {
      const locators = await openStatusPage(page);
      await waitForComponentsRendered(locators);

      await expect(page).toHaveURL(new RegExp(`${STATUS_PAGE_PATH}$`));
      await expect(locators.heading).toBeVisible();
    } finally {
      await anonymous.close();
    }
  }
);

test(
  "Status - open /status, verify the overall status bar reports the same status the public status API returns",
  { tag: ["@dev", "@regression", "@functional"] },
  async ({ page, request }) => {
    const locators = await openStatusPage(page);
    await waitForComponentsRendered(locators);

    const payload = await fetchStatusPayload(request);
    await expect(locators.summaryBar).toContainText(SUMMARY_LABEL[payload.status]);

    await test.step("The bar carries an As-of line rather than staying on Checking", async () => {
      await expect(locators.summaryBar).toContainText(/As of /);
    });
  }
);

test(
  "Status - open /status, verify every component the status API returns is listed with its name and its status label",
  { tag: ["@dev", "@regression", "@functional"] },
  async ({ page, request }) => {
    const locators = await openStatusPage(page);
    await waitForComponentsRendered(locators);

    const payload = await fetchStatusPayload(request);
    expect(payload.components.length, "the status endpoint returned no components").toBeGreaterThan(0);

    for (const component of payload.components) {
      const row = locators.componentRow(component.id);
      await expect(row, `no row rendered for component "${component.id}"`).toBeVisible();
      await expect(locators.componentName(component.id, component.name)).toBeVisible();
      await expect(locators.componentStatusLabel(component.id, COMPONENT_LABEL[component.status])).toBeVisible();
    }

    await test.step("The synthetic dashboard component is present and operational", async () => {
      // The endpoint prepends it because answering at all proves the dashboard serves.
      const dashboard = payload.components.find((c) => c.id === DASHBOARD_COMPONENT_ID);
      expect(dashboard, "the payload carries no dashboard component").toBeTruthy();
      // It is never probed, so it can only read operational — or maintenance, the one
      // state a notice may impose. Degraded, outage and unknown are all unreachable.
      expect(["operational", "maintenance"]).toContain(dashboard?.status);
      await expect(locators.componentRow(DASHBOARD_COMPONENT_ID)).toBeVisible();
    });
  }
);

test(
  "Status - open /status, verify the overall status is the rollup of the listed component statuses rather than a worst-wins fold",
  { tag: ["@dev", "@regression", "@functional"] },
  async ({ page, request }) => {
    const locators = await openStatusPage(page);
    await waitForComponentsRendered(locators);

    const payload = await fetchStatusPayload(request);

    // One component down among healthy ones is `degraded`, not `outage`; only an
    // all-outage fleet is `outage`; `unknown` components never drag the rollup.
    expect(payload.status).toBe(expectedRollup(payload.components));
    await expect(locators.summaryBar).toContainText(SUMMARY_LABEL[expectedRollup(payload.components)]);
  }
);

test(
  "Status - hover a component's status label, verify the explainer tooltip names the status and describes what the check covers",
  { tag: ["@dev", "@regression", "@functional"] },
  async ({ page, request }) => {
    const locators = await openStatusPage(page);
    await waitForComponentsRendered(locators);

    const payload = await fetchStatusPayload(request);
    const component = payload.components[0];
    const label = COMPONENT_LABEL[component.status];

    await locators.componentStatusLabel(component.id, label).hover();

    const tooltip = locators.tooltip;
    await expect(tooltip).toBeVisible();
    await expect(tooltip).toContainText(label);

    await test.step("The body explains the status rather than repeating the label", async () => {
      const body = ((await tooltip.textContent()) ?? "").replace(label, "").trim();
      expect(body.length, "the explainer tooltip rendered its title with no description").toBeGreaterThan(0);
    });
  }
);

test(
  "Status - open /status, verify no component reason exposes an upstream URL, host and port, in-cluster address or IP",
  { tag: ["@dev", "@regression", "@validation"] },
  async ({ request }) => {
    const payload = await fetchStatusPayload(request);

    // The endpoint's stated promise: only a coarse status escapes to an anonymous
    // caller — upstream URLs, versions and raw error text stay server-side. The
    // components array is the whole of what the page renders, so it is the whole of
    // what can leak. checked_at is excluded deliberately: an ISO timestamp trips the
    // host:port shape without being an address.
    const rendered = JSON.stringify(payload.components);

    for (const { name, pattern } of LEAK_PATTERNS) {
      expect(pattern.test(rendered), `the component payload exposes ${name}: ${rendered}`).toBe(false);
    }

    await test.step("Any non-operational component still explains itself", async () => {
      for (const component of payload.components.filter((c) => c.status !== "operational")) {
        expect(component.reason ?? "", `component "${component.id}" is ${component.status} with no reason`).not.toBe("");
      }
    });
  }
);

test(
  "Status - leave /status open past the poll interval, verify it re-requests the status API without a page reload",
  { tag: ["@dev", "@regression", "@functional"] },
  async ({ page }) => {
    let polls = 0;
    // Counted rather than timed: asserting the rendered "As of" clock changed would
    // depend on the endpoint's 10s cache landing on a different wall-clock second.
    page.on("request", (req) => {
      if (req.url().includes(STATUS_API_PATH)) polls += 1;
    });

    const locators = await openStatusPage(page);
    await waitForComponentsRendered(locators);

    await expect
      .poll(() => polls, {
        message: `the page stopped polling ${STATUS_API_PATH} after the first fetch`,
        timeout: POLL_INTERVAL_MS * 5,
      })
      .toBeGreaterThanOrEqual(2);

    // The refresh is in place, not a navigation.
    await expect(page).toHaveURL(new RegExp(`${STATUS_PAGE_PATH}$`));
    await expect(locators.summaryBar).toBeVisible();
  }
);

test(
  "Status - make the status API fail then open /status, verify the page reports it cannot reach the status service instead of showing a false all-clear",
  { tag: ["@dev", "@regression", "@negative"] },
  async ({ page }) => {
    // Client-side interception only — the real endpoint is untouched, so this cannot
    // affect the shared cluster or any other test running against it.
    await page.route(`**${STATUS_API_PATH}`, (route) => route.fulfill({ status: 500, body: "" }));

    await page.goto(STATUS_PAGE_PATH);
    const locators = new StatusLocators(page);

    await expect(locators.summaryBar).toBeVisible();
    await expect(locators.summaryBar).toContainText(SUMMARY_LABEL.unknown);
    await expect(locators.summaryBar).toContainText("Unable to reach the status service");

    await test.step("The components card says it has no data rather than listing none", async () => {
      await expect(locators.componentsCard).toContainText("No component data available.");
    });

    await test.step("A failed check never reads as All Systems Operational", async () => {
      await expect(locators.summaryBar).not.toContainText(SUMMARY_LABEL.operational);
    });
  }
);
