// Not for OSS
import { test, expect } from "@playwright/test";
import { NubiLocators } from "../nubiLocators";
import { CustomAgentsLocators } from "./customAgentsLocators";
import {
  NO_MATCH_TERM,
  clearAgentSearch,
  chooseRowMenuItem,
  deleteAgentByName,
  expectListingSettled,
  fillAgentForm,
  listedAgentNames,
  countNamed,
  openAgentsTab,
  pickAgentType,
  removeAgentIfPresent,
  rowForName,
  searchAgents,
  uniqueAgentName,
} from "./customAgentsHelper";

// LoginPage.navigate() does page.goto(process.env.BASE_URL || ""), which navigates
// to about:blank instead of failing when the key is unset — so the run dies much
// later on an unrelated locator. Fail here, naming the key.
const BASE_URL = process.env.BASE_URL || "";
if (!BASE_URL) {
  throw new Error("BASE_URL is not set — add it to .env / .env.dev");
}

// The tenant brands this option as "<baseTitle> System Agent" (useTenantBranding in
// ListAgents.jsx), so the label is matched by its stable tail rather than in full.
const SYSTEM_AGENT_OPTION = /System Agent$/;
const USER_AGENT_OPTION = /^User Created Agent$/;

test(
  "Custom Agents sanity - open Nubi AI & Tools, land on the Agents tab, verify the search box, the Agent Type filter, the Create Custom Agent button and the agents table render",
  { tag: ["@dev", "@sanity", "@functional"] },
  async ({ page }) => {
    test.setTimeout(180000);

    const { nubi, agents } = await openAgentsTab(page);

    await test.step("The listing toolbar offers search, the type filter and the create action", async () => {
      await expect(nubi.searchAgentInput).toBeVisible({ timeout: 30000 });
      await expect(agents.agentTypeFilter).toBeVisible({ timeout: 30000 });
      await expect(nubi.createCustomAgentBtn).toBeVisible({ timeout: 30000 });
    });

    await test.step("The agents table renders its Name, Description and Action columns", async () => {
      await expect(agents.table).toBeVisible({ timeout: 30000 });
      for (const header of ["Name", "Description", "Status", "Action"]) {
        expect(await agents.columnIndex(header)).toBeGreaterThanOrEqual(0);
      }
    });

    await test.step("The tenant's agent catalog is listed rather than an empty table", async () => {
      // The system catalog ships with every tenant, so an empty Agents tab is a
      // real failure here and not a legitimate no-data state.
      await expect(agents.nameCells.first()).toBeVisible({ timeout: 30000 });
    });
  }
);

test(
  "Custom Agents - search the Agents listing by an existing agent name, verify only agents whose name contains that term stay listed",
  { tag: ["@dev", "@regression", "@search", "@functional"] },
  async ({ page }) => {
    test.setTimeout(180000);

    const { nubi, agents } = await openAgentsTab(page);

    const listed = await listedAgentNames(agents);
    expect(listed.length, "the Agents tab listed no agent to search for").toBeGreaterThan(0);
    // A mid-length slice of a real name, so the term matches that agent and plausibly
    // a few siblings — a whole name would only ever return one row and would not
    // prove the filter is a contains match.
    const term = listed[0].slice(0, Math.max(3, Math.floor(listed[0].length / 2)));

    await searchAgents(agents, nubi, term);

    await test.step(`No row is left whose name omits "${term}"`, async () => {
      await expect
        .poll(
          async () => {
            const names = await listedAgentNames(agents);
            return names.filter((name) => !name.toLowerCase().includes(term.toLowerCase()));
          },
          { timeout: 30000 }
        )
        .toEqual([]);
    });

    await test.step("The search narrowed to a real set rather than emptying the table", async () => {
      // At least one, not exactly one: agent names are not unique across the
      // tenant (a shipped system agent and an account's own copy can carry the
      // same name), and this step is about the row surviving the filter, not
      // about how many rows the dev tenant happens to hold under that name.
      await expect.poll(async () => countNamed(agents, listed[0]), { timeout: 30000 }).toBeGreaterThanOrEqual(1);
    });
  }
);

