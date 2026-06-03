import React, { useEffect, useRef } from 'react';

interface SvgRendererProps {
  svg: string;
  style?: React.CSSProperties;
}

const SvgRenderer: React.FC<SvgRendererProps> = ({ svg, style }) => {
  const iframeRef = useRef<HTMLIFrameElement>(null);
  const blobUrlRef = useRef<string | null>(null);

  useEffect(() => {
    if (!svg) {
      return;
    }

    // Revoke previous blob URL to free memory
    if (blobUrlRef.current) {
      URL.revokeObjectURL(blobUrlRef.current);
    }

    // Wrap bare SVG in a minimal HTML page so the iframe sizes correctly
    // and the SVG fills the available space.
    const html = `<!DOCTYPE html>
<html>
  <head>
    <meta charset="utf-8" />
    <meta http-equiv="Content-Security-Policy"
          content="default-src 'none'; script-src 'unsafe-inline'; style-src 'unsafe-inline'; img-src data:;" />
    <style>
      * { margin: 0; padding: 0; box-sizing: border-box; }
      html, body { width: 100%; height: 100%; }
      body { overflow: auto; }
      svg { max-width: 100%; display: block; }
    </style>
  </head>
  <body>${svg}</body>
</html>`;

    const blob = new Blob([html], { type: 'text/html' });
    const url = URL.createObjectURL(blob);
    blobUrlRef.current = url;

    if (iframeRef.current) {
      iframeRef.current.src = url;
    }

    return () => {
      if (blobUrlRef.current) {
        URL.revokeObjectURL(blobUrlRef.current);
        blobUrlRef.current = null;
      }
    };
  }, [svg]);

  const containerStyle: React.CSSProperties = {
    width: '600px',
    height: '2000px',
    border: '1px solid var(--ds-brand-200)',
    ...style,
  };

  return (
    <iframe
      ref={iframeRef}
      // allow-scripts: lets flame graph hover JS execute
      // NO allow-same-origin: keeps the iframe origin isolated from the parent app
      // This combination means scripts run but cannot access parent DOM, cookies, or localStorage
      sandbox='allow-scripts'
      style={{ ...containerStyle, display: 'block' }}
      title='SVG Viewer'
    />
  );
};

export default SvgRenderer;
