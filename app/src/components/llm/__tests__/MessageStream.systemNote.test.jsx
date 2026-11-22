// The platform's own turns (message_config.system_note) show as a one-line note with their time, never a bubble.

import { render, screen } from '@testing-library/react';
import MessageStream from '@components/llm/MessageStream';
import { noteTime, systemNoteOf } from '@components/llm/utils/systemNote';

jest.mock('@components/llm/MessageItem', () => ({
  __esModule: true,
  default: ({ message }) => <div data-testid={`row-${message.type}`}>{message.text}</div>,
}));
jest.mock('@shared/CustomDrawer', () => ({ __esModule: true, default: () => null, SecondaryDrawer: () => null }));
jest.mock('@components/llm/common/TasksDrawerContent', () => ({ __esModule: true, default: () => null }));
jest.mock('@hooks/useMessageAdditionalData', () => ({ __esModule: true, default: () => ({}) }));
jest.mock('@hooks/useTenantBranding', () => ({ useWatchFeatureEnabled: () => false, useTenantBranding: () => ({ assistantName: 'Nubi' }) }));
jest.mock('@api1/ask-nudgebee', () => ({ __esModule: true, default: {} }));
jest.mock('@components/llm/WatchesTab', () => ({ __esModule: true, default: () => null }));

const noop = () => {};
const baseProps = {
  isProcessing: false,
  collapsedObj: {},
  setCollapsedObj: noop,
  showFullText: {},
  setShowFullText: noop,
  itemProps: { getAgentTokenDataForMessage: () => null },
};

describe('systemNoteOf', () => {
  it('reads the note from a JSON string or an object, and nothing else', () => {
    expect(systemNoteOf('{"system_note":"Nubi was asked to re-check the plan","remediation_plan_turn":true}')).toBe(
      'Nubi was asked to re-check the plan'
    );
    expect(systemNoteOf({ system_note: ' Investigation summary ' })).toBe('Investigation summary');
    expect(systemNoteOf('{"toolName":"x"}')).toBe('');
    expect(systemNoteOf('not json')).toBe('');
    expect(systemNoteOf(null)).toBe('');
    expect(systemNoteOf({ system_note: 3 })).toBe('');
  });
});

describe('MessageStream system notes', () => {
  it('a system turn is one line with its time; a person’s question stays a bubble', () => {
    const at = '2026-10-23T10:34:00Z';
    const messages = [
      { type: 'question', text: 'Re-check the remediation plan for this alert …', systemNote: 'Nubi was asked to re-check the plan', created_at: at },
      { type: 'response', id: 'm1', text: 'Plan v2' },
      { type: 'question', text: 'Why did it fire?' },
      { type: 'response', id: 'm2', text: 'Because …' },
    ];
    render(<MessageStream messages={messages} {...baseProps} />);
    expect(screen.getByTestId('conversation-system-note')).toHaveTextContent(`Nubi was asked to re-check the plan · ${noteTime(at)}`);
    expect(screen.queryByText(/Re-check the remediation plan for this alert/)).toBeNull();
    expect(screen.getAllByTestId('row-question')).toHaveLength(1);
    expect(screen.getByText('Why did it fire?')).toBeInTheDocument();
  });
});