test(
  "Custom Agents - search a name no agent can match, verify the No Data Available empty state, clear the search, verify the full listing returns",
  { tag: ["@dev", "@regression", "@search", "@negative"] },
  async ({ page }) => {
    test.setTimeout(180000);

    const { nubi, agents } = await openAgentsTab(page);

    const before = await listedAgentNames(agents);
    expect(before.length, "the Agents tab listed no agent before the search").toBeGreaterThan(0);

    await searchAgents(agents, nubi, NO_MATCH_TERM);

    await test.step("The table reports no data instead of keeping rows", async () => {
      await expect(agents.emptyState).toBeVisible({ timeout: 30000 });
      await expect(agents.nameCells).toHaveCount(0, { timeout: 30000 });
    });

    await clearAgentSearch(agents, nubi);

    await test.step("Clearing the term brings the listing back", async () => {
      await expect(agents.emptyState).toBeHidden({ timeout: 30000 });
      await expect
        .poll(async () => (await listedAgentNames(agents)).length, { timeout: 30000 })
        .toBeGreaterThanOrEqual(before.length);
    });
  }
);

test(
  "Custom Agents - filter Agent Type by System Agent then by User Created Agent, verify the two sets share no agent and together cover the unfiltered listing",
  { tag: ["@dev", "@regression", "@search", "@functional"] },
  async ({ page }) => {
    test.setTimeout(180000);

    const { agents } = await openAgentsTab(page);

    // The filter partitions on agent.type, which is 'system' or 'custom' and nothing
    // else (the useEffect in ListAgents.jsx), so the two options must divide the
    // unfiltered listing exactly. That holds whatever the dev tenant happens to
    // contain, which a per-row type assertion could not — the table renders no Type
    // column to read.
    const unfiltered = await listedAgentNames(agents);
    expect(unfiltered.length, "the Agents tab listed no agent to filter").toBeGreaterThan(0);

    await pickAgentType(agents, SYSTEM_AGENT_OPTION);
    const systemAgents = await listedAgentNames(agents);

    await pickAgentType(agents, USER_AGENT_OPTION);
    const userAgents = await listedAgentNames(agents);

    await test.step("No agent is reported as both a system agent and a user-created one", async () => {
      expect(systemAgents.filter((name) => userAgents.includes(name))).toEqual([]);
    });

    await test.step("Every unfiltered agent is claimed by exactly one of the two filters", async () => {
      expect(unfiltered.filter((name) => !systemAgents.includes(name) && !userAgents.includes(name))).toEqual([]);
    });

    await test.step("The System Agent filter returned the catalog rather than an empty table", async () => {
      // Every tenant carries the shipped system catalog, so this side of the
      // partition is never legitimately empty; the user-created side can be.
      expect(systemAgents.length).toBeGreaterThan(0);
    });
  }
);

test(
  "Custom Agents - create a custom agent with a unique name, verify a fresh search returns it, delete it, verify the search returns the No Data Available empty state",
  { tag: ["@dev", "@regression", "@crud"] },
  async ({ page }) => {
    test.setTimeout(300000);

    const { nubi, agents } = await openAgentsTab(page);
    const agentName = uniqueAgentName();

    try {
      await nubi.createCustomAgentBtn.click();
      await expect(agents.createAgentModal).toBeVisible({ timeout: 30000 });
      await fillAgentForm(nubi, agentName);
      await nubi.submitCreateAgentBtn.click();

      await test.step("The create form reports success and closes", async () => {
        await expect(nubi.successMessage).toBeVisible({ timeout: 30000 });
        await expect(agents.createAgentModal).toBeHidden({ timeout: 30000 });
      });

      await test.step("A fresh search returns the agent, so it persisted server-side", async () => {
        // Searching again re-reads the listing rather than trusting the snackbar:
        // the toast is posted by the client and says nothing about what was stored.
        await searchAgents(agents, nubi, agentName);
        await expect.poll(async () => countNamed(agents, agentName), { timeout: 30000 }).toBe(1);
      });

      await deleteAgentByName(agents, nubi, agentName);

      await test.step("Searching the deleted name now returns no data", async () => {
        await searchAgents(agents, nubi, agentName);
        await expect(agents.emptyState).toBeVisible({ timeout: 30000 });
      });
    } finally {
      // The delete above is the assertion; this only covers a failure between the
      // create and it, so the shared dev tenant is not left carrying the agent.
      await removeAgentIfPresent(agents, nubi, agentName).catch(() => {});
    }
  }
);

