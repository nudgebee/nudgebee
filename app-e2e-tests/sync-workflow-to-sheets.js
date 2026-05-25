/**
 * sync-workflow-to-sheets.js
 *
 * Syncs all 4 Workflow test cases to the Nudgebee QA Google Sheet.
 *
 * RULES (enforced at runtime):
 *  1. Always asks Executed By, Assignee, Release, Bugs, Remarks before writing
 *  2. Never silently overwrites existing TCs — asks user permission first
 *  3. Reports exactly which rows/TCs would be overridden
 *  4. Checks if sub-sheet already exists → appends to it; if not → creates new
 *  5. Col B (module name) uses =HYPERLINK formula to link to the Workflows tab;
 *     col C (TC name) is plain text — no per-TC sub-sheet exists for Workflows
 *
 * Sheets updated:
 *  - "Workflows"                 → module sheet with 4 TC rows
 *  - "Automation TCs - Overview" → 4 TC entries, all 13 columns filled
 *  - "Modules/Category wise"     → 1 row for Workflows module
 *
 * Usage:
 *   node sync-workflow-to-sheets.js
 */

require("dotenv").config();
const path     = require("path");
const readline = require("readline");
const { google } = require("googleapis");
const fs       = require("fs");

// ─── Config ───────────────────────────────────────────────────────────────────
const SPREADSHEET_ID = process.env.GOOGLE_SHEET_ID;
const CREDS_PATH     = path.resolve(__dirname, process.env.GOOGLE_SHEETS_CREDENTIALS_FILE || "credentials/sheets-credentials.json");

if (!SPREADSHEET_ID) { console.error("❌  GOOGLE_SHEET_ID not set in .env"); process.exit(1); }
if (!fs.existsSync(CREDS_PATH)) { console.error(`❌  Credentials file not found: ${CREDS_PATH}`); process.exit(1); }

// ─── Sheet names ─────────────────────────────────────────────────────────────
const WORKFLOWS_SHEET  = "Workflows";
const OVERVIEW_SHEET   = "Automation TCs - Overview";
const MODULES_SHEET    = "Modules/Category wise";
// NOTE: workflowsGid is fetched dynamically at runtime (see main()) so the
// script works correctly whether the sheet already exists or is newly created.

// ─── Exact formatting matching other module sheets ────────────────────────────
const HEADER_BG  = { red: 0.1882353, green: 0.32941177, blue: 0.5882353 };
const HEADER_FG  = { red: 1, green: 1, blue: 1 };
const DATA_FG    = { red: 0.2627451, green: 0.2627451, blue: 0.2627451 };
const BORDER     = { style: "SOLID", width: 1, color: {} };
const COL_WIDTHS = [82, 180, 234, 180, 180, 336, 322, 385, 100, 115, 148, 120, 130, 100, 100, 100, 100];

// ─── TC data ──────────────────────────────────────────────────────────────────
const WORKFLOWS_HEADER_ROW = [
  "TC ID", "Module/Category", "Test Case Title", "Test Case Description",
  "Prerequisites/Pre-conditions", "Test Steps", "Test Data", "Expected Results",
  "Actual Results", "Status", "Automated", "Priority", "Executed By",
  "Last Execution Date", "Comments/Notes", "File Path", "Defect ID",
];

