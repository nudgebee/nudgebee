import { Box } from '@mui/material';
import { useRef, useEffect, useState, useMemo } from 'react';
import PropTypes from 'prop-types';
import dynamic from 'next/dynamic';
import { marked } from 'marked';
import DOMPurify from 'dompurify';
import { withErrorBoundary, reportHandledError } from '@shared/ErrorBoundary';
import { ds } from 'src/utils/colors';
import DownloadIcon from '@assets/download-f.svg';
import { DropdownMenu } from '@ui/DropdownMenu';
import { toast as snackbar } from '@ui/Toast';
import { createRoot } from 'react-dom/client';

// Diagram rendering needs heavy, diagram-only libraries: `mermaid`, `chart.js` /
// `react-chartjs-2` (via MermaidChartJS) and `html-to-image`. MarkDowns is reached
// on nearly every route (e.g. the header's cluster dropdown pulls it in through
// @shared/format/Text), but markdown that actually contains a mermaid/xychart block
// is rare. Importing these statically shipped all of them in the shared layout chunk
// on every page. They are now loaded on demand — only when a diagram is actually
// present (a `.mermaid` div, an xychart segment, or a PNG export click). See
// enterprise#25990.
const MermaidChartJS = dynamic(() => import('@shared/viewers/MermaidChartJS').then((m) => m.MermaidChartJS), { ssr: false });

// Lazily import + initialize mermaid the first time a diagram needs rendering.
// Memoized so the import happens once and initialize() runs exactly once.
let mermaidPromise = null;
const loadMermaid = () => {
  if (!mermaidPromise) {
    mermaidPromise = import('mermaid').then(({ default: mermaid }) => {
      mermaid.initialize({
        startOnLoad: false,
        theme: 'default',
        securityLevel: 'antiscript',
        flowchart: {
          htmlLabels: true,
          curve: 'basis',
        },
        themeVariables: {
          fontFamily: 'Roboto, Arial, sans-serif',
          fontSize: 'var(--ds-text-small)',
          nodePadding: 16,
        },
      });
      return mermaid;
    });
  }
  return mermaidPromise;
};

// Build a marked renderer whose mermaid code blocks register their source into
// the supplied `chartCodes` map. This map (and the chart-id counter) MUST be
// per MarkDowns instance — not module-level globals.
//
// Previously both lived at module scope and the component's unmount cleanup
// did `chartCodes.clear()` globally. With several MarkDowns mounted at once
// (the conversation view renders many: question, acknowledgment, each task
// card, the final response, plus transient hover-tooltip instances), any
// sibling unmount wiped the shared registry. A still-mounted chart's
// `chartCodes.get(chartId)` then returned undefined, so the chart-render
// effect logged "Chart data not found" and rendered nothing — the visualizer's
// xychart response showed up blank (issue #31218). Scoping the registry per
// instance removes that cross-instance interference.
const createMarkdownRenderer = (chartCodes) => {
  const renderer = new marked.Renderer();
  let chartCounter = 0;

  // A markdown table needs a scroll container that is a *separate* element from
  // the table itself. Making the <table> its own scroller (display:block;
  // overflow-x:auto) leaves the anonymous inner table box constrained to the
  // available width, which pins every column to its min-content. Wrapping instead
  // lets the table take `width: max-content` and the wrapper do the scrolling.
  const baseTable = renderer.table.bind(renderer);
  renderer.table = (token) => `<div class="md-table-wrap">${baseTable(token)}</div>`;

  renderer.image = ({ href, title, text }) => {
    return `<img src="${href}" alt="${text}" title="${
      title || ''
    }" class="markdown-image" loading="lazy" referrerpolicy="no-referrer" style="max-width: 100%; height: auto;" />`;
  };

  renderer.code = ({ text, lang }) => {
    if (lang === 'mermaid') {
      const chartId = `chart-${chartCounter++}`;
      chartCodes.set(chartId, text);
      // Check if it's an xychart
      const trimmedText = text.trim();
      if (trimmedText.startsWith('xychart-beta') || trimmedText.startsWith('xychart')) {
        return `<div class="mermaid-chartjs" data-chart-id="${chartId}"></div>`;
      }
      return `<div class="mermaid" data-chart-id="${chartId}"></div>`;
    }

    return `<pre><code class="language-${lang || ''}">${text}</code></pre>`;
  };

  return renderer;
};

