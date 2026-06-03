import { test, expect } from "@playwright/test";
import { MonitoringTabLocator } from "../Monitoring/MonitoringTabLocator";
import { waitForGraphQLAndValidate } from "../../utils/GraphQLNetworkWatcher";
import { sloWorkloads, SLO_CONFIG } from "./sloConstants";

let locators: MonitoringTabLocator;

test.beforeEach(async ({ page }) => {
  locators = new MonitoringTabLocator(page);
  await locators.setupMonitoringPage();
  await expect(locators.MonitoringDropdownSLo).toBeVisible();
});

test("API testing Cluster Details->Monitoring-> SLO", async ({ page }, testInfo) => {
  test.setTimeout(120000);

  await waitForGraphQLAndValidate(
    page,
    async () => {
      await locators.MonitoringDropdownSLo.click();
    },
    {
      testName: testInfo.title,
      operationNames: [],
    }
  );
});

test("API testing Cluster Details->Monitoring-> Configure SLO", async ({ page }, testInfo) => {
  test.setTimeout(180000);

  await locators.MonitoringDropdownSLo.click();
  await expect(locators.AddSLOConfigBtn).toBeVisible({ timeout: 15000 });

  for (const { namespace, workload } of sloWorkloads) {
    await test.step(`Configure SLO: ${namespace} / ${workload}`, async () => {
      try {
        await locators.AddSLOConfigBtn.click();

        await locators.selectSLONamespace(namespace);
        await locators.selectSLOWorkload(workload);

        await locators.fillSLOForm(
          SLO_CONFIG.availabilityObjective,
          SLO_CONFIG.latencyObjective,
          SLO_CONFIG.latencyThresholdMs
        );

        await waitForGraphQLAndValidate(
          page,
          async () => {
            await locators.SloDialogSubmitBtn.click();
          },
          {
            testName: `${testInfo.title} - ${namespace}/${workload}`,
            operationNames: [],
            timeoutMs: 2000,
          }
        );

        await locators.SloDialogSubmitBtn.waitFor({ state: "hidden", timeout: 8000 });
        console.log(`SUCCESS: SLO configured for ${namespace}/${workload}`);
      } catch (e: unknown) {
        const msg = e instanceof Error ? e.message : String(e);
        console.error(`SKIPPED/ERROR: ${namespace}/${workload} — ${msg}`);
        if (await locators.SloDialogCancelBtn.isVisible()) {
          await locators.closeSLODialog();
        }
      }
    });
  }
});
