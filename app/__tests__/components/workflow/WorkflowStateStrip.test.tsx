import React from 'react';
import { render, screen } from '@testing-library/react';
import WorkflowStateStrip from '@components/workflow/WorkflowStateStrip';

const baseProps = {
  liveVersionNumber: 3,
  liveVersionId: 'v-live',
  draftVersionNumber: 3,
  draftVersionId: 'v-live',
  onPublish: jest.fn(),
  onHistory: jest.fn(),
};

describe('WorkflowStateStrip publish button', () => {
  it('disables Publish when draft matches live and there are no unsaved edits', () => {
    render(<WorkflowStateStrip {...baseProps} hasUnsavedChanges={false} draftAheadOfLive={false} />);
    expect(screen.getByTestId('workflow-publish-btn')).toBeDisabled();
  });

  it('enables Publish when there are unsaved canvas edits', () => {
    render(<WorkflowStateStrip {...baseProps} hasUnsavedChanges draftAheadOfLive={false} />);
    expect(screen.getByTestId('workflow-publish-btn')).toBeEnabled();
  });

  it('enables Publish when the saved draft is ahead of live', () => {
    render(<WorkflowStateStrip {...baseProps} hasUnsavedChanges={false} draftAheadOfLive />);
    expect(screen.getByTestId('workflow-publish-btn')).toBeEnabled();
  });

  it('enables Publish when no live version exists yet', () => {
    render(<WorkflowStateStrip {...baseProps} liveVersionId={null} liveVersionNumber={null} hasUnsavedChanges={false} draftAheadOfLive={false} />);
    expect(screen.getByTestId('workflow-publish-btn')).toBeEnabled();
  });
});

describe('WorkflowStateStrip account chip', () => {
  it('shows the account the automation is saved under, between the draft chip and the live version', () => {
    render(<WorkflowStateStrip {...baseProps} draftAheadOfLive accountName='k8s-prod' accountProvider='K8s' />);
    const account = screen.getByTestId('workflow-account-indicator');
    expect(account).toHaveTextContent('k8s-prod');
    const draft = screen.getByTestId('workflow-draft-indicator');
    const live = screen.getByTestId('workflow-live-indicator');
    expect(draft.compareDocumentPosition(account) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
    expect(account.compareDocumentPosition(live) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
  });

  it('shows the account on a new automation, where the version chips are hidden', () => {
    render(<WorkflowStateStrip {...baseProps} isNewWorkflow accountName='k8s-prod' />);
    expect(screen.getByTestId('workflow-account-indicator')).toHaveTextContent('k8s-prod');
    expect(screen.queryByTestId('workflow-live-indicator')).not.toBeInTheDocument();
  });

  it('renders nothing for the account until its name is known', () => {
    render(<WorkflowStateStrip {...baseProps} />);
    expect(screen.queryByTestId('workflow-account-indicator')).not.toBeInTheDocument();
  });
});
