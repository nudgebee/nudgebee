// Not for OSS
import { test, expect } from "@playwright/test";
import { LoginPage } from "../../pages/LoginPage";
import { openFirstPodDetails, openPodTab } from "./podDetailsHelper";
import { PodDetailsLocators, POD_TABS, EVENTS_TABLE } from "./podDetailsLocators";

// Infra > K8s > cluster > Apps & Infra > Pods > a pod — app/src/pages/kubernetes/podDetails/
// [PodDetails].jsx, rendering PodTitleBox + PodsDetails.
//
// Every case here is read-only. The page's two write surfaces are the Pod Debugger (opens a
// shell into a live container) and the Yaml editor, and the editor is unreachable anyway:
// PodsDetails mounts KubernetesPodYaml without `showEditButton`, whose default is false. The
// debugger is only ever asserted as present, never opened — see "Follow-ups" in the PR.

// A pod id no cluster can hold, for the not-found case.
const UNKNOWN_POD_ID = "00000000-0000-0000-0000-000000000000";

test("Pod Details sanity - open Apps & Infra Pods, click the first pod, verify the header names that pod and carries its resource id", { tag: ["@dev", "@smoke", "@functional"] }, async ({
  page,
}) => {
  test.setTimeout(180000);
  const pod = await openFirstPodDetails(page);

  await expect(pod.locators.podNameHeading).toContainText(pod.podName);
  // The id in the header and the id in the route are the same pod, which is what proves
  // the row that was clicked is the record that opened.
  await expect(pod.locators.podTitleBox()).toContainText(pod.podId);
  await expect(pod.locators.podTitleBox()).toContainText("Last seen:");
  await expect(pod.locators.podDebuggerBtn).toBeVisible();
});

test("Pod Details sanity - open a pod, verify the tab strip offers all ten views and lands on Pod Details", { tag: ["@dev", "@sanity", "@functional"] }, async ({ page }) => {
  test.setTimeout(180000);
  const pod = await openFirstPodDetails(page);

  for (const name of POD_TABS) {
    await expect(pod.locators.tab(name)).toBeVisible();
  }
  await expect(pod.locators.selectedTab()).toHaveCount(1);
  await expect(pod.locators.selectedTab()).toHaveText("Pod Details");
});

test("Pod Details - open a pod, stay on the Pod Details tab, verify the summary card reports the pod's own name, status, node, namespace and QoS class", { tag: ["@dev", "@regression", "@functional"] }, async ({
  page,
}) => {
  test.setTimeout(180000);
  const pod = await openFirstPodDetails(page);

  for (const label of ["Name", "Status", "Created", "Pod IP", "Controlled by", "Parent Controller", "Node", "Namespace", "QoS Class"]) {
    await expect(pod.locators.summaryField(label)).toBeVisible();
  }

  // The card describes the pod that was opened, not whichever one the page loaded last.
  await expect(pod.locators.summaryRow("Name")).toContainText(pod.podName);
  // Each of these renders a value, not an empty cell beside its label.
  await expect(pod.locators.summaryRow("Namespace")).toHaveText(/Namespace:\s*\S+/);
  await expect(pod.locators.summaryRow("Node")).toHaveText(/Node:\s*\S+/);
  await expect(pod.locators.summaryRow("QoS Class")).toHaveText(/QoS Class:\s*\S+/);
});

test("Pod Details - open a pod, click the Logs tab, verify the log panel replaces the summary card and offers the Container filter", { tag: ["@dev", "@regression", "@functional"] }, async ({
  page,
}) => {
  test.setTimeout(180000);
  const pod = await openFirstPodDetails(page);

  await expect(pod.locators.summaryField("QoS Class")).toBeVisible();
  await openPodTab(page, pod, "Logs");

  await expect(pod.locators.logsPanel).toBeVisible({ timeout: 60000 });
  await expect(pod.locators.containerFilter).toBeVisible();
  await expect(pod.locators.previousLogsCheckbox).toBeVisible();
  // The summary card belongs to tab 0 only — PodsDetails renders exactly one tab body, so
  // it must be gone rather than merely covered.
  await expect(pod.locators.summaryField("QoS Class")).toHaveCount(0);
});

// A container-picking case belongs here and was written, then dropped rather than shipped red
// or softened into an assertion that also passes when the filter is empty: on dev the Container
// filter opens with zero role="option" rows, so there is nothing to select. Reported in the PR —
// KubernetesPodLogs fills it from podData.meta.config.containers, and an empty selector on a
// running pod means nobody can switch containers in pod logs. The filter's presence is still
// asserted by the Logs tab case above.

