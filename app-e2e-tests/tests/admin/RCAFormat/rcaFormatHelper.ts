// Not for OSS

import { Page, expect } from "@playwright/test";
import { RCAFormatLocators } from "./rcaFormatLocators";

// Lands on Admin -> AI & Tools -> RCA Format with no account picked, which is
// the sub-tab's own initial state.
export async function openRCAFormat(page: Page): Promise<RCAFormatLocators> {
  const locators = new RCAFormatLocators(page);
  await locators.open();
  return locators;
}

// Reloads the page and waits for the tab shell to come back. This is what
// separates a persisted template from one still held in React state: the reload
// remounts RCAFormatTab, which refetches getRcaFormat, and the hash survives it
// so the sub-tab resolves again.
export async function reloadRCAFormat(page: Page): Promise<RCAFormatLocators> {
  await page.reload({ waitUntil: "domcontentloaded" });
  const locators = new RCAFormatLocators(page);
  await locators.waitForTabShell();
  return locators;
}

// A marker line unique to one test run, so two runs against the same shared dev
// account can never read each other's writes. Deliberately limited to uppercase
// letters, digits and hyphens: CodeMirror's basicSetup enables closeBrackets, so
// a marker containing a quote or an opening bracket would gain a second
// character the moment it was typed and no longer match what was asserted.
export function uniqueMarker(): string {
  return `E2E-RCA-MARKER-${Date.now()}-${Math.floor(Math.random() * 100000)}`;
}

// Writes the marker as the document's FIRST line. Position matters: CodeMirror
// only renders the lines currently in the viewport, so a marker appended to the
// end of a long template would be absent from the DOM — and therefore from any
// assertion — until the editor was scrolled. Line 1 is always painted.
export async function insertMarkerLine(locators: RCAFormatLocators, marker: string): Promise<void> {
  await locators.editor.click();
  await locators.editor.press("ControlOrMeta+Home");
  await locators.editor.pressSequentially(marker, { delay: 10 });
  await locators.editor.press("Enter");
  await expect(locators.editor).toContainText(marker, { timeout: 30000 });
}

// Removes the first line, including its newline. Used to undo insertMarkerLine
// without ever rewriting the rest of the template: the account's real content
// may be longer than the viewport, so selecting all and retyping it would
// silently truncate whatever CodeMirror had not rendered.
export async function deleteFirstLine(locators: RCAFormatLocators): Promise<void> {
  await locators.editor.click();
  await locators.editor.press("ControlOrMeta+Home");
  await locators.editor.press("Shift+ArrowDown");
  await locators.editor.press("Delete");
}

// Saves and waits for the write to be acknowledged. The button going back to
// disabled is the second half of the signal: handleSave re-baselines savedFormat
// only after updateRcaFormat resolved with data, so a disabled button means the
// mutation succeeded rather than merely that a toast was painted.
export async function saveTemplate(locators: RCAFormatLocators, toastText: string): Promise<void> {
  await expect(locators.saveChangesBtn).toBeEnabled({ timeout: 30000 });
  await locators.saveChangesBtn.click();
  await expect(locators.toastWithText(toastText)).toBeVisible({ timeout: 60000 });
  await expect(locators.saveChangesBtn).toBeDisabled({ timeout: 60000 });
}

// Best-effort teardown for the one case that writes. Re-picks the account, drops
// the marker line if it is still there, and saves — so a run that failed midway
// cannot leave the shared dev account carrying this suite's marker.
export async function restoreTemplateIfMarked(
  page: Page,
  accountLabel: string,
  marker: string,
  toastText: string
): Promise<void> {
  try {
    const locators = await reloadRCAFormat(page);
    await locators.chooseAccount(accountLabel);
    // chooseAccount already awaited waitForEditorBody, so the template on screen
    // is final and a direct read answers this — waiting on the marker instead
    // would burn its whole timeout in the finally of every run that failed
    // before the save, which is the common case. Safe against CodeMirror's
    // viewport rendering because insertMarkerLine puts the marker on line 1.
    const content = (await locators.editor.textContent()) ?? "";
    if (!content.includes(marker)) return;
    await deleteFirstLine(locators);
    await saveTemplate(locators, toastText);
    await expect(locators.editor).not.toContainText(marker, { timeout: 30000 });
  } catch (error) {
    console.warn(`[cleanup] the RCA Format marker line could not be removed: ${error}`);
  }
}