const WORKFLOW_TCS = [
  {
    category:      "Workflow Notifications",
    title:         "TC_Workflow_Automation workflow Slack Notification",
    description:   "Verifies that a new workflow with Slack notification task can be created, activated, and successfully executed via manual trigger",
    prerequisites: "User should have valid tenant admin access. Slack integration must be configured in the tenant. Slack channel ID must be available",
    steps:         "1. Login to Nudgebee application to proper tenant\n2. Navigate to Workflows section\n3. Click to create a New Workflow\n4. Paste the workflow JSON configuration (with Slack notification task, manual trigger, and ACTIVE status)\n5. Apply the JSON\n6. Save the workflow with auto-generated name\n7. Set workflow status to ACTIVE and save\n8. Run the workflow manually\n9. Validate GraphQL response for workflow execution",
    testData:      `1. Application URL - ${process.env.BASE_URL}/\n2. Workflow JSON with task type: notifications.im\n3. Provider: slack\n4. Channel ID: ${process.env.SLACK_IM_CHANNEL_ID}\n5. Message: "${process.env.SLACK_IM_MESSAGE}"`,
    expected:      "Workflow should be created and saved successfully. After running, GraphQL validation should pass and Slack notification should be sent to the configured channel",
    status:        "Passed",
    automated:     "Yes",
    priority:      "P1",
    comments:      "Workflow name is auto-generated with timestamp. Timeout: 120s. Retry policy: max 3 attempts",
    filePath:      "nudgebee\\app-e2e-tests\\tests\\workflow\\slacknotification.spec.ts",
  },
  {
    category:      "Workflow Notifications",
    title:         "TC_Workflow_Automation workflow G-chat Notification",
    description:   "Verifies that a new workflow with Google Chat notification task can be created, activated, and successfully executed via manual trigger",
    prerequisites: "User should have valid tenant admin access. Google Chat integration must be configured in the tenant. Google Chat space ID must be available",
    steps:         "1. Login to Nudgebee application to proper tenant\n2. Navigate to Workflows section\n3. Click to create a New Workflow\n4. Paste the workflow JSON configuration (with G-chat notification task, manual trigger, and ACTIVE status)\n5. Apply the JSON\n6. Save the workflow with auto-generated name\n7. Set workflow status to ACTIVE and save\n8. Run the workflow manually\n9. Validate GraphQL response for workflow execution",
    testData:      `1. Application URL - ${process.env.BASE_URL}/\n2. Workflow JSON with task type: notifications.im\n3. Provider: google_chat\n4. Channel: ${process.env.GCHAT_SPACE_ID}\n5. Message: "${process.env.GCHAT_IM_MESSAGE}"`,
    expected:      "Workflow should be created and saved successfully. After running, GraphQL validation should pass and Google Chat notification should be sent to the configured space",
    status:        "Passed",
    automated:     "Yes",
    priority:      "P1",
    comments:      "Workflow name is auto-generated with timestamp. Timeout: 120s. Retry policy: max 3 attempts",
    filePath:      "nudgebee\\app-e2e-tests\\tests\\workflow\\Gchatnotification.spec.ts",
  },
  {
    category:      "Workflow Notifications",
    title:         "TC_Workflow_Automation workflow Ms-teams Notification",
    description:   "Verifies that a new workflow with Microsoft Teams notification task can be created, activated, and successfully executed via manual trigger",
    prerequisites: "User should have valid tenant admin access. MS Teams integration must be configured in the tenant. Teams channel thread ID and team ID must be available",
    steps:         "1. Login to Nudgebee application to proper tenant\n2. Navigate to Workflows section\n3. Click to create a New Workflow\n4. Paste the workflow JSON configuration (with MS Teams notification task, manual trigger, and ACTIVE status)\n5. Apply the JSON\n6. Save the workflow with auto-generated name\n7. Set workflow status to ACTIVE and save\n8. Run the workflow manually\n9. Validate GraphQL response for workflow execution",
    testData:      `1. Application URL - ${process.env.BASE_URL}/\n2. Workflow JSON with task type: notifications.im\n3. Provider: ms_teams\n4. Channel: Teams channel thread ID\n5. Team ID: ${process.env.MSTEAMS_TEAM_ID}`,
    expected:      "Workflow should be created and saved successfully. After running, GraphQL validation should pass and MS Teams notification should be sent to the configured channel",
    status:        "Passed",
    automated:     "Yes",
    priority:      "P1",
    comments:      "Workflow name is auto-generated with timestamp. Timeout: 120s. Retry policy: max 3 attempts",
    filePath:      "nudgebee\\app-e2e-tests\\tests\\workflow\\MsTeamsnotification.spec.ts",
  },
  {
    category:      "Workflow Notifications",
    title:         "TC_Workflow_Automation workflow Email",
    description:   "Verifies that a new workflow with email notification task can be created, activated, and successfully executed via manual trigger",
    prerequisites: "User should have valid tenant admin access. Email/SMTP configuration must be set up in the tenant",
    steps:         "1. Login to Nudgebee application to proper tenant\n2. Navigate to Workflows section\n3. Click to create a New Workflow\n4. Paste the workflow JSON configuration (with email notification task, manual trigger, and ACTIVE status)\n5. Apply the JSON\n6. Save the workflow with auto-generated name\n7. Set workflow status to ACTIVE and save\n8. Run the workflow manually\n9. Validate GraphQL response for workflow execution",
    testData:      `1. Application URL - ${process.env.BASE_URL}/\n2. Workflow JSON with task type: notifications.email\n3. Recipient: ${process.env.USER_1_EMAIL}\n4. Subject: "${process.env.EMAIL_SUBJECT}"\n5. Body: Nudgebee welcome email content`,
    expected:      "Workflow should be created and saved successfully. After running, GraphQL validation should pass and email should be delivered to the specified recipient",
    status:        "Passed",
    automated:     "Yes",
    priority:      "P1",
    comments:      "Workflow name is auto-generated with timestamp. Timeout: 120s. Retry policy: max 3 attempts",
    filePath:      "nudgebee\\app-e2e-tests\\tests\\workflow\\SendEmail.spec.ts",
  },
];

