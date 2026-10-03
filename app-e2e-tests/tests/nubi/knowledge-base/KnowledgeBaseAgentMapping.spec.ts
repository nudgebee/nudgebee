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
    "Knowledge Base - create a KB from the form, verify the mapping mutation carries the every-agent wildcard and that reopening the KB for edit offers no mapping control to change it",
    { tag: ["@dev", "@regression", "@functional", "@crud"] },
    async ({ page }) => {
      const loginPage = new LoginPage(page);
      const nubi = new NubiLocators(page);
      const kb = new KnowledgeBaseLocators(page);

      // The form's agent-picker checklist was removed with the notes/sources
      // redesign (enterprise#37607): a create now always maps to every agent
      // through the wildcard row and an edit re-submits whatever mapping the KB
      // already had, so "which agents" is expressed by @-mentioning them in the
      // note instead. The outgoing MapKBToAgent mutation is therefore the only
      // surface left that proves a row reached llm_kb_agent_mappings, and which
      // agent id it carried.
      const mappedAgentIds = kb.trackAgentMappings();

      await loginPage.doFullLogin();
      await nubi.openPanel();
      await kb.navigateToKnowledgeBase(nubi);
      await kb.navigateToUserTab();

      // A leftover from an aborted run would make the create fail on a duplicate
      // name — and an aborted run can leave more than one.
      await kb.removeAllKBsNamed(KB_NAME);

      await test.step("The create form carries no agent-mapping control", async () => {
        await kb.openCreateModal();
        await expect(kb.allAgentsChip).toHaveCount(0);
        await expect(kb.specificAgentsChip).toHaveCount(0);
        // What replaced it: the content field's own placeholder is where the
        // form now tells the user to name agents.
        await expect(kb.contentTextarea).toHaveAttribute("placeholder", /Mention agents that should use this/);
      });

      await test.step("Saving the KB reports success", async () => {
        await kb.fillForm(KB_NAME, KB_CONTENT, "Created by the agent-mapping e2e test");
        await kb.createBtn.click();
        await expect(kb.successCreated.first()).toBeVisible({ timeout: 20000 });
      });

      // The snackbar only proves ai_create_kb returned. The mapping is a separate
      // mutation, so the recorded agent ids are what prove it was sent at all —
      // and that it was the wildcard rather than some agent the form picked.
      await test.step("The KB was mapped to every agent through the wildcard", async () => {
        await expect.poll(() => mappedAgentIds.ids, { timeout: 30000, message: "no MapKBToAgent mutation was sent for the created KB" }).toContain("*");
      });

      await test.step("Reopening the KB still offers no mapping control", async () => {
        await expect(kb.getKBCardByName(KB_NAME)).toBeVisible({ timeout: 20000 });
        await kb.clickEditForCard(KB_NAME);

        await expect(kb.allAgentsChip).toHaveCount(0);
        await expect(kb.specificAgentsChip).toHaveCount(0);

        await kb.formCancelBtn.click();
      });
    }
  );


  // Retrieval is a second KB. It rides the same every-agent wildcard the case
  // above asserts, which is what makes this assertable: the test cannot control
  // which agent Nubi routes the question to.
  test(
    "Knowledge Base - ask Nubi a question only this KB can answer, verify the answer carries the runbook value and the KB is listed under Additional Contexts as a User KB",
    { tag: ["@dev", "@regression", "@functional", "@crud"] },
    async ({ page }) => {
      test.setTimeout(300000);
      const loginPage = new LoginPage(page);
      const nubi = new NubiLocators(page);
      const kb = new KnowledgeBaseLocators(page);

      await loginPage.doFullLogin();
      await nubi.openPanel();
      await kb.navigateToKnowledgeBase(nubi);
      await kb.navigateToUserTab();

      await kb.removeAllKBsNamed(RAG_KB_NAME);

      // Every KB created through this form maps to all agents (the wildcard row
      // the case above asserts), which is what makes the retrieval below
      // assertable at all: the test cannot control which agent Nubi routes to.
      await test.step("Create the KB", async () => {
        await kb.openCreateModal();
        await kb.fillForm(RAG_KB_NAME, RAG_CONTENT, "Retrieval probe for the agent-mapping e2e test");
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
        // The chat is behind the b-Cortex modal, and its dialog container
        // intercepts every click while it is up.
        await kb.closeBCortex();
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
