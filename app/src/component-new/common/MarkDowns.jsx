/**
 * MarkDowns — markdown renderer with mermaid diagrams + run/copy buttons on code blocks.
 *
 * CSS-ONLY token conformance notes (per Track A §12 audit, 2026-05-07):
 *   - Inline SVG `fill="..."` attributes use literal hex (e.g. fill="#FFFFFF").
 *     Browsers don't resolve `var(--ds-*)` inside SVG presentation attributes.
 *     Accepted exception. See lines with `<svg ... fill="#FFFFFF">` strings.
 *   - PNG-export wrapper (`downloadMermaidAsPNG`) hardcodes `#ffffff` for the
 *     off-screen rasterization background. html-to-image rasterizes a detached
 *     DOM node; CSS variables don't resolve in that context. Accepted exception.
 *   - Translucent-white-on-dark UI affordances (`rgba(255, 255, 255, 0.1/0.2)`)
 *     for code-block button hover states have no DS token equivalent
 *     (gray-alpha is rgba(0,0,0,...), not white-alpha). Accepted exception
 *     until a `--ds-white-alpha-*` token is introduced.
 */
import { Box } from '@mui/material';
import { useRef, useEffect, useState } from 'react';
import PropTypes from 'prop-types';
import { marked } from 'marked';
import DOMPurify from 'dompurify';
import { withErrorBoundary, reportHandledError } from '@common/ErrorBoundary';
import { colors } from 'src/utils/colors';
import mermaid from 'mermaid';
import DownloadIcon from '@assets/download-f.svg';
import Menu from '@mui/material/Menu';
import MenuItem from '@mui/material/MenuItem';
import { toPng } from 'html-to-image';
import { snackbar } from './../common/snackbarService';
import { MermaidChartJS } from './MermaidChartJS';
import { createRoot } from 'react-dom/client';

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
    fontSize: '12px',
    nodePadding: 16,
  },
});

const renderer = new marked.Renderer();

// Store chart codes temporarily
const chartCodes = new Map();
let chartCounter = 0;

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

