import React from 'react';
import { render } from '@testing-library/react';
import { Skeleton } from '@ui/Skeleton';

describe('Skeleton', () => {
  it('renders text skeleton', () => {
    const { container } = render(<Skeleton shape='text' />);
    expect(container.firstChild).toBeInTheDocument();
  });

  it('renders rect skeleton', () => {
    const { container } = render(<Skeleton shape='rect' />);
    expect(container.firstChild).toBeInTheDocument();
  });

  it('renders circle skeleton', () => {
    const { container } = render(<Skeleton shape='circle' />);
    expect(container.firstChild).toBeInTheDocument();
  });

  it('renders all text sizes without crashing', () => {
    const sizes = ['caption', 'text', 'title', 'heading'] as const;
    sizes.forEach((size) => {
      const { unmount } = render(<Skeleton shape='text' size={size} />);
      unmount();
    });
  });

  it('renders shimmer animation by default', () => {
    const { container } = render(<Skeleton shape='rect' animation='shimmer' />);
    expect(container.firstChild).toBeInTheDocument();
  });

  it('renders none animation', () => {
    const { container } = render(<Skeleton shape='rect' animation='none' />);
    expect(container.firstChild).toBeInTheDocument();
  });
});

describe('Skeleton.TableRow', () => {
  it('renders 3 td cells inside a table row', () => {
    const { container } = render(
      <table>
        <tbody>
          <tr>
            <Skeleton.TableRow columns={3} />
          </tr>
        </tbody>
      </table>
    );
    expect(container.querySelectorAll('td')).toHaveLength(3);
  });

  it('renders 5 td cells inside a table row', () => {
    const { container } = render(
      <table>
        <tbody>
          <tr>
            <Skeleton.TableRow columns={5} />
          </tr>
        </tbody>
      </table>
    );
    expect(container.querySelectorAll('td')).toHaveLength(5);
  });
});

describe('Skeleton.Card', () => {
  it('renders without crashing', () => {
    const { container } = render(<Skeleton.Card />);
    expect(container.firstChild).toBeInTheDocument();
  });
});

describe('Skeleton.ChatMessage', () => {
  it('renders without crashing', () => {
    const { container } = render(<Skeleton.ChatMessage />);
    expect(container.firstChild).toBeInTheDocument();
  });
});
