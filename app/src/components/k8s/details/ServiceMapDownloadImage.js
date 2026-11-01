import React, { useEffect } from 'react';
import { Panel, useReactFlow, getNodesBounds } from 'reactflow';
import { toPng, getFontEmbedCSS } from 'html-to-image';
import FileDownloadOutlinedIcon from '@mui/icons-material/FileDownloadOutlined';
import { Button } from '@ui/Button';

function downloadImage(dataUrl, fileName) {
  const a = document.createElement('a');
  a.setAttribute('download', fileName);
  a.setAttribute('href', dataUrl);
  a.click();
}

// Fonts loaded in the document are static for the session, so fetch and
// embed them once and cache the in-flight promise. Caching the promise
// (rather than just the resolved value) also prevents concurrent duplicate
// fetches if Download is clicked while the background prefetch is still
// running.
let cachedFontEmbedCSSPromise = null;

function ensureFontEmbedCSSCached(viewport) {
  if (!viewport) {
    return Promise.resolve(null);
  }
  if (!cachedFontEmbedCSSPromise) {
    cachedFontEmbedCSSPromise = getFontEmbedCSS(viewport).catch((error) => {
      console.error('Error embedding fonts:', error);
      cachedFontEmbedCSSPromise = null;
      return null;
    });
  }
  return cachedFontEmbedCSSPromise;
}

// Safari caps a canvas at ~16.7M pixels and every browser caps a side at 16384px;
// past either, toPng returns a blank image. Large graphs trade resolution to fit.
const MAX_CANVAS_AREA = 16_000_000;
const MAX_CANVAS_SIDE = 16_000;
const IMAGE_PADDING = 100;

export function imagePixelRatio(width, height) {
  return Math.min(2, Math.sqrt(MAX_CANVAS_AREA / (width * height)), MAX_CANVAS_SIDE / width, MAX_CANVAS_SIDE / height);
}

/**
 * Render a ReactFlow graph to a PNG and download it. `viewportEl` is the graph's
 * `.react-flow__viewport`; every node in `nodes` must be rendered into it.
 */
export async function downloadFlowImage(viewportEl, nodes, fileName) {
  const nodesBounds = getNodesBounds(nodes);
  const imageWidth = nodesBounds.width + IMAGE_PADDING * 2;
  const imageHeight = nodesBounds.height + IMAGE_PADDING * 2;
  // html-to-image paints the background onto a canvas and an SVG foreignObject,
  // neither of which can resolve var(--ds-*); pass the token's computed value.
  const backgroundColor = getComputedStyle(viewportEl).getPropertyValue('--ds-background-100').trim() || undefined;
  const fontEmbedCSS = await ensureFontEmbedCSSCached(viewportEl);

  const dataUrl = await toPng(viewportEl, {
    backgroundColor,
    width: imageWidth,
    height: imageHeight,
    style: {
      width: `${imageWidth}px`,
      height: `${imageHeight}px`,
      // 1:1 scale, shifted so the bounds' top-left lands at the padding offset.
      // (reactflow's getViewportForBounds takes padding as a fraction, not px.)
      transform: `translate(${IMAGE_PADDING - nodesBounds.x}px, ${IMAGE_PADDING - nodesBounds.y}px) scale(1)`,
    },
    pixelRatio: imagePixelRatio(imageWidth, imageHeight),
    // Reuse the cached font CSS when it still covers the fonts in use,
    // instead of re-fetching every font file (~100 requests) each click.
    fontEmbedCSS: fontEmbedCSS || undefined,
    // When font embedding failed (a cross-origin stylesheet throws
    // "Cannot access rules" on cssRules access), skip fonts entirely so
    // toPng doesn't re-run the same failing embed and reject the download.
    // The image still renders with system-font fallback.
    skipFonts: !fontEmbedCSS,
  });
  downloadImage(dataUrl, fileName);
}

function ServiceMapDownloadImage() {
  const { getNodes } = useReactFlow();

  // Warm the font cache in the background so the first Download click is
  // fast too, without blocking the graph from rendering.
  useEffect(() => {
    const idleCallback = window.requestIdleCallback || ((cb) => setTimeout(cb, 1));
    const cancelIdleCallback = window.cancelIdleCallback || clearTimeout;

    const handle = idleCallback(() => {
      const viewport = document.querySelector('.react-flow__viewport');
      ensureFontEmbedCSSCached(viewport);
    });

    return () => cancelIdleCallback(handle);
  }, []);

  const onClick = () => {
    const viewport = document.querySelector('.react-flow__viewport');
    downloadFlowImage(viewport, getNodes(), 'servicemap.png').catch((error) => {
      console.error('Error generating image:', error);
    });
  };

  return (
    <Panel position='top-right'>
      <Button id='download-service-map-image-btn' tone='secondary' size='sm' icon={<FileDownloadOutlinedIcon />} onClick={onClick}>
        Download Image
      </Button>
    </Panel>
  );
}

export default ServiceMapDownloadImage;