test(
  "Custom Agents - open the create form, enter the name of an agent that already exists, verify the Agent name already exists error and that Create Agent is refused",
  { tag: ["@dev", "@regression", "@validation", "@negative"] },
  async ({ page }) => {
    test.setTimeout(180000);

    const { nubi, agents } = await openAgentsTab(page);

    // The clashing name is one this test creates, not one read off the listing.
    // The Name cell renders `agent.aliases[0] ?? agent.name` (ListAgents.jsx),
    // so a shipped agent shows a human title ("GitHub Operations") while the
    // duplicate check compares against its identifier — typing what the table
    // shows fails the FORMAT check first ("Name can only contain letters,
    // numbers, and underscores.") and never reaches the clash. A user-created
    // agent has no alias, so its listed name IS its identifier.
    const takenName = uniqueAgentName();

    try {
      await test.step("Create the agent whose name the second create will clash with", async () => {
        await nubi.createCustomAgentBtn.click();
        await expect(agents.createAgentModal).toBeVisible({ timeout: 30000 });
        await fillAgentForm(nubi, takenName);
        await nubi.submitCreateAgentBtn.click();
        await expect(nubi.successMessage).toBeVisible({ timeout: 30000 });
        await expect(agents.createAgentModal).toBeHidden({ timeout: 30000 });
      });

      // The form's duplicate check reads ListAgents' own `allAgentNames`, which
      // is rebuilt from the listing's data — so the clash below only registers
      // once the created agent is actually in that listing. Searching for it is
      // both the proof it persisted and the refetch that puts it there.
      await searchAgents(agents, nubi, takenName);
      await expect.poll(async () => countNamed(agents, takenName), { timeout: 30000 }).toBe(1);
      await clearAgentSearch(agents, nubi);

      await nubi.createCustomAgentBtn.click();
      await expect(agents.createAgentModal).toBeVisible({ timeout: 30000 });

      await nubi.agentNameInput.waitFor({ state: "visible", timeout: 30000 });
      await nubi.agentNameInput.fill(takenName);

      await test.step("The Name field names the clash as the reason", async () => {
        // nameValidation runs on every change, so the message is up before submit.
        await expect(agents.nameFieldError).toContainText("Agent name already exists", { timeout: 15000 });
      });

      await test.step("Submitting leaves the form open and adds no second agent by that name", async () => {
        await nubi.submitCreateAgentBtn.click();
        await expect(agents.createAgentModal).toBeVisible({ timeout: 15000 });
        // No "did a success toast appear" check here: this test created the
        // first agent itself, so one is already on screen. The form staying
        // open and the count below are what prove the second create was
        // refused.
      });

      await agents.agentModalCancelBtn.click();
      await expect(agents.createAgentModal).toBeHidden({ timeout: 30000 });

      await test.step("The listing still holds exactly one agent under that name", async () => {
        await searchAgents(agents, nubi, takenName);
        await expect.poll(async () => countNamed(agents, takenName), { timeout: 30000 }).toBe(1);
      });
    } finally {
      await removeAgentIfPresent(agents, nubi, takenName).catch(() => {});
    }
  }
);

test(
  "Custom Agents - open the create form, submit with Name and Description left empty, verify the Please fill the following fields rejection and that the form stays open",
  { tag: ["@dev", "@regression", "@validation", "@negative"] },
  async ({ page }) => {
    test.setTimeout(180000);

    const { nubi, agents } = await openAgentsTab(page);

    await nubi.createCustomAgentBtn.click();
    await expect(agents.createAgentModal).toBeVisible({ timeout: 30000 });
    await nubi.agentNameInput.waitFor({ state: "visible", timeout: 30000 });

    await nubi.submitCreateAgentBtn.click();

    await test.step("The submit is rejected with the required-fields message", async () => {
      await expect(agents.requiredFieldsToast).toBeVisible({ timeout: 15000 });
    });

    await test.step("Description is marked empty and the form is still open to fix", async () => {
      await expect(agents.descriptionFieldError).toBeVisible({ timeout: 15000 });
      await expect(agents.createAgentModal).toBeVisible({ timeout: 15000 });
      await expect(nubi.successMessage).toHaveCount(0, { timeout: 15000 });
    });

    await agents.agentModalCancelBtn.click();
    await expect(agents.createAgentModal).toBeHidden({ timeout: 30000 });
  }
);

