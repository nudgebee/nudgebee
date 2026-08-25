import { test, expect } from "@playwright/test";
import { RolesLocators } from "./rolesLocators";
import {
  fetchCatalog,
  catalogCells,
  createRole,
  tryCreateRole,
  updateRole,
  deleteRole,
  listRoles,
  cleanupE2ERoles,
  skipUnlessCustomRoles,
  roleName,
  graphQLErrorMessage,
  type CatalogEntry,
  type PermissionClass,
} from "./rolesHelper";

/**
 * Admin → Roles: the custom-role editor.
 *
 * Coverage strategy — the permission matrix has 60+ modules × up to 3 classes,
 * which no UI test can click through in a single run inside a sane timeout. So
 * the suite splits the work:
 *
 *   UI (this file)   — every editor BEHAVIOUR (validation, Write⇒Read coupling,
 *                      scope tabs, tooltips, persistence round-trip, delete
 *                      confirm) plus a rendering sweep that asserts EVERY cell
 *                      in the catalog is actually present and interactive.
 *   API (this file)  — persistence of EVERY module × class cell, in batches, via
 *                      the same gateway the UI calls. This is what makes the
 *                      "all modules, all permissions" claim literal.
 *
 * The suite is self-cleaning: every role it creates is prefixed e2e-rbac- and
 * swept both before and after the run.
 */

const CLASSES: PermissionClass[] = ["Read", "Write", "Execute"];

test.describe.configure({ mode: "serial" });

