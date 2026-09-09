// Not for OSS
import { Page, APIRequestContext, expect } from "@playwright/test";
import { StatusLocators } from "./statusLocators";
import { STATUS_API_PATH, STATUS_PAGE_PATH, StatusPayload, assertStatusPayload } from "./statusConstants";

// Opens the public status page and waits for it to settle into a rendered state.
//
// No login step: /status is matched exactly by the public-route branch in
// app/src/pages/_app.tsx, so it renders bare with no session, cluster selection or
// DataContext. Tests still start from global-setup's storageState, which is harmless
// here — the page behaves identically either way, and the signed-out case is covered
// explicitly by its own spec.
export async function openStatusPage(page: Page): Promise<StatusLocators> {
  const locators = new StatusLocators(page);
  await page.goto(STATUS_PAGE_PATH);

  // The summary bar renders immediately with `unknown` and is repainted once the
  // first fetch lands, so its presence is the page being mounted, not it being ready.
  await expect(locators.summaryBar).toBeVisible();
  return locators;
}

// Reads the same endpoint the page reads, so a spec can assert the UI against the
// payload behind it rather than against whatever the dev cluster happens to be doing
// today. The `request` fixture is its own context and carries none of the page's
// cookies, which is fine and on purpose here: the endpoint is unauthenticated, so an
// anonymous read is the same read the page makes.
export async function fetchStatusPayload(request: APIRequestContext): Promise<StatusPayload> {
  const response = await request.get(STATUS_API_PATH, { headers: { accept: "application/json" } });
  expect(response.status(), `GET ${STATUS_API_PATH} did not answer 200`).toBe(200);

  const payload = await response.json();
  assertStatusPayload(payload);
  return payload;
}

// Waits until the page has painted a real payload rather than its initial `unknown`
// placeholder: the components card swaps its loading line for one row per component.
export async function waitForComponentsRendered(locators: StatusLocators): Promise<void> {
  await expect(locators.componentsPlaceholder).toHaveCount(0);
}
