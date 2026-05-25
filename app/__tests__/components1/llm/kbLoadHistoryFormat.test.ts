import { formatTrigger, formatDuration, formatDocuments } from '@components1/llm/kbLoadHistoryFormat';

describe('formatTrigger', () => {
  it('maps known trigger types to friendly labels', () => {
    expect(formatTrigger({ trigger_type: 'user_retrigger', document_count: 0 })).toBe('Retrigger');
    expect(formatTrigger({ trigger_type: 'system_sync', document_count: 0 })).toBe('Auto sync');
    expect(formatTrigger({ trigger_type: 'user_create', document_count: 0 })).toBe('Create');
    expect(formatTrigger({ trigger_type: 'user_update', document_count: 0 })).toBe('Update');
  });

  it('appends the triggering user when it is not "system"', () => {
    expect(formatTrigger({ trigger_type: 'user_retrigger', triggered_by: 'Bob', document_count: 0 })).toBe('Retrigger · Bob');
  });

  it('omits the user when it is "system" or missing', () => {
    expect(formatTrigger({ trigger_type: 'system_sync', triggered_by: 'system', document_count: 0 })).toBe('Auto sync');
    expect(formatTrigger({ trigger_type: 'user_create', document_count: 0 })).toBe('Create');
  });

  it('falls back to "Auto sync" for a missing trigger type, and echoes an unknown one', () => {
    expect(formatTrigger({ document_count: 0 })).toBe('Auto sync');
    expect(formatTrigger({ trigger_type: 'something_else', document_count: 0 })).toBe('something_else');
  });
});

describe('formatDuration', () => {
  it('returns a dash when the duration is missing', () => {
    expect(formatDuration(null)).toBe('-');
    expect(formatDuration(undefined)).toBe('-');
  });

  it('formats sub-minute durations in seconds', () => {
    expect(formatDuration(0)).toBe('0s');
    expect(formatDuration(12.4)).toBe('12s');
    expect(formatDuration(59)).toBe('59s');
  });

  it('formats longer durations as minutes and seconds', () => {
    expect(formatDuration(60)).toBe('1m 0s');
    expect(formatDuration(135)).toBe('2m 15s');
  });
});

describe('formatDocuments', () => {
  it('shows just the count when succeeded equals submitted', () => {
    expect(formatDocuments({ document_count: 50, expected_document_count: 50 })).toBe(50);
  });

  it('shows just the count when no expected count is recorded', () => {
    expect(formatDocuments({ document_count: 50 })).toBe(50);
  });

  it('shows "succeeded / submitted" when a load dropped documents', () => {
    expect(formatDocuments({ document_count: 30, expected_document_count: 50 })).toBe('30 / 50');
  });
});
