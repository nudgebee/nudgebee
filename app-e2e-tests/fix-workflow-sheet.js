require("dotenv").config();
const path     = require("path");
const readline = require("readline");
const { google } = require("googleapis");

const SPREADSHEET_ID  = process.env.GOOGLE_SHEET_ID;
const CREDS_PATH      = path.resolve(__dirname, process.env.GOOGLE_SHEETS_CREDENTIALS_FILE || "credentials/sheets-credentials.json");
const WORKFLOWS_SHEET = "Workflows";
const TESTER_NAMES    = process.env.TC_TESTER_NAMES
  ? process.env.TC_TESTER_NAMES.split(",").map((n) => n.trim()).filter(Boolean)
  : ["Alice", "Bob"];

const HEADER_BG  = { red: 0.1882353, green: 0.32941177, blue: 0.5882353 };
const HEADER_FG  = { red: 1, green: 1, blue: 1 };
const DATA_FG    = { red: 0.2627451, green: 0.2627451, blue: 0.2627451 };
const BORDER     = { style: "SOLID", width: 1, color: {} };
const COL_WIDTHS = [82, 180, 234, 180, 180, 336, 322, 385, 100, 115, 148, 120, 130, 100, 100, 100, 100];

function repeatCell(sheetId, r1, r2, c1, c2, cell, fields) {
  return {
    repeatCell: {
      range:  { sheetId, startRowIndex: r1, endRowIndex: r2, startColumnIndex: c1, endColumnIndex: c2 },
      cell,
      fields,
    },
  };
}

function ask(rl, q) { return new Promise(r => rl.question(q, a => r(a.trim()))); }

async function askTesterName(rl, label) {
  const list = TESTER_NAMES.join(" / ");
  while (true) {
    const v = await ask(rl, `   ${label} (${list}): `);
    const n = v.charAt(0).toUpperCase() + v.slice(1).toLowerCase();
    if (TESTER_NAMES.includes(n)) return n;
    console.log(`   ⚠️  Enter one of: ${list}`);
  }
}

async function main() {
  const rl = readline.createInterface({ input: process.stdin, output: process.stdout });
  try {
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

    const meta = await sheets.spreadsheets.get({ spreadsheetId: SPREADSHEET_ID });
    const wfSheet = meta.data.sheets.find((s) => s.properties.title === WORKFLOWS_SHEET);
    if (!wfSheet) {
      console.error(`❌  Sheet "${WORKFLOWS_SHEET}" not found in the spreadsheet.`);
      process.exit(1);
    }
    const workflowsGid = wfSheet.properties.sheetId;
    console.log(`\n📋  Found "${WORKFLOWS_SHEET}" (GID: ${workflowsGid})`);

    const valRes = await sheets.spreadsheets.values.get({
      spreadsheetId: SPREADSHEET_ID,
      range: `${WORKFLOWS_SHEET}!C:C`,
    });
    const totalRows = (valRes.data.values || []).length;
    const dataRows  = totalRows - 1;
    console.log(`📋  Workflows sheet: ${totalRows} rows total (1 header + ${dataRows} data rows)`);

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

    console.log("\n🎨  Applying exact formatting (matching Accounts Settings)...");

    const requests = [];
    const dataEndRow = Math.max(totalRows, 6);

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

    requests.push({
      updateDimensionProperties: {
        range: { sheetId: workflowsGid, dimension: "ROWS", startIndex: 0, endIndex: dataEndRow },
        properties: { pixelSize: 20 },
        fields: "pixelSize",
      },
    });

    COL_WIDTHS.forEach((w, i) => {
      requests.push({
        updateDimensionProperties: {
          range: { sheetId: workflowsGid, dimension: "COLUMNS", startIndex: i, endIndex: i + 1 },
          properties: { pixelSize: w },
          fields: "pixelSize",
        },
      });
    });

    requests.push({
      updateSheetProperties: {
        properties: {
          sheetId: workflowsGid,
          gridProperties: { frozenRowCount: 1 },
        },
        fields: "gridProperties.frozenRowCount",
      },
    });

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
