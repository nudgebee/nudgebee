import React from 'react';
import { render, screen } from '@testing-library/react';
import ShimmerLoading from '@components1/common/ShimmerLoading';

describe('ShimmerLoading', () => {
  describe('when isLoading is false', () => {
    it('renders children', () => {
      render(
        <ShimmerLoading isLoading={false}>
          <div data-testid='child-content'>Content</div>
        </ShimmerLoading>
      );
      expect(screen.getByTestId('child-content')).toBeInTheDocument();
    });

    it('renders children text content', () => {
      render(<ShimmerLoading isLoading={false}>Hello World</ShimmerLoading>);
      expect(screen.getByText('Hello World')).toBeInTheDocument();
    });

    it('does not render shimmer div', () => {
      const { container } = render(
        <ShimmerLoading isLoading={false}>
          <span>Child</span>
        </ShimmerLoading>
      );
      expect(container.querySelector('.shimmer')).not.toBeInTheDocument();
    });

    it('renders null children without error', () => {
      expect(() => render(<ShimmerLoading isLoading={false}>{null}</ShimmerLoading>)).not.toThrow();
    });

    it('renders multiple children', () => {
      render(
        <ShimmerLoading isLoading={false}>
          <span data-testid='child-1'>First</span>
          <span data-testid='child-2'>Second</span>
        </ShimmerLoading>
      );
      expect(screen.getByTestId('child-1')).toBeInTheDocument();
      expect(screen.getByTestId('child-2')).toBeInTheDocument();
    });
  });

  describe('when isLoading is true and no lines prop', () => {
    it('renders a div with shimmer class', () => {
      const { container } = render(<ShimmerLoading isLoading={true} />);
      const shimmerDiv = container.querySelector('.shimmer');
      expect(shimmerDiv).toBeInTheDocument();
    });

    it('uses default height of 280px when height is not provided', () => {
      const { container } = render(<ShimmerLoading isLoading={true} />);
      const shimmerDiv = container.querySelector('.shimmer');
      expect(shimmerDiv).toHaveStyle({ height: '280px' });
    });

    it('uses default width of 100% when width is not provided', () => {
      const { container } = render(<ShimmerLoading isLoading={true} />);
      const shimmerDiv = container.querySelector('.shimmer');
      expect(shimmerDiv).toHaveStyle({ width: '100%' });
    });

    it('applies custom height prop', () => {
      const { container } = render(<ShimmerLoading isLoading={true} height='500px' />);
      const shimmerDiv = container.querySelector('.shimmer');
      expect(shimmerDiv).toHaveStyle({ height: '500px' });
    });

    it('applies custom width prop', () => {
      const { container } = render(<ShimmerLoading isLoading={true} width='50%' />);
      const shimmerDiv = container.querySelector('.shimmer');
      expect(shimmerDiv).toHaveStyle({ width: '50%' });
    });

    it('does not render children', () => {
      render(
        <ShimmerLoading isLoading={true}>
          <div data-testid='child-content'>Content</div>
        </ShimmerLoading>
      );
      expect(screen.queryByTestId('child-content')).not.toBeInTheDocument();
    });
  });

  describe('when isLoading is true and lines prop is provided', () => {
    it('renders the correct number of shimmer line boxes', () => {
      const { container } = render(<ShimmerLoading isLoading={true} lines={3} />);
      // The outer Box contains N inner Box elements
      const outerBox = container.firstChild;
      expect(outerBox.children.length).toBe(3);
    });

    it('renders 5 shimmer lines', () => {
      const { container } = render(<ShimmerLoading isLoading={true} lines={5} />);
      const outerBox = container.firstChild;
      expect(outerBox.children.length).toBe(5);
    });

    it('renders 10 shimmer lines', () => {
      const { container } = render(<ShimmerLoading isLoading={true} lines={10} />);
      const outerBox = container.firstChild;
      expect(outerBox.children.length).toBe(10);
    });

    it('renders more than 10 lines (cycles through widths array)', () => {
      const { container } = render(<ShimmerLoading isLoading={true} lines={12} />);
      const outerBox = container.firstChild;
      expect(outerBox.children.length).toBe(12);
    });

    it('does not render shimmer class div when lines is set', () => {
      const { container } = render(<ShimmerLoading isLoading={true} lines={3} />);
      expect(container.querySelector('.shimmer')).not.toBeInTheDocument();
    });

    it('does not render children when loading with lines', () => {
      render(
        <ShimmerLoading isLoading={true} lines={3}>
          <div data-testid='child-content'>Content</div>
        </ShimmerLoading>
      );
      expect(screen.queryByTestId('child-content')).not.toBeInTheDocument();
    });

    it('treats lines=0 as falsy and falls back to single shimmer block', () => {
      const { container } = render(<ShimmerLoading isLoading={true} lines={0} />);
      expect(container.querySelector('.shimmer')).toBeInTheDocument();
    });

    it('accepts custom lineHeight prop without crashing', () => {
      expect(() => render(<ShimmerLoading isLoading={true} lines={3} lineHeight='32px' />)).not.toThrow();
    });

    it('accepts custom lineSpacing prop without crashing', () => {
      expect(() => render(<ShimmerLoading isLoading={true} lines={3} lineSpacing='8px' />)).not.toThrow();
    });
  });

  describe('switching between loading and loaded states', () => {
    it('renders shimmer when isLoading changes to true', () => {
      const { rerender, container } = render(
        <ShimmerLoading isLoading={false}>
          <div data-testid='content'>Loaded</div>
        </ShimmerLoading>
      );
      expect(screen.getByTestId('content')).toBeInTheDocument();

      rerender(
        <ShimmerLoading isLoading={true}>
          <div data-testid='content'>Loaded</div>
        </ShimmerLoading>
      );
      expect(screen.queryByTestId('content')).not.toBeInTheDocument();
      expect(container.querySelector('.shimmer')).toBeInTheDocument();
    });

    it('renders children when isLoading changes to false', () => {
      const { rerender, container } = render(<ShimmerLoading isLoading={true} />);
      expect(container.querySelector('.shimmer')).toBeInTheDocument();

      rerender(
        <ShimmerLoading isLoading={false}>
          <div data-testid='content'>Loaded</div>
        </ShimmerLoading>
      );
      expect(screen.getByTestId('content')).toBeInTheDocument();
    });
  });
});
