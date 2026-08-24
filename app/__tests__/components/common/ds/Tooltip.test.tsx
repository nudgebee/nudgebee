import React from 'react';
import { render, screen } from '@testing-library/react';
import Tooltip from '@ui/Tooltip';

describe('Tooltip', () => {
  it('renders the child element', () => {
    render(
      <Tooltip title='Helpful tip'>
        <button>Hover me</button>
      </Tooltip>
    );
    expect(screen.getByText('Hover me')).toBeInTheDocument();
  });

  it('renders children without a tooltip when title is empty', () => {
    render(
      <Tooltip title=''>
        <span>No tooltip</span>
      </Tooltip>
    );
    expect(screen.getByText('No tooltip')).toBeInTheDocument();
  });

  it('renders default variant without crashing', () => {
    render(
      <Tooltip title='Default tooltip' variant='default'>
        <button>Button</button>
      </Tooltip>
    );
    expect(screen.getByText('Button')).toBeInTheDocument();
  });

  it('renders explainer variant with desc', () => {
    render(
      <Tooltip title='Explainer title' desc='Some description' variant='explainer'>
        <button>Info</button>
      </Tooltip>
    );
    expect(screen.getByText('Info')).toBeInTheDocument();
  });

  it('renders interactive variant with linkUrl/linkText', () => {
    render(
      <Tooltip title='Interactive tooltip' variant='interactive' linkUrl='/docs' linkText='Learn more'>
        <button>Hover</button>
      </Tooltip>
    );
    expect(screen.getByText('Hover')).toBeInTheDocument();
  });

  it('renders with bottom placement', () => {
    render(
      <Tooltip title='Bottom tip' placement='bottom'>
        <button>Bottom</button>
      </Tooltip>
    );
    expect(screen.getByText('Bottom')).toBeInTheDocument();
  });

  it('renders with ReactNode title', () => {
    render(
      <Tooltip title={<strong>Rich content</strong>}>
        <span>Target</span>
      </Tooltip>
    );
    expect(screen.getByText('Target')).toBeInTheDocument();
  });
});
