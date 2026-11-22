import { useEffect, useState } from 'react';
import { act, fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import LogQueryBuilderAutocomplete from '../LogQueryBuilderAutocomplete';
import apiKubernetes1 from '@api1/kubernetes1';
import { toast } from '@ui/Toast';

jest.mock('@api1/kubernetes1', () => ({ __esModule: true, default: { knowledgeGraphFilterOptionLabelValues: jest.fn() } }));
jest.mock('@api1/observability', () => ({ __esModule: true, default: {} }));
jest.mock('@ui/Toast', () => {
  const api = { success: jest.fn(), error: jest.fn(), warning: jest.fn() };
  return { toast: api, snackbar: api };
});

/*
 * #41139: committing a chip by typing `key = value` + Enter left the field on the
 * "Type value for <key> =" step. The keystroke debounce (150 ms) was still pending
 * when Enter committed the chip, so it fired afterwards on the pre-Enter text and
 * re-armed the value step — which then outlived Apply and Clear All.
 */

const LABELS = [{ label: 'region' }, { label: 'app' }];
const DEBOUNCE_WAIT_MS = 250;

const Harness = ({ logProvider = 'knowledge_graph', heading, onQueryItemsChange }) => {
  const [items, setItems] = useState([]);
  // Label keys arrive after mount, as the KG filter options do in the app.
  const [labels, setLabels] = useState([]);
  useEffect(() => setLabels(LABELS), []);
  return (
    <>
      <LogQueryBuilderAutocomplete
        logProvider={logProvider}
        accountId=''
        queryItems={items}
        onQueryItemsChange={(next) => {
          setItems(next);
          onQueryItemsChange?.(next);
        }}
        getLabelsFromProps={labels}
        allowMultipleQueries={false}
        heading={heading}
        height='auto'
        width='100%'
      />
      <button type='button' onClick={() => setItems([])}>
        Clear All
      </button>
    </>
  );
};

const waitPastDebounce = () => act(() => new Promise((resolve) => setTimeout(resolve, DEBOUNCE_WAIT_MS)));

const typeAndCommit = (input, text) => {
  act(() => input.focus());
  fireEvent.change(input, { target: { value: text } });
  // Enter lands before the debounced suggestion lookup fires, as with real typing.
  fireEvent.keyDown(input, { key: 'Enter' });
};

describe('LogQueryBuilderAutocomplete — field resets after a typed chip (#41139)', () => {
  beforeEach(() => {
    apiKubernetes1.knowledgeGraphFilterOptionLabelValues.mockResolvedValue({
      data: { data: { kg_get_filter_values: { data: { values: ['us-east-1', 'eu-west-1'] } } } },
    });
  });

  it.each([
    ['Attribute', 'Type attribute name or select'],
    ['Label', 'Type label name or select'],
  ])('KG %s field returns to its initial placeholder after Enter and after Clear All', async (heading, initial) => {
    const onQueryItemsChange = jest.fn();
    render(<Harness heading={heading} onQueryItemsChange={onQueryItemsChange} />);
    const input = screen.getByPlaceholderText(initial);

    typeAndCommit(input, 'region = us-east-1');
    await waitPastDebounce();

    expect(onQueryItemsChange).toHaveBeenLastCalledWith([expect.objectContaining({ label: 'region', operator: '=', value: 'us-east-1' })]);
    expect(input).toHaveValue('');
    expect(input).toHaveAttribute('placeholder', initial);

    fireEvent.click(screen.getByRole('button', { name: 'Clear All' }));
    await waitPastDebounce();
    expect(input).toHaveAttribute('placeholder', initial);

    // The next key starts a new filter rather than being read as a value for `region`.
    fireEvent.change(input, { target: { value: 'app' } });
    await waitPastDebounce();
    expect(input).toHaveAttribute('placeholder', 'Type operator for "app" or select from suggestions.');
    expect(screen.getByText('= (equals)')).toBeInTheDocument();
  });

  it('adding a KG chip through the suggestion list still resets the field', async () => {
    const onQueryItemsChange = jest.fn();
    render(<Harness heading='Attribute' onQueryItemsChange={onQueryItemsChange} />);
    const input = screen.getByPlaceholderText('Type attribute name or select');

    act(() => input.focus());
    fireEvent.click(await screen.findByText('region'));
    fireEvent.click(await screen.findByText('= (equals)'));
    fireEvent.click(await screen.findByText('us-east-1'));

    await waitFor(() =>
      expect(onQueryItemsChange).toHaveBeenLastCalledWith([expect.objectContaining({ label: 'region', operator: '=', value: 'us-east-1' })])
    );
    expect(input).toHaveValue('');
    expect(input).toHaveAttribute('placeholder', 'Type attribute name or select');
  });

  it('picking a value suggestion within the debounce window still resets the field', async () => {
    const onQueryItemsChange = jest.fn();
    render(<Harness heading='Attribute' onQueryItemsChange={onQueryItemsChange} />);
    const input = screen.getByPlaceholderText('Type attribute name or select');

    act(() => input.focus());
    fireEvent.click(await screen.findByText('region'));
    fireEvent.click(await screen.findByText('= (equals)'));
    const value = await screen.findByText('us-east-1');
    // Type, then click the suggestion before the keystroke lookup fires.
    fireEvent.change(input, { target: { value: 'region = us' } });
    fireEvent.click(value);
    await waitPastDebounce();

    expect(onQueryItemsChange).toHaveBeenLastCalledWith([expect.objectContaining({ label: 'region', operator: '=', value: 'us-east-1' })]);
    expect(input).toHaveValue('');
    expect(input).toHaveAttribute('placeholder', 'Type attribute name or select');
  });

  it('the shared (non-KG) builder also resets after a typed chip', async () => {
    const onQueryItemsChange = jest.fn();
    render(<Harness logProvider='service_map' onQueryItemsChange={onQueryItemsChange} />);
    const input = screen.getByPlaceholderText('Type label name or select from suggestions.');

    typeAndCommit(input, 'region = us-east-1');
    await waitPastDebounce();

    expect(onQueryItemsChange).toHaveBeenLastCalledWith([expect.objectContaining({ label: 'region', value: 'us-east-1' })]);
    expect(input).toHaveAttribute('placeholder', 'Type label name or select from suggestions.');
  });
});

/*
 * #42120: KG sends chips as one object keyed by name, so a second chip for the same
 * key overwrote the first in the request while both chips stayed on screen.
 */
describe('LogQueryBuilderAutocomplete — one KG chip per key (#42120)', () => {
  beforeEach(() => {
    toast.error.mockClear();
    apiKubernetes1.knowledgeGraphFilterOptionLabelValues.mockResolvedValue({
      data: { data: { kg_get_filter_values: { data: { values: ['us-east-1', 'eu-west-1'] } } } },
    });
  });

  // Scoped to the suggestion list: once a chip exists its key text is on screen too.
  const pickSuggestion = async (text) => fireEvent.click(within(await screen.findByRole('list')).getByText(text));
  const pickChip = async (input, key, value) => {
    act(() => input.focus());
    await pickSuggestion(key);
    await pickSuggestion('= (equals)');
    await pickSuggestion(value);
  };

  it('blocks a second value for the same key picked from suggestions, with a notice', async () => {
    const onQueryItemsChange = jest.fn();
    render(<Harness heading='Label' onQueryItemsChange={onQueryItemsChange} />);
    const input = screen.getByPlaceholderText('Type label name or select');

    await pickChip(input, 'region', 'us-east-1');
    await waitFor(() => expect(onQueryItemsChange).toHaveBeenCalledTimes(1));
    await pickChip(input, 'region', 'eu-west-1');

    await waitFor(() => expect(toast.error).toHaveBeenCalledWith(expect.stringContaining('"region" already has a filter')));
    expect(onQueryItemsChange).toHaveBeenCalledTimes(1);
    expect(onQueryItemsChange).toHaveBeenLastCalledWith([expect.objectContaining({ label: 'region', value: 'us-east-1' })]);
    expect(input).toHaveAttribute('placeholder', 'Type label name or select');
  });

  it('blocks a typed second value for the same key, with a notice', async () => {
    const onQueryItemsChange = jest.fn();
    render(<Harness heading='Attribute' onQueryItemsChange={onQueryItemsChange} />);
    const input = screen.getByPlaceholderText('Type attribute name or select');

    typeAndCommit(input, 'region = us-east-1');
    await waitPastDebounce();
    typeAndCommit(input, 'region = eu-west-1');
    await waitPastDebounce();

    expect(toast.error).toHaveBeenCalledWith(expect.stringContaining('"region" already has a filter'));
    expect(onQueryItemsChange).toHaveBeenCalledTimes(1);
    expect(onQueryItemsChange).toHaveBeenLastCalledWith([expect.objectContaining({ label: 'region', value: 'us-east-1' })]);
  });

  it('still adds chips for different keys, and allows a new value once the old chip is removed', async () => {
    const onQueryItemsChange = jest.fn();
    render(<Harness heading='Label' onQueryItemsChange={onQueryItemsChange} />);
    const input = screen.getByPlaceholderText('Type label name or select');

    typeAndCommit(input, 'region = us-east-1');
    await waitPastDebounce();
    typeAndCommit(input, 'app = checkout');
    await waitPastDebounce();
    expect(onQueryItemsChange).toHaveBeenLastCalledWith([
      expect.objectContaining({ label: 'region', value: 'us-east-1' }),
      expect.objectContaining({ label: 'app', value: 'checkout' }),
    ]);

    fireEvent.click(screen.getAllByRole('button', { name: 'Remove filter' })[0]);
    expect(onQueryItemsChange).toHaveBeenLastCalledWith([expect.objectContaining({ label: 'app', value: 'checkout' })]);

    typeAndCommit(input, 'region = eu-west-1');
    await waitPastDebounce();
    expect(toast.error).not.toHaveBeenCalled();
    expect(onQueryItemsChange).toHaveBeenLastCalledWith([
      expect.objectContaining({ label: 'app', value: 'checkout' }),
      expect.objectContaining({ label: 'region', value: 'eu-west-1' }),
    ]);
  });
});