marked.setOptions({
  renderer: renderer,
  breaks: true,
  gfm: true,
  smartLists: true,
  smartypants: true,
});

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
    fontWeight: 500,
    lineHeight: 1.2,
    //scrollMarginTop: '8px',
  },
  '& h1': {
    fontSize: '18px',
    color: colors.text.secondary,
    fontWeight: 500,
    letterSpacing: '-0.025em',
    marginBottom: '14px',
    paddingBottom: '6px',
    borderBottom: '1px solid var(--ds-gray-200)',
  },
  '& h2': {
    fontSize: 'var(--ds-text-body-lg)',
    color: colors.text.secondary,
    fontWeight: 500,
    marginTop: '28px',
    marginBottom: '12px',
    paddingBottom: '8px',
    borderBottom: '1px solid var(--ds-gray-200)',
  },
  '& h3': {
    fontSize: 'var(--ds-text-body-lg)',
    color: 'var(--ds-gray-700)',
    fontWeight: 500,
    marginTop: '20px',
    marginBottom: '8px',
    '& strong': {
      fontWeight: 500,
      fontSize: 'var(--ds-text-body-lg)',
    },
  },
  '& p': {
    fontSize: 'var(--ds-text-body)',
    marginTop: '4px',
    fontWeight: 400,
    marginBottom: '20px',
    color: colors.text.secondary,
    lineHeight: 1.6,
    '& code': {
      backgroundColor: 'var(--ds-gray-100)',
      padding: '2px 6px',
      borderRadius: 'var(--ds-radius-sm)',
      margin: '0 2px',
      fontSize: 'var(--ds-text-caption)',
      color: colors.text.secondary,
      fontFamily: '"Roboto Mono", monospace',
      border: '1px solid var(--ds-gray-200)',
    },
    '& strong, & b': {
      fontWeight: '500',
      color: colors.text.secondary,
      marginBottom: '400px !important',
    },
  },
  '& a': {
    color: 'var(--ds-blue-600)',
    textDecoration: 'none',
    fontSize: 'var(--ds-text-body)',
    transition: 'all 0.2s ease',
    '&:hover': {
      borderBottom: '1px solid var(--ds-blue-400)',
      backgroundColor: 'var(--ds-gray-100)',
    },
  },
  '& ul, & ol': {
    paddingLeft: '16px',
    marginBottom: '24px',
    '& li': {
      marginBottom: '4px',
      color: colors.text.secondary,
      position: 'relative',
      paddingLeft: '0px',
      lineHeight: 1.6,
      fontWeight: 400,
      '&::marker': {
        color: colors.text.secondary,
      },
      '& p': {
        marginTop: 0,
        marginBottom: '4px',
      },
      '& strong, & b': {
        fontWeight: '400',
        color: 'var(--ds-gray-700)',
      },
      '& code': {
        backgroundColor: 'var(--ds-gray-100)',
        padding: '2px 6px',
        borderRadius: 'var(--ds-radius-sm)',
        margin: '0 2px',
        fontSize: 'var(--ds-text-caption)',
        color: colors.text.secondary,
        fontFamily: '"Roboto Mono", monospace',
        border: '1px solid var(--ds-gray-200)',
      },
    },
  },
  '& blockquote': {
    borderLeft: '4px solid var(--ds-gray-300)',
    backgroundColor: 'var(--ds-gray-100)',
    padding: '12px 16px',
    margin: '16px 0',
    color: colors.text.secondary,
    fontStyle: 'italic',
    borderRadius: '0 6px 6px 0',
    '& p': {
      marginBottom: '0px !important',
      '&:last-child': {
        marginBottom: 0,
      },
    },
  },
  '& pre': {
    backgroundColor: 'var(--ds-gray-700)',
    color: 'var(--ds-gray-200) !important',
    padding: '12px 16px !important',
    borderRadius: 'var(--ds-radius-md)',
    marginBottom: '16px',
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
      top: '8px',
      right: '8px',
      opacity: 1,
      backgroundColor: 'transparent',
      border: 'none',
      outline: 'none',
      boxShadow: 'none',
      '& button': {
        backgroundColor: 'rgba(255, 255, 255, 0.1)',
        border: 'none',
        outline: 'none',
        boxShadow: 'none',
      },
    },
  },
  '& hr': {
    border: 'none',
    height: '1px',
    backgroundColor: 'var(--ds-gray-200)',
    margin: '24px 0',
    position: 'relative',
    '&::before': {
      content: '""',
      position: 'absolute',
      width: '40px',
      height: '2px',
      backgroundColor: 'var(--ds-blue-500)',
      top: '-1px',
      left: '50%',
      transform: 'translateX(-50%)',
      borderRadius: '2px',
    },
  },
  '& h1 + hr': {
    display: 'none',
  },
  '& table': {
    width: '100%',
    maxWidth: '100%',
    display: 'block',
    overflowX: 'auto',
    borderCollapse: 'separate',
    borderSpacing: 0,
    marginBottom: '16px',
    '& th': {
      backgroundColor: colors.background.primaryLightest,
      color: colors.text.secondary,
      padding: '10px 16px',
      fontWeight: 500,
      textAlign: 'left',
      fontSize: 'var(--ds-text-body)',
      overflowWrap: 'break-word',
    },
    '& td': {
      padding: '10px 16px',
      borderBottom: '1px solid var(--ds-gray-200)',
      color: colors.text.secondary,
      fontSize: 'var(--ds-text-body)',
      transition: 'background-color 0.2s ease',
      overflowWrap: 'break-word',
    },
    '& tr:hover td': {
      backgroundColor: 'var(--ds-gray-100)',
    },
  },
  '& img': {
    maxWidth: '600px !important',
    height: 'auto !important',
    borderRadius: 'var(--ds-radius-md)',
    display: 'block',
    margin: '16px auto',
    boxShadow: '0 4px 6px -1px var(--ds-gray-alpha-200), 0 2px 4px -1px var(--ds-gray-alpha-100)',
  },
  '& kbd': {
    backgroundColor: 'var(--ds-gray-100)',
    border: '1px solid var(--ds-gray-200)',
    borderBottom: '3px solid var(--ds-gray-300)',
    borderRadius: 'var(--ds-radius-sm)',
    padding: '2px 6px',
    fontSize: 'var(--ds-text-caption)',
    fontFamily: '"Roboto Mono", monospace',
  },
  '& details': {
    marginBottom: '12px',
    '& summary': {
      cursor: 'pointer',
      color: 'var(--ds-blue-500)',
      fontWeight: 500,
      padding: '4px 0',
      '&:hover': {
        color: 'var(--ds-blue-700)',
      },
    },
  },
  '& .mermaid': {
    backgroundColor: 'var(--ds-background-100)',
    padding: 'var(--ds-space-4)',
    borderRadius: 'var(--ds-radius-md)',
    overflowX: 'auto',
  },
};

