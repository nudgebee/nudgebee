// Not for OSS
import { test, expect } from "@playwright/test";
import { LoginPage } from "../../../pages/LoginPage";
import { NubiLocators } from "../nubiLocators";
import { KnowledgeBaseLocators } from "./knowledgeBaseLocators";

const KB_NAME = "auto_kb_agent_mapping_e2e";
const KB_CONTENT = "Restart the pod, then check the OOMKilled reason on the last terminated state.";

// A second KB whose only job is to be retrieved. Both the runbook id and the
// limit are invented, so a correct answer cannot come from anywhere else.
const RAG_KB_NAME = "auto_kb_retrieval_e2e";
const RUNBOOK_ID = "NB-KBTEST-4417";
const RUNBOOK_LIMIT = "3717Mi";
const RAG_CONTENT = `Runbook ${RUNBOOK_ID}: when the checkout-service pod reports OOMKilled, the on-call engineer must set the memory limit to ${RUNBOOK_LIMIT} and restart the deployment.`;
const RAG_QUESTION = `According to runbook ${RUNBOOK_ID}, what memory limit must the on-call engineer set when checkout-service reports OOMKilled?`;

test.describe("Knowledge Base agent mapping", () => {
  test.describe.configure({ mode: "serial" });

  test(
    "Knowledge Base - create a KB mapped to one specific agent, verify the success snackbar and that reopening the KB for edit shows Specific agents with that agent still ticked",
    { tag: ["@dev", "@regression", "@functional", "@crud"] },
    async ({ page }) => {
      const loginPage = new LoginPage(page);
      const nubi = new NubiLocators(page);
      const kb = new KnowledgeBaseLocators(page);

      await loginPage.doFullLogin();
      await nubi.openPanel();
      await kb.navigateToKnowledgeBase(nubi);
      await kb.navigateToUserTab();

      // A leftover from an aborted run would make the create fail on a duplicate name.
      const leftover = kb.getKBCardByName(KB_NAME);
      if (await leftover.isVisible().catch(() => false)) {
        await kb.deleteKBByName(KB_NAME);
      }

      let mappedAgent = "";

      await test.step("The create form defaults to All agents", async () => {
        await kb.openCreateModal();
        await expect(kb.agentSectionLabel).toBeVisible();
        await expect(kb.allAgentsChip).toHaveAttribute("aria-pressed", "true");
      });

      await test.step("Switching to Specific agents and ticking one agent", async () => {
        await kb.specificAgentsChip.click();
        await expect(kb.specificAgentsChip).toHaveAttribute("aria-pressed", "true");
        await expect(kb.agentSearchInput).toBeVisible();

        await expect(kb.firstAgentCheckbox).toBeVisible({ timeout: 15000 });
        mappedAgent = await kb.firstAgentName();
        expect(mappedAgent).not.toBe("");

        await kb.agentCheckbox(mappedAgent).check();
        await expect(kb.agentCheckbox(mappedAgent)).toBeChecked();
      });

      await test.step("Saving the KB reports success", async () => {
        await kb.fillForm(KB_NAME, KB_CONTENT, "Created by the agent-mapping e2e test");
        await kb.createBtn.click();
        await expect(kb.successCreated.first()).toBeVisible({ timeout: 20000 });
      });

      // The snackbar only proves ai_create_kb returned. The mapping is a separate
      // RPC per agent, so the KB is reopened and the picker read back — that is the
      // only surface that proves a row reached llm_kb_agent_mappings.
      await test.step("Reopening the KB shows the mapping was persisted", async () => {
        await expect(kb.getKBCardByName(KB_NAME)).toBeVisible({ timeout: 20000 });
        await kb.clickEditForCard(KB_NAME);

        await expect(kb.specificAgentsChip).toHaveAttribute("aria-pressed", "true");
        await expect(kb.allAgentsChip).toHaveAttribute("aria-pressed", "false");
        await expect(kb.agentCheckbox(mappedAgent)).toBeChecked();

        await kb.formCancelBtn.click();
      });
    }
  );


  // Retrieval is a second KB, mapped to All agents. The specific-agent mapping
  // above only pays off if Nubi routes the question to that exact agent, which
  // the test cannot control — so the wildcard is what makes this assertable.
  test(
    "Knowledge Base - ask Nubi a question only this KB can answer, verify the answer carries the runbook value and the KB is listed under Additional Contexts as a User KB",
    { tag: ["@dev", "@regression", "@functional"] },
    async ({ page }) => {
      test.setTimeout(300000);
      const loginPage = new LoginPage(page);
      const nubi = new NubiLocators(page);
      const kb = new KnowledgeBaseLocators(page);

      await loginPage.doFullLogin();
      await nubi.openPanel();
      await kb.navigateToKnowledgeBase(nubi);
      await kb.navigateToUserTab();

      const leftover = kb.getKBCardByName(RAG_KB_NAME);
      if (await leftover.isVisible().catch(() => false)) {
        await kb.deleteKBByName(RAG_KB_NAME);
      }

      await test.step("Create the KB mapped to All agents", async () => {
        await kb.openCreateModal();
        await kb.fillForm(RAG_KB_NAME, RAG_CONTENT, "Retrieval probe for the agent-mapping e2e test");
        await expect(kb.allAgentsChip).toHaveAttribute("aria-pressed", "true");
        await kb.createBtn.click();
        await expect(kb.successCreated.first()).toBeVisible({ timeout: 20000 });
      });

      // A new KB lands as 'processing' and is only retrievable once the embedding
      // run finishes and flips it to 'active'. Asking before that is a false
      // failure, not a mapping bug, so the status chip gates the question.
      await test.step("Wait for the KB to finish indexing", async () => {
        const card = kb.getKBCardByName(RAG_KB_NAME);
        await expect(card).toBeVisible({ timeout: 30000 });
        await expect(card.getByText(/^active$/i).first()).toBeVisible({ timeout: 180000 });
      });

      await test.step("Ask Nubi the question only the KB can answer", async () => {
        await nubi.newChatBtn.click();
        await nubi.chatTextbox.fill(RAG_QUESTION);
        await nubi.submitBtn.click();
      });

      // RUNBOOK_ID and the limit are invented, so no model can produce them from
      // its own weights or from any other tenant data — an answer carrying the
      // value can only have come through the KB mapping.
      await test.step("The answer carries the value that exists only in this KB", async () => {
        await expect(page.getByText(RUNBOOK_LIMIT).first()).toBeVisible({ timeout: 180000 });
      });

      // The prose is the user-visible proof; this is the structural one. A KB the
      // agent merely guessed at would never appear as a retrieved reference.
      await test.step("The KB is attributed under Additional Contexts", async () => {
        // The same ReferencesDrawerContent renders on two surfaces: an "Additional
        // Contexts" tab on the full /ask-nudgebee page, and a drawer opened from
        // the response rail's contexts count chip when Nubi runs as the panel.
        const contextsEntry = page
          .getByRole("tab", { name: /Additional Contexts/i })
          .or(page.getByRole("button", { name: /\d+ contexts?, open details/i }));
        await expect(contextsEntry.first()).toBeVisible({ timeout: 30000 });
        await contextsEntry.first().click();

        const kbCategoryTab = page.getByRole("tab", { name: /Knowledge Base/i }).last();
        await expect(kbCategoryTab).toBeVisible({ timeout: 15000 });
        await kbCategoryTab.click();

        await expect(page.getByText("User KB", { exact: true }).first()).toBeVisible({ timeout: 15000 });
        await expect(page.getByText(RUNBOOK_ID, { exact: false }).first()).toBeVisible({ timeout: 15000 });
      });
    }
  );

  test(
    "Knowledge Base - delete both KBs created by this suite, verify the delete snackbar and that each card leaves the listing",
    { tag: ["@dev", "@regression", "@crud"] },
    async ({ page }) => {
      const loginPage = new LoginPage(page);
      const nubi = new NubiLocators(page);
      const kb = new KnowledgeBaseLocators(page);

      await loginPage.doFullLogin();
      await nubi.openPanel();
      await kb.navigateToKnowledgeBase(nubi);
      await kb.navigateToUserTab();

      for (const name of [KB_NAME, RAG_KB_NAME]) {
        await kb.deleteKBByName(name);
        await expect(kb.getKBCardByName(name)).toHaveCount(0, { timeout: 20000 });
      }
    }
  );
});
