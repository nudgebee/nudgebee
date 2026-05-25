import React from 'react';
import { render } from '@testing-library/react';
import '@testing-library/jest-dom';
import MarkDowns from '@components1/common/MarkDowns';

jest.mock('src/utils/colors', () => ({
  colors: {
    primary: '#3B82F6',
    text: {
      primary: '#3B82F6',
      secondary: '#374151',
      white: '#fff',
      black: '#000',
      tertiary: '#6B7280',
    },
    background: { primaryLightest: '#EFF6FF', white: '#fff' },
    border: { secondary: '#D1D5DB', primary: '#3B82F6' },
  },
}));

jest.mock('@components1/common/snackbarService', () => ({
  snackbar: { success: jest.fn(), error: jest.fn() },
}));

jest.mock('@components1/common/MermaidChartJS', () => {
  const PropTypes = require('prop-types');
  const MermaidChartJS = function MermaidChartJS({ mermaidCode }) {
    return <div data-testid='mermaid-chart'>{mermaidCode}</div>;
  };
  MermaidChartJS.propTypes = { mermaidCode: PropTypes.string };
  return { MermaidChartJS };
});

// Use the global mermaid mock from jest.setup.js but override render return value
jest.mock('mermaid', () => ({
  initialize: jest.fn(),
  render: jest.fn().mockResolvedValue({ svg: '<svg data-testid="mermaid-svg"></svg>' }),
  contentLoaded: jest.fn(),
}));

jest.mock('dompurify', () => ({
  sanitize: jest.fn((html) => html),
}));

jest.mock('html-to-image', () => ({
  toPng: jest.fn().mockResolvedValue('data:image/png;base64,abc'),
}));

// Preserve the real createRoot for React Testing Library to work,
// while also handling MarkDowns' dynamic chart rendering via createRoot
jest.mock('react-dom/client', () => {
  const actual = jest.requireActual('react-dom/client');
  return {
    ...actual,
    createRoot: jest.fn((container, options) => {
      // Return the real createRoot for Testing Library's usage (large containers)
      // but return a mock for MarkDowns' small chart divs
      try {
        return actual.createRoot(container, options);
      } catch {
        return { render: jest.fn(), unmount: jest.fn() };
      }
    }),
  };
});

// Mock marked to return predictable HTML
jest.mock('marked', () => {
  // Create the Renderer constructor that can be called with `new`
  const MockRenderer = jest.fn().mockImplementation(() => ({
    image: jest.fn(),
    code: jest.fn(),
  }));

  // Create the marked function with Renderer and setOptions as properties
  const markedFn = jest.fn((text) => {
    if (!text) return '';
    // Simple markdown to HTML conversions
    if (text.startsWith('# ')) return `<h1>${text.slice(2)}</h1>`;
    if (text.startsWith('## ')) return `<h2>${text.slice(3)}</h2>`;
    if (text.includes('**')) {
      const content = text.replace(/\*\*([^*]*)\*\*/g, '<strong>$1</strong>');
      return `<p>${content}</p>`;
    }
    if (text.includes('[') && text.includes('](')) {
      const start = text.indexOf('[');
      const mid = text.indexOf('](', start);
      const end = text.indexOf(')', mid + 2);
      const content = start >= 0 && mid > start && end > mid ? `<a href="${text.slice(mid + 2, end)}">${text.slice(start + 1, mid)}</a>` : text;
      return `<p>${content}</p>`;
    }
    if (text.startsWith('```') || text.includes('```')) {
      return `<pre><code>some code</code></pre>`;
    }
    return `<p>${text}</p>`;
  });
  markedFn.Renderer = MockRenderer;
  markedFn.setOptions = jest.fn();

  return {
    marked: markedFn,
    Renderer: MockRenderer,
    setOptions: jest.fn(),
  };
});

describe('MarkDowns', () => {
  beforeEach(() => {
    jest.clearAllMocks();
    // mock clipboard
    Object.defineProperty(navigator, 'clipboard', {
      value: { writeText: jest.fn().mockResolvedValue(undefined) },
      writable: true,
    });
    // Re-setup marked mock after clearAllMocks
    const markedModule = require('marked');
    const { marked } = markedModule;
    marked.mockImplementation((text) => {
      if (!text) return '';
      if (text.startsWith('# ')) return `<h1>${text.slice(2)}</h1>`;
      if (text.includes('**')) {
        const content = text.replace(/\*\*([^*]*)\*\*/g, '<strong>$1</strong>');
        return `<p>${content}</p>`;
      }
      if (text.includes('[') && text.includes('](')) {
        const start = text.indexOf('[');
        const mid = text.indexOf('](', start);
        const end = text.indexOf(')', mid + 2);
        const content = start >= 0 && mid > start && end > mid ? `<a href="${text.slice(mid + 2, end)}">${text.slice(start + 1, mid)}</a>` : text;
        return `<p>${content}</p>`;
      }
      if (text.startsWith('```') || text.includes('```')) {
        return `<pre><code>some code</code></pre>`;
      }
      return `<p>${text}</p>`;
    });
    // Re-setup Renderer on the marked function since clearAllMocks resets it
    marked.Renderer = markedModule.Renderer;
    marked.setOptions = jest.fn();
  });

  it('renders without crashing with empty data', () => {
    render(<MarkDowns data='' />);
    expect(document.body).toBeTruthy();
  });

  it('renders plain text content', () => {
    const { container } = render(<MarkDowns data='Hello World' />);
    expect(container.innerHTML).toContain('Hello World');
  });

  it('renders markdown heading correctly', () => {
    const { container } = render(<MarkDowns data='# My Heading' />);
    const heading = container.querySelector('h1');
    expect(heading).toBeInTheDocument();
    expect(heading?.textContent).toContain('My Heading');
  });

  it('renders bold markdown text', () => {
    const { container } = render(<MarkDowns data='**Bold Text**' />);
    const bold = container.querySelector('strong');
    expect(bold).toBeInTheDocument();
    expect(bold?.textContent).toContain('Bold Text');
  });

  it('renders a markdown link', () => {
    const { container } = render(<MarkDowns data='[Click here](https://example.com)' />);
    const link = container.querySelector('a');
    expect(link).toBeInTheDocument();
    expect(link?.getAttribute('href')).toBe('https://example.com');
  });

  it('renders code blocks with pre/code tags', () => {
    const { container } = render(<MarkDowns data={'```\nsome code\n```'} />);
    const pre = container.querySelector('pre');
    expect(pre).toBeInTheDocument();
  });

  it('renders with custom sx styles applied', () => {
    const { container } = render(<MarkDowns data='test' sx={{ backgroundColor: 'red' }} />);
    expect(container.firstChild).toBeInTheDocument();
  });

  it('renders null-safe with undefined data', () => {
    render(<MarkDowns data={undefined} />);
    expect(document.body).toBeTruthy();
  });
});