test("Pod Details - open a pod, click the Recent Events tab, verify the events table either lists rows or renders its no-data panel", { tag: ["@dev", "@regression", "@functional"] }, async ({
  page,
}) => {
  test.setTimeout(180000);
  const pod = await openFirstPodDetails(page);
  await openPodTab(page, pod, "Recent Events");

  await expect(pod.locators.eventsPanel).toBeVisible({ timeout: 60000 });
  await pod.locators.waitForTable(EVENTS_TABLE);

  // Whether this workload has fired events in the window is the cluster's business, not the
  // page's — but exactly one of the two shapes must be on screen, never neither.
  //
  // The retrying assertion is the wait. waitForTable settles on the tbody being *attached*,
  // which this table can commit a beat before its rows, and count() does not retry — it
  // would read 0 and take the no-data branch against a table that does have rows.
  await expect(pod.locators.eventsRows.first().or(pod.locators.eventsNoData)).toBeVisible({ timeout: 60000 });

  // A probe, not an assertion: the line above already proved one of the two rendered, so this
  // only decides which one, and an absent row here is the expected empty-cluster case.
  if (await pod.locators.eventsRows.first().isVisible()) {
    await expect(pod.locators.eventsNoData).toHaveCount(0);
  } else {
    await expect(pod.locators.eventsNoData).toBeVisible();
  }
});

test("Pod Details - open a pod, click Utilization Trends then Cost Trends, verify the cost panel replaces the utilization panel", { tag: ["@dev", "@regression", "@functional"] }, async ({
  page,
}) => {
  test.setTimeout(180000);
  const pod = await openFirstPodDetails(page);

  await openPodTab(page, pod, "Utilization Trends");
  await expect(pod.locators.utilizationPanel).toBeVisible({ timeout: 60000 });

  await openPodTab(page, pod, "Cost Trends");
  await expect(pod.locators.costPanel).toBeVisible({ timeout: 60000 });
  await expect(pod.locators.utilizationPanel).toHaveCount(0);
});

// A deep-link case belongs here and was written, then dropped rather than shipped red or
// weakened: a cold load of <pod url>#logs opens Pod Details, not Logs. Reported as a product
// bug in the PR — PodsDetails.jsx reads the fragment from router.asPath, which on a cold load
// of an auto-statically-optimized page has not got one yet, so the effect falls to its
// setOption(0) branch. Tab switching via the strip is covered above and is unaffected.

test("Pod Details - open a pod, open the Logs tab, go back in history, verify the browser returns to the Pod Details tab", { tag: ["@dev", "@regression", "@functional"] }, async ({
  page,
}) => {
  test.setTimeout(180000);
  const pod = await openFirstPodDetails(page);

  await openPodTab(page, pod, "Logs");
  await expect(pod.locators.logsPanel).toBeVisible({ timeout: 60000 });

  await page.goBack();

  await expect(pod.locators.selectedTab()).toHaveText("Pod Details", { timeout: 60000 });
  await expect(pod.locators.summaryField("QoS Class")).toBeVisible({ timeout: 60000 });
  await expect(pod.locators.logsPanel).toHaveCount(0);
});

test("Pod Details - open a pod details url whose pod id does not exist, verify no tab strip renders and the page stays on the pod route", { tag: ["@dev", "@regression", "@negative"] }, async ({
  page,
}) => {
  test.setTimeout(180000);

  await new LoginPage(page).doFullLogin();
  const locators = new PodDetailsLocators(page);

  await page.goto(`/kubernetes/podDetails/${UNKNOWN_POD_ID}?PodDetails=${UNKNOWN_POD_ID}#pod-details`);

  // PodTitleBox renders from an empty object, so the header is the proof the route mounted
  // — without it, "no tabs" would also be true of a page that never loaded.
  await expect(locators.podNameHeading).toBeVisible({ timeout: 60000 });
  // PodsDetails returns null when the lookup yields no pod, so the strip must never appear.
  await expect(locators.tabStrip).toHaveCount(0);
  await expect(locators.summaryField("QoS Class")).toHaveCount(0);
  // The page redirects to /kubernetes only when the id is missing altogether, so an unknown
  // one has to stay put rather than bouncing the user out of the route.
  await expect(page).toHaveURL(new RegExp(`/kubernetes/podDetails/${UNKNOWN_POD_ID}`));
});
