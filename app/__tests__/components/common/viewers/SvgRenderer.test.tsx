import React from 'react';
import { render } from '@testing-library/react';
import SvgRenderer from '@shared/viewers/SvgRenderer';

jest.mock('@utils/colors');

const mockCreateObjectURL = jest.fn().mockReturnValue('blob:mock-url');
const mockRevokeObjectURL = jest.fn();
global.URL.createObjectURL = mockCreateObjectURL;
global.URL.revokeObjectURL = mockRevokeObjectURL;

describe('SvgRenderer', () => {
  beforeEach(() => {
    jest.clearAllMocks();
    mockCreateObjectURL.mockReturnValue('blob:mock-url');
  });

  test('renders an iframe container', () => {
    const { container } = render(<SvgRenderer svg='<svg></svg>' />);
    expect(container.querySelector('iframe')).toBeInTheDocument();
  });

  test('creates a blob URL when svg prop is provided', () => {
    render(<SvgRenderer svg='<svg></svg>' />);
    expect(mockCreateObjectURL).toHaveBeenCalledTimes(1);
    expect(mockCreateObjectURL).toHaveBeenCalledWith(expect.any(Blob));
  });

  test('does not create blob URL when svg is empty', () => {
    render(<SvgRenderer svg='' />);
    expect(mockCreateObjectURL).not.toHaveBeenCalled();
  });

  test('applies custom style to iframe', () => {
    const customStyle = { width: '200px', height: '300px' };
    const { container } = render(<SvgRenderer svg='<svg></svg>' style={customStyle} />);
    const iframe = container.querySelector('iframe');
    expect(iframe).toHaveStyle({ width: '200px', height: '300px' });
  });

  test('creates a new blob URL when svg prop changes', () => {
    const { rerender } = render(<SvgRenderer svg="<svg id='first'></svg>" />);
    expect(mockCreateObjectURL).toHaveBeenCalledTimes(1);

    rerender(<SvgRenderer svg="<svg id='second'></svg>" />);
    expect(mockCreateObjectURL).toHaveBeenCalledTimes(2);
  });

  test('revokes previous blob URL when svg prop changes', () => {
    const { rerender } = render(<SvgRenderer svg="<svg id='first'></svg>" />);
    rerender(<SvgRenderer svg="<svg id='second'></svg>" />);
    expect(mockRevokeObjectURL).toHaveBeenCalledWith('blob:mock-url');
  });

  test('iframe has sandbox attribute for script isolation', () => {
    const { container } = render(<SvgRenderer svg='<svg></svg>' />);
    const iframe = container.querySelector('iframe');
    expect(iframe).toHaveAttribute('sandbox', 'allow-scripts');
  });
});
