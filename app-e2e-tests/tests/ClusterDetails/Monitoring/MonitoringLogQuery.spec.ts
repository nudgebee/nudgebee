import { test, expect, Page, Request } from "@playwright/test";
import { LoginPage } from "../../../pages/LoginPage";
import { MonitoringTabLocator } from "../Monitoring/MonitoringTabLocator";
import { waitForGraphQLAndValidate } from "../../utils/GraphQLNetworkWatcher";


test("API testing Cluster Details->Monitoring-> Query Logs", { tag: ["@dev", "@test", "@smoke", "@functional", "@oss"] }, async ({ page }, testInfo) => {
  test.setTimeout(120000);

  const loginPage = new LoginPage(page);
  const locators = new MonitoringTabLocator(page);
  await loginPage.doFullLogin();
  await locators.navigateToMonitoringTab();

  await waitForGraphQLAndValidate(
    page,
    async () => {
      await locators.clickTab(locators.MonitoringDropdownQueryLogs);
    },
    {
      testName: testInfo.title,
      operationNames: [],
    }
  );
});


test(
  "Query Logs - select Loki as the log provider, add a label filter in Builder mode, run the query, then re-run it from Code mode, verify both runs fetch logs and echo the executed query",
  { tag: ["@dev", "@test", "@regression", "@functional", "@oss"] },
  async ({ page }, testInfo) => {
    test.setTimeout(180000);

    const loginPage = new LoginPage(page);
    const locators = new MonitoringTabLocator(page);
    await loginPage.doFullLogin();
    await locators.navigateToMonitoringTab();
    await locators.clickTab(locators.MonitoringDropdownQueryLogs);

    await test.step("Loki is the selected log provider", async () => {
      const selected = await locators.selectLogProvider("loki");
      test.skip(!selected, "This account does not offer Loki as a log provider.");
      await expect.poll(() => locators.currentLogProvider()).toMatch(/loki/i);
    });

    await test.step("Builder mode is the active editor", async () => {
      // Loki lands on Builder by itself - QueryModeSwitcher's default-mode effect picks
      // 'build' for signoz, loggly and loki - so this asserts the state as much as it sets it.
      await locators.switchQueryMode("Builder");
      await expect(locators.queryModeOption("Builder")).toHaveAttribute("aria-checked", "true");
    });

    await test.step("A label filter picked from the suggestions builds a query", async () => {
      const filtered = await locators.addFirstLogLabelFilter();
      test.skip(!filtered, "Loki offered no labels on this account to build a filter from.");
      // Two independent signals that a chip really landed: the builder's own hint is gone,
      // and Add Operation - disabled exactly while the block holds no chips - is live.
      await expect(locators.LogQueryFilterWarning).toHaveCount(0);
      await expect(locators.LogQueryAddOperationBtn).toBeEnabled();
    });

    await test.step("Run Query in Builder mode fetches logs", async () => {
      await waitForGraphQLAndValidate(
        page,
        async () => {
          await locators.RunQueryButton.click();
        },
        {
          testName: testInfo.title,
          // Named, not auto-capture: this step is about Run Query issuing the log query.
          // In auto mode it passes on ANY GraphQL POST, and the tab's own
          // GetDefaultProvider / FetchLogLabels are still settling when the click lands -
          // so it went green while the query never fired at all.
          operationNames: ["FetchLogs"],
        }
      );
      // KubernetesLogs only sets executedQuery from the logs response, so this echo
      // appearing is proof the run came back, not just that a request left.
      await expect(locators.ExecutedQueryLabel).toBeVisible({ timeout: 30000 });
    });

    await test.step("Code mode inherits the built query and runs it again", async () => {
      // Armed BEFORE the switch, and waiting on the round trip itself rather than on the
      // editor holding text. Switching to Code fires QueryModeSwitcher's
      // handleFetchFormattedQuery (GraphQL GetFormattedQuery), and only its response
      // replaces the editor with real LogQL. Until then the editor still shows the
      // Builder's internal where-clause JSON - non-empty, but not a query - so a
      // "not empty" gate lets the click through early and Loki rejects the JSON with
      // `parse error at line 0, col 1: not a valid duration string`.
      const formattedQuery = page.waitForResponse(
        (response) =>
          response.request().method() === "POST" &&
          (response.request().postData() || "").includes("GetFormattedQuery"),
        { timeout: 30000 }
      );
      await locators.switchQueryMode("Code");
      await formattedQuery;
      await expect(locators.QueryCodeEditor).not.toBeEmpty({ timeout: 30000 });

      await waitForGraphQLAndValidate(
        page,
        async () => {
          await locators.RunQueryButton.click();
        },
        {
          testName: testInfo.title,
          operationNames: ["FetchLogs"],
        }
      );
      await expect(locators.ExecutedQueryLabel).toBeVisible({ timeout: 30000 });
    });
  }
);

