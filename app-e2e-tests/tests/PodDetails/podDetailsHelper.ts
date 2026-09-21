// Not for OSS
import { Page, expect } from "@playwright/test";
import { LoginPage } from "../../pages/LoginPage";
import { AppsAndInfraLocators } from "../ClusterDetails/AppsAndInfra/AppsAndInfraLocators";
import { PodDetailsLocators, PODS_TABLE } from "./podDetailsLocators";

// Pod Details (app/src/pages/kubernetes/podDetails/[PodDetails].jsx) has no listing of its
// own and is not on the sidebar. The only in-product route to it is the Pods table on the
// cluster's Apps & Infra tab, whose row click pushes
//   /kubernetes/podDetails/<id>?PodDetails=<id>&accountId=<cluster>#pod-details
// (KubernetesPods.jsx handlePodClick), so every test walks that path once for a live id
// rather than pinning one — a pod id is ephemeral and would go stale within a day.
const NO_PODS_HINT =
  "The Pods table on Apps & Infra rendered no rows, so there is no pod to open. Pod Details " +
  "has no other entry point — point CLUSTER at a cluster that is reporting pods.";

export interface OpenedPod {
  locators: PodDetailsLocators;
  podName: string;
  podId: string;
}

// Signs in, opens the configured cluster, and opens the first pod in its Pods table.
export async function openFirstPodDetails(page: Page): Promise<OpenedPod> {
  const cluster = process.env.CLUSTER_NAME || process.env.CLUSTER;
  if (!cluster) {
    throw new Error("CLUSTER (or CLUSTER_NAME) is not set — add it to .env / .env.dev");
  }

  const appsAndInfra = new AppsAndInfraLocators(page);
  await new LoginPage(page).doFullLogin();
  await appsAndInfra.openClusterFromConfig();
  await appsAndInfra.navigateToCluster();
  await appsAndInfra.clickTab(appsAndInfra.Pods);

  const locators = new PodDetailsLocators(page);
  await locators.waitForTable(PODS_TABLE);
  await expect(locators.podRows.first(), NO_PODS_HINT).toBeVisible({ timeout: 90000 });

  const nameCell = locators.podNameCell(locators.podRows.first());
  await expect(nameCell).toBeVisible({ timeout: 30000 });
  const podName = (await nameCell.innerText()).trim();
  await nameCell.click();

  await page.waitForURL(/\/kubernetes\/podDetails\/[^/?#]+/, { timeout: 60000 });
  await expect(locators.tabStrip).toBeVisible({ timeout: 60000 });

  const podId = new URL(page.url()).pathname.split("/").filter(Boolean).pop() ?? "";

  return { locators, podName, podId };
}

// Opens one tab the way a person does — by clicking the strip. The tabs are Next links
// (Tabs.jsx behavior='router'), so the click also writes the fragment, which is what the
// page reads back on mount; asserting the selected tab rather than the URL keeps this
// about the tab that rendered.
export async function openPodTab(page: Page, opened: OpenedPod, name: string): Promise<void> {
  const tab = opened.locators.tab(name);
  await expect(tab).toBeVisible({ timeout: 30000 });
  await tab.click();
  // Park the cursor off the strip: left on a tab, the sidebar rail's hover flyout is a
  // Popover with an invisible page-wide backdrop that swallows the next click.
  await page.mouse.move(640, 500);
  await expect(tab).toHaveAttribute("aria-selected", "true", { timeout: 30000 });
}
