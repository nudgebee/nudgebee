import React from 'react';
import { render, screen, fireEvent } from '@testing-library/react';
import { ThemesView } from '@components/llm/critique-analyser/views/ThemesView';
import type { CritiqueSummary } from '@api1/critiques';

const summary: CritiqueSummary = {
  totals: { judged: 10, refined: 4, refine_pct: 40 },
  by_agent: [],
  themes: [{ theme: 'hallucination', count: 4, examples: [] }],
};

const BANNER_TITLE = 'Heuristic, not ground truth';

describe('ThemesView heuristic banner', () => {
  beforeEach(() => {
    window.localStorage.clear();
  });

  it('shows the banner by default', () => {
    render(<ThemesView summary={summary} loading={false} error={null} onSelectTheme={() => {}} />);
    expect(screen.getByText(BANNER_TITLE)).toBeInTheDocument();
  });

  // The banner covers a real caveat (heuristic counts, not ground truth) that
  // shouldn't come back on every visit once a user has read and dismissed it.
  it('hides the banner and persists the choice when dismissed', () => {
    const { unmount } = render(<ThemesView summary={summary} loading={false} error={null} onSelectTheme={() => {}} />);
    fireEvent.click(screen.getByLabelText('Dismiss'));
    expect(screen.queryByText(BANNER_TITLE)).not.toBeInTheDocument();
    unmount();

    render(<ThemesView summary={summary} loading={false} error={null} onSelectTheme={() => {}} />);
    expect(screen.queryByText(BANNER_TITLE)).not.toBeInTheDocument();
  });
});
