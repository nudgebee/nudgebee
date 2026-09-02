// The template and AI cards were gated on fail-closed WORKFLOW_TEMPLATES / WORKFLOWS
// flags, so shipped features read as "Coming Soon". Guards that state coming back.
import React from 'react';
import { render, screen } from '@testing-library/react';

jest.mock('@hooks/useTenantBranding', () => ({ useTenantBranding: () => ({ assistantName: 'NuBi' }) }));

import CreateWorkflowOptionsModal from '@components/workflow/components/CreateWorkflowOptionsModal';

const noop = () => {};

describe('CreateWorkflowOptionsModal', () => {
  const renderModal = () =>
    render(
      <CreateWorkflowOptionsModal
        open
        onClose={noop}
        onCreateFromScratch={noop}
        onUseTemplate={noop}
        onAskAI={noop}
        onCreateFromCode={noop}
        accountOptions={[{ value: 'acc-1', label: 'Account One', group: 'AWS' }] as any}
        selectedAccountId='acc-1'
        onAccountChange={noop}
      />
    );

  it('renders all four paths with no Coming Soon copy', () => {
    renderModal();
    expect(screen.getByText('Start from Template')).toBeInTheDocument();
    expect(screen.getByText('Generate with NuBi')).toBeInTheDocument();
    expect(screen.getByText('Pre-built automations for common infra tasks')).toBeInTheDocument();
    expect(screen.getByText('Describe what you need in plain English')).toBeInTheDocument();
    expect(screen.getByText('BETA')).toBeInTheDocument();
    expect(screen.queryByText('Coming Soon')).toBeNull();
  });

  it('makes both previously-gated cards clickable at full opacity', () => {
    renderModal();
    const template = document.querySelector('#wf-create-from-template-card') as HTMLElement;
    const ai = document.querySelector('#wf-create-from-ai-card') as HTMLElement;
    expect(template).toBeTruthy();
    expect(ai).toBeTruthy();
    expect(getComputedStyle(template).opacity).toBe('1');
    expect(getComputedStyle(ai).opacity).toBe('1');
    expect(getComputedStyle(template).cursor).toBe('pointer');
    expect(getComputedStyle(ai).cursor).toBe('pointer');
  });
});
