import React from 'react';
import { render, screen } from '@testing-library/react';
import { AgentsView } from '@components/llm/critique-analyser/views/AgentsView';
import type { CritiqueSummary } from '@api1/critiques';

const summary: CritiqueSummary = {
  totals: { judged: 10, refined: 4, refine_pct: 40 },
  by_agent: [{ agent_name: 'k8s_orchestrator', judged: 10, refined: 4, accepted: 6, refine_pct: 40 }],
  themes: [],
};

describe('AgentsView', () => {
  // #35817: judged was previously shown alongside refined with no way to see
  // how many critiques simply passed, forcing readers to subtract by hand.
  it('shows the accepted count alongside judged and refined', () => {
    render(<AgentsView summary={summary} loading={false} error={null} onSelectAgent={() => {}} />);
    expect(screen.getByText('Accepted')).toBeInTheDocument();
    expect(screen.getByText('6')).toBeInTheDocument();
  });
});
