import React from 'react';
import { render, screen } from '@testing-library/react';
import ExecutedQueryInfo from '@components/common/ExecutedQueryInfo';

describe('ExecutedQueryInfo', () => {
  it('renders the executed query and the provider it ran against', () => {
    render(<ExecutedQueryInfo query='{pod=~"relay-server-.*", namespace="ns-a"}' provider='loki' />);

    expect(screen.getByTestId('evidence-executed-query')).toBeInTheDocument();
    expect(screen.getByText('Executed query')).toBeInTheDocument();
    expect(screen.getByText('loki')).toBeInTheDocument();
    expect(screen.getByText('{pod=~"relay-server-.*", namespace="ns-a"}')).toBeInTheDocument();
  });

  it('renders the query when the provider was not recorded', () => {
    render(<ExecutedQueryInfo query='SELECT * FROM logs' />);

    expect(screen.getByText('SELECT * FROM logs')).toBeInTheDocument();
  });

  it('renders nothing when no query was recorded', () => {
    const { container } = render(<ExecutedQueryInfo provider='loki' />);

    expect(container).toBeEmptyDOMElement();
  });
});
