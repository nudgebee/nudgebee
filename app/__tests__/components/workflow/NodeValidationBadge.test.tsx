import React from 'react';
import { render, screen } from '@testing-library/react';
import '@testing-library/jest-dom';
import { ReactFlowProvider } from 'reactflow';
import TriggerNode from '@components/workflow/nodes/TriggerNode';
import ActionNode from '@components/workflow/nodes/ActionNode';
import SwitchNode from '@components/workflow/nodes/SwitchNode';

// serverError holds the backend's Validate finding (user input), so the badge must not blame the server.
const cases: Array<[string, React.FC<any>, string, Record<string, any>]> = [
  ['TriggerNode', TriggerNode, 'trigger-node-server-error-badge', { trigger: { type: 'schedule' } }],
  ['ActionNode', ActionNode, 'action-node-server-error-badge', { taskConfig: { id: 'hash', action: 'crypto.hash' } }],
  ['SwitchNode', SwitchNode, 'switch-node-server-error-badge', { taskConfig: { id: 'sw', action: 'switch' } }],
];

describe('workflow node validation badge', () => {
  it.each(cases)('%s labels a Validate finding as a validation error, not a server error', (_name, Node, testId, data) => {
    const message = "task 'hash': missing required parameter: 'algorithm'";
    render(
      <ReactFlowProvider>
        <Node id='n1' data={{ ...data, serverError: message }} isConnectable selected={false} />
      </ReactFlowProvider>
    );
    const badge = screen.getByTestId(testId);
    expect(badge).toHaveTextContent('Validation Error');
    expect(badge).not.toHaveTextContent(/server error/i);
    expect(badge).toHaveAttribute('title', message);
  });
});
