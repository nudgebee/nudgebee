// Not for OSS
import { Page, Locator, expect } from "@playwright/test";
import { LoginPage } from "../../../pages/LoginPage";
import { AiGatewayLocators, GatewayScreen, GatewaySection, hasInnerStrip, screenHash, sectionOf } from "./aiGatewayLocators";

// playwright.config.ts sets `baseURL: process.env.BASE_URL`, so an unset BASE_URL does not
// fail here — it fails later as a goto against "about:blank". Named up front so the run
// says which key is missing instead of reporting an unexplained navigation error.
const BASE_URL = process.env.BASE_URL || "";
if (!BASE_URL) {
  throw new Error("BASE_URL is not set — add it to .env / .env.dev");
}

// Preconditions named once, so an environment that never rendered the module fails with the
// reason rather than with an unexplained missing element on every test.
const NO_TAB_HINT =
  "The AI Gateway section strip never rendered. app/src/pages/ai-gateway/index.tsx shows it only when the " +
  "deployment sets UI_ENABLE_LLM_GATEWAY=true — otherwise the page renders an 'AI Gateway is not available' " +
  "empty state. Check that toggle on this deployment before reading this as a module failure.";

const NO_ROOT_HINT =
  "The AI Gateway body never mounted. GatewayUsage.tsx renders #gateway-usage-root unconditionally once " +
  "the page passes its gate, so this means the strip rendered but the view failed to load.";

// Lands on the module by hash rather than by clicking the strips. The page resolves the
// section and GatewayUsage the screen from the hash, so `#monitor/models` opens that screen
// directly. Clicking the strips is exercised on its own by the navigation test.
export async function openGateway(page: Page, screen?: GatewayScreen): Promise<AiGatewayLocators> {
  const locators = new AiGatewayLocators(page);
  await new LoginPage(page).doFullLogin();

  await page.goto(`/ai-gateway${screen ? screenHash(screen) : ""}`);
  await expect(locators.sectionTab(screen ? sectionOf(screen) : "monitor"), NO_TAB_HINT).toBeVisible({ timeout: 60000 });
  await expect(locators.root, NO_ROOT_HINT).toBeVisible({ timeout: 60000 });

  return locators;
}

// Lands on Monitor with a screen fragment the module does not recognise, so the caller can
// assert what the rejection does. Deliberately does not wait for a selected screen — which
// one ends up selected is what the test observes.
export async function openGatewayWithFragment(page: Page, fragment: string): Promise<AiGatewayLocators> {
  const locators = new AiGatewayLocators(page);
  await new LoginPage(page).doFullLogin();

  await page.goto(`/ai-gateway#monitor/${fragment}`);
  await expect(locators.sectionTab("monitor"), NO_TAB_HINT).toBeVisible({ timeout: 60000 });
  await expect(locators.root, NO_ROOT_HINT).toBeVisible({ timeout: 60000 });

  return locators;
}

// Lands through a link to the old Optimize tab, which optimise/index.jsx redirects here.
export async function openGatewayFromOldLink(page: Page, screen: GatewayScreen): Promise<AiGatewayLocators> {
  const locators = new AiGatewayLocators(page);
  await new LoginPage(page).doFullLogin();

  await page.goto(`/optimise#ai-gateway/${screen}`);
  await page.waitForURL(/\/ai-gateway#/, { timeout: 60000 });
  await expect(locators.root, NO_ROOT_HINT).toBeVisible({ timeout: 60000 });

  return locators;
}

// AnchorComponent marks the open section with data-tab-selected on its tab.
export async function expectSectionSelected(locators: AiGatewayLocators, section: GatewaySection, selected = true): Promise<void> {
  await expect(locators.sectionTab(section)).toHaveAttribute("data-tab-selected", String(selected), { timeout: 30000 });
}

// A screen is on show when its section is open and — where the section has an inner strip —
// its tab there is selected. MUI Tab's aria-selected is always in step with what renders.
export async function expectScreenSelected(locators: AiGatewayLocators, screen: GatewayScreen): Promise<void> {
  await expectSectionSelected(locators, sectionOf(screen));
  if (hasInnerStrip(screen)) {
    await expect(locators.screenTab(screen)).toHaveAttribute("aria-selected", "true", { timeout: 30000 });
  }
}

// Opens a screen the way a user does: its section on the page strip first when another
// section is open, then its tab on the inner strip when the section has one.
export async function openScreen(locators: AiGatewayLocators, screen: GatewayScreen): Promise<void> {
  const section = locators.sectionTab(sectionOf(screen));
  if ((await section.getAttribute("data-tab-selected")) !== "true") {
    await section.click();
    await expectSectionSelected(locators, sectionOf(screen));
  }
  if (hasInnerStrip(screen)) {
    await locators.screenTab(screen).click();
  }
  await expectScreenSelected(locators, screen);
}

// The checked option of a ds/ToggleGroup. Each option is a ButtonBase with role="radio"
// carrying aria-checked, so this reads the group's committed state rather than its styling.
export async function expectToggleChecked(option: Locator, checked: boolean): Promise<void> {
  await expect(option).toHaveAttribute("aria-checked", String(checked), { timeout: 30000 });
}

// Picks one value in a FilterDropdown: open the trigger, click the option, and wait for the
// panel to close so the next interaction is not swallowed by the Popover's backdrop.
export async function selectFilterOption(locators: AiGatewayLocators, trigger: Locator, label: string): Promise<void> {
  await trigger.click();
  const option = locators.filterOption(label);
  await expect(option).toBeVisible({ timeout: 30000 });
  await option.click();
  await expect(option).toBeHidden({ timeout: 30000 });
}

// Park the cursor away from the strips and the sidebar. Parked at 0,0 it sits on the sidebar
// rail, whose hover flyout is a Popover with an invisible backdrop that swallows clicks
// page-wide. Mid-viewport is clear of both.
export async function parkCursor(page: Page): Promise<void> {
  await page.mouse.move(640, 500);
}
