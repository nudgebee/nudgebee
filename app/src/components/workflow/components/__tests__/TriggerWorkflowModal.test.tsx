import React from 'react';
import { render, screen } from '@testing-library/react';
import '@testing-library/jest-dom';
import TriggerWorkflowModal from '../TriggerWorkflowModal';

const renderModal = (inputSchema: any[]) =>
  render(
    <TriggerWorkflowModal
      open
      onClose={jest.fn()}
      workflowName='SSL Certificate Expiry Monitor'
      triggerType='manual'
      inputSchema={inputSchema}
      onTrigger={jest.fn()}
    />
  );

describe('TriggerWorkflowModal expected inputs', () => {
  it('reads "No default" for an input without a default, never "undefined"', () => {
    renderModal([
      { id: 'slack_channel', type: 'string', description: 'Channel to alert' },
      { id: 'namespaces', type: 'array', description: 'Namespaces to check' },
    ]);
    expect(screen.getAllByText('No default')).toHaveLength(2);
    expect(screen.queryByText('undefined')).not.toBeInTheDocument();
  });

  it('still shows a default that is set', () => {
    renderModal([{ id: 'domains', type: 'array', description: 'Domains', default: ['example.com'] }]);
    expect(screen.getByText('["example.com"]')).toBeInTheDocument();
  });
});
