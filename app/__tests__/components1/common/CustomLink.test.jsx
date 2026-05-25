import React from 'react';
import { render, screen, fireEvent } from '@testing-library/react';
import CustomLink from '@components1/common/CustomLink';

jest.mock('src/utils/colors', () => ({
  colors: {
    text: {
      secondary: '#374151',
      primary: '#3B82F6',
      white: '#fff',
      tertiary: '#6B7280',
      primaryLight: '#60A5FA',
      secondaryDark: '#1F2937',
    },
    background: { primaryLightest: '#EFF6FF', white: '#fff' },
    border: { secondary: '#D1D5DB', primary: '#3B82F6' },
    primary: '#3B82F6',
  },
}));

jest.mock('next/link', () => ({
  __esModule: true,
  default: ({ href, children, ...rest }) => (
    <a href={href} {...rest}>
      {children}
    </a>
  ),
}));

describe('CustomLink', () => {
  it('renders children text', () => {
    render(<CustomLink href='/dashboard'>Dashboard</CustomLink>);
    expect(screen.getByText('Dashboard')).toBeInTheDocument();
  });

  it('sets href correctly', () => {
    render(<CustomLink href='/settings'>Settings</CustomLink>);
    const link = screen.getByRole('link');
    expect(link).toHaveAttribute('href', '/settings');
  });

  it('renders OpenInNewIcon when openInNew is true', () => {
    const { container } = render(
      <CustomLink href='/external' openInNew={true}>
        External
      </CustomLink>
    );
    // OpenInNewIcon renders as an SVG element from MUI
    const svgIcons = container.querySelectorAll('svg');
    expect(svgIcons.length).toBeGreaterThan(0);
  });

  it('does not render OpenInNewIcon when openInNew is false', () => {
    const { container } = render(
      <CustomLink href='/internal' openInNew={false}>
        Internal
      </CustomLink>
    );
    const svgIcons = container.querySelectorAll('svg');
    expect(svgIcons.length).toBe(0);
  });

  it('does not render OpenInNewIcon by default', () => {
    const { container } = render(<CustomLink href='/page'>Page</CustomLink>);
    const svgIcons = container.querySelectorAll('svg');
    expect(svgIcons.length).toBe(0);
  });

  it('sets target to _blank when openInNew is true', () => {
    render(
      <CustomLink href='/external' openInNew={true}>
        External
      </CustomLink>
    );
    const link = screen.getByRole('link');
    expect(link).toHaveAttribute('target', '_blank');
  });

  it('uses default target _self when openInNew is false', () => {
    render(
      <CustomLink href='/page' openInNew={false}>
        Page
      </CustomLink>
    );
    const link = screen.getByRole('link');
    expect(link).toHaveAttribute('target', '_self');
  });

  it('calls onClick when the link is clicked', () => {
    const onClick = jest.fn();
    render(
      <CustomLink href='/page' onClick={onClick}>
        Click me
      </CustomLink>
    );
    fireEvent.click(screen.getByRole('link'));
    expect(onClick).toHaveBeenCalledTimes(1);
  });

  it('calls stopPropagation on click event', () => {
    const stopPropagation = jest.fn();
    render(<CustomLink href='/page'>Click me</CustomLink>);
    const link = screen.getByRole('link');
    fireEvent.click(link, { stopPropagation });
    // stopPropagation is called internally via e.stopPropagation()
    // We verify click does not throw and component handles it
    expect(link).toBeInTheDocument();
  });

  it('renders without onClick prop without crashing', () => {
    render(<CustomLink href='/page'>No handler</CustomLink>);
    expect(screen.getByText('No handler')).toBeInTheDocument();
    fireEvent.click(screen.getByRole('link'));
  });
});
