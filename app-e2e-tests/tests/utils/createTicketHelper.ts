import { test, Page, Locator, expect } from "@playwright/test";
import { waitForGraphQLAndValidate } from "./GraphQLNetworkWatcher";

export const TICKET_TOOLS: Array<{
  label: string;
  configEnv: string;
  projectEnv?: string;
  issueEnv?: string;
}> = [
  { label: "Jira", configEnv: "JIRA_NAME", projectEnv: "JIRA_PROJECT_NAME", issueEnv: "JIRA_ISSUE_TYPE" },
  { label: "ServiceNow", configEnv: "SERVICE_NOW_NAME", projectEnv: "SERVICE_NOW_TABLE", issueEnv: "SERVICE_NOW_ISSUE_TYPE" },
  { label: "PagerDuty", configEnv: "PAGER_DUTY_NAME", projectEnv: "PAGER_DUTY_SERVICE_NAME", issueEnv: "PAGER_DUTY_ISSUE_TYPE" },
  { label: "GitHub", configEnv: "GITHUB_NAME", projectEnv: "GITHUB_REPO_NAME", issueEnv: "GITHUB_ISSUE_TYPE" },
  { label: "GitLab", configEnv: "GITLAB_INTEGRATION_NAME", projectEnv: "GITLAB_PROJECT_NAME", issueEnv: "GITLAB_ISSUE_TYPE" },
  { label: "ZenDuty", configEnv: "ZENDUTY_INTEGRATION_NAME", issueEnv: "ZENDUTY_ISSUE_TYPE" },
];

export interface TicketToolConfig {
  configName: string;
  projectName?: string;
  issueType?: string;
  toolLabel?: string;
}

export type TicketResult =
  | "created"
  | "commented"
  | "config_not_found"
  | "no_enabled_row";

async function sendSlackAlert(message: string): Promise<void> {
  const webhookUrl = process.env.SLACK_WEBHOOK_URL;
  if (!webhookUrl) {
    console.warn("[createTicket] SLACK_WEBHOOK_URL not set — skipping Slack alert");
    return;
  }
  try {
    const response = await fetch(webhookUrl, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ text: message }),
      signal: AbortSignal.timeout(5000),
    });
    if (!response.ok) {
      console.warn(`[createTicket] Slack alert failed: HTTP ${response.status}`);
    } else {
      console.log("[createTicket] Slack alert sent");
    }
  } catch (err) {
    console.warn(`[createTicket] Slack alert error: ${err}`);
  }
}

async function trySelectDropdownOption(
  page: Page,
  inputLocator: Locator,
  searchText?: string
): Promise<boolean> {
  await inputLocator.click();
  const listbox = page.locator('[role="listbox"]');
  await listbox.waitFor({ state: "visible", timeout: 10000 });

  if (searchText) {
    await page.keyboard.press("Control+a");
    await page.keyboard.type(searchText);
  }

  const firstOption = page.locator('[role="option"]').first();
  await firstOption.waitFor({ state: "visible", timeout: 5000 }).catch(() => {});
  const hasOptions = await firstOption.isVisible().catch(() => false);

  if (!hasOptions) {
    console.log(`[createTicket] No option for "${searchText}" — will skip`);
    await page.keyboard.press("Escape");
    await listbox.waitFor({ state: "hidden", timeout: 5000 }).catch(() => {});
    return false;
  }

  if (searchText) {
    const exactMatch = page
      .getByRole("option", { name: searchText, exact: true })
      .first();
    const exactVisible = await exactMatch.isVisible().catch(() => false);
    await (exactVisible ? exactMatch : firstOption).click({ force: true });
  } else {
    await firstOption.click({ force: true });
  }

  return true;
}

interface ScanResult {
  rowIndex: number | null;
  scannedCount: number;
}

async function findEnabledRowIndex(
  page: Page,
  maxRows: number
): Promise<ScanResult> {
  let scannedCount = 0;

  for (let i = 0; i < maxRows; i++) {
    const threeDotsBtn = page.locator('[aria-label="more"]').nth(i);
    const isVisible = await threeDotsBtn.isVisible().catch(() => false);
    if (!isVisible) break;

    scannedCount = i + 1;
    await threeDotsBtn.click();

    const menuItem = page.getByRole("menuitem", { name: "Create Ticket" });
    const appeared = await menuItem
      .waitFor({ state: "visible", timeout: 5000 })
      .then(() => true)
      .catch(() => false);

    if (!appeared) {
      await page.keyboard.press("Escape");
      await page.waitForTimeout(200);
      continue;
    }

    const isDisabled = await menuItem
      .evaluate((el) => el.getAttribute("aria-disabled") === "true")
      .catch(() => true);

    if (!isDisabled) {
      console.log(`[createTicket] Row ${i}: "Create Ticket" is enabled`);
      return { rowIndex: i, scannedCount };
    }

    console.log(`[createTicket] Row ${i}: already ticketed, trying next`);
    await page.keyboard.press("Escape");
    await page.waitForTimeout(300);
  }

  return { rowIndex: null, scannedCount };
}

