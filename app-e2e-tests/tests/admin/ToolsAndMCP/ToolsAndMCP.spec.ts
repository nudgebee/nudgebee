// Not for OSS

import { test, expect } from "@playwright/test";
import {
  ARIA_DELETE_TOOL,
  BADGE_SYSTEM_WORD,
  BADGE_USER_CREATED,
  BUTTON_DELETE,
  CREATED_BY_SYSTEM,
  CREATED_BY_USER,
  CRUD_TIMEOUT_MS,
  CRUD_TOOL_IMAGE,
  ERROR_CONTAINER_IMAGE_EMPTY,
  ERROR_DESCRIPTION_EMPTY,
  ERROR_NAME_MUST_START_WITH_LETTER,
  ERROR_NAME_REQUIRED,
  FIELD_CONTAINER_IMAGE,
  FIELD_DESCRIPTION,
  FIELD_NAME,
  FILTER_SETTLE_MS,
  HEADER_ACCOUNT,
  HEADER_ACTIONS,
  HEADER_DESCRIPTION,
  HEADER_NAME,
  HEADER_NB_TOOL_TYPE,
  HEADER_STATUS,
  MCP_BANNER_ACTION,
  MCP_BANNER_TEXT,
  MCP_HEADER_CONNECTION,
  MCP_HEADER_CREATED_BY,
  MCP_STATUS_DISABLED,
  MCP_STATUS_ENABLED,
  MCP_REFETCH_TIMEOUT_MS,
  SPEC_TIMEOUT_MS,
  TOAST_CREATED,
} from "./toolsAndMcpConstants";
import {
  deleteToolIfPresent,
  firstAccountLabel,
  findToolByStoredName,
  firstSearchableToolWord,
  openToolsAndMcp,
  reloadToolsAndMcp,
  searchTools,
  uniqueToolName,
  visibleMcpStatuses,
  visibleToolNameCells,
  visibleToolStatuses,
} from "./toolsAndMcpHelper";

// Admin -> AI & Tools -> Tools & MCP
// (app/src/components/llm/admin/ToolsAndMCPAdminTab.jsx, which mounts ListTools
// for the Tools sub-view and MCPConfigList for the read-only MCP Servers one).
//
// The tab opens tenant-wide: ToolsAndMCPAdminTab starts with accountId '', which
// puts ListTools in its isTenantWide branch — an Account column instead of
// NB Tool Type + Actions, and no Create Tool. Picking an account in the header
// filter is what swaps the mount, so several cases below turn on that difference.
//
// This runs against a shared tenant, so exactly one case writes: it registers a
// container tool under a name unique to the run and deletes it again in the same
// test, with a finally arm that cleans up if an assertion fails part-way. Every
// other case either reads, or stops at a client-side guard in CreateTool's
// validateForm that returns before apiAskNudgebee.createTool is ever called.

