import React from 'react';
import { render, screen } from '@testing-library/react';
import LLMAnswerRenderer from '@components1/llm/common/LLMAnswerRenderer';

// Mock heavy internal components so we can assert on what the renderer CHOSE
// to display without pulling MUI / markdown parsers into the test.
jest.mock('@components1/common/MarkDowns', () => {
  return function MockMarkDowns({ data }) {
    // Render the incoming markdown string into a predictable DOM node so
    // tests can grep it.
    return <div data-testid='markdown-output'>{data}</div>;
  };
});

jest.mock('@components1/common/tables/CustomTable2', () => {
  return function MockCustomTable({ headers, tableData }) {
    return (
      <div data-testid='table-output'>
        <div data-testid='table-headers'>{JSON.stringify(headers)}</div>
        <div data-testid='table-rows'>{tableData?.length ?? 0}</div>
      </div>
    );
  };
});

jest.mock('@lib/util', () => ({
  getTableDataFromArrayOfObject: (input) => {
    // Real function iterates an array of objects; we only need enough for
    // the assertion surface.
    if (!Array.isArray(input) || input.length === 0) {
      return { headers: [], tableData: [] };
    }
    return {
      headers: Object.keys(input[0]),
      tableData: input.map((row) => Object.values(row).map((v) => ({ text: String(v) }))),
    };
  },
}));

describe('LLMAnswerRenderer', () => {
  describe('regression: bare-string responses must NOT render as character tables', () => {
    // This guards the class of bug where a quoted string from an LLM
    // (valid JSON but a bare primitive) was passed through JSON.parse and
    // then iterated character-by-character into a table.

    it('quoted plain-string response renders as markdown, not a table', () => {
      const toolCall = {
        text: '"hello. remember the code CANARY-42. say \'ok\' in one word"',
      };
      render(<LLMAnswerRenderer toolCall={toolCall} />);
      expect(screen.queryByTestId('table-output')).not.toBeInTheDocument();
      expect(screen.getByTestId('markdown-output')).toBeInTheDocument();
    });

    it('bare number literal renders as markdown, not a table', () => {
      const toolCall = { text: '42' };
      render(<LLMAnswerRenderer toolCall={toolCall} />);
      expect(screen.queryByTestId('table-output')).not.toBeInTheDocument();
      expect(screen.getByTestId('markdown-output')).toBeInTheDocument();
    });

    it('bare boolean renders as markdown', () => {
      const toolCall = { text: 'true' };
      render(<LLMAnswerRenderer toolCall={toolCall} />);
      expect(screen.queryByTestId('table-output')).not.toBeInTheDocument();
      expect(screen.getByTestId('markdown-output')).toBeInTheDocument();
    });

    it('bare null renders as markdown', () => {
      const toolCall = { text: 'null' };
      render(<LLMAnswerRenderer toolCall={toolCall} />);
      expect(screen.queryByTestId('table-output')).not.toBeInTheDocument();
      expect(screen.getByTestId('markdown-output')).toBeInTheDocument();
    });
  });

  describe('positive cases: structured responses still render as tables', () => {
    it('array of objects renders as table', () => {
      const toolCall = {
        text: JSON.stringify([
          { name: 'payments', status: 'ok' },
          { name: 'orders', status: 'degraded' },
        ]),
      };
      render(<LLMAnswerRenderer toolCall={toolCall} />);
      expect(screen.getByTestId('table-output')).toBeInTheDocument();
      expect(screen.getByTestId('table-rows').textContent).toBe('2');
    });
  });

  describe('plain markdown passes through', () => {
    it('non-JSON text renders as markdown', () => {
      const toolCall = { text: '# Heading\n- bullet one\n- bullet two' };
      render(<LLMAnswerRenderer toolCall={toolCall} />);
      expect(screen.queryByTestId('table-output')).not.toBeInTheDocument();
      expect(screen.getByTestId('markdown-output')).toBeInTheDocument();
      expect(screen.getByTestId('markdown-output').textContent).toContain('Heading');
    });
  });
});
