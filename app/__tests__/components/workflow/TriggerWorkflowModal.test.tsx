import React from 'react';
import { render, screen, fireEvent } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import TriggerWorkflowModal from '@components/workflow/components/TriggerWorkflowModal';

// Mirrors the real call sites (WorkflowListing / RunAutomationMenu): both build
// `defaultInputs` and `inputSchema` inline with getDefaultTriggerInputs(w) /
// getWorkflowInputSchema(w), so both props get a fresh identity on every parent
// render. Those parents poll (listing every 10s, a running execution every 3s,
// a cancelling one every 500ms), so a re-render here stands in for a poll tick.
const INPUT_SCHEMA = [{ id: 'namespace', type: 'string' as const, description: 'Target namespace', default: 'default' }];

function Harness({ open = true, onTrigger = jest.fn() }: { open?: boolean; onTrigger?: (inputs: any) => Promise<void> }) {
  return (
    <TriggerWorkflowModal
      open={open}
      onClose={jest.fn()}
      workflowName='my-automation'
      triggerType='event'
      defaultInputs={{ namespace: 'default' }}
      inputSchema={INPUT_SCHEMA.map((i) => ({ ...i }))}
      onTrigger={onTrigger}
    />
  );
}

const getJsonBox = () => document.querySelector('textarea') as HTMLTextAreaElement;

const CUSTOM_JSON = '{"event":{"id":"evt-1","fingerprint":"fp-1"}}';

describe('TriggerWorkflowModal — input parameters box', () => {
  it('seeds the box from defaults when it opens', () => {
    render(<Harness />);
    expect(getJsonBox().value).toBe(JSON.stringify({ namespace: 'default' }, null, 2));
  });

  it('keeps a user edit across parent re-renders caused by polling (#37242)', () => {
    const { rerender } = render(<Harness />);

    fireEvent.change(getJsonBox(), { target: { value: CUSTOM_JSON } });
    expect(getJsonBox().value).toBe(CUSTOM_JSON);

    // Three poll ticks. Each hands the modal structurally-equal but
    // referentially-new defaultInputs / inputSchema.
    rerender(<Harness />);
    rerender(<Harness />);
    rerender(<Harness />);

    expect(getJsonBox().value).toBe(CUSTOM_JSON);
  });

  it('keeps the edit after unchecking "Use default values", across polls', async () => {
    const user = userEvent.setup();
    const { rerender } = render(<Harness />);

    await user.click(screen.getByText('Use default values'));
    expect(getJsonBox().value).toBe('{}');

    fireEvent.change(getJsonBox(), { target: { value: CUSTOM_JSON } });
    rerender(<Harness />);
    rerender(<Harness />);

    expect(getJsonBox().value).toBe(CUSTOM_JSON);
  });

  it('passes the custom inputs to onTrigger, not the defaults', async () => {
    const onTrigger = jest.fn().mockResolvedValue(undefined);
    const { rerender } = render(<Harness onTrigger={onTrigger} />);

    fireEvent.change(getJsonBox(), { target: { value: CUSTOM_JSON } });
    rerender(<Harness onTrigger={onTrigger} />);

    fireEvent.click(screen.getByRole('button', { name: 'Trigger Automation' }));

    await screen.findByRole('button', { name: 'Trigger Automation' });
    expect(onTrigger).toHaveBeenCalledWith({ event: { id: 'evt-1', fingerprint: 'fp-1' } });
  });

  it('re-seeds defaults when the modal is closed and reopened', () => {
    const { rerender } = render(<Harness />);

    fireEvent.change(getJsonBox(), { target: { value: CUSTOM_JSON } });
    rerender(<Harness open={false} />);
    rerender(<Harness open />);

    expect(getJsonBox().value).toBe(JSON.stringify({ namespace: 'default' }, null, 2));
  });
});