const MARKED_OPTIONS = {
  breaks: true,
  gfm: true,
  smartLists: true,
  smartypants: true,
};

const defaultStyles = {
  fontFamily: '"Roboto", "Helvetica", "Arial", sans-serif',
  fontSize: 'var(--ds-text-body)',
  color: 'var(--ds-gray-700)',
  lineHeight: 1.5,
  '& *': {
    boxSizing: 'border-box',
  },
  '& h1, & h2, & h3, & h4, & h5, & h6': {
    margin: 0,
    fontFamily: '"Roboto", "Helvetica", "Arial", sans-serif',
    fontWeight: 'var(--ds-font-weight-medium)',
    lineHeight: 1.2,
    //scrollMarginTop: '8px',
  },
  '& h1': {
    fontSize: 'var(--ds-text-title)',
    color: ds.gray[700],
    fontWeight: 'var(--ds-font-weight-medium)',
    letterSpacing: '-0.025em',
    marginBottom: 'var(--ds-space-3)',
    paddingBottom: 'var(--ds-space-1)',
    borderBottom: '1px solid var(--ds-brand-150)',
  },
  '& h2': {
    fontSize: 'var(--ds-text-body-lg)',
    color: ds.gray[700],
    fontWeight: 'var(--ds-font-weight-medium)',
    marginTop: 'var(--ds-space-5)',
    marginBottom: 'var(--ds-space-3)',
    paddingBottom: 'var(--ds-space-2)',
    borderBottom: `1px solid color-mix(in srgb, ${ds.gray[300]} 73%, transparent)`,
  },
  '& h3': {
    fontSize: 'var(--ds-text-body-lg)',
    color: 'var(--ds-gray-700)',
    fontWeight: 'var(--ds-font-weight-medium)',
    marginTop: 'var(--ds-space-4)',
    marginBottom: 'var(--ds-space-2)',
    '& strong': {
      fontWeight: 'var(--ds-font-weight-medium)',
      fontSize: 'var(--ds-text-body-lg)',
    },
  },
  '& p': {
    fontSize: 'var(--ds-text-body)',
    marginTop: 'var(--ds-space-1)',
    fontWeight: 'var(--ds-font-weight-regular)',
    marginBottom: 'var(--ds-space-4)',
    color: ds.gray[700],
    lineHeight: 1.6,
    '& code': {
      backgroundColor: 'var(--ds-background-200)',
      padding: 'var(--ds-space-1) var(--ds-space-1)',
      borderRadius: 'var(--ds-radius-sm)',
      margin: '0 var(--ds-space-1)',
      fontSize: 'var(--ds-text-caption)',
      color: ds.gray[700],
      fontFamily: '"Roboto Mono", monospace',
      border: `1px solid ${ds.gray[200]}`,
    },
    '& strong, & b': {
      fontWeight: 'var(--ds-font-weight-medium)',
      color: ds.gray[700],
      marginBottom: 'var(--ds-space-7) !important',
    },
  },
  '& a': {
    color: 'var(--ds-blue-500)',
    textDecoration: 'none',
    fontSize: 'var(--ds-text-body)',
    transition: 'all 0.2s ease',
    '&:hover': {
      borderBottom: '1px solid  var(--ds-blue-400)',
      backgroundColor: 'var(--ds-background-200)',
    },
  },
  '& ul': {
    paddingLeft: 'var(--ds-space-4)',
  },
  '& ol': {
    paddingLeft: 'var(--ds-space-6)',
  },
  '& ul, & ol': {
    marginBottom: 'var(--ds-space-5)',
    '& li': {
      marginBottom: 'var(--ds-space-1)',
      color: ds.gray[700],
      position: 'relative',
      paddingLeft: '0px',
      lineHeight: 1.6,
      fontWeight: 'var(--ds-font-weight-regular)',
      '&::marker': {
        color: ds.gray[700],
      },
      '& p': {
        marginTop: 0,
        marginBottom: 'var(--ds-space-1)',
      },
      '& strong, & b': {
        fontWeight: 'var(--ds-font-weight-regular)',
        color: ds.gray[700],
      },
      '& code': {
        backgroundColor: 'var(--ds-background-200)',
        padding: 'var(--ds-space-1) var(--ds-space-1)',
        borderRadius: 'var(--ds-radius-sm)',
        margin: '0 var(--ds-space-1)',
        fontSize: 'var(--ds-text-caption)',
        color: ds.gray[700],
        fontFamily: '"Roboto Mono", monospace',
        border: `1px solid ${ds.gray[200]}`,
      },
    },
  },
  '& blockquote': {
    borderLeft: `${ds.space[1]} solid ${ds.gray[300]}`,
    backgroundColor: 'var(--ds-background-200)',
    padding: 'var(--ds-space-3) var(--ds-space-4)',
    margin: 'var(--ds-space-4) 0',
    color: ds.gray[700],
    fontStyle: 'italic',
    borderRadius: '0 var(--ds-radius-md) var(--ds-radius-md) 0',
    '& p': {
      marginBottom: '0px !important',
      '&:last-child': {
        marginBottom: 0,
      },
    },
  },
  '& pre': {
    backgroundColor: 'var(--ds-brand-500)',
    color: 'var(--ds-brand-150) !important',
    padding: `var(--ds-space-3) ${ds.space.mul(1, 16)} var(--ds-space-3) var(--ds-space-4) !important`,
    borderRadius: 'var(--ds-radius-lg)',
    marginBottom: 'var(--ds-space-4)',
    whiteSpace: 'pre-wrap',
    wordWrap: 'break-word',
    position: 'relative',
    maxWidth: '100%',
    overflowX: 'auto',
    '& code': {
      color: 'inherit !important',
      fontSize: 'var(--ds-text-body)',
      fontFamily: '"Roboto Mono", monospace',
      lineHeight: 1.6,
      backgroundColor: 'transparent !important',
      padding: 0,
      whiteSpace: 'pre-wrap',
      overflowWrap: 'break-word',
      border: 'none !important',
    },
    '& .copy-button': {
      position: 'absolute',
      top: ds.space[2],
      right: ds.space[2],
      opacity: 1,
      backgroundColor: 'transparent',
      border: 'none',
      outline: 'none',
      boxShadow: 'none',
      '& button': {
        backgroundColor: `color-mix(in srgb, ${ds.background[100]} 10%, transparent)`,
        border: 'none',
        outline: 'none',
        boxShadow: 'none',
      },
    },
  },
  '& hr': {
    border: 'none',
    height: '1px',
    backgroundColor: 'var(--ds-brand-150)',
    margin: 'var(--ds-space-5) 0',
    position: 'relative',
    '&::before': {
      content: '""',
      position: 'absolute',
      width: ds.space.mul(0, 20),
      height: ds.space[0],
      backgroundColor: 'var(--ds-blue-500)',
      top: '-1px',
      left: '50%',
      transform: 'translateX(-50%)',
      borderRadius: 'var(--ds-radius-sm)',
    },
  },
  '& h1 + hr': {
    display: 'none',
  },
  '& .md-table-wrap': {
    maxWidth: '100%',
    overflowX: 'auto',
    marginBottom: 'var(--ds-space-4)',
  },
  '& table': {
    // `max-content` lets each column take its natural width and the wrapper scroll.
    // `min-width: 100%` keeps a narrow table stretched across the message instead
    // of shrink-wrapping to its content.
    width: 'max-content',
    minWidth: '100%',
    borderCollapse: 'separate',
    borderSpacing: 0,
    '& th': {
      backgroundColor: ds.blue[100],
      color: ds.gray[700],
      padding: 'var(--ds-space-2) var(--ds-space-4)',
      fontWeight: 'var(--ds-font-weight-medium)',
      textAlign: 'left',
      fontSize: 'var(--ds-text-body)',
      whiteSpace: 'nowrap',
    },
    '& td': {
      padding: 'var(--ds-space-2) var(--ds-space-4)',
      borderBottom: `1px solid ${ds.gray[200]}`,
      color: ds.gray[700],
      fontSize: 'var(--ds-text-body)',
      transition: 'background-color 0.2s ease',
      // Caps a prose-heavy cell so one long sentence can't stretch the table off
      // screen; `break-word` (never `anywhere`, which collapses min-content to a
      // single character) still breaks unbreakable tokens like ARNs.
      maxWidth: ds.space.mul(0, 160),
      overflowWrap: 'break-word',
    },
    '& tr:hover td': {
      backgroundColor: 'var(--ds-background-200)',
    },
  },
  '& img': {
    maxWidth: `${ds.space.mul(0, 300)} !important`,
    height: 'auto !important',
    borderRadius: 'var(--ds-radius-lg)',
    display: 'block',
    margin: 'var(--ds-space-4) auto',
    boxShadow: `0 ${ds.space[1]} ${ds.space.mul(0, 3)} -1px color-mix(in srgb, ${ds.gray[500]} 10%, transparent), 0 ${ds.space[0]} ${
      ds.space[1]
    } -1px color-mix(in srgb, ${ds.gray[300]} 6%, transparent)`,
  },
  '& kbd': {
    backgroundColor: 'var(--ds-background-200)',
    border: '1px solid var(--ds-brand-150)',
    borderBottom: '3px solid var(--ds-brand-200)',
    borderRadius: 'var(--ds-radius-sm)',
    padding: 'var(--ds-space-1) var(--ds-space-1)',
    fontSize: 'var(--ds-text-caption)',
    fontFamily: '"Roboto Mono", monospace',
  },
  '& details': {
    marginBottom: 'var(--ds-space-3)',
    '& summary': {
      cursor: 'pointer',
      color: 'var(--ds-blue-500)',
      fontWeight: 'var(--ds-font-weight-medium)',
      padding: 'var(--ds-space-1) 0',
      '&:hover': {
        color: 'var(--ds-gray-700)',
      },
    },
  },
  '& .mermaid': {
    backgroundColor: 'var(--ds-background-100)',
    padding: 'var(--ds-space-4)',
    borderRadius: 'var(--ds-radius-lg)',
    overflowX: 'auto',
  },
};

