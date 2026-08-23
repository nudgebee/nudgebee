import { getTaskDescription } from './taskDescription';

describe('getTaskDescription', () => {
  it('uses a registered task enricher', () => {
    expect(getTaskDescription('tickets.create', { title: 'Database unavailable' })).toBe('Ticket: Database unavailable');
  });

  it('does not dispatch inherited object properties as enrichers', () => {
    expect(getTaskDescription('__proto__')).toBe('Execute task');
    expect(getTaskDescription('constructor')).toBe('Execute task');
  });
});
