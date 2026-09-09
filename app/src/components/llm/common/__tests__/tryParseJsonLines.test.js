import { render, screen } from '@testing-library/react';
import ToolDetails, { tryParseJsonLines } from '@components/llm/common/ToolDetails';

// JSONL detection for tool responses: jq's `.[] | {...}` emits one JSON object per
// line, which whole-text JSON.parse rejects. The helper must collect that shape into
// an array — and must NOT fire on anything else (prose, markdown, plans, mixed
// output), so text responses from other agents keep rendering as text.
describe('tryParseJsonLines', () => {
  it('parses two or more JSON-object lines into an array', () => {
    const text = '{"number":36629,"title":"[BUG] - PR-followup bot"}\n{"number":36627,"title":"[BUG] - AWS cost"}';
    expect(tryParseJsonLines(text)).toEqual([
      { number: 36629, title: '[BUG] - PR-followup bot' },
      { number: 36627, title: '[BUG] - AWS cost' },
    ]);
  });

  it('handles CRLF line endings and blank lines between objects', () => {
    const text = '{"a":1}\r\n\r\n{"a":2}\r\n';
    expect(tryParseJsonLines(text)).toEqual([{ a: 1 }, { a: 2 }]);
  });

  it('handles literal backslash-n separators from escaped-newline transports', () => {
    const text = '{"a":1}\\n{"a":2}';
    expect(tryParseJsonLines(text)).toEqual([{ a: 1 }, { a: 2 }]);
  });

  it('keeps \\n escapes inside string values intact (raw pass wins)', () => {
    const text = '{"msg":"line1\\nline2"}\n{"msg":"plain"}';
    expect(tryParseJsonLines(text)).toEqual([{ msg: 'line1\nline2' }, { msg: 'plain' }]);
  });

  it('decodes transport-escaped separators while keeping value escapes intact', () => {
    const text = '{"msg":"line1\\nline2"}\\n{"msg":"plain"}';
    expect(tryParseJsonLines(text)).toEqual([{ msg: 'line1\nline2' }, { msg: 'plain' }]);
  });

  it('handles repeated literal separators between records', () => {
    expect(tryParseJsonLines('{"a":1}\\n\\n{"a":2}')).toEqual([{ a: 1 }, { a: 2 }]);
  });

  it('parses objects whose values contain markdown markers', () => {
    const text = '{"title":"uses `backticks` and **bold**"}\n{"title":"plain"}';
    expect(tryParseJsonLines(text)).toHaveLength(2);
  });

  it('returns null for plain prose', () => {
    expect(tryParseJsonLines('Step 1: check the pods\nStep 2: restart the deployment')).toBeNull();
  });

  it('returns null for markdown', () => {
    expect(tryParseJsonLines('# Plan\n- do this\n- then that')).toBeNull();
  });

  it('returns null when prose is mixed with JSON lines', () => {
    expect(tryParseJsonLines('Here are the issues:\n{"number":1}\n{"number":2}')).toBeNull();
  });

  it('returns null for a single JSON-object line (whole-text parse handles it)', () => {
    expect(tryParseJsonLines('{"stdout":"pod restarted"}')).toBeNull();
  });

  it('returns null for a JSON array, compact or pretty-printed', () => {
    expect(tryParseJsonLines('[{"a":1},{"a":2}]')).toBeNull();
    expect(tryParseJsonLines('[\n  {"a":1},\n  {"a":2}\n]')).toBeNull();
  });

  it('returns null for primitive or nested-array lines', () => {
    expect(tryParseJsonLines('42\n43')).toBeNull();
    expect(tryParseJsonLines('"a"\n"b"')).toBeNull();
    expect(tryParseJsonLines('[1,2]\n[3,4]')).toBeNull();
  });

  it('returns null when any line is malformed JSON', () => {
    expect(tryParseJsonLines('{"a":1}\n{"a":oops}')).toBeNull();
  });

  it('returns null beyond 500 lines', () => {
    const text = Array.from({ length: 501 }, (_, i) => `{"i":${i}}`).join('\n');
    expect(tryParseJsonLines(text)).toBeNull();
  });

  it('returns null for empty or non-string input', () => {
    expect(tryParseJsonLines('')).toBeNull();
    expect(tryParseJsonLines(null)).toBeNull();
    expect(tryParseJsonLines(undefined)).toBeNull();
    expect(tryParseJsonLines(123)).toBeNull();
  });
});

// End-to-end through the drawer: a JSONL response from a tool with no dedicated
// renderer (github) must reach the structured table, while prose from the same
// tool must stay text — the guard the detection exists to uphold.
describe('ToolDetails JSONL response rendering', () => {
  it('renders a JSONL tool response as a table', () => {
    const jsonl = '{"number":36629,"title":"PR-followup bot"}\n{"number":36627,"title":"AWS cost recommendations"}';
    render(<ToolDetails toolCall={{ tool: 'github', response_text: jsonl }} />);
    expect(screen.getByRole('table')).toBeInTheDocument();
    expect(screen.getByText('PR-followup bot')).toBeInTheDocument();
    expect(screen.getByText('AWS cost recommendations')).toBeInTheDocument();
  });

  it('keeps a prose tool response as text, not a table', () => {
    const prose = 'The repository has 12 open issues.\nMost recent one was filed today.';
    render(<ToolDetails toolCall={{ tool: 'github', response_text: prose }} />);
    expect(screen.queryByRole('table')).not.toBeInTheDocument();
    expect(screen.getByText(/12 open issues/)).toBeInTheDocument();
  });
});