// ─── Helpers ──────────────────────────────────────────────────────────────────

// Interactive prompt — returns trimmed user input
function ask(rl, question) {
  return new Promise((resolve) => rl.question(question, (ans) => resolve(ans.trim())));
}

// Ask for a name — must be Vanshika or Utsav (case-insensitive)
async function askTesterName(rl, fieldLabel) {
  while (true) {
    const input = await ask(rl, `   ${fieldLabel} (Vanshika / Utsav): `);
    const name = input.charAt(0).toUpperCase() + input.slice(1).toLowerCase();
    if (name === "Vanshika" || name === "Utsav") return name;
    console.log(`   ⚠️  Invalid input "${input}". Please enter Vanshika or Utsav.`);
  }
}

function tcToRow(tc, executedBy) {
  return [
    "",              // A  TC ID (always empty)
    tc.category,    // B  Module/Category
    tc.title,       // C  Test Case Title
    tc.description, // D  Description
    tc.prerequisites,// E Prerequisites
    tc.steps,       // F  Test Steps
    tc.testData,    // G  Test Data
    tc.expected,    // H  Expected Results
    "",             // I  Actual Results (empty for new)
    tc.status,      // J  Status
    tc.automated,   // K  Automated
    tc.priority,    // L  Priority
    executedBy,     // M  Executed By
    "",             // N  Last Execution Date (empty)
    tc.comments,    // O  Comments/Notes
    tc.filePath,    // P  File Path
    "",             // Q  Defect ID (empty)
  ];
}

function repeatCell(sheetId, r1, r2, c1, c2, cell, fields) {
  return { repeatCell: { range: { sheetId, startRowIndex: r1, endRowIndex: r2, startColumnIndex: c1, endColumnIndex: c2 }, cell, fields } };
}

