/**
 * fix-workflow-sheet.js  (v3 — full format match with Accounts Settings)
 *
 * Fixes Workflows sheet formatting to exactly match other module sheets:
 *  - Row height: 20px (not 80px)
 *  - WrapStrategy: CLIP (not WRAP)
 *  - Font: Roboto, 12px header / 10px data, colour #434343 for data
 *  - Borders: SOLID 1px on all cells
 *  - Column widths matching Accounts Settings sheet
 *  - Freeze row 1
 *  - AutoFilter on header row
 *  - Adds "Executed By" value to Col M for all data rows
 *
 * Usage:  node fix-workflow-sheet.js
 *         TC_executedBy=Utsav node fix-workflow-sheet.js  (non-interactive)
 */

require("dotenv").config();
const path     = require("path");
const readline = require("readline");
const { google } = require("googleapis");

// ── CONFIG ────────────────────────────────────────────────────────────────────
const SPREADSHEET_ID  = process.env.GOOGLE_SHEET_ID;
const CREDS_PATH      = path.resolve(__dirname, process.env.GOOGLE_SHEETS_CREDENTIALS_FILE || "credentials/sheets-credentials.json");
const WORKFLOWS_SHEET = "Workflows";
// NOTE: workflowsGid is fetched dynamically at runtime so the script works
// across different spreadsheets where the sheet GID may differ.
// NOTE: executedBy is prompted interactively (or via TC_executedBy env var).


// ── Exact values copied from Accounts Settings sheet via API ──────────────────
const HEADER_BG   = { red: 0.1882353, green: 0.32941177, blue: 0.5882353 };
const HEADER_FG   = { red: 1, green: 1, blue: 1 };
const DATA_FG     = { red: 0.2627451, green: 0.2627451, blue: 0.2627451 };
const BORDER      = { style: "SOLID", width: 1, color: {} };

// Column widths (px) — exactly matching Accounts Settings sheet (17 cols A–Q)
const COL_WIDTHS  = [82, 180, 234, 180, 180, 336, 322, 385, 100, 115, 148, 120, 130, 100, 100, 100, 100];

// ─── Helper — build repeatCell request ───────────────────────────────────────
function repeatCell(sheetId, r1, r2, c1, c2, cell, fields) {
  return {
    repeatCell: {
      range:  { sheetId, startRowIndex: r1, endRowIndex: r2, startColumnIndex: c1, endColumnIndex: c2 },
      cell,
      fields,
    },
  };
}

// ─── Helpers ──────────────────────────────────────────────────────────────────
function ask(rl, q) { return new Promise(r => rl.question(q, a => r(a.trim()))); }

async function askTesterName(rl, label) {
  while (true) {
    const v = await ask(rl, `   ${label} (Vanshika / Utsav): `);
    const n = v.charAt(0).toUpperCase() + v.slice(1).toLowerCase();
    if (n === "Vanshika" || n === "Utsav") return n;
    console.log("   ⚠️  Enter Vanshika or Utsav only.");
  }
}