test(
  "Custom Agents - open the create form, fill a unique agent name, cancel the form, verify searching that name returns the No Data Available empty state",
  { tag: ["@dev", "@regression", "@negative", "@functional"] },
  async ({ page }) => {
    test.setTimeout(180000);

    const { nubi, agents } = await openAgentsTab(page);
    const agentName = uniqueAgentName();

    try {
      await nubi.createCustomAgentBtn.click();
      await expect(agents.createAgentModal).toBeVisible({ timeout: 30000 });
      await fillAgentForm(nubi, agentName);

      await agents.agentModalCancelBtn.click();
      await expect(agents.createAgentModal).toBeHidden({ timeout: 30000 });

      await test.step("Cancelling stored nothing, so the name is still unused", async () => {
        await searchAgents(agents, nubi, agentName);
        await expect(agents.emptyState).toBeVisible({ timeout: 30000 });
        await expect(agents.nameCellContaining(agentName)).toHaveCount(0, { timeout: 30000 });
      });
    } finally {
      // Cancel is expected to leave nothing behind — that is what this test asserts —
      // so this only runs down a form that submitted despite the cancel.
      await removeAgentIfPresent(agents, nubi, agentName).catch(() => {});
    }
  }
);

test(
  "Custom Agents - create a custom agent, open Edit Agent from its row menu, verify the form opens pre-filled with that agent's name, close it and delete the agent",
  { tag: ["@dev", "@regression", "@crud", "@functional"] },
  async ({ page }) => {
    test.setTimeout(300000);

    const { nubi, agents } = await openAgentsTab(page);
    const agentName = uniqueAgentName();

    try {
      await nubi.createCustomAgentBtn.click();
      await expect(agents.createAgentModal).toBeVisible({ timeout: 30000 });
      await fillAgentForm(nubi, agentName);
      await nubi.submitCreateAgentBtn.click();
      await expect(nubi.successMessage).toBeVisible({ timeout: 30000 });
      await expect(agents.createAgentModal).toBeHidden({ timeout: 30000 });

      await searchAgents(agents, nubi, agentName);
      const row = await rowForName(agents, agentName);
      await expect(row).toBeVisible({ timeout: 30000 });

      // Only a custom agent offers "Edit Agent"; a system agent's row labels the same
      // menu id "Override Agent Prompt", which opens a different dialog.
      await chooseRowMenuItem(agents, row, nubi.editAgentMenuItem);

      await test.step("The edit form opens on that agent, carrying its stored name", async () => {
        await expect(agents.editAgentModal).toBeVisible({ timeout: 30000 });
        await expect(nubi.agentNameInput).toHaveValue(agentName, { timeout: 30000 });
      });

      await agents.agentModalCancelBtn.click();

      await test.step("Closing the edit form returns to the listing with the agent intact", async () => {
        await expect(agents.editAgentModal).toBeHidden({ timeout: 30000 });
        await expect(agents.listingBox).toBeVisible({ timeout: 30000 });
        await expect.poll(async () => countNamed(agents, agentName), { timeout: 30000 }).toBe(1);
      });

      await deleteAgentByName(agents, nubi, agentName);
    } finally {
      // The delete above is the assertion; this only covers a failure before it, and a
      // cleanup that cannot run must not mask the original failure with its own.
      await removeAgentIfPresent(agents, nubi, agentName).catch(() => {});
    }
  }
);

test(
  "Custom Agents - switch from the Agents tab to the Tools tab and back, verify each tab renders its own listing",
  { tag: ["@dev", "@smoke", "@functional"] },
  async ({ page }) => {
    test.setTimeout(180000);

    const { nubi, agents } = await openAgentsTab(page);

    await test.step("The Tools tab replaces the agents listing with the tools listing", async () => {
      await nubi.ToolButton.click();
      await expect(nubi.searchToolInput).toBeVisible({ timeout: 30000 });
      await expect(agents.listingBox).toBeHidden({ timeout: 30000 });
    });

    await test.step("Returning to Agents brings the agents listing back", async () => {
      await nubi.customAgentTab.click();
      await expect(agents.listingBox).toBeVisible({ timeout: 30000 });
      await expect(nubi.searchAgentInput).toBeVisible({ timeout: 30000 });
      await expectListingSettled(agents);
    });
  }
);
