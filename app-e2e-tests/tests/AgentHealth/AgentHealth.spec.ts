// Not for OSS
import { test, expect } from "@playwright/test";
import { openAgentHealth, openAgentHealthUnscoped, expectSelectedTab, expectFragment } from "./agentHealthHelper";
import { AGENT_HEADERS, AGENT_FEATURES, FEATURE_STATE, NOT_CONNECTED } from "./agentHealthLocators";

// Troubleshoot > Agent Health — the per-account agent reporting page
// (app/src/pages/agentHealth.jsx), reached from the cluster dropdown's health indicator.
//
// The module is read-only: it renders what the agent last reported and offers no create,
// edit or delete path, so nothing here writes to the shared dev environment. Its one write
// action ("Sync Now") belongs to the cloud-account variant of the Agent tab, which a K8s
// cluster never renders — see "Follow-ups" in the PR.

// Every test lands twice (the cluster redirect, then the module) against a live agent API.
test.beforeEach(() => {
  test.setTimeout(180000);
});

test(
  "Agent Health sanity - open Agent Health for the selected cluster, verify both the Agent and Proxy Agent tabs render with Agent selected",
  { tag: ["@dev", "@sanity", "@functional"] },
  async ({ page }) => {
    const agentHealth = await openAgentHealth(page, "agent");

    await test.step("A K8s account offers both tabs", async () => {
      await expect(agentHealth.agentTab).toBeVisible();
      await expect(agentHealth.proxyAgentTab).toBeVisible();
      await expect(agentHealth.agentTab).toHaveText(/Agent/);
      await expect(agentHealth.proxyAgentTab).toHaveText(/Proxy Agent/);
    });

    await test.step("The Agent tab owns the page body", async () => {
      await expectSelectedTab(agentHealth.agentTab);
      await expect(agentHealth.proxyAgentTab).toHaveAttribute("aria-selected", "false");
      await expect(agentHealth.agentCard).toBeVisible();
      await expect(agentHealth.agentCard).toContainText("Agent Health");
      await expect(agentHealth.proxyCard).toHaveCount(0);
    });
  }
);

test(
  "Agent Health sanity - open the Agent tab, verify the table lists Status, Agent Version, Latest Version, Last Connected and K8s(Provider/Version)",
  { tag: ["@dev", "@sanity", "@functional"] },
  async ({ page }) => {
    const agentHealth = await openAgentHealth(page, "agent");
    await agentHealth.waitForTable(agentHealth.agentTable, agentHealth.agentEmpty);

    for (const header of AGENT_HEADERS) {
      await expect(agentHealth.columnHeaderIn(agentHealth.agentTable, header)).toBeVisible();
    }

    // A cloud account swaps in a four-column layout (HEADERS_CLOUD), so the count is what
    // proves the k8s contract rather than a coincidental overlap of the first two names.
    await expect(agentHealth.agentTable.locator("thead th")).toHaveCount(AGENT_HEADERS.length);
  }
);

test(
  "Agent Health - open the Agent tab, verify the Features block reports a connection state for Relay, Prometheus, Alert Manager, Logs, Traces, OpenCost and Node Agent",
  { tag: ["@dev", "@smoke", "@functional"] },
  async ({ page }) => {
    const agentHealth = await openAgentHealth(page, "agent");

    await expect(agentHealth.featuresHeading).toBeVisible();

    for (const feature of AGENT_FEATURES) {
      await test.step(`${feature} reports a state`, async () => {
        const item = agentHealth.featureItem(feature);
        await expect(item).toBeVisible();
        await expect(item).toContainText(FEATURE_STATE);
      });
    }

    // The installation namespace rides in the same list and is the one entry with a free-text
    // value, so it is checked for presence rather than for a connection word.
    await expect(agentHealth.featureItem("Agent Namespace")).toBeVisible();
  }
);

test(
  "Agent Health - read the agent row, verify the update-agent warning shows only when the reported version differs from the latest version",
  { tag: ["@dev", "@regression", "@functional", "@validation"] },
  async ({ page }) => {
    const agentHealth = await openAgentHealth(page, "agent");
    await agentHealth.waitForTable(agentHealth.agentTable, agentHealth.agentEmpty);

    const rows = await agentHealth.dataRowsIn(agentHealth.agentTable).count();
    expect(rows, "The cluster reported no agent row, so there is no version pair to compare — connect the agent on the configured cluster").toBeGreaterThan(0);

    // Checked before destructuring: a row short of its columns would hand back undefined
    // here, and undefined slips through every branch below as a silent pass.
    const cells = await agentHealth.rowCells(agentHealth.agentTable);
    expect(cells).toHaveLength(AGENT_HEADERS.length);

    const [status, agentVersion, latestVersion] = cells;
    expect(status).not.toEqual("");

    if (status === NOT_CONNECTED) {
      // agentHealth.jsx renders the two banners as an either/or: a disconnected agent gets
      // the connectivity message and never the version one, whatever the versions say.
      await expect(agentHealth.notConnectedMessage).toBeVisible();
      await expect(agentHealth.upgradeWarning).toHaveCount(0);
      return;
    }

    await expect(agentHealth.notConnectedMessage).toHaveCount(0);

    // shouldUpgrade is only ever set when a latest version came back, so an unknown latest
    // ("-") must leave the warning off however old the installed agent is.
    const knownLatest = latestVersion !== "" && latestVersion !== "-";
    if (knownLatest && agentVersion !== latestVersion) {
      await expect(agentHealth.upgradeWarning).toBeVisible();
    } else {
      await expect(agentHealth.upgradeWarning).toHaveCount(0);
    }
  }
);

