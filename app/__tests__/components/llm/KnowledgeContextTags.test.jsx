import React from 'react';
import { render, screen, fireEvent } from '@testing-library/react';
import KnowledgeContextTags from '@components/llm/KnowledgeContextTags';

function Editor({ initial = [], options = [] }) {
  const [tags, setTags] = React.useState(initial);
  return (
    <>
      <KnowledgeContextTags value={tags} options={options} onChange={setTags} />
      <output>{JSON.stringify(tags)}</output>
    </>
  );
}
function search(text) {
  fireEvent.click(document.getElementById('kb-context-tags'));
  const input = screen.getByPlaceholderText('Search or add a tag…');
  fireEvent.change(input, { target: { value: text } });
  return input;
}
test('adds a custom tag using the single search field and Enter', () => {
  render(<Editor />);
  const input = search(' payments ');
  expect(screen.getAllByRole('textbox')).toHaveLength(1);
  fireEvent.keyDown(input, { key: 'Enter' });
  expect(screen.getByRole('status', { hidden: true })).toHaveTextContent('["payments"]');
  expect(input).toHaveValue('');
});
test('shows matching suggestions and allows a custom value from the same dropdown', () => {
  render(<Editor options={[{ value: 'cart-cache', label: 'cart-cache' }]} />);
  search('cache');
  expect(screen.getByText('cart-cache')).toBeInTheDocument();
  fireEvent.click(screen.getByText('Add “cache”'));
  expect(screen.getByRole('status', { hidden: true })).toHaveTextContent('["cache"]');
});
test('selects a suggestion and preserves saved tags', () => {
  render(<Editor initial={['payments']} options={[{ value: 'cart-cache', label: 'cart-cache' }]} />);
  search('cart');
  fireEvent.click(screen.getByText('cart-cache'));
  expect(screen.getByRole('status', { hidden: true })).toHaveTextContent('["payments","cart-cache"]');
});
test('does not offer case-insensitive duplicates', () => {
  render(<Editor initial={['payments']} />);
  const input = search('PAYMENTS');
  expect(screen.queryByText('Add “PAYMENTS”')).not.toBeInTheDocument();
  fireEvent.keyDown(input, { key: 'Enter' });
  expect(screen.getByRole('status', { hidden: true })).toHaveTextContent('["payments"]');
});
test('retains invalid custom text for correction', () => {
  render(<Editor />);
  const input = search('x'.repeat(129));
  fireEvent.keyDown(input, { key: 'Enter' });
  expect(screen.getByRole('alert')).toHaveTextContent('Use at most 128 characters');
  expect(input).toHaveValue('x'.repeat(129));
  expect(screen.getByRole('status', { hidden: true })).toHaveTextContent('[]');
});

test.each([null, 'not-an-array', {}])('handles invalid tag arrays: %p', (initial) => {
  render(<Editor initial={initial} />);
  const input = search('payments');
  fireEvent.keyDown(input, { key: 'Enter' });
  expect(screen.getByRole('status', { hidden: true })).toHaveTextContent('["payments"]');
});

test('merges case variants without losing the saved selection', () => {
  render(<Editor initial={['PAYMENTS']} options={[{ value: 'payments', label: 'payments' }]} />);
  search('payments');
  expect(screen.getAllByRole('option')).toHaveLength(1);
  fireEvent.click(screen.getByRole('option'));
  expect(screen.getByRole('status', { hidden: true })).toHaveTextContent('[]');
});
