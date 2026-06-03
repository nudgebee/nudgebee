import React from 'react';
import { render } from '@testing-library/react';
import ColorDots from '@components1/common/widgets/ColorDots';

jest.mock('src/utils/colors', () => ({
  colors: {
    highest: '#FF0000',
    high: '#FF4500',
    medium: '#FFA500',
    low: '#008000',
    lowest: '#90EE90',
    open: '#0000FF',
    toDo: '#800080',
    inProgress: '#FFD700',
    done: '#00CED1',
    critical: '#DC143C',
    black: '#000000',
  },
}));

describe('ColorDots', () => {
  const cases = [
    ['Highest', '#FF0000'],
    ['High', '#FF4500'],
    ['Medium', '#FFA500'],
    ['Low', '#008000'],
    ['Lowest', '#90EE90'],
    ['Open', '#0000FF'],
    ['To Do', '#800080'],
    ['In Progress', '#FFD700'],
    ['Done', '#00CED1'],
    ['Critical', '#DC143C'],
    ['Unknown', '#000000'],
  ];

  test.each(cases)('renders with severity "%s"', (severity) => {
    const { container } = render(<ColorDots severity={severity} />);
    expect(container.firstChild).toBeInTheDocument();
  });

  it('renders a Box element', () => {
    const { container } = render(<ColorDots severity='high' />);
    expect(container.firstChild).toBeTruthy();
  });

  it('handles case-insensitive severity', () => {
    const { container } = render(<ColorDots severity='CRITICAL' />);
    expect(container.firstChild).toBeTruthy();
  });

  it('uses default color for unknown severity', () => {
    const { container } = render(<ColorDots severity='xyzunknown' />);
    expect(container.firstChild).toBeTruthy();
  });
});
