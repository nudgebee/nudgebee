import React from 'react';
import { render, screen } from '@testing-library/react';
import { Link } from '@ui/Link';

describe('Link', () => {
  it('renders link text', () => {
    render(<Link href='/dashboard'>Dashboard</Link>);
    expect(screen.getByText('Dashboard')).toBeInTheDocument();
  });

  it('renders as an anchor element', () => {
    render(<Link href='/settings'>Settings</Link>);
    expect(screen.getByRole('link', { name: 'Settings' })).toBeInTheDocument();
  });

  it('adds target=_blank when openInNew=true', () => {
    render(
      <Link href='/docs' openInNew>
        Docs
      </Link>
    );
    expect(screen.getByRole('link')).toHaveAttribute('target', '_blank');
  });

  it('renders with openInNew without crashing', () => {
    render(
      <Link href='/docs' openInNew>
        Docs
      </Link>
    );
    expect(screen.getByRole('link')).toHaveAttribute('target', '_blank');
  });

  it('renders complex children without crashing', () => {
    render(
      <Link href='/page'>
        <span>Primary</span>
        <span>Secondary</span>
      </Link>
    );
    expect(screen.getByText('Primary')).toBeInTheDocument();
    expect(screen.getByText('Secondary')).toBeInTheDocument();
  });

  it('renders children', () => {
    render(
      <Link href='/home'>
        <span data-testid='inner'>Home</span>
      </Link>
    );
    expect(screen.getByTestId('inner')).toBeInTheDocument();
  });

  it('applies maxWidth style when provided', () => {
    render(
      <Link href='/x' maxWidth='200px'>
        Long text here
      </Link>
    );
    expect(screen.getByRole('link')).toBeInTheDocument();
  });
});
