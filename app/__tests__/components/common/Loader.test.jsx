import React from 'react';
import { render, screen } from '@testing-library/react';
import Loader from '@shared/Loader';

// Drive the three loader branches (mascot / neutral spinner / branded image)
// deterministically instead of relying on the real config fetch.
let mockBranding = { loaderUrl: '', isWhiteLabel: false, loading: false };
jest.mock('@hooks/useTenantBranding', () => ({
  useBrandingConfig: () => mockBranding,
}));

// The mascot no longer needs bundled assets; keep @assets stubbed so the real
// (svg-heavy) module isn't pulled into the Loader render tree.
jest.mock('@assets', () => ({}));

// next/image -> plain <img> so the branded-loader branch is assertable.
jest.mock('next/image', () => ({
  __esModule: true,
  default: ({ src, alt, style, unoptimized, ...props }) =>
    React.createElement('img', { src, alt, style, 'data-testid': 'loader-image', 'data-unoptimized': String(unoptimized), ...props }),
}));

describe('Loader', () => {
  beforeEach(() => {
    mockBranding = { loaderUrl: '', isWhiteLabel: false, loading: false };
  });

  it('renders the animated Nubi mascot for the default tenant', () => {
    render(<Loader />);
    expect(screen.getByTestId('nubi-animation-flying')).toBeInTheDocument();
    expect(screen.queryByTestId('loader-image')).not.toBeInTheDocument();
  });

  it('exposes an accessible "Loading..." label', () => {
    render(<Loader />);
    expect(screen.getByLabelText('Loading...')).toBeInTheDocument();
  });

  it('renders the brand-neutral spinner (no bee) while branding config is loading', () => {
    mockBranding = { loaderUrl: '', isWhiteLabel: false, loading: true };
    render(<Loader />);
    expect(screen.getByRole('status')).toBeInTheDocument();
    expect(screen.queryByTestId('nubi-animation-flying')).not.toBeInTheDocument();
  });

  it('renders the brand-neutral spinner (no bee) for white-label tenants', () => {
    mockBranding = { loaderUrl: '', isWhiteLabel: true, loading: false };
    render(<Loader />);
    expect(screen.getByRole('status')).toBeInTheDocument();
    expect(screen.queryByTestId('nubi-animation-flying')).not.toBeInTheDocument();
  });

  it('renders the tenant loader image when an explicit loaderUrl is set', () => {
    mockBranding = { loaderUrl: 'https://cdn.example.com/ria.svg', isWhiteLabel: false, loading: false };
    render(<Loader />);
    const image = screen.getByTestId('loader-image');
    expect(image).toHaveAttribute('src', 'https://cdn.example.com/ria.svg');
    expect(image).toHaveAttribute('alt', 'Loading...');
    expect(image).toHaveAttribute('data-unoptimized', 'true');
    expect(image).toHaveStyle({ width: 'calc(var(--ds-space-0) * 75)' });
    expect(image).toHaveStyle({ height: 'auto' });
  });

  it('renders a centered overlay wrapper', () => {
    const { container } = render(<Loader />);
    const wrapper = container.firstChild;
    expect(wrapper.tagName).toBe('DIV');
    expect(wrapper).toHaveStyle({ display: 'flex' });
    expect(wrapper).toHaveStyle({ justifyContent: 'center' });
    expect(wrapper).toHaveStyle({ alignItems: 'center' });
    expect(wrapper).toHaveStyle({ position: 'absolute' });
  });

  it('merges a custom style prop with the defaults', () => {
    const { container } = render(<Loader style={{ marginTop: '10px' }} />);
    expect(container.firstChild).toHaveStyle({ display: 'flex' });
    expect(container.firstChild).toHaveStyle({ marginTop: '10px' });
  });

  it('lets callers override the default position', () => {
    const { container } = render(<Loader style={{ position: 'static' }} />);
    expect(container.firstChild).toHaveStyle({ position: 'static' });
  });

  it('renders without crashing', () => {
    expect(() => render(<Loader />)).not.toThrow();
    expect(() => render(<Loader style={{}} />)).not.toThrow();
  });
});
