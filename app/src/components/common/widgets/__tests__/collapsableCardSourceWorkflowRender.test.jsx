import React from 'react';
import { render, screen } from '@testing-library/react';
import CollapsableCard from '../CollapsableCard';

// The global next/router mock carries an empty query; this card's link needs an
// accountId in it, so override for this file.
jest.mock('next/router', () => ({
  useRouter: () => ({ query: { accountId: 'acct-1' }, push: jest.fn(), replace: jest.fn(), prefetch: jest.fn().mockResolvedValue(null) }),
}));
jest.mock('@lib/auth', () => ({ hasWriteAccess: () => false }));

const baseProps = {
  idx: 0,
  icon: 'icon.svg',
  text: 'Dependency probe results',
  highlightsData: [],
  contentComponents: [],
  collapsedObj: {},
  expandedCardIndex: -1,
  onCardClick: jest.fn(),
};

const sourceWorkflow = { workflow_id: 'wf-1', workflow_name: 'EC2 Application Diagnostic', execution_id: 'run-9' };

describe('CollapsableCard source-workflow attribution', () => {
  it('names the automation and links to the run that produced the card', () => {
    render(<CollapsableCard {...baseProps} sourceWorkflow={sourceWorkflow} />);

    const link = screen.getByRole('link', { name: /via EC2 Application Diagnostic/ });
    expect(link).toHaveAttribute('href', '/automation/wf-1?accountId=acct-1&executionId=run-9#executions');
    // Opens beside the investigation rather than navigating away from it.
    expect(link).toHaveAttribute('target', '_blank');
  });

  it('carries the testid on a wrapper, because ds/Link drops undeclared props', () => {
    render(<CollapsableCard {...baseProps} sourceWorkflow={sourceWorkflow} />);

    expect(screen.getByTestId('card-source-workflow-link')).toBeInTheDocument();
  });

  it('renders nothing extra for enricher evidence, which carries no stamp', () => {
    // The regression that matters for existing cards: every evidence card on the
    // page renders through this component, and only automation evidence is stamped.
    render(<CollapsableCard {...baseProps} sourceWorkflow={null} />);

    // Asserted first so this cannot pass vacuously on a card that failed to render.
    expect(screen.getByText('Dependency probe results')).toBeInTheDocument();
    expect(screen.queryByTestId('card-source-workflow-link')).not.toBeInTheDocument();
    expect(screen.queryByText(/^via /)).not.toBeInTheDocument();
  });

  it('stays silent when the stamp is incomplete', () => {
    // A stamp with an id but no name would otherwise render "via undefined".
    render(<CollapsableCard {...baseProps} sourceWorkflow={{ workflow_id: 'wf-1' }} />);

    expect(screen.queryByTestId('card-source-workflow-link')).not.toBeInTheDocument();
  });
});