function MarkDowns({ data, sx, allowExecutable, canRunCode = true, onLinkClick }) {
  const containerRef = useRef(null);
  const [copiedStates, setCopiedStates] = useState({});
  // Tracks React roots mounted imperatively for the mermaid (flowchart) SVG
  // download-button overlay, so they can be unmounted when this component does.
  // (xychart charts are now plain React children — they need no manual root.)
  const chartRootsRef = useRef([]);

  // Per-instance chart-source registry + rendered HTML, recomputed only when
  // `data` changes. Keeping `chartCodes` local (vs the old module global) is
  // what fixes the blank-chart bug — see createMarkdownRenderer above.
  const { sanitizedData, chartCodes, segments } = useMemo(() => {
    const codes = new Map();
    const cleanedData = (data || '')
      .split('\n')
      .map((line) => line.trim()) // Removes leading spaces/tabs from every line
      .join('\n');
    const convertedData = marked(cleanedData, { ...MARKED_OPTIONS, renderer: createMarkdownRenderer(codes) });
    const sanitized = DOMPurify.sanitize(convertedData, {
      ADD_TAGS: ['div', 'svg', 'path', 'g', 'defs', 'marker', 'img'],
      ADD_ATTR: [
        'class',
        'id',
        'viewBox',
        'd',
        'fill',
        'stroke',
        'marker-end',
        'data-chart-id',
        'src',
        'alt',
        'title',
        'width',
        'height',
        'referrerpolicy',
        'style',
        'loading',
      ],
    });
    // Split the sanitized HTML around the xychart placeholders so the charts
    // render as REAL React children (see the render below), not via a manual
    // createRoot() into an innerHTML-produced div. The manual-root approach
    // fought React's own DOM ownership: with StrictMode's double-invoke and the
    // dangerouslySetInnerHTML replacing the placeholder nodes on every re-render,
    // it produced "createRoot on a container already passed to createRoot",
    // "removeChild ... is not a child", and (in production) charts that
    // unmounted on a poll re-render and never came back — i.e. blank xychart
    // responses (issue #31218). Flowchart (non-xychart) `.mermaid` divs stay in
    // the HTML and are still rendered to SVG by the mermaid effect below.
    const segs = [];
    const placeholderRe = /<div class="mermaid-chartjs" data-chart-id="([^"]+)"><\/div>/g;
    let lastIndex = 0;
    let match;
    while ((match = placeholderRe.exec(sanitized)) !== null) {
      if (match.index > lastIndex) {
        segs.push({ type: 'html', html: sanitized.slice(lastIndex, match.index) });
      }
      segs.push({ type: 'chart', id: match[1] });
      lastIndex = placeholderRe.lastIndex;
    }
    if (lastIndex < sanitized.length) {
      segs.push({ type: 'html', html: sanitized.slice(lastIndex) });
    }

    return { sanitizedData: sanitized, chartCodes: codes, segments: segs };
  }, [data]);

  const downloadMermaidAsSVG = (svgElement, fileName = 'diagram.svg') => {
    let url;
    const link = document.createElement('a');
    try {
      const svgClone = svgElement.cloneNode(true);
      svgClone.setAttribute('xmlns', 'http://www.w3.org/2000/svg');
      svgClone.setAttribute('xmlns:xlink', 'http://www.w3.org/1999/xlink');

      const serializer = new XMLSerializer();
      const svgString = serializer.serializeToString(svgClone);

      const blob = new Blob([svgString], { type: 'image/svg+xml;charset=utf-8' });
      url = URL.createObjectURL(blob);

      link.href = url;
      link.download = fileName;
      document.body.appendChild(link);
      link.click();
    } catch (err) {
      console.error('Error exporting SVG:', err);
      snackbar.error('Failed to export diagram in SVG. Please try again.');
    } finally {
      if (url) {
        URL.revokeObjectURL(url);
      }
      if (document.body.contains(link)) {
        document.body.removeChild(link);
      }
    }
  };

  const downloadMermaidAsPNG = async (svgElement, fileName = 'diagram.png', scale = 4) => {
    const wrapper = document.createElement('div');
    try {
      wrapper.style.background = ds.background[100];
      wrapper.style.padding = ds.space.mul(0, 10);
      wrapper.style.display = 'inline-block';

      const svgClone = svgElement.cloneNode(true);
      wrapper.appendChild(svgClone);
      document.body.appendChild(wrapper);

      const { toPng } = await import('html-to-image');
      const dataUrl = await toPng(wrapper, {
        backgroundColor: 'var(--ds-background-100)',
        pixelRatio: scale,
        cacheBust: true,
        style: {
          transform: 'scale(1)',
        },
      });
      const link = document.createElement('a');
      link.download = fileName;
      link.href = dataUrl;
      link.click();
    } catch (error) {
      console.error('Error exporting diagram as PNG:', error);
      snackbar.error('Failed to export diagram in PNG. Please try again.');
    } finally {
      if (document.body.contains(wrapper)) {
        document.body.removeChild(wrapper);
      }
    }
  };

  const Dropdown = ({ svg, index, onDownloadPNG, onDownloadSVG }) => {
    return (
      <DropdownMenu
        align='end'
        disablePortal={false}
        trigger={
          <button
            style={{
              all: 'unset',
              position: 'absolute',
              inset: 0,
              cursor: 'pointer',
            }}
          />
        }
        items={[
          { label: 'Download PNG', onSelect: () => onDownloadPNG(svg, `mermaid-diagram-${index + 1}.png`) },
          { label: 'Download SVG', onSelect: () => onDownloadSVG(svg, `mermaid-diagram-${index + 1}.svg`) },
        ]}
      />
    );
  };

  const createDownloadButton = (svg, index) => {
    const wrapper = document.createElement('div');
    wrapper.style.position = 'absolute';
    wrapper.style.top = ds.space[2];
    wrapper.style.right = ds.space[2];
    wrapper.style.zIndex = '10';

    const btn = document.createElement('button');
    btn.type = 'button';
    btn.className = 'download-mermaid-btn';

    Object.assign(btn.style, {
      height: ds.space.mul(0, 12),
      width: ds.space.mul(0, 12),
      display: 'flex',
      justifyContent: 'center',
      alignItems: 'center',
      cursor: 'pointer',
      borderRadius: 'var(--ds-radius-sm)',
      border: '0.5px solid var(--ds-brand-200)',
      background: 'var(--ds-background-100)',
      padding: '0',
    });

    const img = document.createElement('img');
    img.src = DownloadIcon.src;
    img.alt = 'download';
    img.style.width = ds.space[4];
    img.style.height = ds.space[4];
    img.style.pointerEvents = 'none';

    btn.appendChild(img);
    wrapper.appendChild(btn);

    const reactRoot = document.createElement('div');
    wrapper.appendChild(reactRoot);

    const root = createRoot(reactRoot);
    chartRootsRef.current.push(root);
    root.render(<Dropdown svg={svg} index={index} onDownloadPNG={downloadMermaidAsPNG} onDownloadSVG={downloadMermaidAsSVG} />);

    return wrapper;
  };

  useEffect(() => {
    if (!containerRef.current) {
      return;
    }

    const mermaidDivs = containerRef.current.querySelectorAll('.mermaid');
    if (mermaidDivs.length === 0) {
      // No flowchart diagrams in this markdown — never load the mermaid bundle.
      return;
    }

    let cancelled = false;

    // Mermaid v11 requires labels with spaces/special chars to be quoted.
    // AI-generated mermaid code often has unquoted labels like A[Step 1].
    // This sanitizer quotes them: A[Step 1] → A["Step 1"]
    const sanitizeMermaidSyntax = (code) => {
      return code
        .replace(/(\b\w+)\[(?!\[)(?!")([^\]]+)\]/g, '$1["$2"]')
        .replace(/(\b\w+)\((?!\()(?!")([^)]+)\)/g, '$1("$2")')
        .replace(/(\b\w+)\{(?!")([^}]+)\}/g, '$1{"$2"}');
    };

    const cleanupMermaidArtifacts = (renderId) => {
      // mermaid.render() may leave temporary elements in the DOM on failure
      const selectors = [`#d${renderId}`, `#${renderId}`, `[id="${renderId}"]`];
      selectors.forEach((sel) => {
        try {
          document.querySelectorAll(sel).forEach((el) => el.remove());
        } catch {
          // ignore invalid selector errors
        }
      });
    };

    const renderMermaidDiagrams = async () => {
      const mermaid = await loadMermaid();
      // Bail if the component unmounted / data changed while the bundle loaded.
      if (cancelled) {
        return;
      }

      const tryRenderMermaid = async (renderId, code) => {
        const { svg } = await mermaid.render(renderId, code);
        return svg;
      };

      mermaidDivs.forEach(async (div, index) => {
        if (div.hasAttribute('data-mermaid-processed')) {
          return;
        }
        div.setAttribute('data-mermaid-processed', 'true');

        const chartId = div.getAttribute('data-chart-id');
        const originalCode = chartCodes.get(chartId) || '';
        const ts = Date.now();

        // Try original code first, then retry with sanitized (quoted labels), then fallback
        let svg = null;
        const renderId1 = `mermaid-svg-${ts}-${index}`;
        try {
          svg = await tryRenderMermaid(renderId1, originalCode);
        } catch (err) {
          if (cancelled) {
            return;
          }
          cleanupMermaidArtifacts(renderId1);
          const renderId2 = `mermaid-svg-${ts}-${index}-retry`;
          try {
            svg = await tryRenderMermaid(renderId2, sanitizeMermaidSyntax(originalCode));
          } catch (retryErr) {
            if (cancelled) {
              return;
            }
            cleanupMermaidArtifacts(renderId2);
            reportHandledError(retryErr instanceof Error ? retryErr : new Error(String(retryErr)), 'MarkDowns/Mermaid', {
              chartId,
              originalError: err instanceof Error ? err.message : String(err),
            });
          }
        }

        // Bail before mutating the DOM if the effect was cleaned up while mermaid
        // was rendering (component unmounted or `data` changed) — otherwise a stale
        // render could overwrite newer content or touch a detached node.
        if (cancelled) {
          return;
        }

        if (svg) {
          div.innerHTML = DOMPurify.sanitize(svg, {
            USE_PROFILES: { svg: true, svgFilters: true },
            ADD_TAGS: ['foreignObject'],
            ADD_ATTR: ['dominant-baseline', 'text-anchor', 'marker-end', 'marker-start'],
          });
          const svgEl = div.querySelector('svg');
          if (svgEl && !div.querySelector('.download-mermaid-btn')) {
            div.style.position = 'relative';
            const btn = createDownloadButton(svgEl, index);
            div.prepend(btn);
          }
        } else {
          // Both attempts failed — fall back to code block
          const pre = document.createElement('pre');
          const code = document.createElement('code');
          code.className = 'language-mermaid';
          code.textContent = originalCode;
          pre.appendChild(code);
          div.replaceWith(pre);
        }
      });
    };

    renderMermaidDiagrams();

    return () => {
      cancelled = true;
      // Unmount the download-button roots created during this render pass so they
      // don't leak when `data` / `chartCodes` change — this effect re-runs on every
      // such change, and also fires on final unmount. Deferred via setTimeout so the
      // unmount never runs inside React's commit phase.
      chartRootsRef.current.forEach((root) => {
        setTimeout(() => {
          try {
            root.unmount();
          } catch (e) {
            console.error(e);
          }
        }, 0);
      });
      chartRootsRef.current = [];
    };
  }, [sanitizedData, chartCodes]);

  const handleCopy = async (text, index) => {
    try {
      await navigator.clipboard.writeText(text);
      setCopiedStates((prev) => ({ ...prev, [index]: true }));
      setTimeout(() => {
        setCopiedStates((prev) => ({ ...prev, [index]: false }));
      }, 2000);
    } catch (err) {
      console.error('Failed to copy text: ', err);
    }
  };

  const handleRun = (text, _index) => {
    if (typeof allowExecutable === 'function') {
      allowExecutable(text);
    }
  };

  // Check if command is supported for execution (only kubectl and aws are supported for upgrade planner)
  const isSupportedCommand = (text) => {
    const trimmedText = text.trim().toLowerCase();
    return trimmedText.startsWith('kubectl') || trimmedText.startsWith('aws');
  };

  const hasVariablePlaceholders = (text) => {
    if (text.includes('|')) {
      return true;
    }

    const variablePatterns = [
      /<[a-zA-Z-_][a-zA-Z0-9-_]*>/g,
      /\$\{[a-zA-Z-_][a-zA-Z0-9-_]*\}/g,
      /\$[a-zA-Z-_][a-zA-Z0-9-_]*/g,
      /\{[a-zA-Z-_][a-zA-Z0-9-_]*\}/g,
    ];

    return variablePatterns.some((pattern) => pattern.test(text));
  };

  useEffect(() => {
    if (containerRef.current) {
      const preElements = containerRef.current.querySelectorAll('pre');

      preElements.forEach((pre, index) => {
        const existingCopyButton = pre.querySelector('.copy-button');
        const existingRunButton = pre.querySelector('.run-button');
        if (existingCopyButton) {
          existingCopyButton.remove();
        }
        if (existingRunButton) {
          existingRunButton.remove();
        }

        const codeElement = pre.querySelector('code');
        const codeText = codeElement ? codeElement.textContent : pre.textContent;

        if (typeof allowExecutable === 'function' && !hasVariablePlaceholders(codeText) && isSupportedCommand(codeText)) {
          const runButton = document.createElement('button');
          runButton.className = 'run-button';
          runButton.setAttribute('data-index', index);
          runButton.setAttribute('title', 'Run code');
          runButton.disabled = !canRunCode;

          Object.assign(runButton.style, {
            position: 'absolute',
            top: ds.space[1],
            right: ds.space[6],
            background: `color-mix(in srgb, ${ds.green[500]} 80%, transparent)`,
            border: 'none',
            color: 'var(--ds-background-100)',
            padding: 'var(--ds-space-1)',
            borderRadius: 'var(--ds-radius-sm)',
            cursor: canRunCode ? 'pointer' : 'not-allowed',
            opacity: canRunCode ? '1' : '0.4',
            width: ds.space.mul(0, 12),
            height: ds.space.mul(0, 12),
            display: 'flex',
            alignItems: 'center',
            justifyContent: 'center',
            transition: 'background-color 0.2s ease',
            outline: 'none',
            boxShadow: 'none',
          });

          runButton.innerHTML = '<svg width="12" height="12" viewBox="0 0 24 24" fill="#FFFFFF"><path d="M8 5v14l11-7z"/></svg>';

          runButton.addEventListener('mouseenter', () => {
            runButton.style.backgroundColor = ds.green[500];
          });

          runButton.addEventListener('mouseleave', () => {
            runButton.style.backgroundColor = `color-mix(in srgb, ${ds.green[500]} 80%, transparent)`;
          });

          runButton.addEventListener('click', () => {
            if (!canRunCode) {
              return;
            }
            handleRun(codeText, index);
          });

          pre.appendChild(runButton);
        }

        const copyButton = document.createElement('button');
        copyButton.className = 'copy-button';
        copyButton.setAttribute('data-index', index);
        copyButton.setAttribute('title', copiedStates[index] ? 'Copied!' : 'Copy code');

        Object.assign(copyButton.style, {
          position: 'absolute',
          top: ds.space[1],
          right: ds.space[1],
          background: `color-mix(in srgb, ${ds.background[100]} 10%, transparent)`,
          border: 'none',
          color: 'var(--ds-brand-150)',
          padding: 'var(--ds-space-1)',
          borderRadius: 'var(--ds-radius-sm)',
          cursor: 'pointer',
          width: ds.space.mul(0, 12),
          height: ds.space.mul(0, 12),
          display: 'flex',
          alignItems: 'center',
          justifyContent: 'center',
          transition: 'background-color 0.2s ease',
          outline: 'none',
          boxShadow: 'none',
        });

        copyButton.innerHTML = copiedStates[index]
          ? '<svg width="12" height="12" viewBox="0 0 24 24" fill="#FFFFFF"><path d="M9 16.17L4.83 12l-1.42 1.41L9 19 21 7l-1.41-1.41z"/></svg>'
          : '<svg width="12" height="12" viewBox="0 0 24 24" fill="#FFFFFF"><path d="M16 1H4c-1.1 0-2 .9-2 2v14h2V3h12V1zm3 4H8c-1.1 0-2 .9-2 2v14c0 1.1.9 2 2 2h11c1.1 0 2-.9 2-2V7c0-1.1-.9-2-2-2zm0 16H8V7h11v14z"/></svg>';

        copyButton.addEventListener('mouseenter', () => {
          copyButton.style.backgroundColor = `color-mix(in srgb, ${ds.background[100]} 20%, transparent)`;
        });

        copyButton.addEventListener('mouseleave', () => {
          copyButton.style.backgroundColor = `color-mix(in srgb, ${ds.background[100]} 10%, transparent)`;
        });

        copyButton.addEventListener('click', () => handleCopy(codeText, index));

        pre.appendChild(copyButton);
      });

      const anchorElements = containerRef.current.querySelectorAll('a');
      const clickHandlers = [];
      anchorElements.forEach((anchor) => {
        const href = anchor.getAttribute('href') || '';
        if (!href.startsWith('#')) {
          anchor.setAttribute('target', '_blank');
          anchor.setAttribute('rel', 'noopener noreferrer');
        }

        if (onLinkClick) {
          const handler = (e) => {
            const href = anchor.getAttribute('href') || '';
            const linkText = anchor.textContent || '';
            const handled = onLinkClick(href, linkText, e);
            if (handled) {
              e.preventDefault();
              e.stopPropagation();
            }
          };
          anchor.addEventListener('click', handler);
          clickHandlers.push({ anchor, handler });
        }
      });

      return () => {
        clickHandlers.forEach(({ anchor, handler }) => {
          anchor.removeEventListener('click', handler);
        });
      };
    }
  }, [sanitizedData, copiedStates, canRunCode, onLinkClick]);

  const combinedSx = {
    maxWidth: '100%',
    width: '100%',
    padding: 'var(--ds-space-4)',
    fontSize: 'var(--ds-text-small) !important',
    borderRadius: 'var(--ds-radius-lg)',
    maxHeight: ds.space.mul(0, 250),
    overflowY: 'auto',
    overflowX: 'hidden',
    overflowWrap: 'break-word',
    boxSizing: 'border-box',
    ...defaultStyles,
    ...sx,
  };

  return (
    <Box sx={combinedSx} ref={containerRef}>
      {segments.map((seg, i) =>
        seg.type === 'chart' ? (
          <MermaidChartJS key={`chart-${i}`} mermaidCode={chartCodes.get(seg.id) || ''} />
        ) : (
          <div key={`html-${i}`} dangerouslySetInnerHTML={{ __html: seg.html }} />
        )
      )}
    </Box>
  );
}

export default withErrorBoundary(MarkDowns);

MarkDowns.propTypes = {
  data: PropTypes.string,
  sx: PropTypes.object,
  allowExecutable: PropTypes.oneOfType([PropTypes.func, PropTypes.bool]),
  canRunCode: PropTypes.bool,
  onLinkClick: PropTypes.func,
};
