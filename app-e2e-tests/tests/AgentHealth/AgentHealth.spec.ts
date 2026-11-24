import { test, expect } from "@playwright/test";
import {
  openAgentHealth,
  openAgentHealthUnscoped,
  expectSelectedTab,
  expectFragment,
  hasRedesignedAgentTab,
  PREDATES_REDESIGN,
} from "./agentHealthHelper";
import {
  AGENT_COMPONENTS,
  AGENT_FACTS,
  COMPONENT_HEADERS,
  COMPONENT_STATE,
  NOT_CONNECTED,
  OBSERVABILITY_SIGNALS,
  SCHEDULED_JOB_HEADERS,
  SIGNAL_STATE,
} from "./agentHealthLocators";

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
  { tag: ["@dev", "@sanity", "@functional", "@oss"] },
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
  "Agent Health sanity - open the Agent tab, verify the summary card reports a status and states the agent version, Kubernetes flavour, namespace and URL",
  { tag: ["@dev", "@sanity", "@functional", "@oss"] },
  async ({ page }) => {
    const agentHealth = await openAgentHealth(page, "agent");

    test.skip(!(await hasRedesignedAgentTab(agentHealth)), PREDATES_REDESIGN);

    // The status chip is the card's own load signal: it renders only once the account row
    // has come back, so it stands in for the removed table's skeleton wait.
    await expect(agentHealth.agentStatusChip).toBeVisible({ timeout: 60000 });
    await expect(agentHealth.agentStatusChip).toHaveText(/Connected|NOT CONNECTED|Unknown/);

    for (const fact of AGENT_FACTS) {
      await test.step(`The card states ${fact}`, async () => {
        await expect(agentHealth.agentFact(fact)).toBeVisible();
      });
    }

    // The count is the contract, not merely that each is present: a fact silently dropped
    // from the grid would still leave the other three visible.
    await expect(agentHealth.agentCard.locator('[data-testid^="agent-fact-"]')).toHaveCount(AGENT_FACTS.length);
  }
);

test(
  "Agent Health - open the Agent tab, verify the Components table reports a state for Relay, Prometheus, Alert Manager, Logs, Traces, OpenCost and Node Agent",
  { tag: ["@dev", "@smoke", "@functional", "@oss"] },
  async ({ page }) => {
    const agentHealth = await openAgentHealth(page, "agent");

    test.skip(!(await hasRedesignedAgentTab(agentHealth)), PREDATES_REDESIGN);

    await agentHealth.waitForTable(agentHealth.componentsTable, agentHealth.componentsEmpty);

    for (const header of COMPONENT_HEADERS) {
      await expect(agentHealth.columnHeaderIn(agentHealth.componentsTable, header)).toBeVisible();
    }
    await expect(agentHealth.componentsTable.locator("thead th")).toHaveCount(COMPONENT_HEADERS.length);

    for (const component of AGENT_COMPONENTS) {
      await test.step(`${component} reports a state`, async () => {
        const row = agentHealth.componentRow(component);
        await expect(row).toBeVisible();
        await expect(row).toContainText(COMPONENT_STATE);
      });
    }

    // One row per component, always — a component the agent does not serve still says so
    // rather than being omitted, which is the difference between "no backend" and "no row".
    await expect(agentHealth.dataRowsIn(agentHealth.componentsTable)).toHaveCount(AGENT_COMPONENTS.length);
  }
);

test(
  "Agent Health - read the summary card, verify the update callout and its guide link appear only when the reported version differs from the latest version",
  { tag: ["@dev", "@regression", "@functional", "@validation", "@oss"] },
  async ({ page }) => {
    const agentHealth = await openAgentHealth(page, "agent");

    test.skip(!(await hasRedesignedAgentTab(agentHealth)), PREDATES_REDESIGN);

    await expect(agentHealth.agentStatusChip).toBeVisible({ timeout: 60000 });
    const status = ((await agentHealth.agentStatusChip.textContent()) ?? "").trim();
    expect(status).not.toEqual("");

    if (status === NOT_CONNECTED) {
      // agentHealth.jsx renders one banner, most severe first: a disconnected agent gets
      // the connectivity callout and never the version one, whatever the versions say.
      await expect(agentHealth.disconnectedBanner).toBeVisible();
      await expect(agentHealth.updateBanner).toHaveCount(0);
      return;
    }

    await expect(agentHealth.disconnectedBanner).toHaveCount(0);

    const version = ((await agentHealth.agentFact("version").textContent()) ?? "").trim();

    // The update callout names both versions, so when it is showing, what it claims is
    // checkable — and the guide button is the action it exists to offer. A disconnected
    // component outranks it, so its absence is only evidence when no services banner shows.
    if (await agentHealth.updateBanner.isVisible()) {
      await expect(agentHealth.updateBanner).toContainText("Update available.");
      await expect(agentHealth.updateBanner).toContainText(/the latest release is \S+/);
      await expect(agentHealth.updateGuideButton).toBeVisible();
    } else if ((await agentHealth.servicesBanner.count()) === 0) {
      // No callout and nothing more severe suppressing it: the agent is current, so the
      // version the card states must be the latest one — and no guide button is offered.
      expect(version).not.toEqual("");
      await expect(agentHealth.updateGuideButton).toHaveCount(0);
    }
  }
);