function MarkDowns({ data, sx, allowExecutable, canRunCode = true, onLinkClick }) {
  const containerRef = useRef(null);
  const [copiedStates, setCopiedStates] = useState({});
  const chartRootsRef = useRef([]);

  // Clear chart codes on unmount
  useEffect(() => {
    return () => {
      chartCodes.clear();
      chartCounter = 0;
    };
  }, []);

  const cleanedData = (data || '')
    .split('\n')
    .map((line) => line.trim()) // Removes leading spaces/tabs from every line
    .join('\n');
  const convertedData = marked(cleanedData);
  const sanitizedData = DOMPurify.sanitize(convertedData, {
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

  // Cleanup function for Chart.js roots
  useEffect(() => {
    return () => {
      chartRootsRef.current.forEach((root) => {
        try {
          root.unmount();
        } catch (e) {
          console.error(e);
        }
      });
      chartRootsRef.current = [];
    };
  }, [data]);

  // Render Chart.js charts
  useEffect(() => {
    if (!containerRef.current) {
      return;
    }

    const chartDivs = containerRef.current.querySelectorAll('.mermaid-chartjs');

    chartDivs.forEach((div) => {
      if (div.hasAttribute('data-rendered')) {
        return;
      }

      const chartId = div.getAttribute('data-chart-id');
      const chartData = chartCodes.get(chartId);

      if (!chartData) {
        console.warn('Chart data not found for ID:', chartId);
        return;
      }

      div.setAttribute('data-rendered', 'true');

      try {
        const root = createRoot(div);
        chartRootsRef.current.push(root);
        root.render(<MermaidChartJS mermaidCode={chartData} />);
      } catch (error) {
        reportHandledError(error, 'MarkDowns/ChartJS', { chartData });
      }
    });
  }, [sanitizedData]);

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
      wrapper.style.background = '#ffffff';
      wrapper.style.padding = '20px';
      wrapper.style.display = 'inline-block';

      const svgClone = svgElement.cloneNode(true);
      wrapper.appendChild(svgClone);
      document.body.appendChild(wrapper);

      const dataUrl = await toPng(wrapper, {
        backgroundColor: '#ffffff',
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
    const [anchorEl, setAnchorEl] = useState(null);
    const open = Boolean(anchorEl);

    const handleOpen = (e) => {
      setAnchorEl(e.currentTarget);
    };

    const handleClose = () => {
      setAnchorEl(null);
    };

    return (
      <>
        <button
          onClick={handleOpen}
          style={{
            all: 'unset',
            position: 'absolute',
            inset: 0,
            cursor: 'pointer',
          }}
        />
        <Menu
          anchorEl={anchorEl}
          open={open}
          onClose={handleClose}
          anchorOrigin={{ vertical: 'bottom', horizontal: 'right' }}
          transformOrigin={{ vertical: 'top', horizontal: 'right' }}
        >
          <MenuItem
            onClick={() => {
              handleClose();
              onDownloadPNG(svg, `mermaid-diagram-${index + 1}.png`);
            }}
          >
            Download PNG
          </MenuItem>

          <MenuItem
            onClick={() => {
              handleClose();
              onDownloadSVG(svg, `mermaid-diagram-${index + 1}.svg`);
            }}
          >
            Download SVG
          </MenuItem>
        </Menu>
      </>
    );
  };

  const createDownloadButton = (svg, index) => {
    const wrapper = document.createElement('div');
    wrapper.style.position = 'absolute';
    wrapper.style.top = '8px';
    wrapper.style.right = '8px';
    wrapper.style.zIndex = '10';

    const btn = document.createElement('button');
    btn.type = 'button';
    btn.className = 'download-mermaid-btn';

    Object.assign(btn.style, {
      height: '24px',
      width: '24px',
      display: 'flex',
      justifyContent: 'center',
      alignItems: 'center',
      cursor: 'pointer',
      borderRadius: 'var(--ds-radius-sm)',
      border: '0.5px solid var(--ds-gray-300)',
      background: 'var(--ds-background-100)',
      padding: '0',
    });

    const img = document.createElement('img');
    img.src = DownloadIcon.src;
    img.alt = 'download';
    img.style.width = '16px';
    img.style.height = '16px';
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

    const tryRenderMermaid = async (renderId, code) => {
      const { svg } = await mermaid.render(renderId, code);
      return svg;
    };

    const mermaidDivs = containerRef.current.querySelectorAll('.mermaid');

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
        cleanupMermaidArtifacts(renderId1);
        const renderId2 = `mermaid-svg-${ts}-${index}-retry`;
        try {
          svg = await tryRenderMermaid(renderId2, sanitizeMermaidSyntax(originalCode));
        } catch (retryErr) {
          cleanupMermaidArtifacts(renderId2);
          reportHandledError(retryErr instanceof Error ? retryErr : new Error(String(retryErr)), 'MarkDowns/Mermaid', {
            chartId,
            originalError: err instanceof Error ? err.message : String(err),
          });
        }
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
  }, [sanitizedData]);

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
            top: '4px',
            right: '32px',
            background: 'var(--ds-green-500)',
            border: 'none',
            color: 'var(--ds-background-100)',
            padding: '4px',
            borderRadius: '3px',
            cursor: canRunCode ? 'pointer' : 'not-allowed',
            opacity: canRunCode ? '1' : '0.4',
            width: '24px',
            height: '24px',
            display: 'flex',
            alignItems: 'center',
            justifyContent: 'center',
            transition: 'background-color 0.2s ease',
            outline: 'none',
            boxShadow: 'none',
          });

          runButton.innerHTML = '<svg width="12" height="12" viewBox="0 0 24 24" fill="#FFFFFF"><path d="M8 5v14l11-7z"/></svg>';

          runButton.addEventListener('mouseenter', () => {
            runButton.style.backgroundColor = 'var(--ds-green-600)';
          });

          runButton.addEventListener('mouseleave', () => {
            runButton.style.backgroundColor = 'var(--ds-green-500)';
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
          top: '4px',
          right: '4px',
          background: 'rgba(255, 255, 255, 0.1)',
          border: 'none',
          color: 'var(--ds-gray-200)',
          padding: '4px',
          borderRadius: '3px',
          cursor: 'pointer',
          width: '24px',
          height: '24px',
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
          copyButton.style.backgroundColor = 'rgba(255, 255, 255, 0.2)';
        });

        copyButton.addEventListener('mouseleave', () => {
          copyButton.style.backgroundColor = 'rgba(255, 255, 255, 0.1)';
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
  }, [convertedData, copiedStates, canRunCode, onLinkClick]);

  const combinedSx = {
    maxWidth: '100%',
    width: '100%',
    padding: 'var(--ds-space-4)',
    fontSize: 'var(--ds-text-small) !important',
    borderRadius: 'var(--ds-radius-md)',
    maxHeight: '500px',
    overflowY: 'auto',
    overflowX: 'hidden',
    overflowWrap: 'break-word',
    boxSizing: 'border-box',
    ...defaultStyles,
    ...sx,
  };

  return (
    <Box sx={combinedSx} ref={containerRef}>
      <div dangerouslySetInnerHTML={{ __html: sanitizedData }} />
    </Box>
  );
}

export default withErrorBoundary(MarkDowns);

MarkDowns.propTypes = {
  data: PropTypes.string,
  sx: PropTypes.object,
  allowExecutable: PropTypes.func,
  canRunCode: PropTypes.bool,
  onLinkClick: PropTypes.func,
};
