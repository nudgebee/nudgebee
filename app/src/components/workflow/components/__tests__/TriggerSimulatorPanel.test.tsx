import React from 'react';
import { render, screen, waitFor } from '@testing-library/react';
import TriggerSimulatorPanel from '../TriggerSimulatorPanel';

jest.mock('@api1/workflow', () => ({
  __esModule: true,
  default: { checkTriggerMatch: jest.fn() },
}));

// CodeMirror doesn't render in jsdom; the payload editor isn't what these tests assert on.
jest.mock('@ui/CodeEditor', () => ({
  CodeEditor: ({ value }: { value: string }) => <textarea data-testid='trigger-simulator-payload-input' readOnly value={value} />,
}));

import apiWorkflow from '@api1/workflow';

const mockCheck = (apiWorkflow as any).checkTriggerMatch as jest.Mock;

const respond = (result: Record<string, any>) => mockCheck.mockResolvedValue({ data: { workflow_check_trigger_match: result } });

describe('TriggerSimulatorPanel match verdict', () => {
  beforeEach(() => {
    mockCheck.mockReset();
  });

  it('says the payload fires the automation when the filter matches', async () => {
    respond({ matched: true, filter: '{{ event.event_type == "KubePodCrashLooping" }}' });

    render(
      <TriggerSimulatorPanel triggerType='event' accountId='acct-1' triggerParams={{ filter: '{{ event.event_type == "KubePodCrashLooping" }}' }} />
    );

    await waitFor(() => expect(screen.getByTestId('trigger-simulator-match-verdict')).toHaveTextContent('fires the automation'));
  });

  it('reports which gate rejected the payload', async () => {
    respond({
      matched: false,
      gate: 'event_type',
      reason: "this event's type is 'Foo', but the trigger listens for 'KubePodCrashLooping'",
    });

    render(<TriggerSimulatorPanel triggerType='event' accountId='acct-1' triggerParams={{ event_type: 'KubePodCrashLooping' }} />);

    await waitFor(() => {
      const verdict = screen.getByTestId('trigger-simulator-match-verdict');
      expect(verdict).toHaveTextContent('does not fire the automation');
      expect(verdict).toHaveTextContent("trigger listens for 'KubePodCrashLooping'");
    });
  });

  it('distinguishes a filter that could not be evaluated from one that returned false', async () => {
    respond({ matched: false, gate: 'filter', error: 'unexpected token' });

    render(<TriggerSimulatorPanel triggerType='event' accountId='acct-1' triggerParams={{ filter: '{{ event.event_type == }}' }} />);

    await waitFor(() => expect(screen.getByTestId('trigger-simulator-match-verdict')).toHaveTextContent('could not be evaluated: unexpected token'));
  });

  it('skips the check when no account is available', async () => {
    render(<TriggerSimulatorPanel triggerType='event' triggerParams={{ filter: '{{ true }}' }} />);

    await waitFor(() => expect(screen.getByTestId('trigger-simulator-payload-input')).toBeInTheDocument());
    expect(mockCheck).not.toHaveBeenCalled();
    expect(screen.queryByTestId('trigger-simulator-match-verdict')).not.toBeInTheDocument();
  });
});