test.describe("Admin AI & Tools - Tools & MCP", () => {
  test(
    "Tools & MCP sanity - deep-link to Admin AI & Tools Tools & MCP, verify the Tools toggle is selected and the tenant-wide listing renders Name, Description, Status and Account columns with no Create Tool control",
    { tag: ["@dev", "@sanity", "@functional"] },
    async ({ page }) => {
      test.setTimeout(SPEC_TIMEOUT_MS);
      const locators = await openToolsAndMcp(page);

      // The hash carries both levels, so landing on the right sub-tab is itself
      // part of what this asserts — a broken child fragment silently falls back
      // to the first sub-tab (Agents), which renders none of the below.
      await expect(locators.aiToolsTab).toHaveAttribute("data-tab-selected", "true", { timeout: 30000 });
      await expect(locators.toolsMcpTab).toHaveAttribute("aria-selected", "true", { timeout: 30000 });
      await expect(locators.mcpToggle).toHaveAttribute("aria-checked", "false", { timeout: 30000 });

      await expect(locators.toolsHeader(HEADER_NAME)).toBeVisible({ timeout: 60000 });
      await expect(locators.toolsHeader(HEADER_DESCRIPTION)).toBeVisible();
      await expect(locators.toolsHeader(HEADER_STATUS)).toBeVisible();
      // The Account column exists only in the tenant-wide read, and the two
      // per-account columns exist only outside it — so this pair is what pins
      // which branch of ListTools actually mounted.
      await expect(locators.toolsHeader(HEADER_ACCOUNT)).toBeVisible();
      await expect(locators.toolsHeader(HEADER_NB_TOOL_TYPE)).toHaveCount(0);
      await expect(locators.toolsHeader(HEADER_ACTIONS)).toHaveCount(0);
      // Create is per-account, so the tenant-wide mount hides it alongside the
      // per-row Edit and Delete controls.
      await expect(locators.createToolBtn).toHaveCount(0);
    }
  );

  test(
    "Tools & MCP sanity - switch the toggle to MCP Servers and back to Tools, verify the read-only banner and MCP columns replace the tools listing and the tools search box comes back",
    { tag: ["@dev", "@sanity", "@functional"] },
    async ({ page }) => {
      test.setTimeout(SPEC_TIMEOUT_MS);
      const locators = await openToolsAndMcp(page);

      await locators.showMcpServers();
      // ToolsAndMCPAdminTab renders one branch or the other, never both, so the
      // tools search going away is what proves the body actually swapped rather
      // than the toggle merely repainting. showMcpServers already asserts the
      // tools listing container is gone.
      await expect(locators.toolSearch).toHaveCount(0, { timeout: 30000 });
      await expect(locators.mcpBanner).toBeVisible({ timeout: 30000 });
      await expect(locators.mcpBanner).toContainText(MCP_BANNER_TEXT);
      await expect(locators.mcpHeader(MCP_HEADER_CONNECTION)).toBeVisible({ timeout: 60000 });
      await expect(locators.mcpHeader(MCP_HEADER_CREATED_BY)).toBeVisible();

      await locators.showTools();
      await expect(locators.toolSearch).toBeVisible({ timeout: 30000 });
      await expect(locators.toolsHeader(HEADER_ACCOUNT)).toBeVisible({ timeout: 60000 });
      // The banner belongs to the MCP branch only, so it has to be gone once the
      // Tools sub-view is back.
      await expect(locators.mcpBanner).toHaveCount(0, { timeout: 30000 });
    }
  );

  test(
    "Tools & MCP sanity - open the MCP Servers sub-view, verify the account filter is hidden and the read-only banner offers the Manage in Integrations action",
    { tag: ["@dev", "@sanity", "@functional"] },
    async ({ page }) => {
      test.setTimeout(SPEC_TIMEOUT_MS);
      const locators = await openToolsAndMcp(page);

      // The account filter narrows the Tools sub-view only, so ToolsAndMCPAdminTab
      // renders it beside the toggle on that branch and drops it on the MCP one.
      await expect(locators.accountFilter).toBeVisible({ timeout: 30000 });
      await locators.showMcpServers();
      await expect(locators.accountFilter).toHaveCount(0, { timeout: 30000 });

      await expect(locators.mcpBanner).toBeVisible({ timeout: 30000 });
      // The banner's action is the tab's one pointer to the surface that can
      // actually manage MCP servers, so its absence would leave the read-only
      // view a dead end.
      await expect(locators.mcpBanner.getByRole("button", { name: MCP_BANNER_ACTION })).toBeVisible({ timeout: 30000 });
      // MCPConfigList defaults its status filter to Enabled rather than to no
      // filter, which is what the listing's row set is scoped by on arrival.
      await expect(locators.mcpStatusFilter).toContainText(MCP_STATUS_ENABLED, { timeout: 30000 });
    }
  );

  test(
    "Tools & MCP - type a tool name word into Search Tool, verify only tools whose name contains that word are listed, then clear the box and verify the full catalogue returns",
    { tag: ["@dev", "@smoke", "@search"] },
    async ({ page }) => {
      test.setTimeout(SPEC_TIMEOUT_MS);
      const locators = await openToolsAndMcp(page);

      const beforeCount = await locators.toolsRows.count();
      expect(beforeCount, "the tenant's tool catalogue is empty, so there is nothing to search").toBeGreaterThan(0);

      // The term is taken from a row that is really there rather than invented,
      // so this asserts filtering rather than the empty state.
      const term = await firstSearchableToolWord(locators);
      expect(term, "no tool name offered a word long enough to search on").not.toBe("");

      await searchTools(locators, term);
      // The filter runs in a useEffect, which React flushes after the paint that
      // already showed the unfiltered rows — so a single read can catch the old
      // set. toPass re-reads until the committed rows agree with the term.
      await expect(async () => {
        const matched = await visibleToolNameCells(locators);
        expect(matched.length).toBeGreaterThan(0);
        // ListTools matches the search against both the stored snake_case name
        // and the title-cased form it renders, so every surviving row has to
        // carry the term in the Name cell.
        for (const cell of matched) {
          expect(cell.toLowerCase()).toContain(term.toLowerCase());
        }
      }).toPass({ timeout: FILTER_SETTLE_MS });

      await searchTools(locators, "");
      await expect(locators.toolsRows).toHaveCount(beforeCount, { timeout: FILTER_SETTLE_MS });
    }
  );

  test(
    "Tools & MCP - filter Created By to User Created, verify every listed tool carries the User Created badge, then switch to System Generated and verify none of them do",
    { tag: ["@dev", "@regression", "@functional"] },
    async ({ page }) => {
      test.setTimeout(SPEC_TIMEOUT_MS);
      const locators = await openToolsAndMcp(page);
      await expect(locators.createdByFilter).toBeVisible({ timeout: 60000 });

      await locators.chooseFilterOption(locators.createdByFilter, CREATED_BY_USER);
      await expect(locators.toolsTableBody.or(locators.toolsEmptyState)).toBeVisible({ timeout: FILTER_SETTLE_MS });
      // Same post-paint useEffect race as the search case, so the read retries.
      await expect(async () => {
        const userRows = await visibleToolNameCells(locators);
        // A shared tenant may legitimately hold no custom tools, so an empty
        // result is accepted — what is asserted is that nothing mismatching
        // survived.
        for (const cell of userRows) {
          expect(cell).toMatch(BADGE_USER_CREATED);
        }
      }).toPass({ timeout: FILTER_SETTLE_MS });

      await locators.chooseFilterOption(locators.createdByFilter, CREATED_BY_SYSTEM);
      await expect(locators.toolsTableBody.or(locators.toolsEmptyState)).toBeVisible({ timeout: FILTER_SETTLE_MS });
      await expect(async () => {
        const systemRows = await visibleToolNameCells(locators);
        expect(systemRows.length).toBeGreaterThan(0);
        // OwnerTypeBadge builds the system label as `${brandTitle} System`, so
        // the tenant's branding word is not pinned — only the fixed half is,
        // plus the absence of the custom label that the other branch renders.
        for (const cell of systemRows) {
          expect(cell).not.toMatch(BADGE_USER_CREATED);
          expect(cell).toMatch(BADGE_SYSTEM_WORD);
        }
      }).toPass({ timeout: FILTER_SETTLE_MS });
    }
  );

  test(
    "Tools & MCP - filter Status to the first status the catalogue offers, verify every listed tool reports that status in its Status column",
    { tag: ["@dev", "@regression", "@functional"] },
    async ({ page }) => {
      test.setTimeout(SPEC_TIMEOUT_MS);
      const locators = await openToolsAndMcp(page);
      await expect(locators.statusFilter).toBeVisible({ timeout: 60000 });

      // ListTools derives these options from the loaded rows rather than
      // hard-coding them, so the case works whatever statuses this tenant holds.
      const statuses = await locators.statusFilterOptionLabels();
      expect(statuses.length, "the Status filter offered no options, so no tool carried a status").toBeGreaterThan(0);
      const picked = statuses[0];

      await locators.chooseFilterOption(locators.statusFilter, picked);
      await expect(locators.toolsTableBody.or(locators.toolsEmptyState)).toBeVisible({ timeout: FILTER_SETTLE_MS });

      // The filter option's label is snakeToTitleCase(status) while the Status
      // column renders the raw stored value, so the two are compared case- and
      // separator-insensitively rather than as literals.
      const normalise = (value: string) => value.toLowerCase().replace(/[\s_]+/g, "");
      await expect(async () => {
        const shown = await visibleToolStatuses(locators);
        expect(shown.length).toBeGreaterThan(0);
        for (const status of shown) {
          expect(normalise(status)).toBe(normalise(picked));
        }
      }).toPass({ timeout: FILTER_SETTLE_MS });
    }
  );

  test(
    "Tools & MCP - narrow the account filter from All accounts to a single account, verify the listing swaps the Account column for NB Tool Type and Actions and reveals the Create Tool control",
    { tag: ["@dev", "@regression", "@functional"] },
    async ({ page }) => {
      test.setTimeout(SPEC_TIMEOUT_MS);
      const locators = await openToolsAndMcp(page);

      await expect(locators.toolsHeader(HEADER_ACCOUNT)).toBeVisible({ timeout: 60000 });
      const account = await firstAccountLabel(locators);
      expect(account, "the account filter offered no account beyond the All accounts entry").not.toBe("");

      await locators.chooseAccount(account);
      await locators.waitForToolsBody();

      // ListTools re-fetches on accountId and re-renders in its per-account
      // shape, so these three moving together is what proves the filter reached
      // the mount rather than only the trigger's own label.
      await expect(locators.toolsHeader(HEADER_NB_TOOL_TYPE)).toBeVisible({ timeout: 60000 });
      await expect(locators.toolsHeader(HEADER_ACTIONS)).toBeVisible();
      await expect(locators.toolsHeader(HEADER_ACCOUNT)).toHaveCount(0);
      await expect(locators.createToolBtn).toBeVisible({ timeout: 30000 });
    }
  );

  test(
    "Tools & MCP - open Create Tool and submit it empty, verify the Name, Description and Container Image rejections, that the dialog stays open and that no tool created toast appears",
    { tag: ["@dev", "@regression", "@negative", "@validation"] },
    async ({ page }) => {
      test.setTimeout(SPEC_TIMEOUT_MS);
      const locators = await openToolsAndMcp(page);

      const account = await firstAccountLabel(locators);
      expect(account, "the account filter offered no account beyond the All accounts entry").not.toBe("");
      await locators.chooseAccount(account);
      await locators.waitForToolsBody();
      await locators.openCreateDialog();

      await locators.dialogSubmitBtn.click();

      // validateForm runs all three guards and returns false before
      // apiAskNudgebee.createTool is reached, so all three messages are expected
      // together rather than one at a time.
      await expect(locators.dialogError(ERROR_NAME_REQUIRED)).toBeVisible({ timeout: 30000 });
      await expect(locators.dialogError(ERROR_DESCRIPTION_EMPTY)).toBeVisible({ timeout: 30000 });
      await expect(locators.dialogError(ERROR_CONTAINER_IMAGE_EMPTY)).toBeVisible({ timeout: 30000 });

      await expect(locators.createDialog).toBeVisible();
      // The guard returns before the create call, so the absence of the success
      // toast is what proves nothing was written.
      await expect(locators.toastWithText(TOAST_CREATED)).toHaveCount(0);
    }
  );

  test(
    "Tools & MCP - open Create Tool and enter a name that starts with a digit, verify the name must start with a letter rejection and that Description and Container Image are not blamed",
    { tag: ["@dev", "@regression", "@negative", "@validation"] },
    async ({ page }) => {
      test.setTimeout(SPEC_TIMEOUT_MS);
      const locators = await openToolsAndMcp(page);

      const account = await firstAccountLabel(locators);
      expect(account, "the account filter offered no account beyond the All accounts entry").not.toBe("");
      await locators.chooseAccount(account);
      await locators.waitForToolsBody();
      await locators.openCreateDialog();

      // getLlmIdentifierValidationMessage checks the leading character before the
      // length and character-set rules, so this input names exactly one failure.
      await locators.dialogField(FIELD_NAME).fill("9invalid_name");
      await locators.dialogField(FIELD_DESCRIPTION).fill("Rejected before any network call");
      await locators.dialogField(FIELD_CONTAINER_IMAGE).fill(CRUD_TOOL_IMAGE);

      await expect(locators.dialogError(ERROR_NAME_MUST_START_WITH_LETTER)).toBeVisible({ timeout: 30000 });
      // The name error is raised on change, so the other two fields being clean
      // is what separates a targeted rejection from a blanket one.
      await expect(locators.dialogError(ERROR_DESCRIPTION_EMPTY)).toHaveCount(0);
      await expect(locators.dialogError(ERROR_CONTAINER_IMAGE_EMPTY)).toHaveCount(0);

      await locators.dialogSubmitBtn.click();
      await expect(locators.createDialog).toBeVisible();
      await expect(locators.toastWithText(TOAST_CREATED)).toHaveCount(0);
    }
  );

  test(
    "Tools & MCP - open Create Tool, fill the name and image, cancel the dialog, reopen it and verify the form is back to empty with no tool created toast",
    { tag: ["@dev", "@regression", "@functional"] },
    async ({ page }) => {
      test.setTimeout(SPEC_TIMEOUT_MS);
      const locators = await openToolsAndMcp(page);

      const account = await firstAccountLabel(locators);
      expect(account, "the account filter offered no account beyond the All accounts entry").not.toBe("");
      await locators.chooseAccount(account);
      await locators.waitForToolsBody();
      await locators.openCreateDialog();

      const abandoned = uniqueToolName();
      await locators.dialogField(FIELD_NAME).fill(abandoned);
      await locators.dialogField(FIELD_CONTAINER_IMAGE).fill(CRUD_TOOL_IMAGE);
      await expect(locators.dialogField(FIELD_NAME)).toHaveValue(abandoned);

      await locators.dialogCancelBtn.click();
      await locators.createDialog.waitFor({ state: "detached", timeout: 30000 });
      // Cancel never calls apiAskNudgebee.createTool, so the absent toast and the
      // absent row are the two halves of "no side effect".
      await expect(locators.toastWithText(TOAST_CREATED)).toHaveCount(0);
      // Searched by the stored name through the app's own matcher, so this is a
      // real absence check rather than one against a display name this suite
      // guessed at — snakeToTitleCase uppercases parts like "mcp", so a
      // hand-rolled label would make the assertion vacuously true.
      await findToolByStoredName(locators, abandoned);
      await expect(locators.toolsRows).toHaveCount(0, { timeout: 30000 });
      await expect(locators.toolsEmptyState).toBeVisible({ timeout: 30000 });
      await searchTools(locators, "");

      await locators.openCreateDialog();
      // CreateTool's Cancel resets its own state before closing, so the reopened
      // form has to come up empty rather than carrying the abandoned draft.
      await expect(locators.dialogField(FIELD_NAME)).toHaveValue("");
      await expect(locators.dialogField(FIELD_CONTAINER_IMAGE)).toHaveValue("");
    }
  );

  test(
    "Tools & MCP - create a container tool under an account, reload the page and verify the tool is listed with its User Created badge, then delete it and verify the row is gone",
    { tag: ["@dev", "@regression", "@crud"] },
    async ({ page }) => {
      test.setTimeout(CRUD_TIMEOUT_MS);
      const locators = await openToolsAndMcp(page);

      const account = await firstAccountLabel(locators);
      expect(account, "the account filter offered no account beyond the All accounts entry").not.toBe("");
      await locators.chooseAccount(account);
      await locators.waitForToolsBody();

      const storedName = uniqueToolName();
      let created = false;

      try {
        await locators.openCreateDialog();
        await locators.dialogField(FIELD_NAME).fill(storedName);
        await locators.dialogField(FIELD_DESCRIPTION).fill("Created by the automated e2e coverage pass");
        await locators.dialogField(FIELD_CONTAINER_IMAGE).fill(CRUD_TOOL_IMAGE);
        await locators.dialogSubmitBtn.click();

        await expect(locators.toastWithText(TOAST_CREATED)).toBeVisible({ timeout: 60000 });
        created = true;
        await locators.createDialog.waitFor({ state: "detached", timeout: 60000 });

        // The reload is what separates a persisted tool from one still sitting in
        // React state: listTools refetches on mount, and the hash survives the
        // reload so the sub-tab resolves again. The account filter does not
        // survive it, so the row is looked for in the tenant-wide read, where a
        // custom tool is still listed.
        const reloaded = await reloadToolsAndMcp(page);
        // Found through the app's own search on the stored snake_case name rather
        // than by a reproduced display label: ListTools renders the name through
        // snakeToTitleCase, which uppercases whole parts listed in
        // UPPERCASE_ACRONYMS — "mcp" among them — so this suite must not try to
        // reproduce it.
        await findToolByStoredName(reloaded, storedName);
        await expect(reloaded.toolsRows).toHaveCount(1, { timeout: 60000 });
        // The badge is what proves this is the custom record just written rather
        // than a system tool that happens to share the name. Matched
        // case-insensitively: OwnerTypeBadge is text-transform: uppercase, and
        // innerText applies CSS transforms.
        await expect(reloaded.toolNameCell(0)).toContainText(BADGE_USER_CREATED, { timeout: 30000 });

        // Delete is per-account, so the account has to be picked again before the
        // row carries its action buttons. Choosing an account refetches, which
        // rebuilds originalData and re-runs the filter, so the search is
        // re-applied afterwards rather than assumed to have survived.
        await reloaded.chooseAccount(account);
        await reloaded.waitForToolsBody();
        await findToolByStoredName(reloaded, storedName);
        await expect(reloaded.toolsRows).toHaveCount(1, { timeout: 60000 });
        await reloaded.toolsRows.first().getByRole("button", { name: ARIA_DELETE_TOOL }).click();

        await expect(reloaded.deleteDialog()).toBeVisible({ timeout: 30000 });
        await reloaded.deleteDialog().getByRole("button", { name: BUTTON_DELETE, exact: true }).click();
        // The search is still applied, so an emptied listing is proof this row is
        // gone rather than merely scrolled out of a long catalogue.
        await expect(reloaded.toolsRows).toHaveCount(0, { timeout: 60000 });
        created = false;
      } finally {
        // Leaves the shared tenant as it was found when an assertion above fired
        // between the create and the delete.
        if (created) await deleteToolIfPresent(locators, storedName);
      }
    }
  );

  test(
    "Tools & MCP - switch the MCP Servers status filter from Enabled to Disabled, verify every listed server reports Disabled or the listing shows its no-data state",
    { tag: ["@dev", "@regression", "@functional"] },
    async ({ page }) => {
      test.setTimeout(SPEC_TIMEOUT_MS);
      const locators = await openToolsAndMcp(page);
      await locators.showMcpServers();

      await locators.chooseFilterOption(locators.mcpStatusFilter, MCP_STATUS_DISABLED);
      // MCPConfigList refetches rather than filtering in place, and the trigger
      // repaints before the effect that starts the fetch — so the pre-filter rows
      // are still mounted and readable for a moment. While the refetch is in
      // flight CustomTable swaps in a skeleton TableBody that carries no id, so
      // neither terminal shape resolves; retrying the whole read is what waits
      // out both windows.
      await expect(async () => {
        const rowCount = await locators.mcpRows.count();
        if (rowCount === 0) {
          // A tenant with every MCP server enabled is a legitimate state, and the
          // empty state is what the listing owes us for it — a silently blank
          // table would be the bug.
          expect(await locators.mcpEmptyState.count()).toBe(1);
          return;
        }
        const statuses = await visibleMcpStatuses(locators);
        expect(statuses.length).toBe(rowCount);
        for (const status of statuses) {
          expect(status).toMatch(new RegExp(MCP_STATUS_DISABLED, "i"));
        }
      }).toPass({ timeout: MCP_REFETCH_TIMEOUT_MS });
    }
  );
});
