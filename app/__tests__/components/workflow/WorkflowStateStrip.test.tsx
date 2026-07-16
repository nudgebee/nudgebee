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
