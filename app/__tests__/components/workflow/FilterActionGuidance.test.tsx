import React from 'react';
import { render, screen, fireEvent, waitFor } from '@testing-library/react';
import ActionDetailsSidebar from '@components/workflow/ActionDetailsSidebar';

// Verbatim wire payload for data.filter's input_schema, produced by
// `convertSchemaPropertiesToMapAny(FilterTask{}.InputSchema().Properties)` in
// runbook-server. Kept literal so a backend change that drops help/examples
// shows up here as a diff rather than as a silently blank guidance row.
const FILTER_INPUT_SCHEMA = {
  list: {
    default: null,
    description: 'The list to filter. Can be a JSON string or an array.',
    examples: [
      {
        label: '[{"name": "checkout", "cpu": 91}, ...]',
        value: '[{"name": "checkout", "cpu": 91}, {"name": "payments", "cpu": 40}]',
        note: 'A list of objects — filter it on any field, e.g. `cpu > 80`.',
      },
      { label: '["alpha", "beta"]', value: '["alpha", "beta"]', note: 'A list of plain strings.' },
      {
        label: 'Output of a previous action',
        value: "{{ Tasks['previous_task'].output.result }}",
        note: 'Reference an upstream action.',
      },
    ],
    help: 'Must be a JSON **array** — `[...]`, not `{...}`.',
    is_encrypted: false,
    order: 1,
    required: true,
    title: 'List',
    type: 'any',
  },
  condition: {
    default: null,
    description: 'The condition to apply (JSONata predicate).',
    examples: [
      { label: 'status = "active"', value: 'status = "active"', note: 'Exact match on a field.' },
      { label: 'name = "23"', value: 'name = "23"', note: 'Quotes matter.' },
    ],
    help: 'The condition is spliced into `$[<condition>]`.',
    is_encrypted: false,
    order: 2,
    required: true,
    title: 'Condition',
    type: 'string',
  },
};

const TASK_DEFINITIONS = [
  {
    name: 'data.filter',
    description: 'Keep only the items in a list that match a condition.',
    input_schema: FILTER_INPUT_SCHEMA,
    output_schema: { result: { type: 'array', description: 'The filtered list.', required: true } },
  },
];

jest.mock('@shared/viewers/MarkDowns', () => ({
  __esModule: true,
  default: ({ data }: { data: string }) => <div data-testid='markdown'>{data}</div>,
}));
jest.mock('@api1/workflow', () => ({ __esModule: true, default: { listConfigs: jest.fn().mockResolvedValue({ data: {} }) } }));
jest.mock('@api1/account', () => ({
  __esModule: true,
  default: {
    getDefaultProvider: jest.fn().mockResolvedValue({ data: { data: {} } }),
    getNotificationChannelList: jest.fn().mockResolvedValue({ data: { data: [] } }),
    getNotificationUserList: jest.fn().mockResolvedValue({ data: { data: [] } }),
  },
}));
jest.mock('@lib/auth', () => ({ isTenantAdmin: () => false, hasWriteAccess: () => true }));

const editorText = (): string =>
  Array.from(document.querySelectorAll('.cm-line'))
    .map((line) => line.textContent ?? '')
    .join('\n');

const renderFilterPanel = () =>
  render(
    <ActionDetailsSidebar
      variant='inline'
      open
      onClose={jest.fn()}
      selectedActionType='data.filter'
      nodes={[]}
      edges={[]}
      onTaskDataChange={jest.fn()}
      taskDefinitions={TASK_DEFINITIONS as any}
      taskData={{}}
    />
  );

describe('data.filter guidance in the real action panel (#34764)', () => {
  it('offers an example chip for every schema example on both fields', async () => {
    renderFilterPanel();

    await waitFor(() => expect(screen.getByTestId('field-guidance-list')).toBeInTheDocument());
    expect(screen.getByTestId('field-guidance-condition')).toBeInTheDocument();

    expect(screen.getByTestId('field-example-list-0')).toBeInTheDocument();
    expect(screen.getByTestId('field-example-list-2')).toBeInTheDocument();
    expect(screen.getByTestId('field-example-condition-0')).toBeInTheDocument();
    expect(screen.getByTestId('field-example-condition-1')).toBeInTheDocument();

    expect(screen.getByTestId('field-help-list')).toBeInTheDocument();
    expect(screen.getByTestId('field-help-condition')).toBeInTheDocument();
  });

  it('does not pre-fill an object into the List field, which must be an array', async () => {
    renderFilterPanel();
    await waitFor(() => expect(screen.getByTestId('field-guidance-list')).toBeInTheDocument());
    expect(editorText()).not.toContain('{}');
  });

  it('fills the List editor when its example chip is clicked', async () => {
    renderFilterPanel();
    await waitFor(() => expect(screen.getByTestId('field-example-list-1')).toBeInTheDocument());

    fireEvent.click(screen.getByTestId('field-example-list-1'));
    await waitFor(() => expect(editorText()).toContain('alpha'));
    expect(editorText()).toContain('beta');
  });

  it('fills the Condition input when its example chip is clicked', async () => {
    renderFilterPanel();
    await waitFor(() => expect(screen.getByTestId('field-example-condition-0')).toBeInTheDocument());

    fireEvent.click(screen.getByTestId('field-example-condition-0'));
    await waitFor(() => expect(screen.getByDisplayValue('status = "active"')).toBeInTheDocument());
  });

  // Typing into CodeMirror is not simulatable under jsdom (it needs real text
  // measurement), so this drives the panel with the state typing produces: the
  // editor stores the raw string whenever JSON.parse fails.
  it('shows an inline JSON error for the exact input from the bug report, before Run', async () => {
    render(
      <ActionDetailsSidebar
        variant='inline'
        open
        onClose={jest.fn()}
        selectedActionType='data.filter'
        nodes={[]}
        edges={[]}
        onTaskDataChange={jest.fn()}
        taskDefinitions={TASK_DEFINITIONS as any}
        taskData={{ list: `[{"name":'23'},{"name":'24'}]`, condition: 'name = 23' }}
      />
    );

    await waitFor(() => expect(screen.getByText(/list must be valid JSON/i)).toBeInTheDocument());
  });

  it('clears that error once the value is valid JSON', async () => {
    render(
      <ActionDetailsSidebar
        variant='inline'
        open
        onClose={jest.fn()}
        selectedActionType='data.filter'
        nodes={[]}
        edges={[]}
        onTaskDataChange={jest.fn()}
        taskDefinitions={TASK_DEFINITIONS as any}
        taskData={{ list: '["alpha","beta"]', condition: '$ = "beta"' }}
      />
    );

    await waitFor(() => expect(screen.getByTestId('field-guidance-list')).toBeInTheDocument());
    expect(screen.queryByText(/must be valid JSON/i)).not.toBeInTheDocument();
  });
});
