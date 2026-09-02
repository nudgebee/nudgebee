import React from 'react';
import { render, screen, fireEvent } from '@testing-library/react';
import { CollapsableCard } from '@ui/CollapsableCard';

describe('CollapsableCard', () => {
  it('renders the header', () => {
    render(<CollapsableCard header='My Header'>body</CollapsableCard>);
    expect(screen.getByText('My Header')).toBeInTheDocument();
  });

  it('shows body when defaultOpen=true', () => {
    render(
      <CollapsableCard header='Header' defaultOpen={true}>
        Body content
      </CollapsableCard>
    );
    expect(screen.getByText('Body content')).toBeInTheDocument();
  });

  it('hides body when defaultOpen=false', () => {
    render(
      <CollapsableCard header='Header' defaultOpen={false}>
        Body content
      </CollapsableCard>
    );
    expect(screen.queryByText('Body content')).not.toBeInTheDocument();
  });

  it('toggles aria-expanded on header click', () => {
    render(
      <CollapsableCard header='Header' defaultOpen={true}>
        Body content
      </CollapsableCard>
    );
    const btn = screen.getByRole('button');
    expect(btn).toHaveAttribute('aria-expanded', 'true');
    fireEvent.click(btn);
    expect(btn).toHaveAttribute('aria-expanded', 'false');
  });

  it('expands on click when initially collapsed', () => {
    render(
      <CollapsableCard header='Header' defaultOpen={false}>
        Body content
      </CollapsableCard>
    );
    fireEvent.click(screen.getByRole('button'));
    expect(screen.getByText('Body content')).toBeInTheDocument();
  });

  it('fires onOpenChange callback', () => {
    const onOpenChange = jest.fn();
    render(
      <CollapsableCard header='Header' defaultOpen={true} onOpenChange={onOpenChange}>
        body
      </CollapsableCard>
    );
    fireEvent.click(screen.getByRole('button'));
    expect(onOpenChange).toHaveBeenCalledWith(false);
  });

  it('renders meta when composition=header+meta+body', () => {
    render(
      <CollapsableCard header='H' composition='header+meta+body' meta={<span>meta-content</span>} defaultOpen>
        body
      </CollapsableCard>
    );
    expect(screen.getByText('meta-content')).toBeInTheDocument();
  });

  it('renders footer inside Collapse panel', () => {
    render(
      <CollapsableCard header='H' defaultOpen footer={<div>Footer content</div>}>
        body
      </CollapsableCard>
    );
    expect(screen.getByText('Footer content')).toBeInTheDocument();
  });

  it('has aria-expanded attribute on toggle button', () => {
    render(
      <CollapsableCard header='H' defaultOpen>
        body
      </CollapsableCard>
    );
    expect(screen.getByRole('button')).toHaveAttribute('aria-expanded', 'true');
  });
});
