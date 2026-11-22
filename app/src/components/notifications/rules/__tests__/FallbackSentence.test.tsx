import React from 'react';
import { render, screen } from '@testing-library/react';
import { FallbackSentence } from '../RuleParts';
import { Fallbacks } from '../fallback';

describe('FallbackSentence for budget alerts', () => {
  it('says the alerts are dropped without a rule, not sent to default channels', () => {
    const fallbacks = { all: { mode: 'defaults' } } as unknown as Fallbacks;
    render(<FallbackSentence fallbacks={fallbacks} type='llm_budget' />);
    expect(screen.getByText(/budget alerts go to no channel/)).toBeInTheDocument();
    expect(screen.queryByText(/default channel/)).toBeNull();
  });
});
