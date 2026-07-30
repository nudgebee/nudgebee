import React, { useEffect } from 'react';
import { Panel, useReactFlow, getNodesBounds, getViewportForBounds } from 'reactflow';
import { toPng, getFontEmbedCSS } from 'html-to-image';
import FileDownloadOutlinedIcon from '@mui/icons-material/FileDownloadOutlined';
import { Button } from '@ui/Button';
import { colors } from 'src/utils/colors';

function downloadImage(dataUrl) {
  const a = document.createElement('a');
  a.setAttribute('download', 'servicemap.png');
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

  const onClick = async () => {
    const nodesBounds = getNodesBounds(getNodes());

    // Add padding around the graph
    const padding = 100;

    // Calculate dimensions based on actual node bounds
    const imageWidth = nodesBounds.width + padding * 2;
    const imageHeight = nodesBounds.height + padding * 2;

    // Calculate viewport to center the graph with padding
    const graphViewport = getViewportForBounds(
      nodesBounds,
      imageWidth,
      imageHeight,
      0.5, // min zoom
      2, // max zoom
      padding // padding
    );

    const viewport = document.querySelector('.react-flow__viewport');
    const cachedFontEmbedCSS = await ensureFontEmbedCSSCached(viewport);

    toPng(viewport, {
      backgroundColor: colors.background.white,
      width: imageWidth,
      height: imageHeight,
      style: {
        width: `${imageWidth}px`,
        height: `${imageHeight}px`,
        transform: `translate(${graphViewport.x}px, ${graphViewport.y}px) scale(${graphViewport.zoom})`,
      },
      // Increase quality for better output
      pixelRatio: 2,
      // Reuse the cached font CSS when it still covers the fonts in use,
      // instead of re-fetching every font file (~100 requests) each click.
      fontEmbedCSS: cachedFontEmbedCSS || undefined,
      // When font embedding failed (a cross-origin stylesheet throws
      // "Cannot access rules" on cssRules access), skip fonts entirely so
      // toPng doesn't re-run the same failing embed and reject the download.
      // The image still renders with system-font fallback.
      skipFonts: !cachedFontEmbedCSS,
    })
      .then(downloadImage)
      .catch((error) => {
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