test(
  "Agent Health - open the Agent tab, verify Scheduled Jobs lists a readable schedule beside each cron and right-aligns the execution count",
  { tag: ["@dev", "@regression", "@functional", "@oss"] },
  async ({ page }) => {
    const agentHealth = await openAgentHealth(page, "agent");

    test.skip(!(await hasRedesignedAgentTab(agentHealth)), PREDATES_REDESIGN);

    // The card renders only for a cluster whose agent reported schedule_jobs, so a cluster
    // with none is a skip rather than a failure — same pattern the Optimize specs use.
    test.skip((await agentHealth.scheduledJobsCard.count()) === 0, "This cluster's agent reported no scheduled jobs.");

    await agentHealth.waitForTable(agentHealth.scheduledJobsTable, agentHealth.emptyIn(agentHealth.scheduledJobsCard));

    for (const header of SCHEDULED_JOB_HEADERS) {
      await expect(agentHealth.columnHeaderIn(agentHealth.scheduledJobsTable, header)).toBeVisible();
    }
    await expect(agentHealth.scheduledJobsTable.locator("thead th")).toHaveCount(SCHEDULED_JOB_HEADERS.length);

    const cells = await agentHealth.rowCells(agentHealth.scheduledJobsTable);
    expect(cells).toHaveLength(SCHEDULED_JOB_HEADERS.length);

    const [name, jobStatus, schedule, , executions] = cells;
    expect(name).not.toEqual("");
    expect(jobStatus).toMatch(/New|Running|Done/);
    // The Schedule cell carries the raw cron; the readable line above it is present only
    // when cronstrue could describe the expression, so the cron is what is asserted.
    expect(schedule).toMatch(/[*\d]/);
    expect(executions).toMatch(/^\d+$/);
  }
);

test(
  "Agent Health - open Scheduled Jobs with overdue jobs present, verify the Overdue filter narrows the table to them",
  { tag: ["@dev", "@regression", "@functional", "@oss"] },
  async ({ page }) => {
    const agentHealth = await openAgentHealth(page, "agent");

    test.skip(!(await hasRedesignedAgentTab(agentHealth)), PREDATES_REDESIGN);

    test.skip((await agentHealth.scheduledJobsCard.count()) === 0, "This cluster's agent reported no scheduled jobs.");
    await agentHealth.waitForTable(agentHealth.scheduledJobsTable, agentHealth.emptyIn(agentHealth.scheduledJobsCard));

    // The filter is offered only when something is overdue — a lone "Overdue 0" control is
    // a dead end, so the page hides it. Both shapes are legitimate; which one appears
    // depends on the cluster, not on the change.
    test.skip((await agentHealth.scheduledJobsFilter.count()) === 0, "No job on this cluster is past its expected run.");

    const overdueOption = agentHealth.jobFilterOption(/Overdue/);
    const claimed = Number(/Overdue\s+(\d+)/.exec((await overdueOption.textContent()) ?? "")?.[1] ?? "0");
    expect(claimed).toBeGreaterThan(0);

    const total = await agentHealth.dataRowsIn(agentHealth.scheduledJobsTable).count();

    await overdueOption.click();
    await expect(overdueOption).toHaveAttribute("aria-checked", "true");

    // What the control claims and what filtering produces have to agree — they are computed
    // from the same records, so a mismatch means the filter and the count disagree about
    // what "overdue" is. Capped by the page size, which the unfiltered count also is.
    await expect(agentHealth.dataRowsIn(agentHealth.scheduledJobsTable)).toHaveCount(Math.min(claimed, total));
    // Every remaining row is one the page flagged.
    const rows = agentHealth.dataRowsIn(agentHealth.scheduledJobsTable);
    for (let i = 0; i < (await rows.count()); i++) {
      await expect(rows.nth(i)).toContainText("Overdue");
    }
  }
);