test(
  "Agent Health - switch from Agent to Proxy Agent, verify the fragment becomes #proxy-agent and the proxy panel replaces the Agent Health card",
  { tag: ["@dev", "@smoke", "@functional"] },
  async ({ page }) => {
    const agentHealth = await openAgentHealth(page, "agent");
    await expect(agentHealth.agentCard).toBeVisible();

    await agentHealth.proxyAgentTab.click();

    await expectFragment(page, "proxy-agent");
    await expectSelectedTab(agentHealth.proxyAgentTab);
    await expect(agentHealth.proxyPanel).toBeVisible({ timeout: 60000 });
    await expect(agentHealth.agentCard).toHaveCount(0);
  }
);

test(
  "Agent Health - switch to Proxy Agent and back to Agent, verify the Agent Health card and its table return",
  { tag: ["@dev", "@regression", "@functional"] },
  async ({ page }) => {
    const agentHealth = await openAgentHealth(page, "agent");

    await agentHealth.proxyAgentTab.click();
    await expectSelectedTab(agentHealth.proxyAgentTab);
    await expect(agentHealth.proxyPanel).toBeVisible({ timeout: 60000 });

    await agentHealth.agentTab.click();

    await expectFragment(page, "agent");
    await expectSelectedTab(agentHealth.agentTab);
    await expect(agentHealth.agentCard).toBeVisible();
    // Re-read after the switch: the card is remounted, so the table is a fresh node and the
    // previous assertion's element is gone.
    await agentHealth.waitForTable(agentHealth.agentTable, agentHealth.agentEmpty);
    await expect(agentHealth.proxyOnboarding).toHaveCount(0);
  }
);

test(
  "Agent Health - open the module with a #proxy-agent deep link, verify the Proxy Agent tab is the one selected on load",
  { tag: ["@dev", "@regression", "@functional"] },
  async ({ page }) => {
    const agentHealth = await openAgentHealth(page, "proxy-agent");

    await expectSelectedTab(agentHealth.proxyAgentTab);
    await expect(agentHealth.agentTab).toHaveAttribute("aria-selected", "false");
    await expect(agentHealth.proxyPanel).toBeVisible({ timeout: 60000 });
    await expect(agentHealth.agentCard).toHaveCount(0);
  }
);

test(
  "Agent Health - open the Proxy Agent tab then press browser Back, verify history restores the Agent tab and its card",
  { tag: ["@dev", "@regression", "@functional"] },
  async ({ page }) => {
    const agentHealth = await openAgentHealth(page, "agent");

    await agentHealth.proxyAgentTab.click();
    await expectSelectedTab(agentHealth.proxyAgentTab);

    await page.goBack();

    // The page derives its tab from router.asPath on every change, so Back has to move the
    // rendered panel and not only the address bar.
    await expectFragment(page, "agent");
    await expectSelectedTab(agentHealth.agentTab);
    await expect(agentHealth.agentCard).toBeVisible();
  }
);

test(
  "Agent Health sanity - open the bare /agentHealth link, verify the page adopts the selected cluster's account into the URL and opens the Agent tab",
  { tag: ["@dev", "@sanity", "@functional"] },
  async ({ page }) => {
    const agentHealth = await openAgentHealthUnscoped(page);

    await test.step("The account arrives in the URL without the link naming one", async () => {
      // Every deep link into this module relies on it: the sidebar and global search hand
      // over a bare path, and the page fetches nothing until an account is in the query.
      await expect(page).toHaveURL(/\/agentHealth\?[^#]*accountId=[^&#]+/, { timeout: 60000 });
    });

    await test.step("It opens on the Agent tab and renders that tab's card", async () => {
      await expectSelectedTab(agentHealth.agentTab);
      await expect(agentHealth.proxyAgentTab).toHaveAttribute("aria-selected", "false");
      await expect(agentHealth.agentCard).toBeVisible();
    });

    await test.step("What it adopted is a real account id, not an empty or undefined one", async () => {
      // A blank or literal "undefined" would still satisfy the pattern above while leaving
      // every fetch on the page unanswered.
      const adopted = /[?&]accountId=([^&#]+)/.exec(page.url())?.[1] ?? "";
      expect(adopted).toMatch(/^[0-9a-fA-F-]{8,}$/);
    });
  }
);

test(
  "Agent Health sanity - open Agent Health for a K8s account, verify the tab strip offers exactly the Agent and Proxy Agent tabs in that order",
  { tag: ["@dev", "@sanity", "@functional"] },
  async ({ page }) => {
    const agentHealth = await openAgentHealth(page, "agent");

    // A self-hosted fleet drops the Agent tab entirely (isVmAccount in agentHealth.jsx), so
    // the count is the contract — asserting the two are present would pass on a three-tab
    // strip just as happily.
    const tabs = page.getByRole("tab");
    await expect(tabs).toHaveCount(2);
    await expect(tabs).toHaveText([/^Agent$/, /^Proxy Agent$/]);

    await expect(agentHealth.proxyCard).toHaveCount(0);
    await expect(agentHealth.proxyOnboarding).toHaveCount(0);
  }
);