// ─── Main ─────────────────────────────────────────────────────────────────────
async function main() {
  const rl = readline.createInterface({ input: process.stdin, output: process.stdout });
  try {
  // Resolve executedBy — env var (non-interactive) or interactive prompt
  let executedBy;
  if (process.env.TC_executedBy) {
    executedBy = process.env.TC_executedBy;
    console.log(`📝  Executed By: ${executedBy} (from TC_executedBy env var)`);
  } else {
    console.log("\n📝  Please answer before we begin:\n");
    executedBy = await askTesterName(rl, "Executed By (module sheet col M)");
  }

  const auth   = new google.auth.GoogleAuth({ keyFile: CREDS_PATH, scopes: ["https://www.googleapis.com/auth/spreadsheets"] });
  const sheets = google.sheets({ version: "v4", auth });

  // Fetch GID dynamically by sheet name
  const meta = await sheets.spreadsheets.get({ spreadsheetId: SPREADSHEET_ID });
  const wfSheet = meta.data.sheets.find((s) => s.properties.title === WORKFLOWS_SHEET);
  if (!wfSheet) {
    console.error(`❌  Sheet "${WORKFLOWS_SHEET}" not found in the spreadsheet.`);
    process.exit(1);
  }
  const workflowsGid = wfSheet.properties.sheetId;
  console.log(`\n📋  Found "${WORKFLOWS_SHEET}" (GID: ${workflowsGid})`);

  // How many data rows exist?
  const valRes = await sheets.spreadsheets.values.get({
    spreadsheetId: SPREADSHEET_ID,
    range: `${WORKFLOWS_SHEET}!C:C`,  // col A always empty, use col C (TC Title)
  });
  const totalRows = (valRes.data.values || []).length; // includes header
  const dataRows  = totalRows - 1;
  console.log(`📋  Workflows sheet: ${totalRows} rows total (1 header + ${dataRows} data rows)`);

  // ══════════════════════════════════════════════════════════════════
  // STEP 1 — Fill "Executed By" (Col M = index 12) for all data rows
  // ══════════════════════════════════════════════════════════════════
  if (dataRows > 0) {
    console.log(`\n✏️   Writing "${executedBy}" to Executed By column (M2:M${totalRows})...`);
    const executedByValues = Array.from({ length: dataRows }, () => [executedBy]);
    await sheets.spreadsheets.values.update({
      spreadsheetId: SPREADSHEET_ID,
      range: `${WORKFLOWS_SHEET}!M2:M${totalRows}`,
      valueInputOption: "RAW",
      requestBody: { values: executedByValues },
    });
    console.log("✅  Executed By filled");
  }

  // ══════════════════════════════════════════════════════════════════
  // STEP 2 — Apply all formatting in one batchUpdate
  // ══════════════════════════════════════════════════════════════════
  console.log("\n🎨  Applying exact formatting (matching Accounts Settings)...");

  const requests = [];

  // 2a. Header row — blue BG, white Roboto 12 text, CLIP, borders, LEFT+MIDDLE
  requests.push(repeatCell(
    workflowsGid, 0, 1, 0, 17,
    {
      userEnteredFormat: {
        backgroundColor: HEADER_BG,
        textFormat: { foregroundColor: HEADER_FG, fontFamily: "Roboto", fontSize: 12, bold: false },
        horizontalAlignment: "LEFT",
        verticalAlignment: "MIDDLE",
        wrapStrategy: "CLIP",
        borders: { top: BORDER, bottom: BORDER, left: BORDER, right: BORDER },
        padding: { left: 8, right: 8 },
      },
    },
    "userEnteredFormat(backgroundColor,textFormat,horizontalAlignment,verticalAlignment,wrapStrategy,borders,padding)"
  ));

  // 2b. Data rows — white BG, grey Roboto 10 text, CLIP, borders, LEFT+MIDDLE
  const dataEndRow = Math.max(totalRows, 6); // format at least 6 rows in case more are added later
  requests.push(repeatCell(
    workflowsGid, 1, dataEndRow, 0, 17,
    {
      userEnteredFormat: {
        backgroundColor: { red: 1, green: 1, blue: 1 },
        textFormat: { foregroundColor: DATA_FG, fontFamily: "Roboto", fontSize: 10, bold: false },
        horizontalAlignment: "LEFT",
        verticalAlignment: "MIDDLE",
        wrapStrategy: "CLIP",
        borders: { top: BORDER, bottom: BORDER, left: BORDER, right: BORDER },
        padding: { left: 8, right: 8 },
      },
    },
    "userEnteredFormat(backgroundColor,textFormat,horizontalAlignment,verticalAlignment,wrapStrategy,borders,padding)"
  ));

  // 2c. All rows height = 20px
  requests.push({
    updateDimensionProperties: {
      range: { sheetId: workflowsGid, dimension: "ROWS", startIndex: 0, endIndex: dataEndRow },
      properties: { pixelSize: 20 },
      fields: "pixelSize",
    },
  });

  // 2d. Column widths (exact match with Accounts Settings)
  COL_WIDTHS.forEach((w, i) => {
    requests.push({
      updateDimensionProperties: {
        range: { sheetId: workflowsGid, dimension: "COLUMNS", startIndex: i, endIndex: i + 1 },
        properties: { pixelSize: w },
        fields: "pixelSize",
      },
    });
  });

  // 2e. Freeze first row
  requests.push({
    updateSheetProperties: {
      properties: {
        sheetId: workflowsGid,
        gridProperties: { frozenRowCount: 1 },
      },
      fields: "gridProperties.frozenRowCount",
    },
  });

  // 2f. AutoFilter on header row (columns A–Q = 0–16)
  requests.push({
    setBasicFilter: {
      filter: {
        range: {
          sheetId: workflowsGid,
          startRowIndex: 0,
          endRowIndex: dataEndRow,
          startColumnIndex: 0,
          endColumnIndex: 17,
        },
      },
    },
  });

  await sheets.spreadsheets.batchUpdate({
    spreadsheetId: SPREADSHEET_ID,
    requestBody: { requests },
  });

  console.log("✅  Formatting applied:");
  console.log("    → Header: Roboto 12px, blue BG #305696, white text, CLIP");
  console.log("    → Data rows: Roboto 10px, white BG, #434343 text, CLIP");
  console.log("    → All rows: 20px height");
  console.log("    → Borders: SOLID 1px on all cells");
  console.log("    → Column widths: exact match with Accounts Settings");
  console.log("    → Row 1 frozen");
  console.log("    → AutoFilter added (filter dropdown arrows on header)");

  console.log("\n🎉  Done! Workflows sheet now matches other module sheets exactly.");
  } finally {
    rl.close();
  }
}

main().catch((err) => {
  console.error("❌  Error:", err.message || err);
  if (err.response?.data) console.error(JSON.stringify(err.response.data, null, 2));
  process.exit(1);
});