// ─── Main ─────────────────────────────────────────────────────────────────────
async function main() {
  const rl = readline.createInterface({ input: process.stdin, output: process.stdout });

  try {
    console.log("\n━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━");
    console.log("  Nudgebee QA Sheet — Workflow TC Sync");
    console.log("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━\n");

    // ── Auth ──────────────────────────────────────────────────────────────
    const auth   = new google.auth.GoogleAuth({ keyFile: CREDS_PATH, scopes: ["https://www.googleapis.com/auth/spreadsheets"] });
    const sheets = google.sheets({ version: "v4", auth });

    // ══════════════════════════════════════════════════════════════════════
    // STEP 1 — Check sheet FIRST, show what's new, ask before proceeding
    // ══════════════════════════════════════════════════════════════════════
    console.log("🔍  Checking existing TCs in sheet...\n");

    const meta           = await sheets.spreadsheets.get({ spreadsheetId: SPREADSHEET_ID });
    const existingSheets = meta.data.sheets.map((s) => s.properties.title);
    const wfSheetExists  = existingSheets.includes(WORKFLOWS_SHEET);

    let workflowsGid     = wfSheetExists
      ? meta.data.sheets.find((s) => s.properties.title === WORKFLOWS_SHEET).properties.sheetId
      : null;

    let existingWfTitles = [];
    let existingWfRows   = [];

    if (wfSheetExists) {
      const wfRes      = await sheets.spreadsheets.values.get({ spreadsheetId: SPREADSHEET_ID, range: `${WORKFLOWS_SHEET}!C:C` });
      existingWfTitles = (wfRes.data.values || []).flat().map((v) => v.trim());
      const wfRowsRes  = await sheets.spreadsheets.values.get({ spreadsheetId: SPREADSHEET_ID, range: `${WORKFLOWS_SHEET}!A:Q` });
      existingWfRows   = wfRowsRes.data.values || [];
    }

    const newTCs       = WORKFLOW_TCS.filter((tc) => !existingWfTitles.includes(tc.title));
    const duplicateTCs = WORKFLOW_TCS.filter((tc) =>  existingWfTitles.includes(tc.title));

    // ── Show TCs not yet in sheet ─────────────────────────────────────────
    if (newTCs.length === 0) {
      console.log("✅  All TCs are already present in the sheet. Nothing to add.\n");
      console.log("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━\n");
      rl.close();
      return;
    }

    console.log(`📋  The following ${newTCs.length} TC(s) are NOT yet in the sheet:\n`);
    newTCs.forEach((tc, i) => {
      console.log(`   ${i + 1}. ${tc.title}`);
      console.log(`      Category : ${tc.category}`);
      console.log(`      Status   : ${tc.status}`);
      console.log(`      File     : ${tc.filePath}\n`);
    });

    if (duplicateTCs.length > 0) {
      console.log(`ℹ️   Already in sheet (will be skipped):`);
      duplicateTCs.forEach((tc) => {
        const rowIndex = existingWfTitles.indexOf(tc.title) + 1;
        console.log(`   - Row ${rowIndex}: ${tc.title}`);
      });
      console.log();
    }

    // ── Ask: proceed? ─────────────────────────────────────────────────────
    const proceed = await ask(rl, `➡️   Do you want to add these ${newTCs.length} TC(s) to the sheet? (yes / no): `);
    if (proceed.toLowerCase() !== "yes") {
      console.log("\n🚫  Aborted. No changes made.\n");
      rl.close();
      return;
    }

    // ══════════════════════════════════════════════════════════════════════
    // STEP 2 — Follow-up questions (only after user confirms)
    // ══════════════════════════════════════════════════════════════════════
    console.log("\n📝  Please answer the following before syncing:\n");

    const executedBy = await askTesterName(rl, "1. Executed By  — who ran the tests? (sub-sheet col M)");
    const assignee   = await askTesterName(rl, "2. Assignee     — who is responsible? (Overview col L)");
    const release    = await ask(rl,           "3. Release      — which iteration? (e.g. Iteration-158): ");
    const bugsRaw    = await ask(rl,           "4. Bugs/Tickets — any bug IDs or PR refs? (press Enter to skip): ");
    const remarksRaw = await ask(rl,           "5. Remarks      — any comments to add? (press Enter to skip): ");

    const bugs    = bugsRaw.toLowerCase()    === "none" ? "" : bugsRaw;
    const remarks = remarksRaw.toLowerCase() === "none" ? "" : remarksRaw;

    console.log(`\n   ✅  Executed By : ${executedBy}`);
    console.log(`   ✅  Assignee    : ${assignee}`);
    console.log(`   ✅  Release     : ${release || "(blank)"}`);
    console.log(`   ✅  Bugs        : ${bugs    || "(none)"}`);
    console.log(`   ✅  Remarks     : ${remarks || "(none)"}\n`);

    // ══════════════════════════════════════════════════════════════════════
    // STEP 3 — Create Workflows sheet tab if missing
    // ══════════════════════════════════════════════════════════════════════
    if (!wfSheetExists) {
      console.log(`\n➕  Creating sheet tab: "${WORKFLOWS_SHEET}"...`);
      const addRes = await sheets.spreadsheets.batchUpdate({
        spreadsheetId: SPREADSHEET_ID,
        requestBody: { requests: [{ addSheet: { properties: { title: WORKFLOWS_SHEET } } }] },
      });
      workflowsGid = addRes.data.replies[0].addSheet.properties.sheetId;
      console.log(`   ✅  Created "${WORKFLOWS_SHEET}" (GID: ${workflowsGid})`);
    }

    // Write/overwrite header row
    await sheets.spreadsheets.values.update({
      spreadsheetId: SPREADSHEET_ID,
      range: `${WORKFLOWS_SHEET}!A1:Q1`,
      valueInputOption: "RAW",
      requestBody: { values: [WORKFLOWS_HEADER_ROW] },
    });

    // ══════════════════════════════════════════════════════════════════════
    // STEP 4 — Append only NEW TC rows to Workflows sheet
    // ══════════════════════════════════════════════════════════════════════
    console.log(`\n✏️   Writing ${newTCs.length} new TC(s) to "${WORKFLOWS_SHEET}" sheet...`);

    // Existing data rows = existingWfRows minus header (row 1)
    const existingDataCount = Math.max(existingWfRows.length - 1, 0);
    const firstNewRow       = existingDataCount + 2; // row after last existing data row
    const lastNewRow        = firstNewRow + newTCs.length - 1;

    const tcRows = newTCs.map((tc) => tcToRow(tc, executedBy));
    await sheets.spreadsheets.values.update({
      spreadsheetId: SPREADSHEET_ID,
      range: `${WORKFLOWS_SHEET}!A${firstNewRow}:Q${lastNewRow}`,
      valueInputOption: "RAW",
      requestBody: { values: tcRows },
    });
    console.log(`   ✅  ${newTCs.length} TC row(s) written (rows ${firstNewRow}–${lastNewRow})`);

    // ══════════════════════════════════════════════════════════════════════
    // STEP 5 — Apply formatting to Workflows sheet
    // ══════════════════════════════════════════════════════════════════════
    console.log("\n🎨  Applying formatting...");
    const totalRows = 1 + existingDataCount + newTCs.length;

    const formatRequests = [
      // Header row
      repeatCell(workflowsGid, 0, 1, 0, 17, {
        userEnteredFormat: {
          backgroundColor: HEADER_BG,
          textFormat: { foregroundColor: HEADER_FG, fontFamily: "Roboto", fontSize: 12, bold: false },
          horizontalAlignment: "LEFT", verticalAlignment: "MIDDLE",
          wrapStrategy: "CLIP",
          borders: { top: BORDER, bottom: BORDER, left: BORDER, right: BORDER },
          padding: { left: 8, right: 8 },
        },
      }, "userEnteredFormat(backgroundColor,textFormat,horizontalAlignment,verticalAlignment,wrapStrategy,borders,padding)"),

      // Data rows
      repeatCell(workflowsGid, 1, totalRows + 1, 0, 17, {
        userEnteredFormat: {
          backgroundColor: { red: 1, green: 1, blue: 1 },
          textFormat: { foregroundColor: DATA_FG, fontFamily: "Roboto", fontSize: 10, bold: false },
          horizontalAlignment: "LEFT", verticalAlignment: "MIDDLE",
          wrapStrategy: "CLIP",
          borders: { top: BORDER, bottom: BORDER, left: BORDER, right: BORDER },
          padding: { left: 8, right: 8 },
        },
      }, "userEnteredFormat(backgroundColor,textFormat,horizontalAlignment,verticalAlignment,wrapStrategy,borders,padding)"),

      // All row heights = 20px
      { updateDimensionProperties: { range: { sheetId: workflowsGid, dimension: "ROWS", startIndex: 0, endIndex: totalRows + 1 }, properties: { pixelSize: 20 }, fields: "pixelSize" } },

      // Freeze row 1
      { updateSheetProperties: { properties: { sheetId: workflowsGid, gridProperties: { frozenRowCount: 1 } }, fields: "gridProperties.frozenRowCount" } },

      // AutoFilter
      { setBasicFilter: { filter: { range: { sheetId: workflowsGid, startRowIndex: 0, endRowIndex: totalRows + 1, startColumnIndex: 0, endColumnIndex: 17 } } } },
    ];

    // Column widths
    COL_WIDTHS.forEach((w, i) => {
      formatRequests.push({ updateDimensionProperties: { range: { sheetId: workflowsGid, dimension: "COLUMNS", startIndex: i, endIndex: i + 1 }, properties: { pixelSize: w }, fields: "pixelSize" } });
    });

    await sheets.spreadsheets.batchUpdate({ spreadsheetId: SPREADSHEET_ID, requestBody: { requests: formatRequests } });
    console.log("   ✅  Formatting applied (Roboto, borders, 20px rows, freeze, AutoFilter)");

    // ══════════════════════════════════════════════════════════════════════
    // STEP 6 — Overview sheet: append only new TCs
    // ══════════════════════════════════════════════════════════════════════
    console.log(`\n📋  Updating Overview sheet with ${newTCs.length} new TC(s)...`);

    const ovRes    = await sheets.spreadsheets.values.get({ spreadsheetId: SPREADSHEET_ID, range: `${OVERVIEW_SHEET}!C:C` });
    const ovTitles = (ovRes.data.values || []).flat().map((v) => v.trim());
    const ovRows   = (await sheets.spreadsheets.values.get({ spreadsheetId: SPREADSHEET_ID, range: `${OVERVIEW_SHEET}!A:M` })).data.values || [];

    // Counts reflect ALL workflow TCs in the sheet after this sync
    const allWfTCs      = WORKFLOW_TCS; // full set (existing + new)
    const totalAuthored = allWfTCs.length;
    const passedCount   = allWfTCs.filter(tc => tc.status === "Passed").length;
    const failedCount   = allWfTCs.length - passedCount;

    // isFirst = true only for the very first TC of this group being written now
    function buildOverviewRow(tc, isFirst) {
      return [
        "",           // A  TC ID (empty)
        isFirst ? `=HYPERLINK("#gid=${workflowsGid}","${WORKFLOWS_SHEET}")` : "",
        tc.title,     // C  TC name
        tc.status,    // D  Status
        "YES",        // E  Test Cases Authored?
        isFirst ? String(totalAuthored) : "",  // F Total authored
        isFirst ? String(passedCount)   : "",  // G Passed TCs
        isFirst ? String(failedCount)   : "",  // H Failed TCs
        isFirst ? String(totalAuthored) : "",  // I Total TCs
        isFirst ? bugs    : "",                // J Bugs/Tickets
        isFirst ? remarks : "",                // K Remarks
        assignee,                              // L Assignee (ALL rows)
        isFirst ? release : "",                // M Release (first row only)
      ];
    }

    // Only write new TCs (duplicates already exist in Overview)
    const newOvTCs = newTCs.filter((tc) => !ovTitles.includes(tc.title));

    if (newOvTCs.length === 0) {
      console.log(`   ℹ️   All TCs already present in Overview — skipping.`);
    } else {
      const lastOvRow  = ovRows.length;
      const sepRow     = lastOvRow + 1;
      const firstTCRow = lastOvRow + 2;

      const ovNewRows = [
        Array(13).fill(""),                      // blank separator
        buildOverviewRow(newOvTCs[0], true),
        ...newOvTCs.slice(1).map((tc) => buildOverviewRow(tc, false)),
      ];

      await sheets.spreadsheets.values.update({
        spreadsheetId: SPREADSHEET_ID,
        range: `${OVERVIEW_SHEET}!A${sepRow}:M${sepRow + ovNewRows.length - 1}`,
        valueInputOption: "USER_ENTERED",
        requestBody: { values: ovNewRows },
      });
      console.log(`   ✅  Separator at row ${sepRow}`);
      console.log(`   ✅  TCs written at rows ${firstTCRow}–${firstTCRow + newOvTCs.length - 1}`);
      console.log(`   ✅  Col B row ${firstTCRow}: HYPERLINK → "${WORKFLOWS_SHEET}" tab`);
      console.log(`   ✅  Col F: ${totalAuthored} authored | Col G: ${passedCount} passed | Col H: ${failedCount} failed | Col I: ${totalAuthored} total`);
      console.log(`   ✅  Col L (Assignee): ${assignee} — all rows`);
      console.log(`   ✅  Col M (Release): ${release} — first row`);
      if (bugs)    console.log(`   ✅  Col J (Bugs): ${bugs}`);
      if (remarks) console.log(`   ✅  Col K (Remarks): ${remarks}`);
    }

    // ══════════════════════════════════════════════════════════════════════
    // STEP 7 — Modules/Category wise sheet
    // ══════════════════════════════════════════════════════════════════════
    const modRes   = await sheets.spreadsheets.values.get({ spreadsheetId: SPREADSHEET_ID, range: `${MODULES_SHEET}!A:B` });
    const modRows  = modRes.data.values || [];
    const modNames = modRows.map((r) => (r[0] || "").trim());

    if (modNames.includes(WORKFLOWS_SHEET)) {
      console.log(`\nℹ️   "${WORKFLOWS_SHEET}" already in Modules/Category wise — skipping.`);
    } else {
      const sheetMeta = meta.data.sheets.find((s) => s.properties.title === MODULES_SHEET);
      if (sheetMeta == null) {
        console.warn(`\n⚠️   Could not find sheet metadata for "${MODULES_SHEET}" — skipping Modules/Category wise update.`);
      } else {
        const insertIndex = modRows.length - 1; // before last SUM row
        await sheets.spreadsheets.batchUpdate({
          spreadsheetId: SPREADSHEET_ID,
          requestBody: { requests: [{ insertDimension: { range: { sheetId: sheetMeta.properties.sheetId, dimension: "ROWS", startIndex: insertIndex, endIndex: insertIndex + 1 }, inheritFromBefore: true } }] },
        });
        await sheets.spreadsheets.values.update({
          spreadsheetId: SPREADSHEET_ID,
          range: `${MODULES_SHEET}!A${insertIndex + 1}:B${insertIndex + 1}`,
          valueInputOption: "RAW",
          requestBody: { values: [[WORKFLOWS_SHEET, WORKFLOW_TCS.length]] },
        });
        console.log(`\n   ✅  Modules/Category wise: "Workflows" added with count ${WORKFLOW_TCS.length}`);
      }
    }

    // ══════════════════════════════════════════════════════════════════════
    // DONE
    // ══════════════════════════════════════════════════════════════════════
    console.log("\n━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━");
    console.log(`🎉  Done! ${newTCs.length} new TC(s) added.`);
    console.log(`    Executed By : ${executedBy}`);
    console.log(`    Assignee    : ${assignee}`);
    console.log(`    Release     : ${release || "(blank)"}`);
    console.log(`    Bugs        : ${bugs    || "(none)"}`);
    console.log(`    Remarks     : ${remarks || "(none)"}`);
    console.log("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━\n");

  } finally {
    rl.close();
  }
}

main().catch((err) => {
  console.error("❌  Error:", err.message || err);
  if (err.response?.data) console.error(JSON.stringify(err.response.data, null, 2));
  process.exit(1);
});
