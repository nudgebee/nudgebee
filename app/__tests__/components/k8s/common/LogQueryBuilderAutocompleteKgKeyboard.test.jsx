import React, { useEffect, useState } from 'react';
import { render, screen, fireEvent, waitFor } from '@testing-library/react';
import '@testing-library/jest-dom';

const mockKgValues = jest.fn();

jest.mock('@api1/kubernetes1', () => ({
  __esModule: true,
  default: { knowledgeGraphFilterOptionLabelValues: (...args) => mockKgValues(...args) },
}));
jest.mock('@api1/observability', () => ({ __esModule: true, default: {} }));

import LogQueryBuilderAutocomplete from '@components/k8s/common/LogQueryBuilderAutocomplete';

// Mirrors how KnowledgeGraph.jsx mounts the Label / Attribute filters: the key list
// arrives after mount, once the filter options have loaded.
const Harness = ({ heading, onItems }) => {
  const [items, setItems] = useState([]);
  const [keys, setKeys] = useState([]);
  useEffect(() => setKeys([{ label: 'app' }]), []);
  return (
    <LogQueryBuilderAutocomplete
      logProvider='knowledge_graph'
      accountId=''
      onQueryChange={() => {}}
      queryItems={items}
      onQueryItemsChange={(next) => {
        setItems(next);
        onItems(next);
      }}
      getLabelsFromProps={keys}
      allowMultipleQueries={false}
      heading={heading}
      height='auto'
      width='100%'
    />
  );
};

const setup = (heading = 'Label', values = ['analytics-db', 'billing']) => {
  mockKgValues.mockResolvedValue({ data: { data: { kg_get_filter_values: { data: { values } } } } });
  const onItems = jest.fn();
  render(<Harness heading={heading} onItems={onItems} />);
  const input = screen.getByRole('textbox');
  return { input, onItems };
};

// Type the key, then pick the `=` operator with ↓ + Enter — the flow the help text describes.
const pickKeyWithKeyboard = async (input) => {
  fireEvent.focus(input);
  fireEvent.change(input, { target: { value: 'app' } });
  await screen.findByText('= (equals)');
  fireEvent.keyDown(input, { key: 'ArrowDown' });
  fireEvent.keyDown(input, { key: 'Enter' });
  await waitFor(() => expect(mockKgValues).toHaveBeenCalled());
  await waitFor(() => expect(input).not.toBeDisabled());
};

describe('LogQueryBuilderAutocomplete — knowledge_graph keyboard flow (#40698)', () => {
  beforeEach(() => mockKgValues.mockReset());

  it.each(['Label', 'Attribute'])('%s filter: picking the operator leaves no "(equals)" in the input or chip value', async (heading) => {
    const { input, onItems } = setup(heading);
    await pickKeyWithKeyboard(input);

    expect(input).toHaveValue('app = ');

    fireEvent.change(input, { target: { value: 'app = analytics-db' } });
    fireEvent.keyDown(input, { key: 'Enter' });

    await waitFor(() => expect(onItems).toHaveBeenCalled());
    expect(onItems).toHaveBeenLastCalledWith([expect.objectContaining({ label: 'app', operator: '=', value: 'analytics-db' })]);
    expect(screen.queryByText(/\(equals\)/)).not.toBeInTheDocument();
  });

  it('Enter with no value does not create an "app = (equals)" chip', async () => {
    const { input, onItems } = setup('Label', []);
    await pickKeyWithKeyboard(input);

    fireEvent.keyDown(input, { key: 'Enter' });

    expect(onItems).not.toHaveBeenCalled();
  });

  it('typing the whole filter in one go still creates the chip', async () => {
    const { input, onItems } = setup();
    fireEvent.change(input, { target: { value: 'app = analytics-db' } });
    fireEvent.keyDown(input, { key: 'Enter' });

    await waitFor(() => expect(onItems).toHaveBeenLastCalledWith([expect.objectContaining({ label: 'app', operator: '=', value: 'analytics-db' })]));
  });

  it('picking the value with the mouse still creates the chip', async () => {
    const { input, onItems } = setup();
    await pickKeyWithKeyboard(input);

    fireEvent.click(await screen.findByText('analytics-db'));

    await waitFor(() => expect(onItems).toHaveBeenLastCalledWith([expect.objectContaining({ label: 'app', operator: '=', value: 'analytics-db' })]));
  });
});
