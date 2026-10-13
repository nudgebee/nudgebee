import { limitsText } from '../TokenList';

it('shows a cost limit in dollars and keeps the unit word for counts', () => {
  const limits = [
    { metric: 'cost', period: 'month', value: 2 },
    { metric: 'requests', period: 'minute', value: 100, model: 'gpt-4o' },
    { metric: 'tokens', period: 'day', value: 5000 },
  ];
  expect(limitsText(limits)).toBe('$2 / month, 100 requests / minute (gpt-4o), 5000 tokens / day');
  expect(limitsText([])).toBe('—');
});