async function verifyTicketInSidebar(
  page: Page,
  ticketId: string,
  toolLabel: string,
  testName: string
): Promise<void> {
  const anyDialog = page.locator('[role="dialog"]');
  const dialogOpen = await anyDialog.first().isVisible().catch(() => false);
  if (dialogOpen) {
    await page.keyboard.press('Escape');
    await anyDialog.first().waitFor({ state: "hidden", timeout: 5000 }).catch(() => {});
  }

  const ticketsBtn = page.locator("#tickets-sidenavbutton");
  await ticketsBtn.click();

  const listTicket = page.locator("#list-ticket");
  await listTicket.waitFor({ state: "visible", timeout: 20000 });

  const found = await listTicket
    .getByText(ticketId, { exact: true })
    .waitFor({ state: "visible", timeout: 15000 })
    .then(() => true)
    .catch(() => false);

  if (found) {
    console.log(`[createTicket] Ticket "${ticketId}" found in Tickets sidebar`);
  } else {
    console.warn(`[createTicket] Ticket "${ticketId}" NOT FOUND in Tickets sidebar`);
    await sendSlackAlert(
      `*Ticket Verification Failed*\n` +
        `Ticket *${ticketId}* was created via *${toolLabel}* but is not visible in the Tickets sidebar.\n` +
        `Test: ${testName}`
    );
  }
}

async function fillAndSubmitTicketDialog(
  page: Page,
  config: TicketToolConfig,
  testName: string
): Promise<"created" | "commented" | "config_not_found"> {
  const { configName, projectName, issueType } = config;

  const dialog = page
    .locator('[role="dialog"]')
    .filter({ has: page.locator("h2", { hasText: "Create Ticket" }) })
    .first();

  await dialog.waitFor({ state: "visible", timeout: 15000 });
  console.log(`[createTicket] Dialog visible`);

  const configInput = page.locator("#config");
  await configInput.waitFor({ state: "visible", timeout: 10000 });

  const configFound = await trySelectDropdownOption(page, configInput, configName);
  if (!configFound) {
    await dialog
      .getByRole("button", { name: /cancel/i })
      .click()
      .catch(() => {});
    return "config_not_found";
  }
  console.log(`[createTicket] Config: "${configName}"`);

  const projectInput = page.locator("#projectKey");
  await expect(projectInput).not.toBeDisabled({ timeout: 15000 });

  const autoProject = await projectInput.inputValue().catch(() => "");
  if (!autoProject) {
    await trySelectDropdownOption(page, projectInput, projectName);
    console.log(`[createTicket] Project: "${projectName ?? "(first option)"}"`);
  } else {
    console.log(`[createTicket] Project auto-selected: "${autoProject}"`);
  }

  const issueInput = page.locator("#issue");
  await expect(issueInput).not.toBeDisabled({ timeout: 30000 });

  const autoIssue = await issueInput.inputValue().catch(() => "");
  if (!autoIssue) {
    await trySelectDropdownOption(page, issueInput, issueType);
    console.log(`[createTicket] Issue type: "${issueType ?? "(first option)"}"`);
  } else {
    console.log(`[createTicket] Issue type auto-selected: "${autoIssue}"`);
  }

  let outcome: "created" | "commented" = "created";
  let toastText = "";

  await waitForGraphQLAndValidate(
    page,
    async () => {
      const submitBtn = dialog.getByRole("button", { name: "Create Ticket" });
      await submitBtn.waitFor({ state: "visible", timeout: 10000 });
      await submitBtn.click();
      console.log(`[createTicket] Submit clicked`);

      const toast = page.locator('[role="alert"]').filter({
        hasText: /created|Existing ticket found|added comment/i,
      });
      await toast.waitFor({ state: "visible", timeout: 30000 });

      toastText = await toast.first().innerText();
      console.log(`[createTicket] Toast: "${toastText}"`);
      if (/Existing ticket found|added comment/i.test(toastText)) {
        outcome = "commented";
      }
    },
    { testName, operationNames: ["CreateTicket"] }
  );

  if (outcome === "created") {
    const ticketIdMatch = toastText.match(/Ticket\s+(\S+)\s+created/i);
    const ticketId = ticketIdMatch?.[1] ?? null;

    if (ticketId) {
      const toolLabel = config.toolLabel ?? configName;
      await verifyTicketInSidebar(page, ticketId, toolLabel, testName);
    }
  }

  return outcome;
}

