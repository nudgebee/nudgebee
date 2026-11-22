// Writes a WebP beside each failure screenshot so generate-e2e-report.js can embed it instead and the report stays Slack-previewable; Playwright's Chromium does the encoding.
// Usage: node scripts/compress-screenshots.js [resultsFile]
const fs = require("fs");
const path = require("path");
const { chromium } = require("@playwright/test");

const resultsFile = process.argv[2] || path.join("playwright-report", "e2e-results.json");
const MAX_WIDTH = Number(process.env.E2E_REPORT_SCREENSHOT_MAX_WIDTH || 1280);
const QUALITY = Number(process.env.E2E_REPORT_SCREENSHOT_QUALITY || 0.6);

function screenshotPaths(data) {
  const attempts = Array.isArray(data) ? data : data.attempts || [];
  const found = new Set();
  const take = (list) => {
    for (const a of list || []) if ((a.type || "").indexOf("image/png") === 0 && a.source) found.add(a.source);
  };
  const walk = (steps) => {
    for (const s of steps || []) {
      take(s.attachments);
      walk(s.steps);
    }
  };
  for (const at of attempts) {
    take(at.attachments);
    walk(at.steps);
  }
  return [...found].filter((p) => fs.existsSync(p));
}

async function main() {
  const files = screenshotPaths(JSON.parse(fs.readFileSync(resultsFile, "utf8")));
  if (!files.length) {
    console.log("No screenshots to compress.");
    return;
  }
  const browser = await chromium.launch();
  let before = 0;
  let after = 0;
  let compressed = 0;
  try {
    const page = await browser.newPage();
    for (const file of files) {
      // One unreadable or truncated screenshot must not stop the rest from being compressed; it is embedded as PNG instead.
      try {
        const png = fs.readFileSync(file);
        const webp = await page.evaluate(
          async ({ src, maxWidth, quality }) => {
            const img = new Image();
            img.src = src;
            await img.decode();
            const scale = Math.min(1, maxWidth / img.naturalWidth);
            const canvas = document.createElement("canvas");
            canvas.width = Math.round(img.naturalWidth * scale);
            canvas.height = Math.round(img.naturalHeight * scale);
            canvas.getContext("2d").drawImage(img, 0, 0, canvas.width, canvas.height);
            return canvas.toDataURL("image/webp", quality);
          },
          { src: "data:image/png;base64," + png.toString("base64"), maxWidth: MAX_WIDTH, quality: QUALITY }
        );
        const out = Buffer.from(webp.slice(webp.indexOf(",") + 1), "base64");
        // A browser without a WebP encoder hands back PNG; keeping only a smaller WebP leaves the original in use otherwise.
        if (!webp.startsWith("data:image/webp") || out.length >= png.length) continue;
        fs.writeFileSync(file + ".webp", out);
        before += png.length;
        after += out.length;
        compressed++;
      } catch (e) {
        console.log("::warning::Could not compress " + file + ": " + (e && e.message ? e.message : e));
      }
    }
  } finally {
    await browser.close();
  }
  console.log(
    `Compressed ${compressed} of ${files.length} screenshot(s): ${(before / 1048576).toFixed(1)}MB -> ${(after / 1048576).toFixed(1)}MB`
  );
}

main().catch((e) => {
  // The report still builds from the original PNGs, so a failure here only costs size.
  console.log("::warning::Screenshot compression skipped: " + (e && e.message ? e.message : e));
});
