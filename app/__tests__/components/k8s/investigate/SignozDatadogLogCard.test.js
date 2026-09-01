import React from 'react';
import { render, screen } from '@testing-library/react';
import SignozDatadogLogCard from '@components/k8s/investigate/cards/SignozDatadogLogCard';

// The log table itself is exercised by its own tests; stub it so these assertions
// are about the card's provenance header, not the CustomTable render.
jest.mock('@components/k8s/details/SignozDatadogLogs', () => ({
  __esModule: true,
  default: () => React.createElement('div', { 'data-testid': 'signoz-logs' }),
}));

// Shape of a `logs` evidence as the api-server enrichers store it on the event:
// the log lines under data, and the query FetchLogs actually ran (plus the
// resolved provider) under additional_info.
const logEvidence = (additionalInfo) => ({
  type: 'json',
  additional_info: { action_name: 'logs', ...additionalInfo },
  data: JSON.stringify({
    data: [
      {
        timestamp: '2026-09-01T10:42:03.711Z',
        message: 'HTTP error: 504 gateway timeout',
        labels: { pod: 'relay-server-6db5ddcdb6-lwknz' },
        severity: 'error',
      },
    ],
  }),
});

const renderCardContent = async (evidence) => {
  const card = new SignozDatadogLogCard(evidence, {});
  expect(await card.canRenderContent()).toBe(true);
  const [Content] = card.getContentComponents();
  return render(<Content />);
};

describe('SignozDatadogLogCard executed query', () => {
  it('shows the executed provider query and the provider alongside the logs', async () => {
    await renderCardContent(logEvidence({ executed_query: '{pod=~"relay-server-.*", namespace="ns-a"}', provider: 'loki' }));

    expect(screen.getByTestId('evidence-executed-query')).toBeInTheDocument();
    expect(screen.getByText('{pod=~"relay-server-.*", namespace="ns-a"}')).toBeInTheDocument();
    expect(screen.getByText('loki')).toBeInTheDocument();
    expect(screen.getByTestId('signoz-logs')).toBeInTheDocument();
  });

  it('still renders the logs for evidence stored before the query was recorded', async () => {
    await renderCardContent(logEvidence({}));

    expect(screen.queryByTestId('evidence-executed-query')).not.toBeInTheDocument();
    expect(screen.getByTestId('signoz-logs')).toBeInTheDocument();
  });
});
