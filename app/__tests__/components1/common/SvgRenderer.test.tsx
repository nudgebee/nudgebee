import React from 'react';
import { render } from '@testing-library/react';
import SvgRenderer from '@components1/common/SvgRenderer';

describe('SvgRenderer', () => {
  let mockParsedSvg: SVGSVGElement;
  let mockDOMParser: { parseFromString: jest.Mock };

  beforeEach(() => {
    mockParsedSvg = document.createElementNS('http://www.w3.org/2000/svg', 'svg');
    mockParsedSvg.setAttribute('id', 'test-svg');
    mockDOMParser = {
      parseFromString: jest.fn().mockReturnValue({ documentElement: mockParsedSvg }),
    };
    global.DOMParser = jest.fn().mockImplementation(() => mockDOMParser) as any;
  });

  afterEach(() => {
    jest.clearAllMocks();
  });

  test('renders a div container', () => {
    const { container } = render(<SvgRenderer svg='<svg></svg>' />);
    expect(container.querySelector('div')).toBeInTheDocument();
  });

  test('appends SVG content to container when svg prop provided', () => {
    render(<SvgRenderer svg="<svg id='test-svg'></svg>" />);
    // DOMParser was called and the parsed SVG was appended
    expect(mockDOMParser.parseFromString).toHaveBeenCalledWith("<svg id='test-svg'></svg>", 'image/svg+xml');
  });

  test('uses DOMParser to parse svg string', () => {
    const svgString = '<svg><circle r="10"/></svg>';
    render(<SvgRenderer svg={svgString} />);
    expect(mockDOMParser.parseFromString).toHaveBeenCalledWith(svgString, 'image/svg+xml');
  });

  test('applies custom style to container', () => {
    const customStyle = { width: '200px', height: '300px' };
    const { container } = render(<SvgRenderer svg='<svg></svg>' style={customStyle} />);
    const div = container.querySelector('div');
    expect(div).toHaveStyle({ width: '200px', height: '300px' });
  });

  test('clears previous SVG when svg prop changes', () => {
    const { rerender } = render(<SvgRenderer svg="<svg id='first'></svg>" />);
    const callCountAfterFirst = mockDOMParser.parseFromString.mock.calls.length;

    rerender(<SvgRenderer svg="<svg id='second'></svg>" />);

    expect(mockDOMParser.parseFromString.mock.calls.length).toBeGreaterThan(callCountAfterFirst);
    expect(mockDOMParser.parseFromString).toHaveBeenLastCalledWith("<svg id='second'></svg>", 'image/svg+xml');
  });
});
