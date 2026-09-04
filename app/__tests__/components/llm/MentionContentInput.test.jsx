import React from 'react';
import { render, screen, fireEvent } from '@testing-library/react';
import MentionContentInput from '@components/llm/MentionContentInput';

// The two things worth pinning down here are the token grammar (what counts as
// a mention, and what deliberately doesn't) and the caret arithmetic on insert
// — everything else is presentation.

const AGENTS = [
  { name: 'k8s_ops', description: 'Kubernetes operations' },
  { name: 'cost_advisor', description: 'Cost analysis' },
];
const TOOLS = [
  { name: 'run_script', type: 'scripting' },
  { name: 'query_logs', type: 'observability' },
];

const Harness = ({ initial = '' }) => {
  const [value, setValue] = React.useState(initial);
  return <MentionContentInput value={value} onChange={setValue} agents={AGENTS} tools={TOOLS} placeholder='Write here' data-testid='content' />;
};

const typeInto = (textarea, next, caret = next.length) => {
  fireEvent.change(textarea, { target: { value: next } });
  textarea.setSelectionRange(caret, caret);
  fireEvent.keyUp(textarea);
};

describe('MentionContentInput', () => {
  it('offers agents on @ and tools on #', () => {
    render(<Harness />);
    const textarea = screen.getByPlaceholderText('Write here');

    typeInto(textarea, 'restart with @');
    expect(screen.getByText('Agents')).toBeInTheDocument();
    expect(screen.getByText('@k8s_ops')).toBeInTheDocument();
    expect(screen.queryByText('#run_script')).not.toBeInTheDocument();

    typeInto(textarea, 'restart with #');
    expect(screen.getByText('Tools')).toBeInTheDocument();
    expect(screen.getByText('#run_script')).toBeInTheDocument();
    expect(screen.queryByText('@k8s_ops')).not.toBeInTheDocument();
  });

  it('filters the list by what has been typed after the trigger', () => {
    render(<Harness />);
    const textarea = screen.getByPlaceholderText('Write here');

    typeInto(textarea, 'ask @cost');
    expect(screen.getByText('@cost_advisor')).toBeInTheDocument();
    expect(screen.queryByText('@k8s_ops')).not.toBeInTheDocument();
  });

  it('moves the highlight on ArrowDown instead of snapping back to the first row', () => {
    // Regression: handleKeyDown's setHighlight(prev+1) on keydown was being
    // undone by the keyup listener's unconditional setHighlight(0) a moment
    // later — the highlight visually never left row 1 no matter how many
    // times ArrowDown was pressed.
    render(<Harness />);
    const textarea = screen.getByPlaceholderText('Write here');

    typeInto(textarea, 'ask @');
    const rowFor = (name) => screen.getByText(name).closest('[aria-selected]');

    expect(rowFor('@k8s_ops')).toHaveAttribute('aria-selected', 'true');
    expect(rowFor('@cost_advisor')).toHaveAttribute('aria-selected', 'false');

    fireEvent.keyDown(textarea, { key: 'ArrowDown' });
    fireEvent.keyUp(textarea, { key: 'ArrowDown' });

    expect(rowFor('@k8s_ops')).toHaveAttribute('aria-selected', 'false');
    expect(rowFor('@cost_advisor')).toHaveAttribute('aria-selected', 'true');
  });

  it('inserts the picked name with a trailing space and keeps the rest of the line', () => {
    render(<Harness />);
    const textarea = screen.getByPlaceholderText('Write here');

    typeInto(textarea, 'ping @k8 now', 8); // caret sits right after "@k8"
    fireEvent.mouseDown(screen.getByText('@k8s_ops'));

    expect(textarea.value).toBe('ping @k8s_ops now');
  });

  it('labels a resolved mention and ignores one that matches nothing', () => {
    render(<Harness initial='use @k8s_ops and #query_logs, not @ghost or #nope' />);

    const chips = screen.getByTestId('mention-chips');
    expect(chips).toHaveTextContent('Agent@k8s_ops');
    expect(chips).toHaveTextContent('Tool#query_logs');
    expect(chips).not.toHaveTextContent('ghost');
    expect(chips).not.toHaveTextContent('nope');
  });

  it('resolves a mention that ends a sentence', () => {
    render(<Harness initial='Escalate to @k8s_ops. Then run #run_script.' />);

    const chips = screen.getByTestId('mention-chips');
    expect(chips).toHaveTextContent('Agent@k8s_ops');
    expect(chips).toHaveTextContent('Tool#run_script');
  });

  it('does not treat an email address or a URL fragment as a mention', () => {
    render(<Harness initial='mail ops@k8s_ops or see /docs#run_script' />);
    expect(screen.queryByTestId('mention-chips')).not.toBeInTheDocument();
  });

  it('closes the list on Escape', () => {
    render(<Harness />);
    const textarea = screen.getByPlaceholderText('Write here');

    typeInto(textarea, 'ask @');
    expect(screen.getByTestId('mention-suggestions')).toBeInTheDocument();

    fireEvent.keyDown(textarea, { key: 'Escape' });
    expect(screen.queryByTestId('mention-suggestions')).not.toBeInTheDocument();
  });
});
