// Not for OSS
import { Page, Locator, expect } from "@playwright/test";
import { LoginPage } from "../../pages/LoginPage";
import { TicketsLocators } from "./ticketsLocators";

// Precondition for every test here: the signed-in user can reach the Tickets module. The
// sidebar entry is gated on the `tickets` module grant (app/src/components/common/layout/
// index.jsx), and withAuth bounces a user without it — so a permissions problem otherwise
// surfaces as an unexplained missing toolbar on every test in the file.
const NO_MODULE_HINT =
  "The ticket listing did not render. /tickets is gated on the `tickets` module grant — " +
  "confirm the run's LDAP user still holds it before treating this as a UI regression.";

// Separate hint for the cases that need a ticket to act on, so an environment that simply
// holds no tickets reports that rather than a locator that 'did not appear'.
const NO_TICKETS_HINT =
  "The ticket listing is empty. This case needs at least one ticket on the environment — " +
  "the suite deliberately creates none, because every write on this module lands in a real " +
  "Jira/GitHub/PagerDuty project it cannot clean up.";

// Lands on the ticket listing the way a person does and waits for the first fetch.
export async function openTickets(page: Page): Promise<TicketsLocators> {
  const tickets = new TicketsLocators(page);
  await new LoginPage(page).doFullLogin();

  await page.goto("/tickets");
  await expect(tickets.listingRoot, NO_MODULE_HINT).toBeVisible({ timeout: 60000 });
  await tickets.waitForTable();

  // Park the cursor in open content. Left where a click put it, AnchorComponent opens its
  // tab popover over the toolbar below and the next click hits that instead.
  await page.mouse.move(640, 500);

  return tickets;
}

export async function requireTickets(tickets: TicketsLocators): Promise<void> {
  await expect(tickets.rows.first(), NO_TICKETS_HINT).toBeVisible({ timeout: 30000 });
}

// AnchorComponent marks the current tab with data-tab-selected="true". This is the app's
// own notion of which tab is open and, unlike the URL, is always in step with what is
// rendered — so it is what "which tab am I on" is asserted against.
export async function expectSelectedTab(tab: Locator): Promise<void> {
  await expect(tab).toHaveAttribute("data-tab-selected", "true", { timeout: 15000 });
}

// The distinct statuses currently on screen, lowercased. Read through expect.poll by the
// callers so the retry rides out the refetch a filter change kicks off.
export async function distinctStatuses(tickets: TicketsLocators): Promise<string[]> {
  const cells = await tickets.statusCells.allTextContents();
  return [...new Set(cells.map((cell) => cell.trim().toLowerCase()).filter(Boolean))];
}