export async function createTicketFromThreeDotsMenu(
  page: Page,
  config: TicketToolConfig,
  testName: string,
  maxRows = 10
): Promise<TicketResult> {
  const { rowIndex, scannedCount } = await findEnabledRowIndex(page, maxRows);

  if (rowIndex === null) {
    const toolLabel = config.toolLabel ?? config.configName;
    console.warn(
      `[createTicket] All ${scannedCount} rows already ticketed — nothing to do`
    );
    await sendSlackAlert(
      `*Ticket Scan Alert*\n` +
        `Scanned *${scannedCount}* row(s) in All Events but could not find an event ` +
        `for which a *${toolLabel}* ticket can be created.\n` +
        `All scanned rows already have tickets.\n` +
        `Test: ${testName}`
    );
    return "no_enabled_row";
  }

  const menuItem = page.getByRole("menuitem", { name: "Create Ticket" });
  await menuItem.click();
  console.log(`[createTicket] "Create Ticket" clicked on row ${rowIndex}`);

  return fillAndSubmitTicketDialog(page, config, testName);
}

export async function createTicketFromButton(
  page: Page,
  triggerLocator: Locator,
  config: TicketToolConfig,
  testName: string
): Promise<Exclude<TicketResult, "no_enabled_row">> {
  await triggerLocator.waitFor({ state: "visible", timeout: 15000 });
  await triggerLocator.click();
  console.log(`[createTicket] Direct "Create Ticket" button clicked`);

  return fillAndSubmitTicketDialog(page, config, testName) as Promise<
    Exclude<TicketResult, "no_enabled_row">
  >;
}

export function registerTicketCreationTestsFromButton(
  navigateFn: (page: Page) => Promise<void>,
  getButtonLocator: (page: Page) => Locator,
  testPrefix: string
): void {
  for (const tool of TICKET_TOOLS) {
    test(`Create Ticket - ${testPrefix} - ${tool.label}`, async ({ page }, testInfo) => {
      test.setTimeout(180000);

      const configName = process.env[tool.configEnv];
      if (!configName) {
        test.skip(true, `Skipped: env var ${tool.configEnv} is not set`);
        return;
      }

      const config: TicketToolConfig = {
        configName,
        projectName: tool.projectEnv ? process.env[tool.projectEnv] : undefined,
        issueType: tool.issueEnv ? process.env[tool.issueEnv] : undefined,
        toolLabel: tool.label,
      };

      try {
        await navigateFn(page);
        const result = await createTicketFromButton(page, getButtonLocator(page), config, testInfo.title);

        test.skip(
          result === "config_not_found",
          `Config "${configName}" not found in dropdown — ${tool.label} integration may not be configured in this environment`
        );
      } catch (err) {
        await sendSlackAlert(
          `*Ticket Creation Failed*\n` +
            `Tool: *${tool.label}*\n` +
            `Tab: *${testPrefix}*\n` +
            `Test: ${testInfo.title}\n` +
            `Error: ${err instanceof Error ? err.message : String(err)}`
        );
        throw err;
      }
    });
  }
}

export function registerTicketCreationTests(
  navigateFn: (page: Page) => Promise<void>,
  testPrefix: string
): void {
  for (const tool of TICKET_TOOLS) {
    test(`Create Ticket - ${testPrefix} - ${tool.label}`, async ({ page }, testInfo) => {
      test.setTimeout(180000);

      const configName = process.env[tool.configEnv];
      if (!configName) {
        test.skip(true, `Skipped: env var ${tool.configEnv} is not set`);
        return;
      }

      const config: TicketToolConfig = {
        configName,
        projectName: tool.projectEnv ? process.env[tool.projectEnv] : undefined,
        issueType: tool.issueEnv ? process.env[tool.issueEnv] : undefined,
        toolLabel: tool.label,
      };

      try {
        await navigateFn(page);

        const result = await createTicketFromThreeDotsMenu(page, config, testInfo.title);

        test.skip(
          result === "config_not_found",
          `Config "${configName}" not found in dropdown — ${tool.label} integration may not be configured in this environment`
        );
        test.skip(
          result === "no_enabled_row",
          `All visible event rows are already ticketed — no row with enabled "Create Ticket" found`
        );
      } catch (err) {
        await sendSlackAlert(
          `*Ticket Creation Failed*\n` +
            `Tool: *${tool.label}*\n` +
            `Tab: *${testPrefix}*\n` +
            `Test: ${testInfo.title}\n` +
            `Error: ${err instanceof Error ? err.message : String(err)}`
        );
        throw err;
      }
    });
  }
}
