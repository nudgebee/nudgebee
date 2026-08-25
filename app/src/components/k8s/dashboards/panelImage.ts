import { getFontEmbedCSS, toPng } from 'html-to-image';
import { ds } from '@utils/colors';
import { filenameSlug } from '@utils/fileDownload';

/**
 * Rasterising a dashboard, or one panel, to PNG.
 *
 * Same library and the same font-embedding dance as the service-map download
 * (components/k8s/details/ServiceMapDownloadImage.js) — charts are canvases,
 * which html-to-image copies pixel-for-pixel, but the surrounding text is DOM
 * and renders with system fallbacks unless the fonts are inlined.
 */

/**
 * Fonts loaded in the document are static for the session, so fetch and embed
 * them once. The PROMISE is cached, not just its value, so two exports fired
 * back to back don't both pay for ~100 font requests.
 */
let fontCssPromise: Promise<string | null> | null = null;

/**
 * Whether every stylesheet in the document can be read.
 *
 * A CROSS-ORIGIN sheet — this app @imports a font CDN — raises SecurityError on
 * `cssRules`. html-to-image walks every sheet to collect @font-face rules and
 * CATCHES that itself, but reports it with `console.error`
 * (embed-webfonts.js), which Next's dev overlay promotes to a full-screen
 * "Runtime SecurityError". Wrapping our own call cannot suppress a log emitted
 * inside the library, so the only way to stay quiet is not to make it walk.
 *
 * Probing here is the same read, in our own try/catch, with nothing logged.
 */
function stylesheetsAreReadable(): boolean {
  return Array.from(document.styleSheets).every((sheet) => {
    try {
      // Touching the property is what throws; the value is not needed.
      void sheet.cssRules;
      return true;
    } catch {
      return false;
    }
  });
}

function ensureFontCss(node: HTMLElement): Promise<string | null> {
  if (!fontCssPromise) {
    /*
     * A null result means "render with system fonts" — `skipFonts` at the call
     * site, so the library never touches a stylesheet. Charts are canvases and
     * come out identical either way; only DOM text (titles, legends, table
     * cells) falls back.
     *
     * Cached rather than cleared on failure: a sheet is cross-origin for the
     * whole session, so retrying per click only repeats the work.
     */
    fontCssPromise = (async () => {
      if (!stylesheetsAreReadable()) return null;
      try {
        return await getFontEmbedCSS(node);
      } catch {
        return null;
      }
    })();
  }
  return fontCssPromise;
}

/** Marks chrome that is an affordance rather than content — dropped from the image. */
export const EXPORT_HIDE_ATTR = 'data-export-hide';

/**
 * Marks a panel that has neither data nor an error yet, so a whole-dashboard
 * export can wait for it. Panels fetch on scroll, so most of a long dashboard
 * is deliberately unloaded until the export forces it.
 */
export const PANEL_PENDING_ATTR = 'data-panel-pending';

/**
 * Anything inside a panel that scrolls rather than growing.
 *
 * A chart's HTML legend is capped at 150px with `overflow: auto`
 * (charts/LineCharts.jsx) — and that legend is where the NUMBERS are: it
 * prints Max / Min / P99 / Avg per series, which is the closest a static image
 * gets to the hover tooltip. Left scrolled, a twelve-series panel exports with
 * nine of them cut off.
 */
const SCROLLING_SELECTOR = '.chart-legend-container, .chart-legend-container ul';

/**
 * Expands the scrollers for the duration of a capture and returns the undo.
 *
 * Inline styles are saved and restored rather than toggling a class, so a panel
 * whose legend was already expanded by something else is put back the way it
 * was found.
 */
function expandScrollers(root: HTMLElement): () => void {
  const nodes = [root, ...Array.from(root.querySelectorAll<HTMLElement>(SCROLLING_SELECTOR))];
  const saved = nodes.map((node) => ({
    node,
    maxHeight: node.style.maxHeight,
    height: node.style.height,
    overflow: node.style.overflow,
    overflowY: node.style.overflowY,
  }));
  for (const node of nodes) {
    node.style.maxHeight = 'none';
    node.style.height = 'auto';
    node.style.overflow = 'visible';
    node.style.overflowY = 'visible';
  }
  return () => {
    for (const entry of saved) {
      entry.node.style.maxHeight = entry.maxHeight;
      entry.node.style.height = entry.height;
      entry.node.style.overflow = entry.overflow;
      entry.node.style.overflowY = entry.overflowY;
    }
  };
}

export async function downloadNodeAsPng(node: HTMLElement, title: string, fallbackName: string): Promise<void> {
  const fontEmbedCSS = await ensureFontCss(node);
  const restore = expandScrollers(node);
  // One frame so the expanded height is laid out before it is measured.
  await new Promise((resolve) => requestAnimationFrame(() => resolve(null)));
  try {
    await rasterize(node, title, fallbackName, fontEmbedCSS);
  } finally {
    restore();
  }
}

async function rasterize(node: HTMLElement, title: string, fallbackName: string, fontEmbedCSS: string | null): Promise<void> {
  const dataUrl = await toPng(node, {
    backgroundColor: ds.background[100],
    // 2× so the text in the image survives being read on a retina screen or
    // pasted into a doc at full width.
    pixelRatio: 2,
    fontEmbedCSS: fontEmbedCSS || undefined,
    skipFonts: !fontEmbedCSS,
    // Buttons and menus mean nothing in a static image, and the open-menu
    // affordance reads as a rendering artefact.
    filter: (domNode: HTMLElement) => !(domNode instanceof HTMLElement) || domNode.getAttribute(EXPORT_HIDE_ATTR) === null,
  });

  const link = document.createElement('a');
  link.download = `${filenameSlug(title, fallbackName)}.png`;
  link.href = dataUrl;
  document.body.appendChild(link);
  link.click();
  document.body.removeChild(link);
}

/** How many panels inside `container` are still waiting on their query. */
export function pendingPanelCount(container: HTMLElement): number {
  return container.querySelectorAll(`[${PANEL_PENDING_ATTR}='true']`).length;
}

/**
 * Waits for every panel in `container` to carry data or an error.
 *
 * Returns false on timeout rather than throwing: a dashboard with one dead
 * account should still export the fifteen panels that DID load, with the caller
 * saying so, instead of producing nothing.
 */
export async function waitForPanels(container: HTMLElement, timeoutMs = 20000, pollMs = 150): Promise<boolean> {
  const deadline = performance.now() + timeoutMs;
  while (performance.now() < deadline) {
    if (pendingPanelCount(container) === 0) return true;
    await new Promise((resolve) => setTimeout(resolve, pollMs));
  }
  return pendingPanelCount(container) === 0;
}
