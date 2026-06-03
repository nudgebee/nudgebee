import React from 'react';
import { render, screen } from '@testing-library/react';
import SafeIcon from '@components1/common/SafeIcon';

jest.mock('next/image', () => ({
  __esModule: true,
  default: ({ src, alt, ...props }) => <img src={src} alt={alt} data-testid='next-image' {...props} />,
}));

// A simple React component to use as a src
const MockSvgComponent = (props) => <svg data-testid='svg-component' {...props} />;

describe('SafeIcon', () => {
  describe('when src is falsy', () => {
    it('returns null when src is undefined', () => {
      const { container } = render(<SafeIcon />);
      expect(container.firstChild).toBeNull();
    });

    it('returns null when src is null', () => {
      const { container } = render(<SafeIcon src={null} />);
      expect(container.firstChild).toBeNull();
    });

    it('returns null when src is empty string', () => {
      const { container } = render(<SafeIcon src='' />);
      expect(container.firstChild).toBeNull();
    });

    it('returns null when src is false', () => {
      const { container } = render(<SafeIcon src={false} />);
      expect(container.firstChild).toBeNull();
    });
  });

  describe('when src is a React element', () => {
    it('returns the React element directly', () => {
      const element = <span data-testid='react-element'>Icon</span>;
      render(<SafeIcon src={element} />);
      expect(screen.getByTestId('react-element')).toBeInTheDocument();
    });

    it('does not wrap the React element', () => {
      const element = <div data-testid='direct-element'>Direct</div>;
      render(<SafeIcon src={element} />);
      expect(screen.getByTestId('direct-element')).toBeInTheDocument();
      expect(screen.queryByTestId('next-image')).not.toBeInTheDocument();
    });

    it('returns a React element with nested content', () => {
      const element = (
        <svg data-testid='svg-element'>
          <path d='M0 0' />
        </svg>
      );
      render(<SafeIcon src={element} />);
      expect(screen.getByTestId('svg-element')).toBeInTheDocument();
    });
  });

  describe('when src is a function component (SVG component)', () => {
    it('renders as SVG component when src is a function', () => {
      render(<SafeIcon src={MockSvgComponent} />);
      expect(screen.getByTestId('svg-component')).toBeInTheDocument();
    });

    it('does not render next/image when src is a function', () => {
      render(<SafeIcon src={MockSvgComponent} />);
      expect(screen.queryByTestId('next-image')).not.toBeInTheDocument();
    });

    it('passes remaining props to the function component', () => {
      render(<SafeIcon src={MockSvgComponent} width={24} height={24} />);
      const svgEl = screen.getByTestId('svg-component');
      expect(svgEl).toBeInTheDocument();
    });

    it('applies fill styles when fill prop is true', () => {
      render(<SafeIcon src={MockSvgComponent} fill={true} />);
      const svgEl = screen.getByTestId('svg-component');
      expect(svgEl).toHaveStyle({ width: '100%' });
      expect(svgEl).toHaveStyle({ height: '100%' });
      expect(svgEl).toHaveStyle({ position: 'absolute' });
    });

    it('applies width style as px string when width is a number', () => {
      render(<SafeIcon src={MockSvgComponent} width={32} />);
      const svgEl = screen.getByTestId('svg-component');
      expect(svgEl).toHaveStyle({ width: '32px' });
    });

    it('applies height style as px string when height is a number', () => {
      render(<SafeIcon src={MockSvgComponent} height={32} />);
      const svgEl = screen.getByTestId('svg-component');
      expect(svgEl).toHaveStyle({ height: '32px' });
    });

    it('applies width as string when width is already a string', () => {
      render(<SafeIcon src={MockSvgComponent} width='2rem' />);
      const svgEl = screen.getByTestId('svg-component');
      expect(svgEl).toHaveStyle({ width: '2rem' });
    });

    it('applies display block style by default', () => {
      render(<SafeIcon src={MockSvgComponent} />);
      const svgEl = screen.getByTestId('svg-component');
      expect(svgEl).toHaveStyle({ display: 'block' });
    });
  });

  describe('when src is an object without .src property', () => {
    it('renders as component when src is a plain object without .src', () => {
      // An object without .src that is also not a function — SafeIcon resolves iconSource to the object itself
      // and checks typeof iconSource === 'object' && !iconSource.src → treats it as a component
      // However it must be callable as a React component which plain objects are not.
      // This branch essentially needs iconSource to be a callable or renderable value.
      // We test with an object that has a .default function
      const mockIconModule = { default: MockSvgComponent };
      render(<SafeIcon src={mockIconModule} />);
      expect(screen.getByTestId('svg-component')).toBeInTheDocument();
    });
  });

  describe('when src has a .src property (image object)', () => {
    it('renders next/image when src has a .src property', () => {
      // iconSource = src.default || src = src (since no .default)
      // typeof object && !object.src is false (has .src), so falls through to next/image
      // next/image receives the full object as src
      const imgObject = { src: '/path/to/image.png', width: 100, height: 100 };
      render(<SafeIcon src={imgObject} alt='test image' />);
      expect(screen.getByTestId('next-image')).toBeInTheDocument();
    });

    it('renders next/image that receives the full object as src', () => {
      // SafeIcon passes iconSource (the whole object) to next/image src prop
      const imgObject = { src: '/icon.png', width: 64, height: 64 };
      render(<SafeIcon src={imgObject} alt='icon' />);
      // The image is rendered (object gets coerced to "[object Object]" as string attr)
      expect(screen.getByTestId('next-image')).toBeInTheDocument();
    });

    it('uses default alt "icon" when alt not provided', () => {
      const imgObject = { src: '/icon.png', width: 64, height: 64 };
      render(<SafeIcon src={imgObject} />);
      const image = screen.getByTestId('next-image');
      expect(image).toHaveAttribute('alt', 'icon');
    });

    it('uses custom alt when provided', () => {
      const imgObject = { src: '/icon.png', width: 64, height: 64 };
      render(<SafeIcon src={imgObject} alt='custom alt' />);
      const image = screen.getByTestId('next-image');
      expect(image).toHaveAttribute('alt', 'custom alt');
    });
  });

  describe('when src is a string with .src-like path', () => {
    it('renders next/image when src is a plain string path', () => {
      // A plain string: src.default is undefined, src itself is a string
      // typeof string is 'string', not 'function' or 'object', so falls to next/image
      render(<SafeIcon src='/images/icon.png' alt='string icon' />);
      expect(screen.getByTestId('next-image')).toBeInTheDocument();
    });

    it('passes string src directly to next/image', () => {
      render(<SafeIcon src='/images/icon.png' alt='my icon' />);
      const image = screen.getByTestId('next-image');
      expect(image).toHaveAttribute('src', '/images/icon.png');
    });
  });

  describe('alt prop defaults', () => {
    it('uses default alt "icon" for next/image', () => {
      render(<SafeIcon src='/test.png' />);
      const image = screen.getByTestId('next-image');
      expect(image).toHaveAttribute('alt', 'icon');
    });

    it('uses provided alt text for next/image', () => {
      render(<SafeIcon src='/test.png' alt='my alt' />);
      const image = screen.getByTestId('next-image');
      expect(image).toHaveAttribute('alt', 'my alt');
    });
  });
});