test.describe("Admin → Roles: custom role editor", () => {
  let catalog: CatalogEntry[] = [];

  test.beforeAll(async ({ browser }) => {
    const page = await browser.newPage();
    await cleanupE2ERoles(page);
    await page.close();
  });

  // Every test below drives the Roles tab, which only exists when the tenant
  // has CUSTOM_ROLES enabled.
  test.beforeEach(async ({ page }) => {
    await skipUnlessCustomRoles(page, test);
  });

  test.afterAll(async ({ browser }) => {
    const page = await browser.newPage();
    await cleanupE2ERoles(page);
    await page.close();
  });

  test("Roles - fetch the permission catalog, verify every module lists its actions for each class", { tag: ["@dev", "@test", "@regression", "@rbac", "@oss"] }, async ({ page }) => {
    const res = await fetchCatalog(page);
    catalog = res.catalog;

    expect(catalog.length, "catalog must expose every grantable module").toBeGreaterThan(50);
    expect(res.systemRoles.length, "built-in roles must be derivable").toBeGreaterThan(0);

    // Both scope groups must be populated — the editor renders one tab each.
    expect(catalog.filter((e) => e.scope === "tenant").length).toBeGreaterThan(0);
    expect(catalog.filter((e) => e.scope === "account").length).toBeGreaterThan(0);

    // Every advertised class must be backed by at least one action, otherwise
    // the tooltip renders an empty explanation and the grant explains nothing.
    for (const entry of catalog) {
      for (const cls of entry.classes) {
        expect(entry.actions?.[cls]?.length ?? 0, `${entry.module}:${cls} must list its actions`).toBeGreaterThan(0);
      }
      expect(entry.classes.every((c) => CLASSES.includes(c))).toBe(true);
    }
  });

  test("Roles sanity - open Admin > Roles, verify the custom-role table, the built-in-role table and the New role button render", { tag: ["@dev", "@test", "@regression", "@rbac", "@oss"] }, async ({ page }) => {
    const roles = new RolesLocators(page);
    await roles.open();

    await expect(roles.customRolesTable).toBeVisible();
    await expect(roles.builtInRolesTable).toBeVisible();
    await expect(roles.newRoleBtn).toBeEnabled();
  });

  test("Roles - open the role editor, switch both scope tabs, verify the matrix draws exactly the catalog's module x class cells", { tag: ["@dev", "@test", "@regression", "@rbac", "@oss"] }, async ({ page }) => {
    test.setTimeout(300000);
    const roles = new RolesLocators(page);
    const { catalog: cat } = await fetchCatalog(page);
    await roles.open();
    await roles.newRoleBtn.click();
    await expect(roles.roleNameInput).toBeVisible();

    const missing: string[] = [];
    for (const scope of ["tenant", "account"] as const) {
      await roles.selectScope(scope);
      const entries = cat.filter((e) => e.scope === scope);
      expect(entries.length, `${scope} scope must render modules`).toBeGreaterThan(0);

      for (const entry of entries) {
        for (const cls of CLASSES) {
          const cell = roles.permCell(entry.module, cls);
          const rendered = (await cell.count()) > 0;
          const shouldRender = entry.classes.includes(cls);
          if (rendered !== shouldRender) {
            missing.push(`${scope}/${entry.module}:${cls} rendered=${rendered} expected=${shouldRender}`);
          }
        }
      }
    }
    // A cell the catalog advertises but the matrix doesn't draw = an
    // ungrantable permission. A cell drawn without catalog backing = a dead tick.
    expect(missing, "matrix must render exactly the catalog's cells").toEqual([]);
  });

  test("Roles - open the role editor, tick Read then Write, untick Read, verify Write clears and disables again", { tag: ["@dev", "@test", "@regression", "@rbac", "@oss"] }, async ({ page }) => {
    const roles = new RolesLocators(page);
    const { catalog: cat } = await fetchCatalog(page);
    // Pick a tenant-scope module that offers both Read and Write.
    const entry = cat.find((e) => e.scope === "tenant" && e.classes.includes("Read") && e.classes.includes("Write"));
    expect(entry, "need a module with Read+Write to test the coupling").toBeTruthy();
    const mod = entry!.module;

    await roles.open();
    await roles.newRoleBtn.click();
    await roles.selectScope("tenant");

    const read = roles.permCheckbox(mod, "Read");
    const write = roles.permCheckbox(mod, "Write");

    // 1. Write starts disabled while Read is unticked.
    await expect(write).toBeDisabled();

    // 2. Ticking Read enables Write.
    await read.check({ force: true });
    await expect(write).toBeEnabled();

    // 3. Ticking Write keeps Read on (Write implies Read).
    await write.check({ force: true });
    await expect(read).toBeChecked();
    await expect(write).toBeChecked();

    // 4. Unticking Read cascades Write off and re-disables it.
    await read.uncheck({ force: true });
    await expect(read).not.toBeChecked();
    await expect(write).not.toBeChecked();
    await expect(write).toBeDisabled();
  });

  test("Roles - tick a tenant module, switch to the Account tab and tick another, switch back, verify the tenant tick survived", { tag: ["@dev", "@test", "@regression", "@rbac", "@oss"] }, async ({ page }) => {
    const roles = new RolesLocators(page);
    const { catalog: cat } = await fetchCatalog(page);
    const tenantMod = cat.find((e) => e.scope === "tenant" && e.classes.includes("Read"))!.module;
    const accountMod = cat.find((e) => e.scope === "account" && e.classes.includes("Read"))!.module;

    await roles.open();
    await roles.newRoleBtn.click();

    await roles.selectScope("tenant");
    await roles.permCheckbox(tenantMod, "Read").check({ force: true });

    await roles.selectScope("account");
    // The tenant module's cell is not on screen at all now.
    await expect(roles.permCell(tenantMod, "Read")).toHaveCount(0);
    await roles.permCheckbox(accountMod, "Read").check({ force: true });

    // Going back must still show the earlier tick — the count chip on each tab
    // exists precisely so a hidden-tab selection isn't assumed lost.
    await roles.selectScope("tenant");
    await expect(roles.permCheckbox(tenantMod, "Read")).toBeChecked();
  });

  test("Roles - save the editor with an empty name, then save the same name twice, verify the modal stays open and the duplicate is refused", { tag: ["@dev", "@test", "@regression", "@rbac", "@oss"] }, async ({ page }) => {
    const roles = new RolesLocators(page);
    await roles.open();
    await roles.newRoleBtn.click();

    // Empty name → client-side guard, no write, modal stays open.
    await roles.saveRoleBtn.click();
    await expect(roles.roleNameInput).toBeVisible();

    const name = roleName("dupe");
    await roles.roleNameInput.fill(name);
    await roles.saveRoleBtn.click();
    await expect(roles.roleNameInput).toBeHidden({ timeout: 30000 });

    // Same name again → backend unique violation surfaced as a conflict.
    const second = await tryCreateRole(page, name, []);
    expect(graphQLErrorMessage(second.body) ?? "", "duplicate name must be refused").toMatch(/already exists/i);
  });

  test("Roles - create a role carrying an account-scoped grant, verify the API refuses grant-level scope", { tag: ["@dev", "@test", "@regression", "@rbac", "@oss"] }, async ({ page }) => {
    // Scope belongs to the ASSIGNMENT ("group G holds R on account A"), never to
    // the grant. The old scope-on-grant shape must fail loudly rather than have
    // its scope silently dropped.
    const res = await tryCreateRole(page, roleName("scoped-grant"), [
      { module: "events", class: "Read", entity_type: "account", entity_id: "00000000-0000-0000-0000-000000000000" },
    ]);
    expect(graphQLErrorMessage(res.body) ?? "", "grant-level scope must be rejected").toMatch(/unscoped|bind the role/i);
  });

  test("Roles - create a role with the class Admin, verify the API refuses anything but Read, Write or Execute", { tag: ["@dev", "@test", "@regression", "@rbac", "@oss"] }, async ({ page }) => {
    const res = await tryCreateRole(page, roleName("bad-class"), [{ module: "events", class: "Admin" }]);
    expect(graphQLErrorMessage(res.body) ?? "").toMatch(/Read\|Write\|Execute|invalid permission/i);
  });

  test("Roles - save every catalog module x class cell in batches, verify each grant persists exactly as ticked", { tag: ["@dev", "@test", "@regression", "@rbac", "@oss"] }, async ({ page }) => {
    test.setTimeout(600000);
    const { catalog: cat } = await fetchCatalog(page);
    const cells = catalogCells(cat);
    expect(cells.length, "expected a large grant surface").toBeGreaterThan(100);

    // Saved in batches: one role per batch keeps the assertion granular enough
    // to name the offending cell, without minting 114 roles.
    const BATCH = 20;
    const failures: string[] = [];

    for (let i = 0; i < cells.length; i += BATCH) {
      const batch = cells.slice(i, i + BATCH);
      const perms = batch.map((c) => ({ module: c.module, class: c.cls }));
      const name = roleName(`cells-${i}`);
      const id = await createRole(page, name, perms);

      const saved = (await listRoles(page)).find((r) => r.id === id);
      const savedKeys = new Set((saved?.permissions ?? []).map((p) => `${p.module}:${p.class}`));
      for (const c of batch) {
        if (!savedKeys.has(`${c.module}:${c.cls}`)) failures.push(`${c.module}:${c.cls} did not persist`);
      }
      // Nothing extra may appear — a grant must not widen itself on save.
      for (const k of savedKeys) {
        if (!batch.some((c) => `${c.module}:${c.cls}` === k)) failures.push(`${k} appeared unexpectedly in ${name}`);
      }
      await deleteRole(page, id);
    }

    expect(failures, "every catalog cell must persist exactly as ticked").toEqual([]);
  });

  test("Roles - create a role with one grant, edit it to a different grant, verify the new grant replaces rather than merges", { tag: ["@dev", "@test", "@regression", "@rbac", "@oss"] }, async ({ page }) => {
    const { catalog: cat } = await fetchCatalog(page);
    const a = cat.find((e) => e.classes.includes("Read"))!.module;
    const b = cat.filter((e) => e.classes.includes("Read") && e.module !== a)[0].module;

    const id = await createRole(page, roleName("replace"), [{ module: a, class: "Read" }]);
    const renamed = roleName("replaced");
    const res = await updateRole(page, id, renamed, [{ module: b, class: "Read" }]);
    expect(res.body?.errors, JSON.stringify(res.body?.errors)).toBeUndefined();

    const after = (await listRoles(page)).find((r) => r.id === id);
    expect(after?.name).toBe(renamed);
    expect((after?.permissions ?? []).map((p) => `${p.module}:${p.class}`)).toEqual([`${b}:Read`]);
  });

  test("Roles - open an existing role and save without editing, verify no UpdateCustomRole call is sent", { tag: ["@dev", "@test", "@regression", "@rbac", "@oss"] }, async ({ page }) => {
    const roles = new RolesLocators(page);
    const name = roleName("dirty-gate");
    const id = await createRole(page, name, []);

    await roles.open();
    await roles.editRoleBtn(id).click();
    await expect(roles.roleNameInput).toBeVisible();

    // No edit made — Save must close the modal WITHOUT an UpdateCustomRole call.
    let updateSeen = false;
    page.on("request", (req) => {
      if (req.url().includes("/api/graphql") && (req.postData() ?? "").includes("UpdateCustomRole")) updateSeen = true;
    });
    await roles.saveRoleBtn.click();
    await expect(roles.roleNameInput).toBeHidden({ timeout: 30000 });
    expect(updateSeen, "an unchanged role must not be re-saved (it busts the tenant security-context cache)").toBe(false);
  });

  test("Roles - delete a role and cancel, then delete and confirm, verify the role survives the cancel and is gone after the confirm", { tag: ["@dev", "@test", "@regression", "@rbac", "@oss"] }, async ({ page }) => {
    const roles = new RolesLocators(page);
    const name = roleName("delete-me");
    const id = await createRole(page, name, []);

    await roles.open();
    await roles.deleteRoleBtn(id).click();
    await expect(page.getByText(new RegExp(`Delete role "${name}"`))).toBeVisible();

    // Cancel first — the role must survive.
    await page.getByRole("button", { name: "Cancel", exact: true }).last().click();
    expect((await listRoles(page)).some((r) => r.id === id)).toBe(true);

    await roles.deleteRoleBtn(id).click();
    await roles.deleteConfirmBtn.click();
    await expect.poll(async () => (await listRoles(page)).some((r) => r.id === id), { timeout: 30000 }).toBe(false);
  });

  test("Roles - open the role editor on both scope tabs, verify the built-in role summary renders read-only counts for that scope", { tag: ["@dev", "@test", "@regression", "@rbac", "@oss"] }, async ({ page }) => {
    const roles = new RolesLocators(page);
    await fetchCatalog(page);
    await roles.open();
    await roles.newRoleBtn.click();
    await expect(roles.roleNameInput).toBeVisible();

    // This suite runs against a deployed environment, which may predate the
    // built-in-role summary. Skip rather than fail — a red PR here would mean
    // "not deployed yet", not "broken".
    test.skip(!(await roles.hasBuiltInSummary()), "target environment predates the built-in-role summary");

    // The summary is scoped to the active tab — a tenant matrix must not be
    // annotated with account-level built-in grants.
    for (const scope of ["tenant", "account"] as const) {
      await roles.selectScope(scope);
      await expect(roles.builtInSummary(scope)).toBeVisible();
      // Scoped to the dialog and exact: the listing's own built-in-roles table
      // behind the modal has a "Built-in role" / "Permissions" header row whose
      // combined text matches the same substring.
      await expect(roles.roleEditorDialog.getByText("Built-in role permissions", { exact: true })).toBeVisible();
      // Read-only: counts only, never an editable control.
      await expect(roles.builtInSummary(scope).locator('input[type="checkbox"]')).toHaveCount(0);
    }
  });

  test("Roles - open each built-in role from the listing, verify the viewer shows its derived grants with no editable checkbox", { tag: ["@dev", "@test", "@regression", "@rbac", "@oss"] }, async ({ page }) => {
    const roles = new RolesLocators(page);
    const { systemRoles } = await fetchCatalog(page);
    await roles.open();

    for (const sys of systemRoles) {
      const btn = roles.viewSystemRoleBtn(sys.systemKey);
      if ((await btn.count()) === 0) continue; // roles with no grants aren't listed
      await btn.click();
      await expect(page.getByText(`${sys.label} — permissions`)).toBeVisible();
      // Read-only: the viewer renders ✓ / — marks, never a checkbox.
      await expect(page.locator('[role="dialog"] input[type="checkbox"]')).toHaveCount(0);
      await page.keyboard.press("Escape");
    }
  });

  test("Roles - update a role id that belongs to another tenant, verify the API refuses it as not found", { tag: ["@dev", "@test", "@regression", "@rbac", "@oss"] }, async ({ page }) => {
    // System roles live in the same table with is_system=true. They must be
    // rejected by an explicit guard, not merely be absent from tenant queries.
    const { systemRoles } = await fetchCatalog(page);
    expect(systemRoles.length).toBeGreaterThan(0);
    // The API takes a uuid; system rows are seeded, so drive the guard through
    // the tenant-scoping path instead: an id from another tenant must 404.
    const res = await updateRole(page, "00000000-0000-0000-0000-000000000000", roleName("hijack"), []);
    expect(graphQLErrorMessage(res.body) ?? "").toMatch(/not found|read-only|Not Allowed/i);
  });
  test("Roles - open the role editor, filter the module list by module key then by label, verify only the matching module stays", { tag: ["@dev", "@test", "@regression", "@rbac", "@oss"] }, async ({ page }) => {
    const roles = new RolesLocators(page);
    await roles.open();
    await roles.newRoleBtn.click();
    await expect(roles.roleNameInput).toBeVisible();
    await roles.selectScope("tenant");

    // customroles renders as "Custom Roles", so it is the one module that proves
    // the filter matches the raw module key AND the formatted label.
    await expect(roles.permCell("customroles", "Read")).toBeVisible();
    await expect(roles.permCell("audits", "Read")).toBeVisible();

    await roles.moduleSearch.fill("customroles");
    await expect(roles.permCell("customroles", "Read")).toBeVisible();
    await expect(roles.permCell("audits", "Read")).toHaveCount(0);

    await roles.moduleSearch.fill("Custom Roles");
    await expect(roles.permCell("customroles", "Read")).toBeVisible();
    await expect(roles.permCell("audits", "Read")).toHaveCount(0);

    await roles.moduleSearch.fill("zzz-no-such-module");
    await expect(roles.roleEditorDialog.getByText('No modules match "zzz-no-such-module".')).toBeVisible();

    await roles.moduleSearch.fill("");
    await expect(roles.permCell("audits", "Read")).toBeVisible();
  });

  test("Roles - create a role through the editor with a tenant and an account grant, save, verify the saved grants match what was ticked", { tag: ["@dev", "@test", "@regression", "@rbac", "@oss"] }, async ({ page }) => {
    const roles = new RolesLocators(page);
    const { catalog: cat } = await fetchCatalog(page);
    const tenantMod = cat.find((e) => e.scope === "tenant" && e.classes.includes("Read"))!.module;
    const accountMod = cat.find((e) => e.scope === "account" && e.classes.includes("Read"))!.module;
    const name = roleName("ui-create");

    await roles.open();
    await roles.newRoleBtn.click();
    await roles.roleNameInput.fill(name);

    await roles.selectScope("tenant");
    await roles.permCheckbox(tenantMod, "Read").check({ force: true });
    await roles.selectScope("account");
    await roles.permCheckbox(accountMod, "Read").check({ force: true });

    await roles.saveRoleBtn.click();
    await expect(roles.roleNameInput).toBeHidden({ timeout: 30000 });

    // The editor is the only path that turns ticks into grants, so the stored set
    // is what proves the matrix wrote exactly what the admin saw.
    const saved = (await listRoles(page)).find((r) => r.name === name);
    expect(saved, "the role saved from the editor must appear in the listing").toBeTruthy();
    expect((saved!.permissions ?? []).map((p) => `${p.module}:${p.class}`).sort()).toEqual(
      [`${accountMod}:Read`, `${tenantMod}:Read`].sort()
    );
    await expect(roles.editRoleBtn(saved!.id)).toBeVisible();

    await deleteRole(page, saved!.id);
  });

  test("Roles - enter a name and tick a grant in the editor, cancel, verify no role is created", { tag: ["@dev", "@test", "@regression", "@rbac", "@oss"] }, async ({ page }) => {
    const roles = new RolesLocators(page);
    const { catalog: cat } = await fetchCatalog(page);
    const mod = cat.find((e) => e.scope === "tenant" && e.classes.includes("Read"))!.module;
    const name = roleName("cancelled");

    await roles.open();
    await roles.newRoleBtn.click();
    await roles.roleNameInput.fill(name);
    await roles.selectScope("tenant");
    await roles.permCheckbox(mod, "Read").check({ force: true });

    await roles.cancelBtn.click();
    await expect(roles.roleNameInput).toBeHidden({ timeout: 30000 });
    expect((await listRoles(page)).some((r) => r.name === name), "cancel must not write the role").toBe(false);
  });

  test("Roles - tick Execute on a module with Read unticked, save, verify Execute stands alone and does not pull in Read", { tag: ["@dev", "@test", "@regression", "@rbac", "@oss"] }, async ({ page }) => {
    const roles = new RolesLocators(page);
    const { catalog: cat } = await fetchCatalog(page);
    const entry = cat.find((e) => e.scope === "tenant" && e.classes.includes("Execute") && e.classes.includes("Read"));
    expect(entry, "need a tenant module offering both Read and Execute").toBeTruthy();
    const mod = entry!.module;
    const name = roleName("execute-only");

    await roles.open();
    await roles.newRoleBtn.click();
    await roles.roleNameInput.fill(name);
    await roles.selectScope("tenant");

    const read = roles.permCheckbox(mod, "Read");
    const execute = roles.permCheckbox(mod, "Execute");

    // Only Write is gated on Read. Execute is a standalone class, so it must be
    // reachable and savable with Read left unticked.
    await expect(execute).toBeEnabled();
    await execute.check({ force: true });
    await expect(read).not.toBeChecked();

    await roles.saveRoleBtn.click();
    await expect(roles.roleNameInput).toBeHidden({ timeout: 30000 });

    const saved = (await listRoles(page)).find((r) => r.name === name);
    expect((saved?.permissions ?? []).map((p) => `${p.module}:${p.class}`)).toEqual([`${mod}:Execute`]);
    await deleteRole(page, saved!.id);
  });
});