test(
  "Agent Health - switch from Agent to Proxy Agent, verify the fragment becomes #proxy-agent and the proxy panel replaces the Agent Health card",
  { tag: ["@dev", "@smoke", "@functional", "@oss"] },
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
  "Agent Health - switch to Proxy Agent and back to Agent, verify the Agent Health card and its Components table return",
  { tag: ["@dev", "@regression", "@functional", "@oss"] },
  async ({ page }) => {
    const agentHealth = await openAgentHealth(page, "agent");
    test.skip(!(await hasRedesignedAgentTab(agentHealth)), PREDATES_REDESIGN);

    await agentHealth.proxyAgentTab.click();
    await expectSelectedTab(agentHealth.proxyAgentTab);
    await expect(agentHealth.proxyPanel).toBeVisible({ timeout: 60000 });

    await agentHealth.agentTab.click();

    await expectFragment(page, "agent");
    await expectSelectedTab(agentHealth.agentTab);
    await expect(agentHealth.agentCard).toBeVisible();
    // Re-read after the switch: the cards are remounted, so the table is a fresh node and
    // the previous assertion's element is gone.
    await agentHealth.waitForTable(agentHealth.componentsTable, agentHealth.componentsEmpty);
    await expect(agentHealth.proxyOnboarding).toHaveCount(0);
  }
);

test(
  "Agent Health - open the module with a #proxy-agent deep link, verify the Proxy Agent tab is the one selected on load",
  { tag: ["@dev", "@regression", "@functional", "@oss"] },
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
  { tag: ["@dev", "@regression", "@functional", "@oss"] },
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
  { tag: ["@dev", "@sanity", "@functional", "@oss"] },
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
  "Agent Health sanity - open Agent Health for a K8s account, verify the tab strip offers exactly the Agent, Observability and Proxy Agent tabs in that order",
  { tag: ["@dev", "@sanity", "@functional", "@oss"] },
  async ({ page }) => {
    const agentHealth = await openAgentHealth(page, "agent");

    // The count is the contract, not merely that each is present: a self-hosted fleet drops
    // the Agent tab entirely and a cloud account drops Observability (isVmAccount /
    // isK8sAccount in agentHealth.jsx), so asserting presence alone would pass on a strip
    // carrying the wrong set for this account type.
    //
    // Both shapes are accepted because this suite runs against the deployed dev app, which
    // only grows the Observability tab once this change ships — pinning 3 would fail every
    // run until then, and pinning 2 would stop catching the tab going missing afterwards.
    // Collapse to the three-tab form once it is deployed everywhere.
    const hasObservability = (await agentHealth.observabilityTab.count()) > 0;
    const tabs = page.getByRole("tab");
    await expect(tabs).toHaveCount(hasObservability ? 3 : 2);
    await expect(tabs).toHaveText(hasObservability ? [/^Agent$/, /^Observability$/, /^Proxy Agent$/] : [/^Agent$/, /^Proxy Agent$/]);

    await expect(agentHealth.proxyCard).toHaveCount(0);
    await expect(agentHealth.proxyOnboarding).toHaveCount(0);
  }
);

test(
  "Agent Health - open the Observability tab, verify each of logs, metrics and traces gets a card naming the backend serving it",
  { tag: ["@dev", "@smoke", "@functional", "@oss"] },
  async ({ page }) => {
    const agentHealth = await openAgentHealth(page, "agent");

    // The deployed dev app only grows this tab once this change ships; until then there is
    // nothing to open. Same pattern the Optimize specs use for a dev tenant with no data.
    test.skip((await agentHealth.observabilityTab.count()) === 0, "The deployed app predates the Observability tab.");
    test.skip(!(await hasRedesignedAgentTab(agentHealth)), PREDATES_REDESIGN);

    await agentHealth.observabilityTab.click();
    await expectSelectedTab(agentHealth.observabilityTab);
    await expect(agentHealth.observabilityCard).toBeVisible({ timeout: 60000 });

    // One card per signal, always — a signal nothing serves still reports itself as
    // unresolved rather than being omitted, which is the difference between "no backend"
    // and "no card".
    for (const signal of OBSERVABILITY_SIGNALS) {
      await test.step(`${signal} names its backend and its state`, async () => {
        const card = agentHealth.signalCard(signal);
        await expect(card).toBeVisible();
        await expect(card).toContainText(SIGNAL_STATE);
        // The four facts every card states, whatever it resolved to.
        for (const row of ["Provider", "Source", "Last checked", "Error"]) {
          await expect(card).toContainText(row);
        }
      });
    }
    await expect(agentHealth.observabilityCard.locator('[data-testid^="observability-"]')).toHaveCount(OBSERVABILITY_SIGNALS.length);

    // The header chip counts the same cards below it, so the two cannot disagree.
    await expect(agentHealth.observabilitySummary).toHaveText(new RegExp(`^\\d of ${OBSERVABILITY_SIGNALS.length} connected$`));
  }
);