// Counts log-query requests leaving the page. Used to prove a click issued NOTHING,
// which waitForGraphQLAndValidate cannot express - it fails when an operation is missing.
function countFetchLogs(page: Page) {
  let count = 0;
  const listener = (request: Request) => {
    if (request.method() !== "POST") return;
    try {
      const body = JSON.parse(request.postData() || "{}");
      const ops = Array.isArray(body) ? body : [body];
      if (ops.some((op) => op?.operationName === "FetchLogs")) count += 1;
    } catch {
      // Not a JSON body, so not a GraphQL operation this counter cares about.
    }
  };
  page.on("request", listener);
  return () => {
    page.off("request", listener);
    return count;
  };
}

async function openQueryLogs(page: Page): Promise<MonitoringTabLocator> {
  const locators = new MonitoringTabLocator(page);
  await new LoginPage(page).doFullLogin();
  await locators.navigateToMonitoringTab();
  await locators.clickTab(locators.MonitoringDropdownQueryLogs);
  return locators;
}


test(
  "Query Logs sanity - open the Query Logs tab, verify the provider badge, the editor switcher and the Limit, Run Query, auto-refresh and download controls all render",
  { tag: ["@dev", "@test", "@sanity", "@functional", "@oss"] },
  async ({ page }) => {
    test.setTimeout(120000);
    const locators = await openQueryLogs(page);

    await test.step("The toolbar names the log provider it will query", async () => {
      await expect(locators.LogProviderBadge).toBeVisible();
      await expect.poll(() => locators.currentLogProvider()).not.toBe("");
    });

    await test.step("Both query editors are offered", async () => {
      await expect(locators.queryModeOption("Builder")).toBeVisible();
      await expect(locators.queryModeOption("Code")).toBeVisible();
    });

    await test.step("The run controls are on screen", async () => {
      await expect(locators.LogLimitDropdown).toBeVisible();
      await expect(locators.RunQueryButton).toBeVisible();
      await expect(locators.AutoRefreshDropdown).toBeVisible();
      await expect(locators.DownloadLogsBtn).toBeVisible();
    });
  }
);


test(
  "Query Logs - open Builder mode with no label filter, click Run Query, verify the filter-required warning appears and no log query is sent",
  { tag: ["@dev", "@test", "@regression", "@negative", "@validation", "@oss"] },
  async ({ page }) => {
    test.setTimeout(120000);
    const locators = await openQueryLogs(page);

    const selected = await locators.selectLogProvider("loki");
    test.skip(!selected, "This account does not offer Loki as a log provider.");
    await locators.switchQueryMode("Builder");

    await test.step("The builder starts out holding no filter", async () => {
      // Let the tab's own GetDefaultProvider / FetchLogLabels settle first, or the
      // builder is still mounting and "no chips" is indistinguishable from "not ready".
      await page.waitForLoadState("networkidle").catch(() => {});
      await expect(locators.LogQueryAddOperationBtn).toBeVisible({ timeout: 20000 });
      // Disabled exactly while the block holds no chips, so this is a positive read of
      // the empty state rather than the absence of something.
      await expect(locators.LogQueryAddOperationBtn).toBeDisabled();
    });

    await test.step("Run Query is refused and reaches the network with nothing", async () => {
      const stopCounting = countFetchLogs(page);
      await locators.RunQueryButton.click();
      await expect(locators.LogFilterRequiredToast).toBeVisible({ timeout: 15000 });
      expect(stopCounting()).toBe(0);
    });
  }
);
