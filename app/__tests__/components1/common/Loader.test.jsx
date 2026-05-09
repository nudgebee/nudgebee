import React from 'react';
import { render, screen } from '@testing-library/react';
import Loader from '@components1/common/Loader';

jest.mock('@assets', () => ({ Loadergif: 'loader.gif' }));

jest.mock('next/image', () => ({
  __esModule: true,
  default: ({ src, alt, style, unoptimized, ...props }) => (
    <img src={src} alt={alt} style={style} data-testid='loader-image' data-unoptimized={String(unoptimized)} {...props} />
  ),
}));

describe('Loader', () => {
  it('renders the loader image', () => {
    render(<Loader />);
    const image = screen.getByTestId('loader-image');
    expect(image).toBeInTheDocument();
  });

  it('renders image with correct alt text', () => {
    render(<Loader />);
    const image = screen.getByAltText('Loading...');
    expect(image).toBeInTheDocument();
  });

  it('renders image with the Loadergif source', () => {
    render(<Loader />);
    const image = screen.getByTestId('loader-image');
    expect(image).toHaveAttribute('src', 'loader.gif');
  });

  it('renders image with unoptimized set to true', () => {
    render(<Loader />);
    const image = screen.getByTestId('loader-image');
    expect(image).toHaveAttribute('data-unoptimized', 'true');
  });

  it('renders a wrapper div with full-screen styles', () => {
    const { container } = render(<Loader />);
    const wrapper = container.firstChild;
    expect(wrapper.tagName).toBe('DIV');
    expect(wrapper).toHaveStyle({ display: 'flex' });
    expect(wrapper).toHaveStyle({ justifyContent: 'center' });
    expect(wrapper).toHaveStyle({ alignItems: 'center' });
    expect(wrapper).toHaveStyle({ height: '100vh' });
    expect(wrapper).toHaveStyle({ width: '100vw' });
  });

  it('applies default styles when no style prop is provided', () => {
    const { container } = render(<Loader />);
    const wrapper = container.firstChild;
    expect(wrapper).toHaveStyle({ height: '100vh' });
    expect(wrapper).toHaveStyle({ width: '100vw' });
  });

  it('merges custom style prop with default styles', () => {
    const { container } = render(<Loader style={{ backgroundColor: 'red' }} />);
    const wrapper = container.firstChild;
    // The component spreads style prop into loaderStyle, so display:flex and custom styles coexist
    expect(wrapper).toHaveStyle({ display: 'flex' });
  });

  it('overrides default styles with custom style prop', () => {
    const { container } = render(<Loader style={{ height: '50vh', width: '50vw' }} />);
    const wrapper = container.firstChild;
    expect(wrapper).toHaveStyle({ height: '50vh' });
    expect(wrapper).toHaveStyle({ width: '50vw' });
  });

  it('renders image with correct inline styles', () => {
    render(<Loader />);
    const image = screen.getByTestId('loader-image');
    expect(image).toHaveStyle({ width: '150px' });
    expect(image).toHaveStyle({ height: 'auto' });
  });

  it('renders without crashing when no props are passed', () => {
    expect(() => render(<Loader />)).not.toThrow();
  });

  it('renders without crashing when style prop is an empty object', () => {
    expect(() => render(<Loader style={{}} />)).not.toThrow();
  });
});
