import { cleanConversationTitle } from '@components/llm/cost-analyser/format';

describe('cleanConversationTitle', () => {
  it('passes plain one-line titles through', () => {
    expect(cleanConversationTitle('Why is my pod crashing?')).toBe('Why is my pod crashing?');
  });

  it('extracts the embedded Title from a markdown event summary', () => {
    const raw = '### 📝 Event Summary\n- **Title:** relay-server 5xx spike\n- **Severity:** high\n\nLong body...';
    expect(cleanConversationTitle(raw)).toBe('relay-server 5xx spike');
  });

  it('handles underscore-bold markdown', () => {
    expect(cleanConversationTitle('__Event Summary__\n__Title:__ db connection pool exhausted\nbody')).toBe('db connection pool exhausted');
  });

  it('returns an empty string for non-string input', () => {
    expect(cleanConversationTitle(42 as unknown as string)).toBe('');
  });

  it('falls back to the first non-empty line, stripped of markdown', () => {
    expect(cleanConversationTitle('\n## **Fraud detection gRPC errors**\nbody')).toBe('Fraud detection gRPC errors');
  });

  it('truncates very long titles', () => {
    expect(cleanConversationTitle(`# ${'a'.repeat(300)}\nx`).length).toBe(140);
  });

  it('handles empty input', () => {
    expect(cleanConversationTitle(undefined)).toBe('');
  });
});
